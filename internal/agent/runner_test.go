package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// containsRegenKeyword — pure function, no DB required
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-CONTENT-004"]}
func TestContainsRegenKeyword_MatchingKeywords_ReturnsTrue(t *testing.T) {
	cases := []struct {
		text    string
		keyword string
	}{
		{"please rewrite this section", "rewrite"},
		{"change the introduction", "change"},
		{"redo the examples", "redo"},
		{"update with more examples", "update"},
		{"this is incorrect", "incorrect"},
		{"the formula is wrong", "wrong"},
		{"fix the citation", "fix"},
		{"revise the conclusion", "revise"},
		{"regenerate this content", "regenerate"},
	}

	for _, tc := range cases {
		if !containsRegenKeyword(tc.text) {
			t.Errorf("expected true for %q (keyword: %q), got false", tc.text, tc.keyword)
		}
	}
}

// @{"verifies": ["REQ-CONTENT-004"]}
func TestContainsRegenKeyword_NonMatchingText_ReturnsFalse(t *testing.T) {
	cases := []string{
		"great content, well explained",
		"I learned a lot from this section",
		"the examples are very helpful",
		"",
		"thanks for writing this",
	}

	for _, text := range cases {
		if containsRegenKeyword(text) {
			t.Errorf("expected false for %q, got true", text)
		}
	}
}

// @{"verifies": ["REQ-CONTENT-004"]}
func TestContainsRegenKeyword_KeywordAsSubstring_ReturnsTrue(t *testing.T) {
	// "fix" appears as a substring of "prefix" — the naive substring match will
	// still return true here, which is the documented (if imprecise) behaviour.
	if !containsRegenKeyword("please prefix this") {
		t.Error("expected true: 'fix' is a substring of 'prefix'")
	}
}

// ---------------------------------------------------------------------------
// GradeSubmission stubs
// ---------------------------------------------------------------------------

// stubSubmissionRepo records which methods were called and with which arguments.
type stubSubmissionRepo struct {
	setRawScoreCalled    bool
	setRawScoreID        uuid.UUID
	setRawScoreValue     float64
	setRawScoreErr       error
	markFailedCalled     bool
	markFailedID         uuid.UUID
	markFailedErr        error
}

func (r *stubSubmissionRepo) SetRawScore(_ context.Context, submissionID uuid.UUID, rawScore float64) error {
	r.setRawScoreCalled = true
	r.setRawScoreID = submissionID
	r.setRawScoreValue = rawScore
	return r.setRawScoreErr
}

func (r *stubSubmissionRepo) MarkGradingFailed(_ context.Context, submissionID uuid.UUID) error {
	r.markFailedCalled = true
	r.markFailedID = submissionID
	return r.markFailedErr
}

// stubGradeService records ComputeAndStoreGrade calls.
type stubGradeService struct {
	called bool
	err    error
}

func (g *stubGradeService) ComputeAndStoreGrade(_ context.Context, _, _, _, _ uuid.UUID, _ float64) error {
	g.called = true
	return g.err
}

// stubAIClient returns a configurable Anthropic message response.
type stubAIClient struct {
	response *anthropic.Message
	err      error
}

func (c *stubAIClient) Messages(_ context.Context, _, _ uuid.UUID, _ anthropic.MessageNewParams) (*anthropic.Message, error) {
	return c.response, c.err
}

// makeTextMessage builds a minimal *anthropic.Message with a single text block.
func makeTextMessage(text string) *anthropic.Message {
	return &anthropic.Message{
		Content: []anthropic.ContentBlockUnion{
			{Type: "text", Text: text},
		},
	}
}

// buildRunner constructs a minimal AgentRunner for GradeSubmission tests.
func buildRunner(subRepo submissionRepository, gradeSvc gradeService, aiClient aiMessenger) *AgentRunner {
	return &AgentRunner{
		submissionRepo: subRepo,
		gradeSvc:       gradeSvc,
		aiClient:       aiClient,
	}
}

// writeTempFile writes content to a temp file and returns its path.
// The caller must remove it after the test.
func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp("", "grade-test-*.md")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	f.Close()
	return f.Name()
}

