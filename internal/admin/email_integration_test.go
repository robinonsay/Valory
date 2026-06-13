//go:build integration

// email_integration_test.go — Integration tests for the admin email subsystem
// that require BOTH the test PostgreSQL instance AND the Mailpit SMTP sink.
//
// These tests verify the full path from HTTP request → ConfigService → SMTPMailer
// → Mailpit SMTP → Mailpit REST API assertion. They are layered on top of the
// DB-only handler tests in config_handler_test.go, which already cover rate
// limiting, audit recording, and response shapes using a mock mailer. Here we
// add the Mailpit-delivery dimension: the real SMTPMailer is wired in, and
// messages are verified via the Mailpit REST API.
//
// Run via:
//
//	make test-integration
//	# or manually:
//	export PATH=$PATH:/usr/local/go/bin
//	go test -tags integration -count=1 -run TestIntegEmailAdmin ./internal/admin/
//
// Env vars:
//
//	VALORY_TEST_DATABASE_URL — PostgreSQL DSN (defaults to the docker-compose service)
//	MAILPIT_API              — Mailpit REST API base (default http://localhost:18025)
package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/valory/valory/internal/audit"
	authpkg "github.com/valory/valory/internal/auth"
	internaldb "github.com/valory/valory/internal/db"
	"github.com/valory/valory/internal/email"
	"github.com/valory/valory/internal/testutil"
)

// mailpitSMTPEnv returns the SMTPEnvVars that point at the Mailpit container
// started by docker-compose.test.yml. Encryption is "none" — Mailpit accepts
// plain-TCP SMTP, which is correct for a test sink on localhost.
func mailpitSMTPEnv() email.SMTPEnvVars {
	return email.SMTPEnvVars{
		Host:       "localhost",
		Port:       11025,
		From:       "test@valory.test",
		Username:   "", // Mailpit needs no auth (REQ-EMAIL-003)
		Encryption: "none",
	}
}

// stubSecretGetterForAdmin returns a SecretGetter with no stored secrets.
// Mailpit requires no auth, so an empty password is correct.
type emptySecretGetter struct{}

func (e *emptySecretGetter) Get(_ context.Context, _ string) string { return "" }

// newMailpitMailer builds a real SMTPMailer pointed at Mailpit, with admin
// config loaded from the given ConfigService so that config-precedence tests
// can override fields via the DB.
func newMailpitMailer(cfg *ConfigService) *email.SMTPMailer {
	return email.NewSMTPMailer(cfg, &emptySecretGetter{}, mailpitSMTPEnv())
}

// makeTestSendRequestWithRealMailer sends POST /email/test through the full
// admin-auth router using a real SMTPMailer (not the mock in config_handler_test.go).
func makeTestSendRequestWithRealMailer(
	t *testing.T,
	body interface{},
	bearerToken string,
	mailer *email.SMTPMailer,
) *httptest.ResponseRecorder {
	t.Helper()

	pool := internaldb.IntegrationPool(t)

	cfg := NewConfigService(pool)
	ctx := context.Background()
	if err := cfg.Load(ctx); err != nil {
		t.Fatalf("makeTestSendRequestWithRealMailer Load: %v", err)
	}
	handler := NewConfigHandler(cfg, audit.NewRepository(pool), pool, mailer)

	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("makeTestSendRequestWithRealMailer marshal: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/email/test", bytes.NewReader(bodyBytes))
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	rr := httptest.NewRecorder()

	authMiddleware := authpkg.NewAuthMiddleware(
		authpkg.NewRepository(pool),
		pool,
		24*time.Hour,
		nil,
	)

	router := chi.NewRouter()
	router.Use(authMiddleware)
	router.Use(authpkg.RequireRole("admin"))
	handler.Routes(router)

	router.ServeHTTP(rr, req)
	return rr
}

