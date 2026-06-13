// @{"req": ["REQ-FECOURSE-629", "REQ-FECONTENT-225", "REQ-FECONTENT-030", "REQ-FECONTENT-031", "REQ-FECONTENT-032", "REQ-FECONTENT-033", "REQ-FECONTENT-034", "REQ-FECONTENT-035"]}

// renderAsciidoc converts an AsciiDoc source string to sanitized HTML.
//
// @asciidoctor/core and katex are loaded via dynamic import so they land in
// their own chunk and are never pulled into the main bundle. Views that display
// AsciiDoc pay the load cost; all other views do not. KaTeX assets (JS, CSS,
// and fonts) come exclusively from the application bundle — no CDN calls are
// made (REQ-FECONTENT-035).
//
// Pipeline (SDD-024 §4.1):
//   [1] normalizeDollarMath — convert $…$/$$$…$$$ outside code regions to
//       STEM macros; prepend :stem: latexmath if absent
//   [2] asciidoctor.convert — Asciidoctor wraps stem:[…] as \(…\), [stem]++++
//       blocks as \[…\]; safe:'secure' prevents include:: / shell execution
//   [3] renderKatexInHtml — scan for \(…\)/\[…\] and typeset with KaTeX
//   [4] sanitizeHtml — DOMPurify with the extended allowlist; ALWAYS last
//
// Security boundaries:
//   1. safe: 'secure' prevents include:: directives and attribute-based macros
//      from reading files or executing shell commands during conversion.
//   2. The resulting HTML is passed through sanitizeHtml (strict DOMPurify
//      allowlist) as the final XSS barrier. AsciiDoc passthrough blocks
//      (pass:[] / +++<script>+++) can emit raw HTML from the converter; the
//      sanitizer removes any executable content before the string reaches v-html.

import { sanitizeHtml } from './sanitizeHtml'

// Singleton promise so the dynamic import fires only once per page load.
// katex is co-located in the same dynamic import as @asciidoctor/core so Vite
// bundles both into the same lazy chunk (REQ-FECONTENT-035).
let asciidoctorInstancePromise: Promise<ReturnType<typeof import('@asciidoctor/core')['default']>> | null = null
let katexModule: typeof import('katex') | null = null

// @{"req": ["REQ-FECOURSE-629", "REQ-FECONTENT-225", "REQ-FECONTENT-035"]}
async function getAsciidoctorAndKatex(): Promise<{
  asciidoctor: ReturnType<typeof import('@asciidoctor/core')['default']>
  katex: typeof import('katex')
}> {
  if (!asciidoctorInstancePromise) {
    asciidoctorInstancePromise = import('@asciidoctor/core').then(mod => {
      // The default export is a factory function; calling it returns the
      // processor. Support both default-exported factory and named exports.
      const factory = mod.default ?? mod
      return typeof factory === 'function' ? factory() : factory
    })
  }
  if (!katexModule) {
    // Import both katex and its CSS inside the same dynamic-import callback
    // so Vite co-locates them in the asciidoc chunk. Font files referenced by
    // katex.min.css are resolved by Vite's CSS asset pipeline and placed in
    // dist/assets/ — no CDN references are emitted (REQ-FECONTENT-035).
    const [katex] = await Promise.all([
      import('katex'),
      import('katex/dist/katex.min.css'),
    ])
    katexModule = katex
  }
  const asciidoctor = await asciidoctorInstancePromise
  return { asciidoctor, katex: katexModule }
}

// CODE_REGION_RE matches AsciiDoc code regions that must be excluded from
// dollar-sign extraction (SDD-024 §3.2.1, corrected regex from rev 2).
//
// Two top-level alternatives:
//   Alt 1 (delimited block): (-{4,}|\.{4,}|`+)[^\n]*\n[\s\S]*?\n\1[ \t]*$
//     — captures the opening fence in group 1 (4+ hyphens, 4+ dots, or 1+ backticks),
//       matches a newline, any content (lazy), then the same fence text (\1 back-
//       reference) at the end of a line. Covers listing blocks (----), literal
//       blocks (....), and AsciiDoc-style backtick-fenced blocks.
//   Alt 2 (inline code span): (`+)[\s\S]*?\2
//     — captures one or more backticks in group 2, matches any content (lazy),
//       then the same backtick count. Covers inline code.
//
// NOTE: \.{4,} (four or more literal dots) is correct here. \\.{4,} in a JS
// regex literal means a literal backslash followed by any char (4+ times) —
// that would NOT match ....  and would silently fail AC 4 ($HOME in a literal
// block). See SDD-024 §3.2.1 worked-examples table.
// @{"req": ["REQ-FECONTENT-032"]}
const CODE_REGION_RE = /(-{4,}|\.{4,}|`+)[^\n]*\n[\s\S]*?\n\1[ \t]*$|(`+)[\s\S]*?\2/gm

