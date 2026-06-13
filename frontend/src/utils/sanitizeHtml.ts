// @{"req": ["REQ-FECONTENT-001", "REQ-FECONTENT-002", "REQ-FECONTENT-011", "REQ-FECONTENT-015", "REQ-FECONTENT-016", "REQ-FECONTENT-017", "REQ-FECOURSE-629", "REQ-FECONTENT-225", "REQ-FECONTENT-031", "REQ-FECONTENT-034"]}

import DOMPurify from 'dompurify'

// SECTION_SANITIZER_CONFIG is a strict DOMPurify profile for AI-generated AsciiDoc
// content displayed in SectionReaderView and SyllabusView.
//
// As of SDD-024 (sprint 24), section content now uses KaTeX math typesetting.
// The style attribute is permitted conditionally: DOMPurify passes it to the
// afterSanitizeAttributes hook, which enforces a property-scoped allowlist on
// KaTeX/MathML elements only. Non-math elements have their style stripped
// unconditionally (see ensureSectionHookInstalled).
//
// Key differences from renderMarkdown's DOMPURIFY_CONFIG:
//   - FORBID_TAGS explicitly drops form, input, textarea, select, button,
//     style, script, iframe, object, embed, base, link, meta — these are the
//     exact tags used in the probe-confirmed phishing overlay attack.
//   - The style attribute is now conditionally permitted via the hook: math
//     elements (KaTeX-classed or MathML tag) get property-scoped validation;
//     non-math elements still have style stripped unconditionally. This keeps
//     the plain-div overlay probe failing while allowing KaTeX layout styles.
//   - MathML tags added for KaTeX htmlAndMathml output (SDD-024).
//
// Extensions for Asciidoctor HTML output (REQ-FECOURSE-629, REQ-FECONTENT-225):
//   - 'data-lang' added to ALLOWED_ATTR: Asciidoctor emits this on <code>
//     elements to carry the source language name (e.g. data-lang="javascript").
//     It is purely informational; no executable behaviour is attached.
//   - All structural tags Asciidoctor uses (div, section, h1-h6, ul, li, table,
//     pre, code, i, span, etc.) are already present in ALLOWED_TAGS.
//
// @{"req": ["REQ-FECONTENT-011", "REQ-FECONTENT-015", "REQ-FECONTENT-016", "REQ-FECONTENT-017", "REQ-FECOURSE-629", "REQ-FECONTENT-225", "REQ-FECONTENT-031"]}
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
    'caption', 'col', 'colgroup',
    // MathML tags added for KaTeX htmlAndMathml output (SDD-024)
    'math', 'semantics', 'annotation', 'annotation-xml',
    'mrow', 'mi', 'mn', 'mo', 'ms', 'mtext', 'mspace',
    'msup', 'msub', 'msubsup', 'munder', 'mover', 'munderover',
    'mfrac', 'msqrt', 'mroot', 'mfenced', 'mpadded',
    'menclose', 'mtable', 'mtr', 'mtd', 'mlabeledtr',
    'mstyle', 'merror', 'mphantom', 'maction',
    'mglyph', 'malignmark', 'maligngroup'
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
    'open',
    // MathML attributes — mirrors renderMarkdown.ts DOMPURIFY_CONFIG (SDD-024 §5.2)
    'display', 'xmlns', 'mathvariant', 'mathsize', 'mathcolor',
    'lspace', 'rspace', 'stretchy', 'form', 'fence', 'separator',
    'open', 'close', 'notation', 'encoding',
    // style is conditionally permitted — DOMPurify passes it to the
    // afterSanitizeAttributes hook which enforces the KaTeX-element +
    // property-scoped value allowlist gate. Non-math elements still have
    // style stripped unconditionally inside the hook (SDD-024 §5.3).
    'style'
  ],
  // style is now managed by the afterSanitizeAttributes hook rather than
  // forbidden globally. The hook strips style unconditionally on non-math
  // elements, and applies a property-scoped allowlist on KaTeX/MathML elements.
  // This preserves the existing phishing-overlay protection while permitting
  // KaTeX layout styles. (SDD-024 §5.3 — replaces the previous FORBID_ATTR: ['style'])
  FORBID_ATTR: [],
  FORCE_BODY: true,
  RETURN_DOM: false,
  RETURN_DOM_FRAGMENT: false
}

