package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/valory/valory/internal/audit"
	authpkg "github.com/valory/valory/internal/auth"
)

var handlerTestPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		os.Exit(m.Run())
	}

	var err error
	handlerTestPool, err = pgxpool.New(context.Background(), dbURL)
	if err != nil {
		panic(err)
	}
	defer handlerTestPool.Close()

	ctx := context.Background()
	if err := applyHandlerMigrations(ctx, handlerTestPool); err != nil {
		panic(err)
	}
	if err := truncateHandlerTables(ctx, handlerTestPool); err != nil {
		panic(err)
	}

	code := m.Run()

	if err := truncateHandlerTables(ctx, handlerTestPool); err != nil {
		panic(err)
	}

	os.Exit(code)
}

func applyHandlerMigrations(ctx context.Context, p *pgxpool.Pool) error {
	migration001 := `
	BEGIN;

	CREATE EXTENSION IF NOT EXISTS "pgcrypto";

	CREATE TABLE IF NOT EXISTS schema_migrations (
	    version     TEXT        PRIMARY KEY,
	    applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	INSERT INTO schema_migrations (version) VALUES ('001_auth')
	    ON CONFLICT (version) DO NOTHING;

	CREATE TABLE IF NOT EXISTS users (
	    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
	    username            TEXT        NOT NULL UNIQUE,
	    password_hash       TEXT        NOT NULL,
	    role                TEXT        NOT NULL CHECK (role IN ('student', 'admin')),
	    is_active           BOOLEAN     NOT NULL DEFAULT TRUE,
	    failed_login_count  INTEGER     NOT NULL DEFAULT 0,
	    locked_until        TIMESTAMPTZ,
	    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_users_locked_until ON users (locked_until)
	    WHERE locked_until IS NOT NULL;

	CREATE OR REPLACE FUNCTION set_updated_at()
	RETURNS TRIGGER LANGUAGE plpgsql AS $$
	BEGIN
	    NEW.updated_at = NOW();
	    RETURN NEW;
	END;
	$$;

	DROP TRIGGER IF EXISTS users_updated_at ON users;
	CREATE TRIGGER users_updated_at
	    BEFORE UPDATE ON users
	    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

	CREATE TABLE IF NOT EXISTS sessions (
	    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
	    user_id         UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	    token_hash      TEXT        NOT NULL UNIQUE,
	    role            TEXT        NOT NULL CHECK (role IN ('student', 'admin')),
	    last_active_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	    expires_at      TIMESTAMPTZ NOT NULL,
	    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions (user_id);
	CREATE INDEX IF NOT EXISTS idx_sessions_last_active_at ON sessions (last_active_at);

	CREATE TABLE IF NOT EXISTS login_attempts (
	    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
	    user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	    attempted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	    success      BOOLEAN     NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_login_attempts_user_id ON login_attempts (user_id, attempted_at DESC);

	COMMIT;
	`
	if _, err := p.Exec(ctx, migration001); err != nil {
		return err
	}

	migration002 := `
	BEGIN;

	INSERT INTO schema_migrations (version) VALUES ('002_user_security_audit')
	    ON CONFLICT (version) DO NOTHING;

	ALTER TABLE users ADD COLUMN IF NOT EXISTS email TEXT UNIQUE;

	CREATE TABLE IF NOT EXISTS password_reset_tokens (
	    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
	    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	    token_hash  TEXT        NOT NULL UNIQUE,
	    expires_at  TIMESTAMPTZ NOT NULL,
	    used_at     TIMESTAMPTZ
	);

	CREATE INDEX IF NOT EXISTS idx_prt_user_id    ON password_reset_tokens (user_id);
	CREATE INDEX IF NOT EXISTS idx_prt_token_hash ON password_reset_tokens (token_hash);
	CREATE INDEX IF NOT EXISTS idx_prt_expires_at ON password_reset_tokens (expires_at)
	    WHERE used_at IS NULL;

	CREATE TABLE IF NOT EXISTS password_reset_attempts (
	    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
	    user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	    requested_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_pra_user_requested
	    ON password_reset_attempts (user_id, requested_at DESC);

	CREATE TABLE IF NOT EXISTS student_consent (
	    student_id       UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
	    consented_at     TIMESTAMPTZ NOT NULL,
	    consent_version  VARCHAR(16) NOT NULL
	);

	CREATE TABLE IF NOT EXISTS audit_log (
	    id           BIGSERIAL    PRIMARY KEY,
	    admin_id     UUID         NOT NULL REFERENCES users(id),
	    action       VARCHAR(64)  NOT NULL,
	    target_type  VARCHAR(64)  NOT NULL,
	    target_id    UUID,
	    payload      JSONB        NOT NULL DEFAULT '{}',
	    prev_hash    VARCHAR(64)  NOT NULL,
	    entry_hash   VARCHAR(64)  NOT NULL,
	    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_audit_log_admin_id   ON audit_log (admin_id);
	CREATE INDEX IF NOT EXISTS idx_audit_log_created_at ON audit_log (created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_audit_log_target     ON audit_log (target_type, target_id);

	CREATE TABLE IF NOT EXISTS system_config (
	    key         VARCHAR(120) PRIMARY KEY,
	    value       TEXT         NOT NULL,
	    updated_by  UUID         REFERENCES users(id) ON DELETE SET NULL,
	    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
	);

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
	    ('professor_max_tokens',               '16384'),
	    ('audit_retention_days',               '365'),
	    ('notification_retention_days',        '90'),
	    ('consent_version',                    '1.0'),
	    ('anthropic_base_url',                 ''),
	    ('smtp_host',                          ''),
	    ('smtp_port',                          '587'),
	    ('smtp_from',                          ''),
	    ('smtp_username',                      ''),
	    ('smtp_encryption',                    'starttls')
	ON CONFLICT (key) DO NOTHING;

	COMMIT;
	`
	if _, err := p.Exec(ctx, migration002); err != nil {
		return err
	}

	// migration016: email_test_send_attempts rate-limit table (Sprint 19).
	migration016 := `
	CREATE TABLE IF NOT EXISTS email_test_send_attempts (
	    admin_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	    attempted_at TIMESTAMPTZ NOT NULL DEFAULT now()
	);
	CREATE INDEX IF NOT EXISTS email_test_attempts_idx
	    ON email_test_send_attempts (admin_id, attempted_at);
	`
	if _, err := p.Exec(ctx, migration016); err != nil {
		return err
	}

	// migration013: managed_secrets table (Sprint 16).
	// Mirrors 013_managed_secrets.sql without the REVOKE/GRANT statements which
	// only apply to the production valory_app role (absent in the test DB schema).
	migration013 := `
	CREATE TABLE IF NOT EXISTS managed_secrets (
	    name        VARCHAR(120) PRIMARY KEY,
	    ciphertext  BYTEA        NOT NULL,
	    nonce       BYTEA        NOT NULL,
	    last4       VARCHAR(4)   NOT NULL DEFAULT '',
	    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
	    updated_by  UUID         REFERENCES users(id) ON DELETE SET NULL
	);
	`
	if _, err := p.Exec(ctx, migration013); err != nil {
		return err
	}

	return nil
}

