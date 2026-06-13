BEGIN;

-- Migration 021 — Admin oversight RLS policies for syllabi, homework, and submissions.
--
-- WHY this migration exists:
--
-- The admin module's GET /admin/courses/{id}/overview endpoint (REQ-ADMIN-011) needs
-- to read a student's full course material — syllabus, homework, and submissions — on
-- behalf of an authenticated admin. Three tables were previously missing admin SELECT
-- policies:
--
--   syllabi    — no RLS at all (FORCE ROW LEVEL SECURITY was never enabled)
--   homework   — no RLS at all (FORCE ROW LEVEL SECURITY was never enabled)
--   submissions — student + server policies only (migration 007); no admin policy
--
-- This migration:
--   1. Enables and forces RLS on syllabi and homework (they had no RLS at all).
--   2. Adds student- and server-side policies for syllabi and homework so that the
--      existing application paths continue to work correctly after RLS is forced.
--   3. Adds read-only (SELECT-scoped, USING-only, no WITH CHECK) admin policies for
--      syllabi, homework, and submissions.
--
-- Pattern: mirrors grades_admin_select_policy (008_grade.sql:50) and
-- lesson_content_admin_policy (004_agent.sql:188).
--
-- Security: admin policies grant SELECT only. No INSERT/UPDATE/DELETE is granted to
-- the admin role on any of these tables.
--
-- Idempotency: every CREATE POLICY is wrapped in a DO $$ ... EXCEPTION WHEN
-- duplicate_object THEN NULL; END $$; block so the migration is re-runnable on
-- an existing database without error.
--
-- NULLIF guard: the pool's AfterRelease hook resets all session GUCs to '' on every
-- returned connection. A bare ''::uuid cast in USING raises "invalid input syntax for
-- type uuid". NULLIF maps '' to NULL so the equality evaluates to FALSE (deny) rather
-- than raising, consistent with migrations 006, 007, 008, and 009.

INSERT INTO schema_migrations (version) VALUES ('021_admin_oversight_rls')
    ON CONFLICT (version) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Section 1: syllabi — enable RLS and add student, server, and admin policies.
--
-- syllabi.course_id → courses.student_id is the ownership chain. The student
-- policy uses a subquery on courses (which itself has FORCE RLS) so we cannot
-- rely on a bare column comparison; the same subquery pattern as lesson_content
-- (004_agent.sql:181-183) is used here.
-- ---------------------------------------------------------------------------
ALTER TABLE syllabi ENABLE ROW LEVEL SECURITY;
ALTER TABLE syllabi FORCE ROW LEVEL SECURITY;

-- Student read policy: student may only read syllabi for their own courses.
DO $$ BEGIN
    CREATE POLICY syllabi_student_select_policy ON syllabi
        FOR SELECT
        USING (course_id IN (
            SELECT id FROM courses
            WHERE student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid
        ));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Server write policy: allows the generation agent (app.current_role='server')
-- to insert/update syllabi rows without restriction.
DO $$ BEGIN
    CREATE POLICY syllabi_server_policy ON syllabi
        FOR ALL
        USING (current_setting('app.current_role', true) = 'server')
        WITH CHECK (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Admin read-only policy: admin may SELECT any syllabus row. No WITH CHECK —
-- this is intentionally SELECT-only; any admin write attempt is rejected by RLS.
DO $$ BEGIN
    CREATE POLICY syllabi_admin_select_policy ON syllabi
        FOR SELECT
        USING (current_setting('app.current_role', true) = 'admin');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- ---------------------------------------------------------------------------
-- Section 2: homework — enable RLS and add student, server, and admin policies.
--
-- homework.course_id → courses.student_id is the ownership chain, same as
-- syllabi above.
-- ---------------------------------------------------------------------------
ALTER TABLE homework ENABLE ROW LEVEL SECURITY;
ALTER TABLE homework FORCE ROW LEVEL SECURITY;

-- Student read policy: student may only read homework for their own courses.
DO $$ BEGIN
    CREATE POLICY homework_student_select_policy ON homework
        FOR SELECT
        USING (course_id IN (
            SELECT id FROM courses
            WHERE student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid
        ));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Server write policy: allows the generation agent to create homework rows.
DO $$ BEGIN
    CREATE POLICY homework_server_policy ON homework
        FOR ALL
        USING (current_setting('app.current_role', true) = 'server')
        WITH CHECK (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Admin read-only policy: admin may SELECT any homework row. SELECT-only.
DO $$ BEGIN
    CREATE POLICY homework_admin_select_policy ON homework
        FOR SELECT
        USING (current_setting('app.current_role', true) = 'admin');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- ---------------------------------------------------------------------------
-- Section 3: submissions — add admin SELECT policy only.
--
-- submissions already has ENABLE/FORCE ROW LEVEL SECURITY and student + server
-- policies from migrations 007 and 009. We only need to add the admin policy.
-- ---------------------------------------------------------------------------

-- Admin read-only policy: admin may SELECT any submission row. SELECT-only;
-- no WITH CHECK so admin cannot INSERT or UPDATE via this policy.
DO $$ BEGIN
    CREATE POLICY submissions_admin_select_policy ON submissions
        FOR SELECT
        USING (current_setting('app.current_role', true) = 'admin');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

COMMIT;
