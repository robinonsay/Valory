# Sprint 9 — Security Hardening

## Objective

Fix the three production security findings recorded in Sprint 8 (`sprints/Sprint_8.md`):

1. Production connected to PostgreSQL as a bootstrap **superuser** named `valory_app`, so every
   RLS policy — the implementation of REQ-SECURITY-002 — was silently bypassed.
2. The student RLS policies from migrations 006 (student_badges), 007 (submissions ×2), and
   008 (grades) cast `current_setting('app.current_user_id', true)::uuid` without a `NULLIF`
   guard; under a genuine non-superuser role, an empty-string GUC (the state every recycled
   pool connection carries) crashes policy evaluation with `invalid input syntax for type uuid`.
3. Migration 006 granted `valory_app` nothing on the badge tables — permission-denied under a
   proper role.

All three were masked by finding 1: fixing the superuser without the other two would have
broken the agent pipeline and the badge module on day one.

## The fix, in three layers

**Schema (`migrations/009_security_hardening.sql`)** — recreates the four unguarded student
policies with the `NULLIF(..., '')::uuid` idiom (established by migration 004 for the courses
policy) and adds least-privilege GRANTs: `badges` SELECT (catalog is read-only for the app;
seed rows are written by the migration runner), `student_badges` SELECT/INSERT/UPDATE (matching
exactly the SQL the badge repository issues). Applied automatically at api startup.

**Bootstrap (fresh deployments)** — `docker-compose.yml` now bootstraps the cluster with a
dedicated superuser `postgres` (`DB_SUPERUSER_PASSWORD`); a first-boot init script
(`scripts/initdb/01-app-role.sh`, mounted read-only at `/docker-entrypoint-initdb.d`) creates
`valory_app` as `LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS NOINHERIT` with
`APP_DB_PASSWORD` (bound via psql `:'pw'` quoting — never interpolated into SQL) and transfers
ownership of the `valory` database so the api can run embedded migrations (PG15+ schema-public
ownership; pgcrypto/pg_trgm are trusted extensions). Every statement in migrations 001–009 was
audited as legal for a non-superuser database owner. `.env.example` replaces `DB_PASSWORD`
with the two new variables. The devcontainer compose was aligned to the same layout (dev
volumes must be recreated once: `down -v`; dev RLS is now genuinely enforced).

**Existing deployments** — init scripts only run on empty data volumes, so
`docs/runbooks/rls-role-hardening.md` walks an operator through in-place conversion: create a
new `postgres` superuser while still connected as the old one, reconnect, strip `valory_app`
to the hardened attribute set, verify ownership, update `.env`, redeploy, and run verification
queries plus an RLS smoke test. Rollback paths (volume snapshot primary, pg_restore fallback)
are included.

## Work performed

| Task | File | Engineer | Verifier | Outcome |
|---|---|---|---|---|
| T1 NULLIF guards + badge GRANTs | `migrations/009_security_hardening.sql` | senior-engineer | systems-engineer | PASS (first review) |
| T2 Bootstrap superuser split | `docker-compose.yml` | senior-engineer | systems-engineer | PASS (first review) |
| T3 First-boot role script | `scripts/initdb/01-app-role.sh` | senior-engineer | systems-engineer | PASS (first review; lead amendments: comment label fix, annotation colon form) |
| T4 Env contract | `.env.example` | junior-engineer | SQE | PASS after devcontainer follow-up (below) |
| T5 Conversion runbook | `docs/runbooks/rls-role-hardening.md` | design-author | systems-engineer | PASS after 2 rework rounds (see below) |
| T4b Devcontainer alignment (review-discovered) | `.devcontainer/docker-compose.yml` | senior-engineer | systems-engineer | PASS (first review) |
| T4c SDD accuracy (review-discovered) | `docs/sdd/14-infra.adoc` | junior-engineer | SQE | PASS after 1 rework |
| T6 Empty-GUC regression tests | `internal/db/rls_integration_test.go` | test-author | SQE | PASS (first review) |
| T7 Badge integration tests | `internal/badge/integration_test.go` | test-author | SQE | PASS (first review) |
| T8 Harness comment accuracy post-009 | `internal/db/integrationtest.go` | senior-engineer | SQE | PASS (first review) |

