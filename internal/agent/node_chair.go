// node_chair.go — node-scoped Chair entry points for the knowledge-tree pipeline.
//
// Adds GenerateNode and RefineNode as methods on Chair, generalising the two
// existing flat-course entry points (GenerateSyllabus / GenerateSyllabusFromParams)
// into a single node-scoped interface that works for both student (course_id) and
// admin (draft_id) contexts without touching the existing flat-course paths.
//
// Token accounting is parameterised on the call-site context:
//   - Student path: actorID = student_id, contextID = course_id → Messages
//   - Admin path:   actorID = admin_id,   contextID = draft_id  → MessagesForDraft
//
// MessagesForDraft is a new method on ThrottledClient defined here (same package).
// It shares the same API-call and retry structure as Messages but UPSERTs against
// the draft_id partial unique index on agent_token_usage.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// nodeGenerationMaxTokens is the per-call token budget for node generation.
// Content nodes can produce up to 500 AsciiDoc lines; 8192 output tokens gives
// comfortable headroom while remaining within per-student/admin caps.
const nodeGenerationMaxTokens = int64(8192)

// --------------------------------------------------------------------------
// ThrottledClient extension: MessagesForDraft
// --------------------------------------------------------------------------

// MessagesForDraft is the admin-draft counterpart to Messages. It enforces the
// global token cap against the draft_id partial index on agent_token_usage and
// UPSERTs token usage with draft_id as the conflict target. The API-call logic
// (key resolution, retry with exponential backoff) is identical to Messages.
//
// Defined here rather than in client.go so it colocates with its only callers
// (GenerateNode / RefineNode on the admin path) while keeping the UPSERT logic
// in one place per constraint.
//
// @{"req": ["REQ-AGENT-035", "REQ-AGENT-056"]}
func (c *ThrottledClient) MessagesForDraft(
	ctx context.Context,
	draftID uuid.UUID,
	params anthropic.MessageNewParams,
) (*anthropic.Message, error) {
	// Step 1: Check per-draft token cap (uses the same config key as per-student).
	cap := c.configSvc.GetInt64("per_student_token_limit")
	if cap > 0 {
		var used int64
		err := c.pool.QueryRow(ctx,
			`SELECT COALESCE(total_tokens_used, 0) FROM agent_token_usage WHERE draft_id = $1`,
			draftID,
		).Scan(&used)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if used >= cap {
			return nil, ErrTokenCapExceeded
		}
	}

	// Step 2: Resolve API key and optional base URL per call — same logic as Messages.
	var callOpts []option.RequestOption
	if c.secrets != nil {
		if resolvedKey := c.secrets.Get(ctx, "anthropic_api_key"); resolvedKey != "" {
			callOpts = append(callOpts, option.WithAPIKey(resolvedKey))
		}
	}
	if baseURL := c.configSvc.GetString("anthropic_base_url"); baseURL != "" {
		callOpts = append(callOpts, option.WithBaseURL(baseURL))
	} else if envURL := os.Getenv("ANTHROPIC_BASE_URL"); envURL != "" {
		callOpts = append(callOpts, option.WithBaseURL(envURL))
	}

	// Step 3: Retry loop with exponential backoff on HTTP 429 — same structure as Messages.
	retryLimit := c.configSvc.GetInt64("agent_retry_limit")
	var msg *anthropic.Message
	for attempt := int64(0); attempt < retryLimit; attempt++ {
		var err error
		msg, err = c.client.Messages.New(ctx, params, callOpts...)
		if err == nil {
			break
		}

		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) && apiErr.StatusCode == 429 {
			baseDelay := time.Second
			maxDelay := 60 * time.Second
			const maxShift = 30
			shift := attempt
			if shift > maxShift {
				shift = maxShift
			}
			delay := baseDelay * (1 << shift)
			if delay > maxDelay {
				delay = maxDelay
			}
			if delay <= 0 {
				delay = maxDelay
			}
			jitter := time.Duration(rand.Int63n(int64(delay / 4)))
			select {
			case <-time.After(delay + jitter):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}
		return nil, err
	}
	if msg == nil {
		return nil, ErrRateLimitExhausted
	}

	// Step 4: UPSERT token usage against the draft_id partial unique index.
	// The partial index agent_token_usage_draft_idx is defined as:
	//   UNIQUE (draft_id) WHERE draft_id IS NOT NULL
	// Postgres requires the ON CONFLICT predicate to match the index predicate
	// exactly; omitting WHERE draft_id IS NOT NULL causes a runtime error
	// ("no unique or exclusion constraint matching the ON CONFLICT specification").
	totalTokens := msg.Usage.InputTokens + msg.Usage.OutputTokens
	_, err := c.pool.Exec(ctx,
		`INSERT INTO agent_token_usage (draft_id, total_tokens_used)
		 VALUES ($1, $2)
		 ON CONFLICT (draft_id) WHERE draft_id IS NOT NULL
		 DO UPDATE SET total_tokens_used = agent_token_usage.total_tokens_used + EXCLUDED.total_tokens_used`,
		draftID, totalTokens,
	)
	if err != nil {
		return nil, err
	}

	return msg, nil
}

