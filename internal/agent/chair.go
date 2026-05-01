// Package agent implements the multi-agent pipeline for Valory.
// chair.go contains the University Chair agent, responsible for intake,
// syllabus generation, due-date assignment, and student chat.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valory/valory/internal/db"
)

// intakeSentinel is included verbatim in the assistant reply when the Chair
// has gathered enough intake information to proceed to syllabus generation.
const intakeSentinel = "INTAKE_COMPLETE"

// Chair is the University Chair agent. It conducts the intake questionnaire
// (REQ-AGENT-001), generates the course syllabus, assigns homework due dates
// (REQ-AGENT-009), and handles all natural-language chat (REQ-AGENT-015)
// throughout the course lifecycle.
type Chair struct {
	client    *ThrottledClient
	pool      *pgxpool.Pool
	agentRepo *AgentRepository
	chatRepo  *ChatRepository
}

// @{"req": ["REQ-AGENT-001", "REQ-AGENT-009", "REQ-AGENT-015"]}
func NewChair(client *ThrottledClient, pool *pgxpool.Pool, agentRepo *AgentRepository, chatRepo *ChatRepository) *Chair {
	return &Chair{client: client, pool: pool, agentRepo: agentRepo, chatRepo: chatRepo}
}

// RunIntakeStep advances the intake questionnaire by one turn.
// On first call (no chat history), injects a synthetic trigger so the Chair
// sends the opening question. Returns done=true when the reply contains
// intakeSentinel, signalling that the runner can proceed to syllabus generation.
//
// @{"req": ["REQ-AGENT-001"]}
func (c *Chair) RunIntakeStep(ctx context.Context, courseID, studentID uuid.UUID) (done bool, reply string, err error) {
	topic, err := c.courseTopic(ctx, courseID)
	if err != nil {
		return false, "", fmt.Errorf("chair: intake step: %w", err)
	}

	history, err := c.chatRepo.GetFullHistory(ctx, courseID)
	if err != nil {
		return false, "", fmt.Errorf("chair: intake step: load history: %w", err)
	}

	messages := buildMessagesForIntake(history, topic)

	replyText, err := c.callClaude(ctx, studentID, courseID, intakeSystemPrompt(topic), messages, 512)
	if err != nil {
		return false, "", fmt.Errorf("chair: intake step: %w", err)
	}

	if _, err := c.chatRepo.InsertMessage(ctx, courseID, "assistant", replyText); err != nil {
		return false, "", fmt.Errorf("chair: intake step: store reply: %w", err)
	}

	return strings.Contains(replyText, intakeSentinel), replyText, nil
}

// GenerateSyllabus uses the completed intake conversation to produce an
// AsciiDoc syllabus and inserts it into the syllabi table.
// Returns the new syllabus ID.
//
// @{"req": ["REQ-AGENT-001"]}
func (c *Chair) GenerateSyllabus(ctx context.Context, courseID, studentID uuid.UUID) (uuid.UUID, error) {
	topic, err := c.courseTopic(ctx, courseID)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("chair: generate syllabus: %w", err)
	}

	history, err := c.chatRepo.GetFullHistory(ctx, courseID)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("chair: generate syllabus: load history: %w", err)
	}

	messages := buildMessages(history)
	if len(messages) == 0 {
		messages = []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock("Please generate the course syllabus based on my intake responses.")),
		}
	}

	syllabusAdoc, err := c.callClaude(ctx, studentID, courseID, syllabusSystemPrompt(topic), messages, 4096)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("chair: generate syllabus: call claude: %w", err)
	}

	var syllabusID uuid.UUID
	err = c.pool.QueryRow(ctx,
		`INSERT INTO syllabi (course_id, content_adoc, version)
		 VALUES ($1, $2, COALESCE((SELECT MAX(version) FROM syllabi WHERE course_id = $1), 0) + 1)
		 RETURNING id`,
		courseID, syllabusAdoc,
	).Scan(&syllabusID)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("chair: generate syllabus: insert: %w", err)
	}

	return syllabusID, nil
}

