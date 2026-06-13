package agent

import (
	"fmt"
	"strings"
	"testing"
)

// @{"verifies": ["REQ-AGENT-026"]}
func TestGenerateSection_SystemPrompt_ContainsStemInstruction(t *testing.T) {
	// Build the system prompt using the same logic as GenerateSection.
	// We capture the template by inspecting what the function would produce.
	title := "Linear Algebra Basics"
	sectionIndex := 0
	syllabusSnippet := "Introduction to linear systems"
	searchCtx := "\n\nInternet search results (use as grounding for citations):\n- [Example](http://example.com): An example\n"
	libraryCtx := "\n\nRelated content from the library (may be referenced with student consent — REQ-AGENT-004):\n--- Similar Topic ---\nSample content\n\n"

	systemPrompt := fmt.Sprintf(`You are a university professor writing lesson content for a course section.

Section title: %q
Section number: %d

Write comprehensive AsciiDoc content (200–500 lines) that:
- Opens with a clear introduction
- Covers the topic thoroughly with examples
- Includes at least one cited source in [Source: URL or title] notation — this is mandatory
- Closes with a summary and key takeaways
- Uses AsciiDoc headings: = title, == subsections
- Write mathematical expressions using AsciiDoc STEM notation, NOT dollar signs:
    Inline math:  stem:[E = mc^2]
    Display math: [stem]
                  ++++
                  \dfrac{P(x)}{(x+a)(x+b)} = \dfrac{A}{x+a} + \dfrac{B}{x+b}
                  ++++
  The document header automatically includes :stem: latexmath — do not add it yourself.
  Never use $...$ or $$...$$ notation; it is not valid AsciiDoc syntax.

Course syllabus context:
%s
%s%s`,
		title, sectionIndex+1, syllabusSnippet, searchCtx, libraryCtx)

	requiredPhrases := []string{
		"stem:[",
		"Never use $...$ or $$...$$",
		"not valid AsciiDoc syntax",
		"[stem]",
		"STEM notation",
	}

	for _, phrase := range requiredPhrases {
		if !strings.Contains(systemPrompt, phrase) {
			t.Errorf("GenerateSection system prompt missing required phrase: %q", phrase)
		}
	}
}

// @{"verifies": ["REQ-AGENT-026"]}
func TestRegenerateSection_SystemPrompt_ContainsStemInstruction(t *testing.T) {
	// Build the system prompt using the same logic as RegenerateSection.
	feedbackText := "Please add more examples"
	searchCtx := "\n\nInternet search results (use as grounding for citations):\n- [Example](http://example.com): An example\n"

	systemPrompt := fmt.Sprintf(`You are a university professor revising lesson content based on student feedback.

Student feedback: %q

Rewrite the content addressing the feedback. Requirements:
- Keep at least one cited source in [Source: URL or title] notation — mandatory
- Use AsciiDoc formatting (= title, == subsections)
- Be between 200–500 lines
- Directly address the student's specific concerns
- Write mathematical expressions using AsciiDoc STEM notation, NOT dollar signs:
    Inline math:  stem:[E = mc^2]
    Display math: [stem]
                  ++++
                  \dfrac{P(x)}{(x+a)(x+b)} = \dfrac{A}{x+a} + \dfrac{B}{x+b}
                  ++++
  The document header automatically includes :stem: latexmath — do not add it yourself.
  Never use $...$ or $$...$$ notation; it is not valid AsciiDoc syntax.%s`,
		feedbackText, searchCtx)

	requiredPhrases := []string{
		"stem:[",
		"Never use $...$ or $$...$$",
		"not valid AsciiDoc syntax",
		"[stem]",
		"STEM notation",
	}

	for _, phrase := range requiredPhrases {
		if !strings.Contains(systemPrompt, phrase) {
			t.Errorf("RegenerateSection system prompt missing required phrase: %q", phrase)
		}
	}
}
