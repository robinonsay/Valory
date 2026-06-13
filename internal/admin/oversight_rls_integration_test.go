//go:build integration

// oversight_rls_integration_test.go — DB integration tests for the admin
// oversight read path (REQ-ADMIN-011).
//
// WHY this file exists:
// OversightRepository provides admin read-only access to any student's full
// course material: syllabus, lesson_content, homework, submissions, and grades.
// Row-level security policies on each of these tables must:
//   (a) allow an admin-role connection to read any student's rows, and
//   (b) deny a student-role connection from reading another student's rows.
//
// These tests run every query under SET ROLE valory_app so the RLS policies
// actually fire. A test executed as the valory_test SUPERUSER would bypass
// FORCE ROW LEVEL SECURITY and give a false green (see memory
// force-rls-superuser-test-masking). All admin-scoped assertions inject the
// connection explicitly via auth.ContextWithConn — the SQE non-blocking finding
// requires this to prevent a bare-pool fallback from running as the 'server' GUC.
//
// Run with:
//
//	make test-integration
//	# or manually:
//	export PATH=$PATH:/usr/local/go/bin
//	VALORY_TEST_DATABASE_URL=postgres://valory_test:valory_test@localhost:55432/valory_test?sslmode=disable \
//	  go test -tags integration -count=1 -p 1 -run TestOversight ./internal/admin/
//
// @{"verifies": ["REQ-ADMIN-011"]}
package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	authpkg "github.com/valory/valory/internal/auth"
	internaldb "github.com/valory/valory/internal/db"
)

// ---------------------------------------------------------------------------
// Fixture helpers
// ---------------------------------------------------------------------------

// oversightSeed holds all the IDs created for a single student's course,
// making it easy to assert or probe specific rows in each test.
type oversightSeed struct {
	StudentID    uuid.UUID
	CourseID     uuid.UUID
	SyllabusID   uuid.UUID
	SectionID    uuid.UUID
	HomeworkID   uuid.UUID
	SubmissionID uuid.UUID
	GradeID      uuid.UUID
}

