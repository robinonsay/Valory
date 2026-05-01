BEGIN;

-- Sprint 4 — Agent Module
-- REQ-AGENT-001..015: agent_runs, pipeline_events, chat_messages tables
-- REQ-CONTENT-001, REQ-CONTENT-004: lesson_content, section_feedback tables
-- REQ-NOTIFY-001, REQ-NOTIFY-002: notifications table
-- REQ-ADMIN-004: agent_token_usage already present (migration 003); this migration adds tracking tables

INSERT INTO schema_migrations (version) VALUES ('004_agent')
    ON CONFLICT (version) DO NOTHING;

CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- agent_run_type ENUM
DO $$ BEGIN
    CREATE TYPE agent_run_type AS ENUM ('intake', 'syllabus', 'content_generation', 'section_regen', 'grading');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- agent_run_status ENUM
DO $$ BEGIN
    CREATE TYPE agent_run_status AS ENUM ('running', 'completed', 'failed', 'terminated');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- pipeline_event_type ENUM
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

-- chat_role ENUM
DO $$ BEGIN
    CREATE TYPE chat_role AS ENUM ('student', 'assistant');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- notification_type ENUM
DO $$ BEGIN
    CREATE TYPE notification_type AS ENUM ('api_failure', 'generation_timeout', 'admin_escalation');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- agent_runs table (server-side only, no RLS)
CREATE TABLE IF NOT EXISTS agent_runs (
    id              UUID             PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id       UUID             NOT NULL REFERENCES courses(id) ON DELETE RESTRICT,
    run_type        agent_run_type   NOT NULL,
    status          agent_run_status NOT NULL DEFAULT 'running',
    started_at      TIMESTAMPTZ      NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ,
    iteration_count INTEGER          NOT NULL DEFAULT 0, -- REQ-AGENT-007: incremented on each correction attempt
    error           TEXT
);

CREATE INDEX IF NOT EXISTS agent_runs_course_id_idx ON agent_runs (course_id);
CREATE INDEX IF NOT EXISTS agent_runs_status_idx    ON agent_runs (status) WHERE status = 'running';

-- pipeline_events table (server-side only, no RLS)
CREATE TABLE IF NOT EXISTS pipeline_events (
    id           UUID                PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_run_id UUID                NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    event_type   pipeline_event_type NOT NULL,
    payload      JSONB               NOT NULL DEFAULT '{}',
    emitted_at   TIMESTAMPTZ         NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS pipeline_events_run_emitted_idx
    ON pipeline_events (agent_run_id, emitted_at ASC);

-- chat_messages table with RLS
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

-- notifications table with RLS
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

-- lesson_content table with RLS
CREATE TABLE IF NOT EXISTS lesson_content (
    id                UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id         UUID         NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    section_index     INT          NOT NULL,
    title             VARCHAR(255) NOT NULL,
    content_adoc      TEXT         NOT NULL,
    version           INT          NOT NULL DEFAULT 1,
    citation_verified BOOLEAN      NOT NULL DEFAULT FALSE, -- REQ-CONTENT-001: set true by ReviewerAgent after citation check
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

-- section_feedback table with RLS
CREATE TABLE IF NOT EXISTS section_feedback (
    id                     UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    student_id             UUID          NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    course_id              UUID          NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    section_index          INT           NOT NULL,
    feedback_text          VARCHAR(2000) NOT NULL,
    submitted_at           TIMESTAMPTZ   NOT NULL DEFAULT now(),
    regeneration_triggered BOOLEAN       NOT NULL DEFAULT FALSE -- REQ-CONTENT-004: set true when feedback keywords trigger regen
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

-- Fix courses_student_policy from migration 003 to use NULLIF-safe UUID cast
DO $$ BEGIN
    DROP POLICY IF EXISTS courses_student_policy ON courses;
    CREATE POLICY courses_student_policy ON courses
        USING (student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid);
EXCEPTION WHEN others THEN NULL;
END $$;

-- Grants for valory_app role
GRANT SELECT, INSERT, UPDATE, DELETE ON agent_runs       TO valory_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON pipeline_events  TO valory_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON chat_messages    TO valory_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON notifications    TO valory_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON lesson_content   TO valory_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON section_feedback TO valory_app;

COMMIT;
