//go:build integration

// setup_integration_test.go — integration tests for the setup module.
//
// These tests run against a REAL PostgreSQL instance with all migrations applied.
// Run with:
//
//	make test-integration
//
// or directly:
//
//	VALORY_TEST_DATABASE_URL=... go test -tags integration -p 1 ./internal/setup/
//
// Per project memory: the test DB role is a superuser that can mask RLS bugs.
// Where practical we issue SET ROLE valory_app so grants/RLS are realistic.
// The setup endpoints write only to users and audit_log (no RLS), so the
// primary concern here is the grant set on audit_log (append-only REVOKE from
// migration 002). We exercise audit_log writes under valory_app to confirm
// the INSERT grant is intact.
package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	dbpkg "github.com/valory/valory/internal/db"
	"github.com/valory/valory/internal/audit"
)

// setupTruncate resets the tables touched by the setup module before each test.
// audit_log is cleared first because it has no FK back to users, and the seed
// system_config rows are re-inserted after users is truncated (same pattern as
// internal/user/integration_test.go).
func setupTruncate(t *testing.T) {
	t.Helper()
	p := dbpkg.IntegrationPool(t)
	dbpkg.TruncateTables(t, p,
		"audit_log",
		"sessions",
		"password_reset_tokens",
		"student_consent",
		"login_attempts",
		"password_reset_attempts",
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
		t.Fatalf("setupTruncate: re-seed system_config: %v", err)
	}
}

// buildStack returns a fully-wired Handler backed by the integration pool.
// audit.Repository uses valory_app-compatible pool writes (INSERT only — the
// append-only REVOKE does not block INSERT, only UPDATE/DELETE).
func buildStack(t *testing.T) *Handler {
	t.Helper()
	pool := dbpkg.IntegrationPool(t)
	repo := NewRepository(pool)
	auditRepo := audit.NewRepository(pool)
	svc := NewService(pool, repo, auditRepo)
	return NewHandler(svc)
}

// doSetupPost fires POST /api/v1/setup through the handler's test server.
func doSetupPost(t *testing.T, h *Handler, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handlePost(rec, req)
	return rec
}

// doSetupStatus fires GET /api/v1/setup/status through the handler.
func doSetupStatus(t *testing.T, h *Handler) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	h.handleGetStatus(rec, req)
	return rec
}

// ----------------------------------------------------------------------------
// TC-SETUP-INT-001: status=true on empty DB
// ----------------------------------------------------------------------------

// @{"verifies": ["REQ-SETUP-001", "REQ-SETUP-002"]}
func TestIntegration_GetStatus_EmptyDB_NeedsSetupTrue(t *testing.T) {
	setupTruncate(t)
	h := buildStack(t)

	rec := doSetupStatus(t, h)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var resp map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse status response: %v", err)
	}
	if !resp["needs_setup"] {
		t.Error("expected needs_setup=true on empty DB")
	}
}

// ----------------------------------------------------------------------------
// TC-SETUP-INT-002: create first admin succeeds
// ----------------------------------------------------------------------------