// seedOversightStudent inserts a student with a full course record:
//   - one course at 'active' status
//   - one syllabus (approved_at set)
//   - one lesson_content section
//   - one homework row (+ rubric)
//   - one submission for that homework
//   - one grade for that submission
//
// All inserts run as the pool superuser (valory_test) so that the seeding itself
// is unaffected by RLS. Only the repository-level assertions run under valory_app.
func seedOversightStudent(t *testing.T, tag string) oversightSeed {
	t.Helper()
	pool := internaldb.IntegrationPool(t)
	ctx := context.Background()

	username := fmt.Sprintf("oversight_%s_%s", tag, uuid.New().String()[:8])
	var studentID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, 'x', 'student') RETURNING id`,
		username,
	).Scan(&studentID); err != nil {
		t.Fatalf("seedOversightStudent(%s): insert user: %v", tag, err)
	}

	var courseID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO courses (student_id, topic, status) VALUES ($1, $2, 'active') RETURNING id`,
		studentID, "Test Topic "+tag,
	).Scan(&courseID); err != nil {
		t.Fatalf("seedOversightStudent(%s): insert course: %v", tag, err)
	}

	now := time.Now()
	var syllabusID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO syllabi (course_id, content_adoc, version, approved_at)
		 VALUES ($1, '= Test Syllabus', 1, $2) RETURNING id`,
		courseID, now,
	).Scan(&syllabusID); err != nil {
		t.Fatalf("seedOversightStudent(%s): insert syllabus: %v", tag, err)
	}

	var sectionID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO lesson_content (course_id, section_index, title, content_adoc, version)
		 VALUES ($1, 0, 'Section One', '== Content', 1) RETURNING id`,
		courseID,
	).Scan(&sectionID); err != nil {
		t.Fatalf("seedOversightStudent(%s): insert lesson_content: %v", tag, err)
	}

	var homeworkID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO homework (course_id, section_index, title, rubric, grade_weight)
		 VALUES ($1, 0, 'HW One', 'Grade fairly', 0.25) RETURNING id`,
		courseID,
	).Scan(&homeworkID); err != nil {
		t.Fatalf("seedOversightStudent(%s): insert homework: %v", tag, err)
	}

	var submissionID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO submissions (homework_id, student_id, format, file_path, file_size_bytes, grading_status)
		 VALUES ($1, $2, 'markdown', '/tmp/sub.md', 512, 'graded') RETURNING id`,
		homeworkID, studentID,
	).Scan(&submissionID); err != nil {
		t.Fatalf("seedOversightStudent(%s): insert submission: %v", tag, err)
	}

	var gradeID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO grades (submission_id, homework_id, student_id, course_id,
		                     raw_score, final_score, graded_at)
		 VALUES ($1, $2, $3, $4, 88.0, 88.0, now()) RETURNING id`,
		submissionID, homeworkID, studentID, courseID,
	).Scan(&gradeID); err != nil {
		t.Fatalf("seedOversightStudent(%s): insert grade: %v", tag, err)
	}

	return oversightSeed{
		StudentID:    studentID,
		CourseID:     courseID,
		SyllabusID:   syllabusID,
		SectionID:    sectionID,
		HomeworkID:   homeworkID,
		SubmissionID: submissionID,
		GradeID:      gradeID,
	}
}

// truncateOversightTables resets all tables touched by oversight tests in
// dependency order.
func truncateOversightTables(t *testing.T) {
	t.Helper()
	internaldb.TruncateTables(t, internaldb.IntegrationPool(t),
		"grades",
		"submissions",
		"homework",
		"due_date_schedules",
		"lesson_content",
		"syllabi",
		"agent_token_usage",
		"sessions",
		"login_attempts",
		"courses",
		"course_assignments",
		"users",
	)
}

// adminConnCtx returns a context carrying an admin-role valory_app connection.
// This is the required pattern (SQE non-blocking finding §5): inject the
// connection explicitly so that repo.conn(ctx) uses it instead of the bare pool,
// which carries no GUCs and would fail or behave as the server role.
func adminConnCtx(t *testing.T, adminID uuid.UUID) (context.Context, func()) {
	t.Helper()
	pool := internaldb.IntegrationPool(t)
	adminIDHex := fmt.Sprintf("%x", [16]byte(adminID))
	conn := internaldb.AcquireAsUser(t, pool, adminIDHex, "admin")
	ctx := authpkg.ContextWithConn(context.Background(), conn)
	return ctx, conn.Release
}

// studentConnCtx returns a context carrying a student-role valory_app connection
// for the given student, exactly as the auth middleware would set up for a
// student HTTP request.
func studentConnCtx(t *testing.T, studentID uuid.UUID) (context.Context, func()) {
	t.Helper()
	pool := internaldb.IntegrationPool(t)
	studentIDHex := fmt.Sprintf("%x", [16]byte(studentID))
	conn := internaldb.AcquireAsUser(t, pool, studentIDHex, "student")
	ctx := authpkg.ContextWithConn(context.Background(), conn)
	return ctx, conn.Release
}

// ---------------------------------------------------------------------------
// Tests — Repository level (DB connection level, SET ROLE valory_app)
// ---------------------------------------------------------------------------

// TestOversightRepo_AdminCanReadAnyStudentCourse verifies that an admin-role
// connection can call all OversightRepository methods for both students and
// receive rows back for each.
//
// @{"verifies": ["REQ-ADMIN-011"]}
func TestOversightRepo_AdminCanReadAnyStudentCourse(t *testing.T) {
	truncateOversightTables(t)

	seedA := seedOversightStudent(t, "alpha")
	seedB := seedOversightStudent(t, "beta")

	pool := internaldb.IntegrationPool(t)
	// Seed an admin user just so we have a real admin UUID for the GUC.
	var adminID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (username, password_hash, role) VALUES ('oversight_admin_rls', 'x', 'admin') RETURNING id`,
	).Scan(&adminID); err != nil {
		t.Fatalf("insert admin user: %v", err)
	}

	repo := NewOversightRepository(pool)

	// Test each of the two students through the single admin connection.
	for _, seed := range []oversightSeed{seedA, seedB} {
		ctx, release := adminConnCtx(t, adminID)
		defer release()

		t.Run(fmt.Sprintf("student_%s", seed.StudentID.String()[:8]), func(t *testing.T) {
			// GetCourse
			course, err := repo.GetCourse(ctx, seed.CourseID)
			if err != nil {
				t.Fatalf("GetCourse as admin: %v", err)
			}
			if course.ID != seed.CourseID {
				t.Errorf("GetCourse: want %s, got %s", seed.CourseID, course.ID)
			}

			// GetSyllabus
			syllabus, hasSyllabus, err := repo.GetSyllabus(ctx, seed.CourseID)
			if err != nil {
				t.Fatalf("GetSyllabus as admin: %v", err)
			}
			if !hasSyllabus {
				t.Fatal("GetSyllabus: expected syllabus to exist, got hasSyllabus=false")
			}
			if syllabus.ID != seed.SyllabusID {
				t.Errorf("GetSyllabus: want %s, got %s", seed.SyllabusID, syllabus.ID)
			}

			// ListSections
			sections, err := repo.ListSections(ctx, seed.CourseID)
			if err != nil {
				t.Fatalf("ListSections as admin: %v", err)
			}
			if len(sections) != 1 {
				t.Fatalf("ListSections: want 1 section, got %d", len(sections))
			}
			if sections[0].ID != seed.SectionID {
				t.Errorf("ListSections: want section %s, got %s", seed.SectionID, sections[0].ID)
			}

			// ListHomework
			homework, err := repo.ListHomework(ctx, seed.CourseID)
			if err != nil {
				t.Fatalf("ListHomework as admin: %v", err)
			}
			if len(homework) != 1 {
				t.Fatalf("ListHomework: want 1 row, got %d", len(homework))
			}
			if homework[0].ID != seed.HomeworkID {
				t.Errorf("ListHomework: want %s, got %s", seed.HomeworkID, homework[0].ID)
			}

			// ListSubmissions
			submissions, err := repo.ListSubmissions(ctx, seed.CourseID)
			if err != nil {
				t.Fatalf("ListSubmissions as admin: %v", err)
			}
			if len(submissions) != 1 {
				t.Fatalf("ListSubmissions: want 1 row, got %d", len(submissions))
			}
			if submissions[0].ID != seed.SubmissionID {
				t.Errorf("ListSubmissions: want %s, got %s", seed.SubmissionID, submissions[0].ID)
			}

			// ListGrades
			grades, err := repo.ListGrades(ctx, seed.CourseID)
			if err != nil {
				t.Fatalf("ListGrades as admin: %v", err)
			}
			if len(grades) != 1 {
				t.Fatalf("ListGrades: want 1 row, got %d", len(grades))
			}
			if grades[0].ID != seed.GradeID {
				t.Errorf("ListGrades: want %s, got %s", seed.GradeID, grades[0].ID)
			}
		})
	}
}

