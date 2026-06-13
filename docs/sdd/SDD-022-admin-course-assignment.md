# SDD-022 — Admin Course Creation and Assignment

Sprint 22 | Status: DRAFT | Author: design-author | Date: 2026-06-12

---

## 1. Overview

### 1.1 Problem

Admins currently have no way to create courses on behalf of students. Courses are
initiated exclusively by the student through an intake chat session, which means:

- An admin cannot pre-load a curriculum for a class of students.
- The intake chat is required even when the admin already knows exactly what each
  student should learn.
- There is no concept of a shared "assignment" that groups a set of student
  course instances under one administrative action.

### 1.2 Approach

The design introduces a `course_assignments` table that captures the admin's
intent (topic, level, parameters) and a lightweight FK (`courses.assignment_id`)
that links each generated course instance back to that intent. When an admin
assigns students to an assignment, the system creates one `courses` row per
student, pre-seeds the row to skip the intake phase, and relies on the existing
`pollAndGenerate` polling loop to pick it up automatically.

This is the **per-student course-instance model** confirmed by the PM in resolved
decision 4 (Sprint_17-23_Plan.md §Resolved decisions).

### 1.3 Why per-student rows, not shared content

The entire generation pipeline is keyed on `(student_id, course_id)`:

- `agent_token_usage` has a UNIQUE constraint on `(student_id, course_id)`
  (migration 003); each student's token budget is tracked independently.
- `lesson_content`, `chat_messages`, `section_feedback`, `syllabi`, `homework`,
  `due_date_schedules`, `submissions`, `grades`, and `student_badges` all foreign-
  key to `courses.id`. Sharing a `courses` row across multiple students would
  interleave all of these tables' writes across students, breaking grade isolation,
  homework isolation, and the entire RLS model.
- `ThrottledClient.Messages` takes `(studentID, courseID)` and charges the usage
  against `agent_token_usage`; there is no "shared" usage concept.
- Sprint 23 will inject a per-student learning profile into every generation prompt.
  Per-student course rows are the natural seam for that injection.

Sharing content is therefore architecturally incompatible with the existing system.
One course row per assigned student is the only correct design.

---

## 2. Requirements in Scope (22.2 will author the JSON files)

New module: `REQ-ASSIGN` (placed in `internal/admin/requirements/` — see §8.3).

| ID | Title |
|----|-------|
| REQ-ASSIGN-001 | Admin can create a course assignment |
| REQ-ASSIGN-002 | Admin can assign students to an assignment |
| REQ-ASSIGN-003 | System generates one course instance per assigned student |
| REQ-ASSIGN-004 | Admin-assigned courses skip the intake chat phase |
| REQ-ASSIGN-005 | Admin-assigned course generation enters the sequential queue |
| REQ-ASSIGN-006 | Admin can view per-student generation status for an assignment |
| REQ-ASSIGN-007 | Admin can unassign a student before generation starts |
| REQ-ASSIGN-008 | Student sees assigned courses in CourseHub like self-initiated courses |
| REQ-ASSIGN-009 | RLS isolates each student's assigned course instance |
| REQ-ASSIGN-010 | Admin policy allows admin to read all assigned course rows |

Front-end admin requirements belong under `REQ-FEADMIN` (same module directory as
existing admin frontend requirements). Sprint 22.2 will determine the exact numbers
after auditing the existing highest FEADMIN number.

---

## 3. Data Model

### 3.1 New table: `course_assignments`

```sql
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

CREATE INDEX IF NOT EXISTS course_assignments_admin_id_idx
    ON course_assignments (admin_id);
CREATE INDEX IF NOT EXISTS course_assignments_created_at_idx
    ON course_assignments (created_at DESC);

GRANT SELECT, INSERT, UPDATE, DELETE ON course_assignments TO valory_app;
```

Column notes:

- `admin_id`: the admin who created the assignment; soft-blocked on delete
  (RESTRICT) — an admin account cannot be deleted while assignments remain.
  This is consistent with `courses.student_id ON DELETE RESTRICT`.
