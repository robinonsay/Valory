// Package agent implements the multi-agent pipeline for Valory.
// chair.go contains the University Chair agent, responsible for intake,
// syllabus generation, due-date assignment, and student chat.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valory/valory/internal/db"
	"github.com/valory/valory/internal/image"
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
	return c.RunIntakeStepWithImages(ctx, courseID, studentID, nil)
}

// RunIntakeStepWithImages is the vision-aware variant of RunIntakeStep.
// When visionBlocks is non-empty the current student turn is sent to Claude
// as [imageBlock..., textBlock] for this call only — history turns remain
// text-only to avoid re-transmitting images on every subsequent turn.
// When visionBlocks is nil the behaviour is identical to RunIntakeStep.
//
// The student message was already stored by the handler before this call;
// this method reads the full history (including that message) and replaces
// the last user turn with the vision-aware content for the model call.
// History in chat_messages stays text-only.
//
// @{"req": ["REQ-AGENT-001", "REQ-AGENT-023"]}
func (c *Chair) RunIntakeStepWithImages(ctx context.Context, courseID, studentID uuid.UUID, visionBlocks []anthropic.ContentBlockParamUnion) (done bool, reply string, err error) {
	topic, err := c.courseTopic(ctx, courseID)
	if err != nil {
		return false, "", fmt.Errorf("chair: intake step: %w", err)
	}

	history, err := c.chatRepo.GetFullHistory(ctx, courseID)
	if err != nil {
		return false, "", fmt.Errorf("chair: intake step: load history: %w", err)
	}

	msgs := buildMessagesForIntake(history, topic)

	// When vision blocks are present, replace the last user turn (the student
	// message stored just before this call) with the multi-block content so
	// Claude sees image context. History storage remains text-only.
	if len(visionBlocks) > 0 && len(msgs) > 0 {
		last := msgs[len(msgs)-1]
		if last.Role == anthropic.MessageParamRoleUser {
			var lastText string
			if len(last.Content) > 0 && last.Content[0].OfText != nil {
				lastText = last.Content[0].OfText.Text
			}
			msgs[len(msgs)-1] = anthropic.MessageParam{
				Role:    anthropic.MessageParamRoleUser,
				Content: image.BuildCurrentTurnContent(lastText, visionBlocks),
			}
		}
	}

	replyText, err := c.callClaude(ctx, studentID, courseID, intakeSystemPrompt(topic), msgs, 512)
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

	messages := buildMessagesForSyllabus(history, topic)

	syllabusAdoc, err := c.callClaude(ctx, studentID, courseID, syllabusSystemPrompt(topic), messages, 4096)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("chair: generate syllabus: call claude: %w", err)
	}
	// Claude often wraps the document in ```asciidoc fences; stored verbatim
	// they render as literal first/last lines in the syllabus view.
	syllabusAdoc = stripCodeFence(syllabusAdoc)

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
	return c.ChatWithImages(ctx, courseID, studentID, userMessage, nil)
}