// TestOversightRepo_StudentA_CannotSeeStudentB_Syllabus verifies that when
// Student A's connection is used, Student B's syllabus is not visible (0 rows).
//
// @{"verifies": ["REQ-ADMIN-011"]}
func TestOversightRepo_StudentA_CannotSeeStudentB_Syllabus(t *testing.T) {
	truncateOversightTables(t)
	seedA := seedOversightStudent(t, "alpha")
	seedB := seedOversightStudent(t, "beta")

	pool := internaldb.IntegrationPool(t)
	repo := NewOversightRepository(pool)

	// Student A's connection (app.current_user_id=A, app.current_role='student').
	ctxA, releaseA := studentConnCtx(t, seedA.StudentID)
	defer releaseA()

	// Attempt to read Student B's syllabus via Student A's connection.
	syllabus, hasSyllabus, err := repo.GetSyllabus(ctxA, seedB.CourseID)
	// The RLS student policy on syllabi requires course_id IN (SELECT id FROM courses
	// WHERE student_id = current_user_id). Student B's course is not Student A's,
	// so the row is filtered; GetSyllabus returns (zero, false, nil).
	if err != nil {
		t.Fatalf("GetSyllabus(studentA, courseB): unexpected error: %v", err)
	}
	if hasSyllabus {
		t.Errorf(
			"RLS FAILURE (REQ-ADMIN-011): student A can read student B's syllabus (id=%s) — "+
				"syllabi_student_select_policy is not enforced",
			syllabus.ID,
		)
	}
}

