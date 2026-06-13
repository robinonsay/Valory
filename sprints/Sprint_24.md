# Sprint 24 — LaTeX Math in AsciiDoc Content, End-to-End

## Objective

Per `Sprint_24_Brief.md` (PM-issued): math written in LaTeX must typeset in
the section reader, HTML export, and PDF export — including legacy stored
content using `$...$`/`$$...$$` — with STEM as the going-forward convention.
Design contract: `docs/sdd/SDD-024-latex-math.md` (rev 5, approved in
Sprint 17's parallel head start).

## Work performed

| Task | Deliverable | Contributor | Verifier | Outcome |
|---|---|---|---|---|
| 18.2 | 15 reqs: REQ-CONTENT-010..012, REQ-AGENT-026 (REQ-AGENT-015 collision resolved — that ID is the chat-interface req), REQ-FECONTENT-030..038, REQ-SYS-059/060; first use of CLAUDE.md module-dir placement (legacy-exception note added to CLAUDE.md) | requirements-author | SQE + schema | PASS |
| 18.3 | `internal/content/normalize.go` (RE2 fence scanner — Go lacks backreferences; semantically equivalent to SDD regex), `katex_assets.go` (go:embed KaTeX + base64 font inlining), handler export changes (`-a stem=latexmath`; PDF `-r asciidoctor-mathematical -a mathematical-format=svg`), Dockerfile toolchain; 20 unit tests | senior-engineer; lead folds: staged 63 asset files (SQE blocker — go:embed breaks fresh clones), sync.Once CSS cache, header-comment accuracy | SQE + systems | PASS (round 2) |
| 18.4 | `renderAsciidoc.ts` (normalize → asciidoctor(safe:secure) → KaTeX(throwOnError:false) → DOMPurify LAST; KaTeX in lazy chunk) + `sanitizeHtml.ts` (MathML allowlist + §5.3 property-scoped style validation, position='relative' only); 50 vitest tests | senior-engineer | systems (XSS focus) + SQE | PASS (round 1) |
| 18.5 | Professor GenerateSection/RegenerateSection prompts: STEM instruction + worked example; reviewer.go correctly unchanged (its criteria are citation/coherence/soundness — no syntax validation; SQE concurred) | junior-engineer | SQE | PASS |
| 18.6 | `math_export_integration_test.go`: HTML export (KaTeX markup + zero external refs + `$HOME` survives) PASSES against live test DB; PDF export test gated to container env (binaries probe) | test-author | senior SQE | PASS |

## Review pipeline results

- **Systems round 1: PASS** — 12 adversarial probes held (mixed-case
  `POSITION:FIXED`, whitespace/CSS-comment tricks, custom properties,
  `url()`/`expression()`, katex-classed `<style>/<iframe>` (FORBID_TAGS wins),
  100K-char ReDoS probes at 0ms). Non-blocking: per-request CSS recompute
  (lead folded sync.Once), Dockerfile single-stage size, case-sensitive
  `relative` match (conservative; recorded).
- **SQE round 1: FAIL** — one blocker: 63 embedded KaTeX assets untracked
  (go:embed fails on fresh clone). Lead resolved by staging
  `internal/content/assets/` (commit remains the PM's call). Non-blocking
  folds: REQ-FECONTENT-038 traceability into REQ-SYS-059 (after un-doing an
  accidental whole-file reformat — final l1 diff is 40 pure insertions),
  CLAUDE.md dual-convention note.
- **Senior SQE: SHIP.** AC 1–7 walked through with evidence; deferral of the
  single budgeted live e2e run judged acceptable as a release-gate condition
  (not a merge blocker). Two false alarms chased and cleared (build-tag
  partial runs; legacy req-file array style).

## Verification

- `go build`/`go vet` clean; `go test -tags testing ./...` all green
  (canonical Makefile invocation); vitest 465/465; gofmt clean.
- Docker image builds; in-image probe rendered a stem document to `%PDF` via
  asciidoctor-mathematical. Image: 1.13GB vs 258MB baseline.
- Integration HTML export test passed against the live test DB.

## Release-gate condition (before tagging, not before merge)

Run the one budgeted live e2e math journey on a freshly rebuilt stack
(MEMORY: prod compose does not hot-reload): generate a STEM course → typeset
glyphs in reader → HTML export opens with networking disabled → PDF glyphs.

## Backlog / retro

- **Image size:** +872MB vs SDD's 150–250MB estimate (build deps in runtime
  stage). Follow-up: multi-stage gem build (systems' sizing: 300–500MB
  recoverable).
- **Severability correction (PM scoping accuracy):** the senior gate found
  the quarantined `professor_max_tokens` feature is NOT structurally
  severable — it is woven into professor.go/chair.go/client.go/runner.go and
  the NewProfessor signature. The `feature/new-features` branch bundles:
  Sprint 17 (TLS + log), Sprint 24 (math), and the unreviewed max-tokens
  feature. Release notes must reflect this; the max-tokens feature still
  needs a formal review pass (REQ-ADMIN-010 is also non-atomic/>20 words).
- Bare `go test ./...` (without `-tags testing`) silently skips the math unit
  tests — CONTRIBUTING/Makefile note recommended.
- Case-sensitive `position:relative` match is stricter than KaTeX needs
  (KaTeX emits lowercase) — accepted, conservative.
