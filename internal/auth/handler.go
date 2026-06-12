package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valory/valory/internal/security"
)

// @{"req": ["REQ-AUTH-001", "REQ-AUTH-002", "REQ-AUTH-003", "REQ-AUTH-006", "REQ-AUTH-009", "REQ-AUTH-010", "REQ-AUTH-012"]}
type Handler struct {
	svc                *Service
	repo               *Repository
	pool               *pgxpool.Pool
	sessionMaxDuration time.Duration
	consentProvider    ConsentVersionProvider
}

// @{"req": ["REQ-AUTH-001", "REQ-AUTH-002", "REQ-AUTH-003", "REQ-AUTH-006", "REQ-AUTH-009", "REQ-AUTH-010", "REQ-AUTH-012"]}
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// NewHandlerFull constructs a Handler with the additional dependencies required
// by the GetSession endpoint (REQ-AUTH-012). The login handler uses
// sessionMaxDuration to set the cookie Max-Age (REQ-AUTH-009).
//
// @{"req": ["REQ-AUTH-009", "REQ-AUTH-010", "REQ-AUTH-012"]}
func NewHandlerFull(
	svc *Service,
	repo *Repository,
	pool *pgxpool.Pool,
	sessionMaxDuration time.Duration,
	consentProvider ConsentVersionProvider,
) *Handler {
	return &Handler{
		svc:                svc,
		repo:               repo,
		pool:               pool,
		sessionMaxDuration: sessionMaxDuration,
		consentProvider:    consentProvider,
	}
}

// @{"req": ["REQ-AUTH-001", "REQ-AUTH-002", "REQ-AUTH-003", "REQ-AUTH-006", "REQ-AUTH-009", "REQ-AUTH-010"]}
func (h *Handler) Routes(r chi.Router) {
	r.Post("/login", h.login)
	r.Post("/logout", h.logout)
}

// @{"req": ["REQ-AUTH-001", "REQ-AUTH-002", "REQ-AUTH-003", "REQ-AUTH-006", "REQ-AUTH-009"]}
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

	// REQ-AUTH-009: set the session cookie so the browser re-authenticates on
	// subsequent requests without needing an Authorization header. The __Host-
	// prefix enforces Secure + Path=/ + no Domain (HTTPS-only, host-bound).
	maxAge := int(h.sessionMaxDuration.Seconds())
	if maxAge <= 0 {
		// Fall back to 24 hours if sessionMaxDuration was not injected (e.g. tests
		// that use NewHandler rather than NewHandlerFull).
		maxAge = 86400
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "__Host-session",
		Value:    rawToken,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})

	// @{"req": ["REQ-SECURITY-004"]}
	// Issue a CSRF token as a Secure, SameSite=Strict cookie so the browser
	// attaches it automatically and the frontend can echo it in X-CSRF-Token.
	csrfToken, err := security.GenerateCSRFToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	security.SetCSRFCookie(w, csrfToken)

	resp := map[string]interface{}{
		"token":      rawToken,
		"role":       session.Role,
		"expires_at": session.ExpiresAt.Format(time.RFC3339),
	}
	writeJSON(w, http.StatusOK, resp)
}

// @{"req": ["REQ-AUTH-005", "REQ-AUTH-010"]}
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	// REQ-AUTH-010: logout can be reached via cookie auth after a page refresh,
	// so CSRF protection is mandatory here (a cross-site POST could sign the
	// user out otherwise). Inline check mirrors security.CSRFMiddleware.
	csrfCookie, err := r.Cookie("__Host-csrf")
	if err == nil && csrfCookie.Value != "" {
		// Only enforce CSRF when the CSRF cookie is present — if no CSRF cookie
		// exists (e.g. pre-CSRF-era bearer-only clients), skip the check so
		// older clients are not broken. All browser clients receive the CSRF
		// cookie at login time.
		csrfHeader := r.Header.Get("X-CSRF-Token")
		if csrfHeader == "" || csrfHeader != csrfCookie.Value {
			writeError(w, http.StatusForbidden, "csrf_token_mismatch")
			return
		}
	}

	// Prefer the Authorization header; fall back to the session cookie so that
	// cookie-only browser clients (post-refresh) can also log out.
	var rawToken string
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		rawToken = strings.TrimPrefix(authHeader, "Bearer ")
	} else {
		sessionCookie, cookieErr := r.Cookie("__Host-session")
		if cookieErr != nil || sessionCookie.Value == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		rawToken = sessionCookie.Value
	}

	tokenHash := HashToken(rawToken)
	if err := h.svc.Logout(r.Context(), tokenHash); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// REQ-AUTH-010: clear the session cookie in the browser.
	http.SetCookie(w, &http.Cookie{
		Name:     "__Host-session",
		Value:    "",
		Path:     "/",
		MaxAge:   0,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})

	w.WriteHeader(http.StatusNoContent)
}

