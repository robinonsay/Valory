package profile

import (
	"context"
	"fmt"
	"log"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// onboardingSentinel is the marker the onboarding agent includes in its reply
// when it has gathered enough information to proceed to summarization.
// Analogous to the INTAKE_COMPLETE sentinel in chair.go.
const onboardingSentinel = "ONBOARDING_COMPLETE"

// onboardingSystemPrompt is the system prompt for the onboarding questionnaire
// agent.  It instructs Claude to ask 3-5 short questions one at a time covering
// the five learning-preference axes, and to append the sentinel when at least
// three substantive answers covering axes 1-3 have been received.
//
// @{"req": ["REQ-PROFILE-011"]}
func onboardingSystemPrompt() string {
	return fmt.Sprintf(`You are a learning assistant at Valory. Your goal is to understand this student's learning preferences so their courses can be personalized.

Ask 3-5 short, friendly questions ONE AT A TIME covering:
1. Their general knowledge level (beginner / intermediate / advanced in general).
2. Their preferred explanation style (examples-first vs. theory-first).
3. How many hours per week they can study.
4. Any topics or subject areas they are particularly interested in or want to avoid.
5. Any learning challenges or preferences (e.g. "I struggle with abstract math").

When you have received at least 3 substantive answers that cover points 1-3 above, include the exact text %s on its own line at the end of your reply. Do not include this marker until you have enough information.

Keep your responses brief and encouraging.`, onboardingSentinel)
}

// summarizationSystemPrompt is the system prompt used for the single
// summarization call that converts the onboarding conversation into the
// natural-language profile text stored in learning_profiles.summary.
//
// @{"req": ["REQ-PROFILE-012"]}
func summarizationSystemPrompt() string {
	return `You are a concise summarizer. Based on the following learning preference questionnaire responses, write a 3-5 sentence natural-language summary of the student's learning profile. The summary will be injected into course generation prompts. Write in third person ("The student prefers...").
Focus on: knowledge level, preferred explanation style, time budget, topic interests, and any special learning needs.

Return ONLY the summary paragraph. No preamble, no JSON.`
}

// formatHistory renders the onboarding conversation as a plain-text block
// suitable for the summarization call user message.
//
// @{"req": ["REQ-PROFILE-012"]}
func formatHistory(msgs []OnboardingMessageRow) string {
	var sb strings.Builder
	sb.WriteString("Student's onboarding conversation:\n")
	for _, m := range msgs {
		role := "Assistant"
		if m.Role == "student" {
			role = "Student"
		}
		sb.WriteString(role)
		sb.WriteString(": ")
		sb.WriteString(m.Content)
		sb.WriteString("\n")
	}
	return sb.String()
}

// SecretResolver is the narrow interface the OnboardingService uses to obtain
// the current Anthropic API key.  admin.SecretProvider satisfies this interface.
// Defined locally to avoid an import cycle with the admin package.
//
// @{"req": ["REQ-PROFILE-013"]}
type SecretResolver interface {
	Get(ctx context.Context, name string) string
}

// OnboardingService orchestrates the 3-5 turn onboarding conversation and the
// final summarization call.  It calls the Anthropic SDK directly (bypassing
// ThrottledClient) so that onboarding tokens are not charged to any per-course
// token budget (REQ-PROFILE-013).  The per-student token cap only applies to
// course generation; onboarding is a one-time infrastructure cost.
type OnboardingService struct {
	repo       *ProfileRepository
	serverPool *pgxpool.Pool
	secrets    SecretResolver
}

// @{"req": ["REQ-PROFILE-011", "REQ-PROFILE-012", "REQ-PROFILE-013"]}
func NewOnboardingService(repo *ProfileRepository, serverPool *pgxpool.Pool, secrets SecretResolver) *OnboardingService {
	return &OnboardingService{repo: repo, serverPool: serverPool, secrets: secrets}
}

// Start clears any prior partial session for this student and triggers the
// first question from the onboarding agent.  Returns the opening message text.
//
// @{"req": ["REQ-PROFILE-011", "REQ-PROFILE-016"]}
func (s *OnboardingService) Start(ctx context.Context, studentID uuid.UUID) (string, error) {
	// Clear any prior partial session so re-runs start fresh.
	if err := s.repo.DeleteOnboardingMessages(ctx, s.serverPool, studentID); err != nil {
		return "", fmt.Errorf("onboarding: start: clear prior session: %w", err)
	}

	// The opening question is produced by calling Claude with the onboarding
	// system prompt and a synthetic trigger user message.
	reply, err := s.callClaude(ctx, onboardingSystemPrompt(),
		[]anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(
				"Hello, I'd like to set up my learning profile.",
			)),
		},
	)
	if err != nil {
		return "", fmt.Errorf("onboarding: start: call claude: %w", err)
	}

	// Store the assistant's opening question in onboarding_messages so future
	// Advance calls have the full conversation history.
	if err := s.repo.InsertOnboardingMessage(ctx, s.serverPool, studentID, "assistant", reply); err != nil {
		return "", fmt.Errorf("onboarding: start: store reply: %w", err)
	}

	return reply, nil
}

