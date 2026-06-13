//go:build integration

// twofactor_integration_test.go — HTTP-level integration tests for the
// email-based two-factor authentication feature (Sprint 21, task 21.5).
//
// These tests build a full handler stack with 2FA ENABLED (real SMTPMailer
// pointed at the Mailpit SMTP sink, config seeded with a test-send verified
// timestamp, and a Mailpit-backed mailer). They cover:
//
//	(a) Happy path           — POST /login → 202 + pending_token; OTP in Mailpit;
//	                           POST /2fa/verify → 200 session; session reaches GET /session.
//	(b) Wrong-code           — 5 wrong OTPs → 401 too_many_attempts; 6th → 401 (no row).
//	(c) Resend throttle      — resend twice within 60 s → 429 resend_throttled;
//	                           after resend, old code no longer verifies.
//	(e) Break-glass          — VALORY_2FA_BREAK_GLASS set → admin login returns 200
//	                           directly; wrong password still 401; audit row present.
//	(f) 2FA-OFF regression   — two_factor_enabled=false → normal 200 session (no 202).
//	(g) Audit hygiene        — no OTP value appears in any audit_log payload row after
//	                           a complete challenge+verify journey.
//	(h) Pending token rejected — pending token sent as Bearer returns 401 from /session.
//
// Toggle-prerequisite tests (d) live in internal/admin/twofa_toggle_integration_test.go
// because admin.ConfigService owns the 2FA-prerequisite validation logic and
// internal/auth cannot import internal/admin (circular import).
//
// Admin 2FA reset (REQ-AUTH-019) is tested in TestInteg2FA_AdminReset_ClearsPendingAfterCap.
//
// Run with:
//
//	make test-integration
//	# or manually:
//	export PATH=$PATH:/usr/local/go/bin
//	VALORY_TEST_DATABASE_URL=postgres://valory_test:valory_test@localhost:55432/valory_test?sslmode=disable \
//	  MAILPIT_API=http://localhost:18025 \
//	  go test -tags integration -count=1 -p 1 -run TestInteg2FA ./internal/auth/
package auth

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
	internaldb "github.com/valory/valory/internal/db"
	"github.com/valory/valory/internal/email"
	"github.com/valory/valory/internal/testutil"
)

// ---------------------------------------------------------------------------
// Tables truncated before each 2FA integration test (child → parent order).
// ---------------------------------------------------------------------------

var twoFAIntegTables = []string{
	"otp_rate_limits",
	"pending_2fa",
	"audit_log",
	"email_test_send_attempts",
	"login_attempts",
	"sessions",
	"users",
}

// ---------------------------------------------------------------------------
// Helpers shared across all tests in this file
// ---------------------------------------------------------------------------

// twoFAEmptySecretGetter satisfies the SecretGetter interface used by
// email.NewSMTPMailer. Mailpit requires no SMTP authentication so returning
// empty string is correct.
type twoFAEmptySecretGetter struct{}

func (e *twoFAEmptySecretGetter) Get(_ context.Context, _ string) string { return "" }

