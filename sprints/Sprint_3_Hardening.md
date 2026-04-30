# Sprint 3 Hardening — Post-Review Quality Items

## Objective

Address the non-blocking observations raised during Sprint 3 review before Sprint 4 (agent module) begins. These items carry no new feature work — they are correctness, traceability, and test-environment-fidelity improvements identified by the SQE and Systems Engineer gates.

## Source Observations

| # | Reviewer | Severity | Issue |
|---|---|---|---|
| O-1 | Systems Engineer | Medium | `internal/auth/context_test_helper.go` is compiled into the production binary because it is not in a `_test.go` file; `SetTestContext` bypasses all auth checks and should not ship in production |
| O-2 | SQE + Systems Engineer | Medium | `ApproveSyllabus` and `ApproveSyllabusTx` in `repository.go` discard the `CommandTag` from `Exec` — if the UPDATE matches 0 rows (syllabus ID not found or wrong `course_id`), the operation silently succeeds |
| O-3 | Systems Engineer | Low | `courses_created_at_id_idx` is present in the production migration (`migrations/003_course.sql`) but absent from the embedded test migration in `repository_test.go`, so cursor-pagination queries run without their covering index in the test environment |
| O-4 | Systems Engineer | Low | `writeJSONResponse` and `writeJSONError` in `internal/admin/config_handler.go` have no `@{"req":[...]}` requirement annotations, creating a traceability gap |

## Assumptions

1. **O-1 build tag approach**: The cleanest production-binary fix without restructuring the cross-package test helper is to add a `//go:build testing` build tag to `context_test_helper.go`. Tests must then be invoked as `go test -tags testing ./...`. All CI scripts and Makefile targets that run tests must be updated to pass `-tags testing`.

2. **O-2 error semantics**: When `ApproveSyllabus`/`ApproveSyllabusTx` affects 0 rows, return `ErrNotFound`. The service already pre-validates that the course and syllabus IDs are correct before calling these methods, so this path is currently unreachable in practice; adding the check is a defensive correctness measure for future callers.

3. **O-3 scope**: The test migration in `repository_test.go` omits RLS policies intentionally (tests run as the DB owner). Only the missing `courses_created_at_id_idx` needs to be added; RLS divergence is acceptable and expected in the test environment.

4. **O-4 annotation convention**: `writeJSONResponse` and `writeJSONError` are shared helpers used by all 13 admin config endpoints; annotate them with the union of all REQ-ADMIN-001, REQ-ADMIN-002, REQ-ADMIN-003 identifiers consistent with the other helpers in the file.

## Sprint Plan

### Increment 1 — Parallel (all tasks are independent)

| Task | Description | Agent | File(s) |
|---|---|---|---|
| **T1** | **Test-helper build tag** — Add `//go:build testing` as the first line of `internal/auth/context_test_helper.go`. Update any Makefile or CI script (`Makefile`, `docker-compose*.yml`, `.github/workflows/*.yml`) that invokes `go test` to pass `-tags testing`. If no such files exist, add a `Makefile` with a `test` target that runs `go test -tags testing ./...`. Verify `go build ./...` (no tag) produces a binary that does NOT include `SetTestContext`; verify `go test -tags testing ./...` still compiles (skip DB-dependent tests by not setting `TEST_DATABASE_URL`). | `junior-engineer` | `internal/auth/context_test_helper.go`, `Makefile` (create if absent) |
| **T2** | **ApproveSyllabus rows-affected check** — In `internal/course/repository.go`, update both `ApproveSyllabus` and `ApproveSyllabusTx` to capture the `CommandTag` returned by `Exec` and return `ErrNotFound` when `tag.RowsAffected() == 0`. Add a unit test (no DB required) or integration test that inserts a syllabus, then calls `ApproveSyllabus` with a mismatched `course_id` and asserts `errors.Is(err, ErrNotFound)`. Verify `go build ./...` and `go vet ./...` pass. | `junior-engineer` | `internal/course/repository.go`, `internal/course/repository_test.go` |
| **T3** | **Test migration cursor index** — In the `migration003` string inside `TestMain` in `internal/course/repository_test.go`, add the missing index immediately after `courses_status_idx`: `CREATE INDEX IF NOT EXISTS courses_created_at_id_idx ON courses (created_at DESC, id DESC);` — matching the production migration exactly. No other changes. Verify `go build ./...` and `go vet ./...` pass. | `junior-engineer` | `internal/course/repository_test.go` |
| **T4** | **Config handler annotations** — In `internal/admin/config_handler.go`, add `// @{"req": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003"]}` doc comments to `writeJSONResponse` (line ~341) and `writeJSONError` (line ~347). No logic changes. Verify `go build ./...` and `go vet ./...` pass. | `junior-engineer` | `internal/admin/config_handler.go` |

**Verifier (T1 + T2 + T3 + T4):** `software-quality-engineer` + `systems-engineer` (parallel) → `senior-quality-engineer`

---

## Dependency Graph

```
T1, T2, T3, T4 — fully independent, run in parallel
        |
        v
SQE + Systems Engineer → Senior SQE → Deliver
```

## Out of Scope

The following observations from Sprint 3 reviews are **deferred** — they are design considerations, not correctness defects:

- `CourseRepository.Pool()` boundary leak — a `WithTx` callback refactor is valid but cross-cutting; deferred until a second module (e.g., agent) requires the same transaction pattern, at which point a shared abstraction is justified.
- `Transition`/`TransitionTx` two round-trips on wrong-state path — a CTE optimisation; the current volume does not justify it.
- `getConfig` two-query pattern — admin-only, small table; negligible.
- CSRF double-submit cookie pattern — pre-existing Sprint 1 design; out of scope for a course-module hardening sprint.

## Test Cases

| TC | Description | Task |
|---|---|---|
| TC-H-001 | `go build ./...` (no build tag) does not compile `SetTestContext` | T1 |
| TC-H-002 | `go test -tags testing ./internal/auth/...` compiles and passes unit tests | T1 |
| TC-H-003 | `ApproveSyllabus(ctx, wrongCourseID, syllabusID)` returns `ErrNotFound` | T2 |
| TC-H-004 | `ApproveSyllabusTx(ctx, tx, wrongCourseID, syllabusID)` returns `ErrNotFound` | T2 |
| TC-H-005 | Test migration includes `courses_created_at_id_idx`; `go build ./...` passes | T3 |
| TC-H-006 | `writeJSONResponse` and `writeJSONError` carry `@{"req":[...]}` annotations; `go build ./...` passes | T4 |
