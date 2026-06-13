//go:build testing

// normalize_test.go — unit tests for normalizeDollarMathAdoc and
// supporting helpers.
//
// Fixture set mirrors the SDD-024-latex-math.md §3.2 worked-examples table
// and the task-18.3 checklist requirements (§13).
package content

import (
	"strings"
	"testing"
)

// @{"verifies": ["REQ-CONTENT-010"]}
func TestNormalizeDollarMathAdoc_InlineMath(t *testing.T) {
	// Basic inline $…$ converts to stem:[…].
	input := "The formula is $E = mc^2$ for energy."
	got := normalizeDollarMathAdoc(input)
	if !strings.Contains(got, "stem:[E = mc^2]") {
		t.Errorf("want stem:[E = mc^2], got: %s", got)
	}
	if strings.Contains(got, "$E = mc^2$") {
		t.Errorf("raw dollar math must not remain in output, got: %s", got)
	}
}

// @{"verifies": ["REQ-CONTENT-010"]}
func TestNormalizeDollarMathAdoc_DisplayMath(t *testing.T) {
	// Display $$…$$ converts to a [stem] block.
	input := "See the decomposition:\n$$\\dfrac{P(x)}{(x+a)(x+b)} = \\dfrac{A}{x+a} + \\dfrac{B}{x+b}$$\nAbove is partial fractions."
	got := normalizeDollarMathAdoc(input)
	if !strings.Contains(got, "[stem]") {
		t.Errorf("want [stem] block, got: %s", got)
	}
	if !strings.Contains(got, "++++") {
		t.Errorf("want ++++ delimiters, got: %s", got)
	}
	if !strings.Contains(got, `\dfrac{P(x)}{(x+a)(x+b)}`) {
		t.Errorf("want inner expression preserved, got: %s", got)
	}
	if strings.Contains(got, "$$") {
		t.Errorf("raw $$ must not remain in output, got: %s", got)
	}
}

// @{"verifies": ["REQ-CONTENT-010"]}
func TestNormalizeDollarMathAdoc_DisplayBeforeInline(t *testing.T) {
	// Display $$ must be replaced before inline $ so that the single-$ pattern
	// cannot consume one delimiter of a $$ pair. The $$…$$ pair here must
	// produce a single stem block, not two inline stem macros.
	input := "$$x + y$$"
	got := normalizeDollarMathAdoc(input)

	// Should produce one stem block, not two occurrences of stem:[…].
	stemBlockCount := strings.Count(got, "[stem]")
	if stemBlockCount != 1 {
		t.Errorf("want exactly 1 [stem] block, got %d; output: %s", stemBlockCount, got)
	}
	inlineCount := strings.Count(got, "stem:[")
	if inlineCount != 0 {
		t.Errorf("want 0 stem:[…] inline macros, got %d; output: %s", inlineCount, got)
	}
}

// @{"verifies": ["REQ-CONTENT-010"]}
func TestNormalizeDollarMathAdoc_DollarInListingBlock(t *testing.T) {
	// $HOME inside a ----…---- listing block must NOT be converted. This is the
	// acceptance criterion 4 case from SDD-024-latex-math.md.
	input := "Before block.\n\n----\nexport PATH=$HOME/bin:$PATH\n----\n\nAfter block."
	got := normalizeDollarMathAdoc(input)
	if strings.Contains(got, "stem:[") {
		t.Errorf("dollar sign inside listing block must not produce stem macro; got: %s", got)
	}
	if !strings.Contains(got, "$HOME/bin") {
		t.Errorf("literal $HOME must be preserved inside listing block; got: %s", got)
	}
}

// @{"verifies": ["REQ-CONTENT-010"]}
func TestNormalizeDollarMathAdoc_DollarInLiteralBlock(t *testing.T) {
	// $HOME inside a ....….... literal block must NOT be converted.
	// This is the AC 4 case that the erroneous \\.{4,} regex in SDD rev 1
	// would have failed silently — the corrected \.{4,} is load-bearing here.
	input := "Before block.\n\n....\nexport PATH=$HOME/bin:$PATH\n....\n\nAfter block."
	got := normalizeDollarMathAdoc(input)
	if strings.Contains(got, "stem:[") {
		t.Errorf("dollar sign inside literal block must not produce stem macro; got: %s", got)
	}
	if !strings.Contains(got, "$HOME/bin") {
		t.Errorf("literal $HOME must be preserved inside literal block; got: %s", got)
	}
}

// @{"verifies": ["REQ-CONTENT-010"]}
func TestNormalizeDollarMathAdoc_DollarInInlineBacktick(t *testing.T) {
	// $HOME inside an inline backtick span must NOT be converted.
	input := "Run `echo $HOME` to see the path."
	got := normalizeDollarMathAdoc(input)
	if strings.Contains(got, "stem:[") {
		t.Errorf("dollar sign inside inline backtick must not produce stem macro; got: %s", got)
	}
	if !strings.Contains(got, "`echo $HOME`") {
		t.Errorf("literal `echo $HOME` must be preserved; got: %s", got)
	}
}

