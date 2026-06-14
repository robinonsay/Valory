package course

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/valory/valory/internal/auth"
)

// IntakeStarter is a narrow interface for triggering the opening intake question
// after a course is created. Defined here (consumer side) so the course package
// does not import the agent package — cmd/server/main.go satisfies it with
// *agent.Chair.
//
// @{"req": ["REQ-AGENT-018"]}
type IntakeStarter interface {
	// StartIntake fires the opening intake question for a newly created course
	// asynchronously. The implementation must be safe to call from a goroutine.
	StartIntake(ctx context.Context, courseID, studentID uuid.UUID)
}

// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002", "REQ-COURSE-003", "REQ-COURSE-004", "REQ-COURSE-005", "REQ-COURSE-006", "REQ-COURSE-007", "REQ-COURSE-008"]}
type CourseHandler struct {
	svc           *CourseService
	repo          *CourseRepository
	intakeStarter IntakeStarter // nil when not wired (tests, admin paths)
}

// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002", "REQ-COURSE-003", "REQ-COURSE-004", "REQ-COURSE-005", "REQ-COURSE-006", "REQ-COURSE-007", "REQ-COURSE-008"]}
func NewHandler(svc *CourseService) *CourseHandler {
	return &CourseHandler{svc: svc, repo: svc.repo}
}

// SetIntakeStarter injects the kickoff trigger after the handler is constructed.
// Called from cmd/server/main.go after both course and agent modules are wired
// so that neither package imports the other.
//
// @{"req": ["REQ-AGENT-018"]}
func (h *CourseHandler) SetIntakeStarter(s IntakeStarter) {
	h.intakeStarter = s
}

// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002", "REQ-COURSE-003", "REQ-COURSE-004", "REQ-COURSE-005", "REQ-COURSE-006", "REQ-COURSE-007", "REQ-COURSE-008"]}
func (h *CourseHandler) Routes(r chi.Router) {
	r.Post("/", h.createCourse)
	r.Get("/", h.listCourses)
	r.Get("/{id}", h.getCourse)
	r.Get("/{id}/syllabus", h.getSyllabus)
	r.Get("/{id}/homework", h.listHomework)
	r.Post("/{id}/withdraw", h.withdraw)
	r.Post("/{id}/resume", h.resume)
	r.Post("/{id}/syllabus/approve", h.approveSyllabus)
	r.Post("/{id}/syllabus/modification", h.requestModification)
	r.Post("/{id}/schedule/agree", h.agreeToSchedule)
}

