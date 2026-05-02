//go:build testing

// @{"verifies": ["REQ-CONTENT-001", "REQ-CONTENT-002", "REQ-CONTENT-003", "REQ-CONTENT-004"]}
package content

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
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

	h := NewContentHandler(nil, nil)

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
	h := NewContentHandler(repo, nil)
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

// @{"verifies": ["REQ-CONTENT-002", "REQ-CONTENT-003"]}
func TestExportSection_BadFormat_Returns400(t *testing.T) {
	h := NewContentHandler(nil, nil)

	router := chi.NewRouter()
	router.Get("/{id}/{sectionIndex}/export", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(uuid.New()), "student"))
		h.exportSection(w, r)
	})

	req := httptest.NewRequest("GET", "/"+uuid.New().String()+"/0/export?format=docx", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for format=docx, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "BAD_REQUEST" {
		t.Errorf("want error code BAD_REQUEST, got %s", resp["error"])
	}
}

// @{"verifies": ["REQ-CONTENT-002", "REQ-CONTENT-003"]}
func TestExportSection_MissingFormat_Returns400(t *testing.T) {
	h := NewContentHandler(nil, nil)

	router := chi.NewRouter()
	router.Get("/{id}/{sectionIndex}/export", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(uuid.New()), "student"))
		h.exportSection(w, r)
	})

	req := httptest.NewRequest("GET", "/"+uuid.New().String()+"/0/export", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for missing format, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "BAD_REQUEST" {
		t.Errorf("want error code BAD_REQUEST, got %s", resp["error"])
	}
}

// @{"verifies": ["REQ-CONTENT-002", "REQ-CONTENT-003"]}
func TestExportSection_NotFound_Returns404(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewContentRepository(pool)

	studentID := createUser(ctx, t, "export_notfound_"+uuid.New().String())
	courseID := createCourse(ctx, t, studentID, "test topic")

	h := NewContentHandler(repo, nil)
	router := chi.NewRouter()
	router.Get("/{id}/{sectionIndex}/export", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		h.exportSection(w, r)
	})

	req := httptest.NewRequest("GET", "/"+courseID.String()+"/99/export?format=html", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("want 404 for nonexistent section, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "NOT_FOUND" {
		t.Errorf("want error code NOT_FOUND, got %s", resp["error"])
	}
}

// @{"verifies": ["REQ-CONTENT-002", "REQ-CONTENT-003"]}
func TestExportSection_BinaryNotFound_Returns500(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	if _, err := exec.LookPath("asciidoctor"); err == nil {
		t.Skip("asciidoctor is installed; skipping error path test")
	}

	ctx := context.Background()
	repo := NewContentRepository(pool)

	studentID := createUser(ctx, t, "export_notinstalled_"+uuid.New().String())
	courseID := createCourse(ctx, t, studentID, "test topic")

	// Insert verified content
	content, err := repo.InsertLessonContent(ctx, courseID, 0, "Test Section", "= Test\nSome content")
	if err != nil {
		t.Fatalf("InsertLessonContent: %v", err)
	}
	if err := repo.SetCitationVerified(ctx, content.ID); err != nil {
		t.Fatalf("SetCitationVerified: %v", err)
	}

	h := NewContentHandler(repo, nil)
	router := chi.NewRouter()
	router.Get("/{id}/{sectionIndex}/export", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		h.exportSection(w, r)
	})

	req := httptest.NewRequest("GET", "/"+courseID.String()+"/0/export?format=html", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("want 500 when asciidoctor binary not found, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "EXPORT_FAILED" {
		t.Errorf("want error code EXPORT_FAILED, got %s", resp["error"])
	}
}

// @{"verifies": ["REQ-CONTENT-002", "REQ-CONTENT-003"]}
func TestExportSection_HappyPath_HTML(t *testing.T) {
	if _, err := exec.LookPath("asciidoctor"); err != nil {
		t.Skip("asciidoctor not installed")
	}

	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewContentRepository(pool)

	studentID := createUser(ctx, t, "export_html_"+uuid.New().String())
	courseID := createCourse(ctx, t, studentID, "test topic")

	// Insert verified content
	content, err := repo.InsertLessonContent(ctx, courseID, 0, "Introduction", "= Introduction\nHello world.")
	if err != nil {
		t.Fatalf("InsertLessonContent: %v", err)
	}
	if err := repo.SetCitationVerified(ctx, content.ID); err != nil {
		t.Fatalf("SetCitationVerified: %v", err)
	}

	h := NewContentHandler(repo, nil)
	router := chi.NewRouter()
	router.Get("/{id}/{sectionIndex}/export", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		h.exportSection(w, r)
	})

	req := httptest.NewRequest("GET", "/"+courseID.String()+"/0/export?format=html", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("want 200 for successful export, got %d", w.Code)
	}

	if w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("want Content-Type 'text/html; charset=utf-8', got %s", w.Header().Get("Content-Type"))
	}

	expectedDisposition := `attachment; filename="section-0.html"`
	if w.Header().Get("Content-Disposition") != expectedDisposition {
		t.Errorf("want Content-Disposition %q, got %q", expectedDisposition, w.Header().Get("Content-Disposition"))
	}

	if w.Body.Len() == 0 {
		t.Error("want non-empty response body for HTML export")
	}

	if !strings.Contains(w.Body.String(), "<!DOCTYPE html") && !strings.Contains(w.Body.String(), "<html") {
		t.Error("want HTML output, but response does not look like HTML")
	}
}

