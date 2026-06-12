// @{"req": ["REQ-FECOURSE-629", "REQ-FECONTENT-225"]}

// renderAsciidoc converts an AsciiDoc source string to sanitized HTML.
//
// @asciidoctor/core is loaded via dynamic import so it lands in its own chunk
// and is never pulled into the main bundle. Views that display AsciiDoc pay
// the load cost; all other views do not.
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
let asciidoctorInstancePromise: Promise<ReturnType<typeof import('@asciidoctor/core')['default']>> | null = null

// @{"req": ["REQ-FECOURSE-629", "REQ-FECONTENT-225"]}
function getAsciidoctor(): typeof asciidoctorInstancePromise {
  if (!asciidoctorInstancePromise) {
    asciidoctorInstancePromise = import('@asciidoctor/core').then(mod => {
      // The default export is a factory function; calling it returns the
      // processor. Support both default-exported factory and named exports.
      const factory = mod.default ?? mod
      return typeof factory === 'function' ? factory() : factory
    })
  }
  return asciidoctorInstancePromise
}

// @{"req": ["REQ-FECOURSE-629", "REQ-FECONTENT-225"]}
export async function renderAsciidoc(source: string): Promise<string> {
  const asciidoctor = await getAsciidoctor()

  // Convert with:
  //   standalone:false — emit a fragment (no <html><body> wrapper)
  //   safe:'secure'    — disable include macros, attribute-value shell escapes,
  //                      and URI reads so untrusted source cannot exfiltrate data
  //   showtitle        — render the level-0 document title (= Title) as an <h1>
  //   icons:font       — use font icons rather than image files (no img src requests)
  const html = asciidoctor.convert(source, {
    standalone: false,
    safe: 'secure',
    attributes: {
      showtitle: true,
      icons: 'font',
      // Disable the source highlighter to avoid any dynamic asset loading
      'source-highlighter': '',
    },
  }) as string

  // sanitizeHtml is the security boundary: it strips anything that survived
  // the converter, including passthrough block payloads.
  return sanitizeHtml(html)
}
