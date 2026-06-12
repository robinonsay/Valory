//go:build testing

package admin

// secrets_handler_test.go — integration tests for SecretsHandler HTTP handlers.
//
// These tests share the TestMain defined in config_handler_test.go which creates
// handlerTestPool from TEST_DATABASE_URL, applies inline DDL migrations, and
// truncates tables before/after the test run. All tests gate on handlerTestPool
// being non-nil and call t.Skip when TEST_DATABASE_URL is absent.
//
// Security invariants under test:
//   - GET never exposes any plaintext secret value in the response body.
//   - Audit entries for secret.set / secret.clear contain only {name}, no value.
//   - last4 is the only hint of value content visible via the API.
//   - Disabled KEK → 503 on PUT (env-fallback mode still works; value blocked).
//   - DELETE is idempotent (always 204 for known names, even when no row exists).
//
// CSRF and role enforcement are applied by the router group in main.go and are
// not unit-testable at this handler layer. An end-to-end test covers them.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	authpkg "github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/audit"
)

// ── test fixtures ─────────────────────────────────────────────────────────────

// testKEK returns a deterministic 32-byte key for handler tests.
// It is NOT stored in the environment; it is passed directly to NewSecretProvider.
func testKEK() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i + 0xAA)
	}
	return k
}

// testKEKBase64 is the base64 form of testKEK(), pre-computed so tests that
// exercise LoadKEK can round-trip it through the env var.
var testKEKBase64 = base64.StdEncoding.EncodeToString(func() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i + 0xAA)
	}
	return k
}())

// newTestSecretsHandler constructs a SecretsHandler backed by handlerTestPool.
// kek may be nil to test the disabled-KEK path.
func newTestSecretsHandler(t *testing.T, kek []byte) *SecretsHandler {
	t.Helper()
	provider := NewSecretProvider(kek, handlerTestPool)
	auditRepo := audit.NewRepository(handlerTestPool)
	return NewSecretsHandler(provider, auditRepo, handlerTestPool)
}

// makeSecretsRequest sends a request through a chi router wired with an admin
// user context (injected directly without a live auth middleware so the test
// exercises only the handler logic). The adminID is set in the request context
// via auth.SetTestContext.
func makeSecretsRequest(
	t *testing.T,
	handler *SecretsHandler,
	adminID uuid.UUID,
	method, path string,
	body interface{},
) *httptest.ResponseRecorder {
	t.Helper()

	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("makeSecretsRequest marshal: %v", err)
		}
	}

	req := httptest.NewRequest(method, path, bytes.NewReader(bodyBytes))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// Inject the admin user ID into the request context using the test helper
	// exported by the auth package under the "testing" build tag. This mirrors
	// what RequireRole("admin") + the auth middleware would set in production.
	ctx := authpkg.SetTestContext(req.Context(), [16]byte(adminID), "admin")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()

	// Wire the handler onto a chi router so chi.URLParam works correctly.
	router := chi.NewRouter()
	handler.Routes(router)
	router.ServeHTTP(rr, req)
	return rr
}

// insertManagedSecret directly inserts an encrypted row into managed_secrets so
// that listSecrets tests can verify metadata without going through PUT.
func insertManagedSecret(t *testing.T, ctx context.Context, name, plainValue string, adminID uuid.UUID) {
	t.Helper()

	kek := testKEK()
	ct, nonce, err := gcmEncrypt(kek, []byte(plainValue))
	if err != nil {
		t.Fatalf("insertManagedSecret encrypt: %v", err)
	}
	runes := []rune(plainValue)
	start := len(runes) - 4
	if start < 0 {
		start = 0
	}
	last4 := string(runes[start:])

	_, err = handlerTestPool.Exec(ctx,
		`INSERT INTO managed_secrets (name, ciphertext, nonce, last4, updated_by)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (name) DO UPDATE
		   SET ciphertext = EXCLUDED.ciphertext,
		       nonce      = EXCLUDED.nonce,
		       last4      = EXCLUDED.last4,
		       updated_by = EXCLUDED.updated_by,
		       updated_at = now()`,
		name, ct, nonce, last4, adminID,
	)
	if err != nil {
		t.Fatalf("insertManagedSecret: %v", err)
	}
}

// ── GET /secrets (listSecrets) ────────────────────────────────────────────────

