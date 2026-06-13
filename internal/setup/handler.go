package setup

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Handler wires the setup Service to HTTP. Both endpoints are public (no auth,
// no CSRF) — they must be reachable before any user account exists.
//
// @{"req": ["REQ-SETUP-001", "REQ-SETUP-003", "REQ-SETUP-011"]}
type Handler struct {
	svc *Service
}

// @{"req": ["REQ-SETUP-001", "REQ-SETUP-003", "REQ-SETUP-011"]}
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Routes registers the two public setup endpoints on the provided router.
// The router must already be a public (unauthenticated, no-CSRF) group.
//
// @{"req": ["REQ-SETUP-001", "REQ-SETUP-003", "REQ-SETUP-011"]}
func (h *Handler) Routes(r chi.Router) {
	r.Get("/status", h.handleGetStatus)
	r.Post("/", h.handlePost)
}

// handleGetStatus serves GET /api/v1/setup/status.
// Returns 200 {"needs_setup": bool} always; 500 on DB error.
//
// @{"req": ["REQ-SETUP-001", "REQ-SETUP-002"]}
func (h *Handler) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	needsSetup, err := h.svc.NeedsSetup(r.Context())
	if err != nil {
		writeSetupError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeSetupJSON(w, http.StatusOK, map[string]bool{"needs_setup": needsSetup})
}

// setupRequest is the decoded POST body for the create-first-admin endpoint.
type setupRequest struct {
	Username string  `json:"username"`
	Email    *string `json:"email,omitempty"`
	Password string  `json:"password"`
}

// handlePost serves POST /api/v1/setup.
// On success returns 201 {"username": "...", "role": "admin"}.
// The raw password is never included in any response or log line.
//
// Error mapping (per SDD-SETUP-001 §5.6):
//
//	ErrUsernameMissing    → 400 "username_required"
//	ErrPasswordTooShort   → 400 "password_too_short"
//	JSON decode failure   → 400 "invalid_json"
//	ErrAlreadyConfigured  → 409 "already_configured"
//	anything else         → 500 "internal_error"
//
// @{"req": ["REQ-SETUP-003", "REQ-SETUP-004", "REQ-SETUP-005", "REQ-SETUP-006", "REQ-SETUP-007", "REQ-SETUP-008", "REQ-SETUP-009", "REQ-SETUP-010", "REQ-SETUP-011"]}
func (h *Handler) handlePost(w http.ResponseWriter, r *http.Request) {
	// Bound the request body before decoding. This endpoint is PUBLIC and
	// unauthenticated, so without a limit an attacker could stream an arbitrarily
	// large body and exhaust server memory before any account exists. 1 KiB matches
	// the cap used on the other public POSTs (auth login, password reset).
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)

	var req setupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSetupError(w, http.StatusBadRequest, "invalid_json")
		return
	}

	// Normalise optional email: treat empty/whitespace-only string the same as
	// omitted (store NULL). The design doc says store absent/empty email as NULL.
	email := req.Email
	if email != nil && strings.TrimSpace(*email) == "" {
		email = nil
	}

	result, err := h.svc.CreateFirstAdmin(r.Context(), req.Username, email, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, ErrUsernameMissing):
			writeSetupError(w, http.StatusBadRequest, "username_required")
		case errors.Is(err, ErrPasswordTooShort):
			writeSetupError(w, http.StatusBadRequest, "password_too_short")
		case errors.Is(err, ErrAlreadyConfigured):
			writeSetupError(w, http.StatusConflict, "already_configured")
		default:
			writeSetupError(w, http.StatusInternalServerError, "internal_error")
		}
		return
	}

	// Return only username and role — never id, email, or password_hash.
	writeSetupJSON(w, http.StatusCreated, map[string]string{
		"username": result.Username,
		"role":     result.Role,
	})
}

// writeSetupJSON writes a JSON response with the given status code.
//
// @{"req": ["REQ-SETUP-001", "REQ-SETUP-003"]}
func writeSetupJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

// writeSetupError writes a {"error": "<code>"} JSON error response.
//
// @{"req": ["REQ-SETUP-001", "REQ-SETUP-003"]}
func writeSetupError(w http.ResponseWriter, status int, code string) {
	writeSetupJSON(w, status, map[string]string{"error": code})
}
