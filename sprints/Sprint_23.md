# Sprint 23 — Persistent Learning Profile + Onboarding (FINAL sprint)

## Objective

PM item 8: Valory saves a student's learning style across courses, captured via
an onboarding LLM interaction. Sequenced last so the first-login UX composes:
forced password change (Sprint 20) → consent → onboarding → app. The profile
injects into course-generation prompts for ALL of a student's courses — the
personalization keystone the whole architecture was built toward. Design:
docs/sdd/SDD-023-learning-profile.md.

## Work performed

| Task | Deliverable | Contributor | Verifier | Outcome |
|---|---|---|---|---|
| 23.1 | SDD-023: learning_profiles (natural-language summary, one per student) + onboarding_messages, FORCE RLS + student/server policies; onboarding 3-5 Q chat → one summarization call → profile; injection into 5 prompt points with graceful degradation; soft skippable first-login gate; the Sprint-22 RLS lesson baked into the test plan | design-author | SQE + systems | PASS |
| 23.2 | 26 reqs: REQ-PROFILE-001..020 (new module), REQ-FEPROFILE-001..004, REQ-SYS-069/070 | requirements-author | SQE + schema | PASS |
| 23.3 | migration 020; internal/profile (repository conn(ctx) + server-pool variants, onboarding service + summarization, LoadProfileSummary fail-soft, AppendProfileBlock, 6 endpoints); 5 injection points (professor generate/regenerate, chair intake/syllabus/assignment-syllabus); auth session onboarding_prompted; main.go wiring; non-superuser RLS + injection tests | senior-engineer (1st agent STALLED after data+service layers; 2nd completed handler+injection+wiring+tests; +1 fix round) | SQE + systems | PASS (round 2) |
| 23.4 | OnboardingChatView, ProfileView, auth store onboarding_prompted, SOFT router guard (after must_change_password + consent, skippable, once), StudentLayout nav; 553 vitest | junior-engineer | SQE + e2e | PASS |
| 23.5 | HTTP-handler onboarding journey + professor-injection integration (verbatim-when-present / unchanged-when-absent) + onboarding.spec.ts (skip-gated, AI portions honest); all ran LIVE green | test-author | senior SQE + live runs | PASS |

## Review pipeline results

- **Systems gate: PASS** — confirmed RLS isolation under the NON-superuser role
  (db.AcquireAsUser/AcquireAsServer + a manual psql probe: student B cannot read
  student A's profile; the pipeline server conn reads for injection; studentID
  comes from the trusted course row, not user input). Fail-soft LoadProfileSummary
  (returns "" on any error → never blocks generation); onboarding unbilled (no
  agent_token_usage write); PII clean; 2000-char cap server-side;
  AppendProfileBlock uses concatenation not fmt.Sprintf (no %-verb injection).
- **SQE gate: FAIL — 1 BLOCKING:** Complete() returned an error on the
  NON-FATAL post-write steps (cleanup, SetOnboardingPrompted), so the handler
  returned 500 even though the profile was persisted — and the
  onboarding_prompted flag was never set, stranding the student (re-nudge +
  409 on retry). Both gates independently flagged this. Plus the systems gate's
  test/prod mismatch (/profile route missing RequireRole("student") → admin got
  a 500 instead of a clean 403) and non-blocking nits (sentinel error,
  REQ-PROFILE-019/020 HOW-language).
- **Fix round:** Complete() restructured so post-write steps are best-effort
  (flag set before cleanup, failures logged WITHOUT PII, returns (summary,nil)
  whenever the profile persisted; an error only when the profile was NOT
  written); ErrNoActiveSession sentinel + errors.Is → clean 409;
  RequireRole("student") added to /profile in main.go; reqs reworded to behavior.
- **SQE delta: PASS. Senior SQE: SHIP** — independently re-ran the live
  integration tier + a manual RLS psql probe; confirmed the personalization loop
  is real and safe; noted program-level readiness.

## Verification

- go build/vet clean; `go test -tags testing ./...` green; vitest 553/553;
  vue-tsc clean; gofmt clean on sprint-touched files; 26 reqs schema-valid.
- Integration tier ran LIVE (senior gate re-ran): 7 non-superuser RLS probes
  (cross-student denied, server reads for injection); the professor
  GenerateSection injection test (profile present → summary VERBATIM in the
  system prompt; absent → byte-for-byte the no-profile prompt — protecting ALL
  existing course generation); HTTP onboarding journey (start→advance→complete→
  GET profile; skip→flag set, no profile; PUT manual_edit); session
  onboarding_prompted.

## Backlog / conditions

- **(Operator note — release)** Existing students all default onboarding_prompted=false,
  so each gets the one-time onboarding nudge on first login after deploy
  (by design). Inform support before deploying.
- **(Minor)** No explicit admin-gets-403 test on /profile (guard wired + proven
  in the HTTP harness, just not asserted directly) — add next cleanup pass.
- **(Minor, accepted/pre-existing)** Summarization shares the system-wide
  absence of an explicit context deadline; per-message length cap on
  onboarding/advance is self-targeting only; per-section profile load adds one
  negligible indexed PK read per section.
- **(Standing)** Quarantine: professor_max_tokens untouched. First profile-build
  agent STALLED mid-task (a recurring senior-engineer failure mode — see Sprint
  16); the lead detected the half-wired state (handler/injection/wiring/tests
  all missing despite a passing build) and dispatched a precise completion task.
