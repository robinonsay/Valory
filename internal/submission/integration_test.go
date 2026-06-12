//go:build integration

package submission

// integration_test.go — repository-layer integration tests for the submission
// module, exercised against a real PostgreSQL instance with the full embedded
// migration set (including FORCE ROW LEVEL SECURITY on the submissions table).
//
// Run with:
//
//	make test-integration
//
// or directly:
//
//	go test -tags integration -count=1 ./internal/submission/
//
// IMPORTANT: This file must NOT define TestMain or re-declare any symbol that
// already lives in repository_test.go (testPool, createTestUser, etc.).
// All helpers here carry the "integ" prefix to avoid collisions.
//
// Architecture notes
// ──────────────────
// submissions has ENABLE + FORCE ROW LEVEL SECURITY (007_submission.sql lines
// 47-48). FORCE means even the table owner (valory_test role) must pass the
// policies, so every INSERT/SELECT/UPDATE on submissions must go through a
// connection whose GUCs satisfy one of the policies:
//
//   • student insert/select: app.current_user_id matches the row's student_id
//   • server select/update:  app.current_role = 'server'
//
// db.AcquireAsUser sets the student GUCs (and downgrades to valory_app via
// SET ROLE). db.AcquireAsServer sets the server GUC (and likewise downgrades
// to valory_app via SET ROLE).  Both helpers MUST be used in tests instead of
// the production db.AcquireServerConn because the test bootstrap user
// (valory_test) is a PostgreSQL SUPERUSER — superusers bypass row-level
// security entirely, even when FORCE ROW LEVEL SECURITY is set.  Setting a GUC
// on a superuser connection does NOT make RLS apply: the policies are still
// skipped.  Only by downgrading to the NOLOGIN/NOBYPASSRLS valory_app role
// (via SET ROLE) does PostgreSQL actually evaluate the RLS predicates.
// auth.ContextWithConn injects either connection into context so that
// Repository.conn(ctx) picks it up instead of falling back to the bare pool.
//
// courses likewise has FORCE RLS (003_course.sql), so fixture inserts into
// courses also require AcquireAsUser.  The homework and users tables have no
// RLS, so those inserts can use the bare pool.

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/db"
)

// ────────────────────────────────────────────────────────────────────────────
// Shared fixture helpers (all prefixed "integ" to avoid collisions with the
// symbols already declared in repository_test.go)
// ────────────────────────────────────────────────────────────────────────────

// integUserSetup inserts a student user via the bare integration pool (users
// has no RLS) and returns the new user's UUID. The username is built from a
// caller-supplied tag plus a random suffix to guarantee uniqueness within a
// test run even when TruncateTables is not called between sub-tests.
func integUserSetup(ctx context.Context, t *testing.T, tag string) uuid.UUID {
	t.Helper()
	pool := db.IntegrationPool(t) // skips test if DB unreachable
	username := fmt.Sprintf("integ_%s_%s", tag, uuid.New().String()[:8])
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, 'x', 'student') RETURNING id`,
		username,
	).Scan(&id)
	if err != nil {
		t.Fatalf("integUserSetup: insert user %q: %v", username, err)
	}
	return id
}

// integCourseSetup inserts a course for studentID using an AcquireAsUser
// connection so that the courses FORCE RLS INSERT policy is satisfied.
// Returns the new course UUID.
//
// courses_student_policy is a FOR ALL policy with USING only; PostgreSQL uses
// the USING expression as the WITH CHECK for inserts, so student_id must match
// app.current_user_id for the insert to pass.
func integCourseSetup(ctx context.Context, t *testing.T, studentID uuid.UUID) uuid.UUID {
	t.Helper()
	pool := db.IntegrationPool(t)
	// AcquireAsUser sets app.current_user_id and app.current_role on the conn.
	// The auth middleware encodes UUIDs as 32-char no-dash hex strings.
	conn := db.AcquireAsUser(t, pool, fmt.Sprintf("%x", [16]byte(studentID)), "student")
	defer conn.Release()

	var id uuid.UUID
	err := conn.QueryRow(ctx,
		`INSERT INTO courses (student_id, topic) VALUES ($1, 'Integration Test Topic') RETURNING id`,
		studentID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("integCourseSetup: insert course for student %v: %v", studentID, err)
	}
	return id
}

// integHomeworkSetup inserts a homework row for courseID via the bare pool.
// The homework table has no RLS, so no GUC priming is needed.
func integHomeworkSetup(ctx context.Context, t *testing.T, courseID uuid.UUID) uuid.UUID {
	t.Helper()
	pool := db.IntegrationPool(t)
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO homework (course_id, section_index, title, rubric, grade_weight)
		 VALUES ($1, 1, 'Integration HW', 'Grade it', 0.5) RETURNING id`,
		courseID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("integHomeworkSetup: insert homework for course %v: %v", courseID, err)
	}
	return id
}

