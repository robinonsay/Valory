// @{"req": ["REQ-FECOURSE-612", "REQ-FECOURSE-613", "REQ-FECOURSE-614", "REQ-FECOURSE-615", "REQ-FECOURSE-625"]}

import MarkdownIt from 'markdown-it'
import katex from 'katex'
import DOMPurify from 'dompurify'

// markdown-it instance with html disabled so raw HTML in source is escaped,
// not passed through. linkify enables auto-linking. typographer is disabled
// because it converts ASCII apostrophes and quotes to Unicode curly variants,
// which changes the visible text of agent messages and breaks content matching.
const md = new MarkdownIt({
  html: false,
  linkify: true,
  typographer: false
})

// Override the default image renderer to add loading="lazy" and constrain width
// via an inline style. The src, alt, and title are already escaped by markdown-it
// because html:false is set; this renderer only adds safe, controlled attributes.
// @{"req": ["REQ-FECOURSE-614", "REQ-FECOURSE-615"]}
const defaultImageRenderer = md.renderer.rules.image
md.renderer.rules.image = (tokens, idx, options, env, self) => {
  const token = tokens[idx]
  // markdown-it guarantees the src attribute exists on image tokens
  token.attrSet('loading', 'lazy')
  token.attrSet('style', 'max-width:100%;height:auto;')
  if (defaultImageRenderer) {
    return defaultImageRenderer(tokens, idx, options, env, self)
  }
  return self.renderToken(tokens, idx, options)
}

// Placeholders use Unicode Private Use Area (PUA) characters as delimiters so
// markdown-it passes them through untouched. NUL bytes (U+0000) are replaced by
// markdown-it with U+FFFD and cannot be used as markers.
//
// PUA_OPEN  = U+E000 (first PUA codepoint)
// PUA_CLOSE = U+E001 (second PUA codepoint)
// The pattern is: PUA_OPEN + 'M' + decimal-index + PUA_CLOSE
const PUA_OPEN = ''
const PUA_CLOSE = ''

interface MathSlot {
  display: boolean
  source: string
}

// splitCodeRegions partitions a markdown source string into alternating segments:
// even-indexed segments are outside code regions (math extraction applies),
// odd-indexed segments are inside code regions (passed through verbatim).
//
// Code regions are:
//   - Fenced code blocks opened with ``` or ~~~ (matching fence must close them)
//   - Inline code spans delimited by one or more backticks
//
// The regex matches both forms in a single pass so that nested fences and
// backtick sequences within fenced blocks are handled correctly.
//
// @{"req": ["REQ-FECOURSE-613"]}
function splitCodeRegions(source: string): string[] {
  // Matches fenced blocks (```...``` or ~~~...~~~) or inline code (`...`).
  // Fenced blocks: opening fence of 3+ backticks or tildes optionally followed
  //   by an info string, then any content, then a closing fence of the same
  //   character repeated 3+ times on its own line.
  // Inline code: one or more backtick openers, matched by same count closers.
  const CODE_REGION = /(`{3,}|~{3,})[^\n]*\n[\s\S]*?\n\1[ \t]*$|(`+)[\s\S]*?\2/gm

  const segments: string[] = []
  let lastIndex = 0
  let match: RegExpExecArray | null

  while ((match = CODE_REGION.exec(source)) !== null) {
    // Push the non-code segment before this code region (may be empty string)
    segments.push(source.slice(lastIndex, match.index))
    // Push the code region itself
    segments.push(match[0])
    lastIndex = match.index + match[0].length
  }

  // Push any trailing non-code text after the last code region
  segments.push(source.slice(lastIndex))

  return segments
}

