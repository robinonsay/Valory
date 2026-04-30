# Sprint 3 — Course Module & Admin Config Endpoint

## Objective

Deliver the course lifecycle state machine (intake through archival/completion), the syllabus approval gate, due-date schedule persistence, and the deferred admin configuration HTTP endpoints. After this sprint, students can create and manage courses through the full lifecycle, admins can view and update system-wide configuration, and the `courses` table exists as the foundation for the agent, content, submission, and grade modules in subsequent sprints.

## Requirements Covered

| Requirement | Title |
|---|---|
| REQ-COURSE-001 | Single Active Course Restriction |
| REQ-COURSE-002 | Admin System-Wide Course Listing |
| REQ-COURSE-003 | Course Archival on Student Withdrawal |
| REQ-COURSE-004 | Archived Course Resumption |
| REQ-COURSE-005 | Syllabus Modification Request Submission |
| REQ-COURSE-006 | Syllabus Approval Gate |
| REQ-COURSE-007 | Due Date Schedule Persistence |
| REQ-COURSE-008 | Student Due Date Agreement Gate |
| REQ-ADMIN-001 | Agent Retry Limit Configuration |
| REQ-ADMIN-002 | Correction Loop Limit Configuration |
| REQ-ADMIN-003 | Per-Student AI Token Cost Limit Configuration |
| REQ-SECURITY-002 | Per-Student Data Isolation (courses table RLS) |
| REQ-SYS-005 | Admin Course Oversight |
| REQ-SYS-008 | Single Active Course Constraint |
| REQ-SYS-027 | Student Course Archival |
| REQ-SYS-028 | Archived Course Resumption |
| REQ-SYS-029 | Syllabus Modification |
| REQ-SYS-030 | Syllabus Approval Gate |
| REQ-SYS-031 | Due Date Schedule Persistence |
| REQ-SYS-032 | Student Due Date Agreement |
| REQ-SYS-050 | Admin Grading Policy Configuration |
| REQ-SYS-051 | Admin Agent Retry Limit Configuration |

**Deferred to Sprint 4:** REQ-ADMIN-004 (per-student AI token cap enforcement) — requires the agent module's `ThrottledClient` which wraps the Anthropic SDK. The `agent_token_usage` table is created in this sprint's migration to unblock Sprint 4.

## Assumptions

1. **TC-COURSE-002 status discrepancy**: The test case expects a new course to start with status `active`; the SDD state machine specifies courses begin at `intake` (the Chair agent starts the intake questionnaire after creation). The SDD is authoritative — new courses start at `intake`.

2. **TC-ADMIN-011 zero-value conflict**: TC-ADMIN-011 says `per_student_token_limit = 0` should be rejected. The SDD explicitly states: "Setting `per_student_token_limit` to `0` is a valid way to disable AI features for a course." The SDD is authoritative — `0` is valid for `per_student_token_limit`; TC-ADMIN-011 will not be implemented as written.

3. **TC-COURSE-015/016 generate-content endpoint**: These test cases reference `POST /courses/:id/generate-content` which does not exist in the SDD API contract. Content generation is triggered internally by the agent module via a Go channel event when a course transitions to `syllabus_approved`. TC-COURSE-015/016 are verified via the syllabus approval flow: attempting approval when not in `syllabus_draft` returns 409 (gate enforced); approving a valid syllabus transitions to `syllabus_approved` (gate passes). The actual generation trigger is Sprint 4 work.

4. **TC-COURSE-004/005 admin course listing path**: TCs reference `GET /admin/courses`. The SDD uses `GET /api/v1/courses` with role-based filtering — admin sees all, student sees own. No separate `/admin/courses` endpoint exists; the same endpoint with role filtering covers both test cases.

5. **TC-COURSE-013 HTTP status for modification**: TC expects HTTP 201; SDD API contract specifies HTTP 200. SDD is authoritative — `POST /api/v1/courses/:id/syllabus/modification` returns 200.

