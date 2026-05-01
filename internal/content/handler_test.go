//go:build testing

// @{"verifies": ["REQ-CONTENT-004"]}
package content

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/valory/valory/internal/auth"
)

// submitFeedbackStatus sends a POST request to the submitFeedback handler with
// the given feedback text and returns the HTTP status code. The repo is nil
// because the validation cases under test all return before the repo is reached.
func submitFeedbackStatus(t *testing.T, feedbackText string) int {
	t.Helper()

	h := NewContentHandler(nil)

	router := chi.NewRouter()
	router.Post("/{id}/{sectionIndex}/feedback", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(uuid.New()), "student"))
		h.submitFeedback(w, r)
	})

	body, _ := json.Marshal(map[string]string{"feedback_text": feedbackText})
	req := httptest.NewRequest("POST", "/"+uuid.New().String()+"/0/feedback", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)
	return w.Code
}

// @{"verifies": ["REQ-CONTENT-004"]}
func TestSubmitFeedback_EmptyText_Returns400(t *testing.T) {
	if got := submitFeedbackStatus(t, ""); got != http.StatusBadRequest {
		t.Errorf("empty feedback_text: want 400, got %d", got)
	}
}

// @{"verifies": ["REQ-CONTENT-004"]}
func TestSubmitFeedback_TooLong_Returns400(t *testing.T) {
	oversized := strings.Repeat("a", 2001)
	if got := submitFeedbackStatus(t, oversized); got != http.StatusBadRequest {
		t.Errorf("2001-char feedback_text: want 400, got %d", got)
	}
}

// @{"verifies": ["REQ-CONTENT-004"]}
func TestSubmitFeedback_TooLong_MultiByteRunes_Returns400(t *testing.T) {
	// Each 'é' is 2 UTF-8 bytes; 2001 runes = 4002 bytes. This verifies that
	// the guard counts Unicode code points (runes) rather than raw bytes so that
	// multi-byte input is not incorrectly rejected below the 2000-rune limit.
	oversized := strings.Repeat("é", 2001)
	if got := submitFeedbackStatus(t, oversized); got != http.StatusBadRequest {
		t.Errorf("2001 multi-byte rune feedback_text: want 400, got %d", got)
	}
}

// @{"verifies": ["REQ-CONTENT-004"]}
func TestSubmitFeedback_ExactlyAtLimit_MultiByteRunes_PassesLengthGuard(t *testing.T) {
	// 2000 multi-byte runes = 4000 UTF-8 bytes. The length guard must allow this
	// through. This integration test requires a real DB so the repo can persist the row.
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()

	var studentID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
		"feedback_limit_"+uuid.New().String(), "testhash", "student",
	).Scan(&studentID); err != nil {
		t.Fatalf("create test user: %v", err)
	}

	var courseID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO courses (student_id, topic) VALUES ($1, $2) RETURNING id`,
		studentID, "test topic",
	).Scan(&courseID); err != nil {
		t.Fatalf("create test course: %v", err)
	}

	// Insert a verified lesson_content row using server role so the FK exists.
	srvConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire server conn: %v", err)
	}
	defer srvConn.Release()
	if _, err := srvConn.Exec(ctx, "SELECT set_config('app.current_role','server',false)"); err != nil {
		t.Fatalf("set server role: %v", err)
	}
	if _, err := srvConn.Exec(ctx,
		`INSERT INTO lesson_content (course_id, section_index, title, content_adoc, citation_verified)
		 VALUES ($1, 0, 'Title', 'body', true)`,
		courseID,
	); err != nil {
		t.Fatalf("insert lesson_content: %v", err)
	}

	// Acquire a student-role conn to inject into the request context so RLS passes.
	stdConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire student conn: %v", err)
	}
	defer stdConn.Release()
	userIDHex := strings.ReplaceAll(studentID.String(), "-", "")
	if _, err := stdConn.Exec(ctx,
		"SELECT set_config('app.current_user_id',$1,false), set_config('app.current_role','student',false)",
		userIDHex,
	); err != nil {
		t.Fatalf("set student GUCs: %v", err)
	}

	repo := NewContentRepository(pool)
	h := NewContentHandler(repo)
	router := chi.NewRouter()
	router.Post("/{id}/{sectionIndex}/feedback", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.WithTestConn(
			auth.SetTestContext(r.Context(), [16]byte(studentID), "student"),
			stdConn,
		))
		h.submitFeedback(w, r)
	})

	atLimit := strings.Repeat("é", 2000) // 2000 runes, 4000 UTF-8 bytes
	body, _ := json.Marshal(map[string]string{"feedback_text": atLimit})
	req := httptest.NewRequest("POST", "/"+courseID.String()+"/0/feedback", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code == http.StatusBadRequest {
		t.Errorf("2000-rune feedback_text: length guard must not reject at the limit, got 400")
	}
	if w.Code != http.StatusCreated {
		t.Errorf("2000-rune feedback_text: want 201, got %d: %s", w.Code, w.Body.String())
	}
}