// seedSMTPConfig writes the Mailpit SMTP keys and, optionally,
// email_test_send_verified_at into system_config. Uses UPSERT so it works
// whether TruncateTables has removed the seed rows or they are still present.
// Call with setTestSendVerified=true for tests that need the 2FA toggle to be
// enableable.
func seedSMTPConfig(ctx context.Context, t *testing.T, setTestSendVerified bool) {
	t.Helper()
	pool := internaldb.IntegrationPool(t)

	verifiedVal := ""
	if setTestSendVerified {
		verifiedVal = time.Now().UTC().Format(time.RFC3339)
	}

	seedRows := []struct{ k, v string }{
		{"smtp_host", "localhost"},
		{"smtp_port", "11025"},
		{"smtp_from", "2fa-test@valory.test"},
		{"smtp_username", ""},
		{"smtp_encryption", "none"},
		{"email_test_send_verified_at", verifiedVal},
		{"two_factor_enabled", "true"},
	}
	for _, row := range seedRows {
		if _, err := pool.Exec(ctx,
			`INSERT INTO system_config (key, value) VALUES ($1, $2)
			 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
			row.k, row.v,
		); err != nil {
			t.Fatalf("seedSMTPConfig: upsert %s: %v", row.k, err)
		}
	}
}

// resetSystemConfigDefaults restores system_config to post-migration defaults.
// Uses UPSERT so it is safe after TruncateTables or in a fresh run.
func resetSystemConfigDefaults(ctx context.Context, t *testing.T) {
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
			t.Fatalf("resetSystemConfigDefaults: %s: %v", row.k, err)
		}
	}
}

// twoFAHandlerStack builds the full chi router for 2FA integration tests.
// It wires a real SMTPMailer pointed at Mailpit and a staticConfig with
// two_factor_enabled=true (or false when disabled=true). The stack mounts:
//
//   - POST /login and POST /logout (handler.Routes)
//   - POST /2fa/verify and POST /2fa/resend (handler.TwoFARoutes, public)
//   - GET /session (behind authMW + handler.GetSession)
//   - DELETE /users/{id}/2fa/reset (behind authMW + admin-role guard)
func twoFAHandlerStack(t *testing.T, disabled bool) (*chi.Mux, *email.SMTPMailer) {
	t.Helper()
	pool := internaldb.IntegrationPool(t)

	cfgVal := "true"
	if disabled {
		cfgVal = "false"
	}
	// The staticConfig must carry smtp_host so that SMTPMailer.IsConfigured()
	// returns true (it reads smtp_host from the ConfigLoader).
	cfg := &staticConfig{vals: map[string]string{
		"two_factor_enabled": cfgVal,
		"smtp_host":          "localhost",
		"smtp_port":          "11025",
		"smtp_from":          "2fa-test@valory.test",
		"smtp_username":      "",
		"smtp_encryption":    "none",
	}}

	repo := NewRepository(pool)
	auditRepo := audit.NewRepository(pool)

	// SMTPEnvVars is the fallback when config returns ""; passing zeros means
	// all resolution falls through to the staticConfig values above.
	mailer := email.NewSMTPMailer(cfg, &twoFAEmptySecretGetter{}, email.SMTPEnvVars{})

	tf := NewTwoFactorService(repo, auditRepo, pool, cfg, mailer)

	svc := NewService(repo, 15*time.Minute, 24*time.Hour)
	svc.SetTwoFactor(tf, cfg)

	handler := NewHandlerFull(svc, repo, pool, 24*time.Hour, nil)

	authMW := NewAuthMiddleware(repo, pool, 24*time.Hour, nil)

	r := chi.NewRouter()

	// Public: login, 2FA verify/resend (no auth middleware).
	r.Group(func(r chi.Router) {
		handler.Routes(r)
		handler.TwoFARoutes(r)
	})

	// Protected: session endpoint and admin 2FA reset.
	r.Group(func(r chi.Router) {
		r.Use(authMW)
		r.Get("/session", handler.GetSession)
		r.Group(func(r chi.Router) {
			r.Use(RequireRole("admin"))
			r.Delete("/users/{id}/2fa/reset", handler.ResetUserTwoFactor)
		})
	})

	return r, mailer
}

// doLogin performs POST /login and decodes the response body.
func doLogin(r chi.Router, username, password string) (statusCode int, body map[string]interface{}) {
	payload, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	req := httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	var resp map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&resp) //nolint:errcheck
	return rr.Code, resp
}

// doVerify performs POST /2fa/verify and decodes the response body.
func doVerify(r chi.Router, pendingToken, otp string) (statusCode int, body map[string]interface{}) {
	payload, _ := json.Marshal(map[string]string{
		"pending_token": pendingToken,
		"otp":           otp,
	})
	req := httptest.NewRequest(http.MethodPost, "/2fa/verify", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	var resp map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&resp) //nolint:errcheck
	return rr.Code, resp
}

// doResend performs POST /2fa/resend and decodes the response body.
func doResend(r chi.Router, pendingToken string) (statusCode int, body map[string]interface{}) {
	payload, _ := json.Marshal(map[string]string{
		"pending_token": pendingToken,
	})
	req := httptest.NewRequest(http.MethodPost, "/2fa/resend", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	var resp map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&resp) //nolint:errcheck
	return rr.Code, resp
}

// doGetSession performs GET /session with the given Bearer token.
func doGetSession(r chi.Router, bearerToken string) int {
	req := httptest.NewRequest(http.MethodGet, "/session", nil)
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr.Code
}

// seed2FAUser creates a test user with an email address using the integration
// pool (internaldb.IntegrationPool) rather than the package-level `pool` from
// repository_test.go's TestMain (which is only set by TEST_DATABASE_URL, not
// VALORY_TEST_DATABASE_URL that the integration harness uses).
func seed2FAUser(ctx context.Context, t *testing.T, username, password, role, emailAddr string) {
	t.Helper()
	pool := internaldb.IntegrationPool(t)

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("seed2FAUser: HashPassword: %v", err)
	}
	var id [16]byte
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role, email) VALUES ($1, $2, $3, $4) RETURNING id`,
		username, hash, role, emailAddr,
	).Scan(&id); err != nil {
		t.Fatalf("seed2FAUser %q: %v", username, err)
	}
}

// seed2FAUserReturnID inserts a user and returns its UUID. Used in tests that
// need the UUID for subsequent operations (e.g. admin reset).
func seed2FAUserReturnID(ctx context.Context, t *testing.T, username, password, role, emailAddr string) [16]byte {
	t.Helper()
	pool := internaldb.IntegrationPool(t)

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("seed2FAUserReturnID: HashPassword: %v", err)
	}
	var id [16]byte
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role, email) VALUES ($1, $2, $3, $4) RETURNING id`,
		username, hash, role, emailAddr,
	).Scan(&id); err != nil {
		t.Fatalf("seed2FAUserReturnID %q: %v", username, err)
	}
	return id
}

// extractOTPFromMailpit waits for an OTP email to arrive in Mailpit addressed
// to toAddr and extracts the 6-digit code from the body.
func extractOTPFromMailpit(t *testing.T, apiBase, toAddr string) string {
	t.Helper()
	msg := testutil.WaitForMessage(t, apiBase, toAddr, "Your Valory verification code")
	body := testutil.GetMessageBody(t, apiBase, msg.ID)
	otp := extractOTPFromBody(body)
	if otp == "" {
		t.Fatalf("extractOTPFromMailpit: could not extract 6-digit OTP from body: %q", body)
	}
	return otp
}

// ---------------------------------------------------------------------------
// (a) Happy path: full OTP journey via HTTP
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-015", "REQ-AUTH-016", "REQ-AUTH-024"]}
func TestInteg2FA_HappyPath_FullOTPJourney(t *testing.T) {
	// Requires both the integration DB and Mailpit.
	internaldb.IntegrationPool(t)
	testutil.CheckMailpitReachable(t)

	internaldb.TruncateTables(t, internaldb.IntegrationPool(t), twoFAIntegTables...)
	ctx := context.Background()
	seedSMTPConfig(ctx, t, true)
	t.Cleanup(func() { resetSystemConfigDefaults(ctx, t) })

	apiBase := testutil.MailpitAPIBase()
	testutil.ClearMessages(t, apiBase)

	seed2FAUser(ctx, t, "happy_student", "password1", "student", "happy@valory.test")

	r, _ := twoFAHandlerStack(t, false)

	// Phase 1: POST /login → 202 Accepted + pending_token.
	code, body := doLogin(r, "happy_student", "password1")
	if code != http.StatusAccepted {
		t.Fatalf("phase-1 login: expected 202, got %d; body=%v", code, body)
	}
	if body["two_factor_required"] != true {
		t.Errorf("expected two_factor_required=true, got %v", body["two_factor_required"])
	}
	pendingToken, ok := body["pending_token"].(string)
	if !ok || pendingToken == "" {
		t.Fatal("expected non-empty pending_token in 202 response")
	}

	// Assert the pending token is NOT a valid session (REQ-AUTH-015).
	if sc := doGetSession(r, pendingToken); sc != http.StatusUnauthorized {
		t.Errorf("pending token must be rejected by /session: expected 401, got %d", sc)
	}

	// Wait for the OTP email in Mailpit, extract the code.
	otp := extractOTPFromMailpit(t, apiBase, "happy@valory.test")

	// Phase 2: POST /2fa/verify with the correct OTP → 200 + session.
	code, body = doVerify(r, pendingToken, otp)
	if code != http.StatusOK {
		t.Fatalf("phase-2 verify: expected 200, got %d; body=%v", code, body)
	}
	sessionToken, ok := body["token"].(string)
	if !ok || sessionToken == "" {
		t.Fatal("expected non-empty token in verify 200 response")
	}
	if body["role"] == "" {
		t.Error("expected non-empty role in verify response")
	}

	// The full session token must reach GET /session (auth middleware accepts it).
	if sc := doGetSession(r, sessionToken); sc != http.StatusOK {
		t.Errorf("session token must be accepted by /session: expected 200, got %d", sc)
	}
}

// ---------------------------------------------------------------------------
// (b) Wrong-code: 5 wrong OTPs → locked; 6th attempt returns 401 (no row)
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-017", "REQ-AUTH-025"]}
func TestInteg2FA_WrongCode_FiveAttemptsLocksPending(t *testing.T) {
	internaldb.IntegrationPool(t)
	testutil.CheckMailpitReachable(t)

	internaldb.TruncateTables(t, internaldb.IntegrationPool(t), twoFAIntegTables...)
	ctx := context.Background()
	seedSMTPConfig(ctx, t, true)
	t.Cleanup(func() { resetSystemConfigDefaults(ctx, t) })

	testutil.ClearMessages(t, testutil.MailpitAPIBase())

	seed2FAUser(ctx, t, "wrong_student", "password2", "student", "wrong@valory.test")

	r, _ := twoFAHandlerStack(t, false)

	code, body := doLogin(r, "wrong_student", "password2")
	if code != http.StatusAccepted {
		t.Fatalf("login: expected 202, got %d", code)
	}
	pendingToken := body["pending_token"].(string)

	// Consume the OTP email (so Mailpit inbox is clean) but we won't use the code.
	extractOTPFromMailpit(t, testutil.MailpitAPIBase(), "wrong@valory.test")

	const wrongOTP = "000000"

	// 4 wrong attempts: each must return 401 with attempts_remaining.
	for i := 0; i < 4; i++ {
		code, body = doVerify(r, pendingToken, wrongOTP)
		if code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i+1, code)
		}
		remaining, hasRemaining := body["attempts_remaining"]
		if !hasRemaining {
			t.Errorf("attempt %d: expected attempts_remaining in body, got %v", i+1, body)
		}
		// Remaining should decrease from 4 to 1 over attempts 1–4.
		wantRemaining := float64(4 - i)
		if remaining != wantRemaining {
			t.Errorf("attempt %d: expected attempts_remaining=%v, got %v", i+1, wantRemaining, remaining)
		}
	}

	// 5th wrong attempt: too_many_attempts, pending row deleted.
	code, body = doVerify(r, pendingToken, wrongOTP)
	if code != http.StatusUnauthorized {
		t.Fatalf("5th attempt: expected 401, got %d", code)
	}
	if body["error"] != "too_many_attempts" {
		t.Errorf("5th attempt: expected error=too_many_attempts, got %v", body["error"])
	}

	// 6th attempt: row is gone; must return 401 (no row) regardless of OTP value.
	code, body = doVerify(r, pendingToken, wrongOTP)
	if code != http.StatusUnauthorized {
		t.Fatalf("6th attempt: expected 401, got %d", code)
	}
	// The row is gone; error is opaque "invalid or expired code" (not too_many_attempts).
	if body["error"] == "too_many_attempts" {
		t.Error("6th attempt: row should be gone; error must not be too_many_attempts")
	}
}

// ---------------------------------------------------------------------------
// (b2) Admin 2FA reset clears locked state; user can complete a new journey
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-019", "REQ-AUTH-025"]}
func TestInteg2FA_AdminReset_ClearsPendingAfterCap(t *testing.T) {
	internaldb.IntegrationPool(t)
	testutil.CheckMailpitReachable(t)

	pool := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, pool, twoFAIntegTables...)
	ctx := context.Background()
	seedSMTPConfig(ctx, t, true)
	t.Cleanup(func() { resetSystemConfigDefaults(ctx, t) })

	apiBase := testutil.MailpitAPIBase()
	testutil.ClearMessages(t, apiBase)

	// Create the student whose pending state will be locked.
	userID := seed2FAUserReturnID(ctx, t, "reset_student", "password3", "student", "reset@valory.test")
	// Create an admin (needs email for the admin reset route).
	seed2FAUser(ctx, t, "reset_admin", "adminpass", "admin", "resetadm@valory.test")

	r, _ := twoFAHandlerStack(t, false)

	// Student logs in → 202.
	code, body := doLogin(r, "reset_student", "password3")
	if code != http.StatusAccepted {
		t.Fatalf("login: expected 202, got %d", code)
	}
	pendingToken := body["pending_token"].(string)

	// Drain the OTP email from Mailpit (keeps the inbox clean for the next step).
	extractOTPFromMailpit(t, apiBase, "reset@valory.test")

	// Hit the attempt cap with 5 wrong codes.
	const wrongOTP = "000000"
	for i := 0; i < 4; i++ {
		doVerify(r, pendingToken, wrongOTP) //nolint:errcheck
	}
	code, _ = doVerify(r, pendingToken, wrongOTP)
	if code != http.StatusUnauthorized {
		t.Fatalf("5th wrong attempt: expected 401, got %d", code)
	}

	// Admin logs in (break-glass env NOT set, but admin has email so 2FA is
	// required — log in via a separate direct repo call to get the admin session
	// without going through 2FA, since the admin user also has email).
	// To avoid chicken-and-egg, issue an admin session via direct DB insertion.
	adminRepo := NewRepository(pool)
	adminUser, err := adminRepo.GetUserByUsername(ctx, "reset_admin")
	if err != nil {
		t.Fatalf("get admin user: %v", err)
	}
	rawAdminToken, adminTokenHash, adminExpiry, err := IssueToken(24 * time.Hour)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	if _, err := adminRepo.CreateSession(ctx, adminUser.ID, adminTokenHash, "admin", adminExpiry, false); err != nil {
		t.Fatalf("CreateSession for admin: %v", err)
	}

	// Admin calls DELETE /users/{id}/2fa/reset.
	targetID := uuid.UUID(userID)
	resetReq := httptest.NewRequest(http.MethodDelete,
		"/users/"+targetID.String()+"/2fa/reset", nil)
	resetReq.Header.Set("Authorization", "Bearer "+rawAdminToken)
	resetRR := httptest.NewRecorder()
	r.ServeHTTP(resetRR, resetReq)

	if resetRR.Code != http.StatusNoContent {
		t.Fatalf("admin reset: expected 204, got %d: %s", resetRR.Code, resetRR.Body.String())
	}

	// Student can now restart login and receive a new OTP.
	testutil.ClearMessages(t, apiBase)
	code, body = doLogin(r, "reset_student", "password3")
	if code != http.StatusAccepted {
		t.Fatalf("post-reset login: expected 202, got %d", code)
	}
	newPendingToken := body["pending_token"].(string)

	otp := extractOTPFromMailpit(t, apiBase, "reset@valory.test")

	code, _ = doVerify(r, newPendingToken, otp)
	if code != http.StatusOK {
		t.Fatalf("post-reset verify: expected 200, got %d", code)
	}
}

// ---------------------------------------------------------------------------
// (c) Resend throttle: second resend within 60 s → 429; old code no longer works
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-026"]}
func TestInteg2FA_Resend_ThrottledWithin60s(t *testing.T) {
	internaldb.IntegrationPool(t)
	testutil.CheckMailpitReachable(t)

	pool := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, pool, twoFAIntegTables...)
	ctx := context.Background()
	seedSMTPConfig(ctx, t, true)
	t.Cleanup(func() { resetSystemConfigDefaults(ctx, t) })

	apiBase := testutil.MailpitAPIBase()
	testutil.ClearMessages(t, apiBase)

	seed2FAUser(ctx, t, "resend_student", "password4", "student", "resend@valory.test")

	r, _ := twoFAHandlerStack(t, false)

	// Login to get a pending token.
	code, body := doLogin(r, "resend_student", "password4")
	if code != http.StatusAccepted {
		t.Fatalf("login: expected 202, got %d", code)
	}
	pendingToken := body["pending_token"].(string)

	// Capture the initial OTP.
	initialOTP := extractOTPFromMailpit(t, apiBase, "resend@valory.test")

	// First resend: should succeed (200).
	testutil.ClearMessages(t, apiBase)
	code, body = doResend(r, pendingToken)
	if code != http.StatusOK {
		t.Fatalf("first resend: expected 200, got %d; body=%v", code, body)
	}

	// Capture the new OTP delivered by the first resend.
	newOTP := extractOTPFromMailpit(t, apiBase, "resend@valory.test")

	// Verify the old OTP no longer works (it was replaced by the resend).
	if newOTP == initialOTP {
		t.Log("note: initial OTP and new OTP are coincidentally equal (statistically rare; test still valid)")
	}
	if initialOTP != newOTP {
		code, errBody := doVerify(r, pendingToken, initialOTP)
		if code == http.StatusOK {
			t.Error("old OTP must not verify after a resend (new OTP replaced it)")
		}
		_ = errBody
	}

	// Second resend within 60 s: must return 429 with resend_available_at.
	code, body = doResend(r, pendingToken)
	if code != http.StatusTooManyRequests {
		t.Fatalf("second resend within 60 s: expected 429, got %d; body=%v", code, body)
	}
	if body["error"] != "resend_throttled" {
		t.Errorf("expected error=resend_throttled, got %v", body["error"])
	}
	if body["resend_available_at"] == nil {
		t.Error("expected resend_available_at in throttle response body")
	}
}

// ---------------------------------------------------------------------------
// (e) Break-glass: VALORY_2FA_BREAK_GLASS set → admin gets 200 directly;
//     wrong password still 401; audit row present
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-020"]}
func TestInteg2FA_BreakGlass_AdminGetsSessionDirectly(t *testing.T) {
	internaldb.IntegrationPool(t)
	testutil.CheckMailpitReachable(t)

	pool := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, pool, twoFAIntegTables...)
	ctx := context.Background()
	seedSMTPConfig(ctx, t, true)
	t.Cleanup(func() { resetSystemConfigDefaults(ctx, t) })

	testutil.ClearMessages(t, testutil.MailpitAPIBase())

	seed2FAUser(ctx, t, "bg_admin2", "bgpass", "admin", "bgadmin@valory.test")

	// t.Setenv ensures the env var is unset after the test (no cross-test leakage).
	t.Setenv("VALORY_2FA_BREAK_GLASS", "active")

	r, _ := twoFAHandlerStack(t, false)

	// Admin login with correct password → 200 full session (break-glass).
	code, body := doLogin(r, "bg_admin2", "bgpass")
	if code != http.StatusOK {
		t.Fatalf("break-glass admin login: expected 200, got %d; body=%v", code, body)
	}
	if body["two_factor_required"] == true {
		t.Error("break-glass must not return two_factor_required=true")
	}
	sessionToken, ok := body["token"].(string)
	if !ok || sessionToken == "" {
		t.Fatal("expected non-empty session token in break-glass response")
	}

	// Session must be valid.
	if sc := doGetSession(r, sessionToken); sc != http.StatusOK {
		t.Errorf("break-glass session token: expected /session 200, got %d", sc)
	}

	// Break-glass must NOT bypass the password check — wrong password → 401.
	code, _ = doLogin(r, "bg_admin2", "wrongpassword")
	if code != http.StatusUnauthorized {
		t.Errorf("break-glass with wrong password: expected 401, got %d", code)
	}

	// Audit row for 2fa.break_glass_used must exist.
	auditRepo := audit.NewRepository(pool)
	rows, err := auditRepo.ListPaginated(ctx, 50, 0)
	if err != nil {
		t.Fatalf("ListPaginated: %v", err)
	}
	found := false
	for _, row := range rows {
		if row.Action == "2fa.break_glass_used" {
			found = true
			// Audit payload must contain the username, not any token or OTP.
			if strings.Contains(row.PayloadJSON, "bgpass") {
				t.Errorf("break_glass_used audit payload must not contain the password: %s", row.PayloadJSON)
			}
		}
	}
	if !found {
		t.Error("expected audit row with action=2fa.break_glass_used after break-glass login")
	}

	// No OTP email must have been sent to Mailpit.
	msgs, msgErr := countTwoFAMailpitMessages(testutil.MailpitAPIBase())
	if msgErr != nil {
		t.Fatalf("count mailpit messages: %v", msgErr)
	}
	if msgs != 0 {
		t.Errorf("break-glass must not send any OTP emails; Mailpit has %d messages", msgs)
	}
}

// @{"verifies": ["REQ-AUTH-020"]}
func TestInteg2FA_BreakGlass_StudentStillRequires2FA(t *testing.T) {
	internaldb.IntegrationPool(t)
	testutil.CheckMailpitReachable(t)

	pool := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, pool, twoFAIntegTables...)
	ctx := context.Background()
	seedSMTPConfig(ctx, t, true)
	t.Cleanup(func() { resetSystemConfigDefaults(ctx, t) })

	testutil.ClearMessages(t, testutil.MailpitAPIBase())

	seed2FAUser(ctx, t, "bg_student2", "bgstpass", "student", "bgstudent@valory.test")

	t.Setenv("VALORY_2FA_BREAK_GLASS", "active")

	r, _ := twoFAHandlerStack(t, false)

	// Student login with break-glass set: break-glass is admin-only, so student
	// must still get 202 + pending_token.
	code, body := doLogin(r, "bg_student2", "bgstpass")
	if code != http.StatusAccepted {
		t.Fatalf("student login with break-glass: expected 202, got %d; body=%v", code, body)
	}
	if body["two_factor_required"] != true {
		t.Error("student must still get two_factor_required=true even with break-glass set")
	}
}

// ---------------------------------------------------------------------------
// (f) 2FA-OFF regression: with two_factor_enabled=false, login returns 200
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-015"]}
func TestInteg2FA_Off_LoginReturnsNormalSession(t *testing.T) {
	pool := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, pool, twoFAIntegTables...)
	ctx := context.Background()
	t.Cleanup(func() { resetSystemConfigDefaults(ctx, t) })

	seed2FAUser(ctx, t, "off_student", "offpass", "student", "off@valory.test")

	// Build the handler stack with 2FA DISABLED.
	r, _ := twoFAHandlerStack(t, true /* disabled=true */)

	code, body := doLogin(r, "off_student", "offpass")
	if code != http.StatusOK {
		t.Fatalf("2FA-off login: expected 200, got %d; body=%v", code, body)
	}
	if body["two_factor_required"] == true {
		t.Error("2FA-off: must not return two_factor_required=true")
	}
	token, ok := body["token"].(string)
	if !ok || token == "" {
		t.Fatal("2FA-off: expected non-empty session token in 200 response")
	}
	if body["role"] == "" {
		t.Error("2FA-off: expected non-empty role")
	}
}

// ---------------------------------------------------------------------------
// (g) Audit hygiene: no OTP value in any audit_log payload after a journey
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-022"]}
func TestInteg2FA_AuditHygiene_NoOTPInPayloads(t *testing.T) {
	internaldb.IntegrationPool(t)
	testutil.CheckMailpitReachable(t)

	pool := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, pool, twoFAIntegTables...)
	ctx := context.Background()
	seedSMTPConfig(ctx, t, true)
	t.Cleanup(func() { resetSystemConfigDefaults(ctx, t) })

	apiBase := testutil.MailpitAPIBase()
	testutil.ClearMessages(t, apiBase)

	seed2FAUser(ctx, t, "audit_student", "auditpass", "student", "audit@valory.test")

	r, _ := twoFAHandlerStack(t, false)

	// Full 2FA journey so audit rows are written.
	code, body := doLogin(r, "audit_student", "auditpass")
	if code != http.StatusAccepted {
		t.Fatalf("login: expected 202, got %d", code)
	}
	pendingToken := body["pending_token"].(string)

	otp := extractOTPFromMailpit(t, apiBase, "audit@valory.test")

	code, _ = doVerify(r, pendingToken, otp)
	if code != http.StatusOK {
		t.Fatalf("verify: expected 200, got %d", code)
	}

	// Query all audit rows and assert none contain the plain OTP value.
	auditRepo := audit.NewRepository(pool)
	rows, err := auditRepo.ListPaginated(ctx, 200, 0)
	if err != nil {
		t.Fatalf("ListPaginated: %v", err)
	}
	for _, row := range rows {
		if strings.Contains(row.PayloadJSON, otp) {
			t.Errorf("audit_log row %d has action=%q and contains raw OTP %q in payload: %s",
				row.ID, row.Action, otp, row.PayloadJSON)
		}
	}
}

// ---------------------------------------------------------------------------
// (h) Pending token rejected by auth middleware at /session
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-015"]}
func TestInteg2FA_PendingToken_RejectedByAuthMiddleware(t *testing.T) {
	internaldb.IntegrationPool(t)
	testutil.CheckMailpitReachable(t)

	pool := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, pool, twoFAIntegTables...)
	ctx := context.Background()
	seedSMTPConfig(ctx, t, true)
	t.Cleanup(func() { resetSystemConfigDefaults(ctx, t) })

	testutil.ClearMessages(t, testutil.MailpitAPIBase())

	seed2FAUser(ctx, t, "reject_student", "rejectpass", "student", "reject@valory.test")

	r, _ := twoFAHandlerStack(t, false)

	// Login to get the pending token.
	code, body := doLogin(r, "reject_student", "rejectpass")
	if code != http.StatusAccepted {
		t.Fatalf("login: expected 202, got %d", code)
	}
	pendingToken := body["pending_token"].(string)

	// The pending token must not pass the auth middleware for /session.
	if sc := doGetSession(r, pendingToken); sc != http.StatusUnauthorized {
		t.Errorf("pending token at /session: expected 401, got %d", sc)
	}

	// Also try it as a cookie — the middleware rejects non-session tokens regardless.
	req := httptest.NewRequest(http.MethodGet, "/session", nil)
	req.AddCookie(&http.Cookie{Name: "__Host-session", Value: pendingToken})
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("pending token as cookie at /session: expected 401, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// countTwoFAMailpitMessages is a helper that returns the total message count.
// ---------------------------------------------------------------------------

func countTwoFAMailpitMessages(apiBase string) (int, error) {
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

	var result struct {
		Total int `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	return result.Total, nil
}