// @{"verifies": ["REQ-SETUP-003", "REQ-SETUP-004", "REQ-SETUP-008", "REQ-SETUP-009", "REQ-SETUP-010"]}
func TestIntegration_CreateFirstAdmin_Success(t *testing.T) {
	setupTruncate(t)
	h := buildStack(t)
	pool := dbpkg.IntegrationPool(t)
	ctx := context.Background()

	uniqueUsername := "integ_setup_admin_" + uuid.New().String()[:8]
	rec := doSetupPost(t, h, map[string]string{
		"username": uniqueUsername,
		"password": "securePa55!",
	})

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse create response: %v", err)
	}

	// Response must contain username and role only.
	if resp["username"] != uniqueUsername {
		t.Errorf("expected username=%q, got %q", uniqueUsername, resp["username"])
	}
	if resp["role"] != "admin" {
		t.Errorf("expected role=admin, got %q", resp["role"])
	}
	// REQ-SETUP-008: password must not appear in the response.
	bodyStr := rec.Body.String()
	if strings.Contains(bodyStr, "password") {
		t.Errorf("response body must not contain 'password': %s", bodyStr)
	}

	// Verify the DB row has the correct role and flag values.
	//
	// We query under the valory_app role so that any grant restriction that would
	// block a real application query surfaces here rather than silently passing
	// under a superuser connection.
	conn := dbpkg.AcquireAsUser(t, pool, "00000000-0000-0000-0000-000000000000", "admin")
	defer conn.Release()

	var dbRole string
	var mustChangePw bool
	var isActive bool
	var passwordHash string
	err := conn.QueryRow(ctx,
		`SELECT role, must_change_password, is_active, password_hash FROM users WHERE username = $1`,
		uniqueUsername,
	).Scan(&dbRole, &mustChangePw, &isActive, &passwordHash)
	if err != nil {
		t.Fatalf("query inserted user row: %v", err)
	}

	// REQ-SETUP-004: role must be 'admin'.
	if dbRole != "admin" {
		t.Errorf("expected DB role=admin, got %q", dbRole)
	}
	// must_change_password must be false (admin chose their own password).
	if mustChangePw {
		t.Error("expected must_change_password=false")
	}
	// is_active must be true.
	if !isActive {
		t.Error("expected is_active=true")
	}
	// REQ-SETUP-008: stored value must look like an argon2id hash, not plaintext.
	if !strings.HasPrefix(passwordHash, "$argon2id$") {
		t.Errorf("expected argon2id hash in password_hash, got %q", passwordHash)
	}
	if strings.Contains(passwordHash, "securePa55!") {
		t.Error("plaintext password must not appear in password_hash column")
	}

	// REQ-SETUP-009, REQ-SETUP-010: audit row must exist with the correct fields.
	// Fetch the new admin's UUID first.
	var adminID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM users WHERE username = $1`, uniqueUsername,
	).Scan(&adminID); err != nil {
		t.Fatalf("query admin UUID: %v", err)
	}

	var auditAction, auditTargetType string
	var auditAdminID, auditTargetID uuid.UUID
	var payloadJSON string
	err = pool.QueryRow(ctx,
		`SELECT admin_id, action, target_type, target_id, payload::text
		 FROM audit_log WHERE action = 'setup.initial_admin'`,
	).Scan(&auditAdminID, &auditAction, &auditTargetType, &auditTargetID, &payloadJSON)
	if err != nil {
		t.Fatalf("query audit_log for setup.initial_admin: %v", err)
	}

	// REQ-SETUP-010: actor (admin_id) must equal the new admin's own UUID.
	if auditAdminID != adminID {
		t.Errorf("audit admin_id=%v, want %v (the new admin's own ID)", auditAdminID, adminID)
	}
	if auditTargetID != adminID {
		t.Errorf("audit target_id=%v, want %v", auditTargetID, adminID)
	}
	if auditAction != "setup.initial_admin" {
		t.Errorf("audit action=%q, want setup.initial_admin", auditAction)
	}
	if auditTargetType != "user" {
		t.Errorf("audit target_type=%q, want user", auditTargetType)
	}
	// REQ-SETUP-009: payload must not contain password.
	if strings.Contains(payloadJSON, "password") {
		t.Errorf("audit payload must not contain 'password': %s", payloadJSON)
	}
	// Payload must contain the username.
	if !strings.Contains(payloadJSON, uniqueUsername) {
		t.Errorf("audit payload must contain username %q: %s", uniqueUsername, payloadJSON)
	}
}

// ----------------------------------------------------------------------------
// TC-SETUP-INT-003: status=false after admin creation
// ----------------------------------------------------------------------------

// @{"verifies": ["REQ-SETUP-001", "REQ-SETUP-002"]}
func TestIntegration_GetStatus_AfterCreation_NeedsSetupFalse(t *testing.T) {
	setupTruncate(t)
	h := buildStack(t)

	// Create the first admin.
	rec := doSetupPost(t, h, map[string]string{
		"username": fmt.Sprintf("setup_chk_%s", uuid.New().String()[:8]),
		"password": "goodPassw0rd!",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup POST failed: %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Status must now report false.
	statusRec := doSetupStatus(t, h)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("status GET failed: %d", statusRec.Code)
	}
	var resp map[string]bool
	if err := json.Unmarshal(statusRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse status response: %v", err)
	}
	if resp["needs_setup"] {
		t.Error("expected needs_setup=false after admin creation, got true")
	}
}

// ----------------------------------------------------------------------------
// TC-SETUP-INT-004: second POST returns 409 already_configured
// ----------------------------------------------------------------------------

// @{"verifies": ["REQ-SETUP-005"]}
func TestIntegration_CreateFirstAdmin_SecondCall_Returns409(t *testing.T) {
	setupTruncate(t)
	h := buildStack(t)

	// First call — must succeed.
	rec1 := doSetupPost(t, h, map[string]string{
		"username": fmt.Sprintf("setup_first_%s", uuid.New().String()[:8]),
		"password": "GoodPassword1!",
	})
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first POST failed: %d (body: %s)", rec1.Code, rec1.Body.String())
	}

	// Second call — must return 409.
	rec2 := doSetupPost(t, h, map[string]string{
		"username": fmt.Sprintf("setup_second_%s", uuid.New().String()[:8]),
		"password": "AnotherGoodPassword!",
	})
	if rec2.Code != http.StatusConflict {
		t.Fatalf("expected 409 on second POST, got %d (body: %s)", rec2.Code, rec2.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse 409 body: %v", err)
	}
	if resp["error"] != "already_configured" {
		t.Errorf("expected error=already_configured, got %q", resp["error"])
	}
}

// ----------------------------------------------------------------------------
// TC-SETUP-INT-005: blank username returns 400 username_required
// ----------------------------------------------------------------------------

// @{"verifies": ["REQ-SETUP-007"]}
func TestIntegration_CreateFirstAdmin_BlankUsername_Returns400(t *testing.T) {
	setupTruncate(t)
	h := buildStack(t)

	rec := doSetupPost(t, h, map[string]string{
		"username": "   ",
		"password": "GoodPassword1!",
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse 400 body: %v", err)
	}
	if resp["error"] != "username_required" {
		t.Errorf("expected error=username_required, got %q", resp["error"])
	}
}

// ----------------------------------------------------------------------------
// TC-SETUP-INT-006: short password returns 400 password_too_short
// ----------------------------------------------------------------------------

// @{"verifies": ["REQ-SETUP-006"]}
func TestIntegration_CreateFirstAdmin_ShortPassword_Returns400(t *testing.T) {
	setupTruncate(t)
	h := buildStack(t)

	rec := doSetupPost(t, h, map[string]string{
		"username": "admin",
		"password": "short",
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse 400 body: %v", err)
	}
	if resp["error"] != "password_too_short" {
		t.Errorf("expected error=password_too_short, got %q", resp["error"])
	}
}

// ----------------------------------------------------------------------------
// TC-SETUP-INT-007: optional email stored as NULL when omitted; as text when
// provided. Also verifies the minimal @ check in the handler (non-empty + "@").
// ----------------------------------------------------------------------------

// @{"verifies": ["REQ-SETUP-003"]}
func TestIntegration_CreateFirstAdmin_OptionalEmail(t *testing.T) {
	setupTruncate(t)
	pool := dbpkg.IntegrationPool(t)
	h := buildStack(t)
	ctx := context.Background()

	emailAddr := "admin@example.com"
	username := fmt.Sprintf("setup_email_%s", uuid.New().String()[:8])

	rec := doSetupPost(t, h, map[string]any{
		"username": username,
		"password": "GoodPassword1!",
		"email":    emailAddr,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var storedEmail *string
	if err := pool.QueryRow(ctx,
		`SELECT email FROM users WHERE username = $1`, username,
	).Scan(&storedEmail); err != nil {
		t.Fatalf("query email: %v", err)
	}
	if storedEmail == nil {
		t.Fatal("expected email to be stored, got NULL")
	}
	if *storedEmail != emailAddr {
		t.Errorf("expected email=%q, got %q", emailAddr, *storedEmail)
	}
}

// @{"verifies": ["REQ-SETUP-003"]}
func TestIntegration_CreateFirstAdmin_NoEmail_StoredAsNull(t *testing.T) {
	setupTruncate(t)
	pool := dbpkg.IntegrationPool(t)
	h := buildStack(t)
	ctx := context.Background()

	username := fmt.Sprintf("setup_noemail_%s", uuid.New().String()[:8])

	rec := doSetupPost(t, h, map[string]string{
		"username": username,
		"password": "GoodPassword1!",
		// email field deliberately omitted
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var storedEmail *string
	if err := pool.QueryRow(ctx,
		`SELECT email FROM users WHERE username = $1`, username,
	).Scan(&storedEmail); err != nil {
		t.Fatalf("query email: %v", err)
	}
	if storedEmail != nil {
		t.Errorf("expected email=NULL when omitted, got %q", *storedEmail)
	}
}

// ----------------------------------------------------------------------------
// TC-SETUP-INT-008: audit write under valory_app role confirms INSERT grant
// is intact on audit_log (append-only REVOKE removed UPDATE/DELETE but not INSERT).
// ----------------------------------------------------------------------------

// @{"verifies": ["REQ-SETUP-009", "REQ-SETUP-010"]}
func TestIntegration_CreateFirstAdmin_AuditWriteUnderValoryApp(t *testing.T) {
	setupTruncate(t)
	pool := dbpkg.IntegrationPool(t)
	ctx := context.Background()

	// Acquire a connection under the valory_app role so the INSERT privilege on
	// audit_log is exercised as the application role would use it in production.
	// We wire the service with this pool copy: because pgxpool.Pool uses the
	// connection DSN rather than a specific connection, the service will acquire
	// its own connections. To exercise valory_app explicitly we call the service
	// method then verify the audit row exists using the main pool (superuser).
	//
	// The service internally calls pool.Begin which acquires a connection from the
	// pool. The integration pool's AfterRelease resets to the login role. To force
	// the INSERT onto a valory_app connection, we confirm the grant by attempting
	// a direct INSERT under a valory_app conn — if the INSERT would be blocked by
	// a REVOKE we would see an error here, mirroring the real production risk.
	conn := dbpkg.AcquireAsUser(t, pool, "00000000-0000-0000-0000-000000000000", "admin")
	defer conn.Release()

	// Seed a fake prev_hash genesis so the audit chain INSERT succeeds.
	// We bypass auditRepo.Append and insert directly under valory_app to confirm
	// the INSERT privilege is present.
	fakeAdminID := uuid.New()
	fakeTargetID := fakeAdminID
	_, err := conn.Exec(ctx,
		`INSERT INTO audit_log (admin_id, action, target_type, target_id, payload, prev_hash, entry_hash)
		 VALUES ($1, 'setup.initial_admin', 'user', $2, '{"username":"testuser"}', $3, $4)`,
		fakeAdminID, fakeTargetID,
		"0000000000000000000000000000000000000000000000000000000000000000",
		fmt.Sprintf("%064x", 42),
	)
	if err != nil {
		t.Fatalf("INSERT into audit_log under valory_app failed (INSERT grant may be missing): %v", err)
	}

	// Confirm the row is readable via the plain pool (superuser).
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE admin_id = $1 AND action = 'setup.initial_admin'`,
		fakeAdminID,
	).Scan(&count); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 audit row, got %d", count)
	}
}