// AssignDueDates parses the approved syllabus to extract section titles, creates
// a homework entry for each section, and assigns weekly due dates starting from
// today (REQ-AGENT-009).
//
// @{"req": ["REQ-AGENT-009"]}
func (c *Chair) AssignDueDates(ctx context.Context, courseID, studentID uuid.UUID, syllabusAdoc string) error {
	params := anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeSonnet4_6,
		MaxTokens: 512,
		System: []anthropic.TextBlockParam{{
			Text: `Extract all section titles from the AsciiDoc syllabus. Return a JSON array of strings, one per section, in order. Return ONLY the JSON array with no other text. Example: ["Introduction", "Chapter 1: Basics"]`,
		}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(syllabusAdoc)),
		},
	}

	msg, err := c.client.Messages(ctx, studentID, courseID, params)
	if err != nil {
		return fmt.Errorf("chair: assign due dates: parse titles: %w", err)
	}
	if len(msg.Content) == 0 {
		return errors.New("chair: assign due dates: empty response")
	}

	raw := stripCodeFence(msg.Content[0].Text)
	var titles []string
	if err := json.Unmarshal([]byte(raw), &titles); err != nil {
		return fmt.Errorf("chair: assign due dates: unmarshal titles: %w", err)
	}
	if len(titles) == 0 {
		return errors.New("chair: assign due dates: no sections found in syllabus")
	}

	gradeWeight := 1.0 / float64(len(titles))
	now := time.Now().UTC()

	for i, title := range titles {
		var hwID uuid.UUID
		err := c.pool.QueryRow(ctx,
			`INSERT INTO homework (course_id, section_index, title, rubric, grade_weight)
			 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			courseID, i, title,
			"Submit a written response demonstrating understanding of: "+title+".",
			gradeWeight,
		).Scan(&hwID)
		if err != nil {
			return fmt.Errorf("chair: assign due dates: insert homework %d: %w", i, err)
		}

		dueDate := now.AddDate(0, 0, (i+1)*7)
		if _, err := c.pool.Exec(ctx,
			`INSERT INTO due_date_schedules (course_id, homework_id, due_date) VALUES ($1, $2, $3)`,
			courseID, hwID, dueDate,
		); err != nil {
			return fmt.Errorf("chair: assign due dates: insert schedule %d: %w", i, err)
		}
	}

	return nil
}

// Chat processes a single student message and returns the assistant reply.
// It stores both sides of the exchange in chat_messages (REQ-AGENT-015).
//
// @{"req": ["REQ-AGENT-015"]}
func (c *Chair) Chat(ctx context.Context, courseID, studentID uuid.UUID, userMessage string) (string, error) {
	if _, err := c.chatRepo.InsertMessage(ctx, courseID, "student", userMessage); err != nil {
		return "", fmt.Errorf("chair: chat: store student message: %w", err)
	}

	history, err := c.chatRepo.GetFullHistory(ctx, courseID)
	if err != nil {
		return "", fmt.Errorf("chair: chat: load history: %w", err)
	}

	messages := buildMessages(history)

	replyText, err := c.callClaude(ctx, studentID, courseID, chairSystemPrompt(), messages, 1024)
	if err != nil {
		return "", fmt.Errorf("chair: chat: call claude: %w", err)
	}

	if _, err := c.chatRepo.InsertMessage(ctx, courseID, "assistant", replyText); err != nil {
		return "", fmt.Errorf("chair: chat: store reply: %w", err)
	}

	return replyText, nil
}

// callClaude is a thin helper that builds MessageNewParams and calls the ThrottledClient.
//
// @{"req": ["REQ-AGENT-001", "REQ-AGENT-015"]}
func (c *Chair) callClaude(ctx context.Context, studentID, courseID uuid.UUID, system string, messages []anthropic.MessageParam, maxTokens int64) (string, error) {
	msg, err := c.client.Messages(ctx, studentID, courseID, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeSonnet4_6,
		MaxTokens: maxTokens,
		System:    []anthropic.TextBlockParam{{Text: system}},
		Messages:  messages,
	})
	if err != nil {
		return "", err
	}
	if len(msg.Content) == 0 {
		return "", errors.New("chair: empty response from claude")
	}
	return msg.Content[0].Text, nil
}

// courseTopic fetches the topic from the courses table using a server-role
// connection so the courses_server_select_policy RLS check passes.
//
// @{"req": ["REQ-AGENT-001"]}
func (c *Chair) courseTopic(ctx context.Context, courseID uuid.UUID) (string, error) {
	conn, err := db.AcquireServerConn(ctx, c.pool)
	if err != nil {
		return "", fmt.Errorf("chair: course topic: %w", err)
	}
	defer conn.Release()
	var topic string
	if err := conn.QueryRow(ctx, `SELECT topic FROM courses WHERE id = $1`, courseID).Scan(&topic); err != nil {
		return "", err
	}
	return topic, nil
}

// buildMessages converts ChatMessageRow history to Anthropic MessageParam slice.
// Consecutive messages with the same role are merged — Anthropic requires strict
// user/assistant alternation.
//
// @{"req": ["REQ-AGENT-001", "REQ-AGENT-015"]}
func buildMessages(history []ChatMessageRow) []anthropic.MessageParam {
	var msgs []anthropic.MessageParam
	for _, h := range history {
		isUser := h.Role == "student"
		// Merge into the previous turn when roles match.
		if len(msgs) > 0 {
			prev := msgs[len(msgs)-1]
			prevIsUser := prev.Role == anthropic.MessageParamRoleUser
			if isUser == prevIsUser {
				combined := prev.Content[0].OfText.Text + "\n" + h.Content
				if isUser {
					msgs[len(msgs)-1] = anthropic.NewUserMessage(anthropic.NewTextBlock(combined))
				} else {
					msgs[len(msgs)-1] = anthropic.NewAssistantMessage(anthropic.NewTextBlock(combined))
				}
				continue
			}
		}
		if isUser {
			msgs = append(msgs, anthropic.NewUserMessage(anthropic.NewTextBlock(h.Content)))
		} else {
			msgs = append(msgs, anthropic.NewAssistantMessage(anthropic.NewTextBlock(h.Content)))
		}
	}
	return msgs
}

// buildMessagesForIntake is like buildMessages but guarantees the first message
// is a user turn, injecting a synthetic trigger when history is empty or starts
// with an assistant message. This satisfies the Anthropic API requirement.
//
// @{"req": ["REQ-AGENT-001"]}
func buildMessagesForIntake(history []ChatMessageRow, topic string) []anthropic.MessageParam {
	msgs := buildMessages(history)
	if len(msgs) == 0 || msgs[0].Role == "assistant" {
		trigger := anthropic.NewUserMessage(anthropic.NewTextBlock(
			"I'd like to learn about " + topic + ". Please begin the intake questionnaire.",
		))
		msgs = append([]anthropic.MessageParam{trigger}, msgs...)
	}
	return msgs
}

// stripCodeFence removes optional ```json / ``` fences that Claude sometimes adds.
//
// @{"req": ["REQ-AGENT-001", "REQ-AGENT-009", "REQ-CONTENT-001"]}
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

// @{"req": ["REQ-AGENT-001"]}
func intakeSystemPrompt(topic string) string {
	return fmt.Sprintf(`You are the University Chair at Valory conducting an intake questionnaire for a student who wants to learn about %q.

Ask structured questions one at a time to understand:
1. Their current knowledge level (beginner / intermediate / advanced)
2. Their specific learning goals
3. How many hours per week they can dedicate
4. Any topics to prioritise or skip
5. Their preferred explanation style (examples-heavy, theory-first, etc.)

When you have received at least 3 substantive student replies that cover the points above, include the exact text %q on its own line at the end of your response. Do not include this marker until you have enough information.`, topic, intakeSentinel)
}

// @{"req": ["REQ-AGENT-001"]}
func syllabusSystemPrompt(topic string) string {
	return fmt.Sprintf(`You are the University Chair at Valory creating a personalised course syllabus for a student learning about %q.

Using the intake conversation above, write an AsciiDoc course syllabus that includes:
- A course title (= Title) and one-paragraph description
- 5–8 numbered sections with clear, descriptive titles (== Section N: Title)
- Two or three learning objectives per section
- Estimated time for each section

Format strictly as valid AsciiDoc. Keep the document under 300 lines.`, topic)
}

// @{"req": ["REQ-AGENT-015"]}
func chairSystemPrompt() string {
	return `You are the University Chair at Valory, an AI professor system. You help students throughout their learning journey. Be professional, encouraging, and concise. Answer questions about course content, guide students through material, and support their progress. When you do not know something, say so rather than guessing.`
}
