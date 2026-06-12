# Sprint 11 — Intake Chat End-to-End + Consent Transparency

## Objective

Fix every defect from the PM's first live browser demo. All five demo notes
traced to three real defects and one missing feature; the headline finding was
that the Chair agent's intake driver had been built but **never wired**:
`RunIntakeStep` and `GenerateSyllabus` had zero callers, no chat-history
endpoint existed, and the frontend ignored the synchronous chat reply while
waiting for SSE events the backend never emits.

## Root causes (from the PM demo notes)

1. **No consent statement link** — ConsentView rendered one generic sentence;
   no AI-processing consent statement existed anywhere in the repo.
2. **Empty chat page after course creation** — nothing triggered the Chair's
   opening question; no history endpoint meant stored messages could never load.
3. **Chat input off the page** — `height: 100vh` inside StudentLayout's nav
   chrome pushed the input bar below the viewport.
4. **"Agent did not respond"** — it did: POST `/chat` returns `{"reply"}`
   synchronously, but the frontend discarded the response body and listened for
   SSE `message` events that nothing emits. The reply was paid for, stored, and
   rendered nowhere. The SSE `status_change` redirect was also unreachable
   (never emitted; payload shape parsed wrong).
5. **No send feedback** — no typing indicator during the multi-second Claude
   call; POST failures silently swallowed.

## Work performed

| Task | File(s) | Engineer | Verifier | Outcome |
|---|---|---|---|---|
| T1 Requirements: intake chat lifecycle + consent display (14 new reqs: REQ-FECOURSE-026/027/260/261/270, REQ-FEAUTH-056/057/167/168, REQ-AGENT-017..021) | requirements/frontend/*, l2-requirements.json | requirements-author | SQE + schema validation | PASS after fix round (HOW→WHAT rewording) |
| T2 Consent statement v1.0 + intake chat design contract (endpoint shapes, SSE envelope, kickoff semantics) | docs/consent/ai-processing-consent-v1.0.adoc, docs/sdd/15-intake-chat.adoc (+frontend split) | design-author | SQE fact-check vs code | PASS; 4 open questions resolved by lead, doc split under 500-line rule |
| T3 Backend intake wiring: GET /chat/history (owner-gated, `[]` never null), intake-aware POST /chat (sentinel detect+strip → intake→syllabus_draft → status_change event → async GenerateSyllabus), async idempotent kickoff on course creation via course.IntakeStarter DI seam, migration 011 | internal/agent/{chair,handler,handler_test}.go, internal/course/handler.go, cmd/server/main.go, migrations/011_intake_chat.sql | senior-engineer (×2 — first agent stalled mid-edit; second continued in place) | SQE + systems-engineer | PASS after fix round |
| T4 IntakeChatView rework: history on mount, reply from POST body, typing indicator, dismissible error banner, SSE envelope (.payload) parsing, status-based redirects, viewport fix (100% not 100vh) | frontend/src/views/IntakeChatView.vue + test | junior-engineer | SQE + systems-engineer | PASS |
| T5 ConsentView: full statement in scrollable panel, version label, single CONSENT_VERSION constant for display+POST | frontend/src/views/ConsentView.vue + test | junior-engineer | SQE + systems-engineer | PASS after fix round (annotations, /courses test name, finally) |
| T6 E2E: consent-statement spec, intake input-in-viewport regression spec, history-contract spec; audit of 5 existing specs (none broken) | frontend/e2e/consent-statement.spec.ts, intake-chat.spec.ts | test-author | senior-SQE + live runs | PASS — 15/15 twice against the running stack |
| Sprint record | sprints/Sprint_11.md | software-lead | — | this document |

## Review pipeline results

- **Round 1 — SQE: FAIL** (5 major, 3 minor): wrong consent req annotations
  (password-reset IDs); missing per-test `@verifies` annotations;
  REQ-AGENT-020/021 `Test`-verified but untested; `/dashboard` vs `/courses`
  requirement/test contradiction; HOW-wording in 3 requirement descriptions.
- **Round 1 — Systems engineer: FAIL** (1 blocker, 3 major, 1 minor):
  pool connection held across the 10–30s Claude call in the kickoff retry path
  (pool-exhaustion deadlock); unbounded Claude cost amplification on failing
  kickoffs; missing `id` tiebreaker in the last-assistant-message UPDATE;
  silent stranding of courses when background GenerateSyllabus fails; design
  doc over the 500-line limit. All pool/GUC/RLS triples and migration 011
  apply-safety verified clean.
- **Fix round**: connection released before every Claude round-trip; kickoff
  attempts capped at 3 via `courses.intake_kickoff_attempts` claimed atomically
  with the flag (a failed kickoff never bricks a course — the student can
  initiate via POST /chat); `ORDER BY created_at DESC, id DESC`; `api_failure`
  event emitted on GenerateSyllabus failure; REQ-AGENT-020/021 now tested via
  injectable seams on AgentHandler (nil-resolving to real implementations).
- **Re-review — Systems: PASS. SQE: FAIL→PASS** after lead amendments
  (REQ-FECOURSE-260 residual HOW clause; parent REQ-FEAUTH-036 still said
  /dashboard; ConsentView test router mock registered /dashboard).
- **Senior SQE final gate: SHIP.** Traceability walked for all 5 new backend
  reqs; zero new dangling annotation IDs; consent statement claims
  cross-checked against code (deletion rights, audit logging, consent
  recording); migration verified applied and idempotent in the live DB.

## Live verification (running production stack)

- **E2E: 15/15 passing, twice** (self-healing cleanup proven; DB left with 0
  open courses; stack left running for demos).
- **Kickoff verified against real Claude**: recent courses show
  `intake_kickoff_sent=t`, `attempts=1`, exactly one assistant opening message
  each — no duplicates, no runaway retries.
- Go matrix green: `go build ./...`, `go vet -tags testing ./...`,
  `go test -tags testing ./...`; `make test-integration` green (migration 011
  exercised from scratch).
- Frontend unit suite green: 25 files, 233 tests.

## Cost note

Course creation now makes **one async Claude call** (the opening intake
question, PM-approved). This includes e2e runs: the Sprint 11 verification
consumed 6 background kickoff calls. E2E specs never send chat messages and
never assert on AI-generated content.

## Deferred / backlog

- `generating` course status is in Withdraw()'s allowed-from list but never
  set by `RunContentGeneration` — pre-existing gap, PM to adjudicate.
- ~39 pre-existing dangling requirement annotations in 4 legacy views
  (CourseHubView, NotificationsPanel, AuditLogView, UserManagementView) —
  carried from Sprint 10; this sprint introduced none.
- Empty-reply guard: IntakeChatView pushes an agent bubble even for
  `reply === ""` (masked today by the immediate redirect in the only path that
  produces it) — add a content guard.
- Sprint 10 leftovers still open: GET /consent/status endpoint (admins still
  see the interstitial once per session), inert `session_inactivity_seconds` /
  `account_lockout_seconds` config keys, `audit_retention_days` purge design,
  `parseFieldError` single-key 422 attachment.
- Requirement JSON files were re-serialized to uniform indent=2 during lead
  amendments — cosmetic one-time diff churn; semantic content verified
  identical apart from intended edits.
