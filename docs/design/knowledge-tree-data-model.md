# TDD: Knowledge-Tree DAG Data Model

**Task:** G2-S1-T1  
**Status:** Design  
**Author:** design-author  
**Date:** 2026-06-13

---

## Reconciliation log (rev 2)

Changes applied by the cross-document reconciliation pass (2026-06-13):

- **C1** — Added `failed` to `node_status_enum` (6 values total: `pending`, `generating`,
  `awaiting_review`, `approved`, `rejected`, `failed`). Updated lifecycle table and diagram
  in §7 to include the `failed` terminal state and its transition from `generating`.
- **C2** — Renamed `tree_backed` to `tree_mode` throughout this document (§5.1 prose, §5.2
  table, §5.3 prose, and the migration DDL `ALTER TABLE courses` statement).
- **node_chats FK** — Replaced the polymorphic no-FK `node_id` design (referenced from T3)
  with two nullable, typed FK columns (`course_node_id`, `draft_node_id`), each
  `ON DELETE CASCADE`. Schema and RLS added in §4.6.
- **admin-draft token accounting** — Documented in §4.7: `agent_token_usage` gains a
  nullable `draft_id UUID REFERENCES course_drafts(id)` column; a partial unique index
  enforces `(admin_id, draft_id)` when `draft_id IS NOT NULL`. Admin-path calls use
  `(admin_id, draft_id)`; student-path calls continue using `(student_id, course_id)`.
- **admin-draft SSE anchoring** — Documented in §4.8: `agent_runs` gains a nullable
  `draft_id UUID REFERENCES course_drafts(id)` column. `GetEventsAfter` gains a
  `draft_id`-scoped overload. `agent_runs.course_id` NOT NULL constraint is relaxed to allow
  NULL when `draft_id IS NOT NULL`.
- **syllabus checkpoint** — §7 now states explicitly that the `syllabus` node is
  pre-seeded `approved` at tree initialisation; its HITL gate is skipped. The first active
  HITL gate is at the `section_goal` layer (§7, lifecycle note).
- **per-layer token cap** — §8.3 (new) marks `per_layer_token_budget` as a binding control
  (default `0` = disabled). Cross-reference added to T2 §8.3.

---

## 1. Overview

### Problem

Valory currently generates course content as a flat pipeline: intake → syllabus text → homework
entries → lesson content rows, all keyed by `section_index`. This approach conflates the
knowledge structure with the storage layout. There is no explicit representation of *why* a
section exists (its goal), *what* prerequisite concepts it builds on, or *what* atomic content
pieces compose it. The pipeline cannot pause at a layer boundary to present a partial result for
review, cannot easily regenerate one concept without re-touching the whole section, and cannot
share a concept node across multiple learning paths.

### Approach

Introduce a `course_nodes` table that represents every node in a course's knowledge graph as a
typed row. The agent pipeline grows the graph layer by layer:

```
root (course intent)
  └── syllabus          (one child per course — the approved structure summary)
        └── section_goal  (one per section — what the student should achieve)
              └── concept    (one per atomic concept within the section)
                    └── content  (one AsciiDoc content piece per concept)
```

This structure:
- Makes each generation step an explicit database event with a reviewable status.
- Enables partial completion — a course can be `active` with some concepts still `generating`.
- Keeps the graph inspectable and queryable without parsing AsciiDoc.
- Provides the foundation for future sharing or reuse of concept nodes across courses.

### Why this design now

The flat pipeline has been shipped and works. This new model is additive: it is a parallel
representation that coexists with the flat tables. No existing data is touched. New courses
opt into the tree; old courses remain flat. This satisfies the CLAUDE.md constraint against
piecemeal migration of legacy data.

---

## 2. Requirements in scope

This TDD is an input to **G2-S1-T4** (requirements derivation). No `REQ-*` IDs are claimed
here. The requirements author will derive IDs from this document.

---

## 3. DAG vs. Strict Tree — Decision

**Decision: strict tree with deferred DAG capability.**

The task brief uses the phrase "DAG tree" and asks for a justified decision. Here is the
analysis:

### The case for a DAG (multiple parents per node)