func truncateHandlerTables(ctx context.Context, p *pgxpool.Pool) error {
	statements := []string{
		`TRUNCATE TABLE audit_log CASCADE`,
		`TRUNCATE TABLE managed_secrets CASCADE`,
		`TRUNCATE TABLE email_test_send_attempts CASCADE`,
		`TRUNCATE TABLE student_consent CASCADE`,
		`TRUNCATE TABLE password_reset_attempts CASCADE`,
		`TRUNCATE TABLE password_reset_tokens CASCADE`,
		`TRUNCATE TABLE login_attempts CASCADE`,
		`TRUNCATE TABLE sessions CASCADE`,
		`TRUNCATE TABLE users CASCADE`,
		// Upsert rather than UPDATE: TRUNCATE users CASCADE above also empties
		// system_config (its updated_by FK references users), so the canonical
		// rows must be re-inserted, not merely reset in place.
		`INSERT INTO system_config (key, value) VALUES
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
			('professor_max_tokens',               '16384'),
			('audit_retention_days',               '365'),
			('notification_retention_days',        '90'),
			('consent_version',                    '1.0'),
			('anthropic_base_url',                 ''),
			('smtp_host',                          ''),
			('smtp_port',                          '587'),
			('smtp_from',                          ''),
			('smtp_username',                      ''),
			('smtp_encryption',                    'starttls')
		ON CONFLICT (key) DO UPDATE
			SET value = EXCLUDED.value, updated_by = NULL, updated_at = now()`,
	}
	for _, stmt := range statements {
		if _, err := p.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// createHandlerTestUser inserts a user and returns its UUID.
func createHandlerTestUser(ctx context.Context, t *testing.T, username, role string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := handlerTestPool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
		username, "hash", role).Scan(&id)
	if err != nil {
		t.Fatalf("createHandlerTestUser: %v", err)
	}
	return id
}

// loginAsAdmin creates an admin user, logs in, and returns the raw token.
func loginAsAdmin(ctx context.Context, t *testing.T) (uuid.UUID, string) {
	t.Helper()
	username := "admin_ch_" + uuid.New().String()
	password := "adminpass123"

	hash, err := authpkg.HashPassword(password)
	if err != nil {
		t.Fatalf("loginAsAdmin hash: %v", err)
	}
	var adminID uuid.UUID
	err = handlerTestPool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, 'admin') RETURNING id`,
		username, hash).Scan(&adminID)
	if err != nil {
		t.Fatalf("loginAsAdmin insert: %v", err)
	}

	authRepo := authpkg.NewRepository(handlerTestPool)
	authSvc := authpkg.NewService(authRepo, 15*time.Minute, 24*time.Hour)
	result, err := authSvc.Login(ctx, username, password)
	if err != nil {
		t.Fatalf("loginAsAdmin login: %v", err)
	}
	return adminID, result.RawToken
}

// loginAsStudent creates a student user and returns a raw auth token.
func loginAsStudent(ctx context.Context, t *testing.T) string {
	t.Helper()
	username := "student_ch_" + uuid.New().String()
	password := "studentpass123"

	hash, err := authpkg.HashPassword(password)
	if err != nil {
		t.Fatalf("loginAsStudent hash: %v", err)
	}
	if _, err := handlerTestPool.Exec(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, 'student')`,
		username, hash); err != nil {
		t.Fatalf("loginAsStudent insert: %v", err)
	}

	authRepo := authpkg.NewRepository(handlerTestPool)
	authSvc := authpkg.NewService(authRepo, 15*time.Minute, 24*time.Hour)
	result, err := authSvc.Login(ctx, username, password)
	if err != nil {
		t.Fatalf("loginAsStudent login: %v", err)
	}
	return result.RawToken
}

