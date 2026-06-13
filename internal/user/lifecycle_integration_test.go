//go:build integration

// lifecycle_integration_test.go — HTTP-handler-level integration tests for the
// admin-created account lifecycle (Sprint 20).
//
// These tests exercise the complete HTTP journey at the handler level against a
// real PostgreSQL instance and a real Mailpit SMTP sink. They use the same
// integration harness as the rest of the user module (db.IntegrationPool,
// testutil.CheckMailpitReachable) and follow the patterns established by
// internal/admin/email_integration_test.go.
//
// Journey assertions tested:
//
//  1. Admin creates user with generate_password=true →
//     welcome email ARRIVES in Mailpit containing username + temp password +
//     change instructions.
//
//  2. Login with the extracted temp password succeeds and the response carries
//     must_change_password:true.
//
//  3. A non-exempt API call from that session returns 403 MUST_CHANGE_PASSWORD.
//
//  4. POST /users/me/password with the temp password as current_password → 204.
//
//  5. Flag is cleared: GET /users/me returns must_change_password:false.
//
//  6. All other sessions for the user are invalidated (a second session created
//     before the change is confirmed dead after the change).
//
//  7. Re-login with the new password succeeds; login with the old temp password
//     fails (403 / 401).
//
//  8. Resend-welcome regenerates the temp password: the new email arrives in
//     Mailpit with a different body; the original temp password is dead; the
//     audit row for user.resend_welcome is credential-free.
//
//  9. Unconfigured-mailer path: creation with a NoOpMailer returns 201 +
//     email_sent:false + temp_password in the response body + the
//     user.create_temp audit row contains no password.
//
// Run via:
//
//	make test-integration
//	# or manually:
//	export PATH=$PATH:/usr/local/go/bin
//	go test -tags integration -count=1 -p 1 -run TestIntegLifecycle ./internal/user/
//
// Env vars:
//
//	VALORY_TEST_DATABASE_URL — PostgreSQL DSN (defaults to docker-compose test service)
//	MAILPIT_API              — Mailpit REST API base (default http://localhost:18025)
package user

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	emailpkg "github.com/valory/valory/internal/email"
	"github.com/valory/valory/internal/testutil"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// integLifecycleTruncate resets all tables touched by the lifecycle integration
// tests and re-seeds the system_config rows that TRUNCATE CASCADE removes.
// Mirrors integTruncate in integration_test.go but lives here so the
// //go:build integration tag is isolated to this file.
func integLifecycleTruncate(t *testing.T) {
	t.Helper()
	p := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, p,
		"audit_log",
		"student_consent",
		"password_reset_attempts",
		"password_reset_tokens",
		"login_attempts",
		"sessions",
		"users",
	)

	ctx := context.Background()
	_, err := p.Exec(ctx, `
		INSERT INTO system_config (key, value) VALUES
		    ('agent_retry_limit',                  '3'),
		    ('correction_loop_max_iterations',     '5'),
		    ('per_student_token_limit',            '500000'),
		    ('late_penalty_rate',                  '0.05'),
		    ('homework_weight',                    '0.7'),
		    ('project_weight',                     '0.3'),
		    ('session_inactivity_seconds',         '1800'),
		    ('account_lockout_seconds',            '900'),
		    ('max_upload_bytes',                   '10485760'),
		    ('content_generation_timeout_seconds', '300'),
		    ('audit_retention_days',               '365'),
		    ('notification_retention_days',        '90'),
		    ('consent_version',                    '1.0')
		ON CONFLICT (key) DO NOTHING
	`)
	if err != nil {
		t.Fatalf("integLifecycleTruncate: re-seed system_config: %v", err)
	}
}