// extractMathTokens replaces $$...$$ and $...$ spans with opaque placeholders
// so that the markdown pass does not interpret LaTeX syntax as markdown. Each
// captured span is stored in the returned slots array indexed by the placeholder
// number embedded in the string.
//
// Display math ($$...$$) is matched first to prevent the single-$ pattern from
// consuming one delimiter of a $$ pair.
//
// Code-awareness: fenced code blocks (```...``` and ~~~...~~~) and inline code
// spans (`...`) are excluded from dollar-sign extraction.  Dollar signs inside
// code (e.g. $HOME, $USER in a bash example) are passed through verbatim and
// are never sent to KaTeX.
//
// @{"req": ["REQ-FECOURSE-613"]}
function extractMathTokens(source: string): { text: string; slots: MathSlot[] } {
  const slots: MathSlot[] = []

  // Segment the source into alternating non-code / code regions.
  // Even-indexed segments are outside code; odd-indexed are code verbatim.
  const segments = splitCodeRegions(source)

  const processedSegments = segments.map((segment, i) => {
    // Odd-indexed segments are code regions — leave them untouched
    if (i % 2 === 1) return segment

    // Replace $$...$$ display math (possibly multi-line) outside code
    let out = segment.replace(/\$\$([\s\S]+?)\$\$/g, (_match, inner: string) => {
      const idx = slots.length
      slots.push({ display: true, source: inner })
      return `${PUA_OPEN}M${idx}${PUA_CLOSE}`
    })

    // Replace $...$ inline math (single line only, no empty content) outside code
    out = out.replace(/\$([^\n$]+?)\$/g, (_match, inner: string) => {
      const idx = slots.length
      slots.push({ display: false, source: inner })
      return `${PUA_OPEN}M${idx}${PUA_CLOSE}`
    })

    return out
  })

  return { text: processedSegments.join(''), slots }
}

// renderMathSlots replaces the opaque PUA-delimited placeholders with KaTeX-
// generated HTML. throwOnError is false so a bad LaTeX expression renders as
// an error span rather than throwing.
//
// @{"req": ["REQ-FECOURSE-613", "REQ-FECOURSE-615"]}
function renderMathSlots(html: string, slots: MathSlot[]): string {
  // Escape PUA chars for use in a regex literal
  const open = PUA_OPEN.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const close = PUA_CLOSE.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const pattern = new RegExp(`${open}M(\\d+)${close}`, 'g')

  return html.replace(pattern, (_match, idxStr: string) => {
    const idx = parseInt(idxStr, 10)
    const slot = slots[idx]
    if (!slot) return ''
    try {
      return katex.renderToString(slot.source, {
        displayMode: slot.display,
        throwOnError: false,
        // htmlAndMathml provides both accessible MathML and visual HTML output
        output: 'htmlAndMathml'
      })
    } catch {
      // Should not reach here with throwOnError:false, but defend anyway
      return `<span class="math-error">[math error]</span>`
    }
  })
}

// DOMPurify configuration: allowlist profile that permits KaTeX's HTML/MathML
// output and standard markdown-generated markup, while forbidding all active
// content.  This is the final, authoritative sanitization stage.
//
// Allowed tags are the union of:
//   - Standard markdown output elements
//   - KaTeX HTML output elements (span with specific classes)
//   - MathML elements (math, semantics, annotation, mrow, etc.)
//   - img (constrained below)
//
// Forbidden unconditionally: script, iframe, object, embed, form, style,
// base, meta, link, and any element not in the allowlist.
//
// @{"req": ["REQ-FECOURSE-615"]}
const DOMPURIFY_CONFIG: DOMPurify.Config = {
  ALLOWED_TAGS: [
    // Block markdown
    'p', 'br', 'hr',
    'h1', 'h2', 'h3', 'h4', 'h5', 'h6',
    'ul', 'ol', 'li',
    'blockquote',
    'pre', 'code',
    'table', 'thead', 'tbody', 'tfoot', 'tr', 'th', 'td',
    // Inline markdown
    'strong', 'b', 'em', 'i', 's', 'del', 'ins',
    'a',
    'img',
    // KaTeX HTML output uses spans with class names for font and layout
    'span',
    // MathML (produced by KaTeX htmlAndMathml output)
    'math', 'semantics', 'annotation', 'annotation-xml',
    'mrow', 'mi', 'mn', 'mo', 'ms', 'mtext', 'mspace',
    'msup', 'msub', 'msubsup', 'munder', 'mover', 'munderover',
    'mfrac', 'msqrt', 'mroot', 'mfenced', 'mpadded',
    'menclose', 'mtable', 'mtr', 'mtd', 'mlabeledtr',
    'mstyle', 'merror', 'mphantom', 'maction',
    'mglyph', 'malignmark', 'maligngroup'
  ],
  ALLOWED_ATTR: [
    // Standard attributes
    'class', 'id',
    // img: src allowed only for http(s) and data:image URIs (enforced below)
    'src', 'alt', 'title', 'width', 'height', 'loading', 'style',
    // a: href (javascript: stripped below), rel, target
    'href', 'rel', 'target',
    // table
    'colspan', 'rowspan', 'scope',
    // code
    'lang',
    // MathML
    'display', 'xmlns', 'mathvariant', 'mathsize', 'mathcolor',
    'lspace', 'rspace', 'stretchy', 'form', 'fence', 'separator',
    'open', 'close', 'notation', 'encoding',
    // KaTeX: aria attributes for accessibility
    'aria-hidden', 'aria-label', 'role'
  ],
  // FORCE_BODY ensures a fragment context — no html/body/head wrappers added
  FORCE_BODY: true,
  // Return a string, not a DOM node
  RETURN_DOM: false,
  RETURN_DOM_FRAGMENT: false
}

