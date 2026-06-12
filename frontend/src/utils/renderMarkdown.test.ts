// @{"req": ["REQ-FECOURSE-612", "REQ-FECOURSE-613", "REQ-FECOURSE-614", "REQ-FECOURSE-615", "REQ-FECOURSE-616"]}

import { describe, it, expect } from 'vitest'
import { renderMarkdown } from './renderMarkdown'

// Helper: parse the output string into a DOM so we can use querySelector
function parse(html: string): Document {
  const doc = new DOMParser().parseFromString(`<body>${html}</body>`, 'text/html')
  return doc
}

describe('renderMarkdown — markdown rendering (REQ-FECOURSE-612)', () => {
  it('renders a heading', () => {
    const out = renderMarkdown('# Hello World')
    const doc = parse(out)
    expect(doc.querySelector('h1')?.textContent?.trim()).toBe('Hello World')
  })

  it('renders bold and italic emphasis', () => {
    const out = renderMarkdown('**bold** and _italic_')
    const doc = parse(out)
    expect(doc.querySelector('strong')?.textContent).toBe('bold')
    expect(doc.querySelector('em')?.textContent).toBe('italic')
  })

  it('renders an unordered list', () => {
    const out = renderMarkdown('- item one\n- item two\n- item three')
    const doc = parse(out)
    const items = doc.querySelectorAll('li')
    expect(items.length).toBe(3)
    expect(items[0].textContent?.trim()).toBe('item one')
  })

  it('renders an ordered list', () => {
    const out = renderMarkdown('1. first\n2. second')
    const doc = parse(out)
    const items = doc.querySelectorAll('li')
    expect(items.length).toBe(2)
    expect(items[1].textContent?.trim()).toBe('second')
  })

  it('renders an inline code span', () => {
    const out = renderMarkdown('Use `const x = 1` in JavaScript.')
    const doc = parse(out)
    expect(doc.querySelector('code')?.textContent).toBe('const x = 1')
  })

  it('renders a fenced code block', () => {
    const out = renderMarkdown('```js\nconst y = 2\n```')
    const doc = parse(out)
    // Should have a pre element containing code
    expect(doc.querySelector('pre code')).not.toBeNull()
  })

  it('renders a blockquote', () => {
    const out = renderMarkdown('> This is a quote.')
    const doc = parse(out)
    expect(doc.querySelector('blockquote')).not.toBeNull()
  })

  it('renders a table', () => {
    const src = '| A | B |\n|---|---|\n| 1 | 2 |'
    const out = renderMarkdown(src)
    const doc = parse(out)
    expect(doc.querySelector('table')).not.toBeNull()
    expect(doc.querySelector('th')).not.toBeNull()
    expect(doc.querySelector('td')).not.toBeNull()
  })
})

describe('renderMarkdown — LaTeX math rendering (REQ-FECOURSE-613)', () => {
  it('renders inline math $...$ and produces KaTeX output', () => {
    const out = renderMarkdown('The formula is $E = mc^2$ in physics.')
    // KaTeX produces .katex spans
    const doc = parse(out)
    expect(doc.querySelector('.katex')).not.toBeNull()
  })

  it('renders display math $$...$$ and produces KaTeX display output', () => {
    const out = renderMarkdown('$$\\sum_{i=0}^{n} i = \\frac{n(n+1)}{2}$$')
    const doc = parse(out)
    // KaTeX wraps display math in .katex-display
    expect(doc.querySelector('.katex-display')).not.toBeNull()
  })

  it('renders multiple inline math spans in one message', () => {
    const out = renderMarkdown('$a^2$ plus $b^2$ equals $c^2$')
    const doc = parse(out)
    const katexNodes = doc.querySelectorAll('.katex')
    expect(katexNodes.length).toBeGreaterThanOrEqual(3)
  })

  it('does not produce a script tag for invalid LaTeX (throwOnError:false)', () => {
    // \\badcommand is invalid LaTeX — KaTeX renders it as an error span, not throws
    const out = renderMarkdown('$\\badcommand{x}$')
    expect(out).not.toContain('<script')
    // Should still produce some KaTeX markup (error class)
    expect(out.toLowerCase()).toContain('katex')
  })
})

describe('renderMarkdown — image rendering (REQ-FECOURSE-614)', () => {
  it('renders a markdown image with an https src preserved', () => {
    const out = renderMarkdown('![alt text](https://example.com/diagram.png)')
    const doc = parse(out)
    const img = doc.querySelector('img')
    expect(img).not.toBeNull()
    expect(img?.getAttribute('src')).toBe('https://example.com/diagram.png')
  })

  it('adds loading="lazy" to images', () => {
    const out = renderMarkdown('![fig](https://example.com/fig.png)')
    const doc = parse(out)
    const img = doc.querySelector('img')
    expect(img?.getAttribute('loading')).toBe('lazy')
  })

  it('strips a javascript: src from an img tag in raw HTML (html:false test)', () => {
    // html:false means this raw tag is escaped, not passed through
    const out = renderMarkdown('<img src="javascript:alert(1)">')
    // The tag should appear as escaped text, not as an actual img element
    const doc = parse(out)
    const imgs = doc.querySelectorAll('img')
    for (const img of Array.from(imgs)) {
      const src = img.getAttribute('src') ?? ''
      expect(src).not.toMatch(/^javascript:/i)
    }
  })

  it('strips a javascript: src from an img URL even if somehow injected via markdown', () => {
    // Markdown image syntax with javascript: URL
    const out = renderMarkdown('![xss](javascript:alert(1))')
    const doc = parse(out)
    const imgs = doc.querySelectorAll('img')
    for (const img of Array.from(imgs)) {
      const src = img.getAttribute('src') ?? ''
      expect(src).not.toMatch(/^javascript:/i)
    }
  })
})

