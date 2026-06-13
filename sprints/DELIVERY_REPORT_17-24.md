# Final Delivery Report — Sprints 17–24 (PM Feature Batch, June 2026)

Status: **ALL NINE PM ITEMS DELIVERED — every sprint passed the senior-SQE gate.**
Execution: 2026-06-12 → 2026-06-13. Software Lead orchestration.

## PM request → delivery

| # | PM item | Sprint | Outcome |
|---|---|---|---|
| 1 | AsciiDoc math in LaTeX renders in HTML + PDF (served and exported) | **24** | SHIP — STEM convention + legacy `$…$` compatibility in all 3 render paths (in-app KaTeX, self-contained HTML export, asciidoctor-mathematical PDF); XSS-hardened sanitizer |
| 2 | Course generation log shows raw JSON | **17** | SHIP — every agent event renders as a natural sentence; raw JSON can never reach the screen |
| 3 | Admins create + assign courses to students | **22** | SHIP — per-student course-instance model; RLS-isolated; rides the existing generation poller |
| 4 | Users reset password after first sign-in | **20** | SHIP — forced first-login change; authenticated change-password; other sessions invalidated |
| 5 | Student emailed credentials + reset instructions on account creation | **20** | SHIP — system-generated temp password (no durable credential by email); welcome email; resend |
| 6 | Admin-configurable from-address + email server (free/self-hosted) | **19** | SHIP — `internal/email` Mailer; none/STARTTLS/TLS; AUTH optional (free postfix); managed-secret password; test-send |
| 7 | Email 2FA for students + admins | **21** | SHIP — two-phase login, hashed single-use OTP, global toggle gated on a verified test-send, break-glass recovery |
| 8 | Persistent learning style across courses (onboarding LLM) | **23** | SHIP — learning_profiles; onboarding chat; profile injected into all course-generation prompts |
| 9 | Production fix for the SSL warning when self-hosting | **17** | SHIP — operator-supplied certs + reverse-proxy mode + TLS runbook |

## Execution order and sprint outcomes

17 → 24 → 19 → 20 → 21 → 22 → 23 (dependency-driven: the email subsystem preceded
welcome emails and 2FA; math and admin-courses ran independently).

| Sprint | Theme | Review rounds | Senior gate |
|---|---|---|---|
| 17 | Generation-log readability + production TLS | 1 round + folds | SHIP |
| 24 | LaTeX math end-to-end | 3 SDD rounds (security) + folds | SHIP |
| 19 | Admin-configurable email | 2 rounds (SMTP header injection, anti-enum) | SHIP |
| 20 | Account lifecycle (temp pw + forced change + welcome email) | 2 rounds + Finding-1 fold | SHIP |
| 21 | Email 2FA | 2 rounds (live TOCTOU probe) | SHIP |
| 22 | Admin course creation & assignment | 2 rounds (FORCE-RLS blocker) | SHIP |
| 23 | Learning profile + onboarding | 2 rounds (Complete() partial-success) | SHIP |

## What the review pipeline caught (value delivered by the gates)

- **Sprint 22 — production-only RLS failure:** AssignmentRepository used the plain
  pool (cleared GUCs) on a FORCE-RLS table; all 28 integration tests PASSED only
  because the test DB role is a superuser that bypasses RLS. The feature would
  have been dead on arrival in production. Fixed + two new non-superuser tests now
  guard it permanently. Recorded as a durable project memory.
- **Sprint 21 — OTP brute-force concurrency:** a live 20-way concurrent-verify
  probe proved the attempt-cap is atomic under `pg_advisory_xact_lock` (not
  raceable).
- **Sprint 19 — SMTP header injection:** caught a latent CRLF-injection vector in
  the mailer before Sprint 20/21 caller-supplied subjects could reach it.
- **Sprint 24 — three sanitizer rounds:** each caught a real XSS regression
  (style-overlay re-open, a regex that passed `position:fixed`, a false KaTeX
  claim) before the math feature could ship an injection surface.
- **Sprints 20 & 23 — login/onboarding-flow correctness:** the session-restore
  flag gap (Sprint 20) and the Complete() partial-success strand (Sprint 23) were
  both caught at the senior/SQE gate.

## Cross-cutting properties maintained

- **Additive migrations** 015*–020 (no destructive schema changes); RLS changes
  always carry non-superuser probe tests.
- **Graceful degradation:** every personalization/feature path is byte-for-byte
  the prior behavior when unconfigured (no profile, no email, 2FA off, no
  assignment) — the right risk posture across a large change set.
- **Secret hygiene:** SMTP password + OTP + temp password + profile summary never
  appear in logs, audit payloads, or HTTP responses (managed-secrets AES-256-GCM
  where persisted; per-call API-key resolution).
- **No CDN dependency** anywhere (KaTeX assets embedded for offline self-hosts).

## Test posture at delivery

- Backend: `go build`/`vet` clean; `go test -tags testing ./...` green; the live
  integration tier (Postgres + Mailpit) green across email, account-lifecycle,
  2FA, assignment (incl. HTTP-level RLS probes), and learning-profile journeys.
- Frontend: 553 vitest tests green; vue-tsc clean.
- e2e: Playwright specs authored and skip-gated for every feature
  (admin-config, account-lifecycle, two-factor-login, admin-assignment,
  onboarding); the live/paid-AI portions are gated.

## Outstanding before merge/deploy (operator + PM actions)

1. **Commit hygiene (IMPORTANT):** the working tree on `feature/new-features`
   bundles all seven sprints PLUS an **unreviewed, pre-existing
   `professor_max_tokens` feature** (migration 015, admin config UI,
   REQ-ADMIN-010) that was quarantined from every review. It is intermingled
   line-level in cmd/server/main.go, internal/agent/professor.go, and
   requirements/l2-requirements.json. Decide: stage the sprint work explicitly,
   or ship the max-tokens feature too — but it has NOT passed review and
   REQ-ADMIN-010 is non-atomic/>20 words, so it needs a formal pass first.
2. **Release-gate e2e:** run the budgeted live AI journeys on a freshly rebuilt
   stack (prod compose does not hot-reload) — Sprint 24 math render, and the
   onboarding/assignment AI paths — before tagging.
3. **Operator notes:** (a) reverse-proxy mode requires the compose healthcheck
   scheme change (Sprint 17 runbook); (b) all existing students get a one-time
   onboarding nudge on first login post-deploy (Sprint 23); (c)
   VALORY_2FA_BREAK_GLASS is presence-triggered (Sprint 21).
4. **Backlog (non-blocking):** image-size multi-stage build (Sprint 24 +872MB);
   2FA resend daily-cap from the durable table (Sprint 21); admin-budget for
   assignment-time generation (Sprint 22); repo-wide pre-existing gofmt drift
   (~20 files since Sprint 2); SMTP-config-change should clear the 2FA
   test-send marker.

## Sprint documents

Per-sprint detail: sprints/Sprint_17.md, Sprint_19.md, Sprint_20.md,
Sprint_21.md, Sprint_22.md, Sprint_23.md, Sprint_24.md (+ Sprint_24_Brief.md).
Designs: docs/sdd/SDD-019/021/022/023 + SDD-024-latex-math. TLS runbook:
docs/runbooks/tls-production.md.
