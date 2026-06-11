//go:build integration

// integration_test.go — repository-layer integration tests for the auth module.
//
// These tests run against a real PostgreSQL instance provisioned by the shared
// harness in internal/db (package db, build tag "integration"). They exercise
// schema-level behaviours — triggers, CHECK constraints, UNIQUE constraints, and
// CASCADE FK rules — that the inline-schema tests in repository_test.go cannot
// faithfully reproduce because those tests use a hand-written DDL snapshot
// rather than the real embedded migrations.
//
// Run with:
//
//	make test-integration
//	# or manually:
//	go test -tags integration -run TestInteg ./internal/auth/
//
// The harness skips all tests automatically when the test container is not
// running, so `go test ./...` remains green in CI without Docker.
package auth

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	internaldb "github.com/valory/valory/internal/db"
)

// integTables lists every auth-owned table that must be clean at the start of
// each integration test. The order is child-to-parent so that the single
// TRUNCATE ... RESTART IDENTITY CASCADE in TruncateTables handles FK cycles
// without needing separate statements.
var integTables = []string{
	"login_attempts",
	"password_reset_attempts",
	"student_consent",
	"password_reset_tokens",
	"sessions",
	"users",
}

// TestInteg_SetUpdatedAtTrigger_FiresOnUpdate verifies that the set_updated_at
// trigger defined in the real migration advances updated_at whenever a users row
// is modified via SetLockoutState. The inline schema in repository_test.go
// defines equivalent DDL, but running against the embedded migration confirms
// that the migration file itself creates a working trigger — not just the
// test's in-memory replica.
//
// @{"verifies": ["REQ-AUTH-006"]}
func TestInteg_SetUpdatedAtTrigger_FiresOnUpdate(t *testing.T) {
	pool := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, pool, integTables...)

	ctx := context.Background()
	repo := NewRepository(pool)

	// Seed a user directly; capture the initial updated_at.
	var userID [16]byte
	var createdAt, updatedAt time.Time
	err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role)
		 VALUES ($1, $2, $3)
		 RETURNING id, created_at, updated_at`,
		"integ_trigger_user", "hash_trigger", "student",
	).Scan(&userID, &createdAt, &updatedAt)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// Backdate updated_at by one second so that the next UPDATE's NOW() is
	// guaranteed to be strictly greater, even on virtualized hosts where two
	// consecutive transactions can share a millisecond timestamp.
	if _, err := pool.Exec(ctx,
		`UPDATE users SET updated_at = NOW() - INTERVAL '1 second' WHERE id = $1`,
		userID,
	); err != nil {
		t.Fatalf("backdate updated_at: %v", err)
	}
	// Re-read the backdated value to use as the comparison baseline.
	if err := pool.QueryRow(ctx,
		`SELECT updated_at FROM users WHERE id = $1`, userID,
	).Scan(&updatedAt); err != nil {
		t.Fatalf("read backdated updated_at: %v", err)
	}

	// SetLockoutState issues an UPDATE, which must fire the set_updated_at
	// trigger and advance the updated_at column past the backdated value.
	lockedUntil := time.Now().Add(10 * time.Minute)
	if err := repo.SetLockoutState(ctx, userID, 1, &lockedUntil); err != nil {
		t.Fatalf("SetLockoutState: %v", err)
	}

	var afterUpdate time.Time
	err = pool.QueryRow(ctx,
		`SELECT updated_at FROM users WHERE id = $1`, userID,
	).Scan(&afterUpdate)
	if err != nil {
		t.Fatalf("query updated_at: %v", err)
	}

	// The trigger must have advanced updated_at past the backdated baseline.
	if !afterUpdate.After(updatedAt) {
		t.Errorf(
			"set_updated_at trigger did not fire: updated_at = %v, want > %v",
			afterUpdate, updatedAt,
		)
	}
}

// TestInteg_UniqueUsername_ConstraintViolation verifies that the UNIQUE(username)
// constraint on the users table rejects a duplicate username at the database
// level. This catches a regression where the migration accidentally drops or
// weakens the constraint.
//
// @{"verifies": ["REQ-AUTH-001"]}
func TestInteg_UniqueUsername_ConstraintViolation(t *testing.T) {
	pool := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, pool, integTables...)

	ctx := context.Background()

	// First insert must succeed.
	_, err := pool.Exec(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3)`,
		"integ_dup_user", "hash", "student",
	)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}

	// Second insert with the same username must fail with a unique-violation
	// error. We check the PostgreSQL error code 23505 (unique_violation).
	_, dupErr := pool.Exec(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3)`,
		"integ_dup_user", "hash2", "admin",
	)
	if dupErr == nil {
		t.Fatal("expected unique-constraint violation, got nil error")
	}
	// PostgreSQL error code 23505 means unique_violation. Check that the error
	// message contains the column or constraint so the failure is diagnosable.
	if !strings.Contains(dupErr.Error(), "23505") && !strings.Contains(strings.ToLower(dupErr.Error()), "unique") {
		t.Errorf("expected unique-violation error, got: %v", dupErr)
	}
}

// TestInteg_UserRoleCheckConstraint_RejectsInvalidRole verifies that the
// CHECK (role IN ('student', 'admin')) constraint on the users table rejects
// an invalid role value. This catches a regression where the migration
// accidentally drops or weakens the constraint.
//
// @{"verifies": ["REQ-AUTH-003"]}
func TestInteg_UserRoleCheckConstraint_RejectsInvalidRole(t *testing.T) {
	pool := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, pool, integTables...)

	ctx := context.Background()

	_, err := pool.Exec(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3)`,
		"integ_bad_role_user", "hash", "superuser",
	)
	if err == nil {
		t.Fatal("expected CHECK constraint violation for invalid role, got nil error")
	}
	// PostgreSQL error code 23514 = check_violation.
	if !strings.Contains(err.Error(), "23514") && !strings.Contains(strings.ToLower(err.Error()), "check") {
		t.Errorf("expected check-constraint error, got: %v", err)
	}
}

