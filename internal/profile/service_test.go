package profile

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// appendProfileBlock — REQ-PROFILE-010 regression guard
// ---------------------------------------------------------------------------

// TestAppendProfileBlock_EmptySummary_ReturnPromptUnchanged is the critical
// regression guard for REQ-PROFILE-010: when no profile exists the prompt must
// be returned byte-for-byte identical so that existing generation behaviour is
// preserved. Any modification (even whitespace) would constitute a regression.
//
// @{"verifies": ["REQ-PROFILE-010"]}
func TestAppendProfileBlock_EmptySummary_ReturnPromptUnchanged(t *testing.T) {
	original := "You are a university professor writing lesson content."
	got := AppendProfileBlock(original, "")
	if got != original {
		t.Errorf("AppendProfileBlock with empty summary must return prompt byte-for-byte unchanged:\nwant: %q\n got: %q", original, got)
	}
}

// TestAppendProfileBlock_NonEmptySummary_AppendsBlock verifies that a non-empty
// summary is appended as a "Student learning profile:" block.
//
// @{"verifies": ["REQ-PROFILE-006", "REQ-PROFILE-007", "REQ-PROFILE-008", "REQ-PROFILE-009"]}
func TestAppendProfileBlock_NonEmptySummary_AppendsBlock(t *testing.T) {
	prompt := "You are a university professor."
	summary := "The student prefers example-first explanations."
	got := AppendProfileBlock(prompt, summary)

	if !strings.Contains(got, "Student learning profile:") {
		t.Errorf("expected profile block header in result, got: %q", got)
	}
	if !strings.Contains(got, summary) {
		t.Errorf("expected summary text in result, got: %q", got)
	}
	// Original prompt must still be present.
	if !strings.HasPrefix(got, prompt) {
		t.Errorf("original prompt must be a prefix of the result, got: %q", got)
	}
}

// TestAppendProfileBlock_MultilineSummary verifies that multi-line summaries
// are preserved verbatim inside the injected block.
//
// @{"verifies": ["REQ-PROFILE-002", "REQ-PROFILE-010"]}
func TestAppendProfileBlock_MultilineSummary_PreservedVerbatim(t *testing.T) {
	prompt := "Base prompt."
	summary := "Line one.\nLine two.\nLine three."
	got := AppendProfileBlock(prompt, summary)
	if !strings.Contains(got, summary) {
		t.Errorf("multi-line summary must appear verbatim in result, got: %q", got)
	}
}

// ---------------------------------------------------------------------------
// onboardingSystemPrompt — REQ-PROFILE-011
// ---------------------------------------------------------------------------

// TestOnboardingSystemPrompt_ContainsSentinel verifies that the onboarding
// system prompt references the ONBOARDING_COMPLETE sentinel so the agent
// knows when to signal completion.
//
// @{"verifies": ["REQ-PROFILE-011"]}
func TestOnboardingSystemPrompt_ContainsSentinel(t *testing.T) {
	prompt := onboardingSystemPrompt()
	if !strings.Contains(prompt, onboardingSentinel) {
		t.Errorf("onboarding system prompt must reference the %q sentinel, got: %q", onboardingSentinel, prompt)
	}
}

// TestOnboardingSystemPrompt_CoversFiveAxes verifies that the prompt addresses
// all five learning-preference axes specified in SDD-023 §6.1.
//
// @{"verifies": ["REQ-PROFILE-011"]}
func TestOnboardingSystemPrompt_CoversFiveAxes(t *testing.T) {
	prompt := onboardingSystemPrompt()
	axes := []string{
		"knowledge level",
		"explanation style",
		"hours per week",
		"topics",
		"learning challenges",
	}
	for _, axis := range axes {
		if !strings.Contains(strings.ToLower(prompt), axis) {
			t.Errorf("onboarding system prompt missing axis %q", axis)
		}
	}
}

// ---------------------------------------------------------------------------
// summarizationSystemPrompt — REQ-PROFILE-012
// ---------------------------------------------------------------------------

// TestSummarizationSystemPrompt_ThirdPerson verifies the summarization prompt
// instructs the model to write in third person.
//
// @{"verifies": ["REQ-PROFILE-012"]}
func TestSummarizationSystemPrompt_ThirdPerson(t *testing.T) {
	prompt := summarizationSystemPrompt()
	if !strings.Contains(prompt, "third person") {
		t.Errorf("summarization system prompt must instruct third-person writing, got: %q", prompt)
	}
}