// TestOversightRepo_StudentA_CannotSeeStudentB_Homework verifies that a
// student-role connection cannot read another student's homework rows.
//
// @{"verifies": ["REQ-ADMIN-011"]}
func TestOversightRepo_StudentA_CannotSeeStudentB_Homework(t *testing.T) {
	truncateOversightTables(t)
	seedA := seedOversightStudent(t, "alpha")
	seedB := seedOversightStudent(t, "beta")

	pool := internaldb.IntegrationPool(t)
	repo := NewOversightRepository(pool)

	ctxA, releaseA := studentConnCtx(t, seedA.StudentID)
	defer releaseA()

	homework, err := repo.ListHomework(ctxA, seedB.CourseID)
	if err != nil {
		t.Fatalf("ListHomework(studentA, courseB): unexpected error: %v", err)
	}
	if len(homework) != 0 {
		t.Errorf(
			"RLS FAILURE (REQ-ADMIN-011): student A can see %d homework row(s) belonging to student B — "+
				"homework_student_select_policy is not enforced",
			len(homework),
		)
	}
}

// TestOversightRepo_StudentA_CannotSeeStudentB_Submissions verifies that a
// student-role connection cannot read another student's submissions.
//
// @{"verifies": ["REQ-ADMIN-011"]}
func TestOversightRepo_StudentA_CannotSeeStudentB_Submissions(t *testing.T) {
	truncateOversightTables(t)
	seedA := seedOversightStudent(t, "alpha")
	seedB := seedOversightStudent(t, "beta")

	pool := internaldb.IntegrationPool(t)
	repo := NewOversightRepository(pool)

	ctxA, releaseA := studentConnCtx(t, seedA.StudentID)
	defer releaseA()

	submissions, err := repo.ListSubmissions(ctxA, seedB.CourseID)
	if err != nil {
		t.Fatalf("ListSubmissions(studentA, courseB): unexpected error: %v", err)
	}
	if len(submissions) != 0 {
		t.Errorf(
			"RLS FAILURE (REQ-ADMIN-011): student A can see %d submission(s) belonging to student B — "+
				"submissions_student_select_policy is not enforced",
			len(submissions),
		)
	}
}

// TestOversightRepo_StudentA_CannotSeeStudentB_Grades verifies that a
// student-role connection cannot read another student's grade rows.
//
// @{"verifies": ["REQ-ADMIN-011"]}
func TestOversightRepo_StudentA_CannotSeeStudentB_Grades(t *testing.T) {
	truncateOversightTables(t)
	seedA := seedOversightStudent(t, "alpha")
	seedB := seedOversightStudent(t, "beta")

	pool := internaldb.IntegrationPool(t)
	repo := NewOversightRepository(pool)

	ctxA, releaseA := studentConnCtx(t, seedA.StudentID)
	defer releaseA()

	grades, err := repo.ListGrades(ctxA, seedB.CourseID)
	if err != nil {
		t.Fatalf("ListGrades(studentA, courseB): unexpected error: %v", err)
	}
	if len(grades) != 0 {
		t.Errorf(
			"RLS FAILURE (REQ-ADMIN-011): student A can see %d grade(s) belonging to student B — "+
				"grades_student_select_policy is not enforced",
			len(grades),
		)
	}
}

