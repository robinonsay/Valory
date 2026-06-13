// normalize.go — render-time dollar-to-STEM normalization for AsciiDoc content.
//
// This file deliberately duplicates logic from the TypeScript normalizeDollarMath
// function in frontend/src/utils/renderAsciidoc.ts. The duplication is intentional
// per SDD-024-latex-math.md §9: the algorithms are short, the AsciiDoc code-region
// markers differ from Markdown fenced blocks, and a shared cross-language abstraction
// would cost more than it saves. Both implementations must stay in sync if the
// normalization algorithm changes.
package content

import (
	"regexp"
	"strings"
)

// fenceOpenRe matches the start of an AsciiDoc delimited block:
//   - four or more hyphens (listing block ----) or
//   - four or more dots   (literal block ....)
//
// anchored at the start of a line. The match stops at the first newline so that
// we capture only the fence line itself and can look for the matching close.
//
// Note: \.{4,} is four or more literal dots — NOT \\.{4,} which would require
// a leading backslash and would silently miss ....‑fenced literal blocks.
// (Rev 2 correction from SDD-024-latex-math.md §3.2.1.)
var fenceOpenRe = regexp.MustCompile(`(?m)^(-{4,}|\.{4,})[ \t]*$`)

// backtickOpenRe matches an inline backtick span opener: one or more backticks
// not at the start of a line (to avoid mistaking a block fence for an inline span).
var backtickOpenRe = regexp.MustCompile("`+")

// displayMathRe matches $$…$$ display math spans (possibly multi-line, non-greedy).
var displayMathRe = regexp.MustCompile(`(?s)\$\$(.+?)\$\$`)

// inlineMathRe matches $…$ inline math spans (single line, non-empty, no newline).
var inlineMathRe = regexp.MustCompile(`\$([^\n$]+?)\$`)

// stemHeaderRe detects an existing :stem: latexmath declaration so that
// injectStemHeader is idempotent.
var stemHeaderRe = regexp.MustCompile(`(?m)^:stem:\s*latexmath`)

// normalizeDollarMathAdoc converts dollar-delimited math in raw AsciiDoc source
// to AsciiDoc STEM notation so that Asciidoctor can wrap it in \(…\) / \[…\]
// delimiters for KaTeX to typeset.
//
// The algorithm mirrors the TypeScript normalizeDollarMath in renderAsciidoc.ts
// (SDD-024-latex-math.md §3.2). Go's regexp/RE2 engine does not support
// backreferences, so code-region exclusion is implemented with a scanner that
// finds fence openers and scans forward for a matching close fence, rather than
// relying on a \1 backreference.
//
// Excluded regions (passed through unmodified, dollar signs never scanned):
//   - Delimited listing blocks:  ----  …  ----  (4 or more hyphens)
//   - Delimited literal blocks:  ....  …  ....  (4 or more dots)
//   - Inline backtick spans:     `…`  (one or more backticks, any count)
//
// Within each non-code segment:
//  1. Replace $$…$$ display math with [stem]\n++++\n…\n++++\n blocks.
//  2. Replace $…$ inline math with stem:[…] macros.
//     Display is replaced before inline so that the single-$ pattern cannot
//     consume one delimiter of a $$ pair.
//
// Idempotent on content already written as STEM macros: stem:[…] and [stem]
// blocks contain no bare $, so they are unaffected by either replacement.
//
// @{"req": ["REQ-CONTENT-010"]}
func normalizeDollarMathAdoc(src string) string {
	if src == "" {
		return src
	}

	// splitCodeRegions splits src into alternating non-code / code-verbatim
	// segments. Index 0 is always non-code (possibly empty). Odd indices are
	// code-verbatim. We use a manual scanner so we can match the closing fence
	// to the exact opening fence text without backreferences.
	segments := splitCodeRegions(src)

	var buf strings.Builder
	buf.Grow(len(src))

	for i, seg := range segments {
		if i%2 == 0 {
			// Non-code: apply dollar-to-stem conversion.
			buf.WriteString(convertDollarMath(seg))
		} else {
			// Code verbatim: pass through unmodified.
			buf.WriteString(seg)
		}
	}

	return buf.String()
}

