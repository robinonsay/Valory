# Sprint 19 — Admin-Configurable Email Subsystem

## Objective

PM item 6: admin-configurable `from` address and SMTP server with support for
free/self-hosted relays (no paid email service required). Foundation for
Sprint 20 (welcome emails) and Sprint 21 (email 2FA). Design:
`docs/sdd/SDD-019-email-subsystem.md`.

## Work performed

| Task | Deliverable | Contributor | Verifier | Outcome |
|---|---|---|---|---|
| 19.1 | SDD-019: Mailer interface (Send/IsConfigured), config keys + managed-secret password, none/starttls/tls, AUTH-optional, admin>env precedence, read-per-send, test-send endpoint, Mailpit harness; lead patches: migration 014→016 (014/015 taken), Mailpit healthcheck curl→wget | design-author | SQE + systems | PASS |
| 19.2 | 18 reqs: REQ-EMAIL-001..011 (module dir), REQ-FEADMIN-600..604, REQ-SYS-061/062 (l1 pure-insertion) | requirements-author | SQE + schema | PASS |
| 19.3 | internal/email (3 dial paths, maybeAuth, RedactPassword, sanitizeHeader), migration 016, admin allowedKeys/validation + test-send (advisory-lock rate limit 5/60s, name-only audit, 200/400/422/429/502/503), user.mailerAdapter refactor, main.go wiring + startup WARN; 19 unit tests | senior-engineer (+1 fix round) | SQE + systems | PASS (round 2) |
| 19.4 | SystemConfigView Email section: 5 config fields, encryption dropdown, write-only masked password (Sprint-16 pattern), test-send UI, honest explanations; 473/473 vitest | junior-engineer | SQE + e2e-severability check | PASS |
| 19.5 | Mailpit in docker-compose.test.yml, internal/testutil/mailpit.go, 12 integration tests — ALL RAN LIVE against Mailpit+Postgres, all pass; found SDD healthcheck bug (curl absent in Alpine image) | test-author | senior SQE + live runs | PASS |

## Review pipeline results

- **Round 1: both gates FAIL.** SQE: REQ-EMAIL-007 unsatisfied —
  RequestPasswordReset propagated SMTP errors → HTTP 500, breaking
  anti-enumeration; gofmt on mailpit.go (lead-folded). Systems: **SMTP header
  injection** — from/to/subject interpolated into RFC 2822 DATA headers with
  no CRLF stripping; latent vector for Sprint 20/21 caller-supplied subjects.
- **Fix round (originating senior-engineer):** sanitizeHeader (strip \r\n,
  \r, \n from header values, never body) + swallow-and-log in
  RequestPasswordReset (recipient address kept out of the log line) + tests.
- **Delta re-reviews:** systems PASS (incl. Unicode line-separator
  adversarial assessment: SMTP is CRLF-only on the wire; bare-LF tolerance is
  the real risk and is covered). SQE delta raised one finding the lead
  disputed; **senior gate adjudicated: finding VOID** — the gofmt drift in
  internal/user/handler*.go exists at HEAD since the Sprint 2 commit
  (bb54d5a) and Sprint 19's working diff on those files is empty. (Audit
  honesty note: the lead's counter-claim that bb54d5a "does not exist" was
  itself wrong — it is a Sprint 2 commit; the substance stood.)
- **Senior SQE: SHIP.** Integration tier re-run LIVE at the gate (Mailpit +
  Postgres healthy, every package ok). Playwright severability confirmed:
  admin-config.spec.ts has no total-count assertion — the email section
  ships without breaking it.

## Verification

- go build/vet clean; `go test -tags testing ./...` all green; vitest
  473/473; gofmt clean on every sprint-touched file.
- Integration: 12/12 live against real SMTP sink + real Postgres (no mocks),
  incl. delivery assertions via Mailpit REST API, audit-payload
  password-absence probe, rate-limit 429 on 6th call, admin-config-wins
  precedence end-to-end. Migration 016 applies from scratch.

## Backlog

- **(Major, standing)** Quarantine bleed: the working tree commingles
  Sprint 17/19/24 with the unreviewed max-tokens feature (incl. its
  admin-config.spec.ts 14-field change, which will fail live e2e if deployed
  without that feature). Lead must stage paths explicitly at commit time.
- `.env.example` lacks SMTP_* documentation (incl. new SMTP_USERNAME) — doc
  follow-up.
- Repo-wide pre-existing gofmt drift (~20 files, since Sprint 2) — dedicated
  housekeeping pass.
- Stale IsConfigured doc comment (mentions context it doesn't take) —
  trivial.
- e2e: admin-config.spec.ts email-section coverage when the live e2e tier
  next runs.
