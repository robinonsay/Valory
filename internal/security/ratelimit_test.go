package security

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		os.Exit(0)
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

	DO $$
	BEGIN
	    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'valory_app') THEN
	        CREATE ROLE valory_app
	            NOLOGIN
	            NOINHERIT
	            NOSUPERUSER
	            NOCREATEDB
	            NOCREATEROLE
	            NOBYPASSRLS;
	    END IF;
	END
	$$;

	GRANT USAGE ON SCHEMA public TO valory_app;
	GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO valory_app;
	GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO valory_app;

	REVOKE UPDATE, DELETE ON audit_log FROM valory_app;

	REVOKE INSERT, UPDATE, DELETE ON schema_migrations FROM valory_app;

	ALTER DEFAULT PRIVILEGES IN SCHEMA public
	    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO valory_app;

	ALTER DEFAULT PRIVILEGES IN SCHEMA public
	    GRANT USAGE, SELECT ON SEQUENCES TO valory_app;

	COMMIT;
	`
	if _, err := p.Exec(ctx, migration002); err != nil {
		return err
	}

	return nil
}

func truncateTables(ctx context.Context, p *pgxpool.Pool) error {
	statements := []string{
		`TRUNCATE TABLE password_reset_attempts CASCADE`,
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

func createTestUser(ctx context.Context, t *testing.T, username string) [16]byte {
	var userID [16]byte
	err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
		username, "hash", "student").
		Scan(&userID)
	if err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}
	return userID
}

// @{"verifies": ["REQ-SECURITY-003"]}
func TestCheckAndRecord_FirstRequest_Succeeds(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	userID := createTestUser(ctx, t, "user1")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM password_reset_attempts WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	err := CheckAndRecordPasswordReset(ctx, pool, userID)
	if err != nil {
		t.Fatalf("CheckAndRecordPasswordReset failed: %v", err)
	}

	var count int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM password_reset_attempts WHERE user_id = $1`,
		userID).Scan(&count)
	if err != nil {
		t.Fatalf("failed to query password_reset_attempts: %v", err)
	}

	if count != 1 {
		t.Errorf("expected 1 attempt recorded, got %d", count)
	}
}

// @{"verifies": ["REQ-SECURITY-003"]}
func TestCheckAndRecord_ThirdRequest_Succeeds(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	userID := createTestUser(ctx, t, "user2")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM password_reset_attempts WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	_, _ = pool.Exec(ctx,
		`INSERT INTO password_reset_attempts (user_id) VALUES ($1)`,
		userID)
	_, _ = pool.Exec(ctx,
		`INSERT INTO password_reset_attempts (user_id) VALUES ($1)`,
		userID)

	err := CheckAndRecordPasswordReset(ctx, pool, userID)
	if err != nil {
		t.Fatalf("CheckAndRecordPasswordReset failed: %v", err)
	}

	var count int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM password_reset_attempts WHERE user_id = $1`,
		userID).Scan(&count)
	if err != nil {
		t.Fatalf("failed to query password_reset_attempts: %v", err)
	}

	if count != 3 {
		t.Errorf("expected 3 attempts recorded, got %d", count)
	}
}

// @{"verifies": ["REQ-SECURITY-003"]}
func TestCheckAndRecord_FourthRequest_RateLimited(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	userID := createTestUser(ctx, t, "user3")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM password_reset_attempts WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	_, _ = pool.Exec(ctx,
		`INSERT INTO password_reset_attempts (user_id) VALUES ($1)`,
		userID)
	_, _ = pool.Exec(ctx,
		`INSERT INTO password_reset_attempts (user_id) VALUES ($1)`,
		userID)
	_, _ = pool.Exec(ctx,
		`INSERT INTO password_reset_attempts (user_id) VALUES ($1)`,
		userID)

	err := CheckAndRecordPasswordReset(ctx, pool, userID)
	if err != ErrRateLimitExceeded {
		t.Fatalf("expected ErrRateLimitExceeded, got %v", err)
	}

	var count int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM password_reset_attempts WHERE user_id = $1`,
		userID).Scan(&count)
	if err != nil {
		t.Fatalf("failed to query password_reset_attempts: %v", err)
	}

	if count != 3 {
		t.Errorf("expected no new attempt recorded; expected 3 attempts, got %d", count)
	}
}

