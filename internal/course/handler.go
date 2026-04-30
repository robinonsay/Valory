package course

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/valory/valory/internal/auth"
)

// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002", "REQ-COURSE-003", "REQ-COURSE-004", "REQ-COURSE-005", "REQ-COURSE-006", "REQ-COURSE-008"]}
type CourseHandler struct {
	svc *CourseService
}

// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002", "REQ-COURSE-003", "REQ-COURSE-004", "REQ-COURSE-005", "REQ-COURSE-006", "REQ-COURSE-008"]}
func NewHandler(svc *CourseService) *CourseHandler {
	return &CourseHandler{svc: svc}
}

// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002", "REQ-COURSE-003", "REQ-COURSE-004", "REQ-COURSE-005", "REQ-COURSE-006", "REQ-COURSE-008"]}
func (h *CourseHandler) Routes(r chi.Router) {
	r.Post("/", h.createCourse)
	r.Get("/", h.listCourses)
	r.Post("/{id}/withdraw", h.withdraw)
	r.Post("/{id}/resume", h.resume)
	r.Post("/{id}/syllabus/approve", h.approveSyllabus)
	r.Post("/{id}/syllabus/modification", h.requestModification)
	r.Post("/{id}/schedule/agree", h.agreeToSchedule)
}

// @{"req": ["REQ-COURSE-001"]}
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

	resp := courseToResponse(course)
	writeJSON(w, http.StatusCreated, resp)
}

// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002"]}
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
		"id":                course.ID.String(),
		"status":            course.Status,
		"syllabus_version":  syllabus.Version,
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
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	resp := map[string]interface{}{
		"id":                course.ID.String(),
		"status":            course.Status,
		"syllabus_version":  syllabus.Version,
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
		"id":            courseID.String(),
		"agreed_count":  agreedCount,
	}
	writeJSON(w, http.StatusOK, resp)
}

// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002", "REQ-COURSE-003", "REQ-COURSE-004", "REQ-COURSE-005", "REQ-COURSE-006", "REQ-COURSE-008"]}
func courseToResponse(course CourseRow) map[string]interface{} {
	resp := map[string]interface{}{
		"id":         course.ID.String(),
		"student_id": course.StudentID.String(),
		"title":      course.Title,
		"topic":      course.Topic,
		"status":     course.Status,
		"created_at": course.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		"updated_at": course.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if course.PreWithdrawalStatus != nil {
		resp["pre_withdrawal_status"] = *course.PreWithdrawalStatus
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
