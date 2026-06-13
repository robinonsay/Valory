# TDD: Layered Generation State Machine + Per-Level HITL Checkpoint Protocol

**Task:** G2-S1-T2  
**Author:** design-author  
**Status:** Draft  
**Date:** 2026-06-13  
**Depends on (assumed node model):** G2-S1-T1 (`course_nodes` table, `status` field)

---

## Reconciliation log (rev 2)

Changes applied by the cross-document reconciliation pass (2026-06-13):

- **C1** — `node_status_enum` in §3.2 now lists all 6 values (`pending`, `generating`,
  `awaiting_review`, `approved`, `rejected`, `failed`) matching T1 §4.1. The `failed`
  state was already used throughout this document; the enum declaration now agrees.
- **C2** — Renamed `tree_backed` to `tree_mode` throughout this document (§3.3, §5.3, §7.1,
  §7.2, §13 migration table). This aligns with the single authoritative flag name standardised
  in T1 §5.1.
- **C3 — HITL endpoint reconciliation** — §4.3 (HITL checkpoint protocol) previously listed
  `POST approve`, `POST reject`, and `POST layers/{layer}/approve-all`. These have been
  replaced by the T3-authoritative verbs: `PATCH .../nodes/{id}/approve`,
  `PATCH .../nodes/{id}/feedback` (moves node to `rejected`; feedback stored in `node_chats`
  and `payload.feedback`), and `POST .../nodes/{id}/regenerate`. The bulk-approve endpoint
  is removed from this document; if a bulk operation is later desired, it must be defined in
  T3 and referenced here.
- **C3 — Layer expand model** — §4.3 now specifies that the next layer does NOT auto-expand
  when all nodes in the current layer are approved. Instead, an explicit
  `POST .../layers/{layer}/expand` call is required (defined in T3 §5.2.8). The sequence
  diagram in §9 has been updated to reflect this. The poller is the ENGINE; it picks up
  `status = 'generating'` courses only AFTER the human has called `/expand`.
- **`/feedback` vs `/reject` clarification** — §4.3 now explicitly states: `feedback` is the
  only endpoint for human rejection. It accepts a required feedback comment and transitions
  the node to `rejected`. There is no separate `/reject` endpoint. The feedback text is
  stored both as a `user`-role row in `node_chats` (via T3) and in `payload.feedback` (for
  the regeneration prompt context). The word "reject" in prose now means the outcome, not an
  endpoint name.
- **Syllabus checkpoint** — §4.3 and §9 now state explicitly that the `syllabus` node is
  pre-seeded `approved` at tree initialisation; the first human HITL gate is at the
  `section_goal` layer. Cross-reference to T1 §7.
- **per-layer token cap** — §8.3 `per_layer_token_budget` is marked as a **binding control**
  (default `0` = disabled). The description is updated to match T1 §8.3.
- **admin-draft token accounting** — §8.2 updated to note that `agent_token_usage` is
  extended with a nullable `draft_id` column (see T1 §4.7); admin calls use `(admin_id,
  draft_id)` as the conflict target.
- **admin-draft SSE anchoring** — §11.3 updated: `agent_runs.course_id` is relaxed to
  nullable when `draft_id IS NOT NULL` (see T1 §4.8). The "Partially adopted" note in §11.3
  is now complete.

---

## 1. Overview

### Problem

Today's content generation is a flat one-shot pipeline. After the syllabus is approved,
`RunContentGeneration` (runner.go:331) calls `generateAllSections` (runner.go:397), which
iterates every homework section sequentially, invoking the professor and reviewer for each.
Human-in-the-loop feedback only happens post-hoc: a student submits free-text to
`section_feedback`, a background ticker (`pollFeedback`, runner.go:246) scans for change-request
keywords, and if found it re-runs a section. This is reactive and coarse-grained — the human
sees a complete course first, then patches it.

The new model replaces this with a **layered generation loop**:

```
generate layer N → persist all nodes (status=awaiting_review) → PAUSE
  → human reviews via API checkpoint
    → approve → human calls POST .../layers/{layer}/expand → unlock layer N+1 → repeat
    → reject with feedback (PATCH .../nodes/{id}/feedback) → regenerate layer N nodes → PAUSE again
```

Each layer of the knowledge tree (root, syllabus, section_goal, concept, content) is generated
as a discrete batch. The system pauses after each batch and waits for explicit human approval
**and** an explicit layer-expand call before growing the next layer. Generation is driven by
durable database state, not in-memory goroutine continuations, so a server restart mid-tree
recovers automatically.

### Why this approach

- **Per-level checkpoints** give the human oversight before the system commits Anthropic tokens
  to deeper layers. Rejecting a section_goal before its concepts are generated costs 1 call
  instead of 5-10.
- **Explicit layer expansion** (not auto-advance) is the cost gate. The human must call
  `POST .../layers/{layer}/expand` before the engine generates the next layer. This directly
  satisfies root-ask #5 ("HITL at EACH level").
- **Durable state in `course_nodes.status`** (defined in G2-S1-T1) means no resumability
  scaffolding outside the database. The polling loop re-reads status and resumes from the first
  non-completed layer.
- **Backward compatibility by flag** (`courses.tree_mode`). Flat courses skip this path
  entirely; the existing `generateAllSections` / `runReviewLoop` path is unchanged.

---

## 2. Requirements in scope

