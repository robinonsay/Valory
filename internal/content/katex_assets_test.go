//go:build testing

// katex_assets_test.go — unit tests for the KaTeX asset injection pipeline.
//
// Tests verify:
//  1. No external url() references remain in the CSS after inlining (all
//     font/… paths become data URIs).
//  2. No <script src=…> tags are present in the injected output (assets are
//     inline, not linked).
//  3. The KaTeX <style> and <script> blocks are present in the output.
package content

import (
	"strings"
	"testing"
)

// @{"verifies": ["REQ-CONTENT-011"]}
func TestSelfContainedKatexCSS_NoExternalFontURL(t *testing.T) {
	css, err := selfContainedKatexCSS()
	if err != nil {
		t.Fatalf("selfContainedKatexCSS: %v", err)
	}

	cssStr := string(css)

	// After inlining, no url(fonts/…) references must remain. All font
	// references must have been replaced with url(data:…) data URIs.
	if strings.Contains(cssStr, "url(fonts/") {
		t.Error("selfContainedKatexCSS: output still contains url(fonts/…) external references; all must be base64-inlined")
	}

	// At least some data URIs must be present (proves inlining occurred, not
	// that the function merely stripped the references).
	if !strings.Contains(cssStr, "url(data:font/") {
		t.Error("selfContainedKatexCSS: expected url(data:font/…) data URIs to be present; none found")
	}
}

// @{"verifies": ["REQ-CONTENT-011"]}
func TestInjectKatexAssets_NoExternalFontURL(t *testing.T) {
	sampleHTML := []byte(`<!DOCTYPE html>
<html>
<head><title>Test</title></head>
<body><p class="stemblock">\[E = mc^2\]</p></body>
</html>`)

	result, err := injectKatexAssets(sampleHTML)
	if err != nil {
		t.Fatalf("injectKatexAssets: %v", err)
	}

	resultStr := string(result)

	// No external font url() references in the output.
	if strings.Contains(resultStr, "url(fonts/") {
		t.Error("injectKatexAssets: output still contains url(fonts/…) external references")
	}
}

// @{"verifies": ["REQ-CONTENT-011"]}
func TestInjectKatexAssets_NoScriptSrc(t *testing.T) {
	// All KaTeX JS must be inline — no <script src=…> external references.
	sampleHTML := []byte(`<!DOCTYPE html>
<html>
<head><title>Test</title></head>
<body><p>\(x^2\)</p></body>
</html>`)

	result, err := injectKatexAssets(sampleHTML)
	if err != nil {
		t.Fatalf("injectKatexAssets: %v", err)
	}

	resultStr := string(result)
	if strings.Contains(resultStr, `<script src=`) {
		t.Error("injectKatexAssets: output contains <script src=…>; all scripts must be inline")
	}
}

// @{"verifies": ["REQ-CONTENT-011"]}
func TestInjectKatexAssets_ContainsStyleAndScript(t *testing.T) {
	sampleHTML := []byte(`<!DOCTYPE html>
<html>
<head><title>Test</title></head>
<body><p>\(x^2\)</p></body>
</html>`)

	result, err := injectKatexAssets(sampleHTML)
	if err != nil {
		t.Fatalf("injectKatexAssets: %v", err)
	}

	resultStr := string(result)

	if !strings.Contains(resultStr, "<style>") {
		t.Error("injectKatexAssets: output must contain an inline <style> block")
	}
	if !strings.Contains(resultStr, "<script>") {
		t.Error("injectKatexAssets: output must contain inline <script> blocks")
	}
	// The auto-render invocation must be present.
	if !strings.Contains(resultStr, "renderMathInElement") {
		t.Error("injectKatexAssets: output must contain renderMathInElement auto-render call")
	}
}

// @{"verifies": ["REQ-CONTENT-011"]}
func TestInjectKatexAssets_StyleBeforeHeadClose(t *testing.T) {
	// The <style> block must be injected before </head> so the CSS is in the
	// document head as expected by browsers.
	sampleHTML := []byte(`<!DOCTYPE html>
<html>
<head><title>Test</title></head>
<body></body>
</html>`)

	result, err := injectKatexAssets(sampleHTML)
	if err != nil {
		t.Fatalf("injectKatexAssets: %v", err)
	}

	resultStr := string(result)
	styleIdx := strings.Index(resultStr, "<style>")
	headCloseIdx := strings.Index(resultStr, "</head>")

	if styleIdx == -1 {
		t.Fatal("injectKatexAssets: <style> block not found")
	}
	if headCloseIdx == -1 {
		t.Fatal("injectKatexAssets: </head> not found")
	}
	if styleIdx > headCloseIdx {
		t.Errorf("injectKatexAssets: <style> (at %d) must appear before </head> (at %d)", styleIdx, headCloseIdx)
	}
}

// @{"verifies": ["REQ-CONTENT-011"]}
func TestInjectKatexAssets_FragmentInput_NoPanic(t *testing.T) {
	// Fragment HTML without <head> or <body> must not panic; assets are
	// prepended/appended instead.
	fragment := []byte(`<div class="stemblock">\[E = mc^2\]</div>`)
	result, err := injectKatexAssets(fragment)
	if err != nil {
		t.Fatalf("injectKatexAssets on fragment: %v", err)
	}
	if len(result) == 0 {
		t.Error("injectKatexAssets on fragment: result must be non-empty")
	}
}
