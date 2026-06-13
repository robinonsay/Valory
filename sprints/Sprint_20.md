# Sprint 20 — Admin-Created Account Lifecycle

## Objective

PM items 4 + 5: students get admin-created accounts and must change their
password at first sign-in; on creation they receive an email with credentials
and reset instructions. Per resolved decision 2, the welcome email carries a
SYSTEM-GENERATED TEMPORARY password valid only until the forced first-login
change — no durable credential travels by email. Depends on Sprint 19 mailer.

## Work performed

| Task | Deliverable | Contributor | Verifier | Outcome |
|---|---|---|---|---|
| 20.1 | 26 reqs: REQ-USER-008..019, REQ-AUTH-013/014, REQ-FEUSER-001..005, REQ-FEADMIN-610..614, REQ-SYS-063/064 | requirements-author | SQE + schema | PASS |
| 20.2 | migration 017 (users.must_change_password); createUser generate_password XOR password; crypto/rand 16-char ~93-bit temp password; welcome email via Sprint-19 Mailer (fail-soft); resend (regenerate + invalidate old); authenticated change-password (verify current → clear flag → delete other sessions, keep current); MustChangePasswordMiddleware; login + session responses carry the flag; audit name-only; 20 unit + 3 integration journeys | senior-engineer (+1 fix round) | SQE + systems | PASS (round 2) |
| 20.3 | ChangePasswordView, passwordValidation.ts, auth store flag + global 403 handler, router guard, LoginView redirect, UserManagementView (generate toggle, one-time temp display + copy + never-when-sent, resend confirm); 497 vitest | junior-engineer | SQE + e2e | PASS |
| 20.4 | 3 lifecycle integration journeys (Mailpit + Postgres, all ran LIVE green) + account-lifecycle.spec.ts Playwright journey | test-author | senior SQE + live runs | PASS |

## Review pipeline results

- **Round 1: systems PASS** (3 non-blocking: middleware suffix-match latent
  hazard, no resend rate-limit, resend audit non-transactional). **SQE FAIL**
  — 3 BLOCKING: (1) resend-welcome URL mismatch — production mounts user
  admin routes at /api/v1/users, but frontend AND the integration-test router
  used /api/v1/admin/users, so the test masked a real 404; (2) e2e step-8
  left confirm-password empty → disabled submit → no-op; (3) e2e used a
  data-testid the component never rendered. Plus a NON-BLOCKING adjudication:
  client password policy is stricter than the server floor (UX hardening,
  server authoritative) — comment-only.
- **Fix round (lead folds):** frontend + integration-test router + request
  paths + vitest assertion + handler doc-comments all aligned to the
  production /api/v1/users path; e2e confirm-fill + data-testid + reload-based
  not-shown-again assertion; middleware tightened from HasSuffix to EXACT
  path equality (systems' non-blocking prescription); password-policy comment
  + REQ-FEUSER-004 rationale correction. Lead RE-RAN the live integration
  tier: 3/3 journeys + all 4 packages green against the corrected routing.
- **SQE delta: PASS.**
- **Senior SQE: SHIP** — but surfaced one MAJOR finding both prior gates
  missed: **GET /auth/session omitted must_change_password**, so on boot/reload
  the frontend guard flag read a stale false and REQ-FEUSER-002's proactive
  interposition degraded to a reactive bounce off the backend 403 (security
  invariant held; UX path did not).
- **Finding-1 closed now (lead, not deferred):** added the field to
  sessionResponse + GetSession's existing users-table read; the store already
  consumed it on restore, so the loop closed; added a frontend restore test
  and a live-verified backend test; broadened REQ-AUTH-013 to cover both the
  login and session-restore responses; fixed the stale main.go comment.
  **Senior SQE delta: PASS — Finding 1 fully closed, no new issues.**

## Verification

- go build/vet clean; `go test -tags testing ./...` all green; vitest
  497/497; gofmt clean on all sprint-touched files.
- Integration tier ran LIVE (Postgres + Mailpit): full lifecycle journey
  (create→email→login→403→change→flag-cleared→other-sessions-dead→re-login,
  old temp dead), resend regeneration, unconfigured-mailer temp-in-response;
  plus the new GetSession flag test. All pass.
- Security: temp password is crypto/rand ~93 bits, never logged/stored/echoed
  except the one-time email_sent:false response; forced-change enforced by
  exact-path middleware (3 exempt paths only); change-password keeps the
  current session and kills others atomically.

## Backlog

- **(Backlog, systems)** No rate limit on admin resend-welcome / createUser
  (admin-only behind auth+CSRF; acceptable).
- **(Backlog, systems)** ResendWelcome rotates the credential before the
  audit transaction (best-effort audit, consistent with existing
  ModifyUser/DeactivateUser house pattern).
- **(Minor)** auth store refreshSession duplicates restoreSession's
  populate/clear block (both correct today; latent drift risk) — cleanup
  candidate.
- **(Minor)** ChangePasswordView.test.ts uses file-level @{"req"} rather than
  per-test @{"verifies"} (coverage present; convention only).
- **(Standing)** Quarantine: professor_max_tokens feature remains unreviewed
  in-tree; Sprint 20 does not entangle with it. Lead must stage paths
  explicitly at commit time.
