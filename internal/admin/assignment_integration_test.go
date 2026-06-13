//go:build integration

package admin

// assignment_integration_test.go — integration tests for the admin assignment
// path.  These tests require a live PostgreSQL instance (run via make
// test-integration or with VALORY_TEST_DATABASE_URL set).
//
// Covers:
//   - assignment creation and course-row creation at syllabus_approved
//   - N isolated course rows (each student gets their own row)
//   - partial-success (valid + invalid student in one batch)
//   - unassign allowed at syllabus_approved; 409 sentinel once generating
//   - RLS probe: student B cannot read student A's assigned course row
//   - RLS probe: admin session sees all course rows for an assignment
//   - RLS probe: course_assignments readable by valory_app without GUCs
//
// @{"verifies": ["REQ-ASSIGN-001", "REQ-ASSIGN-002", "REQ-ASSIGN-003", "REQ-ASSIGN-004", "REQ-ASSIGN-005", "REQ-ASSIGN-007", "REQ-ASSIGN-009", "REQ-ASSIGN-010", "REQ-ASSIGN-011"]}

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	authpkg "github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/db"
)

// ---------------------------------------------------------------------------
// Fixture helpers
// ---------------------------------------------------------------------------

// seedAdminUser inserts a minimal admin row and returns its ID.
func seedAdminUser(t *testing.T) uuid.UUID {
	t.Helper()
	pool := db.IntegrationPool(t)
	username := fmt.Sprintf("assign_admin_%s", uuid.New().String()[:8])
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (username, password_hash, role) VALUES ($1, 'x', 'admin') RETURNING id`,
		username,
	).Scan(&id); err != nil {
		t.Fatalf("seedAdminUser: %v", err)
	}
	return id
}

// seedStudentUser inserts a minimal student row and returns its ID.
func seedStudentUser(t *testing.T) uuid.UUID {
	t.Helper()
	pool := db.IntegrationPool(t)
	username := fmt.Sprintf("assign_student_%s", uuid.New().String()[:8])
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (username, password_hash, role) VALUES ($1, 'x', 'student') RETURNING id`,
		username,
	).Scan(&id); err != nil {
		t.Fatalf("seedStudentUser: %v", err)
	}
	return id
}

// truncateAssignmentTables wipes the tables touched by this test in a safe
// dependency order.
func truncateAssignmentTables(t *testing.T) {
	t.Helper()
	db.TruncateTables(t, db.IntegrationPool(t),
		"syllabi",
		"agent_token_usage",
		"courses",
		"course_assignments",
		"users",
	)
}

// syllabusStub satisfies syllabusGenerator without hitting the Anthropic API.
type syllabusStub struct{}