6. **TC-COURSE-019/020 schedule agreement path**: TCs reference `POST /courses/:id/due-dates/agree`; SDD specifies `POST /api/v1/courses/:id/schedule/agree`. SDD path is canonical.

7. **agent_token_usage table**: Created in migration 003 alongside `courses` because it carries a `REFERENCES courses(id)` foreign key. The `ThrottledClient` that enforces the cap is deferred to Sprint 4 (agent module). The table is created now to keep migration ordering consistent with the FK dependency.

8. **RLS for courses table**: Migration 003 enables RLS on `courses` and adds two policies using the GUC pattern established in Sprint 1: a student policy (`student_id = current_setting('app.current_user_id', true)::uuid`) and an admin policy (`current_setting('app.current_role', true) = 'admin'`). The `valory_app` role (created in migration 002) must have RLS bypass disabled — confirm it was created with `NOBYPASSRLS`.

9. **Admin config `config.change` audit entry**: `PATCH /api/v1/admin/config` writes to `system_config` and appends an `audit_log` entry with action `config.change` in the same database transaction, so either both the config update and its audit record commit or neither does. The payload is `{"keys_changed": ["key1", "key2"]}`.

10. **configSvc.Load() placement**: Per SDD, `configSvc.Load(ctx)` is called after the transaction commits. It reloads the full in-memory map; it is not inside the transaction.

11. **GET /api/v1/admin/config response meta fields**: The `updated_by` and `updated_at` meta fields reflect the single most-recent write across all keys. Fetch via `SELECT updated_by, updated_at FROM system_config ORDER BY updated_at DESC LIMIT 1`; join to `users` for `username`. If no row has been updated by an admin yet (all rows show NULL `updated_by`), return `null` for both fields.

## Pre-Sprint State

| File | What it provides |
|---|---|
| `internal/auth/middleware.go` | `AuthMiddleware`, `RequireRole` |
| `internal/auth/repository.go` | Session CRUD, user lookup |
| `internal/auth/service.go` | Login / logout |
| `internal/auth/handler.go` | `POST /api/v1/auth/login`, `POST /api/v1/auth/logout` |
| `internal/admin/config.go` | `ConfigService` (in-memory map, no HTTP endpoints) |
| `internal/audit/audit.go` | `Entry`, `computeEntryHash`, `verifyChain` |
| `internal/audit/repository.go` | `AuditRepository.Append`, `ListPaginated`, `StreamAll` |
| `internal/audit/handler.go` | `GET /api/v1/admin/audit`, `GET /api/v1/admin/audit/verify` |
| `internal/user/` | Full user module (repo, service, handler, email) |
| `internal/security/` | CSRF middleware, password-reset rate limiter |
| `migrations/001_auth.sql` | `users`, `sessions`, `login_attempts` |
| `migrations/002_user_security_audit.sql` | `password_reset_tokens`, `audit_log`, `system_config`, `valory_app` role |
| `cmd/server/main.go` | Full Sprint 2 wiring |

## Sprint Plan

### Increment 1 — Database Migration (sequential)

