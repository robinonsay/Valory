package auth

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

// @{"verifies": ["REQ-AUTH-001", "REQ-AUTH-002"]}
func TestLogin_ValidCredentials_Returns200AndToken(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "alice", "s3cr3t!", "student")

	svc := newTestService(15*time.Minute, 24*time.Hour)
	handler := NewHandler(svc)

	r := chi.NewRouter()
	handler.Routes(r)

	body, _ := json.Marshal(map[string]string{
		"username": "alice",
		"password": "s3cr3t!",
	})

	req := httptest.NewRequest("POST", "/login", bytes.NewReader(body))
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	token, ok := resp["token"].(string)
	if !ok || token == "" {
		t.Fatal("expected non-empty token field")
	}

	role, ok := resp["role"].(string)
	if !ok || role == "" {
		t.Fatal("expected non-empty role field")
	}

	expiresAt, ok := resp["expires_at"].(string)
	if !ok || expiresAt == "" {
		t.Fatal("expected non-empty expires_at field")
	}
}

// @{"verifies": ["REQ-AUTH-001", "REQ-AUTH-002"]}
func TestLogin_WrongPassword_Returns401(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "bob", "correct", "student")

	svc := newTestService(15*time.Minute, 24*time.Hour)
	handler := NewHandler(svc)

	r := chi.NewRouter()
	handler.Routes(r)

	body, _ := json.Marshal(map[string]string{
		"username": "bob",
		"password": "wrong",
	})

	req := httptest.NewRequest("POST", "/login", bytes.NewReader(body))
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["error"] != "invalid credentials" {
		t.Fatalf("expected error 'invalid credentials', got: %s", resp["error"])
	}
}

// @{"verifies": ["REQ-AUTH-001"]}
func TestLogin_UnknownUsername_Returns401(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}

	svc := newTestService(15*time.Minute, 24*time.Hour)
	handler := NewHandler(svc)

	r := chi.NewRouter()
	handler.Routes(r)

	body, _ := json.Marshal(map[string]string{
		"username": "nonexistent",
		"password": "password",
	})

	req := httptest.NewRequest("POST", "/login", bytes.NewReader(body))
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	errMsg := resp["error"]
	if errMsg != "invalid credentials" {
		t.Fatalf("error message should not reveal whether username exists, got: %s", errMsg)
	}
}

// @{"verifies": ["REQ-AUTH-001"]}
func TestLogin_BlankPassword_Returns400(t *testing.T) {
	svc := newTestService(15*time.Minute, 24*time.Hour)
	handler := NewHandler(svc)

	r := chi.NewRouter()
	handler.Routes(r)

	body, _ := json.Marshal(map[string]string{
		"username": "alice",
		"password": "",
	})

	req := httptest.NewRequest("POST", "/login", bytes.NewReader(body))
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// @{"verifies": ["REQ-AUTH-002"]}
func TestLogin_SuccessfulLogin_TokenIsNonEmpty(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "charlie", "password123", "admin")

	svc := newTestService(15*time.Minute, 24*time.Hour)
	handler := NewHandler(svc)

	r := chi.NewRouter()
	handler.Routes(r)

	body, _ := json.Marshal(map[string]string{
		"username": "charlie",
		"password": "password123",
	})

	req := httptest.NewRequest("POST", "/login", bytes.NewReader(body))
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	token, ok := resp["token"].(string)
	if !ok || token == "" {
		t.Fatal("expected non-empty token in response")
	}
}

// @{"verifies": ["REQ-AUTH-003"]}
func TestLogin_StudentRole_TokenResponseContainsRole(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "student_user", "pass", "student")

	svc := newTestService(15*time.Minute, 24*time.Hour)
	handler := NewHandler(svc)

	r := chi.NewRouter()
	handler.Routes(r)

	body, _ := json.Marshal(map[string]string{
		"username": "student_user",
		"password": "pass",
	})

	req := httptest.NewRequest("POST", "/login", bytes.NewReader(body))
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	role, ok := resp["role"].(string)
	if !ok || role != "student" {
		t.Fatalf("expected role 'student', got %v", role)
	}
}

