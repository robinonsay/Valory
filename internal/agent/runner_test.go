package agent

import (
	"testing"
)

// ---------------------------------------------------------------------------
// containsRegenKeyword — pure function, no DB required
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-CONTENT-004"]}
func TestContainsRegenKeyword_MatchingKeywords_ReturnsTrue(t *testing.T) {
	cases := []struct {
		text    string
		keyword string
	}{
		{"please rewrite this section", "rewrite"},
		{"change the introduction", "change"},
		{"redo the examples", "redo"},
		{"update with more examples", "update"},
		{"this is incorrect", "incorrect"},
		{"the formula is wrong", "wrong"},
		{"fix the citation", "fix"},
		{"revise the conclusion", "revise"},
		{"regenerate this content", "regenerate"},
	}

	for _, tc := range cases {
		if !containsRegenKeyword(tc.text) {
			t.Errorf("expected true for %q (keyword: %q), got false", tc.text, tc.keyword)
		}
	}
}

// @{"verifies": ["REQ-CONTENT-004"]}
func TestContainsRegenKeyword_NonMatchingText_ReturnsFalse(t *testing.T) {
	cases := []string{
		"great content, well explained",
		"I learned a lot from this section",
		"the examples are very helpful",
		"",
		"thanks for writing this",
	}

	for _, text := range cases {
		if containsRegenKeyword(text) {
			t.Errorf("expected false for %q, got true", text)
		}
	}
}

// @{"verifies": ["REQ-CONTENT-004"]}
func TestContainsRegenKeyword_KeywordAsSubstring_ReturnsTrue(t *testing.T) {
	// "fix" appears as a substring of "prefix" — the naive substring match will
	// still return true here, which is the documented (if imprecise) behaviour.
	if !containsRegenKeyword("please prefix this") {
		t.Error("expected true: 'fix' is a substring of 'prefix'")
	}
}