describe('renderMarkdown — XSS sanitization (REQ-FECOURSE-615)', () => {
  it('does NOT produce a <script> element from raw script injection attempt', () => {
    const out = renderMarkdown('<script>alert(1)</script>')
    // html:false escapes the tag; DOMPurify also removes it
    expect(out).not.toContain('<script')
  })

  it('does NOT execute onerror from a raw img tag (html:false escapes it)', () => {
    // html:false means markdown-it escapes the tag to &lt;img ...&gt; so the
    // browser never creates an img element — the content is safe text.
    // DOMPurify is also applied as a final guard.
    const out = renderMarkdown('<img src=x onerror=alert(1)>')
    const doc = parse(out)
    // No element in the parsed document should carry an onerror attribute
    const withOnerror = doc.querySelectorAll('[onerror]')
    expect(withOnerror.length).toBe(0)
    // If an img somehow exists, it must not have an onerror attribute
    const imgs = doc.querySelectorAll('img')
    for (const img of Array.from(imgs)) {
      expect(img.getAttribute('onerror')).toBeNull()
    }
  })

  it('strips javascript: href from a markdown link', () => {
    const out = renderMarkdown('[click me](javascript:alert(1))')
    const doc = parse(out)
    const anchors = doc.querySelectorAll('a')
    for (const a of Array.from(anchors)) {
      const href = a.getAttribute('href') ?? ''
      expect(href).not.toMatch(/^javascript:/i)
    }
  })

  it('strips on* attributes from any element', () => {
    // html:false escapes raw HTML to entity text, so <b onclick=...> becomes
    // literal text that the browser cannot execute.
    // DOMPurify removes any on* that somehow survive to DOM elements.
    // We verify by parsing the output: no element should have an on* attribute.
    const out = renderMarkdown('**hello** <b onclick="alert(1)">world</b>')
    const doc = parse(out)
    // No element in the parsed DOM should carry an onclick attribute
    const withOnclick = doc.querySelectorAll('[onclick]')
    expect(withOnclick.length).toBe(0)
    // The strong from **hello** must still render normally
    expect(doc.querySelector('strong')).not.toBeNull()
  })

  it('does not allow script injection inside a LaTeX edge case', () => {
    // The LaTeX source contains HTML markup — KaTeX renders it as an error or
    // ignores it; DOMPurify then strips any residual active content.
    const out = renderMarkdown('$$\\text{<img onerror=alert(1)>}$$')
    const doc = parse(out)
    const imgs = doc.querySelectorAll('img')
    for (const img of Array.from(imgs)) {
      expect(img.getAttribute('onerror')).toBeNull()
    }
    expect(out).not.toContain('<script')
  })

  it('never emits an iframe element', () => {
    const out = renderMarkdown('<iframe src="https://evil.com"></iframe>')
    expect(out).not.toContain('<iframe')
    expect(out).not.toContain('</iframe>')
  })

  it('never emits a form element', () => {
    const out = renderMarkdown('<form action="https://evil.com"><input name="x"></form>')
    expect(out).not.toContain('<form')
  })

  it('never emits an object element', () => {
    const out = renderMarkdown('<object data="https://evil.com/plugin.swf"></object>')
    expect(out).not.toContain('<object')
  })

  it('never emits an embed element', () => {
    const out = renderMarkdown('<embed src="https://evil.com/plugin.swf">')
    expect(out).not.toContain('<embed')
  })

  it('preserves a legitimate https link', () => {
    const out = renderMarkdown('[visit](https://example.com)')
    const doc = parse(out)
    const a = doc.querySelector('a')
    expect(a?.getAttribute('href')).toBe('https://example.com')
  })
})

