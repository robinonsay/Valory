package audit

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// @{"req": ["REQ-AUDIT-001", "REQ-AUDIT-002"]}
type Handler struct {
	repo *Repository
}

// @{"req": ["REQ-AUDIT-001", "REQ-AUDIT-002"]}
func NewHandler(repo *Repository) *Handler {
	return &Handler{repo: repo}
}

// @{"req": ["REQ-AUDIT-001", "REQ-AUDIT-002"]}
func (h *Handler) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Get("/verify", h.verify)
}

// @{"req": ["REQ-AUDIT-001"]}
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		var err error
		if limit, err = strconv.Atoi(l); err != nil {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 200")
			return
		}
	}

	if limit < 1 || limit > 200 {
		writeError(w, http.StatusBadRequest, "limit must be between 1 and 200")
		return
	}

	before := int64(0)
	if b := r.URL.Query().Get("before"); b != "" {
		var err error
		if before, err = strconv.ParseInt(b, 10, 64); err != nil {
			writeError(w, http.StatusBadRequest, "before must be a valid integer")
			return
		}
	}

	rows, err := h.repo.ListPaginated(r.Context(), limit, before)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	nextBefore := int64(0)
	if len(rows) > 0 {
		nextBefore = rows[len(rows)-1].ID
	}

	entries := make([]map[string]any, len(rows))
	for i, row := range rows {
		var payload map[string]any
		if err := json.Unmarshal([]byte(row.PayloadJSON), &payload); err != nil {
			payload = map[string]any{}
		}

		entries[i] = map[string]any{
			"id":          row.ID,
			"admin_id":    row.AdminID.String(),
			"action":      row.Action,
			"target_type": row.TargetType,
			"target_id":   row.TargetID,
			"payload":     payload,
			"prev_hash":   row.PrevHash,
			"entry_hash":  row.EntryHash,
			"created_at":  row.CreatedAt.Format(time.RFC3339),
		}
	}

	resp := map[string]any{
		"entries":     entries,
		"next_before": nextBefore,
	}
	writeJSON(w, http.StatusOK, resp)
}

// @{"req": ["REQ-AUDIT-002"]}
func (h *Handler) verify(w http.ResponseWriter, r *http.Request) {
	v := NewChainVerifier()
	err := h.repo.StreamAll(r.Context(), func(row AuditRow) error {
		v.Push(row)
		return nil
	})

	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	valid, firstBrokenID := v.Done()

	resp := map[string]any{"valid": valid}
	if !valid {
		resp["first_broken_id"] = firstBrokenID
	}

	writeJSON(w, http.StatusOK, resp)
}

// @{"req": ["REQ-AUDIT-001", "REQ-AUDIT-002"]}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// @{"req": ["REQ-AUDIT-001", "REQ-AUDIT-002"]}
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
