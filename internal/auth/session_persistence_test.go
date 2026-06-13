package auth

// session_persistence_test.go — unit tests for Sprint 13.2 session-persistence
// changes: cookie set on login, middleware cookie fallback, GET /auth/session,
// and logout CSRF + cookie clearing.
//
// All tests in this file are in the same package as the production code
// (package auth) so they can access unexported helpers and share the TestMain
// pool declared in repository_test.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// ---------------------------------------------------------------------------
// Login — REQ-AUTH-009: __Host-session cookie set on success
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-009"]}
func TestLogin_SetsSessionCookie(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "cookie_login_user", "pass", "student")

	svc := newTestService(15*time.Minute, 24*time.Hour)
	handler := NewHandlerFull(svc, NewRepository(pool), pool, 24*time.Hour, nil)

	r := chi.NewRouter()
	handler.Routes(r)

	body, _ := json.Marshal(map[string]string{"username": "cookie_login_user", "password": "pass"})
	req := httptest.NewRequest("POST", "/login", bytes.NewReader(body))
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var sessionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "__Host-session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected __Host-session cookie, got none")
	}
	if sessionCookie.Value == "" {
		t.Error("__Host-session cookie value must not be empty")
	}
	if !sessionCookie.HttpOnly {
		t.Error("__Host-session must be HttpOnly")
	}
	if !sessionCookie.Secure {
		t.Error("__Host-session must be Secure")
	}
	if sessionCookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("__Host-session SameSite must be Strict, got %v", sessionCookie.SameSite)
	}
	if sessionCookie.Path != "/" {
		t.Errorf("__Host-session Path must be /, got %q", sessionCookie.Path)
	}
	if sessionCookie.MaxAge <= 0 {
		t.Errorf("__Host-session MaxAge must be > 0, got %d", sessionCookie.MaxAge)
	}
}

// @{"verifies": ["REQ-AUTH-009"]}
func TestLogin_SessionCookieMaxAgeMatchesDuration(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "cookie_maxage_user", "pass", "student")

	const wantSeconds = 7200 // 2 hours
	svc := newTestService(15*time.Minute, wantSeconds*time.Second)
	handler := NewHandlerFull(svc, NewRepository(pool), pool, wantSeconds*time.Second, nil)

	r := chi.NewRouter()
	handler.Routes(r)

	body, _ := json.Marshal(map[string]string{"username": "cookie_maxage_user", "password": "pass"})
	req := httptest.NewRequest("POST", "/login", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	for _, c := range w.Result().Cookies() {
		if c.Name == "__Host-session" {
			if c.MaxAge != wantSeconds {
				t.Errorf("want MaxAge %d, got %d", wantSeconds, c.MaxAge)
			}
			return
		}
	}
	t.Fatal("__Host-session cookie not found in response")
}

// ---------------------------------------------------------------------------
// Auth middleware — REQ-AUTH-011: cookie fallback, bearer preference
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-011"]}
func TestAuthMiddleware_CookieFallback_PassesThrough(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "mw_cookie_fallback", "pass", "student")

	svc := newTestService(30*time.Minute, 24*time.Hour)
	rawToken, err := loginGetRawToken(t, svc, ctx, "mw_cookie_fallback", "pass")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	repo := NewRepository(pool)
	mw := NewAuthMiddleware(repo, pool, 30*time.Minute, nil)
	srv := httptest.NewServer(mw(newInnerHandler()))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	// Set the session cookie instead of the Authorization header.
	req.AddCookie(&http.Cookie{Name: "__Host-session", Value: rawToken})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 via cookie auth, got %d", resp.StatusCode)
	}

	var body contextResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Role != "student" {
		t.Errorf("expected role student, got %q", body.Role)
	}
}

// @{"verifies": ["REQ-AUTH-011"]}
func TestAuthMiddleware_BearerPreferredOverCookie(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "mw_bearer_pref_A", "pass", "student")
	createTestUserWithPassword(ctx, t, pool, "mw_bearer_pref_B", "pass", "admin")

	svc := newTestService(30*time.Minute, 24*time.Hour)
	tokenA, err := loginGetRawToken(t, svc, ctx, "mw_bearer_pref_A", "pass")
	if err != nil {
		t.Fatalf("Login A failed: %v", err)
	}
	tokenB, err := loginGetRawToken(t, svc, ctx, "mw_bearer_pref_B", "pass")
	if err != nil {
		t.Fatalf("Login B failed: %v", err)
	}

	repo := NewRepository(pool)
	mw := NewAuthMiddleware(repo, pool, 30*time.Minute, nil)
	srv := httptest.NewServer(mw(newInnerHandler()))
	defer srv.Close()

	// Send tokenA in the cookie and tokenB in the bearer header.
	// The middleware must use tokenB (the bearer) so the role is "admin".
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Header.Set("Authorization", "Bearer "+tokenB)
	req.AddCookie(&http.Cookie{Name: "__Host-session", Value: tokenA})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body contextResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// tokenB is admin — verifies bearer took precedence over the student cookie.
	if body.Role != "admin" {
		t.Errorf("expected bearer (admin) to take precedence, got role %q", body.Role)
	}
}

