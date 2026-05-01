package agent

import (
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
