package agent

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		// No database configured — skip all integration tests gracefully.
		os.Exit(m.Run())
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

// applyMigrations runs all four schema migrations idempotently so the test
// database has the full schema regardless of its prior state.
func applyMigrations(ctx context.Context, p *pgxpool.Pool) error {
	migration001 := `
	BEGIN;

	CREATE EXTENSION IF NOT EXISTS "pgcrypto";

	CREATE TABLE IF NOT EXISTS schema_migrations (
	    version     TEXT        PRIMARY KEY,
	    applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	INSERT INTO schema_migrations (version) VALUES ('001_auth')
	    ON CONFLICT (version) DO NOTHING;

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

	CREATE INDEX IF NOT EXISTS idx_users_locked_until ON users (locked_until)
	    WHERE locked_until IS NOT NULL;

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

	INSERT INTO schema_migrations (version) VALUES ('002_user_security_audit')
	    ON CONFLICT (version) DO NOTHING;

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
	CREATE INDEX IF NOT EXISTS idx_prt_expires_at ON password_reset_tokens (expires_at)
	    WHERE used_at IS NULL;

	CREATE TABLE IF NOT EXISTS password_reset_attempts (
	    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
	    user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	    requested_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_pra_user_requested
	    ON password_reset_attempts (user_id, requested_at DESC);

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

	INSERT INTO schema_migrations (version) VALUES ('003_course')
	    ON CONFLICT (version) DO NOTHING;

	-- CREATE TYPE has no IF NOT EXISTS form; use the duplicate_object guard
	-- like the other enum types below.
	DO $$ BEGIN
	    CREATE TYPE course_status AS ENUM (
	        'intake',
	        'syllabus_draft',
	        'syllabus_approved',
	        'generating',
	        'active',
	        'archived',
	        'completed'
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

	CREATE UNIQUE INDEX IF NOT EXISTS courses_single_active_idx
	    ON courses (student_id)
	    WHERE status NOT IN ('archived', 'completed');

	CREATE INDEX IF NOT EXISTS courses_student_id_idx ON courses (student_id);
	CREATE INDEX IF NOT EXISTS courses_status_idx     ON courses (status);
	CREATE INDEX IF NOT EXISTS courses_created_at_id_idx ON courses (created_at DESC, id DESC);

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
	CREATE UNIQUE INDEX IF NOT EXISTS due_date_schedules_unique_hw
	    ON due_date_schedules (course_id, homework_id);

	CREATE TABLE IF NOT EXISTS agent_token_usage (
	    id                 UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
	    student_id         UUID    NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
	    course_id          UUID    NOT NULL REFERENCES courses(id)  ON DELETE CASCADE,
	    total_tokens_used  BIGINT  NOT NULL DEFAULT 0
	                               CHECK (total_tokens_used >= 0),

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

	INSERT INTO schema_migrations (version) VALUES ('004_agent')
	    ON CONFLICT (version) DO NOTHING;

	CREATE EXTENSION IF NOT EXISTS pg_trgm;

	DO $$ BEGIN
	    CREATE TYPE agent_run_type AS ENUM ('intake', 'syllabus', 'content_generation', 'section_regen', 'grading');
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

	DO $$ BEGIN
	    CREATE TYPE agent_run_status AS ENUM ('running', 'completed', 'failed', 'terminated');
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

	DO $$ BEGIN
	    CREATE TYPE pipeline_event_type AS ENUM (
	        'intake_started',
	        'intake_complete',
	        'syllabus_draft_ready',
	        'syllabus_approved',
	        'generation_started',
	        'section_generating',
	        'section_review_passed',
	        'section_review_failed',
	        'correction_escalated',
	        'generation_complete',
	        'generation_timeout',
	        'api_failure',
	        'section_regen_started',
	        'section_regen_complete',
	        'token_cap_reached'
	    );
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

	DO $$ BEGIN
	    CREATE TYPE chat_role AS ENUM ('student', 'assistant');
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

	DO $$ BEGIN
	    CREATE TYPE notification_type AS ENUM ('api_failure', 'generation_timeout', 'admin_escalation');
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

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

	CREATE INDEX IF NOT EXISTS pipeline_events_run_emitted_idx
	    ON pipeline_events (agent_run_id, emitted_at ASC);

	CREATE TABLE IF NOT EXISTS chat_messages (
	    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
	    course_id  UUID        NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
	    role       chat_role   NOT NULL,
	    content    TEXT        NOT NULL,
	    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	);

	CREATE INDEX IF NOT EXISTS chat_messages_course_created_at_idx
	    ON chat_messages (course_id, created_at DESC);

	CREATE TABLE IF NOT EXISTS notifications (
	    id          UUID              PRIMARY KEY DEFAULT gen_random_uuid(),
	    student_id  UUID              NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	    type        notification_type NOT NULL,
	    message     TEXT              NOT NULL,
	    read_at     TIMESTAMPTZ,
	    created_at  TIMESTAMPTZ       NOT NULL DEFAULT now()
	);

	CREATE INDEX IF NOT EXISTS idx_notifications_student_unread
	    ON notifications (student_id, created_at DESC)
	    WHERE read_at IS NULL;

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

	CREATE INDEX IF NOT EXISTS idx_lesson_content_course_section
	    ON lesson_content (course_id, section_index);

	CREATE TABLE IF NOT EXISTS section_feedback (
	    id                     UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
	    student_id             UUID          NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	    course_id              UUID          NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
	    section_index          INT           NOT NULL,
	    feedback_text          VARCHAR(2000) NOT NULL,
	    submitted_at           TIMESTAMPTZ   NOT NULL DEFAULT now(),
	    regeneration_triggered BOOLEAN       NOT NULL DEFAULT FALSE
	);

	CREATE INDEX IF NOT EXISTS idx_section_feedback_student_course
	    ON section_feedback (student_id, course_id, section_index);

	COMMIT;
	`
	if _, err := p.Exec(ctx, migration004); err != nil {
		return err
	}

	migration005 := `
	BEGIN;

	INSERT INTO schema_migrations (version) VALUES ('005_server_rls')
	    ON CONFLICT (version) DO NOTHING;

	DO $$ BEGIN
	    CREATE POLICY courses_server_select_policy ON courses
	        FOR SELECT
	        USING (current_setting('app.current_role', true) = 'server');
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

	DO $$ BEGIN
	    CREATE POLICY courses_server_update_policy ON courses
	        FOR UPDATE
	        USING (current_setting('app.current_role', true) = 'server');
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

	DO $$ BEGIN
	    CREATE POLICY lesson_content_server_select_policy ON lesson_content
	        FOR SELECT
	        USING (current_setting('app.current_role', true) = 'server');
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

	DO $$ BEGIN
	    CREATE POLICY lesson_content_server_update_policy ON lesson_content
	        FOR UPDATE
	        USING (current_setting('app.current_role', true) = 'server');
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

	DO $$ BEGIN
	    CREATE POLICY section_feedback_server_select_policy ON section_feedback
	        FOR SELECT
	        USING (current_setting('app.current_role', true) = 'server');
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

	DO $$ BEGIN
	    CREATE POLICY section_feedback_server_update_policy ON section_feedback
	        FOR UPDATE
	        USING (current_setting('app.current_role', true) = 'server');
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

	COMMIT;
	`
	if _, err := p.Exec(ctx, migration005); err != nil {
		return err
	}

	// ── Migration 022 additions (tree_generation) ────────────────────────────
	// ALTER TYPE … ADD VALUE must run OUTSIDE a transaction block (PG < 16
	// compatibility and migration 022 precedent). These are separate Exec calls
	// so the statements run under the simple-query protocol without a surrounding
	// BEGIN. IF NOT EXISTS makes each a no-op on re-run.
	migration022EnumValues := []string{
		`ALTER TYPE course_status    ADD VALUE IF NOT EXISTS 'awaiting_layer_approval' AFTER 'active'`,
		`ALTER TYPE course_status    ADD VALUE IF NOT EXISTS 'awaiting_regeneration'   AFTER 'awaiting_layer_approval'`,
		`ALTER TYPE agent_run_type   ADD VALUE IF NOT EXISTS 'tree_layer_generation'   AFTER 'grading'`,
		`ALTER TYPE agent_run_type   ADD VALUE IF NOT EXISTS 'node_generation'          AFTER 'tree_layer_generation'`,
	}
	for _, stmt := range migration022EnumValues {
		if _, err := p.Exec(ctx, stmt); err != nil {
			return err
		}
	}

	// Migration 022 DDL: add columns referenced by the agent package tests.
	// The full tree schema (course_nodes, course_drafts, draft_nodes, node_chats,
	// RLS policies, triggers) is omitted — only the columns the repository and
	// client tests touch are included. draft_id on agent_token_usage is added as
	// a plain UUID (no FK to course_drafts, which is not created here) because
	// the test only needs the column to exist for the partial-index conflict
	// predicate (WHERE draft_id IS NULL) used in client_test.go.
	migration022DDL := `
	BEGIN;

	INSERT INTO schema_migrations (version) VALUES ('022_tree_generation')
	    ON CONFLICT (version) DO NOTHING;

	ALTER TABLE courses
	    ADD COLUMN IF NOT EXISTS tree_mode     BOOLEAN        NOT NULL DEFAULT false,
	    ADD COLUMN IF NOT EXISTS current_layer TEXT;

	ALTER TABLE agent_runs
	    ALTER COLUMN course_id DROP NOT NULL,
	    ADD COLUMN IF NOT EXISTS draft_id UUID;

	DO $$ BEGIN
	    ALTER TABLE agent_runs
	        ADD CONSTRAINT agent_runs_context_check CHECK (
	            (course_id IS NOT NULL AND draft_id IS NULL)
	            OR
	            (course_id IS NULL     AND draft_id IS NOT NULL)
	        );
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

	ALTER TABLE agent_token_usage
	    ALTER COLUMN student_id DROP NOT NULL,
	    ALTER COLUMN course_id  DROP NOT NULL,
	    ADD COLUMN IF NOT EXISTS draft_id UUID;

	DO $$ BEGIN
	    ALTER TABLE agent_token_usage
	        DROP CONSTRAINT uq_token_usage_student_course;
	EXCEPTION WHEN undefined_object THEN NULL;
	END $$;

	DO $$ BEGIN
	    ALTER TABLE agent_token_usage
	        ADD CONSTRAINT agent_token_usage_context_check CHECK (
	            (student_id IS NOT NULL AND course_id IS NOT NULL AND draft_id IS NULL)
	            OR
	            (student_id IS NULL     AND course_id IS NULL     AND draft_id IS NOT NULL)
	        );
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

	CREATE UNIQUE INDEX IF NOT EXISTS agent_token_usage_student_course_idx
	    ON agent_token_usage (student_id, course_id)
	    WHERE draft_id IS NULL;

	CREATE UNIQUE INDEX IF NOT EXISTS agent_token_usage_draft_idx
	    ON agent_token_usage (draft_id)
	    WHERE draft_id IS NOT NULL;

	COMMIT;
	`
	if _, err := p.Exec(ctx, migration022DDL); err != nil {
		return err
	}

	// ── Migration 024 additions (generation_run_lifecycle) ───────────────────
	// ALTER TYPE … ADD VALUE must run outside a transaction block.
	migration024EnumValues := []string{
		`ALTER TYPE course_status    ADD VALUE IF NOT EXISTS 'generation_failed'`,
		`ALTER TYPE notification_type ADD VALUE IF NOT EXISTS 'generation_retrying'`,
		`ALTER TYPE notification_type ADD VALUE IF NOT EXISTS 'generation_failed'`,
		`ALTER TYPE notification_type ADD VALUE IF NOT EXISTS 'generation_recovery'`,
	}
	for _, stmt := range migration024EnumValues {
		if _, err := p.Exec(ctx, stmt); err != nil {
			return err
		}
	}

	// Migration 024 DDL: new columns, claim-guard index, eligibility index, and
	// the D22 widened courses_single_active_idx that excludes 'generation_failed'.
	migration024DDL := `
	BEGIN;

	INSERT INTO schema_migrations (version) VALUES ('024_generation_run_lifecycle')
	    ON CONFLICT (version) DO NOTHING;

	ALTER TABLE courses
	    ADD COLUMN IF NOT EXISTS generation_attempt_count INTEGER NOT NULL DEFAULT 0;

	ALTER TABLE courses
	    ADD COLUMN IF NOT EXISTS next_eligible_at TIMESTAMPTZ;

	CREATE UNIQUE INDEX IF NOT EXISTS agent_runs_one_running_per_course_type_idx
	    ON agent_runs (course_id, run_type)
	    WHERE status = 'running' AND course_id IS NOT NULL;

	CREATE INDEX IF NOT EXISTS courses_generation_eligible_idx
	    ON courses (status, generation_attempt_count, next_eligible_at)
	    WHERE status = 'syllabus_approved';

	-- D22: widen the active-course uniqueness guard to also exclude
	-- 'generation_failed', so a terminally-failed course does not block the
	-- student from creating a new one.
	DROP INDEX IF EXISTS courses_single_active_idx;
	CREATE UNIQUE INDEX IF NOT EXISTS courses_single_active_idx
	    ON courses (student_id)
	    WHERE status NOT IN ('archived', 'completed', 'generation_failed');

	COMMIT;
	`
	if _, err := p.Exec(ctx, migration024DDL); err != nil {
		return err
	}

	return nil
}