// newTestHandler returns a fully-wired AdminConfigHandler for handler tests.
// A nil mailer is passed — existing tests do not exercise the email/test endpoint.
func newTestHandler(t *testing.T) *AdminConfigHandler {
	t.Helper()
	cfg := NewConfigService(handlerTestPool)
	ctx := context.Background()
	if err := cfg.Load(ctx); err != nil {
		t.Fatalf("newTestHandler Load: %v", err)
	}
	return NewConfigHandler(cfg, audit.NewRepository(handlerTestPool), handlerTestPool, nil)
}

// makeConfigRequest sends a request through a chi router wired with auth
// middleware and RequireRole("admin").
func makeConfigRequest(t *testing.T, method, path string, body interface{}, bearerToken string) *httptest.ResponseRecorder {
	t.Helper()
	handler := newTestHandler(t)

	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("makeConfigRequest marshal: %v", err)
		}
	}

	req := httptest.NewRequest(method, path, bytes.NewReader(bodyBytes))
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	rr := httptest.NewRecorder()

	authMiddleware := authpkg.NewAuthMiddleware(
		authpkg.NewRepository(handlerTestPool),
		handlerTestPool,
		24*time.Hour,
		nil, // consent gate disabled in tests
	)

	router := chi.NewRouter()
	router.Use(authMiddleware)
	router.Use(authpkg.RequireRole("admin"))
	handler.Routes(router)

	router.ServeHTTP(rr, req)
	return rr
}

// makeConfigRequestNoRole sends a request through auth middleware only — no
// RequireRole — so the handler code itself runs even for non-admin callers.
// Used to test the 403 path by presenting a student token.
func makeConfigRequestNoRole(t *testing.T, method, path string, body interface{}, bearerToken string) *httptest.ResponseRecorder {
	t.Helper()
	handler := newTestHandler(t)

	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("makeConfigRequestNoRole marshal: %v", err)
		}
	}

	req := httptest.NewRequest(method, path, bytes.NewReader(bodyBytes))
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	rr := httptest.NewRecorder()

	authMiddleware := authpkg.NewAuthMiddleware(
		authpkg.NewRepository(handlerTestPool),
		handlerTestPool,
		24*time.Hour,
		nil,
	)

	router := chi.NewRouter()
	router.Use(authMiddleware)
	// RequireRole intentionally omitted to let the student reach the handler.
	handler.Routes(router)

	router.ServeHTTP(rr, req)
	return rr
}

// @{"verifies": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003", "REQ-ADMIN-009", "REQ-ADMIN-010", "REQ-EMAIL-001"]}
func TestGetConfig_ReturnsAll20Keys(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	rr := makeConfigRequest(t, "GET", "/", nil, token)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Config map[string]string `json:"config"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	expectedKeys := []string{
		"agent_retry_limit",
		"correction_loop_max_iterations",
		"per_student_token_limit",
		"late_penalty_rate",
		"homework_weight",
		"project_weight",
		"session_inactivity_seconds",
		"account_lockout_seconds",
		"max_upload_bytes",
		"content_generation_timeout_seconds",
		"professor_max_tokens",
		"audit_retention_days",
		"notification_retention_days",
		"consent_version",
		"anthropic_base_url",
		// Sprint 19: SMTP config keys (REQ-EMAIL-001..011)
		"smtp_host",
		"smtp_port",
		"smtp_from",
		"smtp_username",
		"smtp_encryption",
	}

	if len(resp.Config) != len(expectedKeys) {
		t.Errorf("expected %d keys, got %d", len(expectedKeys), len(resp.Config))
	}
	for _, k := range expectedKeys {
		if _, ok := resp.Config[k]; !ok {
			t.Errorf("missing key %q in response", k)
		}
	}
}

// @{"verifies": ["REQ-ADMIN-001"]}
func TestPatchConfig_SingleKeyPersistsAndReloads(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	body := map[string]interface{}{
		"config": map[string]string{
			"agent_retry_limit": "7",
		},
	}

	rr := makeConfigRequest(t, "PATCH", "/", body, token)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		UpdatedKeys []string          `json:"updated_keys"`
		Config      map[string]string `json:"config"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(resp.UpdatedKeys) != 1 || resp.UpdatedKeys[0] != "agent_retry_limit" {
		t.Errorf("unexpected updated_keys: %v", resp.UpdatedKeys)
	}

	// Verify DB was updated.
	var dbVal string
	if err := handlerTestPool.QueryRow(ctx,
		`SELECT value FROM system_config WHERE key = 'agent_retry_limit'`).Scan(&dbVal); err != nil {
		t.Fatalf("db query: %v", err)
	}
	if dbVal != "7" {
		t.Errorf("expected DB value '7', got %q", dbVal)
	}

	// Verify in-memory config was reloaded (reflected in response).
	if resp.Config["agent_retry_limit"] != "7" {
		t.Errorf("expected in-memory '7', got %q", resp.Config["agent_retry_limit"])
	}
}