No formal REQ-IDs are claimed by this design task itself (per the G2-S1-T2 contract). The
design feeds into G2-S1-T4 (requirements derivation). The following existing requirements are
directly affected:

| ID | Existing requirement | Impact of this design |
|----|---------------------|-----------------------|
| REQ-AGENT-003 | Polling loop triggers content generation | Extended: tree-mode poll looks for `awaiting_layer_approval` courses instead of `syllabus_approved` |
| REQ-AGENT-006 | SSE pipeline events | New event types for layer checkpoints |
| REQ-AGENT-007 | Correction loop max iterations | Applies per-node in the professor↔reviewer micro-loop |
| REQ-AGENT-008 | Escalation after max iterations | Still applies per-node |
| REQ-AGENT-010 | Section feedback keyword polling | **Replaced** for tree-backed courses by explicit HITL endpoints |
| REQ-AGENT-011 | Token cap (per_student_token_limit) | Multiplied by layer count; requires per-layer budget sub-cap |
| REQ-AGENT-014 | Generation timeout | Applies per-layer, not per-course |
| REQ-CONTENT-004 | Feedback keyword detection | **Disabled** for tree-backed courses |

---

## 3. Data model assumptions (from G2-S1-T1)

This design assumes the following schema from G2-S1-T1. It references only the fields needed
here; the full DDL lives in the T1 artifact.

### 3.1 course_nodes table (assumed)

```sql
CREATE TABLE course_nodes (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id   UUID        NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    parent_id   UUID        REFERENCES course_nodes(id) ON DELETE CASCADE,
    node_type   node_type   NOT NULL,  -- root|syllabus|section_goal|concept|content
    ordering    INT         NOT NULL DEFAULT 0,
    status      node_status NOT NULL DEFAULT 'pending',
    payload     JSONB       NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### 3.2 Node status enum (assumed)

The following six values are defined in `node_status_enum` (T1 §4.1). All six are referenced
by this document; the authoritative DDL is in T1.

```
pending          -- not yet generated
generating       -- professor call in-flight
awaiting_review  -- generated; waiting for human checkpoint
approved         -- human approved; children may be generated
rejected         -- human rejected with feedback; must regenerate
failed           -- unrecoverable generation error (terminal; no automatic retry)
```

The `status` field is the exclusive source of truth for where a node sits in the lifecycle.
No in-memory state encodes this.

### 3.3 courses table extension (new column — migration G2-S2)

```sql
ALTER TABLE courses ADD COLUMN IF NOT EXISTS tree_mode BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE courses ADD COLUMN IF NOT EXISTS current_layer node_type;
```

- `tree_mode = true` routes the course through the layered path.
- `current_layer` records which layer is currently awaiting review. NULL means generation has
  not started or has completed.
- Flat (`tree_mode = false`) courses ignore both columns; their lifecycle is unchanged.

### 3.4 Layer sequence

The tree grows in strict depth-first order through these layers (left = first):

```
1. root          -- pre-seeded approved at tree init; no HITL gate
2. syllabus      -- pre-seeded approved (student already approved in intake flow); no HITL gate
3. section_goal  -- FIRST human HITL gate
4. concept       -- second human HITL gate
5. content       -- third human HITL gate
```

Each layer is fully approved before the next is generated. The human must:
1. Approve each node in the layer (`PATCH .../nodes/{id}/approve`), or provide feedback
   (`PATCH .../nodes/{id}/feedback`) and wait for regeneration.
2. Call `POST .../layers/{layer}/expand` to unlock generation of the next layer.

Parallelism within a layer is permitted (all nodes at the same layer can generate
concurrently), but the pause gate is at the layer boundary and requires an explicit expand
call.

---

## 4. State machine specification

### 4.1 Per-node state transitions

```
                  +----------+
              +-->| rejected |<---------+
              |   +----------+          |
              |        |                |
    [human    |   [regenerate          |
     provides |    triggered]          |
     feedback]|        v                |
              |                         |
   pending ---+---> generating -------> awaiting_review
                         |                    |
                    [error /             [human approves:
                     token cap /          PATCH .../approve]
                     max retries]              |
                         |                    v
                         v                 approved
                       failed
```

Allowed transitions (source → target):

| From | To | Trigger |
|------|----|---------|
| `pending` | `generating` | LayerRunner picks up the node |
| `generating` | `awaiting_review` | Professor + reviewer micro-loop completes |
| `generating` | `failed` | Unrecoverable error (timeout, token cap, max iterations exhausted after escalation) |
| `awaiting_review` | `approved` | Human calls `PATCH .../nodes/{id}/approve` |
| `awaiting_review` | `rejected` | Human calls `PATCH .../nodes/{id}/feedback` with non-empty feedback |
| `rejected` | `generating` | LayerRunner re-picks up the node after human rejection |
| `generating` (retry) | `awaiting_review` | Regeneration completes |

The transition `awaiting_review → generating` is not valid (regeneration must flow through
`rejected` first so there is always an explicit human action on record).

**Feedback vs. reject clarification:** There is no separate `/reject` endpoint. The single
`PATCH .../nodes/{id}/feedback` endpoint accepts a required `feedback` field (empty string
returns 400), stores the text both as a `user`-role `node_chats` row and in
`payload.feedback`, and transitions the node to `rejected`. "Reject" is the outcome; the
endpoint that produces it is `/feedback`.

**Per-layer approval rule:** A layer is considered approved when ALL non-failed nodes in that
layer have `status = 'approved'`. A single `failed` node does not block the layer; it emits
an escalation event and is skipped (matching the existing `runReviewLoop` escalation logic for
max-iteration exhaustion). If all nodes in a layer are failed, the layer transitions to a
`layer_generation_failed` event and the course halts.

### 4.2 Per-layer state (derived, not stored)

The layer-level state is derived from the aggregate of node statuses. The runner never stores
a separate layer status row; it queries:

```sql
-- Check whether a layer is fully settled (approved or failed).
SELECT
    COUNT(*) FILTER (WHERE status = 'approved')   AS approved_count,
    COUNT(*) FILTER (WHERE status = 'failed')     AS failed_count,
    COUNT(*) FILTER (WHERE status NOT IN ('approved','failed')) AS pending_count
