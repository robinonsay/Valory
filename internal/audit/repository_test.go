package audit

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		// No DB available; individual tests skip via pool == nil guard.
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

	// Truncate before running to clear any rows left by an interrupted previous run.
	if err := truncateAuditLog(ctx, pool); err != nil {
		panic(err)
	}

	code := m.Run()

	if err := truncateAuditLog(ctx, pool); err != nil {
		panic(err)
	}

	os.Exit(code)
}

func applyMigrations(ctx context.Context, p *pgxpool.Pool) error {
	// Migration 001: auth tables
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

	// Migration 002: audit_log and related tables
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

func truncateAuditLog(ctx context.Context, p *pgxpool.Pool) error {
	// Separate Exec calls to guarantee the table is cleared
	_, err := p.Exec(ctx, `TRUNCATE TABLE audit_log CASCADE`)
	return err
}

func createTestUser(ctx context.Context, t *testing.T, username, passwordHash, role string) uuid.UUID {
	var userID uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
		username, passwordHash, role).
		Scan(&userID)
	if err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}
	return userID
}

// @{"verifies": ["REQ-AUDIT-002"]}
func TestAppend_CreatesEntry(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	adminID := createTestUser(ctx, t, "admin_"+uuid.New().String(), "hash", "admin")

	entry := Entry{
		AdminID:    adminID,
		Action:     "CREATE_COURSE",
		TargetType: "course",
		TargetID:   nil,
		Payload: map[string]any{
			"title": "Introduction to Go",
		},
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("failed to begin transaction: %v", err)
	}
	defer tx.Rollback(ctx)

	err = repo.Append(ctx, tx, entry)
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	err = tx.Commit(ctx)
	if err != nil {
		t.Fatalf("failed to commit transaction: %v", err)
	}

	// Verify the row was created
	var id int64
	var prevHash, entryHash string
	err = pool.QueryRow(ctx,
		`SELECT id, prev_hash, entry_hash FROM audit_log WHERE admin_id = $1`,
		adminID).
		Scan(&id, &prevHash, &entryHash)
	if err != nil {
		t.Fatalf("failed to query audit_log: %v", err)
	}

	if id == 0 {
		t.Error("expected id to be non-zero")
	}
	if prevHash != GenesisHash {
		t.Errorf("expected prev_hash to be GenesisHash, got %q", prevHash)
	}
	if entryHash == "" {
		t.Error("expected entry_hash to be non-empty")
	}
}

// @{"verifies": ["REQ-AUDIT-002"]}
func TestAppend_ChainsPrevHash(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	adminID := createTestUser(ctx, t, "admin_"+uuid.New().String(), "hash", "admin")

	// Insert first entry
	entry1 := Entry{
		AdminID:    adminID,
		Action:     "CREATE_COURSE",
		TargetType: "course",
		TargetID:   nil,
		Payload: map[string]any{
			"title": "Course 1",
		},
	}

	tx1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("failed to begin transaction: %v", err)
	}

	err = repo.Append(ctx, tx1, entry1)
	if err != nil {
		tx1.Rollback(ctx)
		t.Fatalf("Append failed for first entry: %v", err)
	}

	err = tx1.Commit(ctx)
	if err != nil {
		t.Fatalf("failed to commit first transaction: %v", err)
	}

	// Get the first entry's hash
	var firstHash string
	err = pool.QueryRow(ctx,
		`SELECT entry_hash FROM audit_log WHERE admin_id = $1 ORDER BY id ASC LIMIT 1`,
		adminID).
		Scan(&firstHash)
	if err != nil {
		t.Fatalf("failed to get first entry hash: %v", err)
	}

	// Insert second entry
	entry2 := Entry{
		AdminID:    adminID,
		Action:     "DELETE_COURSE",
		TargetType: "course",
		TargetID:   nil,
		Payload: map[string]any{
			"title": "Course 1",
		},
	}

	tx2, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("failed to begin second transaction: %v", err)
	}

	err = repo.Append(ctx, tx2, entry2)
	if err != nil {
		tx2.Rollback(ctx)
		t.Fatalf("Append failed for second entry: %v", err)
	}

	err = tx2.Commit(ctx)
	if err != nil {
		t.Fatalf("failed to commit second transaction: %v", err)
	}

	// Get the second entry's prev_hash
	var secondPrevHash string
	err = pool.QueryRow(ctx,
		`SELECT prev_hash FROM audit_log WHERE admin_id = $1 ORDER BY id DESC LIMIT 1`,
		adminID).
		Scan(&secondPrevHash)
	if err != nil {
		t.Fatalf("failed to get second entry prev_hash: %v", err)
	}

	if secondPrevHash != firstHash {
		t.Errorf("expected second entry's prev_hash to equal first entry's hash, got %q, expected %q", secondPrevHash, firstHash)
	}
}

// @{"verifies": ["REQ-AUDIT-001"]}
func TestListPaginated_ReturnsRows(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	adminID := createTestUser(ctx, t, "admin_"+uuid.New().String(), "hash", "admin")

	// Insert 3 entries
	for i := 1; i <= 3; i++ {
		entry := Entry{
			AdminID:    adminID,
			Action:     "ACTION_" + string(rune('0'+i)),
			TargetType: "type",
			TargetID:   nil,
			Payload:    map[string]any{"index": i},
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("failed to begin transaction: %v", err)
		}

		err = repo.Append(ctx, tx, entry)
		if err != nil {
			tx.Rollback(ctx)
			t.Fatalf("Append failed: %v", err)
		}

		err = tx.Commit(ctx)
		if err != nil {
			t.Fatalf("failed to commit transaction: %v", err)
		}
	}

	// List with limit 2
	rows, err := repo.ListPaginated(ctx, 2, 0)
	if err != nil {
		t.Fatalf("ListPaginated failed: %v", err)
	}

	if len(rows) != 2 {
		t.Errorf("expected 2 rows, got %d", len(rows))
	}

	// Verify descending order (most recent first)
	if len(rows) >= 2 && rows[0].ID <= rows[1].ID {
		t.Error("expected rows in descending id order")
	}
}

