package submission

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testPool is package-level so TestMain can share it with all test functions.
var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		// testPool stays nil; each DB-dependent test guards with t.Skip.
		// Non-DB tests (e.g. handler stubs) still run.
		os.Exit(m.Run())
	}

	var err error
	testPool, err = pgxpool.New(context.Background(), dbURL)
	if err != nil {
		panic(err)
	}
	defer testPool.Close()

	ctx := context.Background()
	if err := applySubmissionMigrations(ctx, testPool); err != nil {
		panic(err)
	}

	if err := truncateSubmissionTables(ctx, testPool); err != nil {
		panic(err)
	}

	code := m.Run()

	if err := truncateSubmissionTables(ctx, testPool); err != nil {
		panic(err)
	}

	os.Exit(code)
}

// applySubmissionMigrations sets up the minimal schema required by the submission
// repository tests. It mirrors the approach used in internal/course/repository_test.go.
func applySubmissionMigrations(ctx context.Context, p *pgxpool.Pool) error {
	statements := []string{
		// Foundation tables
		`CREATE EXTENSION IF NOT EXISTS "pgcrypto"`,

		`CREATE TABLE IF NOT EXISTS schema_migrations (
		    version     TEXT        PRIMARY KEY,
		    applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS users (
		    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
		    username            TEXT        NOT NULL UNIQUE,
		    password_hash       TEXT        NOT NULL,
		    role                TEXT        NOT NULL CHECK (role IN ('student', 'admin')),
		    is_active           BOOLEAN     NOT NULL DEFAULT TRUE,
		    failed_login_count  INTEGER     NOT NULL DEFAULT 0,
		    locked_until        TIMESTAMPTZ,
		    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,

		// course_status enum and courses table (homework FK target)
		`DO $$ BEGIN
		    CREATE TYPE course_status AS ENUM (
		        'intake','syllabus_draft','syllabus_approved',
		        'generating','active','archived','completed'
		    );
		EXCEPTION WHEN duplicate_object THEN NULL;
		END $$`,

		`CREATE TABLE IF NOT EXISTS courses (
		    id                     UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
		    student_id             UUID          NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
		    title                  TEXT          NOT NULL DEFAULT '',
		    topic                  TEXT          NOT NULL,
		    status                 course_status NOT NULL DEFAULT 'intake',
		    pre_withdrawal_status  course_status,
		    created_at             TIMESTAMPTZ   NOT NULL DEFAULT now(),
		    updated_at             TIMESTAMPTZ   NOT NULL DEFAULT now()
		)`,

		`CREATE TABLE IF NOT EXISTS homework (
		    id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
		    course_id     UUID         NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
		    section_index INT          NOT NULL,
		    title         VARCHAR(255) NOT NULL,
		    rubric        TEXT         NOT NULL,
		    grade_weight  NUMERIC(4,3) NOT NULL CHECK (grade_weight > 0 AND grade_weight <= 1),
		    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
		)`,

		// submission_format and grading_status enums
		`DO $$ BEGIN
		    CREATE TYPE submission_format AS ENUM ('latex', 'markdown', 'asciidoc');
		EXCEPTION WHEN duplicate_object THEN NULL;
		END $$`,

		`DO $$ BEGIN
		    CREATE TYPE grading_status AS ENUM ('pending', 'graded', 'failed');
		EXCEPTION WHEN duplicate_object THEN NULL;
		END $$`,

		`CREATE TABLE IF NOT EXISTS submissions (
		    id               UUID              PRIMARY KEY DEFAULT gen_random_uuid(),
		    homework_id      UUID              NOT NULL REFERENCES homework(id) ON DELETE CASCADE,
		    student_id       UUID              NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
		    format           submission_format NOT NULL,
		    file_path        TEXT              NOT NULL,
		    file_size_bytes  BIGINT            NOT NULL CHECK (file_size_bytes > 0),
		    submitted_at     TIMESTAMPTZ       NOT NULL DEFAULT now(),
		    raw_score        NUMERIC(5,2),
		    grading_status   grading_status    NOT NULL DEFAULT 'pending',
		    CONSTRAINT uq_submission_hw_student UNIQUE (homework_id, student_id)
		)`,
	}

	for _, stmt := range statements {
		if _, err := p.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func truncateSubmissionTables(ctx context.Context, p *pgxpool.Pool) error {
	tables := []string{
		"submissions",
		"homework",
		"courses",
		"users",
	}
	for _, tbl := range tables {
		if _, err := p.Exec(ctx, "TRUNCATE TABLE "+tbl+" CASCADE"); err != nil {
			return err
		}
	}
	return nil
}

// createTestUser inserts a student user and returns its ID.
func createTestUser(ctx context.Context, t *testing.T, username string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := testPool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
		username, "testhash", "student",
	).Scan(&id)
	if err != nil {
		t.Fatalf("createTestUser: %v", err)
	}
	return id
}

// createTestCourse inserts a course for the given student and returns its ID.
func createTestCourse(ctx context.Context, t *testing.T, studentID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := testPool.QueryRow(ctx,
		`INSERT INTO courses (student_id, topic) VALUES ($1, $2) RETURNING id`,
		studentID, "Test Topic",
	).Scan(&id)
	if err != nil {
		t.Fatalf("createTestCourse: %v", err)
	}
	return id
}

// createTestHomework inserts a homework row for the given course and returns its ID.
func createTestHomework(ctx context.Context, t *testing.T, courseID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := testPool.QueryRow(ctx,
		`INSERT INTO homework (course_id, section_index, title, rubric, grade_weight)
		 VALUES ($1, 1, 'HW1', 'Grade it', 0.5) RETURNING id`,
		courseID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("createTestHomework: %v", err)
	}
	return id
}

// @{"verifies": ["REQ-SUBMISSION-001"]}
func TestInsert_Success(t *testing.T) {
	if testPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(testPool)

	studentID := createTestUser(ctx, t, "student_insert_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID)
	homeworkID := createTestHomework(ctx, t, courseID)

	row, err := repo.Insert(ctx, homeworkID, studentID, "latex", "/uploads/test.tex", 1024)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	if row.HomeworkID != homeworkID {
		t.Errorf("HomeworkID = %v, want %v", row.HomeworkID, homeworkID)
	}
	if row.StudentID != studentID {
		t.Errorf("StudentID = %v, want %v", row.StudentID, studentID)
	}
	if row.Format != "latex" {
		t.Errorf("Format = %q, want %q", row.Format, "latex")
	}
	if row.FileSizeBytes != 1024 {
		t.Errorf("FileSizeBytes = %d, want 1024", row.FileSizeBytes)
	}
	if row.GradingStatus != "pending" {
		t.Errorf("GradingStatus = %q, want %q", row.GradingStatus, "pending")
	}
	if row.RawScore != nil {
		t.Errorf("RawScore should be nil before grading")
	}
}

// @{"verifies": ["REQ-SUBMISSION-001"]}
func TestInsert_DuplicateReturnsErrAlreadySubmitted(t *testing.T) {
	if testPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(testPool)

	studentID := createTestUser(ctx, t, "student_dup_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID)
	homeworkID := createTestHomework(ctx, t, courseID)

	_, err := repo.Insert(ctx, homeworkID, studentID, "markdown", "/uploads/a.md", 512)
	if err != nil {
		t.Fatalf("first Insert failed: %v", err)
	}

	_, err = repo.Insert(ctx, homeworkID, studentID, "markdown", "/uploads/b.md", 512)
	if !errors.Is(err, ErrAlreadySubmitted) {
		t.Errorf("second Insert error = %v, want ErrAlreadySubmitted", err)
	}
}

// @{"verifies": ["REQ-SUBMISSION-001"]}
func TestGetLatestByHomework_ReturnsInsertedRecord(t *testing.T) {
	if testPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(testPool)

	studentID := createTestUser(ctx, t, "student_get_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID)
	homeworkID := createTestHomework(ctx, t, courseID)

	inserted, err := repo.Insert(ctx, homeworkID, studentID, "asciidoc", "/uploads/doc.adoc", 2048)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	got, err := repo.GetLatestByHomework(ctx, homeworkID, studentID)
	if err != nil {
		t.Fatalf("GetLatestByHomework failed: %v", err)
	}

	if got.ID != inserted.ID {
		t.Errorf("ID = %v, want %v", got.ID, inserted.ID)
	}
	if got.Format != "asciidoc" {
		t.Errorf("Format = %q, want %q", got.Format, "asciidoc")
	}
}

// @{"verifies": ["REQ-SUBMISSION-001"]}
func TestGetLatestByHomework_NotFound(t *testing.T) {
	if testPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(testPool)

	_, err := repo.GetLatestByHomework(ctx, uuid.New(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

// @{"verifies": ["REQ-SUBMISSION-001"]}
func TestListPendingGrading_ReturnsPendingSubmissions(t *testing.T) {
	if testPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(testPool)

	studentID := createTestUser(ctx, t, "student_pending_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID)
	homeworkID := createTestHomework(ctx, t, courseID)

	inserted, err := repo.Insert(ctx, homeworkID, studentID, "latex", "/uploads/hw.tex", 999)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	pending, err := repo.ListPendingGrading(ctx)
	if err != nil {
		t.Fatalf("ListPendingGrading failed: %v", err)
	}

	found := false
	for _, p := range pending {
		if p.ID == inserted.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("inserted submission %v not found in pending list", inserted.ID)
	}
}

// @{"verifies": ["REQ-SUBMISSION-001"]}
func TestSetRawScore_TransitionsToGraded(t *testing.T) {
	if testPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(testPool)

	studentID := createTestUser(ctx, t, "student_score_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID)
	homeworkID := createTestHomework(ctx, t, courseID)

	inserted, err := repo.Insert(ctx, homeworkID, studentID, "markdown", "/uploads/essay.md", 4096)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	if err := repo.SetRawScore(ctx, inserted.ID, 88.5); err != nil {
		t.Fatalf("SetRawScore failed: %v", err)
	}

	got, err := repo.GetLatestByHomework(ctx, homeworkID, studentID)
	if err != nil {
		t.Fatalf("GetLatestByHomework failed: %v", err)
	}

	if got.GradingStatus != "graded" {
		t.Errorf("GradingStatus = %q, want %q", got.GradingStatus, "graded")
	}
	if got.RawScore == nil {
		t.Fatal("RawScore is nil, want 88.5")
	}
	if *got.RawScore != 88.5 {
		t.Errorf("RawScore = %v, want 88.5", *got.RawScore)
	}
}

// @{"verifies": ["REQ-SUBMISSION-001"]}
func TestMarkGradingFailed_TransitionsToFailed(t *testing.T) {
	if testPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(testPool)

	studentID := createTestUser(ctx, t, "student_fail_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID)
	homeworkID := createTestHomework(ctx, t, courseID)

	inserted, err := repo.Insert(ctx, homeworkID, studentID, "asciidoc", "/uploads/report.adoc", 1500)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	if err := repo.MarkGradingFailed(ctx, inserted.ID); err != nil {
		t.Fatalf("MarkGradingFailed failed: %v", err)
	}

	got, err := repo.GetLatestByHomework(ctx, homeworkID, studentID)
	if err != nil {
		t.Fatalf("GetLatestByHomework failed: %v", err)
	}

	if got.GradingStatus != "failed" {
		t.Errorf("GradingStatus = %q, want %q", got.GradingStatus, "failed")
	}
}
