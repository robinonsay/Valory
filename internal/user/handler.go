package user

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/security"
)

// userLister is the subset of Service used by the list endpoint. Keeping it as
// a narrow interface lets tests supply a lightweight stub without a real DB.
//
// @{"req": ["REQ-USER-001", "REQ-USER-002"]}
type userLister interface {
	ListUsers(ctx context.Context, role string, limit int) ([]UserRow, error)
}

// @{"req": ["REQ-USER-001", "REQ-USER-002", "REQ-USER-003", "REQ-USER-005", "REQ-USER-006", "REQ-USER-007", "REQ-SECURITY-005", "REQ-USER-008", "REQ-USER-009", "REQ-USER-010", "REQ-USER-012", "REQ-USER-014", "REQ-USER-015", "REQ-USER-016"]}
type Handler struct {
	svc    *Service
	lister userLister
}

// @{"req": ["REQ-USER-001", "REQ-USER-002", "REQ-USER-003", "REQ-USER-005", "REQ-USER-006", "REQ-USER-007", "REQ-SECURITY-005", "REQ-USER-008", "REQ-USER-009", "REQ-USER-010", "REQ-USER-012", "REQ-USER-014", "REQ-USER-015", "REQ-USER-016"]}
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc, lister: svc}
}

// AdminRoutes registers admin-only routes (caller applies RequireRole("admin")).
// @{"req": ["REQ-USER-001", "REQ-USER-002", "REQ-USER-003", "REQ-USER-007", "REQ-USER-012"]}
func (h *Handler) AdminRoutes(r chi.Router) {
	r.Get("/", h.listUsers)
	r.Post("/", h.createUser)
	r.Patch("/{id}", h.modifyUser)
	r.Post("/{id}/deactivate", h.deactivateUser)
	r.Post("/{id}/activate", h.activateUser)
	r.Delete("/{id}", h.deleteStudent)
	// Resend welcome email with a freshly-generated temp password.
	r.Post("/{id}/resend-welcome", h.resendWelcome)
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

// MeRoutes registers the current-user and change-password endpoints (caller has authMW).
// @{"req": ["REQ-USER-001", "REQ-USER-014", "REQ-USER-015", "REQ-USER-016"]}
func (h *Handler) MeRoutes(r chi.Router) {
	r.Get("/me", h.getCurrentUser)
	r.Post("/me/password", h.changePassword)
}

// @{"req": ["REQ-USER-001", "REQ-USER-002"]}
func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	role := r.URL.Query().Get("role")
	if role != "" && role != "student" && role != "admin" {
		writeError(w, http.StatusBadRequest, "role must be 'student', 'admin', or omitted")
		return
	}

	limit := 50
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		parsed, err := strconv.Atoi(limitStr)
		if err != nil || parsed < 1 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = parsed
	}
	if limit > 100 {
		limit = 100
	}

	users, err := h.lister.ListUsers(r.Context(), role, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	type userItem struct {
		ID        string  `json:"id"`
		Username  string  `json:"username"`
		Role      string  `json:"role"`
		IsActive  bool    `json:"is_active"`
		Email     *string `json:"email,omitempty"`
		CreatedAt string  `json:"created_at"`
	}

	items := make([]userItem, 0, len(users))
	for _, u := range users {
		items = append(items, userItem{
			ID:        u.ID.String(),
			Username:  u.Username,
			Role:      u.Role,
			IsActive:  u.IsActive,
			Email:     u.Email,
			CreatedAt: u.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"users": items})
}

// createUser handles POST /api/v1/users (admin-only group).
// It accepts either an explicit password (existing behaviour) or
// generate_password:true (Sprint 20 temp-password path). Exactly one must be
// supplied; supplying both or neither is a 400 error.
//
// @{"req": ["REQ-USER-001", "REQ-USER-008", "REQ-USER-009", "REQ-USER-010", "REQ-USER-011", "REQ-USER-017"]}
func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	adminID := uuid.UUID(rawID)

	var req struct {
		Username         string  `json:"username"`
		Email            *string `json:"email"`
		Role             string  `json:"role"`
		Password         string  `json:"password"`
		GeneratePassword bool    `json:"generate_password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	if req.Username == "" || req.Role == "" {
		writeError(w, http.StatusBadRequest, "username and role are required")
		return
	}

	if req.Email != nil && *req.Email == "" {
		req.Email = nil
	}

	// Exactly one of password or generate_password must be provided.
	hasPassword := req.Password != ""
	if hasPassword && req.GeneratePassword {
		writeError(w, http.StatusBadRequest, "provide either password or generate_password, not both")
		return
	}
	if !hasPassword && !req.GeneratePassword {
		writeError(w, http.StatusBadRequest, "username, role, and one of password or generate_password are required")
		return
	}

	if req.GeneratePassword {
		user, result, err := h.svc.CreateUserWithTempPassword(r.Context(), adminID, req.Username, req.Email, req.Role)
		if err != nil {
			if errors.Is(err, ErrDuplicateUsername) {
				writeError(w, http.StatusConflict, "username already taken")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}

		resp := userToResponse(user)
		resp["email_sent"] = result.EmailSent
		if !result.EmailSent {
			// One-time admin display: only surfaced when email delivery did not
			// occur (REQ-FEADMIN-612/613).
			resp["temp_password"] = result.TempPassword
		}
		writeJSON(w, http.StatusCreated, resp)
		return
	}

	// Explicit password path (existing behaviour, unchanged).
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

// resendWelcome handles POST /api/v1/users/{id}/resend-welcome (admin-only group).
// Regenerates a temporary password, overwrites the old one, and re-sends the
// welcome email. The old password is immediately invalid (REQ-USER-013).
//
// @{"req": ["REQ-USER-012", "REQ-USER-013", "REQ-USER-018"]}
func (h *Handler) resendWelcome(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	adminID := uuid.UUID(rawID)

	idStr := chi.URLParam(r, "id")
	targetID, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	result, err := h.svc.ResendWelcome(r.Context(), adminID, targetID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	resp := map[string]interface{}{
		"email_sent": result.EmailSent,
	}
	if !result.EmailSent {
		resp["temp_password"] = result.TempPassword
	}
	writeJSON(w, http.StatusOK, resp)
}

// changePassword handles POST /api/v1/users/me/password (authenticated, any role).
// Requires the caller to supply their current password. On success, clears the
// must_change_password flag and invalidates all other sessions for the user.
//
// @{"req": ["REQ-USER-014", "REQ-USER-015", "REQ-USER-016", "REQ-USER-019"]}
func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	userID := uuid.UUID(rawID)

	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	if req.CurrentPassword == "" || req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "current_password and new_password are required")
		return
	}

	// Extract the current session token from the request so that ChangePassword
	// can preserve it while deleting all other sessions (REQ-USER-016).
	currentTokenHash := currentSessionTokenHash(r)

	if err := h.svc.ChangePassword(r.Context(), userID, req.CurrentPassword, req.NewPassword, currentTokenHash); err != nil {
		if errors.Is(err, ErrWrongPassword) {
			writeError(w, http.StatusForbidden, "current password is incorrect")
			return
		}
		if errors.Is(err, ErrPasswordTooShort) {
			writeError(w, http.StatusBadRequest, "new password must be at least 8 characters")
			return
		}
		if errors.Is(err, ErrUserNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// currentSessionTokenHash extracts the raw session token from the request
// (Bearer header preferred, __Host-session cookie fallback) and returns its
// SHA-256 hash. The result is used to identify the current session for
// preservation during ChangePassword (REQ-USER-016).
//
// @{"req": ["REQ-USER-016"]}
func currentSessionTokenHash(r *http.Request) string {
	if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
		return auth.HashToken(strings.TrimPrefix(authHeader, "Bearer "))
	}
	if cookie, err := r.Cookie("__Host-session"); err == nil && cookie.Value != "" {
		return auth.HashToken(cookie.Value)
	}
	return ""
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

// @{"req": ["REQ-USER-001"]}
func (h *Handler) getCurrentUser(w http.ResponseWriter, r *http.Request) {
	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	userID := uuid.UUID(rawID)
	user, err := h.svc.GetUserByID(r.Context(), userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	resp := map[string]interface{}{
		"id":                   user.ID.String(),
		"username":             user.Username,
		"role":                 user.Role,
		"is_active":            user.IsActive,
		"must_change_password": user.MustChangePassword,
	}

	writeJSON(w, http.StatusOK, resp)
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
		"id":                   user.ID.String(),
		"username":             user.Username,
		"role":                 user.Role,
		"is_active":            user.IsActive,
		"must_change_password": user.MustChangePassword,
		"created_at":           user.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		"updated_at":           user.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
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
