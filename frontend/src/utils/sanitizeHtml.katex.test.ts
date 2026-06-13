// @{"req": ["REQ-FECONTENT-031", "REQ-FECONTENT-034"]}
// @{"verifies": ["REQ-FECONTENT-031", "REQ-FECONTENT-034"]}

import { describe, it, expect } from 'vitest'
import { sanitizeHtml } from './sanitizeHtml'

function parse(html: string): Document {
  return new DOMParser().parseFromString(`<body>${html}</body>`, 'text/html')
}

// ─── Overlay probe A: plain div with position:fixed ─────────────────────────
// This is the EXISTING probe from sanitizeHtml.test.ts (lines 11-23). It is
// re-stated here for completeness per SDD-024 §5 — the plain div has no katex*
// class so the hook strips style unconditionally.
describe('sanitizeHtml KaTeX — overlay probe A: plain div (existing contract)', () => {
  it('strips style from plain div with position:fixed (no katex class)', () => {
    const payload =
      '<div style="position:fixed;top:0;left:0;width:100%;height:100%;z-index:9999">overlay</div>'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    const div = doc.querySelector('div')
    if (div) {
      expect(div.getAttribute('style')).toBeNull()
    }
    expect(out).not.toContain('position:fixed')
    expect(out).not.toContain('position: fixed')
  })
})

// ─── Overlay probe B: katex-classed span with position:fixed ────────────────
// The span qualifies as a KaTeX element (class starts with "katex"), so the
// allowlist IS consulted — but position only admits the exact value 'relative',
// so 'fixed' fails, validateKatexStyle returns false, and the entire style
// attribute is stripped. (SDD-024 §5 probe B, task 18.6)
describe('sanitizeHtml KaTeX — overlay probe B: katex-classed span', () => {
  it('strips style from katex-classed span with position:fixed', () => {
    const payload =
      '<span class="katex" style="position:fixed;top:0;left:0;width:100%;height:100%;z-index:9999">overlay</span>'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    const span = doc.querySelector('span.katex')
    // The span may survive as an unstyled element; style must be absent
    if (span) {
      expect(span.getAttribute('style')).toBeNull()
    }
    expect(out).not.toContain('position:fixed')
    expect(out).not.toContain('position: fixed')
  })

  it('strips style from katex-classed span with position:absolute', () => {
    const payload =
      '<span class="katex" style="position:absolute;top:0;left:0;width:100%;height:100%">overlay</span>'
    const out = sanitizeHtml(payload)
    expect(out).not.toContain('position:absolute')
    expect(out).not.toContain('position: absolute')
  })

  it('strips style containing z-index (not in allowed property set)', () => {
    const payload =
      '<span class="katex-html" style="z-index:9999;height:1em">bad</span>'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    const span = doc.querySelector('span')
    if (span) {
      expect(span.getAttribute('style')).toBeNull()
    }
  })
})

// ─── Op-symbol pass-probe ────────────────────────────────────────────────────
// KaTeX 0.17.0 emits exactly this style for non-limits single-symbol operators
// (\int, inline \sum, \prod, \oint) via op.ts. The position:relative declaration
// must pass the exact-value scoped branch; the whole style must be preserved.
// (SDD-024 §5 op-symbol pass-probe, rev-4 regression guard)
describe('sanitizeHtml KaTeX — op-symbol pass-probe (position:relative)', () => {
  it('preserves style="margin-right:0.1945em;position:relative;top:-0.0006em" on katex span', () => {
    const payload =
      '<span class="katex" style="margin-right:0.1945em;position:relative;top:-0.0006em">op</span>'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    const span = doc.querySelector('span.katex')
    expect(span).not.toBeNull()
    // The style attribute must be preserved (all three declarations pass)
    const style = span!.getAttribute('style')
    expect(style).not.toBeNull()
    expect(style).toContain('margin-right')
    expect(style).toContain('position:relative')
    expect(style).toContain('top')
  })
})

