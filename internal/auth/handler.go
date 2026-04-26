package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// @{"req": ["REQ-AUTH-001", "REQ-AUTH-002", "REQ-AUTH-003", "REQ-AUTH-006"]}
type Handler struct {
	svc *Service
}

// @{"req": ["REQ-AUTH-001", "REQ-AUTH-002", "REQ-AUTH-003", "REQ-AUTH-006"]}
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// @{"req": ["REQ-AUTH-001", "REQ-AUTH-002", "REQ-AUTH-003", "REQ-AUTH-006"]}
func (h *Handler) Routes(r chi.Router) {
	r.Post("/login", h.login)
	r.Post("/logout", h.logout)
}

// @{"req": ["REQ-AUTH-001", "REQ-AUTH-002", "REQ-AUTH-003", "REQ-AUTH-006"]}
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}

	rawToken, session, err := h.svc.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			writeError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		// ErrAccountDisabled is mapped to "invalid credentials" to avoid confirming
		// that a given username exists in the system.
		if errors.Is(err, ErrAccountDisabled) {
			writeError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		// ErrAccountLocked is mapped to "invalid credentials" for the same reason as
		// ErrAccountDisabled: revealing a locked status confirms the username exists.
		if errors.Is(err, ErrAccountLocked) {
			writeError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	resp := map[string]interface{}{
		"token":      rawToken,
		"role":       session.Role,
		"expires_at": session.ExpiresAt.Format(time.RFC3339),
	}
	writeJSON(w, http.StatusOK, resp)
}

// @{"req": ["REQ-AUTH-005"]}
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || parts[0] != "Bearer" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	rawToken := parts[1]
	tokenHash := HashToken(rawToken)

	if err := h.svc.Logout(r.Context(), tokenHash); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// @{"req": ["REQ-AUTH-001", "REQ-AUTH-002", "REQ-AUTH-003", "REQ-AUTH-005", "REQ-AUTH-006"]}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// @{"req": ["REQ-AUTH-001", "REQ-AUTH-002", "REQ-AUTH-003", "REQ-AUTH-005", "REQ-AUTH-006"]}
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