// @{"verifies": ["REQ-AUTH-011"]}
func TestAuthMiddleware_EmptyCookieAndNoHeader_Returns401(t *testing.T) {
	repo := NewRepository(pool)
	mw := NewAuthMiddleware(repo, pool, 30*time.Minute, nil)
	srv := httptest.NewServer(mw(newInnerHandler()))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	// Cookie present but value is empty — must not authenticate.
	req.AddCookie(&http.Cookie{Name: "__Host-session", Value: ""})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// GET /auth/session — REQ-AUTH-012
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-012"]}
func TestGetSession_ValidSession_Returns200WithShape(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "sess_valid_user", "pass", "student")

	svc := newTestService(30*time.Minute, 24*time.Hour)
	rawToken, err := loginGetRawToken(t, svc, ctx, "sess_valid_user", "pass")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	repo := NewRepository(pool)
	authMW := NewAuthMiddleware(repo, pool, 30*time.Minute, nil)
	handler := NewHandlerFull(svc, repo, pool, 24*time.Hour, nil)

	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(authMW)
		r.Get("/auth/session", handler.GetSession)
	})

	req := httptest.NewRequest("GET", "/auth/session", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp sessionResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.UserID == "" {
		t.Error("user_id must not be empty")
	}
	if resp.Username != "sess_valid_user" {
		t.Errorf("expected username sess_valid_user, got %q", resp.Username)
	}
	if resp.Role != "student" {
		t.Errorf("expected role student, got %q", resp.Role)
	}
	if resp.ExpiresAt == "" {
		t.Error("expires_at must not be empty")
	}
	// A freshly created (non-temp) user is not flagged.
	if resp.MustChangePassword {
		t.Error("must_change_password should be false for a normal account")
	}
}

// TestGetSession_MustChangePassword_FlagSurfaced verifies the boot/reload
// restore path carries must_change_password so the SPA router guard
// (REQ-FEUSER-002) interposes proactively rather than relying on the backend
// 403 net. Regression guard for the Sprint 20 senior-gate Finding 1.
//
// @{"verifies": ["REQ-AUTH-013", "REQ-FEUSER-002"]}
func TestGetSession_MustChangePassword_FlagSurfaced(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "sess_mcp_user", "pass", "student")
	if _, err := pool.Exec(ctx,
		`UPDATE users SET must_change_password = true WHERE username = $1`,
		"sess_mcp_user",
	); err != nil {
		t.Fatalf("set flag: %v", err)
	}

	svc := newTestService(30*time.Minute, 24*time.Hour)
	rawToken, err := loginGetRawToken(t, svc, ctx, "sess_mcp_user", "pass")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	repo := NewRepository(pool)
	authMW := NewAuthMiddleware(repo, pool, 30*time.Minute, nil)
	handler := NewHandlerFull(svc, repo, pool, 24*time.Hour, nil)

	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(authMW)
		r.Get("/auth/session", handler.GetSession)
	})

	req := httptest.NewRequest("GET", "/auth/session", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp sessionResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.MustChangePassword {
		t.Error("must_change_password should be true for a flagged account")
	}
}

// @{"verifies": ["REQ-AUTH-012"]}
func TestGetSession_NoSession_Returns401(t *testing.T) {
	repo := NewRepository(pool)
	authMW := NewAuthMiddleware(repo, pool, 30*time.Minute, nil)
	svc := newTestService(30*time.Minute, 24*time.Hour)
	handler := NewHandlerFull(svc, repo, pool, 24*time.Hour, nil)

	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(authMW)
		r.Get("/auth/session", handler.GetSession)
	})

	req := httptest.NewRequest("GET", "/auth/session", nil)
	// No Authorization header and no cookie — middleware rejects with 401.
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// @{"verifies": ["REQ-AUTH-012"]}
func TestGetSession_CookieAuth_Returns200(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "sess_cookie_user", "pass", "student")

	svc := newTestService(30*time.Minute, 24*time.Hour)
	rawToken, err := loginGetRawToken(t, svc, ctx, "sess_cookie_user", "pass")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	repo := NewRepository(pool)
	authMW := NewAuthMiddleware(repo, pool, 30*time.Minute, nil)
	handler := NewHandlerFull(svc, repo, pool, 24*time.Hour, nil)

	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(authMW)
		r.Get("/auth/session", handler.GetSession)
	})

	req := httptest.NewRequest("GET", "/auth/session", nil)
	req.AddCookie(&http.Cookie{Name: "__Host-session", Value: rawToken})
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 via cookie, got %d: %s", w.Code, w.Body.String())
	}

	var resp sessionResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Username != "sess_cookie_user" {
		t.Errorf("expected sess_cookie_user, got %q", resp.Username)
	}
}