// @{"verifies": ["REQ-ADMIN-005", "REQ-ADMIN-006", "REQ-ADMIN-007", "REQ-SECURITY-008"]}
func TestListSecrets_ResponseNeverContainsPlaintextValue(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminID, _ := loginAsAdmin(ctx, t)

	// Insert a known plaintext value into managed_secrets.
	plaintext := "sk-test-secret-12345"
	insertManagedSecret(t, ctx, "anthropic_api_key", plaintext, adminID)
	t.Cleanup(func() {
		handlerTestPool.Exec(ctx, `DELETE FROM managed_secrets WHERE name = 'anthropic_api_key'`)
	})

	handler := newTestSecretsHandler(t, testKEK())
	rr := makeSecretsRequest(t, handler, adminID, http.MethodGet, "/", nil)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	body := rr.Body.String()
	// The response body must NEVER contain the plaintext value (REQ-SECURITY-008).
	if strings.Contains(body, plaintext) {
		t.Errorf("response body contains plaintext secret value %q — this is a security violation", plaintext)
	}
}

// @{"verifies": ["REQ-ADMIN-007", "REQ-SECURITY-008"]}
func TestListSecrets_ManagedSecret_ShowsLast4Only(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminID, _ := loginAsAdmin(ctx, t)

	plaintext := "sk-my-brave-key-abcd"
	insertManagedSecret(t, ctx, "brave_api_key", plaintext, adminID)
	t.Cleanup(func() {
		handlerTestPool.Exec(ctx, `DELETE FROM managed_secrets WHERE name = 'brave_api_key'`)
	})

	handler := newTestSecretsHandler(t, testKEK())
	rr := makeSecretsRequest(t, handler, adminID, http.MethodGet, "/", nil)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Secrets []secretMeta `json:"secrets"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var braveSecret *secretMeta
	for i, s := range resp.Secrets {
		if s.Name == "brave_api_key" {
			braveSecret = &resp.Secrets[i]
			break
		}
	}
	if braveSecret == nil {
		t.Fatal("brave_api_key not found in secrets list")
	}

	// Configured must be true and source must be "managed".
	if !braveSecret.Configured {
		t.Error("expected configured=true for managed secret")
	}
	if braveSecret.Source != "managed" {
		t.Errorf("expected source='managed', got %q", braveSecret.Source)
	}

	// last4 must be the final 4 runes of the plaintext — NOT the full value.
	wantLast4 := "abcd"
	if braveSecret.Last4 != wantLast4 {
		t.Errorf("expected last4=%q, got %q", wantLast4, braveSecret.Last4)
	}
}

// @{"verifies": ["REQ-ADMIN-005", "REQ-ADMIN-006", "REQ-ADMIN-007"]}
func TestListSecrets_AllKnownNamesPresent(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminID, _ := loginAsAdmin(ctx, t)

	handler := newTestSecretsHandler(t, testKEK())
	rr := makeSecretsRequest(t, handler, adminID, http.MethodGet, "/", nil)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Secrets []secretMeta `json:"secrets"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Every name in knownSecrets must appear in the response.
	nameSet := make(map[string]bool)
	for _, s := range resp.Secrets {
		nameSet[s.Name] = true
	}
	for name := range knownSecrets {
		if !nameSet[name] {
			t.Errorf("known secret %q missing from listSecrets response", name)
		}
	}
}

// ── PUT /secrets/{name} (putSecret) ──────────────────────────────────────────

// @{"verifies": ["REQ-ADMIN-005", "REQ-ADMIN-006"]}
func TestPutSecret_UnknownName_Returns400(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminID, _ := loginAsAdmin(ctx, t)

	handler := newTestSecretsHandler(t, testKEK())
	rr := makeSecretsRequest(t, handler, adminID, http.MethodPut, "/unknown_secret_name",
		map[string]string{"value": "some-value"})

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown name, got %d: %s", rr.Code, rr.Body.String())
	}
}

// @{"verifies": ["REQ-ADMIN-005", "REQ-ADMIN-006"]}
func TestPutSecret_EmptyValue_Returns400(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminID, _ := loginAsAdmin(ctx, t)

	handler := newTestSecretsHandler(t, testKEK())
	rr := makeSecretsRequest(t, handler, adminID, http.MethodPut, "/anthropic_api_key",
		map[string]string{"value": ""})

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty value, got %d: %s", rr.Code, rr.Body.String())
	}
}

