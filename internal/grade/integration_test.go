//go:build integration

// integration_test.go — repository-layer integration tests for the grade module.
//
// These tests run against a real PostgreSQL instance (spun up via Docker Compose)
// with all embedded migrations applied. No database mocks are used.
//
// Run with:
//
//	make test-integration
//	go test -tags integration ./internal/grade/
//
// The shared harness (internal/db.IntegrationPool) skips every test in this file
// automatically when the database is unreachable, so a normal `go test ./...`
// run remains green without Docker.
package grade

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/db"
)

// ---------------------------------------------------------------------------
// Fixture helpers
// ---------------------------------------------------------------------------

// insertIntgUser creates a minimal user row using the bare pool.
// The test DB connects as the PostgreSQL superuser (valory_test) which bypasses
// FORCE RLS, so fixture inserts do not need GUC-scoped connections.
func insertIntgUser(ctx context.Context, t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role)
		 VALUES ($1, 'hash_intg', 'student')
		 RETURNING id`,
		"intg_user_"+uuid.New().String(),
	).Scan(&id)
	if err != nil {
		t.Fatalf("insertIntgUser: %v", err)
	}
	return id
}

// insertIntgCourse creates an active course owned by studentID.
// Uses the bare (superuser) pool to bypass FORCE RLS on courses.
func insertIntgCourse(ctx context.Context, t *testing.T, pool *pgxpool.Pool, studentID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO courses (student_id, topic, status) VALUES ($1, 'Integration Test', 'active') RETURNING id`,
		studentID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insertIntgCourse: %v", err)
	}
	return id
}

// insertIntgHomework creates a homework row with the given gradeWeight.
// homework has no RLS so the bare pool is safe here.
func insertIntgHomework(ctx context.Context, t *testing.T, pool *pgxpool.Pool, courseID uuid.UUID, gradeWeight float64) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO homework (course_id, section_index, title, rubric, grade_weight)
		 VALUES ($1, 1, 'Intg HW', 'rubric', $2)
		 RETURNING id`,
		courseID, gradeWeight,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insertIntgHomework: %v", err)
	}
	return id
}

// insertIntgSubmission inserts a submission row for the given student/homework pair.
// Uses the bare (superuser) pool to bypass FORCE RLS on submissions.
func insertIntgSubmission(ctx context.Context, t *testing.T, pool *pgxpool.Pool, hwID, studentID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO submissions (homework_id, student_id, format, file_path, file_size_bytes)
		 VALUES ($1, $2, 'markdown', '/tmp/intg_test.md', 512)
		 RETURNING id`,
		hwID, studentID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insertIntgSubmission: %v", err)
	}
	return id
}

// acquireServerConn returns a pool connection configured for the server role.
// It delegates entirely to db.AcquireAsServer, which issues SET ROLE valory_app
// and sets app.current_role='server' so that grades FORCE RLS server-access
// policies are enforced rather than bypassed.
func acquireServerConn(ctx context.Context, t *testing.T, pool *pgxpool.Pool) *pgxpool.Conn {
	t.Helper()
	return db.AcquireAsServer(t, pool)
}