FROM course_nodes
WHERE course_id = $1
  AND node_type = $2;
```

When `pending_count = 0`, the layer is settled:
- If `approved_count > 0` (at least one node approved): layer is approved; the UI may show an
  "expand to next layer" button. The engine waits for the explicit expand call.
- If `approved_count = 0` (all failed): emit `layer_generation_failed` event; halt the course.

The `courses.current_layer` column tracks the active layer so the poller can find courses
awaiting expansion without scanning all course_nodes.

### 4.3 HITL checkpoint protocol (the pause gate)

After all nodes in a layer reach `awaiting_review` (or `failed`), the runner:

1. Emits a `layer_awaiting_review` pipeline event with payload `{layer, node_ids, course_id}`.
2. Sets `courses.current_layer = <layer>` and `courses.status = 'awaiting_layer_approval'`
   (new course status value — see section 4.4).
3. Returns. The goroutine exits. No lock is held. No memory state persists.

The human then acts via the following endpoints (full spec in G2-S1-T3):

- `PATCH /api/v1/courses/{courseId}/nodes/{nodeId}/approve`
  Marks one node approved. No request body.

- `PATCH /api/v1/courses/{courseId}/nodes/{nodeId}/feedback`
  Marks one node rejected; the `feedback` field (required, non-empty string) is stored as a
  `user`-role `node_chats` row AND in `payload.feedback`. The node transitions to `rejected`.
  There is no separate `/reject` endpoint.

- `POST /api/v1/courses/{courseId}/nodes/{nodeId}/regenerate`
  Re-triggers generation for a `rejected` or `failed` node. Transitions node to `generating`.

- `POST /api/v1/courses/{courseId}/layers/{layer}/expand`
  **Layer-advance trigger.** Called after all nodes in the current layer are `approved`.
  Precondition: `pending_count = 0` AND `approved_count > 0` for the layer (else 409).
  Effect: inserts child nodes for the next layer (`status = 'pending'`), sets
  `courses.status = 'generating'` and `courses.current_layer = <next_layer>`. The poller
  picks this up on the next tick and calls `LayeredRunner.GenerateLayer`.

**Layer expansion is explicit, not automatic.** The poller never auto-advances to the next
layer. It only calls `generateNextLayer` after the human has called `/expand`. This design
honors the "HITL checkpoint at EACH level" root-ask (#5) and makes the expand decision the
human's cost gate.

**Admin surface equivalents** (full spec in G2-S1-T3 §5.1):

- `PATCH /api/v1/admin/drafts/{draftId}/nodes/{nodeId}/approve`
- `PATCH /api/v1/admin/drafts/{draftId}/nodes/{nodeId}/feedback`
- `POST /api/v1/admin/drafts/{draftId}/nodes/{nodeId}/regenerate`
- `POST /api/v1/admin/drafts/{draftId}/layers/{layer}/expand`

These mirror the student endpoints exactly; the only difference is the resource path prefix
and the auth requirement (`RequireRole("admin")`).

### 4.4 New course status values

The existing `course_status` enum (003_course.sql) requires two new values. They are added
via migration G2-S2 using `ALTER TYPE ... ADD VALUE IF NOT EXISTS` (non-destructive, no
rollback risk for the enum itself):

```sql
ALTER TYPE course_status ADD VALUE IF NOT EXISTS 'awaiting_layer_approval' AFTER 'active';
ALTER TYPE course_status ADD VALUE IF NOT EXISTS 'awaiting_regeneration'   AFTER 'awaiting_layer_approval';
```

These values are only reachable when `tree_mode = true`. Flat courses never enter them.

---

## 5. Runner design: wrap vs. replace decision

**Decision: wrap `generateAllSections` / `runReviewLoop` by adding a new layered code path
that coexists with the flat path. The existing functions are not modified.**

### 5.1 Rationale

`generateAllSections` and `runReviewLoop` are a tight professor↔reviewer micro-loop per
section. Their internal logic (content generation → reviewer review → correction → escalation)
is correct and well-tested. The new design reuses this logic at the per-node level.

What changes is the **outer loop** (who drives which nodes to generate, in what order, and
when to pause). The outer loop is currently `generateAllSections` (a sequential for-loop over
homework sections). The new outer loop is `generateLayer` — a parallel-node dispatcher that
generates all nodes of type T in one layer, then pauses.

The professor and reviewer agents are reused per-node with no changes. The `runReviewLoop`
function is the building block called by `generateLayer` for each node.

### 5.2 New LayeredRunner (internal/agent/layered_runner.go)

A new struct `LayeredRunner` is added to the `agent` package:

```go
type LayeredRunner struct {
    pool      *pgxpool.Pool
    agentRepo *AgentRepository
    professor *Professor
    reviewer  *Reviewer
    configSvc interface {
        GetInt64(string) int64
    }
}
```

It exposes:

```go
// GenerateLayer generates all pending/rejected nodes at the given layer for the course.
// Called by the poller when courses.status = 'generating' and current_layer is set.
// It runs each node's professor+reviewer micro-loop (via runReviewLoop) concurrently,
// updates node statuses, and when all nodes are settled, transitions the course to
// 'awaiting_layer_approval' and emits the checkpoint event.
func (lr *LayeredRunner) GenerateLayer(ctx context.Context, runID, courseID, studentID uuid.UUID, layer NodeType) error

