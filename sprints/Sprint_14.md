# Sprint 14 — Rich Chat Rendering: Markdown, LaTeX, Images (+ XSS Hardening)

## Objective

PM request: the chat should render markdown, LaTeX math, and images. Because
AI output is untrusted input, the sprint's center of gravity was the
sanitization pipeline — and the adversarial review paid off by finding a
pre-existing critical hole in a different view.

## Work performed

| Task | Deliverable | Contributor | Verifier | Outcome |
|---|---|---|---|---|
| Reqs | REQ-FECOURSE-612..616 (markdown/LaTeX/images/sanitize/student-plain-text), REQ-AGENT-022 (replies in Markdown+LaTeX) | software-lead | SQE + schema | PASS (616 reworded after review; Markdown/LaTeX kept as observable format contracts — lead decision, PM flag) |
| 14.1 | frontend/src/utils/renderMarkdown.ts: markdown-it (html:false) → code-region-aware $-math extraction → KaTeX (trust:false) → DOMPurify allowlist + attr hook (img src https/raster-data: only, javascript: strip, on* strip); IntakeChatView agent bubbles via the pipeline, student bubbles stay plain interpolation; deps markdown-it/katex | senior-engineer | SQE + systems (XSS attack pass) | PASS after fix round |
| 14.2 | chairSystemPrompt + intakeSystemPrompt emit Markdown/$LaTeX$ (sentinel contract untouched, +5 prompt tests) | junior-engineer | SQE | PASS |
| 14.3 | e2e content-rendering.spec.ts: rendered markdown elements, .katex typesetting, image rendering, XSS inertness via window.__pwned probes, student plain-text | test-author | senior SQE + live runs | PASS (2 courses/run, 0 AI sends) |
| Fixes | Security fix round (below) | senior-engineer | senior SQE re-probes | SHIP |

## Security findings from the adversarial review (the headline)

1. **CRITICAL (pre-existing): SectionReaderView rendered AI AsciiDoc through
   BARE default-config DOMPurify** — probe-confirmed that `<form action=attacker>`,
   `<input>`, and `position:fixed` full-viewport phishing overlays survived.
   Fixed with a shared strict sanitizer (frontend/src/utils/sanitizeHtml.ts):
   FORBID_TAGS form/input/style/script/iframe/…, style attribute forbidden
   entirely on that surface, same img/href hook. 27 sanitizer tests.
2. data:image/svg+xml img src allowed (inert in <img> but tracking-pixel
   surface) → tightened to raster-only `data:image/(png|jpe?g|gif|webp);base64,`.
3. Paired `$` inside fenced/inline code mangled into KaTeX (`echo $HOME and
   $USER`) → math extraction is now code-region-aware.
4. PUA placeholder chars (U+E000/E001) stripped from input (display-fidelity).

Attack vectors probed and confirmed inert: placeholder smuggling, style/id
abuse in the chat pipeline, linkify scheme tricks (entity-encoded/mixed-case
javascript:), KaTeX \href/\url with trust:false, DOM clobbering, raw-HTML
passthrough (html:false escapes everything).

## Verification

- Go matrix green; vitest 320/320 (27 files); production build green (KaTeX
  CSS/fonts bundled — verified live by asset hash).
- AI-free e2e 33/33 twice (5 new rendering/XSS specs). 0 open courses.
- Senior SQE independently re-probed both fixed holes + fresh vectors: SHIP.

## Backlog / follow-ups

- REQ-AGENT-022 mild atomicity (Markdown + LaTeX in one shall) — PM hygiene
  pass candidate; 612/613 technology naming accepted as format contracts.
- Global DOMPurify hooks registered by two modules (currently idempotent and
  benign — consider scoped instances if more surfaces appear).
- KaTeX adds ~200-250KB gzip to the single 545KB chunk — code-splitting
  candidate (Sprint 15+).
- Relative img srcs are stripped by design (security); if professors ever
  emit relative image paths, revisit with a same-origin allowance.
- mailto: links render without rel=noopener (harmless; noted).