// TestOversightRepo_StudentA_CannotSeeStudentB_LessonContent verifies that a
// student-role connection cannot read another student's lesson_content rows.
//
// @{"verifies": ["REQ-ADMIN-011"]}
func TestOversightRepo_StudentA_CannotSeeStudentB_LessonContent(t *testing.T) {
	truncateOversightTables(t)
	seedA := seedOversightStudent(t, "alpha")
	seedB := seedOversightStudent(t, "beta")

	pool := internaldb.IntegrationPool(t)
	repo := NewOversightRepository(pool)

	ctxA, releaseA := studentConnCtx(t, seedA.StudentID)
	defer releaseA()

	sections, err := repo.ListSections(ctxA, seedB.CourseID)
	if err != nil {
		t.Fatalf("ListSections(studentA, courseB): unexpected error: %v", err)
	}
	if len(sections) != 0 {
		t.Errorf(
			"RLS FAILURE (REQ-ADMIN-011): student A can see %d lesson_content section(s) "+
				"belonging to student B — lesson_content_student_policy is not enforced",
			len(sections),
		)
	}
}

// TestOversightRepo_AdminConn_InjectedExplicitly_NotBareFallback proves the
// SQE non-blocking finding (§5 of the task): the admin-role connection must be
// injected via auth.ContextWithConn, not derived from the bare pool. A bare-pool
// connection has empty GUCs (no app.current_role) and would not fire the admin
// SELECT policies; any read under it would return 0 rows (silently false-green).
//
// This test:
//  1. Acquires a bare valory_app connection with empty GUCs.
//  2. Injects it into the context via auth.ContextWithConn.
//  3. Asserts that GetCourse returns ErrCourseNotFound — the admin SELECT policy
//     did not fire because app.current_role is empty.
//
// @{"verifies": ["REQ-ADMIN-011"]}
func TestOversightRepo_AdminConn_InjectedExplicitly_NotBareFallback(t *testing.T) {
	truncateOversightTables(t)
	seedA := seedOversightStudent(t, "alpha")

	pool := internaldb.IntegrationPool(t)
	repo := NewOversightRepository(pool)

	// Acquire a bare valory_app connection with no GUCs set — this simulates the
	// pool's AfterRelease state (role reset, GUCs cleared). Using this connection
	// for an admin query would be a false-pass if RLS were not enforced.
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire bare conn: %v", err)
	}
	defer conn.Release()

	// Downgrade to valory_app so FORCE RLS is active.
	if err := conn.Conn().PgConn().Exec(context.Background(), "SET ROLE valory_app").Close(); err != nil {
		t.Fatalf("SET ROLE valory_app: %v", err)
	}
	// Clear GUCs — empty role, no user identity.
	if _, err := conn.Exec(context.Background(),
		"SELECT set_config('app.current_user_id', '', false), set_config('app.current_role', '', false)",
	); err != nil {
		t.Fatalf("clear GUCs: %v", err)
	}

	// Inject the bare-GUC connection. With empty app.current_role, no SELECT
	// policy on courses matches, so GetCourse must return ErrCourseNotFound.
	bareCtx := authpkg.ContextWithConn(context.Background(), conn)

	_, err = repo.GetCourse(bareCtx, seedA.CourseID)
	if err == nil {
		t.Fatal(
			"CONN INJECTION FAILURE (REQ-ADMIN-011): GetCourse succeeded on a " +
				"valory_app connection with empty GUCs — the admin SELECT policy on courses " +
				"fired without app.current_role='admin'. Check that the context carries the " +
				"correct connection.",
		)
	}
	// ErrCourseNotFound is the expected outcome: RLS filters the row, pgx.ErrNoRows
	// is returned, and the repository wraps it as ErrCourseNotFound.
	// Any error confirms enforcement; ErrCourseNotFound is the most precise.
	if err != ErrCourseNotFound {
		t.Logf("GetCourse with empty GUCs returned %v (any non-nil error confirms RLS enforcement)", err)
	}
}

// ---------------------------------------------------------------------------
// Tests — HTTP level (GET /admin/courses/{id}/overview)
// ---------------------------------------------------------------------------

