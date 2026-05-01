BEGIN;

INSERT INTO schema_migrations (version) VALUES ('007_submission')
    ON CONFLICT (version) DO NOTHING;

DO $$ BEGIN
    CREATE TYPE submission_format AS ENUM ('latex', 'markdown', 'asciidoc');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE TYPE grading_status AS ENUM ('pending', 'graded', 'failed');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE TABLE IF NOT EXISTS submissions (
    id               UUID              PRIMARY KEY DEFAULT gen_random_uuid(),
    homework_id      UUID              NOT NULL REFERENCES homework(id) ON DELETE CASCADE,
    student_id       UUID              NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    format           submission_format NOT NULL,
    file_path        TEXT              NOT NULL,
    file_size_bytes  BIGINT            NOT NULL CHECK (file_size_bytes > 0),
    submitted_at     TIMESTAMPTZ       NOT NULL DEFAULT now(),
    raw_score        NUMERIC(5,2),     -- null until graded by agent
    grading_status   grading_status    NOT NULL DEFAULT 'pending',
    CONSTRAINT uq_submission_hw_student UNIQUE (homework_id, student_id)
);

CREATE INDEX IF NOT EXISTS submissions_student_id_idx    ON submissions (student_id);
CREATE INDEX IF NOT EXISTS submissions_homework_id_idx   ON submissions (homework_id);
CREATE INDEX IF NOT EXISTS submissions_grading_status_idx ON submissions (grading_status)
    WHERE grading_status = 'pending';

-- FK from student_badges.redeemed_for_submission_id (badge module migration ran first)
-- Only add if student_badges table exists (006_badge.sql may have run)
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'student_badges') THEN
        ALTER TABLE student_badges
            ADD CONSTRAINT fk_student_badges_submission
            FOREIGN KEY (redeemed_for_submission_id) REFERENCES submissions(id) ON DELETE SET NULL;
    END IF;
END $$;

-- RLS: students may only see their own submissions.
-- FORCE is required because valory_app is the table owner and would otherwise
-- bypass RLS entirely even when connected as the application role.
ALTER TABLE submissions ENABLE ROW LEVEL SECURITY;
ALTER TABLE submissions FORCE ROW LEVEL SECURITY;

DO $$ BEGIN
    CREATE POLICY submissions_student_select_policy ON submissions
        FOR SELECT
        USING (student_id = (current_setting('app.current_user_id', true))::uuid);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY submissions_student_insert_policy ON submissions
        FOR INSERT
        WITH CHECK (student_id = (current_setting('app.current_user_id', true))::uuid);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Allow server background processes (agent grading runner) to read and update submissions
DO $$ BEGIN
    CREATE POLICY submissions_server_select_policy ON submissions
        FOR SELECT
        USING (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY submissions_server_update_policy ON submissions
        FOR UPDATE
        USING (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

GRANT SELECT, INSERT, UPDATE ON submissions TO valory_app;

COMMIT;
