package agent

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/google/uuid"
)

// mockConfigSvc satisfies the anonymous configSvc interface used by ThrottledClient.
type mockConfigSvc struct {
	int64Values   map[string]int64
	float64Values map[string]float64
}

func (m *mockConfigSvc) GetInt64(key string) int64 {
	return m.int64Values[key]
}

func (m *mockConfigSvc) GetFloat64(key string) float64 {
	return m.float64Values[key]
}

// mockTransport is an http.RoundTripper that replays a fixed sequence of
// pre-built responses.  Each RoundTrip call advances the internal index.
// Once all responses are consumed it panics, which is intentional: a test that
// drives more HTTP calls than the mock was configured with is itself buggy.
type mockTransport struct {
	responses []*http.Response
	calls     int
}

func (m *mockTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	if m.calls >= len(m.responses) {
		// Return an error rather than panic so the SDK can propagate it cleanly;
		// the test will catch the unexpected nil message or unexpected error.
		return nil, io.EOF
	}
	resp := m.responses[m.calls]
	m.calls++
	return resp, nil
}

// make200 returns a minimal 200 Anthropic Messages response that the SDK can
// deserialise into an *anthropic.Message with InputTokens=10, OutputTokens=5.
func make200() *http.Response {
	body := `{
		"id": "msg_1",
		"type": "message",
		"role": "assistant",
		"content": [{"type": "text", "text": "hello"}],
		"model": "claude-sonnet-4-6",
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 10, "output_tokens": 5}
	}`
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// make429 returns a minimal 429 Anthropic rate-limit response.
func make429() *http.Response {
	body := `{"type":"error","error":{"type":"rate_limit_error","message":"rate limited"}}`
	return &http.Response{
		StatusCode: 429,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// minimalParams returns a MessageNewParams that satisfies all required fields.
func minimalParams() anthropic.MessageNewParams {
	return anthropic.MessageNewParams{
		MaxTokens: 1024,
		Model:     anthropic.ModelClaudeSonnet4_6,
		Messages: []anthropic.MessageParam{
			{
				Role: anthropic.MessageParamRoleUser,
				Content: []anthropic.ContentBlockParamUnion{
					{OfText: &anthropic.TextBlockParam{Text: "hello"}},
				},
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Test 1 — successful round-trip
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AGENT-012", "REQ-ADMIN-004", "REQ-SYS-044"]}
func TestMessages_Success_ReturnsNonNilMessage(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	transport := &mockTransport{responses: []*http.Response{make200()}}
	cfg := &mockConfigSvc{
		int64Values: map[string]int64{
			"per_student_token_limit": 0, // 0 disables the cap check
			"agent_retry_limit":       3,
		},
	}

	studentID := createTestUser(ctx, t, "client_success_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Mathematics", "intake")

	tc := &ThrottledClient{
		client:    anthropic.NewClient(option.WithAPIKey("test-key"), option.WithHTTPClient(&http.Client{Transport: transport}), option.WithMaxRetries(0)),
		pool:      pool,
		configSvc: cfg,
	}

	msg, err := tc.Messages(ctx, studentID, courseID, minimalParams())
	if err != nil {
		t.Fatalf("Messages() unexpected error: %v", err)
	}
	if msg == nil {
		t.Fatal("Messages() returned nil message; expected non-nil")
	}
}

// ---------------------------------------------------------------------------
// Test 2 — rate-limit retry then success
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AGENT-012", "REQ-ADMIN-004", "REQ-SYS-044"]}
func TestMessages_RateLimitRetry_ThenSuccess_ReturnsMessage(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	transport := &mockTransport{
		responses: []*http.Response{make429(), make429(), make200()},
	}
	cfg := &mockConfigSvc{
		int64Values: map[string]int64{
			"per_student_token_limit": 0,
			// 5 retries — enough to exhaust the 2 rate-limits and reach the 200
			"agent_retry_limit": 5,
		},
	}

	studentID := createTestUser(ctx, t, "client_retry_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Physics", "intake")

	tc := &ThrottledClient{
		client:    anthropic.NewClient(option.WithAPIKey("test-key"), option.WithHTTPClient(&http.Client{Transport: transport}), option.WithMaxRetries(0)),
		pool:      pool,
		configSvc: cfg,
	}

	msg, err := tc.Messages(ctx, studentID, courseID, minimalParams())
	if err != nil {
		t.Fatalf("Messages() unexpected error after retries: %v", err)
	}
	if msg == nil {
		t.Fatal("Messages() returned nil message after retries; expected non-nil")
	}
	// Confirm that the mock was actually called 3 times (2× 429 + 1× 200).
	if transport.calls != 3 {
		t.Errorf("expected 3 HTTP calls (2×429 + 1×200), got %d", transport.calls)
	}
}

// ---------------------------------------------------------------------------
// Test 3 — rate-limit exhausted
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AGENT-012", "REQ-ADMIN-004", "REQ-SYS-044"]}
func TestMessages_RateLimitExhausted_ReturnsErrRateLimitExhausted(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	const retryLimit = int64(3)

	ctx := context.Background()
	// Provide exactly retryLimit 429 responses so every attempt is rate-limited.
	responses := make([]*http.Response, retryLimit)
	for i := range responses {
		responses[i] = make429()
	}
	transport := &mockTransport{responses: responses}
	cfg := &mockConfigSvc{
		int64Values: map[string]int64{
			"per_student_token_limit": 0,
			"agent_retry_limit":       retryLimit,
		},
	}

	studentID := createTestUser(ctx, t, "client_exhausted_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Chemistry", "intake")

	tc := &ThrottledClient{
		client:    anthropic.NewClient(option.WithAPIKey("test-key"), option.WithHTTPClient(&http.Client{Transport: transport}), option.WithMaxRetries(0)),
		pool:      pool,
		configSvc: cfg,
	}

	_, err := tc.Messages(ctx, studentID, courseID, minimalParams())
	if err == nil {
		t.Fatal("Messages() expected ErrRateLimitExhausted, got nil")
	}
	if err != ErrRateLimitExhausted {
		t.Errorf("Messages() error = %v; want ErrRateLimitExhausted", err)
	}
}

// ---------------------------------------------------------------------------
// Test 4 — context cancelled during backoff
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AGENT-012", "REQ-ADMIN-004", "REQ-SYS-044"]}
func TestMessages_ContextCancelledDuringBackoff_ReturnsCtxErr(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	// A cancelable context — we will cancel it from a separate goroutine after
	// the first 429 is delivered, while the backoff select is sleeping.
	ctx, cancel := context.WithCancel(context.Background())

	// cancelTransport cancels the context the moment the first 429 is consumed.
	// This races the backoff timer and exercises the ctx.Done() branch.
	cancelTransport := &cancelOnFirstCallTransport{
		inner:  make429(),
		cancel: cancel,
	}

	cfg := &mockConfigSvc{
		int64Values: map[string]int64{
			"per_student_token_limit": 0,
			// Large retry limit so the loop would keep going if not cancelled.
			"agent_retry_limit": 100,
		},
	}

	studentID := createTestUser(ctx, t, "client_cancel_"+uuid.New().String())
	// Use a fresh background context for DB operations that must not be cancelled.
	courseID := createTestCourse(context.Background(), t, studentID, "Biology", "intake")

	tc := &ThrottledClient{
		client:    anthropic.NewClient(option.WithAPIKey("test-key"), option.WithHTTPClient(&http.Client{Transport: cancelTransport}), option.WithMaxRetries(0)),
		pool:      pool,
		configSvc: cfg,
	}

	_, err := tc.Messages(ctx, studentID, courseID, minimalParams())
	if err == nil {
		t.Fatal("Messages() expected context error, got nil")
	}
	if err != context.Canceled {
		t.Errorf("Messages() error = %v; want context.Canceled", err)
	}
}

// cancelOnFirstCallTransport returns a 429 and immediately cancels the context
// so that the backoff select fires on ctx.Done() rather than the timer.
type cancelOnFirstCallTransport struct {
	inner  *http.Response
	cancel context.CancelFunc
	called bool
}

func (c *cancelOnFirstCallTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	if !c.called {
		c.called = true
		// Cancel the context before returning — this ensures the backoff
		// select will see ctx.Done() closed before the timer fires.
		c.cancel()
		return c.inner, nil
	}
	// Should never reach here; the context should have stopped the loop.
	return nil, io.EOF
}

// ---------------------------------------------------------------------------
// Test 5 — token cap exceeded (requires DB)
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AGENT-012", "REQ-ADMIN-004", "REQ-SYS-044"]}
func TestMessages_TokenCapExceeded_ReturnsErrTokenCapExceeded(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()

	// callCount tracks whether any HTTP request was actually sent.
	// A token-cap rejection must short-circuit before any API call.
	callTracker := &countingTransport{}

	const tokenCap = int64(1000)
	cfg := &mockConfigSvc{
		int64Values: map[string]int64{
			// Cap is 1000; we will insert a row with exactly 1000 tokens used.
			"per_student_token_limit": tokenCap,
			"agent_retry_limit":       3,
		},
	}

	studentID := createTestUser(ctx, t, "client_tokencap_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Economics", "intake")

	// Pre-seed agent_token_usage so the student has exhausted the cap.
	_, err := pool.Exec(ctx,
		`INSERT INTO agent_token_usage (student_id, course_id, total_tokens_used)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (student_id, course_id)
		 DO UPDATE SET total_tokens_used = EXCLUDED.total_tokens_used`,
		studentID, courseID, tokenCap,
	)
	if err != nil {
		t.Fatalf("seed agent_token_usage: %v", err)
	}

	tc := &ThrottledClient{
		client:    anthropic.NewClient(option.WithAPIKey("test-key"), option.WithHTTPClient(&http.Client{Transport: callTracker}), option.WithMaxRetries(0)),
		pool:      pool,
		configSvc: cfg,
	}

	_, err = tc.Messages(ctx, studentID, courseID, minimalParams())
	if err == nil {
		t.Fatal("Messages() expected ErrTokenCapExceeded, got nil")
	}
	if err != ErrTokenCapExceeded {
		t.Errorf("Messages() error = %v; want ErrTokenCapExceeded", err)
	}
	// No HTTP call should have been made when the cap is already exceeded.
	if callTracker.calls != 0 {
		t.Errorf("expected 0 HTTP calls when token cap exceeded, got %d", callTracker.calls)
	}
}

// countingTransport records how many HTTP calls were attempted without
// completing them (returns an error immediately).
type countingTransport struct {
	calls int
}

func (c *countingTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	c.calls++
	return nil, io.EOF
}

// ---------------------------------------------------------------------------
// Test 6 — large retry limit does not overflow (no DB needed)
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AGENT-012", "REQ-ADMIN-004", "REQ-SYS-044"]}
func TestMessages_BackoffNoOverflow_NoPanic(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	const largeRetryLimit = int64(40)

	// Use a short deadline so the backoff select exits via ctx.Done() after a
	// few iterations — proving no overflow panic without sleeping ~35 minutes.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Supply exactly largeRetryLimit 429 responses.
	responses := make([]*http.Response, largeRetryLimit)
	for i := range responses {
		responses[i] = make429()
	}
	transport := &mockTransport{responses: responses}
	cfg := &mockConfigSvc{
		int64Values: map[string]int64{
			"per_student_token_limit": 0,
			"agent_retry_limit":       largeRetryLimit,
		},
	}

	studentID := createTestUser(ctx, t, "client_overflow_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Philosophy", "intake")

	tc := &ThrottledClient{
		client:    anthropic.NewClient(option.WithAPIKey("test-key"), option.WithHTTPClient(&http.Client{Transport: transport}), option.WithMaxRetries(0)),
		pool:      pool,
		configSvc: cfg,
	}

	// The test goal is that no panic occurs due to integer overflow in the
	// exponential backoff calculation.  We wrap in a deferred recover to make a
	// panic into a test failure rather than crashing the test binary.
	// The context deadline ends the retry loop after a few iterations.
	didPanic := func() (panicked bool) {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		_, _ = tc.Messages(ctx, studentID, courseID, minimalParams())
		return false
	}()

	if didPanic {
		t.Error("Messages() panicked during exponential backoff with large retry limit")
	}
}

// ---------------------------------------------------------------------------
// Test 7 — successful call persists token usage via UPSERT
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AGENT-012", "REQ-ADMIN-004", "REQ-SYS-044"]}
func TestMessages_Success_UpsertTokenUsage(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	// Two consecutive successful calls; the second should accumulate tokens.
	transport := &mockTransport{responses: []*http.Response{make200(), make200()}}
	cfg := &mockConfigSvc{
		int64Values: map[string]int64{
			"per_student_token_limit": 0,
			"agent_retry_limit":       3,
		},
	}

	studentID := createTestUser(ctx, t, "client_upsert_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "History", "intake")

	tc := &ThrottledClient{
		client:    anthropic.NewClient(option.WithAPIKey("test-key"), option.WithHTTPClient(&http.Client{Transport: transport}), option.WithMaxRetries(0)),
		pool:      pool,
		configSvc: cfg,
	}

	// First call: should insert a row with 15 tokens (10 input + 5 output).
	if _, err := tc.Messages(ctx, studentID, courseID, minimalParams()); err != nil {
		t.Fatalf("first Messages() call failed: %v", err)
	}
	var totalAfterFirst int64
	if err := pool.QueryRow(ctx,
		`SELECT total_tokens_used FROM agent_token_usage WHERE student_id = $1 AND course_id = $2`,
		studentID, courseID).Scan(&totalAfterFirst); err != nil {
		t.Fatalf("SELECT after first call: %v", err)
	}
	// make200 returns usage: {input_tokens: 10, output_tokens: 5} = 15 total.
	if totalAfterFirst != 15 {
		t.Errorf("total_tokens_used after first call: expected 15, got %d", totalAfterFirst)
	}

	// Second call: should accumulate to 30 tokens.
	if _, err := tc.Messages(ctx, studentID, courseID, minimalParams()); err != nil {
		t.Fatalf("second Messages() call failed: %v", err)
	}
	var totalAfterSecond int64
	if err := pool.QueryRow(ctx,
		`SELECT total_tokens_used FROM agent_token_usage WHERE student_id = $1 AND course_id = $2`,
		studentID, courseID).Scan(&totalAfterSecond); err != nil {
		t.Fatalf("SELECT after second call: %v", err)
	}
	if totalAfterSecond != 30 {
		t.Errorf("total_tokens_used after second call: expected 30, got %d", totalAfterSecond)
	}
}