// @{"verifies": ["REQ-AUTH-012"]}
func TestGetSession_AdminAlwaysConsented(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "sess_admin_consent", "pass", "admin")

	svc := newTestService(30*time.Minute, 24*time.Hour)
	rawToken, err := loginGetRawToken(t, svc, ctx, "sess_admin_consent", "pass")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	repo := NewRepository(pool)
	// Supply a consent provider requiring version "1.0" — admin has no row.
	provider := &mockConsentProvider{values: map[string]string{"consent_version": "1.0"}}
	authMW := NewAuthMiddleware(repo, pool, 30*time.Minute, nil) // nil = no gate in middleware
	handler := NewHandlerFull(svc, repo, pool, 24*time.Hour, provider)

	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(authMW)
		r.Get("/auth/session", handler.GetSession)
	})

	req := httptest.NewRequest("GET", "/auth/session", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp sessionResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Consented {
		t.Error("admin must always be consented=true regardless of consent row")
	}
}

// @{"verifies": ["REQ-AUTH-012"]}
func TestGetSession_StudentWithConsent_ConsentedTrue(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	userID := createTestUserWithPassword(ctx, t, pool, "sess_student_consented", "pass", "student")

	_, err := pool.Exec(ctx,
		`INSERT INTO student_consent (student_id, consented_at, consent_version) VALUES ($1, NOW(), $2)`,
		userID, "1.0",
	)
	if err != nil {
		t.Fatalf("insert consent: %v", err)
	}

	svc := newTestService(30*time.Minute, 24*time.Hour)
	rawToken, err := loginGetRawToken(t, svc, ctx, "sess_student_consented", "pass")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	repo := NewRepository(pool)
	provider := &mockConsentProvider{values: map[string]string{"consent_version": "1.0"}}
	authMW := NewAuthMiddleware(repo, pool, 30*time.Minute, nil)
	handler := NewHandlerFull(svc, repo, pool, 24*time.Hour, provider)

	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(authMW)
		r.Get("/auth/session", handler.GetSession)
	})

	req := httptest.NewRequest("GET", "/auth/session", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp sessionResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Consented {
		t.Error("expected consented=true for student with matching consent version")
	}
}

// @{"verifies": ["REQ-AUTH-012"]}
func TestGetSession_StudentWithoutConsent_ConsentedFalse(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "sess_student_unconsented", "pass", "student")

	svc := newTestService(30*time.Minute, 24*time.Hour)
	rawToken, err := loginGetRawToken(t, svc, ctx, "sess_student_unconsented", "pass")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	repo := NewRepository(pool)
	provider := &mockConsentProvider{values: map[string]string{"consent_version": "1.0"}}
	authMW := NewAuthMiddleware(repo, pool, 30*time.Minute, nil)
	handler := NewHandlerFull(svc, repo, pool, 24*time.Hour, provider)

	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(authMW)
		r.Get("/auth/session", handler.GetSession)
	})

	req := httptest.NewRequest("GET", "/auth/session", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp sessionResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Consented {
		t.Error("expected consented=false for student without consent row")
	}
}

// ---------------------------------------------------------------------------
// GET /auth/session — Finding-1: CSRF cookie re-issued on 200 (REQ-SECURITY-004)
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-012", "REQ-SECURITY-004"]}
// After a browser restart the __Host-session cookie (24h Max-Age) survives but
// the __Host-csrf cookie (session cookie) does not. GetSession must re-issue a
// fresh __Host-csrf cookie on every 200 response so that mutations do not 403.
func TestGetSession_200_ReissuesCSRFCookie(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "sess_csrf_reissue", "pass", "student")

	svc := newTestService(30*time.Minute, 24*time.Hour)
	rawToken, err := loginGetRawToken(t, svc, ctx, "sess_csrf_reissue", "pass")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	repo := NewRepository(pool)
	authMW := NewAuthMiddleware(repo, pool, 30*time.Minute, nil)
	handler := NewHandlerFull(svc, repo, pool, 24*time.Hour, nil)

	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(authMW)
		r.Get("/auth/session", handler.GetSession)
	})

	req := httptest.NewRequest("GET", "/auth/session", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var csrfCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "__Host-csrf" {
			csrfCookie = c
			break
		}
	}
	if csrfCookie == nil {
		t.Fatal("expected __Host-csrf Set-Cookie header in GET /auth/session 200 response")
	}
	if csrfCookie.Value == "" {
		t.Error("__Host-csrf cookie value must not be empty")
	}
	if !csrfCookie.Secure {
		t.Error("__Host-csrf must be Secure")
	}
	if csrfCookie.Path != "/" {
		t.Errorf("__Host-csrf Path must be /, got %q", csrfCookie.Path)
	}
}