// truncateTables wipes all test data between test runs.  Tables are ordered to
// respect foreign-key constraints (children before parents).
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

// createTestUser inserts a minimal student user and returns its UUID.
func createTestUser(ctx context.Context, t *testing.T, username string) uuid.UUID {
	t.Helper()
	var userID uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
		username, "testhash", "student").
		Scan(&userID)
	if err != nil {
		t.Fatalf("createTestUser: %v", err)
	}
	return userID
}

// createTestCourse inserts a course for a student with the given status and
// returns its UUID.  It bypasses RLS by using the superuser pool connection.
func createTestCourse(ctx context.Context, t *testing.T, studentID uuid.UUID, topic, status string) uuid.UUID {
	t.Helper()
	var courseID uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO courses (student_id, topic, status) VALUES ($1, $2, $3::course_status) RETURNING id`,
		studentID, topic, status).
		Scan(&courseID)
	if err != nil {
		t.Fatalf("createTestCourse: %v", err)
	}
	return courseID
}

// @{"verifies": ["REQ-AGENT-006", "REQ-AGENT-007", "REQ-AGENT-013"]}
func TestCreateRun_ReturnsCorrectFields(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	studentID := createTestUser(ctx, t, "agent_create_run_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Intro to Algorithms", "intake")

	run, err := repo.CreateRun(ctx, courseID, "content_generation")
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}

	// Each field is checked independently so failures point to the exact
	// property that is wrong rather than a combined assertion.
	if run.CourseID != courseID {
		t.Errorf("CourseID: expected %v, got %v", courseID, run.CourseID)
	}
	if run.RunType != "content_generation" {
		t.Errorf("RunType: expected %q, got %q", "content_generation", run.RunType)
	}
	if run.Status != "running" {
		t.Errorf("Status: expected %q, got %q", "running", run.Status)
	}
	if run.IterationCount != 0 {
		t.Errorf("IterationCount: expected 0, got %d", run.IterationCount)
	}
	if run.CompletedAt != nil {
		t.Errorf("CompletedAt: expected nil, got %v", run.CompletedAt)
	}
}

// @{"verifies": ["REQ-AGENT-006", "REQ-AGENT-007", "REQ-AGENT-013"]}
func TestSetRunStatus_Terminal_SetsCompletedAt(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	studentID := createTestUser(ctx, t, "agent_setstatus_terminal_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Linear Algebra", "intake")

	run, err := repo.CreateRun(ctx, courseID, "content_generation")
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}

	if err := repo.SetRunStatus(ctx, run.ID, "completed", nil); err != nil {
		t.Fatalf("SetRunStatus failed: %v", err)
	}

	// Read back from the database to verify both fields are persisted correctly.
	var status string
	var completedAt *time.Time
	err = pool.QueryRow(ctx,
		`SELECT status, completed_at FROM agent_runs WHERE id = $1`, run.ID).
		Scan(&status, &completedAt)
	if err != nil {
		t.Fatalf("SELECT after SetRunStatus failed: %v", err)
	}

	if status != "completed" {
		t.Errorf("status: expected %q, got %q", "completed", status)
	}
	if completedAt == nil {
		t.Errorf("completed_at: expected non-nil timestamp for terminal status")
	}
}

// @{"verifies": ["REQ-AGENT-006", "REQ-AGENT-007"]}
func TestSetRunStatus_NonTerminal_CompletedAtStaysNull(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	// The only non-terminal status that SetRunStatus currently handles without
	// setting completed_at is any status other than 'completed', 'failed', or
	// 'terminated'.  We exercise the 'running' status, which represents an
	// intermediate state that may be used to reset or re-queue a run in the
	// future without closing it.
	studentID := createTestUser(ctx, t, "agent_setstatus_nonterminal_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Calculus", "intake")

	run, err := repo.CreateRun(ctx, courseID, "content_generation")
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}

	// 'running' is a non-terminal status — completed_at must remain NULL.
	if err := repo.SetRunStatus(ctx, run.ID, "running", nil); err != nil {
		t.Fatalf("SetRunStatus to 'running' failed: %v", err)
	}

	var completedAt *time.Time
	err = pool.QueryRow(ctx,
		`SELECT completed_at FROM agent_runs WHERE id = $1`, run.ID).
		Scan(&completedAt)
	if err != nil {
		t.Fatalf("SELECT after SetRunStatus failed: %v", err)
	}

	if completedAt != nil {
		t.Errorf("completed_at: expected nil for non-terminal status, got %v", completedAt)
	}
}

// @{"verifies": ["REQ-AGENT-006", "REQ-AGENT-007"]}
func TestIncrementIteration_ReturnsSequentialValues(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	studentID := createTestUser(ctx, t, "agent_increment_iter_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Statistics", "intake")

	run, err := repo.CreateRun(ctx, courseID, "content_generation")
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}

	// Each IncrementIteration call must return the new value after the
	// increment, verifying the counter advances by exactly 1 each time.
	for _, expected := range []int{1, 2, 3} {
		got, err := repo.IncrementIteration(ctx, run.ID)
		if err != nil {
			t.Fatalf("IncrementIteration (expected %d) failed: %v", expected, err)
		}
		if got != expected {
			t.Errorf("IncrementIteration: expected %d, got %d", expected, got)
		}
	}
}

// @{"verifies": ["REQ-AGENT-006"]}
func TestEmitEvent_RowPersistedWithCorrectFields(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	studentID := createTestUser(ctx, t, "agent_emit_event_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Discrete Math", "intake")

	run, err := repo.CreateRun(ctx, courseID, "content_generation")
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}

	payload := map[string]string{"section_id": "abc"}
	if err := repo.EmitEvent(ctx, run.ID, "generation_started", payload); err != nil {
		t.Fatalf("EmitEvent failed: %v", err)
	}

	// Verify the event was persisted with the correct agent_run_id, event_type,
	// and that the JSON payload round-trips cleanly.
	var agentRunID uuid.UUID
	var eventType string
	var rawPayload []byte
	err = pool.QueryRow(ctx,
		`SELECT agent_run_id, event_type, payload
		 FROM pipeline_events
		 WHERE agent_run_id = $1`, run.ID).
		Scan(&agentRunID, &eventType, &rawPayload)
	if err != nil {
		t.Fatalf("SELECT pipeline_events failed: %v", err)
	}

	if agentRunID != run.ID {
		t.Errorf("agent_run_id: expected %v, got %v", run.ID, agentRunID)
	}
	if eventType != "generation_started" {
		t.Errorf("event_type: expected %q, got %q", "generation_started", eventType)
	}

	var gotPayload map[string]string
	if err := json.Unmarshal(rawPayload, &gotPayload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if gotPayload["section_id"] != "abc" {
		t.Errorf("payload[section_id]: expected %q, got %q", "abc", gotPayload["section_id"])
	}
}

// @{"verifies": ["REQ-AGENT-006"]}
func TestGetEventsAfter_NoAfterID_ReturnsAllInAscendingOrder(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	studentID := createTestUser(ctx, t, "agent_getevents_noafter_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Graph Theory", "intake")

	run, err := repo.CreateRun(ctx, courseID, "content_generation")
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}

	// Events are ordered by emitted_at; sleep briefly between emissions so
	// each has a distinct timestamp that survives microsecond rounding.
	eventTypes := []string{"generation_started", "section_generating", "section_review_passed"}
	for _, et := range eventTypes {
		if err := repo.EmitEvent(ctx, run.ID, et, map[string]string{}); err != nil {
			t.Fatalf("EmitEvent(%q) failed: %v", et, err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	events, err := repo.GetEventsAfter(ctx, courseID, nil, 100)
	if err != nil {
		t.Fatalf("GetEventsAfter failed: %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	// Verify ascending emitted_at order.
	for i := 1; i < len(events); i++ {
		if !events[i].EmittedAt.After(events[i-1].EmittedAt) {
			t.Errorf("events not in strict ascending order at index %d", i)
		}
	}

	// Verify the first event is the earliest type emitted.
	if events[0].EventType != "generation_started" {
		t.Errorf("first event: expected %q, got %q", "generation_started", events[0].EventType)
	}
}

// @{"verifies": ["REQ-AGENT-006"]}
func TestGetEventsAfter_WithAfterID_ReturnsTailOnly(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	studentID := createTestUser(ctx, t, "agent_getevents_afterid_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Number Theory", "intake")

	run, err := repo.CreateRun(ctx, courseID, "content_generation")
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}

	// Emit 3 events with distinguishable timestamps.
	eventTypes := []string{"generation_started", "section_generating", "section_review_passed"}
	for _, et := range eventTypes {
		if err := repo.EmitEvent(ctx, run.ID, et, map[string]string{}); err != nil {
			t.Fatalf("EmitEvent(%q) failed: %v", et, err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Retrieve all events to obtain the ID of the first one.
	all, err := repo.GetEventsAfter(ctx, courseID, nil, 100)
	if err != nil {
		t.Fatalf("GetEventsAfter (baseline) failed: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 baseline events, got %d", len(all))
	}

	// Passing the first event's ID must return only the 2nd and 3rd events.
	firstID := all[0].ID
	tail, err := repo.GetEventsAfter(ctx, courseID, &firstID, 100)
	if err != nil {
		t.Fatalf("GetEventsAfter with afterID failed: %v", err)
	}

	if len(tail) != 2 {
		t.Fatalf("expected 2 tail events, got %d", len(tail))
	}
	if tail[0].ID == firstID {
		t.Errorf("tail must not include the pivot event itself")
	}
	if tail[0].EventType != "section_generating" {
		t.Errorf("tail[0]: expected %q, got %q", "section_generating", tail[0].EventType)
	}
	if tail[1].EventType != "section_review_passed" {
		t.Errorf("tail[1]: expected %q, got %q", "section_review_passed", tail[1].EventType)
	}
}

// @{"verifies": ["REQ-AGENT-013"]}
func TestTerminateStudentRuns_OnlyTerminatesRunningRuns(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	// Each run needs its own course; a student may only have one active course
	// at a time (unique index on student_id for non-archived statuses).
	// Use two students sharing the same logical owner for clarity.
	studentID := createTestUser(ctx, t, "agent_terminate_"+uuid.New().String())

	// Student may only have one active course at a time; archive the first
	// before creating the second.
	courseID1 := createTestCourse(ctx, t, studentID, "Thermodynamics", "intake")
	run1, err := repo.CreateRun(ctx, courseID1, "content_generation")
	if err != nil {
		t.Fatalf("CreateRun (run1) failed: %v", err)
	}

	// Mark courseID1 as archived so we can create courseID2 for the same student.
	if _, err := pool.Exec(ctx,
		`UPDATE courses SET status = 'archived' WHERE id = $1`, courseID1); err != nil {
		t.Fatalf("archive course1: %v", err)
	}

	courseID2 := createTestCourse(ctx, t, studentID, "Quantum Mechanics", "intake")
	run2, err := repo.CreateRun(ctx, courseID2, "content_generation")
	if err != nil {
		t.Fatalf("CreateRun (run2) failed: %v", err)
	}

	// Complete run2 before calling TerminateStudentRuns; only run1 is still running.
	if err := repo.SetRunStatus(ctx, run2.ID, "completed", nil); err != nil {
		t.Fatalf("SetRunStatus (run2) failed: %v", err)
	}

	terminated, err := repo.TerminateStudentRuns(ctx, studentID)
	if err != nil {
		t.Fatalf("TerminateStudentRuns failed: %v", err)
	}

	// Only the still-running run1 should have been terminated.
	if len(terminated) != 1 {
		t.Fatalf("expected 1 terminated run, got %d: %v", len(terminated), terminated)
	}
	if terminated[0] != run1.ID {
		t.Errorf("terminated run: expected %v, got %v", run1.ID, terminated[0])
	}

	// run2 must remain 'completed', not re-terminated.
	var run2Status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM agent_runs WHERE id = $1`, run2.ID).Scan(&run2Status); err != nil {
		t.Fatalf("SELECT run2 status: %v", err)
	}
	if run2Status != "completed" {
		t.Errorf("run2 status: expected %q, got %q", "completed", run2Status)
	}
}

