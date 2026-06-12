// @{"req": ["REQ-FECONTENT-001", "REQ-FECONTENT-002", "REQ-FECONTENT-011", "REQ-FECONTENT-015", "REQ-FECONTENT-016", "REQ-FECONTENT-017", "REQ-FECOURSE-629", "REQ-FECONTENT-225"]}

import DOMPurify from 'dompurify'

// SECTION_SANITIZER_CONFIG is a strict DOMPurify profile for AI-generated AsciiDoc
// content displayed in SectionReaderView and SyllabusView. It is intentionally more
// restrictive than the renderMarkdown profile because section/syllabus content never
// contains KaTeX math, so there is no need to allow span/style for font layout.
//
// Key differences from renderMarkdown's DOMPURIFY_CONFIG:
//   - FORBID_TAGS explicitly drops form, input, textarea, select, button,
//     style, script, iframe, object, embed, base, link, meta — these are the
//     exact tags used in the probe-confirmed phishing overlay attack.
//   - The style attribute is forbidden entirely (FORBID_ATTR: ['style']).
//     Section content has no KaTeX output, so position:fixed / expression() /
//     url() overlays cannot be constructed at all.  The col/colgroup style
//     attribute (Asciidoctor table column widths) is also stripped — tables
//     still render with default equal-width columns, which is acceptable.
//   - No MathML tags in ALLOWED_TAGS (section content does not use KaTeX).
//
// Extensions for Asciidoctor HTML output (REQ-FECOURSE-629, REQ-FECONTENT-225):
//   - 'data-lang' added to ALLOWED_ATTR: Asciidoctor emits this on <code>
//     elements to carry the source language name (e.g. data-lang="javascript").
//     It is purely informational; no executable behaviour is attached.
//   - All structural tags Asciidoctor uses (div, section, h1-h6, ul, li, table,
//     pre, code, i, span, etc.) are already present in ALLOWED_TAGS.
//
// @{"req": ["REQ-FECONTENT-011", "REQ-FECONTENT-015", "REQ-FECONTENT-016", "REQ-FECONTENT-017", "REQ-FECOURSE-629", "REQ-FECONTENT-225"]}
const SECTION_SANITIZER_CONFIG: DOMPurify.Config = {
  ALLOWED_TAGS: [
    // Block content
    'p', 'br', 'hr',
    'h1', 'h2', 'h3', 'h4', 'h5', 'h6',
    'ul', 'ol', 'li',
    'blockquote',
    'pre', 'code',
    'table', 'thead', 'tbody', 'tfoot', 'tr', 'th', 'td',
    'div', 'section', 'article', 'header', 'footer', 'aside',
    // Inline content
    'strong', 'b', 'em', 'i', 's', 'del', 'ins',
    'a',
    'img',
    'span',
    'sup', 'sub',
    'abbr', 'cite', 'dfn', 'kbd', 'samp', 'var',
    'figure', 'figcaption',
    'details', 'summary',
    'dl', 'dt', 'dd',
    'caption', 'col', 'colgroup'
  ],
  // Explicitly forbid the tags used in the probe-confirmed phishing overlay attack
  // and any other active-content tags.  FORBID_TAGS takes precedence over
  // ALLOWED_TAGS, so listing a tag in both results in it being forbidden.
  FORBID_TAGS: [
    'form', 'input', 'textarea', 'select', 'button',
    'style', 'script', 'iframe', 'object', 'embed',
    'base', 'link', 'meta',
    'noscript', 'template', 'slot', 'canvas', 'svg'
  ],
  ALLOWED_ATTR: [
    // Generic
    'class', 'id',
    // img
    'src', 'alt', 'title', 'width', 'height', 'loading',
    // a
    'href', 'rel', 'target',
    // table
    'colspan', 'rowspan', 'scope',
    // code / pre
    'lang',
    // Asciidoctor emits data-lang on <code> elements for source language labels.
    // It is informational only; no executable behaviour is attached.
    'data-lang',
    // accessibility
    'aria-hidden', 'aria-label', 'role',
    // details/summary
    'open'
  ],
  // Forbid the style attribute entirely: section content has no KaTeX, so there
  // is no legitimate reason for inline styles.  This is the cleanest way to
  // prevent position:fixed / expression() / url() phishing overlays.
  FORBID_ATTR: ['style'],
  FORCE_BODY: true,
  RETURN_DOM: false,
  RETURN_DOM_FRAGMENT: false
}

// sectionSanitizerHookInstalled guards the one-time addHook call.
// DOMPurify hooks are global; installing the same hook twice doubles its effect.
let sectionSanitizerHookInstalled = false

// @{"req": ["REQ-FECONTENT-011", "REQ-FECONTENT-015", "REQ-FECONTENT-016", "REQ-FECONTENT-017"]}
function ensureSectionHookInstalled(): void {
  if (sectionSanitizerHookInstalled) return
  sectionSanitizerHookInstalled = true

  DOMPurify.addHook('afterSanitizeAttributes', (node: Element) => {
    // Strip any on* event-handler attributes that survive the allowlist
    // (belt-and-suspenders; ALLOWED_ATTR already excludes them)
    for (const attr of Array.from(node.attributes)) {
      if (/^on/i.test(attr.name)) {
        node.removeAttribute(attr.name)
      }
    }

    // Enforce safe image sources: only https://, http://, or data:image/<known type>;base64,
    if (node.tagName === 'IMG') {
      const src = node.getAttribute('src') ?? ''
      if (
        !/^https?:\/\//i.test(src) &&
        !/^data:image\/(png|jpe?g|gif|webp);base64,/i.test(src)
      ) {
        node.removeAttribute('src')
      }
    }

    // Enforce safe link hrefs: strip javascript: scheme
    if (node.tagName === 'A') {
      const href = node.getAttribute('href') ?? ''
      if (/^javascript:/i.test(href)) {
        node.removeAttribute('href')
      }
      if (href.startsWith('http')) {
        node.setAttribute('rel', 'noopener noreferrer')
        node.setAttribute('target', '_blank')
      }
    }
  })
}

// sanitizeHtml sanitizes AI-generated AsciiDoc HTML for display in
// SectionReaderView and SyllabusView.  It uses a strict allowlist that:
//   - forbids form, input, and all other active-content elements
//   - forbids the style attribute entirely (no KaTeX needed here)
//   - strips U+E000/U+E001 PUA characters before DOMPurify runs, preventing
//     any interaction with the renderMarkdown placeholder scheme
//   - applies the afterSanitizeAttributes hook for img src and a href safety
//
// This is also the security boundary for renderAsciidoc: AsciiDoc passthrough
// blocks (pass:[] / +++) can emit raw HTML from the converter; this function
// ensures that executable content is stripped before the string reaches v-html.
//
// @{"req": ["REQ-FECONTENT-001", "REQ-FECONTENT-002", "REQ-FECONTENT-011", "REQ-FECONTENT-015", "REQ-FECONTENT-016", "REQ-FECONTENT-017", "REQ-FECOURSE-629", "REQ-FECONTENT-225"]}
export function sanitizeHtml(raw: string): string {
  ensureSectionHookInstalled()

  // Strip U+E000 and U+E001 (PUA placeholders used by renderMarkdown) before
  // sanitization so they cannot interfere with placeholder extraction or be
  // used as smuggling vectors.
  const stripped = raw.replace(/[\uE000\uE001]/g, '')

  return DOMPurify.sanitize(stripped, SECTION_SANITIZER_CONFIG) as string
}