A DAG would allow a concept node such as "gradient descent" to be a child of both a
"machine learning fundamentals" section and an "optimization methods" section within the same
course, or even to be shared across entirely different courses.  This avoids redundant content
generation and makes the knowledge graph more semantically accurate — a concept really does
belong to multiple topics.

### Why strict tree is chosen for this sprint

1. **RLS isolation.** A node's visibility is derived from its course. In a DAG a node could
   belong to two courses owned by different students, requiring a more complex policy: either
   a join-based check across all parent paths to find any owning course, or a separate ACL
   table. Either adds complexity and potential for RLS bypass bugs before the model is even
   tested.

2. **Generation semantics.** The current agent pipeline generates content per course, per
   student. Sharing a concept across students would require cache invalidation logic, version
   management, and policy decisions about who owns a shared node — none of which is in scope
   for this sprint.

3. **Ordering is well-defined.** Concepts within a section have a natural order (the
   `ordering` column). In a DAG with multiple parents the ordering semantics become ambiguous
   unless each edge carries its own weight, doubling the schema complexity.

4. **Rollback path.** `parent_id` is a self-FK on `course_nodes`. Adding a DAG edge table
   (`course_node_edges`) later is a purely additive migration. The strict-tree schema does not
   foreclose DAG extension; it simply defers it.

### Structural consequence

Each non-root node has exactly one non-null `parent_id`. The root node (`node_type = 'root'`)
has `parent_id = NULL`. The uniqueness constraint on `(course_id, parent_id, ordering)` enforces
sibling ordering without ambiguity.

---

## 4. Data Model

### 4.1 ENUM types

```sql
-- node_type_enum: the five layers of the knowledge tree.
CREATE TYPE node_type_enum AS ENUM (
    'root',           -- Layer 0: one per course, holds the course intent/topic
    'syllabus',       -- Layer 1: one per course, the structured outline summary
    'section_goal',   -- Layer 2: one per syllabus section, the learning objective
    'concept',        -- Layer 3: one atomic concept within a section_goal
    'content'         -- Layer 4: one AsciiDoc content piece attached to a concept
);

-- node_status_enum: lifecycle of a single node.
-- Six values; the full set is shared by course_nodes and draft_nodes.
CREATE TYPE node_status_enum AS ENUM (
    'pending',          -- created but not yet picked up by the agent pipeline
    'generating',       -- agent pipeline is actively working on this node
    'awaiting_review',  -- content generated; waiting for automated or human review
    'approved',         -- passed review; visible to the student
    'rejected',         -- failed review; will be regenerated or escalated
    'failed'            -- unrecoverable error (timeout / token-cap / max-iterations exhausted)
                        -- terminal; no automatic retry; triggers escalation event
);
```

### 4.2 `course_nodes` table

```sql
CREATE TABLE IF NOT EXISTS course_nodes (
    -- Primary key: random UUID, consistent with courses, syllabi, homework, etc.
    id          UUID            PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Every node belongs to exactly one course.  Cascade delete so that
    -- withdrawing or deleting a course removes the entire tree automatically.
    course_id   UUID            NOT NULL
                                REFERENCES courses(id) ON DELETE CASCADE,

    -- Self-referencing FK.  NULL only for the root node (node_type = 'root').
    -- A non-null parent_id MUST refer to a node in the same course; this
    -- invariant is enforced at the application layer and optionally by a
    -- trigger (see Section 4.4).
    parent_id   UUID            REFERENCES course_nodes(id) ON DELETE CASCADE,

    -- Type discriminator: determines which layer this node occupies.
    node_type   node_type_enum  NOT NULL,

    -- Sibling order within the same parent.  0-indexed integers.
    -- The uniqueness constraint on (course_id, parent_id, ordering) prevents
    -- duplicate positions.  NULL parent_id rows (roots) are excluded from the
    -- unique constraint by using a partial unique index (see Section 4.3).
    ordering    INTEGER         NOT NULL DEFAULT 0
                                CHECK (ordering >= 0),

    -- Node lifecycle status.
    status      node_status_enum NOT NULL DEFAULT 'pending',

    -- Flexible JSONB store for node-specific data.
    -- See Section 4.5 for the per-node_type payload schema.
    payload     JSONB           NOT NULL DEFAULT '{}',

    -- Timestamps.
    created_at  TIMESTAMPTZ     NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ     NOT NULL DEFAULT now(),

    -- A root node must have no parent.
    CONSTRAINT root_has_no_parent
        CHECK (node_type != 'root' OR parent_id IS NULL),

    -- A non-root node must have a parent.
    CONSTRAINT non_root_has_parent
        CHECK (node_type = 'root' OR parent_id IS NOT NULL)
);
```

