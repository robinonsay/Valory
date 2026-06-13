# Knowledge-Tree API + SSE Specification

**Task:** G2-S1-T3
**Status:** Design (no production code)
**Depends on:** G2-S1-T1 (data model — `course_nodes` table and RLS)
**Feeds into:** G2-S1-T4 (requirements derivation)

---

## Reconciliation log (rev 2)

Changes applied by the cross-document reconciliation pass (2026-06-13):

- **C2** — Added `tree_mode` discriminator reference in §5.2 (student list-nodes endpoint
  and rendering path). The field name was already correct in T3; no conflicts found.
- **C3 — HITL endpoint reconciliation** — The T3 verbs (`PATCH approve`, `PATCH feedback`,
  `POST regenerate`) are the authoritative human-interaction surface. T2 §4.3 has been
  updated to use these exact paths. The old T2 verbs (`POST approve`, `POST reject`,
  `POST layers/{layer}/approve-all`) no longer exist.
- **C3 — Layer expand model** — Added §5.2.8 (student) and §5.1.11 (admin): explicit
  `POST .../layers/{layer}/expand` endpoints. These are the only mechanism to advance the
  engine to the next layer. The engine does NOT auto-advance. Cross-reference to T2 §4.3.
- **`/feedback` vs `/reject` clarification** — §5.1.8 and §5.2.5 now explicitly state:
  `PATCH .../feedback` is the only rejection endpoint. It requires a non-empty `feedback`
  field. The effect is: store the feedback as a `user`-role `node_chats` row AND in
  `payload.feedback`, then transition status to `rejected`. There is no `/reject` endpoint.
- **node_chats referential integrity** — §4.3 (`node_chats` schema) is replaced by a
  reference to T1 §4.6, which is now the authoritative DDL. The polymorphic no-FK design
  has been dropped. T3 retains only the API-facing description (the two-nullable-FK design
  is transparent to API callers).
- **admin chat-history GET** — §5.1.12 added: `GET /api/v1/admin/drafts/{draftId}/nodes/
  {nodeId}/chat` mirrors the student endpoint at §5.2.7.
- **admin-draft token accounting** — §8.7 updated: `agent_token_usage` is extended with
  a nullable `draft_id` column (see T1 §4.7). Admin calls use `(admin_id, draft_id)` as
  context. No separate table is needed.
- **admin-draft SSE anchoring** — §10.5 updated: `agent_runs` gains a nullable `draft_id`
  column (see T1 §4.8); `GetEventsAfter` is extended with a `draft_id`-scoped overload.
  `agent_runs.course_id` NOT NULL is relaxed. Both changes are authoritative in T1 §4.8.
- **syllabus checkpoint** — §7.1 sequence updated: syllabus node is pre-seeded `approved`;
  the first human HITL gate shown is at `section_goal`. Cross-reference to T1 §7 and T2 §4.3.
- **per-layer token cap** — §8.7 now references `per_layer_token_budget` as a binding
  control (see T1 §8.3 / T2 §8.3).

---

## 1. Overview

This document specifies the HTTP API and SSE event-stream contract for interacting with the
knowledge-tree layer of Valory. It covers two distinct actor surfaces:

- **Admin-authoring:** An admin co-develops a course tree interactively with the Chair agent
  before assigning it to students. Today, `AssignStudents` calls
  `GenerateSyllabusFromParams` synchronously with no preview. The new path adds an
  interactive draft lifecycle so admins can refine the tree before it is frozen for assignment.
- **Student-building:** A student grows their own knowledge tree, node by node, guided by the
  Chair agent with optional HITL refinement.

The design generalises the two existing Chair entry points
(`GenerateSyllabus` at `internal/agent/chair.go:131` and `GenerateSyllabusFromParams` at
`chair.go:191`) into a node-scoped `GenerateNode` / `RefineNode` model that works for both
actors and all node types in the DAG.

The SSE event-stream idiom mirrors the existing pipeline-events stream exposed at
`GET /api/v1/courses/{id}/events` (`internal/agent/handler.go:streamEvents`).

---

## 2. Requirements in scope

No `REQ-*` IDs are claimed by this design task. This TDD is an input to G2-S1-T4 where
requirements will be authored. The design satisfies the six acceptance criteria in the G2-S1-T3
contract.

---

## 3. Actors, roles, and authz model

| Actor | Session role | Context |
|---|---|---|
| Admin | `admin` | May act on any draft owned by their admin ID |
| Student | `student` | May act only on their own courses and nodes |

**Admin-draft isolation:**

Admin-authored trees exist on a separate `course_drafts` record (see section 4.1) that is
not associated with any student until the admin explicitly publishes it to an assignment.
Draft nodes carry `draft_id` in `draft_nodes` rather than a student `course_id`. This keeps
admin work isolated from the `courses` and `course_nodes` RLS policies that gate on
`app.current_user_id`.

**Student-owned node trees:**

Student nodes are rows in `course_nodes` keyed by `course_id` (the existing student course
UUID). RLS on `course_nodes` is specified in the G2-S1-T1 TDD: student-owns via
`app.current_user_id`, admin-all via `app.current_role='admin'`, server-write via
`app.current_role='server'`.

---

## 4. Data model extensions (reference, not authoritative — authoritative is G2-S1-T1)

### 4.1 `course_drafts` table (admin drafts)

This table is the authoritative design element for the admin-authoring path. It is additive
and does not touch existing tables.

```sql
CREATE TABLE course_drafts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title       TEXT NOT NULL,
    topic       TEXT NOT NULL,
    level       TEXT NOT NULL,       -- beginner | intermediate | advanced
    parameters  JSONB NOT NULL DEFAULT '{}',
    status      TEXT NOT NULL DEFAULT 'draft',  -- draft | published
    published_to_assignment_id UUID REFERENCES assignments(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- RLS: admin owns their own drafts
ALTER TABLE course_drafts ENABLE ROW LEVEL SECURITY;
ALTER TABLE course_drafts FORCE ROW LEVEL SECURITY;

CREATE POLICY course_drafts_admin_own ON course_drafts
    FOR ALL
    USING (
        admin_id = NULLIF(current_setting('app.current_user_id', true), '')::UUID
        AND current_setting('app.current_role', true) = 'admin'
    );

CREATE POLICY course_drafts_server_write ON course_drafts
    FOR ALL
    USING (current_setting('app.current_role', true) = 'server');
```

