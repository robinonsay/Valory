package agent

import (
	"context"
	"errors"
	"math/rand"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrRateLimitExhausted = errors.New("anthropic: rate limit exhausted after max retries")
var ErrTokenCapExceeded = errors.New("anthropic: per-student token cap exceeded")

// ThrottledClient wraps the Anthropic SDK client with per-student token cap
// enforcement and exponential-backoff retry on HTTP 429 responses.
type ThrottledClient struct {
	client    anthropic.Client
	pool      *pgxpool.Pool
	configSvc interface {
		GetInt64(string) int64
		GetFloat64(string) float64
	}
}

// @{"req": ["REQ-AGENT-012", "REQ-ADMIN-004", "REQ-SYS-044", "REQ-SYS-049"]}
func NewThrottledClient(apiKey string, pool *pgxpool.Pool, configSvc interface {
	GetInt64(string) int64
	GetFloat64(string) float64
}) *ThrottledClient {
	return &ThrottledClient{
		client:    anthropic.NewClient(option.WithAPIKey(apiKey)),
		pool:      pool,
		configSvc: configSvc,
	}
}

// Messages sends a message to the Anthropic API, enforcing per-student token
// caps and retrying on rate-limit errors with exponential backoff.
//
// @{"req": ["REQ-AGENT-012", "REQ-ADMIN-004", "REQ-SYS-044", "REQ-SYS-049"]}
func (c *ThrottledClient) Messages(ctx context.Context, studentID, courseID uuid.UUID, params anthropic.MessageNewParams) (*anthropic.Message, error) {
	// Step 1: Check per-student token cap before making any API call.
	cap := c.configSvc.GetInt64("per_student_token_limit")
	if cap > 0 {
		var used int64
		err := c.pool.QueryRow(ctx,
			`SELECT COALESCE(total_tokens_used, 0) FROM agent_token_usage WHERE student_id = $1 AND course_id = $2`,
			studentID, courseID,
		).Scan(&used)
		if err != nil {
			// pgx returns pgx.ErrNoRows when the row does not exist; treat that
			// as zero usage rather than a hard error.
			if !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
			used = 0
		}
		if used >= cap {
			return nil, ErrTokenCapExceeded
		}
	}

	// Step 2: Retry loop with exponential backoff on HTTP 429.
	retryLimit := c.configSvc.GetInt64("agent_retry_limit")
	var msg *anthropic.Message
	for attempt := int64(0); attempt < retryLimit; attempt++ {
		var err error
		msg, err = c.client.Messages.New(ctx, params)
		if err == nil {
			break
		}

		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) && apiErr.StatusCode == 429 {
			// Rate limited — back off and retry.
			baseDelay := time.Second
			maxDelay := 60 * time.Second

			// Cap the shift to prevent int64 overflow: 2^30 * 1s is already >60s
			// so any higher shift would just be clamped to maxDelay anyway.
			const maxShift = 30
			shift := attempt
			if shift > maxShift {
				shift = maxShift
			}
			delay := baseDelay * (1 << shift)
			if delay > maxDelay {
				delay = maxDelay
			}
			// Defensive: catch any residual overflow that produces a non-positive value.
			if delay <= 0 {
				delay = maxDelay
			}
			jitter := time.Duration(rand.Int63n(int64(delay / 4)))

			// Use a context-aware select so that server shutdown or account
			// deletion termination is not blocked by a sleeping retry.
			select {
			case <-time.After(delay + jitter):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}

		// Non-retryable error — surface immediately.
		return nil, err
	}

	if msg == nil {
		// All retry attempts exhausted on rate-limit responses.
		return nil, ErrRateLimitExhausted
	}

	// Step 3: Track token usage via an UPSERT so the cap check stays accurate.
	totalTokens := msg.Usage.InputTokens + msg.Usage.OutputTokens
	_, err := c.pool.Exec(ctx,
		`INSERT INTO agent_token_usage (student_id, course_id, total_tokens_used)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (student_id, course_id)
		 DO UPDATE SET total_tokens_used = agent_token_usage.total_tokens_used + EXCLUDED.total_tokens_used`,
		studentID, courseID, totalTokens,
	)
	if err != nil {
		return nil, err
	}

	return msg, nil
}
