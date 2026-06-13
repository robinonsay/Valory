# Sprint 21 — Email-Based Two-Factor Authentication

## Objective

PM item 7: email 2FA for students and admins. Per resolved decision 3, a
GLOBAL admin toggle whose enable is blocked until email is configured AND a
test-send has succeeded. Completes the email-dependent chain (19 mailer → 20
welcome emails → 21 OTP). Design: docs/sdd/SDD-021-email-2fa.md.

## Work performed

| Task | Deliverable | Contributor | Verifier | Outcome |
|---|---|---|---|---|
| 21.1 | SDD-021: separate pending_2fa table (pending token is NOT a session), SHA-256 OTP hash + attempt-cap/TTL/single-use controls, global toggle with email+test-send prerequisites, no-email-admin block, admin reset + VALORY_2FA_BREAK_GLASS recovery, audit actor rules (lead note: migration 010 removed the audit FK, so user-triggered events use the subject's own id) | design-author | SQE + systems | PASS |
| 21.2 | 17 reqs: REQ-AUTH-015..026, REQ-FEAUTH-200..202, REQ-FEADMIN-700/701, REQ-SYS-065/066 | requirements-author | SQE + schema | PASS |
| 21.3 | migration 018 (pending_2fa, otp_rate_limits, seeds); internal/auth/twofactor.go (crypto/rand 6-digit, two-phase Login, VerifyOTP with pg_advisory_xact_lock, ResendOTP throttle, ResetUserTwoFactor, break-glass); pre-session verify/resend endpoints; admin toggle prerequisites + 2FA-reset; audit otp/otp_hash redaction | senior-engineer (+1 fix round) | systems (auth focus) + SQE | PASS (round 2) |
| 21.4 | OtpVerifyView, auth store pending state (isAuthenticated stays false), router guard, LoginView 202 handling, SystemConfigView toggle disabled-with-reason; 516 vitest | junior-engineer (+1 fix round) | SQE + e2e | PASS (round 2) |
| 21.5 | 16 integration journeys (auth + admin) ran LIVE green against Postgres+Mailpit; two-factor-login.spec.ts (skip-gated) | test-author | senior SQE + live runs | PASS |

## Review pipeline results

- **Systems gate: PASS.** Ran a LIVE 20-way concurrent-verify TOCTOU probe:
  the `pg_advisory_xact_lock` + double-checked re-read held — the OTP
  attempt-cap (the load-bearing brute-force control) is NOT bypassable under
  concurrency. Confirmed pending token can't authenticate (separate table),
  break-glass requires password + admin + is audited, OTP never
  logged/echoed/stored-plaintext.
- **SQE gate: FAIL** — 2 BLOCKING: (1) OtpVerifyView `body` const scoped
  inside the 429 block but referenced outside it (TS2304; daily-cap error
  never displayed); (2) REQ-FEADMIN-701 test (toggle-disabled-when-prereq-
  unmet) missing.
- **Fix round:** frontend — body hoist (typecheck clean) + REQ-FEADMIN-701
  test + unused-binding cleanup. Backend (lead folded two systems
  non-blocking items, one security-relevant): removed
  email_test_send_verified_at from allowedKeys so an admin can no longer
  PATCH-forge the test-send prerequisite (testEmailSend still writes it via
  its direct audit-tx path; PATCH now 400); OTP-send-failure now returns 503
  (new ErrOTPSendFailed sentinel) instead of an opaque 500.
- **SQE delta: PASS. Senior SQE: SHIP** — re-ran full verification + live
  integration tier; security posture sound; fix folds appropriate; 2FA-OFF
  login byte-for-byte unchanged; quarantine severability intact.

## Verification

- go build/vet clean; `go test -tags testing ./...` green; vitest 516/516;
  vue-tsc clean; gofmt clean on all 13 sprint-touched Go files; 17 new reqs
  schema-valid with full traceability.
- Integration tier ran LIVE (Postgres + Mailpit): happy-path OTP extracted
  from the Mailpit email and verified → session; 5-wrong-code lockout; admin
  reset; resend 60s throttle (old OTP dead); break-glass (admin-only,
  password still required, audited); toggle prerequisites (email unconfigured
  / no test-send marker / admin-without-email all rejected 422); 2FA-off
  normal session; audit-contains-no-OTP.

## Backlog

- **(Major-adjacent)** Resend daily-cap (10/24h) is read from the per-pending
  row, which resets to 0 when the row is recreated on a new login — so a user
  with the correct password can exceed 10 OTP emails/day (inbox amplification;
  does NOT weaken brute-force — the 60s throttle and 5-attempt cap are intact
  per the systems TOCTOU probe). Fix: make the cap authoritative from the
  durable otp_rate_limits table (already exists for this).
- **(Minor)** No background GC of expired pending_2fa rows (expiry enforced on
  read; zero security impact, minor bloat).
- **(Minor)** Changing SMTP config does not invalidate email_test_send_verified_at
  (mitigated by the live IsConfigured() check at toggle time). Fix: clear the
  marker on any smtp_* key change.
- **(Trivial)** OTPVerifyResult struct in twofactor.go is dead code with a
  misleading comment — remove on a future touch.
- **(Standing)** Quarantine: professor_max_tokens untouched; stage Sprint
  paths explicitly at commit.

## Operator note

VALORY_2FA_BREAK_GLASS is presence-triggered (any non-empty value activates
the admin-only, password-still-required bypass) and logs a startup WARN +
audits each use. Documented accepted tradeoff (operator with env access is
already trusted).
