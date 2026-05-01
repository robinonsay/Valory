//go:build testing

// repository_test.go — integration tests for the grade Repository.
// Tests are skipped when TEST_DATABASE_URL is not set so the CI baseline
// (no real DB) does not fail. When the env var is provided a live PostgreSQL
// instance is expected with the test schema applied.
package grade

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL != "" {
		var err error
		testPool, err = pgxpool.New(context.Background(), dbURL)
		if err != nil {
			panic(err)
		}
		defer testPool.Close()

		ctx := context.Background()
		if err := applyMigrations(ctx, testPool); err != nil {
			panic(err)
		}

		if err := truncateTables(ctx, testPool); err != nil {
			panic(err)
		}
	}

	code := m.Run()

	if testPool != nil {
		if err := truncateTables(context.Background(), testPool); err != nil {
			panic(err)
		}
	}

	os.Exit(code)
}

func applyMigrations(ctx context.Context, p *pgxpool.Pool) error {
	// Each migration is idempotent (IF NOT EXISTS + ON CONFLICT DO NOTHING),
	// so we can safely run all of them even if the schema already exists.
	migrationsSQL := []string{
		migrationAuth,
		migrationUserSecurity,
		migrationCourse,
		migrationAgent,
		migrationServerRLS,
		migrationBadge,
		migrationSubmission,
		migrationGrade,
	}
	for _, sql := range migrationsSQL {
		if _, err := p.Exec(ctx, sql); err != nil {
			return err
		}
	}
	return nil
}

func truncateTables(ctx context.Context, p *pgxpool.Pool) error {
	// Truncate in FK-safe order (child tables first).
	_, err := p.Exec(ctx, `
		TRUNCATE grades, submissions, student_badges, badges,
		         due_date_schedules, homework, agent_token_usage,
		         syllabi, courses, users
		CASCADE`)
	return err
}

// skipIfNoDB skips the test when TEST_DATABASE_URL is not set.
// Repository integration tests require a live PostgreSQL instance.
func skipIfNoDB(t *testing.T) {
	t.Helper()
	if testPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
}

