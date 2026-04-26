package security

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// @{"verifies": ["REQ-SECURITY-004"]}
func TestCSRFMiddleware_GET_SkipsValidation(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := CSRFMiddleware(handler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	middleware.ServeHTTP(w, req)

	if !called {
		t.Error("handler not called for GET request")
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

// @{"verifies": ["REQ-SECURITY-004"]}
func TestCSRFMiddleware_HEAD_SkipsValidation(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := CSRFMiddleware(handler)
	req := httptest.NewRequest(http.MethodHead, "/", nil)
	w := httptest.NewRecorder()

	middleware.ServeHTTP(w, req)

	if !called {
		t.Error("handler not called for HEAD request")
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

// @{"verifies": ["REQ-SECURITY-004"]}
func TestCSRFMiddleware_OPTIONS_SkipsValidation(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := CSRFMiddleware(handler)
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	w := httptest.NewRecorder()

	middleware.ServeHTTP(w, req)

	if !called {
		t.Error("handler not called for OPTIONS request")
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

// @{"verifies": ["REQ-SECURITY-004"]}
func TestCSRFMiddleware_POST_MissingCookie_Returns403(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := CSRFMiddleware(handler)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	w := httptest.NewRecorder()

	middleware.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", w.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Errorf("failed to decode response body: %v", err)
	}

	if body["error"] != "csrf_token_missing" {
		t.Errorf("expected error 'csrf_token_missing', got '%s'", body["error"])
	}
}

// @{"verifies": ["REQ-SECURITY-004"]}
func TestCSRFMiddleware_POST_MissingHeader_Returns403(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := CSRFMiddleware(handler)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.AddCookie(&http.Cookie{
		Name:  "__Host-csrf",
		Value: "test-token",
	})
	w := httptest.NewRecorder()

	middleware.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", w.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Errorf("failed to decode response body: %v", err)
	}

	if body["error"] != "csrf_token_missing" {
		t.Errorf("expected error 'csrf_token_missing', got '%s'", body["error"])
	}
}

// @{"verifies": ["REQ-SECURITY-004"]}
func TestCSRFMiddleware_POST_MismatchedToken_Returns403(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := CSRFMiddleware(handler)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.AddCookie(&http.Cookie{
		Name:  "__Host-csrf",
		Value: "abc",
	})
	req.Header.Set("X-CSRF-Token", "xyz")
	w := httptest.NewRecorder()

	middleware.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", w.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Errorf("failed to decode response body: %v", err)
	}

	if body["error"] != "csrf_token_mismatch" {
		t.Errorf("expected error 'csrf_token_mismatch', got '%s'", body["error"])
	}
}

// @{"verifies": ["REQ-SECURITY-004"]}
func TestCSRFMiddleware_POST_MatchingToken_Passes(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := CSRFMiddleware(handler)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.AddCookie(&http.Cookie{
		Name:  "__Host-csrf",
		Value: "matching-token",
	})
	req.Header.Set("X-CSRF-Token", "matching-token")
	w := httptest.NewRecorder()

	middleware.ServeHTTP(w, req)

	if !called {
		t.Error("handler not called for POST with matching token")
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

// @{"verifies": ["REQ-SECURITY-004"]}
func TestCSRFMiddleware_DELETE_ValidatesToken(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := CSRFMiddleware(handler)
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req.AddCookie(&http.Cookie{
		Name:  "__Host-csrf",
		Value: "matching-token",
	})
	req.Header.Set("X-CSRF-Token", "matching-token")
	w := httptest.NewRecorder()

	middleware.ServeHTTP(w, req)

	if !called {
		t.Error("handler not called for DELETE with matching token")
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

// @{"verifies": ["REQ-SECURITY-004"]}
func TestCSRFMiddleware_PATCH_ValidatesToken(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := CSRFMiddleware(handler)
	req := httptest.NewRequest(http.MethodPatch, "/", nil)
	req.AddCookie(&http.Cookie{
		Name:  "__Host-csrf",
		Value: "matching-token",
	})
	req.Header.Set("X-CSRF-Token", "matching-token")
	w := httptest.NewRecorder()

	middleware.ServeHTTP(w, req)

	if !called {
		t.Error("handler not called for PATCH with matching token")
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

// @{"verifies": ["REQ-SECURITY-004"]}
func TestSetCSRFCookie_SetsCorrectAttributes(t *testing.T) {
	w := httptest.NewRecorder()
	token := "test-token-value"

	SetCSRFCookie(w, token)

	setCookie := w.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, "__Host-csrf=test-token-value") {
		t.Errorf("expected cookie name and value, got: %s", setCookie)
	}

	if !strings.Contains(setCookie, "Path=/") {
		t.Error("expected Path=/")
	}

	if !strings.Contains(setCookie, "Secure") {
		t.Error("expected Secure flag")
	}

	if !strings.Contains(setCookie, "SameSite=Strict") {
		t.Error("expected SameSite=Strict")
	}

	if strings.Contains(setCookie, "HttpOnly") {
		t.Error("HttpOnly should not be set (must be JS-readable)")
	}
}

// @{"verifies": ["REQ-SECURITY-004"]}
func TestGenerateCSRFToken_Length(t *testing.T) {
	token1, err := GenerateCSRFToken()
	if err != nil {
		t.Fatalf("GenerateCSRFToken failed: %v", err)
	}

	token2, err := GenerateCSRFToken()
	if err != nil {
		t.Fatalf("GenerateCSRFToken failed: %v", err)
	}

	expectedLen := 43
	if len(token1) != expectedLen {
		t.Errorf("expected token length %d, got %d", expectedLen, len(token1))
	}

	if token1 == token2 {
		t.Error("two consecutive calls generated the same token")
	}
}