// ExpandToNextLayer inserts the child nodes (pending) for the layer after currentLayer,
// sets courses.status='generating' and courses.current_layer=<nextLayer>, then returns.
// Called by the expand-layer HTTP handler (POST .../layers/{layer}/expand) AFTER the
// human has approved all nodes in the current layer.
// Returns ErrTreeComplete when the content layer is the currentLayer (no further layers).
func (lr *LayeredRunner) ExpandToNextLayer(ctx context.Context, courseID uuid.UUID, currentLayer NodeType) error
```

Note: `AdvanceToNextLayer` from the original design is renamed `ExpandToNextLayer` to
reflect the explicit-human-trigger model. The poller does NOT call this; the HTTP handler does.

### 5.3 Poller integration (AgentRunner)

`AgentRunner.Start` gains a third ticker (every 30s):

```go
layerTicker := time.NewTicker(30 * time.Second)
```

The new tick calls `pollLayeredGeneration`:

```go
func (r *AgentRunner) pollLayeredGeneration(ctx context.Context) {
    // Find tree-mode courses with status = 'generating' that have a current_layer set.
    // (These are courses where the human has already called /expand.)
    // Also find tree-mode courses with status = 'awaiting_regeneration' (rejected nodes).
    // Dispatch a LayeredRunner.GenerateLayer goroutine per course.
    // Does NOT call ExpandToNextLayer — that is the HTTP handler's responsibility.
}
```

`pollAndGenerate` (existing, runner.go:226) is modified to check `tree_mode`:
- `tree_mode = false` (default): existing behaviour (finds `syllabus_approved`, runs
  `RunContentGeneration`).
- `tree_mode = true`: seeds the root node (status=pending), sets `courses.status = 'generating'`
  and `courses.current_layer = 'root'`, and lets `pollLayeredGeneration` drive from there.

### 5.4 The professor↔reviewer micro-loop per node

For tree-backed courses, `runReviewLoop` is called with the node's content (stored in
`course_nodes.payload`). It follows the existing correction/escalation logic identically.
The only difference is that the content is stored in `course_nodes.payload` rather than
`lesson_content`, and the node's `status` is updated rather than a pipeline event alone.

The `GeneratedSection` struct maps to a `course_nodes` row as follows:

| GeneratedSection field | course_nodes mapping |
|------------------------|----------------------|
| `ContentID` | `course_nodes.id` |
| `SectionIndex` | `course_nodes.ordering` |
| `Title` | `course_nodes.payload->>'title'` |
| `ContentAdoc` | `course_nodes.payload->>'content_adoc'` |

This mapping is internal to `LayeredRunner`; the professor and reviewer see the same
`GeneratedSection` struct.

### 5.5 Replacement of section_feedback keyword polling for tree-backed courses

`pollFeedback` (runner.go:246) is modified to skip courses where `tree_mode = true`:

```go
WHERE sf.regeneration_triggered = false
  AND c.status = 'active'
  AND c.tree_mode = false   -- NEW: skip tree-backed courses
```

For tree-backed courses, the feedback loop is replaced entirely by the HITL feedback endpoint.
When a human calls `PATCH .../nodes/{id}/feedback`, the feedback is stored in both:
- `node_chats` (as a `user`-role message, for Chair conversation continuity), and
- `course_nodes.payload.feedback` (for the professor re-prompt on regeneration).

The `pollLayeredGeneration` ticker picks up nodes with `status = 'rejected'` and re-runs the
professor call with `payload.feedback` as the input to `professor.RegenerateSection`.

This is explicit HITL feedback rather than keyword-heuristic feedback. The keyword detection
function `containsRegenKeyword` is not called for tree-backed courses.

---

## 6. Resumability / durability specification

### 6.1 Invariant

**No in-memory state encodes generation progress for a tree-backed course.** All state lives
in `course_nodes.status`, `courses.current_layer`, and `courses.status`. A server restart
drops all in-flight goroutines. On restart, `pollLayeredGeneration` finds any course with
`status IN ('generating', 'awaiting_regeneration')` and replays from the database.

### 6.2 Restart recovery scenarios

| Scenario | DB state at restart | Recovery action |
|----------|--------------------|----|
| Server restarts while generating layer N nodes | Some nodes `status='generating'`, course `status='generating'` | Nodes stuck in `generating` are re-queued: poll resets them to `pending` after a staleness threshold (e.g., `updated_at < now() - interval '10 minutes'`) and re-dispatches |
| Server restarts while waiting for human approval | course `status='awaiting_layer_approval'`, nodes `status='awaiting_review'` | Nothing to resume; poller skips these; SSE reconnect re-fetches events from the last event ID |
| Server restarts after human approves and calls /expand but before next layer starts | course `status='generating'`, `current_layer` advanced | Poller picks up immediately on next tick |
| Server restarts after a node reaches `failed` | Node `status='failed'`, course `status='generating'` | Poller skips failed nodes; if all nodes in the layer are settled (approved+failed), transitions course to `awaiting_layer_approval` |

### 6.3 Stuck-generating node recovery

The poller's `pollLayeredGeneration` includes a staleness guard:

```sql
-- Nodes that have been 'generating' for more than 10 minutes are presumed orphaned
-- (the goroutine died without updating status). Reset them to 'pending' so they are retried.
UPDATE course_nodes
SET status = 'pending', updated_at = now()
WHERE course_id = $1
  AND status = 'generating'
  AND updated_at < now() - interval '10 minutes';
