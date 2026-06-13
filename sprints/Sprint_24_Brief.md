# Sprint 24 Brief — LaTeX Math in AsciiDoc Content, End-to-End

Status: READY FOR EXECUTION
Source: PM feature batch item 1 (see `Sprint_17-23_Plan.md`); expanded with
findings from a June 2026 rendering investigation.
Sequencing: independent of all other planned sprints — may run before or
after Sprint 17.

## Objective

Course content containing LaTeX math must render as typeset math everywhere
content is displayed or exported: the in-app section reader, the HTML
export, and the PDF export. Today it renders as raw source text.

## Problem statement (observed bug)

Generated course sections contain math written with Markdown-style dollar
delimiters, e.g.:

```
State the standard decomposition template:
$\dfrac{P(x)}{(x+a)(x+b)} = \dfrac{A}{x+a} + \dfrac{B}{x+b}$.
```

In the HTML view this appears verbatim — dollar signs, backslashes and all.
Root cause is twofold:

1. **`$...$` is not AsciiDoc syntax.** Asciidoctor passes it through as
   plain text. AsciiDoc's native math mechanism is STEM: a
   `:stem: latexmath` header attribute plus `stem:[...]` inline macros and
   `[stem]` delimited blocks. The professor agent's prompts never instruct
   either form, so the LLM falls back to the Markdown habit.
2. **No render path typesets math even when STEM syntax is used.**
   Asciidoctor does not typeset math itself — it only wraps stem content in
   `\(...\)` / `\[...]` delimiters and expects a typesetting library on the
   page. None of our three render paths provides one.

## Current state (verified against the code)

| Path | Location | Gap |
|---|---|---|
| In-app section reader | `frontend/src/utils/renderAsciidoc.ts` — client-side `@asciidoctor/core` (`standalone:false`, `safe:'secure'`), output piped through `frontend/src/utils/sanitizeHtml.ts` | No stem attribute, no math renderer. The sanitizer **explicitly forbids** the `style` attribute and MathML tags on the documented assumption "section content has no KaTeX" — those comments and the allowlist must change together. |
| HTML export | server-side `asciidoctor` invocation, `internal/content/handler.go:205-276` | No stem attributes passed; no embedded math assets. |
| PDF export | server-side `asciidoctor-pdf`, same handler | Toolchain lacks `asciidoctor-mathematical`; math would be ignored even with stem syntax. |
| Chat (reference implementation, working) | `frontend/src/utils/renderMarkdown.ts` | Already renders `$...$` / `$$...$$` via KaTeX (`katex` ^0.17.0 is in `frontend/package.json`). Pipeline: extract math spans into opaque placeholders (skipping code regions) → markdown pass → `katex.renderToString` with `throwOnError:false` → DOMPurify with a KaTeX-aware allowlist. **Reuse this pattern.** |

## Key design constraints (binding)

1. **Legacy content must render.** Existing stored sections contain
   `$...$` / `$$...$$` math. Adding stem support alone does not fix them.
   The design must handle dollar-delimited math in already-stored content —
   recommended approach: a normalization step (dollar spans → stem
   equivalents, or placeholder extraction à la `renderMarkdown.ts`) applied
   consistently in **all three** render paths, not just the client. The
   design-author decides between render-time normalization and a one-time
   content migration; render-time is the safer default (no destructive
   rewrite of student content), but the choice must be justified in the TDD.
2. **Going-forward convention is proper AsciiDoc STEM.** Documents carry
   `:stem: latexmath`; agents write `stem:[...]` inline and `[stem]` blocks.
   Dollar-delimiter handling is a compatibility layer, not the convention.
3. **No CDN.** Self-hosted deployments may be fully offline. KaTeX assets
   (JS/CSS/fonts) ship in the frontend bundle; the HTML export must be
   self-contained (math pre-rendered or assets inlined).
4. **Sanitize after typesetting, never before.** The DOMPurify pass remains
   the final stage in the client pipeline. Extend the section-content
   sanitizer allowlist to exactly what KaTeX emits (spans with KaTeX
   classes, MathML elements, aria attributes) — mirror the decisions
   already made in `renderMarkdown.ts`'s purify config rather than
   inventing a new policy. This is the security hot-spot of the sprint.