// @{"verifies": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003"]}
func TestPatchConfig_MultiKeyUpdate(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	body := map[string]interface{}{
		"config": map[string]string{
			"agent_retry_limit":              "4",
			"correction_loop_max_iterations": "8",
		},
	}

	rr := makeConfigRequest(t, "PATCH", "/", body, token)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify both keys were persisted.
	for k, expected := range map[string]string{
		"agent_retry_limit":              "4",
		"correction_loop_max_iterations": "8",
	} {
		var dbVal string
		if err := handlerTestPool.QueryRow(ctx,
			`SELECT value FROM system_config WHERE key = $1`, k).Scan(&dbVal); err != nil {
			t.Fatalf("db query %q: %v", k, err)
		}
		if dbVal != expected {
			t.Errorf("key %q: expected %q, got %q", k, expected, dbVal)
		}
	}
}

// @{"verifies": ["REQ-ADMIN-001"]}
func TestPatchConfig_UnknownKeyReturns400(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	// Snapshot the row before the rejected PATCH: earlier tests may have
	// legitimately written it, so assert "unchanged", not "never touched".
	var beforeBy *uuid.UUID
	var beforeAt time.Time
	if err := handlerTestPool.QueryRow(ctx,
		`SELECT updated_by, updated_at FROM system_config WHERE key = 'agent_retry_limit'`).Scan(&beforeBy, &beforeAt); err != nil {
		t.Fatalf("db query (before): %v", err)
	}

	body := map[string]interface{}{
		"config": map[string]string{
			"nonexistent_key": "42",
		},
	}

	rr := makeConfigRequest(t, "PATCH", "/", body, token)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify the rejected PATCH wrote nothing.
	var afterBy *uuid.UUID
	var afterAt time.Time
	if err := handlerTestPool.QueryRow(ctx,
		`SELECT updated_by, updated_at FROM system_config WHERE key = 'agent_retry_limit'`).Scan(&afterBy, &afterAt); err != nil {
		t.Fatalf("db query (after): %v", err)
	}
	if !afterAt.Equal(beforeAt) {
		t.Errorf("expected no DB write, but updated_at changed: %v -> %v", beforeAt, afterAt)
	}
	if (beforeBy == nil) != (afterBy == nil) || (beforeBy != nil && afterBy != nil && *beforeBy != *afterBy) {
		t.Error("expected no DB write, but updated_by changed")
	}
}

// @{"verifies": ["REQ-ADMIN-001"]}
func TestPatchConfig_AgentRetryLimitZeroReturns422(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	body := map[string]interface{}{
		"config": map[string]string{
			"agent_retry_limit": "0",
		},
	}

	rr := makeConfigRequest(t, "PATCH", "/", body, token)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := resp["validation_errors"]; !ok {
		t.Error("expected validation_errors key in response")
	}
}

// @{"verifies": ["REQ-ADMIN-001"]}
func TestPatchConfig_AgentRetryLimitNegativeReturns422(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	body := map[string]interface{}{
		"config": map[string]string{
			"agent_retry_limit": "-1",
		},
	}

	rr := makeConfigRequest(t, "PATCH", "/", body, token)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d: %s", rr.Code, rr.Body.String())
	}
}

// @{"verifies": ["REQ-ADMIN-003"]}
func TestPatchConfig_PerStudentTokenLimitZeroIsValid(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	body := map[string]interface{}{
		"config": map[string]string{
			"per_student_token_limit": "0",
		},
	}

	rr := makeConfigRequest(t, "PATCH", "/", body, token)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 (zero is valid — disables AI), got %d: %s", rr.Code, rr.Body.String())
	}
}

