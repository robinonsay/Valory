package admin

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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/valory/valory/internal/audit"
	"github.com/valory/valory/internal/auth"
)

// AdminAssignmentHandler implements the HTTP endpoints for course assignments.
// All endpoints require RequireRole("admin") and CSRF middleware, applied by the
// caller in main.go before mounting.
//
// @{"req": ["REQ-ASSIGN-001", "REQ-ASSIGN-002", "REQ-ASSIGN-003", "REQ-ASSIGN-004", "REQ-ASSIGN-005", "REQ-ASSIGN-006", "REQ-ASSIGN-007", "REQ-ASSIGN-009", "REQ-ASSIGN-010", "REQ-ASSIGN-011"]}
type AdminAssignmentHandler struct {
	svc       *AssignmentService
	auditRepo *audit.Repository
	pool      *pgxpool.Pool
}

// @{"req": ["REQ-ASSIGN-001", "REQ-ASSIGN-002", "REQ-ASSIGN-003", "REQ-ASSIGN-004", "REQ-ASSIGN-005", "REQ-ASSIGN-006", "REQ-ASSIGN-007", "REQ-ASSIGN-009", "REQ-ASSIGN-010", "REQ-ASSIGN-011"]}
func NewAssignmentHandler(svc *AssignmentService, auditRepo *audit.Repository, pool *pgxpool.Pool) *AdminAssignmentHandler {
	return &AdminAssignmentHandler{svc: svc, auditRepo: auditRepo, pool: pool}
}

// Routes registers the assignment endpoints on the provided router.
// The caller must apply RequireRole("admin") and CSRF before mounting.
//
// @{"req": ["REQ-ASSIGN-001", "REQ-ASSIGN-002", "REQ-ASSIGN-003", "REQ-ASSIGN-004", "REQ-ASSIGN-005", "REQ-ASSIGN-006", "REQ-ASSIGN-007"]}
func (h *AdminAssignmentHandler) Routes(r chi.Router) {
	r.Post("/", h.createAssignment)
	r.Get("/", h.listAssignments)
	r.Get("/{id}", h.getAssignment)
	r.Post("/{id}/students", h.assignStudents)
	r.Delete("/{id}/students/{studentId}", h.unassignStudent)
}

