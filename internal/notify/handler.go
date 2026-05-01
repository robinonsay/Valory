// handler.go — HTTP handler for the notify module.
// Exposes notification listing and mark-read endpoints.
package notify

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/valory/valory/internal/auth"
)

// NotifyHandler serves student notification endpoints.
type NotifyHandler struct {
	repo *NotificationRepository
}

// @{"req": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
func NewNotifyHandler(repo *NotificationRepository) *NotifyHandler {
	return &NotifyHandler{repo: repo}
}

// Routes mounts the notification endpoints under an already-authenticated router.
//
// @{"req": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
func (h *NotifyHandler) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/{id}/read", h.markRead)
}

// list returns the student's notifications. Supports ?unread=true and ?limit=N
// query parameters.
//
// @{"req": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
func (h *NotifyHandler) list(w http.ResponseWriter, r *http.Request) {
	rawUserID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	studentID := uuid.UUID(rawUserID)

	unreadOnly := r.URL.Query().Get("unread") == "true"

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	var beforeID *uuid.UUID
	if b := r.URL.Query().Get("before"); b != "" {
		if id, err := uuid.Parse(b); err == nil {
			beforeID = &id
		}
	}

	rows, err := h.repo.List(r.Context(), studentID, unreadOnly, limit, beforeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	type notifResponse struct {
		ID        uuid.UUID  `json:"id"`
		Type      string     `json:"type"`
		Message   string     `json:"message"`
		ReadAt    *string    `json:"read_at,omitempty"`
		CreatedAt string     `json:"created_at"`
	}

	resp := make([]notifResponse, 0, len(rows))
	for _, n := range rows {
		item := notifResponse{
			ID:        n.ID,
			Type:      n.Type,
			Message:   n.Message,
			CreatedAt: n.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
		}
		if n.ReadAt != nil {
			s := n.ReadAt.Format("2006-01-02T15:04:05.999999999Z07:00")
			item.ReadAt = &s
		}
		resp = append(resp, item)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"notifications": resp,
	})
}

// markRead marks a single notification as read for the authenticated student.
// Returns 404 if not found, 403 if it belongs to another student.
// Idempotent: marking an already-read notification returns 200.
//
// @{"req": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
func (h *NotifyHandler) markRead(w http.ResponseWriter, r *http.Request) {
	rawUserID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	studentID := uuid.UUID(rawUserID)

	notifID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid notification id")
		return
	}

	row, err := h.repo.MarkRead(r.Context(), notifID, studentID)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "notification not found")
		return
	}
	if errors.Is(err, ErrForbidden) {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "access forbidden")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	type notifResponse struct {
		ID        uuid.UUID `json:"id"`
		Type      string    `json:"type"`
		Message   string    `json:"message"`
		ReadAt    *string   `json:"read_at,omitempty"`
		CreatedAt string    `json:"created_at"`
	}

	resp := notifResponse{
		ID:        row.ID,
		Type:      row.Type,
		Message:   row.Message,
		CreatedAt: row.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
	}
	if row.ReadAt != nil {
		s := row.ReadAt.Format("2006-01-02T15:04:05.999999999Z07:00")
		resp.ReadAt = &s
	}

	writeJSON(w, http.StatusOK, resp)
}

// @{"req": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// @{"req": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}