### 4.2 `draft_nodes` table (admin-draft node storage)

Admin draft nodes live in `draft_nodes` rather than `course_nodes` to avoid polluting
student-oriented RLS on `course_nodes` and to make cleanup trivial on draft deletion.

```sql
CREATE TABLE draft_nodes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    draft_id    UUID NOT NULL REFERENCES course_drafts(id) ON DELETE CASCADE,
    parent_id   UUID REFERENCES draft_nodes(id) ON DELETE CASCADE,
    node_type   TEXT NOT NULL,  -- same taxonomy as course_nodes (see G2-S1-T1)
    ordering    INTEGER NOT NULL DEFAULT 0,
    status      TEXT NOT NULL DEFAULT 'pending',
    payload     JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- RLS mirrors course_drafts
ALTER TABLE draft_nodes ENABLE ROW LEVEL SECURITY;
ALTER TABLE draft_nodes FORCE ROW LEVEL SECURITY;

CREATE POLICY draft_nodes_admin_own ON draft_nodes
    FOR ALL
    USING (
        EXISTS (
            SELECT 1 FROM course_drafts cd
            WHERE cd.id = draft_nodes.draft_id
              AND cd.admin_id = NULLIF(current_setting('app.current_user_id', true), '')::UUID
              AND current_setting('app.current_role', true) = 'admin'
        )
    );

CREATE POLICY draft_nodes_server_write ON draft_nodes
    FOR ALL
    USING (current_setting('app.current_role', true) = 'server');
```

### 4.3 `node_chats` table — referential integrity design

The authoritative DDL for `node_chats` is in **G2-S1-T1 §4.6**. The design uses two
nullable, typed FK columns (`course_node_id` and `draft_node_id`, each `ON DELETE CASCADE`)
rather than a polymorphic `node_id` with no FK. This ensures referential integrity and
correct cascade deletes when a node is removed.

The API surface of node chats is specified in §5.1.12 (admin) and §5.2.7 (student). The
schema (including RLS) is in T1 §4.6 and is not duplicated here.

### 4.4 `course_nodes` extensions (student path)

`course_nodes` is specified in G2-S1-T1. No additional columns are required for this API
spec beyond what T1 defines.

---

## 5. API contract

All endpoints live under `/api/v1`. Every endpoint requires:
- A valid session cookie (`__Host-session`) or `Authorization: Bearer <token>` header.
- For state-mutating methods (POST, PATCH, DELETE): the CSRF double-submit token
  (`__Host-csrf` cookie + `X-CSRF-Token` header) enforced by `security.CSRFMiddleware`.
- GET endpoints are exempt from CSRF (safe method) per the existing middleware.

Error envelope (consistent with existing `writeAgentError` pattern):

```json
{ "error": "<CODE>", "message": "<human-readable string>" }
```

### 5.1 Admin-authoring surface

Mounted under `/api/v1/admin/drafts`. The enclosing router group applies
`auth.RequireRole("admin")` and `security.CSRFMiddleware` identically to the existing
admin assignment routes at line 526 of `cmd/server/main.go`.

#### 5.1.1 Create a draft

```
POST /api/v1/admin/drafts
```

Request body:

```json
{
  "title": "Introduction to Rust",
  "topic": "Rust programming language",
  "level": "beginner",
  "parameters": { "focus": "systems programming" }
}
```

Response `201 Created`:

```json
{
  "draft": {
    "id": "<uuid>",
    "admin_id": "<uuid>",
    "title": "Introduction to Rust",
    "topic": "Rust programming language",
    "level": "beginner",
    "parameters": { "focus": "systems programming" },
    "status": "draft",
    "created_at": "<rfc3339>",
    "updated_at": "<rfc3339>"
  }
}
```

Error cases:
- `400 BAD_REQUEST` — missing/invalid fields (`title`, `topic`, `level` required; `level` must
  be one of `beginner`, `intermediate`, `advanced`).
- `401 UNAUTHORIZED` — no valid session.
- `403 FORBIDDEN` — session role is not `admin`.

---

#### 5.1.2 List drafts

```
GET /api/v1/admin/drafts?status=draft&limit=20&cursor=<opaque>
```

Query parameters:
- `status` (optional): filter by `draft` or `published`. Default: all.
- `limit` (optional): 1–100, default 20.
- `cursor` (optional): opaque pagination cursor (base64-encoded `created_at,id` pair).

Response `200 OK`:

```json
{
  "drafts": [ { /* same shape as create response */ } ],
  "next_cursor": "<opaque|null>"
}
```

---

#### 5.1.3 Get a draft

```
GET /api/v1/admin/drafts/{draftId}
```

Response `200 OK`:

```json
{
  "draft": { /* same shape as create response */ },
  "nodes": [
    {
      "id": "<uuid>",
      "draft_id": "<uuid>",
      "parent_id": "<uuid|null>",
      "node_type": "syllabus",
      "ordering": 0,
      "status": "approved",
      "payload": { /* node-type-specific content */ },
      "created_at": "<rfc3339>",
      "updated_at": "<rfc3339>"
    }
  ]
}
```

Error cases:
- `403 FORBIDDEN` — draft does not belong to this admin.
- `404 NOT_FOUND` — draft not found.

---

#### 5.1.4 Delete a draft

```
DELETE /api/v1/admin/drafts/{draftId}
```

Only drafts with `status = 'draft'` (not yet published) can be deleted. Cascades to
`draft_nodes` and `node_chats` (via the `draft_node_id` FK in `node_chats`) automatically.

Response `204 No Content`.

Error cases:
- `403 FORBIDDEN` — not owner.
- `404 NOT_FOUND` — not found.
- `409 CONFLICT` + code `DRAFT_ALREADY_PUBLISHED` — cannot delete a published draft.