// splitCodeRegions returns src split into alternating non-code (even index) and
// code-verbatim (odd index) segments.
//
// Code-verbatim segments are:
//   - Delimited AsciiDoc blocks opened by ----… or ....… (and closed by the
//     same fence text on its own line). If no closing fence is found, the rest
//     of the document is treated as code (conservative: prevents stray $ from
//     being converted after an unclosed fence).
//   - Inline backtick spans: one or more backticks followed by the same count
//     of backticks. If no closing backtick group is found on the same logical
//     span, the opener is not treated as code (backtick runs at line start that
//     are not block fences are skipped).
//
// @{"req": ["REQ-CONTENT-010"]}
func splitCodeRegions(src string) []string {
	var result []string
	pos := 0

	for pos < len(src) {
		// Find next fence opener or backtick run.
		fenceLocs := fenceOpenRe.FindStringIndex(src[pos:])
		backtickLocs := backtickOpenRe.FindStringIndex(src[pos:])

		// Determine which comes first.
		fenceStart := -1
		if fenceLocs != nil {
			fenceStart = pos + fenceLocs[0]
		}
		backtickStart := -1
		if backtickLocs != nil {
			backtickStart = pos + backtickLocs[0]
		}

		if fenceStart == -1 && backtickStart == -1 {
			// No more code regions; append remainder as non-code and stop.
			result = append(result, src[pos:])
			return result
		}

		// If both found, process the earlier one first; tie goes to fence.
		if backtickStart == -1 || (fenceStart != -1 && fenceStart <= backtickStart) {
			// Delimited block: fence is at src[fenceStart : fenceStart+fenceLocs[1]-fenceLocs[0]+pos] — wait,
			// fenceLocs is relative to src[pos:], so actual positions are:
			relStart := fenceLocs[0]
			relEnd := fenceLocs[1]
			fenceText := src[pos+relStart : pos+relEnd]

			// Non-code segment before the fence.
			result = append(result, src[pos:pos+relStart])

			// Find the closing fence: same fence text on its own line after the
			// opening newline. Scan from the character after the fence line's newline.
			fenceLineEnd := pos + relEnd
			// fenceLineEnd already points past the \n because fenceOpenRe anchors
			// at end-of-line; advance past the \n if present.
			if fenceLineEnd < len(src) && src[fenceLineEnd] == '\n' {
				fenceLineEnd++
			}

			closeIdx := findClosingFence(src, fenceLineEnd, fenceText)
			if closeIdx == -1 {
				// No closing fence found: treat rest of document as code verbatim
				// (conservative; prevents stray $ from being converted after an
				// unclosed fence — matches AsciiDoc parser behavior for malformed docs).
				result = append(result, src[pos+relStart:])
				return result
			}

			// closeIdx is the position of the start of the closing fence line.
			// The closing fence line itself ends at closeIdx + len(fenceText); we
			// want to include the trailing newline (if any) in the verbatim segment.
			closeEnd := closeIdx + len(fenceText)
			if closeEnd < len(src) && src[closeEnd] == '\n' {
				closeEnd++
			}

			result = append(result, src[pos+relStart:closeEnd])
			pos = closeEnd
		} else {
			// Inline backtick span.
			relStart := backtickLocs[0]
			relEnd := backtickLocs[1]
			openerText := src[pos+relStart : pos+relEnd]

			// Non-code segment before the backtick run.
			result = append(result, src[pos:pos+relStart])

			// Find the closing backtick run of the same length after the opener.
			afterOpener := pos + relEnd
			closeIdx := strings.Index(src[afterOpener:], openerText)
			if closeIdx == -1 {
				// No closing backtick found; treat the opener as non-code text and
				// advance past it so we don't loop forever.
				// Pop the non-code segment we just appended and extend it instead.
				last := len(result) - 1
				result[last] = result[last] + openerText
				pos = pos + relEnd
			} else {
				closeEnd := afterOpener + closeIdx + len(openerText)
				result = append(result, src[pos+relStart:closeEnd])
				pos = closeEnd
			}
		}
	}

	return result
}

// findClosingFence searches src[from:] for a line containing only fenceText
// (plus optional trailing spaces/tabs) and returns the byte offset of the
// start of that line within src. Returns -1 if not found.
//
// @{"req": ["REQ-CONTENT-010"]}
func findClosingFence(src string, from int, fenceText string) int {
	search := src[from:]
	for {
		idx := strings.Index(search, fenceText)
		if idx == -1 {
			return -1
		}

		// The fence must start at the beginning of a line (idx == 0 or
		// preceded by \n) and end at the end of a line (followed by optional
		// whitespace and then \n or EOF).
		lineStart := from + idx
		atLineStart := lineStart == 0 || src[lineStart-1] == '\n'
		afterFence := lineStart + len(fenceText)

		// Allow trailing spaces/tabs before end-of-line.
		trailingEnd := afterFence
		for trailingEnd < len(src) && (src[trailingEnd] == ' ' || src[trailingEnd] == '\t') {
			trailingEnd++
		}
		atLineEnd := trailingEnd == len(src) || src[trailingEnd] == '\n'

		if atLineStart && atLineEnd {
			return lineStart
		}

		// Advance past this non-fence occurrence and keep searching.
		search = search[idx+len(fenceText):]
		from += idx + len(fenceText)
	}
}

// convertDollarMath replaces dollar-delimited math in a non-code segment.
// Display ($$…$$) is replaced before inline ($…$) to prevent the single-$
// pattern from consuming one delimiter of a $$ pair.
//
// @{"req": ["REQ-CONTENT-010"]}
func convertDollarMath(seg string) string {
	// Replace $$…$$ display math with an AsciiDoc stem block.
	seg = displayMathRe.ReplaceAllStringFunc(seg, func(match string) string {
		inner := displayMathRe.FindStringSubmatch(match)
		if len(inner) < 2 {
			return match
		}
		// Trim surrounding whitespace from the inner expression; the stem block
		// delimiter adds its own newlines.
		content := strings.TrimSpace(inner[1])
		return "[stem]\n++++\n" + content + "\n++++\n"
	})

	// Replace $…$ inline math with stem:[…].
	seg = inlineMathRe.ReplaceAllStringFunc(seg, func(match string) string {
		inner := inlineMathRe.FindStringSubmatch(match)
		if len(inner) < 2 {
			return match
		}
		return "stem:[" + inner[1] + "]"
	})

	return seg
}

// injectStemHeader prepends `:stem: latexmath\n` to src if the attribute is
// not already present. Asciidoctor treats a duplicate :stem: declaration as
// idempotent (the second value wins), but the check avoids noisy duplication
// for content that already follows the STEM convention (task 18.5 prompt
// change generates documents with :stem: latexmath already in the header).
//
// @{"req": ["REQ-CONTENT-010"]}
func injectStemHeader(src string) string {
	if stemHeaderRe.MatchString(src) {
		return src
	}
	return ":stem: latexmath\n" + src
}
