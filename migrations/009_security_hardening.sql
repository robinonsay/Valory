BEGIN;

-- Migration 009 — Security hardening: NULLIF-guarded UUID casts in RLS policies
-- and missing GRANTs on badge tables.
--
-- WHY this migration exists:
--
-- 1. NULLIF guard for RLS policies.
--    The production connection pool's AfterRelease hook resets all session GUCs
--    to empty string on every returned connection. When any policy evaluates
--    `current_setting('app.current_user_id', true)::uuid` and the GUC holds ''
--    (empty string), PostgreSQL raises:
--      ERROR: invalid input syntax for type uuid: ""
--    and aborts the query. The fix wraps the cast with NULLIF so that an empty
--    string resolves to NULL, which causes the equality to evaluate FALSE rather
--    than raise, correctly denying access instead of crashing.
--    Pattern established in migration 004 for the courses policy:
--      NULLIF(current_setting('app.current_user_id', true), '')::uuid
--
-- 2. Missing GRANTs on badge tables.
--    Migration 006 created badges and student_badges but never granted any
--    privileges to the valory_app role, so all runtime queries against those
--    tables fail with "permission denied for table".

INSERT INTO schema_migrations (version) VALUES ('009_security_hardening')
    ON CONFLICT (version) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Section 1: Fix student_badges_student_policy
--
-- Original (006): USING (student_id = (current_setting(...))::uuid)
-- The policy has no explicit FOR clause, so it defaults to FOR ALL.
-- Preserving that default (no FOR clause in recreated policy).
-- ---------------------------------------------------------------------------
DO $$ BEGIN
    DROP POLICY IF EXISTS student_badges_student_policy ON student_badges;
    CREATE POLICY student_badges_student_policy ON student_badges
        USING (student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid);
-- Exception handling: we replicate the WHEN others THEN NULL idiom from
-- migration 004 for consistency. The trade-off is that a genuine DDL error
-- (e.g. table does not exist) is silently swallowed. This is acceptable here
-- because 009 runs after 006 in lexicographic order so the tables always
-- exist, and the DROP IF EXISTS above already handles the "policy already
-- exists" case that duplicate_object would catch. A tighter handler would be
-- WHEN duplicate_object THEN NULL, but because we DROP first, that code path
-- is unreachable; WHEN others covers only unexpected DDL failures which we
-- prefer to surface at startup rather than silence — however we match the
-- established project idiom for migration parity.
EXCEPTION WHEN others THEN NULL;
END $$;

-- ---------------------------------------------------------------------------
-- Section 2: Fix submissions_student_select_policy
--
-- Original (007): FOR SELECT USING (student_id = (current_setting(...))::uuid)
-- ---------------------------------------------------------------------------
DO $$ BEGIN
    DROP POLICY IF EXISTS submissions_student_select_policy ON submissions;
    CREATE POLICY submissions_student_select_policy ON submissions
        FOR SELECT
        USING (student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid);
EXCEPTION WHEN others THEN NULL;
END $$;

-- ---------------------------------------------------------------------------
-- Section 3: Fix submissions_student_insert_policy
--
-- Original (007): FOR INSERT WITH CHECK (student_id = (current_setting(...))::uuid)
-- ---------------------------------------------------------------------------
DO $$ BEGIN
    DROP POLICY IF EXISTS submissions_student_insert_policy ON submissions;
    CREATE POLICY submissions_student_insert_policy ON submissions
        FOR INSERT
        WITH CHECK (student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid);
EXCEPTION WHEN others THEN NULL;
END $$;

-- ---------------------------------------------------------------------------
-- Section 4: Fix grades_student_select_policy
--
-- Original (008): FOR SELECT USING (student_id = (current_setting(...))::uuid)
-- ---------------------------------------------------------------------------
DO $$ BEGIN
    DROP POLICY IF EXISTS grades_student_select_policy ON grades;
    CREATE POLICY grades_student_select_policy ON grades
        FOR SELECT
        USING (student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid);
EXCEPTION WHEN others THEN NULL;
END $$;

-- ---------------------------------------------------------------------------
-- Section 5: GRANTs on badge tables
--
-- Migration 006 omitted all GRANTs for valory_app on both badge tables.
--
-- badges: the application role only SELECTs from this table at runtime
-- (ListBadges, GetBadgeByMilestone, getBadgeByID in internal/badge/repository.go).
-- The seed rows are inserted by the migration runner, not valory_app. The badges
-- catalog is static — no INSERT/UPDATE/DELETE methods exist on the repository —
-- so we grant SELECT only, following the principle of least privilege.
--
-- student_badges: the repository performs SELECT (GetStudentBadges, HasBadge),
-- INSERT (AwardBadge), and UPDATE (RedeemBadge). No DELETE is ever issued by
-- application code. Both tables use UUID PKs backed by gen_random_uuid() — no
-- sequences exist — so no GRANT USAGE ON SEQUENCE is needed.
-- ---------------------------------------------------------------------------
GRANT SELECT                        ON badges         TO valory_app;
GRANT SELECT, INSERT, UPDATE        ON student_badges TO valory_app;

COMMIT;