// @{"verifies": ["REQ-ADMIN-001"]}
func TestPatchConfig_LatePenaltyRateTooHighReturns422(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	body := map[string]interface{}{
		"config": map[string]string{
			"late_penalty_rate": "1.5",
		},
	}

	rr := makeConfigRequest(t, "PATCH", "/", body, token)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d: %s", rr.Code, rr.Body.String())
	}
}

// @{"verifies": ["REQ-ADMIN-001"]}
func TestPatchConfig_WeightSumNot1Returns422(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	// homework_weight=0.6, project_weight=0.3 — sum is 0.9, not 1.0.
	body := map[string]interface{}{
		"config": map[string]string{
			"homework_weight": "0.6",
			"project_weight":  "0.3",
		},
	}

	rr := makeConfigRequest(t, "PATCH", "/", body, token)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errs, ok := resp["validation_errors"].([]interface{})
	if !ok || len(errs) == 0 {
		t.Errorf("expected validation_errors slice, got %v", resp["validation_errors"])
	}
}

// @{"verifies": ["REQ-AUDIT-001"]}
func TestPatchConfig_AuditEntryCreatedInSameTransaction(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	// Count audit entries before the request.
	var before int
	if err := handlerTestPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE action = 'config.change'`).Scan(&before); err != nil {
		t.Fatalf("count before: %v", err)
	}

	body := map[string]interface{}{
		"config": map[string]string{
			"audit_retention_days": "180",
		},
	}

	rr := makeConfigRequest(t, "PATCH", "/", body, token)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Exactly one new audit entry.
	var after int
	if err := handlerTestPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE action = 'config.change'`).Scan(&after); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != before+1 {
		t.Errorf("expected %d audit entries, got %d", before+1, after)
	}

	// Verify the payload contains the changed key.
	var payloadJSON string
	if err := handlerTestPool.QueryRow(ctx,
		`SELECT payload::text FROM audit_log WHERE action = 'config.change' ORDER BY id DESC LIMIT 1`).Scan(&payloadJSON); err != nil {
		t.Fatalf("payload query: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	keysChanged, ok := payload["keys_changed"]
	if !ok {
		t.Fatalf("expected keys_changed in audit payload, got %v", payload)
	}
	keysSlice, ok := keysChanged.([]interface{})
	if !ok || len(keysSlice) != 1 || keysSlice[0] != "audit_retention_days" {
		t.Errorf("unexpected keys_changed: %v", keysChanged)
	}
}

// @{"verifies": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003"]}
func TestPatchConfig_NonAdminReturns403(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentToken := loginAsStudent(ctx, t)

	body := map[string]interface{}{
		"config": map[string]string{
			"agent_retry_limit": "5",
		},
	}

	// Use the full router with RequireRole("admin") — the student token should
	// be rejected at the role-gate before reaching the handler.
	rr := makeConfigRequest(t, "PATCH", "/", body, studentToken)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

// @{"verifies": ["REQ-GRADE-002", "REQ-GRADE-003"]}
func TestPatchConfig_OneSidedWeightSumFailsReturns422(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	// First, set a known baseline: homework_weight=0.7, project_weight=0.3
	baseline := map[string]interface{}{
		"config": map[string]string{
			"homework_weight": "0.7",
			"project_weight":  "0.3",
		},
	}
	rr := makeConfigRequest(t, "PATCH", "/", baseline, token)
	if rr.Code != http.StatusOK {
		t.Fatalf("setup: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Now send only homework_weight=0.9. The stored project_weight is 0.3,
	// so the effective sum is 1.2 — must be rejected with 422.
	body := map[string]interface{}{
		"config": map[string]string{
			"homework_weight": "0.9",
		},
	}

	rr = makeConfigRequest(t, "PATCH", "/", body, token)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 (one-sided weight sum mismatch), got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errs, ok := resp["validation_errors"].([]interface{})
	if !ok || len(errs) == 0 {
		t.Errorf("expected validation_errors, got %v", resp["validation_errors"])
	}
}

// @{"verifies": ["REQ-SUBMISSION-002"]}
func TestPatchConfig_MaxUploadBytesBelowMinReturns422(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	body := map[string]interface{}{
		"config": map[string]string{
			"max_upload_bytes": "512",
		},
	}

	rr := makeConfigRequest(t, "PATCH", "/", body, token)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 (max_upload_bytes < 1024), got %d: %s", rr.Code, rr.Body.String())
	}
}

// @{"verifies": ["REQ-SECURITY-005"]}
func TestPatchConfig_EmptyConsentVersionReturns422(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	body := map[string]interface{}{
		"config": map[string]string{
			"consent_version": "",
		},
	}

	rr := makeConfigRequest(t, "PATCH", "/", body, token)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 (empty consent_version), got %d: %s", rr.Code, rr.Body.String())
	}
}

// ---------------------------------------------------------------------------
// anthropic_base_url validation tests (REQ-ADMIN-009)
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-ADMIN-009"]}
func TestPatchConfig_AnthropicBaseURL_EmptyIsValid(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	body := map[string]interface{}{
		"config": map[string]string{
			"anthropic_base_url": "",
		},
	}

	rr := makeConfigRequest(t, "PATCH", "/", body, token)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 (empty base URL is valid), got %d: %s", rr.Code, rr.Body.String())
	}
}

// @{"verifies": ["REQ-ADMIN-009"]}
func TestPatchConfig_AnthropicBaseURL_ValidHTTPSIsAccepted(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	body := map[string]interface{}{
		"config": map[string]string{
			"anthropic_base_url": "https://my-gateway.local:8443",
		},
	}

	rr := makeConfigRequest(t, "PATCH", "/", body, token)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 (valid https URL), got %d: %s", rr.Code, rr.Body.String())
	}
}