// MathML tag set — used by the hook to identify MathML elements. Matches the
// set enumerated in SDD-024 §5.1.
// @{"req": ["REQ-FECONTENT-031"]}
const MATHML_TAGS = new Set([
  'math', 'semantics', 'annotation', 'annotation-xml',
  'mrow', 'mi', 'mn', 'mo', 'ms', 'mtext', 'mspace',
  'msup', 'msub', 'msubsup', 'munder', 'mover', 'munderover',
  'mfrac', 'msqrt', 'mroot', 'mfenced', 'mpadded',
  'menclose', 'mtable', 'mtr', 'mtd', 'mlabeledtr',
  'mstyle', 'merror', 'mphantom', 'maction',
  'mglyph', 'malignmark', 'maligngroup'
])

// Property-scoped style validation constants (SDD-024 §5.3 pseudocode verbatim).
// Each property set maps to a specific value pattern; unknown properties reject
// the entire style attribute.
// @{"req": ["REQ-FECONTENT-031"]}
const GEOMETRY_VALUE_RE = /^-?[0-9]+(\.[0-9]+)?(em|ex|px|rem|%)$|^0$/

const GEOMETRY_PROPS = new Set([
  'height', 'width', 'min-width', 'top', 'bottom', 'left',
  'vertical-align', 'margin', 'margin-left', 'margin-right', 'margin-top',
  'padding-left', 'border-bottom-width', 'border-right-width',
  'border-top-width', 'border-width',
])

const COLOR_PROPS = new Set(['color', 'background-color', 'border-color'])