### 4.3 Indexes

Two hot access patterns are identified in the task contract:

**Pattern 1 — Fetch a node's children** (used by the pipeline to expand one layer):

```sql
-- Covers: SELECT * FROM course_nodes WHERE parent_id = $1 ORDER BY ordering ASC
CREATE INDEX IF NOT EXISTS course_nodes_parent_id_idx
    ON course_nodes (parent_id, ordering ASC)
    WHERE parent_id IS NOT NULL;
```

**Pattern 2 — Fetch a whole layer (node_type) for a course** (used by the renderer to load
all concepts or all section goals for a course in one query):

```sql
-- Covers: SELECT * FROM course_nodes WHERE course_id = $1 AND node_type = $2
CREATE INDEX IF NOT EXISTS course_nodes_course_type_idx
    ON course_nodes (course_id, node_type);
```

**Partial unique index for sibling ordering** (enforces ordering uniqueness among siblings
without conflicting on multiple NULL parent_id roots):

```sql
-- Prevents duplicate ordering positions among children of the same parent.
CREATE UNIQUE INDEX IF NOT EXISTS course_nodes_sibling_order_idx
    ON course_nodes (course_id, parent_id, ordering)
    WHERE parent_id IS NOT NULL;
```

**Root uniqueness** (enforces exactly one root per course):

```sql
-- Each course has at most one root node.
CREATE UNIQUE INDEX IF NOT EXISTS course_nodes_one_root_idx
    ON course_nodes (course_id)
    WHERE node_type = 'root';
```

### 4.4 Cross-course parent guard (application-layer invariant)

The self-FK `parent_id REFERENCES course_nodes(id)` does not prevent a node in course A from
pointing to a parent in course B (they share the same table). The application layer MUST
always supply `course_id` explicitly and verify that `parent_id` (when not null) refers to a
row with the same `course_id` before inserting.

A database-level guard can be added as a trigger if belt-and-suspenders enforcement is needed:

```sql
-- Optional trigger: reject cross-course parent assignments.
CREATE OR REPLACE FUNCTION course_nodes_check_parent_course()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.parent_id IS NOT NULL THEN
        PERFORM 1 FROM course_nodes
        WHERE id = NEW.parent_id AND course_id = NEW.course_id;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'course_nodes: parent_id % does not belong to course_id %',
                NEW.parent_id, NEW.course_id;
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER course_nodes_parent_course_check
    BEFORE INSERT OR UPDATE OF parent_id, course_id ON course_nodes
    FOR EACH ROW EXECUTE FUNCTION course_nodes_check_parent_course();
```

Whether to deploy this trigger is left to the migration author (G2-S2). It trades a small
overhead per write for strong referential integrity within the same course partition.

### 4.5 Payload schema by node_type

`payload` is JSONB. Each `node_type` has a documented shape; fields are nullable unless
marked required.

| node_type | Key fields in payload |
|---|---|
| `root` | `topic` (string, required) — the student's raw topic request; `intent_summary` (string) — the chair's paraphrase after intake |
| `syllabus` | `content_adoc` (string, required) — the full AsciiDoc syllabus text; `syllabus_id` (UUID) — FK back to the `syllabi` table row for flat-course compatibility |
| `section_goal` | `section_index` (int, required) — matches `homework.section_index`; `title` (string, required); `objectives` (string[]) |
| `concept` | `title` (string, required); `description` (string) — one-sentence summary of the concept |
| `content` | `content_adoc` (string, required) — the AsciiDoc content body; `lesson_content_id` (UUID) — FK back to `lesson_content` for flat-course compatibility; `citation_verified` (bool, default false) |

Cross-linking to legacy IDs (`syllabus_id`, `lesson_content_id`) in the payload allows the
renderer to fall back to the flat tables if needed, and enables future tooling to reconcile
the two representations without schema changes.