| Task | Description | Agent | File(s) |
|---|---|---|---|
| **T1** | **DB Migration 003** — Wrapped in a single `BEGIN`/`COMMIT`. Create `course_status` ENUM (`intake`, `syllabus_draft`, `syllabus_approved`, `generating`, `active`, `archived`, `completed`). Create `courses` table per SDD data model (columns: `id UUID PK`, `student_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT`, `title TEXT NOT NULL DEFAULT ''`, `topic TEXT NOT NULL`, `status course_status NOT NULL DEFAULT 'intake'`, `pre_withdrawal_status course_status`, `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`, `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`). Add partial unique index `courses_single_active_idx ON courses(student_id) WHERE status NOT IN ('archived', 'completed')`. Add indexes `courses_student_id_idx` and `courses_status_idx`. Enable RLS on `courses` (`ALTER TABLE courses ENABLE ROW LEVEL SECURITY`); create student policy: `CREATE POLICY courses_student_policy ON courses USING (student_id = current_setting('app.current_user_id', true)::uuid)`; create admin policy: `CREATE POLICY courses_admin_policy ON courses USING (current_setting('app.current_role', true) = 'admin')`. Grant the `valory_app` role SELECT/INSERT/UPDATE/DELETE on `courses`. Create `syllabi` table (columns: `id`, `course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE`, `content_adoc TEXT NOT NULL`, `version INT NOT NULL DEFAULT 1`, `approved_at TIMESTAMPTZ`, `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`); add index `syllabi_course_id_idx`. Create `homework` table per SDD (columns: `id`, `course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE`, `section_index INT NOT NULL`, `title VARCHAR(255) NOT NULL`, `rubric TEXT NOT NULL`, `grade_weight NUMERIC(4,3) NOT NULL CHECK (grade_weight > 0)`, `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`); add index `homework_course_id_idx`. Create `due_date_schedules` table (columns: `id`, `course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE`, `homework_id UUID NOT NULL REFERENCES homework(id) ON DELETE CASCADE`, `due_date TIMESTAMPTZ NOT NULL`, `agreed_at TIMESTAMPTZ`, `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`); add index `due_date_schedules_course_id_idx` and unique index `due_date_schedules_unique_hw ON due_date_schedules(course_id, homework_id)`. Create `agent_token_usage` table (columns: `id UUID PK`, `student_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE`, `course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE`, `total_tokens_used BIGINT NOT NULL DEFAULT 0 CHECK (total_tokens_used >= 0)`, UNIQUE on `(student_id, course_id)`); add index `idx_token_usage_student_id`. Grant `valory_app` SELECT/INSERT/UPDATE/DELETE on `syllabi`, `homework`, `due_date_schedules`, `agent_token_usage`. | `junior-engineer` | `migrations/003_course.sql` |

**Verifier (T1):** `software-quality-engineer` + `systems-engineer` (parallel) → `senior-quality-engineer`

---

### Increment 2 — Parallel Foundation (after T1)

