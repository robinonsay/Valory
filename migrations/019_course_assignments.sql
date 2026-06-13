-- migrations/019_course_assignments.sql
-- Sprint 22 — Admin Course Assignment
-- REQ-ASSIGN-001..011, REQ-SYS-067, REQ-SYS-068
--
-- Creates the course_assignments table and adds the nullable assignment_id FK
-- to courses.  Existing course rows are unaffected (assignment_id IS NULL means
-- student-initiated, the unchanged behaviour).
--
-- No RLS is enabled on course_assignments: the table is admin-only and is
-- never reached by any student code path.  The RequireRole("admin") middleware
-- gate is the enforcement boundary (see SDD-022 §5.5).
--
-- Rollback (additive-only migration, safe to reverse):
--   DROP INDEX IF EXISTS courses_assignment_id_idx;
--   ALTER TABLE courses DROP COLUMN IF EXISTS assignment_id;
--   DROP TABLE IF EXISTS course_assignments;
--   DELETE FROM schema_migrations WHERE version = '019_course_assignments';

BEGIN;

INSERT INTO schema_migrations (version) VALUES ('019_course_assignments')
    ON CONFLICT (version) DO NOTHING;

-- course_assignments captures the admin's curriculum intent (topic, level,
-- parameters).  One row per distinct assignment; many courses rows may FK to it.
CREATE TABLE IF NOT EXISTS course_assignments (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_id    UUID        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    title       TEXT        NOT NULL DEFAULT '',
    topic       TEXT        NOT NULL,
    level       TEXT        NOT NULL DEFAULT 'beginner',
    parameters  JSONB       NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Admin-scoped lookup (list assignments for this admin) and time-ordered cursor.
CREATE INDEX IF NOT EXISTS course_assignments_admin_id_idx
    ON course_assignments (admin_id);
CREATE INDEX IF NOT EXISTS course_assignments_created_at_idx
    ON course_assignments (created_at DESC);

-- valory_app is the login role used by the application.  Full DML is granted so
-- the admin handler can INSERT, SELECT, UPDATE, and DELETE via the pool without
-- needing superuser or the server-role path.  Students never touch this table
-- (no student code path issues a query against it).
GRANT SELECT, INSERT, UPDATE, DELETE ON course_assignments TO valory_app;

-- courses.assignment_id links each per-student course instance back to the
-- assignment that created it.  NULL means student-initiated (existing behaviour).
-- ON DELETE SET NULL: deleting an assignment preserves the already-generated
-- course rows so students are not affected; the FK becomes informational only.
ALTER TABLE courses
    ADD COLUMN IF NOT EXISTS assignment_id UUID
        REFERENCES course_assignments(id) ON DELETE SET NULL;

-- Partial index: only indexes rows that actually have an assignment_id so the
-- index is small and the admin "list courses for assignment" query is fast.
CREATE INDEX IF NOT EXISTS courses_assignment_id_idx
    ON courses (assignment_id)
    WHERE assignment_id IS NOT NULL;

-- RLS coverage note: no policy change is required.
--
-- courses_student_policy (migration 004, NULLIF-safe) checks student_id, not
-- assignment_id.  An assigned course has student_id = <that student>, so
-- student A sees their own row and cannot see student B's row — identical to
-- self-initiated courses.
--
-- courses_admin_policy (migration 003) allows any row when
-- app.current_role = 'admin', so admin handlers can query all assigned rows
-- without modification.
--
-- courses_server_select_policy / courses_server_update_policy (migration 005)
-- allow all rows when app.current_role = 'server', so the AgentRunner's
-- pollAndGenerate loop picks up assigned courses at syllabus_approved without
-- any runner change.
--
-- See SDD-022 §5 and §6.1 for the full RLS probe test specifications.

COMMIT;