// ChatWithImages is the vision-aware variant of Chat. When visionBlocks is
// non-empty the current user turn is built as a multi-block content list
// [imageBlock..., textBlock] per the SDD. History turns remain text-only.
// When visionBlocks is nil or empty the call is identical to Chat.
//
// @{"req": ["REQ-AGENT-015", "REQ-AGENT-023"]}
func (c *Chair) ChatWithImages(ctx context.Context, courseID, studentID uuid.UUID, userMessage string, visionBlocks []anthropic.ContentBlockParamUnion) (string, error) {
	if _, err := c.chatRepo.InsertMessage(ctx, courseID, "student", userMessage); err != nil {
		return "", fmt.Errorf("chair: chat: store student message: %w", err)
	}

	history, err := c.chatRepo.GetFullHistory(ctx, courseID)
	if err != nil {
		return "", fmt.Errorf("chair: chat: load history: %w", err)
	}

	// Build history turns as text-only params (no image re-send across prior
	// turns — current turn only carries images to bound token cost).
	historyMsgs := ensureUserFirst(buildMessages(history))

	// Replace the last message (the student turn just stored) with the
	// vision-aware multi-block content. The last element of historyMsgs is
	// always the student turn because InsertMessage ran above and
	// buildMessages maps "student" → user role.
	var messages []anthropic.MessageParam
	if len(visionBlocks) > 0 && len(historyMsgs) > 0 {
		messages = historyMsgs[:len(historyMsgs)-1]
		currentContent := image.BuildCurrentTurnContent(userMessage, visionBlocks)
		currentTurn := anthropic.MessageParam{
			Role:    anthropic.MessageParamRoleUser,
			Content: currentContent,
		}
		messages = append(messages, currentTurn)
	} else {
		messages = historyMsgs
	}

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

// courseStatus fetches status and intake_kickoff_sent from courses using a
// server-role connection. Used by the intake-aware chat handler to branch on
// the current lifecycle phase without depending on the request-scoped conn.
//
// @{"req": ["REQ-AGENT-019"]}
func (c *Chair) courseStatus(ctx context.Context, courseID uuid.UUID) (string, error) {
	conn, err := db.AcquireServerConn(ctx, c.pool)
	if err != nil {
		return "", fmt.Errorf("chair: course status: %w", err)
	}
	defer conn.Release()
	var status string
	if err := conn.QueryRow(ctx, `SELECT status FROM courses WHERE id = $1`, courseID).Scan(&status); err != nil {
		return "", fmt.Errorf("chair: course status: %w", err)
	}
	return status, nil
}

// transitionToSyllabusDraft transitions the course from intake to
// syllabus_draft using a server-role connection. The WHERE clause guards
// against double-transition: if the status has already moved on (e.g. a
// concurrent request), the UPDATE affects zero rows, which is safe.
//
// @{"req": ["REQ-AGENT-019"]}
func (c *Chair) transitionToSyllabusDraft(ctx context.Context, courseID uuid.UUID) error {
	conn, err := db.AcquireServerConn(ctx, c.pool)
	if err != nil {
		return fmt.Errorf("chair: transition syllabus_draft: %w", err)
	}
	defer conn.Release()
	_, err = conn.Exec(ctx,
		`UPDATE courses SET status = 'syllabus_draft', updated_at = now() WHERE id = $1 AND status = 'intake'`,
		courseID,
	)
	return err
}

// updateLastAssistantMessage overwrites the content of the most recently
// inserted assistant message for the course. This is called after sentinel
// stripping so the history stored in chat_messages never contains the raw
// INTAKE_COMPLETE marker.
//
// @{"req": ["REQ-AGENT-019"]}
func (c *Chair) updateLastAssistantMessage(ctx context.Context, courseID uuid.UUID, newContent string) error {
	conn, err := db.AcquireServerConn(ctx, c.pool)
	if err != nil {
		return fmt.Errorf("chair: update last assistant message: %w", err)
	}
	defer conn.Release()
	// ORDER BY created_at DESC, id DESC: the secondary id sort is the tiebreaker
	// for concurrent inserts that land in the same timestamp bucket. Without it,
	// two rows with identical created_at could produce non-deterministic selection
	// and overwrite the wrong message.
	_, err = conn.Exec(ctx,
		`UPDATE chat_messages SET content = $1
		 WHERE id = (
		     SELECT id FROM chat_messages
		     WHERE course_id = $2 AND role = 'assistant'
		     ORDER BY created_at DESC, id DESC LIMIT 1
		 )`,
		newContent, courseID,
	)
	return err
}

// StartIntake satisfies the course.IntakeStarter interface. It calls
// kickoffIntake (the idempotent atomic gate) and logs any error so the course
// creation path remains fast. The goroutine is launched by the caller
// (CourseHandler.createCourse); this method executes synchronously within it.
//
// @{"req": ["REQ-AGENT-018"]}
func (c *Chair) StartIntake(ctx context.Context, courseID, studentID uuid.UUID) {
	if err := c.kickoffIntake(ctx, courseID, studentID); err != nil {
		log.Printf("chair: StartIntake for course %s: %v", courseID, err)
	}
}

// kickoffIntake sends the opening intake question for a newly created course.
// It is idempotent: the UPDATE ... WHERE intake_kickoff_sent = false acts as
// an atomic gate — if two goroutines race here only one will see rowsAffected=1
// and proceed to call RunIntakeStep. The other will see 0 rows and exit
// cleanly.
//
// Attempt cap: the UPDATE atomically increments intake_kickoff_attempts so
// that N concurrent lazy-retries from the history endpoint cannot each fire
// their own Claude call. On RunIntakeStep failure the gate is reset
// (intake_kickoff_sent = false) only when attempts < 3. At attempts >= 3,
// intake_kickoff_sent is left true permanently so no further retries fire.
// This is safe because the student can still type in the chat box, triggering
// POST /chat which calls RunIntakeStep directly — a failed kickoff never
// bricks the course.
//
// @{"req": ["REQ-AGENT-018"]}
func (c *Chair) kickoffIntake(ctx context.Context, courseID, studentID uuid.UUID) error {
	// Atomically claim the right to send the opening question AND increment the
	// attempt counter. The single UPDATE prevents TOCTOU between the attempt read
	// and the flag set — both happen in one statement that only fires when
	// intake_kickoff_sent = false.
	conn, err := db.AcquireServerConn(ctx, c.pool)
	if err != nil {
		return fmt.Errorf("chair: kickoff intake: acquire conn: %w", err)
	}
	var attempts int
	err = conn.QueryRow(ctx,
		`UPDATE courses
		    SET intake_kickoff_sent = true,
		        intake_kickoff_attempts = intake_kickoff_attempts + 1
		  WHERE id = $1 AND intake_kickoff_sent = false
		  RETURNING intake_kickoff_attempts`,
		courseID,
	).Scan(&attempts)
	conn.Release()
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Another goroutine or a previous boot already sent the opening question.
			return nil
		}
		return fmt.Errorf("chair: kickoff intake: mark sent: %w", err)
	}

	// Create an intake agent_run to anchor pipeline events for this lifecycle.
	run, err := c.agentRepo.CreateRun(ctx, courseID, "intake")
	if err != nil {
		// Non-fatal for the kickoff itself, but log so operators can diagnose
		// missing pipeline events if the event emission below also fails.
		log.Printf("chair: kickoff intake: create agent run: %v", err)
	}

	_, _, err = c.RunIntakeStep(ctx, courseID, studentID)
	if err != nil {
		// The kickoff failed. Reset intake_kickoff_sent only when attempts < 3
		// so the lazy-retry in the history endpoint can try again next load.
		// At attempts >= 3 we leave intake_kickoff_sent=true: the student can
		// still initiate intake via POST /chat, so the course is not bricked.
		const maxKickoffAttempts = 3
		if attempts < maxKickoffAttempts {
			resetConn, resetErr := db.AcquireServerConn(ctx, c.pool)
			if resetErr == nil {
				if _, execErr := resetConn.Exec(ctx,
					`UPDATE courses SET intake_kickoff_sent = false WHERE id = $1`,
					courseID,
				); execErr != nil {
					log.Printf("chair: kickoff intake: reset kickoff_sent flag: %v", execErr)
				}
				resetConn.Release()
			}
		} else {
			log.Printf("chair: kickoff intake: giving up after %d attempts for course %s — student can still initiate via POST /chat", attempts, courseID)
		}
		return fmt.Errorf("chair: kickoff intake: %w", err)
	}

	// Mark the intake run completed; it remains as the anchor for future
	// status_change events emitted when the sentinel is detected.
	if run.ID != (uuid.UUID{}) {
		if err := c.agentRepo.SetRunStatus(ctx, run.ID, "completed", nil); err != nil {
			log.Printf("chair: kickoff intake: set run status completed: %v", err)
		}
	}

	return nil
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