// createAssignment handles POST /api/v1/admin/assignments.
//
// @{"req": ["REQ-ASSIGN-001"]}
func (h *AdminAssignmentHandler) createAssignment(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)

	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	adminID := uuid.UUID(rawID)

	var req struct {
		Title      string          `json:"title"`
		Topic      string          `json:"topic"`
		Level      string          `json:"level"`
		Parameters json.RawMessage `json:"parameters"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Topic = strings.TrimSpace(req.Topic)
	if req.Topic == "" {
		writeJSONError(w, http.StatusBadRequest, "topic is required")
		return
	}
	if len(req.Topic) > 500 {
		writeJSONError(w, http.StatusBadRequest, "topic must be at most 500 characters")
		return
	}
	if len(req.Title) > 255 {
		writeJSONError(w, http.StatusBadRequest, "title must be at most 255 characters")
		return
	}
	if req.Level == "" {
		req.Level = "beginner"
	}
	switch req.Level {
	case "beginner", "intermediate", "advanced":
	default:
		writeJSONError(w, http.StatusBadRequest, "level must be one of: beginner, intermediate, advanced")
		return
	}
	// parameters must be a JSON object (not an array or scalar) or absent.
	if len(req.Parameters) > 0 && !isJSONObject(req.Parameters) {
		writeJSONError(w, http.StatusBadRequest, "parameters must be a JSON object")
		return
	}
	if len(req.Parameters) == 0 {
		req.Parameters = json.RawMessage("{}")
	}

	assignment, err := h.svc.CreateAssignment(r.Context(), adminID, req.Title, req.Topic, req.Level, req.Parameters)
	if err != nil {
		log.Printf("admin: createAssignment: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Audit: name-only — no topic/parameter values in the payload to avoid
	// accidentally logging sensitive curriculum information.
	h.appendAudit(r.Context(), adminID, "assignment.created", "course_assignment", &assignment.ID,
		map[string]any{"level": assignment.Level})

	writeJSONResponse(w, http.StatusCreated, assignmentToResponse(assignment))
}

// listAssignments handles GET /api/v1/admin/assignments.
//
// @{"req": ["REQ-ASSIGN-006"]}
func (h *AdminAssignmentHandler) listAssignments(w http.ResponseWriter, r *http.Request) {
	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	adminID := uuid.UUID(rawID)

	limitStr := r.URL.Query().Get("limit")
	limit := 20
	if limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed >= 1 && parsed <= 200 {
			limit = parsed
		}
	}
	cursor := r.URL.Query().Get("cursor")

	assignments, nextCursor, err := h.svc.ListAssignments(r.Context(), adminID, cursor, limit)
	if err != nil {
		log.Printf("admin: listAssignments: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	items := make([]map[string]interface{}, 0, len(assignments))
	for _, a := range assignments {
		items = append(items, map[string]interface{}{
			"id":            a.ID.String(),
			"title":         a.Title,
			"topic":         a.Topic,
			"level":         a.Level,
			"student_count": a.StudentCount,
			"created_at":    a.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}

	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"assignments": items,
		"next_cursor": nextCursor,
	})
}

// getAssignment handles GET /api/v1/admin/assignments/{id}.
//
// @{"req": ["REQ-ASSIGN-006", "REQ-ASSIGN-010"]}
func (h *AdminAssignmentHandler) getAssignment(w http.ResponseWriter, r *http.Request) {
	assignmentID, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}

	assignment, students, err := h.svc.GetAssignment(r.Context(), assignmentID)
	if err != nil {
		if errors.Is(err, ErrAssignmentNotFound) {
			writeJSONError(w, http.StatusNotFound, "assignment not found")
			return
		}
		log.Printf("admin: getAssignment %s: %v", assignmentID, err)
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	studentItems := make([]map[string]interface{}, 0, len(students))
	for _, s := range students {
		studentItems = append(studentItems, map[string]interface{}{
			"student_id":    s.StudentID.String(),
			"username":      s.Username,
			"course_id":     s.CourseID.String(),
			"course_status": s.CourseStatus,
			"assigned_at":   s.AssignedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}

	resp := assignmentToResponse(assignment)
	resp["students"] = studentItems
	writeJSONResponse(w, http.StatusOK, resp)
}

// assignStudents handles POST /api/v1/admin/assignments/{id}/students.
// Implements the partial-success model: attempts each student independently,
// reports per-student results (REQ-ASSIGN-011).
//
// @{"req": ["REQ-ASSIGN-002", "REQ-ASSIGN-003", "REQ-ASSIGN-004", "REQ-ASSIGN-005", "REQ-ASSIGN-011"]}
func (h *AdminAssignmentHandler) assignStudents(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	adminID := uuid.UUID(rawID)

	assignmentID, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}

	// Confirm the assignment exists before processing any students.
	if _, err := h.svc.repo.GetAssignment(r.Context(), assignmentID); err != nil {
		if errors.Is(err, ErrAssignmentNotFound) {
			writeJSONError(w, http.StatusNotFound, "assignment not found")
			return
		}
		log.Printf("admin: assignStudents: get assignment %s: %v", assignmentID, err)
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	var req struct {
		StudentIDs []string `json:"student_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.StudentIDs) == 0 {
		writeJSONError(w, http.StatusBadRequest, "student_ids must not be empty")
		return
	}
	if len(req.StudentIDs) > maxBatchSize {
		writeJSONError(w, http.StatusBadRequest,
			"batch size exceeds maximum of "+strconv.Itoa(maxBatchSize)+" students per request")
		return
	}

	var studentIDs []uuid.UUID
	for _, s := range req.StudentIDs {
		id, err := uuid.Parse(s)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid student_id: "+s)
			return
		}
		studentIDs = append(studentIDs, id)
	}

	created, errs := h.svc.AssignStudents(r.Context(), assignmentID, studentIDs)

	// Audit name-only for the batch action.
	h.appendAudit(r.Context(), adminID, "assignment.students_assigned", "course_assignment", &assignmentID,
		map[string]any{"created_count": len(created), "error_count": len(errs)})

	createdItems := make([]map[string]interface{}, 0, len(created))
	for _, c := range created {
		createdItems = append(createdItems, map[string]interface{}{
			"student_id":    c.StudentID.String(),
			"course_id":     c.CourseID.String(),
			"course_status": "syllabus_approved",
		})
	}
	errorItems := make([]map[string]interface{}, 0, len(errs))
	for _, e := range errs {
		errorItems = append(errorItems, map[string]interface{}{
			"student_id": e.StudentID.String(),
			"error":      e.Error,
		})
	}

	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"created": createdItems,
		"errors":  errorItems,
	})
}

