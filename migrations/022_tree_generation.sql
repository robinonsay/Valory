-- migrations/022_tree_generation.sql
-- Sprint 28 (G2-S2) — Knowledge-Tree Schema Foundation
--
-- @{"req", ["REQ-AGENT-027", "REQ-AGENT-028", "REQ-AGENT-029", "REQ-AGENT-030",
--           "REQ-AGENT-031", "REQ-AGENT-032", "REQ-AGENT-033", "REQ-AGENT-034",
--           "REQ-AGENT-035", "REQ-AGENT-036", "REQ-AGENT-037", "REQ-AGENT-038",
--           "REQ-AGENT-039", "REQ-AGENT-040", "REQ-AGENT-041", "REQ-AGENT-042",
--           "REQ-AGENT-043", "REQ-AGENT-044", "REQ-AGENT-045", "REQ-AGENT-046",
--           "REQ-AGENT-047", "REQ-AGENT-048", "REQ-AGENT-049", "REQ-AGENT-050",
--           "REQ-AGENT-051", "REQ-AGENT-052", "REQ-AGENT-053", "REQ-AGENT-054",
--           "REQ-AGENT-055", "REQ-AGENT-056", "REQ-AGENT-057", "REQ-AGENT-058",
--           "REQ-AGENT-059", "REQ-AGENT-060", "REQ-SYS-072", "REQ-SYS-075"]}
--
-- Creates the full knowledge-tree schema.  All changes are additive so existing
-- flat courses and data rows are never touched.  Every CREATE is guarded with IF
-- NOT EXISTS and every policy creation is wrapped in a duplicate_object guard so
-- this file can be applied more than once without error (idempotent re-run).
--
-- STRUCTURE OF THIS FILE
-- ─────────────────────
-- 1. Schema migration version stamp.
-- 2. ALTER TYPE … ADD VALUE statements for existing enums.
--    These run OUTSIDE the BEGIN…COMMIT block because ALTER TYPE ADD VALUE
--    implicitly commits in PostgreSQL when it needs to update the system
--    catalogue; placing them before BEGIN keeps the execution context clean
--    and matches the pattern documented in state-machine TDD §13.
-- 3. BEGIN … COMMIT block containing all DDL, RLS, trigger, and config work.
--
-- Rollback (additive only, reversible):
--   DROP TABLE IF EXISTS node_chats CASCADE;
--   DROP TABLE IF EXISTS draft_nodes CASCADE;
--   DROP TABLE IF EXISTS course_drafts CASCADE;
--   DROP TABLE IF EXISTS course_nodes CASCADE;
--   ALTER TABLE assignments DROP COLUMN IF EXISTS draft_id;
--   ALTER TABLE agent_token_usage DROP COLUMN IF EXISTS draft_id;
--     …(restore NOT NULL constraints manually after verifying no null rows)…
--   ALTER TABLE agent_runs DROP COLUMN IF EXISTS draft_id;
--   ALTER TABLE courses DROP COLUMN IF EXISTS current_layer;
--   ALTER TABLE courses DROP COLUMN IF EXISTS tree_mode;
--   DROP TYPE IF EXISTS node_status_enum;
--   DROP TYPE IF EXISTS node_type_enum;
--   DELETE FROM schema_migrations WHERE version = '022_tree_generation';

-- ─────────────────────────────────────────────────────────────────────────────
-- 1. Version stamp (idempotent)
-- ─────────────────────────────────────────────────────────────────────────────

INSERT INTO schema_migrations (version) VALUES ('022_tree_generation')
    ON CONFLICT (version) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────────
-- 2. Enum value additions — MUST run OUTSIDE the transaction block.
--    PostgreSQL 12+ allows ADD VALUE inside a transaction only when the new
--    value is not used in the same transaction; however, placing these before
--    BEGIN is cleaner and matches the state-machine TDD §13 guidance.
--    All statements are no-ops if the value already exists (IF NOT EXISTS).
-- ─────────────────────────────────────────────────────────────────────────────

-- course_status: two new lifecycle states used only by tree-mode courses.
-- Flat courses never enter these states; the existing status graph is unchanged.
ALTER TYPE course_status ADD VALUE IF NOT EXISTS 'awaiting_layer_approval' AFTER 'active';
ALTER TYPE course_status ADD VALUE IF NOT EXISTS 'awaiting_regeneration'   AFTER 'awaiting_layer_approval';