// TestInteg_SessionRoleCheckConstraint_RejectsInvalidRole verifies that the
// sessions table's own CHECK (role IN ('student', 'admin')) constraint rejects
// an out-of-range role value passed through CreateSession. This is distinct from
// the users table constraint; both must be present to prevent privilege escalation
// via direct session insertion.
//
// @{"verifies": ["REQ-AUTH-003"]}
func TestInteg_SessionRoleCheckConstraint_RejectsInvalidRole(t *testing.T) {
	pool := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, pool, integTables...)

	ctx := context.Background()
	repo := NewRepository(pool)

	// Seed a valid user first (sessions FK references users.id).
	var userID [16]byte
	err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
		"integ_sess_role_user", "hash", "student",
	).Scan(&userID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// CreateSession with an invalid role must surface a check-violation error
	// from the database (role CHECK on the sessions table), not silently succeed.
	_, createErr := repo.CreateSession(ctx, userID, "integ_bad_role_token", "moderator", time.Now().Add(time.Hour))
	if createErr == nil {
		t.Fatal("expected CHECK constraint violation for invalid session role, got nil error")
	}
	if !strings.Contains(createErr.Error(), "23514") && !strings.Contains(strings.ToLower(createErr.Error()), "check") {
		t.Errorf("expected check-constraint error, got: %v", createErr)
	}
}

// TestInteg_SessionTokenHashUnique_ConstraintViolation verifies that the
// UNIQUE(token_hash) index on the sessions table is present in the real migration
// and rejects duplicate token hashes through CreateSession. A missing or dropped
// UNIQUE index would allow session token collisions, enabling session hijacking.
//
// @{"verifies": ["REQ-AUTH-002"]}
func TestInteg_SessionTokenHashUnique_ConstraintViolation(t *testing.T) {
	pool := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, pool, integTables...)

	ctx := context.Background()
	repo := NewRepository(pool)

	var userID [16]byte
	err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
		"integ_dup_token_user", "hash", "student",
	).Scan(&userID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	expiresAt := time.Now().Add(time.Hour)
	dupTokenHash := "integ_dup_token_hash_abc123"

	// First CreateSession must succeed.
	if _, err := repo.CreateSession(ctx, userID, dupTokenHash, "student", expiresAt); err != nil {
		t.Fatalf("first CreateSession: %v", err)
	}

	// Second CreateSession with the same token_hash must fail.
	_, dupErr := repo.CreateSession(ctx, userID, dupTokenHash, "student", expiresAt)
	if dupErr == nil {
		t.Fatal("expected unique-constraint violation on token_hash, got nil error")
	}
	if !strings.Contains(dupErr.Error(), "23505") && !strings.Contains(strings.ToLower(dupErr.Error()), "unique") {
		t.Errorf("expected unique-violation error, got: %v", dupErr)
	}
}