// insertTestUser creates a minimal user row and returns its ID.
//
// @{"verifies": ["REQ-GRADE-001"]}
func insertTestUser(ctx context.Context, t *testing.T, p *pgxpool.Pool, role string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := p.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role)
		 VALUES ($1, 'hash', $2)
		 RETURNING id`,
		"user_"+uuid.New().String(), role,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

// insertTestCourse inserts a course and returns its ID.
//
// @{"verifies": ["REQ-GRADE-001"]}
func insertTestCourse(ctx context.Context, t *testing.T, p *pgxpool.Pool, studentID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := p.QueryRow(ctx,
		`INSERT INTO courses (student_id, topic, status) VALUES ($1, 'Math', 'active') RETURNING id`,
		studentID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert course: %v", err)
	}
	return id
}

// insertTestHomework inserts a homework row and returns its ID.
//
// @{"verifies": ["REQ-GRADE-001"]}
func insertTestHomework(ctx context.Context, t *testing.T, p *pgxpool.Pool, courseID uuid.UUID, gradeWeight float64) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := p.QueryRow(ctx,
		`INSERT INTO homework (course_id, section_index, title, rubric, grade_weight)
		 VALUES ($1, 1, 'HW1', 'rubric text', $2)
		 RETURNING id`,
		courseID, gradeWeight,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert homework: %v", err)
	}
	return id
}

// insertTestSubmission inserts a submission row and returns its ID.
//
// @{"verifies": ["REQ-GRADE-001"]}
func insertTestSubmission(ctx context.Context, t *testing.T, p *pgxpool.Pool, homeworkID, studentID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := p.QueryRow(ctx,
		`INSERT INTO submissions (homework_id, student_id, format, file_path, file_size_bytes)
		 VALUES ($1, $2, 'markdown', '/tmp/test.md', 100)
		 RETURNING id`,
		homeworkID, studentID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert submission: %v", err)
	}
	return id
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-GRADE-001", "REQ-GRADE-002", "REQ-GRADE-004", "REQ-GRADE-005"]}
func TestRepository_Insert_RoundTrip(t *testing.T) {
	skipIfNoDB(t)
	ctx := context.Background()
	studentID := insertTestUser(ctx, t, testPool, "student")
	courseID := insertTestCourse(ctx, t, testPool, studentID)
	hwID := insertTestHomework(ctx, t, testPool, courseID, 0.5)
	subID := insertTestSubmission(ctx, t, testPool, hwID, studentID)

	repo := NewRepository(testPool)
	g := GradeRow{
		SubmissionID:       subID,
		HomeworkID:         hwID,
		StudentID:          studentID,
		CourseID:           courseID,
		RawScore:           80,
		LateDays:           2,
		LatePenaltyRate:    0.05,
		LatePenaltyAmount:  8,
		BadgeWaiverApplied: false,
		BadgeImprovement:   0,
		FinalScore:         72,
	}

	inserted, err := repo.Insert(ctx, g)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if inserted.ID == uuid.Nil {
		t.Error("expected non-nil ID after insert")
	}
	if inserted.FinalScore != 72 {
		t.Errorf("expected final_score=72, got %.2f", inserted.FinalScore)
	}
}

// @{"verifies": ["REQ-GRADE-001"]}
func TestRepository_GetBySubmission_NotFound(t *testing.T) {
	skipIfNoDB(t)
	ctx := context.Background()
	repo := NewRepository(testPool)

	_, err := repo.GetBySubmission(ctx, uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// @{"verifies": ["REQ-GRADE-001"]}
func TestRepository_GetBySubmission_Found(t *testing.T) {
	skipIfNoDB(t)
	ctx := context.Background()
	studentID := insertTestUser(ctx, t, testPool, "student")
	courseID := insertTestCourse(ctx, t, testPool, studentID)
	hwID := insertTestHomework(ctx, t, testPool, courseID, 0.5)
	subID := insertTestSubmission(ctx, t, testPool, hwID, studentID)

	repo := NewRepository(testPool)
	g := GradeRow{
		SubmissionID:       subID,
		HomeworkID:         hwID,
		StudentID:          studentID,
		CourseID:           courseID,
		RawScore:           90,
		LateDays:           0,
		LatePenaltyRate:    0.05,
		LatePenaltyAmount:  0,
		BadgeWaiverApplied: false,
		BadgeImprovement:   0,
		FinalScore:         90,
	}
	if _, err := repo.Insert(ctx, g); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.GetBySubmission(ctx, subID)
	if err != nil {
		t.Fatalf("GetBySubmission: %v", err)
	}
	if got.FinalScore != 90 {
		t.Errorf("expected final_score=90, got %.2f", got.FinalScore)
	}
}

// @{"verifies": ["REQ-GRADE-003"]}
func TestRepository_GetCourseGradeSummary_WeightedAverage(t *testing.T) {
	skipIfNoDB(t)
	ctx := context.Background()
	studentID := insertTestUser(ctx, t, testPool, "student")
	courseID := insertTestCourse(ctx, t, testPool, studentID)

	// Two homework assignments with different weights and scores.
	hw1ID := insertTestHomework(ctx, t, testPool, courseID, 0.6)
	hw2ID := insertTestHomework(ctx, t, testPool, courseID, 0.4)
	sub1ID := insertTestSubmission(ctx, t, testPool, hw1ID, studentID)
	sub2ID := insertTestSubmission(ctx, t, testPool, hw2ID, studentID)

	repo := NewRepository(testPool)
	// Grade hw1: 80, grade hw2: 70.
	// Weighted = (80*0.6 + 70*0.4) / (0.6+0.4) = (48+28)/1 = 76
	for _, g := range []GradeRow{
		{SubmissionID: sub1ID, HomeworkID: hw1ID, StudentID: studentID, CourseID: courseID,
			RawScore: 80, LatePenaltyRate: 0.05, FinalScore: 80},
		{SubmissionID: sub2ID, HomeworkID: hw2ID, StudentID: studentID, CourseID: courseID,
			RawScore: 70, LatePenaltyRate: 0.05, FinalScore: 70},
	} {
		if _, err := repo.Insert(ctx, g); err != nil {
			t.Fatalf("Insert grade: %v", err)
		}
	}

	summary, err := repo.GetCourseGradeSummary(ctx, courseID, studentID)
	if err != nil {
		t.Fatalf("GetCourseGradeSummary: %v", err)
	}
	// Allow 0.01 tolerance for floating-point representation.
	if summary.WeightedScore < 75.99 || summary.WeightedScore > 76.01 {
		t.Errorf("expected weighted_score≈76, got %.4f", summary.WeightedScore)
	}
}

// @{"verifies": ["REQ-GRADE-001"]}
func TestRepository_Insert_Idempotent(t *testing.T) {
	skipIfNoDB(t)
	// A second Insert for the same submission_id should upsert rather than error.
	ctx := context.Background()
	studentID := insertTestUser(ctx, t, testPool, "student")
	courseID := insertTestCourse(ctx, t, testPool, studentID)
	hwID := insertTestHomework(ctx, t, testPool, courseID, 0.5)
	subID := insertTestSubmission(ctx, t, testPool, hwID, studentID)

	repo := NewRepository(testPool)
	base := GradeRow{
		SubmissionID: subID, HomeworkID: hwID, StudentID: studentID, CourseID: courseID,
		RawScore: 70, LatePenaltyRate: 0.05, FinalScore: 70,
	}
	if _, err := repo.Insert(ctx, base); err != nil {
		t.Fatalf("first Insert: %v", err)
	}

	// Update the score on the second insert (simulates re-grading).
	base.FinalScore = 85
	base.RawScore = 85
	updated, err := repo.Insert(ctx, base)
	if err != nil {
		t.Fatalf("second Insert (upsert): %v", err)
	}
	if updated.FinalScore != 85 {
		t.Errorf("expected upserted final_score=85, got %.2f", updated.FinalScore)
	}
}

// ---------------------------------------------------------------------------
// Inline migration SQL (copied from files to keep tests self-contained)
// ---------------------------------------------------------------------------

var migrationAuth = `
BEGIN;
CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
INSERT INTO schema_migrations (version) VALUES ('001_auth') ON CONFLICT (version) DO NOTHING;
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('student','admin')),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    failed_login_count INTEGER NOT NULL DEFAULT 0,
    locked_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    role TEXT NOT NULL CHECK (role IN ('student','admin')),
    last_active_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS login_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    attempted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    success BOOLEAN NOT NULL
);
COMMIT;`

var migrationUserSecurity = `
BEGIN;
INSERT INTO schema_migrations (version) VALUES ('002_user_security_audit') ON CONFLICT (version) DO NOTHING;
ALTER TABLE users ADD COLUMN IF NOT EXISTS email TEXT UNIQUE;
CREATE TABLE IF NOT EXISTS password_reset_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ
);
CREATE TABLE IF NOT EXISTS password_reset_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    requested_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS student_consent (
    student_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    consented_at TIMESTAMPTZ NOT NULL,
    consent_version VARCHAR(16) NOT NULL
);
CREATE TABLE IF NOT EXISTS audit_log (
    id BIGSERIAL PRIMARY KEY,
    admin_id UUID NOT NULL REFERENCES users(id),
    action VARCHAR(64) NOT NULL,
    target_type VARCHAR(64) NOT NULL,
    target_id UUID,
    payload JSONB NOT NULL DEFAULT '{}',
    prev_hash VARCHAR(64) NOT NULL,
    entry_hash VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
COMMIT;`

var migrationCourse = `
BEGIN;
INSERT INTO schema_migrations (version) VALUES ('003_course') ON CONFLICT (version) DO NOTHING;
DO $$ BEGIN CREATE TYPE course_status AS ENUM ('intake','syllabus_draft','syllabus_approved','generating','active','archived','completed');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
CREATE TABLE IF NOT EXISTS courses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    student_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    title TEXT NOT NULL DEFAULT '',
    topic TEXT NOT NULL,
    status course_status NOT NULL DEFAULT 'intake',
    pre_withdrawal_status course_status,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS courses_single_active_idx ON courses (student_id) WHERE status NOT IN ('archived','completed');
CREATE INDEX IF NOT EXISTS courses_student_id_idx ON courses (student_id);
CREATE TABLE IF NOT EXISTS syllabi (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    content_adoc TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    approved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS homework (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    section_index INT NOT NULL,
    title VARCHAR(255) NOT NULL,
    rubric TEXT NOT NULL,
    grade_weight NUMERIC(4,3) NOT NULL CHECK (grade_weight > 0 AND grade_weight <= 1),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS homework_course_id_idx ON homework (course_id);
CREATE TABLE IF NOT EXISTS due_date_schedules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    homework_id UUID NOT NULL REFERENCES homework(id) ON DELETE CASCADE,
    due_date TIMESTAMPTZ NOT NULL,
    agreed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS due_date_schedules_unique_hw ON due_date_schedules (course_id, homework_id);
CREATE TABLE IF NOT EXISTS agent_token_usage (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    student_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    total_tokens_used BIGINT NOT NULL DEFAULT 0 CHECK (total_tokens_used >= 0),
    CONSTRAINT uq_token_usage_student_course UNIQUE (student_id, course_id)
);
COMMIT;`

var migrationAgent = `
BEGIN;
INSERT INTO schema_migrations (version) VALUES ('004_agent') ON CONFLICT (version) DO NOTHING;
CREATE EXTENSION IF NOT EXISTS pg_trgm;
DO $$ BEGIN CREATE TYPE agent_run_type AS ENUM ('intake','syllabus','content_generation','section_regen','grading');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN CREATE TYPE agent_run_status AS ENUM ('running','completed','failed','terminated');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN CREATE TYPE pipeline_event_type AS ENUM ('intake_started','intake_complete','syllabus_draft_ready','syllabus_approved','generation_started','section_generating','section_review_passed','section_review_failed','correction_escalated','generation_complete','generation_timeout','api_failure','section_regen_started','section_regen_complete','token_cap_reached');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN CREATE TYPE chat_role AS ENUM ('student','assistant');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN CREATE TYPE notification_type AS ENUM ('api_failure','generation_timeout','admin_escalation');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
CREATE TABLE IF NOT EXISTS agent_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE RESTRICT,
    run_type agent_run_type NOT NULL,
    status agent_run_status NOT NULL DEFAULT 'running',
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    iteration_count INTEGER NOT NULL DEFAULT 0,
    error TEXT
);
CREATE TABLE IF NOT EXISTS pipeline_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_run_id UUID NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    event_type pipeline_event_type NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}',
    emitted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS chat_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    role chat_role NOT NULL,
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS notifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    student_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type notification_type NOT NULL,
    message TEXT NOT NULL,
    read_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS lesson_content (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    section_index INT NOT NULL,
    title VARCHAR(255) NOT NULL,
    content_adoc TEXT NOT NULL,
    version INT NOT NULL DEFAULT 1,
    citation_verified BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (course_id, section_index, version)
);
CREATE TABLE IF NOT EXISTS section_feedback (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    student_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    section_index INT NOT NULL,
    feedback_text VARCHAR(2000) NOT NULL,
    submitted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    regeneration_triggered BOOLEAN NOT NULL DEFAULT FALSE
);
COMMIT;`

var migrationServerRLS = `
BEGIN;
INSERT INTO schema_migrations (version) VALUES ('005_server_rls') ON CONFLICT (version) DO NOTHING;
COMMIT;`

var migrationBadge = `
BEGIN;
INSERT INTO schema_migrations (version) VALUES ('006_badge') ON CONFLICT (version) DO NOTHING;
DO $$ BEGIN CREATE TYPE milestone_type AS ENUM ('first_submission','submitted_on_time','perfect_score','all_sections_complete');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN CREATE TYPE reward_type AS ENUM ('grade_improvement','late_penalty_waiver');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
CREATE TABLE IF NOT EXISTS badges (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL UNIQUE,
    description TEXT NOT NULL,
    milestone milestone_type NOT NULL,
    reward reward_type NOT NULL,
    reward_value NUMERIC(5,2) NOT NULL DEFAULT 0 CHECK (reward_value >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS student_badges (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    student_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    badge_id UUID NOT NULL REFERENCES badges(id) ON DELETE CASCADE,
    awarded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    redeemed_for_submission_id UUID,
    CONSTRAINT uq_student_badge UNIQUE (student_id, badge_id)
);
COMMIT;`

var migrationSubmission = `
BEGIN;
INSERT INTO schema_migrations (version) VALUES ('007_submission') ON CONFLICT (version) DO NOTHING;
DO $$ BEGIN CREATE TYPE submission_format AS ENUM ('latex','markdown','asciidoc');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN CREATE TYPE grading_status AS ENUM ('pending','graded','failed');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
CREATE TABLE IF NOT EXISTS submissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    homework_id UUID NOT NULL REFERENCES homework(id) ON DELETE CASCADE,
    student_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    format submission_format NOT NULL,
    file_path TEXT NOT NULL,
    file_size_bytes BIGINT NOT NULL CHECK (file_size_bytes > 0),
    submitted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    raw_score NUMERIC(5,2),
    grading_status grading_status NOT NULL DEFAULT 'pending',
    CONSTRAINT uq_submission_hw_student UNIQUE (homework_id, student_id)
);
COMMIT;`

var migrationGrade = `
BEGIN;
INSERT INTO schema_migrations (version) VALUES ('008_grade') ON CONFLICT (version) DO NOTHING;
CREATE TABLE IF NOT EXISTS grades (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    submission_id UUID NOT NULL REFERENCES submissions(id) ON DELETE CASCADE,
    homework_id UUID NOT NULL REFERENCES homework(id) ON DELETE CASCADE,
    student_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    raw_score NUMERIC(5,2) NOT NULL CHECK (raw_score BETWEEN 0 AND 100),
    late_days INT NOT NULL DEFAULT 0 CHECK (late_days >= 0),
    late_penalty_rate NUMERIC(5,4) NOT NULL DEFAULT 0,
    late_penalty_amount NUMERIC(5,2) NOT NULL DEFAULT 0,
    badge_waiver_applied BOOLEAN NOT NULL DEFAULT FALSE,
    badge_improvement NUMERIC(5,2) NOT NULL DEFAULT 0,
    final_score NUMERIC(5,2) NOT NULL CHECK (final_score BETWEEN 0 AND 100),
    graded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_grade_submission UNIQUE (submission_id)
);
COMMIT;`

// Ensure the time package is used.
var _ = time.Now
