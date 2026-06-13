//go:build integration

// twofa_toggle_integration_test.go — Integration tests for the 2FA toggle
// prerequisite enforcement in the admin config handler (Sprint 21, task 21.5).
//
// These tests verify the PATCH /api/v1/admin/config endpoint's cross-key
// validation logic for two_factor_enabled:
//
//	(d1) Enabling 2FA without email configured → 422
//	(d2) Enabling 2FA with email configured but no test-send verified → 422
//	(d3) Enabling 2FA with test-send verified but an active admin without email → 422
//	(d4) Enabling 2FA with all prerequisites met → 200 (enable succeeds)
//	(d5) Disabling 2FA has no prerequisites → 200 immediately
//
// These tests live in the admin package because the prerequisite validation
// logic lives in admin.ConfigService / patchConfig. The auth package cannot
// import admin (circular import), so the integration tests are split:
//   - internal/auth/twofactor_integration_test.go: full OTP HTTP journey
//   - internal/admin/twofa_toggle_integration_test.go: toggle prerequisites
//
// Run with:
//
//	make test-integration
//	# or manually:
//	export PATH=$PATH:/usr/local/go/bin
//	VALORY_TEST_DATABASE_URL=postgres://valory_test:valory_test@localhost:55432/valory_test?sslmode=disable \
//	  go test -tags integration -count=1 -p 1 -run TestInteg2FAToggle ./internal/admin/
package admin

