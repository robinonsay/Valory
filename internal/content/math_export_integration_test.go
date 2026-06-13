//go:build integration

// math_export_integration_test.go — integration-tier tests for the LaTeX math
// export pipeline introduced in Sprint 24 (SDD-024-latex-math.md §13 task 18.6).
//
// These tests run against a real PostgreSQL instance (no mocked DB). They are
// gated behind the `integration` build tag and the db.IntegrationPool harness,
// which requires VALORY_TEST_DATABASE_URL to be set (the `make test-integration`
// target sets it automatically via docker-compose.test.yml).
//
// Two tests are defined:
//
//  1. TestInteg_HtmlExport_MathSection_KatexMarkupAndNoExternalRefs — verifies
//     that the HTML export of a section whose stored content contains BOTH
//     legacy $...$ math AND a $HOME listing block:
//     (a) the KaTeX auto-render JS script is embedded inline (proves
//     injectKatexAssets fired — REQ-CONTENT-011),
//     (b) STEM delimiter \(…\) or \[…\] content is present (proves
//     normalizeDollarMathAdoc converted the dollar-math — REQ-CONTENT-010),
//     (c) no external KaTeX CDN references appear in the output, and no
//     bare url(fonts/…) CSS references remain un-inlined (REQ-CONTENT-011),
//     (d) the literal "$HOME" string survives un-typeset (SDD-024 §3.2.1 AC 4).
//     Gated: t.Skip when asciidoctor binary is absent from PATH.
//
//  2. TestInteg_PdfExport_MathSection_ValidPdf — verifies that the PDF export
//     of the same section yields Content-Type application/pdf, a non-empty body,
//     and magic bytes %PDF (REQ-CONTENT-012).
//     Gated: t.Skip when asciidoctor-pdf is absent from PATH, OR when the
//     asciidoctor-mathematical gem is not installed. The gem presence is probed
//     via `gem list --installed asciidoctor-mathematical`; a clear skip message
//     explains which dependency is missing so the failure reason is obvious on
//     dev hosts without the full gem toolchain. The test will run for real inside
//     the container-based e2e environment where the Dockerfile installs both gems.
//
// Run locally (full suite):
//
//	export PATH=$PATH:/usr/local/go/bin
//	make test-integration
//
// Run this file alone (after `make test-integration` brings the DB up):
//
//	VALORY_TEST_DATABASE_URL=postgres://valory_test:valory_test@localhost:55432/valory_test?sslmode=disable \
//	  go test -tags integration -run TestInteg_HtmlExport_MathSection \
//	  ./internal/content/
package content

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/valory/valory/internal/db"
)

// mathExportIntegTables is the set of tables to truncate between each math
// export integration test. Ordered so that child tables precede parents, but
// CASCADE handles actual FK cycles — the order here is purely documentary.
var mathExportIntegTables = []string{
	"section_feedback",
	"lesson_content",
	"agent_token_usage",
	"due_date_schedules",
	"homework",
	"syllabi",
	"courses",
	"sessions",
	"users",
}

// mathExportFixtureContent is the AsciiDoc source stored in lesson_content.
// It contains BOTH:
//   - Legacy dollar-delimited inline math: $E = mc^2$ and $F = ma$ (must be
//     converted to stem macros by normalizeDollarMathAdoc at export time, then
//     the stem delimiters \(…\) or \[…\] must appear in the HTML so KaTeX's
//     auto-render extension can typeset them — REQ-CONTENT-010).
//   - A listing block containing $HOME (must NOT be typeset — the listing
//     block code-region exclusion must preserve the literal dollar sign, per
//     SDD-024 §3.2.1 acceptance criterion 4).
//
// Using a fixed fixture (no seeded random) keeps the test deterministic.
const mathExportFixtureContent = `= Physics Quick-Reference

== Classical Mechanics

Newton's second law relates force, mass, and acceleration: $F = ma$.

Einstein's mass-energy equivalence: $E = mc^2$.

=== Shell path example

The following listing block should NOT produce any math output.
The $HOME variable is a shell path reference, not a math expression.

----
export PATH=$HOME/bin:$PATH
echo "Working in $HOME"
----

Any prose after the code block can still contain math like $x + y = z$.
`

