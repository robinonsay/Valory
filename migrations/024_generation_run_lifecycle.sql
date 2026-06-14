-- migrations/024_generation_run_lifecycle.sql
-- Sprint 31 (G3-S1) — Generation Run Lifecycle: bounded dispatch, retry policy,
-- terminal failure state.
--
-- @{"req", ["REQ-AGENT-061", "REQ-AGENT-062", "REQ-AGENT-063", "REQ-AGENT-064",
--           "REQ-AGENT-065", "REQ-AGENT-066", "REQ-AGENT-067", "REQ-AGENT-069"]}
--
-- Problem addressed: pollAndGenerate re-triggers any course in 'syllabus_approved'
-- every 30 s if any previous run failed (no backoff, no budget, no terminal state).
-- This produced 925 failed content_generation runs in 10 hours during a demo session.
--
-- What this migration adds (all additive — no rows or enum values are removed):
--   1. course_status enum value 'generation_failed' (terminal poller-exclusion state)
--   2. notification_type enum values: 'generation_retrying', 'generation_failed',
--      'generation_recovery'
--   3. courses.generation_attempt_count  — dispatch-level failure counter per course
--   4. courses.next_eligible_at          — backoff timestamp (NULL = immediately eligible)
--   5. Partial unique index on agent_runs (course_id, run_type) WHERE status='running'
--      — atomic "one running run per course per type" claim guard (D19)
--   6. Partial index on courses WHERE status='syllabus_approved' for eligibility scans
--   7. Seed system_config keys generation_backoff_seconds and generation_max_attempts
--
-- NOTE (D22): The widening of courses_single_active_idx to also exclude
-- 'generation_failed' lives in migration 025. It cannot be in this file because
-- runMigrations sends each whole file as a single simple-query string (one implicit
-- transaction), and PostgreSQL forbids using a newly-added enum value in the same
-- transaction that adds it (SQLSTATE 55P04).
--
-- PG 16 assumption: ALTER TYPE … ADD VALUE inside a transaction block is supported
-- on PostgreSQL 16+ (the version Valory targets per docker-compose). These statements
-- are placed OUTSIDE the BEGIN…COMMIT block (before the version stamp) following the
-- migration 022 precedent, to remain safe on PG ≤ 15 and to avoid referencing a
-- newly-added enum value in the same statement that adds it.
--
-- Idempotency: every DDL change is guarded with IF NOT EXISTS or a DO…EXCEPTION
-- block. The schema_migrations self-record uses ON CONFLICT DO NOTHING. Re-running
-- this file on an already-migrated database is a safe no-op.
--
-- Rollback notes (additive-only; requires compensation migration 025):
--   ALTER TABLE courses DROP COLUMN IF EXISTS generation_attempt_count;
--   ALTER TABLE courses DROP COLUMN IF EXISTS next_eligible_at;
--   DROP INDEX IF EXISTS courses_generation_eligible_idx;
--   DROP INDEX IF EXISTS agent_runs_one_running_per_course_type_idx;
--   (Enum values cannot be removed once added; ensure no rows carry generation_failed
--    before reverting. Replace the enum with a new type if required.)

-- ─────────────────────────────────────────────────────────────────────────────
-- 1. Enum additions — OUTSIDE the transaction block (migration 022 precedent).
--    PG 16+ allows these inside a transaction, but placing them before BEGIN
--    keeps the file compatible with PG 15 and avoids the restriction that newly
--    added enum values may not be used in the same command that adds them.
--    IF NOT EXISTS ensures each statement is a no-op on re-run.
-- ─────────────────────────────────────────────────────────────────────────────

-- course_status: terminal generation failure state (REQ-AGENT-062).
-- The poller's eligibility query never selects courses with this status.
-- Recovery requires an explicit re-trigger action (REQ-AGENT-069).
ALTER TYPE course_status ADD VALUE IF NOT EXISTS 'generation_failed';

-- notification_type: three new generation lifecycle notification values.
-- 'generation_failed' here is a notification_type value; it is distinct from
-- the course_status value of the same name (they live in different enum types).
ALTER TYPE notification_type ADD VALUE IF NOT EXISTS 'generation_retrying';
ALTER TYPE notification_type ADD VALUE IF NOT EXISTS 'generation_failed';
ALTER TYPE notification_type ADD VALUE IF NOT EXISTS 'generation_recovery';