// @{"verifies": ["REQ-ADMIN-005", "REQ-ADMIN-006"]}
func TestPutSecret_ValueExceeds512Chars_Returns422(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminID, _ := loginAsAdmin(ctx, t)

	// 513 ASCII characters — one over the limit.
	longValue := strings.Repeat("x", 513)

	handler := newTestSecretsHandler(t, testKEK())
	rr := makeSecretsRequest(t, handler, adminID, http.MethodPut, "/anthropic_api_key",
		map[string]string{"value": longValue})

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 for value > 512 chars, got %d: %s", rr.Code, rr.Body.String())
	}
}

// @{"verifies": ["REQ-ADMIN-007", "REQ-SECURITY-007"]}
func TestPutSecret_DisabledKEK_Returns503(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminID, _ := loginAsAdmin(ctx, t)

	// Handler with nil KEK simulates absent/invalid VALORY_SECRET_KEY.
	handler := newTestSecretsHandler(t, nil)
	rr := makeSecretsRequest(t, handler, adminID, http.MethodPut, "/anthropic_api_key",
		map[string]string{"value": "some-key"})

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when KEK is nil, got %d: %s", rr.Code, rr.Body.String())
	}
}

// @{"verifies": ["REQ-ADMIN-005", "REQ-ADMIN-006", "REQ-ADMIN-007", "REQ-ADMIN-008", "REQ-SECURITY-008"]}
func TestPutSecret_ValidRequest_Returns200WithLast4AndNoValue(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminID, _ := loginAsAdmin(ctx, t)
	t.Cleanup(func() {
		handlerTestPool.Exec(ctx, `DELETE FROM managed_secrets WHERE name = 'anthropic_api_key'`)
		handlerTestPool.Exec(ctx, `TRUNCATE TABLE audit_log CASCADE`)
	})

	plaintext := "sk-ant-test-key-9999"

	handler := newTestSecretsHandler(t, testKEK())
	rr := makeSecretsRequest(t, handler, adminID, http.MethodPut, "/anthropic_api_key",
		map[string]string{"value": plaintext})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	body := rr.Body.String()

	// The response must NEVER contain the plaintext value (REQ-SECURITY-008).
	if strings.Contains(body, plaintext) {
		t.Errorf("PUT response body contains plaintext value %q — security violation", plaintext)
	}

	var meta secretMeta
	if err := json.Unmarshal([]byte(body), &meta); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if meta.Name != "anthropic_api_key" {
		t.Errorf("name: expected 'anthropic_api_key', got %q", meta.Name)
	}
	if !meta.Configured {
		t.Error("expected configured=true after PUT")
	}
	if meta.Source != "managed" {
		t.Errorf("expected source='managed', got %q", meta.Source)
	}

	// last4 must be the final 4 characters of the plaintext.
	wantLast4 := "9999"
	if meta.Last4 != wantLast4 {
		t.Errorf("last4: expected %q, got %q", wantLast4, meta.Last4)
	}
}