### 4.6 `node_chats` table — typed FK design (authoritative)

Chat history for the per-node Chair conversation is stored in a dedicated `node_chats` table.
The table uses **two nullable, typed FK columns** rather than a polymorphic `node_id` column,
to preserve referential integrity with cascade deletes:

```sql
-- node_chats: stores per-node Chair conversation turns for both student and admin paths.
-- Exactly one of course_node_id / draft_node_id is non-null per row.
CREATE TABLE node_chats (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Student-path: references a row in course_nodes.
    -- Null when this message belongs to an admin draft node.
    course_node_id   UUID        REFERENCES course_nodes(id) ON DELETE CASCADE,

    -- Admin-path: references a row in draft_nodes.
    -- Null when this message belongs to a student course node.
    draft_node_id    UUID        REFERENCES draft_nodes(id) ON DELETE CASCADE,

    role             TEXT        NOT NULL CHECK (role IN ('user', 'assistant')),
    content          TEXT        NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Exactly one of the two FK columns must be set.
    CONSTRAINT node_chats_exactly_one_owner
        CHECK (
            (course_node_id IS NOT NULL AND draft_node_id IS NULL)
            OR
            (course_node_id IS NULL AND draft_node_id IS NOT NULL)
        )
);

-- Index for ordered retrieval of a node's conversation history.
CREATE INDEX node_chats_course_node_idx
    ON node_chats (course_node_id, created_at ASC)
    WHERE course_node_id IS NOT NULL;

CREATE INDEX node_chats_draft_node_idx
    ON node_chats (draft_node_id, created_at ASC)
    WHERE draft_node_id IS NOT NULL;
```

**RLS on node_chats:**

```sql
ALTER TABLE node_chats ENABLE ROW LEVEL SECURITY;
ALTER TABLE node_chats FORCE ROW LEVEL SECURITY;

-- Student sees messages for nodes in their own courses.
DO $$ BEGIN
    CREATE POLICY node_chats_student_own ON node_chats
        FOR ALL
        USING (
            course_node_id IS NOT NULL
            AND EXISTS (
                SELECT 1 FROM course_nodes cn
                JOIN courses c ON c.id = cn.course_id
                WHERE cn.id = node_chats.course_node_id
                  AND c.student_id = NULLIF(current_setting('app.current_user_id', true), '')::UUID
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
                  AND cd.admin_id = NULLIF(current_setting('app.current_user_id', true), '')::UUID
                  AND current_setting('app.current_role', true) = 'admin'
            )
        );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Server role may insert and read (agent pipeline writes chat turns).
DO $$ BEGIN
    CREATE POLICY node_chats_server_write ON node_chats
        FOR ALL
        USING (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

GRANT SELECT, INSERT, DELETE ON node_chats TO valory_app;
```

**Note on `draft_nodes`:** The `draft_nodes` table is defined in G2-S1-T3 §4.2. This FK
reference is valid only after `draft_nodes` is created; the migration must order
`draft_nodes` creation before `node_chats` creation.

### 4.7 `agent_token_usage` extensions for admin drafts

The existing `agent_token_usage` table is keyed `(student_id NOT NULL, course_id NOT NULL)`.
Admin draft generation has no `student_id` and no `course_id`. Rather than a separate table,
the columns are relaxed to nullable and a nullable `draft_id` column is added:

```sql
-- Migration: relax NOT NULL constraints and add draft_id column.
ALTER TABLE agent_token_usage
    ALTER COLUMN student_id DROP NOT NULL,
    ALTER COLUMN course_id  DROP NOT NULL,
    ADD COLUMN IF NOT EXISTS draft_id UUID REFERENCES course_drafts(id) ON DELETE CASCADE;

-- A row must reference either (student_id, course_id) or (admin_id context via draft_id).
-- Enforce non-null pairing with a check constraint.
ALTER TABLE agent_token_usage
    ADD CONSTRAINT agent_token_usage_context_check CHECK (
        (student_id IS NOT NULL AND course_id IS NOT NULL AND draft_id IS NULL)
        OR
        (student_id IS NULL AND course_id IS NULL AND draft_id IS NOT NULL)
    );

-- Existing unique index (student_id, course_id) becomes partial.
-- The migration must DROP the old unique constraint and replace with two partial indexes.
DROP INDEX IF EXISTS agent_token_usage_student_course_idx;  -- adjust name to actual index name

CREATE UNIQUE INDEX agent_token_usage_student_course_idx
    ON agent_token_usage (student_id, course_id)
    WHERE draft_id IS NULL;

CREATE UNIQUE INDEX agent_token_usage_draft_idx
    ON agent_token_usage (draft_id)
    WHERE draft_id IS NOT NULL;
```

