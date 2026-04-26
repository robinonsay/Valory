package auth

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		// No DB available; individual tests skip via pool == nil guard.
		os.Exit(0)
	}

	var err error
	pool, err = pgxpool.New(context.Background(), dbURL)
	if err != nil {
		panic(err)
	}
	defer pool.Close()

	ctx := context.Background()
	if err := applyMigration(ctx, pool); err != nil {
		panic(err)
	}

	// Truncate before running to clear any rows left by an interrupted previous run.
	// Hardcoded usernames per test would otherwise hit UNIQUE constraint failures.
	if err := truncateTables(ctx, pool); err != nil {
		panic(err)
	}

	code := m.Run()

	if err := truncateTables(ctx, pool); err != nil {
		panic(err)
	}

	os.Exit(code)
}

func applyMigration(ctx context.Context, p *pgxpool.Pool) error {
	migration := `
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
	_, err := p.Exec(ctx, migration)
	return err
}

func truncateTables(ctx context.Context, p *pgxpool.Pool) error {
	// Three separate Exec calls: pgxpool uses the extended query protocol which
	// does not support multiple statements in a single Exec when no $N params are
	// present on some pgx builds. Separate calls guarantee all three tables are cleared.
	statements := []string{
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

func createTestUser(ctx context.Context, t *testing.T, username, passwordHash, role string) [16]byte {
	var userID [16]byte
	err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
		username, passwordHash, role).
		Scan(&userID)
	if err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}
	return userID
}

// @{"verifies": ["REQ-AUTH-001"]}
func TestGetUserByUsernameFound(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	username := "testuser1"
	passwordHash := "somehash123"
	role := "student"
	userID := createTestUser(ctx, t, username, passwordHash, role)

	user, err := repo.GetUserByUsername(ctx, username)
	if err != nil {
		t.Fatalf("GetUserByUsername failed: %v", err)
	}

	if user == nil {
		t.Fatal("expected user to be returned")
	}
	if user.ID != userID {
		t.Errorf("expected ID %v, got %v", userID, user.ID)
	}
	if user.Username != username {
		t.Errorf("expected username %q, got %q", username, user.Username)
	}
	if user.PasswordHash != passwordHash {
		t.Errorf("expected passwordHash %q, got %q", passwordHash, user.PasswordHash)
	}
	if user.Role != role {
		t.Errorf("expected role %q, got %q", role, user.Role)
	}
	if !user.IsActive {
		t.Error("expected IsActive to be true")
	}
	if user.FailedLoginCount != 0 {
		t.Errorf("expected FailedLoginCount 0, got %d", user.FailedLoginCount)
	}
	if user.LockedUntil != nil {
		t.Errorf("expected LockedUntil to be nil, got %v", user.LockedUntil)
	}
}

// @{"verifies": ["REQ-AUTH-001"]}
func TestGetUserByUsernameNotFound(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	user, err := repo.GetUserByUsername(ctx, "nonexistent")
	if err != ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
	if user != nil {
		t.Fatal("expected user to be nil")
	}
}

// @{"verifies": ["REQ-AUTH-006"]}
func TestRecordLoginAttemptSuccess(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)
	userID := createTestUser(ctx, t, "testuser2", "hash", "student")

	err := repo.RecordLoginAttempt(ctx, userID, true)
	if err != nil {
		t.Fatalf("RecordLoginAttempt failed: %v", err)
	}

	var success bool
	err = pool.QueryRow(ctx,
		`SELECT success FROM login_attempts WHERE user_id = $1`,
		userID).Scan(&success)
	if err != nil {
		t.Fatalf("failed to query login_attempts: %v", err)
	}
	if !success {
		t.Error("expected success to be true")
	}
}

// @{"verifies": ["REQ-AUTH-006"]}
func TestRecordLoginAttemptFailure(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)
	userID := createTestUser(ctx, t, "testuser3", "hash", "student")

	err := repo.RecordLoginAttempt(ctx, userID, false)
	if err != nil {
		t.Fatalf("RecordLoginAttempt failed: %v", err)
	}

	var success bool
	err = pool.QueryRow(ctx,
		`SELECT success FROM login_attempts WHERE user_id = $1`,
		userID).Scan(&success)
	if err != nil {
		t.Fatalf("failed to query login_attempts: %v", err)
	}
	if success {
		t.Error("expected success to be false")
	}
}

// @{"verifies": ["REQ-AUTH-006"]}
func TestSetLockoutState(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)
	userID := createTestUser(ctx, t, "testuser4", "hash", "student")

	lockedUntil := time.Now().Add(1 * time.Hour)
	err := repo.SetLockoutState(ctx, userID, 5, &lockedUntil)
	if err != nil {
		t.Fatalf("SetLockoutState failed: %v", err)
	}

	var count int
	var stored *time.Time
	err = pool.QueryRow(ctx,
		`SELECT failed_login_count, locked_until FROM users WHERE id = $1`,
		userID).Scan(&count, &stored)
	if err != nil {
		t.Fatalf("failed to query users: %v", err)
	}
	if count != 5 {
		t.Errorf("expected failed_login_count 5, got %d", count)
	}
	if stored == nil {
		t.Error("expected locked_until to be set")
	}
}

// @{"verifies": ["REQ-AUTH-006"]}
func TestSetLockoutStateClearsLock(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)
	userID := createTestUser(ctx, t, "testuser5", "hash", "student")

	lockedUntil := time.Now().Add(1 * time.Hour)
	_ = repo.SetLockoutState(ctx, userID, 5, &lockedUntil)

	err := repo.SetLockoutState(ctx, userID, 0, nil)
	if err != nil {
		t.Fatalf("SetLockoutState failed: %v", err)
	}

	var count int
	var stored *time.Time
	err = pool.QueryRow(ctx,
		`SELECT failed_login_count, locked_until FROM users WHERE id = $1`,
		userID).Scan(&count, &stored)
	if err != nil {
		t.Fatalf("failed to query users: %v", err)
	}
	if count != 0 {
		t.Errorf("expected failed_login_count 0, got %d", count)
	}
	if stored != nil {
		t.Errorf("expected locked_until to be nil, got %v", stored)
	}
}

// @{"verifies": ["REQ-AUTH-006"]}
func TestResetLoginState(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)
	userID := createTestUser(ctx, t, "testuser6", "hash", "student")

	lockedUntil := time.Now().Add(1 * time.Hour)
	_ = repo.SetLockoutState(ctx, userID, 5, &lockedUntil)

	err := repo.ResetLoginState(ctx, userID)
	if err != nil {
		t.Fatalf("ResetLoginState failed: %v", err)
	}

	var count int
	var stored *time.Time
	err = pool.QueryRow(ctx,
		`SELECT failed_login_count, locked_until FROM users WHERE id = $1`,
		userID).Scan(&count, &stored)
	if err != nil {
		t.Fatalf("failed to query users: %v", err)
	}
	if count != 0 {
		t.Errorf("expected failed_login_count 0, got %d", count)
	}
	if stored != nil {
		t.Errorf("expected locked_until to be nil, got %v", stored)
	}
}

// @{"verifies": ["REQ-AUTH-002", "REQ-AUTH-003", "REQ-AUTH-005"]}
func TestCreateSessionAndGetSessionByTokenHash(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)
	userID := createTestUser(ctx, t, "testuser7", "hash", "admin")

	tokenHash := "testhash123"
	role := "admin"
	expiresAt := time.Now().Add(24 * time.Hour)

	created, err := repo.CreateSession(ctx, userID, tokenHash, role, expiresAt)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	if created == nil {
		t.Fatal("expected session to be returned")
	}
	if created.UserID != userID {
		t.Errorf("expected UserID %v, got %v", userID, created.UserID)
	}
	if created.TokenHash != tokenHash {
		t.Errorf("expected TokenHash %q, got %q", tokenHash, created.TokenHash)
	}
	if created.Role != role {
		t.Errorf("expected Role %q, got %q", role, created.Role)
	}

	retrieved, err := repo.GetSessionByTokenHash(ctx, tokenHash)
	if err != nil {
		t.Fatalf("GetSessionByTokenHash failed: %v", err)
	}

	if retrieved == nil {
		t.Fatal("expected session to be returned")
	}
	if retrieved.ID != created.ID {
		t.Errorf("expected ID %v, got %v", created.ID, retrieved.ID)
	}
	if retrieved.TokenHash != tokenHash {
		t.Errorf("expected TokenHash %q, got %q", tokenHash, retrieved.TokenHash)
	}
}

// @{"verifies": ["REQ-AUTH-005"]}
func TestGetSessionByTokenHashNotFound(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	session, err := repo.GetSessionByTokenHash(ctx, "nonexistent")
	if err != ErrSessionNotFound {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
	if session != nil {
		t.Fatal("expected session to be nil")
	}
}

// @{"verifies": ["REQ-AUTH-005"]}
func TestDeleteSession(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)
	userID := createTestUser(ctx, t, "testuser8", "hash", "student")

	tokenHash := "testhash456"
	expiresAt := time.Now().Add(24 * time.Hour)
	_, _ = repo.CreateSession(ctx, userID, tokenHash, "student", expiresAt)

	err := repo.DeleteSession(ctx, tokenHash)
	if err != nil {
		t.Fatalf("DeleteSession failed: %v", err)
	}

	session, err := repo.GetSessionByTokenHash(ctx, tokenHash)
	if err != ErrSessionNotFound {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
	if session != nil {
		t.Fatal("expected session to be nil")
	}
}

// @{"verifies": ["REQ-AUTH-005"]}
func TestDeleteSessionIdempotent(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	err := repo.DeleteSession(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("DeleteSession should be idempotent, got error: %v", err)
	}
}

// @{"verifies": ["REQ-AUTH-005"]}
func TestUpdateLastActiveAt(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)
	userID := createTestUser(ctx, t, "testuser9", "hash", "student")

	tokenHash := "testhash789"
	expiresAt := time.Now().Add(24 * time.Hour)
	session, _ := repo.CreateSession(ctx, userID, tokenHash, "student", expiresAt)
	initialLastActive := session.LastActiveAt

	time.Sleep(100 * time.Millisecond)

	err := repo.UpdateLastActiveAt(ctx, tokenHash)
	if err != nil {
		t.Fatalf("UpdateLastActiveAt failed: %v", err)
	}

	updated, err := repo.GetSessionByTokenHash(ctx, tokenHash)
	if err != nil {
		t.Fatalf("GetSessionByTokenHash failed: %v", err)
	}

	if updated.LastActiveAt.Equal(initialLastActive) {
		t.Error("expected LastActiveAt to be updated")
	}
	if updated.LastActiveAt.Before(initialLastActive) {
		t.Error("expected LastActiveAt to advance")
	}
}

// @{"verifies": ["REQ-USER-003"]}
func TestDeleteAllUserSessions(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)
	userID1 := createTestUser(ctx, t, "testuser10", "hash", "student")
	userID2 := createTestUser(ctx, t, "testuser11", "hash", "student")

	expiresAt := time.Now().Add(24 * time.Hour)
	_, _ = repo.CreateSession(ctx, userID1, "hash1", "student", expiresAt)
	_, _ = repo.CreateSession(ctx, userID1, "hash2", "student", expiresAt)
	_, _ = repo.CreateSession(ctx, userID2, "hash3", "student", expiresAt)

	err := repo.DeleteAllUserSessions(ctx, userID1)
	if err != nil {
		t.Fatalf("DeleteAllUserSessions failed: %v", err)
	}

	_, err = repo.GetSessionByTokenHash(ctx, "hash1")
	if err != ErrSessionNotFound {
		t.Fatalf("expected hash1 to be deleted")
	}

	_, err = repo.GetSessionByTokenHash(ctx, "hash2")
	if err != ErrSessionNotFound {
		t.Fatalf("expected hash2 to be deleted")
	}

	session3, err := repo.GetSessionByTokenHash(ctx, "hash3")
	if err != nil {
		t.Fatalf("expected hash3 to exist, got error: %v", err)
	}
	if session3 == nil {
		t.Fatal("expected session3 to be returned")
	}
}
