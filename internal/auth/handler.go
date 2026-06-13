package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valory/valory/internal/security"
)

// @{"req": ["REQ-AUTH-001", "REQ-AUTH-002", "REQ-AUTH-003", "REQ-AUTH-006", "REQ-AUTH-009", "REQ-AUTH-010", "REQ-AUTH-012", "REQ-AUTH-015"]}
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

// Routes registers the login/logout auth endpoints. The 2FA verify/resend
// endpoints are registered separately via TwoFARoutes because they must be
// mounted in the public group (no auth middleware), not behind authMW.
//
// @{"req": ["REQ-AUTH-001", "REQ-AUTH-002", "REQ-AUTH-003", "REQ-AUTH-006", "REQ-AUTH-009", "REQ-AUTH-010"]}
func (h *Handler) Routes(r chi.Router) {
	r.Post("/login", h.login)
	r.Post("/logout", h.logout)
}

// TwoFARoutes registers the pre-session 2FA endpoints on the provided router.
// These must be mounted WITHOUT the auth middleware because the pending token
// (not a session token) is the credential on these routes.
//
// @{"req": ["REQ-AUTH-015", "REQ-AUTH-016", "REQ-AUTH-017", "REQ-AUTH-026"]}
func (h *Handler) TwoFARoutes(r chi.Router) {
	r.Post("/2fa/verify", h.verifyOTP)
	r.Post("/2fa/resend", h.resendOTP)
}

// @{"req": ["REQ-AUTH-001", "REQ-AUTH-002", "REQ-AUTH-003", "REQ-AUTH-006", "REQ-AUTH-009", "REQ-AUTH-015", "REQ-AUTH-020"]}
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

	result, err := h.svc.Login(r.Context(), req.Username, req.Password)
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
		// ErrNoEmailAddress: user has no email on file; cannot deliver OTP.
		// Return 503 with a user-specific message (REQ-AUTH-023).
		if errors.Is(err, ErrNoEmailAddress) {
			writeError(w, http.StatusServiceUnavailable, "your account has no email address on file — contact your administrator")
			return
		}
		// ErrOTPSendFailed: the pending row was created but SMTP delivery failed.
		// Return 503 so the client knows to retry via the resend endpoint rather
		// than treating this as an unrecoverable server error.
		if errors.Is(err, ErrOTPSendFailed) {
			writeError(w, http.StatusServiceUnavailable, "verification code could not be delivered — please retry or use the resend option")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Discriminate on the result type returned by Service.Login.
	if result.PendingTwoFactor != nil {
		// Phase 1 complete: return the pending token in the body (NOT a cookie).
		// Status 202 signals to the frontend that a second step is needed.
		writeJSON(w, http.StatusAccepted, map[string]interface{}{
			"two_factor_required": true,
			"pending_token":       result.PendingTwoFactor.PendingToken,
			"expires_at":          result.PendingTwoFactor.ExpiresAt.Format(time.RFC3339),
		})
		return
	}

	// Normal (non-2FA) path: issue session cookie and CSRF cookie.
	session := result.Session
	rawToken := result.RawToken

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

	// REQ-AUTH-013: include must_change_password so the frontend can interpose
	// the forced change-password screen immediately after login without an extra
	// round-trip. The value comes from the users row read during Login().
	resp := map[string]interface{}{
		"token":                rawToken,
		"role":                 session.Role,
		"expires_at":           session.ExpiresAt.Format(time.RFC3339),
		"must_change_password": session.MustChangePassword,
	}
	writeJSON(w, http.StatusOK, resp)
}