// Install DOMPurify hook once to enforce per-element security rules that the
// allowlist alone cannot express:
//   1. img src must be http(s) or data:image — strip javascript: and other schemes
//   2. a href must not be javascript: — strip it
//   3. Any on* attribute is stripped (belt-and-suspenders; ALLOWED_ATTR excludes them)
//
// The hook runs after DOMPurify's attribute allowlist check, so it only sees
// attributes that survived the allowlist.
//
// @{"req": ["REQ-FECOURSE-614", "REQ-FECOURSE-615"]}
let hookInstalled = false
function ensureDOMPurifyHooksInstalled(): void {
  if (hookInstalled) return
  hookInstalled = true

  DOMPurify.addHook('afterSanitizeAttributes', (node: Element) => {
    // Strip on* event handlers (belt-and-suspenders)
    for (const attr of Array.from(node.attributes)) {
      if (/^on/i.test(attr.name)) {
        node.removeAttribute(attr.name)
      }
    }

    // Enforce safe image sources
    if (node.tagName === 'IMG') {
      const src = node.getAttribute('src') ?? ''
      // Allow:
      // 1. https:// and http:// URLs (external images)
      // 2. data:image/<known-type>;base64, URIs (inline base64)
      // 3. /api/v1/images/<uuid> paths (same-origin uploaded images)
      //
      // data:image/svg+xml is excluded because SVG can contain script elements;
      // only raster formats (png, jpeg, gif, webp) are permitted.
      // The UUID regex ensures only valid image paths are matched.
      if (
        !/^https?:\/\//i.test(src) &&
        !/^data:image\/(png|jpe?g|gif|webp);base64,/i.test(src) &&
        !/^\/api\/v1\/images\/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(src)
      ) {
        node.removeAttribute('src')
      }
    }

    // Enforce safe link hrefs
    if (node.tagName === 'A') {
      const href = node.getAttribute('href') ?? ''
      if (/^javascript:/i.test(href)) {
        node.removeAttribute('href')
      }
      // Add rel="noopener noreferrer" to all external links for safety
      if (href.startsWith('http')) {
        node.setAttribute('rel', 'noopener noreferrer')
        node.setAttribute('target', '_blank')
      }
    }
  })
}

// renderMarkdown converts agent message content to safe, sanitized HTML.
//
// Pipeline:
//   1. Extract $$...$$ and $...$ math spans into opaque placeholders.
//   2. Render the remaining text through markdown-it (html:false prevents raw
//      HTML pass-through).
//   3. Replace placeholders with KaTeX-rendered HTML.
//   4. Sanitize the combined output with DOMPurify using a strict allowlist.
//
// The DOMPurify pass is always the final stage so KaTeX output cannot introduce
// any XSS vector regardless of what the LaTeX source contains.
//
// @{"req": ["REQ-FECOURSE-612", "REQ-FECOURSE-613", "REQ-FECOURSE-614", "REQ-FECOURSE-615", "REQ-FECOURSE-625"]}
export function renderMarkdown(source: string): string {
  ensureDOMPurifyHooksInstalled()

  // Step 0: strip U+E000 and U+E001 (PUA placeholder delimiters) from the
  // input before placeholder extraction.  Without this, a crafted input
  // containing these characters could collide with placeholder markers and
  // corrupt the slot lookup table or bypass the sanitization boundary.
  const sanitizedSource = source.replace(/[\uE000\uE001]/g, '')

  // Step 1: protect math tokens from markdown-it
  const { text, slots } = extractMathTokens(sanitizedSource)

  // Step 2: render markdown (html:false — raw HTML in source is escaped)
  const mdHtml = md.render(text)

  // Step 3: inject KaTeX output in place of placeholders
  const withMath = slots.length > 0 ? renderMathSlots(mdHtml, slots) : mdHtml

  // Step 4: sanitize — this is the security boundary
  return DOMPurify.sanitize(withMath, DOMPURIFY_CONFIG) as string
}