---

#### 5.1.5 Generate a node (admin)

This endpoint is the generalisation of `GenerateSyllabusFromParams`. It triggers the Chair
to generate content for the specified node type within the draft.

```
POST /api/v1/admin/drafts/{draftId}/nodes/generate
```

Request body:

```json
{
  "node_type": "syllabus",
  "parent_id": null,
  "parameters": { "additional_context": "Focus on async patterns" }
}
```

Fields:
- `node_type` (required): one of `syllabus`, `section_goal`, `concept`, `content` (not
  `root` — the root node is implicitly created with the draft).
- `parent_id` (optional UUID): the parent node in the draft tree. `null` is valid only for
  `syllabus`-type nodes (direct children of the implicit root).
- `parameters` (optional object): supplementary context that will be appended to the Chair
  system prompt for this generation call.

Response `202 Accepted`:

```json
{
  "node": {
    "id": "<uuid>",
    "draft_id": "<uuid>",
    "parent_id": "<uuid|null>",
    "node_type": "syllabus",
    "ordering": 0,
    "status": "generating",
    "payload": {},
    "created_at": "<rfc3339>",
    "updated_at": "<rfc3339>"
  },
  "run_id": "<uuid>"
}
```

The actual content arrives via the node-scoped SSE stream (section 5.3). The `run_id` lets
the client subscribe to events from the correct agent run.

Error cases:
- `400 BAD_REQUEST` — invalid `node_type` or missing required fields.
- `403 FORBIDDEN` — not owner.
- `404 NOT_FOUND` — draft or `parent_id` not found.
- `409 CONFLICT` + code `NODE_ALREADY_GENERATING` — a generation is already in progress for
  this node position.

---

#### 5.1.6 Refine a node (admin)

Sends a chat-turn to the Chair scoped to one draft node. The Chair reads the node's existing
payload as context and the accumulated `node_chats` history, then generates a revised payload.

```
POST /api/v1/admin/drafts/{draftId}/nodes/{nodeId}/refine
```

Request body:

```json
{
  "message": "Add a section on error handling idioms"
}
```

Response `202 Accepted`:

```json
{
  "node": { /* current node shape, status = 'generating' */ },
  "run_id": "<uuid>"
}
```

The revised node content arrives via SSE. The `message` is stored in `node_chats`
(`draft_node_id` FK) as a `user`-role message before invoking the Chair so that history
accumulates.

Error cases:
- `400 BAD_REQUEST` — empty message.
- `403 FORBIDDEN` — not owner.
- `404 NOT_FOUND` — draft or node not found.
- `409 CONFLICT` + code `NODE_ALREADY_GENERATING` — concurrent generation in progress.

---

#### 5.1.7 Approve a node (admin)

```
PATCH /api/v1/admin/drafts/{draftId}/nodes/{nodeId}/approve
```

No request body. Transitions node `status` from `awaiting_review` to `approved`. An approved
node's payload is frozen for the publish step.

Response `200 OK`:

```json
{
  "node": { /* node with status = 'approved' */ }
}
```

Error cases:
- `403 FORBIDDEN` — not owner.
- `404 NOT_FOUND`.
- `409 CONFLICT` + code `NODE_NOT_REVIEWABLE` — node is still `generating` or `pending`.

---

#### 5.1.8 Reject / request feedback on a node (admin)

```
PATCH /api/v1/admin/drafts/{draftId}/nodes/{nodeId}/feedback
```

This is the **only** endpoint for rejecting a node. There is no separate `/reject` endpoint.

Request body:

```json
{
  "feedback": "The third section is too advanced for beginners"
}
```

`feedback` is required and must be non-empty (returns `400 BAD_REQUEST` if missing or empty).

Effect:
1. Stores the feedback as a `user`-role row in `node_chats` (keyed by `draft_node_id`).
2. Stores the feedback text in `draft_nodes.payload.feedback` for use in the regeneration
   prompt.
3. Transitions node `status` to `rejected`.
4. The node is now eligible for `POST .../regenerate` or for the poller to pick it up.

Response `200 OK`:

```json
{
  "node": { /* node with status = 'rejected' */ }
}
```

Error cases:
- `400 BAD_REQUEST` — empty or missing `feedback`.
- `403 FORBIDDEN` — not owner.
- `404 NOT_FOUND`.

---

#### 5.1.9 Regenerate a node (admin)

```
POST /api/v1/admin/drafts/{draftId}/nodes/{nodeId}/regenerate
```

No request body. Re-triggers the Chair generation for a node that has been `rejected` or
`failed`. The node's `payload.feedback` (set by the `/feedback` endpoint) is included in the
Chair's re-prompt. Transitions node `status` to `generating`.

Response `202 Accepted`:

```json
{
  "node": { /* node with status = 'generating' */ },
  "run_id": "<uuid>"
}
```

Error cases:
- `403 FORBIDDEN` — not owner.
- `404 NOT_FOUND`.
- `409 CONFLICT` + code `NODE_NOT_REGENERABLE` — node is not in `rejected` or `failed` status.

---

#### 5.1.10 Publish draft to assignment

Converting a fully-approved draft into an assignable course is the moment the admin-authored
tree becomes an actual assignment. This replaces the synchronous syllabus generation step
in `AssignStudents` for tree-backed courses.

```
POST /api/v1/admin/drafts/{draftId}/publish
```

Request body:

```json
{
  "assignment_title": "Rust for Systems Engineers",
  "assignment_id": "<uuid|null>"
}
```

Fields:
- `assignment_id` (optional): if supplied, publishes the draft to an existing assignment
  (adds it as the tree template). If null, a new assignment is created using the draft's
  title/topic/level.

Precondition: ALL nodes in the draft must have `status = 'approved'` or the endpoint returns
`409 CONFLICT + DRAFT_HAS_UNAPPROVED_NODES`.

Response `200 OK`:

```json
{
  "assignment_id": "<uuid>",
  "draft": { /* draft with status = 'published' */ }
}
```