```

This interval must be longer than the per-node generation timeout. The generation timeout
(REQ-AGENT-014) applies per-layer call, so a 10-minute staleness threshold is conservative
for a typical node (professor call ≤ 2 minutes for 16K tokens at Anthropic throughput).

### 6.4 Idempotency

The `ExpandToNextLayer` function inserts child nodes only when `pending_count = 0` for the
layer. The INSERT uses `ON CONFLICT DO NOTHING` on a unique index
`(course_id, node_type, ordering)` so parallel dispatches cannot double-insert nodes.

---

## 7. Backward compatibility

### 7.1 Flat courses (tree_mode = false)

All existing courses have `tree_mode = false` (the column defaults to false). The following
code paths are **unmodified**:

- `RunContentGeneration` — called only for flat courses by `pollAndGenerate`.
- `generateAllSections` — unchanged.
- `runReviewLoop` — unchanged (also reused internally by `LayeredRunner`).
- `pollFeedback` — unchanged for flat courses; new `AND c.tree_mode = false` guard skips
  tree-backed courses only.
- All `lesson_content` writes happen only through the flat path.
- `syllabi` and `homework` tables are not touched by the tree path.

### 7.2 Coexistence in RunContentGeneration

`pollAndGenerate` is the only function that changes its branching logic:

```go
func (r *AgentRunner) pollAndGenerate(ctx context.Context) {
    courses, _ := r.agentRepo.ListUntriggeredApprovals(ctx)
    for _, c := range courses {
        if c.TreeMode {
            go r.seedTreeAndGenerateRoot(ctx, c.CourseID, c.StudentID)
        } else {
            go r.RunContentGeneration(ctx, c.CourseID, c.StudentID)
        }
    }
}
```

`ListUntriggeredApprovals` returns the `tree_mode` flag as part of `CourseStudentRow` (one
new field; backward compatible).

### 7.3 Rendering

Flat courses render from `lesson_content`. Tree-backed courses render from `course_nodes`
filtered by `node_type = 'content'`. The frontend uses a discriminator from the course
object (`tree_mode`) to choose the render path. This is specified in G2-S1-T3.

---

## 8. Paid-API cost model

### 8.1 Call multiplication factor

The layered model introduces more Anthropic calls per course than the flat model. For a
6-section syllabus:

| Layer | Nodes per layer | Calls per node (professor + reviewer) | Review iterations (avg) | Total calls |
|-------|----------------|----------------------------------------|-------------------------|-------------|
| root | 1 | 2 | 1.2 | ~2 |
| syllabus | 1 | 2 | 1.2 | ~2 |
| section_goal | 6 | 2 | 1.2 | ~14 |
| concept | 18 (3/section) | 2 | 1.2 | ~43 |
| content | 18 | 2 | 1.2 | ~43 |
| **Total** | **44** | — | — | **~104** |

The flat path calls professor + reviewer once per section (6 × 2 × 1.2 ≈ 14 calls for the
same course). The layered path is roughly 7× more calls for a 5-layer tree with concepts.

Per node, token cost:
- Professor (content): ~16K output tokens + ~2K input ≈ 18K tokens/node
- Reviewer: ~1K input + ~256 output ≈ 1.3K tokens/node
- Per content node: ~19K tokens

For 18 content nodes: ~342K output tokens per course. At claude-sonnet-4-6 pricing, this is
significant. A flat course generates roughly 6 × 18K ≈ 108K output tokens for content alone.

### 8.2 Impact on REQ-AGENT-011 (token cap)

The existing `per_student_token_limit` config key and the `agent_token_usage` table already
aggregate tokens across all calls for a (student, course) pair. The layered model does not
require a new tracking table; it just reaches the cap faster.

Admin draft generation calls use the `(admin_id, draft_id)` context on a relaxed
`agent_token_usage` schema (see T1 §4.7 for the DDL). The `ThrottledClient.Messages` UPSERT
is parameterised on context at call time so no code duplication is needed.

The risk is that a student's cap is exhausted partway through the tree — e.g., after the
concept layer — leaving `content` nodes unpayable. This would silently fail mid-tree without
the student understanding why.

### 8.3 Per-layer token budget sub-cap (binding control)

**This is a binding design control, not a suggestion.** A `per_layer_token_budget` system
config key (admin-settable, type INT64) is added. Default value: `0` (disabled — no per-layer
cap, only the global `per_student_token_limit` applies).

When `per_layer_token_budget > 0`, `LayeredRunner.GenerateLayer` enforces it:

```go
layerBudget := r.configSvc.GetInt64("per_layer_token_budget")
// Default: 0 (disabled). When > 0, enforce before each node call.
```

Implementation:

1. Before calling `professor.GenerateSection` for a node, read `agent_token_usage` for the
   course. If `total_tokens_used + estimated_node_tokens > layerBudget`, transition the node
   to `failed` with reason `token_budget_exceeded`; escalation fires.
2. A `course_nodes.tokens_used` column (nullable INT) optionally tracks per-node token spend
   so the admin can inspect cost breakdown per node. This column is incremented by
   `LayeredRunner` after each professor+reviewer call by reading `msg.Usage`.

**Secondary mitigation: explicit layer expansion as the cost gate.**
The explicit `POST .../layers/{layer}/expand` call (§4.3) is itself a cost gate: the human
must consciously choose to incur the next layer's token cost. The engine never auto-advances.

**Tertiary mitigation: context caching.**
The professor's system prompt for sibling nodes in the same layer shares a large prefix (the
syllabus snippet, profile summary). If the Anthropic API's prompt caching feature is available,
system prompt caching can reduce input token cost for sibling calls by ~90%. This is an
optimization for G2-S3 to evaluate; the design does not depend on it.

Cross-reference: T1 §8.3 describes this same control from the data-model perspective. The
config key name, semantics, and default are identical between the two documents.

### 8.4 Token-cap accounting changes for G2-S3

The Systems Engineer review (G2-S3) should evaluate:

1. Whether `per_student_token_limit` should be raised for tree-mode courses, or whether a
   separate `per_course_tree_token_limit` key is needed.
2. Whether the admin should be shown a cost estimate before approving "expand to next layer"
   (e.g., "generating the concept layer will consume approximately N tokens").
3. Whether partial tree completion (e.g., stopping after section_goals are approved, treating
   those as the delivered content) is a valid cost-control strategy that the product should
   support explicitly.

### 8.5 No change to agent_token_usage schema (student path)

The `agent_token_usage` table (003_course.sql) accumulates tokens via the existing UPSERT in
`ThrottledClient.Messages`. No schema change is required for the student token-tracking path.
The per-node `tokens_used` column on `course_nodes` (if added) is additive and optional; it
is a reporting feature, not a control feature.

The admin-path schema extension (nullable `draft_id` column) is documented in T1 §4.7.

---

## 9. Agent interaction flow

The following describes the full loop for one course in tree mode. Note: the syllabus node is
pre-seeded `approved`; the sequence shows the first active HITL gate at section_goal.
The human must call `POST .../layers/{layer}/expand` to unlock each new layer.

```
Student/Admin                 API Handler          Poller (30s tick)       LayeredRunner          Professor/Reviewer
     |                             |                      |                      |                       |
     |-- approve syllabus -------> |                      |                      |                       |
     |                             |-- UPDATE courses     |                      |                       |
     |                             |   status='syllabus_  |                      |                       |
     |                             |   approved'          |                      |                       |
     |                             |   tree_mode=true --->|                      |                       |
     |                             |                      |                      |                       |
     |                             |         [30s tick]   |                      |                       |
     |                             |                      |-- seedTreeAndGenerate|                       |
     |                             |                      |   Root() ----------->|                       |
     |                             |                      |                      |-- INSERT root node    |
     |                             |                      |                      |   status=approved     |
     |                             |                      |                      |   (pre-seeded)        |
     |                             |                      |                      |-- INSERT syllabus node|
     |                             |                      |                      |   status=approved     |
     |                             |                      |                      |   (pre-seeded;        |
     |                             |                      |                      |   already approved    |
     |                             |                      |                      |   in intake flow)     |
     |                             |                      |                      |                       |
     |                             |                      |                      |-- INSERT section_goal |
     |                             |                      |                      |   nodes (pending)     |
     |                             |                      |                      |-- GenerateLayer(      |
     |                             |                      |                      |   'section_goal')     |
     |                             |                      |                      |-- runReviewLoop() --->|
     |                             |                      |                      |                       |-- professor.GenerateSection
     |                             |                      |                      |                       |-- reviewer.ReviewSection
     |                             |                      |                      |<-- GeneratedSection --|
     |                             |                      |                      |                       |
     |                             |                      |                      |-- UPDATE nodes        |
     |                             |                      |                      |   status=awaiting_    |
     |                             |                      |                      |   review              |
     |                             |                      |                      |                       |
     |                             |                      |                      |-- UPDATE courses      |
     |                             |                      |                      |   status=             |
     |                             |                      |                      |   awaiting_layer_     |
     |                             |                      |                      |   approval,           |
     |                             |                      |                      |   current_layer=      |
     |                             |                      |                      |   section_goal        |
     |                             |                      |                      |                       |
     |<-- SSE: layer_awaiting_    |                      |-- emit layer_        |                       |
     |    review (section_goal)    |                      |   awaiting_review    |                       |
     |                             |                      |   pipeline_event     |                       |
     |                             |                      |                      |                       |
     | [human reviews each node]   |                      |                      |                       |
     |-- PATCH /nodes/{id}/approve->|                     |                      |                       |
     |                             |-- UPDATE node        |                      |                       |
     |                             |   status=approved    |                      |                       |
     | [repeat for all nodes]      |                      |                      |                       |
     |                             |                      |                      |                       |
     | [all approved; human calls expand]                 |                      |                       |
     |-- POST /layers/section_goal/expand --------------->|                      |                       |
     |                             |-- ExpandToNextLayer()|                      |                       |
     |                             |   INSERT concept     |                      |                       |
     |                             |   nodes (pending)    |                      |                       |
     |                             |-- UPDATE courses     |                      |                       |
     |                             |   status='generating'|                      |                       |
     |                             |   current_layer=     |                      |                       |
     |                             |   'concept'          |                      |                       |
     |<-- 200 OK                   |                      |                      |                       |
     |                             |                      |                      |                       |
     |                             |         [30s tick]   |                      |                       |
     |                             |                      |-- pollLayeredGenera- |                       |
     |                             |                      |   tion() ----------->|                       |
     |                             |                      |                      |-- GenerateLayer(      |
     |                             |                      |                      |   'concept') -------->|
     |                             |                      |                      |   ... (same loop)     |
     |                             |                      |                      |                       |
     [repeats for concept and content layers; each requires human /expand call]
     |                             |                      |                      |                       |
     |                             |                      |                      |-- ErrTreeComplete     |
     |                             |                      |                      |-- UPDATE courses      |
     |                             |                      |                      |   status='active'     |
     |<-- SSE: generation_complete |                      |                      |                       |