// Advance stores the student's answer, calls Claude with the full conversation
// history, stores the reply, and returns it along with a done flag.  done=true
// when the reply contains the ONBOARDING_COMPLETE sentinel.
//
// @{"req": ["REQ-PROFILE-011"]}
func (s *OnboardingService) Advance(ctx context.Context, studentID uuid.UUID, studentMessage string) (reply string, done bool, err error) {
	// Store the student's turn.
	if err := s.repo.InsertOnboardingMessage(ctx, s.serverPool, studentID, "student", studentMessage); err != nil {
		return "", false, fmt.Errorf("onboarding: advance: store student message: %w", err)
	}

	history, err := s.repo.GetOnboardingHistory(ctx, s.serverPool, studentID)
	if err != nil {
		return "", false, fmt.Errorf("onboarding: advance: load history: %w", err)
	}

	msgs := buildOnboardingMessages(history)

	replyText, err := s.callClaude(ctx, onboardingSystemPrompt(), msgs)
	if err != nil {
		return "", false, fmt.Errorf("onboarding: advance: call claude: %w", err)
	}

	// Store the assistant's reply.
	if err := s.repo.InsertOnboardingMessage(ctx, s.serverPool, studentID, "assistant", replyText); err != nil {
		return "", false, fmt.Errorf("onboarding: advance: store reply: %w", err)
	}

	return replyText, strings.Contains(replyText, onboardingSentinel), nil
}

// Complete summarizes the onboarding conversation into a profile text, writes
// it to learning_profiles, then performs two best-effort post-write steps:
//
//  1. SetOnboardingPrompted — prevents the onboarding nudge from reappearing.
//     Done first so a cleanup failure cannot block the flag.
//  2. DeleteOnboardingMessages — keeps onboarding_messages small; orphaned rows
//     are harmless because Start() clears them on the next run.
//
// Once the profile is successfully persisted the operation is a SUCCESS from
// the client's perspective.  Failures in the post-write steps are logged
// (without PII) and do not cause an error return.  Only three conditions
// produce a non-nil error (and therefore an HTTP 4xx/5xx):
//   - No active session (ErrNoActiveSession → 409).
//   - Anthropic summarization call failed.
//   - UpsertProfileAsServer failed (profile not written).
//
// @{"req": ["REQ-PROFILE-012", "REQ-PROFILE-014"]}
func (s *OnboardingService) Complete(ctx context.Context, studentID uuid.UUID) (string, error) {
	history, err := s.repo.GetOnboardingHistory(ctx, s.serverPool, studentID)
	if err != nil {
		return "", fmt.Errorf("onboarding: complete: load history: %w", err)
	}
	if len(history) == 0 {
		// Return the package sentinel so the handler can use errors.Is for a
		// clean 409 rather than comparing error strings.
		return "", fmt.Errorf("onboarding: complete: %w", ErrNoActiveSession)
	}

	// Single summarization call — claude-haiku-4-5 for cost efficiency (same
	// model used by the reviewer for its short, bounded-output calls).
	summary, err := s.callClaudeHaiku(ctx, summarizationSystemPrompt(),
		[]anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(formatHistory(history))),
		},
		512,
	)
	if err != nil {
		return "", fmt.Errorf("onboarding: complete: summarize: %w", err)
	}
	summary = strings.TrimSpace(summary)

	// Write the profile via the server pool (server RLS policy on
	// learning_profiles allows INSERT and UPDATE when role = 'server').
	if _, err := s.repo.UpsertProfileAsServer(ctx, s.serverPool, studentID, summary, "onboarding"); err != nil {
		return "", fmt.Errorf("onboarding: complete: upsert profile: %w", err)
	}

	// --- Profile is now persisted; all steps below are best-effort. ---

	// Step 1: mark the user as having been prompted for onboarding.  Done before
	// cleanup so a cleanup failure cannot prevent the flag from being set.
	// A failure here means the nudge may reappear on the next login (minor UX
	// annoyance, not a data-loss scenario).  Do NOT log the summary — no PII.
	if err := s.repo.SetOnboardingPrompted(ctx, studentID); err != nil {
		log.Printf("onboarding: complete: set prompted flag (best-effort, profile already written): %v", err)
	}

	// Step 2: clean up the conversation rows so onboarding_messages stays small.
	// Orphaned rows are harmless; Start() deletes them on any re-run.
	if err := s.repo.DeleteOnboardingMessages(ctx, s.serverPool, studentID); err != nil {
		log.Printf("onboarding: complete: cleanup messages (best-effort, profile already written): %v", err)
	}

	return summary, nil
}