// @{"verifies": ["REQ-ADMIN-009"]}
func TestPatchConfig_AnthropicBaseURL_FTPSchemeReturns422(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	body := map[string]interface{}{
		"config": map[string]string{
			"anthropic_base_url": "ftp://bad.example.com",
		},
	}

	rr := makeConfigRequest(t, "PATCH", "/", body, token)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 (ftp:// scheme rejected), got %d: %s", rr.Code, rr.Body.String())
	}
}

// @{"verifies": ["REQ-ADMIN-009"]}
func TestPatchConfig_AnthropicBaseURL_GarbageStringReturns422(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	body := map[string]interface{}{
		"config": map[string]string{
			"anthropic_base_url": "not a url at all",
		},
	}

	rr := makeConfigRequest(t, "PATCH", "/", body, token)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 (garbage string rejected), got %d: %s", rr.Code, rr.Body.String())
	}
}

// @{"verifies": ["REQ-ADMIN-009"]}
func TestPatchConfig_AnthropicBaseURL_JavascriptSchemeReturns422(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	body := map[string]interface{}{
		"config": map[string]string{
			"anthropic_base_url": "javascript:alert(1)",
		},
	}

	rr := makeConfigRequest(t, "PATCH", "/", body, token)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 (javascript: scheme rejected), got %d: %s", rr.Code, rr.Body.String())
	}
}

// ---------------------------------------------------------------------------
// professor_max_tokens validation tests (REQ-ADMIN-010)
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-ADMIN-010"]}
func TestPatchConfig_ProfessorMaxTokens_ValidValueIsAccepted(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	body := map[string]interface{}{
		"config": map[string]string{
			"professor_max_tokens": "8192",
		},
	}

	rr := makeConfigRequest(t, "PATCH", "/", body, token)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 (valid professor_max_tokens), got %d: %s", rr.Code, rr.Body.String())
	}
}

// @{"verifies": ["REQ-ADMIN-010"]}
func TestPatchConfig_ProfessorMaxTokens_BelowMinReturns422(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	// 1023 is one below the floor: anything lower cannot fit even a minimal
	// section and guarantees mid-word truncation that fails review.
	body := map[string]interface{}{
		"config": map[string]string{
			"professor_max_tokens": "1023",
		},
	}

	rr := makeConfigRequest(t, "PATCH", "/", body, token)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 (professor_max_tokens < 1024), got %d: %s", rr.Code, rr.Body.String())
	}
}

// @{"verifies": ["REQ-ADMIN-010"]}
func TestPatchConfig_ProfessorMaxTokens_AboveMaxReturns422(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	// 16385 is one above the ceiling: non-streaming calls risk SDK HTTP
	// timeouts beyond ~16K output tokens.
	body := map[string]interface{}{
		"config": map[string]string{
			"professor_max_tokens": "16385",
		},
	}

	rr := makeConfigRequest(t, "PATCH", "/", body, token)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 (professor_max_tokens > 16384), got %d: %s", rr.Code, rr.Body.String())
	}
}

// @{"verifies": ["REQ-ADMIN-010"]}
func TestPatchConfig_ProfessorMaxTokens_NonIntegerReturns422(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	body := map[string]interface{}{
		"config": map[string]string{
			"professor_max_tokens": "lots",
		},
	}

	rr := makeConfigRequest(t, "PATCH", "/", body, token)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 (non-integer professor_max_tokens), got %d: %s", rr.Code, rr.Body.String())
	}
}

// ---------------------------------------------------------------------------
// SMTP config key validation tests (REQ-EMAIL-001..011)
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-EMAIL-002"]}
func TestPatchConfig_SmtpEncryption_ValidValuesAccepted(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	for _, enc := range []string{"none", "starttls", "tls"} {
		body := map[string]interface{}{
			"config": map[string]string{"smtp_encryption": enc},
		}
		rr := makeConfigRequest(t, "PATCH", "/", body, token)
		if rr.Code != http.StatusOK {
			t.Errorf("smtp_encryption=%q: expected 200, got %d: %s", enc, rr.Code, rr.Body.String())
		}
	}
}

