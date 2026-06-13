# Sprint 17 — Quick Wins: Generation-Log Readability + Production TLS (+ Sprint 24 Design Head-Start)

## Objective

PM items 2 and 9 from the June 2026 feature batch (plan:
`Sprint_17-23_Plan.md`): the course-generation log showed raw JSON SSE
envelopes to users, and self-hosted production deployments hit browser SSL
warnings with no path besides ACME. Per PM approval, Sprint 24's design task
(18.1, LaTeX math TDD) ran in parallel as a head start.

## Work performed

| Task | Deliverable | Contributor | Verifier | Outcome |
|---|---|---|---|---|
| 17.3 | 15 new reqs: REQ-FECOURSE-071..074/561..562 (log humanization), REQ-INFRA-003..008 + REQ-SYS-056..058 (TLS modes); fix round reworded REQ-INFRA-008 (ACME coverage), 072/562 (de-jargoned), 004 (half-pair case) | requirements-author (2 rounds) | SQE + schema validation | PASS |
| 17.1 | `humanizeGenerationEvent.ts` pure util mapping all 10 agent event types to natural sentences (1-based section display); snake_case→Title-Case fallback — raw JSON can never reach the screen; wired into `GeneratingView.vue` else-branch; 39 new vitest tests | junior-engineer | SQE + systems | PASS (round 1) |
| 17.2 | TLS modes in `internal/infra/tls.go` + `cmd/server/main.go`: BYO certs (`VALORY_TLS_CERT_FILE`/`KEY_FILE`), precedence BYO > ACME > self-signed, fail-fast on invalid/half pair, `VALORY_BEHIND_PROXY=true` plain-HTTP mode with full mutual exclusion, mode logging; pure `ResolveTLSMode` + 8 unit tests; `.env.example` | senior-engineer; lead folded proxy port :8080→:8443 (compose-mapping consistency) + `@verifies` annotation fix | SQE + systems (2 rounds) | PASS |
| 17.4 | `docs/runbooks/tls-production.md`: decision guide (ACME / BYO / reverse-proxy), Caddy+nginx examples, __Host-cookie HTTPS caveat, healthcheck guidance, troubleshooting + env-var tables; lead folded ECDSA-safe `openssl pkey` check | design-author | SQE + senior SQE | PASS |
| 18.1 | `docs/sdd/SDD-024-latex-math.md` (rev 5): render-time `$`-normalization (all 3 paths), STEM convention, KaTeX client pipeline, property-scoped style allowlist for the sanitizer, go:embed self-contained HTML export, asciidoctor-mathematical PDF path, prompt convention | design-author (3 rounds) + lead folds (revs 4–5 applying reviewer-prescribed fixes) | SQE + systems (3 rounds) + senior SQE | PASS |

## Review pipeline results

- **Round 1 (SQE + systems in parallel): FAIL.** Blockers: `@verifies`
  annotation key on TLS tests; REQ-INFRA-008 didn't authorize the
  ACME-exclusion behavior its own test verified; SDD regex `\\.{4,}` could
  never match `....` literal fences (would have silently failed AC 4); SDD
  removed `style` from FORBID_ATTR while incorrectly claiming the sanitizer
  hook compensates — would have re-opened the phishing-overlay XSS.
- **Round 2: SQE PASS; systems FAIL** — the rev-3 allowlist's `[a-zA-Z]+`
  branch passed `position:fixed` (reviewer proved it empirically in node),
  contradicting the document's own guarantee. Fixed via property-scoped
  (property, value) validation.
- **Round 3 (systems): FAIL** — rev 3's "KaTeX never emits inline position"
  evidence was false: `op.ts` emits `position:relative` for `\int`/inline
  `\sum`/`\prod`/`\oint` (live-verified against KaTeX 0.17.0); dropping it
  would regress operator spacing. Lead folded the reviewer's verbatim fix
  (rev 4: `position` admits exactly `relative`) and re-verified the final
  logic empirically: all attack declarations reject (`fixed`, `absolute`,
  `sticky`, `inherit`, `calc(`, `url(`, `z-index`), all KaTeX emissions pass.
- **Senior SQE: SHIP.** Full cross-artifact consistency confirmed (runbook ↔
  implementation ↔ reqs ↔ compose, zero stray :8080); lead folds judged
  appropriate. Gate findings (two stale rev-3 SDD lines — one load-bearing in
  the §13 implementation checklist — and a leftover self-correction
  narration) were closed by the lead post-gate as rev 5.

## Verification

- `go build ./...`, `go vet ./...`, `go test ./...` green (DB-gated
  integration tests skip cleanly without `TEST_DATABASE_URL` — unit-level
  pass; TLS logic is pure and fully covered); `gofmt -l` clean.
- vitest 415/415 (31 files) including 39 new humanizer/view tests.
- Requirement schema: all 15 in-scope reqs validate (pattern, ≤20 words,
  atomic); no dangling REQ references across the 11 touched files.

## Quarantined — NOT part of this sprint (PM attention)

An unattributed, complete `professor_max_tokens` feature sits in the working
tree (migration 015, `internal/admin/config_handler*`, admin `systemConfig`
stores, `internal/agent/professor.go` wiring, REQ-ADMIN-010, `.gitignore`,
a `.DS_Store` deletion). It matches the Sprint 24 brief's "June 2026
rendering investigation" (fixes 4096-token mid-word truncation) and appears
deliberate — likely the PM's own working changes. It was excluded from all
Sprint 17 reviews. **Caveat from the senior gate:** it is functionally
severable but structurally intermingled (line-level, not file-level) with
`cmd/server/main.go` and `requirements/l2-requirements.json`; and
REQ-ADMIN-010's description is non-atomic and >20 words, so it will not pass
review as-is when it is formally submitted.

## Backlog

- **Sprint 24 / task 18.2:** SDD-024 forward-references REQ-AGENT-015, which
  already exists — requirements-author must reconcile (reuse vs. renumber).
- Frontend test annotation convention is mixed (`@req` file-level vs
  `@verifies` per-test); standardize in a future cleanup (house-style).
- Runbook startup-log examples are marked illustrative; actual format is
  `tls: mode=...` (accepted; REQ-INFRA-007 requires content, not format).
- Operators using reverse-proxy mode must adjust the compose healthcheck
  scheme (documented in runbook Option C + compose comment).
