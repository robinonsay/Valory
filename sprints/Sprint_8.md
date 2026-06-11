# Sprint 8 — Integration Verification

## Objective

Make the project's "no DB mocks in integration tests" mandate real: stand up an ephemeral
PostgreSQL test stack, build a shared integration-test harness that applies the real embedded
migrations, cover the six core modules' repository layers with integration tests against the
genuine schema (RLS policies, FK cascades, CHECK constraints, partial unique indexes), and
implement the first HTTP-level end-to-end tests. Sprints 1–7 delivered the full backend and
SPA with unit tests only; before this sprint, no test had ever executed against the real
migrated schema.

## Environment constraint

The development environment for this sprint had **no Docker and no PostgreSQL server**.
All integration/e2e tests were therefore verified by compilation (`go test -c -tags
integration`), `go vet`, and deep static review against the migration SQL — not by execution.

**Sprint exit criterion (action required):** run `make test-integration` on a Docker-capable
machine. Failures, if any, roll into a fast-follow.

## Work performed

### Increment A — harness and hygiene

| Task | File | Engineer | Verifier | Outcome |
|---|---|---|---|---|
| A1 Ephemeral test DB stack | `docker-compose.test.yml` | junior-engineer | systems-engineer | PASS (first review) |
| A2 Integration harness | `internal/db/integrationtest.go` | senior-engineer | SQE, then systems-engineer | PASS after 3 rework rounds (see below) |
| A3 `test-integration` target | `Makefile` | junior-engineer | systems-engineer | PASS (first review); lead amendment added `-p 1` (see notes) |
| A4 Rename `REQ-FECOURSE.json` → `REQ-FE-COURSE.json` | `requirements/frontend/REQ-FE-COURSE.json` | junior-engineer | SQE | PASS (pure R100 rename) |

### Increment B — repository integration tests

| Task | File | Engineer | Verifier | Outcome |
|---|---|---|---|---|
| B1 auth | `internal/auth/integration_test.go` | test-author | SQE | PASS after 1 rework (traceability, flaky-sleep fix) |
| B2 user | `internal/user/integration_test.go` | test-author | SQE | PASS after 1 rework (2 vacuous CASCADE tests, traceability) |
| B3 course | `internal/course/integration_test.go` | test-author | SQE | PASS after 2 reworks (nonexistent-trigger blocker, server-seed crash, traceability) |
| B4 content | `internal/content/integration_test.go` | test-author | SQE | PASS after 2 reworks (server-conn RLS bypass, fabricated TC IDs) |
| B5 submission | `internal/submission/integration_test.go` | test-author | SQE | PASS after 1 rework (server-conn RLS bypass); first-review PASS thereafter |
| B6 grade | `internal/grade/integration_test.go` | test-author | SQE | PASS after 1 rework (fabricated TC IDs, helper delegation) |

### Increment C — e2e and records

| Task | File | Engineer | Verifier | Outcome |
|---|---|---|---|---|
| C1 HTTP e2e (TC-SYSFLOW-002, TC-SYSFLOW-003) | `cmd/server/e2e_test.go` | senior-engineer | systems-engineer | PASS (first review) |
| C2 Sprint record | `sprints/Sprint_8.md` | software-lead | senior-SQE final gate | this document |

### Lead amendments (verified at the final gate)

- `Makefile`: added `-p 1` to the integration `go test` invocation — package test binaries
  share one database; concurrent cross-package `TRUNCATE` would destroy each other's fixtures.
- `.gitignore`: anchored the bare `server` pattern to `/server` — it matched the `cmd/server/`
  directory and was silently hiding `cmd/server/e2e_test.go` from git.

## Harness design (internal/db/integrationtest.go)

- `IntegrationPool(t)` — process-wide pool (`sync.Once`), applies the real embedded
  migrations via the simple-query protocol, **skips** (never fails) when the DB is
  unreachable; DSN from `VALORY_TEST_DATABASE_URL` (default matches `docker-compose.test.yml`).
- `TruncateTables(t, pool, tables...)` — per-test isolation via `TRUNCATE … RESTART IDENTITY CASCADE`.
- `AcquireAsUser(t, pool, userID, role)` — issues `SET ROLE valory_app` (NOBYPASSRLS), then
  sets the `app.current_user_id` / `app.current_role` GUCs as the auth middleware does.
  Rejects empty userID.
- `AcquireAsServer(t, pool)` — `SET ROLE valory_app`, `app.current_role='server'`, and
  `app.current_user_id` = all-zeros UUID sentinel.
- Custom `AfterRelease`: `RESET ROLE` + GUC clear on every release, so `SET ROLE` never leaks
  between tests.