```

### 9.1 Rejection path

When a human provides feedback on a node:

```
Human                    API Handler               Poller               LayeredRunner
  |-- PATCH .../nodes/   |                           |                     |
  |   {id}/feedback ----> |                           |                     |
  |   {feedback: "..."}   |-- store in node_chats    |                     |
  |                        |   (user role)             |                     |
  |                        |-- UPDATE node             |                     |
  |                        |   status=rejected         |                     |
  |                        |   payload.feedback=<text> |                     |
  |                        |-- check: all awaiting_    |                     |
  |                        |   review nodes resolved?  |                     |
  |                        |   (some rejected, none    |                     |
  |                        |   awaiting) YES:          |                     |
  |                        |   UPDATE courses          |                     |
  |                        |   status=awaiting_regen   |                     |
  |<-- 200 OK              |                           |                     |
  |                        |          [30s tick]       |                     |
  |                        |                           |-- find 'awaiting_regen' courses
  |                        |                           |-- UPDATE rejected nodes -> pending
  |                        |                           |-- UPDATE course status='generating'
  |                        |                           |-- GenerateLayer (rejected nodes only)-->
  |                        |                           |                     |-- professor.RegenerateSection(payload.feedback)
  |                        |                           |                     |-- reviewer.ReviewSection
  |                        |                           |                     |-- UPDATE node awaiting_review
  |                        |                           |                     |-- UPDATE course awaiting_layer_approval
  |<-- SSE: layer_awaiting_review (same layer, re-review)                    |