// @{"verifies": ["REQ-EMAIL-002"]}
func TestPatchConfig_SmtpEncryption_InvalidReturns422(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	body := map[string]interface{}{
		"config": map[string]string{"smtp_encryption": "ssl"},
	}
	rr := makeConfigRequest(t, "PATCH", "/", body, token)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 for invalid smtp_encryption, got %d: %s", rr.Code, rr.Body.String())
	}
}

// @{"verifies": ["REQ-EMAIL-001"]}
func TestPatchConfig_SmtpPort_ValidRange(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	for _, port := range []string{"25", "587", "465", "1", "65535"} {
		body := map[string]interface{}{
			"config": map[string]string{"smtp_port": port},
		}
		rr := makeConfigRequest(t, "PATCH", "/", body, token)
		if rr.Code != http.StatusOK {
			t.Errorf("smtp_port=%q: expected 200, got %d: %s", port, rr.Code, rr.Body.String())
		}
	}
}

// @{"verifies": ["REQ-EMAIL-001"]}
func TestPatchConfig_SmtpPort_OutOfRangeReturns422(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	for _, port := range []string{"0", "65536", "99999", "abc"} {
		body := map[string]interface{}{
			"config": map[string]string{"smtp_port": port},
		}
		rr := makeConfigRequest(t, "PATCH", "/", body, token)
		if rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("smtp_port=%q: expected 422, got %d: %s", port, rr.Code, rr.Body.String())
		}
	}
}