-- agent_run_type: two new run categories for the layered runner.
ALTER TYPE agent_run_type ADD VALUE IF NOT EXISTS 'tree_layer_generation' AFTER 'grading';
ALTER TYPE agent_run_type ADD VALUE IF NOT EXISTS 'node_generation'       AFTER 'tree_layer_generation';

-- pipeline_event_type: eleven layer-level events (state-machine TDD §10)
-- plus six node-scoped events (api-sse TDD §5.3.5).
-- Listed in logical emission order to aid grep; emitted in any sequence.
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'layer_generation_started'   AFTER 'token_cap_reached';
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'layer_node_generating'      AFTER 'layer_generation_started';
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'layer_node_generated'       AFTER 'layer_node_generating';
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'layer_node_failed'          AFTER 'layer_node_generated';
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'layer_awaiting_review'      AFTER 'layer_node_failed';
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'layer_approved'             AFTER 'layer_awaiting_review';
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'layer_node_approved'        AFTER 'layer_approved';
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'layer_node_rejected'        AFTER 'layer_node_approved';
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'layer_regeneration_started' AFTER 'layer_node_rejected';
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'layer_generation_failed'    AFTER 'layer_regeneration_started';
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'tree_generation_complete'   AFTER 'layer_generation_failed';
-- Node-scoped SSE event types (api-sse §5.3.5) — de-duplicated from the
-- layer-level list; none of the names below appeared above.
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'node_generating'      AFTER 'tree_generation_complete';
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'node_ready'           AFTER 'node_generating';
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'node_approved'        AFTER 'node_ready';
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'node_rejected'        AFTER 'node_approved';
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'node_error'           AFTER 'node_rejected';
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'layer_awaiting_expand' AFTER 'node_error';

-- ─────────────────────────────────────────────────────────────────────────────
-- 3. Main DDL transaction
-- ─────────────────────────────────────────────────────────────────────────────

BEGIN;

-- ── 3.1  New ENUM types ───────────────────────────────────────────────────────