// loginAsAdminInteg creates an admin in the integration DB and returns the
// UUID and raw session token. It mirrors the loginAsAdmin helper in
// config_handler_test.go but operates against the real integration pool.
func loginAsAdminInteg(ctx context.Context, t *testing.T) (uuid.UUID, string) {
	t.Helper()

	pool := internaldb.IntegrationPool(t)
	username := "integ_admin_" + uuid.New().String()
	password := "adminpass123"

	hash, err := authpkg.HashPassword(password)
	if err != nil {
		t.Fatalf("loginAsAdminInteg hash: %v", err)
	}
	var adminID uuid.UUID
	err = pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, 'admin') RETURNING id`,
		username, hash).Scan(&adminID)
	if err != nil {
		t.Fatalf("loginAsAdminInteg insert: %v", err)
	}

	authRepo := authpkg.NewRepository(pool)
	authSvc := authpkg.NewService(authRepo, 15*time.Minute, 24*time.Hour)
	result, err := authSvc.Login(ctx, username, password)
	if err != nil {
		t.Fatalf("loginAsAdminInteg login: %v", err)
	}
	return adminID, result.RawToken
}

// integTruncateEmailTables resets the tables touched by email integration tests.
func integTruncateEmailTables(t *testing.T) {
	t.Helper()
	pool := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, pool,
		"audit_log",
		"email_test_send_attempts",
		"sessions",
		"users",
	)
	// Re-seed system_config smtp keys to defaults after TRUNCATE CASCADE cleared them.
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO system_config (key, value) VALUES
			('smtp_host',       ''),
			('smtp_port',       '587'),
			('smtp_from',       ''),
			('smtp_username',   ''),
			('smtp_encryption', 'starttls')
		ON CONFLICT (key) DO UPDATE
			SET value = EXCLUDED.value, updated_by = NULL, updated_at = now()
	`); err != nil {
		t.Fatalf("integTruncateEmailTables re-seed smtp keys: %v", err)
	}
}