- `title`: optional human-readable label for the assignment (e.g. "Week 3 —
  Linear Algebra"). Default empty so it is never null in application code.
- `topic`: the course topic string exactly as `courses.topic` would receive it
  from a student.
- `level`: one of `beginner`, `intermediate`, `advanced` (validated at the API
  layer; stored as plain text, not an enum, so adding values is non-destructive).
- `parameters`: a free-form JSONB object for future extensibility (learning
  objectives, required sections, time budget). Sprint 22 treats this as
  pass-through context; Sprint 23 will merge it with the learning profile.
  The JSONB default `{}` means it is never null.

No RLS is applied to `course_assignments`. This table is admin-managed and only
ever queried under the admin policy or the server policy.  Applying FORCE RLS
here would require a separate admin_can_read policy and a server policy, with
no security benefit: students have no path to this table (it is never joined in
student-facing queries). The admin endpoints that read it are already gated
behind `RequireRole("admin")`.

Security note: the absence of RLS on `course_assignments` is an explicit design
choice. The `valory_app` login role is NOSUPERUSER and NOBYPASSRLS (migration
009 / initdb). For a table with no RLS enabled, FORCE RLS has no effect and all
rows are visible to the login role. Since only admin HTTP handlers touch this
table, and those handlers are behind `RequireRole("admin")`, the application-layer
check is the correct enforcement boundary.

### 3.2 Schema change: `courses.assignment_id`

```sql
ALTER TABLE courses
    ADD COLUMN IF NOT EXISTS assignment_id UUID
        REFERENCES course_assignments(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS courses_assignment_id_idx
    ON courses (assignment_id)
    WHERE assignment_id IS NOT NULL;
```

- `assignment_id IS NULL` means the course was student-initiated (existing
  behavior, no change).
- `assignment_id IS NOT NULL` means the course was created by the admin
  assignment flow.
- `ON DELETE SET NULL`: if an assignment is deleted (future admin housekeeping
  path), existing course rows are not deleted — they continue their lifecycle
  independently. The FK is informational and used for admin reporting.

### 3.3 `courses_single_active_idx` uniqueness interaction

The existing index:

```sql
CREATE UNIQUE INDEX courses_single_active_idx
    ON courses (student_id)
    WHERE status NOT IN ('archived', 'completed');
```

This index prevents two active courses for the same student, whether
student-initiated or admin-assigned. This is intentional: the system only supports
one active course per student at a time. The assignment flow must check this
constraint before creating course rows (see §4.2 — attempt to INSERT returns a
23505 unique violation that the service layer maps to a descriptive error in the
admin response).

### 3.4 No new table for `course_assignment_students`

A junction table might seem natural, but student-assignment membership is already
fully represented by `courses.assignment_id`. The "assigned students" list is just
`SELECT student_id FROM courses WHERE assignment_id = $1`. This avoids a three-
table join for the common query (list courses for assignment) and keeps the
migration minimal. The trade-off is that unassigning a student who has already
started generating is expressed as deleting their `courses` row (guarded — see
§4.3).

### 3.5 Full migration: `019_course_assignments.sql`

```sql
BEGIN;

INSERT INTO schema_migrations (version) VALUES ('019_course_assignments')
    ON CONFLICT (version) DO NOTHING;

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

CREATE INDEX IF NOT EXISTS course_assignments_admin_id_idx
    ON course_assignments (admin_id);
CREATE INDEX IF NOT EXISTS course_assignments_created_at_idx
    ON course_assignments (created_at DESC);

GRANT SELECT, INSERT, UPDATE, DELETE ON course_assignments TO valory_app;

ALTER TABLE courses
    ADD COLUMN IF NOT EXISTS assignment_id UUID
        REFERENCES course_assignments(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS courses_assignment_id_idx
    ON courses (assignment_id)
    WHERE assignment_id IS NOT NULL;

-- Extend the admin RLS policy on courses to explicitly confirm that it covers
-- rows introduced via assignment_id. The policy was created in migrations 003/004
-- and already covers all rows via `current_setting('app.current_role') = 'admin'`.
-- No DDL change needed — this comment documents the intentional coverage.
-- See §6.1 for RLS probe test specifications.

COMMIT;
```

Rollback plan: the migration is additive only. To roll back:
1. `DROP INDEX IF EXISTS courses_assignment_id_idx;`
2. `ALTER TABLE courses DROP COLUMN IF EXISTS assignment_id;`
3. `DROP TABLE IF EXISTS course_assignments;`
4. `DELETE FROM schema_migrations WHERE version = '019_course_assignments';`

The rollback is safe because `assignment_id` is nullable and no existing column
constraints are modified.

---

## 4. Assignment-to-Generation Flow

### 4.1 Status entry point: `syllabus_approved`

A student-initiated course passes through the following lifecycle:

```
intake  ->  syllabus_draft  ->  syllabus_approved  ->  generating  ->  active
```

The existing `pollAndGenerate` loop in `AgentRunner` queries:

```sql
SELECT c.id, c.student_id FROM courses c
WHERE c.status = 'syllabus_approved'
AND NOT EXISTS (
    SELECT 1 FROM agent_runs ar
    WHERE ar.course_id = c.id
    AND ar.run_type = 'content_generation'
    AND ar.status IN ('running', 'completed')
)
```

Admin-assigned courses are inserted with `status = 'syllabus_approved'` and a
pre-seeded syllabus row (generated from the assignment parameters — see §4.2).
This means:

- The `pollAndGenerate` loop picks them up on the next 30-second tick with zero
  changes to the runner.
- No new enqueue mechanism is required.
- The `courses_single_active_idx` partial unique index still covers
  `syllabus_approved` (it only excludes `archived` and `completed`), so the
  one-active-course constraint is enforced.

Why `syllabus_approved` and not `generating`? The runner transitions a course to
`generating` immediately when `RunContentGeneration` starts (it calls
`chair.AssignDueDates` first via `ensureDueDates`), and then to `active` on
completion. Inserting directly at `generating` would bypass `ensureDueDates` on
a race if the runner picks up the row before the API handler finishes. Inserting
at `syllabus_approved` leverages the existing idempotent guard in the polling
query and the full generation path without modification.

### 4.2 Syllabus pre-seeding

The admin's `topic`, `level`, and `parameters` substitute for the intake-chat
outputs that a normal course would produce. The assignment API handler:

1. Inserts one `courses` row per assigned student with:
   - `status = 'syllabus_approved'`
   - `topic = assignment.topic`
   - `assignment_id = assignment.id`
   - `intake_kickoff_sent = true` (prevents the intake kickoff path from ever
     firing for this course; the column already exists from the intake-chat
     implementation)

2. Inserts one `syllabi` row per student course with:
   - `content_adoc = <generated from assignment parameters by Chair.GenerateSyllabusFromParams>`
   - `approved_at = now()` (already approved — admin's decision substitutes for
     student approval)
   - `version = 1`

The syllabus content is generated synchronously by a new `Chair` method,
`GenerateSyllabusFromParams(ctx, topic, level, parameters)`, which constructs a
brief syllabus prompt from the admin-supplied fields without any prior chat
history. This method calls the Anthropic API once per student; for N students
this is N API calls made sequentially within the HTTP handler before returning.

Alternative: generate the syllabus asynchronously (start the student courses in
`intake` status and let a background worker generate+approve the syllabi). This
was rejected because it introduces a new intermediate status or a flag to track
"syllabus needs auto-generation," adds complexity, and delays the generation
fan-out. Synchronous syllabus generation is fast (one Anthropic call, ~2 seconds),
and N students in a typical classroom assignment (20-30) is well within HTTP timeout.
If the class size exceeds 50, the endpoint is expected to return quickly with a
background-generation approach — but this is an open question (§7.2).

### 4.3 Student approval: bypassed

Admin-assigned courses do NOT stop at `syllabus_draft` for student approval. The
rationale:

- The admin, not the student, defines the curriculum. Requiring the student to
  approve a syllabus they did not request creates friction with no educational
  benefit for structured assignments.
- The `approved_at = now()` on the pre-seeded syllabus row is the persisted
  record that the syllabus was auto-approved.

Student agency: students retain the ability to use the chat interface once the
course is `active`. They can ask questions, request section regeneration, and
submit homework — the same as any active course. They cannot modify the syllabus
of an admin-assigned course after the fact (the syllabus is already approved and
the course transitions past `syllabus_draft`).

The `courseToResponse` function in `internal/course/handler.go` does not expose
`assignment_id` today. Adding it to the response is in scope for 22.3 so the
frontend can display a badge distinguishing assigned courses from self-initiated
ones.

### 4.4 Generation fan-out: sequential queue

The existing `pollAndGenerate` goroutine launches one goroutine per eligible
course found in the poll. For N students assigned simultaneously, all N courses
enter `syllabus_approved` at once and the next poll tick will find all of them.
Each gets its own goroutine, and `ThrottledClient.Messages` enforces the per-
student token budget.

The word "sequential queue" in the sprint plan is interpreted as: respect the
existing `ThrottledClient` rate limiting and per-student `agent_token_usage`
budgets. True serial sequencing (process one generation at a time globally) would
be an unnecessary bottleneck and is not implemented. The existing concurrency
model (goroutine-per-course, rate-limited via ThrottledClient) is the correct
fan-out strategy at the token-budget enforcement level.

If rate limits become a concern with large assignments, the `pollAndGenerate` loop
already has a natural throttle: it polls every 30 seconds and processes whatever
is pending. Large assignments are enqueued as a batch and consumed across
successive poll cycles.

---

## 5. RLS Extension

### 5.1 Existing RLS landscape (relevant policies)

| Table | Student policy | Admin policy | Server policy |
|-------|---------------|--------------|---------------|
| `courses` | `student_id = NULLIF(current_setting('app.current_user_id',true),'')::uuid` | `current_setting('app.current_role',true) = 'admin'` | SELECT + UPDATE via 005 |
| `syllabi` | inherited via courses FK (no direct policy; accessed through courses RLS) | (same) | (same) |
| `chat_messages` | course_id in (SELECT id FROM courses WHERE student_id = ...) | `current_setting('app.current_role',true) = 'admin'` | INSERT |
| `lesson_content` | course_id in courses subquery | admin | SELECT/UPDATE/INSERT |
| `homework` | course_id in courses subquery | (admin reads via pool) | (server writes) |
| `agent_token_usage` | no RLS (server-side only) | — | — |

Note: `syllabi`, `homework`, `due_date_schedules`, `section_feedback` do not have
their own RLS policies. They rely on foreign keys to `courses` and the fact that
queries always join through `courses.id`, which is protected by the courses RLS
policies. This is the existing pattern and is not changed.

### 5.2 Admin-assigned rows and student RLS

An admin-assigned course row has:
- `student_id = <assigned student UUID>` (populated at creation time)
- `assignment_id = <assignment UUID>` (the new FK)

The `courses_student_policy` checks `student_id`, not `assignment_id`. A student
whose `app.current_user_id` GUC matches `student_id` can see their own course
exactly as if they had created it themselves. Student A cannot see student B's
row because `student_id` differs.

No changes are required to the student policy.

### 5.3 Admin reads of all assigned courses

The `courses_admin_policy` already allows any row when
`app.current_role = 'admin'`. Admin API handlers execute under the request-scoped
connection set by the auth middleware, which sets `app.current_role = 'admin'`
for admin sessions. No changes are required to the admin policy.

This means admin endpoints can query `courses WHERE assignment_id = $1` without
any policy change and see all student rows for that assignment.

### 5.4 Server policy interaction

The `courses_server_select_policy` and `courses_server_update_policy` already
cover all `courses` rows when `app.current_role = 'server'`. The agent runner uses
`serverPool`, which pre-stamps `app.current_role = 'server'` on every connection
via `BeforeAcquire`. No changes needed.

### 5.5 `course_assignments` table RLS

`course_assignments` does NOT have RLS enabled (see §3.1 rationale). The table
is only accessed by:
- Admin handlers (behind `RequireRole("admin")`) using the request-scoped connection
  (which carries `app.current_role = 'admin'`, but RLS is not forced so this is
  moot).
- The assignment creation service which runs under the admin session.

Students never reach a code path that queries `course_assignments`.

### 5.6 RLS security tradeoffs

**Tradeoff 1: No FORCE RLS on `course_assignments`.**
Risk: if a bug or future code path accidentally exposes `course_assignments`
data to a student, RLS would not catch it. Mitigation: the table contains only
admin-authored metadata (topic, level, parameters) with no PII; the actual
privacy-sensitive data (grades, submissions, chat) is in tables that DO have FORCE
RLS keyed on `student_id`. The `RequireRole` middleware is the enforcement
boundary. This tradeoff is acceptable and consistent with other admin-only tables
in the codebase (e.g., `audit_log`, `managed_secrets`).

**Tradeoff 2: `assignment_id` is visible in the student's `courses` row.**
Students can see `assignment_id` in `GET /courses/{id}` if it is included in the
response. The UUID reveals only that the course was assigned, not the content of
the assignment. The assignment table itself is not student-accessible. Including
`assignment_id` in the course response is informational and not a security risk.

---

## 6. API Contract

All endpoints are under `/api/v1/admin/assignments` and require:
- Valid session (authMW)
- `RequireRole("admin")`
- CSRF token (security.CSRFMiddleware)

### 6.1 POST /api/v1/admin/assignments

Create a new course assignment. Does not assign any students.

**Request body:**
```json
{
  "title":      "string (optional, max 255 chars)",
  "topic":      "string (required, non-empty, max 500 chars)",
  "level":      "beginner | intermediate | advanced (optional, default: beginner)",
  "parameters": { "any": "jsonb object (optional, default: {})" }
}
```

**Response 201:**
```json
{
  "id":         "uuid",
  "admin_id":   "uuid",
  "title":      "string",
  "topic":      "string",
  "level":      "string",
  "parameters": {},
  "created_at": "RFC3339",
  "updated_at": "RFC3339"
}
```

**Error cases:**
- 400 `BAD_REQUEST`: topic missing or empty, level not in allowed set, parameters
  not valid JSON object.
- 401 `UNAUTHORIZED`: no session.
- 403 `FORBIDDEN`: session role is not admin.
- 500 `INTERNAL_ERROR`: database error.

### 6.2 GET /api/v1/admin/assignments

List all assignments created by the authenticated admin.

**Query params:** `limit` (1-200, default 20), `cursor` (opaque base64 string).

**Response 200:**
```json
{
  "assignments": [
    {
      "id":            "uuid",
      "title":         "string",
      "topic":         "string",
      "level":         "string",
      "student_count": 3,
      "created_at":    "RFC3339"
    }
  ],
  "next_cursor": "string | null"
}
```

Note: `student_count` is a COUNT of `courses` rows with `assignment_id = assignment.id`
(any status). This is a single GROUP BY in the query; it does not require an
additional round-trip.

### 6.3 GET /api/v1/admin/assignments/{id}

Get a single assignment with per-student generation status.

**Response 200:**
```json
{
  "id":         "uuid",
  "title":      "string",
  "topic":      "string",
  "level":      "string",
  "parameters": {},
  "created_at": "RFC3339",
  "students": [
    {
      "student_id":   "uuid",
      "username":     "string",
      "course_id":    "uuid",
      "course_status": "syllabus_approved | generating | active | archived | completed | failed",
      "assigned_at":  "RFC3339 (courses.created_at)"
    }
  ]
}
```

The query joins `courses` and `users`:

```sql
SELECT c.id, c.student_id, c.status, c.created_at,
       u.username
FROM courses c
JOIN users u ON u.id = c.student_id
WHERE c.assignment_id = $1
ORDER BY c.created_at ASC
```

This query runs under the admin session (app.current_role = 'admin'), so the
`courses_admin_policy` allows it to see all rows regardless of `student_id`.

**Error cases:**
- 404 `NOT_FOUND`: assignment does not exist.
- 403 `FORBIDDEN`: non-admin session.

### 6.4 POST /api/v1/admin/assignments/{id}/students

Assign one or more students to an assignment. For each student, creates one
`courses` row at `syllabus_approved` with a pre-seeded syllabus.

**Request body:**
```json
{
  "student_ids": ["uuid", "uuid"]
}
```

**Response 200:**
```json
{
  "created": [
    {
      "student_id":  "uuid",
      "course_id":   "uuid",
      "course_status": "syllabus_approved"
    }
  ],
  "errors": [
    {
      "student_id": "uuid",
      "error":      "COURSE_ALREADY_ACTIVE | STUDENT_NOT_FOUND | SYLLABUS_GENERATION_FAILED"
    }
  ]
}
```

The endpoint uses a partial-success model: it attempts each student in order,
records successes and errors, and returns 200 with the combined result. This
avoids a single failed student blocking the entire batch. The caller (admin UI)
is responsible for displaying per-student error messages.

**Processing steps per student:**
1. Verify `student_id` references an active student-role user. Return
   `STUDENT_NOT_FOUND` if not.
2. Attempt `INSERT INTO courses (student_id, topic, status, assignment_id, intake_kickoff_sent) VALUES ($1, $2, 'syllabus_approved', $3, true)`. If a unique
   violation on `courses_single_active_idx` occurs, record `COURSE_ALREADY_ACTIVE`.
3. Call `chair.GenerateSyllabusFromParams(ctx, courseID, studentID, topic, level, parameters)`. On failure, delete the newly created course row (compensating
   rollback) and record `SYLLABUS_GENERATION_FAILED`.
4. Insert the returned syllabus into `syllabi` with `approved_at = now()`.

Step 3 uses the admin session connection, which carries `app.current_role = 'admin'`.
The syllabus INSERT writes to `syllabi` (which is accessed through the courses FK;
the admin policy on courses covers this path).

**Error cases:**
- 400 `BAD_REQUEST`: student_ids empty, not an array of UUIDs.
- 404 `NOT_FOUND`: assignment does not exist.
- 403 `FORBIDDEN`: non-admin session.

### 6.5 DELETE /api/v1/admin/assignments/{id}/students/{studentId}

Unassign a student from an assignment.

**Unassign semantics:**

- If `course.status IN ('syllabus_approved')` (not yet picked up by the runner):
  delete the `courses` row. This is safe because no agent run has started and no
  lesson content, submissions, or grades exist.
- If `course.status IN ('generating', 'active', 'archived', 'completed')`:
  return 409 `CONFLICT` with code `GENERATION_IN_PROGRESS` (for `generating`)
  or `COURSE_ALREADY_ACTIVE` (for `active`). An admin must explicitly withdraw/
  archive the course via the normal course lifecycle if they want to remove it.
  Silently deleting a course mid-generation or after completion could corrupt
  ongoing work and destroy student data.

The `DELETE` is idempotent: if the student has no course row for this assignment,
return 200 with `{"deleted": false, "reason": "not_assigned"}`.

**Response 200:**
```json
{ "deleted": true }
```
or
```json
{ "deleted": false, "reason": "not_assigned" }
```

**Error cases:**
- 409 `CONFLICT` / `GENERATION_IN_PROGRESS`: course is generating.
- 409 `CONFLICT` / `COURSE_ALREADY_ACTIVE`: course is active or beyond.
- 404 `NOT_FOUND`: assignment does not exist.
- 403 `FORBIDDEN`: non-admin session.

### 6.6 `Chair.GenerateSyllabusFromParams` (new method)

This is not an HTTP endpoint; it is a new method on the `Chair` struct in
`internal/agent/chair.go`.

```go
// GenerateSyllabusFromParams generates a course syllabus from admin-supplied
// parameters, without any prior intake chat history. Called by the assignment
// handler when pre-seeding courses for admin-assigned students.
//
// The generated AsciiDoc is inserted into the syllabi table with approved_at set
// by the caller (the admin handler) in the same transaction as the courses INSERT.
//
// @{"req": ["REQ-ASSIGN-003", "REQ-ASSIGN-004"]}
func (c *Chair) GenerateSyllabusFromParams(
    ctx context.Context,
    courseID, studentID uuid.UUID,
    topic, level string,
    parameters json.RawMessage,
) (string, error)
```

The method constructs a syllabus prompt that includes `topic`, `level`, and
`parameters` in place of an intake conversation. It follows the same
`stripCodeFence` cleanup and AsciiDoc format conventions as `GenerateSyllabus`.

The prompt should request 5-8 sections (same as `syllabusSystemPrompt`), specify
the level, and incorporate any structured parameters (e.g., required topics,
time budget) from the JSONB object if present.

**Token usage accounting:** the `Messages` call inside this method charges tokens
against `agent_token_usage(student_id, course_id)`, exactly as any other Claude
call. The `ThrottledClient` enforces the per-student token cap.

---

## 7. Agent Interaction

### 7.1 Sequence: admin assigns students

```
Admin browser
    |
    | POST /api/v1/admin/assignments
    v
AdminAssignmentHandler.createAssignment()
    | INSERT course_assignments row
    v
    200 { id, ... }

Admin browser
    |
    | POST /api/v1/admin/assignments/{id}/students
    v
AdminAssignmentHandler.assignStudents()
    |--- for each student_id ---
    |
    | INSERT courses (student_id, topic, status='syllabus_approved',
    |                 assignment_id, intake_kickoff_sent=true)
    |
    | chair.GenerateSyllabusFromParams(ctx, courseID, studentID,
    |                                   topic, level, parameters)
    |   |
    |   | ThrottledClient.Messages(ctx, studentID, courseID, ...)
    |   v
    |   Anthropic API → syllabus AsciiDoc
    |
    | INSERT syllabi (course_id, content_adoc, approved_at=now())
    |
    +--- next student ---
    v
    200 { created: [...], errors: [...] }

--- 30 seconds later ---

AgentRunner.pollAndGenerate()
    |
    | SELECT courses WHERE status='syllabus_approved' AND no content_generation run
    | -> returns the newly assigned student courses
    |
    +--- for each course (goroutine) ---
    |
    | RunContentGeneration(ctx, courseID, studentID)
    |   | transition courses.status → 'generating' (via server pool UPDATE)
    |   | ensureDueDates → chair.AssignDueDates (inserts homework rows)
    |   | generateAllSections → professor.GenerateSection × N
    |   |   → reviewer.ReviewSection × N
    |   | transition courses.status → 'active'
    v
    GeneratingView (SSE) shows progress log to student
    Student auto-navigated to CourseHub when status → active
```

### 7.2 Sprint 23 seam

When Sprint 23 lands, `professor.GenerateSection` will have access to a per-
student learning profile. The natural injection point is the `systemPrompt`
inside `GenerateSection`, which already carries `syllabusAdoc` as course context.
The profile will be an additional context string fetched via `studentID`:

```go
profile := profileRepo.GetProfile(ctx, studentID)
// inject into systemPrompt alongside syllabusAdoc
```

For `GenerateSyllabusFromParams`, the same injection applies: the learning
profile is appended to the prompt if available. If not available (S23 not yet
deployed, or student has not completed onboarding), the prompt degrades
gracefully to admin parameters only.

The seam is: `(studentID, courseID)` is already threaded through every agent
call. Sprint 23 adds a repository lookup keyed on `studentID`; no interface
changes are required in Sprint 22.

---

## 8. Frontend Design

### 8.1 Admin view: new "Assignments" tab in the admin area

A new `AdminAssignmentsView.vue` component added to the admin section of the
router. This view is separate from the existing course oversight view.

Layout:
- Header with "New Assignment" button.
- Table listing assignments (title, topic, level, student_count, created_at).
- Row click navigates to assignment detail.

`AdminAssignmentDetailView.vue`:
- Displays assignment metadata (topic, level, parameters).
- "Assign Students" button opens a modal with a searchable list of active student
  accounts (fetched from `GET /api/v1/users?role=student`).
- Per-student status table: username, course status, assigned_at, link to
  admin course oversight for that course.
- "Unassign" button per student row (calls DELETE, handles 409 errors gracefully).

### 8.2 Student view: no changes required

The student's assigned course appears in the existing course list at
`GET /api/v1/courses` because:
- The `courses` row has `student_id = <student UUID>`.
- The `courses_student_policy` returns the row for the owning student.
- `ListCourses` in `course/service.go` filters by `student_id` for students.

The student navigates to the course normally. When status is `syllabus_approved`
or `generating`, the frontend router should redirect to `GeneratingView` (which
already handles SSE progress). When status is `active`, `CourseHub` renders as
usual.

The `courseToResponse` function in `course/handler.go` should be extended to
include `assignment_id` so the frontend can optionally display "Assigned by
instructor" badge. The field is a UUID or null; null means self-initiated.

No intake chat view is presented for assigned courses. The frontend router guard
should skip the intake/syllabus approval flow when a course is already at
`syllabus_approved` or beyond on load (the existing router guards already do this
for `generating` and `active`; `syllabus_approved` needs to be handled the same
way as `generating` from the student's perspective for assigned courses).

### 8.3 Module directory placement

```
internal/admin/requirements/REQ-ASSIGN-001.json
internal/admin/requirements/REQ-ASSIGN-002.json
...
internal/admin/requirements/REQ-ASSIGN-010.json
```

The `REQ-ASSIGN` module is placed under `internal/admin` because the assignment
feature is an admin-driven operation. The backend handler will live in
`internal/admin/assignment_handler.go`.

Frontend requirements (`REQ-FEADMIN-NNN`) go in `frontend/requirements/` alongside
existing `REQ-FEADMIN` files.

---

## 9. Alternatives Considered

### 9.1 Shared course row with per-student content

Rejected. See §1.3 for the full architectural incompatibility argument. The one-
sentence summary: `agent_token_usage`, `grades`, `submissions`, and RLS are all
keyed on `(student_id, course_id)` and cannot be shared.

### 9.2 Status entry at `generating` (skip `syllabus_approved`)

Rejected. Inserting at `generating` would bypass `ensureDueDates` and require
calling `chair.AssignDueDates` inside the HTTP handler, duplicating logic that
the runner already handles. It would also require a new server-policy INSERT
on the `courses` table (the existing policy only covers SELECT and UPDATE). Entering
at `syllabus_approved` reuses the entire runner code path unchanged.

### 9.3 Async syllabus generation (new `admin_pending` status)

Rejected for Sprint 22. This would require a new status, a new background worker
or extending `pollAndGenerate`, and more frontend states. The synchronous approach
is simpler. If class sizes exceed the HTTP timeout threshold, this can be
revisited in a fast-follow.

### 9.4 `course_assignment_students` junction table

Rejected. Membership is fully derivable from `courses.assignment_id`. A junction
table would require keeping it in sync with `courses`, creating potential for
divergence. The FK on `courses` is the canonical record.

### 9.5 Separate `assigned_course_status` enum value

Rejected. Adding a new lifecycle status for "assigned but not yet picked up by
the runner" would require updating every status check in the codebase. Using
`syllabus_approved` is semantically accurate (the syllabus is approved by the
admin at creation time) and requires zero changes to existing status-handling code.

---

## 10. Open Questions

### 10.1 Large assignment batches (>50 students)

Synchronous syllabus generation per student at N=50 means 50 sequential Anthropic
calls in a single HTTP handler. At ~2 seconds per call, this is ~100 seconds,
which will exceed most reverse-proxy timeouts. Mitigations:
- Cap the batch size at 20 students per POST, requiring multiple calls for larger
  assignments.
- Move syllabus generation to a background queue (the rejected §9.3 approach).

Decision needed from PM or Software Lead before 22.3 starts. The default design
caps at 20 per call.

### 10.2 Admin `assignment_id` filter on `GET /api/v1/courses`

Should the existing `GET /api/v1/courses` admin list endpoint gain an
`assignment_id` query parameter for filtering? This is useful for the admin
oversight view but adds surface area. Current plan: serve this via the new
`GET /api/v1/admin/assignments/{id}` detail endpoint instead. No change to the
existing courses list endpoint.

### 10.3 Syllabus parameters schema validation

The `parameters` JSONB field is free-form in this sprint. Sprint 23 will define a
structured schema (learning_profile_id, required_topics[], time_budget_hours).
Should Sprint 22 enforce any schema at all, or store it verbatim?

Current plan: store verbatim (any valid JSON object). Validate that it is a JSON
object (not an array or scalar). Sprint 23 will layer a stricter schema on top.

### 10.4 Admin ability to re-generate an assigned course

If a student's assigned course generation fails (status remains `generating` with
a failed `agent_run`), an admin needs a recovery path. The existing `pollAndGenerate`
already retries failed runs (the NOT EXISTS check excludes only `running` and
`completed` status, not `failed`). So failed generations are automatically retried.
If the syllabus itself is wrong, there is no admin edit-syllabus endpoint today.
This is out of scope for Sprint 22.

---

## 11. Acceptance Criteria for 22.3, 22.4, 22.5

### 22.3 (Backend) checklist

- [ ] Migration `019_course_assignments.sql` applies idempotently (re-run safe via
      `ON CONFLICT DO NOTHING` and `IF NOT EXISTS`).
- [ ] `course_assignments` table created with correct columns, indexes, and GRANT.
- [ ] `courses.assignment_id` nullable FK added with index.
- [ ] `Chair.GenerateSyllabusFromParams` implemented; generates valid AsciiDoc
      from topic/level/parameters without chat history.
- [ ] `AdminAssignmentHandler` implements all 5 endpoints (§6.1–6.5).
- [ ] Handler is mounted under `/api/v1/admin/assignments` behind `RequireRole("admin")`
      and CSRF middleware.
- [ ] `POST .../students` partial-success model: successful students are created
      even when one student fails; errors reported per-student.
- [ ] `courses.intake_kickoff_sent = true` set on INSERT for assigned courses.
- [ ] `courses.status = 'syllabus_approved'` on INSERT.
- [ ] `syllabi.approved_at = now()` on INSERT.
- [ ] `courseToResponse` extended to include `assignment_id` (null or UUID).
- [ ] Existing `pollAndGenerate` loop picks up the new courses without modification.
- [ ] `DELETE .../students/{id}` blocks unassign when status not `syllabus_approved`.
- [ ] No plaintext topic/parameters values logged; no secret in any HTTP response body.

### 22.4 (Frontend) checklist

- [ ] `AdminAssignmentsView.vue`: list assignments, create assignment modal.
- [ ] `AdminAssignmentDetailView.vue`: show assignment metadata, student status
      table, assign-students modal (calls `GET /api/v1/users?role=student`),
      unassign button with 409-conflict handling.
- [ ] Student CourseHub: assigned courses appear in the list with `assignment_id`
      badge ("Assigned by instructor").
- [ ] Router guard: assigned course at `syllabus_approved` redirects student to
      `GeneratingView` (same as `generating`).
- [ ] No raw JSON or error code exposed to the student on generation failure;
      reuses existing `GeneratingView` error UI.

### 22.5 (Tests) checklist

**RLS probe tests (integration — real PostgreSQL):**

- [ ] Student A can read their own assigned course row.
- [ ] Student A cannot read student B's assigned course row (even for the same
      `assignment_id`).