import (
	"bytes"
	"context"
	"encoding/json"
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

// ---------------------------------------------------------------------------
// Shared table set for toggle integration tests (child → parent order).
// ---------------------------------------------------------------------------

var toggleIntegTables = []string{
	"otp_rate_limits",
	"pending_2fa",
	"audit_log",
	"email_test_send_attempts",
	"login_attempts",
	"sessions",
	"users",
}

// ---------------------------------------------------------------------------
// Builder helpers shared across toggle tests
// ---------------------------------------------------------------------------

// toggleEmptySecretGetter satisfies SecretGetter (Mailpit needs no auth).
type toggleEmptySecretGetter struct{}

func (e *toggleEmptySecretGetter) Get(_ context.Context, _ string) string { return "" }

// seedToggleSMTPConfig seeds the Mailpit SMTP keys and optionally sets
// email_test_send_verified_at. Uses UPSERT so it works whether the rows were
// previously truncated (by internaldb.TruncateTables) or already present.
// Call with setVerified=true when the test requires the test-send prerequisite.
func seedToggleSMTPConfig(ctx context.Context, t *testing.T, setVerified bool) {
	t.Helper()
	pool := internaldb.IntegrationPool(t)

	verifiedVal := ""
	if setVerified {
		verifiedVal = time.Now().UTC().Format(time.RFC3339)
	}

	// UPSERT all system_config keys needed for 2FA toggle tests. The ON CONFLICT
	// clause handles the case where the migration seed rows are still present
	// (normal run without TRUNCATE) and where they were removed by TruncateTables.
	seedRows := []struct{ k, v string }{
		{"smtp_host", "localhost"},
		{"smtp_port", "11025"},
		{"smtp_from", "toggle-test@valory.test"},
		{"smtp_username", ""},
		{"smtp_encryption", "none"},
		{"email_test_send_verified_at", verifiedVal},
		{"two_factor_enabled", "false"},
	}
	for _, row := range seedRows {
		if _, err := pool.Exec(ctx,
			`INSERT INTO system_config (key, value) VALUES ($1, $2)
			 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
			row.k, row.v,
		); err != nil {
			t.Fatalf("seedToggleSMTPConfig: upsert %s: %v", row.k, err)
		}
	}
}

// clearToggleSMTPConfig resets SMTP keys and test-send marker to defaults
// using UPSERT so it is safe regardless of whether rows are present.
func clearToggleSMTPConfig(ctx context.Context, t *testing.T) {
	t.Helper()
	pool := internaldb.IntegrationPool(t)

	defaults := []struct{ k, v string }{
		{"smtp_host", ""},
		{"smtp_port", "587"},
		{"smtp_from", ""},
		{"smtp_username", ""},
		{"smtp_encryption", "starttls"},
		{"email_test_send_verified_at", ""},
		{"two_factor_enabled", "false"},
	}
	for _, row := range defaults {
		if _, err := pool.Exec(ctx,
			`INSERT INTO system_config (key, value) VALUES ($1, $2)
			 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
			row.k, row.v,
		); err != nil {
			t.Fatalf("clearToggleSMTPConfig: %s: %v", row.k, err)
		}
	}
}

// loginAsAdminWithEmail creates an admin with an email address, issues a
// direct session (bypassing 2FA — safe here because 2FA is being tested at
// the config layer, not the login layer), and returns the admin UUID and token.
func loginAsAdminWithEmail(ctx context.Context, t *testing.T) (uuid.UUID, string) {
	t.Helper()
	pool := internaldb.IntegrationPool(t)

	username := "toggle_admin_" + uuid.New().String()
	password := "togglepass123"
	email := username + "@valory.test"

	hash, err := authpkg.HashPassword(password)
	if err != nil {
		t.Fatalf("loginAsAdminWithEmail hash: %v", err)
	}
	var adminID uuid.UUID
	err = pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role, email) VALUES ($1, $2, 'admin', $3) RETURNING id`,
		username, hash, email,
	).Scan(&adminID)
	if err != nil {
		t.Fatalf("loginAsAdminWithEmail insert: %v", err)
	}

	authRepo := authpkg.NewRepository(pool)
	authSvc := authpkg.NewService(authRepo, 15*time.Minute, 24*time.Hour)
	result, err := authSvc.Login(ctx, username, password)
	if err != nil {
		t.Fatalf("loginAsAdminWithEmail login: %v", err)
	}
	return adminID, result.RawToken
}

// buildToggleHandler constructs the router used by toggle tests with an
// optional real SMTPMailer (when mailpitMailer != nil) or a nil mailer (for
// "email not configured" tests).
func buildToggleHandler(t *testing.T, mailer email.Mailer) *httptest.ResponseRecorder {
	// Returned unused — callers build their own requests.
	// This function exists to centralise handler construction.
	_ = mailer
	return nil
}

// doPatchConfig sends PATCH / to the config handler with the given body.
func doPatchConfig(t *testing.T, body map[string]interface{}, bearerToken string, mailer email.Mailer) *httptest.ResponseRecorder {
	t.Helper()
	pool := internaldb.IntegrationPool(t)

	cfg := NewConfigService(pool)
	ctx := context.Background()
	if err := cfg.Load(ctx); err != nil {
		t.Fatalf("doPatchConfig Load: %v", err)
	}

	handler := NewConfigHandler(cfg, audit.NewRepository(pool), pool, mailer)

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("doPatchConfig marshal: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	rr := httptest.NewRecorder()

	authMW := authpkg.NewAuthMiddleware(
		authpkg.NewRepository(pool),
		pool,
		24*time.Hour,
		nil,
	)
	router := chi.NewRouter()
	router.Use(authMW)
	router.Use(authpkg.RequireRole("admin"))
	handler.Routes(router)
	router.ServeHTTP(rr, req)
	return rr
}

// ---------------------------------------------------------------------------
// (d1) Enabling 2FA without email configured → 422
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-018"]}
func TestInteg2FAToggle_EnableBlocked_EmailNotConfigured(t *testing.T) {
	internaldb.IntegrationPool(t)

	pool := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, pool, toggleIntegTables...)
	ctx := context.Background()
	t.Cleanup(func() { clearToggleSMTPConfig(ctx, t) })

	// Clear SMTP config so mailer.IsConfigured() returns false.
	clearToggleSMTPConfig(ctx, t)

	_, token := loginAsAdminWithEmail(ctx, t)

	// Nil mailer: IsConfigured() returns false unconditionally.
	rr := doPatchConfig(t,
		map[string]interface{}{"config": map[string]string{"two_factor_enabled": "true"}},
		token,
		nil, // nil mailer → IsConfigured() false
	)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 when email not configured, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	errs, ok := resp["validation_errors"].([]interface{})
	if !ok || len(errs) == 0 {
		t.Fatalf("expected validation_errors array, got %v", resp)
	}

	found := false
	for _, e := range errs {
		if strings.Contains(e.(string), "two_factor_enabled") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected validation error mentioning two_factor_enabled, got: %v", errs)
	}
}

// ---------------------------------------------------------------------------
// (d2) Enabling 2FA with email configured but no test-send verified → 422
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-018"]}
func TestInteg2FAToggle_EnableBlocked_TestSendNotVerified(t *testing.T) {
	internaldb.IntegrationPool(t)
	testutil.CheckMailpitReachable(t)

	pool := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, pool, toggleIntegTables...)
	ctx := context.Background()
	// SMTP configured (Mailpit) but test-send NOT verified.
	seedToggleSMTPConfig(ctx, t, false)
	t.Cleanup(func() { clearToggleSMTPConfig(ctx, t) })

	_, token := loginAsAdminWithEmail(ctx, t)

	cfg := NewConfigService(pool)
	if err := cfg.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}
	mailer := email.NewSMTPMailer(cfg, &toggleEmptySecretGetter{}, email.SMTPEnvVars{})

	rr := doPatchConfig(t,
		map[string]interface{}{"config": map[string]string{"two_factor_enabled": "true"}},
		token,
		mailer,
	)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 when test-send not verified, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errs, _ := resp["validation_errors"].([]interface{})
	found := false
	for _, e := range errs {
		if strings.Contains(e.(string), "two_factor_enabled") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected two_factor_enabled validation error, got: %v", errs)
	}
}

// ---------------------------------------------------------------------------
// (d3) Enabling 2FA with test-send verified but an active admin without email → 422
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-018", "REQ-AUTH-021"]}
func TestInteg2FAToggle_EnableBlocked_AdminWithoutEmail(t *testing.T) {
	internaldb.IntegrationPool(t)
	testutil.CheckMailpitReachable(t)

	pool := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, pool, toggleIntegTables...)
	ctx := context.Background()
	// SMTP configured AND test-send verified.
	seedToggleSMTPConfig(ctx, t, true)
	t.Cleanup(func() { clearToggleSMTPConfig(ctx, t) })

	// Create an admin WITH email (the one who sends the PATCH request).
	_, token := loginAsAdminWithEmail(ctx, t)

	// Create a SECOND active admin WITHOUT email — this must block the toggle.
	adminNoEmail := "admin_noemail_" + uuid.New().String()
	hash, _ := authpkg.HashPassword("pass")
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (username, password_hash, role, is_active) VALUES ($1, $2, 'admin', true)`,
		adminNoEmail, hash,
	); err != nil {
		t.Fatalf("insert no-email admin: %v", err)
	}

	cfg := NewConfigService(pool)
	if err := cfg.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}
	mailer := email.NewSMTPMailer(cfg, &toggleEmptySecretGetter{}, email.SMTPEnvVars{})

	rr := doPatchConfig(t,
		map[string]interface{}{"config": map[string]string{"two_factor_enabled": "true"}},
		token,
		mailer,
	)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 when admin has no email, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errs, _ := resp["validation_errors"].([]interface{})
	found := false
	for _, e := range errs {
		if strings.Contains(e.(string), "admin") || strings.Contains(e.(string), "email") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected validation error about admin email, got: %v", errs)
	}
}

