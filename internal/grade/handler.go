// handler.go — HTTP endpoints for reading grade data.
// Students can read their own grades via RLS-gated repository calls.
// No write endpoints are exposed: grades are computed by the agent runner.
package grade

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/valory/valory/internal/auth"
)

// gradeServiceInterface is the subset of Service the handler needs.
type gradeServiceInterface interface {
	GetCourseGradeSummary(ctx context.Context, courseID, studentID uuid.UUID) (CourseSummary, error)
}

// gradeRepoForHandler is the subset of Repository the handler needs.
// Kept separate from gradeServiceInterface so handlers can bypass the service
// for simple read-through queries that do not require badge computation.
type gradeRepoForHandler interface {
	getByHomeworkAndStudent(ctx context.Context, homeworkID, studentID uuid.UUID) (GradeRow, error)
}

// Handler serves grade read endpoints for students.
type Handler struct {
	svc  gradeServiceInterface
	repo gradeRepoForHandler
}

// @{"req": ["REQ-GRADE-001", "REQ-GRADE-002", "REQ-GRADE-003", "REQ-GRADE-004", "REQ-GRADE-005"]}
func NewHandler(svc gradeServiceInterface, repo *Repository) *Handler {
	return &Handler{svc: svc, repo: repo}
}

// CourseRoutes mounts the course-level grade summary endpoint.
// Must be nested inside /courses/{id} in the authenticated router group.
//
// @{"req": ["REQ-GRADE-003", "REQ-GRADE-004"]}
func (h *Handler) CourseRoutes(r chi.Router) {
	r.Get("/", h.getCourseGrade)
}

// HomeworkRoutes mounts the per-homework grade endpoint.
// Must be nested inside /courses/{id}/homework/{homeworkId} in the authenticated group.
//
// @{"req": ["REQ-GRADE-001", "REQ-GRADE-002", "REQ-GRADE-005"]}
func (h *Handler) HomeworkRoutes(r chi.Router) {
	r.Get("/", h.getHomeworkGrade)
}

// getCourseGrade returns the weighted average grade summary for the requesting
// student in the specified course. Badge grade improvements are folded in by
// the service layer (REQ-GRADE-004).
//
// @{"req": ["REQ-GRADE-003", "REQ-GRADE-004"]}
func (h *Handler) getCourseGrade(w http.ResponseWriter, r *http.Request) {
	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	studentID := uuid.UUID(rawID)

	courseIDStr := chi.URLParam(r, "id")
	courseID, err := uuid.Parse(courseIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}

	summary, err := h.svc.GetCourseGradeSummary(r.Context(), courseID, studentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, courseSummaryResponse(summary))
}

// getHomeworkGrade returns the grade row for the student's submission on a
// specific homework assignment. Returns 404 when the homework has not been graded.
//
// @{"req": ["REQ-GRADE-001", "REQ-GRADE-002", "REQ-GRADE-005"]}
func (h *Handler) getHomeworkGrade(w http.ResponseWriter, r *http.Request) {
	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	studentID := uuid.UUID(rawID)

	_, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}

	homeworkIDStr := chi.URLParam(r, "homeworkId")
	homeworkID, err := uuid.Parse(homeworkIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid homework id")
		return
	}

	// Look up the grade directly by homework_id + student_id.
	// The grade table carries both foreign keys, avoiding a two-step query
	// through the submissions table.
	g, err := h.repo.getByHomeworkAndStudent(r.Context(), homeworkID, studentID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "grade not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, gradeResponse(g))
}

// courseSummaryResponse converts a CourseSummary to the JSON response shape.
//
// @{"req": ["REQ-GRADE-003", "REQ-GRADE-004"]}
func courseSummaryResponse(s CourseSummary) map[string]interface{} {
	return map[string]interface{}{
		"course_id":      s.CourseID.String(),
		"student_id":     s.StudentID.String(),
		"weighted_score": s.WeightedScore,
		"total_weight":   s.TotalWeight,
	}
}

// gradeResponse converts a GradeRow to the JSON response shape.
//
// @{"req": ["REQ-GRADE-001", "REQ-GRADE-002", "REQ-GRADE-005"]}
func gradeResponse(g GradeRow) map[string]interface{} {
	return map[string]interface{}{
		"id":                   g.ID.String(),
		"submission_id":        g.SubmissionID.String(),
		"homework_id":          g.HomeworkID.String(),
		"student_id":           g.StudentID.String(),
		"course_id":            g.CourseID.String(),
		"raw_score":            g.RawScore,
		"late_days":            g.LateDays,
		"late_penalty_rate":    g.LatePenaltyRate,
		"late_penalty_amount":  g.LatePenaltyAmount,
		"badge_waiver_applied": g.BadgeWaiverApplied,
		"badge_improvement":    g.BadgeImprovement,
		"final_score":          g.FinalScore,
		"graded_at":            g.GradedAt.Format(time.RFC3339),
	}
}

// writeJSON serialises v as JSON with the given HTTP status code.
//
// @{"req": ["REQ-GRADE-001", "REQ-GRADE-003"]}
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error envelope.
//
// @{"req": ["REQ-GRADE-001", "REQ-GRADE-003"]}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}