// --------------------------------------------------------------------------
// Chair node entry points
// --------------------------------------------------------------------------

// GenerateNode generates content for one course_node or draft_node from scratch.
// nodeType controls which system prompt is selected (syllabus / section_goal /
// concept / content). priorContext is the accumulated node_chats history for the
// node and may be nil for a first-time generation.
//
// The caller sets isDraftContext = true for admin draft nodes (uses contextID as
// draftID in token accounting) and false for student course nodes (contextID is
// courseID, actorID is studentID).
//
// The generated content is returned as a JSON payload whose shape is node-type
// specific (see nodePayloadForResponse). The caller is responsible for persisting
// the payload to course_nodes / draft_nodes and emitting pipeline events.
//
// @{"req": ["REQ-AGENT-035", "REQ-AGENT-036", "REQ-AGENT-044", "REQ-AGENT-060"]}
func (c *Chair) GenerateNode(
	ctx context.Context,
	actorID uuid.UUID,
	contextID uuid.UUID,
	isDraftContext bool,
	nodeType string,
	topic string,
	level string,
	parameters json.RawMessage,
	priorContext []NodeChatMessage,
) (json.RawMessage, error) {
	systemPrompt := nodeSystemPrompt(nodeType, topic, level, parameters)
	msgs := buildNodeMessages(priorContext, topic, nodeType)

	var raw string
	var err error

	if isDraftContext {
		raw, err = c.callClaudeForDraft(ctx, contextID, systemPrompt, msgs, nodeGenerationMaxTokens)
	} else {
		raw, err = c.callClaude(ctx, actorID, contextID, systemPrompt, msgs, nodeGenerationMaxTokens)
	}
	if err != nil {
		return nil, fmt.Errorf("chair: generate node %s: %w", nodeType, err)
	}

	raw = stripCodeFence(raw)
	return nodePayloadForResponse(nodeType, raw)
}

// RefineNode refines an existing node's content based on new human feedback.
// existingPayload is the current node payload. chatHistory is the full node_chats
// history including the new user feedback message, which must be persisted by the
// caller before this call so the Chair sees the complete conversation.
//
// @{"req": ["REQ-AGENT-050", "REQ-AGENT-051"]}
func (c *Chair) RefineNode(
	ctx context.Context,
	actorID uuid.UUID,
	contextID uuid.UUID,
	isDraftContext bool,
	nodeType string,
	existingPayload json.RawMessage,
	chatHistory []NodeChatMessage,
) (json.RawMessage, error) {
	systemPrompt := nodeRefineSystemPrompt(nodeType, existingPayload)
	msgs := buildNodeChatMessages(chatHistory)

	if len(msgs) == 0 {
		// No feedback history: produce a minor revision without specific direction.
		msgs = []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock("Please improve the existing content.")),
		}
	}

	var raw string
	var err error

	if isDraftContext {
		raw, err = c.callClaudeForDraft(ctx, contextID, systemPrompt, msgs, nodeGenerationMaxTokens)
	} else {
		raw, err = c.callClaude(ctx, actorID, contextID, systemPrompt, msgs, nodeGenerationMaxTokens)
	}
	if err != nil {
		return nil, fmt.Errorf("chair: refine node %s: %w", nodeType, err)
	}

	raw = stripCodeFence(raw)
	return nodePayloadForResponse(nodeType, raw)
}