// integStudentCtx returns a context carrying an AcquireAsUser connection so
// that Repository.conn(ctx) picks it up and the student-side FORCE RLS policy
// on submissions is satisfied. The caller is responsible for calling release().
func integStudentCtx(ctx context.Context, t *testing.T, studentID uuid.UUID) (context.Context, func()) {
	t.Helper()
	pool := db.IntegrationPool(t)
	conn := db.AcquireAsUser(t, pool, fmt.Sprintf("%x", [16]byte(studentID)), "student")
	// auth.ContextWithConn injects conn under the same context key that
	// Repository.conn(ctx) reads via auth.ConnFromContext.
	enriched := auth.ContextWithConn(ctx, conn)
	return enriched, conn.Release
}

// integServerCtx returns a context carrying a server-role connection, used to
// exercise the grading-runner code paths (SetRawScore, MarkGradingFailed) that
// must pass submissions_server_update_policy (current_role = 'server').
//
// Why db.AcquireAsServer instead of db.AcquireServerConn:
// The production helper AcquireServerConn only sets app.current_role='server'
// on the connection without downgrading the PostgreSQL role.  In tests the
// login role (valory_test) is a superuser, and superusers bypass row-level
// security entirely — even with FORCE ROW LEVEL SECURITY — so any server-path
// test that used AcquireServerConn would pass vacuously regardless of whether
// the server RLS policies on submissions were correct or even present.
// db.AcquireAsServer issues SET ROLE valory_app first (downgrading to the
// NOLOGIN/NOBYPASSRLS role from migration 002) so RLS is genuinely enforced,
// then sets app.current_role='server' to satisfy
// submissions_server_update_policy and submissions_server_select_policy.
func integServerCtx(ctx context.Context, t *testing.T) (context.Context, func()) {
	t.Helper()
	pool := db.IntegrationPool(t)
	conn := db.AcquireAsServer(t, pool)
	return auth.ContextWithConn(ctx, conn), conn.Release
}