The published draft's tree template is recorded on the assignment so that when students are
later enrolled via the existing `POST /api/v1/admin/assignments/{id}/students` endpoint, each
student's `course_nodes` are copied from `draft_nodes` with status reset to `pending` (the
student then grows them through the normal student-building flow).

Error cases:
- `403 FORBIDDEN` — not owner.
- `404 NOT_FOUND`.
- `409 CONFLICT` + `DRAFT_ALREADY_PUBLISHED` — already published.
- `409 CONFLICT` + `DRAFT_HAS_UNAPPROVED_NODES` — tree is incomplete.

---

#### 5.1.11 Expand draft to next layer (admin)

```
POST /api/v1/admin/drafts/{draftId}/layers/{layer}/expand
```

Unlocks generation of the next layer in the draft tree after all nodes in `{layer}` are
`approved`. This is the admin equivalent of the student `POST .../layers/{layer}/expand`
endpoint (§5.2.8).

`{layer}` is the **current** layer whose nodes are all approved (e.g., `section_goal`).

Precondition: all `draft_nodes` with the given `node_type = {layer}` must have
`status = 'approved'` and `pending_count = 0`. Returns `409 CONFLICT +
LAYER_NOT_FULLY_APPROVED` if not.

Effect:
1. Inserts child nodes for the next layer (`status = 'pending'`).
2. Sets `course_drafts.status = 'generating'` (or a draft-equivalent field if the draft
   lifecycle uses different status values — see open question 10.6).
3. The node-generation poller picks up the draft and calls `LayeredRunner.GenerateLayer`
   on the next tick.

Response `200 OK`:

```json
{
  "next_layer": "concept",
  "inserted_node_count": 6
}
```

Error cases:
- `403 FORBIDDEN` — not owner.
- `404 NOT_FOUND` — draft not found or `{layer}` has no nodes.
- `409 CONFLICT` + `LAYER_NOT_FULLY_APPROVED` — some nodes not yet approved.
- `409 CONFLICT` + `TREE_ALREADY_COMPLETE` — `{layer}` is the final layer (`content`).

Cross-reference: T2 §4.3 specifies that layer expansion is always explicit and is the human
cost gate. The engine never auto-advances.

---

#### 5.1.12 Get node chat history (admin)

```
GET /api/v1/admin/drafts/{draftId}/nodes/{nodeId}/chat
```

Returns the `node_chats` history for the given draft node in ascending `created_at` order.
Queries via `draft_node_id` FK (see T1 §4.6).

Response `200 OK`:

```json
{
  "messages": [
    {
      "id": "<uuid>",
      "role": "user",
      "content": "Add more examples",
      "created_at": "<rfc3339>"
    },
    {
      "id": "<uuid>",
      "role": "assistant",
      "content": "Here is the revised section with additional examples...",
      "created_at": "<rfc3339>"
    }
  ]
}
```

Error cases:
- `403 FORBIDDEN` — draft not owned by this admin.
- `404 NOT_FOUND` — draft or node not found.

---

### 5.2 Student-building surface

Mounted under `/api/v1/courses/{courseId}/nodes`. The enclosing router group applies the
existing `authMW` (session + consent gate) and `security.CSRFMiddleware`. Students can only
act on nodes belonging to their own courses — enforced by `course_nodes` RLS.

#### 5.2.1 List nodes for a course

```
GET /api/v1/courses/{courseId}/nodes
```

Optional query parameters:
- `node_type` — filter to a specific layer (e.g. `section_goal`).
- `parent_id` — filter to children of a specific parent.

Response `200 OK`:

```json
{
  "nodes": [
    {
      "id": "<uuid>",
      "course_id": "<uuid>",
      "parent_id": "<uuid|null>",
      "node_type": "section_goal",
      "ordering": 1,
      "status": "approved",
      "payload": { /* AsciiDoc content or structured JSON per node type */ },
      "created_at": "<rfc3339>",
      "updated_at": "<rfc3339>"
    }
  ]
}
```

The `GET /courses/{courseId}` response includes a `tree_mode` boolean field. The frontend
uses this discriminator to choose between the flat (`lesson_content`) and tree (`course_nodes`)
render paths. See T1 §5.2 for the rendering decision table.

RLS enforcement: the SELECT on `course_nodes` applies the student-own policy via
`app.current_user_id`. The handler uses the request-scoped connection (from
`auth.ConnFromContext`) so the GUC is set.

---

#### 5.2.2 Generate a node (student)

```
POST /api/v1/courses/{courseId}/nodes/generate
```

Request body:

```json
{
  "node_type": "concept",
  "parent_id": "<uuid>",
  "hint": "I want to focus on ownership vs. borrowing"
}
```

Fields:
- `node_type` (required).
- `parent_id` (required for non-root nodes).
- `hint` (optional): natural language hint stored as a `user`-role message in `node_chats`
  (keyed by `course_node_id`) before generation, giving the Chair context without requiring a
  full refine exchange.

Response `202 Accepted`:

```json
{
  "node": {
    "id": "<uuid>",
    "course_id": "<uuid>",
    "parent_id": "<uuid>",
    "node_type": "concept",
    "ordering": 0,
    "status": "generating",
    "payload": {},
    "created_at": "<rfc3339>",
    "updated_at": "<rfc3339>"
  },
  "run_id": "<uuid>"
}
```

Error cases:
- `400 BAD_REQUEST` — invalid node_type or missing parent_id for non-root.
- `403 FORBIDDEN` — course not owned by this student (RLS or explicit check).
- `404 NOT_FOUND` — course or parent node not found.
- `409 CONFLICT` + `NODE_ALREADY_GENERATING` — concurrent generation for this node.

---

#### 5.2.3 Refine a node (student)

```
POST /api/v1/courses/{courseId}/nodes/{nodeId}/refine
```

Request body:

```json
{
  "message": "Can you make this concept less theoretical and more example-driven?"
}
```

Response `202 Accepted`:

```json
{
  "node": { /* node with status = 'generating' */ },
  "run_id": "<uuid>"
}
```