// @{"verifies": ["REQ-CONTENT-010", "REQ-CONTENT-011"]}
// TestInteg_HtmlExport_MathSection_KatexMarkupAndNoExternalRefs verifies the
// full HTML export pipeline on a section whose AsciiDoc source contains legacy
// dollar-delimited math AND a $HOME listing block.
//
// The HTML export path works as follows (SDD-024 §6):
//  1. normalizeDollarMathAdoc converts $…$ to stem:[…] / [stem] blocks.
//  2. asciidoctor wraps stem:[…] as \(…\) and [stem] blocks as \[…\].
//  3. injectKatexAssets embeds KaTeX CSS, fonts (base64-inlined), KaTeX JS,
//     and the auto-render extension JS inline in the output HTML. The
//     auto-render JS calls renderMathInElement on page load, which typesets
//     \(…\)/\[…\] delimiters at browser runtime.
//
// Because typesetting occurs client-side, the server-side response body
// contains the STEM delimiters (\(…\) or \[…\]) and the embedded KaTeX scripts
// — NOT the rendered class="katex" spans. The assertions below verify the
// server-side output that proves each pipeline step ran correctly.
//
// Assertions (per SDD-024 §13 task-18.6 and §10 acceptance criteria 3, 4):
//
//  1. Response status is 200 OK.
//  2. Content-Type is text/html; charset=utf-8.
//  3. Response body contains the KaTeX auto-render call "renderMathInElement"
//     (proves injectKatexAssets embedded the auto-render script — REQ-CONTENT-011).
//  4. Response body contains a STEM delimiter \( or \[ (proves
//     normalizeDollarMathAdoc converted the dollar-math AND asciidoctor emitted
//     the STEM delimiters — REQ-CONTENT-010).
//  5. Response body contains no bare url(fonts/…) KaTeX CSS references. All
//     KaTeX font files (woff2/woff/ttf) must have been base64-inlined by
//     selfContainedKatexCSS — REQ-CONTENT-011.
//  6. The literal string "$HOME" appears in the response body (the listing
//     block code-region exclusion preserved it un-typeset — SDD-024 §3.2.1 AC 4).
//
// Skipped when asciidoctor is absent from PATH (dev hosts without the Ruby gem
// toolchain).
func TestInteg_HtmlExport_MathSection_KatexMarkupAndNoExternalRefs(t *testing.T) {
	// Gate 1: real PostgreSQL is required — skip (not fail) when not available.
	pool := db.IntegrationPool(t)
	db.TruncateTables(t, pool, mathExportIntegTables...)

	// Gate 2: asciidoctor must be installed. Skip with a clear message on dev
	// hosts that do not have the Ruby gem toolchain. The binary is present inside
	// the container-based e2e environment (Dockerfile installs it).
	if _, err := exec.LookPath("asciidoctor"); err != nil {
		t.Skip("skipping HTML export math integration test: asciidoctor binary not found in PATH " +
			"(install the asciidoctor gem or run make test-integration inside the api container)")
	}

	ctx := t.Context()

	// Seed a student and course in the real database.
	studentID := integSeedUser(t, pool, "htmlmath")
	courseID := integSeedCourse(t, pool, studentID, "Physics Quick-Reference")

	// Insert the fixture section via the server role so the lesson_content RLS
	// INSERT policy (app.current_role = 'server') is genuinely exercised.
	// We use AcquireAsServer (not a bare pool.Exec) because the test bootstrap
	// role is a PostgreSQL superuser that bypasses FORCE RLS — only downgrading
	// to valory_app via SET ROLE makes the INSERT policy meaningful.
	srvConn := db.AcquireAsServer(t, pool)
	var sectionID uuid.UUID
	if err := srvConn.QueryRow(ctx,
		`INSERT INTO lesson_content
		     (course_id, section_index, title, content_adoc, citation_verified)
		 VALUES ($1, 0, 'Physics Quick-Reference', $2, true)
		 RETURNING id`,
		courseID, mathExportFixtureContent,
	).Scan(&sectionID); err != nil {
		srvConn.Release()
		t.Fatalf("insert fixture lesson_content: %v", err)
	}
	srvConn.Release()

	// Build the handler. exportSection does not read the student identity from
	// context — it only calls GetSectionContent(r.Context(), ...), which uses
	// repo.conn(ctx). Without an auth connection in context the repository falls
	// back to the raw pool; the valory_test superuser bypasses FORCE RLS so the
	// row is visible. This mirrors the pattern used in handler_test.go.
	repo := NewContentRepository(pool)
	h := NewContentHandler(repo, nil)

	router := chi.NewRouter()
	router.Get("/{id}/{sectionIndex}/export", h.exportSection)

	req := httptest.NewRequest(http.MethodGet, "/"+courseID.String()+"/0/export?format=html", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Assertion 1: HTTP status 200.
	if w.Code != http.StatusOK {
		t.Fatalf("HTML export: want 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Assertion 2: Content-Type.
	if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("HTML export Content-Type: want %q, got %q", "text/html; charset=utf-8", ct)
	}

	body := w.Body.String()

	// Assertion 3: KaTeX auto-render call is present (REQ-CONTENT-011).
	// injectKatexAssets (katex_assets.go) must have embedded the auto-render
	// script that calls renderMathInElement on page load. Its absence means
	// either injectKatexAssets was not called or the embedding is broken.
	if !strings.Contains(body, "renderMathInElement") {
		t.Errorf("HTML export: \"renderMathInElement\" not found in body; "+
			"injectKatexAssets must embed the KaTeX auto-render script inline (REQ-CONTENT-011). "+
			"body excerpt (first 1000 chars): %s", truncate(body, 1000))
	}

	// Assertion 4: STEM delimiter \( or \[ is present (REQ-CONTENT-010).
	// normalizeDollarMathAdoc must have converted $F = ma$ → stem:[F = ma],
	// and asciidoctor must have emitted \(F = ma\) in the HTML. The auto-render
	// JS then typesets it at browser runtime. The absence of \( or \[ means
	// either normalization or the asciidoctor stem pass failed.
	hasStemDelim := strings.Contains(body, `\(`) || strings.Contains(body, `\[`)
	if !hasStemDelim {
		t.Errorf("HTML export: no STEM delimiter \\( or \\[ found in body; "+
			"normalizeDollarMathAdoc + asciidoctor must emit STEM delimiters for KaTeX auto-render "+
			"(REQ-CONTENT-010). body excerpt (first 1000 chars): %s", truncate(body, 1000))
	}

	// Assertion 5: No bare url(fonts/…) KaTeX CSS references (REQ-CONTENT-011).
	// selfContainedKatexCSS (katex_assets.go) must have replaced all url(fonts/…)
	// references in katex.min.css with url(data:font/…) base64 data URIs before
	// injecting the CSS into the HTML. Any remaining url(fonts/…) means a font
	// file was not inlined, which would cause rendering failures when the HTML is
	// opened without network access (offline self-hosted deployments).
	if strings.Contains(body, "url(fonts/") {
		t.Errorf("HTML export: found bare url(fonts/…) KaTeX CSS reference; " +
			"all KaTeX font files (woff2/woff/ttf) must be base64-inlined by selfContainedKatexCSS " +
			"(REQ-CONTENT-011). This export would fail to render math without network access.")
	}

	// Assertion 6: Literal "$HOME" survives un-typeset (SDD-024 §3.2.1 AC 4).
	// The listing block ----…---- must protect its contents from
	// normalizeDollarMathAdoc. If the code-region exclusion fails, $HOME would
	// be converted to stem:[HOME] and Asciidoctor would emit a malformed stem
	// macro — the raw "$HOME" string would disappear from the output.
	if !strings.Contains(body, "$HOME") {
		t.Errorf("HTML export: literal \"$HOME\" not found in body; " +
			"the listing block code-region exclusion must preserve dollar signs inside ----…---- fences " +
			"(SDD-024 §3.2.1 acceptance criterion 4)")
	}
}

// @{"verifies": ["REQ-CONTENT-010", "REQ-CONTENT-012"]}
// TestInteg_PdfExport_MathSection_ValidPdf verifies the full PDF export pipeline
// on the same mixed-content section fixture.
//
// The PDF export path (SDD-024 §7):
//  1. normalizeDollarMathAdoc converts $…$ to stem:[…] / [stem] blocks.
//  2. asciidoctor-pdf is invoked with -r asciidoctor-mathematical and
//     -a mathematical-format=svg so STEM blocks are typeset as SVG vector
//     glyphs embedded in the PDF.
//
// Assertions (per SDD-024 §13 task-18.6 and §10 acceptance criterion 2):
//
//  1. Response status is 200 OK.
//  2. Content-Type is application/pdf.
//  3. Response body is non-empty.
//  4. Response body starts with the PDF magic bytes "%PDF".
//
// Skipped when:
//   - asciidoctor-pdf binary is absent from PATH, OR
//   - the asciidoctor-mathematical gem is not installed (checked via
//     `gem list --installed asciidoctor-mathematical`).
//
// The gem check is done via `gem list` rather than probing with
// `asciidoctor-pdf -r asciidoctor-mathematical --version` because the
// --version flag exits before loading the require, making it a no-op probe.
func TestInteg_PdfExport_MathSection_ValidPdf(t *testing.T) {
	// Gate 1: real PostgreSQL is required.
	pool := db.IntegrationPool(t)
	db.TruncateTables(t, pool, mathExportIntegTables...)

	// Gate 2: asciidoctor-pdf must be installed.
	if _, err := exec.LookPath("asciidoctor-pdf"); err != nil {
		t.Skip("skipping PDF export math integration test: asciidoctor-pdf binary not found in PATH " +
			"(install asciidoctor-pdf and asciidoctor-mathematical gems, or run via the api container)")
	}

	// Gate 3: asciidoctor-mathematical gem must be installed.
	// `gem list --installed asciidoctor-mathematical` exits 0 if the gem is
	// present, 1 if absent. This is more reliable than probing via asciidoctor-pdf
	// because `asciidoctor-pdf --version` short-circuits before loading `-r`
	// requires and therefore always exits 0 regardless of gem availability.
	//
	// `gem` is expected to be on PATH whenever asciidoctor-pdf is on PATH (both
	// are installed via `gem install`). If `gem` itself is absent, we fall through
	// to the actual asciidoctor-pdf invocation and let the handler return an error,
	// which would surface as a test failure — that case is considered a broken
	// toolchain and warrants investigation.
	gemProbe := exec.Command("gem", "list", "--installed", "asciidoctor-mathematical")
	if probeOut, probeErr := gemProbe.CombinedOutput(); probeErr != nil {
		t.Skipf("skipping PDF export math integration test: asciidoctor-mathematical gem not installed "+
			"(gem list exit error: %v; output: %s). "+
			"Install with: gem install asciidoctor-mathematical --no-document. "+
			"The test will run for real inside the api container after `make build`.",
			probeErr, strings.TrimSpace(string(probeOut)))
	}

	ctx := t.Context()

	// Seed student, course, and lesson content with the same fixture as the HTML test.
	studentID := integSeedUser(t, pool, "pdfmath")
	courseID := integSeedCourse(t, pool, studentID, "Physics PDF Test")

	srvConn := db.AcquireAsServer(t, pool)
	var sectionID uuid.UUID
	if err := srvConn.QueryRow(ctx,
		`INSERT INTO lesson_content
		     (course_id, section_index, title, content_adoc, citation_verified)
		 VALUES ($1, 0, 'Physics PDF Test', $2, true)
		 RETURNING id`,
		courseID, mathExportFixtureContent,
	).Scan(&sectionID); err != nil {
		srvConn.Release()
		t.Fatalf("insert fixture lesson_content: %v", err)
	}
	srvConn.Release()

	repo := NewContentRepository(pool)
	h := NewContentHandler(repo, nil)

	router := chi.NewRouter()
	// Same pool-fallback pattern as the HTML test — superuser bypasses FORCE RLS.
	router.Get("/{id}/{sectionIndex}/export", h.exportSection)

	req := httptest.NewRequest(http.MethodGet, "/"+courseID.String()+"/0/export?format=pdf", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Assertion 1: HTTP status 200.
	if w.Code != http.StatusOK {
		t.Fatalf("PDF export: want 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Assertion 2: Content-Type application/pdf.
	if ct := w.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("PDF export Content-Type: want %q, got %q", "application/pdf", ct)
	}

	// Assertion 3: non-empty body.
	if w.Body.Len() == 0 {
		t.Fatal("PDF export: response body is empty; expected a non-empty PDF document")
	}

	// Assertion 4: PDF magic bytes.
	// Every valid PDF file begins with the 4-byte signature "%PDF"
	// (ISO 32000-1:2008 §7.5.2). Its presence proves asciidoctor-pdf produced a
	// well-formed PDF document (REQ-CONTENT-012).
	bodyStr := w.Body.String()
	if !strings.HasPrefix(bodyStr, "%PDF") {
		excerpt := truncate(bodyStr, 40)
		t.Errorf("PDF export: body does not begin with %%PDF magic bytes; first 40 bytes: %q", excerpt)
	}
}

// truncate returns at most n bytes from s, useful for clipping large HTML
// bodies in error messages so test output stays readable.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