// callClaude invokes the Anthropic API using the standard model for the
// onboarding questionnaire (claude-sonnet-4-6 matches the rest of the system
// but haiku is acceptable here; we use sonnet for richer question quality).
// Tokens are NOT recorded to agent_token_usage (unbilled per SDD §6.2).
//
// @{"req": ["REQ-PROFILE-011", "REQ-PROFILE-013"]}
func (s *OnboardingService) callClaude(ctx context.Context, system string, messages []anthropic.MessageParam) (string, error) {
	return s.callClaudeHaiku(ctx, system, messages, 512)
}

// callClaudeHaiku calls claude-haiku-4-5 directly (bypassing ThrottledClient)
// so that onboarding tokens are never charged to any course token budget.
//
// @{"req": ["REQ-PROFILE-013"]}
func (s *OnboardingService) callClaudeHaiku(ctx context.Context, system string, messages []anthropic.MessageParam, maxTokens int64) (string, error) {
	// Resolve the API key per-call so admin UI key updates apply immediately.
	apiKey := s.secrets.Get(ctx, "anthropic_api_key")

	// Build client options: override the key when the secret resolver provides
	// one; otherwise the SDK falls back to ANTHROPIC_API_KEY env var.
	var opts []option.RequestOption
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	client := anthropic.NewClient(opts...)

	msg, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeHaiku4_5,
		MaxTokens: maxTokens,
		System:    []anthropic.TextBlockParam{{Text: system}},
		Messages:  messages,
	})
	if err != nil {
		return "", fmt.Errorf("profile: claude call: %w", err)
	}
	if len(msg.Content) == 0 {
		return "", fmt.Errorf("profile: claude call: empty response")
	}
	return msg.Content[0].Text, nil
}

// buildOnboardingMessages converts the onboarding_messages history to the
// Anthropic MessageParam slice required by the API.  Consecutive same-role
// turns are merged to satisfy the strict user/assistant alternation requirement.
//
// @{"req": ["REQ-PROFILE-011"]}
func buildOnboardingMessages(history []OnboardingMessageRow) []anthropic.MessageParam {
	var msgs []anthropic.MessageParam
	for _, h := range history {
		isUser := h.Role == "student"

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

	// The Anthropic API requires the first message to be a user turn.
	if len(msgs) > 0 && msgs[0].Role == anthropic.MessageParamRoleAssistant {
		trigger := anthropic.NewUserMessage(anthropic.NewTextBlock(
			"Hello, I'd like to set up my learning profile.",
		))
		msgs = append([]anthropic.MessageParam{trigger}, msgs...)
	}

	return msgs
}

// LoadProfileSummary acquires a server-role connection to read the student's
// profile summary.  Returns "" when no profile exists — this is the graceful-
// degradation path (REQ-PROFILE-010): callers append nothing to the system
// prompt, leaving generation behaviour byte-for-byte identical to the pre-
// profile world.  Load errors are also treated as "" (fail-soft) so a transient
// DB error never blocks course generation.
//
// This function is the single injection point used by the professor and chair
// to retrieve the profile before assembling their system prompts.
//
// @{"req": ["REQ-PROFILE-005", "REQ-PROFILE-010"]}
func LoadProfileSummary(ctx context.Context, serverPool *pgxpool.Pool, studentID uuid.UUID) string {
	repo := &ProfileRepository{pool: serverPool}
	row, err := repo.GetProfileAsServer(ctx, serverPool, studentID)
	if err != nil {
		// ErrNotFound (no profile) and transient DB errors both degrade to "".
		// Errors are not logged here: the agent pipeline is hot path; the
		// caller already has a run/span context for diagnostics.
		return ""
	}
	return row.Summary
}

// AppendProfileBlock returns the system prompt with the student learning
// profile block appended when summary is non-empty.  When summary is ""
// the original prompt is returned byte-for-byte — no modification.
//
// The profile block is appended as the last paragraph so the model reads
// student context as the most proximate signal before forming its response.
//
// Exported so the agent package (professor.go, chair.go) can call it directly
// without re-implementing the injection logic.
//
// @{"req": ["REQ-PROFILE-006", "REQ-PROFILE-007", "REQ-PROFILE-008", "REQ-PROFILE-009", "REQ-PROFILE-010"]}
func AppendProfileBlock(prompt, summary string) string {
	if summary == "" {
		return prompt
	}
	return prompt + "\n\nStudent learning profile:\n" + summary
}
