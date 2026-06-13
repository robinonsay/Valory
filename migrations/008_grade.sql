BEGIN;

INSERT INTO schema_migrations (version) VALUES ('008_grade')
    ON CONFLICT (version) DO NOTHING;

CREATE TABLE IF NOT EXISTS grades (
    id                     UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    submission_id          UUID         NOT NULL REFERENCES submissions(id) ON DELETE CASCADE,
    homework_id            UUID         NOT NULL REFERENCES homework(id)    ON DELETE CASCADE,
    student_id             UUID         NOT NULL REFERENCES users(id)       ON DELETE CASCADE,
    course_id              UUID         NOT NULL REFERENCES courses(id)     ON DELETE CASCADE,
    raw_score              NUMERIC(5,2) NOT NULL CHECK (raw_score BETWEEN 0 AND 100),
    late_days              INT          NOT NULL DEFAULT 0 CHECK (late_days >= 0),
    late_penalty_rate      NUMERIC(5,4) NOT NULL DEFAULT 0,  -- e.g. 0.05 = 5% per day
    late_penalty_amount    NUMERIC(5,2) NOT NULL DEFAULT 0,
    badge_waiver_applied   BOOLEAN      NOT NULL DEFAULT FALSE,
    badge_improvement      NUMERIC(5,2) NOT NULL DEFAULT 0,
    final_score            NUMERIC(5,2) NOT NULL CHECK (final_score BETWEEN 0 AND 100),
    graded_at              TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT uq_grade_submission UNIQUE (submission_id)
);

CREATE INDEX IF NOT EXISTS grades_student_id_idx  ON grades (student_id);
CREATE INDEX IF NOT EXISTS grades_course_id_idx   ON grades (course_id);
CREATE INDEX IF NOT EXISTS grades_homework_id_idx ON grades (homework_id);

-- RLS: students may only read their own grades
ALTER TABLE grades ENABLE ROW LEVEL SECURITY;
ALTER TABLE grades FORCE ROW LEVEL SECURITY;

-- NULLIF guard on the empty-string GUC (see 006_badge.sql for the rationale).
-- DROP+CREATE so pre-fix databases converge to the safe policy.
DROP POLICY IF EXISTS grades_student_select_policy ON grades;
DO $$ BEGIN
    CREATE POLICY grades_student_select_policy ON grades
        FOR SELECT
        USING (student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY grades_server_policy ON grades
        FOR ALL
        USING (current_setting('app.current_role', true) = 'server')
        WITH CHECK (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY grades_admin_select_policy ON grades
        FOR SELECT
        USING (current_setting('app.current_role', true) = 'admin');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- The server role needs INSERT + UPDATE (grading agent writes the grade)
GRANT SELECT, INSERT, UPDATE ON grades TO valory_app;

COMMIT;