// @{"req": ["REQ-COURSE-001", "REQ-AGENT-018"]}
func (h *CourseHandler) createCourse(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	userID := uuid.UUID(rawID)

	var req struct {
		Topic string `json:"topic"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request")
		return
	}

	if strings.TrimSpace(req.Topic) == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "topic is required")
		return
	}

	course, err := h.svc.CreateCourse(r.Context(), userID, req.Topic)
	if err != nil {
		if errors.Is(err, ErrCourseAlreadyActive) {
			writeError(w, http.StatusConflict, "COURSE_ALREADY_ACTIVE", "student already has an active course")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	// Trigger the opening intake question asynchronously so the POST /courses
	// response remains fast (REQ-AGENT-018). A background context is used so
	// the goroutine is not cancelled when the HTTP response is sent. Failure
	// here is non-fatal: the lazy-retry in GET /chat/history will re-trigger
	// the kickoff when the student first loads the chat view.
	if h.intakeStarter != nil {
		cID := course.ID
		sID := userID
		go func() {
			h.intakeStarter.StartIntake(context.Background(), cID, sID)
		}()
	} else {
		log.Printf("course: createCourse: intakeStarter not wired for course %s", course.ID)
	}

	resp := courseToResponse(course)
	writeJSON(w, http.StatusCreated, resp)
}

// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002", "REQ-FEADMIN-708"]}
func (h *CourseHandler) listCourses(w http.ResponseWriter, r *http.Request) {
	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	userID := uuid.UUID(rawID)

	role, ok := auth.RoleFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}

	statusFilter := r.URL.Query().Get("status")

	validStatuses := map[string]bool{
		"intake": true, "syllabus_draft": true, "syllabus_approved": true,
		"generating": true, "active": true, "archived": true, "completed": true,
	}
	if statusFilter != "" && !validStatuses[statusFilter] {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid status filter")
		return
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 20
	if limitStr != "" {
		parsed, err := strconv.Atoi(limitStr)
		if err != nil || parsed < 1 || parsed > 200 {
			limit = 20
		} else {
			limit = parsed
		}
	}

	cursor := r.URL.Query().Get("cursor")

	courses, nextCursor, err := h.svc.ListCourses(r.Context(), userID, role, statusFilter, cursor, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	var courseItems []map[string]interface{}
	for _, course := range courses {
		item := courseToResponse(course)
		courseItems = append(courseItems, item)
	}

	resp := map[string]interface{}{
		"courses":     courseItems,
		"next_cursor": nextCursor,
	}
	writeJSON(w, http.StatusOK, resp)
}

// @{"req": ["REQ-COURSE-003"]}
func (h *CourseHandler) withdraw(w http.ResponseWriter, r *http.Request) {
	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	userID := uuid.UUID(rawID)

	courseIDStr := chi.URLParam(r, "id")
	courseID, err := uuid.Parse(courseIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}

	course, err := h.svc.Withdraw(r.Context(), courseID, userID)
	if err != nil {
		if errors.Is(err, ErrForbidden) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "access forbidden")
			return
		}
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "course not found")
			return
		}
		if errors.Is(err, ErrInvalidTransition) {
			writeError(w, http.StatusConflict, "INVALID_TRANSITION", "invalid state transition")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	resp := map[string]interface{}{
		"id":     course.ID.String(),
		"status": course.Status,
	}
	writeJSON(w, http.StatusOK, resp)
}

// @{"req": ["REQ-COURSE-004"]}
func (h *CourseHandler) resume(w http.ResponseWriter, r *http.Request) {
	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	userID := uuid.UUID(rawID)

	courseIDStr := chi.URLParam(r, "id")
	courseID, err := uuid.Parse(courseIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}

	course, err := h.svc.Resume(r.Context(), courseID, userID)
	if err != nil {
		if errors.Is(err, ErrForbidden) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "access forbidden")
			return
		}
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "course not found")
			return
		}
		if errors.Is(err, ErrCourseAlreadyActive) {
			writeError(w, http.StatusConflict, "COURSE_ALREADY_ACTIVE", "student already has an active course")
			return
		}
		if errors.Is(err, ErrInvalidTransition) {
			writeError(w, http.StatusConflict, "INVALID_TRANSITION", "invalid state transition")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	resp := map[string]interface{}{
		"id":     course.ID.String(),
		"status": course.Status,
	}
	writeJSON(w, http.StatusOK, resp)
}

// @{"req": ["REQ-COURSE-006"]}
func (h *CourseHandler) approveSyllabus(w http.ResponseWriter, r *http.Request) {
	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	userID := uuid.UUID(rawID)

	courseIDStr := chi.URLParam(r, "id")
	courseID, err := uuid.Parse(courseIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}

	syllabus, course, err := h.svc.ApproveSyllabus(r.Context(), courseID, userID)
	if err != nil {
		if errors.Is(err, ErrForbidden) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "access forbidden")
			return
		}
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "course not found")
			return
		}
		if errors.Is(err, ErrInvalidTransition) {
			writeError(w, http.StatusConflict, "NOT_IN_SYLLABUS_DRAFT", "invalid state transition")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	resp := map[string]interface{}{
		"id":               course.ID.String(),
		"status":           course.Status,
		"syllabus_version": syllabus.Version,
	}
	writeJSON(w, http.StatusOK, resp)
}

// @{"req": ["REQ-COURSE-005"]}
func (h *CourseHandler) requestModification(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	userID := uuid.UUID(rawID)

	courseIDStr := chi.URLParam(r, "id")
	courseID, err := uuid.Parse(courseIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}

	var req struct {
		Request string `json:"request"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request")
		return
	}

	if strings.TrimSpace(req.Request) == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "request is required")
		return
	}

	syllabus, course, err := h.svc.RequestModification(r.Context(), courseID, userID, req.Request)
	if err != nil {
		if errors.Is(err, ErrForbidden) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "access forbidden")
			return
		}
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "course not found")
			return
		}
		if errors.Is(err, ErrBadRequest) {
			writeError(w, http.StatusBadRequest, "BAD_REQUEST", "request is required")
			return
		}
		// Log the underlying cause: a bare INTERNAL_ERROR with no log line makes
		// failures here (e.g. an RLS denial on the syllabi write) undiagnosable.
		log.Printf("course: requestModification: course=%s: %v", courseID, err)
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	resp := map[string]interface{}{
		"id":               course.ID.String(),
		"status":           course.Status,
		"syllabus_version": syllabus.Version,
	}
	writeJSON(w, http.StatusOK, resp)
}