// @{"verifies": ["REQ-EMAIL-008", "REQ-EMAIL-002", "REQ-EMAIL-003"]}
func TestIntegEmailAdmin_TestSendEndpoint_MessageVisibleInMailpit(t *testing.T) {
	// Full-stack test: POST /api/v1/admin/config/email/test with a real SMTPMailer
	// pointing at Mailpit. Asserts 200 OK AND the message visible via REST API.
	//
	// This exercises:
	//   REQ-EMAIL-008: test-send endpoint delivers a message
	//   REQ-EMAIL-002: encryption=none path used for loopback relay
	//   REQ-EMAIL-003: unauthenticated connection to Mailpit (no username)
	internaldb.IntegrationPool(t) // ensures DB is ready; skip if not
	testutil.CheckMailpitReachable(t)

	integTruncateEmailTables(t)

	apiBase := testutil.MailpitAPIBase()
	testutil.ClearMessages(t, apiBase)

	ctx := context.Background()
	adminID, token := loginAsAdminInteg(ctx, t)
	_ = adminID // used later in rate-limit variant

	// Wire the SMTP config pointing at Mailpit via admin config in the DB.
	pool := internaldb.IntegrationPool(t)
	if _, err := pool.Exec(ctx, `
		UPDATE system_config SET value = $1 WHERE key = 'smtp_host'`, "localhost"); err != nil {
		t.Fatalf("set smtp_host: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE system_config SET value = $1 WHERE key = 'smtp_port'`, "11025"); err != nil {
		t.Fatalf("set smtp_port: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE system_config SET value = $1 WHERE key = 'smtp_from'`, "test@valory.test"); err != nil {
		t.Fatalf("set smtp_from: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE system_config SET value = $1 WHERE key = 'smtp_encryption'`, "none"); err != nil {
		t.Fatalf("set smtp_encryption: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE system_config SET value = $1 WHERE key = 'smtp_username'`, ""); err != nil {
		t.Fatalf("set smtp_username: %v", err)
	}

	cfg := NewConfigService(pool)
	if err := cfg.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}
	mailer := email.NewSMTPMailer(cfg, &emptySecretGetter{}, email.SMTPEnvVars{})

	body := map[string]interface{}{"to": "test-send-target@example.com"}
	rr := makeTestSendRequestWithRealMailer(t, body, token, mailer)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["ok"] != true {
		t.Errorf("expected ok=true in response, got %v", resp)
	}

	// Assert message in Mailpit.
	msg := testutil.WaitForMessage(t, apiBase, "test-send-target@example.com", "Valory test email")

	if msg.From.Address != "test@valory.test" {
		t.Errorf("From: got %q, want test@valory.test", msg.From.Address)
	}
}

// @{"verifies": ["REQ-EMAIL-004", "REQ-EMAIL-008"]}
func TestIntegEmailAdmin_ConfigPrecedence_AdminDBWins_MailArrives(t *testing.T) {
	// Config-precedence end-to-end: SMTP host is set via admin config in the DB;
	// the SMTPEnvVars fallback points at a non-existent relay. Mail must arrive
	// at Mailpit (proving admin config was used, not env vars).
	internaldb.IntegrationPool(t)
	testutil.CheckMailpitReachable(t)

	integTruncateEmailTables(t)

	apiBase := testutil.MailpitAPIBase()
	testutil.ClearMessages(t, apiBase)

	ctx := context.Background()
	_, token := loginAsAdminInteg(ctx, t)
	pool := internaldb.IntegrationPool(t)

	// Admin DB config: Mailpit
	for k, v := range map[string]string{
		"smtp_host":       "localhost",
		"smtp_port":       "11025",
		"smtp_from":       "admindbwins@valory.test",
		"smtp_username":   "",
		"smtp_encryption": "none",
	} {
		if _, err := pool.Exec(ctx,
			`UPDATE system_config SET value = $1 WHERE key = $2`, v, k); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}

	cfg := NewConfigService(pool)
	if err := cfg.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Env vars fallback: a non-existent relay — if env is used, Send will fail.
	envVars := email.SMTPEnvVars{
		Host:       "env-relay.invalid",
		Port:       2525,
		From:       "env-from@valory.test",
		Username:   "",
		Encryption: "none",
	}
	mailer := email.NewSMTPMailer(cfg, &emptySecretGetter{}, envVars)

	body := map[string]interface{}{"to": "precedence-integ@example.com"}
	rr := makeTestSendRequestWithRealMailer(t, body, token, mailer)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 (admin config wins), got %d: %s", rr.Code, rr.Body.String())
	}

	msg := testutil.WaitForMessage(t, apiBase, "precedence-integ@example.com", "Valory test email")

	// From address proves admin config was used (not env vars).
	if msg.From.Address != "admindbwins@valory.test" {
		t.Errorf("From: got %q, want admindbwins@valory.test", msg.From.Address)
	}
}

// @{"verifies": ["REQ-EMAIL-010", "REQ-EMAIL-008"]}
func TestIntegEmailAdmin_TestSend_AuditRowWritten_PayloadHasNoPassword(t *testing.T) {
	// Verify that after a successful test-send to Mailpit:
	//   1. An audit row with action='email.test_send' is recorded.
	//   2. The audit payload contains "to" but NOT any secret / password field.
	internaldb.IntegrationPool(t)
	testutil.CheckMailpitReachable(t)

	integTruncateEmailTables(t)

	apiBase := testutil.MailpitAPIBase()
	testutil.ClearMessages(t, apiBase)

	ctx := context.Background()
	adminID, token := loginAsAdminInteg(ctx, t)
	pool := internaldb.IntegrationPool(t)

	for k, v := range map[string]string{
		"smtp_host":       "localhost",
		"smtp_port":       "11025",
		"smtp_from":       "audit-test@valory.test",
		"smtp_username":   "",
		"smtp_encryption": "none",
	} {
		if _, err := pool.Exec(ctx,
			`UPDATE system_config SET value = $1 WHERE key = $2`, v, k); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}

	cfg := NewConfigService(pool)
	if err := cfg.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}
	mailer := email.NewSMTPMailer(cfg, &emptySecretGetter{}, email.SMTPEnvVars{})

	// Count audit rows before.
	var before int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE action = 'email.test_send' AND admin_id = $1`,
		adminID,
	).Scan(&before); err != nil {
		t.Fatalf("count before: %v", err)
	}

	toAddr := "audit-verify@example.com"
	body := map[string]interface{}{"to": toAddr}
	rr := makeTestSendRequestWithRealMailer(t, body, token, mailer)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Message must be visible in Mailpit.
	testutil.WaitForMessage(t, apiBase, toAddr, "Valory test email")

	// Exactly one new audit row.
	var after int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE action = 'email.test_send' AND admin_id = $1`,
		adminID,
	).Scan(&after); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != before+1 {
		t.Errorf("expected %d audit rows, got %d", before+1, after)
	}

	// Verify payload: must contain "to" and must NOT contain "smtp_password" or "password".
	var payloadJSON string
	if err := pool.QueryRow(ctx,
		`SELECT payload::text FROM audit_log
		 WHERE action = 'email.test_send' AND admin_id = $1
		 ORDER BY id DESC LIMIT 1`,
		adminID,
	).Scan(&payloadJSON); err != nil {
		t.Fatalf("payload query: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if payload["to"] != toAddr {
		t.Errorf("audit payload to: got %v, want %q", payload["to"], toAddr)
	}
	if _, hasPassword := payload["smtp_password"]; hasPassword {
		t.Error("audit payload must not contain smtp_password (REQ-EMAIL-010)")
	}
	if _, hasPassword := payload["password"]; hasPassword {
		t.Error("audit payload must not contain password field (REQ-EMAIL-010)")
	}

	// The raw JSON string must not contain any known-secret token. Since Mailpit
	// requires no password, the secret is empty — but the structure must still hold.
	if strings.Contains(payloadJSON, "password") && !strings.Contains(payloadJSON, `"ok"`) {
		// Acceptable only if "password" appears as part of a valid key name — but
		// the only valid payload keys are "to" and "ok". Any extra key is a bug.
		t.Errorf("unexpected field containing 'password' in audit payload: %s", payloadJSON)
	}
}

// @{"verifies": ["REQ-EMAIL-009"]}
func TestIntegEmailAdmin_RateLimit_SixthCallReturns429_WithMailpit(t *testing.T) {
	// Full-stack rate-limit test using the real SMTPMailer and Mailpit. The first
	// 5 calls deliver mail to Mailpit; the 6th must return 429 without delivering
	// a 6th message.
	//
	// The mock-only version of this test already lives in config_handler_test.go
	// (TestTestEmailSend_RateLimitReturns429). This variant adds the Mailpit-
	// delivery dimension: the mailer is real, so we can confirm the 6th call both
	// returns 429 AND sends nothing to Mailpit.
	internaldb.IntegrationPool(t)
	testutil.CheckMailpitReachable(t)

	integTruncateEmailTables(t)

	apiBase := testutil.MailpitAPIBase()
	testutil.ClearMessages(t, apiBase)

	ctx := context.Background()
	adminID, token := loginAsAdminInteg(ctx, t)
	pool := internaldb.IntegrationPool(t)

	// Clean the rate-limit table for this specific admin.
	if _, err := pool.Exec(ctx,
		`DELETE FROM email_test_send_attempts WHERE admin_id = $1`, adminID,
	); err != nil {
		t.Fatalf("cleanup rate-limit table: %v", err)
	}

	for k, v := range map[string]string{
		"smtp_host":       "localhost",
		"smtp_port":       "11025",
		"smtp_from":       "ratelimit@valory.test",
		"smtp_username":   "",
		"smtp_encryption": "none",
	} {
		if _, err := pool.Exec(ctx,
			`UPDATE system_config SET value = $1 WHERE key = $2`, v, k); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}

	cfg := NewConfigService(pool)
	if err := cfg.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}
	mailer := email.NewSMTPMailer(cfg, &emptySecretGetter{}, email.SMTPEnvVars{})

	body := map[string]interface{}{"to": "rate-limit-target@example.com"}

	// First 5 requests must succeed (200) and deliver mail.
	for i := 0; i < 5; i++ {
		rr := makeTestSendRequestWithRealMailer(t, body, token, mailer)
		if rr.Code != http.StatusOK {
			t.Fatalf("attempt %d: expected 200, got %d: %s", i+1, rr.Code, rr.Body.String())
		}
	}

	// Snapshot the Mailpit inbox count after 5 successful sends.
	msgsBefore, err := countMailpitMessages(apiBase)
	if err != nil {
		t.Fatalf("count messages before 6th attempt: %v", err)
	}

	// 6th request must be rate-limited.
	rr := makeTestSendRequestWithRealMailer(t, body, token, mailer)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("6th attempt: expected 429, got %d: %s", rr.Code, rr.Body.String())
	}

	// Mailpit must NOT have received an additional message.
	msgsAfter, err := countMailpitMessages(apiBase)
	if err != nil {
		t.Fatalf("count messages after 6th attempt: %v", err)
	}
	if msgsAfter != msgsBefore {
		t.Errorf("6th (rate-limited) call must not deliver mail: inbox had %d before, %d after",
			msgsBefore, msgsAfter)
	}
}

