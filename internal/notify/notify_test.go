package notify

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		// No database URL provided; skip all integration tests gracefully.
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

// applyMigrations runs every schema migration in order so the integration tests
// operate against a fully-initialised database. The statements are idempotent
// (IF NOT EXISTS / ON CONFLICT DO NOTHING) so running them against an already-
// migrated database is safe.
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

	CREATE TYPE IF NOT EXISTS course_status AS ENUM (
	    'intake',
	    'syllabus_draft',
	    'syllabus_approved',
	    'generating',
	    'active',
	    'archived',
	    'completed'
	);

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

	ALTER TABLE chat_messages ENABLE ROW LEVEL SECURITY;
	ALTER TABLE chat_messages FORCE ROW LEVEL SECURITY;

	DO $$ BEGIN
	    CREATE POLICY chat_messages_student_policy ON chat_messages
	        USING (course_id IN (SELECT id FROM courses WHERE student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid))
	        WITH CHECK (course_id IN (SELECT id FROM courses WHERE student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid));
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

	DO $$ BEGIN
	    CREATE POLICY chat_messages_admin_policy ON chat_messages
	        USING (current_setting('app.current_role', true) = 'admin');
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

	DO $$ BEGIN
	    CREATE POLICY chat_messages_server_policy ON chat_messages
	        FOR INSERT
	        WITH CHECK (current_setting('app.current_role', true) = 'server');
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

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

	ALTER TABLE notifications ENABLE ROW LEVEL SECURITY;
	ALTER TABLE notifications FORCE ROW LEVEL SECURITY;

	DO $$ BEGIN
	    CREATE POLICY notifications_student_policy ON notifications
	        USING (student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid);
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

	DO $$ BEGIN
	    CREATE POLICY notifications_admin_policy ON notifications
	        USING (current_setting('app.current_role', true) = 'admin');
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

	DO $$ BEGIN
	    CREATE POLICY notifications_server_policy ON notifications
	        FOR INSERT
	        WITH CHECK (current_setting('app.current_role', true) = 'server');
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

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

	CREATE INDEX IF NOT EXISTS idx_lesson_content_trgm
	    ON lesson_content USING gin (title gin_trgm_ops);

	ALTER TABLE lesson_content ENABLE ROW LEVEL SECURITY;
	ALTER TABLE lesson_content FORCE ROW LEVEL SECURITY;

	DO $$ BEGIN
	    CREATE POLICY lesson_content_student_policy ON lesson_content
	        USING (course_id IN (SELECT id FROM courses WHERE student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid))
	        WITH CHECK (course_id IN (SELECT id FROM courses WHERE student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid));
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

	DO $$ BEGIN
	    CREATE POLICY lesson_content_admin_policy ON lesson_content
	        USING (current_setting('app.current_role', true) = 'admin');
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

	DO $$ BEGIN
	    CREATE POLICY lesson_content_server_policy ON lesson_content
	        FOR INSERT
	        WITH CHECK (current_setting('app.current_role', true) = 'server');
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

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

	ALTER TABLE section_feedback ENABLE ROW LEVEL SECURITY;
	ALTER TABLE section_feedback FORCE ROW LEVEL SECURITY;

	DO $$ BEGIN
	    CREATE POLICY section_feedback_student_policy ON section_feedback
	        USING (student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid)
	        WITH CHECK (student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid);
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

	DO $$ BEGIN
	    CREATE POLICY section_feedback_admin_policy ON section_feedback
	        USING (current_setting('app.current_role', true) = 'admin');
	EXCEPTION WHEN duplicate_object THEN NULL;
	END $$;

	DO $$ BEGIN
	    DROP POLICY IF EXISTS courses_student_policy ON courses;
	    CREATE POLICY courses_student_policy ON courses
	        USING (student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid);
	EXCEPTION WHEN others THEN NULL;
	END $$;

	COMMIT;
	`

	if _, err := p.Exec(ctx, migration004); err != nil {
		return err
	}

	return nil
}

// truncateTables removes all rows from tables in dependency-safe order so each
// test run starts from a known-clean state.
func truncateTables(ctx context.Context, p *pgxpool.Pool) error {
	statements := []string{
		`TRUNCATE TABLE section_feedback CASCADE`,
		`TRUNCATE TABLE lesson_content CASCADE`,
		`TRUNCATE TABLE notifications CASCADE`,
		`TRUNCATE TABLE pipeline_events CASCADE`,
		`TRUNCATE TABLE agent_runs CASCADE`,
		`TRUNCATE TABLE chat_messages CASCADE`,
		`TRUNCATE TABLE agent_token_usage CASCADE`,
		`TRUNCATE TABLE due_date_schedules CASCADE`,
		`TRUNCATE TABLE homework CASCADE`,
		`TRUNCATE TABLE syllabi CASCADE`,
		`TRUNCATE TABLE courses CASCADE`,
		`TRUNCATE TABLE audit_log CASCADE`,
		`TRUNCATE TABLE student_consent CASCADE`,
		`TRUNCATE TABLE password_reset_attempts CASCADE`,
		`TRUNCATE TABLE password_reset_tokens CASCADE`,
		`TRUNCATE TABLE login_attempts CASCADE`,
		`TRUNCATE TABLE sessions CASCADE`,
		`TRUNCATE TABLE users CASCADE`,
	}
	for _, stmt := range statements {
		if _, err := p.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// insertTestUser inserts a minimal user row and returns its generated UUID.
// A real user row is required because notifications.student_id has a foreign key
// constraint referencing users(id).
func insertTestUser(ctx context.Context, t *testing.T, username string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
		username, "testhash", "student",
	).Scan(&id)
	if err != nil {
		t.Fatalf("insertTestUser: %v", err)
	}
	return id
}

// @{"verifies": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
func TestWrite_InsertSuccess(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()

	// Each test gets a uniquely-named user to avoid cross-test interference.
	studentID := insertTestUser(ctx, t, "notify_user_"+uuid.New().String())

	n := Notification{
		StudentID: studentID,
		Type:      TypeAPIFailure,
		Message:   "API call timed out",
	}

	if err := Write(ctx, pool, n); err != nil {
		t.Fatalf("Write returned unexpected error: %v", err)
	}

	// Verify the persisted row has the correct field values and read_at IS NULL.
	var (
		gotStudentID string
		gotType      string
		gotMessage   string
		gotReadAt    *string // NULL expected
	)
	err := pool.QueryRow(ctx,
		`SELECT student_id, type, message, read_at
		   FROM notifications
		  WHERE student_id = $1
		  ORDER BY created_at DESC
		  LIMIT 1`,
		studentID,
	).Scan(&gotStudentID, &gotType, &gotMessage, &gotReadAt)
	if err != nil {
		t.Fatalf("query after Write failed: %v", err)
	}

	if gotStudentID != studentID.String() {
		t.Errorf("student_id: want %s, got %s", studentID, gotStudentID)
	}
	if gotType != TypeAPIFailure {
		t.Errorf("type: want %q, got %q", TypeAPIFailure, gotType)
	}
	if gotMessage != n.Message {
		t.Errorf("message: want %q, got %q", n.Message, gotMessage)
	}
	if gotReadAt != nil {
		t.Errorf("read_at: want NULL, got %v", *gotReadAt)
	}
}

// @{"verifies": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
func TestWrite_MultipleNotifications(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()

	studentID := insertTestUser(ctx, t, "notify_multi_"+uuid.New().String())

	cases := []struct {
		notifType string
		message   string
	}{
		{TypeAPIFailure, "first: api failure"},
		{TypeGenerationTimeout, "second: generation timed out"},
	}

	for _, tc := range cases {
		n := Notification{
			StudentID: studentID,
			Type:      tc.notifType,
			Message:   tc.message,
		}
		if err := Write(ctx, pool, n); err != nil {
			t.Fatalf("Write(%s) returned unexpected error: %v", tc.notifType, err)
		}
	}

	// Both rows must be present in the database.
	rows, err := pool.Query(ctx,
		`SELECT type, message FROM notifications WHERE student_id = $1 ORDER BY created_at ASC`,
		studentID,
	)
	if err != nil {
		t.Fatalf("query after two Writes failed: %v", err)
	}
	defer rows.Close()

	var results []struct {
		notifType string
		message   string
	}
	for rows.Next() {
		var r struct {
			notifType string
			message   string
		}
		if err := rows.Scan(&r.notifType, &r.message); err != nil {
			t.Fatalf("scan failed: %v", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows iteration error: %v", err)
	}

	if len(results) != len(cases) {
		t.Fatalf("row count: want %d, got %d", len(cases), len(results))
	}

	for i, tc := range cases {
		if results[i].notifType != tc.notifType {
			t.Errorf("row %d type: want %q, got %q", i, tc.notifType, results[i].notifType)
		}
		if results[i].message != tc.message {
			t.Errorf("row %d message: want %q, got %q", i, tc.message, results[i].message)
		}
	}
}

// @{"verifies": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
func TestWrite_InvalidStudentID_ReturnsForeignKeyError(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()

	// Use a random UUID that has no corresponding row in the users table so the
	// foreign key constraint on notifications.student_id must be violated.
	nonExistentStudentID := uuid.New()

	n := Notification{
		StudentID: nonExistentStudentID,
		Type:      TypeAdminEscalation,
		Message:   "should never be stored",
	}

	err := Write(ctx, pool, n)
	if err == nil {
		t.Fatal("Write with non-existent student_id: want FK error, got nil")
	}
}