// @{"verifies": ["REQ-CONTENT-010"]}
func TestNormalizeDollarMathAdoc_AlreadyStem_Idempotent(t *testing.T) {
	// Content already using stem:[…] must pass through unmodified (no double-wrapping).
	input := "The energy equation is stem:[E = mc^2] by Einstein."
	got := normalizeDollarMathAdoc(input)
	if got != input {
		t.Errorf("idempotency: already-stem content must be unchanged;\nwant: %s\ngot:  %s", input, got)
	}
}

// @{"verifies": ["REQ-CONTENT-010"]}
func TestNormalizeDollarMathAdoc_AlreadyStemBlock_Idempotent(t *testing.T) {
	// Content already using a [stem] block must pass through unmodified.
	input := "[stem]\n++++\n\\dfrac{P(x)}{(x+a)(x+b)}\n++++\n"
	got := normalizeDollarMathAdoc(input)
	if got != input {
		t.Errorf("idempotency: already-stem block must be unchanged;\nwant: %s\ngot:  %s", input, got)
	}
}

// @{"verifies": ["REQ-CONTENT-010"]}
func TestNormalizeDollarMathAdoc_MultipleInlineExpressions(t *testing.T) {
	// Multiple inline math spans in the same paragraph all convert.
	input := "Solve for $x$ when $y = 0$ using the formula $z = x + y$."
	got := normalizeDollarMathAdoc(input)
	count := strings.Count(got, "stem:[")
	if count != 3 {
		t.Errorf("want 3 stem:[…] macros, got %d; output: %s", count, got)
	}
}

// @{"verifies": ["REQ-CONTENT-010"]}
func TestNormalizeDollarMathAdoc_ProseDollarNoMatch(t *testing.T) {
	// A lone dollar sign without a matching closing $ on the same line must
	// not be converted (the inline regex requires a non-empty match with no
	// newline). This guards against false-positive on prices like "$10 flat fee."
	input := "The service costs $10 flat fee."
	got := normalizeDollarMathAdoc(input)
	if strings.Contains(got, "stem:[") {
		t.Errorf("lone dollar sign must not produce stem macro; got: %s", got)
	}
}

// @{"verifies": ["REQ-CONTENT-010"]}
func TestNormalizeDollarMathAdoc_SixHyphenFence(t *testing.T) {
	// A six-hyphen fence (------) is also a valid listing block delimiter and
	// must exclude its contents from dollar conversion.
	input := "Text before.\n\n------\n$x = 1$\n------\n\nText after."
	got := normalizeDollarMathAdoc(input)
	if strings.Contains(got, "stem:[") {
		t.Errorf("dollar inside 6-hyphen fence must not produce stem macro; got: %s", got)
	}
}

// @{"verifies": ["REQ-CONTENT-010"]}
func TestNormalizeDollarMathAdoc_MixedCodeAndMath(t *testing.T) {
	// A document with both a code listing and inline math: code is excluded,
	// math in prose is converted.
	input := "Use the formula $E = mc^2$ in your calculation.\n\n----\nresult = $HOME/calc\n----\n\nOr use $F = ma$ instead."
	got := normalizeDollarMathAdoc(input)

	if !strings.Contains(got, "stem:[E = mc^2]") {
		t.Errorf("math in prose must be converted; got: %s", got)
	}
	if !strings.Contains(got, "stem:[F = ma]") {
		t.Errorf("math in prose after code block must be converted; got: %s", got)
	}
	if strings.Contains(got, "stem:[$HOME") {
		t.Errorf("dollar inside code block must not produce stem macro; got: %s", got)
	}
}

// @{"verifies": ["REQ-CONTENT-010"]}
func TestInjectStemHeader_AbsentHeader_Prepends(t *testing.T) {
	src := "= My Document\n\nSome content."
	got := injectStemHeader(src)
	if !strings.HasPrefix(got, ":stem: latexmath\n") {
		t.Errorf("want :stem: latexmath prepended; got: %s", got)
	}
}

// @{"verifies": ["REQ-CONTENT-010"]}
func TestInjectStemHeader_AlreadyPresent_Idempotent(t *testing.T) {
	src := ":stem: latexmath\n= My Document\n\nSome content."
	got := injectStemHeader(src)
	count := strings.Count(got, ":stem: latexmath")
	if count != 1 {
		t.Errorf("idempotency: want exactly 1 :stem: latexmath declaration, got %d; output: %s", count, got)
	}
}

// @{"verifies": ["REQ-CONTENT-010"]}
func TestNormalizeDollarMathAdoc_EmptyInput(t *testing.T) {
	if got := normalizeDollarMathAdoc(""); got != "" {
		t.Errorf("empty input must return empty string, got: %q", got)
	}
}

// @{"verifies": ["REQ-CONTENT-010"]}
func TestNormalizeDollarMathAdoc_DisplayMultiLine(t *testing.T) {
	// Multi-line $$…$$ must become a single [stem] block.
	input := "$$\n\\dfrac{P(x)}{(x+a)(x+b)} =\n\\dfrac{A}{x+a} + \\dfrac{B}{x+b}\n$$"
	got := normalizeDollarMathAdoc(input)
	if !strings.Contains(got, "[stem]") {
		t.Errorf("want [stem] block for multi-line display math; got: %s", got)
	}
	if strings.Contains(got, "$$") {
		t.Errorf("raw $$ must not remain; got: %s", got)
	}
}