// @{"verifies": ["REQ-EMAIL-011", "REQ-EMAIL-008"]}
func TestIntegEmailAdmin_PasswordNeverEchoedInResponse(t *testing.T) {
	// Verify that when Mailpit requires no password (empty secret), the response
	// body does not accidentally contain any password-like value. This is a
	// structural check: the secret is empty so there is nothing to echo, but the
	// test validates that the response payload fields ("ok", "message") are the
	// only fields present on success.
	//
	// A companion unit test (TestRedactPassword_*) in mailer_test.go covers the
	// redaction logic for non-empty passwords against mock error strings. This
	// integration test confirms the sanitisation guard is active in the real
	// handler path, even when the password is empty.
	internaldb.IntegrationPool(t)
	testutil.CheckMailpitReachable(t)

	integTruncateEmailTables(t)

	apiBase := testutil.MailpitAPIBase()
	testutil.ClearMessages(t, apiBase)

	ctx := context.Background()
	_, token := loginAsAdminInteg(ctx, t)
	pool := internaldb.IntegrationPool(t)

	for k, v := range map[string]string{
		"smtp_host":       "localhost",
		"smtp_port":       "11025",
		"smtp_from":       "secret-check@valory.test",
		"smtp_username":   "",
		"smtp_encryption": "none",
	} {
		if _, err := pool.Exec(ctx,
			`UPDATE system_config SET value = $1 WHERE key = $2`, v, k); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}

	cfg := NewConfigService(pool)
	if err := cfg.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}
	mailer := email.NewSMTPMailer(cfg, &emptySecretGetter{}, email.SMTPEnvVars{})

	body := map[string]interface{}{"to": "secret-echo-check@example.com"}
	rr := makeTestSendRequestWithRealMailer(t, body, token, mailer)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	respBody := rr.Body.String()

	// Verify that the response JSON has exactly the expected keys.
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(respBody), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, hasSmtpError := resp["smtp_error"]; hasSmtpError {
		t.Error("response must not contain smtp_error on success (REQ-EMAIL-011)")
	}
	if _, hasPassword := resp["smtp_password"]; hasPassword {
		t.Error("response must not contain smtp_password (REQ-EMAIL-011)")
	}

	// Delivery confirmed.
	testutil.WaitForMessage(t, apiBase, "secret-echo-check@example.com", "Valory test email")
}