| Task | Description | Agent | File(s) |
|---|---|---|---|
| **T2** | **Course repository** — `CourseRepository` holding `*pgxpool.Pool`. Define `CourseRow` struct (all `courses` columns), `SyllabusRow`, `HomeworkRow`, `DueDateRow`. Export sentinel errors `ErrInvalidTransition`, `ErrNoPendingSchedule`. Methods: `CreateCourse(ctx, studentID uuid.UUID, topic string) (CourseRow, error)` — INSERT returning all fields; `GetCourseByID(ctx, id uuid.UUID) (CourseRow, error)` — SELECT by id, returns `pgx.ErrNoRows` for not-found; `ListCourses(ctx, studentID *uuid.UUID, statusFilter string, cursor string, limit int) ([]CourseRow, nextCursor string, error)` — when `studentID != nil` add `WHERE student_id = $N`; cursor-based pagination on `(created_at, id)` using base64-encoded JSON; default limit 20, max 200; `Transition(ctx, id uuid.UUID, allowedFrom []string, newStatus string, preWithdrawalStatus *string) (CourseRow, error)` — executes `UPDATE courses SET status = $newStatus, pre_withdrawal_status = COALESCE($preWithdrawalStatus, pre_withdrawal_status), updated_at = now() WHERE id = $id AND status = ANY($allowedFrom) RETURNING *`; zero rows updated → returns `ErrInvalidTransition`; `GetLatestSyllabus(ctx, courseID uuid.UUID) (SyllabusRow, error)` — SELECT where course_id = $1 ORDER BY version DESC LIMIT 1; `InsertSyllabus(ctx, courseID uuid.UUID, contentAdoc string, version int) (SyllabusRow, error)`; `ApproveSyllabus(ctx, courseID, syllabusID uuid.UUID) error` — UPDATE syllabi SET approved_at = now() WHERE id = $syllabusID AND course_id = $courseID; `InsertHomework(ctx, courseID uuid.UUID, sectionIndex int, title, rubric string, gradeWeight float64) (HomeworkRow, error)`; `InsertDueDateSchedule(ctx, courseID, homeworkID uuid.UUID, dueDate time.Time) error`; `AgreeToSchedule(ctx, courseID uuid.UUID) (agreedCount int, error)` — UPDATE due_date_schedules SET agreed_at = now() WHERE course_id = $1 AND agreed_at IS NULL, RETURNING count; 0 rows → returns `ErrNoPendingSchedule`. Parameterised queries only; no SQL injection vectors. Integration tests against real PostgreSQL using TestMain. Test all methods including: unique violation on concurrent create, transition guard (wrong-state → ErrInvalidTransition), schedule agreement idempotency. | `junior-engineer` | `internal/course/repository.go`, `internal/course/repository_test.go` |
| **T3** | **Admin config handler** — `AdminConfigHandler` holding `*admin.ConfigService`, `*audit.AuditRepository`, `*pgxpool.Pool`. Defined allowed keys (compile-time `map[string]string` of key→type) matching the 13 SDD keys. `GET /api/v1/admin/config`: SELECT all rows from `system_config`; SELECT the most-recent `updated_by`, `updated_at` (ORDER BY updated_at DESC LIMIT 1); JOIN to `users` for `username` (may be NULL); return JSON per SDD contract. `PATCH /api/v1/admin/config`: (1) decode `{"config": {...}}` body; (2) reject any unknown key → 400; (3) validate each value: `agent_retry_limit`, `correction_loop_max_iterations` ≥ 1 (integer); `per_student_token_limit` ≥ 0 (BIGINT — 0 is valid); `late_penalty_rate` float 0.0–1.0; `max_upload_bytes` ≥ 1024 (integer); `session_inactivity_seconds`, `account_lockout_seconds`, `content_generation_timeout_seconds` ≥ 1 (integer); `audit_retention_days`, `notification_retention_days` ≥ 1 (integer); `consent_version` non-empty string; (4) if `homework_weight` or `project_weight` is in the request, compute the effective value of the other from the current in-memory config and assert they sum to 1.0 (tolerance 0.001); (5) accumulate all validation failures and return 422 with `{"validation_errors": [...]}` if any — no DB write performed; (6) begin transaction; (7) for each key UPDATE `system_config SET value = $v, updated_by = $adminID, updated_at = now() WHERE key = $k`; (8) call `audit.Append(ctx, tx, Entry{AdminID: adminID, Action: "config.change", TargetID: "", Payload: json.Marshal({"keys_changed": [...]})})` in same transaction; (9) commit; (10) call `configSvc.Load(ctx)` post-commit; (11) return 200 with updated full config and `updated_keys` list. Both routes require `RequireRole("admin")`. Unit tests for each validation rule. Integration tests: valid PATCH updates DB and in-memory config; unknown key → 400; validation failure → 422 with no DB change; non-admin → 403; audit entry created on PATCH; GET returns all 13 keys. | `senior-engineer` | `internal/admin/config_handler.go`, `internal/admin/config_handler_test.go` |

**Verifier (T2 + T3):** `software-quality-engineer` + `systems-engineer` (parallel) → `senior-quality-engineer`

---

### Increment 3 — Course Service (sequential, after T2)

