package user

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		os.Exit(m.Run())
	}

	var err error
	pool, err = pgxpool.New(context.Background(), dbURL)
	if err != nil {
		panic(err)
	}
	defer pool.Close()

	ctx := context.Background()
	if err := applyMigrations(ctx, pool); err != nil {
		panic(err)
	}

	if err := truncateTables(ctx, pool); err != nil {
		panic(err)
	}

	code := m.Run()

	if err := truncateTables(ctx, pool); err != nil {
		panic(err)
	}

	os.Exit(code)
}

func applyMigrations(ctx context.Context, p *pgxpool.Pool) error {
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

	COMMIT;
	`

	if _, err := p.Exec(ctx, migration002); err != nil {
		return err
	}

	return nil
}

func truncateTables(ctx context.Context, p *pgxpool.Pool) error {
	statements := []string{
		`TRUNCATE TABLE audit_log CASCADE`,
		`TRUNCATE TABLE student_consent CASCADE`,
		`TRUNCATE TABLE password_reset_attempts CASCADE`,
		`TRUNCATE TABLE password_reset_tokens CASCADE`,
		`TRUNCATE TABLE login_attempts CASCADE`,
		`TRUNCATE TABLE sessions CASCADE`,
		`TRUNCATE TABLE users CASCADE`,
	}
	for _, stmt := range statements {
		if _, err := p.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func createTestUser(ctx context.Context, t *testing.T, username string, email *string, passwordHash, role string) uuid.UUID {
	var userID uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash, role) VALUES ($1, $2, $3, $4) RETURNING id`,
		username, email, passwordHash, role).
		Scan(&userID)
	if err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}
	return userID
}

func createTestSession(ctx context.Context, t *testing.T, userID uuid.UUID, tokenHash string) uuid.UUID {
	var sessionID uuid.UUID
	expiresAt := time.Now().Add(24 * time.Hour)
	err := pool.QueryRow(ctx,
		`INSERT INTO sessions (user_id, token_hash, role, expires_at) VALUES ($1, $2, $3, $4) RETURNING id`,
		userID, tokenHash, "student", expiresAt).
		Scan(&sessionID)
	if err != nil {
		t.Fatalf("failed to create test session: %v", err)
	}
	return sessionID
}

// @{"verifies": ["REQ-USER-002"]}
func TestCreateUser_Success(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	username := "testuser_create_" + uuid.New().String()
	role := "student"

	user, err := repo.CreateUser(ctx, username, nil, "somehash", role)
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	if user.Username != username {
		t.Errorf("expected username %q, got %q", username, user.Username)
	}
	if user.Role != role {
		t.Errorf("expected role %q, got %q", role, user.Role)
	}
	if !user.IsActive {
		t.Error("expected IsActive to be true")
	}
	if user.Email != nil {
		t.Errorf("expected email to be nil, got %v", user.Email)
	}
}

// @{"verifies": ["REQ-USER-002"]}
func TestCreateUser_DuplicateUsername(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	username := "testuser_dup_" + uuid.New().String()

	_, err1 := repo.CreateUser(ctx, username, nil, "hash1", "student")
	if err1 != nil {
		t.Fatalf("first CreateUser failed: %v", err1)
	}

	_, err2 := repo.CreateUser(ctx, username, nil, "hash2", "admin")
	if err2 != ErrDuplicateUsername {
		t.Fatalf("expected ErrDuplicateUsername, got %v", err2)
	}
}

// @{"verifies": ["REQ-USER-001"]}
func TestGetUserByID_Found(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	username := "testuser_getid_" + uuid.New().String()
	userID := createTestUser(ctx, t, username, nil, "somehash", "student")

	user, err := repo.GetUserByID(ctx, userID)
	if err != nil {
		t.Fatalf("GetUserByID failed: %v", err)
	}

	if user.ID != userID {
		t.Errorf("expected ID %v, got %v", userID, user.ID)
	}
	if user.Username != username {
		t.Errorf("expected username %q, got %q", username, user.Username)
	}
}