// mailpitMailer builds a real SMTPMailer that delivers to the Mailpit SMTP sink
// used by the test environment. The encryption mode is "none" — Mailpit accepts
// unauthenticated plain-TCP connections (no TLS, no SMTP AUTH).
//
// This mirrors mailpitSMTPEnv in internal/admin/email_integration_test.go.
func mailpitMailer() *emailpkg.SMTPMailer {
	env := emailpkg.SMTPEnvVars{
		Host:       "localhost",
		Port:       11025,
		From:       "test@valory.test",
		Username:   "", // Mailpit needs no auth (REQ-EMAIL-003)
		Encryption: "none",
	}
	// The stubbed ConfigLoader returns empty strings for all keys, meaning the
	// env-var values are used as-is (highest precedence falls to the env vars
	// when admin config is empty).
	return emailpkg.NewSMTPMailer(&emptyLifecycleConfig{}, &emptyLifecycleSecrets{}, env)
}

// emptyLifecycleConfig implements emailpkg.ConfigLoader with no stored values.
// The SMTPMailer falls through to env-var values for every key.
type emptyLifecycleConfig struct{}

func (e *emptyLifecycleConfig) GetString(_ string) string { return "" }

// emptyLifecycleSecrets implements emailpkg.SecretGetter with no secrets.
// Mailpit requires no password, so an empty return is correct.
type emptyLifecycleSecrets struct{}

func (e *emptyLifecycleSecrets) Get(_ context.Context, _ string) string { return "" }

