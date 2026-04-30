BEGIN;

CREATE TYPE course_status AS ENUM (
    'intake',
    'syllabus_draft',
    'syllabus_approved',
    'generating',
    'active',
    'archived',
    'completed'
);

CREATE TABLE courses (
    id                     UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    student_id             UUID          NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    title                  TEXT          NOT NULL DEFAULT '',
    topic                  TEXT          NOT NULL,
    status                 course_status NOT NULL DEFAULT 'intake',
    pre_withdrawal_status  course_status,
    created_at             TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX courses_single_active_idx
    ON courses (student_id)
    WHERE status NOT IN ('archived', 'completed');

CREATE INDEX courses_student_id_idx   ON courses (student_id);
CREATE INDEX courses_status_idx       ON courses (status);
CREATE INDEX courses_created_at_id_idx ON courses (created_at DESC, id DESC);

ALTER TABLE courses ENABLE ROW LEVEL SECURITY;
ALTER TABLE courses FORCE ROW LEVEL SECURITY;

CREATE POLICY courses_student_policy ON courses
    USING (student_id = current_setting('app.current_user_id', true)::uuid);

CREATE POLICY courses_admin_policy ON courses
    USING (current_setting('app.current_role', true) = 'admin');

CREATE TABLE syllabi (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id    UUID        NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    content_adoc TEXT        NOT NULL,
    version      INTEGER     NOT NULL DEFAULT 1,
    approved_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX syllabi_course_id_idx ON syllabi (course_id);

CREATE TABLE homework (
    id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id     UUID         NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    section_index INT          NOT NULL,
    title         VARCHAR(255) NOT NULL,
    rubric        TEXT         NOT NULL,
    grade_weight  NUMERIC(4,3) NOT NULL CHECK (grade_weight > 0 AND grade_weight <= 1),
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX homework_course_id_idx ON homework (course_id);

CREATE TABLE due_date_schedules (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id   UUID        NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    homework_id UUID        NOT NULL REFERENCES homework(id) ON DELETE CASCADE,
    due_date    TIMESTAMPTZ NOT NULL,
    agreed_at   TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX due_date_schedules_course_id_idx ON due_date_schedules (course_id);
CREATE UNIQUE INDEX due_date_schedules_unique_hw
    ON due_date_schedules (course_id, homework_id);

CREATE TABLE agent_token_usage (
    id                 UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
    student_id         UUID    NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    course_id          UUID    NOT NULL REFERENCES courses(id)  ON DELETE CASCADE,
    total_tokens_used  BIGINT  NOT NULL DEFAULT 0
                               CHECK (total_tokens_used >= 0),

    CONSTRAINT uq_token_usage_student_course UNIQUE (student_id, course_id)
);

CREATE INDEX idx_token_usage_student_id ON agent_token_usage (student_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON courses            TO valory_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON syllabi            TO valory_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON homework           TO valory_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON due_date_schedules TO valory_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON agent_token_usage  TO valory_app;

COMMIT;
