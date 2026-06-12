-- migrations/011_intake_chat.sql
-- Sprint 11 — Intake chat wiring: status_change pipeline event type and
-- intake_kickoff_sent idempotency flag on courses.
--
-- REQ-AGENT-018: intake_kickoff_sent prevents duplicate opening questions.
-- REQ-AGENT-019: status_change event carries the new course status after
--   intake completion so the SSE client can redirect the student.
-- REQ-AGENT-021: status_change must be a valid pipeline_event_type enum value.
BEGIN;

INSERT INTO schema_migrations (version) VALUES ('011_intake_chat')
    ON CONFLICT (version) DO NOTHING;

-- Add status_change to the pipeline_event_type enum.
-- ALTER TYPE ... ADD VALUE IF NOT EXISTS is safe: it does not lock existing
-- rows and does not require a rollback path because ENUM values cannot be
-- removed without a full enum rebuild.
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'status_change';

-- Add intake_kickoff_sent flag so the kickoff goroutine is idempotent.
-- DEFAULT FALSE ensures all existing rows get the correct initial value.
-- The column is used as an atomic gate: the kickoff goroutine issues
--   UPDATE courses SET intake_kickoff_sent = true WHERE id = $1 AND intake_kickoff_sent = false
-- so that concurrent goroutines or a restarted server cannot insert a
-- duplicate opening question.
ALTER TABLE courses
    ADD COLUMN IF NOT EXISTS intake_kickoff_sent BOOLEAN NOT NULL DEFAULT FALSE;

-- Add attempt counter so the lazy-retry path has a hard cap on Claude calls.
-- The kickoff goroutine increments this atomically in the same guarded UPDATE
-- that claims intake_kickoff_sent, preventing unbounded retries from N
-- concurrent history-endpoint loads while intake_kickoff_sent = false.
-- Cap is enforced at 3 attempts in kickoffIntake (see chair.go).
ALTER TABLE courses
    ADD COLUMN IF NOT EXISTS intake_kickoff_attempts INTEGER NOT NULL DEFAULT 0;

-- Allow server to SELECT chat_messages so background goroutines (kickoff,
-- GenerateSyllabus) can read full history via the serverPool.
-- The student SELECT policy (004) already exists; this is the missing server
-- counterpart that lets chair.RunIntakeStep read history with a server-role conn.
DO $$ BEGIN
    CREATE POLICY chat_messages_server_select_policy ON chat_messages
        FOR SELECT
        USING (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Allow server to UPDATE chat_messages (not currently needed but consistent
-- with the pattern on other RLS tables that agents touch).
DO $$ BEGIN
    CREATE POLICY chat_messages_server_update_policy ON chat_messages
        FOR UPDATE
        USING (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

COMMIT;