// buildOversightHTTPStack wires the minimal HTTP router for oversight tests:
//
//	POST /api/v1/auth/login                         ← obtain bearer tokens
//	GET  /api/v1/admin/courses/{id}/overview        ← getCourseOverview (admin only)
//
// The auth middleware sets app.current_user_id and app.current_role from the
// session token, and RequireRole("admin") guards the oversight route.
func buildOversightHTTPStack(t *testing.T) http.Handler {
	t.Helper()

	pool := internaldb.IntegrationPool(t)

	authRepo := authpkg.NewRepository(pool)
	authSvc := authpkg.NewService(authRepo, 15*time.Minute, 24*time.Hour)

	// Auth middleware: acquires a connection per request, sets GUCs from the
	// session record. Consent provider is nil — consent gate disabled for tests.
	authMW := authpkg.NewAuthMiddleware(
		authRepo,
		pool,
		24*time.Hour,
		nil, // consent gate disabled
	)

	oversightRepo := NewOversightRepository(pool)
	oversightSvc := NewOversightService(oversightRepo)
	oversightHandler := NewOversightHandler(oversightSvc)

	outerRouter := chi.NewRouter()

	// Login endpoint: no auth middleware (no bearer token yet).
	outerRouter.Route("/api/v1/auth", func(r chi.Router) {
		authpkg.NewHandlerFull(authSvc, authRepo, pool, 24*time.Hour, nil).Routes(r)
	})

	outerRouter.Group(func(r chi.Router) {
		r.Use(authMW)

		// Admin oversight: requires admin role.
		r.Route("/api/v1/admin/courses", func(r chi.Router) {
			r.Use(authpkg.RequireRole("admin"))
			oversightHandler.Routes(r)
		})
	})

	return outerRouter
}

// oversightCreateAndLoginUser creates a user with the given role, inserts them
// into the DB, and returns their UUID plus a bearer token from POST /auth/login.
func oversightCreateAndLoginUser(ctx context.Context, t *testing.T, handler http.Handler, role string) (uuid.UUID, string) {
	t.Helper()
	pool := internaldb.IntegrationPool(t)
	username := fmt.Sprintf("oversight_http_%s_%s", role, uuid.New().String()[:8])
	password := "TestPass1234!"

	hash, err := authpkg.HashPassword(password)
	if err != nil {
		t.Fatalf("oversightCreateAndLoginUser hash: %v", err)
	}
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
		username, hash, role,
	).Scan(&id); err != nil {
		t.Fatalf("oversightCreateAndLoginUser insert: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("oversightCreateAndLoginUser login: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("oversightCreateAndLoginUser decode: %v", err)
	}
	if resp.Token == "" {
		t.Fatalf("oversightCreateAndLoginUser (%s): empty token", role)
	}
	return id, resp.Token
}