| Task | Description | Agent | File(s) |
|---|---|---|---|
| **T4** | **Course service** — `CourseService` holding `*CourseRepository`. Export sentinel errors `ErrCourseAlreadyActive`, `ErrForbidden`, `ErrNotFound`, `ErrBadRequest`, `ErrNoPendingSchedule`. Methods: `CreateCourse(ctx context.Context, studentID uuid.UUID, topic string) (CourseRow, error)` — calls `repo.CreateCourse`; detects PostgreSQL SQLSTATE 23505 on `courses_single_active_idx` and returns `ErrCourseAlreadyActive`; `GetCourse(ctx, courseID, requesterID uuid.UUID, role string) (CourseRow, error)` — calls repo; maps `pgx.ErrNoRows` to `ErrNotFound`; if role == "student" and row.StudentID != requesterID returns `ErrForbidden`; `ListCourses(ctx, requesterID uuid.UUID, role string, statusFilter string, cursor string, limit int) ([]CourseRow, nextCursor string, error)` — for role "student" passes `&requesterID` to repo; for "admin" passes nil (no filter); `Withdraw(ctx, courseID, studentID uuid.UUID) (CourseRow, error)` — calls `GetCourse` to verify ownership (→ `ErrForbidden`); reads current status; calls `Transition(allowedFrom: [intake, syllabus_draft, syllabus_approved, generating, active], newStatus: archived, preWithdrawalStatus: &currentStatus)`; maps `ErrInvalidTransition` to service-level `ErrInvalidTransition`; `Resume(ctx, courseID, studentID uuid.UUID) (CourseRow, error)` — ownership check; verifies current status == `archived` (else `ErrInvalidTransition`); reads `PreWithdrawalStatus` (falls back to "active" if nil per SDD assumption 1); calls `Transition(allowedFrom: [archived], newStatus: pre_withdrawal_status, preWithdrawalStatus: nil)`; maps PostgreSQL unique violation on `courses_single_active_idx` to `ErrCourseAlreadyActive`; `ApproveSyllabus(ctx, courseID, studentID uuid.UUID) (SyllabusRow, CourseRow, error)` — ownership check; GetCourse verifies status == `syllabus_draft` (else returns `ErrInvalidTransition` with message `NOT_IN_SYLLABUS_DRAFT`); fetches latest syllabus; calls `repo.ApproveSyllabus` and `repo.Transition(allowedFrom: [syllabus_draft], newStatus: syllabus_approved)` in a transaction; returns syllabus and updated course; `RequestModification(ctx, courseID, studentID uuid.UUID, requestText string) (SyllabusRow, CourseRow, error)` — ownership check; returns `ErrBadRequest` if `strings.TrimSpace(requestText) == ""`; fetches latest syllabus version; inserts new syllabus with `version = latestVersion+1` and `content_adoc = requestText`; calls `Transition(allowedFrom: [syllabus_draft, syllabus_approved], newStatus: syllabus_draft)` in same transaction; `AgreeToSchedule(ctx, courseID, studentID uuid.UUID) (int, error)` — ownership check; calls `repo.AgreeToSchedule`; maps `ErrNoPendingSchedule` to service `ErrNoPendingSchedule`. Integration tests covering full state machine: `CreateCourse` unique violation maps correctly; `Withdraw` saves pre_withdrawal_status; `Resume` restores status and rejects when active course exists; `ApproveSyllabus` blocks wrong-state; `RequestModification` increments syllabus version; `AgreeToSchedule` sets agreed_at. | `senior-engineer` | `internal/course/service.go`, `internal/course/service_test.go` |

**Verifier (T4):** `software-quality-engineer` + `systems-engineer` (parallel) → `senior-quality-engineer`

---

### Increment 4 — Course HTTP Handlers (sequential, after T4)