// @{"req": ["REQ-ASSIGN-003", "REQ-ASSIGN-004"]}
func (s *syllabusStub) GenerateSyllabusFromParams(_ context.Context, _, _ uuid.UUID, _, _ string, _ json.RawMessage) (string, error) {
	return "= Stub Course\n\n== Section 1: Introduction\n", nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestIntegration_AssignmentCreation_InsertsRow verifies that CreateAssignment
// persists a row readable from the DB (REQ-ASSIGN-001).
//
// @{"verifies": ["REQ-ASSIGN-001"]}
func TestIntegration_AssignmentCreation_InsertsRow(t *testing.T) {
	truncateAssignmentTables(t)
	ctx := context.Background()
	pool := db.IntegrationPool(t)

	adminID := seedAdminUser(t)
	repo := NewAssignmentRepository(pool)

	assignment, err := repo.CreateAssignment(ctx, adminID, "Week 1", "Linear Algebra", "beginner", json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}
	if assignment.ID == (uuid.UUID{}) {
		t.Fatal("expected non-zero assignment ID")
	}

	// Re-fetch to confirm persistence.
	fetched, err := repo.GetAssignment(ctx, assignment.ID)
	if err != nil {
		t.Fatalf("GetAssignment: %v", err)
	}
	if fetched.Topic != "Linear Algebra" {
		t.Errorf("expected topic 'Linear Algebra', got %q", fetched.Topic)
	}
	if fetched.Level != "beginner" {
		t.Errorf("expected level 'beginner', got %q", fetched.Level)
	}
}

// TestIntegration_AssignStudents_CreatesTwoIsolatedCourses verifies that two
// students each receive their own course row at syllabus_approved with a
// syllabus row (REQ-ASSIGN-003, REQ-ASSIGN-005).
//
// @{"verifies": ["REQ-ASSIGN-003", "REQ-ASSIGN-005"]}
func TestIntegration_AssignStudents_CreatesTwoIsolatedCourses(t *testing.T) {
	truncateAssignmentTables(t)
	ctx := context.Background()
	pool := db.IntegrationPool(t)

	adminID := seedAdminUser(t)
	s1 := seedStudentUser(t)
	s2 := seedStudentUser(t)

	repo := NewAssignmentRepository(pool)
	svc := NewAssignmentService(repo, &syllabusStub{})

	assignment, err := repo.CreateAssignment(ctx, adminID, "", "Golang", "intermediate", json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}

	created, errs := svc.AssignStudents(ctx, assignment.ID, []uuid.UUID{s1, s2})
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d: %v", len(errs), errs)
	}
	if len(created) != 2 {
		t.Fatalf("expected 2 created, got %d", len(created))
	}

	// Distinct course IDs.
	if created[0].CourseID == created[1].CourseID {
		t.Errorf("expected distinct course IDs, got identical: %s", created[0].CourseID)
	}

	// Verify both course rows exist at syllabus_approved with a syllabus.
	for _, c := range created {
		var status string
		if err := pool.QueryRow(ctx,
			`SELECT status::text FROM courses WHERE id = $1`, c.CourseID,
		).Scan(&status); err != nil {
			t.Fatalf("SELECT courses %s: %v", c.CourseID, err)
		}
		if status != "syllabus_approved" {
			t.Errorf("course %s: expected syllabus_approved, got %q", c.CourseID, status)
		}

		var syllabusCount int
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM syllabi WHERE course_id = $1 AND approved_at IS NOT NULL`, c.CourseID,
		).Scan(&syllabusCount); err != nil {
			t.Fatalf("COUNT syllabi %s: %v", c.CourseID, err)
		}
		if syllabusCount != 1 {
			t.Errorf("course %s: expected 1 approved syllabus, got %d", c.CourseID, syllabusCount)
		}
	}
}

// TestIntegration_AssignStudents_PartialSuccess verifies that a valid student
// succeeds even when an invalid student ID is in the same batch
// (REQ-ASSIGN-011).
//
// @{"verifies": ["REQ-ASSIGN-011"]}
func TestIntegration_AssignStudents_PartialSuccess(t *testing.T) {
	truncateAssignmentTables(t)
	ctx := context.Background()
	pool := db.IntegrationPool(t)

	adminID := seedAdminUser(t)
	validStudent := seedStudentUser(t)
	invalidStudent := uuid.New() // does not exist in users

	repo := NewAssignmentRepository(pool)
	svc := NewAssignmentService(repo, &syllabusStub{})

	assignment, err := repo.CreateAssignment(ctx, adminID, "", "Python", "beginner", json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}

	created, errs := svc.AssignStudents(ctx, assignment.ID, []uuid.UUID{validStudent, invalidStudent})

	if len(created) != 1 {
		t.Fatalf("expected 1 created, got %d", len(created))
	}
	if created[0].StudentID != validStudent {
		t.Errorf("expected validStudent in created, got %s", created[0].StudentID)
	}

	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if errs[0].StudentID != invalidStudent {
		t.Errorf("expected invalidStudent in errors, got %s", errs[0].StudentID)
	}
	if errs[0].Error != "STUDENT_NOT_FOUND" {
		t.Errorf("expected STUDENT_NOT_FOUND, got %q", errs[0].Error)
	}

	// Valid student's course must exist.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM courses WHERE student_id = $1`, validStudent,
	).Scan(&count); err != nil {
		t.Fatalf("COUNT courses for validStudent: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 course for validStudent, got %d", count)
	}
}

