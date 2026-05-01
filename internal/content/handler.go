// handler.go — HTTP handler for the content module.
// Exposes section content delivery and student feedback submission.
package content

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/valory/valory/internal/auth"
)

// ContentHandler serves lesson content and accepts student feedback.
// Feedback is stored and the runner polls asynchronously to decide whether
// to trigger regeneration based on keywords (REQ-CONTENT-004).
type ContentHandler struct {
	repo *ContentRepository
}

// @{"req": ["REQ-CONTENT-001", "REQ-CONTENT-002", "REQ-CONTENT-003", "REQ-CONTENT-004"]}
func NewContentHandler(repo *ContentRepository) *ContentHandler {
	return &ContentHandler{repo: repo}
}

// Routes mounts the content endpoints under an already-authenticated router.
//
// @{"req": ["REQ-CONTENT-001", "REQ-CONTENT-002", "REQ-CONTENT-003", "REQ-CONTENT-004"]}
func (h *ContentHandler) Routes(r chi.Router) {
	r.Get("/{sectionIndex}", h.getSection)
	r.Post("/{sectionIndex}/feedback", h.submitFeedback)
}

// getSection returns the latest citation-verified lesson content for a section.
// Returns 404 if the section does not exist, 202 if it exists but is still
// being reviewed.
//
// @{"req": ["REQ-CONTENT-001"]}
func (h *ContentHandler) getSection(w http.ResponseWriter, r *http.Request) {
	courseID, ok := parseCourseID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}

	sectionIndex, err := strconv.Atoi(chi.URLParam(r, "sectionIndex"))
	if err != nil || sectionIndex < 0 {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid section index")
		return
	}

	row, err := h.repo.GetSectionContent(r.Context(), courseID, sectionIndex)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "section not found")
		return
	}
	if errors.Is(err, ErrNotVerified) {
		// Content exists but citation review is still in progress.
		writeJSON(w, http.StatusAccepted, map[string]string{
			"status":  "pending_review",
			"message": "content is being reviewed and will be available shortly",
		})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":                row.ID,
		"course_id":         row.CourseID,
		"section_index":     row.SectionIndex,
		"title":             row.Title,
		"content_adoc":      row.ContentAdoc,
		"version":           row.Version,
		"citation_verified": row.CitationVerified,
		"created_at":        row.CreatedAt,
	})
}

// submitFeedback records student feedback on a section (REQ-CONTENT-004).
// Feedback that contains change-request keywords optionally triggers
// section regeneration via the injected runner.
//
// @{"req": ["REQ-CONTENT-004"]}
func (h *ContentHandler) submitFeedback(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	rawUserID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	studentID := uuid.UUID(rawUserID)

	courseID, ok := parseCourseID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}

	sectionIndex, err := strconv.Atoi(chi.URLParam(r, "sectionIndex"))
	if err != nil || sectionIndex < 0 {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid section index")
		return
	}

	var req struct {
		FeedbackText string `json:"feedback_text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if len(req.FeedbackText) == 0 {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "feedback_text is required")
		return
	}
	if utf8.RuneCountInString(req.FeedbackText) > 2000 {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "feedback_text exceeds 2000 characters")
		return
	}

	fb, err := h.repo.InsertFeedback(r.Context(), studentID, courseID, sectionIndex, req.FeedbackText)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":                      fb.ID,
		"student_id":              fb.StudentID,
		"course_id":               fb.CourseID,
		"section_index":           fb.SectionIndex,
		"feedback_text":           fb.FeedbackText,
		"submitted_at":            fb.SubmittedAt,
		"regeneration_triggered":  fb.RegenerationTriggered,
	})
}

// parseCourseID extracts and parses the {id} URL parameter. Matches the
// course router convention where the course UUID is bound as {id}.
func parseCourseID(r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.UUID{}, false
	}
	return id, true
}

// @{"req": ["REQ-CONTENT-001", "REQ-CONTENT-002", "REQ-CONTENT-003", "REQ-CONTENT-004"]}
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// @{"req": ["REQ-CONTENT-001", "REQ-CONTENT-002", "REQ-CONTENT-003", "REQ-CONTENT-004"]}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}