// normalizeDollarMath converts legacy $…$ / $$…$$ math in an AsciiDoc source
// string to AsciiDoc STEM macros that Asciidoctor understands, skipping any
// dollar signs that appear inside code regions (listing blocks, literal blocks,
// inline code spans). It also prepends :stem: latexmath if not already present.
//
// This is the TypeScript twin of the Go normalizeDollarMathAdoc function in
// internal/content/ — both implement the same algorithm (SDD-024 §3.2, §9).
// Duplication is deliberate: the three render paths are self-contained and a
// shared cross-language abstraction would add more complexity than it removes.
//
// @{"req": ["REQ-FECONTENT-030", "REQ-FECONTENT-032"]}
export function normalizeDollarMath(src: string): string {
  // Segment the source into alternating non-code / code regions.
  // Even-indexed segments are outside code; odd-indexed are code verbatim.
  // Reset lastIndex before use — the regex has the 'g' flag so state persists.
  CODE_REGION_RE.lastIndex = 0
  const segments: string[] = []
  let lastIndex = 0
  let match: RegExpExecArray | null

  while ((match = CODE_REGION_RE.exec(src)) !== null) {
    segments.push(src.slice(lastIndex, match.index))
    segments.push(match[0])
    lastIndex = match.index + match[0].length
  }
  segments.push(src.slice(lastIndex))

  // Process only the non-code (even-indexed) segments.
  const processed = segments.map((segment, i) => {
    if (i % 2 === 1) return segment

    // Replace $$…$$ display math first (possibly multi-line) to prevent the
    // single-$ pattern from consuming one delimiter of a $$ pair.
    let out = segment.replace(/\$\$([\s\S]+?)\$\$/g, (_m, inner: string) => {
      return `[stem]\n++++\n${inner}\n++++\n`
    })

    // Replace $…$ inline math (single line only, non-empty, no newlines inside).
    out = out.replace(/\$([^\n$]+?)\$/g, (_m, inner: string) => {
      return `stem:[${inner}]`
    })

    return out
  })

  const normalized = processed.join('')

  // Prepend :stem: latexmath if not already present. The attribute is idempotent
  // (setting it twice is harmless per Asciidoctor docs) but the prepend is
  // skipped when already present to avoid noisy duplication.
  if (!normalized.includes(':stem:')) {
    return `:stem: latexmath\n${normalized}`
  }
  return normalized
}

// renderKatexInHtml scans the Asciidoctor-converted HTML for \(…\) (inline
// stem) and \[…\] (display stem) delimiters and replaces them with KaTeX
// output. throwOnError:false means malformed LaTeX produces an error span
// rather than throwing (REQ-FECONTENT-033).
//
// Why regex-over-string (not DOM): Asciidoctor's output is already structured —
// math content is wrapped in known element classes — so a single-pass regex
// over the string is sufficient and avoids adding a DOM parsing dependency here.
// This approach also works in test environments without a full DOM (SDD-024 §4.2).
//
// The inline regex uses a non-greedy match that stops at the first \) not
// preceded by a backslash; the display regex similarly stops at the first \].
// This is safe for KaTeX's own output, which never contains nested \( sequences.
//
// @{"req": ["REQ-FECONTENT-030", "REQ-FECONTENT-033"]}
export function renderKatexInHtml(html: string, katex: typeof import('katex')): string {
  // Display math: \[…\] — produced by Asciidoctor for [stem]++++…++++ blocks.
  // Must be processed before inline to avoid partial matches.
  let out = html.replace(/\\\[([\s\S]*?)\\]/g, (_m, inner: string) => {
    try {
      return katex.default.renderToString(inner, {
        displayMode: true,
        throwOnError: false,
        output: 'htmlAndMathml',
      })
    } catch {
      // throwOnError:false should prevent reaching here; defend anyway.
      return `<span class="katex-error">[math error]</span>`
    }
  })

  // Inline math: \(…\) — produced by Asciidoctor for stem:[…] macros.
  out = out.replace(/\\\(([\s\S]*?)\\\)/g, (_m, inner: string) => {
    try {
      return katex.default.renderToString(inner, {
        displayMode: false,
        throwOnError: false,
        output: 'htmlAndMathml',
      })
    } catch {
      return `<span class="katex-error">[math error]</span>`
    }
  })

  return out
}

// @{"req": ["REQ-FECOURSE-629", "REQ-FECONTENT-225", "REQ-FECONTENT-030", "REQ-FECONTENT-031", "REQ-FECONTENT-032", "REQ-FECONTENT-033", "REQ-FECONTENT-034", "REQ-FECONTENT-035"]}
export async function renderAsciidoc(source: string): Promise<string> {
  const { asciidoctor, katex } = await getAsciidoctorAndKatex()

  // [1] Normalize legacy dollar-sign math to STEM macros and ensure the
  //     :stem: latexmath header attribute is present (SDD-024 §4.1 step 1).
  const normalized = normalizeDollarMath(source)

  // [2] Convert with:
  //   standalone:false — emit a fragment (no <html><body> wrapper)
  //   safe:'secure'    — disable include macros, attribute-value shell escapes,
  //                      and URI reads so untrusted source cannot exfiltrate data
  //   stem:latexmath   — tells Asciidoctor to wrap stem macros in \(…\)/\[…\]
  //   showtitle        — render the level-0 document title (= Title) as an <h1>
  //   icons:font       — use font icons rather than image files (no img src requests)
  const html = asciidoctor.convert(normalized, {
    standalone: false,
    safe: 'secure',
    attributes: {
      stem: 'latexmath',
      showtitle: true,
      icons: 'font',
      // Disable the source highlighter to avoid any dynamic asset loading
      'source-highlighter': '',
    },
  }) as string

  // [3] Typeset the \(…\)/\[…\] delimiters Asciidoctor emitted (SDD-024 §4.1 step 3).
  const withMath = renderKatexInHtml(html, katex)

  // [4] sanitizeHtml is the security boundary: it strips anything that survived
  //     the converter and KaTeX pass, including passthrough block payloads.
  //     This MUST be the final stage (REQ-FECONTENT-034, SDD-024 §4.1).
  return sanitizeHtml(withMath)
}