```

---

## 10. New pipeline event types

The following event types are added to the `pipeline_event_type` enum (migration G2-S2):

| Event type | Payload | Meaning |
|-----------|---------|---------|
| `layer_generation_started` | `{layer, course_id, node_count}` | LayeredRunner begins generating a layer |
| `layer_node_generating` | `{layer, node_id, ordering}` | Individual node generation started |
| `layer_node_generated` | `{layer, node_id, ordering}` | Node reached awaiting_review |
| `layer_node_failed` | `{layer, node_id, error}` | Node reached failed (escalation follows) |
| `layer_awaiting_review` | `{layer, node_ids, course_id}` | All nodes in layer settled; pause gate |
| `layer_approved` | `{layer, course_id}` | Human approved all nodes in layer; expand call issued |
| `layer_node_approved` | `{layer, node_id}` | Individual node approved |
| `layer_node_rejected` | `{layer, node_id, feedback}` | Individual node rejected via /feedback |
| `layer_regeneration_started` | `{layer, node_ids}` | Regeneration of rejected nodes |
| `layer_generation_failed` | `{layer, course_id}` | All nodes in layer failed |
| `tree_generation_complete` | `{course_id}` | All layers approved; course active |

Existing events (`generation_started`, `section_generating`, `section_review_passed`, etc.)
are preserved and continue to fire for flat courses.

---

## 11. Alternatives considered

### 11.1 Replace generateAllSections entirely

**Rejected.** Removing the existing function would break flat-course generation for all
in-flight and future flat courses. The backward-compatibility requirement (from the task
contract) and the CLAUDE.md instruction against piecemeal migration make this untenable.
Wrapping by adding a new code path is safer.

### 11.2 In-memory state machine with persistence only at checkpoints

**Rejected.** Any in-memory state (channels, goroutine-local maps) is lost on restart.
The task contract explicitly requires durability via `course_nodes.status`. Persisting all
transitions to the database costs one DB round-trip per transition, which is acceptable given
generation calls dominate latency (seconds per node, not milliseconds).

### 11.3 Separate layer_runs table

One option was to add a `layer_runs` table analogous to `agent_runs`, one row per layer per
course. This would give the admin a clean history of each layer's generation run.

**Partially adopted.** A new `run_type` value `'tree_layer_generation'` is added to
`agent_run_type` enum, and a new `agent_runs` row is created per layer. This provides the
pipeline event anchor without a new table. For the student path, `agent_runs.course_id`
gives the course FK. For the admin path, `agent_runs.draft_id` gives the draft FK
(T1 §4.8: `agent_runs.course_id` is relaxed to nullable; a `draft_id` column is added).

### 11.4 Streaming generation (SSE during generation)

The professor call is non-streaming today (`client.Messages.New`). Streaming would allow
the frontend to show the content appearing token-by-token. This was considered for the content
layer nodes.

**Deferred.** Streaming complicates the reviewer pipeline (the reviewer needs the full text).
The existing non-streaming approach is kept. If streaming is added in a later sprint, it can
be a display-only optimization (stream to SSE, persist the full response when complete).

### 11.5 Parallel layer generation across multiple layers

Generating layer N+1 concurrently with layer N review was considered for throughput.

**Rejected for this design.** The whole purpose of HITL checkpoints is that the human reviews
each layer before deeper layers are generated. Concurrent generation would negate the cost
control and oversight benefits. Intra-layer parallelism (multiple nodes within one layer
generated concurrently) is retained.

### 11.6 Auto-advance to next layer when all nodes approved

**Rejected.** Auto-advance would bypass the human cost gate and violate root-ask #5 ("HITL
at EACH level"). The engine waits for an explicit `POST .../layers/{layer}/expand` call from
the human before inserting and generating the next layer. This also prevents surprise token
consumption if the human steps away after approving a layer.

---

## 12. Open questions

1. **Who drives tree-mode course creation?** The task contract mentions both admin-author and
   student-build entry points (G2-S1-T3). Does `tree_mode = true` get set at course creation
   time, or can a student toggle it? If admin-only, a guard is needed in the approval handler.
   This is a PM decision; the design assumes it is set at course creation and cannot be changed
   after the first node is generated.

2. **What is the human-reviewer role?** In the current system, the "reviewer" is the AI
   Reviewer agent. In the new system, "human review" is the student or admin using the
   approve/reject endpoints. The AI Reviewer still runs as an internal micro-loop (quality
   gate before the human sees the content), but the human is the final gate. The distinction
   should be made explicit in the UI spec (G2-S1-T3).

3. **Q12.3 — Syllabus checkpoint:** Resolved. The `syllabus` node is pre-seeded `approved`
   at tree initialisation. The first human HITL gate is at the `section_goal` layer. See T1 §7
   and §4.3 / §9 of this document for the full description.

4. **Concept count per section_goal:** The design assumes 3 concepts per section_goal as a
   default. Should this be configurable per course, per section, or fixed? Affects the
   token-cost estimate significantly.

5. **Timeout scope:** REQ-AGENT-014 specifies a per-course `content_generation_timeout_seconds`.
   For layered generation, the timeout should be per-layer (a layer can take longer than a
   flat-course section for large concept counts). Should the existing config key be reused with
   per-layer semantics, or should a separate `tree_layer_generation_timeout_seconds` key be
   added? Proposed: add a new key to avoid silently changing flat-course timeout semantics.

---

## 13. Migration plan summary (for G2-S2)

This design requires the following schema changes, all non-destructive:

| Change | SQL statement | Rollback |
|--------|--------------|---------|
| Add `tree_mode` column to `courses` | `ALTER TABLE courses ADD COLUMN IF NOT EXISTS tree_mode BOOLEAN NOT NULL DEFAULT false` | `ALTER TABLE courses DROP COLUMN IF EXISTS tree_mode` |
| Add `current_layer` column to `courses` | `ALTER TABLE courses ADD COLUMN IF NOT EXISTS current_layer node_type` | `ALTER TABLE courses DROP COLUMN IF EXISTS current_layer` |
| Add `awaiting_layer_approval` to `course_status` enum | `ALTER TYPE course_status ADD VALUE IF NOT EXISTS 'awaiting_layer_approval'` | No rollback for enum values; new status simply unused if feature is disabled |
| Add `awaiting_regeneration` to `course_status` enum | `ALTER TYPE course_status ADD VALUE IF NOT EXISTS 'awaiting_regeneration'` | Same as above |
| Add `tree_layer_generation` to `agent_run_type` enum | `ALTER TYPE agent_run_type ADD VALUE IF NOT EXISTS 'tree_layer_generation'` | Same |
| Add new `pipeline_event_type` values | `ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS ...` (×11 values) | Enum values unused if feature disabled |
| Add `course_nodes` table | Per G2-S1-T1 DDL | `DROP TABLE IF EXISTS course_nodes` |
| Relax `agent_runs.course_id` NOT NULL; add `draft_id` | Per T1 §4.8 DDL | Remove `draft_id` column; restore NOT NULL (requires no null values exist) |
| Relax `agent_token_usage` columns; add `draft_id` | Per T1 §4.7 DDL | Remove `draft_id`; restore NOT NULL on `student_id`, `course_id` |

All changes are applied in a single migration file (e.g., `migrations/021_tree_generation.sql`)
inside a transaction. Enum `ADD VALUE` statements must be executed outside a transaction in
PostgreSQL (they implicitly commit). Migration will split into an enum-add phase and a DDL-
add phase or use `ALTER TYPE ... ADD VALUE` before the `BEGIN` block.

---

*End of TDD G2-S1-T2*