// @{"verifies": ["REQ-CONTENT-002", "REQ-CONTENT-003"]}
func TestExportSection_HappyPath_PDF(t *testing.T) {
	if _, err := exec.LookPath("asciidoctor-pdf"); err != nil {
		t.Skip("asciidoctor-pdf not installed")
	}

	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewContentRepository(pool)

	studentID := createUser(ctx, t, "export_pdf_"+uuid.New().String())
	courseID := createCourse(ctx, t, studentID, "test topic")

	// Insert verified content
	content, err := repo.InsertLessonContent(ctx, courseID, 1, "Chapter One", "= Chapter One\nLet's begin.")
	if err != nil {
		t.Fatalf("InsertLessonContent: %v", err)
	}
	if err := repo.SetCitationVerified(ctx, content.ID); err != nil {
		t.Fatalf("SetCitationVerified: %v", err)
	}

	h := NewContentHandler(repo, nil)
	router := chi.NewRouter()
	router.Get("/{id}/{sectionIndex}/export", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		h.exportSection(w, r)
	})

	req := httptest.NewRequest("GET", "/"+courseID.String()+"/1/export?format=pdf", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("want 200 for successful export, got %d", w.Code)
	}

	if w.Header().Get("Content-Type") != "application/pdf" {
		t.Errorf("want Content-Type 'application/pdf', got %s", w.Header().Get("Content-Type"))
	}

	expectedDisposition := `attachment; filename="section-1.pdf"`
	if w.Header().Get("Content-Disposition") != expectedDisposition {
		t.Errorf("want Content-Disposition %q, got %q", expectedDisposition, w.Header().Get("Content-Disposition"))
	}

	if w.Body.Len() == 0 {
		t.Error("want non-empty response body for PDF export")
	}

	// PDF files start with magic number %PDF
	if !strings.HasPrefix(w.Body.String(), "%PDF") {
		t.Error("want PDF output, but response does not start with PDF magic number")
	}
}

// @{"verifies": ["REQ-CONTENT-001"]}
func TestListSections_WithSections_Returns200(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewContentRepository(pool)

	studentID := createUser(ctx, t, "listsections_with_"+uuid.New().String())
	courseID := createCourse(ctx, t, studentID, "test topic")

	// Insert and verify two sections
	section0, err := repo.InsertLessonContent(ctx, courseID, 0, "Introduction", "= Intro")
	if err != nil {
		t.Fatalf("InsertLessonContent section 0: %v", err)
	}
	if err := repo.SetCitationVerified(ctx, section0.ID); err != nil {
		t.Fatalf("SetCitationVerified section 0: %v", err)
	}

	section1, err := repo.InsertLessonContent(ctx, courseID, 1, "Chapter One", "= Chapter 1")
	if err != nil {
		t.Fatalf("InsertLessonContent section 1: %v", err)
	}
	if err := repo.SetCitationVerified(ctx, section1.ID); err != nil {
		t.Fatalf("SetCitationVerified section 1: %v", err)
	}

	h := NewContentHandler(repo, nil)
	router := chi.NewRouter()
	router.Get("/{id}/sections", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		h.listSections(w, r)
	})

	req := httptest.NewRequest("GET", "/"+courseID.String()+"/sections", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var sections []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&sections); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(sections) != 2 {
		t.Errorf("want 2 sections, got %d", len(sections))
	}

	if len(sections) >= 1 {
		if sections[0]["section_index"] != float64(0) {
			t.Errorf("section[0].section_index: expected 0, got %v", sections[0]["section_index"])
		}
		if sections[0]["title"] != "Introduction" {
			t.Errorf("section[0].title: expected 'Introduction', got %v", sections[0]["title"])
		}
	}

	if len(sections) >= 2 {
		if sections[1]["section_index"] != float64(1) {
			t.Errorf("section[1].section_index: expected 1, got %v", sections[1]["section_index"])
		}
		if sections[1]["title"] != "Chapter One" {
			t.Errorf("section[1].title: expected 'Chapter One', got %v", sections[1]["title"])
		}
	}
}

// @{"verifies": ["REQ-CONTENT-001"]}
func TestListSections_Empty_Returns200WithEmptyArray(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewContentRepository(pool)

	studentID := createUser(ctx, t, "listsections_empty_"+uuid.New().String())
	courseID := createCourse(ctx, t, studentID, "test topic")

	h := NewContentHandler(repo, nil)
	router := chi.NewRouter()
	router.Get("/{id}/sections", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		h.listSections(w, r)
	})

	req := httptest.NewRequest("GET", "/"+courseID.String()+"/sections", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	body := strings.TrimSpace(w.Body.String())
	if body != "[]" {
		t.Errorf("want empty array '[]' not null, got %q", body)
	}
}