// ensureUserFirst prepends a neutral synthetic user turn when the
// conversation starts with an assistant message — the Anthropic API requires
// the first message to be a user turn. Kickoff-era courses always start with
// the Chair's opening question, so general chat needs this anchor too.
//
// @{"req": ["REQ-AGENT-015"]}
func ensureUserFirst(msgs []anthropic.MessageParam) []anthropic.MessageParam {
	if len(msgs) > 0 && msgs[0].Role == "assistant" {
		trigger := anthropic.NewUserMessage(anthropic.NewTextBlock(
			"Let's continue our course discussion.",
		))
		msgs = append([]anthropic.MessageParam{trigger}, msgs...)
	}
	return msgs
}

// buildMessagesForSyllabus converts the intake history for the syllabus
// generation call. The Anthropic API requires the first message to be a user
// turn AND rejects conversations ending with an assistant turn ("assistant
// message prefill" is unsupported on this model). Since the kickoff feature,
// intake history both starts (opening question) and ends (INTAKE_COMPLETE
// reply) with the Chair, so both ends must be anchored with synthetic user
// turns or the call 400s — found live by the journey e2e suite.
//
// @{"req": ["REQ-AGENT-020"]}
func buildMessagesForSyllabus(history []ChatMessageRow, topic string) []anthropic.MessageParam {
	msgs := buildMessages(history)
	closing := anthropic.NewUserMessage(anthropic.NewTextBlock(
		"Please generate the course syllabus for " + topic + " based on my intake responses.",
	))
	if len(msgs) == 0 {
		return []anthropic.MessageParam{closing}
	}
	if msgs[0].Role == "assistant" {
		trigger := anthropic.NewUserMessage(anthropic.NewTextBlock(
			"I'd like to learn about " + topic + ".",
		))
		msgs = append([]anthropic.MessageParam{trigger}, msgs...)
	}
	if msgs[len(msgs)-1].Role == "assistant" {
		msgs = append(msgs, closing)
	}
	return msgs
}

