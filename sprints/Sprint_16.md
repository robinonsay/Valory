# Sprint 16 — Admin-Managed API Keys + Plain-Language Config Explanations

## Objective

PM request: ANTHROPIC_API_KEY and the Brave key configurable from the admin
page (user-friendliness) and an explanation for every config item so a
non-expert can configure the system. This deliberately amends the
"secrets from env only" convention — with security rails set by the lead.

## Work performed

| Task | Deliverable | Contributor | Verifier | Outcome |
|---|---|---|---|---|
| 16.1 | docs/sdd/18(+18a/18b): AES-256-GCM with env KEK (VALORY_SECRET_KEY, base64-32; absent → disabled+WARN, env fallback, never crash); managed_secrets table; SecretProvider (30s TTL, Invalidate-on-write); per-call option.WithAPIKey for the SDK; write-only masked API; CLAUDE.md amendment text | design-author | SQE + systems | PASS |
| Reqs | REQ-ADMIN-005..008, REQ-SECURITY-006..008, REQ-FEADMIN-500..504/510..516 (18 reqs) | requirements-author | SQE + schema | PASS (510 reworded by lead) |
| 16.2 | migration 013; internal/admin/secrets.go + secrets_handler.go; ThrottledClient per-call key resolution; Professor per-call brave resolution; ANTHROPIC_API_KEY now optional+WARN; audit name-only events + redaction; CLAUDE.md amendment (lead-applied); .env/.env.example | senior-engineer (×2 stalls — wiring was complete; lead verified) + dedicated test-author round (39 backend tests, the stalled agents had written none) | SQE + systems | PASS |
| 16.3 | Admin "API Keys" section (status badges Configured ····last4 / env / none; always-blank password inputs; Save/Clear+confirm; inline errors) + EXPLANATIONS for all 15 keys incl. honest inert-key disclosures | junior-engineer | SQE + e2e | PASS |
| 16.4 | e2e admin-api-key.spec.ts: section renders, brave save round-trip with NEVER-echoed assertion on page + API body, live audit-redaction probe, explanation honesty checks (anthropic key never touched) | test-author | senior SQE + live runs | PASS |

## Review pipeline results

- **SQE: PASS and systems: PASS — the program's first first-round double
  pass.** Non-blocking items folded in by lead: RLS hygiene on
  managed_secrets (with an honesty-corrected comment after the senior gate
  assessed the permissive policy as net-zero defense — protection is the
  GRANT model), dead import removed (a botched lead sed briefly broke 13
  tests — caught and fixed within the round), REQ-FEADMIN-510 rewording.
- **Systems highlights**: AuthZ probes (student 403 / unauth 401 / no-CSRF
  403); hot-reload probed live (set→status flips→clear→env fallback);
  **SSE leak analysis: Anthropic 401 error strings contain no key
  material**, so a bad managed key cannot leak through pipeline events;
  .env confirmed gitignored/untracked.
- **Senior SQE: SHIP**, with the RLS-comment honesty assessment on record
  and explanation copy spot-checked against code truth (4/4 honest).

## Verification

- Go matrix + make test-integration green (013 incl. RLS appendix applies
  from scratch; FORCE RLS does not break valory_app); 39 new backend tests;
  vitest 361/361; build green; AI-free e2e 41/41 (multiple runs, zero AI
  tokens — anthropic_api_key untouched throughout).
- Live: brave key set/cleared round-trip with full state restoration; audit
  entries carry name-only payloads.

## Backlog

- docs/guides/admin-configuration.md docs pass (secrets section; in-UI
  explanations are authoritative meanwhile).
- REQ-SECURITY-006 names AES-256-GCM (accepted precedent: security reqs may
  name observable contracts).
- Benign ms-scale cache-recache window on Invalidate (documented).
- Operator note: VALORY_SECRET_KEY loss makes managed ciphertexts
  undecryptable → system falls back to env keys (by design).