Admin-path UPSERT uses `(draft_id)` as the conflict target. The existing student-path UPSERT
in `ThrottledClient.Messages` is unchanged (it always supplies both `student_id` and
`course_id`); it gains a `draft_id = NULL` WHERE clause when re-checked so it hits the
correct partial index.

### 4.8 `agent_runs` extensions for admin-draft SSE anchoring

The existing `agent_runs.course_id` is `NOT NULL`. The admin draft event stream cannot anchor
on a `course_id` because admin drafts are not stored in `courses`. The column is relaxed and
a `draft_id` column is added:

```sql
-- Migration: relax course_id NOT NULL and add draft_id column.
ALTER TABLE agent_runs
    ALTER COLUMN course_id DROP NOT NULL,
    ADD COLUMN IF NOT EXISTS draft_id UUID REFERENCES course_drafts(id) ON DELETE CASCADE;

-- At least one context anchor must be set.
ALTER TABLE agent_runs
    ADD CONSTRAINT agent_runs_context_check CHECK (
        (course_id IS NOT NULL AND draft_id IS NULL)
        OR
        (course_id IS NULL AND draft_id IS NOT NULL)
    );
```

`GetEventsAfter` in `agent/repository.go` gains a `draft_id`-scoped overload:

```go
// GetEventsAfterForDraft returns pipeline events for the given draft, ordered by emitted_at.
// It joins pipeline_events → agent_runs on agent_run_id and filters by draft_id.
func (r *AgentRepository) GetEventsAfterForDraft(
    ctx context.Context,
    draftID uuid.UUID,
    afterEventID uuid.UUID,
) ([]PipelineEvent, error)
```

The existing `GetEventsAfter(courseID, afterEventID)` signature is unchanged. Flat-course and
student-tree paths continue to use it without modification.

---

## 5. Coexistence: tree-mode vs. flat courses

### 5.1 Course-level flag

A new column is added to the `courses` table (in the G2-S2 migration):

```sql
ALTER TABLE courses
    ADD COLUMN IF NOT EXISTS tree_mode BOOLEAN NOT NULL DEFAULT FALSE;
```

- `tree_mode = FALSE` (default): the course is a legacy flat course. The pipeline writes to
  `syllabi`, `homework`, `lesson_content` as it always has. No `course_nodes` rows exist for
  this course.
- `tree_mode = TRUE`: the pipeline grows a `course_nodes` tree. The flat tables may still
  hold compatibility rows (via the `syllabus_id` / `lesson_content_id` cross-links in the
  payload), but the tree is the source of truth.

**No existing course is touched.** The column defaults to `FALSE`, so every pre-existing row
keeps its current behavior. New courses created after the migration are set to `TRUE` only if
the feature is explicitly enabled via a config flag (e.g., `enable_tree_mode_courses` in
`system_config`). This gives operators a safe rollout path.

### 5.2 Rendering logic

| Course type | What the API/frontend renders |
|---|---|
| `tree_mode = FALSE` | Existing endpoints: `GET /courses/:id/syllabus`, `/lesson-content`, `/homework`. No change. |
| `tree_mode = TRUE` | New tree endpoints (specified in G2-S1-T3): `GET /courses/:id/nodes` returns the full node graph; layer-specific endpoints return nodes filtered by `node_type`. |

The frontend switches rendering path on the `tree_mode` field in the `GET /courses/:id`
response. Legacy components render as before; tree components are behind a feature gate.

### 5.3 In-flight flat courses