The `message` is stored in `node_chats` (keyed by `course_node_id`) as a `user`-role message
before invoking the Chair.

---

#### 5.2.4 Approve a node (student)

```
PATCH /api/v1/courses/{courseId}/nodes/{nodeId}/approve
```

No request body. Transitions node `status` from `awaiting_review` to `approved`.

After this call, the handler checks the layer aggregate (see T2 §4.2). If all nodes in the
layer are now settled (no more `awaiting_review` nodes), the SSE stream emits
`layer_awaiting_expand` to prompt the student to call the expand endpoint when ready.

Response `200 OK`:

```json
{
  "node": { /* node with status = 'approved' */ }
}
```

Error cases:
- `403 FORBIDDEN` — course not owned by student.
- `404 NOT_FOUND`.
- `409 CONFLICT` + `NODE_NOT_REVIEWABLE` — node is still `generating` or `pending`.

---

#### 5.2.5 Request feedback on a node (student)

```
PATCH /api/v1/courses/{courseId}/nodes/{nodeId}/feedback
```

This is the **only** endpoint for rejecting a node. There is no separate `/reject` endpoint.

Request body:

```json
{
  "feedback": "Too brief — please expand the examples"
}
```

`feedback` is required and must be non-empty (returns `400 BAD_REQUEST` if missing or empty).

Effect:
1. Stores the feedback as a `user`-role row in `node_chats` (keyed by `course_node_id`).
2. Stores the feedback text in `course_nodes.payload.feedback` for use in the regeneration
   prompt.
3. Transitions node `status` to `rejected`.

The poller (`pollLayeredGeneration`, T2 §5.3) picks up `rejected` nodes and re-triggers
generation with the stored feedback.

Response `200 OK`:

```json
{
  "node": { /* node with status = 'rejected' */ }
}
```

Error cases:
- `400 BAD_REQUEST` — empty or missing `feedback`.
- `403 FORBIDDEN` — not owner.
- `404 NOT_FOUND`.

---

#### 5.2.6 Regenerate a node (student)

```
POST /api/v1/courses/{courseId}/nodes/{nodeId}/regenerate
```

No request body. Re-triggers Chair generation for a node in `rejected` or `failed` status.
The node's `payload.feedback` (set by the `/feedback` endpoint) is included in the Chair's
re-prompt. Transitions node `status` to `generating`.

Response `202 Accepted`:

```json
{
  "node": { /* node with status = 'generating' */ },
  "run_id": "<uuid>"
}
```

Error cases:
- `403 FORBIDDEN` — not owner.
- `404 NOT_FOUND`.
- `409 CONFLICT` + `NODE_NOT_REGENERABLE` — node is not in `rejected` or `failed` status.

---

#### 5.2.7 Get node chat history (student)

```
GET /api/v1/courses/{courseId}/nodes/{nodeId}/chat
```

Returns the `node_chats` history for the given node in ascending `created_at` order. Queries
via `course_node_id` FK (see T1 §4.6).

Response `200 OK`:

```json
{
  "messages": [
    {
      "id": "<uuid>",
      "role": "user",
      "content": "Add more examples",
      "created_at": "<rfc3339>"
    },
    {
      "id": "<uuid>",
      "role": "assistant",
      "content": "Here is the revised section with additional examples...",
      "created_at": "<rfc3339>"
    }
  ]
}
```

Error cases:
- `403 FORBIDDEN` — course not owned by this student.
- `404 NOT_FOUND` — course or node not found.

---

#### 5.2.8 Expand to next layer (student)

```
POST /api/v1/courses/{courseId}/layers/{layer}/expand
```

Unlocks generation of the next layer after all nodes in `{layer}` are `approved`. This is the
explicit human cost gate; the engine does not auto-advance. See T2 §4.3 for the full protocol
and rationale.

`{layer}` is the **current** fully-approved layer (e.g., `section_goal`). Valid values match
`node_type_enum` (T1 §4.1): `root`, `syllabus`, `section_goal`, `concept`, `content`.

Precondition: all `course_nodes` for `courseId` with `node_type = {layer}` must have
`status = 'approved'` and `pending_count = 0`. Returns `409` if not.

Effect (delegated to `LayeredRunner.ExpandToNextLayer` — see T2 §5.2):
1. Inserts child nodes for the next layer (`status = 'pending'`).
2. Sets `courses.status = 'generating'` and `courses.current_layer = <nextLayer>`.
3. The poller picks up the course on the next 30s tick and calls `GenerateLayer`.

Response `200 OK`:

```json
{
  "next_layer": "concept",
  "inserted_node_count": 6
}
```

Error cases:
- `400 BAD_REQUEST` — `{layer}` is not a valid `node_type`.
- `403 FORBIDDEN` — course not owned by this student.
- `404 NOT_FOUND` — course not found or `{layer}` has no nodes.
- `409 CONFLICT` + `LAYER_NOT_FULLY_APPROVED` — some nodes not yet `approved`.
- `409 CONFLICT` + `TREE_ALREADY_COMPLETE` — `{layer}` is `content` (final layer).

Cross-reference: T2 §4.3 specifies that the poller does NOT call `ExpandToNextLayer`; only
this HTTP handler does. T2 §5.2 (`ExpandToNextLayer` Go signature) is called by this handler.

---

### 5.3 Node-scoped SSE event stream

The event stream mirrors the existing pipeline-events stream at
`GET /api/v1/courses/{id}/events` (`agent/handler.go:streamEvents`). The wire format is
identical; only the filter and mounting point differ.

#### 5.3.1 Admin stream (per draft)

```
GET /api/v1/admin/drafts/{draftId}/events?after=<eventId>
```

Auth: admin session, `RequireRole("admin")`. No CSRF (GET is safe).

The handler queries `pipeline_events` joined to `agent_runs` filtered by `draft_id` (using
the `GetEventsAfterForDraft` repository overload — see T1 §4.8). The `after` cursor semantics
are identical to the student stream.

#### 5.3.2 Student stream (per course)

