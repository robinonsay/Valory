package submission

import (
	"errors"
	"strings"
	"testing"
)

// @{"verifies": ["REQ-SUBMISSION-001"]}
func TestDetectFormat(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     string
		wantErr  error
	}{
		{
			name:     "tex extension",
			filename: "document.tex",
			want:     "latex",
		},
		{
			name:     "tex extension uppercase",
			filename: "document.TEX",
			want:     "latex",
		},
		{
			name:     "md extension",
			filename: "readme.md",
			want:     "markdown",
		},
		{
			name:     "markdown extension",
			filename: "notes.markdown",
			want:     "markdown",
		},
		{
			name:     "adoc extension",
			filename: "doc.adoc",
			want:     "asciidoc",
		},
		{
			name:     "asciidoc extension",
			filename: "doc.asciidoc",
			want:     "asciidoc",
		},
		{
			name:     "unknown extension",
			filename: "file.pdf",
			wantErr:  ErrUnsupportedFormat,
		},
		{
			name:     "no extension",
			filename: "Makefile",
			wantErr:  ErrUnsupportedFormat,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DetectFormat(tc.filename)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("DetectFormat(%q) error = %v, want %v", tc.filename, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DetectFormat(%q) unexpected error: %v", tc.filename, err)
			}
			if got != tc.want {
				t.Errorf("DetectFormat(%q) = %q, want %q", tc.filename, got, tc.want)
			}
		})
	}
}

// @{"verifies": ["REQ-SUBMISSION-003"]}
func TestValidateContent(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		content string
		wantErr error
	}{
		{
			name:    "valid latex with documentclass",
			format:  "latex",
			content: `\documentclass{article}\begin{document}Hello\end{document}`,
		},
		{
			name:    "valid latex with begin document only",
			format:  "latex",
			content: `% preamble\n\begin{document}\nHello\n\end{document}`,
		},
		{
			name:    "invalid latex missing markers",
			format:  "latex",
			content: "This is just plain text without any LaTeX markers.",
			wantErr: ErrInvalidContent,
		},
		{
			name:    "valid markdown plain text",
			format:  "markdown",
			content: "# Hello World\n\nThis is a paragraph.",
		},
		{
			name:    "valid markdown empty-ish",
			format:  "markdown",
			content: "simple text",
		},
		{
			name:    "invalid markdown binary content with null bytes",
			format:  "markdown",
			content: "text\x00binary",
			wantErr: ErrInvalidContent,
		},
		{
			name:    "valid asciidoc with heading",
			format:  "asciidoc",
			content: "= Document Title\n\nSome content here.",
		},
		{
			name:    "invalid asciidoc without heading",
			format:  "asciidoc",
			content: "This is just a paragraph without any heading.",
			wantErr: ErrInvalidContent,
		},
		{
			// "x = 5" contains "= " but not at line start; the old strings.Contains
			// check would have passed this as valid AsciiDoc.
			name:    "invalid asciidoc with mid-line equals — false positive guard",
			format:  "asciidoc",
			content: "x = 5",
			wantErr: ErrInvalidContent,
		},
		{
			name:    "valid asciidoc document title at line start",
			format:  "asciidoc",
			content: "= My Document\n\nsome content",
		},
		{
			name:    "invalid content binary not valid utf8",
			format:  "markdown",
			content: string([]byte{0xFF, 0xFE, 0x00, 0x01}),
			wantErr: ErrInvalidContent,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateContent(tc.format, strings.NewReader(tc.content))
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("ValidateContent(%q, ...) error = %v, want %v", tc.format, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Errorf("ValidateContent(%q, ...) unexpected error: %v", tc.format, err)
			}
		})
	}
}