// @{"verifies": ["REQ-AGENT-006", "REQ-AGENT-007"]}
func TestListRunningContentGenerations_ReturnsOnlyRunning(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	// Create two students, each with one course.
	studentA := createTestUser(ctx, t, "agent_list_running_a_"+uuid.New().String())
	courseA := createTestCourse(ctx, t, studentA, "Biology", "intake")
	runA, err := repo.CreateRun(ctx, courseA, "content_generation")
	if err != nil {
		t.Fatalf("CreateRun (A) failed: %v", err)
	}

	studentB := createTestUser(ctx, t, "agent_list_running_b_"+uuid.New().String())
	courseB := createTestCourse(ctx, t, studentB, "Chemistry", "intake")
	runB, err := repo.CreateRun(ctx, courseB, "content_generation")
	if err != nil {
		t.Fatalf("CreateRun (B) failed: %v", err)
	}

	// Terminate runB so it is no longer 'running'.
	if err := repo.SetRunStatus(ctx, runB.ID, "terminated", nil); err != nil {
		t.Fatalf("SetRunStatus (B) failed: %v", err)
	}

	runs, err := repo.ListRunningContentGenerations(ctx)
	if err != nil {
		t.Fatalf("ListRunningContentGenerations failed: %v", err)
	}

	// Build an index of returned run IDs for O(1) membership checks.
	found := make(map[uuid.UUID]bool, len(runs))
	for _, r := range runs {
		found[r.ID] = true
	}

	if !found[runA.ID] {
		t.Errorf("expected runA (%v) in running list", runA.ID)
	}
	if found[runB.ID] {
		t.Errorf("expected runB (%v) absent from running list (status=terminated)", runB.ID)
	}
}

