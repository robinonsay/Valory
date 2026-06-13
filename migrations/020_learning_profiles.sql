-- migrations/020_learning_profiles.sql
-- Sprint 23 — Persistent Student Learning Profile and Onboarding
-- REQ-PROFILE-001..016, REQ-SYS-069, REQ-SYS-070
--
-- Creates learning_profiles and onboarding_messages tables with FORCE RLS.
-- Adds users.onboarding_prompted column (soft-gate flag).
--
-- learning_profiles policies mirror the four-policy pattern from migration 005
-- (server SELECT, INSERT, UPDATE) plus the student own-row pattern from 004.
-- onboarding_messages policies follow the chat_messages server-policy pattern
-- from migration 011.
--
-- Rollback (additive-only, safe to reverse):
--   DROP TABLE IF EXISTS onboarding_messages;
--   DROP TABLE IF EXISTS learning_profiles;
--   ALTER TABLE users DROP COLUMN IF EXISTS onboarding_prompted;
--   DELETE FROM schema_migrations WHERE version = '020_learning_profiles';

BEGIN;

INSERT INTO schema_migrations (version) VALUES ('020_learning_profiles')
    ON CONFLICT (version) DO NOTHING;

-- learning_profiles: one row per student, student_id is the PK.
-- No surrogate id needed — callers always look up by student_id.
-- source CHECK constraint is intentionally small: adding new sources requires
-- a migration so extensibility is controlled.
CREATE TABLE IF NOT EXISTS learning_profiles (
    student_id   UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    summary      TEXT        NOT NULL DEFAULT '',
    source       TEXT        NOT NULL DEFAULT 'onboarding'
                             CHECK (source IN ('onboarding', 'manual_edit')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

GRANT SELECT, INSERT, UPDATE ON learning_profiles TO valory_app;

ALTER TABLE learning_profiles ENABLE ROW LEVEL SECURITY;
ALTER TABLE learning_profiles FORCE ROW LEVEL SECURITY;

-- Student can read and write their own profile row.
-- NULLIF guard prevents an empty GUC string from casting to an invalid UUID
-- (the same guard used in migrations 004 and 009 for all student policies).
DO $$ BEGIN
    CREATE POLICY learning_profiles_student_policy ON learning_profiles
        USING (NULLIF(current_setting('app.current_role', true), '') = 'student'
           AND student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Server can read any profile for prompt injection (the agent pipeline uses
-- app.current_role = 'server' via the server pool).
DO $$ BEGIN
    CREATE POLICY learning_profiles_server_select_policy ON learning_profiles
        FOR SELECT
        USING (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Server can INSERT profiles (used by the onboarding complete path which
-- writes via a server-role connection).
DO $$ BEGIN
    CREATE POLICY learning_profiles_server_insert_policy ON learning_profiles
        FOR INSERT
        WITH CHECK (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Server can UPDATE profiles (used by onboarding complete when a prior
-- profile row already exists and must be replaced).
DO $$ BEGIN
    CREATE POLICY learning_profiles_server_update_policy ON learning_profiles
        FOR UPDATE
        USING (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- onboarding_messages: turn-by-turn history for the onboarding chat.
-- Analogous to chat_messages (migration 004) but keyed on student_id
-- instead of course_id so onboarding has no course dependency.
-- Rows are deleted (cleanup) after the profile is successfully written.
CREATE TABLE IF NOT EXISTS onboarding_messages (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    student_id  UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role        TEXT        NOT NULL CHECK (role IN ('student', 'assistant')),
    content     TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Composite index on student_id + created_at for efficient ordered reads of
-- a student's full onboarding conversation history.
CREATE INDEX IF NOT EXISTS onboarding_messages_student_id_idx
    ON onboarding_messages (student_id, created_at ASC);

GRANT SELECT, INSERT, DELETE ON onboarding_messages TO valory_app;

ALTER TABLE onboarding_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE onboarding_messages FORCE ROW LEVEL SECURITY;

-- Student can read their own onboarding messages.
DO $$ BEGIN
    CREATE POLICY onboarding_messages_student_select_policy ON onboarding_messages
        FOR SELECT
        USING (NULLIF(current_setting('app.current_role', true), '') = 'student'
           AND student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Server can read, insert, and delete onboarding messages (no FOR clause
-- = all DML). This mirrors the chat_messages_server_policy pattern from
-- migration 011 where the server needs full access for the onboarding agent.
DO $$ BEGIN
    CREATE POLICY onboarding_messages_server_policy ON onboarding_messages
        USING (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- users.onboarding_prompted: tracks whether the student has already seen the
-- onboarding nudge (set true on skip or on complete).  Separate from learning_profiles
-- existence to correctly distinguish "never prompted" from "prompted and skipped".
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS onboarding_prompted BOOLEAN NOT NULL DEFAULT false;

COMMIT;