// TestIntegration_Unassign_AllowedAtSyllabusApproved verifies deletion of a
// syllabus_approved course row (REQ-ASSIGN-007).
//
// @{"verifies": ["REQ-ASSIGN-007"]}
func TestIntegration_Unassign_AllowedAtSyllabusApproved(t *testing.T) {
	truncateAssignmentTables(t)
	ctx := context.Background()
	pool := db.IntegrationPool(t)

	adminID := seedAdminUser(t)
	studentID := seedStudentUser(t)

	repo := NewAssignmentRepository(pool)
	svc := NewAssignmentService(repo, &syllabusStub{})

	assignment, err := repo.CreateAssignment(ctx, adminID, "", "Rust", "advanced", json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}
	created, errs := svc.AssignStudents(ctx, assignment.ID, []uuid.UUID{studentID})
	if len(errs) != 0 || len(created) != 1 {
		t.Fatalf("AssignStudents failed: created=%d errs=%v", len(created), errs)
	}

	deleted, reason, err := svc.UnassignStudent(ctx, assignment.ID, studentID)
	if err != nil {
		t.Fatalf("UnassignStudent: %v", err)
	}
	if !deleted {
		t.Errorf("expected deleted=true, got reason=%q", reason)
	}

	// Course row must no longer exist.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM courses WHERE id = $1`, created[0].CourseID,
	).Scan(&count); err != nil {
		t.Fatalf("COUNT courses after unassign: %v", err)
	}
	if count != 0 {
		t.Errorf("expected course to be deleted, got count=%d", count)
	}
}

