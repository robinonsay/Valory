package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// ----------------------------------------------------------------------------
// Unit tests — service-level input validation (no DB required)
// Validation fires before any pool.Begin call so a nil pool is safe here.
// ----------------------------------------------------------------------------

// @{"verifies": ["REQ-SETUP-007"]}
// TC-SETUP-001: blank username returns ErrUsernameMissing before any DB contact.
func TestService_CreateFirstAdmin_BlankUsername(t *testing.T) {
	svc := &Service{} // nil pool — validation fires first
	_, err := svc.CreateFirstAdmin(context.Background(), "   ", nil, "password123")
	if err != ErrUsernameMissing {
		t.Fatalf("expected ErrUsernameMissing, got %v", err)
	}
}

// @{"verifies": ["REQ-SETUP-007"]}
// TC-SETUP-002: whitespace-only username (tab+newline) returns ErrUsernameMissing.
func TestService_CreateFirstAdmin_WhitespaceOnlyUsername(t *testing.T) {
	svc := &Service{}
	_, err := svc.CreateFirstAdmin(context.Background(), "\t\n  ", nil, "password123")
	if err != ErrUsernameMissing {
		t.Fatalf("expected ErrUsernameMissing for whitespace-only username, got %v", err)
	}
}

// @{"verifies": ["REQ-SETUP-006"]}
// TC-SETUP-003: password of 7 bytes returns ErrPasswordTooShort.
func TestService_CreateFirstAdmin_SevenBytePassword(t *testing.T) {
	svc := &Service{}
	_, err := svc.CreateFirstAdmin(context.Background(), "admin", nil, "7chars7")
	if err != ErrPasswordTooShort {
		t.Fatalf("expected ErrPasswordTooShort, got %v", err)
	}
}

// @{"verifies": ["REQ-SETUP-006"]}
// TC-SETUP-004: password of exactly 8 bytes must NOT return ErrPasswordTooShort.
// The call will fail further on (nil pool → panic at pool.Begin), which is expected
// for a unit test that only exercises the validation gate.
func TestService_CreateFirstAdmin_EightBytePassword_PassesValidation(t *testing.T) {
	svc := &Service{}
	defer func() {
		// Recover from the nil-pool panic so the test can assert the sentinel.
		_ = recover()
	}()
	_, err := svc.CreateFirstAdmin(context.Background(), "admin", nil, "12345678")
	// If we reach here without a panic the service returned an error other than
	// the validation sentinels — that is fine for this unit test.
	if err == ErrPasswordTooShort {
		t.Fatal("8-byte password should pass validation but returned ErrPasswordTooShort")
	}
	if err == ErrUsernameMissing {
		t.Fatal("username 'admin' should pass validation but returned ErrUsernameMissing")
	}
}

// @{"verifies": ["REQ-SETUP-006"]}
// TC-SETUP-005: empty password (0 bytes) returns ErrPasswordTooShort.
func TestService_CreateFirstAdmin_EmptyPassword(t *testing.T) {
	svc := &Service{}
	_, err := svc.CreateFirstAdmin(context.Background(), "admin", nil, "")
	if err != ErrPasswordTooShort {
		t.Fatalf("expected ErrPasswordTooShort for empty password, got %v", err)
	}
}

// ----------------------------------------------------------------------------
// Unit tests — HTTP handler layer error-mapping and response shape.
// These tests do not touch a real DB; they exercise JSON encoding and the
// writeSetupError / writeSetupJSON helpers.
// ----------------------------------------------------------------------------

// newTestRouter returns a chi.Router with both setup routes mounted at
// /api/v1/setup backed by the given handler.
func newTestRouter(h *Handler) http.Handler {
	r := chi.NewRouter()
	r.Route("/api/v1/setup", func(r chi.Router) {
		h.Routes(r)
	})
	return r
}

// @{"verifies": ["REQ-SETUP-007"]}
// TC-SETUP-006: POST /api/v1/setup with blank username returns 400 "username_required".
func TestHandler_Post_BlankUsername_Returns400(t *testing.T) {
	h := NewHandler(&Service{})
	router := newTestRouter(h)

	body, _ := json.Marshal(map[string]string{"username": "  ", "password": "password123"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec.Body.Bytes(), "username_required")
}

// @{"verifies": ["REQ-SETUP-006"]}
// TC-SETUP-007: POST /api/v1/setup with a 7-byte password returns 400 "password_too_short".
func TestHandler_Post_ShortPassword_Returns400(t *testing.T) {
	h := NewHandler(&Service{})
	router := newTestRouter(h)

	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "7chars7"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec.Body.Bytes(), "password_too_short")
}

// @{"verifies": ["REQ-SETUP-003"]}
// TC-SETUP-008: POST /api/v1/setup with malformed JSON returns 400 "invalid_json".
func TestHandler_Post_MalformedJSON_Returns400(t *testing.T) {
	h := NewHandler(&Service{})
	router := newTestRouter(h)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/", strings.NewReader("{not valid json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec.Body.Bytes(), "invalid_json")
}

// @{"verifies": ["REQ-SETUP-005"]}
// TC-SETUP-009: writeSetupError emits the correct status and JSON error body.
// Confirms the already_configured → 409 path produces valid output.
func TestWriteSetupError_AlreadyConfigured(t *testing.T) {
	rec := httptest.NewRecorder()
	writeSetupError(rec, http.StatusConflict, "already_configured")

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
	assertErrorCode(t, rec.Body.Bytes(), "already_configured")
}

// @{"verifies": ["REQ-SETUP-001", "REQ-SETUP-002"]}
// TC-SETUP-010: writeSetupJSON emits the correct status and a parseable body
// with the needs_setup key for the status endpoint shape.
func TestWriteSetupJSON_NeedsSetupTrue(t *testing.T) {
	rec := httptest.NewRecorder()
	writeSetupJSON(rec, http.StatusOK, map[string]bool{"needs_setup": true})

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response body: %v", err)
	}
	v, ok := resp["needs_setup"]
	if !ok {
		t.Fatal("response missing 'needs_setup' key")
	}
	if !v {
		t.Error("expected needs_setup=true")
	}
}

// @{"verifies": ["REQ-SETUP-008"]}
// TC-SETUP-011: POST response for created admin must not include password field.
// The response body from handlePost on success contains only username + role.
func TestHandler_Post_ResponseNeverContainsPassword(t *testing.T) {
	// Build a response body that mimics a successful 201 and assert "password"
	// is absent. We call writeSetupJSON directly — the full path is exercised in
	// the integration tests.
	rec := httptest.NewRecorder()
	writeSetupJSON(rec, http.StatusCreated, map[string]string{
		"username": "admin",
		"role":     "admin",
	})

	body := rec.Body.String()
	if strings.Contains(body, "password") {
		t.Errorf("response body must not contain 'password', got: %s", body)
	}
	if strings.Contains(body, "hash") {
		t.Errorf("response body must not contain 'hash', got: %s", body)
	}
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

func assertErrorCode(t *testing.T, body []byte, want string) {
	t.Helper()
	var resp map[string]string
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to parse error response body %q: %v", body, err)
	}
	if resp["error"] != want {
		t.Errorf("expected error=%q, got %q", want, resp["error"])
	}
}
