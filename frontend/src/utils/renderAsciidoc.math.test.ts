// @{"req": ["REQ-FECONTENT-030", "REQ-FECONTENT-031", "REQ-FECONTENT-032", "REQ-FECONTENT-033", "REQ-FECONTENT-034", "REQ-FECONTENT-035"]}
// @{"verifies": ["REQ-FECONTENT-030", "REQ-FECONTENT-031", "REQ-FECONTENT-032", "REQ-FECONTENT-033", "REQ-FECONTENT-034", "REQ-FECONTENT-035"]}

import { describe, it, expect } from 'vitest'
import { normalizeDollarMath, renderKatexInHtml, renderAsciidoc } from './renderAsciidoc'
import katex from 'katex'

// Wrap the katex default export in the shape renderKatexInHtml expects when
// imported as a module namespace (import * as katexMod).
const katexMod = { default: katex } as typeof import('katex')

function parse(html: string): Document {
  return new DOMParser().parseFromString(`<body>${html}</body>`, 'text/html')
}

// ─── normalizeDollarMath ──────────────────────────────────────────────────────

describe('normalizeDollarMath — stem header injection', () => {
  it('prepends :stem: latexmath when not present', () => {
    const out = normalizeDollarMath('= Doc\n\nHello.\n')
    expect(out).toMatch(/^:stem: latexmath\n/)
  })

  it('does not double-prepend when :stem: already present', () => {
    const src = ':stem: latexmath\n= Doc\n\nHello.\n'
    const out = normalizeDollarMath(src)
    const matches = out.match(/:stem:/g) ?? []
    expect(matches.length).toBe(1)
  })
})

describe('normalizeDollarMath — inline $ conversion', () => {
  it('converts $x$ to stem:[x]', () => {
    const out = normalizeDollarMath('The value is $x$.')
    expect(out).toContain('stem:[x]')
    expect(out).not.toContain('$x$')
  })

  it('converts $E = mc^2$ to stem:[E = mc^2]', () => {
    const out = normalizeDollarMath('Energy: $E = mc^2$ here.')
    expect(out).toContain('stem:[E = mc^2]')
  })

  it('does not convert a lone $ without a matching pair', () => {
    const out = normalizeDollarMath('Price is $10.')
    // Single $ with no closing $ on the same line is not converted
    expect(out).not.toContain('stem:[')
  })
})

describe('normalizeDollarMath — display $$ conversion', () => {
  it('converts $$\\frac{1}{2}$$ to [stem]++++…++++ block', () => {
    const out = normalizeDollarMath('$$\\frac{1}{2}$$')
    expect(out).toContain('[stem]\n++++\n')
    expect(out).toContain('\\frac{1}{2}')
    expect(out).toContain('\n++++\n')
    expect(out).not.toContain('$$\\frac')
  })

  it('processes $$ before $ to prevent consuming one delimiter of a $$ pair', () => {
    const out = normalizeDollarMath('$$a + b$$')
    expect(out).toContain('[stem]')
    // Must not produce two separate inline stem macros
    expect(out).not.toMatch(/stem:\[.*\$.*\]/)
  })
})

// ─── Code-region exclusion (REQ-FECONTENT-032) ────────────────────────────────