-- pipeline_event_type: four new dispatch-lifecycle event values (D23).
-- runner.go emits these via EmitEvent; without them the inserts silently fail
-- because pipeline_events.event_type is typed as pipeline_event_type (not text).
-- 'generation_terminal' is reused by layered_runner.go (settleLayer / D24) as the
-- tree-generation-complete event; keeping a single value avoids consumer confusion.
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'generation_claimed';
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'token_cap_preflight_failed';
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'generation_failed_with_retry';
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'generation_terminal';

-- ─────────────────────────────────────────────────────────────────────────────
-- 2. Version stamp (idempotent) — inside transaction so it rolls back on error
-- ─────────────────────────────────────────────────────────────────────────────

BEGIN;

INSERT INTO schema_migrations (version) VALUES ('024_generation_run_lifecycle')
    ON CONFLICT (version) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────────
-- 3. New columns on courses (REQ-AGENT-065, REQ-AGENT-067)
-- ─────────────────────────────────────────────────────────────────────────────

-- generation_attempt_count: counts dispatch-level failures (content_generation or
-- tree_layer_generation) per course since the course last reached terminal SUCCESS
-- (flat → 'active'; tree → tree-generation-complete). Per D21:
--   • incremented by 1 when a dispatch-level run transitions to 'failed'.
--   • reset to 0 ONLY on terminal SUCCESS, not on intermediate layer completions.
--   • NOT incremented by section_regen or terminated runs.
-- Default 0 means all existing courses are eligible immediately after migration.
ALTER TABLE courses
    ADD COLUMN IF NOT EXISTS generation_attempt_count INTEGER NOT NULL DEFAULT 0;

-- next_eligible_at: the earliest timestamp at which this course may be dispatched
-- again. NULL means immediately eligible (no backoff in effect). Set to
-- NOW() + backoff_window when a dispatch-level run fails; cleared on terminal
-- SUCCESS or explicit re-trigger. The eligibility query uses this to enforce the
-- backoff window between automatic retries (REQ-AGENT-067).
ALTER TABLE courses
    ADD COLUMN IF NOT EXISTS next_eligible_at TIMESTAMPTZ;

-- ─────────────────────────────────────────────────────────────────────────────
-- 4. Unique partial index on agent_runs — atomic claim guard (REQ-AGENT-066, D19)
-- ─────────────────────────────────────────────────────────────────────────────
-- Each course may have at most one 'running' run of each type at a time. A
-- partial index on status='running' keeps the index small (only active rows).
-- An INSERT that violates this constraint is rejected with SQLSTATE 23505
-- (unique_violation), which CreateRun interprets as ErrRunAlreadyClaimed.
-- course_id IS NOT NULL guard is required because agent_runs.course_id is nullable
-- (admin draft runs have course_id NULL and must not conflict with each other).
CREATE UNIQUE INDEX IF NOT EXISTS agent_runs_one_running_per_course_type_idx
    ON agent_runs (course_id, run_type)
    WHERE status = 'running' AND course_id IS NOT NULL;

-- ─────────────────────────────────────────────────────────────────────────────
-- 5. Eligibility covering index on courses (REQ-AGENT-063)
-- ─────────────────────────────────────────────────────────────────────────────
-- Accelerates ListUntriggeredApprovals at every poll tick. The partial index
-- predicate on status='syllabus_approved' limits the index to only the rows the
-- eligibility query touches, keeping index maintenance cheap. The included
-- columns (generation_attempt_count, next_eligible_at) allow index-only scans
-- for the backoff and max-attempts filter predicates.
CREATE INDEX IF NOT EXISTS courses_generation_eligible_idx
    ON courses (status, generation_attempt_count, next_eligible_at)
    WHERE status = 'syllabus_approved';

-- ─────────────────────────────────────────────────────────────────────────────
-- 6. Seed system_config keys (OQ-4 resolved: seeded for safe first-run behaviour)
-- ─────────────────────────────────────────────────────────────────────────────
-- Mirrors the pattern established by migration 002 (correction_loop_max_iterations)
-- and migration 022 (enable_tree_mode_courses). ON CONFLICT DO NOTHING preserves
-- any operator-set values already present in the table.
-- generation_backoff_seconds: seconds to wait between automatic retry attempts.
-- generation_max_attempts: number of dispatch-level failures before terminal state.
INSERT INTO system_config (key, value)
VALUES
    ('generation_backoff_seconds', '600'),
    ('generation_max_attempts',    '5')
ON CONFLICT (key) DO NOTHING;

COMMIT;