// @{"verifies": ["REQ-AGENT-003", "REQ-AGENT-006", "REQ-AGENT-007"]}
func TestListUntriggeredApprovals_AppearsWithoutExistingRun(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	// A course in 'syllabus_approved' with no content_generation run must
	// appear in the result set (REQ-AGENT-003: trigger generation on approval).
	studentID := createTestUser(ctx, t, "agent_untriggered_yes_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Organic Chemistry", "syllabus_approved")

	// maxAttempts=100 keeps the cap from filtering this course — the test only
	// verifies the "no run exists → eligible" predicate, not the attempt-count gate.
	results, err := repo.ListUntriggeredApprovals(ctx, 100)
	if err != nil {
		t.Fatalf("ListUntriggeredApprovals failed: %v", err)
	}

	found := false
	for _, r := range results {
		if r.CourseID == courseID && r.StudentID == studentID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected course %v to appear in untriggered approvals", courseID)
	}
}

// @{"verifies": ["REQ-AGENT-003", "REQ-AGENT-006", "REQ-AGENT-007"]}
func TestListUntriggeredApprovals_AbsentWhenRunExists(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	// A course in 'syllabus_approved' that already has a content_generation
	// run must NOT appear — the trigger has already fired.
	studentID := createTestUser(ctx, t, "agent_untriggered_no_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Physical Chemistry", "syllabus_approved")

	if _, err := repo.CreateRun(ctx, courseID, "content_generation"); err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}

	// maxAttempts=100 keeps the cap from filtering this course — the test only
	// verifies the "run already exists → excluded" predicate.
	results, err := repo.ListUntriggeredApprovals(ctx, 100)
	if err != nil {
		t.Fatalf("ListUntriggeredApprovals failed: %v", err)
	}

	for _, r := range results {
		if r.CourseID == courseID {
			t.Errorf("course %v must NOT appear in untriggered approvals when a run exists", courseID)
		}
	}
}

