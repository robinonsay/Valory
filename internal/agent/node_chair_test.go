// node_chair_test.go — unit tests for node_chair.go helpers.
//
// These tests cover buildNodeChatMessages (merge/alternation logic) and
// nodePayloadForResponse (syllabus/content AsciiDoc path, section_goal/concept
// JSON path, AND the malformed-JSON fallback branch).  No DB or Anthropic API
// calls are made; all functions under test are pure.
package agent

import (
	"encoding/json"
	"strings"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// buildNodeChatMessages
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AGENT-051", "REQ-AGENT-060"]}
func TestBuildNodeChatMessages_Empty_ReturnsEmpty(t *testing.T) {
	msgs := buildNodeChatMessages(nil)
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
}

// @{"verifies": ["REQ-AGENT-051", "REQ-AGENT-060"]}
func TestBuildNodeChatMessages_AlternatingRoles_PreservedInOrder(t *testing.T) {
	history := []NodeChatMessage{
		{ID: uuid.New(), Role: "user", Content: "What is a goroutine?"},
		{ID: uuid.New(), Role: "assistant", Content: "A goroutine is a lightweight thread."},
		{ID: uuid.New(), Role: "user", Content: "Give an example."},
	}
	msgs := buildNodeChatMessages(history)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if msgs[0].Role != anthropic.MessageParamRoleUser {
		t.Errorf("msg[0]: expected user, got %q", msgs[0].Role)
	}
	if msgs[1].Role != anthropic.MessageParamRoleAssistant {
		t.Errorf("msg[1]: expected assistant, got %q", msgs[1].Role)
	}
	if msgs[2].Role != anthropic.MessageParamRoleUser {
		t.Errorf("msg[2]: expected user, got %q", msgs[2].Role)
	}
}

// @{"verifies": ["REQ-AGENT-051", "REQ-AGENT-060"]}
func TestBuildNodeChatMessages_ConsecutiveUserMessages_Merged(t *testing.T) {
	history := []NodeChatMessage{
		{ID: uuid.New(), Role: "user", Content: "First question."},
		{ID: uuid.New(), Role: "user", Content: "Second question."},
	}
	msgs := buildNodeChatMessages(history)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 merged message, got %d", len(msgs))
	}
	if msgs[0].Role != anthropic.MessageParamRoleUser {
		t.Errorf("merged msg: expected user, got %q", msgs[0].Role)
	}
	text := msgs[0].Content[0].OfText.Text
	if text != "First question.\nSecond question." {
		t.Errorf("merged text: expected %q, got %q", "First question.\nSecond question.", text)
	}
}

// @{"verifies": ["REQ-AGENT-051", "REQ-AGENT-060"]}
func TestBuildNodeChatMessages_ConsecutiveAssistantMessages_Merged(t *testing.T) {
	history := []NodeChatMessage{
		{ID: uuid.New(), Role: "assistant", Content: "Part A."},
		{ID: uuid.New(), Role: "assistant", Content: "Part B."},
	}
	msgs := buildNodeChatMessages(history)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 merged message, got %d", len(msgs))
	}
	if msgs[0].Role != anthropic.MessageParamRoleAssistant {
		t.Errorf("merged msg: expected assistant, got %q", msgs[0].Role)
	}
	text := msgs[0].Content[0].OfText.Text
	if text != "Part A.\nPart B." {
		t.Errorf("merged text: expected %q, got %q", "Part A.\nPart B.", text)
	}
}

// @{"verifies": ["REQ-AGENT-051", "REQ-AGENT-060"]}
func TestBuildNodeChatMessages_ThreeConsecutiveSameRole_AllMergedIntoOne(t *testing.T) {
	history := []NodeChatMessage{
		{ID: uuid.New(), Role: "user", Content: "A"},
		{ID: uuid.New(), Role: "user", Content: "B"},
		{ID: uuid.New(), Role: "user", Content: "C"},
	}
	msgs := buildNodeChatMessages(history)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 merged message, got %d", len(msgs))
	}
	want := "A\nB\nC"
	if msgs[0].Content[0].OfText.Text != want {
		t.Errorf("expected %q, got %q", want, msgs[0].Content[0].OfText.Text)
	}
}