// verifyOTP handles POST /api/v1/auth/2fa/verify. It is a pre-session endpoint:
// the pending token (from the phase-1 login response body) is the credential,
// not a session token. On success it issues a full session with cookies.
//
// @{"req": ["REQ-AUTH-015", "REQ-AUTH-016", "REQ-AUTH-017", "REQ-AUTH-022", "REQ-AUTH-024", "REQ-AUTH-025"]}
func (h *Handler) verifyOTP(w http.ResponseWriter, r *http.Request) {
	// Return 404 when 2FA is globally disabled so the endpoint does not exist
	// from the client's perspective when the feature is off.
	if h.svc.twoFactor == nil || !isTwoFactorEnabled(h.svc.config) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	var req struct {
		PendingToken string `json:"pending_token"`
		OTP          string `json:"otp"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.PendingToken == "" || req.OTP == "" {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	rawToken, session, err := h.svc.twoFactor.VerifyOTP(r.Context(), req.PendingToken, req.OTP, h.sessionMaxDuration)
	if err != nil {
		var wrongErr *OTPWrongError
		if errors.As(err, &wrongErr) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":              "invalid or expired code",
				"attempts_remaining": wrongErr.AttemptsRemaining,
			})
			return
		}
		if errors.Is(err, ErrTooManyAttempts) {
			writeError(w, http.StatusUnauthorized, "too_many_attempts")
			return
		}
		if errors.Is(err, ErrOTPInvalid) {
			writeError(w, http.StatusUnauthorized, "invalid or expired code")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Success: issue the full session (same path as normal login).
	maxAge := int(h.sessionMaxDuration.Seconds())
	if maxAge <= 0 {
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

	csrfToken, err := security.GenerateCSRFToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	security.SetCSRFCookie(w, csrfToken)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":                rawToken,
		"role":                 session.Role,
		"expires_at":           session.ExpiresAt.Format(time.RFC3339),
		"must_change_password": session.MustChangePassword,
	})
}

// resendOTP handles POST /api/v1/auth/2fa/resend. Rate-limited (60 s throttle,
// 10 per 24 h). The pending token is the credential.
//
// @{"req": ["REQ-AUTH-026", "REQ-AUTH-022"]}
func (h *Handler) resendOTP(w http.ResponseWriter, r *http.Request) {
	if h.svc.twoFactor == nil || !isTwoFactorEnabled(h.svc.config) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	var req struct {
		PendingToken string `json:"pending_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.PendingToken == "" {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	expiresAt, _, err := h.svc.twoFactor.ResendOTP(r.Context(), req.PendingToken, h.sessionMaxDuration)
	if err != nil {
		var throttleErr *ResendThrottledError
		if errors.As(err, &throttleErr) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":               "resend_throttled",
				"resend_available_at": throttleErr.ResendAvailableAt.Format(time.RFC3339),
			})
			return
		}
		if errors.Is(err, ErrResendDailyCap) {
			writeError(w, http.StatusTooManyRequests, "resend_daily_cap_exceeded")
			return
		}
		if errors.Is(err, ErrOTPInvalid) {
			writeError(w, http.StatusUnauthorized, "invalid or expired code")
			return
		}
		if errors.Is(err, ErrNoEmailAddress) {
			writeError(w, http.StatusServiceUnavailable, "your account has no email address on file — contact your administrator")
			return
		}
		// Mailer errors (SMTP down, etc.).
		if h.svc.twoFactor != nil && !h.svc.twoFactor.mailer.IsConfigured() {
			writeError(w, http.StatusServiceUnavailable, "email not configured")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"expires_at":          expiresAt.Format(time.RFC3339),
		"resend_available_at": nil,
	})
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
// @{"req": ["REQ-AUTH-012", "REQ-AUTH-013", "REQ-PROFILE-014"]}
type sessionResponse struct {
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	Consented bool   `json:"consented"`
	ExpiresAt string `json:"expires_at"`
	// MustChangePassword lets the SPA interpose the forced change-password
	// screen on boot/reload, not only on the login POST response. Without it
	// the router guard (REQ-FEUSER-002) reads a stale false on every restore
	// and the interposition degrades to a reactive bounce off the backend 403.
	MustChangePassword bool `json:"must_change_password"`
	// OnboardingPrompted is the soft-gate flag set after the student has been
	// shown the onboarding nudge (either completed or skipped). The frontend
	// router guard reads this on every SPA boot/reload so it can redirect to
	// /onboarding exactly once without a second round-trip (REQ-PROFILE-014).
	OnboardingPrompted bool `json:"onboarding_prompted"`
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

	// Retrieve the username, must_change_password, and onboarding_prompted
	// flags with a single query. The middleware already verified the session,
	// so this row is guaranteed to exist. Both flags come from users (the
	// authoritative source) so the SPA boot/reload path always sees current
	// state without a separate round-trip.
	var username string
	var mustChangePassword bool
	var onboardingPrompted bool
	if err := h.pool.QueryRow(r.Context(),
		`SELECT username, must_change_password, onboarding_prompted FROM users WHERE id = $1`, userID,
	).Scan(&username, &mustChangePassword, &onboardingPrompted); err != nil {
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
		UserID:             uuidStr,
		Username:           username,
		Role:               role,
		Consented:          consented,
		ExpiresAt:          session.ExpiresAt.Format(time.RFC3339),
		MustChangePassword: mustChangePassword,
		OnboardingPrompted: onboardingPrompted,
	})
}

// ResetUserTwoFactor handles DELETE /api/v1/admin/users/{id}/2fa/reset.
// It clears any pending-2FA state for the specified user. Requires admin role
// (enforced by the router group in main.go).
//
// @{"req": ["REQ-AUTH-019", "REQ-AUTH-022"]}
func (h *Handler) ResetUserTwoFactor(w http.ResponseWriter, r *http.Request) {
	if h.svc.twoFactor == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	adminRawID, ok := UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	adminID := uuid.UUID(adminRawID)

	userIDStr := chi.URLParam(r, "id")
	targetUUID, err := uuid.Parse(userIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	// Verify the target user exists (return 404 if not).
	var exists bool
	if dbErr := h.pool.QueryRow(r.Context(),
		`SELECT TRUE FROM users WHERE id = $1`, targetUUID,
	).Scan(&exists); dbErr != nil || !exists {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	if err := h.svc.twoFactor.ResetUserTwoFactor(r.Context(), adminID, [16]byte(targetUUID)); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	w.WriteHeader(http.StatusNoContent)
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