// @{"verifies": ["REQ-USER-001"]}
func TestGetUserByID_NotFound(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	randomID := uuid.New()

	_, err := repo.GetUserByID(ctx, randomID)
	if err != ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

// @{"verifies": ["REQ-USER-001"]}
func TestGetUserByUsername_Found(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	username := "testuser_getusr_" + uuid.New().String()
	userID := createTestUser(ctx, t, username, nil, "somehash", "student")

	user, err := repo.GetUserByUsername(ctx, username)
	if err != nil {
		t.Fatalf("GetUserByUsername failed: %v", err)
	}

	if user.ID != userID {
		t.Errorf("expected ID %v, got %v", userID, user.ID)
	}
	if user.Username != username {
		t.Errorf("expected username %q, got %q", username, user.Username)
	}
}

// @{"verifies": ["REQ-USER-002"]}
func TestUpdateUser_PartialUpdate(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	username := "testuser_update_" + uuid.New().String()
	userID := createTestUser(ctx, t, username, nil, "oldhash", "student")

	newUsername := "updated_" + uuid.New().String()
	fields := UpdateFields{
		Username: &newUsername,
	}

	user, err := repo.UpdateUser(ctx, userID, fields)
	if err != nil {
		t.Fatalf("UpdateUser failed: %v", err)
	}

	if user.Username != newUsername {
		t.Errorf("expected username %q, got %q", newUsername, user.Username)
	}
	if user.PasswordHash != "oldhash" {
		t.Errorf("expected password hash to remain unchanged, got %q", user.PasswordHash)
	}
}

// @{"verifies": ["REQ-USER-002"]}
func TestUpdateUser_NoFields(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	username := "testuser_nofields_" + uuid.New().String()
	userID := createTestUser(ctx, t, username, nil, "hash", "student")

	_, err := repo.UpdateUser(ctx, userID, UpdateFields{})
	if err != ErrNoFieldsToUpdate {
		t.Fatalf("expected ErrNoFieldsToUpdate, got %v", err)
	}
}

// @{"verifies": ["REQ-USER-003"]}
func TestSetActive_Deactivate_DeletesSessions(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	username := "testuser_setactive_" + uuid.New().String()
	userID := createTestUser(ctx, t, username, nil, "hash", "student")

	createTestSession(ctx, t, userID, "testtokenA_"+uuid.New().String())

	err := repo.SetActive(ctx, userID, false)
	if err != nil {
		t.Fatalf("SetActive failed: %v", err)
	}

	user, err := repo.GetUserByID(ctx, userID)
	if err != nil {
		t.Fatalf("GetUserByID failed: %v", err)
	}
	if user.IsActive {
		t.Error("expected IsActive to be false")
	}

	var sessionCount int
	err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM sessions WHERE user_id = $1`, userID).Scan(&sessionCount)
	if err != nil {
		t.Fatalf("failed to count sessions: %v", err)
	}
	if sessionCount != 0 {
		t.Errorf("expected 0 sessions, got %d", sessionCount)
	}
}

// @{"verifies": ["REQ-USER-003"]}
func TestSetActive_Activate(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	username := "testuser_activate_" + uuid.New().String()
	userID := createTestUser(ctx, t, username, nil, "hash", "student")

	err := repo.SetActive(ctx, userID, false)
	if err != nil {
		t.Fatalf("SetActive(false) failed: %v", err)
	}

	err = repo.SetActive(ctx, userID, true)
	if err != nil {
		t.Fatalf("SetActive(true) failed: %v", err)
	}

	user, err := repo.GetUserByID(ctx, userID)
	if err != nil {
		t.Fatalf("GetUserByID failed: %v", err)
	}
	if !user.IsActive {
		t.Error("expected IsActive to be true")
	}
}

// @{"verifies": ["REQ-USER-007"]}
func TestDeleteStudent_CascadesUserRow(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	username := "testuser_delstudent_" + uuid.New().String()
	userID := createTestUser(ctx, t, username, nil, "hash", "student")

	err := repo.DeleteStudent(ctx, userID)
	if err != nil {
		t.Fatalf("DeleteStudent failed: %v", err)
	}

	_, err = repo.GetUserByID(ctx, userID)
	if err != ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound after deletion, got %v", err)
	}
}

// @{"verifies": ["REQ-USER-007"]}
func TestDeleteStudent_RejectsAdmin(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	username := "testuser_admin_del_" + uuid.New().String()
	userID := createTestUser(ctx, t, username, nil, "hash", "admin")

	err := repo.DeleteStudent(ctx, userID)
	if err != ErrNotAStudent {
		t.Fatalf("expected ErrNotAStudent, got %v", err)
	}
}

// @{"verifies": ["REQ-USER-005"]}
func TestCreatePasswordResetToken_And_GetValid(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	username := "testuser_resettoken_" + uuid.New().String()
	userID := createTestUser(ctx, t, username, nil, "hash", "student")

	tokenHash := "testhash_reset_" + uuid.New().String()
	expiresAt := time.Now().Add(1 * time.Hour)

	err := repo.CreatePasswordResetToken(ctx, userID, tokenHash, expiresAt)
	if err != nil {
		t.Fatalf("CreatePasswordResetToken failed: %v", err)
	}

	token, err := repo.GetValidResetToken(ctx, tokenHash)
	if err != nil {
		t.Fatalf("GetValidResetToken failed: %v", err)
	}

	if token.UserID != userID {
		t.Errorf("expected UserID %v, got %v", userID, token.UserID)
	}
	if token.TokenHash != tokenHash {
		t.Errorf("expected TokenHash %q, got %q", tokenHash, token.TokenHash)
	}
	if token.UsedAt != nil {
		t.Errorf("expected UsedAt to be nil, got %v", token.UsedAt)
	}
}

// @{"verifies": ["REQ-USER-005", "REQ-USER-006"]}
func TestMarkResetTokenUsed_PreventsReuse(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	username := "testuser_tokenused_" + uuid.New().String()
	userID := createTestUser(ctx, t, username, nil, "hash", "student")

	tokenHash := "testhash_mark_" + uuid.New().String()
	expiresAt := time.Now().Add(1 * time.Hour)

	err := repo.CreatePasswordResetToken(ctx, userID, tokenHash, expiresAt)
	if err != nil {
		t.Fatalf("CreatePasswordResetToken failed: %v", err)
	}

	token, err := repo.GetValidResetToken(ctx, tokenHash)
	if err != nil {
		t.Fatalf("GetValidResetToken failed: %v", err)
	}

	err = repo.MarkResetTokenUsed(ctx, token.ID)
	if err != nil {
		t.Fatalf("MarkResetTokenUsed failed: %v", err)
	}

	_, err = repo.GetValidResetToken(ctx, tokenHash)
	if err != ErrTokenNotFound {
		t.Fatalf("expected ErrTokenNotFound after marking used, got %v", err)
	}
}

// @{"verifies": ["REQ-USER-005"]}
func TestGetValidResetToken_Expired(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	username := "testuser_expired_" + uuid.New().String()
	userID := createTestUser(ctx, t, username, nil, "hash", "student")

	tokenHash := "testhash_expired_" + uuid.New().String()
	expiresAt := time.Now().Add(-1 * time.Hour)

	err := repo.CreatePasswordResetToken(ctx, userID, tokenHash, expiresAt)
	if err != nil {
		t.Fatalf("CreatePasswordResetToken failed: %v", err)
	}

	_, err = repo.GetValidResetToken(ctx, tokenHash)
	if err != ErrTokenNotFound {
		t.Fatalf("expected ErrTokenNotFound for expired token, got %v", err)
	}
}

// @{"verifies": ["REQ-SECURITY-005"]}
func TestUpsertConsent_And_Get(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	username := "testuser_consent_" + uuid.New().String()
	userID := createTestUser(ctx, t, username, nil, "hash", "student")

	version := "1.0"

	err := repo.UpsertConsent(ctx, userID, version)
	if err != nil {
		t.Fatalf("UpsertConsent failed: %v", err)
	}

	retrieved, err := repo.GetConsentVersion(ctx, userID)
	if err != nil {
		t.Fatalf("GetConsentVersion failed: %v", err)
	}

	if retrieved != version {
		t.Errorf("expected version %q, got %q", version, retrieved)
	}
}

// @{"verifies": ["REQ-SECURITY-005"]}
func TestUpsertConsent_UpdatesVersion(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	username := "testuser_consentupd_" + uuid.New().String()
	userID := createTestUser(ctx, t, username, nil, "hash", "student")

	err := repo.UpsertConsent(ctx, userID, "1.0")
	if err != nil {
		t.Fatalf("first UpsertConsent failed: %v", err)
	}

	err = repo.UpsertConsent(ctx, userID, "2.0")
	if err != nil {
		t.Fatalf("second UpsertConsent failed: %v", err)
	}

	retrieved, err := repo.GetConsentVersion(ctx, userID)
	if err != nil {
		t.Fatalf("GetConsentVersion failed: %v", err)
	}

	if retrieved != "2.0" {
		t.Errorf("expected version 2.0, got %q", retrieved)
	}
}
