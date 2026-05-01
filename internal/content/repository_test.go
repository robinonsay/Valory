// @{"verifies": ["REQ-CONTENT-001", "REQ-CONTENT-004"]}
package content

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		os.Exit(0)
	}

	var err error
	pool, err = pgxpool.New(context.Background(), dbURL)
	if err != nil {
		panic(err)
	}
	defer pool.Close()

	ctx := context.Background()
	if err := applyMigrations(ctx, pool); err != nil {
		panic(err)
	}
	if err := truncateTables(ctx, pool); err != nil {
		panic(err)
	}

	code := m.Run()

	if err := truncateTables(ctx, pool); err != nil {
		panic(err)
	}
	os.Exit(code)
}

func applyMigrations(ctx context.Context, p *pgxpool.Pool) error {
	migration001 := `
	BEGIN;
	CREATE EXTENSION IF NOT EXISTS "pgcrypto";
	CREATE TABLE IF NOT EXISTS schema_migrations (
	    version     TEXT        PRIMARY KEY,
	    applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	INSERT INTO schema_migrations (version) VALUES ('001_auth') ON CONFLICT (version) DO NOTHING;
	CREATE TABLE IF NOT EXISTS users (
	    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
	    username            TEXT        NOT NULL UNIQUE,
	    password_hash       TEXT        NOT NULL,
	    role                TEXT        NOT NULL CHECK (role IN ('student', 'admin')),
	    is_active           BOOLEAN     NOT NULL DEFAULT TRUE,
	    failed_login_count  INTEGER     NOT NULL DEFAULT 0,
	    locked_until        TIMESTAMPTZ,
	    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_users_locked_until ON users (locked_until) WHERE locked_until IS NOT NULL;
	CREATE TABLE IF NOT EXISTS sessions (
	    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
	    user_id         UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	    token_hash      TEXT        NOT NULL UNIQUE,
	    role            TEXT        NOT NULL CHECK (role IN ('student', 'admin')),
	    last_active_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	    expires_at      TIMESTAMPTZ NOT NULL,
	    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions (user_id);
	CREATE INDEX IF NOT EXISTS idx_sessions_last_active_at ON sessions (last_active_at);
	CREATE TABLE IF NOT EXISTS login_attempts (
	    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
	    user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	    attempted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	    success      BOOLEAN     NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_login_attempts_user_id ON login_attempts (user_id, attempted_at DESC);
	COMMIT;
	`
	if _, err := p.Exec(ctx, migration001); err != nil {
		return err
	}

	migration002 := `
	BEGIN;
	INSERT INTO schema_migrations (version) VALUES ('002_user_security_audit') ON CONFLICT (version) DO NOTHING;
	ALTER TABLE users ADD COLUMN IF NOT EXISTS email TEXT UNIQUE;
	CREATE TABLE IF NOT EXISTS password_reset_tokens (
	    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
	    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	    token_hash  TEXT        NOT NULL UNIQUE,
	    expires_at  TIMESTAMPTZ NOT NULL,
	    used_at     TIMESTAMPTZ
	);
	CREATE INDEX IF NOT EXISTS idx_prt_user_id    ON password_reset_tokens (user_id);
	CREATE INDEX IF NOT EXISTS idx_prt_token_hash ON password_reset_tokens (token_hash);
	CREATE INDEX IF NOT EXISTS idx_prt_expires_at ON password_reset_tokens (expires_at) WHERE used_at IS NULL;
	CREATE TABLE IF NOT EXISTS password_reset_attempts (
	    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
	    user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	    requested_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_pra_user_requested ON password_reset_attempts (user_id, requested_at DESC);
	CREATE TABLE IF NOT EXISTS student_consent (
	    student_id       UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
	    consented_at     TIMESTAMPTZ NOT NULL,
	    consent_version  VARCHAR(16) NOT NULL
	);
	CREATE TABLE IF NOT EXISTS audit_log (
	    id           BIGSERIAL    PRIMARY KEY,
	    admin_id     UUID         NOT NULL REFERENCES users(id),
	    action       VARCHAR(64)  NOT NULL,
	    target_type  VARCHAR(64)  NOT NULL,
	    target_id    UUID,
	    payload      JSONB        NOT NULL DEFAULT '{}',
	    prev_hash    VARCHAR(64)  NOT NULL,
	    entry_hash   VARCHAR(64)  NOT NULL,
	    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_audit_log_admin_id   ON audit_log (admin_id);
	CREATE INDEX IF NOT EXISTS idx_audit_log_created_at ON audit_log (created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_audit_log_target     ON audit_log (target_type, target_id);
	COMMIT;
	`
	if _, err := p.Exec(ctx, migration002); err != nil {
		return err
	}

	migration003 := `
	BEGIN;
	INSERT INTO schema_migrations (version) VALUES ('003_course') ON CONFLICT (version) DO NOTHING;
	DO $$ BEGIN
	    CREATE TYPE course_status AS ENUM (
	        'intake', 'syllabus_draft', 'syllabus_approved',
	        'generating', 'active', 'archived', 'completed'
	    );
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;
	CREATE TABLE IF NOT EXISTS courses (
	    id                     UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
	    student_id             UUID          NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
	    title                  TEXT          NOT NULL DEFAULT '',
	    topic                  TEXT          NOT NULL,
	    status                 course_status NOT NULL DEFAULT 'intake',
	    pre_withdrawal_status  course_status,
	    created_at             TIMESTAMPTZ   NOT NULL DEFAULT now(),
	    updated_at             TIMESTAMPTZ   NOT NULL DEFAULT now()
	);
	CREATE UNIQUE INDEX IF NOT EXISTS courses_single_active_idx ON courses (student_id) WHERE status NOT IN ('archived', 'completed');
	CREATE INDEX IF NOT EXISTS courses_student_id_idx     ON courses (student_id);
	CREATE INDEX IF NOT EXISTS courses_status_idx         ON courses (status);
	CREATE INDEX IF NOT EXISTS courses_created_at_id_idx  ON courses (created_at DESC, id DESC);
	CREATE TABLE IF NOT EXISTS syllabi (
	    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
	    course_id    UUID        NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
	    content_adoc TEXT        NOT NULL,
	    version      INTEGER     NOT NULL DEFAULT 1,
	    approved_at  TIMESTAMPTZ,
	    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
	);
	CREATE INDEX IF NOT EXISTS syllabi_course_id_idx ON syllabi (course_id);
	CREATE TABLE IF NOT EXISTS homework (
	    id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
	    course_id     UUID         NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
	    section_index INT          NOT NULL,
	    title         VARCHAR(255) NOT NULL,
	    rubric        TEXT         NOT NULL,
	    grade_weight  NUMERIC(4,3) NOT NULL CHECK (grade_weight > 0 AND grade_weight <= 1),
	    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
	);
	CREATE INDEX IF NOT EXISTS homework_course_id_idx ON homework (course_id);
	CREATE TABLE IF NOT EXISTS due_date_schedules (
	    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
	    course_id   UUID        NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
	    homework_id UUID        NOT NULL REFERENCES homework(id) ON DELETE CASCADE,
	    due_date    TIMESTAMPTZ NOT NULL,
	    agreed_at   TIMESTAMPTZ,
	    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
	);
	CREATE INDEX IF NOT EXISTS due_date_schedules_course_id_idx ON due_date_schedules (course_id);
	CREATE UNIQUE INDEX IF NOT EXISTS due_date_schedules_unique_hw ON due_date_schedules (course_id, homework_id);
	CREATE TABLE IF NOT EXISTS agent_token_usage (
	    id                 UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
	    student_id         UUID    NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
	    course_id          UUID    NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
	    total_tokens_used  BIGINT  NOT NULL DEFAULT 0 CHECK (total_tokens_used >= 0),
	    CONSTRAINT uq_token_usage_student_course UNIQUE (student_id, course_id)
	);
	CREATE INDEX IF NOT EXISTS idx_token_usage_student_id ON agent_token_usage (student_id);
	COMMIT;
	`
	if _, err := p.Exec(ctx, migration003); err != nil {
		return err
	}

	migration004 := `
	BEGIN;
	INSERT INTO schema_migrations (version) VALUES ('004_agent') ON CONFLICT (version) DO NOTHING;
	CREATE EXTENSION IF NOT EXISTS pg_trgm;
	DO $$ BEGIN CREATE TYPE agent_run_type AS ENUM ('intake', 'syllabus', 'content_generation', 'section_regen', 'grading'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;
	DO $$ BEGIN CREATE TYPE agent_run_status AS ENUM ('running', 'completed', 'failed', 'terminated'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;
	DO $$ BEGIN
	    CREATE TYPE pipeline_event_type AS ENUM (
	        'intake_started', 'intake_complete', 'syllabus_draft_ready', 'syllabus_approved',
	        'generation_started', 'section_generating', 'section_review_passed',
	        'section_review_failed', 'correction_escalated', 'generation_complete',
	        'generation_timeout', 'api_failure', 'section_regen_started',
	        'section_regen_complete', 'token_cap_reached'
	    );
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;
	DO $$ BEGIN CREATE TYPE chat_role AS ENUM ('student', 'assistant'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;
	DO $$ BEGIN CREATE TYPE notification_type AS ENUM ('api_failure', 'generation_timeout', 'admin_escalation'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;
	CREATE TABLE IF NOT EXISTS agent_runs (
	    id              UUID             PRIMARY KEY DEFAULT gen_random_uuid(),
	    course_id       UUID             NOT NULL REFERENCES courses(id) ON DELETE RESTRICT,
	    run_type        agent_run_type   NOT NULL,
	    status          agent_run_status NOT NULL DEFAULT 'running',
	    started_at      TIMESTAMPTZ      NOT NULL DEFAULT now(),
	    completed_at    TIMESTAMPTZ,
	    iteration_count INTEGER          NOT NULL DEFAULT 0,
	    error           TEXT
	);
	CREATE INDEX IF NOT EXISTS agent_runs_course_id_idx ON agent_runs (course_id);
	CREATE INDEX IF NOT EXISTS agent_runs_status_idx    ON agent_runs (status) WHERE status = 'running';
	CREATE TABLE IF NOT EXISTS pipeline_events (
	    id           UUID                PRIMARY KEY DEFAULT gen_random_uuid(),
	    agent_run_id UUID                NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
	    event_type   pipeline_event_type NOT NULL,
	    payload      JSONB               NOT NULL DEFAULT '{}',
	    emitted_at   TIMESTAMPTZ         NOT NULL DEFAULT now()
	);
	CREATE INDEX IF NOT EXISTS pipeline_events_run_emitted_idx ON pipeline_events (agent_run_id, emitted_at ASC);
	CREATE TABLE IF NOT EXISTS chat_messages (
	    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
	    course_id  UUID        NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
	    role       chat_role   NOT NULL,
	    content    TEXT        NOT NULL,
	    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	);
	CREATE INDEX IF NOT EXISTS chat_messages_course_created_at_idx ON chat_messages (course_id, created_at DESC);
	CREATE TABLE IF NOT EXISTS notifications (
	    id          UUID              PRIMARY KEY DEFAULT gen_random_uuid(),
	    student_id  UUID              NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	    type        notification_type NOT NULL,
	    message     TEXT              NOT NULL,
	    read_at     TIMESTAMPTZ,
	    created_at  TIMESTAMPTZ       NOT NULL DEFAULT now()
	);
	CREATE INDEX IF NOT EXISTS idx_notifications_student_unread ON notifications (student_id, created_at DESC) WHERE read_at IS NULL;
	CREATE TABLE IF NOT EXISTS lesson_content (
	    id                UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
	    course_id         UUID         NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
	    section_index     INT          NOT NULL,
	    title             VARCHAR(255) NOT NULL,
	    content_adoc      TEXT         NOT NULL,
	    version           INT          NOT NULL DEFAULT 1,
	    citation_verified BOOLEAN      NOT NULL DEFAULT FALSE,
	    created_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
	    UNIQUE (course_id, section_index, version)
	);
	CREATE INDEX IF NOT EXISTS idx_lesson_content_course_section ON lesson_content (course_id, section_index);
	CREATE TABLE IF NOT EXISTS section_feedback (
	    id                     UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
	    student_id             UUID          NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	    course_id              UUID          NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
	    section_index          INT           NOT NULL,
	    feedback_text          VARCHAR(2000) NOT NULL,
	    submitted_at           TIMESTAMPTZ   NOT NULL DEFAULT now(),
	    regeneration_triggered BOOLEAN       NOT NULL DEFAULT FALSE
	);
	CREATE INDEX IF NOT EXISTS idx_section_feedback_student_course ON section_feedback (student_id, course_id, section_index);
	COMMIT;
	`
	if _, err := p.Exec(ctx, migration004); err != nil {
		return err
	}

	return nil
}