// createTestCourseWithAttempts inserts a syllabus_approved course with explicit
// generation_attempt_count and next_eligible_at values. Used by eligibility tests
// that need to exercise backoff and max-attempt predicates.
func createTestCourseWithAttempts(ctx context.Context, t *testing.T, studentID uuid.UUID, topic string, attemptCount int, nextEligibleAt *time.Time, treeMode bool) uuid.UUID {
	t.Helper()
	var courseID uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO courses (student_id, topic, status, generation_attempt_count, next_eligible_at, tree_mode)
		 VALUES ($1, $2, 'syllabus_approved', $3, $4, $5)
		 RETURNING id`,
		studentID, topic, attemptCount, nextEligibleAt, treeMode).
		Scan(&courseID)
	if err != nil {
		t.Fatalf("createTestCourseWithAttempts: %v", err)
	}
	return courseID
}

// ─── ListUntriggeredApprovals — eligibility predicate tests ────────────────

// @{"verifies": ["REQ-AGENT-064", "REQ-AGENT-067"]}
func TestListUntriggeredApprovals_ExcludesBackoffCourse(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	studentID := createTestUser(ctx, t, "agent_backoff_excl_"+uuid.New().String())

	// next_eligible_at is 1 hour in the future — still within the backoff window.
	future := time.Now().Add(time.Hour)
	courseID := createTestCourseWithAttempts(ctx, t, studentID, "Topology", 1, &future, false)

	results, err := repo.ListUntriggeredApprovals(ctx, 100)
	if err != nil {
		t.Fatalf("ListUntriggeredApprovals failed: %v", err)
	}
	for _, r := range results {
		if r.CourseID == courseID {
			t.Errorf("course within backoff window must NOT appear in untriggered approvals")
		}
	}
}

// @{"verifies": ["REQ-AGENT-064", "REQ-AGENT-067"]}
func TestListUntriggeredApprovals_IncludesCourseAfterBackoffExpires(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	studentID := createTestUser(ctx, t, "agent_backoff_incl_"+uuid.New().String())

	// next_eligible_at is 1 second in the past — backoff window has elapsed.
	past := time.Now().Add(-time.Second)
	courseID := createTestCourseWithAttempts(ctx, t, studentID, "Algebraic Topology", 1, &past, false)

	results, err := repo.ListUntriggeredApprovals(ctx, 100)
	if err != nil {
		t.Fatalf("ListUntriggeredApprovals failed: %v", err)
	}
	found := false
	for _, r := range results {
		if r.CourseID == courseID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("course past its backoff window must appear in untriggered approvals")
	}
}

// @{"verifies": ["REQ-AGENT-064", "REQ-AGENT-067"]}
func TestListUntriggeredApprovals_ExcludesCourseAtMaxAttempts(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	studentID := createTestUser(ctx, t, "agent_maxattempts_"+uuid.New().String())

	// generation_attempt_count == maxAttempts: course is at the cap, not below it.
	const maxAttempts int64 = 5
	courseID := createTestCourseWithAttempts(ctx, t, studentID, "Measure Theory", int(maxAttempts), nil, false)

	results, err := repo.ListUntriggeredApprovals(ctx, maxAttempts)
	if err != nil {
		t.Fatalf("ListUntriggeredApprovals failed: %v", err)
	}
	for _, r := range results {
		if r.CourseID == courseID {
			t.Errorf("course at max attempts must NOT appear in untriggered approvals")
		}
	}
}

// @{"verifies": ["REQ-AGENT-064"]}
func TestListUntriggeredApprovals_ExcludesGenerationFailedCourse(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	studentID := createTestUser(ctx, t, "agent_genfailed_excl_"+uuid.New().String())

	// Insert the course as syllabus_approved first (unique index constraint),
	// then move it to generation_failed to test the exclusion predicate.
	courseID := createTestCourse(ctx, t, studentID, "Complex Analysis", "syllabus_approved")
	if _, err := pool.Exec(ctx,
		`UPDATE courses SET status = 'generation_failed' WHERE id = $1`, courseID); err != nil {
		t.Fatalf("set generation_failed: %v", err)
	}

	results, err := repo.ListUntriggeredApprovals(ctx, 100)
	if err != nil {
		t.Fatalf("ListUntriggeredApprovals failed: %v", err)
	}
	for _, r := range results {
		if r.CourseID == courseID {
			t.Errorf("generation_failed course must NEVER appear in untriggered approvals")
		}
	}
}

// @{"verifies": ["REQ-AGENT-003", "REQ-AGENT-064"]}
func TestListUntriggeredApprovals_IncludesCleanSyllabusApproved(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	// Positive control: a clean syllabus_approved course (zero attempts, no
	// backoff, no existing run) must always appear.
	studentID := createTestUser(ctx, t, "agent_clean_approved_"+uuid.New().String())
	courseID := createTestCourseWithAttempts(ctx, t, studentID, "Real Analysis", 0, nil, false)

	results, err := repo.ListUntriggeredApprovals(ctx, 5)
	if err != nil {
		t.Fatalf("ListUntriggeredApprovals failed: %v", err)
	}
	found := false
	for _, r := range results {
		if r.CourseID == courseID {
			if r.TreeMode {
				t.Errorf("expected flat course (TreeMode=false), got true")
			}
			found = true
			break
		}
	}
	if !found {
		t.Errorf("clean syllabus_approved course must appear in untriggered approvals")
	}
}

// @{"verifies": ["REQ-AGENT-037", "REQ-AGENT-064"]}
func TestListUntriggeredApprovals_TreeBranchReturnsTreeMode(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	// tree_mode=true course: must appear and have TreeMode=true in the result,
	// so pollAndGenerate routes it to the layered pipeline.
	studentID := createTestUser(ctx, t, "agent_tree_branch_"+uuid.New().String())
	courseID := createTestCourseWithAttempts(ctx, t, studentID, "Category Theory", 0, nil, true)

	results, err := repo.ListUntriggeredApprovals(ctx, 5)
	if err != nil {
		t.Fatalf("ListUntriggeredApprovals failed: %v", err)
	}
	found := false
	for _, r := range results {
		if r.CourseID == courseID {
			if !r.TreeMode {
				t.Errorf("expected tree-mode course (TreeMode=true), got false")
			}
			found = true
			break
		}
	}
	if !found {
		t.Errorf("tree-mode syllabus_approved course must appear in untriggered approvals")
	}
}

// ─── ConsecutiveFailures ────────────────────────────────────────────────────

// @{"verifies": ["REQ-AGENT-065"]}
func TestConsecutiveFailures_ZeroWithNoRuns(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	studentID := createTestUser(ctx, t, "agent_cf_zero_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Set Theory", "intake")

	count, err := repo.ConsecutiveFailures(ctx, courseID, "content_generation")
	if err != nil {
		t.Fatalf("ConsecutiveFailures failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 consecutive failures with no runs, got %d", count)
	}
}

// @{"verifies": ["REQ-AGENT-065"]}
func TestConsecutiveFailures_NFailsStraight(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	studentID := createTestUser(ctx, t, "agent_cf_straight_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Logic", "intake")

	// Insert 3 failed runs with sequential started_at values so the ordering
	// within ConsecutiveFailures' COALESCE subquery is deterministic.
	for i := 0; i < 3; i++ {
		run, err := repo.CreateRun(ctx, courseID, "content_generation")
		if err != nil {
			t.Fatalf("CreateRun (fail %d) failed: %v", i, err)
		}
		if err := repo.SetRunStatus(ctx, run.ID, "failed", nil); err != nil {
			t.Fatalf("SetRunStatus (fail %d) failed: %v", i, err)
		}
	}

	count, err := repo.ConsecutiveFailures(ctx, courseID, "content_generation")
	if err != nil {
		t.Fatalf("ConsecutiveFailures failed: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 consecutive failures, got %d", count)
	}
}

// @{"verifies": ["REQ-AGENT-065"]}
func TestConsecutiveFailures_ResetsAfterSuccess(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	studentID := createTestUser(ctx, t, "agent_cf_reset_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Number Theory Advanced", "intake")

	// 2 failures before the success run.
	for i := 0; i < 2; i++ {
		run, err := repo.CreateRun(ctx, courseID, "content_generation")
		if err != nil {
			t.Fatalf("CreateRun (pre-success fail %d): %v", i, err)
		}
		if err := repo.SetRunStatus(ctx, run.ID, "failed", nil); err != nil {
			t.Fatalf("SetRunStatus (pre-success fail %d): %v", i, err)
		}
	}

	// One completed run — resets the consecutive counter.
	successRun, err := repo.CreateRun(ctx, courseID, "content_generation")
	if err != nil {
		t.Fatalf("CreateRun (success): %v", err)
	}
	if err := repo.SetRunStatus(ctx, successRun.ID, "completed", nil); err != nil {
		t.Fatalf("SetRunStatus (success): %v", err)
	}

	// 3 failures after the success run.
	for i := 0; i < 3; i++ {
		run, err := repo.CreateRun(ctx, courseID, "content_generation")
		if err != nil {
			t.Fatalf("CreateRun (post-success fail %d): %v", i, err)
		}
		if err := repo.SetRunStatus(ctx, run.ID, "failed", nil); err != nil {
			t.Fatalf("SetRunStatus (post-success fail %d): %v", i, err)
		}
	}

	// Only the 3 post-success failures should count.
	count, err := repo.ConsecutiveFailures(ctx, courseID, "content_generation")
	if err != nil {
		t.Fatalf("ConsecutiveFailures failed: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 consecutive failures since last success, got %d", count)
	}
}

// ─── IncrementAttemptCount ──────────────────────────────────────────────────

// @{"verifies": ["REQ-AGENT-065", "REQ-AGENT-067"]}
func TestIncrementAttemptCount_IncrementsAndSetsBackoff(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	studentID := createTestUser(ctx, t, "agent_inc_attempt_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Probability Theory", "intake")

	const backoffSeconds int64 = 600

	// First increment: count should become 1 and next_eligible_at should be set.
	beforeCall := time.Now()
	newCount, err := repo.IncrementAttemptCount(ctx, courseID, backoffSeconds)
	afterCall := time.Now()
	if err != nil {
		t.Fatalf("IncrementAttemptCount: %v", err)
	}
	if newCount != 1 {
		t.Errorf("IncrementAttemptCount: expected count=1, got %d", newCount)
	}

	// Read back both columns to verify they are set correctly.
	var count int
	var nextEligible time.Time
	err = pool.QueryRow(ctx,
		`SELECT generation_attempt_count, next_eligible_at FROM courses WHERE id = $1`,
		courseID).Scan(&count, &nextEligible)
	if err != nil {
		t.Fatalf("SELECT after IncrementAttemptCount: %v", err)
	}
	if count != 1 {
		t.Errorf("generation_attempt_count: expected 1, got %d", count)
	}
	// next_eligible_at must be approximately now + backoffSeconds. Allow ±2 seconds
	// of clock skew between Go and PostgreSQL, and for any round-trip latency.
	const tolerance = 2 * time.Second
	expectedMin := beforeCall.Add(time.Duration(backoffSeconds)*time.Second - tolerance)
	expectedMax := afterCall.Add(time.Duration(backoffSeconds)*time.Second + tolerance)
	if nextEligible.Before(expectedMin) || nextEligible.After(expectedMax) {
		t.Errorf("next_eligible_at %v outside expected window [%v, %v]", nextEligible, expectedMin, expectedMax)
	}

	// Second increment: count should become 2.
	newCount2, err := repo.IncrementAttemptCount(ctx, courseID, backoffSeconds)
	if err != nil {
		t.Fatalf("IncrementAttemptCount (2nd): %v", err)
	}
	if newCount2 != 2 {
		t.Errorf("IncrementAttemptCount (2nd): expected count=2, got %d", newCount2)
	}
}

// ─── SetCourseTerminal ──────────────────────────────────────────────────────

// @{"verifies": ["REQ-AGENT-062"]}
func TestSetCourseTerminal_TransitionsToGenerationFailed(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	studentID := createTestUser(ctx, t, "agent_terminal_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Stochastic Processes", "syllabus_approved")

	if err := repo.SetCourseTerminal(ctx, courseID); err != nil {
		t.Fatalf("SetCourseTerminal: %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM courses WHERE id = $1`, courseID).Scan(&status); err != nil {
		t.Fatalf("SELECT after SetCourseTerminal: %v", err)
	}
	if status != "generation_failed" {
		t.Errorf("status: expected 'generation_failed', got %q", status)
	}
}

