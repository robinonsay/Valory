# Sprints 17–24 Plan — PM Feature Batch (June 2026)

Status: **APPROVED by PM (2026-06-12)** — all five decision points resolved
(see "Resolved decisions"). Sprint 18 (math) is superseded by the
PM-issued **Sprint 24 brief** (`Sprint_24_Brief.md`), which expands the same
scope with June 2026 rendering-investigation findings; the slot number 18 is
retired to avoid task-ID collisions (the brief preserves 18.x task IDs).

## PM request (9 items)

1. AsciiDoc math written in LaTeX, rendering in HTML and PDF — both in-app and on export
2. Course generation log shows raw JSON events — should read as natural text
3. Admins can create courses and assign them to students
4. Users can change their password after first sign-in (accounts are admin-created)
5. On account creation, student receives an email with credentials + reset instructions
6. Admin-configurable `from` address and email server; must support free/self-hosted SMTP
7. Email-based 2FA for students and admins
8. Persistent per-student learning style used across courses (onboarding LLM interaction)
9. Production fix for the SSL warning when self-hosting

## Resolved decisions (PM, 2026-06-12)

1. **S17:** plain-HTTP mode for TLS-terminating reverse proxies is **in scope**, gated behind an explicit `VALORY_BEHIND_PROXY=true`.
2. **S19 (accounts):** welcome email carries a **system-generated temporary password** valid only until the forced first-login change. No durable credential travels by email.
3. **S20 (2FA):** **global admin toggle**; enable is blocked until email is configured and a test-send has succeeded.
4. **S21 (admin courses):** **per-student course-instance model** confirmed — admin defines an assignment (topic, level, parameters); the system generates one personalized instance per assigned student.
5. **Ordering:** as proposed below. Math runs as **Sprint 24** per the PM brief (independent — may run before or after Sprint 17).

## Current-state audit (what already exists)