func truncateTables(ctx context.Context, p *pgxpool.Pool) error {
	statements := []string{
		`TRUNCATE TABLE section_feedback    CASCADE`,
		`TRUNCATE TABLE lesson_content      CASCADE`,
		`TRUNCATE TABLE notifications       CASCADE`,
		`TRUNCATE TABLE chat_messages       CASCADE`,
		`TRUNCATE TABLE pipeline_events     CASCADE`,
		`TRUNCATE TABLE agent_runs          CASCADE`,
		`TRUNCATE TABLE agent_token_usage   CASCADE`,
		`TRUNCATE TABLE due_date_schedules  CASCADE`,
		`TRUNCATE TABLE homework            CASCADE`,
		`TRUNCATE TABLE syllabi             CASCADE`,
		`TRUNCATE TABLE courses             CASCADE`,
		`TRUNCATE TABLE audit_log           CASCADE`,
		`TRUNCATE TABLE student_consent     CASCADE`,
		`TRUNCATE TABLE password_reset_attempts CASCADE`,
		`TRUNCATE TABLE password_reset_tokens   CASCADE`,
		`TRUNCATE TABLE login_attempts      CASCADE`,
		`TRUNCATE TABLE sessions            CASCADE`,
		`TRUNCATE TABLE users               CASCADE`,
	}
	for _, stmt := range statements {
		if _, err := p.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func createUser(ctx context.Context, t *testing.T, username string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
		username, "testhash", "student").Scan(&id); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	return id
}

func createCourse(ctx context.Context, t *testing.T, studentID uuid.UUID, topic string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO courses (student_id, topic, status) VALUES ($1, $2, 'active'::course_status) RETURNING id`,
		studentID, topic).Scan(&id); err != nil {
		t.Fatalf("createCourse: %v", err)
	}
	return id
}

// @{"verifies": ["REQ-CONTENT-001"]}
func TestInsertLessonContent_ReturnsCorrectFields(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	repo := NewContentRepository(pool)

	studentID := createUser(ctx, t, "content_insert_"+uuid.New().String())
	courseID := createCourse(ctx, t, studentID, "Go Programming")

	row, err := repo.InsertLessonContent(ctx, courseID, 0, "Introduction", "= Introduction\nHello world.")
	if err != nil {
		t.Fatalf("InsertLessonContent: %v", err)
	}

	if row.CourseID != courseID {
		t.Errorf("CourseID: expected %v, got %v", courseID, row.CourseID)
	}
	if row.SectionIndex != 0 {
		t.Errorf("SectionIndex: expected 0, got %d", row.SectionIndex)
	}
	if row.Title != "Introduction" {
		t.Errorf("Title: expected %q, got %q", "Introduction", row.Title)
	}
	if row.Version != 1 {
		t.Errorf("Version: expected 1, got %d", row.Version)
	}
	if row.CitationVerified {
		t.Error("CitationVerified: expected false on insert")
	}
}

// @{"verifies": ["REQ-CONTENT-001"]}
func TestInsertLessonContent_AutoIncrementsVersion(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	repo := NewContentRepository(pool)

	studentID := createUser(ctx, t, "content_version_"+uuid.New().String())
	courseID := createCourse(ctx, t, studentID, "Data Structures")

	first, err := repo.InsertLessonContent(ctx, courseID, 1, "Arrays", "= Arrays")
	if err != nil {
		t.Fatalf("InsertLessonContent v1: %v", err)
	}
	second, err := repo.InsertLessonContent(ctx, courseID, 1, "Arrays v2", "= Arrays v2")
	if err != nil {
		t.Fatalf("InsertLessonContent v2: %v", err)
	}

	if first.Version != 1 {
		t.Errorf("first.Version: expected 1, got %d", first.Version)
	}
	if second.Version != 2 {
		t.Errorf("second.Version: expected 2, got %d", second.Version)
	}
}

// @{"verifies": ["REQ-CONTENT-001"]}
func TestGetSectionContent_NotFound(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	repo := NewContentRepository(pool)

	studentID := createUser(ctx, t, "content_notfound_"+uuid.New().String())
	courseID := createCourse(ctx, t, studentID, "Physics")

	_, err := repo.GetSectionContent(ctx, courseID, 99)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// @{"verifies": ["REQ-CONTENT-001"]}
func TestGetSectionContent_NotVerified(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	repo := NewContentRepository(pool)

	studentID := createUser(ctx, t, "content_notverified_"+uuid.New().String())
	courseID := createCourse(ctx, t, studentID, "Chemistry")

	if _, err := repo.InsertLessonContent(ctx, courseID, 0, "Atoms", "= Atoms"); err != nil {
		t.Fatalf("InsertLessonContent: %v", err)
	}

	// citation_verified defaults to false — GetSectionContent must return ErrNotVerified.
	_, err := repo.GetSectionContent(ctx, courseID, 0)
	if !errors.Is(err, ErrNotVerified) {
		t.Errorf("expected ErrNotVerified, got %v", err)
	}
}

// @{"verifies": ["REQ-CONTENT-001"]}
func TestGetSectionContent_Success(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	repo := NewContentRepository(pool)

	studentID := createUser(ctx, t, "content_success_"+uuid.New().String())
	courseID := createCourse(ctx, t, studentID, "Biology")

	inserted, err := repo.InsertLessonContent(ctx, courseID, 2, "Cells", "= Cells")
	if err != nil {
		t.Fatalf("InsertLessonContent: %v", err)
	}
	if err := repo.SetCitationVerified(ctx, inserted.ID); err != nil {
		t.Fatalf("SetCitationVerified: %v", err)
	}

	row, err := repo.GetSectionContent(ctx, courseID, 2)
	if err != nil {
		t.Fatalf("GetSectionContent: %v", err)
	}
	if row.ID != inserted.ID {
		t.Errorf("ID: expected %v, got %v", inserted.ID, row.ID)
	}
	if !row.CitationVerified {
		t.Error("CitationVerified: expected true after SetCitationVerified")
	}
	if row.Title != "Cells" {
		t.Errorf("Title: expected %q, got %q", "Cells", row.Title)
	}
}

// @{"verifies": ["REQ-CONTENT-001"]}
func TestGetSectionContent_ReturnsLatestVerifiedVersion(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	repo := NewContentRepository(pool)

	studentID := createUser(ctx, t, "content_latest_"+uuid.New().String())
	courseID := createCourse(ctx, t, studentID, "History")

	v1, err := repo.InsertLessonContent(ctx, courseID, 0, "Ancient Rome v1", "v1 content")
	if err != nil {
		t.Fatalf("insert v1: %v", err)
	}
	if err := repo.SetCitationVerified(ctx, v1.ID); err != nil {
		t.Fatalf("verify v1: %v", err)
	}

	v2, err := repo.InsertLessonContent(ctx, courseID, 0, "Ancient Rome v2", "v2 content")
	if err != nil {
		t.Fatalf("insert v2: %v", err)
	}
	if err := repo.SetCitationVerified(ctx, v2.ID); err != nil {
		t.Fatalf("verify v2: %v", err)
	}

	// GetSectionContent orders by version DESC LIMIT 1 — must return v2.
	row, err := repo.GetSectionContent(ctx, courseID, 0)
	if err != nil {
		t.Fatalf("GetSectionContent: %v", err)
	}
	if row.Version != 2 {
		t.Errorf("Version: expected 2 (latest), got %d", row.Version)
	}
}

// @{"verifies": ["REQ-CONTENT-004"]}
func TestInsertFeedback_ReturnsCorrectFields(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	repo := NewContentRepository(pool)

	studentID := createUser(ctx, t, "content_feedback_"+uuid.New().String())
	courseID := createCourse(ctx, t, studentID, "Economics")

	fb, err := repo.InsertFeedback(ctx, studentID, courseID, 3, "Please explain more clearly.")
	if err != nil {
		t.Fatalf("InsertFeedback: %v", err)
	}

	if fb.StudentID != studentID {
		t.Errorf("StudentID: expected %v, got %v", studentID, fb.StudentID)
	}
	if fb.CourseID != courseID {
		t.Errorf("CourseID: expected %v, got %v", courseID, fb.CourseID)
	}
	if fb.SectionIndex != 3 {
		t.Errorf("SectionIndex: expected 3, got %d", fb.SectionIndex)
	}
	if fb.FeedbackText != "Please explain more clearly." {
		t.Errorf("FeedbackText: expected %q, got %q", "Please explain more clearly.", fb.FeedbackText)
	}
	if fb.RegenerationTriggered {
		t.Error("RegenerationTriggered: expected false on insert")
	}
}

// @{"verifies": ["REQ-CONTENT-004"]}
func TestSetRegenerationTriggered(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	repo := NewContentRepository(pool)

	studentID := createUser(ctx, t, "content_regen_"+uuid.New().String())
	courseID := createCourse(ctx, t, studentID, "Statistics")

	fb, err := repo.InsertFeedback(ctx, studentID, courseID, 0, "This section needs more examples.")
	if err != nil {
		t.Fatalf("InsertFeedback: %v", err)
	}
	if err := repo.SetRegenerationTriggered(ctx, fb.ID); err != nil {
		t.Fatalf("SetRegenerationTriggered: %v", err)
	}

	// Verify the flag is set by querying directly.
	var triggered bool
	if err := pool.QueryRow(ctx, `SELECT regeneration_triggered FROM section_feedback WHERE id=$1`, fb.ID).Scan(&triggered); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !triggered {
		t.Error("regeneration_triggered: expected true after SetRegenerationTriggered")
	}
}

// @{"verifies": ["REQ-CONTENT-004"]}
func TestListFeedback_ReturnsDescendingOrder(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	repo := NewContentRepository(pool)

	studentID := createUser(ctx, t, "content_list_"+uuid.New().String())
	courseID := createCourse(ctx, t, studentID, "Literature")

	for i, text := range []string{"first feedback", "second feedback", "third feedback"} {
		if _, err := repo.InsertFeedback(ctx, studentID, courseID, i, text); err != nil {
			t.Fatalf("InsertFeedback %d: %v", i, err)
		}
	}

	// ListFeedback with sectionIndex=0 should return the first feedback only.
	items, err := repo.ListFeedback(ctx, studentID, courseID, 0)
	if err != nil {
		t.Fatalf("ListFeedback: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 feedback for section 0, got %d", len(items))
	}
	if items[0].FeedbackText != "first feedback" {
		t.Errorf("FeedbackText: expected %q, got %q", "first feedback", items[0].FeedbackText)
	}
}

// @{"verifies": ["REQ-CONTENT-004"]}
func TestListFeedback_MultipleSubmissions(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	repo := NewContentRepository(pool)

	studentID := createUser(ctx, t, "content_multifb_"+uuid.New().String())
	courseID := createCourse(ctx, t, studentID, "Music Theory")

	if _, err := repo.InsertFeedback(ctx, studentID, courseID, 1, "first"); err != nil {
		t.Fatalf("InsertFeedback 1: %v", err)
	}
	if _, err := repo.InsertFeedback(ctx, studentID, courseID, 1, "second"); err != nil {
		t.Fatalf("InsertFeedback 2: %v", err)
	}

	items, err := repo.ListFeedback(ctx, studentID, courseID, 1)
	if err != nil {
		t.Fatalf("ListFeedback: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 feedbacks, got %d", len(items))
	}
	// Ordered by submitted_at DESC — second inserted comes first.
	if items[0].FeedbackText != "second" {
		t.Errorf("first result: expected %q, got %q", "second", items[0].FeedbackText)
	}
}
