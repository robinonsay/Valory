BEGIN;

-- Migration 023 — Restore student write access to their own syllabi.
--
-- Migration 021 enabled FORCE ROW LEVEL SECURITY on `syllabi` and added a
-- student SELECT policy, a server ALL policy, and an admin SELECT policy. It did
-- NOT add a student *write* policy, effectively modeling syllabi as
-- server-write-only. That contradicts the existing flat-course flow, where a
-- student writes their own syllabi on the request-scoped (RLS-GUC) connection:
--   - CourseService.RequestModification -> InsertSyllabusTx  (INSERT new version)
--   - CourseService.ApproveSyllabus     -> ApproveSyllabusTx (UPDATE approved_at)
-- Both run under app.current_role='student', so after 021 they were denied by
-- RLS ("new row violates row-level security policy for table syllabi"),
-- surfacing as a 500 on POST /courses/{id}/syllabus/modification and the
-- syllabus-approve path. (Initial generation is unaffected: the agent writes as
-- app.current_role='server', which the server policy already permits.)
--
-- Fix: add student INSERT and UPDATE policies on syllabi scoped to course
-- ownership, mirroring the syllabi_student_select_policy added in 021 and the way
-- `courses` and `submissions` already let students write their own rows. Student
-- isolation is preserved by the ownership subquery (a student can only write
-- syllabi for a course whose courses.student_id matches their own user id); the
-- admin policy stays read-only and the server policy is unchanged.
--
-- The ownership subquery uses NULLIF(current_setting(...,'true'),'')::uuid so an
-- empty/unset GUC casts to NULL (no rows) instead of raising, matching 021.
--
-- Idempotency: each CREATE POLICY is wrapped in DO $$ ... EXCEPTION WHEN
-- duplicate_object THEN NULL, and the migration self-records via ON CONFLICT DO
-- NOTHING, so re-running (the runner re-applies every file on each boot) is safe.

INSERT INTO schema_migrations (version) VALUES ('023_syllabi_student_write_rls')
    ON CONFLICT (version) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Student INSERT policy: a student may insert a syllabus row for a course they
-- own (a new version produced by RequestModification). FOR INSERT takes a
-- WITH CHECK clause only (there is no existing row to test with USING).
-- ---------------------------------------------------------------------------
DO $$ BEGIN
    CREATE POLICY syllabi_student_insert_policy ON syllabi
        FOR INSERT
        WITH CHECK (course_id IN (
            SELECT id FROM courses
            WHERE student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid
        ));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- ---------------------------------------------------------------------------
-- Student UPDATE policy: a student may update a syllabus row for a course they
-- own (e.g. ApproveSyllabus setting approved_at). FOR UPDATE takes both USING
-- (which existing rows are updatable) and WITH CHECK (the post-update row must
-- still belong to the student, preventing reparenting to another course).
-- ---------------------------------------------------------------------------
DO $$ BEGIN
    CREATE POLICY syllabi_student_update_policy ON syllabi
        FOR UPDATE
        USING (course_id IN (
            SELECT id FROM courses
            WHERE student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid
        ))
        WITH CHECK (course_id IN (
            SELECT id FROM courses
            WHERE student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid
        ));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

COMMIT;