The existing `GET /api/v1/courses/{courseId}/events` stream is reused. Node-level events
are injected into the same `pipeline_events` table attached to an `agent_runs` row — the
same mechanism used today. The client distinguishes node events from course-level events by
`event_type` (see section 5.3.4).

No new SSE endpoint is needed for the student path.

#### 5.3.3 Response headers (both streams)

```
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
X-Accel-Buffering: no
```

Opening keepalive comment sent immediately, then a 2-second poll ticker (same as existing):

```
: keepalive

```

#### 5.3.4 Event format (wire)

Each SSE event follows the existing format from `agent/handler.go`:

```
event: <event_type>
data: <json-encoded envelope>

```

JSON envelope:

```json
{
  "id": "<pipeline_event uuid>",
  "agent_run_id": "<uuid>",
  "event_type": "<string>",
  "payload": { /* event-specific fields */ },
  "emitted_at": "<rfc3339nano>"
}
```

#### 5.3.5 Node-specific event types

| `event_type` | Emitted when | Payload fields |
|---|---|---|
| `node_generating` | Chair begins generating a node | `node_id`, `node_type`, `draft_id\|course_id` |
| `node_ready` | Chair finishes; node transitions to `awaiting_review` | `node_id`, `node_type`, `payload_summary` (first 200 chars) |
| `node_approved` | Node approved | `node_id`, `node_type` |
| `node_rejected` | Node rejected via `/feedback` | `node_id`, `node_type` |
| `node_error` | Generation failed (`failed` status) | `node_id`, `node_type`, `error` |
| `layer_awaiting_expand` | All nodes in layer approved; waiting for human `/expand` call | `layer`, `course_id\|draft_id` |
| `status_change` | Course/draft lifecycle change | `status` (existing, reused) |
| `api_failure` | Chair API error | `error` (existing, reused) |

The `node_generating` and `node_ready` events replace the need for polling. On receipt of
`node_ready` the client can refresh the node detail (GET on the node endpoint) to display
the full content. On receipt of `layer_awaiting_expand` the client can show the "expand to
next layer" button.

#### 5.3.6 Resumption

The `?after=<eventId>` query parameter is supported identically to the existing stream: the
server fetches events with `emitted_at >` the referenced event's timestamp. The admin draft
stream queries `pipeline_events` joined to `agent_runs` filtered by `draft_id` (via
`GetEventsAfterForDraft` — see T1 §4.8). The wire API is the same for both streams.

#### 5.3.7 Keepalive and connection lifetime

The 2-second ticker sends a `flush` even when no new events exist. This keeps the TCP
connection open through proxies and the browser's event-source reconnect logic. The server
respects `r.Context().Done()` to detect client disconnection (identical to existing
`streamEvents`). Long-lived admin preview sessions are expected; the design does not impose
a server-side timeout beyond the session inactivity period.

---

## 6. Chair generalisation: GenerateNode / RefineNode

### 6.1 Current entry points

| Function | Actor | Mechanism |
|---|---|---|
| `GenerateSyllabus` | Student | Reads full intake history; generates AsciiDoc |
| `GenerateSyllabusFromParams` | Admin assignment | No history; uses topic/level/params prompt |

Both call `callClaude` synchronously and return the generated content to the caller.

### 6.2 Generalised interface

The following two methods are added to `Chair` (or a new `NodeChair` struct that wraps the
existing `Chair`). They replace the two existing entry points for the tree-backed path; the
flat-course entry points remain unchanged for coexistence.

```go
// GenerateNode generates content for one course_node or draft_node from scratch.
// nodeType controls which system prompt is selected. priorContext is the
// accumulated node_chats history for the node (may be nil for first generation).
// The generated content is returned as a JSON-serialisable payload (the structure
// is node-type-specific, defined below).
//
// Token usage is charged against agent_token_usage(actorID, contextID) where:
//   - actorID is studentID (student path) or adminID (admin path)
//   - contextID is courseID (student) or draftID (admin)
//   See T1 §4.7 for the agent_token_usage schema extension.
func (c *Chair) GenerateNode(
    ctx       context.Context,
    actorID   uuid.UUID,
    contextID uuid.UUID, // course_id or draft_id
    nodeType  string,
    topic     string,
    level     string,
    parameters json.RawMessage,
    priorContext []NodeChatMessage,
) (json.RawMessage, error)

// RefineNode refines an existing node's content based on new human feedback.
// existingPayload is the current node payload; feedback is the human message.
// The updated payload is returned; node_chats must be persisted by the caller
// before invoking this method so the Chair sees the full conversation including
// the new message.
func (c *Chair) RefineNode(
    ctx             context.Context,
    actorID         uuid.UUID,
    contextID       uuid.UUID,
    nodeType        string,
    existingPayload json.RawMessage,
    chatHistory     []NodeChatMessage,
) (json.RawMessage, error)

// NodeChatMessage is a single turn in the per-node conversation.
type NodeChatMessage struct {
    Role    string // "user" | "assistant"
    Content string
}
```

### 6.3 Node-type prompts

Each `nodeType` maps to a dedicated system prompt following the pattern of the existing
`assignmentSyllabusPrompt`, `syllabusSystemPrompt`, and `intakeSystemPrompt` functions:

| `node_type` | Prompt focus |
|---|---|
| `syllabus` | Course overview, section list, objectives — mirrors `assignmentSyllabusPrompt` |
| `section_goal` | Detailed goals for one section given the syllabus as context |
| `concept` | Single concept explanation, examples, exercises |
| `content` | Full AsciiDoc lesson content (max 500 lines, `include::` for composition) |

For `RefineNode`, the system prompt appends the existing node payload and the full
`node_chats` history so the Chair can produce a revised version without losing prior context.

### 6.4 Background execution

All generation calls are dispatched in background goroutines by the handler (same pattern as
the existing `handleIntakeChat` goroutine that launches `GenerateSyllabus`). The HTTP
response returns `202 Accepted` immediately; completion is signalled via SSE. The goroutine
uses a detached `context.Background()` so that the HTTP connection being closed does not
cancel the generation mid-flight.

The `agent_runs` table anchors the pipeline events. For each `GenerateNode` / `RefineNode`
call the handler:

1. Creates an `agent_run` row (`run_type = 'node_generation'`), with `course_id` (student
   path) or `draft_id` (admin path) as the context anchor. See T1 §4.8 for the schema.
2. Updates the node `status` to `generating` (server-pool connection).
3. Emits a `node_generating` pipeline event.
4. Launches the goroutine.
5. On completion: updates node `status` to `awaiting_review`, writes the payload, emits
   `node_ready` or `node_error`, sets `agent_run.status = 'completed'|'failed'`.

---

## 7. Admin-authoring end-to-end flow

### 7.1 Sequence

The syllabus node is pre-seeded `approved` by `seedTreeAndGenerateRoot` (T1 §7, T2 §4.3).
The first human HITL gate is at the `section_goal` layer. The sequence below shows the
admin expanding from the initial draft state.

```
Admin                  API                        Chair (background)
  |                     |                                |
  |-- POST /admin/drafts ------>                         |
  |<-- 201 draft{id}             |                       |
  |                              |                       |
  |-- POST /admin/drafts/{id}/nodes/generate (node_type=section_goal) -->
  |<-- 202 node{id, status=generating}                   |
  |                              |-- GenerateNode() ---> |
  |-- GET /admin/drafts/{id}/events (SSE)                |
  |<-- event: node_generating                            |
  |                              |                       |
  |                              |<-- payload ----------- |
  |                              |-- update node{status=awaiting_review}
  |<-- event: node_ready                                 |
  |                              |                       |
  |-- GET /admin/drafts/{id}/nodes/{nodeId} -- view payload
  |                              |                       |
  | (optionally refine)          |                       |
  |-- POST .../nodes/{nodeId}/refine (message)           |
  |<-- 202 node{status=generating}                       |
  |<-- event: node_generating ... node_ready             |
  |                              |                       |
  |-- PATCH .../nodes/{nodeId}/approve                   |
  |<-- 200 node{status=approved}                         |
  |                              |                       |
  | (repeat for all section_goal nodes)                  |
  |                              |                       |
  | [all section_goal nodes approved]                    |
  |<-- event: layer_awaiting_expand (section_goal layer) |
  |                              |                       |
  |-- POST /admin/drafts/{id}/layers/section_goal/expand |
  |<-- 200 {next_layer:"concept", inserted_node_count:6} |
  |                              |                       |
  | (poller generates concept nodes; repeat HITL cycle)  |
  |                              |                       |
  | (all layers approved)        |                       |
  |-- POST /admin/drafts/{id}/publish                    |
  |<-- 200 {assignment_id}                               |
  |                              |                       |
  |-- POST /admin/assignments/{assignmentId}/students    |
  |   (existing endpoint, unchanged)                     |
```

### 7.2 Draft-to-assignment promotion

When `POST /admin/drafts/{id}/publish` is called with all nodes approved:

1. A new (or existing) `assignments` row is created/updated with `draft_id` recorded.
2. The draft `status` is set to `published`.
3. When `AssignStudents` is later called, if the assignment has a `draft_id`, the
   `assignOneStudent` flow copies `draft_nodes` into `course_nodes` (with
   `status = 'pending'`, `course_id` set to the new course) rather than calling
   `GenerateSyllabusFromParams`. This keeps the existing `AssignStudents` code path alive
   for non-tree assignments (coexistence).

The `assignments` table gains a nullable `draft_id UUID REFERENCES course_drafts(id)` column.
For non-tree assignments this is null; the existing synchronous syllabus path is used.

---

## 8. Security and RLS

### 8.1 Authentication and session

All endpoints are behind the existing `authMW` session middleware (`internal/auth/middleware.go`)
or `auth.RequireRole("admin")`. Sessions carry `app.current_user_id` and `app.current_role`
as PostgreSQL GUCs on the request-scoped connection.

### 8.2 CSRF

State-mutating admin endpoints (POST/PATCH/DELETE) are inside the `security.CSRFMiddleware`
group exactly as the existing admin assignment routes. The double-submit pattern
(`__Host-csrf` cookie + `X-CSRF-Token` header, compared with `hmac.Equal`) applies.

Student endpoints are inside the existing `security.CSRFMiddleware` group that wraps all
`/api/v1` routes except `/setup`, `/auth`, and `/password-reset`.

SSE endpoints (GET) are exempt from CSRF by the middleware's safe-method check.

### 8.3 Admin-draft isolation