// ─── ResetAttemptCount ──────────────────────────────────────────────────────

// @{"verifies": ["REQ-AGENT-065"]}
func TestResetAttemptCount_ZeroesCountAndClearsBackoff(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	studentID := createTestUser(ctx, t, "agent_reset_attempt_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Ergodic Theory", "intake")

	// Seed the course with a non-zero count and a future backoff window.
	future := time.Now().Add(time.Hour)
	if _, err := pool.Exec(ctx,
		`UPDATE courses SET generation_attempt_count = 3, next_eligible_at = $1 WHERE id = $2`,
		future, courseID); err != nil {
		t.Fatalf("seed attempt count: %v", err)
	}

	if err := repo.ResetAttemptCount(ctx, courseID); err != nil {
		t.Fatalf("ResetAttemptCount: %v", err)
	}

	var count int
	var nextEligible *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT generation_attempt_count, next_eligible_at FROM courses WHERE id = $1`,
		courseID).Scan(&count, &nextEligible); err != nil {
		t.Fatalf("SELECT after ResetAttemptCount: %v", err)
	}
	if count != 0 {
		t.Errorf("generation_attempt_count: expected 0, got %d", count)
	}
	if nextEligible != nil {
		t.Errorf("next_eligible_at: expected NULL, got %v", nextEligible)
	}
}

// ─── RecoverGenerationFailed ────────────────────────────────────────────────

// @{"verifies": ["REQ-AGENT-069"]}
func TestRecoverGenerationFailed_RestoresSyllabusApproved(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	studentID := createTestUser(ctx, t, "agent_recover_ok_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Functional Analysis", "syllabus_approved")

	// Move the course to the terminal state and seed stale counters.
	future := time.Now().Add(time.Hour)
	if _, err := pool.Exec(ctx,
		`UPDATE courses SET status = 'generation_failed', generation_attempt_count = 5, next_eligible_at = $1 WHERE id = $2`,
		future, courseID); err != nil {
		t.Fatalf("seed generation_failed: %v", err)
	}

	if err := repo.RecoverGenerationFailed(ctx, courseID); err != nil {
		t.Fatalf("RecoverGenerationFailed: %v", err)
	}

	var status string
	var count int
	var nextEligible *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT status, generation_attempt_count, next_eligible_at FROM courses WHERE id = $1`,
		courseID).Scan(&status, &count, &nextEligible); err != nil {
		t.Fatalf("SELECT after RecoverGenerationFailed: %v", err)
	}
	if status != "syllabus_approved" {
		t.Errorf("status: expected 'syllabus_approved', got %q", status)
	}
	if count != 0 {
		t.Errorf("generation_attempt_count: expected 0 after recovery, got %d", count)
	}
	if nextEligible != nil {
		t.Errorf("next_eligible_at: expected NULL after recovery, got %v", nextEligible)
	}
}