// @{"req": ["REQ-COURSE-008"]}
func (h *CourseHandler) agreeToSchedule(w http.ResponseWriter, r *http.Request) {
	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	userID := uuid.UUID(rawID)

	courseIDStr := chi.URLParam(r, "id")
	courseID, err := uuid.Parse(courseIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}

	agreedCount, err := h.svc.AgreeToSchedule(r.Context(), courseID, userID)
	if err != nil {
		if errors.Is(err, ErrForbidden) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "access forbidden")
			return
		}
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "course not found")
			return
		}
		if errors.Is(err, ErrNoPendingSchedule) {
			writeError(w, http.StatusConflict, "NO_PENDING_SCHEDULE", "no pending due dates to agree")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	resp := map[string]interface{}{
		"id":           courseID.String(),
		"agreed_count": agreedCount,
	}
	writeJSON(w, http.StatusOK, resp)
}

// @{"req": ["REQ-COURSE-001", "REQ-COURSE-007"]}
func (h *CourseHandler) getCourse(w http.ResponseWriter, r *http.Request) {
	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	userID := uuid.UUID(rawID)

	role, ok := auth.RoleFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}

	courseIDStr := chi.URLParam(r, "id")
	courseID, err := uuid.Parse(courseIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}

	course, err := h.svc.GetCourse(r.Context(), courseID, userID, role)
	if err != nil {
		if errors.Is(err, ErrForbidden) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "access forbidden")
			return
		}
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "course not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, courseToResponse(course))
}