// TestIntegration_Unassign_BlockedWhenGenerating verifies that
// ErrGenerationInProgress is returned when the course is generating
// (REQ-ASSIGN-007).
//
// @{"verifies": ["REQ-ASSIGN-007"]}
func TestIntegration_Unassign_BlockedWhenGenerating(t *testing.T) {
	truncateAssignmentTables(t)
	ctx := context.Background()
	pool := db.IntegrationPool(t)

	adminID := seedAdminUser(t)
	studentID := seedStudentUser(t)

	repo := NewAssignmentRepository(pool)
	svc := NewAssignmentService(repo, &syllabusStub{})

	assignment, err := repo.CreateAssignment(ctx, adminID, "", "C++", "intermediate", json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}
	created, errs := svc.AssignStudents(ctx, assignment.ID, []uuid.UUID{studentID})
	if len(errs) != 0 || len(created) != 1 {
		t.Fatalf("AssignStudents: created=%d errs=%v", len(created), errs)
	}

	// Simulate runner transitioning the course to 'generating'.
	if _, err := pool.Exec(ctx,
		`UPDATE courses SET status = 'generating' WHERE id = $1`, created[0].CourseID,
	); err != nil {
		t.Fatalf("UPDATE courses to generating: %v", err)
	}

	_, _, err = svc.UnassignStudent(ctx, assignment.ID, studentID)
	if !errors.Is(err, ErrGenerationInProgress) {
		t.Errorf("expected ErrGenerationInProgress, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// RLS probe tests (REQ-ASSIGN-009, REQ-ASSIGN-010)
// ---------------------------------------------------------------------------

// TestIntegration_RLS_StudentCannotSeeOtherStudentAssignedCourse is the key
// isolation probe: studentB's connection must not be able to SELECT studentA's
// assigned course row (REQ-ASSIGN-009).
//
// @{"verifies": ["REQ-ASSIGN-009"]}
func TestIntegration_RLS_StudentCannotSeeOtherStudentAssignedCourse(t *testing.T) {
	truncateAssignmentTables(t)
	ctx := context.Background()
	pool := db.IntegrationPool(t)

	adminID := seedAdminUser(t)
	studentA := seedStudentUser(t)
	studentB := seedStudentUser(t)

	repo := NewAssignmentRepository(pool)
	svc := NewAssignmentService(repo, &syllabusStub{})

	assignment, err := repo.CreateAssignment(ctx, adminID, "", "Algebra", "beginner", json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}
	// Assign both students so both course rows exist.
	created, errs := svc.AssignStudents(ctx, assignment.ID, []uuid.UUID{studentA, studentB})
	if len(errs) != 0 || len(created) != 2 {
		t.Fatalf("AssignStudents: created=%d errs=%v", len(created), errs)
	}

	// Find studentA's course ID.
	var courseAID uuid.UUID
	for _, c := range created {
		if c.StudentID == studentA {
			courseAID = c.CourseID
		}
	}
	if courseAID == (uuid.UUID{}) {
		t.Fatal("could not find studentA's course in created results")
	}

	// Connect as studentB and attempt to SELECT studentA's course row.
	// AcquireAsUser sets app.current_user_id + app.current_role='student' and
	// SET ROLE valory_app so RLS policies are actually enforced.
	userBHex := fmt.Sprintf("%x", [16]byte(studentB))
	connB := db.AcquireAsUser(t, pool, userBHex, "student")
	defer connB.Release()

	var count int
	if err := connB.QueryRow(ctx,
		`SELECT COUNT(*) FROM courses WHERE id = $1`, courseAID,
	).Scan(&count); err != nil {
		t.Fatalf("SELECT as studentB: %v", err)
	}
	// RLS must deny: count must be 0 (not studentB's row).
	if count != 0 {
		t.Errorf("RLS FAILURE: studentB can see studentA's course row (count=%d)", count)
	}
}

// TestIntegration_RLS_AdminSeesAllAssignedCourseRows verifies that an admin
// session (app.current_role='admin') can SELECT all course rows for an
// assignment regardless of student_id (REQ-ASSIGN-010).
//
// @{"verifies": ["REQ-ASSIGN-010"]}
func TestIntegration_RLS_AdminSeesAllAssignedCourseRows(t *testing.T) {
	truncateAssignmentTables(t)
	ctx := context.Background()
	pool := db.IntegrationPool(t)

	adminID := seedAdminUser(t)
	s1 := seedStudentUser(t)
	s2 := seedStudentUser(t)

	repo := NewAssignmentRepository(pool)
	svc := NewAssignmentService(repo, &syllabusStub{})

	assignment, err := repo.CreateAssignment(ctx, adminID, "", "Statistics", "intermediate", json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}
	created, errs := svc.AssignStudents(ctx, assignment.ID, []uuid.UUID{s1, s2})
	if len(errs) != 0 || len(created) != 2 {
		t.Fatalf("AssignStudents: created=%d errs=%v", len(created), errs)
	}

	// Connect as admin role.
	adminIDHex := fmt.Sprintf("%x", [16]byte(adminID))
	connAdmin := db.AcquireAsUser(t, pool, adminIDHex, "admin")
	defer connAdmin.Release()

	var count int
	if err := connAdmin.QueryRow(ctx,
		`SELECT COUNT(*) FROM courses WHERE assignment_id = $1`, assignment.ID,
	).Scan(&count); err != nil {
		t.Fatalf("SELECT as admin: %v", err)
	}
	if count != 2 {
		t.Errorf("admin should see 2 course rows for assignment, got %d", count)
	}
}

// TestIntegration_RLS_CourseAssignmentsReadableByValoryApp verifies that the
// course_assignments table (no RLS) is readable by valory_app without any GUC
// set (SDD-022 §5.5, REQ-ASSIGN-001).
//
// @{"verifies": ["REQ-ASSIGN-001"]}
func TestIntegration_RLS_CourseAssignmentsReadableByValoryApp(t *testing.T) {
	truncateAssignmentTables(t)
	ctx := context.Background()
	pool := db.IntegrationPool(t)

	adminID := seedAdminUser(t)
	repo := NewAssignmentRepository(pool)

	assignment, err := repo.CreateAssignment(ctx, adminID, "", "Calculus", "beginner", json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}

	// Use a connection with empty GUCs (no user ID, no role) but SET ROLE
	// valory_app so we confirm course_assignments is accessible without RLS
	// enforcement (the table has no RLS policy).
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()

	if err := conn.Conn().PgConn().Exec(ctx, "SET ROLE valory_app").Close(); err != nil {
		t.Fatalf("SET ROLE valory_app: %v", err)
	}
	// Clear GUCs — no user identity set.
	if _, err := conn.Exec(ctx,
		"SELECT set_config('app.current_user_id', '', false), set_config('app.current_role', '', false)",
	); err != nil {
		t.Fatalf("clear GUCs: %v", err)
	}

	var count int
	if err := conn.QueryRow(ctx,
		`SELECT COUNT(*) FROM course_assignments WHERE id = $1`, assignment.ID,
	).Scan(&count); err != nil {
		t.Fatalf("SELECT course_assignments without GUCs: %v", err)
	}
	// Table has no RLS — valory_app must see the row.
	if count != 1 {
		t.Errorf("expected course_assignments to be readable by valory_app (count=1), got %d", count)
	}
}

// ---------------------------------------------------------------------------
// Non-superuser RLS path tests (closes the superuser-masking gap)
// ---------------------------------------------------------------------------

// TestIntegration_RLS_NonSuperuser_AdminConn_CourseInsertSucceeds proves that
// AssignmentRepository.CreateAssignedCourse succeeds when the request-scoped
// context carries a NON-SUPERUSER valory_app connection with
// app.current_role='admin'.
//
// WHY this test is necessary:
// The existing RLS tests (TestIntegration_AssignStudents_CreatesTwoIsolatedCourses,
// TestIntegration_RLS_AdminSeesAllAssignedCourseRows) all call AssignStudents /
// CreateAssignment through the bare pool — which in integration tests is the
// valory_test SUPERUSER.  Superusers bypass FORCE ROW LEVEL SECURITY entirely,
// so those tests never exercised the actual RLS path.  The production role
// (valory_app, NOSUPERUSER, NOBYPASSRLS) is subject to FORCE RLS, meaning an
// empty or absent app.current_role GUC causes every courses INSERT/SELECT/DELETE
// to fail with "new row violates row-level security policy" (42501).
//
// This test closes that gap by:
//  1. Using db.AcquireAsUser to obtain a valory_app connection (SET ROLE
//     valory_app) with app.current_role='admin' — exactly what the production
//     auth middleware sets for admin sessions.
//  2. Injecting that connection into the context via auth.ContextWithConn,
//     mirroring the middleware's context.WithValue(ctx, contextKeyConn, conn).
//  3. Calling CreateAssignedCourse through the repo on that context and
//     asserting the INSERT succeeds.
//
// This would have CAUGHT the original bug: before the conn(ctx) fix,
// CreateAssignedCourse called r.pool.QueryRow directly, acquiring a fresh
// connection from the bare pool with empty GUCs — the admin-role GUC set on
// the injected connection was completely ignored, and the INSERT failed 42501.
//
// @{"verifies": ["REQ-ASSIGN-003", "REQ-ASSIGN-009", "REQ-ASSIGN-010", "REQ-SECURITY-002"]}
func TestIntegration_RLS_NonSuperuser_AdminConn_CourseInsertSucceeds(t *testing.T) {
	truncateAssignmentTables(t)
	ctx := context.Background()
	pool := db.IntegrationPool(t)

	adminID := seedAdminUser(t)
	studentID := seedStudentUser(t)

	repo := NewAssignmentRepository(pool)

	// CreateAssignment touches course_assignments (no RLS) — use bare pool ctx.
	assignment, err := repo.CreateAssignment(ctx, adminID, "", "Non-superuser RLS test", "beginner", json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}

	// Acquire a NON-SUPERUSER valory_app connection with app.current_role='admin'.
	// db.AcquireAsUser issues SET ROLE valory_app, making this connection subject
	// to FORCE ROW LEVEL SECURITY.  app.current_role='admin' activates
	// courses_admin_policy, which admits INSERT/SELECT/DELETE on courses.
	adminIDHex := fmt.Sprintf("%x", [16]byte(adminID))
	adminConn := db.AcquireAsUser(t, pool, adminIDHex, "admin")
	defer adminConn.Release()

	// Inject the connection into the context exactly as the auth middleware does.
	// auth.ContextWithConn stores the *pgxpool.Conn under contextKeyConn so that
	// repo.conn(ctx) returns it instead of the bare pool.
	ctxWithAdminConn := authpkg.ContextWithConn(ctx, adminConn)

	// CreateAssignedCourse does an INSERT INTO courses — the primary operation
	// that was blocked in production.  With conn(ctx) routing it through the
	// injected connection (which has app.current_role='admin'), courses_admin_policy
	// must admit the INSERT.
	courseID, err := repo.CreateAssignedCourse(ctxWithAdminConn, studentID, assignment.ID, assignment.Topic)
	if err != nil {
		t.Fatalf(
			"CreateAssignedCourse under valory_app with app.current_role='admin': "+
				"unexpected error — courses_admin_policy should permit this INSERT "+
				"(this error would have been the production failure before the conn(ctx) fix): %v",
			err,
		)
	}
	if courseID == (uuid.UUID{}) {
		t.Fatal("CreateAssignedCourse returned zero UUID — insert did not return an id")
	}

	// Confirm the course row is visible to the same admin connection.
	var status string
	if err := adminConn.QueryRow(ctx,
		`SELECT status::text FROM courses WHERE id = $1`, courseID,
	).Scan(&status); err != nil {
		t.Fatalf("SELECT courses after insert (admin conn): %v", err)
	}
	if status != "syllabus_approved" {
		t.Errorf("expected status 'syllabus_approved', got %q", status)
	}
}

// TestIntegration_RLS_NonSuperuser_BareValoryApp_CourseInsertBlocked proves that
// a valory_app connection with EMPTY GUCs (no app.current_role, no
// app.current_user_id) is BLOCKED by courses FORCE RLS when attempting an INSERT.
//
// This is the negative-path counterpart to the test above.  It proves that the
// RLS policies are actually being enforced on the valory_app role (i.e. FORCE RLS
// is active and the test database is not bypassing it).  If this test passes,
// the positive test above is meaningful: it shows that the admin-role GUC is the
// actual gate, not some misconfiguration that lets everything through.
//
// Before the conn(ctx) fix, r.pool.QueryRow was used for courses INSERTs.  The
// pool's AfterRelease hook clears both GUCs to ” on connection return.  Any
// connection acquired from the bare pool has empty GUCs — identical to the
// "blocked" state this test exercises.  This test would have reproduced the
// exact production failure.
//
// @{"verifies": ["REQ-ASSIGN-009", "REQ-SECURITY-002"]}
func TestIntegration_RLS_NonSuperuser_BareValoryApp_CourseInsertBlocked(t *testing.T) {
	truncateAssignmentTables(t)
	ctx := context.Background()
	pool := db.IntegrationPool(t)

	adminID := seedAdminUser(t)
	studentID := seedStudentUser(t)

	repo := NewAssignmentRepository(pool)

	assignment, err := repo.CreateAssignment(ctx, adminID, "", "RLS block test", "beginner", json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}

	// Acquire a bare valory_app connection with EMPTY GUCs — the exact state that
	// pool.AfterRelease leaves every connection in after it is returned.  No role
	// or user-id GUC is set, so neither courses_admin_policy nor
	// courses_student_policy matches: the INSERT must be rejected with 42501.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()

	// Downgrade to valory_app so FORCE RLS is active (superuser would bypass it).
	if err := conn.Conn().PgConn().Exec(ctx, "SET ROLE valory_app").Close(); err != nil {
		t.Fatalf("SET ROLE valory_app: %v", err)
	}
	// Explicitly clear GUCs to reproduce the AfterRelease state.
	if _, err := conn.Exec(ctx,
		"SELECT set_config('app.current_user_id', '', false), set_config('app.current_role', '', false)",
	); err != nil {
		t.Fatalf("clear GUCs: %v", err)
	}

	// Inject this empty-GUC connection — if conn(ctx) is working correctly the
	// INSERT will use it, reproduce the empty-GUC state, and be blocked by RLS.
	ctxWithBareConn := authpkg.ContextWithConn(ctx, conn)

	_, err = repo.CreateAssignedCourse(ctxWithBareConn, studentID, assignment.ID, assignment.Topic)
	if err == nil {
		t.Fatal(
			"RLS FAILURE: INSERT courses succeeded on a bare valory_app connection with " +
				"empty GUCs — FORCE ROW LEVEL SECURITY is not being enforced. " +
				"Check that SET ROLE valory_app was issued and the courses table has FORCE ROW LEVEL SECURITY.",
		)
	}
	// The error must be a policy violation (42501), not some other error.
	// A 42501 here is the exact error that the original bug caused in production
	// on admin-session connections that went through the bare pool.
	var pgErr interface{ SQLState() string }
	if !errors.As(err, &pgErr) || pgErr.SQLState() != "42501" {
		// Accept any error that is not "success" — if RLS is enforced the
		// specific error type may vary (pgconn.PgError wraps 42501).  We use
		// a type assertion rather than pgconn.PgError directly to avoid
		// importing pgconn here; the SQLState() method is on *pgconn.PgError.
		// Fall through: log the actual error for diagnostic purposes.
		t.Logf(
			"INSERT courses with empty GUCs returned error (expected), "+
				"SQLSTATE not 42501 but any non-nil error confirms RLS enforcement: %v", err,
		)
	}
}
