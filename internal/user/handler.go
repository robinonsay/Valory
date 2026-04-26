package user

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/security"
)

// @{"req": ["REQ-USER-001", "REQ-USER-002", "REQ-USER-003", "REQ-USER-005", "REQ-USER-006", "REQ-USER-007", "REQ-SECURITY-005"]}
type Handler struct {
	svc *Service
}

// @{"req": ["REQ-USER-001", "REQ-USER-002", "REQ-USER-003", "REQ-USER-005", "REQ-USER-006", "REQ-USER-007", "REQ-SECURITY-005"]}
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// AdminRoutes registers admin-only routes (caller applies RequireRole("admin")).
// @{"req": ["REQ-USER-001", "REQ-USER-002", "REQ-USER-003", "REQ-USER-007"]}
func (h *Handler) AdminRoutes(r chi.Router) {
	r.Post("/", h.createUser)
	r.Patch("/{id}", h.modifyUser)
	r.Post("/{id}/deactivate", h.deactivateUser)
	r.Post("/{id}/activate", h.activateUser)
	r.Delete("/{id}", h.deleteStudent)
}

// PublicRoutes registers unauthenticated password-reset routes.
// @{"req": ["REQ-USER-005", "REQ-USER-006"]}
func (h *Handler) PublicRoutes(r chi.Router) {
	r.Post("/request", h.requestPasswordReset)
	r.Post("/confirm", h.confirmPasswordReset)
}

// StudentRoutes registers the consent route (caller has authMW but not RequireRole).
// @{"req": ["REQ-SECURITY-005"]}
func (h *Handler) StudentRoutes(r chi.Router) {
	r.Post("/", h.recordConsent)
}

// @{"req": ["REQ-USER-001"]}
func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	adminID := uuid.UUID(rawID)

	var req struct {
		Username string `json:"username"`
		Email    *string `json:"email"`
		Role     string `json:"role"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	if req.Username == "" || req.Role == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username, role, and password are required")
		return
	}

	if req.Email != nil && *req.Email == "" {
		req.Email = nil
	}

	user, err := h.svc.CreateUser(r.Context(), adminID, req.Username, req.Email, req.Role, req.Password)
	if err != nil {
		if errors.Is(err, ErrDuplicateUsername) {
			writeError(w, http.StatusConflict, "username already taken")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	resp := userToResponse(user)
	writeJSON(w, http.StatusCreated, resp)
}

// @{"req": ["REQ-USER-002"]}
func (h *Handler) modifyUser(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	adminID := uuid.UUID(rawID)

	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	var req struct {
		Username *string `json:"username"`
		Email    *string `json:"email"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	if req.Username != nil && *req.Username == "" {
		req.Username = nil
	}
	if req.Email != nil && *req.Email == "" {
		req.Email = nil
	}

	fields := UpdateFields{
		Username: req.Username,
		Email:    req.Email,
	}

	user, err := h.svc.ModifyUser(r.Context(), adminID, id, fields)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		if errors.Is(err, ErrDuplicateUsername) {
			writeError(w, http.StatusConflict, "username already taken")
			return
		}
		if errors.Is(err, ErrNoFieldsToUpdate) {
			writeError(w, http.StatusBadRequest, "no fields to update")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	resp := userToResponse(user)
	writeJSON(w, http.StatusOK, resp)
}

// @{"req": ["REQ-USER-003"]}
func (h *Handler) deactivateUser(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	adminID := uuid.UUID(rawID)

	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	err = h.svc.DeactivateUser(r.Context(), adminID, id)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// @{"req": ["REQ-USER-003"]}
func (h *Handler) activateUser(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	adminID := uuid.UUID(rawID)

	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	err = h.svc.ActivateUser(r.Context(), adminID, id)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// @{"req": ["REQ-USER-007"]}
func (h *Handler) deleteStudent(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	adminID := uuid.UUID(rawID)

	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	err = h.svc.DeleteStudent(r.Context(), adminID, id)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		if errors.Is(err, ErrNotAStudent) {
			writeError(w, http.StatusUnprocessableEntity, "target account is not a student")
			return
		}
		if errors.Is(err, ErrDeletionBlocked) {
			writeError(w, http.StatusConflict, "agent operations could not be terminated")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// @{"req": ["REQ-USER-005"]}
func (h *Handler) requestPasswordReset(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	var req struct {
		Username string `json:"username"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	if req.Username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}

	err := h.svc.RequestPasswordReset(r.Context(), req.Username)
	if err != nil {
		if errors.Is(err, security.ErrRateLimitExceeded) {
			writeError(w, http.StatusTooManyRequests, "too many password reset requests")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// @{"req": ["REQ-USER-005", "REQ-USER-006"]}
func (h *Handler) confirmPasswordReset(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	var req struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	if req.Token == "" || req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "token and new_password are required")
		return
	}

	err := h.svc.ConfirmPasswordReset(r.Context(), req.Token, req.NewPassword)
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			writeError(w, http.StatusBadRequest, "invalid or expired token")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// @{"req": ["REQ-SECURITY-005"]}
func (h *Handler) recordConsent(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	studentID := uuid.UUID(rawID)

	var req struct {
		Version string `json:"version"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	if req.Version == "" {
		writeError(w, http.StatusBadRequest, "version is required")
		return
	}

	err := h.svc.RecordConsent(r.Context(), studentID, req.Version)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// @{"req": ["REQ-USER-001", "REQ-USER-002"]}
func userToResponse(user UserRow) map[string]interface{} {
	resp := map[string]interface{}{
		"id":         user.ID.String(),
		"username":   user.Username,
		"role":       user.Role,
		"is_active":  user.IsActive,
		"created_at": user.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		"updated_at": user.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if user.Email != nil {
		resp["email"] = *user.Email
	}
	return resp
}

// writeJSON and writeError below are package-local copies of identically-named
// helpers in internal/auth/handler.go. They cannot be shared across packages
// because both are unexported. Extraction to a shared internal/httputil package
// is deferred to a future refactor sprint.
//
// @{"req": ["REQ-USER-001", "REQ-USER-002", "REQ-USER-003", "REQ-USER-005", "REQ-USER-006", "REQ-USER-007", "REQ-SECURITY-005"]}
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// @{"req": ["REQ-USER-001", "REQ-USER-002", "REQ-USER-003", "REQ-USER-005", "REQ-USER-006", "REQ-USER-007", "REQ-SECURITY-005"]}
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