// stripCodeFence removes optional ```json / ``` fences that Claude sometimes adds.
//
// @{"req": ["REQ-AGENT-001", "REQ-AGENT-009", "REQ-CONTENT-001"]}
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// Drop the whole opening fence line so any language tag goes with it
		// (```json, ```asciidoc, …) — a bare TrimPrefix("```") would leave
		// the tag word behind as the first line of the document.
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		} else {
			s = strings.TrimPrefix(s, "```")
		}
	}
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return strings.TrimSpace(s)
}

// @{"req": ["REQ-AGENT-001", "REQ-AGENT-022"]}
func intakeSystemPrompt(topic string) string {
	return fmt.Sprintf(`You are the University Chair at Valory conducting an intake questionnaire for a student who wants to learn about %q.

Ask structured questions one at a time to understand:
1. Their current knowledge level (beginner / intermediate / advanced)
2. Their specific learning goals
3. How many hours per week they can dedicate
4. Any topics to prioritise or skip
5. Their preferred explanation style (examples-heavy, theory-first, etc.)

When you have received at least 3 substantive student replies that cover the points above, include the exact text %q on its own line at the end of your response. Do not include this marker until you have enough information.

Format your replies using Markdown:
- Use bullet lists for structured content
- Use **bold** to emphasize key terms
- For mathematical notation, use LaTeX delimited by $...$ (inline) and $$...$$ (display)`, topic, intakeSentinel)
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

// @{"req": ["REQ-AGENT-015", "REQ-AGENT-022"]}
func chairSystemPrompt() string {
	return `You are the University Chair at Valory, an AI professor system. You help students throughout their learning journey. Be professional, encouraging, and concise. Answer questions about course content, guide students through material, and support their progress. When you do not know something, say so rather than guessing.

Format your replies using Markdown:
- Use headings sparingly (## for major sections only)
- Use bullet lists and numbered lists for structured content
- Use **bold** to emphasize key terms and concepts
- Use fenced code blocks for code examples
- Do not wrap entire replies in code fences

For mathematical notation, use LaTeX delimited by:
- $...$ for inline mathematics (e.g., $x^2 + y^2 = z^2$)
- $$...$$ for display mathematics (centered, on its own line)

Keep replies concise and well-formatted.`
}
