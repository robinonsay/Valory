package agent

import (
	"strings"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

// ---------------------------------------------------------------------------
// stripCodeFence
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AGENT-001"]}
func TestStripCodeFence_NoFence_Unchanged(t *testing.T) {
	input := `{"approved": true}`
	if got := stripCodeFence(input); got != input {
		t.Errorf("expected %q, got %q", input, got)
	}
}

// @{"verifies": ["REQ-AGENT-001"]}
func TestStripCodeFence_JsonFence_Stripped(t *testing.T) {
	input := "```json\n{\"ok\": true}\n```"
	want := `{"ok": true}`
	if got := stripCodeFence(input); got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

// @{"verifies": ["REQ-AGENT-001"]}
func TestStripCodeFence_PlainFence_Stripped(t *testing.T) {
	input := "```\nhello\n```"
	want := "hello"
	if got := stripCodeFence(input); got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

// ---------------------------------------------------------------------------
// buildMessages
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AGENT-001", "REQ-AGENT-015"]}
func TestBuildMessages_EmptyHistory_ReturnsEmpty(t *testing.T) {
	msgs := buildMessages(nil)
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
}

// @{"verifies": ["REQ-AGENT-001", "REQ-AGENT-015"]}
func TestBuildMessages_AlternatingRoles_PreservedInOrder(t *testing.T) {
	history := []ChatMessageRow{
		{Role: "student", Content: "Hello"},
		{Role: "assistant", Content: "Hi there"},
		{Role: "student", Content: "How are you?"},
	}
	msgs := buildMessages(history)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if msgs[0].Role != anthropic.MessageParamRoleUser {
		t.Errorf("msg[0]: expected user role, got %q", msgs[0].Role)
	}
	if msgs[1].Role != anthropic.MessageParamRoleAssistant {
		t.Errorf("msg[1]: expected assistant role, got %q", msgs[1].Role)
	}
	if msgs[2].Role != anthropic.MessageParamRoleUser {
		t.Errorf("msg[2]: expected user role, got %q", msgs[2].Role)
	}
}

// @{"verifies": ["REQ-AGENT-001", "REQ-AGENT-015"]}
func TestBuildMessages_ConsecutiveUserMsgs_Merged(t *testing.T) {
	history := []ChatMessageRow{
		{Role: "student", Content: "First"},
		{Role: "student", Content: "Second"},
	}
	msgs := buildMessages(history)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 merged message, got %d", len(msgs))
	}
	text := msgs[0].Content[0].OfText.Text
	if text != "First\nSecond" {
		t.Errorf("expected merged text %q, got %q", "First\nSecond", text)
	}
}

// @{"verifies": ["REQ-AGENT-001", "REQ-AGENT-015"]}
func TestBuildMessages_ConsecutiveAssistantMsgs_Merged(t *testing.T) {
	history := []ChatMessageRow{
		{Role: "assistant", Content: "Part 1"},
		{Role: "assistant", Content: "Part 2"},
	}
	msgs := buildMessages(history)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 merged message, got %d", len(msgs))
	}
	if msgs[0].Role != anthropic.MessageParamRoleAssistant {
		t.Errorf("expected assistant role, got %q", msgs[0].Role)
	}
	text := msgs[0].Content[0].OfText.Text
	if text != "Part 1\nPart 2" {
		t.Errorf("expected %q, got %q", "Part 1\nPart 2", text)
	}
}

// @{"verifies": ["REQ-AGENT-001", "REQ-AGENT-015"]}
func TestBuildMessages_ThreeSameRole_AllMergedIntoOne(t *testing.T) {
	history := []ChatMessageRow{
		{Role: "student", Content: "A"},
		{Role: "student", Content: "B"},
		{Role: "student", Content: "C"},
	}
	msgs := buildMessages(history)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	want := "A\nB\nC"
	if msgs[0].Content[0].OfText.Text != want {
		t.Errorf("expected %q, got %q", want, msgs[0].Content[0].OfText.Text)
	}
}

// ---------------------------------------------------------------------------
// buildMessagesForIntake
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AGENT-001"]}
func TestBuildMessagesForIntake_EmptyHistory_InjectsTrigger(t *testing.T) {
	msgs := buildMessagesForIntake(nil, "Go programming")
	if len(msgs) == 0 {
		t.Fatal("expected at least one message, got 0")
	}
	if msgs[0].Role != anthropic.MessageParamRoleUser {
		t.Errorf("first message must be user, got %q", msgs[0].Role)
	}
}

// @{"verifies": ["REQ-AGENT-001"]}
func TestBuildMessagesForIntake_AssistantFirst_InjectsTriggerBefore(t *testing.T) {
	history := []ChatMessageRow{
		{Role: "assistant", Content: "Welcome! What would you like to learn?"},
	}
	msgs := buildMessagesForIntake(history, "Rust")
	if msgs[0].Role != anthropic.MessageParamRoleUser {
		t.Errorf("first message must be user after injection, got %q", msgs[0].Role)
	}
	// Original assistant message is now second.
	if len(msgs) < 2 {
		t.Fatal("expected 2 messages after trigger injection")
	}
	if msgs[1].Role != anthropic.MessageParamRoleAssistant {
		t.Errorf("second message must be assistant, got %q", msgs[1].Role)
	}
}

// @{"verifies": ["REQ-AGENT-001"]}
func TestBuildMessagesForIntake_UserFirst_NoInjection(t *testing.T) {
	history := []ChatMessageRow{
		{Role: "student", Content: "I want to learn Go"},
	}
	msgs := buildMessagesForIntake(history, "Go")
	// No injection needed — count should be 1.
	if len(msgs) != 1 {
		t.Errorf("expected 1 message (no injection), got %d", len(msgs))
	}
}

// ---------------------------------------------------------------------------
// buildMessagesForSyllabus
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AGENT-020"]}
func TestBuildMessagesForSyllabus_AssistantBookends_AnchoredWithUserTurns(t *testing.T) {
	// Post-kickoff intake history: starts with the Chair's opening question and
	// ends with the Chair's (sentinel-stripped) INTAKE_COMPLETE reply. The
	// Anthropic API rejects assistant-first and assistant-final conversations,
	// so both ends must be anchored with synthetic user turns.
	history := []ChatMessageRow{
		{Role: "assistant", Content: "Welcome! What is your background?"},
		{Role: "student", Content: "Complete beginner."},
		{Role: "assistant", Content: "Great, intake is complete."},
	}
	msgs := buildMessagesForSyllabus(history, "Linear Algebra")
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages (trigger + 3 history + closing), got %d", len(msgs))
	}
	if msgs[0].Role != anthropic.MessageParamRoleUser {
		t.Errorf("first message must be a user turn, got %q", msgs[0].Role)
	}
	if msgs[len(msgs)-1].Role != anthropic.MessageParamRoleUser {
		t.Errorf("last message must be a user turn, got %q", msgs[len(msgs)-1].Role)
	}
}