// ---------------------------------------------------------------------------
// (d4) Enabling 2FA with all prerequisites met → 200
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-018"]}
func TestInteg2FAToggle_Enable_Succeeds_WhenPrerequisitesMet(t *testing.T) {
	internaldb.IntegrationPool(t)
	testutil.CheckMailpitReachable(t)

	pool := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, pool, toggleIntegTables...)
	ctx := context.Background()
	// All prerequisites met.
	seedToggleSMTPConfig(ctx, t, true)
	t.Cleanup(func() { clearToggleSMTPConfig(ctx, t) })

	// Admin WITH email (both admins in the DB have emails after loginAsAdminWithEmail).
	_, token := loginAsAdminWithEmail(ctx, t)

	cfg := NewConfigService(pool)
	if err := cfg.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}
	mailer := email.NewSMTPMailer(cfg, &toggleEmptySecretGetter{}, email.SMTPEnvVars{})

	rr := doPatchConfig(t,
		map[string]interface{}{"config": map[string]string{"two_factor_enabled": "true"}},
		token,
		mailer,
	)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 when prerequisites met, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	cfgMap, ok := resp["config"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected config in response, got %v", resp)
	}
	if cfgMap["two_factor_enabled"] != "true" {
		t.Errorf("expected two_factor_enabled=true in updated config, got %v", cfgMap["two_factor_enabled"])
	}

	// Confirm audit row for 2fa.config.enabled was written.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE action = '2fa.config.enabled'`,
	).Scan(&count); err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if count == 0 {
		t.Error("expected at least one audit_log row with action=2fa.config.enabled")
	}
}