A course that is mid-generation when the migration runs keeps `tree_mode = FALSE` because
the column defaults false and no backfill is applied. The agent runner checks `tree_mode`
before dispatching: flat courses follow the old poll-and-generate path; tree-backed courses
follow the new layer-by-layer path specified in G2-S1-T2.

---

## 6. RLS Policies

All policies follow the idiom established in migration `004_agent.sql`:

- Student-read: `course_id IN (SELECT id FROM courses WHERE student_id = NULLIF(...)::uuid)`.
  The `NULLIF(..., '')` guard prevents the superuser test role from masking RLS bugs (see
  memory `force-rls-superuser-test-masking`).
- Admin-all: `current_setting('app.current_role', true) = 'admin'`.
- Server-write: `current_setting('app.current_role', true) = 'server'` on INSERT.

```sql
ALTER TABLE course_nodes ENABLE ROW LEVEL SECURITY;
ALTER TABLE course_nodes FORCE ROW LEVEL SECURITY;

-- Student may read nodes belonging to their own courses.
-- The NULLIF guard on both USING and WITH CHECK mirrors the lesson_content_student_policy
-- in 004_agent.sql, which was the first policy to use the guarded subselect idiom.
DO $$ BEGIN
    CREATE POLICY course_nodes_student_policy ON course_nodes
        USING (
            course_id IN (
                SELECT id FROM courses
                WHERE student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid
            )
        )
        WITH CHECK (
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
-- USING clause omitted: server inserts without needing to read-own.
DO $$ BEGIN
    CREATE POLICY course_nodes_server_policy ON course_nodes
        FOR INSERT
        WITH CHECK (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Server role may also update node status and payload (e.g., marking approved/rejected).
DO $$ BEGIN
    CREATE POLICY course_nodes_server_update_policy ON course_nodes
        FOR UPDATE
        USING (current_setting('app.current_role', true) = 'server')
        WITH CHECK (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

GRANT SELECT, INSERT, UPDATE, DELETE ON course_nodes TO valory_app;
```

### RLS policy notes

1. **NULLIF guard rationale.** The test database role is typically superuser, which means
   `FORCE ROW LEVEL SECURITY` is still bypassed for superuser connections unless a `SET ROLE`
   is applied. The `NULLIF(current_setting('app.current_user_id', true), '')` pattern converts
   an empty string (the default when the GUC is unset) to NULL, causing the uuid cast to
   return NULL, which makes the `student_id = NULL` comparison false for every row. This means
   an RLS-unguarded test connection sees zero rows rather than all rows, surfacing the bug
   instead of hiding it. This matches the behavior in `lesson_content_student_policy` (004).

2. **Server UPDATE policy.** The agent pipeline needs to transition node status from
   `generating` → `awaiting_review` → `approved`/`rejected`/`failed`. A separate UPDATE policy
   with `FOR UPDATE` is added rather than bundling it into the INSERT policy, because the
   INSERT policy's `WITH CHECK` clause applies only to new rows.

3. **Student write restriction.** Students may not insert or update `course_nodes` directly.
   The student policy has no `FOR INSERT` or `FOR UPDATE` clause, so RLS defaults to deny.
   Students interact with the tree via the chat and feedback APIs only.

---

## 7. Status Lifecycle

### Transition diagram

```
pending --> generating --> awaiting_review --> approved
                |                 |
           [error /               |
            token cap /      [human rejects
            max retries]      with feedback]
                |                 |
                v                 v
              failed           rejected --> (pending again, for retry)
```

**`failed` is a terminal state.** A `failed` node does not automatically retry. The
`LayeredRunner` emits a `layer_node_failed` escalation event and skips the node in the
layer-settled calculation (see G2-S1-T2 §4.1). If all nodes in a layer reach `failed`, the
engine emits `layer_generation_failed` and halts the course.

**`rejected` is not terminal.** A human calls `PATCH .../nodes/{id}/feedback` (stores
feedback text and moves to `rejected`), then the poller picks it up and transitions back to
`pending` → `generating` for regeneration. See G2-S1-T2 §4.1 and G2-S1-T3 §5.1.8/5.2.5.

### Status descriptions

