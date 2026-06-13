// katex_assets.go — embeds KaTeX distribution assets and provides
// injectKatexAssets which makes an exported HTML file fully self-contained
// (no external network references required to render math).
//
// KaTeX assets are committed to the repository (copied once from
// frontend/node_modules/katex/dist/, katex ^0.17.0) because //go:embed
// resolves from the source tree at build time — a fresh clone must build
// without node_modules present. Re-copy these files when upgrading the
// katex npm dependency. The directory layout mirrors the dist layout KaTeX
// expects: fonts/ sub-directory for woff2/woff/ttf files.
//
// @{"req": ["REQ-CONTENT-011"]}
package content

import (
	"bytes"
	"embed"
	"encoding/base64"
	"io/fs"
	"regexp"
	"strings"
	"sync"
)

//go:embed assets/katex.min.css
var katexCSS []byte

//go:embed assets/katex.min.js
var katexJS []byte

//go:embed assets/auto-render.min.js
var autoRenderJS []byte

//go:embed assets/fonts
var fontsFS embed.FS

// fontRefRe matches url(fonts/SomeFont.woff2) (and .woff, .ttf) references
// inside the KaTeX CSS. The reference may optionally be quoted with single or
// double quotes. Group 1 captures the bare path relative to the fonts/ prefix:
// e.g. "KaTeX_Main-Regular.woff2".
//
// The replacement rewrites these to data URIs so the exported HTML file has
// no external font dependencies.
var fontRefRe = regexp.MustCompile(`url\(["']?fonts/([^)"']+)["']?\)`)

// fontMIME maps file extensions to their MIME type strings for data URIs.
var fontMIME = map[string]string{
	"woff2": "font/woff2",
	"woff":  "font/woff",
	"ttf":   "font/truetype",
}

// selfContainedKatexCSS returns the KaTeX CSS with all url(fonts/…) references
// replaced by base64 data URIs. The rewrite inflates ~24 KB of CSS to ~1.4 MB
// (60 base64-inlined fonts), so it is computed once and cached via sync.Once
// rather than per export request.
//
// @{"req": ["REQ-CONTENT-011"]}
func selfContainedKatexCSS() ([]byte, error) {
	cssOnce.Do(func() {
		cachedCSS, cachedCSSErr = computeSelfContainedKatexCSS()
	})
	return cachedCSS, cachedCSSErr
}

var (
	cssOnce      sync.Once
	cachedCSS    []byte
	cachedCSSErr error
)

// @{"req": ["REQ-CONTENT-011"]}
func computeSelfContainedKatexCSS() ([]byte, error) {
	css := katexCSS
	var replaceErr error

	css = fontRefRe.ReplaceAllFunc(css, func(match []byte) []byte {
		if replaceErr != nil {
			return match
		}

		sub := fontRefRe.FindSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		filename := string(sub[1])

		fontData, err := fs.ReadFile(fontsFS, "assets/fonts/"+filename)
		if err != nil {
			// Font file missing from embed — return the original reference so
			// the CSS is still valid even if that font variant is unavailable.
			// Log suppressed here; the caller logs the error if desired.
			replaceErr = err
			return match
		}

		// Determine MIME type from extension.
		ext := filename[strings.LastIndex(filename, ".")+1:]
		mime, ok := fontMIME[ext]
		if !ok {
			mime = "application/octet-stream"
		}

		encoded := base64.StdEncoding.EncodeToString(fontData)
		return []byte("url(data:" + mime + ";base64," + encoded + ")")
	})

	if replaceErr != nil {
		return nil, replaceErr
	}
	return css, nil
}

// injectKatexAssets takes the raw HTML output from asciidoctor and returns a
// fully self-contained HTML file that renders STEM math without any external
// network calls.
//
// It:
//  1. Replaces the closing </head> tag with an inline <style> block containing
//     the KaTeX CSS with all font url() references base64-inlined.
//  2. Appends inline <script> blocks for katex.min.js and auto-render.min.js
//     before </body>, followed by the auto-render invocation that typesets
//     \(…\) / \[…\] delimiters produced by Asciidoctor's stem processing.
//
// If the HTML does not contain </head> or </body> (e.g. asciidoctor fragment
// output), the style block is prepended and the scripts are appended.
//
// @{"req": ["REQ-CONTENT-011"]}
func injectKatexAssets(html []byte) ([]byte, error) {
	inlinedCSS, err := selfContainedKatexCSS()
	if err != nil {
		return nil, err
	}

	styleBlock := []byte("<style>\n" + string(inlinedCSS) + "\n</style>")
	katexScript := []byte("<script>\n" + string(katexJS) + "\n</script>")
	autoRenderScript := []byte("<script>\n" + string(autoRenderJS) + "\n</script>")

	// The auto-render invocation: call renderMathInElement on document.body
	// with the same delimiters that Asciidoctor stem processing emits.
	// throwOnError:false degrades gracefully (shows error text, never throws).
	autoRenderCall := []byte(`<script>
document.addEventListener("DOMContentLoaded", function() {
  renderMathInElement(document.body, {
    delimiters: [
      {left: "\\[", right: "\\]", display: true},
      {left: "\\(", right: "\\)", display: false}
    ],
    throwOnError: false
  });
});
</script>`)

	// Inject style before </head>.
	headClose := []byte("</head>")
	if idx := bytes.Index(html, headClose); idx != -1 {
		var buf bytes.Buffer
		buf.Write(html[:idx])
		buf.Write(styleBlock)
		buf.WriteByte('\n')
		buf.Write(html[idx:])
		html = buf.Bytes()
	} else {
		// Fragment output: prepend the style block.
		html = append(styleBlock, html...)
	}

	// Inject scripts before </body>.
	bodyClose := []byte("</body>")
	if idx := bytes.Index(html, bodyClose); idx != -1 {
		var buf bytes.Buffer
		buf.Write(html[:idx])
		buf.Write(katexScript)
		buf.WriteByte('\n')
		buf.Write(autoRenderScript)
		buf.WriteByte('\n')
		buf.Write(autoRenderCall)
		buf.WriteByte('\n')
		buf.Write(html[idx:])
		html = buf.Bytes()
	} else {
		// Fragment output: append the scripts.
		html = append(html, katexScript...)
		html = append(html, '\n')
		html = append(html, autoRenderScript...)
		html = append(html, '\n')
		html = append(html, autoRenderCall...)
	}

	return html, nil
}