// TestSummarizationSystemPrompt_NoPreamble verifies the prompt explicitly asks
// for no preamble or JSON wrapper in the output.
//
// @{"verifies": ["REQ-PROFILE-012"]}
func TestSummarizationSystemPrompt_NoPreamble(t *testing.T) {
	prompt := summarizationSystemPrompt()
	if !strings.Contains(prompt, "No preamble") && !strings.Contains(prompt, "no preamble") {
		t.Errorf("summarization prompt must instruct 'No preamble', got: %q", prompt)
	}
}

// ---------------------------------------------------------------------------
// formatHistory — REQ-PROFILE-012
// ---------------------------------------------------------------------------

// TestFormatHistory_IncludesAllTurns verifies that every message in the
// onboarding history appears in the formatted output.
//
// @{"verifies": ["REQ-PROFILE-012"]}
func TestFormatHistory_IncludesAllTurns(t *testing.T) {
	msgs := []OnboardingMessageRow{
		{Role: "assistant", Content: "What is your experience level?"},
		{Role: "student", Content: "Intermediate."},
		{Role: "assistant", Content: "How many hours per week can you study?"},
		{Role: "student", Content: "About 10 hours."},
	}
	formatted := formatHistory(msgs)

	for _, m := range msgs {
		if !strings.Contains(formatted, m.Content) {
			t.Errorf("formatHistory missing content %q", m.Content)
		}
	}
	if !strings.Contains(formatted, "Student:") {
		t.Errorf("formatHistory must label student turns with 'Student:'")
	}
	if !strings.Contains(formatted, "Assistant:") {
		t.Errorf("formatHistory must label assistant turns with 'Assistant:'")
	}
}

// ---------------------------------------------------------------------------
// buildOnboardingMessages — REQ-PROFILE-011
// ---------------------------------------------------------------------------

// TestBuildOnboardingMessages_StartsWithUserTurn verifies that the converted
// message slice always begins with a user turn (Anthropic API requirement).
//
// @{"verifies": ["REQ-PROFILE-011"]}
func TestBuildOnboardingMessages_StartsWithUserTurn(t *testing.T) {
	// History that starts with an assistant turn (the opening question).
	msgs := []OnboardingMessageRow{
		{Role: "assistant", Content: "Hello! What is your experience level?"},
		{Role: "student", Content: "Intermediate."},
	}
	params := buildOnboardingMessages(msgs)
	if len(params) == 0 {
		t.Fatal("buildOnboardingMessages returned empty slice")
	}
	first := params[0]
	if first.Role != "user" {
		t.Errorf("first message must be user role, got %q", first.Role)
	}
}

// TestBuildOnboardingMessages_CountThreeToFive verifies that a valid 3-turn
// conversation produces a message slice of reasonable length (≥3 turns plus
// the synthetic trigger = ≥4 total params including trigger).
//
// @{"verifies": ["REQ-PROFILE-011"]}
func TestBuildOnboardingMessages_AtLeastThreeTurns(t *testing.T) {
	msgs := []OnboardingMessageRow{
		{Role: "assistant", Content: "Q1"},
		{Role: "student", Content: "A1"},
		{Role: "assistant", Content: "Q2"},
		{Role: "student", Content: "A2"},
		{Role: "assistant", Content: "Q3 ONBOARDING_COMPLETE"},
	}
	params := buildOnboardingMessages(msgs)
	// 3 student turns + 2 assistant turns = 5 params (plus synthetic trigger → 6 or merged).
	// At minimum the conversation history should produce ≥3 items.
	if len(params) < 3 {
		t.Errorf("expected at least 3 message params for a 5-turn conversation, got %d", len(params))
	}
}

// ---------------------------------------------------------------------------
// ErrNoActiveSession sentinel — FIX 2 regression guard
// ---------------------------------------------------------------------------

// TestErrNoActiveSession_IsWrappable verifies that ErrNoActiveSession can be
// detected via errors.Is after wrapping with fmt.Errorf("%w"), which is exactly
// how Complete() returns it. This guards against the fragile string-compare
// anti-pattern that was replaced.
//
// @{"verifies": ["REQ-PROFILE-012"]}
func TestErrNoActiveSession_IsWrappable(t *testing.T) {
	wrapped := fmt.Errorf("onboarding: complete: %w", ErrNoActiveSession)
	if !errors.Is(wrapped, ErrNoActiveSession) {
		t.Errorf("errors.Is must detect ErrNoActiveSession through a fmt.Errorf wrap; got: %v", wrapped)
	}
}

// TestErrNoActiveSession_IsDistinctFromErrNotFound ensures the two package
// sentinels are not accidentally aliased.
//
// @{"verifies": ["REQ-PROFILE-012"]}
func TestErrNoActiveSession_IsDistinctFromErrNotFound(t *testing.T) {
	if errors.Is(ErrNoActiveSession, ErrNotFound) {
		t.Error("ErrNoActiveSession and ErrNotFound must be distinct sentinel values")
	}
}