// Property-scoped: accepts currentColor, transparent, hex values, or CSS named
// colors (lowercase alpha only). The lowercase-alpha sub-branch is reachable
// ONLY for color properties — geometry and position properties use their own
// value patterns and never reach this regex.
const COLOR_VALUE_RE = /^(currentColor|transparent|#[0-9a-fA-F]{3,6}|[a-z]+)$/

const BORDER_STYLE_PROPS = new Set(['border-right-style', 'border-style'])
const BORDER_STYLE_VALUE_RE = /^(solid|dashed|dotted|none)$/

const TEXT_SHADOW_VALUE_RE = /^none$/

// isDeclarationAllowed validates one CSS property:value pair against the
// property-scoped allowlist (SDD-024 §5.3 pseudocode).
// @{"req": ["REQ-FECONTENT-031"]}
function isDeclarationAllowed(prop: string, value: string): boolean {
  const p = prop.trim().toLowerCase()
  const v = value.trim()
  if (GEOMETRY_PROPS.has(p)) {
    return GEOMETRY_VALUE_RE.test(v)
  }
  if (COLOR_PROPS.has(p)) {
    return COLOR_VALUE_RE.test(v)
  }
  if (BORDER_STYLE_PROPS.has(p)) {
    return BORDER_STYLE_VALUE_RE.test(v)
  }
  if (p === 'text-shadow') {
    return TEXT_SHADOW_VALUE_RE.test(v)
  }
  // KaTeX emits position:relative inline for non-limits single-symbol
  // operators (\int, inline \sum, \prod, \oint) via op.ts when baseShift != 0.
  // 'relative' keeps the element in normal flow and cannot create an overlay;
  // every other position value is rejected (z-index is also not allowed, so
  // stacking abuse is blocked).
  if (p === 'position') {
    return v === 'relative'
  }
  // Unknown property — reject. An allowlist of exact properties is safer than
  // any denylist approach (SDD-024 §11.7).
  return false
}

// validateKatexStyle validates the full semicolon-delimited style attribute
// value against the property-scoped allowlist. Returns true only if every
// declaration passes; any failing declaration causes the entire attribute to
// be removed. (SDD-024 §5.3 pseudocode verbatim)
// @{"req": ["REQ-FECONTENT-031"]}
function validateKatexStyle(styleAttrValue: string): boolean {
  const declarations = styleAttrValue.split(';')
  for (const decl of declarations) {
    const trimmed = decl.trim()
    if (trimmed === '') continue
    const colonIdx = trimmed.indexOf(':')
    if (colonIdx < 1) return false
    const prop = trimmed.slice(0, colonIdx)
    const value = trimmed.slice(colonIdx + 1)
    if (!isDeclarationAllowed(prop, value)) return false
  }
  return true
}

// isMathElement returns true if the node qualifies as a KaTeX or MathML math
// element. Identification follows SDD-024 §5.3: MathML tag name, or a class
// attribute containing at least one token matching /^katex/, or an ancestor
// with such a class (belt-and-suspenders ancestor walk).
// @{"req": ["REQ-FECONTENT-031"]}
function isMathElement(node: Element): boolean {
  // MathML tag name check
  if (MATHML_TAGS.has(node.tagName.toLowerCase())) return true

  // KaTeX class check on the element itself
  const cls = node.getAttribute('class') ?? ''
  if (cls.split(/\s+/).some(token => token.startsWith('katex'))) return true

  // Ancestor walk: check if this element is inside a KaTeX subtree
  let ancestor = node.parentElement
  while (ancestor !== null) {
    const ancestorCls = ancestor.getAttribute('class') ?? ''
    if (ancestorCls.split(/\s+/).some(token => token.startsWith('katex'))) {
      return true
    }
    ancestor = ancestor.parentElement
  }

  return false
}

// sectionSanitizerHookInstalled guards the one-time addHook call.
// DOMPurify hooks are global; installing the same hook twice doubles its effect.
let sectionSanitizerHookInstalled = false

// @{"req": ["REQ-FECONTENT-011", "REQ-FECONTENT-015", "REQ-FECONTENT-016", "REQ-FECONTENT-017", "REQ-FECONTENT-031", "REQ-FECONTENT-034"]}
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

    // Style attribute: conditionally permitted only on KaTeX/MathML elements,
    // and only for the exact set of properties KaTeX emits (SDD-024 §5.3).
    //
    // Non-math elements: strip style unconditionally — preserves the existing
    // phishing-overlay protection (plain-div overlay probe must keep passing).
    //
    // Math elements: apply the property-scoped allowlist. If any single
    // declaration is unknown or has an out-of-range value, strip the entire
    // style attribute. This neutralizes the katex-classed-span overlay probe
    // (position:fixed fails because position only admits 'relative').
    if (node.hasAttribute('style')) {
      if (!isMathElement(node)) {
        node.removeAttribute('style')
      } else {
        const styleValue = node.getAttribute('style') ?? ''
        if (!validateKatexStyle(styleValue)) {
          node.removeAttribute('style')
        }
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
//   - allows style only on KaTeX/MathML elements, with a property-scoped
//     allowlist enforced by the afterSanitizeAttributes hook (SDD-024 §5.3)
//   - strips U+E000/U+E001 PUA characters before DOMPurify runs, preventing
//     any interaction with the renderMarkdown placeholder scheme
//   - applies the afterSanitizeAttributes hook for img src and a href safety
//
// This is also the security boundary for renderAsciidoc: AsciiDoc passthrough
// blocks (pass:[] / +++) can emit raw HTML from the converter; this function
// ensures that executable content is stripped before the string reaches v-html.
//
// @{"req": ["REQ-FECONTENT-001", "REQ-FECONTENT-002", "REQ-FECONTENT-011", "REQ-FECONTENT-015", "REQ-FECONTENT-016", "REQ-FECONTENT-017", "REQ-FECOURSE-629", "REQ-FECONTENT-225", "REQ-FECONTENT-031", "REQ-FECONTENT-034"]}
export function sanitizeHtml(raw: string): string {
  ensureSectionHookInstalled()

  // Strip U+E000 and U+E001 (PUA placeholders used by renderMarkdown) before
  // sanitization so they cannot interfere with placeholder extraction or be
  // used as smuggling vectors.
  const stripped = raw.replace(/[]/g, '')

  return DOMPurify.sanitize(stripped, SECTION_SANITIZER_CONFIG) as string
}