// @{"verifies": ["REQ-SECURITY-003"]}
func TestCheckAndRecord_CounterPerAccount(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	userA := createTestUser(ctx, t, "userA")
	userB := createTestUser(ctx, t, "userB")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM password_reset_attempts WHERE user_id IN ($1, $2)`, userA, userB)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1, $2)`, userA, userB)
	})

	_, _ = pool.Exec(ctx,
		`INSERT INTO password_reset_attempts (user_id) VALUES ($1)`,
		userA)
	_, _ = pool.Exec(ctx,
		`INSERT INTO password_reset_attempts (user_id) VALUES ($1)`,
		userA)
	_, _ = pool.Exec(ctx,
		`INSERT INTO password_reset_attempts (user_id) VALUES ($1)`,
		userA)

	err := CheckAndRecordPasswordReset(ctx, pool, userB)
	if err != nil {
		t.Fatalf("CheckAndRecordPasswordReset for userB failed: %v", err)
	}

	var countB int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM password_reset_attempts WHERE user_id = $1`,
		userB).Scan(&countB)
	if err != nil {
		t.Fatalf("failed to query password_reset_attempts for userB: %v", err)
	}

	if countB != 1 {
		t.Errorf("expected 1 attempt for userB, got %d", countB)
	}
}

// @{"verifies": ["REQ-SECURITY-003"]}
func TestCheckAndRecord_ExpiredAttemptsNotCounted(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	userID := createTestUser(ctx, t, "user4")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM password_reset_attempts WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	_, _ = pool.Exec(ctx,
		`INSERT INTO password_reset_attempts (user_id, requested_at) VALUES ($1, NOW() - INTERVAL '2 hours')`,
		userID)
	_, _ = pool.Exec(ctx,
		`INSERT INTO password_reset_attempts (user_id, requested_at) VALUES ($1, NOW() - INTERVAL '2 hours')`,
		userID)
	_, _ = pool.Exec(ctx,
		`INSERT INTO password_reset_attempts (user_id, requested_at) VALUES ($1, NOW() - INTERVAL '2 hours')`,
		userID)

	err := CheckAndRecordPasswordReset(ctx, pool, userID)
	if err != nil {
		t.Fatalf("CheckAndRecordPasswordReset failed: %v", err)
	}

	var count int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM password_reset_attempts WHERE user_id = $1 AND requested_at > NOW() - INTERVAL '1 hour'`,
		userID).Scan(&count)
	if err != nil {
		t.Fatalf("failed to query recent password_reset_attempts: %v", err)
	}

	if count != 1 {
		t.Errorf("expected 1 recent attempt, got %d", count)
	}
}

// @{"verifies": ["REQ-SECURITY-003"]}
func TestPruneOldResetAttempts_DeletesOldRows(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	userID := createTestUser(ctx, t, "user5")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM password_reset_attempts WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	_, _ = pool.Exec(ctx,
		`INSERT INTO password_reset_attempts (user_id, requested_at) VALUES ($1, NOW() - INTERVAL '8 days')`,
		userID)
	_, _ = pool.Exec(ctx,
		`INSERT INTO password_reset_attempts (user_id, requested_at) VALUES ($1, NOW() - INTERVAL '1 day')`,
		userID)

	err := PruneOldResetAttempts(ctx, pool)
	if err != nil {
		t.Fatalf("PruneOldResetAttempts failed: %v", err)
	}

	var count int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM password_reset_attempts WHERE user_id = $1`,
		userID).Scan(&count)
	if err != nil {
		t.Fatalf("failed to query password_reset_attempts: %v", err)
	}

	if count != 1 {
		t.Errorf("expected 1 row remaining after pruning, got %d", count)
	}

	var requestedAt time.Time
	err = pool.QueryRow(ctx,
		`SELECT requested_at FROM password_reset_attempts WHERE user_id = $1`,
		userID).Scan(&requestedAt)
	if err != nil {
		t.Fatalf("failed to query remaining attempt: %v", err)
	}

	if requestedAt.Before(time.Now().Add(-2 * 24 * time.Hour)) {
		t.Error("expected the recent row to remain, but found an old one instead")
	}
}
