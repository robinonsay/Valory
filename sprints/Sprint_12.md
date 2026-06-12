# Sprint 12 — E2E Hardening: The Real Student Journey in Playwright

## Objective

Address the PM's core criticism after the live demo: the e2e suite's
AI-cost-free rule meant no spec ever waited on AI output — exactly where the
demo bugs lived. This sprint added a PAID, AI-inclusive **journey tier** (run
on demand, PM-approved cost), deterministic DB seeding for the AI-free tier,
and full requirements traceability for the e2e surface.

## Work performed

| Task | Deliverable | Contributor | Verifier | Outcome |
|---|---|---|---|---|
| 12.1 | Two-tier Playwright config (`test:e2e` AI-free vs `test:e2e:journey`, retries=0, 240s); frontend/e2e/seed.ts superuser seeding helpers (stdin psql, SQL-literal escaping, UUID validation); seed-smoke.spec.ts; e2e README | senior-engineer | SQE + systems-engineer | PASS after fix round |
| 12.2 | journey/intake-to-syllabus.spec.ts — full real flow: preparing indicator → opening message → multi-turn AI intake → INTAKE_COMPLETE redirect → drafting indicator → generated syllabus rendered. **Passed live in 1.2m** | test-author | live runs + senior SQE | PASS after lead fixes (typing-indicator selector ambiguity; reply-vs-redirect race) |
| 12.3 | processing-indicators.spec.ts — 4 AI-free specs: preparing indicator, send-failure banner via aborted route, drafting indicator + auto-render via seeded syllabus, non-404 error banner | test-author | SQE + live runs | PASS |
| 12.4 | All 41 dangling requirement annotations closed (zero remain, independently verified); requirements/frontend/TEST-E2E.md journey-to-spec traceability plan | requirements-author (×2 — first agent under-scanned and mis-formatted; lead re-verified and merged) | SQE + lead scan | PASS after completion round |
| Fixes | Product bugs **found by the journey tier** (below) | software-lead amendments | SQE + systems + senior SQE | SHIP |

## Product bugs found by the new tests (the headline)

1. **Stale-deployment gap**: the e2e suite was running against a frontend
   image built before the latest commit — the indicator code wasn't deployed.
   Burned 4 paid journey attempts before diagnosis. Logged follow-up: build
   guard (Makefile rebuild target or build-stamp meta tag asserted by the
   suite) — see backlog.
2. **GenerateSyllabus 400'd on every kickoff-era course** (the PM's demo
   journey would have died at the syllabus step): since Sprint 11's kickoff,
   intake history starts AND ends with assistant turns; the Anthropic API
   rejects assistant-final conversations. Fix: `buildMessagesForSyllabus`
   anchors both ends with synthetic user turns (+3 unit tests). Verified
   live: pre-fix journey courses have 0 syllabi; post-fix course generated a
   6.8KB syllabus.
3. **Active-course chat had the same latent 400** (systems-engineer review
   follow-on): `Chat()` sent assistant-first history. Fix: `ensureUserFirst`
   (+2 unit tests).
4. **Syllabi stored with raw ``` fences** (senior-SQE finding): Claude wraps
   output in ```asciidoc fences; stripCodeFence previously left the language
   tag behind. Fix: fence line dropped wholesale (+1 unit test); applied in
   GenerateSyllabus.

## Review pipeline results

- SQE round 1: FAIL (seed-smoke missing try/finally; HOW-wording in
  REQ-FECONTENT-210/REQ-FECOURSE-540; stale TEST-E2E.md rows; wrong req ID in
  a journey comment) → all fixed.
- Systems engineer: PASS; flagged the Chat() latent 400 (fixed in-sprint),
  seeding-helper security review clean (no remote surface, correct escaping
  under standard_conforming_strings), cost discipline confirmed (no Approve
  click, MAX_TURNS=8, retries=0, afterEach cleanup).
- Senior SQE: **SHIP** with two majors folded in before commit (fence
  stripping; annotation syntax normalized in the journey spec).

## Verification

- Go matrix green (build, vet, `go test -tags testing ./...`);
  `make test-integration` green (15 packages).
- vitest 241/241; AI-free e2e 20/20 twice (self-healing proven).
- Journey tier: 1/1 passed live (~1.2m, one full real intake + syllabus).
- Live DB: journey course generated a real syllabus; DB left with 0 open
  courses.

## Cost ledger (journey tier, this sprint)

~9 journey-spec runs during development (4 against the stale deployment, 3
during spec debugging, 1 post-backend-fix pass, 1 verification) plus ~14
kickoff calls from AI-free-tier course creations. Steady-state: 1 journey run
= 1 kickoff + ~5 chat turns + 1 syllabus generation.

## Backlog / follow-ups

- **Stale-container guard** (systems recommendation): Makefile
  `e2e-rebuild-and-test` target (Option A) or build-stamp meta tag asserted
  by the journey tier (Option B, recommended). → Sprint 13 candidate.
- **Approve→generating e2e remains PLANNED**: withdrawal does not cancel
  in-flight content generation (runner has no per-course cancellation), so
  e2e approval is cost-unbounded. Needs a runner cancellation mechanism
  first. → PM decision on priority.
- Annotation grammar split (`@{"req": ...}` in Go vs `@{"req", ...}` in e2e)
  — PM to pick one canonical form; add a lint.
- REQ-FECOURSE-056/490 near-duplicates; REQ-FECOURSE-057 mild compound — PM
  requirement-hygiene sweep.
- seed.ts: NUL bytes not stripped (safe with static fixtures; note if dynamic
  content is ever passed).
- seedSyllabus/GenerateSyllabus version arithmetic not concurrency-safe
  (benign under workers=1 and current app flow).