// acquireStudentConn returns a pool connection scoped to the given student.
// It delegates to db.AcquireAsUser, which issues SET ROLE valory_app (so FORCE
// RLS is enforced) and sets app.current_user_id and app.current_role GUCs.
// The user ID is passed as a 32-char no-dash hex string — the same format the
// auth middleware uses and that the RLS policy's ::uuid cast expects.
func acquireStudentConn(ctx context.Context, t *testing.T, pool *pgxpool.Pool, studentID uuid.UUID) *pgxpool.Conn {
	t.Helper()
	// Use hex format (no dashes) — that is what the auth middleware passes and
	// what the RLS policy's ::uuid cast expects. The [16]byte conversion is
	// load-bearing: %x on uuid.UUID itself goes through its String() method
	// and hex-encodes the dashed text form, which the ::uuid cast rejects.
	userIDHex := fmt.Sprintf("%x", [16]byte(studentID))
	return db.AcquireAsUser(t, pool, userIDHex, "student")
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// RLS isolation — student A cannot read student B's grades.
//
// The grades table student-select policy USING clause binds a row to the
// session's app.current_user_id GUC. A connection scoped to student A must
// not see rows owned by student B; GetBySubmission must return ErrNotFound
// rather than student B's grade.
//
// @{"verifies": ["REQ-SECURITY-002", "REQ-GRADE-001"]}
func TestRepository_RLSIsolation_StudentCannotReadAnotherStudentsGrade(t *testing.T) {
	pool := db.IntegrationPool(t)
	ctx := context.Background()

	db.TruncateTables(t, pool,
		"grades", "submissions", "student_badges", "badges",
		"due_date_schedules", "homework", "agent_token_usage",
		"syllabi", "courses", "users",
	)

	// Seed two distinct students.
	studentAID := insertIntgUser(ctx, t, pool)
	studentBID := insertIntgUser(ctx, t, pool)

	courseAID := insertIntgCourse(ctx, t, pool, studentAID)
	courseBID := insertIntgCourse(ctx, t, pool, studentBID)

	hwAID := insertIntgHomework(ctx, t, pool, courseAID, 0.5)
	hwBID := insertIntgHomework(ctx, t, pool, courseBID, 0.5)

	subAID := insertIntgSubmission(ctx, t, pool, hwAID, studentAID)
	subBID := insertIntgSubmission(ctx, t, pool, hwBID, studentBID)

	// Insert grade for student B using a server-role connection (the grading
	// agent's write path).  This must succeed so the row exists for the
	// subsequent isolation check.
	serverConn := acquireServerConn(ctx, t, pool)
	defer serverConn.Release()

	serverCtx := auth.ContextWithConn(ctx, serverConn)
	repo := NewRepository(pool)

	_, err := repo.Insert(serverCtx, GradeRow{
		SubmissionID: subBID,
		HomeworkID:   hwBID,
		StudentID:    studentBID,
		CourseID:     courseBID,
		RawScore:     88,
		FinalScore:   88,
	})
	if err != nil {
		t.Fatalf("server-role insert for student B: %v", err)
	}

	// Also insert a grade for student A so the table is not empty; this
	// rules out a "zero rows" shortcut in any future LIMIT optimisation.
	serverConnA := acquireServerConn(ctx, t, pool)
	defer serverConnA.Release()
	serverCtxA := auth.ContextWithConn(ctx, serverConnA)
	_, err = repo.Insert(serverCtxA, GradeRow{
		SubmissionID: subAID,
		HomeworkID:   hwAID,
		StudentID:    studentAID,
		CourseID:     courseAID,
		RawScore:     75,
		FinalScore:   75,
	})
	if err != nil {
		t.Fatalf("server-role insert for student A: %v", err)
	}

	// Attempt to read student B's grade row as student A: RLS should block it.
	studentAConn := acquireStudentConn(ctx, t, pool, studentAID)
	defer studentAConn.Release()
	studentACtx := auth.ContextWithConn(ctx, studentAConn)

	_, err = repo.GetBySubmission(studentACtx, subBID)
	if err == nil {
		t.Fatal("expected ErrNotFound when student A reads student B's grade, got nil error")
	}
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

// Server-role INSERT passes grades FORCE RLS.
//
// The grading pipeline runs as a background goroutine that injects a
// server-role connection via auth.ContextWithConn. This test verifies that
// the INSERT reaches the database and the returned row has the expected field
// values — confirming the server-access policy (app.current_role='server') is
// satisfied end-to-end against a real schema.
//
// @{"verifies": ["REQ-GRADE-001", "REQ-GRADE-002"]}
func TestRepository_ServerRoleInsert_GradeStoredAndReturned(t *testing.T) {
	pool := db.IntegrationPool(t)
	ctx := context.Background()

	db.TruncateTables(t, pool,
		"grades", "submissions", "student_badges", "badges",
		"due_date_schedules", "homework", "agent_token_usage",
		"syllabi", "courses", "users",
	)

	studentID := insertIntgUser(ctx, t, pool)
	courseID := insertIntgCourse(ctx, t, pool, studentID)
	hwID := insertIntgHomework(ctx, t, pool, courseID, 0.6)
	subID := insertIntgSubmission(ctx, t, pool, hwID, studentID)

	serverConn := acquireServerConn(ctx, t, pool)
	defer serverConn.Release()
	serverCtx := auth.ContextWithConn(ctx, serverConn)

	repo := NewRepository(pool)
	want := GradeRow{
		SubmissionID:       subID,
		HomeworkID:         hwID,
		StudentID:          studentID,
		CourseID:           courseID,
		RawScore:           90,
		LateDays:           2,
		LatePenaltyRate:    0.05,
		LatePenaltyAmount:  9.0,
		BadgeWaiverApplied: false,
		BadgeImprovement:   0,
		FinalScore:         81,
	}

	got, err := repo.Insert(serverCtx, want)
	if err != nil {
		t.Fatalf("Insert with server-role conn: %v", err)
	}

	// The RETURNING clause must populate a DB-assigned UUID.
	if got.ID == uuid.Nil {
		t.Error("expected non-nil grade ID from RETURNING clause")
	}
	// Verify each persisted field round-trips correctly.
	if got.RawScore != want.RawScore {
		t.Errorf("RawScore: want %.2f, got %.2f", want.RawScore, got.RawScore)
	}
	if got.LateDays != want.LateDays {
		t.Errorf("LateDays: want %d, got %d", want.LateDays, got.LateDays)
	}
	if got.LatePenaltyAmount != want.LatePenaltyAmount {
		t.Errorf("LatePenaltyAmount: want %.2f, got %.2f", want.LatePenaltyAmount, got.LatePenaltyAmount)
	}
	if got.FinalScore != want.FinalScore {
		t.Errorf("FinalScore: want %.2f, got %.2f", want.FinalScore, got.FinalScore)
	}
	if got.BadgeWaiverApplied != want.BadgeWaiverApplied {
		t.Errorf("BadgeWaiverApplied: want %v, got %v", want.BadgeWaiverApplied, got.BadgeWaiverApplied)
	}
}

// FK integrity — inserting a grade with a non-existent submission_id must
// return a PostgreSQL foreign-key violation error.
//
// The grades table declares:
//
//	submission_id UUID NOT NULL REFERENCES submissions(id) ON DELETE CASCADE
//
// Attempting to insert with a random (unknown) UUID must fail at the DB level,
// confirming the constraint is active in the real schema.
//
// @{"verifies": ["REQ-GRADE-001"]}
func TestRepository_Insert_FKViolation_UnknownSubmissionID(t *testing.T) {
	pool := db.IntegrationPool(t)
	ctx := context.Background()

	db.TruncateTables(t, pool,
		"grades", "submissions", "student_badges", "badges",
		"due_date_schedules", "homework", "agent_token_usage",
		"syllabi", "courses", "users",
	)

	studentID := insertIntgUser(ctx, t, pool)
	courseID := insertIntgCourse(ctx, t, pool, studentID)
	hwID := insertIntgHomework(ctx, t, pool, courseID, 0.5)

	serverConn := acquireServerConn(ctx, t, pool)
	defer serverConn.Release()
	serverCtx := auth.ContextWithConn(ctx, serverConn)

	repo := NewRepository(pool)
	// Use a random UUID that does not correspond to any submission row.
	bogusSubID := uuid.New()

	_, err := repo.Insert(serverCtx, GradeRow{
		SubmissionID: bogusSubID, // FK violation: no matching row in submissions
		HomeworkID:   hwID,
		StudentID:    studentID,
		CourseID:     courseID,
		RawScore:     70,
		FinalScore:   70,
	})
	// Any non-nil error from PostgreSQL confirms the FK constraint is enforced.
	if err == nil {
		t.Fatal("expected FK violation error for unknown submission_id, got nil")
	}
}

// CHECK constraint — raw_score above 100 must be rejected.
//
// The grades migration declares:
//
//	raw_score NUMERIC(5,2) NOT NULL CHECK (raw_score BETWEEN 0 AND 100)
//
// An Insert attempt with raw_score=101 must fail, confirming the constraint
// exists and is enforced in the real schema.
//
// @{"verifies": ["REQ-GRADE-001"]}
func TestRepository_Insert_CheckViolation_RawScoreAbove100(t *testing.T) {
	pool := db.IntegrationPool(t)
	ctx := context.Background()

	db.TruncateTables(t, pool,
		"grades", "submissions", "student_badges", "badges",
		"due_date_schedules", "homework", "agent_token_usage",
		"syllabi", "courses", "users",
	)

	studentID := insertIntgUser(ctx, t, pool)
	courseID := insertIntgCourse(ctx, t, pool, studentID)
	hwID := insertIntgHomework(ctx, t, pool, courseID, 0.5)
	subID := insertIntgSubmission(ctx, t, pool, hwID, studentID)

	serverConn := acquireServerConn(ctx, t, pool)
	defer serverConn.Release()
	serverCtx := auth.ContextWithConn(ctx, serverConn)

	repo := NewRepository(pool)
	_, err := repo.Insert(serverCtx, GradeRow{
		SubmissionID: subID,
		HomeworkID:   hwID,
		StudentID:    studentID,
		CourseID:     courseID,
		RawScore:     101, // violates CHECK (raw_score BETWEEN 0 AND 100)
		FinalScore:   95,
	})
	if err == nil {
		t.Fatal("expected CHECK constraint error for raw_score=101, got nil")
	}
}

// CHECK constraint — final_score below 0 must be rejected.
//
// The grades migration declares:
//
//	final_score NUMERIC(5,2) NOT NULL CHECK (final_score BETWEEN 0 AND 100)
//
// Confirming this constraint prevents the storage layer from ever persisting a
// logically invalid grade even if the computation layer has a bug.
//
// @{"verifies": ["REQ-GRADE-001"]}
func TestRepository_Insert_CheckViolation_FinalScoreBelow0(t *testing.T) {
	pool := db.IntegrationPool(t)
	ctx := context.Background()

	db.TruncateTables(t, pool,
		"grades", "submissions", "student_badges", "badges",
		"due_date_schedules", "homework", "agent_token_usage",
		"syllabi", "courses", "users",
	)

	studentID := insertIntgUser(ctx, t, pool)
	courseID := insertIntgCourse(ctx, t, pool, studentID)
	hwID := insertIntgHomework(ctx, t, pool, courseID, 0.5)
	subID := insertIntgSubmission(ctx, t, pool, hwID, studentID)

	serverConn := acquireServerConn(ctx, t, pool)
	defer serverConn.Release()
	serverCtx := auth.ContextWithConn(ctx, serverConn)

	repo := NewRepository(pool)
	_, err := repo.Insert(serverCtx, GradeRow{
		SubmissionID: subID,
		HomeworkID:   hwID,
		StudentID:    studentID,
		CourseID:     courseID,
		RawScore:     50,
		FinalScore:   -1, // violates CHECK (final_score BETWEEN 0 AND 100)
	})
	if err == nil {
		t.Fatal("expected CHECK constraint error for final_score=-1, got nil")
	}
}

// Weighted-score aggregation — GetCourseGradeSummary computes the correct
// weighted average across two homework assignments with real DB rows.
//
// Verifies REQ-GRADE-003: grade weights come from the homework.grade_weight
// column (set at course-planning time, derived from the approved syllabus via
// REQ-AGENT-002). The query is:
//
//	SUM(g.final_score * h.grade_weight) / SUM(h.grade_weight)
//
// With hw1: score=80, weight=0.6 and hw2: score=60, weight=0.4:
//
//	(80*0.6 + 60*0.4) / (0.6+0.4) = (48+24)/1 = 72
//
// Uses a student-scoped connection so the student-select RLS policy on grades
// is exercised in addition to the aggregation correctness check.
//
// @{"verifies": ["REQ-GRADE-003", "REQ-SECURITY-002"]}
func TestRepository_GetCourseGradeSummary_WeightedAverageFromRealRows(t *testing.T) {
	pool := db.IntegrationPool(t)
	ctx := context.Background()

	db.TruncateTables(t, pool,
		"grades", "submissions", "student_badges", "badges",
		"due_date_schedules", "homework", "agent_token_usage",
		"syllabi", "courses", "users",
	)

	studentID := insertIntgUser(ctx, t, pool)
	courseID := insertIntgCourse(ctx, t, pool, studentID)

	hw1ID := insertIntgHomework(ctx, t, pool, courseID, 0.6)
	hw2ID := insertIntgHomework(ctx, t, pool, courseID, 0.4)
	sub1ID := insertIntgSubmission(ctx, t, pool, hw1ID, studentID)
	sub2ID := insertIntgSubmission(ctx, t, pool, hw2ID, studentID)

	// Insert both grade rows as the server (grading pipeline write path).
	serverConn := acquireServerConn(ctx, t, pool)
	defer serverConn.Release()
	serverCtx := auth.ContextWithConn(ctx, serverConn)
	repo := NewRepository(pool)

	for _, g := range []GradeRow{
		{SubmissionID: sub1ID, HomeworkID: hw1ID, StudentID: studentID, CourseID: courseID,
			RawScore: 80, LatePenaltyRate: 0.05, FinalScore: 80},
		{SubmissionID: sub2ID, HomeworkID: hw2ID, StudentID: studentID, CourseID: courseID,
			RawScore: 60, LatePenaltyRate: 0.05, FinalScore: 60},
	} {
		if _, err := repo.Insert(serverCtx, g); err != nil {
			t.Fatalf("Insert grade: %v", err)
		}
	}

	// Read the summary as the student so the student-select RLS policy is
	// exercised in addition to the aggregation correctness.
	studentConn := acquireStudentConn(ctx, t, pool, studentID)
	defer studentConn.Release()
	studentCtx := auth.ContextWithConn(ctx, studentConn)

	summary, err := repo.GetCourseGradeSummary(studentCtx, courseID, studentID)
	if err != nil {
		t.Fatalf("GetCourseGradeSummary: %v", err)
	}

	// (80*0.6 + 60*0.4) / (0.6+0.4) = 72.0
	const wantScore = 72.0
	const wantWeight = 1.0
	const epsilon = 0.01 // tolerance for NUMERIC → float64 conversion

	if summary.WeightedScore < wantScore-epsilon || summary.WeightedScore > wantScore+epsilon {
		t.Errorf("WeightedScore: want %.4f ± %.4f, got %.4f", wantScore, epsilon, summary.WeightedScore)
	}
	if summary.TotalWeight < wantWeight-epsilon || summary.TotalWeight > wantWeight+epsilon {
		t.Errorf("TotalWeight: want %.4f ± %.4f, got %.4f", wantWeight, epsilon, summary.TotalWeight)
	}
	if summary.CourseID != courseID {
		t.Errorf("CourseID: want %s, got %s", courseID, summary.CourseID)
	}
	if summary.StudentID != studentID {
		t.Errorf("StudentID: want %s, got %s", studentID, summary.StudentID)
	}
}

// Upsert idempotency with server-role connection — a second Insert for the
// same submission_id updates the existing row rather than returning a
// duplicate-key error.
//
// The repository uses ON CONFLICT (submission_id) DO UPDATE to model
// re-grading (e.g. an admin triggers a re-grade after a rubric correction).
// This test confirms the upsert path works end-to-end against the real UNIQUE
// constraint:
//
//	CONSTRAINT uq_grade_submission UNIQUE (submission_id)
//
// @{"verifies": ["REQ-GRADE-001", "REQ-GRADE-002"]}
func TestRepository_ServerRoleInsert_Upsert_RegradesExistingRow(t *testing.T) {
	pool := db.IntegrationPool(t)
	ctx := context.Background()

	db.TruncateTables(t, pool,
		"grades", "submissions", "student_badges", "badges",
		"due_date_schedules", "homework", "agent_token_usage",
		"syllabi", "courses", "users",
	)

	studentID := insertIntgUser(ctx, t, pool)
	courseID := insertIntgCourse(ctx, t, pool, studentID)
	hwID := insertIntgHomework(ctx, t, pool, courseID, 0.5)
	subID := insertIntgSubmission(ctx, t, pool, hwID, studentID)

	repo := NewRepository(pool)

	// First grade: initial automated score.
	serverConn1 := acquireServerConn(ctx, t, pool)
	defer serverConn1.Release()
	ctx1 := auth.ContextWithConn(ctx, serverConn1)

	first, err := repo.Insert(ctx1, GradeRow{
		SubmissionID: subID,
		HomeworkID:   hwID,
		StudentID:    studentID,
		CourseID:     courseID,
		RawScore:     70,
		FinalScore:   70,
	})
	if err != nil {
		t.Fatalf("first Insert: %v", err)
	}

	// Second grade on the same submission (re-grade after rubric fix).
	serverConn2 := acquireServerConn(ctx, t, pool)
	defer serverConn2.Release()
	ctx2 := auth.ContextWithConn(ctx, serverConn2)

	second, err := repo.Insert(ctx2, GradeRow{
		SubmissionID: subID,
		HomeworkID:   hwID,
		StudentID:    studentID,
		CourseID:     courseID,
		RawScore:     85,
		FinalScore:   85,
	})
	if err != nil {
		t.Fatalf("second Insert (upsert): %v", err)
	}

	// The grade row ID must remain stable across the upsert (ON CONFLICT DO UPDATE
	// retains the original primary key).
	if second.ID != first.ID {
		t.Errorf("upsert changed grade ID: first=%s, second=%s", first.ID, second.ID)
	}
	// The updated score must be reflected.
	if second.FinalScore != 85 {
		t.Errorf("FinalScore after upsert: want 85, got %.2f", second.FinalScore)
	}
}

// Student reads own grade — GetBySubmission returns the correct row when the
// context carries the owning student's scoped connection.
//
// Verifies the student-select policy on grades:
//
//	USING (student_id = (current_setting('app.current_user_id', true))::uuid)
//
// This is the positive-path counterpart to the RLS isolation test above: the
// same policy that blocks cross-student reads also permits reads by the row's
// owner.
//
// @{"verifies": ["REQ-SECURITY-002", "REQ-GRADE-001"]}
func TestRepository_StudentCanReadOwnGrade_ViaRLSScopedConn(t *testing.T) {
	pool := db.IntegrationPool(t)
	ctx := context.Background()

	db.TruncateTables(t, pool,
		"grades", "submissions", "student_badges", "badges",
		"due_date_schedules", "homework", "agent_token_usage",
		"syllabi", "courses", "users",
	)

	studentID := insertIntgUser(ctx, t, pool)
	courseID := insertIntgCourse(ctx, t, pool, studentID)
	hwID := insertIntgHomework(ctx, t, pool, courseID, 0.5)
	subID := insertIntgSubmission(ctx, t, pool, hwID, studentID)

	// Insert the grade as the server (grading pipeline).
	serverConn := acquireServerConn(ctx, t, pool)
	defer serverConn.Release()
	serverCtx := auth.ContextWithConn(ctx, serverConn)
	repo := NewRepository(pool)

	inserted, err := repo.Insert(serverCtx, GradeRow{
		SubmissionID: subID,
		HomeworkID:   hwID,
		StudentID:    studentID,
		CourseID:     courseID,
		RawScore:     95,
		FinalScore:   95,
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Read back using the owning student's RLS-scoped connection.
	studentConn := acquireStudentConn(ctx, t, pool, studentID)
	defer studentConn.Release()
	studentCtx := auth.ContextWithConn(ctx, studentConn)

	got, err := repo.GetBySubmission(studentCtx, subID)
	if err != nil {
		t.Fatalf("GetBySubmission as owning student: %v", err)
	}
	if got.ID != inserted.ID {
		t.Errorf("grade ID mismatch: want %s, got %s", inserted.ID, got.ID)
	}
	if got.FinalScore != 95 {
		t.Errorf("FinalScore: want 95, got %.2f", got.FinalScore)
	}
}
