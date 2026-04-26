package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// This file intentionally has no TestMain. The package-level TestMain in
// repository_test.go initialises the shared `pool`, runs applyMigration, and
// truncates tables around m.Run() for the entire package binary. Declaring a
// second TestMain here would cause a compile-time duplicate-symbol error.

// contextResponse is the JSON body returned by the inner handler in middleware tests.
type contextResponse struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

// newInnerHandler returns an HTTP handler that reads UserID and Role from context
// and serialises them as JSON. Used to verify that the middleware propagates
// context values correctly.
func newInnerHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, _ := UserIDFromContext(r.Context())
		role, _ := RoleFromContext(r.Context())
		resp := contextResponse{
			UserID: fmt.Sprintf("%x", uid),
			Role:   role,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
}

// @{"verifies": ["REQ-AUTH-004", "REQ-AUTH-005"]}
func TestAuthMiddleware_ValidToken_PassesThrough(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "mw_valid", "pass", "student")

	svc := newTestService(30*time.Minute, 24*time.Hour)
	rawToken, _, err := svc.Login(ctx, "mw_valid", "pass")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	repo := NewRepository(pool)
	mw := NewAuthMiddleware(repo, pool, 30*time.Minute)
	srv := httptest.NewServer(mw(newInnerHandler()))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)

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
		t.Fatalf("failed to decode response body: %v", err)
	}
	if body.UserID == "" {
		t.Error("expected UserID in context, got empty string")
	}
	if body.Role != "student" {
		t.Errorf("expected role %q, got %q", "student", body.Role)
	}
}

