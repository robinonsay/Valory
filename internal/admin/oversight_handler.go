package admin

import (
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// OversightHandler serves the read-only admin oversight endpoint for student
// course material. No mutation endpoints are present; only GET is registered.
// The caller in main.go applies auth.RequireRole("admin") and CSRF middleware
// before mounting.
//
// @{"req": ["REQ-ADMIN-011"]}
type OversightHandler struct {
	svc *OversightService
}

// @{"req": ["REQ-ADMIN-011"]}
func NewOversightHandler(svc *OversightService) *OversightHandler {
	return &OversightHandler{svc: svc}
}

// Routes registers the oversight endpoints on the provided router.
// The caller must apply RequireRole("admin") before mounting.
//
// @{"req": ["REQ-ADMIN-011"]}
func (h *OversightHandler) Routes(r chi.Router) {
	r.Get("/{id}/overview", h.getCourseOverview)
}

// getCourseOverview handles GET /admin/courses/{id}/overview.
// Returns the full admin-visible material for the course: course summary,
// syllabus, lesson sections, homework (with rubric and due date), submissions,
// and grades. The response is read-only; no mutations are possible via this
// handler.
//
// @{"req": ["REQ-ADMIN-011"]}
func (h *OversightHandler) getCourseOverview(w http.ResponseWriter, r *http.Request) {
	courseID, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}

	overview, err := h.svc.GetCourseOverview(r.Context(), courseID)
	if err != nil {
		if errors.Is(err, ErrCourseNotFound) {
			writeJSONError(w, http.StatusNotFound, "course not found")
			return
		}
		log.Printf("admin oversight: getCourseOverview %s: %v", courseID, err)
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	resp := buildOverviewResponse(overview)
	writeJSONResponse(w, http.StatusOK, resp)
}

// buildOverviewResponse converts a CourseOverview into the API response map.
//
// @{"req": ["REQ-ADMIN-011"]}
func buildOverviewResponse(ov CourseOverview) map[string]interface{} {
	// --- course ---
	courseMap := map[string]interface{}{
		"id":         ov.Course.ID.String(),
		"student_id": ov.Course.StudentID.String(),
		"username":   ov.Course.Username,
		"topic":      ov.Course.Topic,
		"status":     ov.Course.Status,
		"created_at": ov.Course.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		"updated_at": ov.Course.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}

	// --- syllabus (optional — may not exist yet if course is still in draft) ---
	var syllabusMap interface{}
	if ov.Syllabus != nil {
		s := ov.Syllabus
		sm := map[string]interface{}{
			"id":           s.ID.String(),
			"content_adoc": s.ContentAdoc,
			"version":      s.Version,
			"created_at":   s.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
		if s.ApprovedAt != nil {
			sm["approved_at"] = s.ApprovedAt.Format("2006-01-02T15:04:05Z07:00")
		} else {
			sm["approved_at"] = nil
		}
		syllabusMap = sm
	}

	// --- sections ---
	sections := make([]map[string]interface{}, 0, len(ov.Sections))
	for _, sec := range ov.Sections {
		sections = append(sections, map[string]interface{}{
			"id":                sec.ID.String(),
			"section_index":     sec.SectionIndex,
			"title":             sec.Title,
			"content_adoc":      sec.ContentAdoc,
			"version":           sec.Version,
			"citation_verified": sec.CitationVerified,
			"created_at":        sec.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}

	// --- homework ---
	homework := make([]map[string]interface{}, 0, len(ov.Homework))
	for _, hw := range ov.Homework {
		hm := map[string]interface{}{
			"id":            hw.ID.String(),
			"section_index": hw.SectionIndex,
			"title":         hw.Title,
			"rubric":        hw.Rubric,
			"grade_weight":  hw.GradeWeight,
			"created_at":    hw.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
		if hw.DueDate != nil {
			hm["due_date"] = hw.DueDate.Format("2006-01-02T15:04:05Z07:00")
		} else {
			hm["due_date"] = nil
		}
		homework = append(homework, hm)
	}

	// --- submissions ---
	submissions := make([]map[string]interface{}, 0, len(ov.Submissions))
	for _, sub := range ov.Submissions {
		sm := map[string]interface{}{
			"id":              sub.ID.String(),
			"homework_id":     sub.HomeworkID.String(),
			"student_id":      sub.StudentID.String(),
			"format":          sub.Format,
			"file_path":       sub.FilePath,
			"file_size_bytes": sub.FileSizeBytes,
			"submitted_at":    sub.SubmittedAt.Format("2006-01-02T15:04:05Z07:00"),
			"grading_status":  sub.GradingStatus,
		}
		if sub.RawScore != nil {
			sm["raw_score"] = *sub.RawScore
		} else {
			sm["raw_score"] = nil
		}
		submissions = append(submissions, sm)
	}

	// --- grades ---
	grades := make([]map[string]interface{}, 0, len(ov.Grades))
	for _, g := range ov.Grades {
		grades = append(grades, map[string]interface{}{
			"id":                   g.ID.String(),
			"submission_id":        g.SubmissionID.String(),
			"homework_id":          g.HomeworkID.String(),
			"raw_score":            g.RawScore,
			"late_days":            g.LateDays,
			"late_penalty_rate":    g.LatePenaltyRate,
			"late_penalty_amount":  g.LatePenaltyAmount,
			"badge_waiver_applied": g.BadgeWaiverApplied,
			"badge_improvement":    g.BadgeImprovement,
			"final_score":          g.FinalScore,
			"graded_at":            g.GradedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}

	return map[string]interface{}{
		"course":      courseMap,
		"syllabus":    syllabusMap,
		"sections":    sections,
		"homework":    homework,
		"submissions": submissions,
		"grades":      grades,
	}
}