// sessionResponse is the wire shape returned by GET /api/v1/auth/session.
//
// @{"req": ["REQ-AUTH-012"]}
type sessionResponse struct {
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	Consented bool   `json:"consented"`
	ExpiresAt string `json:"expires_at"`
}

// GetSession returns the caller's current session state for SPA boot restore
// (REQ-AUTH-012). The auth middleware has already validated the session token
// (from Bearer header or __Host-session cookie) before this handler runs.
//
// Finding-1 fix (Sprint 13 SQE): after a browser restart the __Host-session
// cookie persists (24h Max-Age) but the __Host-csrf cookie does not (session
// cookie, cleared on browser exit). Without re-issuing the CSRF cookie here,
// every subsequent mutation returns 403 csrf_token_missing. The fix mirrors
// what login does: generate a fresh CSRF token and set it via SetCSRFCookie.
//
// @{"req": ["REQ-AUTH-012", "REQ-SECURITY-004"]}
func (h *Handler) GetSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	role, _ := RoleFromContext(r.Context())

	// Retrieve the username with a single query. The middleware already verified
	// the session, so this row is guaranteed to exist.
	var username string
	if err := h.pool.QueryRow(r.Context(),
		`SELECT username FROM users WHERE id = $1`, userID,
	).Scan(&username); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Retrieve session expiry so the frontend can compute isExpired.
	tokenHash := tokenHashFromRequest(r)
	session, err := h.repo.GetSessionByTokenHash(r.Context(), tokenHash)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Determine consent status using the same logic as the auth middleware.
	// Admins are always considered consented (REQ-SECURITY-005).
	consented := role == "admin"
	if !consented && h.consentProvider != nil {
		currentVersion := h.consentProvider.GetString("consent_version")
		var storedVersion string
		_ = h.pool.QueryRow(r.Context(),
			`SELECT consent_version FROM student_consent WHERE student_id = $1`,
			userID,
		).Scan(&storedVersion)
		consented = !semverLess(storedVersion, currentVersion)
	} else if !consented {
		// No consent provider — treat as consented (same as authOnlyMW behaviour).
		consented = true
	}

	// Format user_id as a hyphenated UUID string for the frontend.
	uuidStr := formatUUID(userID)

	// Re-issue the CSRF cookie so that browser-restart sessions (where
	// __Host-session survives but the session-only __Host-csrf does not) can
	// immediately perform mutations without getting 403 csrf_token_missing.
	csrfToken, err := security.GenerateCSRFToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	security.SetCSRFCookie(w, csrfToken)

	writeJSON(w, http.StatusOK, sessionResponse{
		UserID:    uuidStr,
		Username:  username,
		Role:      role,
		Consented: consented,
		ExpiresAt: session.ExpiresAt.Format(time.RFC3339),
	})
}

// tokenHashFromRequest extracts the raw token from the request (bearer header
// preferred, session cookie fallback) and returns its hash. Called only from
// GetSession after the middleware has already validated that exactly one source
// is present.
//
// @{"req": ["REQ-AUTH-012"]}
func tokenHashFromRequest(r *http.Request) string {
	if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
		return HashToken(strings.TrimPrefix(authHeader, "Bearer "))
	}
	if cookie, err := r.Cookie("__Host-session"); err == nil && cookie.Value != "" {
		return HashToken(cookie.Value)
	}
	return ""
}

// formatUUID converts a 16-byte UUID to the standard hyphenated string form
// (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx).
//
// @{"req": ["REQ-AUTH-012"]}
func formatUUID(b [16]byte) string {
	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16],
	)
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