| Task | Description | Agent | File(s) |
|---|---|---|---|
| **T5** | **Course HTTP handlers** — `CourseHandler` holding `*CourseService`. Helper `writeError(w, status, code, message)` for consistent JSON errors. Routes: `POST /api/v1/courses` → `CreateCourse` — reads `{"topic": "..."}` from body (400 if empty/missing); calls `svc.CreateCourse`; 201 on success; 409 `COURSE_ALREADY_ACTIVE` on `ErrCourseAlreadyActive`. `GET /api/v1/courses` → `ListCourses` — reads `status`, `limit` (default 20, clamp 1–200), `cursor` query params; reads `userID` and `role` from context; calls `svc.ListCourses`; 200 with `{"courses": [...], "next_cursor": "..."}`. Admin response for each course item includes `student_id` and joins `student_email` from users — repo must support this join for admin queries (add `student_email` to ListCourses when role == "admin"). `POST /api/v1/courses/:id/withdraw` → `Withdraw` — parses `courseID` from chi URL param; 200 `{"id": "...", "status": "archived"}`; 403 `ErrForbidden`; 404 `ErrNotFound`; 409 `INVALID_TRANSITION`. `POST /api/v1/courses/:id/resume` → `Resume` — 200 `{"id": "...", "status": "..."}`; 403; 404; 409 `COURSE_ALREADY_ACTIVE` or `INVALID_TRANSITION`. `POST /api/v1/courses/:id/syllabus/approve` → `ApproveSyllabus` — 200 `{"id": "...", "status": "syllabus_approved", "syllabus_version": N}`; 403; 404; 409 `NOT_IN_SYLLABUS_DRAFT`. `POST /api/v1/courses/:id/syllabus/modification` → `RequestModification` — reads `{"request": "..."}` from body; 400 if empty `request`; 200 `{"id": "...", "status": "syllabus_draft", "syllabus_version": N}`; 403; 404. `POST /api/v1/courses/:id/schedule/agree` → `AgreeToSchedule` — 200 `{"id": "...", "agreed_count": N}`; 403; 404; 409 `NO_PENDING_SCHEDULE`. Extract `userID` (`uuid.UUID`) and `role` (`string`) from request context (keys set by `AuthMiddleware`). Integration tests using TestMain against real PostgreSQL, covering TC-COURSE-001 through TC-COURSE-019. | `junior-engineer` | `internal/course/handler.go`, `internal/course/handler_test.go` |

**Verifier (T5):** `software-quality-engineer` + `systems-engineer` (parallel) → `senior-quality-engineer`

---

### Increment 5 — Server Wiring (sequential, after T5)

| Task | Description | Agent | File(s) |
|---|---|---|---|
| **T6** | **Main entry-point update** — Update `cmd/server/main.go`: (1) instantiate `course.NewRepository(pool)` and `course.NewService(courseRepo)`; (2) instantiate `course.NewHandler(courseSvc)`; (3) instantiate `admin.NewConfigHandler(configSvc, auditRepo, pool)`; (4) add non-fatal `log.Printf` warning if `BRAVE_API_KEY` env var is absent (required in Sprint 4 for web search); (5) in the existing authenticated + CSRF router group, mount course routes: `POST /api/v1/courses`, `GET /api/v1/courses`, `POST /api/v1/courses/{id}/withdraw`, `POST /api/v1/courses/{id}/resume`, `POST /api/v1/courses/{id}/syllabus/approve`, `POST /api/v1/courses/{id}/syllabus/modification`, `POST /api/v1/courses/{id}/schedule/agree` (chi uses `{id}` brace syntax for URL params); (6) in the existing admin-role group, mount `GET /api/v1/admin/config` and `PATCH /api/v1/admin/config`. The `noopTerminator` already present satisfies `user.AgentTerminator`; no change needed there. Smoke test: `go build ./...` and `go vet ./...` pass clean. | `junior-engineer` | `cmd/server/main.go` *(update)* |

**Verifier (T6):** `software-quality-engineer` + `systems-engineer` (parallel) → `senior-quality-engineer`

---

## Dependency Graph

```
T1 (migration 003)
 ├─> T2 (course repository) ──> T4 (course service) ──> T5 (course handlers) ──┐
 └─> T3 (admin config handler) ────────────────────────────────────────────────┤
                                                                                 └─> T6 (main wiring)
```

## Review Pipeline

For each increment:

```
Contributor completes task
        |
        v
SQE + Systems Engineer (parallel review)
        |
   Pass? ──yes──> Senior SQE ──pass──> next increment
        |
        no
        v
Return to contributor with feedback → Review again
```

## Test Cases Covered

### Course module (TC-COURSE-*)

