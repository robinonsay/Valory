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

	authpkg "github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/audit"
)

var handlerTestPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		os.Exit(0)
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
	    ('audit_retention_days',               '365'),
	    ('notification_retention_days',        '90'),
	    ('consent_version',                    '1.0')
	ON CONFLICT (key) DO NOTHING;

	COMMIT;
	`
	if _, err := p.Exec(ctx, migration002); err != nil {
		return err
	}

	return nil
}

func truncateHandlerTables(ctx context.Context, p *pgxpool.Pool) error {
	statements := []string{
		`TRUNCATE TABLE audit_log CASCADE`,
		`TRUNCATE TABLE student_consent CASCADE`,
		`TRUNCATE TABLE password_reset_attempts CASCADE`,
		`TRUNCATE TABLE password_reset_tokens CASCADE`,
		`TRUNCATE TABLE login_attempts CASCADE`,
		`TRUNCATE TABLE sessions CASCADE`,
		`TRUNCATE TABLE users CASCADE`,
		`UPDATE system_config SET value = CASE key
			WHEN 'agent_retry_limit'                  THEN '3'
			WHEN 'correction_loop_max_iterations'     THEN '5'
			WHEN 'per_student_token_limit'            THEN '500000'
			WHEN 'late_penalty_rate'                  THEN '0.05'
			WHEN 'homework_weight'                    THEN '0.7'
			WHEN 'project_weight'                     THEN '0.3'
			WHEN 'session_inactivity_seconds'         THEN '1800'
			WHEN 'account_lockout_seconds'            THEN '900'
			WHEN 'max_upload_bytes'                   THEN '10485760'
			WHEN 'content_generation_timeout_seconds' THEN '300'
			WHEN 'audit_retention_days'               THEN '365'
			WHEN 'notification_retention_days'        THEN '90'
			WHEN 'consent_version'                    THEN '1.0'
			ELSE value END,
		updated_by = NULL,
		updated_at = now()`,
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
	rawToken, _, err := authSvc.Login(ctx, username, password)
	if err != nil {
		t.Fatalf("loginAsAdmin login: %v", err)
	}
	return adminID, rawToken
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
	rawToken, _, err := authSvc.Login(ctx, username, password)
	if err != nil {
		t.Fatalf("loginAsStudent login: %v", err)
	}
	return rawToken
}

// newTestHandler returns a fully-wired AdminConfigHandler for handler tests.
func newTestHandler(t *testing.T) *AdminConfigHandler {
	t.Helper()
	cfg := NewConfigService(handlerTestPool)
	ctx := context.Background()
	if err := cfg.Load(ctx); err != nil {
		t.Fatalf("newTestHandler Load: %v", err)
	}
	return NewConfigHandler(cfg, audit.NewRepository(handlerTestPool), handlerTestPool)
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

// @{"verifies": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003"]}
func TestGetConfig_ReturnsAll13Keys(t *testing.T) {
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
		"audit_retention_days",
		"notification_retention_days",
		"consent_version",
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

	body := map[string]interface{}{
		"config": map[string]string{
			"nonexistent_key": "42",
		},
	}

	rr := makeConfigRequest(t, "PATCH", "/", body, token)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify no DB write occurred.
	var updatedBy *uuid.UUID
	if err := handlerTestPool.QueryRow(ctx,
		`SELECT updated_by FROM system_config WHERE key = 'agent_retry_limit'`).Scan(&updatedBy); err != nil {
		t.Fatalf("db query: %v", err)
	}
	if updatedBy != nil {
		t.Error("expected no DB write, but updated_by is set")
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