// TestInteg_DeleteUser_CascadesToSessionsAndLoginAttempts verifies that the
// ON DELETE CASCADE FK rules on sessions and login_attempts automatically remove
// all child rows when a user row is deleted. This is required for permanent
// deletion of all personal data belonging to a student account.
//
// @{"verifies": ["REQ-USER-007"]}
func TestInteg_DeleteUser_CascadesToSessionsAndLoginAttempts(t *testing.T) {
	pool := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, pool, integTables...)

	ctx := context.Background()
	repo := NewRepository(pool)

	var userID [16]byte
	err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
		"integ_cascade_user", "hash", "student",
	).Scan(&userID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// Create a session and a login_attempt row for this user.
	if _, err := repo.CreateSession(ctx, userID, "integ_cascade_token", "student", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := repo.RecordLoginAttempt(ctx, userID, true); err != nil {
		t.Fatalf("RecordLoginAttempt: %v", err)
	}

	// Confirm the child rows exist before deletion.
	var sessCount, attemptCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM sessions WHERE user_id = $1`, userID).Scan(&sessCount); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessCount != 1 {
		t.Fatalf("expected 1 session before cascade, got %d", sessCount)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM login_attempts WHERE user_id = $1`, userID).Scan(&attemptCount); err != nil {
		t.Fatalf("count login_attempts: %v", err)
	}
	if attemptCount != 1 {
		t.Fatalf("expected 1 login_attempt before cascade, got %d", attemptCount)
	}

	// Delete the user directly — this triggers ON DELETE CASCADE on child tables.
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	// Both child rows must now be gone.
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM sessions WHERE user_id = $1`, userID).Scan(&sessCount); err != nil {
		t.Fatalf("count sessions after cascade: %v", err)
	}
	if sessCount != 0 {
		t.Errorf("expected 0 sessions after user DELETE CASCADE, got %d", sessCount)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM login_attempts WHERE user_id = $1`, userID).Scan(&attemptCount); err != nil {
		t.Fatalf("count login_attempts after cascade: %v", err)
	}
	if attemptCount != 0 {
		t.Errorf("expected 0 login_attempts after user DELETE CASCADE, got %d", attemptCount)
	}
}

// TestInteg_Migration002EmailColumn_UniqueConstraintEnforced verifies that
// migration 002 adds the email column to users and enforces UNIQUE on it. Both
// the column's existence and the constraint must be present; a missing constraint
// would allow duplicate email addresses, undermining account-identity guarantees.
//
// @{"verifies": ["REQ-AUTH-001"]}
func TestInteg_Migration002EmailColumn_UniqueConstraintEnforced(t *testing.T) {
	pool := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, pool, integTables...)

	ctx := context.Background()

	// Insert two users with the same non-NULL email. The second must fail.
	_, err := pool.Exec(ctx,
		`INSERT INTO users (username, password_hash, role, email) VALUES ($1, $2, $3, $4)`,
		"integ_email_user1", "hash", "student", "shared@example.com",
	)
	if err != nil {
		// If email column does not exist the INSERT itself fails — that too is a
		// migration regression we want to catch.
		t.Fatalf("first insert with email: %v (migration 002 may not have run)", err)
	}

	_, dupErr := pool.Exec(ctx,
		`INSERT INTO users (username, password_hash, role, email) VALUES ($1, $2, $3, $4)`,
		"integ_email_user2", "hash2", "admin", "shared@example.com",
	)
	if dupErr == nil {
		t.Fatal("expected unique-constraint violation on email, got nil error")
	}
	if !strings.Contains(dupErr.Error(), "23505") && !strings.Contains(strings.ToLower(dupErr.Error()), "unique") {
		t.Errorf("expected unique-violation error for duplicate email, got: %v", dupErr)
	}
}

