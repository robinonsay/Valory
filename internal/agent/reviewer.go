// reviewer.go — the Reviewer agent.
// Checks generated lesson content for citation completeness (REQ-CONTENT-001).
// The correction loop and escalation logic live in runner.go (REQ-AGENT-007/008).
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valory/valory/internal/db"
)

// ReviewResult is returned by Reviewer.ReviewSection.
type ReviewResult struct {
	Approved bool
	Feedback string // non-empty when Approved == false
}

// Reviewer verifies that generated content meets academic quality standards,
// primarily citation completeness (REQ-CONTENT-001).
type Reviewer struct {
	client    *ThrottledClient
	pool      *pgxpool.Pool
	agentRepo *AgentRepository
}

// @{"req": ["REQ-CONTENT-001"]}
func NewReviewer(client *ThrottledClient, pool *pgxpool.Pool, agentRepo *AgentRepository) *Reviewer {
	return &Reviewer{client: client, pool: pool, agentRepo: agentRepo}
}

// ReviewSection checks a single piece of lesson content for citations and
// overall quality. Returns ReviewResult with Approved=true if the content
// passes. When Approved=false, Feedback describes what must be fixed.
//
// If the content passes, citation_verified is set to true in the DB so that
// GetSectionContent (content/repository.go) allows student access.
//
// @{"req": ["REQ-CONTENT-001"]}
func (r *Reviewer) ReviewSection(ctx context.Context, runID, courseID, studentID uuid.UUID, contentID uuid.UUID, contentAdoc string) (ReviewResult, error) {
	snippet := contentAdoc
	if len(snippet) > 4000 {
		snippet = snippet[:4000]
	}

	systemPrompt := `You are a strict academic quality reviewer checking lesson content.

Verify that the content:
1. Contains at least one properly cited source using [Source: ...] notation or standard citation formats
2. Is coherent, complete, and addresses the stated section topic
3. Is educationally sound

Respond with a JSON object only — no other text:
{"approved": true/false, "feedback": "specific issues to fix, or empty string if approved"}

Be strict about citations — this is a hard requirement (REQ-CONTENT-001). Content without at least one citation must be rejected.`

	msg, err := r.client.Messages(ctx, studentID, courseID, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeHaiku4_5,
		MaxTokens: 512,
		System:    []anthropic.TextBlockParam{{Text: systemPrompt}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(snippet)),
		},
	})
	if err != nil {
		return ReviewResult{}, fmt.Errorf("reviewer: api call: %w", err)
	}
	if len(msg.Content) == 0 {
		return ReviewResult{}, errors.New("reviewer: empty response from claude")
	}

	raw := stripCodeFence(msg.Content[0].Text)

	var result struct {
		Approved bool   `json:"approved"`
		Feedback string `json:"feedback"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		// Unparseable response — treat as not approved; surface the raw text as feedback.
		return ReviewResult{Approved: false, Feedback: raw}, nil
	}

	if result.Approved {
		// Use a server-role connection: lesson_content_server_update_policy requires
		// app.current_role = 'server' for UPDATE operations.
		conn, connErr := db.AcquireServerConn(ctx, r.pool)
		if connErr != nil {
			return ReviewResult{}, fmt.Errorf("reviewer: server conn for citation update: %w", connErr)
		}
		_, err = conn.Exec(ctx, `UPDATE lesson_content SET citation_verified = true WHERE id = $1`, contentID)
		conn.Release()
		if err != nil {
			return ReviewResult{}, fmt.Errorf("reviewer: set citation_verified: %w", err)
		}
	}

	return ReviewResult{Approved: result.Approved, Feedback: result.Feedback}, nil
}