// ---------------------------------------------------------------------------
// (d5) Disabling 2FA has no prerequisites → 200 immediately
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-018"]}
func TestInteg2FAToggle_Disable_NoPrerequisites_Succeeds(t *testing.T) {
	internaldb.IntegrationPool(t)

	pool := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, pool, toggleIntegTables...)
	ctx := context.Background()
	// Start with 2FA enabled (upsert in DB); SMTP irrelevant for disabling.
	if _, err := pool.Exec(ctx,
		`INSERT INTO system_config (key, value) VALUES ('two_factor_enabled', 'true')
		 ON CONFLICT (key) DO UPDATE SET value = 'true', updated_at = NOW()`,
	); err != nil {
		t.Fatalf("set two_factor_enabled=true: %v", err)
	}
	t.Cleanup(func() { clearToggleSMTPConfig(ctx, t) })

	_, token := loginAsAdminWithEmail(ctx, t)

	// Nil mailer — disabling must not check mailer.IsConfigured().
	rr := doPatchConfig(t,
		map[string]interface{}{"config": map[string]string{"two_factor_enabled": "false"}},
		token,
		nil,
	)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 when disabling 2FA, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	cfgMap, _ := resp["config"].(map[string]interface{})
	if cfgMap["two_factor_enabled"] != "false" {
		t.Errorf("expected two_factor_enabled=false after disable, got %v", cfgMap["two_factor_enabled"])
	}

	// Audit row for 2fa.config.disabled must exist.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE action = '2fa.config.disabled'`,
	).Scan(&count); err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if count == 0 {
		t.Error("expected at least one audit_log row with action=2fa.config.disabled")
	}
}