// @{"verifies": ["REQ-AGENT-051", "REQ-AGENT-060"]}
func TestBuildNodeChatMessages_MixedRolesWithConsecutiveDuplicates_MergedCorrectly(t *testing.T) {
	// user, user, assistant, assistant, user → [user(merged), assistant(merged), user]
	history := []NodeChatMessage{
		{ID: uuid.New(), Role: "user", Content: "Q1"},
		{ID: uuid.New(), Role: "user", Content: "Q2"},
		{ID: uuid.New(), Role: "assistant", Content: "A1"},
		{ID: uuid.New(), Role: "assistant", Content: "A2"},
		{ID: uuid.New(), Role: "user", Content: "Q3"},
	}
	msgs := buildNodeChatMessages(history)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages after merging, got %d", len(msgs))
	}
	// First: user with Q1+Q2 merged
	if msgs[0].Role != anthropic.MessageParamRoleUser {
		t.Errorf("msg[0]: expected user, got %q", msgs[0].Role)
	}
	if msgs[0].Content[0].OfText.Text != "Q1\nQ2" {
		t.Errorf("msg[0]: expected %q, got %q", "Q1\nQ2", msgs[0].Content[0].OfText.Text)
	}
	// Second: assistant with A1+A2 merged
	if msgs[1].Role != anthropic.MessageParamRoleAssistant {
		t.Errorf("msg[1]: expected assistant, got %q", msgs[1].Role)
	}
	if msgs[1].Content[0].OfText.Text != "A1\nA2" {
		t.Errorf("msg[1]: expected %q, got %q", "A1\nA2", msgs[1].Content[0].OfText.Text)
	}
	// Third: user Q3 alone
	if msgs[2].Role != anthropic.MessageParamRoleUser {
		t.Errorf("msg[2]: expected user, got %q", msgs[2].Role)
	}
	if msgs[2].Content[0].OfText.Text != "Q3" {
		t.Errorf("msg[2]: expected %q, got %q", "Q3", msgs[2].Content[0].OfText.Text)
	}
}

// @{"verifies": ["REQ-AGENT-051", "REQ-AGENT-060"]}
func TestBuildNodeChatMessages_SingleUserMessage_NotMerged(t *testing.T) {
	history := []NodeChatMessage{
		{ID: uuid.New(), Role: "user", Content: "Hello."},
	}
	msgs := buildNodeChatMessages(history)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Content[0].OfText.Text != "Hello." {
		t.Errorf("expected %q, got %q", "Hello.", msgs[0].Content[0].OfText.Text)
	}
}

// ---------------------------------------------------------------------------
// nodePayloadForResponse — table-driven
// ---------------------------------------------------------------------------

// payloadCase is a single row in the nodePayloadForResponse table test.
type payloadCase struct {
	name       string
	nodeType   string
	raw        string
	wantKey    string   // JSON key that must be present in the returned payload
	wantValue  string   // value substring that must appear for wantKey
	wantErrNil bool     // whether err must be nil
	// If true, the returned JSON must itself parse cleanly.
	wantValidJSON bool
}

// @{"verifies": ["REQ-AGENT-044", "REQ-AGENT-060"]}
func TestNodePayloadForResponse_TableDriven(t *testing.T) {
	cases := []payloadCase{
		{
			name:          "syllabus_adoc_path",
			nodeType:      "syllabus",
			raw:           "= Course Title\n\n== Section 1\n\nSome content.",
			wantKey:       "content_adoc",
			wantValue:     "= Course Title",
			wantErrNil:    true,
			wantValidJSON: true,
		},
		{
			name:          "content_adoc_path",
			nodeType:      "content",
			raw:           "= Lesson\n\n== Intro\n\nLesson text.",
			wantKey:       "content_adoc",
			wantValue:     "= Lesson",
			wantErrNil:    true,
			wantValidJSON: true,
		},
		{
			name:          "section_goal_valid_json",
			nodeType:      "section_goal",
			raw:           `{"title":"Goroutines","section_index":0,"objectives":["Understand scheduling"]}`,
			wantKey:       "title",
			wantValue:     "Goroutines",
			wantErrNil:    true,
			wantValidJSON: true,
		},
		{
			name:          "concept_valid_json",
			nodeType:      "concept",
			raw:           `{"title":"Goroutine","description":"A lightweight thread managed by the Go runtime."}`,
			wantKey:       "title",
			wantValue:     "Goroutine",
			wantErrNil:    true,
			wantValidJSON: true,
		},
		{
			name:     "section_goal_malformed_json_fallback",
			nodeType: "section_goal",
			// Claude returned prose instead of JSON — must fall back to {"raw": ...}
			raw:           "Here is the section goal: focus on goroutines.",
			wantKey:       "raw",
			wantValue:     "Here is the section goal:",
			wantErrNil:    true,
			wantValidJSON: true,
		},
		{
			name:          "concept_malformed_json_fallback",
			nodeType:      "concept",
			raw:           "The concept is concurrency primitives.",
			wantKey:       "raw",
			wantValue:     "The concept is concurrency",
			wantErrNil:    true,
			wantValidJSON: true,
		},
		{
			name:          "unknown_type_content_key",
			nodeType:      "unknown_node",
			raw:           "Some raw text.",
			wantKey:       "content",
			wantValue:     "Some raw text.",
			wantErrNil:    true,
			wantValidJSON: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := nodePayloadForResponse(tc.nodeType, tc.raw)

			if tc.wantErrNil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.wantErrNil && err == nil {
				t.Fatal("expected error, got nil")
			}

			if tc.wantValidJSON {
				var m map[string]json.RawMessage
				if err := json.Unmarshal(got, &m); err != nil {
					t.Fatalf("result is not valid JSON: %v — got: %s", err, string(got))
				}
				valRaw, ok := m[tc.wantKey]
				if !ok {
					t.Fatalf("expected JSON key %q not present in payload: %s", tc.wantKey, string(got))
				}
				// The value may be a JSON string or embedded object; stringify for substring check.
				valStr := string(valRaw)
				// JSON-encoded strings have surrounding quotes; strip them for comparison.
				if strings.HasPrefix(valStr, `"`) {
					var s string
					if err := json.Unmarshal(valRaw, &s); err == nil {
						valStr = s
					}
				}
				if !strings.Contains(valStr, tc.wantValue) {
					t.Errorf("payload[%q]: expected to contain %q, got %q", tc.wantKey, tc.wantValue, valStr)
				}
			}
		})
	}
}