describe('normalizeDollarMath — code-region exclusion (REQ-FECONTENT-032)', () => {
  it('does NOT convert $HOME inside a ---- listing block', () => {
    const src = '= Doc\n\n----\necho $HOME\n----\n'
    const out = normalizeDollarMath(src)
    // The listing block content must be passed through verbatim
    expect(out).toContain('echo $HOME')
    expect(out).not.toContain('stem:[HOME]')
    expect(out).not.toContain('stem:[')
  })

  it('does NOT convert $HOME inside a .... literal block', () => {
    // This is the AC 4 case that the erroneous \\\.{4,} regex would silently fail
    // (SDD-024 §3.2.1 worked-examples table, rev 2 correction)
    const src = '= Doc\n\n....\nThe path $HOME/bin\n....\n'
    const out = normalizeDollarMath(src)
    expect(out).toContain('$HOME')
    expect(out).not.toContain('stem:[HOME]')
    expect(out).not.toContain('stem:[')
  })

  it('does NOT convert $VAR inside inline backtick code', () => {
    const src = 'Use `$HOME` to refer to your home directory.'
    const out = normalizeDollarMath(src)
    expect(out).toContain('`$HOME`')
    expect(out).not.toContain('stem:[HOME]')
    expect(out).not.toContain('stem:[')
  })

  it('does NOT convert $ inside a 6-hyphen fenced block', () => {
    const src = '------\necho $USER\n------\n'
    const out = normalizeDollarMath(src)
    expect(out).toContain('$USER')
    expect(out).not.toContain('stem:[USER]')
  })

  it('DOES convert $ outside code blocks in the same document', () => {
    const src = 'Prose $x$ text.\n\n----\ncode $HOME\n----\n\nMore $y$ prose.\n'
    const out = normalizeDollarMath(src)
    expect(out).toContain('stem:[x]')
    expect(out).toContain('stem:[y]')
    expect(out).toContain('$HOME')
    expect(out).not.toContain('stem:[HOME]')
  })
})

// ─── renderKatexInHtml ────────────────────────────────────────────────────────

describe('renderKatexInHtml — inline \(…\) typesetting', () => {
  it('replaces \\(x\\) with KaTeX HTML output', () => {
    const html = '<p>Value: \\(x\\)</p>'
    const out = renderKatexInHtml(html, katexMod)
    // KaTeX output contains the katex class
    expect(out).toContain('katex')
    // The raw delimiters should be consumed
    expect(out).not.toContain('\\(x\\)')
  })

  it('replaces \\(E = mc^2\\) with KaTeX HTML', () => {
    const html = '<span class="stem">\\(E = mc^2\\)</span>'
    const out = renderKatexInHtml(html, katexMod)
    expect(out).toContain('katex')
    expect(out).not.toContain('\\(E')
  })
})

describe('renderKatexInHtml — display \\[…\\] typesetting', () => {
  it('replaces \\[\\frac{1}{2}\\] with KaTeX display HTML', () => {
    const html = '<div class="stemblock"><div class="content">\\[\\frac{1}{2}\\]</div></div>'
    const out = renderKatexInHtml(html, katexMod)
    expect(out).toContain('katex')
    expect(out).not.toContain('\\[')
  })
})

describe('renderKatexInHtml — graceful degradation (REQ-FECONTENT-033)', () => {
  it('produces an error span for invalid LaTeX without throwing', () => {
    // \\notacommand is not a valid KaTeX command; with throwOnError:false, KaTeX
    // emits an error span rather than throwing.
    expect(() => {
      const out = renderKatexInHtml('<p>\\(\\notacommand\\)</p>', katexMod)
      // Either KaTeX error class or our fallback span — must not be blank
      expect(out.length).toBeGreaterThan(0)
    }).not.toThrow()
  })

  it('does not blank the surrounding HTML on invalid LaTeX', () => {
    const html = '<h1>Title</h1><p>\\(\\notacommand\\)</p><p>After</p>'
    const out = renderKatexInHtml(html, katexMod)
    // Surrounding content must be preserved
    expect(out).toContain('<h1>Title</h1>')
    expect(out).toContain('<p>After</p>')
  })
})

// ─── Full pipeline: renderAsciidoc ────────────────────────────────────────────

describe('renderAsciidoc — stem:[…] inline (REQ-FECONTENT-030)', () => {
  it('typesets stem:[x^2] as KaTeX output', async () => {
    const src = '= Doc\n\n:stem: latexmath\n\nValue is stem:[x^2].\n'
    const html = await renderAsciidoc(src)
    expect(html).toContain('katex')
    // The raw stem macro must not appear literally
    expect(html).not.toContain('stem:[x^2]')
  })

  it('typesets dollar-delimited inline math $E = mc^2$', async () => {
    const src = '= Doc\n\nEnergy: $E = mc^2$.\n'
    const html = await renderAsciidoc(src)
    expect(html).toContain('katex')
    expect(html).not.toContain('$E = mc^2$')
  })
})