describe('renderMarkdown — tightened data: URI allowlist (Fix 2)', () => {
  it('allows data:image/png;base64, URI for an img src', () => {
    // Construct a minimal valid base64 data URI (1x1 transparent PNG)
    const pngDataUri = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=='
    const out = renderMarkdown(`![tiny](${pngDataUri})`)
    const doc = parse(out)
    const img = doc.querySelector('img')
    expect(img).not.toBeNull()
    expect(img?.getAttribute('src')).toBe(pngDataUri)
  })

  it('allows data:image/jpeg;base64, URI', () => {
    const jpegDataUri = 'data:image/jpeg;base64,/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAAgGBg=='
    const out = renderMarkdown(`![photo](${jpegDataUri})`)
    const doc = parse(out)
    const img = doc.querySelector('img')
    // src is preserved (not stripped)
    expect(img?.getAttribute('src')).toBe(jpegDataUri)
  })

  it('strips data:image/svg+xml src — SVG can contain script', () => {
    const svgDataUri = 'data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciPjxzY3JpcHQ+YWxlcnQoMSk8L3NjcmlwdD48L3N2Zz4='
    const out = renderMarkdown(`![svg](${svgDataUri})`)
    const doc = parse(out)
    const imgs = doc.querySelectorAll('img')
    for (const img of Array.from(imgs)) {
      const src = img.getAttribute('src') ?? ''
      expect(src).not.toMatch(/^data:image\/svg/i)
    }
  })

  it('strips data:text/html src', () => {
    const htmlDataUri = 'data:text/html,<script>alert(1)</script>'
    const out = renderMarkdown(`![x](${htmlDataUri})`)
    const doc = parse(out)
    const imgs = doc.querySelectorAll('img')
    for (const img of Array.from(imgs)) {
      const src = img.getAttribute('src') ?? ''
      expect(src).not.toMatch(/^data:text/i)
    }
  })

  it('strips a bare data:image/ URI without a known subtype', () => {
    // data:image/ without a known subtype like png/jpeg/gif/webp must be stripped
    const badUri = 'data:image/tiff;base64,AAAA'
    const out = renderMarkdown(`![x](${badUri})`)
    const doc = parse(out)
    const imgs = doc.querySelectorAll('img')
    for (const img of Array.from(imgs)) {
      const src = img.getAttribute('src') ?? ''
      expect(src).not.toMatch(/^data:image\/tiff/i)
    }
  })
})

describe('renderMarkdown — code-aware math extraction (Fix 3)', () => {
  it('does not treat $HOME and $USER inside a fenced bash block as math', () => {
    const src = '```bash\necho $HOME and $USER\n```'
    const out = renderMarkdown(src)
    const doc = parse(out)
    // The pre>code block must contain literal $HOME and $USER text
    const code = doc.querySelector('pre code')
    expect(code).not.toBeNull()
    expect(code?.textContent).toContain('$HOME')
    expect(code?.textContent).toContain('$USER')
    // Must not have produced KaTeX output for the variables
    expect(out).not.toContain('katex')
  })

  it('does not treat dollar signs inside a tilde-fenced block as math', () => {
    const src = '~~~sh\ncd $PROJECT_ROOT\n~~~'
    const out = renderMarkdown(src)
    const doc = parse(out)
    const code = doc.querySelector('pre code')
    expect(code?.textContent).toContain('$PROJECT_ROOT')
    expect(out).not.toContain('katex')
  })

  it('keeps $x$ literal inside an inline code span while $y^2$ outside renders as KaTeX', () => {
    const src = 'Use `$x$` for math notation, but $y^2$ is the formula.'
    const out = renderMarkdown(src)
    const doc = parse(out)
    // The inline code span must contain literal $x$
    const codeEl = doc.querySelector('code')
    expect(codeEl?.textContent).toContain('$x$')
    // The $y^2$ outside the code span must have been typeset by KaTeX
    expect(doc.querySelector('.katex')).not.toBeNull()
  })

  it('renders display math $$...$$ adjacent to a code block correctly', () => {
    const src = '```bash\necho $HOME\n```\n\n$$\\frac{a}{b}$$'
    const out = renderMarkdown(src)
    const doc = parse(out)
    // The bash code block keeps $HOME literal
    const code = doc.querySelector('pre code')
    expect(code?.textContent).toContain('$HOME')
    // The display math after the code block renders as KaTeX display
    expect(doc.querySelector('.katex-display')).not.toBeNull()
  })

  it('normal inline math outside code still typesets', () => {
    const src = 'The area is $\\pi r^2$.'
    const out = renderMarkdown(src)
    const doc = parse(out)
    expect(doc.querySelector('.katex')).not.toBeNull()
  })
})

describe('renderMarkdown — U+E000/U+E001 input stripping (Fix 4)', () => {
  it('strips bare U+E000 characters from input before rendering', () => {
    // U+E000 is the PUA_OPEN placeholder delimiter; if present in input it
    // could collide with the math slot lookup table
    const src = 'hello  world'
    const out = renderMarkdown(src)
    // The output must not contain U+E000 in any form
    expect(out).not.toContain('')
  })

  it('strips bare U+E001 characters from input before rendering', () => {
    const src = 'test  content'
    const out = renderMarkdown(src)
    expect(out).not.toContain('')
  })

  it('strips a crafted U+E000/U+E001 sequence that mimics a placeholder', () => {
    // A crafted input with PUA_OPEN + "M0" + PUA_CLOSE that has no corresponding
    // slot must not produce broken output or throw
    const src = 'M0 is not a valid slot'
    const out = renderMarkdown(src)
    expect(out).not.toContain('')
    expect(out).not.toContain('')
  })

  it('renders normally when input has no PUA characters', () => {
    const out = renderMarkdown('# Clean input\n\nNo special characters.')
    const doc = parse(out)
    expect(doc.querySelector('h1')?.textContent?.trim()).toBe('Clean input')
  })
})