// TestInteg_Migration002PasswordResetTokens_TableAndFK verifies that migration
// 002 creates the password_reset_tokens table with a working FK to users(id) ON
// DELETE CASCADE. This table is absent from the inline schema used by
// repository_test.go; confirming both its existence and its cascade rule ensures
// the migration was applied completely.
//
// @{"verifies": ["REQ-AUTH-001"]}
func TestInteg_Migration002PasswordResetTokens_TableAndFK(t *testing.T) {
	pool := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, pool, integTables...)

	ctx := context.Background()

	// Seed a user.
	var userID [16]byte
	err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
		"integ_prt_user", "hash", "student",
	).Scan(&userID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// Insert a password_reset_token row to confirm the table exists and
	// the user_id FK is valid.
	_, err = pool.Exec(ctx,
		`INSERT INTO password_reset_tokens (user_id, token_hash, expires_at)
		 VALUES ($1, $2, $3)`,
		userID, fmt.Sprintf("prt_token_%x", userID), time.Now().Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("insert password_reset_token (migration 002 may not have run): %v", err)
	}

	// Deleting the user must cascade to password_reset_tokens (ON DELETE CASCADE).
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM password_reset_tokens WHERE user_id = $1`, userID,
	).Scan(&count); err != nil {
		t.Fatalf("count password_reset_tokens: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 password_reset_tokens after user CASCADE DELETE, got %d", count)
	}
}

// TestInteg_SetUpdatedAtTrigger_FiresOnResetLoginState verifies that the
// set_updated_at trigger fires for the ResetLoginState UPDATE path, not just for
// SetLockoutState. Guarding both UPDATE paths prevents a regression where the
// trigger is accidentally dropped or narrowed to specific column combinations,
// leaving updated_at stale after a login-state reset.
//
// @{"verifies": ["REQ-AUTH-006"]}
func TestInteg_SetUpdatedAtTrigger_FiresOnResetLoginState(t *testing.T) {
	pool := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, pool, integTables...)

	ctx := context.Background()
	repo := NewRepository(pool)

	var userID [16]byte
	var initialUpdatedAt time.Time
	err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role)
		 VALUES ($1, $2, $3)
		 RETURNING id, updated_at`,
		"integ_reset_trigger_user", "hash_reset", "admin",
	).Scan(&userID, &initialUpdatedAt)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// First, apply a lockout state so that ResetLoginState has a meaningful
	// UPDATE to perform (changing locked_until from non-NULL back to NULL).
	lockedUntil := time.Now().Add(5 * time.Minute)
	if err := repo.SetLockoutState(ctx, userID, 3, &lockedUntil); err != nil {
		t.Fatalf("SetLockoutState: %v", err)
	}

	// Capture the updated_at after the lockout set, then backdate it by one
	// second so the next UPDATE's NOW() is guaranteed to be strictly greater,
	// even on virtualized hosts where two consecutive transactions can share a
	// millisecond timestamp.
	var afterLockout time.Time
	if err := pool.QueryRow(ctx, `SELECT updated_at FROM users WHERE id = $1`, userID).Scan(&afterLockout); err != nil {
		t.Fatalf("query updated_at after lockout: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE users SET updated_at = NOW() - INTERVAL '1 second' WHERE id = $1`,
		userID,
	); err != nil {
		t.Fatalf("backdate updated_at before reset: %v", err)
	}
	// Re-read the backdated value to use as the comparison baseline.
	if err := pool.QueryRow(ctx, `SELECT updated_at FROM users WHERE id = $1`, userID).Scan(&afterLockout); err != nil {
		t.Fatalf("read backdated updated_at: %v", err)
	}

	// ResetLoginState issues an UPDATE — trigger must fire again and advance
	// updated_at past the backdated baseline.
	if err := repo.ResetLoginState(ctx, userID); err != nil {
		t.Fatalf("ResetLoginState: %v", err)
	}

	var afterReset time.Time
	if err := pool.QueryRow(ctx, `SELECT updated_at FROM users WHERE id = $1`, userID).Scan(&afterReset); err != nil {
		t.Fatalf("query updated_at after reset: %v", err)
	}

	if !afterReset.After(afterLockout) {
		t.Errorf(
			"set_updated_at trigger did not fire on ResetLoginState: updated_at = %v, want > %v",
			afterReset, afterLockout,
		)
	}
}