// ---------------------------------------------------------------------------
// Logout — REQ-AUTH-010: cookie cleared, CSRF enforced when cookie present
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-010"]}
func TestLogout_ClearsSessionCookie(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "logout_cookie_clear", "pass", "student")

	svc := newTestService(15*time.Minute, 24*time.Hour)
	rawToken, err := loginGetRawToken(t, svc, ctx, "logout_cookie_clear", "pass")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	handler := NewHandlerFull(svc, NewRepository(pool), pool, 24*time.Hour, nil)
	r := chi.NewRouter()
	handler.Routes(r)

	req := httptest.NewRequest("POST", "/logout", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	// Attach a CSRF cookie so the CSRF check in logout passes.
	csrfVal := "test-csrf-token"
	req.AddCookie(&http.Cookie{Name: "__Host-csrf", Value: csrfVal})
	req.Header.Set("X-CSRF-Token", csrfVal)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}

	var clearedCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "__Host-session" {
			clearedCookie = c
			break
		}
	}
	if clearedCookie == nil {
		t.Fatal("expected __Host-session cookie in response to clear it")
	}
	if clearedCookie.MaxAge != 0 {
		t.Errorf("expected MaxAge=0 to clear cookie, got %d", clearedCookie.MaxAge)
	}
	if clearedCookie.Value != "" {
		t.Errorf("expected empty cookie value, got %q", clearedCookie.Value)
	}
}

// @{"verifies": ["REQ-AUTH-010"]}
func TestLogout_CookieAuth_NoHeader_Returns204(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "logout_cookie_only", "pass", "student")

	svc := newTestService(15*time.Minute, 24*time.Hour)
	rawToken, err := loginGetRawToken(t, svc, ctx, "logout_cookie_only", "pass")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	handler := NewHandlerFull(svc, NewRepository(pool), pool, 24*time.Hour, nil)
	r := chi.NewRouter()
	handler.Routes(r)

	req := httptest.NewRequest("POST", "/logout", nil)
	// No Authorization header — use cookie only.
	csrfVal := "csrf-abc"
	req.AddCookie(&http.Cookie{Name: "__Host-session", Value: rawToken})
	req.AddCookie(&http.Cookie{Name: "__Host-csrf", Value: csrfVal})
	req.Header.Set("X-CSRF-Token", csrfVal)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for cookie-only logout, got %d", w.Code)
	}
}

// @{"verifies": ["REQ-AUTH-010"]}
func TestLogout_CSRFMismatch_Returns403(t *testing.T) {
	svc := newTestService(15*time.Minute, 24*time.Hour)
	handler := NewHandlerFull(svc, NewRepository(pool), pool, 24*time.Hour, nil)
	r := chi.NewRouter()
	handler.Routes(r)

	req := httptest.NewRequest("POST", "/logout", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	// CSRF cookie present but header does not match → 403.
	req.AddCookie(&http.Cookie{Name: "__Host-csrf", Value: "real-csrf"})
	req.Header.Set("X-CSRF-Token", "wrong-csrf")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on CSRF mismatch, got %d", w.Code)
	}
}

// @{"verifies": ["REQ-AUTH-010"]}
func TestLogout_CSRFHeaderMissing_Returns403(t *testing.T) {
	svc := newTestService(15*time.Minute, 24*time.Hour)
	handler := NewHandlerFull(svc, NewRepository(pool), pool, 24*time.Hour, nil)
	r := chi.NewRouter()
	handler.Routes(r)

	req := httptest.NewRequest("POST", "/logout", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	// CSRF cookie present but no X-CSRF-Token header.
	req.AddCookie(&http.Cookie{Name: "__Host-csrf", Value: "real-csrf"})
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when X-CSRF-Token header is absent, got %d", w.Code)
	}
}

// @{"verifies": ["REQ-AUTH-005"]}
// Existing bearer-only logout must still work (no CSRF cookie → skip CSRF check).
func TestLogout_BearerOnly_NoCsrfCookie_Returns204(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "logout_bearer_only", "pass", "student")

	svc := newTestService(15*time.Minute, 24*time.Hour)
	rawToken, err := loginGetRawToken(t, svc, ctx, "logout_bearer_only", "pass")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	handler := NewHandlerFull(svc, NewRepository(pool), pool, 24*time.Hour, nil)
	r := chi.NewRouter()
	handler.Routes(r)

	req := httptest.NewRequest("POST", "/logout", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	// No CSRF cookie → the CSRF check is skipped entirely (legacy bearer-only
	// clients do not receive the CSRF cookie).
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for bearer-only logout without CSRF cookie, got %d", w.Code)
	}
}