// @{"verifies": ["REQ-AUTH-003"]}
func TestLogin_AdminRole_TokenResponseContainsRole(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "admin_user", "pass", "admin")

	svc := newTestService(15*time.Minute, 24*time.Hour)
	handler := NewHandler(svc)

	r := chi.NewRouter()
	handler.Routes(r)

	body, _ := json.Marshal(map[string]string{
		"username": "admin_user",
		"password": "pass",
	})

	req := httptest.NewRequest("POST", "/login", bytes.NewReader(body))
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	role, ok := resp["role"].(string)
	if !ok || role != "admin" {
		t.Fatalf("expected role 'admin', got %v", role)
	}
}

// @{"verifies": ["REQ-AUTH-005"]}
func TestLogout_ValidToken_Returns204(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "logout_valid", "pass", "student")

	svc := newTestService(15*time.Minute, 24*time.Hour)

	rawToken, _, err := svc.Login(ctx, "logout_valid", "pass")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	handler := NewHandler(svc)

	r := chi.NewRouter()
	handler.Routes(r)

	req := httptest.NewRequest("POST", "/logout", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
}

// @{"verifies": ["REQ-AUTH-005"]}
func TestLogout_NoAuthHeader_Returns401(t *testing.T) {
	svc := newTestService(15*time.Minute, 24*time.Hour)
	handler := NewHandler(svc)

	r := chi.NewRouter()
	handler.Routes(r)

	req := httptest.NewRequest("POST", "/logout", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// @{"verifies": ["REQ-AUTH-005"]}
func TestLogout_UnknownToken_Returns204(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}

	svc := newTestService(15*time.Minute, 24*time.Hour)
	handler := NewHandler(svc)

	r := chi.NewRouter()
	handler.Routes(r)

	req := httptest.NewRequest("POST", "/logout", nil)
	req.Header.Set("Authorization", "Bearer token_that_does_not_match_any_session")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 (idempotent logout for unknown token), got %d", w.Code)
	}
}

// @{"verifies": ["REQ-AUTH-006"]}
func TestLogin_LockedAccount_Returns401WithGenericMessage(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "locked_user", "pass", "student")

	svc := newTestService(15*time.Minute, 24*time.Hour)

	// Trigger 5 consecutive failed logins to lock the account.
	for i := 0; i < 5; i++ {
		_, _, err := svc.Login(ctx, "locked_user", "wrong")
		if err == nil {
			t.Fatalf("expected error on failed login attempt %d", i+1)
		}
	}

	handler := NewHandler(svc)
	r := chi.NewRouter()
	handler.Routes(r)

	body, _ := json.Marshal(map[string]string{
		"username": "locked_user",
		"password": "pass",
	})

	req := httptest.NewRequest("POST", "/login", bytes.NewReader(body))
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Locked accounts return "invalid credentials" to avoid confirming username existence.
	if resp["error"] != "invalid credentials" {
		t.Fatalf("expected 'invalid credentials' for locked account, got: %s", resp["error"])
	}
}

// @{"verifies": ["REQ-AUTH-005"]}
func TestLogout_NonBearerScheme_Returns401(t *testing.T) {
	svc := newTestService(15*time.Minute, 24*time.Hour)
	handler := NewHandler(svc)

	r := chi.NewRouter()
	handler.Routes(r)

	req := httptest.NewRequest("POST", "/logout", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// @{"verifies": ["REQ-AUTH-001"]}
func TestLogin_DisabledAccount_Returns401WithGenericMessage(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	userID := createTestUserWithPassword(ctx, t, pool, "disabled_user", "pass", "student")

	// Deactivate the account.
	_, err := pool.Exec(ctx, "UPDATE users SET is_active = false WHERE id = $1", userID)
	if err != nil {
		t.Fatalf("failed to deactivate user: %v", err)
	}

	svc := newTestService(15*time.Minute, 24*time.Hour)
	handler := NewHandler(svc)

	r := chi.NewRouter()
	handler.Routes(r)

	body, _ := json.Marshal(map[string]string{
		"username": "disabled_user",
		"password": "pass",
	})

	req := httptest.NewRequest("POST", "/login", bytes.NewReader(body))
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Disabled accounts must return the same message as invalid credentials to
	// avoid confirming that a given username exists in the system.
	if resp["error"] != "invalid credentials" {
		t.Fatalf("expected 'invalid credentials' for disabled account, got: %s", resp["error"])
	}
}