// ---------------------------------------------------------------------------
// GradeSubmission tests
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-GRADE-001", "REQ-GRADE-002"]}
func TestGradeSubmission_ValidJSONResponse_SetsRawScoreAndComputesGrade(t *testing.T) {
	filePath := writeTempFile(t, "student answer content")
	defer os.Remove(filePath)

	subRepo := &stubSubmissionRepo{}
	gradeSvc := &stubGradeService{}
	aiClient := &stubAIClient{response: makeTextMessage(`{"score": 85}`)}

	runner := buildRunner(subRepo, gradeSvc, aiClient)

	sub := SubmissionToGrade{
		ID:         uuid.New(),
		HomeworkID: uuid.New(),
		StudentID:  uuid.New(),
		CourseID:   uuid.New(),
		FilePath:   filePath,
		Rubric:     "rubric text",
	}

	err := runner.GradeSubmission(context.Background(), sub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !subRepo.setRawScoreCalled {
		t.Error("expected SetRawScore to be called")
	}
	if subRepo.setRawScoreValue != 85 {
		t.Errorf("expected raw score=85, got %.1f", subRepo.setRawScoreValue)
	}
	if !gradeSvc.called {
		t.Error("expected ComputeAndStoreGrade to be called")
	}
	if subRepo.markFailedCalled {
		t.Error("expected MarkGradingFailed NOT to be called on success")
	}
}

// @{"verifies": ["REQ-GRADE-001", "REQ-GRADE-002"]}
func TestGradeSubmission_MalformedJSONResponse_MarksGradingFailed(t *testing.T) {
	filePath := writeTempFile(t, "student answer content")
	defer os.Remove(filePath)

	subRepo := &stubSubmissionRepo{}
	gradeSvc := &stubGradeService{}
	aiClient := &stubAIClient{response: makeTextMessage(`not valid json`)}

	runner := buildRunner(subRepo, gradeSvc, aiClient)

	sub := SubmissionToGrade{
		ID:       uuid.New(),
		FilePath: filePath,
		Rubric:   "rubric",
	}

	err := runner.GradeSubmission(context.Background(), sub)
	if err == nil {
		t.Error("expected error for malformed JSON, got nil")
	}
	if !subRepo.markFailedCalled {
		t.Error("expected MarkGradingFailed to be called on malformed JSON")
	}
	if subRepo.setRawScoreCalled {
		t.Error("expected SetRawScore NOT to be called on malformed JSON")
	}
}

// @{"verifies": ["REQ-GRADE-001", "REQ-GRADE-002"]}
func TestGradeSubmission_ScoreOutOfRange_Negative_MarksGradingFailed(t *testing.T) {
	filePath := writeTempFile(t, "student answer content")
	defer os.Remove(filePath)

	subRepo := &stubSubmissionRepo{}
	gradeSvc := &stubGradeService{}
	aiClient := &stubAIClient{response: makeTextMessage(`{"score": -5}`)}

	runner := buildRunner(subRepo, gradeSvc, aiClient)

	sub := SubmissionToGrade{
		ID:       uuid.New(),
		FilePath: filePath,
		Rubric:   "rubric",
	}

	err := runner.GradeSubmission(context.Background(), sub)
	if err == nil {
		t.Error("expected error for negative score")
	}
	if !subRepo.markFailedCalled {
		t.Error("expected MarkGradingFailed for out-of-range score")
	}
}

// @{"verifies": ["REQ-GRADE-001", "REQ-GRADE-002"]}
func TestGradeSubmission_ScoreOutOfRange_Above100_MarksGradingFailed(t *testing.T) {
	filePath := writeTempFile(t, "student answer content")
	defer os.Remove(filePath)

	subRepo := &stubSubmissionRepo{}
	gradeSvc := &stubGradeService{}
	aiClient := &stubAIClient{response: makeTextMessage(`{"score": 150}`)}

	runner := buildRunner(subRepo, gradeSvc, aiClient)

	sub := SubmissionToGrade{
		ID:       uuid.New(),
		FilePath: filePath,
		Rubric:   "rubric",
	}

	err := runner.GradeSubmission(context.Background(), sub)
	if err == nil {
		t.Error("expected error for score > 100")
	}
	if !subRepo.markFailedCalled {
		t.Error("expected MarkGradingFailed for out-of-range score")
	}
}

// @{"verifies": ["REQ-GRADE-001", "REQ-GRADE-002"]}
func TestGradeSubmission_FileNotFound_MarksGradingFailed(t *testing.T) {
	subRepo := &stubSubmissionRepo{}
	gradeSvc := &stubGradeService{}
	aiClient := &stubAIClient{response: makeTextMessage(`{"score": 80}`)}

	runner := buildRunner(subRepo, gradeSvc, aiClient)

	sub := SubmissionToGrade{
		ID:       uuid.New(),
		FilePath: "/nonexistent/path/to/submission.md",
		Rubric:   "rubric",
	}

	err := runner.GradeSubmission(context.Background(), sub)
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
	if !subRepo.markFailedCalled {
		t.Error("expected MarkGradingFailed when file cannot be read")
	}
	if subRepo.setRawScoreCalled {
		t.Error("expected SetRawScore NOT to be called when file is missing")
	}
}

// @{"verifies": ["REQ-GRADE-001", "REQ-GRADE-002"]}
func TestGradeSubmission_AIClientError_MarksGradingFailed(t *testing.T) {
	filePath := writeTempFile(t, "student answer content")
	defer os.Remove(filePath)

	subRepo := &stubSubmissionRepo{}
	gradeSvc := &stubGradeService{}
	aiClient := &stubAIClient{err: errors.New("API unavailable")}

	runner := buildRunner(subRepo, gradeSvc, aiClient)

	sub := SubmissionToGrade{
		ID:       uuid.New(),
		FilePath: filePath,
		Rubric:   "rubric",
	}

	err := runner.GradeSubmission(context.Background(), sub)
	if err == nil {
		t.Error("expected error when AI client fails")
	}
	if !subRepo.markFailedCalled {
		t.Error("expected MarkGradingFailed when AI call fails")
	}
}

// Ensure stubAIClient satisfies aiMessenger at compile time.
var _ aiMessenger = (*stubAIClient)(nil)

// Ensure stubSubmissionRepo satisfies submissionRepository at compile time.
var _ submissionRepository = (*stubSubmissionRepo)(nil)

// Ensure stubGradeService satisfies gradeService at compile time.
var _ gradeService = (*stubGradeService)(nil)

// suppress "declared and not used" for fmt in case it's used in future helpers
var _ = fmt.Sprintf
