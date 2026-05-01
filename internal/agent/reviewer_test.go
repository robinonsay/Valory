package agent

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/google/uuid"
)

// makeReviewResponse builds a minimal 200 Anthropic response whose text body is
// the given JSON string (no code fences).
func makeReviewResponse(jsonBody string) *http.Response {
	body := `{
		"id": "msg_review",
		"type": "message",
		"role": "assistant",
		"content": [{"type": "text", "text": ` + jsonBody + `}],
		"model": "claude-haiku-4-5",
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 50, "output_tokens": 20}
	}`
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// makeReviewJSONText encodes a JSON string as a JSON string literal so it can
// be embedded inside the outer JSON body.
func makeReviewJSONText(text string) string {
	var buf strings.Builder
	buf.WriteByte('"')
	for _, c := range text {
		switch c {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\n':
			buf.WriteString(`\n`)
		default:
			buf.WriteRune(c)
		}
	}
	buf.WriteByte('"')
	return buf.String()
}

// newTestReviewer builds a Reviewer wired to a mock HTTP transport so no real
// Anthropic calls are made.  The transport will replay responses in order.
func newTestReviewer(t *testing.T, transport *mockTransport) *Reviewer {
	t.Helper()
	cfg := &mockConfigSvc{
		int64Values: map[string]int64{
			"per_student_token_limit": 0,
			"agent_retry_limit":       1,
		},
	}
	tc := &ThrottledClient{
		client:    anthropic.NewClient(option.WithAPIKey("test-key"), option.WithHTTPClient(&http.Client{Transport: transport}), option.WithMaxRetries(0)),
		pool:      pool,
		configSvc: cfg,
	}
	repo := NewAgentRepository(pool)
	return NewReviewer(tc, pool, repo)
}

// ---------------------------------------------------------------------------
// ReviewSection — approved path
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-CONTENT-001"]}
func TestReviewSection_Approved_SetsCitationVerifiedTrue(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := createTestUser(ctx, t, "reviewer_approved_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Computer Science", "active")

	// Insert a lesson_content row whose citation_verified we will check.
	var contentID uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO lesson_content (course_id, section_index, title, content_adoc, version)
		 VALUES ($1, 0, 'Test Section', 'content here', 1) RETURNING id`,
		courseID,
	).Scan(&contentID)
	if err != nil {
		t.Fatalf("insert lesson_content: %v", err)
	}

	approvedJSON := makeReviewJSONText(`{\"approved\":true,\"feedback\": \"\"}`)
	transport := &mockTransport{responses: []*http.Response{makeReviewResponse(approvedJSON)}}
	rev := newTestReviewer(t, transport)

	repo := NewAgentRepository(pool)
	run, err := repo.CreateRun(ctx, courseID, "content_generation")
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	result, err := rev.ReviewSection(ctx, run.ID, courseID, studentID, contentID, "Some content with [Source: Wikipedia] citation.")
	if err != nil {
		t.Fatalf("ReviewSection: %v", err)
	}
	if !result.Approved {
		t.Errorf("expected Approved=true, got false; feedback: %s", result.Feedback)
	}

	// Verify citation_verified was set to true in the DB.
	var verified bool
	if err := pool.QueryRow(ctx, `SELECT citation_verified FROM lesson_content WHERE id = $1`, contentID).Scan(&verified); err != nil {
		t.Fatalf("query citation_verified: %v", err)
	}
	if !verified {
		t.Error("expected citation_verified=true in DB after approval, got false")
	}
}

// ---------------------------------------------------------------------------
// ReviewSection — rejected path
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-CONTENT-001"]}
func TestReviewSection_Rejected_ReturnsFeedback(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := createTestUser(ctx, t, "reviewer_rejected_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "History", "active")

	var contentID uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO lesson_content (course_id, section_index, title, content_adoc, version)
		 VALUES ($1, 0, 'History Basics', 'no citations here', 1) RETURNING id`,
		courseID,
	).Scan(&contentID)
	if err != nil {
		t.Fatalf("insert lesson_content: %v", err)
	}

	rejectedJSON := makeReviewJSONText(`{\"approved\":false,\"feedback\":\"Missing required citations\"}`)
	transport := &mockTransport{responses: []*http.Response{makeReviewResponse(rejectedJSON)}}
	rev := newTestReviewer(t, transport)

	repo := NewAgentRepository(pool)
	run, err := repo.CreateRun(ctx, courseID, "content_generation")
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	result, err := rev.ReviewSection(ctx, run.ID, courseID, studentID, contentID, "Content without citations.")
	if err != nil {
		t.Fatalf("ReviewSection: %v", err)
	}
	if result.Approved {
		t.Error("expected Approved=false, got true")
	}
	if result.Feedback == "" {
		t.Error("expected non-empty Feedback on rejection")
	}

	// citation_verified must remain false.
	var verified bool
	if err := pool.QueryRow(ctx, `SELECT citation_verified FROM lesson_content WHERE id = $1`, contentID).Scan(&verified); err != nil {
		t.Fatalf("query citation_verified: %v", err)
	}
	if verified {
		t.Error("expected citation_verified=false in DB after rejection, got true")
	}
}

// ---------------------------------------------------------------------------
// ReviewSection — unparseable response treated as rejection
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-CONTENT-001"]}
func TestReviewSection_UnparseableResponse_TreatedAsRejection(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := createTestUser(ctx, t, "reviewer_unparseable_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Biology", "active")

	var contentID uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO lesson_content (course_id, section_index, title, content_adoc, version)
		 VALUES ($1, 0, 'Cell Biology', 'content', 1) RETURNING id`,
		courseID,
	).Scan(&contentID)
	if err != nil {
		t.Fatalf("insert lesson_content: %v", err)
	}

	// Return plain text instead of JSON — should be treated as rejection.
	garbageJSON := makeReviewJSONText("not a json object at all")
	transport := &mockTransport{responses: []*http.Response{makeReviewResponse(garbageJSON)}}
	rev := newTestReviewer(t, transport)

	repo := NewAgentRepository(pool)
	run, err := repo.CreateRun(ctx, courseID, "content_generation")
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	result, err := rev.ReviewSection(ctx, run.ID, courseID, studentID, contentID, "Some content.")
	if err != nil {
		t.Fatalf("ReviewSection: unexpected error: %v", err)
	}
	if result.Approved {
		t.Error("expected Approved=false for unparseable response, got true")
	}
	if result.Feedback == "" {
		t.Error("expected Feedback to contain raw response text")
	}
}
