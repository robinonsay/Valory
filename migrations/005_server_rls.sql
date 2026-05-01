-- migrations/005_server_rls.sql
-- Adds server-role RLS policies for background agent writes.
-- The app server sets app.current_role = 'server' on acquired connections
-- before any background (non-request) database operation.
BEGIN;

INSERT INTO schema_migrations (version) VALUES ('005_server_rls')
    ON CONFLICT (version) DO NOTHING;

-- Allow server background processes to read course metadata (e.g., topic, status).
DO $$ BEGIN
    CREATE POLICY courses_server_select_policy ON courses
        FOR SELECT
        USING (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Allow server to transition course status (e.g., intake → generating → active).
DO $$ BEGIN
    CREATE POLICY courses_server_update_policy ON courses
        FOR UPDATE
        USING (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Allow server to SELECT lesson_content for the content library (REQ-AGENT-004).
DO $$ BEGIN
    CREATE POLICY lesson_content_server_select_policy ON lesson_content
        FOR SELECT
        USING (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Allow server to UPDATE lesson_content (e.g., set citation_verified = true).
DO $$ BEGIN
    CREATE POLICY lesson_content_server_update_policy ON lesson_content
        FOR UPDATE
        USING (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Allow server to SELECT section_feedback for the regen polling loop (REQ-AGENT-010).
DO $$ BEGIN
    CREATE POLICY section_feedback_server_select_policy ON section_feedback
        FOR SELECT
        USING (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Allow server to UPDATE section_feedback (e.g., set regeneration_triggered = true).
DO $$ BEGIN
    CREATE POLICY section_feedback_server_update_policy ON section_feedback
        FOR UPDATE
        USING (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

COMMIT;