// callClaudeForDraft is the draft-context variant of callClaude. Routes token
// accounting through MessagesForDraft (draft_id partial index).
//
// @{"req": ["REQ-AGENT-035", "REQ-AGENT-056"]}
func (c *Chair) callClaudeForDraft(
	ctx context.Context,
	draftID uuid.UUID,
	system string,
	messages []anthropic.MessageParam,
	maxTokens int64,
) (string, error) {
	msg, err := c.client.MessagesForDraft(ctx, draftID, anthropic.MessageNewParams{
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

// --------------------------------------------------------------------------
// Per-node-type system prompts
// --------------------------------------------------------------------------

// nodeSystemPrompt returns the generation system prompt for the given node type.
// The four node types correspond to the four content layers of the knowledge tree
// (api-sse §6.3).
//
// @{"req": ["REQ-AGENT-044", "REQ-AGENT-060"]}
func nodeSystemPrompt(nodeType, topic, level string, parameters json.RawMessage) string {
	var paramStr string
	if len(parameters) > 0 && string(parameters) != "{}" && string(parameters) != "null" {
		paramStr = "\n\nAdditional parameters:\n" + string(parameters)
	}
	switch nodeType {
	case "syllabus":
		return nodeSyllabusPrompt(topic, level, paramStr)
	case "section_goal":
		return nodeSectionGoalPrompt(topic, level, paramStr)
	case "concept":
		return nodeConceptPrompt(topic, level, paramStr)
	case "content":
		return nodeContentPrompt(topic, level, paramStr)
	default:
		return fmt.Sprintf(
			"You are the University Chair at Valory generating a %s node for the topic %q at the %s level."+
				" Produce clear, accurate educational content.%s",
			nodeType, topic, level, paramStr,
		)
	}
}

// nodeRefineSystemPrompt returns the refinement system prompt. It embeds the
// existing payload so the Chair revises rather than regenerates from scratch.
//
// @{"req": ["REQ-AGENT-050", "REQ-AGENT-051"]}
func nodeRefineSystemPrompt(nodeType string, existingPayload json.RawMessage) string {
	return fmt.Sprintf(
		"You are the University Chair at Valory revising a %s node based on human feedback.\n\n"+
			"Existing content:\n%s\n\n"+
			"Revise the content based on the feedback in the conversation below. "+
			"Return only the revised content in the same format as the original.",
		nodeType, string(existingPayload),
	)
}

// nodeSyllabusPrompt is the system prompt for syllabus node generation.
// Mirrors assignmentSyllabusPrompt for the tree-generation path.
//
// @{"req": ["REQ-AGENT-044", "REQ-AGENT-060"]}
func nodeSyllabusPrompt(topic, level, paramStr string) string {
	return fmt.Sprintf(`You are the University Chair at Valory creating a course syllabus node.

Topic: %q
Level: %s%s

Write an AsciiDoc course syllabus that includes:
- A course title (= Title) and one-paragraph description
- 5–8 numbered sections with clear, descriptive titles (== Section N: Title)
- Two or three learning objectives per section
- Estimated time for each section

Tailor the depth and pacing to the stated level (%s):
- beginner: assume no prior knowledge, introduce concepts from scratch
- intermediate: assume familiarity with basics, focus on applying concepts
- advanced: assume solid domain knowledge, focus on nuanced and complex topics

Format strictly as valid AsciiDoc. Keep the document under 300 lines.
Return the AsciiDoc content only — no explanatory text, no code fences.`,
		topic, level, paramStr, level)
}

// nodeSectionGoalPrompt is the system prompt for section_goal node generation.
// Each section_goal describes the learning objectives for one syllabus section.
//
// @{"req": ["REQ-AGENT-044", "REQ-AGENT-060"]}
func nodeSectionGoalPrompt(topic, level, paramStr string) string {
	return fmt.Sprintf(`You are the University Chair at Valory defining a section goal node.

Topic: %q
Level: %s%s

For the section described in the conversation, produce a JSON object with:
- "title": string — the section title
- "section_index": integer — the zero-based section index
- "objectives": array of strings — two or three measurable learning objectives

Return a single valid JSON object only — no prose, no code fences.`,
		topic, level, paramStr)
}

// nodeConceptPrompt is the system prompt for concept node generation.
// Each concept describes one atomic idea within a section.
//
// @{"req": ["REQ-AGENT-044", "REQ-AGENT-060"]}
func nodeConceptPrompt(topic, level, paramStr string) string {
	return fmt.Sprintf(`You are the University Chair at Valory defining a concept node.

Topic: %q
Level: %s%s

For the concept described in the conversation, produce a JSON object with:
- "title": string — the concept title (concise noun phrase)
- "description": string — one to three sentence summary of the concept and its significance

Return a single valid JSON object only — no prose, no code fences.`,
		topic, level, paramStr)
}

// nodeContentPrompt is the system prompt for content node generation.
// Content nodes are full AsciiDoc lesson documents (max 500 lines per CLAUDE.md).
//
// @{"req": ["REQ-AGENT-044", "REQ-AGENT-060"]}
func nodeContentPrompt(topic, level, paramStr string) string {
	return fmt.Sprintf(`You are the University Chair at Valory writing a content node — a complete AsciiDoc lesson.

Topic: %q
Level: %s%s

Write an AsciiDoc lesson document that:
- Opens with a = Title heading
- Contains clearly delineated sections (== Heading level 2)
- Includes concrete examples, code blocks where relevant, and exercises
- Uses include:: directives for sub-documents when a section would exceed ~100 lines,
  keeping this top-level file under 500 lines total
- Ends with a == Summary section

Tailor depth to the level (%s):
- beginner: clear definitions, step-by-step examples, minimal assumed knowledge
- intermediate: building on fundamentals, real-world application patterns
- advanced: nuanced edge cases, performance considerations, idiomatic usage

Format strictly as valid AsciiDoc. Keep the document under 500 lines.
Return the AsciiDoc content only — no explanatory prose, no code fences.`,
		topic, level, paramStr, level)
}

// --------------------------------------------------------------------------
// Message-building helpers
// --------------------------------------------------------------------------

// buildNodeMessages constructs Anthropic MessageParams for an initial generation
// call. Prior chat turns are included first as user/assistant history, followed by
// a synthetic generation-request user turn.
//
// @{"req": ["REQ-AGENT-051", "REQ-AGENT-060"]}
func buildNodeMessages(priorContext []NodeChatMessage, topic, nodeType string) []anthropic.MessageParam {
	msgs := buildNodeChatMessages(priorContext)
	trigger := fmt.Sprintf("Please generate the %s content for the topic: %s", nodeType, topic)
	msgs = append(msgs, anthropic.NewUserMessage(anthropic.NewTextBlock(trigger)))
	return msgs
}

// buildNodeChatMessages converts a NodeChatMessage slice to Anthropic MessageParam
// slice. Consecutive messages with the same role are merged to satisfy the API's
// strict user/assistant alternation requirement.
//
// @{"req": ["REQ-AGENT-051", "REQ-AGENT-055"]}
func buildNodeChatMessages(history []NodeChatMessage) []anthropic.MessageParam {
	var msgs []anthropic.MessageParam
	for _, h := range history {
		isUser := h.Role == "user"
		if len(msgs) > 0 {
			prev := msgs[len(msgs)-1]
			prevIsUser := prev.Role == anthropic.MessageParamRoleUser
			if isUser == prevIsUser {
				var prevText string
				if len(prev.Content) > 0 && prev.Content[0].OfText != nil {
					prevText = prev.Content[0].OfText.Text
				}
				combined := prevText + "\n" + h.Content
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

// nodePayloadForResponse wraps the raw Claude output in the node-type-specific
// JSON payload shape expected by course_nodes / draft_nodes.
//
// AsciiDoc node types (syllabus, content): raw string stored under content_adoc.
// JSON node types (section_goal, concept): raw is validated and embedded as JSON.
//
// @{"req": ["REQ-AGENT-044", "REQ-AGENT-060"]}
func nodePayloadForResponse(nodeType, raw string) (json.RawMessage, error) {
	switch nodeType {
	case "syllabus", "content":
		result := map[string]string{"content_adoc": raw}
		b, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("chair: node payload: marshal %s: %w", nodeType, err)
		}
		return json.RawMessage(b), nil

	case "section_goal", "concept":
		// Validate the JSON Claude returned before storing it.
		var v json.RawMessage
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			// Claude returned non-JSON; wrap in a fallback so the write succeeds
			// and the caller can surface the parse error via a node event.
			result := map[string]string{"raw": raw}
			b, mErr := json.Marshal(result)
			if mErr != nil {
				return nil, fmt.Errorf("chair: node payload: fallback marshal %s: %w", nodeType, mErr)
			}
			return json.RawMessage(b), nil
		}
		return v, nil

	default:
		result := map[string]string{"content": raw}
		b, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("chair: node payload: marshal %s: %w", nodeType, err)
		}
		return json.RawMessage(b), nil
	}
}