| Status | Meaning |
|---|---|
| `pending` | Node row exists but agent pipeline has not started work on it yet. |
| `generating` | Agent pipeline has claimed this node and is actively generating content. |
| `awaiting_review` | Content has been generated. The reviewer agent or a human admin must approve it. |
| `approved` | Node has passed review and is visible to the student. |
| `rejected` | Node failed human review. Stored feedback triggers regeneration (via the poller). Reset to `pending` when the regeneration begins. |
| `failed` | Unrecoverable error: timeout, token-cap exceeded, or correction-loop max-iterations exhausted after escalation. Terminal; no automatic retry. Escalation event fired. |

### Syllabus checkpoint decision

The `syllabus` node is **pre-seeded as `approved`** at tree initialisation. When
`pollAndGenerate` transitions a course to `tree_mode = true`, it calls `seedTreeAndGenerateRoot`:

1. Insert root node (`status = 'pending'`) — generates and auto-approves (no human gate at
   root layer; root content is the student's own topic intent).
2. Run the Chair's existing syllabus generation.
3. Insert syllabus node with the generated content and immediately set `status = 'approved'`
   — the syllabus is already a human-approved artifact from the existing flat-course flow
   (the student approved it before `tree_mode` was activated).
4. Advance directly to the `section_goal` layer.

The **first active HITL gate** is therefore at the `section_goal` layer. This honors the
"HITL at EACH level" root-ask (root #5) for all content layers while avoiding a redundant
checkpoint for the syllabus, which the student already approved in the intake flow.

The `updated_at` timestamp is always refreshed on status transitions so the pipeline can order
work by staleness.

A `pending` node with `updated_at` older than a configurable threshold can be treated as
stalled and picked up by a background sweep — this mirrors the `pollAndGenerate` pattern in
`runner.go`.

---

## 8. Agent Interaction

The knowledge-tree pipeline replaces `generateAllSections` in `runner.go` for tree-backed
courses. The following sequence describes the layer-by-layer growth:

```
AgentRunner.RunTreeGeneration(ctx, courseID, studentID)
  |
  +-- 1. Fetch root node (node_type='root', status='approved')
  |       -- created at course creation time, status set to 'approved' after intake
  |
  +-- 2. GenerateSyllabus
  |       Chair.GenerateSyllabus() -> inserts syllabus node (node_type='syllabus')
  |       Syllabus pre-seeded as status='approved' (no HITL gate at syllabus layer)
  |
  +-- 3. HITL GATE: section_goal layer (first human checkpoint)
  |       For each section in syllabus:
  |       insert section_goal node (node_type='section_goal', status='pending')
  |       GenerateLayer('section_goal') -> status='awaiting_review'
  |       Human: PATCH .../nodes/{id}/approve  OR  PATCH .../nodes/{id}/feedback
  |       When all settled: expand to concept layer
  |
  +-- 4. HITL GATE: concept layer
  |       For each section_goal:
  |         Professor.GenerateConcepts() -> inserts concept nodes (node_type='concept')
  |         Human reviews each -> PATCH approve / PATCH feedback
  |       When all settled: expand to content layer
  |
  +-- 5. HITL GATE: content layer
  |       For each concept:
  |         Professor.GenerateContent() -> inserts content node (node_type='content')
  |         Human reviews -> PATCH approve / PATCH feedback
  |
  +-- 6. All content nodes approved -> course.status = 'active'
```

Layer expansion requires an **explicit `POST .../layers/{layer}/expand`** call after all
nodes in the current layer are `approved`. The poller does NOT auto-advance. See G2-S1-T2 §4.3
and G2-S1-T3 §5.2.8 for the endpoint definition and the rationale (explicit expand honors
HITL at each level and is the cost gate).

The pipeline emits `pipeline_events` at each layer boundary, matching the existing SSE
pattern in `runner.go`. Students receive real-time progress updates via the existing SSE
endpoint at `/events`.

Each layer step is idempotent: the pipeline queries for existing nodes at the target layer
before inserting, so a restart after a failure picks up from the last approved node.

### 8.3 Per-layer token budget (binding control)

A `per_layer_token_budget` system config key controls how many tokens `LayeredRunner` may
spend per layer expansion. Default value: `0` (disabled — no per-layer cap, only the global
`per_student_token_limit` applies). When set to a positive integer by an admin:

- `LayeredRunner.GenerateLayer` checks `agent_token_usage.total_tokens_used` before each
  node call.
- If `total_tokens_used + estimated_node_tokens > per_layer_token_budget`, the node
  transitions to `failed` with reason `token_budget_exceeded`, and escalation fires.
- The check is per-layer (the budget resets when the layer counter advances), not
  per-course-lifetime. The global `per_student_token_limit` continues to apply as a
  hard ceiling across all layers.

Cross-reference: G2-S1-T2 §8.3 describes the same control. Both documents are authoritative
for their respective concerns (data model vs. runner logic); the config key name and semantics
are identical.

---

## 9. Alternatives Considered

### 9.1 Extend the flat tables with parent/type columns

Add `parent_id` and `node_type` to `lesson_content`. This reuses existing infra but mixes
two representations in one table, complicates RLS (existing policies assume `lesson_content`
is per-course-section), and makes the flat rendering path fragile. Rejected.

### 9.2 Separate table per node type

Create `syllabus_nodes`, `section_goal_nodes`, `concept_nodes`, `content_nodes`. This gives
strong typing per layer but multiplies schema objects by 4x, complicates cross-layer joins
(e.g., "what is the approval status of all nodes in this course?"), and makes the agent
pipeline harder to write generically. Rejected.

### 9.3 Store the tree in JSONB on the `courses` table

One `tree JSONB` column on `courses`. Simple to read but impossible to query efficiently at
the node level, lacks per-node RLS, prevents partial updates without read-modify-write cycles,
and grows unboundedly. Rejected.

### 9.4 Full DAG with an edge table

Separate `course_node_edges(from_id, to_id, edge_type)` table alongside `course_nodes`. This
enables multi-parent semantics but requires solving the RLS problem for shared nodes (a node
visible to two students) and complicates ordering. Deferred to a future sprint as described in
Section 3.

### 9.5 Polymorphic `node_id` in `node_chats` (rejected)

The original T3 draft used a single `node_id UUID NOT NULL` column with a `node_owner_type`
discriminator and no FK. This would produce orphan rows on node deletion because PostgreSQL
cannot enforce a FK to "either this table or that table". The two-nullable-FK design in §4.6
trades one nullable column for full referential integrity and cascade delete correctness.
Rejected the polymorphic approach.

---

## 10. Open Questions

1. **Trigger deployment.** Should the cross-course parent guard trigger (Section 4.4) be
   deployed in the G2-S2 migration, or deferred until a bug is observed? Recommendation:
   deploy it — the overhead is negligible and the safety value is high during early development.
   Decision needed from the Software Lead before G2-S2 implementation begins.

2. **Config flag for tree-mode.** Should `enable_tree_mode_courses` be a `system_config`
   row or a server startup environment variable? A DB config row allows runtime toggling
   without restart; an env var is simpler but requires a deploy. Recommendation: DB config
   row (consistent with `correction_loop_max_iterations` and `content_generation_timeout_seconds`).
   Needs PM sign-off.

3. **Flat table compatibility rows.** The payload design includes `syllabus_id` and
   `lesson_content_id` cross-links (Section 4.5). Should the tree pipeline write to the flat
   tables in addition to `course_nodes`, keeping both in sync? This simplifies the initial
   rollout (existing endpoints keep working) at the cost of double-writes. Alternative: tree
   courses skip the flat tables entirely and new endpoints serve the tree directly. Needs a
   decision before G2-S2 migration and G2-S3 (agent integration) can proceed.

4. **Concurrent concept generation.** Can multiple concept nodes within the same section_goal
   be generated in parallel? The current `runner.go` generates sections sequentially to bound
   resource usage. The tree model enables parallelism by layer; whether to exploit it depends
   on the token-cap budget and the throttling strategy in `ThrottledClient`. Out of scope for
   this TDD; flagged for the G2 architecture review.

5. **`agent_token_usage` migration order.** The `agent_token_usage` schema change in §4.7
   requires dropping the existing `(student_id, course_id)` unique constraint and replacing
   it with two partial indexes. The migration must verify the exact constraint/index name
   before issuing `DROP INDEX`. This is a G2-S2 migration-author responsibility.