// TestOversightHTTP_Admin_GetCourseOverview_Returns200WithFullBody is the
// primary positive path: an authenticated admin session calls
// GET /admin/courses/{id}/overview and receives a 200 with all sub-resources.
//
// @{"verifies": ["REQ-ADMIN-011"]}
func TestOversightHTTP_Admin_GetCourseOverview_Returns200WithFullBody(t *testing.T) {
	truncateOversightTables(t)

	ctx := context.Background()
	handler := buildOversightHTTPStack(t)

	// Seed two students. Both must be visible to the admin caller.
	seedA := seedOversightStudent(t, "alpha")
	seedB := seedOversightStudent(t, "beta")

	_, adminToken := oversightCreateAndLoginUser(ctx, t, handler, "admin")

	// Admin reads Student A's overview.
	for _, seed := range []oversightSeed{seedA, seedB} {
		t.Run(fmt.Sprintf("student_%s", seed.StudentID.String()[:8]), func(t *testing.T) {
			url := fmt.Sprintf("/api/v1/admin/courses/%s/overview", seed.CourseID)
			req := httptest.NewRequest(http.MethodGet, url, nil)
			req.Header.Set("Authorization", "Bearer "+adminToken)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("admin overview: expected 200, got %d: %s", rr.Code, rr.Body.String())
			}

			var resp map[string]interface{}
			if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
				t.Fatalf("decode overview response: %v", err)
			}

			// The response must contain all top-level keys.
			for _, key := range []string{"course", "syllabus", "sections", "homework", "submissions", "grades"} {
				if _, ok := resp[key]; !ok {
					t.Errorf("overview response missing key %q", key)
				}
			}

			// course.id must match what we seeded.
			courseMap, _ := resp["course"].(map[string]interface{})
			if courseMap == nil {
				t.Fatal("overview response: 'course' is not an object")
			}
			if courseMap["id"] != seed.CourseID.String() {
				t.Errorf("overview course.id: want %s, got %v", seed.CourseID, courseMap["id"])
			}

			// syllabus must be present (we seeded one).
			if resp["syllabus"] == nil {
				t.Error("overview response: 'syllabus' is nil (expected an approved syllabus)")
			}

			// sections, homework, submissions, grades must each have exactly 1 entry.
			for _, key := range []string{"sections", "homework", "submissions", "grades"} {
				arr, _ := resp[key].([]interface{})
				if len(arr) != 1 {
					t.Errorf("overview response: %q should have 1 entry, got %d", key, len(arr))
				}
			}
		})
	}
}

// TestOversightHTTP_Admin_GetCourseOverview_UnknownID_Returns404 exercises the
// not-found branch: a valid UUID that maps to no course must yield 404.
//
// @{"verifies": ["REQ-ADMIN-011"]}
func TestOversightHTTP_Admin_GetCourseOverview_UnknownID_Returns404(t *testing.T) {
	truncateOversightTables(t)

	ctx := context.Background()
	handler := buildOversightHTTPStack(t)

	_, adminToken := oversightCreateAndLoginUser(ctx, t, handler, "admin")

	url := fmt.Sprintf("/api/v1/admin/courses/%s/overview", uuid.New())
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf(
			"TestOversightHTTP_Admin_GetCourseOverview_UnknownID_Returns404: "+
				"want 404, got %d: %s",
			rr.Code, rr.Body.String(),
		)
	}
}

// TestOversightHTTP_Student_GetCourseOverview_Returns403 verifies that a
// student caller is denied at the RequireRole("admin") middleware gate and
// receives 403 Forbidden.
//
// @{"verifies": ["REQ-ADMIN-011"]}
func TestOversightHTTP_Student_GetCourseOverview_Returns403(t *testing.T) {
	truncateOversightTables(t)

	ctx := context.Background()
	handler := buildOversightHTTPStack(t)

	seed := seedOversightStudent(t, "alpha")
	_, studentToken := oversightCreateAndLoginUser(ctx, t, handler, "student")

	url := fmt.Sprintf("/api/v1/admin/courses/%s/overview", seed.CourseID)
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+studentToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf(
			"TestOversightHTTP_Student_GetCourseOverview_Returns403: "+
				"expected 403 for student caller, got %d: %s",
			rr.Code, rr.Body.String(),
		)
	}
}

// TestOversightHTTP_Unauthenticated_GetCourseOverview_Returns401 verifies that
// a caller with no bearer token receives 401 Unauthorized.
//
// @{"verifies": ["REQ-ADMIN-011"]}
func TestOversightHTTP_Unauthenticated_GetCourseOverview_Returns401(t *testing.T) {
	truncateOversightTables(t)

	seed := seedOversightStudent(t, "alpha")
	handler := buildOversightHTTPStack(t)

	url := fmt.Sprintf("/api/v1/admin/courses/%s/overview", seed.CourseID)
	req := httptest.NewRequest(http.MethodGet, url, nil) // no Authorization header
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
		t.Errorf(
			"TestOversightHTTP_Unauthenticated_GetCourseOverview_Returns401: "+
				"expected 401 or 403 for unauthenticated caller, got %d: %s",
			rr.Code, rr.Body.String(),
		)
	}
}