// buildLifecycleStack constructs the full HTTP router for the lifecycle
// integration tests. The stack mirrors what cmd/server/main.go wires up but is
// deliberately minimal: only the auth middleware and the user handler routes
// required by the journey assertions.
//
// The returned router layout:
//
//	POST   /api/v1/auth/login
//	POST   /api/v1/auth/logout
//	POST   /api/v1/users                    ← createUser (admin-only)
//	POST   /api/v1/users/{id}/resend-welcome (admin-only)
//	GET    /api/v1/users/me                 ← getCurrentUser (authenticated)
//	POST   /api/v1/users/me/password        ← changePassword (authenticated)
//	GET    /api/v1/courses                  ← stub 200 — any non-exempt route
func buildLifecycleStack(t *testing.T, mailer emailpkg.Mailer) http.Handler {
	t.Helper()

	pool := internaldb.IntegrationPool(t)

	authRepo := authpkg.NewRepository(pool)
	authSvc := authpkg.NewService(authRepo, 15*time.Minute, 24*time.Hour)
	authHandler := authpkg.NewHandlerFull(
		authSvc,
		authRepo,
		pool,
		24*time.Hour,
		nil, // no consent provider needed for lifecycle tests
	)

	userSvc := NewService(
		pool,
		NewRepository(pool),
		audit.NewRepository(pool),
		&noopEmail{}, // password-reset email transport (unused in lifecycle tests)
		1*time.Hour,
		&noopTerminator{},
	)
	userSvc.SetMailer(mailer)
	userHandler := NewHandler(userSvc)

	authMiddleware := authpkg.NewAuthMiddleware(
		authRepo,
		pool,
		24*time.Hour,
		nil,
	)
	mustChangeMW := authpkg.MustChangePasswordMiddleware(pool)

	r := chi.NewRouter()

	// Auth routes (unauthenticated).
	r.Route("/api/v1/auth", func(r chi.Router) {
		authHandler.Routes(r)
	})

	// Authenticated routes — apply auth middleware + must-change-password guard.
	r.Group(func(r chi.Router) {
		r.Use(authMiddleware)
		r.Use(mustChangeMW)

		// Mirror production exactly (cmd/server/main.go): both MeRoutes and the
		// admin group are nested under /api/v1/users, so admin user actions live
		// at /api/v1/users (not /api/v1/admin/users). The earlier test-only
		// /api/v1/admin/users mount masked a real frontend URL mismatch.
		r.Route("/api/v1/users", func(r chi.Router) {
			userHandler.MeRoutes(r)
			r.Group(func(r chi.Router) {
				r.Use(authpkg.RequireRole("admin"))
				userHandler.AdminRoutes(r)
			})
		})

		// A generic protected route to verify the must-change-password block.
		r.Get("/api/v1/courses", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	})

	return r
}

// doJSON performs a JSON request against the given handler and returns the
// response recorder. bearerToken may be empty for unauthenticated requests.
func doJSON(t *testing.T, h http.Handler, method, path string, body interface{}, bearerToken string) *httptest.ResponseRecorder {
	t.Helper()

	var bodyReader io.Reader = http.NoBody
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("doJSON marshal: %v", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// loginInteg performs a POST /api/v1/auth/login and returns the raw token from
// the response. The test fatals on any error or non-200 response.
func loginInteg(t *testing.T, h http.Handler, username, password string) (rawToken string, mustChange bool) {
	t.Helper()

	rr := doJSON(t, h, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": username,
		"password": password,
	}, "")

	if rr.Code != http.StatusOK {
		t.Fatalf("loginInteg: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("loginInteg decode: %v", err)
	}

	tok, _ := resp["token"].(string)
	if tok == "" {
		t.Fatalf("loginInteg: token absent in response: %v", resp)
	}
	mc, _ := resp["must_change_password"].(bool)
	return tok, mc
}

// createAdminInteg inserts an admin account directly (bypassing the handler) so
// the lifecycle journey can authenticate as an admin without bootstrapping a
// second admin account through the API — there is no public admin-creation endpoint.
func createAdminInteg(ctx context.Context, t *testing.T) (uuid.UUID, string, string) {
	t.Helper()

	pool := internaldb.IntegrationPool(t)
	username := fmt.Sprintf("lifecycle_admin_%s", uuid.New().String()[:8])
	password := "AdminPass!2026"

	hash, err := authpkg.HashPassword(password)
	if err != nil {
		t.Fatalf("createAdminInteg hash: %v", err)
	}

	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, 'admin') RETURNING id`,
		username, hash,
	).Scan(&id); err != nil {
		t.Fatalf("createAdminInteg insert: %v", err)
	}

	return id, username, password
}

// extractTempPasswordInteg parses the cleartext temp password out of a welcome
// email body. The email body written by sendWelcomeEmail contains:
//
//	Temporary password: <PWD>
func extractTempPasswordInteg(t *testing.T, body string) string {
	t.Helper()
	const prefix = "Temporary password: "
	idx := strings.Index(body, prefix)
	if idx < 0 {
		t.Fatalf("extractTempPasswordInteg: prefix %q not found in body: %q", prefix, body)
	}
	rest := body[idx+len(prefix):]
	end := strings.IndexAny(rest, "\r\n")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// ── Full Mailpit journey ───────────────────────────────────────────────────────

// @{"verifies": ["REQ-USER-008", "REQ-USER-009", "REQ-USER-010", "REQ-USER-011", "REQ-USER-012", "REQ-USER-013", "REQ-USER-014", "REQ-USER-015", "REQ-USER-016", "REQ-USER-017", "REQ-USER-018", "REQ-USER-019", "REQ-AUTH-013", "REQ-AUTH-014"]}
func TestIntegLifecycle_FullJourney_WithMailpit(t *testing.T) {
	// Full-stack lifecycle journey: admin creates user → welcome email in Mailpit
	// → login with temp password (must_change_password:true) → blocked on non-
	// exempt route → change password → flag cleared → other session dead →
	// new password works / old temp fails.

	internaldb.IntegrationPool(t)     // ensures DB is ready; skips if not
	testutil.CheckMailpitReachable(t) // skips if Mailpit is not running

	integLifecycleTruncate(t)

	apiBase := testutil.MailpitAPIBase()
	testutil.ClearMessages(t, apiBase)

	ctx := context.Background()

	// Build the full HTTP stack using the real Mailpit mailer.
	h := buildLifecycleStack(t, mailpitMailer())

	// ── Step 1: admin creates user with generate_password=true ────────────────

	_, adminUsername, adminPassword := createAdminInteg(ctx, t)
	adminToken, _ := loginInteg(t, h, adminUsername, adminPassword)

	studentEmail := fmt.Sprintf("lifecycle_%s@example.com", uuid.New().String()[:8])
	studentUsername := fmt.Sprintf("lifecycle_stu_%s", uuid.New().String()[:8])

	rr := doJSON(t, h, http.MethodPost, "/api/v1/users", map[string]interface{}{
		"username":          studentUsername,
		"email":             studentEmail,
		"role":              "student",
		"generate_password": true,
	}, adminToken)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create user: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var createResp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&createResp); err != nil {
		t.Fatalf("create user decode: %v", err)
	}

	// REQ-USER-009: must_change_password must be true in the creation response.
	if createResp["must_change_password"] != true {
		t.Errorf("REQ-USER-009: expected must_change_password=true in create response, got %v",
			createResp["must_change_password"])
	}

	// REQ-USER-010: email_sent must be true (Mailpit is configured and running).
	if createResp["email_sent"] != true {
		t.Errorf("REQ-USER-010: expected email_sent=true (Mailpit is running), got %v",
			createResp["email_sent"])
	}
	// REQ-USER-010/011: temp_password must NOT be present when email was sent.
	if _, hasTempPwd := createResp["temp_password"]; hasTempPwd {
		t.Error("REQ-USER-010: temp_password must not be in response when email_sent=true")
	}

	// ── Step 2: welcome email arrives in Mailpit with username + temp password ─

	// REQ-USER-010: welcome email arrives at the student's address.
	msg := testutil.WaitForMessage(t, apiBase, studentEmail, "Your Valory account credentials")

	if msg.From.Address != "test@valory.test" {
		t.Errorf("welcome email From: got %q, want test@valory.test", msg.From.Address)
	}

	// Parse the temp password out of the email body.
	body := testutil.GetMessageBody(t, apiBase, msg.ID)
	if body == "" {
		t.Fatal("welcome email body is empty")
	}

	// The body must contain the student's username.
	if !strings.Contains(body, studentUsername) {
		t.Errorf("welcome email body must contain username %q, got: %q", studentUsername, body)
	}
	// The body must contain change instructions.
	if !strings.Contains(body, "change your password") {
		t.Errorf("welcome email body must contain change instructions, got: %q", body)
	}

	tempPassword := extractTempPasswordInteg(t, body)
	if len(tempPassword) != tempPasswordLen {
		t.Errorf("extracted temp password has unexpected length: got %d, want %d (value=%q)",
			len(tempPassword), tempPasswordLen, tempPassword)
	}

	// ── Step 3: login with temp password → must_change_password:true ──────────

	// REQ-AUTH-013: login response must include must_change_password:true.
	studentToken, mustChange := loginInteg(t, h, studentUsername, tempPassword)
	if !mustChange {
		t.Error("REQ-AUTH-013: login with temp password must return must_change_password:true")
	}

	// ── Step 4: non-exempt API call returns 403 MUST_CHANGE_PASSWORD ──────────

	// REQ-AUTH-014: GET /courses is not exempt; must return 403 with error code.
	rrCourses := doJSON(t, h, http.MethodGet, "/api/v1/courses", nil, studentToken)
	if rrCourses.Code != http.StatusForbidden {
		t.Errorf("REQ-AUTH-014: expected 403 on non-exempt route, got %d", rrCourses.Code)
	}
	var blockedBody map[string]string
	if err := json.NewDecoder(rrCourses.Body).Decode(&blockedBody); err != nil {
		t.Fatalf("REQ-AUTH-014: decode blocked response: %v", err)
	}
	if blockedBody["error"] != "MUST_CHANGE_PASSWORD" {
		t.Errorf("REQ-AUTH-014: expected error=MUST_CHANGE_PASSWORD, got %q", blockedBody["error"])
	}

	// ── Step 5a: create a second session (to verify it is invalidated later) ──

	// Create a second session for the same student by logging in again.
	// This session will be used to verify REQ-USER-016: all other sessions die.
	secondToken, _ := loginInteg(t, h, studentUsername, tempPassword)

	// ── Step 5b: change password using the temp password as current ────────────

	newPassword := "NewSecure!2026"

	// REQ-USER-014: POST /users/me/password with correct current_password → 204.
	rrChange := doJSON(t, h, http.MethodPost, "/api/v1/users/me/password", map[string]string{
		"current_password": tempPassword,
		"new_password":     newPassword,
	}, studentToken)
	if rrChange.Code != http.StatusNoContent {
		t.Fatalf("REQ-USER-014: expected 204, got %d: %s", rrChange.Code, rrChange.Body.String())
	}

	// ── Step 6: must_change_password flag is cleared ───────────────────────────

	// REQ-USER-015: GET /users/me must return must_change_password:false.
	rrMe := doJSON(t, h, http.MethodGet, "/api/v1/users/me", nil, studentToken)
	if rrMe.Code != http.StatusOK {
		t.Fatalf("REQ-USER-015: expected 200 from GET /users/me, got %d: %s", rrMe.Code, rrMe.Body.String())
	}
	var meBody map[string]interface{}
	if err := json.NewDecoder(rrMe.Body).Decode(&meBody); err != nil {
		t.Fatalf("REQ-USER-015: decode /users/me: %v", err)
	}
	if meBody["must_change_password"] != false {
		t.Errorf("REQ-USER-015: expected must_change_password=false after change, got %v",
			meBody["must_change_password"])
	}

	// ── Step 7: second session is invalidated ─────────────────────────────────

	// REQ-USER-016: using the second session token after the password change must
	// fail. GET /users/me with the second token must return 401 (session deleted).
	rrSecond := doJSON(t, h, http.MethodGet, "/api/v1/users/me", nil, secondToken)
	if rrSecond.Code != http.StatusUnauthorized {
		t.Errorf("REQ-USER-016: expected 401 for second session after password change, got %d",
			rrSecond.Code)
	}

	// ── Step 8: re-login with new password works; old temp fails ──────────────

	// New password should work.
	newToken, newMustChange := loginInteg(t, h, studentUsername, newPassword)
	if newToken == "" {
		t.Fatal("REQ-USER-014: re-login with new password failed (empty token)")
	}
	if newMustChange {
		t.Error("re-login after password change must not set must_change_password=true")
	}

	// REQ-USER-013 (analogous — old credential dead): login with old temp password
	// must fail (401 — the hash was overwritten by ChangePassword).
	rrOldPwd := doJSON(t, h, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": studentUsername,
		"password": tempPassword,
	}, "")
	if rrOldPwd.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 when using old temp password after change, got %d", rrOldPwd.Code)
	}

	// ── Step 9: audit rows are credential-free ────────────────────────────────

	// REQ-USER-017: audit row for user.create_temp must not contain the temp password.
	// REQ-USER-019: audit row for user.password_change must not contain any password.
	pool := internaldb.IntegrationPool(t)
	rows, err := pool.Query(ctx,
		`SELECT action, payload::text FROM audit_log WHERE action IN ('user.create_temp', 'user.password_change')`)
	if err != nil {
		t.Fatalf("audit query: %v", err)
	}
	defer rows.Close()
	foundCreateTemp := false
	foundPasswordChange := false
	for rows.Next() {
		var action, payload string
		if err := rows.Scan(&action, &payload); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		switch action {
		case "user.create_temp":
			foundCreateTemp = true
			if strings.Contains(payload, tempPassword) {
				t.Errorf("REQ-USER-017: user.create_temp audit payload contains temp password")
			}
			if strings.Contains(payload, "password") && strings.Contains(payload, tempPassword) {
				t.Errorf("REQ-USER-017: password appears in audit payload: %s", payload)
			}
		case "user.password_change":
			foundPasswordChange = true
			if strings.Contains(payload, tempPassword) {
				t.Errorf("REQ-USER-019: user.password_change audit payload contains old password")
			}
			if strings.Contains(payload, newPassword) {
				t.Errorf("REQ-USER-019: user.password_change audit payload contains new password")
			}
		}
	}
	if !foundCreateTemp {
		t.Error("REQ-USER-017: expected user.create_temp audit entry")
	}
	if !foundPasswordChange {
		t.Error("REQ-USER-019: expected user.password_change audit entry")
	}

	// Silence newToken — used above to prove login with new password succeeds.
	_ = newToken
}

// ── Resend-welcome journey ─────────────────────────────────────────────────────

// @{"verifies": ["REQ-USER-012", "REQ-USER-013", "REQ-USER-018"]}
func TestIntegLifecycle_ResendWelcome_NewEmailArrivesOldPasswordDead(t *testing.T) {
	// Resend-welcome journey: admin creates user (temp password A) → admin
	// resends welcome (temp password B arrives in Mailpit) → login with A fails
	// → login with B succeeds → user.resend_welcome audit row is credential-free.

	internaldb.IntegrationPool(t)
	testutil.CheckMailpitReachable(t)

	integLifecycleTruncate(t)

	apiBase := testutil.MailpitAPIBase()
	testutil.ClearMessages(t, apiBase)

	ctx := context.Background()
	h := buildLifecycleStack(t, mailpitMailer())

	_, adminUsername, adminPassword := createAdminInteg(ctx, t)
	adminToken, _ := loginInteg(t, h, adminUsername, adminPassword)

	studentEmail := fmt.Sprintf("resend_%s@example.com", uuid.New().String()[:8])
	studentUsername := fmt.Sprintf("resend_stu_%s", uuid.New().String()[:8])

	// Step 1: create user → first welcome email.
	rrCreate := doJSON(t, h, http.MethodPost, "/api/v1/users", map[string]interface{}{
		"username":          studentUsername,
		"email":             studentEmail,
		"role":              "student",
		"generate_password": true,
	}, adminToken)
	if rrCreate.Code != http.StatusCreated {
		t.Fatalf("create user: expected 201, got %d: %s", rrCreate.Code, rrCreate.Body.String())
	}

	var createResp map[string]interface{}
	if err := json.NewDecoder(rrCreate.Body).Decode(&createResp); err != nil {
		t.Fatalf("create user decode: %v", err)
	}
	userIDStr, _ := createResp["id"].(string)
	if userIDStr == "" {
		t.Fatal("user ID absent in create response")
	}

	// Extract first temp password from the first welcome email.
	firstMsg := testutil.WaitForMessage(t, apiBase, studentEmail, "Your Valory account credentials")
	firstBody := testutil.GetMessageBody(t, apiBase, firstMsg.ID)
	firstTempPwd := extractTempPasswordInteg(t, firstBody)

	// Step 2: clear Mailpit before resend to isolate the second email.
	testutil.ClearMessages(t, apiBase)

	// Step 3: admin resends welcome.
	rrResend := doJSON(t, h, http.MethodPost,
		"/api/v1/users/"+userIDStr+"/resend-welcome", nil, adminToken)
	if rrResend.Code != http.StatusOK {
		t.Fatalf("resend-welcome: expected 200, got %d: %s", rrResend.Code, rrResend.Body.String())
	}

	var resendResp map[string]interface{}
	if err := json.NewDecoder(rrResend.Body).Decode(&resendResp); err != nil {
		t.Fatalf("resend-welcome decode: %v", err)
	}
	// REQ-USER-012: email_sent=true because Mailpit is running.
	if resendResp["email_sent"] != true {
		t.Errorf("REQ-USER-012: expected email_sent=true after resend, got %v", resendResp["email_sent"])
	}

	// Step 4: second welcome email arrives in Mailpit.
	secondMsg := testutil.WaitForMessage(t, apiBase, studentEmail, "Your Valory account credentials")
	secondBody := testutil.GetMessageBody(t, apiBase, secondMsg.ID)
	secondTempPwd := extractTempPasswordInteg(t, secondBody)

	// REQ-USER-013: second temp password must differ from the first.
	if firstTempPwd == secondTempPwd {
		t.Error("REQ-USER-013: resend must generate a different temp password (got same value)")
	}

	// Step 5: login with the old (first) temp password must fail.
	// REQ-USER-013: previous temp password is invalidated on resend.
	rrOldLogin := doJSON(t, h, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": studentUsername,
		"password": firstTempPwd,
	}, "")
	if rrOldLogin.Code != http.StatusUnauthorized {
		t.Errorf("REQ-USER-013: expected 401 for old temp password after resend, got %d",
			rrOldLogin.Code)
	}

	// Step 6: login with the new (second) temp password must succeed.
	_, secondMustChange := loginInteg(t, h, studentUsername, secondTempPwd)
	if !secondMustChange {
		t.Error("REQ-USER-009: login with new temp password must return must_change_password=true")
	}

	// Step 7: audit row for user.resend_welcome must be credential-free.
	// REQ-USER-018.
	pool := internaldb.IntegrationPool(t)
	rows, err := pool.Query(ctx,
		`SELECT payload::text FROM audit_log WHERE action = 'user.resend_welcome'`)
	if err != nil {
		t.Fatalf("audit query: %v", err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		found = true
		var payload string
		if err := rows.Scan(&payload); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if strings.Contains(payload, firstTempPwd) {
			t.Errorf("REQ-USER-018: resend audit payload contains first temp password")
		}
		if strings.Contains(payload, secondTempPwd) {
			t.Errorf("REQ-USER-018: resend audit payload contains second temp password")
		}
	}
	if !found {
		t.Error("REQ-USER-018: expected user.resend_welcome audit entry")
	}
}

// ── Unconfigured-mailer path ───────────────────────────────────────────────────

// @{"verifies": ["REQ-USER-008", "REQ-USER-009", "REQ-USER-011", "REQ-USER-017"]}
func TestIntegLifecycle_UnconfiguredMailer_TempPasswordInResponse(t *testing.T) {
	// When the mailer is not configured (NoOpMailer.IsConfigured() == false):
	//   • Account creation returns 201.
	//   • email_sent is false.
	//   • temp_password is present in the response (one-time admin display).
	//   • The user.create_temp audit row does not contain the password.
	//
	// This test does NOT require Mailpit — it uses the NoOpMailer and only needs
	// the database integration pool.

	internaldb.IntegrationPool(t) // skips if DB is unavailable

	integLifecycleTruncate(t)

	ctx := context.Background()

	// Build the stack with a NoOpMailer (IsConfigured()==false).
	h := buildLifecycleStack(t, &emailpkg.NoOpMailer{Out: io.Discard})

	_, adminUsername, adminPassword := createAdminInteg(ctx, t)
	adminToken, _ := loginInteg(t, h, adminUsername, adminPassword)

	studentEmail := fmt.Sprintf("noop_%s@example.com", uuid.New().String()[:8])
	studentUsername := fmt.Sprintf("noop_stu_%s", uuid.New().String()[:8])

	rr := doJSON(t, h, http.MethodPost, "/api/v1/users", map[string]interface{}{
		"username":          studentUsername,
		"email":             studentEmail,
		"role":              "student",
		"generate_password": true,
	}, adminToken)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// REQ-USER-011: email_sent must be false.
	if resp["email_sent"] != false {
		t.Errorf("REQ-USER-011: expected email_sent=false with unconfigured mailer, got %v",
			resp["email_sent"])
	}

	// REQ-USER-011: temp_password must be present in the response.
	tempPwd, hasTempPwd := resp["temp_password"].(string)
	if !hasTempPwd || tempPwd == "" {
		t.Errorf("REQ-USER-011: expected non-empty temp_password in response when email_sent=false, got %v",
			resp["temp_password"])
	}

	// REQ-USER-009: must_change_password must be true.
	if resp["must_change_password"] != true {
		t.Errorf("REQ-USER-009: expected must_change_password=true, got %v", resp["must_change_password"])
	}

	// REQ-USER-017: the user.create_temp audit row must not contain the temp password.
	pool := internaldb.IntegrationPool(t)
	rows, err := pool.Query(ctx,
		`SELECT payload::text FROM audit_log WHERE action = 'user.create_temp'`)
	if err != nil {
		t.Fatalf("audit query: %v", err)
	}
	defer rows.Close()
	foundAudit := false
	for rows.Next() {
		foundAudit = true
		var payload string
		if err := rows.Scan(&payload); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if strings.Contains(payload, tempPwd) {
			t.Errorf("REQ-USER-017: user.create_temp audit payload contains temp password: %s", payload)
		}
	}
	if !foundAudit {
		t.Error("REQ-USER-017: expected user.create_temp audit entry")
	}

	// Sanity: the temp password actually works for login (proves the hash stored
	// matches the cleartext returned in the response — no copy-paste error).
	_, mustChange := loginInteg(t, h, studentUsername, tempPwd)
	if !mustChange {
		t.Error("login with temp password from response must return must_change_password=true")
	}
}