// unassignStudent handles DELETE /api/v1/admin/assignments/{id}/students/{studentId}.
//
// @{"req": ["REQ-ASSIGN-007"]}
func (h *AdminAssignmentHandler) unassignStudent(w http.ResponseWriter, r *http.Request) {
	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	adminID := uuid.UUID(rawID)

	assignmentID, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	studentID, ok := parseUUIDParam(w, r, "studentId")
	if !ok {
		return
	}

	// Confirm the assignment exists.
	if _, err := h.svc.repo.GetAssignment(r.Context(), assignmentID); err != nil {
		if errors.Is(err, ErrAssignmentNotFound) {
			writeJSONError(w, http.StatusNotFound, "assignment not found")
			return
		}
		log.Printf("admin: unassignStudent: get assignment %s: %v", assignmentID, err)
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	deleted, reason, err := h.svc.UnassignStudent(r.Context(), assignmentID, studentID)
	if err != nil {
		switch {
		case errors.Is(err, ErrGenerationInProgress):
			writeJSONResponse(w, http.StatusConflict, map[string]interface{}{
				"error":   "GENERATION_IN_PROGRESS",
				"message": "course is currently generating content; unassign is not permitted",
			})
		case errors.Is(err, ErrCourseAlreadyActive):
			writeJSONResponse(w, http.StatusConflict, map[string]interface{}{
				"error":   "COURSE_ALREADY_ACTIVE",
				"message": "course is active or beyond; use the course archive/withdraw flow instead",
			})
		default:
			log.Printf("admin: unassignStudent %s from %s: %v", studentID, assignmentID, err)
			writeJSONError(w, http.StatusInternalServerError, "internal server error")
		}
		return
	}

	if !deleted {
		writeJSONResponse(w, http.StatusOK, map[string]interface{}{
			"deleted": false,
			"reason":  reason,
		})
		return
	}

	h.appendAudit(r.Context(), adminID, "assignment.student_unassigned", "course_assignment", &assignmentID,
		map[string]any{"student_id": studentID.String()})

	writeJSONResponse(w, http.StatusOK, map[string]interface{}{"deleted": true})
}

// appendAudit is a best-effort audit append: failures are logged but do not
// cause the HTTP handler to fail.  This mirrors the pattern in secrets_handler.go.
//
// @{"req": ["REQ-ASSIGN-001", "REQ-ASSIGN-002", "REQ-ASSIGN-007"]}
func (h *AdminAssignmentHandler) appendAudit(ctx context.Context, adminID uuid.UUID, action, targetType string, targetID *uuid.UUID, payload map[string]any) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		log.Printf("admin: audit begin (%s): %v", action, err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	entry := audit.Entry{
		AdminID:    adminID,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Payload:    payload,
	}
	if err := h.auditRepo.Append(ctx, tx, entry); err != nil {
		log.Printf("admin: audit append (%s): %v", action, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		log.Printf("admin: audit commit (%s): %v", action, err)
	}
}

// assignmentToResponse converts an AssignmentRow to the API response map.
//
// @{"req": ["REQ-ASSIGN-001", "REQ-ASSIGN-006"]}
func assignmentToResponse(a AssignmentRow) map[string]interface{} {
	params := a.Parameters
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	return map[string]interface{}{
		"id":         a.ID.String(),
		"admin_id":   a.AdminID.String(),
		"title":      a.Title,
		"topic":      a.Topic,
		"level":      a.Level,
		"parameters": params,
		"created_at": a.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		"updated_at": a.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// parseUUIDParam extracts and parses a UUID path parameter, writing a 400 on
// failure and returning false.
//
// @{"req": ["REQ-ASSIGN-001", "REQ-ASSIGN-006", "REQ-ASSIGN-007"]}
func parseUUIDParam(w http.ResponseWriter, r *http.Request, param string) (uuid.UUID, bool) {
	raw := chi.URLParam(r, param)
	id, err := uuid.Parse(raw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid "+param)
		return uuid.UUID{}, false
	}
	return id, true
}

// isJSONObject returns true when raw is a valid JSON object (starts with '{').
// A '{' first non-whitespace byte is the canonical indicator of a JSON object.
//
// @{"req": ["REQ-ASSIGN-001"]}
func isJSONObject(raw json.RawMessage) bool {
	for _, b := range raw {
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			continue
		}
		return b == '{'
	}
	return false
}