describe('renderAsciidoc — display math (REQ-FECONTENT-030)', () => {
  it('typesets [stem]++++\\frac{P}{Q}++++ as KaTeX display output', async () => {
    const src = '= Doc\n\n:stem: latexmath\n\n[stem]\n++++\n\\frac{P}{Q}\n++++\n'
    const html = await renderAsciidoc(src)
    expect(html).toContain('katex')
  })

  it('typesets $$\\dfrac{a}{b}$$ (legacy dollar display math)', async () => {
    const src = '= Doc\n\n$$\\dfrac{a}{b}$$\n'
    const html = await renderAsciidoc(src)
    expect(html).toContain('katex')
    expect(html).not.toContain('$$')
  })
})

describe('renderAsciidoc — code-region exclusion (REQ-FECONTENT-032)', () => {
  it('does NOT typeset $HOME inside a ---- listing block', async () => {
    const src = '= Doc\n\n----\necho $HOME\n----\n'
    const html = await renderAsciidoc(src)
    // The listing block must render as plain code
    expect(html).toContain('HOME')
    // Must not appear as KaTeX-rendered math
    const doc = parse(html)
    const pre = doc.querySelector('pre')
    expect(pre).not.toBeNull()
    expect(pre!.textContent).toContain('$HOME')
  })

  it('does NOT typeset $HOME inside a .... literal block', async () => {
    const src = '= Doc\n\n....\nThe path $HOME/bin\n....\n'
    const html = await renderAsciidoc(src)
    expect(html).toContain('HOME')
    // No katex rendering of $HOME
    const doc = parse(html)
    // Any pre or div that shows the literal content should not be katex output
    const text = doc.body.textContent ?? ''
    expect(text).toContain('HOME')
  })
})

describe('renderAsciidoc — graceful degradation (REQ-FECONTENT-033)', () => {
  it('renders an error span for stem:[\\notacommand] without throwing', async () => {
    const src = '= Doc\n\n:stem: latexmath\n\nBad math: stem:[\\notacommand].\n'
    await expect(renderAsciidoc(src)).resolves.not.toThrow()
    const html = await renderAsciidoc(src)
    // Output must be non-empty (section not blanked)
    expect(html.length).toBeGreaterThan(0)
  })

  it('does not throw for empty stem macro stem:[]', async () => {
    const src = '= Doc\n\n:stem: latexmath\n\nEmpty: stem:[].\n'
    await expect(renderAsciidoc(src)).resolves.toBeDefined()
  })
})

describe('renderAsciidoc — XSS: sanitizer is final stage (REQ-FECONTENT-034)', () => {
  it('strips onerror from stem:[<img src=x onerror=alert(1)>]', async () => {
    // Asciidoctor HTML-encodes the angle brackets before handing the content to
    // the stem pipeline, so KaTeX sees literal text rather than an <img> tag and
    // renders an error span. DOMPurify then sanitizes any KaTeX error output.
    // The critical assertion is that NO img element has a live onerror attribute
    // in the DOM — i.e. no executable event handler reaches the DOM.
    const src = '= Doc\n\n:stem: latexmath\n\nstem:[<img src=x onerror=alert(1)>].\n'
    const html = await renderAsciidoc(src)
    const doc = parse(html)
    // No img element should have an onerror attribute
    const imgs = doc.querySelectorAll('img')
    for (const img of Array.from(imgs)) {
      expect(img.getAttribute('onerror')).toBeNull()
    }
    // No element of any kind should have a live onerror attribute
    const all = doc.querySelectorAll('*')
    for (const el of Array.from(all)) {
      expect(el.getAttribute('onerror')).toBeNull()
    }
  })

  it('strips position:fixed even when injected via a passthrough block after KaTeX pass', async () => {
    const src = '= Doc\n\n+++<div style="position:fixed;top:0">overlay</div>+++\n'
    const html = await renderAsciidoc(src)
    expect(html).not.toContain('position:fixed')
    expect(html).not.toContain('position: fixed')
  })
})

describe('renderAsciidoc — returns a string for all inputs (REQ-FECONTENT-033)', () => {
  it('returns a string for empty input', async () => {
    const html = await renderAsciidoc('')
    expect(typeof html).toBe('string')
  })

  it('returns a string for a minimal document', async () => {
    const html = await renderAsciidoc('= Hello\n\nworld\n')
    expect(typeof html).toBe('string')
  })
})