- [ ] Admin can read all course rows for `assignment_id = X`.
- [ ] Server role can SELECT and UPDATE any course row regardless of `assignment_id`.
- [ ] A query run as `valory_app` with empty GUCs (`app.current_user_id = ''`,
      `app.current_role = ''`) returns zero rows from `courses` (FORCE RLS; the
      NULLIF guard prevents the UUID cast error).
- [ ] `course_assignments` is readable by `valory_app` without any GUC set (no RLS).

**Unit tests:**

- [ ] `Chair.GenerateSyllabusFromParams`: valid AsciiDoc returned; code-fence
      stripped; empty parameters produces reasonable output.
- [ ] `assignStudents` service: `COURSE_ALREADY_ACTIVE` returned for duplicate
      student; compensating DELETE runs when syllabus generation fails.
- [ ] Unassign: 409 returned when status is `generating` or `active`.

**Integration tests:**

- [ ] Full assign-to-generate flow: admin creates assignment → assigns 2 students →
      `pollAndGenerate` picks up both courses → verify `generating` status reached.
- [ ] `courseToResponse` includes `assignment_id` for assigned courses.
- [ ] `GET /api/v1/admin/assignments/{id}` returns correct `students` array with
      accurate `course_status`.

**E2E (one budgeted live AI run):**

- [ ] Admin assigns 1 student → student logs in → sees assigned course in CourseHub
      → `GeneratingView` shows progress → course transitions to active.

---

## 12. Cross-Module Dependencies

| Dependency | Direction | Notes |
|---|---|---|
| `internal/agent.Chair` | assignment handler imports | `GenerateSyllabusFromParams` method added to Chair |
| `internal/course.CourseRepository` | assignment handler imports | `CreateCourse`-equivalent for admin path; may reuse or extend |
| `internal/auth.RequireRole` | assignment handler uses | already available |
| `internal/db.AcquireServerConn` | used in `GenerateSyllabusFromParams` for syllabi INSERT | same pattern as `GenerateSyllabus` |
| `internal/agent.AgentRunner.pollAndGenerate` | unchanged, picks up new rows | no modification |
| `courses_single_active_idx` | assignment handler must handle 23505 violation | map to `COURSE_ALREADY_ACTIVE` error |
| Sprint 23 learning profile | future injection into `GenerateSyllabusFromParams` and `GenerateSection` | seam is `studentID` parameter, already present |