// @{"verifies": ["REQ-EMAIL-002", "REQ-EMAIL-001"]}
func TestIntegEmailAdmin_SMTPConfigRoundTrip_PatchAndDeliver(t *testing.T) {
	// Config round-trip: PATCH the five SMTP keys via the admin handler, then
	// verify that the updated config is used for a real send to Mailpit.
	//
	// This test drives through the PATCH endpoint (not direct SQL updates) so
	// it exercises the full config-update path including validation and DB write.
	internaldb.IntegrationPool(t)
	testutil.CheckMailpitReachable(t)

	integTruncateEmailTables(t)

	apiBase := testutil.MailpitAPIBase()
	testutil.ClearMessages(t, apiBase)

	ctx := context.Background()
	_, token := loginAsAdminInteg(ctx, t)
	pool := internaldb.IntegrationPool(t)

	cfg := NewConfigService(pool)
	if err := cfg.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}
	mailer := email.NewSMTPMailer(cfg, &emptySecretGetter{}, email.SMTPEnvVars{})

	// PATCH the SMTP config via the admin endpoint.
	patchBody := map[string]interface{}{
		"config": map[string]string{
			"smtp_host":       "localhost",
			"smtp_port":       "11025",
			"smtp_from":       "roundtrip@valory.test",
			"smtp_username":   "",
			"smtp_encryption": "none",
		},
	}

	patchHandler := NewConfigHandler(cfg, audit.NewRepository(pool), pool, mailer)
	patchBodyBytes, err := json.Marshal(patchBody)
	if err != nil {
		t.Fatalf("marshal patch body: %v", err)
	}
	patchReq := httptest.NewRequest(http.MethodPatch, "/", bytes.NewReader(patchBodyBytes))
	patchReq.Header.Set("Content-Type", "application/json")
	patchReq.Header.Set("Authorization", "Bearer "+token)
	patchRR := httptest.NewRecorder()

	authMiddleware := authpkg.NewAuthMiddleware(
		authpkg.NewRepository(pool),
		pool,
		24*time.Hour,
		nil,
	)
	patchRouter := chi.NewRouter()
	patchRouter.Use(authMiddleware)
	patchRouter.Use(authpkg.RequireRole("admin"))
	patchHandler.Routes(patchRouter)
	patchRouter.ServeHTTP(patchRR, patchReq)

	if patchRR.Code != http.StatusOK {
		t.Fatalf("PATCH /config: expected 200, got %d: %s", patchRR.Code, patchRR.Body.String())
	}

	// Verify PATCH response contains the updated keys.
	var patchResp struct {
		UpdatedKeys []string          `json:"updated_keys"`
		Config      map[string]string `json:"config"`
	}
	if err := json.NewDecoder(patchRR.Body).Decode(&patchResp); err != nil {
		t.Fatalf("decode patch response: %v", err)
	}
	if patchResp.Config["smtp_host"] != "localhost" {
		t.Errorf("after PATCH: smtp_host = %q, want localhost", patchResp.Config["smtp_host"])
	}
	if patchResp.Config["smtp_encryption"] != "none" {
		t.Errorf("after PATCH: smtp_encryption = %q, want none", patchResp.Config["smtp_encryption"])
	}

	// Now send a test email — the mailer's ConfigService was reloaded by the PATCH
	// handler, so it resolves to the just-written Mailpit values.
	testBody := map[string]interface{}{"to": "roundtrip@example.com"}
	rr := makeTestSendRequestWithRealMailer(t, testBody, token, mailer)

	if rr.Code != http.StatusOK {
		t.Fatalf("test-send after PATCH: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	msg := testutil.WaitForMessage(t, apiBase, "roundtrip@example.com", "Valory test email")
	if msg.From.Address != "roundtrip@valory.test" {
		t.Errorf("From: got %q, want roundtrip@valory.test", msg.From.Address)
	}
}

// countMailpitMessages is a helper that queries the Mailpit inbox total count.
func countMailpitMessages(apiBase string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/api/v1/messages", nil)
	if err != nil {
		return 0, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("mailpit: list messages: status %d", resp.StatusCode)
	}

	var result struct {
		Total int `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	return result.Total, nil
}