// @{"verifies": ["REQ-AGENT-020"]}
func TestBuildMessagesForSyllabus_EmptyHistory_SingleUserTurn(t *testing.T) {
	msgs := buildMessagesForSyllabus(nil, "Linear Algebra")
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != anthropic.MessageParamRoleUser {
		t.Errorf("expected user role, got %q", msgs[0].Role)
	}
}

// @{"verifies": ["REQ-AGENT-020"]}
func TestBuildMessagesForSyllabus_UserBookends_Unchanged(t *testing.T) {
	history := []ChatMessageRow{
		{Role: "student", Content: "I want to learn."},
		{Role: "assistant", Content: "Tell me more."},
		{Role: "student", Content: "Beginner, 5 hours a week."},
	}
	msgs := buildMessagesForSyllabus(history, "Linear Algebra")
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages unchanged, got %d", len(msgs))
	}
}

// @{"verifies": ["REQ-AGENT-015"]}
func TestEnsureUserFirst_AssistantFirst_PrependsUserTurn(t *testing.T) {
	history := []ChatMessageRow{
		{Role: "assistant", Content: "Welcome! What would you like to learn?"},
		{Role: "student", Content: "Tell me about eigenvalues."},
	}
	msgs := ensureUserFirst(buildMessages(history))
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages (trigger + 2 history), got %d", len(msgs))
	}
	if msgs[0].Role != anthropic.MessageParamRoleUser {
		t.Errorf("first message must be a user turn, got %q", msgs[0].Role)
	}
}