### Lead amendment at the final gate

The Senior SQE gate (which approved delivery) caught that `docs/sdd/14-infra.adoc` still
carried the pre-hardening compose snippet (`POSTGRES_USER: valory_app`, `${DB_PASSWORD}`) and
stale env examples (`DB_PASSWORD`, the nonexistent `SESSION_SECRET`/`SESSION_TIMEOUT_SECONDS`)
that two earlier reviews missed. Fixed in-sprint by the software lead: the quoted db service
now mirrors `docker-compose.yml` and the env snippet mirrors `.env.example` (including the
real `AUTH_*` variables and the `DATABASE_URL`/`APP_DB_PASSWORD` equality note).

### Review-loop notes

- The runbook needed two rework rounds, both caught by the systems-engineer gate: round 1
  (raw password pasting into SQL; a doubly-broken pg_restore path; wrong expected
  `rolbypassrls` output) and round 2 (the first fix's `-e PW` indirection expanded `$PW` on
  the **host** shell — `docker compose exec` passes argv directly with no container shell —
  silently setting empty passwords; tar restore path mismatch; pg_restore referencing a
  host-only path inside the container; missing `-T` on stdin-receiving execs).
- The `.env.example` review surfaced the devcontainer as a leftover `DB_PASSWORD` consumer
  still running the superuser-as-app layout, and stale SDD passages — both fixed as
  review-discovered tasks rather than left as drift.

## Verification

- Full matrix green in this environment: `make build vet test` (unit suites), frontend suite
  untouched, `go build/vet -tags integration ./...`, and per-package test-binary compiles for
  all 8 integration-test packages + `cmd/server`.
- `internal/db/rls_integration_test.go` pins the finding-2 fix at runtime: empty-GUC
  connections get 0 rows + no error from the three previously-crashing tables, INSERT fails
  with 42501 (policy violation) not 22P02 (cast error), and a control test proves legitimate
  access still works.
- `internal/badge/integration_test.go` pins the finding-3 fix: a `SET ROLE valory_app`
  connection can read the badge catalog (pre-009: permission denied), the server-role award
  path and student redeem path work under the real policies, and cross-student isolation holds.
- **Environment constraint (unchanged from Sprint 8): no Docker here.** Tests are
  compile-verified and statically reviewed. **Exit criterion: run `make test-integration` on a
  Docker-capable machine** — it now also executes the Sprint 9 regression tests.

## Deployment notes (action required)

- **Fresh deployments:** set `DB_SUPERUSER_PASSWORD` and `APP_DB_PASSWORD` in `.env` (the
  `DATABASE_URL` password must equal `APP_DB_PASSWORD`); everything else is automatic.
- **Existing deployments:** follow `docs/runbooks/rls-role-hardening.md` start to finish
  before deploying the new compose file.
- **Devcontainers:** recreate the dev database once (`docker compose -f
  .devcontainer/docker-compose.yml down -v`).

## Deferred / backlog (carried to Sprint 10)

- `docs/sdd/sdd.html` is a stale generated artifact (predates Sprints 8–9 source edits);
  regenerate with asciidoctor when the SDD is next touched.
- `.env.example` (pre-existing) lists `AGENT_RETRY_LIMIT`, `CORRECTION_LOOP_MAX`,
  `CONTENT_GENERATION_TIMEOUT_SECONDS`, and the devcontainer compose passes some of them as
  env vars — nothing reads them (they are database-backed `system_config` keys); clean up.
- Sprint 8 carry-overs unchanged: full-lifecycle e2e (needs `buildServer()` extraction + AI
  client fake), integration tests for admin/audit/notify, TC registry entries for schema-level
  scenarios, `REQ-FECOURSE-NNN` internal ID normalization, legacy `TEST_DATABASE_URL`
  scaffolding consolidation.
- Migration 002's `REVOKE UPDATE, DELETE ON audit_log FROM valory_app` relies on revoking the
  owner's own implicit privileges; an owner can re-grant itself. Acceptable today (app code
  never re-grants), but a dedicated low-privilege runtime role separate from the migration
  owner would make audit_log append-only enforcement robust — candidate for a future sprint.