| Item | Existing groundwork | Gap |
|---|---|---|
| 1 | Three render paths: client-side `@asciidoctor/core` + sanitizer (`frontend/src/utils/renderAsciidoc.ts`), server `asciidoctor` HTML export, `asciidoctor-pdf` export (`internal/content/handler.go:205-276`); chat already typesets `$...$` via KaTeX (`renderMarkdown.ts` — reference implementation) | No stem/latexmath support in any course-content path; legacy stored content uses `$...$`; prompts never instruct LaTeX; PDF toolchain lacks `asciidoctor-mathematical`. Full detail: `Sprint_24_Brief.md` |
| 2 | `GeneratingView.vue` handles `progress`/`status_change`; SSE pipeline emits typed agent events | Unknown event types fall through to `messages.push(rawJSON)` (`GeneratingView.vue:37-39`) |
| 3 | `CourseOversightView` (read-only); courses are strictly student-owned (`courses.student_id NOT NULL` + RLS, migration 003) | No admin create/assign model at all — needs design |
| 4 | Token-based email reset flow exists (REQ-USER-005, `PasswordResetView.vue`, `requestPasswordReset`/`confirmPasswordReset`) | No forced change at first login; no authenticated change-password endpoint |
| 5 | `createUser` admin endpoint exists (admin types the password) | No welcome email; no temp-password generation; no first-login flag |
| 6 | `user.SMTPTransport` — env-only (`SMTP_HOST/PORT/FROM/PASSWORD`), STARTTLS + AUTH **mandatory** in code | Fails on no-auth localhost relays (postfix); not admin-configurable; no test-send |
| 7 | — | Everything: OTP storage, two-phase login, frontend, rate limits |
| 8 | Per-course intake chat exists (migration 011, `IntakeChatView`) | No persistent cross-course learning profile; nothing injected into professor prompts |
| 9 | `ACME_DOMAIN` → autocert (Let's Encrypt) in `internal/infra/tls.go`; fallback is self-signed → browser warning | No BYO-cert option; no reverse-proxy mode; no operator runbook |

## Dependency graph and execution order

```
S17 (quick wins: 2, 9)        — first
S24 (LaTeX math: 1)           — second; independent of everything (brief is READY FOR EXECUTION)
S19 (email subsystem: 6)      — foundation for the account/2FA chain
   └── S20 (account lifecycle: 4+5)   — needs mailer
          └── S21 (email 2FA: 7)      — needs mailer; sequenced after S20 (both touch login flow)
S22 (admin courses: 3)        — independent; design-heavy
S23 (learning profile: 8)     — after S20 (first-login UX: password change → onboarding)
```

Planned execution order: **17 → 24 → 19 → 20 → 21 → 22 → 23.**
(Sprint slot 18 retired — superseded by Sprint 24.)

---

## Sprint 17 — Quick wins: generation log readability (2) + production TLS (9)

Two small independent tracks, run in parallel.

| Task | Deliverable | Contributor | Verifier |
|---|---|---|---|
| 17.1 | `GeneratingView.vue`: map every known agent `event_type` + payload to a natural-language line ("Generating section 1: Course Description…"); graceful prettifier fallback so raw JSON can never reach the screen; vitest coverage | junior-engineer | SQE + systems |
| 17.2 | TLS deployment modes: (a) BYO certs via `VALORY_TLS_CERT_FILE`/`VALORY_TLS_KEY_FILE` (precedence: BYO > ACME > self-signed dev fallback; fail fast with a clear error on an unreadable/invalid pair); (b) plain-HTTP reverse-proxy mode gated behind explicit `VALORY_BEHIND_PROXY=true` (per resolved decision 1); startup log states which mode is active | senior-engineer | systems (security focus) + SQE |
| 17.3 | Reqs for both tracks | requirements-author | SQE + schema validation |
| 17.4 | `docs/runbooks/tls-production.md`: ACME setup, BYO certs, reverse-proxy guidance | design-author | senior SQE |

## Sprint 24 — LaTeX math in AsciiDoc content, end-to-end (1)

**Authoritative source: `Sprint_24_Brief.md` (PM-issued, READY FOR EXECUTION).**
Supersedes the originally planned Sprint 18. Key expansions over the old plan:

- **Legacy content must render (binding):** existing stored sections use
  `$...$`/`$$...$$`. A compatibility layer handles dollar-delimited math in
  **all three** render paths; render-time normalization is the default unless
  the TDD justifies otherwise. Going-forward convention is proper STEM
  (`:stem: latexmath`, `stem:[...]`, `[stem]` blocks).
- **Reuse the chat pipeline pattern** (`renderMarkdown.ts`): placeholder
  extraction skipping code regions → KaTeX `throwOnError:false` → DOMPurify
  with a KaTeX-aware allowlist. KaTeX only — no MathJax.
- **No CDN; HTML export must be self-contained** (offline deployments).
- **Sanitize after typesetting** — sanitizer allowlist extension mirrors
  `renderMarkdown.ts` decisions; `safe:'secure'` and no-`include::` stay.

| Task | Deliverable | Contributor | Verifier |
|---|---|---|---|
| 18.1 | Mini-TDD: stem convention; legacy `$`-delimiter strategy with justified decision; client KaTeX pipeline; sanitizer allowlist delta (enumerated); self-contained HTML export approach; PDF via `asciidoctor-mathematical` | design-author | SQE + systems |
| 18.2 | Requirement JSON files (content + agent + frontend modules) per the TDD | requirements-author | SQE + schema validation |
| 18.3 | Backend: export endpoints pass stem attributes + legacy normalization; Dockerfile adds `asciidoctor-mathematical`; `:stem: latexmath` header injection for existing documents | senior-engineer | SQE + systems |
| 18.4 | Frontend: `renderAsciidoc.ts` stem + legacy-dollar support + KaTeX render pass; `sanitizeHtml.ts` allowlist extension with updated boundary comments | senior-engineer | systems (XSS focus) + SQE |
| 18.5 | Agent prompts: professor/reviewer write math as latexmath stem (inline + block), with a worked example in the prompt | junior-engineer | SQE |
| 18.6 | Tests: stem + legacy-dollar fixtures (incl. `$HOME`-in-code-block negative case); XSS probes through the math path; integration: HTML export typeset with no external asset refs, PDF math glyphs; e2e math journey | test-author | senior SQE + live runs |

Acceptance criteria, known costs (Docker image weight from
`asciidoctor-mathematical`; KaTeX assets stay in the lazy AsciiDoc chunk), and
execution verification notes (rebuild stack before e2e; AI-free suite + one
budgeted live journey) are as written in the brief.

## Sprint 19 — Email subsystem: admin-configurable SMTP (6)

Foundation for Sprints 20–21.

| Task | Deliverable | Contributor | Verifier |
|---|---|---|---|
| 19.1 | Mini-TDD: `internal/email` Mailer interface; config keys (`smtp_host`, `smtp_port`, `smtp_from`, `smtp_username`, `smtp_encryption: none\|starttls\|tls`, auth **optional**); password stored via Sprint-16 managed-secrets (AES-256-GCM); env-var fallback preserved per CLAUDE.md convention; admin send-test-email endpoint (rate-limited, audited name-only) | design-author | SQE + systems |
| 19.2 | Reqs | requirements-author | SQE + schema |
| 19.3 | Backend: Mailer implementation supporting no-auth/no-TLS loopback relays (free postfix) through authenticated STARTTLS/TLS providers; refactor `user.SMTPTransport` call sites onto it; config + secret plumbing; test-send endpoint | senior-engineer | SQE + systems |
| 19.4 | Frontend: SystemConfigView "Email" section with plain-language explanations (Sprint-16 pattern) + "Send test email" button | junior-engineer | SQE + e2e |
| 19.5 | Tests: unit + integration against a Mailpit container added to `docker-compose.test.yml` (real SMTP sink, no mocks — house rule); e2e config round-trip with never-echoed password assertion | test-author | senior SQE + live runs |

**"Free email" support:** no-auth loopback relay (host postfix/exim) is the zero-cost path; the docs guide (19.1 output) also covers free-tier app-password providers.

## Sprint 20 — Admin-created account lifecycle (4 + 5)

Depends on Sprint 19 mailer. Approach per resolved decision 2.

| Task | Deliverable | Contributor | Verifier |
|---|---|---|---|
| 20.1 | Reqs | requirements-author | SQE + schema |
| 20.2 | Backend: migration `users.must_change_password` (backfill `false` for existing accounts); `createUser` gains generate-temp-password option + sets flag; welcome email (username + temp password + login URL + "you will be asked to change it"); login response surfaces the flag; authenticated change-password endpoint (requires current password; invalidates other sessions); audit events | senior-engineer | SQE + systems |
| 20.3 | Frontend: forced change-password screen interposed after login when flagged (blocks app navigation); change-password in account settings; UserManagementView shows email-sent status + manual resend | junior-engineer | SQE + e2e |
| 20.4 | e2e: admin creates account → Mailpit captures welcome email → first login → forced change → re-login with new password; resend path | test-author | senior SQE + live runs |

## Sprint 21 — Email 2FA (7)

Depends on Sprint 19; sequenced after Sprint 20 (both rework the login flow). Global toggle per resolved decision 3.

| Task | Deliverable | Contributor | Verifier |
|---|---|---|---|
| 21.1 | Mini-TDD: two-phase login (password OK → short-lived pending-2FA state, **not** a full session → OTP verify → session issued); 6-digit OTP hashed at rest, ~10 min TTL, single-use, attempt cap + resend throttle; global admin toggle (enable blocked until email is configured + test-send has succeeded); lockout recovery: admin per-user reset + `VALORY_2FA_BREAK_GLASS` env for the locked-out-sole-admin case; audit events | design-author | SQE + systems |
| 21.2 | Reqs | requirements-author | SQE + schema |
| 21.3 | Backend: migration (OTP table + rate-limit columns), pending-2FA auth state, issue/verify/resend endpoints, toggle in admin config | senior-engineer | systems (auth focus) + SQE |
| 21.4 | Frontend: OTP entry screen (resend with countdown, attempt errors); admin toggle UI with explanation | junior-engineer | SQE + e2e |
| 21.5 | Tests: unit (expiry/single-use/rate caps), integration with Mailpit, e2e full 2FA login incl. wrong-code and resend paths | test-author | senior SQE + live runs |

## Sprint 22 — Admin course creation & assignment (3)

Per-student instance model confirmed (resolved decision 4).

| Task | Deliverable | Contributor | Verifier |
|---|---|---|---|
| 22.1 | TDD: admin defines a course assignment (topic, level, parameters) and assigns students; system generates a per-student course instance (content is personalized and `agent_token_usage` is per student+course); RLS extension for admin-created rows; generation fan-out (sequential queue to respect token budgets); assigned-course skips student intake chat (admin parameters + learning profile when S23 lands) | design-author | SQE + systems |
| 22.2 | Reqs | requirements-author | SQE + schema |
| 22.3 | Backend: migration (assignments table, `courses.assignment_id` nullable FK), admin endpoints (create assignment, assign/unassign students, status), RLS updates, generation fan-out | senior-engineer | systems + SQE |
| 22.4 | Frontend: admin "Courses" view — create assignment, pick students, per-student generation status; student side: assigned course appears in CourseHub | junior-engineer | SQE + e2e |
| 22.5 | Tests: unit + integration (RLS probes: student A cannot see student B's instance; admin sees all), e2e assign→generate→student-sees-course | test-author | senior SQE + live runs |

## Sprint 23 — Persistent learning profile + onboarding (8)

Sequenced after Sprint 20 so the first-login UX composes: password change → onboarding → app.

| Task | Deliverable | Contributor | Verifier |
|---|---|---|---|
| 23.1 | Mini-TDD: `learning_profiles` table (student FK, profile text/JSON, source, updated_at); onboarding LLM chat (3–5 questions, modeled on intake chat; model summarizes answers into the profile); profile injected into professor/intake/reviewer prompts for **all** courses; student can view/edit and re-run onboarding from settings | design-author | SQE + systems |
| 23.2 | Reqs | requirements-author | SQE + schema |
| 23.3 | Backend: migration, onboarding chat endpoints (reuse agent chat plumbing), profile repository, prompt injection in `professor.go` and intake | senior-engineer | SQE + systems |
| 23.4 | Frontend: onboarding chat view on first login (after forced password change), profile section in settings, skip allowed (profile optional — prompts degrade gracefully) | junior-engineer | SQE + e2e |
| 23.5 | Tests: unit, integration (profile reaches prompt assembly), e2e onboarding journey; one budgeted live AI run for chat quality | test-author | senior SQE + live runs |

---

## Review pipeline (applies to every sprint)

Contributor → SQE + systems-engineer in parallel → fail returns to contributor with feedback → senior SQE final gate → deliver. Sprint summary written to `sprints/Sprint_NN.md` on completion. Nothing ships without the senior SQE gate.

## Standing assumptions

- Existing accounts are not disrupted: `must_change_password` backfills false; 2FA defaults off until an admin enables it.
- Email-dependent features fail soft: if no mailer is configured, account creation still works (admin shown a "no email sent — email not configured" notice).
- All new secrets (SMTP password) ride the Sprint-16 managed-secrets subsystem; env fallback preserved; never echoed in any response, log, or audit payload.
- No CDN dependencies anywhere (self-hosters may be air-gapped): KaTeX assets are bundled/embedded.
- Migrations are additive only; RLS changes get explicit probe tests.
- Execution hygiene (from memory + the S24 brief): rebuild stack images before any e2e run (no hot-reload in prod compose); Go toolchain at `/usr/local/go/bin` for non-interactive shells; prefer AI-free e2e + budgeted single live journeys.