5. **Per CLAUDE.md:** comments explain the why; no speculative
   abstractions; requirements as JSON validating against
   `schemas/requirements.schema.json`, living beside the modules they
   govern (expected modules: content, agent, frontend-course/content).
6. `safe:'secure'` and the no-`include::` posture in `renderAsciidoc.ts`
   are security boundaries — do not weaken them to enable math.

## Out of scope

- Math input by students (chat already handles its own rendering).
- Re-generating or editing existing course content via LLM.
- Markdown course content (courses are AsciiDoc-only).
- MathJax (KaTeX is already a dependency; do not add a second typesetter).

## Tasks

| Task | Deliverable | Contributor | Verifier |
|---|---|---|---|
| 18.1 | Mini-TDD: stem convention; legacy `$`-delimiter strategy (constraint 1) with explicit decision + justification; client KaTeX pipeline; sanitizer allowlist delta (enumerated tags/attrs/classes); self-contained HTML export approach; PDF via `asciidoctor-mathematical` in the Docker image | design-author | SQE + systems |
| 18.2 | Requirement JSON files (content + agent + frontend modules) per the TDD | requirements-author | SQE + schema validation |
| 18.3 | Backend: export endpoints pass stem attributes + legacy normalization; Dockerfile adds `asciidoctor-mathematical`; header injection so existing documents gain `:stem: latexmath` | senior-engineer | SQE + systems |
| 18.4 | Frontend: `renderAsciidoc.ts` stem + legacy-dollar support + KaTeX render pass; `sanitizeHtml.ts` allowlist extension with updated boundary comments | senior-engineer | systems (XSS focus) + SQE |
| 18.5 | Agent prompts: professor/reviewer instructed to write math as latexmath stem (inline + block forms), with a worked example in the prompt | junior-engineer | SQE |
| 18.6 | Tests: vitest fixtures for stem and legacy-dollar sources (incl. dollar-in-code-block negative case, e.g. `$HOME` in a bash snippet must NOT typeset); XSS probes through the math path (malicious LaTeX, raw HTML smuggled in math spans); integration: HTML export contains typeset math with no external asset references, PDF export produces math glyphs; e2e math journey | test-author | senior SQE + live runs |

## Acceptance criteria

1. The motivating example above renders as typeset math in the section
   reader, with **zero changes** to the stored content.
2. A newly generated course (post-prompt-change) uses stem syntax and
   renders correctly in-app, in HTML export, and in PDF export.
3. HTML export opens correctly in a browser with networking disabled.
4. `$HOME` inside a source/code block renders literally — never as math.
5. Math content cannot introduce XSS: sanitizer remains the final stage;
   probes from 18.6 pass.
6. Invalid LaTeX degrades gracefully (KaTeX `throwOnError:false` —
   render the error text, never throw or blank the section).
7. All existing tests stay green; new requirement files validate against
   the schema.

## Known costs / risks (accepted upfront)

- `asciidoctor-mathematical` adds significant Docker image weight (native
  deps: cairo, pango, etc.). Accepted; note final image size delta in the
  sprint retro.
- KaTeX CSS+fonts add to the frontend bundle. Keep them in the
  AsciiDoc/lazy chunk alongside `@asciidoctor/core`, not the main bundle.
- Dollar-delimiter detection has inherent ambiguity (currency, shell
  variables). The code-region exclusion from `renderMarkdown.ts` handles
  the known cases; document any residual heuristic limits in the TDD.

## Verification notes for the executing agent

- Production compose does not hot-reload — **rebuild the stack images
  before any e2e run** or you will test stale code.
- The Go toolchain lives at `/usr/local/go/bin`; prefix `PATH` accordingly
  for make targets on non-interactive shells.
- e2e journeys that exercise generation consume paid Anthropic tokens;
  prefer the AI-free e2e suite plus targeted fixtures for render-path
  coverage, and reserve a single live journey for final sign-off.
