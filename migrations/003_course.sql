BEGIN;

-- Sprint 3 — Course Module
--
-- Retrofitted for idempotent re-application (the migration 004 idiom):
-- runMigrations re-executes every embedded file on every startup, so each
-- statement must tolerate already-existing objects. Before this retrofit the
-- bare CREATE TYPE below aborted any second application of the migration set —
-- crashing server restarts against an existing database and silently skipping
-- every integration-test package after the first in a `make test-integration` run.

INSERT INTO schema_migrations (version) VALUES ('003_course')
    ON CONFLICT (version) DO NOTHING;

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

CREATE INDEX IF NOT EXISTS courses_student_id_idx   ON courses (student_id);
CREATE INDEX IF NOT EXISTS courses_status_idx       ON courses (status);
CREATE INDEX IF NOT EXISTS courses_created_at_id_idx ON courses (created_at DESC, id DESC);

ALTER TABLE courses ENABLE ROW LEVEL SECURITY;
ALTER TABLE courses FORCE ROW LEVEL SECURITY;

-- courses_student_policy is superseded by migration 004, which drops and
-- recreates it with a NULLIF guard. A duplicate_object guard (rather than
-- DROP + CREATE) is required here so a re-run of this file does not clobber
-- 004's guarded version with this original unguarded one.
DO $$ BEGIN
    CREATE POLICY courses_student_policy ON courses
        USING (student_id = current_setting('app.current_user_id', true)::uuid);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY courses_admin_policy ON courses
        USING (current_setting('app.current_role', true) = 'admin');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

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

GRANT SELECT, INSERT, UPDATE, DELETE ON courses            TO valory_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON syllabi            TO valory_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON homework           TO valory_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON due_date_schedules TO valory_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON agent_token_usage  TO valory_app;

COMMIT;