Admin drafts and draft nodes are in `course_drafts` / `draft_nodes`, not in the `courses` /
`course_nodes` tables. RLS on those tables does not expose admin draft content to students.
The admin's own `course_drafts` policy gates on `admin_id =
NULLIF(current_setting('app.current_user_id', true), '')::UUID` with the `NULLIF` guard
required by the `force-rls-superuser-test-masking` memory (prevents superuser test bypass).

### 8.4 Student node isolation

`course_nodes` RLS (spec'd in G2-S1-T1) gates all student node access via
`course_id → courses.student_id = app.current_user_id`. Students cannot read or write
other students' nodes.

Handlers perform an explicit ownership check before issuing node reads/writes using the
request-scoped connection (same pattern as `courseOwnedBy` in `agent/handler.go:674`).

### 8.5 Server-write path

Node generation runs on the `serverPool` (same as existing agent pipeline). The `draft_nodes`
and `course_nodes` `server_write` policies gate on `app.current_role='server'`. The
`serverPool.BeforeAcquire` hook sets `app.current_role='server'` on every acquired
connection, matching the existing pattern in `cmd/server/main.go:87–96`.

### 8.6 Admin cross-draft access

An admin may only act on drafts where `admin_id = current_user_id`. Handlers validate this
with an explicit SELECT before any mutation (or rely on RLS to return 0 rows). A 404 is
returned (not 403) when a draft is not found to avoid leaking existence.

Implementation note: returning 404 vs 403 is a policy choice. For admin surfaces (unlike
student surfaces where existence-leaking is a higher concern), 403 is also acceptable for
clear error messaging. The recommended default is 403 when the admin is authenticated but
the resource belongs to another admin, and 404 when no row exists at all. Handlers should
issue a SELECT and inspect `rowsAffected` to distinguish the two cases.

### 8.7 Rate limiting and token caps

`GenerateNode` and `RefineNode` call `c.client.Messages` via `ThrottledClient`
(`internal/agent/throttled_client.go`), which enforces the per-student/per-admin token cap
already configured in the admin config.

**Admin-draft token accounting:** `agent_token_usage` is extended with a nullable `draft_id`
column (T1 §4.7). Admin-path calls use `(admin_id, draft_id)` as the UPSERT conflict target.
No separate table is needed.

**Per-layer token budget:** The `per_layer_token_budget` system config key is a binding
control (default `0` = disabled). When non-zero, `LayeredRunner.GenerateLayer` enforces it
before each node call (T1 §8.3, T2 §8.3). This applies equally to admin draft nodes and
student course nodes.

---

## 9. Alternatives considered

### 9.1 Single `course_nodes` table for admin drafts

Using a `draft_id` column on `course_nodes` (with `course_id` nullable) would simplify the
schema to one table. Rejected because:
- RLS on `course_nodes` is designed around `course_id → courses.student_id` ownership.
  Injecting `draft_id` as a parallel ownership axis complicates the policy and risks
  accidental disclosure.
- Cascaded deletes for draft cleanup are simpler with a dedicated table.
- A separate `course_drafts` table makes the admin-authoring lifecycle explicit and auditable.

### 9.2 JSONB `node_chat_history` column on `course_nodes`

Storing per-node chat turns as a JSONB array column would avoid a join but:
- Makes the history append-only-unsafe (concurrent updates cause JSONB array-append races).
- Prevents indexing individual messages by `created_at`.
- Makes history purge (GDPR / retention) harder to target.
Separate `node_chats` table is preferred.

### 9.3 Streaming generation via chunked SSE

The Anthropic SDK supports streaming responses. Rather than waiting for the full payload
before emitting `node_ready`, we could stream token-by-token into the SSE event stream.
Deferred: the existing pipeline does not stream; adding streaming to the Chair adds
significant complexity (partial payload storage, reconnection state). The 2-second poll
with a completion event is sufficient for MVP.

### 9.4 Reuse `GET /courses/{id}/events` for admin drafts

Admin drafts do not have a `course_id`, so the existing event stream cannot be reused
without adding a course row for each draft (which would pollute the courses table). A
separate `GET /admin/drafts/{id}/events` endpoint is cleaner.

### 9.5 Synchronous approve-and-assign in a single request

Making `POST /admin/drafts/{id}/publish` also trigger student assignment avoids a second
round-trip. Rejected because the existing `AssignStudents` endpoint handles partial-success
per-student and is already deployed; reusing it keeps the blast radius of this change small.

### 9.6 Polymorphic `node_id` in `node_chats` (rejected)

The original draft used a single `node_id UUID NOT NULL` column with a `node_owner_type`
discriminator and no foreign key. PostgreSQL cannot enforce a FK to "either this table or
that table", so this design would produce orphan rows on node deletion. Replaced by two
nullable typed FK columns (`course_node_id`, `draft_node_id`) each `ON DELETE CASCADE`.
See T1 §4.6 for the authoritative DDL.

### 9.7 Auto-advance to next layer (rejected)

Auto-advancing to the next layer when all nodes in the current layer are approved was
considered for usability. Rejected because it bypasses the human cost gate (root-ask #5)
and can consume large token budgets without the user's explicit consent. The explicit
`POST .../layers/{layer}/expand` call is required. See T2 §11.6 for the full rationale.

---

## 10. Open questions

### 10.1 Token accounting for admin drafts

Resolved: option (a) from the original question — add a nullable `draft_id` column to
`agent_token_usage`. See T1 §4.7 for the full DDL. Admin-path calls use `(admin_id, draft_id)`
as the conflict target. The existing student-path UPSERT is unchanged.

### 10.2 `node_type = 'root'` implicit creation

The `course_drafts` row itself does not serve as the root node. A `draft_nodes` row with
`node_type = 'root'` is always inserted on draft creation, for uniformity with `course_nodes`.
The root's `payload` holds the `topic` and `level` from the draft creation request.

### 10.3 Pagination for node list

`GET /courses/{courseId}/nodes` currently returns all nodes for a course. If courses grow to
hundreds of nodes, pagination will be needed. For MVP, returning all nodes is acceptable
(the tree is bounded by the syllabus depth). A `limit` / `cursor` parameter can be added
in a future sprint.

### 10.4 Concurrent generation guard

The `NODE_ALREADY_GENERATING` conflict check requires an atomic test-and-set. The handler
must UPDATE the node status from `pending/awaiting_review/rejected` to `generating` with a
WHERE clause guard (e.g. `WHERE status != 'generating'`) and check `rowsAffected = 1`.
This is the same pattern as `courses.intake_kickoff_sent` in `chair.go:kickoffIntake`.
The implementation detail is left to the engineer but must be called out in the requirements.

### 10.5 Draft SSE and `pipeline_events` join

Resolved: `agent_runs` gains a nullable `draft_id` column (T1 §4.8). `GetEventsAfterForDraft`
filters by `draft_id`. `agent_runs.course_id` NOT NULL is relaxed. Both changes are
authoritative in T1 §4.8 and are referenced from T2 §11.3 and this document §5.3.1.

### 10.6 Draft lifecycle status values

The current `course_drafts.status` column uses TEXT values `'draft'` and `'published'`
(§4.1). If the draft tree-generation poller uses `courses.status` patterns (e.g.,
`'generating'`, `'awaiting_layer_approval'`), it is unclear whether `course_drafts` needs
its own status enum or whether these values are added as text variants. Recommendation: add
a `current_layer node_type_enum` column to `course_drafts` (mirroring `courses.current_layer`
in T1 §3.3 / T2 §3.3) so the poller can locate drafts awaiting generation without scanning
all `draft_nodes`. Needs Lead sign-off before G2-S2 migration.