// @{"verifies": ["REQ-EMAIL-006"]}
func TestPatchConfig_SmtpHost_EmptyIsValid(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	// Empty smtp_host is explicitly valid — it means "not configured".
	body := map[string]interface{}{
		"config": map[string]string{"smtp_host": ""},
	}
	rr := makeConfigRequest(t, "PATCH", "/", body, token)
	if rr.Code != http.StatusOK {
		t.Errorf("empty smtp_host: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Test-send endpoint tests (REQ-EMAIL-008, REQ-EMAIL-009)
// ---------------------------------------------------------------------------

// mockMailerForTest implements email.Mailer for handler tests.
// Defined here (not in a _test package) so it's available to the test helpers
// in this file without a separate test package.
type mockMailerForTest struct {
	sendErr    error
	configured bool
}

func (m *mockMailerForTest) Send(_ context.Context, _, _, _ string) error {
	return m.sendErr
}
func (m *mockMailerForTest) IsConfigured() bool { return m.configured }

// makeTestSendRequest sends a POST /email/test request through the full
// admin-auth router with the given mailer wired into the handler.
func makeTestSendRequest(t *testing.T, body interface{}, bearerToken string, mailer *mockMailerForTest) *httptest.ResponseRecorder {
	t.Helper()

	cfg := NewConfigService(handlerTestPool)
	ctx := context.Background()
	if err := cfg.Load(ctx); err != nil {
		t.Fatalf("makeTestSendRequest Load: %v", err)
	}
	handler := NewConfigHandler(cfg, audit.NewRepository(handlerTestPool), handlerTestPool, mailer)

	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("makeTestSendRequest marshal: %v", err)
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
		authpkg.NewRepository(handlerTestPool),
		handlerTestPool,
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

// @{"verifies": ["REQ-EMAIL-008"]}
func TestTestEmailSend_NotConfiguredReturns503(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	m := &mockMailerForTest{configured: false}
	body := map[string]interface{}{"to": "admin@example.com"}
	rr := makeTestSendRequest(t, body, token, m)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["ok"] != false {
		t.Errorf("expected ok=false, got %v", resp["ok"])
	}
}

// @{"verifies": ["REQ-EMAIL-008"]}
func TestTestEmailSend_SuccessReturns200(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	m := &mockMailerForTest{configured: true, sendErr: nil}
	body := map[string]interface{}{"to": "admin@example.com"}
	rr := makeTestSendRequest(t, body, token, m)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["ok"] != true {
		t.Errorf("expected ok=true, got %v", resp["ok"])
	}
}

// @{"verifies": ["REQ-EMAIL-008"]}
func TestTestEmailSend_MissingToReturns400(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	m := &mockMailerForTest{configured: true}
	body := map[string]interface{}{"to": ""}
	rr := makeTestSendRequest(t, body, token, m)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

// @{"verifies": ["REQ-EMAIL-009"]}
func TestTestEmailSend_RateLimitReturns429(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminID, token := loginAsAdmin(ctx, t)

	// Clean up any stale attempts for this admin from other tests.
	if _, err := handlerTestPool.Exec(ctx,
		`DELETE FROM email_test_send_attempts WHERE admin_id = $1`, adminID,
	); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	m := &mockMailerForTest{configured: true}
	body := map[string]interface{}{"to": "admin@example.com"}

	// First 5 must succeed.
	for i := 0; i < 5; i++ {
		rr := makeTestSendRequest(t, body, token, m)
		if rr.Code != http.StatusOK {
			t.Fatalf("attempt %d: expected 200, got %d: %s", i+1, rr.Code, rr.Body.String())
		}
	}

	// 6th must be rate-limited.
	rr := makeTestSendRequest(t, body, token, m)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("6th attempt: expected 429, got %d: %s", rr.Code, rr.Body.String())
	}
}

// @{"verifies": ["REQ-EMAIL-010"]}
func TestTestEmailSend_AuditEventRecorded(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminID, token := loginAsAdmin(ctx, t)

	// Clean up rate-limit table so this admin hasn't hit the limit.
	if _, err := handlerTestPool.Exec(ctx,
		`DELETE FROM email_test_send_attempts WHERE admin_id = $1`, adminID,
	); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	var before int
	if err := handlerTestPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE action = 'email.test_send'`).Scan(&before); err != nil {
		t.Fatalf("count before: %v", err)
	}

	m := &mockMailerForTest{configured: true}
	body := map[string]interface{}{"to": "audit-check@example.com"}
	rr := makeTestSendRequest(t, body, token, m)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var after int
	if err := handlerTestPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE action = 'email.test_send'`).Scan(&after); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != before+1 {
		t.Errorf("expected %d audit entries, got %d", before+1, after)
	}

	// Verify the payload contains the to-address and no secret value.
	var payloadJSON string
	if err := handlerTestPool.QueryRow(ctx,
		`SELECT payload::text FROM audit_log WHERE action = 'email.test_send' ORDER BY id DESC LIMIT 1`,
	).Scan(&payloadJSON); err != nil {
		t.Fatalf("payload query: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if payload["to"] != "audit-check@example.com" {
		t.Errorf("expected to in audit payload, got %v", payload["to"])
	}
	if _, hasPassword := payload["smtp_password"]; hasPassword {
		t.Error("audit payload must not contain smtp_password")
	}
}

// ---------------------------------------------------------------------------
// email_test_send_verified_at write-protection tests (security, FIX-1)
// ---------------------------------------------------------------------------

// TestPatchConfig_EmailTestSendVerifiedAt_RejectedByPATCH verifies that the
// PATCH endpoint no longer accepts email_test_send_verified_at, so an admin
// cannot forge the test-send prerequisite gate (resolved-decision-3).
//
// @{"verifies": ["REQ-AUTH-018"]}
func TestPatchConfig_EmailTestSendVerifiedAt_RejectedByPATCH(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	_, token := loginAsAdmin(ctx, t)

	// Attempt to set a valid RFC 3339 timestamp via PATCH — must be rejected as
	// an unknown key now that email_test_send_verified_at is removed from allowedKeys.
	body := map[string]interface{}{
		"config": map[string]string{
			"email_test_send_verified_at": time.Now().UTC().Format(time.RFC3339),
		},
	}

	rr := makeConfigRequest(t, "PATCH", "/", body, token)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 (unknown key), got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errMsg, _ := resp["error"].(string)
	if errMsg == "" {
		t.Errorf("expected error message in response, got: %v", resp)
	}
}

// TestTestEmailSend_SetsVerifiedAtOnSuccess verifies that the test-send success
// path (the only allowed write mechanism) persists email_test_send_verified_at
// in the DB with a valid RFC 3339 timestamp.
//
// @{"verifies": ["REQ-AUTH-018"]}
func TestTestEmailSend_SetsVerifiedAtOnSuccess(t *testing.T) {
	if handlerTestPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	adminID, token := loginAsAdmin(ctx, t)

	// Clean up rate-limit entries so this admin has not hit the limit.
	if _, err := handlerTestPool.Exec(ctx,
		`DELETE FROM email_test_send_attempts WHERE admin_id = $1`, adminID,
	); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	// Ensure the marker is clear before the test.
	if _, err := handlerTestPool.Exec(ctx,
		`INSERT INTO system_config (key, value) VALUES ('email_test_send_verified_at', '')
		 ON CONFLICT (key) DO UPDATE SET value = '', updated_at = NOW()`,
	); err != nil {
		t.Fatalf("clear marker: %v", err)
	}

	m := &mockMailerForTest{configured: true, sendErr: nil}
	body := map[string]interface{}{"to": "verify-marker@example.com"}
	rr := makeTestSendRequest(t, body, token, m)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// The marker must now be a non-empty RFC 3339 timestamp written only by the
	// test-send success path — never by the PATCH endpoint.
	var markerVal string
	if err := handlerTestPool.QueryRow(ctx,
		`SELECT value FROM system_config WHERE key = 'email_test_send_verified_at'`,
	).Scan(&markerVal); err != nil {
		t.Fatalf("query marker: %v", err)
	}
	if markerVal == "" {
		t.Fatal("expected email_test_send_verified_at to be set after successful test-send")
	}
	if _, parseErr := time.Parse(time.RFC3339, markerVal); parseErr != nil {
		t.Errorf("email_test_send_verified_at is not valid RFC 3339: %q", markerVal)
	}
}
