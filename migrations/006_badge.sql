BEGIN;

INSERT INTO schema_migrations (version) VALUES ('006_badge')
    ON CONFLICT (version) DO NOTHING;

-- milestone_type: the academic event that triggers the badge award
DO $$ BEGIN
    CREATE TYPE milestone_type AS ENUM (
        'first_submission',       -- first homework submitted (any)
        'submitted_on_time',      -- homework submitted before due date
        'perfect_score',          -- homework graded 100/100
        'all_sections_complete'   -- all lesson sections have feedback submitted
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- reward_type: what the badge grants when earned
DO $$ BEGIN
    CREATE TYPE reward_type AS ENUM (
        'grade_improvement',    -- adds reward_value points to final grade (REQ-BADGE-002)
        'late_penalty_waiver'   -- waives the late penalty on one submission (REQ-BADGE-003)
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE TABLE IF NOT EXISTS badges (
    id            UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
    name          VARCHAR(100)   NOT NULL UNIQUE,
    description   TEXT           NOT NULL,
    milestone     milestone_type NOT NULL,
    reward        reward_type    NOT NULL,
    -- reward_value is the magnitude of the reward; 0 for waiver badges (REQ-BADGE-002)
    reward_value  NUMERIC(5,2)   NOT NULL DEFAULT 0
                                 CHECK (reward_value >= 0),
    created_at    TIMESTAMPTZ    NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS student_badges (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    student_id  UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    badge_id    UUID        NOT NULL REFERENCES badges(id) ON DELETE CASCADE,
    awarded_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- null until the student redeems it against a specific submission (REQ-BADGE-002, REQ-BADGE-003)
    -- FK to submissions added in 007_submission.sql
    redeemed_for_submission_id UUID,
    CONSTRAINT uq_student_badge UNIQUE (student_id, badge_id)
);

CREATE INDEX IF NOT EXISTS student_badges_student_id_idx ON student_badges (student_id);
CREATE INDEX IF NOT EXISTS student_badges_badge_id_idx   ON student_badges (badge_id);

-- RLS protects per-student badge rows the same way all other per-student tables
-- are protected (see migrations/005_server_rls.sql for the pattern).
ALTER TABLE student_badges ENABLE ROW LEVEL SECURITY;
ALTER TABLE student_badges FORCE ROW LEVEL SECURITY;

-- NULLIF guard: the migration/server connection runs with app.current_user_id
-- set to the empty string (pool AfterRelease baseline). A bare ''::uuid raises
-- "invalid input syntax for type uuid" when this policy is evaluated during an
-- FK-validation scan on a fresh database (e.g. 007's ADD CONSTRAINT). NULLIF
-- maps '' to NULL so the cast yields NULL (no match) instead of erroring.
-- DROP+CREATE so databases created before this fix converge to the safe policy.
DROP POLICY IF EXISTS student_badges_student_policy ON student_badges;
DO $$ BEGIN
    CREATE POLICY student_badges_student_policy ON student_badges
        USING (student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY student_badges_server_policy ON student_badges
        FOR ALL
        USING (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY student_badges_admin_policy ON student_badges
        FOR SELECT
        USING (current_setting('app.current_role', true) = 'admin');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Seed the four standard badges (REQ-BADGE-001, REQ-BADGE-002, REQ-BADGE-003)
INSERT INTO badges (name, description, milestone, reward, reward_value) VALUES
    ('First Step',      'Awarded for submitting your first homework assignment.',           'first_submission',      'grade_improvement',  2.0),
    ('Punctual',        'Awarded for submitting a homework assignment before the due date.','submitted_on_time',     'late_penalty_waiver', 0.0),
    ('Perfectionist',   'Awarded for receiving a perfect score on a homework assignment.',  'perfect_score',         'grade_improvement',  5.0),
    ('Course Complete', 'Awarded for submitting feedback on all course sections.',          'all_sections_complete', 'grade_improvement',  3.0)
ON CONFLICT (name) DO NOTHING;

COMMIT;