// @{"req": ["REQ-COURSE-001", "REQ-COURSE-007"]}
func (h *CourseHandler) getSyllabus(w http.ResponseWriter, r *http.Request) {
	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	userID := uuid.UUID(rawID)

	role, ok := auth.RoleFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}

	courseIDStr := chi.URLParam(r, "id")
	courseID, err := uuid.Parse(courseIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}

	// Ownership check: GetCourse enforces student can only access own courses.
	if _, err := h.svc.GetCourse(r.Context(), courseID, userID, role); err != nil {
		if errors.Is(err, ErrForbidden) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "access forbidden")
			return
		}
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "course not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	syllabus, err := h.repo.GetLatestSyllabus(r.Context(), courseID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "syllabus not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	resp := map[string]interface{}{
		"id":           syllabus.ID.String(),
		"course_id":    syllabus.CourseID.String(),
		"content_adoc": syllabus.ContentAdoc,
		"version":      syllabus.Version,
		"created_at":   syllabus.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if syllabus.ApprovedAt != nil {
		resp["approved_at"] = syllabus.ApprovedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	writeJSON(w, http.StatusOK, resp)
}

// @{"req": ["REQ-COURSE-001", "REQ-COURSE-007"]}
func (h *CourseHandler) listHomework(w http.ResponseWriter, r *http.Request) {
	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	userID := uuid.UUID(rawID)

	role, ok := auth.RoleFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}

	courseIDStr := chi.URLParam(r, "id")
	courseID, err := uuid.Parse(courseIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}

	// Ownership check: GetCourse enforces student can only access own courses.
	if _, err := h.svc.GetCourse(r.Context(), courseID, userID, role); err != nil {
		if errors.Is(err, ErrForbidden) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "access forbidden")
			return
		}
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "course not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	homeworks, err := h.repo.ListHomeworkByCourseID(r.Context(), courseID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	dueDates, err := h.repo.ListDueDatesByCourseID(r.Context(), courseID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	items := make([]map[string]interface{}, 0, len(homeworks))
	for _, hw := range homeworks {
		item := map[string]interface{}{
			"id":            hw.ID.String(),
			"course_id":     hw.CourseID.String(),
			"section_index": hw.SectionIndex,
			"title":         hw.Title,
			"grade_weight":  hw.GradeWeight,
		}
		if dd, exists := dueDates[hw.ID]; exists {
			item["due_date"] = dd.Format("2006-01-02T15:04:05Z07:00")
		}
		items = append(items, item)
	}

	writeJSON(w, http.StatusOK, items)
}

// courseToResponse converts a CourseRow to the JSON response map.
// assignment_id is included so the frontend can display an "Assigned by
// instructor" badge when the value is non-null (REQ-ASSIGN-008).
// student_username and student_email are populated by the admin path
// (REQ-FEADMIN-708) and omitted by the student path.
// tree_mode is always present (REQ-SYS-073, REQ-SYS-077) so the frontend can
// route tree-mode courses to the interactive-tree UI without a nil-check.
//
// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002", "REQ-COURSE-003", "REQ-COURSE-004", "REQ-COURSE-005", "REQ-COURSE-006", "REQ-COURSE-008", "REQ-ASSIGN-008", "REQ-FEADMIN-708", "REQ-SYS-073", "REQ-SYS-077"]}
func courseToResponse(course CourseRow) map[string]interface{} {
	resp := map[string]interface{}{
		"id":         course.ID.String(),
		"student_id": course.StudentID.String(),
		"title":      course.Title,
		"topic":      course.Topic,
		"status":     course.Status,
		"created_at": course.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		"updated_at": course.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		// tree_mode is a non-pointer bool; always emit it (false for flat courses,
		// true for layered knowledge-tree courses added in migration 022).
		"tree_mode": course.TreeMode,
	}
	if course.PreWithdrawalStatus != nil {
		resp["pre_withdrawal_status"] = *course.PreWithdrawalStatus
	}
	// assignment_id is null for student-initiated courses and a UUID string for
	// admin-assigned courses.  Included in the response so the frontend can
	// optionally render an "Assigned by instructor" badge (REQ-ASSIGN-008).
	if course.AssignmentID != nil {
		resp["assignment_id"] = course.AssignmentID.String()
	} else {
		resp["assignment_id"] = nil
	}
	// student_username and student_email are populated by the admin path of
	// ListCourses (REQ-FEADMIN-708) and included in the response when present.
	if course.StudentUsername != nil {
		resp["student_username"] = *course.StudentUsername
	}
	if course.StudentEmail != nil {
		resp["student_email"] = *course.StudentEmail
	}
	return resp
}

// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002", "REQ-COURSE-003", "REQ-COURSE-004", "REQ-COURSE-005", "REQ-COURSE-006", "REQ-COURSE-008"]}
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002", "REQ-COURSE-003", "REQ-COURSE-004", "REQ-COURSE-005", "REQ-COURSE-006", "REQ-COURSE-008"]}
func writeError(w http.ResponseWriter, status int, code string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{
		"error":   code,
		"message": message,
	})
}