// ---------------------------------------------------------------------------
// (d6) GET /config response includes two_fa_prerequisites struct
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-018", "REQ-AUTH-021"]}
func TestInteg2FAToggle_GetConfig_IncludesTwoFAPrerequisites(t *testing.T) {
	internaldb.IntegrationPool(t)
	testutil.CheckMailpitReachable(t)

	pool := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, pool, toggleIntegTables...)
	ctx := context.Background()
	seedToggleSMTPConfig(ctx, t, true)
	t.Cleanup(func() { clearToggleSMTPConfig(ctx, t) })

	_, token := loginAsAdminWithEmail(ctx, t)

	cfg := NewConfigService(pool)
	if err := cfg.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}
	mailer := email.NewSMTPMailer(cfg, &toggleEmptySecretGetter{}, email.SMTPEnvVars{})
	handler := NewConfigHandler(cfg, audit.NewRepository(pool), pool, mailer)

	authMW := authpkg.NewAuthMiddleware(
		authpkg.NewRepository(pool),
		pool,
		24*time.Hour,
		nil,
	)
	router := chi.NewRouter()
	router.Use(authMW)
	router.Use(authpkg.RequireRole("admin"))
	handler.Routes(router)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	prereqs, ok := resp["two_fa_prerequisites"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected two_fa_prerequisites object in GET /config response, got %v", resp)
	}

	// email_configured should be true (we seeded Mailpit SMTP keys).
	if prereqs["email_configured"] != true {
		t.Errorf("expected email_configured=true, got %v", prereqs["email_configured"])
	}

	// test_send_verified should be true (we called seedToggleSMTPConfig with setVerified=true).
	if prereqs["test_send_verified"] != true {
		t.Errorf("expected test_send_verified=true, got %v", prereqs["test_send_verified"])
	}

	// admins_without_email: only the loginAsAdminWithEmail admin is in DB; it has an email.
	if prereqs["admins_without_email"].(float64) != 0 {
		t.Errorf("expected admins_without_email=0, got %v", prereqs["admins_without_email"])
	}
}

// ---------------------------------------------------------------------------
// (d7) test-send success sets email_test_send_verified_at (REQ-AUTH-018 §4.4)
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-018"]}
func TestInteg2FAToggle_TestSendSetsVerifiedAt(t *testing.T) {
	internaldb.IntegrationPool(t)
	testutil.CheckMailpitReachable(t)

	pool := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, pool,
		"audit_log", "email_test_send_attempts", "sessions", "users",
	)
	ctx := context.Background()
	seedToggleSMTPConfig(ctx, t, false) // test-send NOT yet verified
	t.Cleanup(func() { clearToggleSMTPConfig(ctx, t) })

	apiBase := testutil.MailpitAPIBase()
	testutil.ClearMessages(t, apiBase)

	_, token := loginAsAdminWithEmail(ctx, t)

	cfg := NewConfigService(pool)
	if err := cfg.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}
	mailer := email.NewSMTPMailer(cfg, &toggleEmptySecretGetter{}, email.SMTPEnvVars{})
	handler := NewConfigHandler(cfg, audit.NewRepository(pool), pool, mailer)

	authMW := authpkg.NewAuthMiddleware(
		authpkg.NewRepository(pool),
		pool,
		24*time.Hour,
		nil,
	)
	router := chi.NewRouter()
	router.Use(authMW)
	router.Use(authpkg.RequireRole("admin"))
	handler.Routes(router)

	// Confirm verified_at is empty before the test send.
	var verifiedBefore string
	if err := pool.QueryRow(ctx,
		`SELECT value FROM system_config WHERE key = 'email_test_send_verified_at'`,
	).Scan(&verifiedBefore); err != nil {
		t.Fatalf("query before: %v", err)
	}
	if verifiedBefore != "" {
		t.Fatalf("expected empty email_test_send_verified_at before test-send, got %q", verifiedBefore)
	}

	// POST /email/test.
	testBody := map[string]string{"to": "verifyat@valory.test"}
	bodyBytes, _ := json.Marshal(testBody)
	req := httptest.NewRequest(http.MethodPost, "/email/test", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("test-send: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify the message arrived in Mailpit.
	testutil.WaitForMessage(t, apiBase, "verifyat@valory.test", "Valory test email")

	// email_test_send_verified_at must now be set to a parseable RFC 3339 timestamp.
	var verifiedAfter string
	if err := pool.QueryRow(ctx,
		`SELECT value FROM system_config WHERE key = 'email_test_send_verified_at'`,
	).Scan(&verifiedAfter); err != nil {
		t.Fatalf("query after: %v", err)
	}
	if verifiedAfter == "" {
		t.Fatal("expected email_test_send_verified_at to be set after successful test-send")
	}
	if _, parseErr := time.Parse(time.RFC3339, verifiedAfter); parseErr != nil {
		t.Errorf("email_test_send_verified_at is not a valid RFC 3339 timestamp: %q", verifiedAfter)
	}
}