-- node_type_enum: the five layers of the knowledge tree (data-model TDD §4.1).
DO $$ BEGIN
    CREATE TYPE node_type_enum AS ENUM (
        'root',          -- Layer 0: one per course; holds the course intent/topic
        'syllabus',      -- Layer 1: one per course; the structured outline summary
        'section_goal',  -- Layer 2: one per syllabus section; the learning objective
        'concept',       -- Layer 3: one atomic concept within a section_goal
        'content'        -- Layer 4: one AsciiDoc content piece attached to a concept
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- node_status_enum: lifecycle of a single node (data-model TDD §4.1).
-- Six values; 'failed' is a terminal state; 'rejected' enables regeneration.
DO $$ BEGIN
    CREATE TYPE node_status_enum AS ENUM (
        'pending',          -- created but not yet picked up by the agent pipeline
        'generating',       -- agent pipeline is actively working on this node
        'awaiting_review',  -- content generated; waiting for review or human approval
        'approved',         -- passed review; visible to the student
        'rejected',         -- failed human review; stored feedback triggers regeneration
        'failed'            -- unrecoverable error (token cap, max iterations, timeout)
                            -- terminal; no automatic retry; triggers escalation event
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- ── 3.2  course_nodes ─────────────────────────────────────────────────────────

-- Every node in a student's knowledge tree (data-model TDD §4.2).
-- parent_id self-FK enforces tree structure; cross-course violations are caught
-- by the trigger in §3.7.  tokens_used is a nullable reporting column (Lead D8)
-- populated by the LayeredRunner after each generation call.
CREATE TABLE IF NOT EXISTS course_nodes (
    id          UUID             PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id   UUID             NOT NULL
                                 REFERENCES courses(id) ON DELETE CASCADE,
    parent_id   UUID             REFERENCES course_nodes(id) ON DELETE CASCADE,
    node_type   node_type_enum   NOT NULL,
    ordering    INTEGER          NOT NULL DEFAULT 0
                                 CHECK (ordering >= 0),
    status      node_status_enum NOT NULL DEFAULT 'pending',
    payload     JSONB            NOT NULL DEFAULT '{}',
    tokens_used INTEGER,         -- optional per-node cost reporting (Lead D8)
    created_at  TIMESTAMPTZ      NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ      NOT NULL DEFAULT now(),

    -- Root node must have no parent (and only root has no parent).
    CONSTRAINT course_nodes_root_has_no_parent
        CHECK (node_type != 'root' OR parent_id IS NULL),
    CONSTRAINT course_nodes_non_root_has_parent
        CHECK (node_type  = 'root' OR parent_id IS NOT NULL)
);

-- Children lookup: covers WHERE parent_id = $1 ORDER BY ordering ASC.
CREATE INDEX IF NOT EXISTS course_nodes_parent_id_idx
    ON course_nodes (parent_id, ordering ASC)
    WHERE parent_id IS NOT NULL;

-- Layer lookup: covers WHERE course_id = $1 AND node_type = $2.
CREATE INDEX IF NOT EXISTS course_nodes_course_type_idx
    ON course_nodes (course_id, node_type);

-- Sibling ordering uniqueness: prevents duplicate ordering positions among
-- children of the same parent.  Partial so multiple root rows (different
-- courses) do not conflict with each other on (course_id, NULL, 0).
CREATE UNIQUE INDEX IF NOT EXISTS course_nodes_sibling_order_idx
    ON course_nodes (course_id, parent_id, ordering)
    WHERE parent_id IS NOT NULL;

-- Root uniqueness: at most one root node per course.
CREATE UNIQUE INDEX IF NOT EXISTS course_nodes_one_root_idx
    ON course_nodes (course_id)
    WHERE node_type = 'root';

-- ── 3.3  course_drafts ────────────────────────────────────────────────────────

-- Admin-authored draft trees (api-sse TDD §4.1).
-- status is TEXT (not enum) per Lead decision D4; valid text values are
-- 'draft', 'published', 'generating', 'awaiting_layer_approval',
-- 'awaiting_regeneration'.
-- current_layer mirrors courses.current_layer for poller queries (Lead D4).
CREATE TABLE IF NOT EXISTS course_drafts (
    id                         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_id                   UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title                      TEXT        NOT NULL,
    topic                      TEXT        NOT NULL,
    level                      TEXT        NOT NULL,   -- beginner | intermediate | advanced
    parameters                 JSONB       NOT NULL DEFAULT '{}',
    status                     TEXT        NOT NULL DEFAULT 'draft',
    current_layer              node_type_enum,         -- Lead D4: tracks active layer
    published_to_assignment_id UUID        REFERENCES course_assignments(id),
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ── 3.4  draft_nodes ──────────────────────────────────────────────────────────

-- Admin-draft node storage (api-sse TDD §4.2).  Kept separate from course_nodes
-- so student RLS policies on course_nodes are not complicated by admin content.
-- Uses TEXT for node_type and status (not the enums) for flexibility during the
-- admin-authoring flow; the application layer enforces valid values.
CREATE TABLE IF NOT EXISTS draft_nodes (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    draft_id    UUID        NOT NULL REFERENCES course_drafts(id) ON DELETE CASCADE,
    parent_id   UUID        REFERENCES draft_nodes(id) ON DELETE CASCADE,
    node_type   TEXT        NOT NULL,  -- same taxonomy as course_nodes
    ordering    INTEGER     NOT NULL DEFAULT 0,
    status      TEXT        NOT NULL DEFAULT 'pending',
    payload     JSONB       NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- draft_id lookup: covers WHERE draft_id = $1 (poller and generation pipeline).
CREATE INDEX IF NOT EXISTS draft_nodes_draft_id_idx ON draft_nodes (draft_id);

-- Children lookup: covers WHERE parent_id = $1 ORDER BY ordering ASC.
CREATE INDEX IF NOT EXISTS draft_nodes_parent_id_idx ON draft_nodes (parent_id, ordering ASC) WHERE parent_id IS NOT NULL;

-- Sibling ordering uniqueness: prevents duplicate ordering positions among children
-- of the same draft parent.  Partial so root rows (NULL parent_id) do not conflict.
CREATE UNIQUE INDEX IF NOT EXISTS draft_nodes_sibling_order_idx ON draft_nodes (draft_id, parent_id, ordering) WHERE parent_id IS NOT NULL;

-- ── 3.5  node_chats ───────────────────────────────────────────────────────────

-- Per-node Chair conversation turns for both student and admin paths
-- (data-model TDD §4.6).  Two nullable, typed FKs with ON DELETE CASCADE ensure
-- referential integrity; a CHECK constraint enforces exactly-one-owner invariant.
-- node_chats is created AFTER course_nodes and draft_nodes so its FKs are valid.
CREATE TABLE IF NOT EXISTS node_chats (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    course_node_id   UUID        REFERENCES course_nodes(id) ON DELETE CASCADE,
    draft_node_id    UUID        REFERENCES draft_nodes(id)  ON DELETE CASCADE,
    role             TEXT        NOT NULL CHECK (role IN ('user', 'assistant')),
    content          TEXT        NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Exactly one of the two FK columns must be set.
    CONSTRAINT node_chats_exactly_one_owner
        CHECK (
            (course_node_id IS NOT NULL AND draft_node_id IS NULL)
            OR
            (course_node_id IS NULL     AND draft_node_id IS NOT NULL)
        )
);

-- Ordered retrieval of a student node's conversation history.
CREATE INDEX IF NOT EXISTS node_chats_course_node_idx
    ON node_chats (course_node_id, created_at ASC)
    WHERE course_node_id IS NOT NULL;

-- Ordered retrieval of an admin draft node's conversation history.
CREATE INDEX IF NOT EXISTS node_chats_draft_node_idx
    ON node_chats (draft_node_id, created_at ASC)
    WHERE draft_node_id IS NOT NULL;

-- ── 3.6  ALTERs to existing tables ───────────────────────────────────────────

-- courses: tree_mode flag (data-model TDD §5.1) and current_layer tracker
-- (state-machine TDD §3.3).  Both default to non-tree values so every existing
-- course row remains unaffected (REQ-SYS-072).
ALTER TABLE courses
    ADD COLUMN IF NOT EXISTS tree_mode     BOOLEAN        NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS current_layer node_type_enum;

-- agent_runs: relax course_id NOT NULL and add draft_id for admin-draft SSE
-- anchoring (data-model TDD §4.8).  The context_check constraint enforces that
-- exactly one of (course_id, draft_id) is non-null.
ALTER TABLE agent_runs
    ALTER COLUMN course_id DROP NOT NULL,
    ADD COLUMN IF NOT EXISTS draft_id UUID REFERENCES course_drafts(id) ON DELETE CASCADE;

-- ADD CONSTRAINT is not IF NOT EXISTS in Postgres 14; guard with DO block.
DO $$ BEGIN
    ALTER TABLE agent_runs
        ADD CONSTRAINT agent_runs_context_check CHECK (
            (course_id IS NOT NULL AND draft_id IS NULL)
            OR
            (course_id IS NULL     AND draft_id IS NOT NULL)
        );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Index on draft_id for poller and SSE queries that filter agent_runs by draft.
CREATE INDEX IF NOT EXISTS agent_runs_draft_id_idx ON agent_runs (draft_id) WHERE draft_id IS NOT NULL;

-- agent_token_usage: relax student_id / course_id NOT NULL and add draft_id for
-- admin-draft token accounting (data-model TDD §4.7).
--
-- D9 (binding): the unique constraint is named 'uq_token_usage_student_course'
-- (verified in 003_course.sql — defined as an inline CONSTRAINT, not an index).
-- Drop the constraint by name with IF EXISTS, then replace with two partial
-- unique indexes that are themselves guarded with IF NOT EXISTS.
ALTER TABLE agent_token_usage
    ALTER COLUMN student_id DROP NOT NULL,
    ALTER COLUMN course_id  DROP NOT NULL,
    ADD COLUMN IF NOT EXISTS draft_id UUID REFERENCES course_drafts(id) ON DELETE CASCADE;

-- Drop the old table-level unique constraint (not an index; use ALTER TABLE).
-- Postgres has no DROP CONSTRAINT IF EXISTS before version 14; guard with DO.
DO $$ BEGIN
    ALTER TABLE agent_token_usage
        DROP CONSTRAINT uq_token_usage_student_course;
EXCEPTION WHEN undefined_object THEN NULL;
END $$;

-- Exactly-one-context check: either student + course path, or admin draft path.
DO $$ BEGIN
    ALTER TABLE agent_token_usage
        ADD CONSTRAINT agent_token_usage_context_check CHECK (
            (student_id IS NOT NULL AND course_id IS NOT NULL AND draft_id IS NULL)
            OR
            (student_id IS NULL     AND course_id IS NULL     AND draft_id IS NOT NULL)
        );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Replace the old unique constraint with two partial unique indexes.
-- Partial indexes are natively IF NOT EXISTS safe.
CREATE UNIQUE INDEX IF NOT EXISTS agent_token_usage_student_course_idx
    ON agent_token_usage (student_id, course_id)
    WHERE draft_id IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS agent_token_usage_draft_idx
    ON agent_token_usage (draft_id)
    WHERE draft_id IS NOT NULL;

-- assignments (course_assignments): add nullable draft_id FK (Lead D5, api-sse §7.2).
-- Column only; the publish/copy logic is G2-S3.
ALTER TABLE course_assignments
    ADD COLUMN IF NOT EXISTS draft_id UUID REFERENCES course_drafts(id);

-- ── 3.7  Cross-course / cross-draft parent-guard triggers (Lead D2) ───────────

-- Reject inserts/updates where parent_id points to a node in a different course.
-- The self-FK on parent_id does not enforce same-course invariant alone.
CREATE OR REPLACE FUNCTION course_nodes_check_parent_course()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.parent_id IS NOT NULL THEN
        PERFORM 1 FROM course_nodes
        WHERE id = NEW.parent_id AND course_id = NEW.course_id;
        IF NOT FOUND THEN
            RAISE EXCEPTION
                'course_nodes: parent_id % does not belong to course_id %',
                NEW.parent_id, NEW.course_id;
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

-- Guard trigger is idempotent: CREATE OR REPLACE covers the function;
-- the trigger itself is dropped first if it exists so the CREATE does not
-- raise "trigger already exists".
DROP TRIGGER IF EXISTS course_nodes_parent_course_check ON course_nodes;
CREATE TRIGGER course_nodes_parent_course_check
    BEFORE INSERT OR UPDATE OF parent_id, course_id ON course_nodes
    FOR EACH ROW EXECUTE FUNCTION course_nodes_check_parent_course();

-- Analogous guard for draft_nodes: reject cross-draft parent assignments.
CREATE OR REPLACE FUNCTION draft_nodes_check_parent_draft()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.parent_id IS NOT NULL THEN
        PERFORM 1 FROM draft_nodes
        WHERE id = NEW.parent_id AND draft_id = NEW.draft_id;
        IF NOT FOUND THEN
            RAISE EXCEPTION
                'draft_nodes: parent_id % does not belong to draft_id %',
                NEW.parent_id, NEW.draft_id;
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS draft_nodes_parent_draft_check ON draft_nodes;
CREATE TRIGGER draft_nodes_parent_draft_check
    BEFORE INSERT OR UPDATE OF parent_id, draft_id ON draft_nodes
    FOR EACH ROW EXECUTE FUNCTION draft_nodes_check_parent_draft();

-- ── 3.8  RLS: course_nodes ────────────────────────────────────────────────────

ALTER TABLE course_nodes ENABLE ROW LEVEL SECURITY;
ALTER TABLE course_nodes FORCE ROW LEVEL SECURITY;

-- Student may READ nodes belonging to their own courses (read-only; no INSERT/UPDATE).
-- NULLIF guard: empty GUC string casts to NULL → comparison always false →
-- zero rows returned → RLS bug surfaces even with a superuser test connection.
-- Mirrors lesson_content_student_policy in migration 004.
-- FOR SELECT only: students interact via the chat/feedback APIs, never write
-- course_nodes directly (data-model TDD §6 point 3).
-- D10 hardening: DROP IF EXISTS first so a re-run over an already-migrated DB
-- replaces the old FOR ALL + WITH CHECK policy with the correct FOR SELECT policy.
DROP POLICY IF EXISTS course_nodes_student_policy ON course_nodes;
DO $$ BEGIN
    CREATE POLICY course_nodes_student_policy ON course_nodes
        FOR SELECT
        USING (
            course_id IN (
                SELECT id FROM courses
                WHERE student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid
            )
        );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Admin may read and write all nodes across all courses.
DO $$ BEGIN
    CREATE POLICY course_nodes_admin_policy ON course_nodes
        USING (current_setting('app.current_role', true) = 'admin');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Server role may insert (agent pipeline writes nodes).
DO $$ BEGIN
    CREATE POLICY course_nodes_server_policy ON course_nodes
        FOR INSERT
        WITH CHECK (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Server role may update node status and payload (e.g., pending → generating →
-- awaiting_review → approved/rejected/failed).
DO $$ BEGIN
    CREATE POLICY course_nodes_server_update_policy ON course_nodes
        FOR UPDATE
        USING     (current_setting('app.current_role', true) = 'server')
        WITH CHECK (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Server role must also SELECT nodes to drive the LayeredRunner pipeline; without
-- this policy the server role sees zero rows even after INSERT (FORCE RLS applies
-- to all commands including SELECT).  Mirrors 005_server_rls.sql pattern.
DO $$ BEGIN
    CREATE POLICY course_nodes_server_select_policy ON course_nodes
        FOR SELECT
        USING (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

GRANT SELECT, INSERT, UPDATE, DELETE ON course_nodes TO valory_app;

-- ── 3.9  RLS: course_drafts ───────────────────────────────────────────────────

ALTER TABLE course_drafts ENABLE ROW LEVEL SECURITY;
ALTER TABLE course_drafts FORCE ROW LEVEL SECURITY;

-- Admin owns their own drafts: admin_id must match current_user_id with NULLIF
-- guard, and current_role must be 'admin'.
DO $$ BEGIN
    CREATE POLICY course_drafts_admin_own ON course_drafts
        FOR ALL
        USING (
            admin_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid
            AND current_setting('app.current_role', true) = 'admin'
        );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Server role may read/write all drafts (poller and generation pipeline).
DO $$ BEGIN
    CREATE POLICY course_drafts_server_write ON course_drafts
        FOR ALL
        USING (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

GRANT SELECT, INSERT, UPDATE, DELETE ON course_drafts TO valory_app;

-- ── 3.10  RLS: draft_nodes ────────────────────────────────────────────────────

ALTER TABLE draft_nodes ENABLE ROW LEVEL SECURITY;
ALTER TABLE draft_nodes FORCE ROW LEVEL SECURITY;

-- Admin may access draft_nodes only within their own drafts; the EXISTS subquery
-- joins through course_drafts to verify admin_id with the NULLIF guard.
DO $$ BEGIN
    CREATE POLICY draft_nodes_admin_own ON draft_nodes
        FOR ALL
        USING (
            EXISTS (
                SELECT 1 FROM course_drafts cd
                WHERE cd.id = draft_nodes.draft_id
                  AND cd.admin_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid
                  AND current_setting('app.current_role', true) = 'admin'
            )
        );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Server role may read/write all draft_nodes (poller and generation pipeline).
DO $$ BEGIN
    CREATE POLICY draft_nodes_server_write ON draft_nodes
        FOR ALL
        USING (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

GRANT SELECT, INSERT, UPDATE, DELETE ON draft_nodes TO valory_app;

-- ── 3.11  RLS: node_chats ─────────────────────────────────────────────────────

ALTER TABLE node_chats ENABLE ROW LEVEL SECURITY;
ALTER TABLE node_chats FORCE ROW LEVEL SECURITY;

-- Student sees messages for nodes in their own courses (via course_node_id).
DO $$ BEGIN
    CREATE POLICY node_chats_student_own ON node_chats
        FOR ALL
        USING (
            course_node_id IS NOT NULL
            AND EXISTS (
                SELECT 1 FROM course_nodes cn
                JOIN courses c ON c.id = cn.course_id
                WHERE cn.id = node_chats.course_node_id
                  AND c.student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid
                  AND current_setting('app.current_role', true) = 'student'
            )
        );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Admin sees messages for draft nodes belonging to their own drafts.
DO $$ BEGIN
    CREATE POLICY node_chats_admin_own ON node_chats
        FOR ALL
        USING (
            draft_node_id IS NOT NULL
            AND EXISTS (
                SELECT 1 FROM draft_nodes dn
                JOIN course_drafts cd ON cd.id = dn.draft_id
                WHERE dn.id = node_chats.draft_node_id
                  AND cd.admin_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid
                  AND current_setting('app.current_role', true) = 'admin'
            )
        );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Server role may insert and read chat turns (agent pipeline writes them).
DO $$ BEGIN
    CREATE POLICY node_chats_server_write ON node_chats
        FOR ALL
        USING (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

GRANT SELECT, INSERT, DELETE ON node_chats TO valory_app;

-- ── 3.12  system_config seed — enable_tree_mode_courses (Lead D3) ────────────

-- Idempotent ON CONFLICT DO NOTHING mirrors the pattern used by
-- migration 014 (anthropic_base_url) and 018 (two_factor_enabled).
-- Default 'false' means tree mode is opted-in by operators at runtime;
-- existing flat courses are unaffected until the flag is enabled.
INSERT INTO system_config (key, value) VALUES ('enable_tree_mode_courses', 'false')
    ON CONFLICT (key) DO NOTHING;

COMMIT;