// TestNodePayloadForResponse_SyllabusAsciiDoc_ContentAdocKeyPresent verifies
// that the syllabus AsciiDoc path stores content under "content_adoc".
//
// @{"verifies": ["REQ-AGENT-044", "REQ-AGENT-060"]}
func TestNodePayloadForResponse_SyllabusAsciiDoc_ContentAdocKeyPresent(t *testing.T) {
	raw := "= Course Syllabus\n\n== Week 1\n\nIntroduction to concurrency."
	payload, err := nodePayloadForResponse("syllabus", raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]string
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("payload is not valid JSON object of strings: %v", err)
	}
	if _, ok := m["content_adoc"]; !ok {
		t.Errorf("expected key content_adoc in payload, keys present: %v", keysOf(m))
	}
	if m["content_adoc"] != raw {
		t.Errorf("content_adoc: expected %q, got %q", raw, m["content_adoc"])
	}
}

// TestNodePayloadForResponse_SectionGoalValidJSON_EmbeddedAsJSON verifies that
// a valid JSON response for section_goal is returned as-is (not wrapped).
//
// @{"verifies": ["REQ-AGENT-044", "REQ-AGENT-060"]}
func TestNodePayloadForResponse_SectionGoalValidJSON_EmbeddedAsJSON(t *testing.T) {
	raw := `{"title":"Introduction","section_index":0,"objectives":["Understand basics"]}`
	payload, err := nodePayloadForResponse("section_goal", raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}

	// The valid JSON case: payload IS the raw JSON itself (no outer wrapper),
	// so the top-level keys include "title", "section_index", "objectives".
	if _, ok := m["title"]; !ok {
		t.Errorf("expected top-level key 'title' from valid JSON passthrough, got keys: %v", keysOf2(m))
	}
}

// TestNodePayloadForResponse_SectionGoalMalformedJSON_FallbackRawKey verifies
// that a non-JSON response for section_goal produces {"raw": "<original text>"}.
//
// @{"verifies": ["REQ-AGENT-044", "REQ-AGENT-060"]}
func TestNodePayloadForResponse_SectionGoalMalformedJSON_FallbackRawKey(t *testing.T) {
	raw := "I apologize, but here is the section goal in prose form."
	payload, err := nodePayloadForResponse("section_goal", raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]string
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("fallback payload is not a JSON string map: %v", err)
	}
	if _, ok := m["raw"]; !ok {
		t.Errorf("expected key 'raw' in fallback payload, got keys: %v", keysOf(m))
	}
	if m["raw"] != raw {
		t.Errorf("raw value: expected %q, got %q", raw, m["raw"])
	}
}

// keysOf returns the keys of a map[string]string for error messages.
func keysOf(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// keysOf2 returns the keys of a map[string]json.RawMessage for error messages.
func keysOf2(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