Two non-obvious design facts, discovered the hard way (each caused a failed review round):

1. The test bootstrap user is a PostgreSQL **superuser** and superusers bypass RLS entirely —
   even `FORCE ROW LEVEL SECURITY`. RLS tests are only meaningful after `SET ROLE valory_app`.
2. Several student RLS policies cast `current_setting('app.current_user_id', true)::uuid`
   **without a NULLIF guard**; an empty-string GUC crashes policy evaluation. Server-role
   connections therefore carry a valid-but-unmatchable all-zeros UUID.

## Production security findings (Sprint 9 candidates — all currently masked by finding 1)

1. **Production runs as a superuser; RLS is completely bypassed.** `docker-compose.yml` sets
   `POSTGRES_USER: valory_app`, making the bootstrap **superuser** the app's login role.
   Migration 002's `CREATE ROLE valory_app … NOBYPASSRLS` is skipped (role exists), so every
   RLS policy in the system — the implementation of REQ-SECURITY-002 — is silently inert in
   production. Data isolation currently rests on handler-level ownership checks alone.
   Fix direction: bootstrap as `postgres`, create `valory_app` as a `LOGIN NOBYPASSRLS`
   non-owner role, grant per migrations.
2. **Missing `NULLIF` guards in student policies** (final migrated state: 006 student_badges,
   007 submissions, 008 grades; the courses policy from 003 is re-created WITH a guard by 004,
   and 004's own policies are guarded). Under a genuine non-superuser role,
   any connection whose `app.current_user_id` GUC is empty — e.g. recycled pool connections
   after the production `AfterRelease` hook, used by background/server paths — raises
   `invalid input syntax for type uuid: ""` and aborts the query. The agent pipeline's course
   transitions would crash the day finding 1 is fixed, unless this is fixed with it.
3. **Migration 006 grants `valory_app` nothing on badge tables.** Under a proper non-superuser
   role, all badge operations would be permission-denied.

## Other findings and deferrals (Sprint 9 backlog)

- **Full-lifecycle e2e (TC-SYSFLOW-001) blocked on testability:** `main()` wires everything
  inline (no extractable `buildServer()`), and Chair/Professor/Reviewer take the concrete
  `*ThrottledClient` — the AI client cannot be faked without a refactor. C1's wiring is a
  documented bounded copy of `main()`'s subset.
- **Test-case registry gaps:** many schema-level scenarios covered by B1–B6 have no registered
  TC entries (`testcases/*.json` are HTTP-scoped). Reviews removed all fabricated/mis-mapped
  TC references; tests cite real TC IDs where a registered case genuinely matches (e.g.
  TC-COURSE-001, TC-SECURITY-*, TC-USER-021) and otherwise rely on `@{"verifies"}` REQ
  annotations. PM to decide whether to register integration-level TC entries.
- **Comment-accuracy follow-up (from the final gate):** harness comments in
  `internal/db/integrationtest.go` list migration 003 among the unguarded policies; correct to
  006/007/008 (003's courses policy is superseded by 004's guarded version).
- **Legacy test scaffolding:** most modules carry a pre-existing `TestMain` +
  `TEST_DATABASE_URL` pattern with inline duplicated schema (drift risk vs. real migrations).
  Consolidation onto the new harness is deferred.
- **`REQ-FE-COURSE.json` internal IDs** still use the `REQ-FECOURSE-NNN` pattern, referenced
  from 15+ frontend files — multi-file normalization deferred.
- Integration tests for badge, admin, audit, notify modules — deferred (kept sprint sized).

## Requirements exercised this sprint

REQ-SECURITY-002 (RLS isolation: courses, content, submissions, grades), REQ-SECURITY-005,
REQ-COURSE-001 (single-active partial index), REQ-AGENT-003, REQ-AGENT-004,
REQ-AUTH-001/002/003/006, REQ-USER-001/002/003/005/006/007, REQ-CONTENT-001/004,
REQ-SUBMISSION-001/002, REQ-GRADE-001/002/003, REQ-SYS-008, REQ-SYS-011, REQ-SYS-030.

## Verification summary

- `go build ./...`, `go vet ./...` — clean (integration files excluded from normal builds).
- `go build -tags integration ./...`, `go vet -tags integration ./...` — clean.
- `go test -c -tags integration` per package (auth, user, course, content, submission, grade,
  cmd/server) — all compile.
- `make test` (unit suites, `-tags testing`) — all green; frontend 189/189 green.
- Every file passed its designated review gate; all failed reviews were reworked by the
  originating role and re-reviewed to PASS.