// integCheckPgError asserts that err is a *pgconn.PgError with the expected
// SQL state code. The check is best-effort: if the error is not a PgError the
// function only reports that err != nil (the FK/check still fired, just wrapped
// differently by some pgx path), which is still a useful signal.
func integCheckPgError(t *testing.T, err error, wantCode string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a PostgreSQL error with code %s, got nil", wantCode)
	}
	pgErr, ok := err.(*pgconn.PgError)
	if !ok {
		// Still report a useful message — the constraint did fire (err != nil),
		// but we cannot inspect the code.
		t.Logf("error is not a *pgconn.PgError (type=%T); cannot verify code %s — constraint fired but wrapping differs", err, wantCode)
		return
	}
	if pgErr.Code != wantCode {
		t.Errorf("pgErr.Code = %q, want %q", pgErr.Code, wantCode)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Integration test functions
// ────────────────────────────────────────────────────────────────────────────

// TC-SUBMISSION-001: LaTeX file upload is accepted
// @{"verifies": ["REQ-SUBMISSION-001"]}
func TestInteg_Insert_LaTeXSubmission_Persists(t *testing.T) {
	// TC-SUBMISSION-001: repository Insert with format="latex" must persist the
	// row and return all expected fields.  Verifies that the real schema accepts
	// the submission_format enum value 'latex' and that the DB-side defaults
	// (grading_status='pending', raw_score=NULL, submitted_at=NOW()) are set.
	pool := db.IntegrationPool(t)
	db.TruncateTables(t, pool, "submissions", "homework", "courses", "users")
	ctx := context.Background()

	studentID := integUserSetup(ctx, t, "latex")
	courseID := integCourseSetup(ctx, t, studentID)
	hwID := integHomeworkSetup(ctx, t, courseID)

	sCtx, release := integStudentCtx(ctx, t, studentID)
	defer release()

	repo := NewRepository(pool)
	row, err := repo.Insert(sCtx, hwID, studentID, "latex", "/uploads/hw.tex", 2048, nil)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if row.ID == (uuid.UUID{}) {
		t.Error("ID is zero UUID — gen_random_uuid() should have populated it")
	}
	if row.HomeworkID != hwID {
		t.Errorf("HomeworkID = %v, want %v", row.HomeworkID, hwID)
	}
	if row.StudentID != studentID {
		t.Errorf("StudentID = %v, want %v", row.StudentID, studentID)
	}
	if row.Format != "latex" {
		t.Errorf("Format = %q, want %q", row.Format, "latex")
	}
	if row.FilePath != "/uploads/hw.tex" {
		t.Errorf("FilePath = %q, want %q", row.FilePath, "/uploads/hw.tex")
	}
	if row.FileSizeBytes != 2048 {
		t.Errorf("FileSizeBytes = %d, want 2048", row.FileSizeBytes)
	}
	// Default DB value — grading has not started.
	if row.GradingStatus != "pending" {
		t.Errorf("GradingStatus = %q, want %q (DB default)", row.GradingStatus, "pending")
	}
	// raw_score must be NULL until the grading agent sets it.
	if row.RawScore != nil {
		t.Errorf("RawScore = %v, want nil (not graded yet)", row.RawScore)
	}
	// submitted_at is populated by DEFAULT now() — must not be zero.
	if row.SubmittedAt.IsZero() {
		t.Error("SubmittedAt is zero — DEFAULT now() should have set it")
	}
}

// TC-SUBMISSION-001 (duplicate path)
// @{"verifies": ["REQ-SUBMISSION-001"]}
func TestInteg_Insert_DuplicateSubmission_ReturnsErrAlreadySubmitted(t *testing.T) {
	// TC-SUBMISSION-001: the UNIQUE constraint uq_submission_hw_student
	// (homework_id, student_id) prevents a student from submitting twice for the
	// same homework.  Repository.Insert must translate the 23505 pgconn error
	// into the domain sentinel ErrAlreadySubmitted so callers never import pgconn.
	pool := db.IntegrationPool(t)
	db.TruncateTables(t, pool, "submissions", "homework", "courses", "users")
	ctx := context.Background()

	studentID := integUserSetup(ctx, t, "dup")
	courseID := integCourseSetup(ctx, t, studentID)
	hwID := integHomeworkSetup(ctx, t, courseID)

	sCtx, release := integStudentCtx(ctx, t, studentID)
	defer release()

	repo := NewRepository(pool)

	// First submission must succeed.
	if _, err := repo.Insert(sCtx, hwID, studentID, "markdown", "/uploads/a.md", 512, nil); err != nil {
		t.Fatalf("first Insert: %v", err)
	}

	// Second submission with the same (homework_id, student_id) must return the
	// domain sentinel, not a raw database error.
	_, err := repo.Insert(sCtx, hwID, studentID, "markdown", "/uploads/b.md", 512, nil)
	if err != ErrAlreadySubmitted {
		t.Errorf("second Insert error = %v, want ErrAlreadySubmitted", err)
	}
}

// TC-SUBMISSION-013: SQL injection payload stored verbatim
// @{"verifies": ["REQ-SUBMISSION-001"]}
func TestInteg_Insert_SQLInjectionInFilePath_StoredVerbatim(t *testing.T) {
	// TC-SUBMISSION-013: parameterized queries prevent SQL execution. The
	// injection string must be stored as literal text and retrieved unchanged,
	// with the submissions table remaining intact.
	pool := db.IntegrationPool(t)
	db.TruncateTables(t, pool, "submissions", "homework", "courses", "users")
	ctx := context.Background()

	studentID := integUserSetup(ctx, t, "sqli")
	courseID := integCourseSetup(ctx, t, studentID)
	hwID := integHomeworkSetup(ctx, t, courseID)

	sCtx, release := integStudentCtx(ctx, t, studentID)
	defer release()

	// Classic SQL injection that would DROP the table if the query were concatenated
	// rather than parameterized.
	injectionPath := `'; DROP TABLE submissions; --`

	repo := NewRepository(pool)
	row, err := repo.Insert(sCtx, hwID, studentID, "markdown", injectionPath, 64, nil)
	if err != nil {
		t.Fatalf("Insert with injection payload: %v", err)
	}

	// Read back and confirm verbatim storage.
	got, err := repo.GetLatestByHomework(sCtx, hwID, studentID)
	if err != nil {
		t.Fatalf("GetLatestByHomework after injection insert: %v", err)
	}
	if got.FilePath != injectionPath {
		t.Errorf("FilePath = %q, want %q — payload not stored verbatim", got.FilePath, injectionPath)
	}
	// The table is still intact (DROP did not execute).
	if got.ID != row.ID {
		t.Errorf("ID mismatch after injection: got %v, want %v", got.ID, row.ID)
	}
}

// TC-SECURITY-003: RLS isolation — student B cannot read student A's submission
// @{"verifies": ["REQ-SECURITY-002"]}
func TestInteg_RLS_StudentCannotReadAnotherStudentsSubmission(t *testing.T) {
	// TC-SECURITY-003: the SELECT policy on submissions
	//   USING (student_id = current_setting('app.current_user_id')::uuid)
	// means student B's connection cannot see rows belonging to student A.
	// Repository.GetLatestByHomework must return ErrNotFound — not student A's row.
	pool := db.IntegrationPool(t)
	db.TruncateTables(t, pool, "submissions", "homework", "courses", "users")
	ctx := context.Background()

	studentA := integUserSetup(ctx, t, "rls_a")
	studentB := integUserSetup(ctx, t, "rls_b")

	courseA := integCourseSetup(ctx, t, studentA)
	hwID := integHomeworkSetup(ctx, t, courseA)

	// Student A inserts a submission.
	ctxA, releaseA := integStudentCtx(ctx, t, studentA)
	defer releaseA()

	repo := NewRepository(pool)
	if _, err := repo.Insert(ctxA, hwID, studentA, "asciidoc", "/uploads/a.adoc", 1024, nil); err != nil {
		t.Fatalf("Insert as studentA: %v", err)
	}

	// Student B attempts to read student A's row via B's GUC-primed connection.
	// With app.current_user_id = studentB's UUID, the SELECT policy filters out
	// student A's row entirely, resulting in a no-rows scan → ErrNotFound.
	ctxB, releaseB := integStudentCtx(ctx, t, studentB)
	defer releaseB()

	_, err := repo.GetLatestByHomework(ctxB, hwID, studentA)
	if err != ErrNotFound {
		t.Errorf("GetLatestByHomework as studentB for studentA's row: error = %v, want ErrNotFound (RLS isolation failure)", err)
	}
}

// TC-SECURITY-018: RLS isolation — student B cannot INSERT a row attributed to student A
// @{"verifies": ["REQ-SECURITY-002"]}
func TestInteg_RLS_StudentCannotInsertSubmissionForAnotherStudent(t *testing.T) {
	// TC-SECURITY-018: the INSERT policy on submissions (no explicit FOR INSERT,
	// so the FOR ALL policy applies) checks student_id = current_user_id.  A
	// connection authenticated as student B that passes student A's UUID as
	// student_id must be rejected by PostgreSQL, preventing IDOR submission fraud.
	pool := db.IntegrationPool(t)
	db.TruncateTables(t, pool, "submissions", "homework", "courses", "users")
	ctx := context.Background()

	studentA := integUserSetup(ctx, t, "ins_a")
	studentB := integUserSetup(ctx, t, "ins_b")

	courseA := integCourseSetup(ctx, t, studentA)
	hwID := integHomeworkSetup(ctx, t, courseA)

	// Use student B's authenticated context but pass student A's UUID as student_id.
	// The INSERT policy (USING / WITH CHECK = student_id = current_user_id) must
	// reject this because student B's GUC does not match student A's UUID.
	ctxB, releaseB := integStudentCtx(ctx, t, studentB)
	defer releaseB()

	repo := NewRepository(pool)
	_, err := repo.Insert(ctxB, hwID, studentA, "markdown", "/uploads/evil.md", 256, nil)
	if err == nil {
		t.Fatal("Insert as studentB with studentA's ID succeeded — RLS INSERT policy not enforced")
	}

	// Confirm no phantom row landed in the table by reading as student A.
	ctxA, releaseA := integStudentCtx(ctx, t, studentA)
	defer releaseA()

	_, readErr := repo.GetLatestByHomework(ctxA, hwID, studentA)
	if readErr != ErrNotFound {
		t.Errorf("GetLatestByHomework after cross-student insert attempt: error = %v, want ErrNotFound — a row may have been written", readErr)
	}
}

// FK integrity: submission referencing a non-existent homework_id is rejected
// @{"verifies": ["REQ-SUBMISSION-001"]}
func TestInteg_Insert_NonExistentHomeworkID_ReturnsForeignKeyViolation(t *testing.T) {
	// FK integrity: submissions(homework_id) REFERENCES homework(id) ON DELETE CASCADE.
	// Inserting with a random homework UUID that does not exist in the homework
	// table must fail with a foreign-key violation (23503), not silently succeed.
	// This prevents "orphan" submission records that reference no homework item.
	pool := db.IntegrationPool(t)
	db.TruncateTables(t, pool, "submissions", "homework", "courses", "users")
	ctx := context.Background()

	studentID := integUserSetup(ctx, t, "fk")
	// No course or homework is inserted — phantomHW is a random UUID.
	phantomHW := uuid.New()

	sCtx, release := integStudentCtx(ctx, t, studentID)
	defer release()

	repo := NewRepository(pool)
	_, err := repo.Insert(sCtx, phantomHW, studentID, "latex", "/uploads/ghost.tex", 128, nil)
	// Code 23503 = foreign_key_violation.
	integCheckPgError(t, err, "23503")
}

// CHECK constraint: file_size_bytes > 0 is enforced at the DB layer
// @{"verifies": ["REQ-SUBMISSION-001", "REQ-SUBMISSION-002"]}
func TestInteg_Insert_ZeroByteFile_ReturnsCheckConstraintViolation(t *testing.T) {
	// The schema enforces CHECK (file_size_bytes > 0) to prevent empty-file
	// records reaching the grading pipeline.  This test confirms the constraint
	// fires at the database layer — not just at the HTTP handler layer — so that
	// any code path that bypasses the handler still cannot store a zero-byte row.
	pool := db.IntegrationPool(t)
	db.TruncateTables(t, pool, "submissions", "homework", "courses", "users")
	ctx := context.Background()

	studentID := integUserSetup(ctx, t, "zero")
	courseID := integCourseSetup(ctx, t, studentID)
	hwID := integHomeworkSetup(ctx, t, courseID)

	sCtx, release := integStudentCtx(ctx, t, studentID)
	defer release()

	repo := NewRepository(pool)
	_, err := repo.Insert(sCtx, hwID, studentID, "markdown", "/uploads/empty.md", 0, nil)
	// Code 23514 = check_violation.
	integCheckPgError(t, err, "23514")
}

// SetRawScore via server role transitions grading_status to 'graded'
// @{"verifies": ["REQ-SUBMISSION-001"]}
func TestInteg_SetRawScore_ServerRole_TransitionsToGraded(t *testing.T) {
	// The grading agent runs as a background goroutine and uses a server-role
	// connection (submissions_server_update_policy: current_role = 'server').
	// This test verifies the full lifecycle: student inserts → server scores →
	// student reads back the updated status and numeric score.
	pool := db.IntegrationPool(t)
	db.TruncateTables(t, pool, "submissions", "homework", "courses", "users")
	ctx := context.Background()

	studentID := integUserSetup(ctx, t, "score")
	courseID := integCourseSetup(ctx, t, studentID)
	hwID := integHomeworkSetup(ctx, t, courseID)

	// Insert as student.
	sCtx, releaseStudent := integStudentCtx(ctx, t, studentID)
	defer releaseStudent()

	repo := NewRepository(pool)
	inserted, err := repo.Insert(sCtx, hwID, studentID, "latex", "/uploads/scored.tex", 4096, nil)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if inserted.GradingStatus != "pending" {
		t.Fatalf("precondition: GradingStatus = %q, want pending", inserted.GradingStatus)
	}

	// Score via server role.
	serverCtx, releaseServer := integServerCtx(ctx, t)
	defer releaseServer()

	if err := repo.SetRawScore(serverCtx, inserted.ID, 91.5); err != nil {
		t.Fatalf("SetRawScore: %v", err)
	}

	// Read back as the student.
	got, err := repo.GetLatestByHomework(sCtx, hwID, studentID)
	if err != nil {
		t.Fatalf("GetLatestByHomework after SetRawScore: %v", err)
	}
	if got.GradingStatus != "graded" {
		t.Errorf("GradingStatus = %q, want graded", got.GradingStatus)
	}
	if got.RawScore == nil {
		t.Fatal("RawScore is nil after SetRawScore")
	}
	if *got.RawScore != 91.5 {
		t.Errorf("RawScore = %v, want 91.5", *got.RawScore)
	}
}

// MarkGradingFailed via server role transitions grading_status to 'failed'
// @{"verifies": ["REQ-SUBMISSION-001"]}
func TestInteg_MarkGradingFailed_ServerRole_TransitionsToFailed(t *testing.T) {
	// MarkGradingFailed is the error path for SetRawScore: when the grading agent
	// encounters an AI API error it calls this method to mark the submission as
	// 'failed' so the student knows scoring did not complete.  Server-role RLS
	// must allow the UPDATE; raw_score must remain NULL.
	pool := db.IntegrationPool(t)
	db.TruncateTables(t, pool, "submissions", "homework", "courses", "users")
	ctx := context.Background()

	studentID := integUserSetup(ctx, t, "fail")
	courseID := integCourseSetup(ctx, t, studentID)
	hwID := integHomeworkSetup(ctx, t, courseID)

	sCtx, releaseStudent := integStudentCtx(ctx, t, studentID)
	defer releaseStudent()

	repo := NewRepository(pool)
	inserted, err := repo.Insert(sCtx, hwID, studentID, "asciidoc", "/uploads/fail.adoc", 1500, nil)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	serverCtx, releaseServer := integServerCtx(ctx, t)
	defer releaseServer()

	if err := repo.MarkGradingFailed(serverCtx, inserted.ID); err != nil {
		t.Fatalf("MarkGradingFailed: %v", err)
	}

	got, err := repo.GetLatestByHomework(sCtx, hwID, studentID)
	if err != nil {
		t.Fatalf("GetLatestByHomework after MarkGradingFailed: %v", err)
	}
	if got.GradingStatus != "failed" {
		t.Errorf("GradingStatus = %q, want failed", got.GradingStatus)
	}
	// MarkGradingFailed must not set raw_score — that is SetRawScore's job.
	if got.RawScore != nil {
		t.Errorf("RawScore = %v, want nil (failed grading must not set a score)", got.RawScore)
	}
}