// ─── Legitimate-sizing pass-probe ────────────────────────────────────────────
// All three declarations pass GEOMETRY_VALUE_RE: KaTeX layout styles must not
// be stripped. (SDD-024 §5 legitimate-sizing pass-probe)
describe('sanitizeHtml KaTeX — legitimate-sizing pass-probe', () => {
  it('preserves height/vertical-align/margin-right on katex-html span', () => {
    const payload =
      '<span class="katex-html" style="height:0.694em;vertical-align:-0.5em;margin-right:0.1667em">math</span>'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    const span = doc.querySelector('span.katex-html')
    expect(span).not.toBeNull()
    const style = span!.getAttribute('style')
    expect(style).not.toBeNull()
    expect(style).toContain('height:0.694em')
    expect(style).toContain('vertical-align:-0.5em')
    expect(style).toContain('margin-right:0.1667em')
  })

  it('preserves negative em values (vertical-align)', () => {
    const payload =
      '<span class="katex-html" style="vertical-align:-0.5em">x</span>'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    const span = doc.querySelector('span')
    expect(span?.getAttribute('style')).toContain('vertical-align:-0.5em')
  })

  it('preserves border-bottom-width em values', () => {
    const payload =
      '<span class="katex-html" style="border-bottom-width:0.04em">frac</span>'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    const span = doc.querySelector('span')
    expect(span?.getAttribute('style')).toContain('border-bottom-width:0.04em')
  })

  it('preserves color:currentColor', () => {
    const payload =
      '<span class="katex-html" style="color:currentColor">color</span>'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    const span = doc.querySelector('span')
    expect(span?.getAttribute('style')).toContain('color:currentColor')
  })

  it('preserves color named color (red)', () => {
    const payload = '<span class="katex" style="color:red">c</span>'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    const span = doc.querySelector('span')
    expect(span?.getAttribute('style')).toContain('color:red')
  })

  it('preserves color hex value', () => {
    const payload = '<span class="katex" style="color:#3366ff">hex</span>'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    const span = doc.querySelector('span')
    expect(span?.getAttribute('style')).toContain('color:#3366ff')
  })

  it('preserves border-style:solid', () => {
    const payload =
      '<span class="katex-html" style="border-style:solid">rule</span>'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    const span = doc.querySelector('span')
    expect(span?.getAttribute('style')).toContain('border-style:solid')
  })

  it('preserves text-shadow:none on math element', () => {
    const payload =
      '<span class="katex-error" style="text-shadow:none">err</span>'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    const span = doc.querySelector('span')
    expect(span?.getAttribute('style')).toContain('text-shadow:none')
  })
})

// ─── MathML elements ─────────────────────────────────────────────────────────
// MathML tags must pass through the sanitizer intact (SDD-024 §5.1).
describe('sanitizeHtml KaTeX — MathML element pass-through', () => {
  it('preserves <math> element with xmlns attribute', () => {
    const payload = '<math xmlns="http://www.w3.org/1998/Math/MathML"><mrow><mi>x</mi></mrow></math>'
    const out = sanitizeHtml(payload)
    expect(out).toContain('<math')
    expect(out).toContain('<mrow')
    expect(out).toContain('<mi')
    expect(out).toContain('x')
  })

  it('preserves mfrac element', () => {
    const payload = '<math><mfrac><mn>1</mn><mn>2</mn></mfrac></math>'
    const out = sanitizeHtml(payload)
    expect(out).toContain('<mfrac')
  })

  it('preserves MathML attributes like mathvariant', () => {
    const payload = '<math><mi mathvariant="bold">x</mi></math>'
    const out = sanitizeHtml(payload)
    expect(out).toContain('mathvariant')
  })
})

// ─── XSS: event handlers on katex elements still stripped ────────────────────
describe('sanitizeHtml KaTeX — event handlers stripped on math elements', () => {
  it('strips onerror from katex-classed span', () => {
    const payload =
      '<span class="katex" onerror="alert(1)">x</span>'
    const out = sanitizeHtml(payload)
    expect(out).not.toContain('onerror')
  })

  it('strips onclick from MathML element', () => {
    const payload = '<math onclick="alert(1)"><mrow><mi>x</mi></mrow></math>'
    const out = sanitizeHtml(payload)
    expect(out).not.toContain('onclick')
  })
})

// ─── Rejection examples ───────────────────────────────────────────────────────
describe('sanitizeHtml KaTeX — property-scoped rejection', () => {
  it('strips style with top:inherit on katex span', () => {
    // 'inherit' does not match GEOMETRY_VALUE_RE
    const payload =
      '<span class="katex" style="top:inherit">x</span>'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    const span = doc.querySelector('span')
    expect(span?.getAttribute('style')).toBeNull()
  })

  it('strips style with background-image url on katex span', () => {
    const payload =
      '<span class="katex" style="background-image:url(https://evil.com)">x</span>'
    const out = sanitizeHtml(payload)
    expect(out).not.toContain('background-image')
  })

  it('strips style with color:expression() on katex span', () => {
    const payload =
      '<span class="katex" style="color:expression(alert(1))">x</span>'
    const out = sanitizeHtml(payload)
    expect(out).not.toContain('expression(')
  })
})