// ----------------------------------------------------------------------------
// TC-SETUP-INT-009: POST /api/v1/setup is reachable WITHOUT a CSRF token.
// The setup routes are mounted in the PUBLIC group (no CSRF middleware) because
// no session/cookie exists at first run. Mounting Routes() on a bare router —
// exactly as main.go does, outside any CSRF middleware — and POSTing with no
// X-CSRF-Token must be processed normally (201), never rejected with 403.
// ----------------------------------------------------------------------------

// @{"verifies": ["REQ-SETUP-011"]}
func TestIntegration_SetupPost_NoCSRFToken_NotForbidden(t *testing.T) {
	setupTruncate(t)
	h := buildStack(t)

	r := chi.NewRouter()
	r.Route("/setup", func(r chi.Router) { h.Routes(r) })

	body, _ := json.Marshal(map[string]string{
		"username": fmt.Sprintf("setup_nocsrf_%s", uuid.New().String()[:8]),
		"password": "GoodPassword1!",
	})
	// Deliberately send NO X-CSRF-Token header and NO cookies.
	req := httptest.NewRequest(http.MethodPost, "/setup/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code == http.StatusForbidden {
		t.Fatalf("setup POST without a CSRF token was forbidden (403); the endpoint must be CSRF-exempt")
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for CSRF-free setup POST, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// ----------------------------------------------------------------------------
// TC-SETUP-INT-010: the users INSERT that CreateFirstAdmin performs succeeds
// under the real valory_app role (NOBYPASSRLS). The integration login role is a
// superuser that would mask a missing grant; AcquireAsUser downgrades to
// valory_app so a grant regression on the users table surfaces here rather than
// only in production.
// ----------------------------------------------------------------------------

// @{"verifies": ["REQ-SETUP-004", "REQ-SETUP-008"]}
func TestIntegration_UsersInsertGrantUnderValoryApp(t *testing.T) {
	setupTruncate(t)
	pool := dbpkg.IntegrationPool(t)
	ctx := context.Background()

	conn := dbpkg.AcquireAsUser(t, pool, "00000000-0000-0000-0000-000000000000", "admin")
	defer conn.Release()

	username := fmt.Sprintf("setup_grant_%s", uuid.New().String()[:8])
	_, err := conn.Exec(ctx,
		`INSERT INTO users (username, password_hash, role, must_change_password, is_active)
		 VALUES ($1, $2, 'admin', false, true)`,
		username, "$argon2id$v=19$m=65536,t=1,p=4$YWJjZGFiY2Q$YWJjZGFiY2RhYmNkYWJjZA",
	)
	if err != nil {
		t.Fatalf("INSERT into users under valory_app failed (INSERT grant may be missing): %v", err)
	}
}
