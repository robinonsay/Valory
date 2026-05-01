//go:build testing

package submission

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/course"
)

// stubCourseRepo implements courseOwnershipChecker for handler tests.
// It returns a fixed CourseRow or error without a database.
type stubCourseRepo struct {
	row course.CourseRow
	err error
}

// @{"verifies": ["REQ-SUBMISSION-001", "REQ-SUBMISSION-002"]}
func (s *stubCourseRepo) GetCourseByID(_ context.Context, _ uuid.UUID) (course.CourseRow, error) {
	return s.row, s.err
}

// stubConfigSvc implements the configSvc interface returning a fixed value.
type stubConfigSvc struct{ maxBytes int64 }

// @{"verifies": ["REQ-SUBMISSION-002"]}
func (s *stubConfigSvc) GetInt64(_ string) int64 { return s.maxBytes }

// buildUploadRequest constructs a multipart POST request with a single file field.
func buildUploadRequest(t *testing.T, courseID, homeworkID uuid.UUID, filename, content string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	fmt.Fprint(fw, content)
	w.Close()
	path := fmt.Sprintf("/courses/%s/homework/%s/submissions", courseID, homeworkID)
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

// routeUpload wires the upload handler into a chi router matching the URL pattern
// used by buildUploadRequest so that chi.URLParam works correctly.
func routeUpload(h *Handler, studentID uuid.UUID, role string) http.Handler {
	r := chi.NewRouter()
	r.Post("/courses/{id}/homework/{homeworkId}/submissions", func(w http.ResponseWriter, req *http.Request) {
		req = req.WithContext(auth.SetTestContext(req.Context(), [16]byte(studentID), role))
		h.upload(w, req)
	})
	return r
}

// routeGetLatest wires the getLatest handler into a chi router.
func routeGetLatest(h *Handler, studentID uuid.UUID, role string) http.Handler {
	r := chi.NewRouter()
	r.Get("/courses/{id}/homework/{homeworkId}/submissions/latest", func(w http.ResponseWriter, req *http.Request) {
		req = req.WithContext(auth.SetTestContext(req.Context(), [16]byte(studentID), role))
		h.getLatest(w, req)
	})
	return r
}

// @{"verifies": ["REQ-SUBMISSION-002"]}
func TestUpload_FileTooLarge(t *testing.T) {
	studentID := uuid.New()
	courseID := uuid.New()
	homeworkID := uuid.New()

	cRepo := &stubCourseRepo{
		row: course.CourseRow{ID: courseID, StudentID: studentID},
	}
	// maxBytes is set to 10 so even the multipart envelope overhead causes
	// ParseMultipartForm to fail, triggering the 413 path before any DB access.
	const maxBytes = int64(10)
	h := NewHandler(nil, cRepo, t.TempDir(), &stubConfigSvc{maxBytes: maxBytes})

	// Build a multipart body whose total size far exceeds the tiny limit.
	oversized := strings.Repeat("x", 1024)
	req := buildUploadRequest(t, courseID, homeworkID, "essay.md", oversized)
	w := httptest.NewRecorder()

	routeUpload(h, studentID, "student").ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "REQUEST_TOO_LARGE" {
		t.Errorf("expected error REQUEST_TOO_LARGE, got %q", resp["error"])
	}
}

// @{"verifies": ["REQ-SUBMISSION-001"]}
func TestUpload_UnsupportedFormat(t *testing.T) {
	studentID := uuid.New()
	courseID := uuid.New()
	homeworkID := uuid.New()

	cRepo := &stubCourseRepo{
		row: course.CourseRow{ID: courseID, StudentID: studentID},
	}
	h := NewHandler(nil, cRepo, t.TempDir(), &stubConfigSvc{maxBytes: defaultMaxUploadBytes})

	req := buildUploadRequest(t, courseID, homeworkID, "report.docx", "some content")
	w := httptest.NewRecorder()

	routeUpload(h, studentID, "student").ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "UNSUPPORTED_FORMAT" {
		t.Errorf("expected error UNSUPPORTED_FORMAT, got %q", resp["error"])
	}
}

// @{"verifies": ["REQ-SUBMISSION-001"]}
func TestUpload_Unauthenticated(t *testing.T) {
	courseID := uuid.New()
	homeworkID := uuid.New()

	h := NewHandler(nil, nil, t.TempDir(), &stubConfigSvc{maxBytes: defaultMaxUploadBytes})

	// Build request but mount the handler without injecting an auth context.
	path := fmt.Sprintf("/courses/%s/homework/%s/submissions", courseID, homeworkID)
	req := httptest.NewRequest(http.MethodPost, path, nil)

	r := chi.NewRouter()
	r.Post("/courses/{id}/homework/{homeworkId}/submissions", h.upload)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "UNAUTHORIZED" {
		t.Errorf("expected error UNAUTHORIZED, got %q", resp["error"])
	}
}

// @{"verifies": ["REQ-SUBMISSION-001"]}
func TestGetLatest_NotFound(t *testing.T) {
	if testPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	studentID := uuid.New()
	courseID := uuid.New()
	// Use a random homeworkID that does not exist in the DB.
	homeworkID := uuid.New()

	// The stub returns a course owned by this student so the ownership check passes.
	cRepo := &stubCourseRepo{
		row: course.CourseRow{ID: courseID, StudentID: studentID},
	}
	repo := NewRepository(testPool)
	h := NewHandler(repo, cRepo, t.TempDir(), &stubConfigSvc{maxBytes: defaultMaxUploadBytes})

	path := fmt.Sprintf("/courses/%s/homework/%s/submissions/latest", courseID, homeworkID)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()

	routeGetLatest(h, studentID, "student").ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "NOT_FOUND" {
		t.Errorf("expected error NOT_FOUND, got %q", resp["error"])
	}
}