// @{"verifies": ["REQ-AGENT-069"]}
func TestRecoverGenerationFailed_NoOpForNonTerminalCourse(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	studentID := createTestUser(ctx, t, "agent_recover_noop_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Harmonic Analysis", "syllabus_approved")

	// RecoverGenerationFailed must be a no-op when status != 'generation_failed'
	// (the WHERE status='generation_failed' guard in the UPDATE).
	if err := repo.RecoverGenerationFailed(ctx, courseID); err != nil {
		t.Fatalf("RecoverGenerationFailed (no-op): %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM courses WHERE id = $1`, courseID).Scan(&status); err != nil {
		t.Fatalf("SELECT after no-op RecoverGenerationFailed: %v", err)
	}
	if status != "syllabus_approved" {
		t.Errorf("status: expected 'syllabus_approved' (unchanged), got %q", status)
	}
}

// ─── CreateRun claim guard ──────────────────────────────────────────────────

// @{"verifies": ["REQ-AGENT-066"]}
func TestCreateRun_ClaimGuard_SecondRunReturnsClaimed(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	studentID := createTestUser(ctx, t, "agent_claim_guard_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Operator Algebras", "intake")

	// First CreateRun must succeed.
	_, err := repo.CreateRun(ctx, courseID, "content_generation")
	if err != nil {
		t.Fatalf("first CreateRun failed: %v", err)
	}

	// Second CreateRun for the same (course_id, run_type) while the first is
	// still 'running' must return ErrRunAlreadyClaimed.
	_, err = repo.CreateRun(ctx, courseID, "content_generation")
	if err == nil {
		t.Fatal("expected ErrRunAlreadyClaimed, got nil")
	}
	if err != ErrRunAlreadyClaimed {
		t.Errorf("expected ErrRunAlreadyClaimed, got %v", err)
	}
}

// @{"verifies": ["REQ-AGENT-066"]}
func TestCreateRun_ClaimGuard_AllowsAfterCompleted(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewAgentRepository(pool)

	studentID := createTestUser(ctx, t, "agent_claim_after_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Spectral Theory", "intake")

	// First run — running → completed.
	run1, err := repo.CreateRun(ctx, courseID, "content_generation")
	if err != nil {
		t.Fatalf("first CreateRun failed: %v", err)
	}
	if err := repo.SetRunStatus(ctx, run1.ID, "completed", nil); err != nil {
		t.Fatalf("SetRunStatus (complete run1): %v", err)
	}

	// After completion the unique index predicate (WHERE status='running') no
	// longer covers run1, so a new running claim must succeed.
	_, err = repo.CreateRun(ctx, courseID, "content_generation")
	if err != nil {
		t.Errorf("second CreateRun after completion should succeed, got %v", err)
	}
}