// @{"verifies": ["REQ-AUTH-005"]}
func TestAuthMiddleware_NoHeader_Returns401(t *testing.T) {
	repo := NewRepository(pool)
	mw := NewAuthMiddleware(repo, pool, 30*time.Minute)
	srv := httptest.NewServer(mw(newInnerHandler()))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// @{"verifies": ["REQ-AUTH-005"]}
func TestAuthMiddleware_TamperedToken_Returns401(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "mw_tamper", "pass", "student")

	svc := newTestService(30*time.Minute, 24*time.Hour)
	rawToken, _, err := svc.Login(ctx, "mw_tamper", "pass")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	// Flip the last character to produce a token that hashes differently.
	tampered := rawToken[:len(rawToken)-1] + "X"
	if tampered == rawToken {
		tampered = rawToken[:len(rawToken)-1] + "Y"
	}

	repo := NewRepository(pool)
	mw := NewAuthMiddleware(repo, pool, 30*time.Minute)
	srv := httptest.NewServer(mw(newInnerHandler()))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Header.Set("Authorization", "Bearer "+tampered)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// @{"verifies": ["REQ-AUTH-005"]}
func TestAuthMiddleware_ExpiredSession_Returns401(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	userID := createTestUserWithPassword(ctx, t, pool, "mw_expired", "pass", "student")

	rawToken, tokenHash, _, err := IssueToken(time.Hour)
	if err != nil {
		t.Fatalf("IssueToken failed: %v", err)
	}

	// Insert a session whose expires_at is already in the past.
	_, err = pool.Exec(ctx,
		`INSERT INTO sessions (user_id, token_hash, role, expires_at, last_active_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		userID, tokenHash, "student",
		time.Now().Add(-time.Second),
		time.Now().Add(-time.Second),
	)
	if err != nil {
		t.Fatalf("failed to insert expired session: %v", err)
	}

	repo := NewRepository(pool)
	mw := NewAuthMiddleware(repo, pool, 30*time.Minute)
	srv := httptest.NewServer(mw(newInnerHandler()))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// @{"verifies": ["REQ-AUTH-005"]}
func TestAuthMiddleware_InactiveSession_Returns401(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	userID := createTestUserWithPassword(ctx, t, pool, "mw_inactive", "pass", "student")

	inactivityPeriod := 30 * time.Minute
	rawToken, tokenHash, _, err := IssueToken(24 * time.Hour)
	if err != nil {
		t.Fatalf("IssueToken failed: %v", err)
	}

	// Insert a session whose last_active_at is past the inactivity threshold.
	_, err = pool.Exec(ctx,
		`INSERT INTO sessions (user_id, token_hash, role, expires_at, last_active_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		userID, tokenHash, "student",
		time.Now().Add(24*time.Hour),
		time.Now().Add(-(inactivityPeriod + time.Second)),
	)
	if err != nil {
		t.Fatalf("failed to insert inactive session: %v", err)
	}

	repo := NewRepository(pool)
	mw := NewAuthMiddleware(repo, pool, inactivityPeriod)
	srv := httptest.NewServer(mw(newInnerHandler()))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// @{"verifies": ["REQ-AUTH-004", "REQ-SYS-002"]}
func TestRequireRole_WrongRole_Returns403(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "mw_student", "pass", "student")

	svc := newTestService(30*time.Minute, 24*time.Hour)
	rawToken, _, err := svc.Login(ctx, "mw_student", "pass")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	repo := NewRepository(pool)
	authMW := NewAuthMiddleware(repo, pool, 30*time.Minute)
	roleMW := RequireRole("admin")
	handler := authMW(roleMW(newInnerHandler()))
	srv := httptest.NewServer(handler)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

// @{"verifies": ["REQ-AUTH-004", "REQ-SYS-002"]}
func TestRequireRole_CorrectRole_PassesThrough(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "mw_admin", "pass", "admin")

	svc := newTestService(30*time.Minute, 24*time.Hour)
	rawToken, _, err := svc.Login(ctx, "mw_admin", "pass")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	repo := NewRepository(pool)
	authMW := NewAuthMiddleware(repo, pool, 30*time.Minute)
	roleMW := RequireRole("admin")
	handler := authMW(roleMW(newInnerHandler()))
	srv := httptest.NewServer(handler)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

// @{"verifies": ["REQ-AUTH-005"]}
func TestAuthMiddleware_TamperedRoleClaim_Returns401(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "mw_role_tamper", "pass", "student")

	svc := newTestService(30*time.Minute, 24*time.Hour)
	rawToken, _, err := svc.Login(ctx, "mw_role_tamper", "pass")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	// Modify the middle of the base64url token. The hash will differ from any
	// stored session row, so the lookup fails with 401.
	mid := len(rawToken) / 2
	runes := []rune(rawToken)
	if runes[mid] == 'A' {
		runes[mid] = 'B'
	} else {
		runes[mid] = 'A'
	}
	tampered := string(runes)

	repo := NewRepository(pool)
	mw := NewAuthMiddleware(repo, pool, 30*time.Minute)
	srv := httptest.NewServer(mw(newInnerHandler()))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Header.Set("Authorization", "Bearer "+tampered)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// @{"verifies": ["REQ-SECURITY-002"]}
func TestAuthMiddleware_RLSParamsSetInContext(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	userID := createTestUserWithPassword(ctx, t, pool, "mw_rls", "pass", "student")

	svc := newTestService(30*time.Minute, 24*time.Hour)
	rawToken, _, err := svc.Login(ctx, "mw_rls", "pass")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	// The expected value is the hex representation of the UUID bytes matching what
	// the middleware writes via fmt.Sprintf("%x", session.UserID).
	expectedUserIDHex := fmt.Sprintf("%x", userID)

	rlsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, ok := ConnFromContext(r.Context())
		if !ok {
			http.Error(w, "no conn in context", http.StatusInternalServerError)
			return
		}
		var val string
		if err := conn.QueryRow(r.Context(),
			"SELECT current_setting('app.current_user_id', true)",
		).Scan(&val); err != nil {
			http.Error(w, "query failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, val)
	})

	repo := NewRepository(pool)
	mw := NewAuthMiddleware(repo, pool, 30*time.Minute)
	srv := httptest.NewServer(mw(rlsHandler))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	gotUserIDHex := strings.TrimSpace(string(body))

	if gotUserIDHex != expectedUserIDHex {
		t.Errorf("RLS param mismatch: got %q, want %q", gotUserIDHex, expectedUserIDHex)
	}
}

// @{"verifies": ["REQ-AUTH-004"]}
func TestRequireRole_NoContext_Returns403(t *testing.T) {
	// Wire RequireRole directly with no NewAuthMiddleware in the chain.
	// This verifies that RequireRole correctly returns 403 when no role has
	// been placed in context (e.g., an unauthenticated request that bypasses
	// the auth middleware).
	roleMW := RequireRole("admin")
	handler := roleMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 when no role in context, got %d", resp.StatusCode)
	}
}
