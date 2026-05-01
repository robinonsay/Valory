package submission

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

var ErrUnsupportedFormat = errors.New("submission: unsupported file format")
var ErrInvalidContent = errors.New("submission: file content does not match declared format")

// DetectFormat returns the submission_format enum value ("latex", "markdown",
// "asciidoc") derived from the file extension, or ErrUnsupportedFormat when
// the extension is not among the three accepted formats.
//
// @{"req": ["REQ-SUBMISSION-001"]}
func DetectFormat(filename string) (string, error) {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".tex":
		return "latex", nil
	case ".md", ".markdown":
		return "markdown", nil
	case ".adoc", ".asciidoc":
		return "asciidoc", nil
	default:
		return "", ErrUnsupportedFormat
	}
}

// ValidateContent reads up to the first 8 KB of the file and checks whether the
// content is consistent with the declared format. Returns ErrInvalidContent if not.
//
// The heuristics applied per format:
//   - latex:    must contain \documentclass or \begin{document}
//   - markdown: valid UTF-8 with no null bytes (null bytes signal binary content)
//   - asciidoc: valid UTF-8 containing at least one "= " heading
//
// @{"req": ["REQ-SUBMISSION-003"]}
func ValidateContent(format string, r io.Reader) error {
	buf := make([]byte, 8192)
	n, err := io.ReadFull(r, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return err
	}
	buf = buf[:n]

	// All three supported formats are plain-text; binary content is always invalid.
	if !utf8.Valid(buf) {
		return ErrInvalidContent
	}

	switch format {
	case "latex":
		s := string(buf)
		if !strings.Contains(s, `\documentclass`) && !strings.Contains(s, `\begin{document}`) {
			return ErrInvalidContent
		}
	case "markdown":
		// Null bytes indicate binary data even though the UTF-8 check passed for
		// the non-null portions; reject them explicitly.
		if bytes.ContainsRune(buf, 0) {
			return ErrInvalidContent
		}
	case "asciidoc":
		// A valid AsciiDoc document must contain a title line starting with "= "
		// followed by at least one non-space character.
		hasTitle := false
		for _, line := range strings.Split(string(buf), "\n") {
			if strings.HasPrefix(line, "= ") && len(strings.TrimSpace(line[2:])) > 0 {
				hasTitle = true
				break
			}
		}
		if !hasTitle {
			return ErrInvalidContent
		}
	}
	return nil
}