// @{"verifies": ["REQ-AGENT-015"]}
func TestEnsureUserFirst_UserFirst_Unchanged(t *testing.T) {
	history := []ChatMessageRow{
		{Role: "student", Content: "Tell me about eigenvalues."},
		{Role: "assistant", Content: "Gladly."},
	}
	msgs := ensureUserFirst(buildMessages(history))
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages unchanged, got %d", len(msgs))
	}
}

// @{"verifies": ["REQ-AGENT-020"]}
func TestStripCodeFence_AsciidocFence_RemovedIncludingTag(t *testing.T) {
	in := "```asciidoc\n= Course Syllabus\n\n== Week 1\n```"
	got := stripCodeFence(in)
	if strings.HasPrefix(got, "```") || strings.HasSuffix(got, "```") {
		t.Errorf("fences must be stripped, got %q", got)
	}
	if strings.HasPrefix(got, "asciidoc") {
		t.Errorf("language tag must be stripped with the fence line, got %q", got)
	}
	if !strings.Contains(got, "= Course Syllabus") {
		t.Errorf("content must survive stripping, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// chairSystemPrompt
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AGENT-022"]}
func TestChairSystemPrompt_ContainsMarkdownGuidance(t *testing.T) {
	prompt := chairSystemPrompt()
	requiredPhrases := []string{
		"Format your replies using Markdown",
		"bullet lists",
		"**bold**",
		"fenced code blocks",
	}
	for _, phrase := range requiredPhrases {
		if !strings.Contains(prompt, phrase) {
			t.Errorf("chairSystemPrompt missing expected phrase: %q", phrase)
		}
	}
}

// @{"verifies": ["REQ-AGENT-022"]}
func TestChairSystemPrompt_ContainsLaTeXGuidance(t *testing.T) {
	prompt := chairSystemPrompt()
	requiredPhrases := []string{
		"LaTeX",
		"$...$",
		"$$...$$",
		"inline mathematics",
		"display mathematics",
	}
	for _, phrase := range requiredPhrases {
		if !strings.Contains(prompt, phrase) {
			t.Errorf("chairSystemPrompt missing expected LaTeX phrase: %q", phrase)
		}
	}
}

// ---------------------------------------------------------------------------
// intakeSystemPrompt
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AGENT-022"]}
func TestIntakeSystemPrompt_ContainsMarkdownGuidance(t *testing.T) {
	prompt := intakeSystemPrompt("test topic")
	requiredPhrases := []string{
		"Format your replies using Markdown",
		"bullet lists",
		"**bold**",
	}
	for _, phrase := range requiredPhrases {
		if !strings.Contains(prompt, phrase) {
			t.Errorf("intakeSystemPrompt missing expected phrase: %q", phrase)
		}
	}
}

// @{"verifies": ["REQ-AGENT-022"]}
func TestIntakeSystemPrompt_ContainsLaTeXGuidance(t *testing.T) {
	prompt := intakeSystemPrompt("test topic")
	requiredPhrases := []string{
		"LaTeX",
		"$...$",
		"$$...$$",
	}
	for _, phrase := range requiredPhrases {
		if !strings.Contains(prompt, phrase) {
			t.Errorf("intakeSystemPrompt missing expected LaTeX phrase: %q", phrase)
		}
	}
}

// @{"verifies": ["REQ-AGENT-022", "REQ-AGENT-001"]}
func TestIntakeSystemPrompt_PreservesSentinelRules(t *testing.T) {
	prompt := intakeSystemPrompt("test topic")
	requiredPhrases := []string{
		"3 substantive student replies",
		intakeSentinel,
		"Do not include this marker until you have enough information",
	}
	for _, phrase := range requiredPhrases {
		if !strings.Contains(prompt, phrase) {
			t.Errorf("intakeSystemPrompt missing sentinel rule: %q", phrase)
		}
	}
}
