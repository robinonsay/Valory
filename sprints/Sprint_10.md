# Sprint 10 — Demo Readiness: UI Flow Fixes, Config Alignment, Onboarding, Logout, E2E

## Objective

Make the first live demo possible. Fix every defect blocking the student and admin
UI journeys, deliver the requested features (getting-started pages, admin
configuration guide, student logout), and stand up a browser-driven Playwright e2e
suite as a repeatable preflight. Unlike Sprints 8–9, every change in this sprint was
verified against the **running production stack** (Docker, real `valory_app`
non-superuser role, enforced RLS) — which surfaced production bugs that no unit,
integration, or static review had caught.

## Carry-in fixes (found driving the UI before the sprint proper)

- **Login was impossible via the UI**: LoginView rendered an `Email` field
  (`type="email"` rejects usernames) and posted `{email}` to an API expecting
  `{username, password}`. Its unit tests asserted the broken contract.
- **Consent appeared to fail although it succeeded**: the API client called
  `response.json()` on `204 No Content` responses (consent, user deactivate/
  activate/delete, reset-confirm) and threw on the empty body.
- **Empty lists white-screened pages**: Go marshals nil slices as JSON `null`;
  eight list-consumption sites iterated it (courses, sections, homework, badges,
  notifications, users, audit entries, course oversight). All guarded with `?? []`.

## Work performed

| Task | File(s) | Engineer | Verifier | Outcome |
|---|---|---|---|---|
| R1 Requirements: course topic contract, onboarding + guide reqs | requirements/frontend/REQ-FE-ONBOARD.json (new, 19 reqs), REQ-FE-COURSE.json | requirements-author | SQE + schema validation | PASS |
| T1 Runner config key `correction_loop_max_iterations` (was a nonexistent key; admin setting silently ignored) | internal/agent/runner.go | junior-engineer | SQE + systems-engineer | PASS; lead amendment aligned fallback to seeded default 5 |
| T2 Config page on the backend's real 13 keys, labels/hints, server-exact weight rule, per-field 422 display | frontend …/admin/systemConfig.ts, SystemConfigView.vue | junior-engineer | SQE + systems-engineer | PASS after lead fixes (phantom REQ IDs remapped; `>=` tolerance boundary) |
| T3 Course modal: enabled topic input, `{topic}` body, title→topic card fallback | frontend …/CourseDashboardView.vue, types/course.ts | junior-engineer | SQE + systems-engineer | PASS after lead fix (backend returns the course FLAT; `response.course` was always undefined) |
| T4 Server-side logout (`logoutServer`), StudentLayout with persistent nav, GettingStartedView, router restructure | frontend stores/auth.ts, layouts/, router/, views/ | senior-engineer | SQE + systems-engineer | PASS after lead amendments (role-scoped getting-started routes — one path cannot render in two layouts; single `/` record; admin-consent guard rule) |
| T5 Admin configuration guide | docs/guides/admin-configuration.md | design-author | SQE fact-check vs code | PASS (honestly documents 3 reserved/env-overridden keys) |
| T6 Playwright e2e suite (12 tests, 5 specs, AI-cost-free) | frontend/e2e/, playwright.config.ts | test-author | senior-SQE + live runs | PASS after lead fixes (in-SPA navigation — memory-only tokens do not survive `page.goto`; CSRF-correct self-healing cleanup via `page.request`) |
| Sprint record | sprints/Sprint_10.md | software-lead | — | this document |

## Review pipeline results

- **SQE**: FAIL → fixed (phantom requirement IDs in annotations).
- **Systems engineer**: FAIL → fixed (course-create flat-response mismatch that
  navigated to `/courses/undefined/intake`; admins able to land on the chrome-less
  `/consent` page; weight-tolerance boundary mismatch; runner fallback drift).
- **Senior SQE final gate**: SHIP. Follow-ups delivered in-sprint: guardFn rule-6
  unit tests added. Logged for PM: ~25 pre-existing dangling requirement
  annotations in four legacy views (not introduced or worsened by this sprint).

## Production bugs found only by live e2e (the headline of this sprint)

1. **The course module was RLS-broken in production.** `CourseRepository` ran every
   query on bare pool connections whose RLS GUCs are empty (production
   `AfterRelease` clears them). Under the genuine `valory_app` role: student course
   creation was denied by RLS, the student course list was always empty, and the
   agent pipeline's transitions would have failed the same way. Fix:
   request-scoped `conn(ctx)`/`BeginTx(ctx)` wiring in the course repository
   (matching badge/submission/grade), plus `db.NewServerPool` whose
   `BeforeAcquire` sets the server-role GUCs (all-zeros user sentinel) for the
   chair/professor/reviewer/runner actors in `cmd/server/main.go`.
2. **Deleting any user failed with `permission denied for table audit_log`.**
   PostgreSQL runs FK referential-integrity checks as the referencing table's
   owner using `SELECT … FOR KEY SHARE`, which requires the UPDATE privilege that
   migration 002 deliberately revokes for append-only tamper evidence. Migration
   010 drops `audit_log_admin_id_fkey`: the hash chain — not referential
   integrity — is the tamper-evidence mechanism, and admin accounts are only ever
   deactivated through the API, never deleted. The integration suite had masked
   both bugs because its bootstrap login is a superuser, which bypasses privilege
   checks inside RI triggers.

## Verification

- Go matrix green: `go build ./...`, `go vet ./...`, `go test -tags testing ./...`.
- `make test-integration` fully green (16 packages) after every backend change.
- Frontend unit suite green: 25 files, 219 tests (vitest; e2e dir excluded).
- **Live e2e: 12/12 passing** against the running stack (`npm run test:e2e` in
  frontend/), leaving the database clean (0 open courses). The suite consumes no
  Anthropic tokens (course creation inserts a row; only intake chat calls the AI,
  and no spec touches it).

## Deferred / backlog

- Dangling requirement annotations in CourseHubView, NotificationsPanel,
  AuditLogView, UserManagementView (pre-existing) — PM to reconcile.
- `parseFieldError` only attaches 422 errors carrying a `key:` prefix (cross-key
  weight errors); single-key errors fall back to the generic banner.
- Admins still see the one-click consent interstitial once per session (the SPA
  cannot know server-side consent state on a fresh login); harmless but worth a
  `GET /consent/status` endpoint eventually.
- `session_inactivity_seconds` / `account_lockout_seconds` config keys are inert
  (server reads env vars); `audit_retention_days` has no purge worker.