| TC | Title | Task | Notes |
|---|---|---|---|
| TC-COURSE-001 | Student cannot start a second course while one is active | T5 | |
| TC-COURSE-002 | Student can start a course when no active course exists | T5 | Expected status is `intake`, not `active` — per SDD |
| TC-COURSE-003 | Student with only an archived course can start a new course | T5 | |
| TC-COURSE-004 | Admin receives a listing of all courses | T5 | Tested via `GET /api/v1/courses` with admin role |
| TC-COURSE-005 | Non-admin student rejected from admin course listing | T5 | Student sees only own courses; no 403 since endpoint is shared |
| TC-COURSE-006 | Admin course listing returns empty array when no courses exist | T5 | |
| TC-COURSE-007 | Withdrawing from active course sets status to archived | T5 | |
| TC-COURSE-008 | Withdraw from course not owned by student is rejected | T5 | Returns 403 |
| TC-COURSE-009 | Withdrawing from already-archived course returns 409 | T5 | |
| TC-COURSE-010 | Student can resume an archived course | T5 | Restores to `pre_withdrawal_status` |
| TC-COURSE-011 | Student with active course cannot resume second archived course | T5 | Returns 409 `COURSE_ALREADY_ACTIVE` |
| TC-COURSE-012 | Resuming an active course is rejected | T5 | Returns 409 `INVALID_TRANSITION` |
| TC-COURSE-013 | Syllabus modification request persisted | T5 | Returns HTTP 200 per SDD (not 201) |
| TC-COURSE-014 | Syllabus modification with empty body rejected | T5 | |
| TC-COURSE-015 | Content generation blocked when syllabus not approved | T5 | Verified via approval endpoint returning 409 when not in `syllabus_draft` |
| TC-COURSE-016 | Content generation proceeds after syllabus approval | T5 | Verified via approval endpoint transitioning to `syllabus_approved`; generation trigger is Sprint 4 |
| TC-COURSE-017 | Approved syllabus due dates stored in database | T5 | Tested via `AgreeToSchedule` setting `agreed_at` |
| TC-COURSE-018 | `AgreeToSchedule` with no pending rows returns 409 | T5 | |
| TC-COURSE-019 | Student explicitly agrees to due date schedule | T5 | |
| TC-COURSE-020 | Due date schedule not finalized without agreement | T5 | Enforcement at submission layer is Sprint 6 work; gate at `schedule/agree` endpoint tested here |

### Admin config (TC-ADMIN-*)

| TC | Title | Task | Notes |
|---|---|---|---|
| TC-ADMIN-001 | Admin sets agent retry limit to valid value | T3 | |
| TC-ADMIN-002 | Agent retry limit of zero rejected | T3 | |
| TC-ADMIN-003 | Non-admin rejected from setting agent retry limit | T3 | |
| TC-ADMIN-004 | Retry limit reflected in agent invocations | Deferred Sprint 4 | Requires agent module |
| TC-ADMIN-005 | Negative agent retry limit rejected | T3 (unit) | |
| TC-ADMIN-006 | Admin sets correction loop limit | T3 | |
| TC-ADMIN-007 | Non-integer correction loop limit rejected | T3 (unit) | |
| TC-ADMIN-008 | Correction loop stops at configured limit | Deferred Sprint 4 | Requires agent module |
| TC-ADMIN-009 | Correction loop limit of 1 stops after first failure | Deferred Sprint 4 | Requires agent module |
| TC-ADMIN-010 | Admin sets per-student token limit | T3 | |
| TC-ADMIN-011 | `per_student_token_limit = 0` valid (disables AI) | T3 | TC assertion inverted per SDD — 0 is valid |
| TC-ADMIN-012 | Non-admin rejected from setting token limit | T3 | |
| TC-ADMIN-013 | Token limit at boundary accepted | T3 (unit) | |
| TC-ADMIN-014–016 | Token cap enforcement | Deferred Sprint 4 | Requires agent ThrottledClient |