// @{"verifies": ["REQ-AUDIT-001"]}
func TestListPaginated_CursorPagination(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	// Truncate so we control exactly how many rows exist and can make precise
	// assertions about page sizes without interference from other tests.
	if err := truncateAuditLog(ctx, pool); err != nil {
		t.Fatalf("failed to truncate audit_log: %v", err)
	}

	adminID := createTestUser(ctx, t, "admin_"+uuid.New().String(), "hash", "admin")

	// Insert exactly 3 entries so page 1 (limit 2) has 2 rows and page 2 has 1.
	var insertedIDs []int64
	for i := 1; i <= 3; i++ {
		entry := Entry{
			AdminID:    adminID,
			Action:     "ACTION_" + string(rune('0'+i)),
			TargetType: "type",
			TargetID:   nil,
			Payload:    map[string]any{"index": i},
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("failed to begin transaction: %v", err)
		}

		err = repo.Append(ctx, tx, entry)
		if err != nil {
			tx.Rollback(ctx)
			t.Fatalf("Append failed: %v", err)
		}

		if err = tx.Commit(ctx); err != nil {
			t.Fatalf("failed to commit transaction: %v", err)
		}

		var id int64
		if err = pool.QueryRow(ctx, `SELECT id FROM audit_log ORDER BY id DESC LIMIT 1`).Scan(&id); err != nil {
			t.Fatalf("failed to fetch inserted id: %v", err)
		}
		insertedIDs = append(insertedIDs, id)
	}

	// Page 1: 2 most recent rows (descending).
	page1, err := repo.ListPaginated(ctx, 2, 0)
	if err != nil {
		t.Fatalf("ListPaginated page 1 failed: %v", err)
	}
	if len(page1) != 2 {
		t.Errorf("expected 2 rows on page 1, got %d", len(page1))
	}

	// Page 2: cursor is the smallest ID from page 1.
	cursor := page1[len(page1)-1].ID
	page2, err := repo.ListPaginated(ctx, 2, cursor)
	if err != nil {
		t.Fatalf("ListPaginated page 2 failed: %v", err)
	}
	if len(page2) != 1 {
		t.Errorf("expected 1 row on page 2, got %d", len(page2))
	}

	// No ID overlap between pages.
	for _, r1 := range page1 {
		for _, r2 := range page2 {
			if r1.ID == r2.ID {
				t.Error("expected no overlap between pages")
			}
		}
	}
}

// @{"verifies": ["REQ-AUDIT-002"]}
func TestStreamAll_AscendingOrder(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	adminID := createTestUser(ctx, t, "admin_"+uuid.New().String(), "hash", "admin")

	// Insert 3 entries
	for i := 1; i <= 3; i++ {
		entry := Entry{
			AdminID:    adminID,
			Action:     "ACTION_" + string(rune('0'+i)),
			TargetType: "type",
			TargetID:   nil,
			Payload:    map[string]any{"index": i},
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("failed to begin transaction: %v", err)
		}

		err = repo.Append(ctx, tx, entry)
		if err != nil {
			tx.Rollback(ctx)
			t.Fatalf("Append failed: %v", err)
		}

		err = tx.Commit(ctx)
		if err != nil {
			t.Fatalf("failed to commit transaction: %v", err)
		}
	}

	// Stream all and collect rows for this admin.
	var adminRows []AuditRow
	err := repo.StreamAll(ctx, func(row AuditRow) error {
		if row.AdminID == adminID {
			adminRows = append(adminRows, row)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("StreamAll failed: %v", err)
	}

	if len(adminRows) != 3 {
		t.Errorf("expected 3 rows for this admin, got %d", len(adminRows))
	}

	// Verify ascending order (oldest first).
	for i := 0; i < len(adminRows)-1; i++ {
		if adminRows[i].ID >= adminRows[i+1].ID {
			t.Error("expected rows in ascending id order")
		}
	}
}

// @{"verifies": ["REQ-AUDIT-002"]}
func TestStreamAll_BatchBoundary(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	adminID := createTestUser(ctx, t, "admin_"+uuid.New().String(), "hash", "admin")

	// Insert 1001 entries
	for i := 1; i <= 1001; i++ {
		entry := Entry{
			AdminID:    adminID,
			Action:     "ACTION",
			TargetType: "type",
			TargetID:   nil,
			Payload:    map[string]any{"index": i},
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("failed to begin transaction: %v", err)
		}

		err = repo.Append(ctx, tx, entry)
		if err != nil {
			tx.Rollback(ctx)
			t.Fatalf("Append failed: %v", err)
		}

		err = tx.Commit(ctx)
		if err != nil {
			t.Fatalf("failed to commit transaction: %v", err)
		}
	}

	// Stream all and collect rows for this admin.
	var adminRows []AuditRow
	err := repo.StreamAll(ctx, func(row AuditRow) error {
		if row.AdminID == adminID {
			adminRows = append(adminRows, row)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("StreamAll failed: %v", err)
	}

	if len(adminRows) != 1001 {
		t.Errorf("expected 1001 rows for this admin, got %d", len(adminRows))
	}
}
