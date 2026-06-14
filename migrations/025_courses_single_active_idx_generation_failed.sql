-- migrations/025_courses_single_active_idx_generation_failed.sql
-- Sprint 31 (G3-S1) — Widen courses_single_active_idx to exclude 'generation_failed' (D22).
--
-- @{"req", ["REQ-AGENT-062"]}
--
-- Why this is a separate migration from 024:
--   runMigrations (cmd/server/main.go) sends each whole migration file as a single
--   simple-query string via conn.Conn().PgConn().Exec(ctx, sql). PostgreSQL treats
--   this as one implicit transaction. The 'generation_failed' value is added to the
--   course_status enum in migration 024. PostgreSQL forbids using a newly-added enum
--   value in the same single-statement-string transaction that adds it (SQLSTATE 55P04,
--   "unsafe use of new value of enum type"). By placing the DROP/CREATE of
--   courses_single_active_idx here, in its own file, it runs in a separate Exec call
--   after migration 024 has fully committed, so 'generation_failed' is already visible
--   in the system catalogue and its use in the WHERE predicate is safe.
--
-- What this migration does:
--   Lead decision D22: a course in terminal 'generation_failed' state must not block
--   the student from creating a new course. The original partial unique index from
--   migration 003 predicated on WHERE status NOT IN ('archived', 'completed'), which
--   would treat 'generation_failed' as an in-progress course and prevent a new one.
--   This migration widens the exclusion to also exclude 'generation_failed'.
--
-- Idempotency: DROP IF EXISTS + CREATE UNIQUE INDEX IF NOT EXISTS. The schema_migrations
-- self-record uses ON CONFLICT DO NOTHING. Re-running on an already-migrated database
-- is a safe no-op.
--
-- Rollback:
--   DROP INDEX IF EXISTS courses_single_active_idx;
--   CREATE UNIQUE INDEX IF NOT EXISTS courses_single_active_idx
--       ON courses (student_id) WHERE status NOT IN ('archived', 'completed');
--   DELETE FROM schema_migrations WHERE version = '025_courses_single_active_idx_generation_failed';

BEGIN;

INSERT INTO schema_migrations (version)
    VALUES ('025_courses_single_active_idx_generation_failed')
    ON CONFLICT (version) DO NOTHING;

-- Widen the single-active-course guard to treat 'generation_failed' as terminal
-- (same as 'archived' and 'completed'). The DROP removes the old predicate; the
-- CREATE replaces it with the wider one. IF NOT EXISTS on the CREATE ensures a
-- re-run after a partial failure does not error if the index was already created.
DROP INDEX IF EXISTS courses_single_active_idx;
CREATE UNIQUE INDEX IF NOT EXISTS courses_single_active_idx
    ON courses (student_id)
    WHERE status NOT IN ('archived', 'completed', 'generation_failed');

COMMIT;