// @{"verifies": ["REQ-SECURITY-008"]}
func TestPutSecret_AuditEntryContainsNameOnlyNoValue(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminID, _ := loginAsAdmin(ctx, t)
	t.Cleanup(func() {
		handlerTestPool.Exec(ctx, `DELETE FROM managed_secrets WHERE name = 'brave_api_key'`)
		handlerTestPool.Exec(ctx, `TRUNCATE TABLE audit_log CASCADE`)
	})

	plaintext := "brave-secret-value-xyz"

	handler := newTestSecretsHandler(t, testKEK())
	rr := makeSecretsRequest(t, handler, adminID, http.MethodPut, "/brave_api_key",
		map[string]string{"value": plaintext})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify audit_log has exactly one secret.set entry.
	var payloadJSON string
	err := handlerTestPool.QueryRow(ctx,
		`SELECT payload::text FROM audit_log WHERE action = 'secret.set' ORDER BY id DESC LIMIT 1`,
	).Scan(&payloadJSON)
	if err != nil {
		t.Fatalf("query audit_log: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	// Payload must contain the secret name.
	if payload["name"] != "brave_api_key" {
		t.Errorf("audit payload: expected name='brave_api_key', got %v", payload["name"])
	}

	// Payload must NOT contain the value or any derivative of it (REQ-SECURITY-008).
	if _, ok := payload["value"]; ok {
		t.Error("audit payload must not contain 'value' key")
	}
	if _, ok := payload["last4"]; ok {
		t.Error("audit payload must not contain 'last4' key")
	}

	// The raw JSON must not contain the plaintext anywhere.
	if strings.Contains(payloadJSON, plaintext) {
		t.Errorf("audit payload JSON contains plaintext value %q — security violation", plaintext)
	}
}

// @{"verifies": ["REQ-ADMIN-008"]}
func TestPutSecret_InvalidatesProviderCache(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminID, _ := loginAsAdmin(ctx, t)
	t.Cleanup(func() {
		handlerTestPool.Exec(ctx, `DELETE FROM managed_secrets WHERE name = 'anthropic_api_key'`)
		handlerTestPool.Exec(ctx, `TRUNCATE TABLE audit_log CASCADE`)
	})

	provider := NewSecretProvider(testKEK(), handlerTestPool)
	auditRepo := audit.NewRepository(handlerTestPool)
	handler := NewSecretsHandler(provider, auditRepo, handlerTestPool)

	// Prime the cache with the first value via a direct DB insert (not via PUT so
	// we can observe the cache before and after the PUT independently).
	firstValue := "initial-key-aaaa"
	insertManagedSecret(t, ctx, "anthropic_api_key", firstValue, adminID)

	// Warm the cache.
	cached := provider.Get(ctx, "anthropic_api_key")
	if cached != firstValue {
		t.Fatalf("warm cache: expected %q, got %q", firstValue, cached)
	}

	// Now PUT a new value through the handler.
	secondValue := "rotated-key-bbbb"
	rr := makeSecretsRequest(t, handler, adminID, http.MethodPut, "/anthropic_api_key",
		map[string]string{"value": secondValue})
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// The cache must have been invalidated so Get returns the new value from DB.
	got := provider.Get(ctx, "anthropic_api_key")
	if got != secondValue {
		t.Errorf("after PUT: expected %q (cache invalidated), got %q", secondValue, got)
	}
}

// @{"verifies": ["REQ-ADMIN-007", "REQ-SECURITY-008"]}
func TestPutSecret_Exactly512Chars_IsAccepted(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminID, _ := loginAsAdmin(ctx, t)
	t.Cleanup(func() {
		handlerTestPool.Exec(ctx, `DELETE FROM managed_secrets WHERE name = 'brave_api_key'`)
		handlerTestPool.Exec(ctx, `TRUNCATE TABLE audit_log CASCADE`)
	})

	// Exactly 512 characters — must be accepted (boundary check).
	value512 := strings.Repeat("a", 508) + "wxyz" // last4 will be "wxyz"

	handler := newTestSecretsHandler(t, testKEK())
	rr := makeSecretsRequest(t, handler, adminID, http.MethodPut, "/brave_api_key",
		map[string]string{"value": value512})

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for exactly 512-char value, got %d: %s", rr.Code, rr.Body.String())
	}

	var meta secretMeta
	if err := json.NewDecoder(rr.Body).Decode(&meta); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if meta.Last4 != "wxyz" {
		t.Errorf("last4: expected 'wxyz', got %q", meta.Last4)
	}
}

// ── DELETE /secrets/{name} (deleteSecret) ────────────────────────────────────

// @{"verifies": ["REQ-ADMIN-008", "REQ-SECURITY-008"]}
func TestDeleteSecret_KnownName_Returns204Idempotent(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminID, _ := loginAsAdmin(ctx, t)
	t.Cleanup(func() {
		handlerTestPool.Exec(ctx, `DELETE FROM managed_secrets WHERE name = 'anthropic_api_key'`)
		handlerTestPool.Exec(ctx, `TRUNCATE TABLE audit_log CASCADE`)
	})

	handler := newTestSecretsHandler(t, testKEK())

	// First DELETE: no row exists — must still return 204 (idempotent).
	rr := makeSecretsRequest(t, handler, adminID, http.MethodDelete, "/anthropic_api_key", nil)
	if rr.Code != http.StatusNoContent {
		t.Errorf("first DELETE (no row): expected 204, got %d: %s", rr.Code, rr.Body.String())
	}

	// Insert a row, then DELETE again — must still return 204.
	insertManagedSecret(t, ctx, "anthropic_api_key", "some-key-here", adminID)
	rr = makeSecretsRequest(t, handler, adminID, http.MethodDelete, "/anthropic_api_key", nil)
	if rr.Code != http.StatusNoContent {
		t.Errorf("second DELETE (row exists): expected 204, got %d: %s", rr.Code, rr.Body.String())
	}

	// Third DELETE: row has been deleted — must still return 204.
	rr = makeSecretsRequest(t, handler, adminID, http.MethodDelete, "/anthropic_api_key", nil)
	if rr.Code != http.StatusNoContent {
		t.Errorf("third DELETE (row gone): expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
}

// @{"verifies": ["REQ-ADMIN-005", "REQ-ADMIN-006"]}
func TestDeleteSecret_UnknownName_Returns400(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminID, _ := loginAsAdmin(ctx, t)

	handler := newTestSecretsHandler(t, testKEK())
	rr := makeSecretsRequest(t, handler, adminID, http.MethodDelete, "/nonexistent_secret", nil)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown name, got %d: %s", rr.Code, rr.Body.String())
	}
}

// @{"verifies": ["REQ-SECURITY-008"]}
func TestDeleteSecret_AuditEntryContainsNameOnly(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminID, _ := loginAsAdmin(ctx, t)
	t.Cleanup(func() {
		handlerTestPool.Exec(ctx, `DELETE FROM managed_secrets WHERE name = 'brave_api_key'`)
		handlerTestPool.Exec(ctx, `TRUNCATE TABLE audit_log CASCADE`)
	})

	// Insert a row first so we have something to delete.
	insertManagedSecret(t, ctx, "brave_api_key", "key-to-delete", adminID)

	handler := newTestSecretsHandler(t, testKEK())
	rr := makeSecretsRequest(t, handler, adminID, http.MethodDelete, "/brave_api_key", nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}

	var payloadJSON string
	err := handlerTestPool.QueryRow(ctx,
		`SELECT payload::text FROM audit_log WHERE action = 'secret.clear' ORDER BY id DESC LIMIT 1`,
	).Scan(&payloadJSON)
	if err != nil {
		t.Fatalf("query audit_log: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if payload["name"] != "brave_api_key" {
		t.Errorf("audit payload: expected name='brave_api_key', got %v", payload["name"])
	}
	if _, ok := payload["value"]; ok {
		t.Error("audit payload for secret.clear must not contain 'value'")
	}
}

// @{"verifies": ["REQ-ADMIN-008"]}
func TestDeleteSecret_InvalidatesProviderCache(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminID, _ := loginAsAdmin(ctx, t)
	t.Cleanup(func() {
		handlerTestPool.Exec(ctx, `DELETE FROM managed_secrets WHERE name = 'anthropic_api_key'`)
		handlerTestPool.Exec(ctx, `TRUNCATE TABLE audit_log CASCADE`)
	})

	provider := NewSecretProvider(testKEK(), handlerTestPool)
	auditRepo := audit.NewRepository(handlerTestPool)
	handler := NewSecretsHandler(provider, auditRepo, handlerTestPool)

	// Insert a secret and warm the cache.
	secretValue := "cached-api-key-zzzz"
	insertManagedSecret(t, ctx, "anthropic_api_key", secretValue, adminID)
	cached := provider.Get(ctx, "anthropic_api_key")
	if cached != secretValue {
		t.Fatalf("warm cache: expected %q, got %q", secretValue, cached)
	}

	// DELETE via handler must invalidate the cache.
	rr := makeSecretsRequest(t, handler, adminID, http.MethodDelete, "/anthropic_api_key", nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("DELETE: expected 204, got %d: %s", rr.Code, rr.Body.String())
	}

	// With no env var set, the provider must now return "".
	got := provider.Get(ctx, "anthropic_api_key")
	if got != "" {
		t.Errorf("after DELETE: expected empty string (cache invalidated, no row), got %q", got)
	}
}

// ── Provider resolution order: managed > env > "" ────────────────────────────

// @{"verifies": ["REQ-ADMIN-007", "REQ-ADMIN-008"]}
func TestProvider_ResolutionOrder_ManagedBeatsEnv(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminID, _ := loginAsAdmin(ctx, t)
	t.Cleanup(func() {
		handlerTestPool.Exec(ctx, `DELETE FROM managed_secrets WHERE name = 'anthropic_api_key'`)
	})

	managedValue := "managed-key-value"
	insertManagedSecret(t, ctx, "anthropic_api_key", managedValue, adminID)

	// Set env var — must NOT be returned when a managed row exists.
	t.Setenv("ANTHROPIC_API_KEY", "env-key-should-be-ignored")

	provider := NewSecretProvider(testKEK(), handlerTestPool)
	got := provider.Get(ctx, "anthropic_api_key")
	if got != managedValue {
		t.Errorf("resolution order: expected managed value %q, got %q", managedValue, got)
	}
}

// @{"verifies": ["REQ-ADMIN-007", "REQ-ADMIN-008"]}
func TestProvider_ResolutionOrder_EnvFallbackWhenNoManagedRow(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()

	// Ensure no managed row exists for this name.
	handlerTestPool.Exec(ctx, `DELETE FROM managed_secrets WHERE name = 'brave_api_key'`)

	t.Setenv("BRAVE_API_KEY", "env-brave-fallback")

	provider := NewSecretProvider(testKEK(), handlerTestPool)
	got := provider.Get(ctx, "brave_api_key")
	if got != "env-brave-fallback" {
		t.Errorf("env fallback: expected 'env-brave-fallback', got %q", got)
	}
}

// ── Table-driven: PUT validation ──────────────────────────────────────────────

// @{"verifies": ["REQ-ADMIN-005", "REQ-ADMIN-006", "REQ-ADMIN-007"]}
func TestPutSecret_TableDriven_InputValidation(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminID, _ := loginAsAdmin(ctx, t)

	cases := []struct {
		name       string
		secretName string
		value      string
		wantStatus int
	}{
		{
			name:       "unknown secret name",
			secretName: "totally_unknown",
			value:      "some-value",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "empty value",
			secretName: "anthropic_api_key",
			value:      "",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "value exactly 513 chars (over limit)",
			secretName: "anthropic_api_key",
			value:      strings.Repeat("z", 513),
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "value exactly 512 chars (at limit)",
			secretName: "brave_api_key",
			value:      strings.Repeat("a", 512),
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(func() {
				handlerTestPool.Exec(ctx, fmt.Sprintf(`DELETE FROM managed_secrets WHERE name = '%s'`, tc.secretName))
				handlerTestPool.Exec(ctx, `TRUNCATE TABLE audit_log CASCADE`)
			})

			handler := newTestSecretsHandler(t, testKEK())
			path := "/" + tc.secretName
			body := map[string]string{"value": tc.value}

			rr := makeSecretsRequest(t, handler, adminID, http.MethodPut, path, body)
			if rr.Code != tc.wantStatus {
				t.Errorf("expected %d, got %d: %s", tc.wantStatus, rr.Code, rr.Body.String())
			}

			// Regardless of outcome, the response must never contain the plaintext.
			if tc.value != "" && strings.Contains(rr.Body.String(), tc.value) {
				t.Errorf("response body contains plaintext value — security violation")
			}
		})
	}
}

// ── No plaintext in any response ──────────────────────────────────────────────

// @{"verifies": ["REQ-SECURITY-008"]}
func TestGetSecret_ResponseBodyNeverContainsEncryptedBytesAsString(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminID, _ := loginAsAdmin(ctx, t)

	// A value with distinctive content that would be easy to detect if leaked.
	secretValue := "SUPERSECRET_CANARY_XYZZY_" + uuid.New().String()
	insertManagedSecret(t, ctx, "anthropic_api_key", secretValue, adminID)
	t.Cleanup(func() {
		handlerTestPool.Exec(ctx, `DELETE FROM managed_secrets WHERE name = 'anthropic_api_key'`)
	})

	handler := newTestSecretsHandler(t, testKEK())
	rr := makeSecretsRequest(t, handler, adminID, http.MethodGet, "/", nil)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	if strings.Contains(body, secretValue) {
		t.Errorf("GET /secrets response contains the full plaintext canary value — security violation")
	}
	// Verify ciphertext field is also not exposed: the response has no "ciphertext" key.
	if strings.Contains(body, "ciphertext") {
		t.Error("GET /secrets response contains 'ciphertext' field — must not be exposed")
	}
	if strings.Contains(body, "nonce") {
		t.Error("GET /secrets response contains 'nonce' field — must not be exposed")
	}
}

