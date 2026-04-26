package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// This file intentionally has no TestMain. The package-level TestMain in
// repository_test.go initialises the shared `pool`, runs applyMigration, and
// truncates tables around m.Run() for the entire package binary. Declaring a
// second TestMain here would cause a compile-time duplicate-symbol error.

// createTestUserWithPassword hashes password with HashPassword, inserts the user,
// and returns the UUID.
//
// @{"req": ["REQ-AUTH-001", "REQ-AUTH-002", "REQ-AUTH-003", "REQ-AUTH-006"]}
func createTestUserWithPassword(ctx context.Context, t *testing.T, p *pgxpool.Pool, username, password, role string) [16]byte {
	t.Helper()
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	var id [16]byte
	err = p.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
		username, hash, role).Scan(&id)
	if err != nil {
		t.Fatalf("failed to insert test user %q: %v", username, err)
	}
	return id
}

// @{"req": ["REQ-AUTH-001", "REQ-AUTH-002", "REQ-AUTH-003", "REQ-AUTH-005", "REQ-AUTH-006"]}
func newTestService(lockoutDuration, sessionMaxDuration time.Duration) *Service {
	return NewService(NewRepository(pool), lockoutDuration, sessionMaxDuration)
}

// @{"verifies": ["REQ-AUTH-001", "REQ-AUTH-006"]}
func TestLogin_BlankUsername(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	svc := newTestService(15*time.Minute, 24*time.Hour)
	_, _, err := svc.Login(context.Background(), "", "password")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

// @{"verifies": ["REQ-AUTH-001", "REQ-AUTH-006"]}
func TestLogin_BlankPassword(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	svc := newTestService(15*time.Minute, 24*time.Hour)
	_, _, err := svc.Login(context.Background(), "user", "")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

// @{"verifies": ["REQ-AUTH-001", "REQ-AUTH-006"]}
func TestLogin_UnknownUser(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	svc := newTestService(15*time.Minute, 24*time.Hour)
	_, _, err := svc.Login(context.Background(), "doesnotexist", "irrelevant")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

// @{"verifies": ["REQ-AUTH-001", "REQ-USER-004"]}
func TestLogin_InactiveAccount(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	userID := createTestUserWithPassword(ctx, t, pool, "inactive_user", "secret", "student")

	// Deactivate the account directly.
	if _, err := pool.Exec(ctx, `UPDATE users SET is_active = FALSE WHERE id = $1`, userID); err != nil {
		t.Fatalf("failed to deactivate user: %v", err)
	}

	svc := newTestService(15*time.Minute, 24*time.Hour)
	_, _, err := svc.Login(ctx, "inactive_user", "secret")
	if !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("expected ErrAccountDisabled, got %v", err)
	}
}

// @{"verifies": ["REQ-AUTH-001", "REQ-AUTH-002", "REQ-AUTH-003"]}
func TestLogin_Success(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "success_user", "correctpass", "admin")

	svc := newTestService(15*time.Minute, 24*time.Hour)
	rawToken, session, err := svc.Login(ctx, "success_user", "correctpass")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	if rawToken == "" {
		t.Fatal("expected non-empty rawToken")
	}
	if session == nil {
		t.Fatal("expected session to be returned")
	}
	if session.Role != "admin" {
		t.Errorf("expected role %q, got %q", "admin", session.Role)
	}
}

// TC-AUTH-016: Account is locked after five consecutive failed login attempts.
//
// @{"verifies": ["REQ-AUTH-006"]}
func TestTC_AUTH_016_LockedAfterFiveFailures(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "carol", "rightpass", "student")

	svc := newTestService(15*time.Minute, 24*time.Hour)
	for i := 0; i < 5; i++ {
		_, _, err := svc.Login(ctx, "carol", "wrongpass")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: expected ErrInvalidCredentials, got %v", i+1, err)
		}
	}

	_, _, err := svc.Login(ctx, "carol", "rightpass")
	if !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("expected ErrAccountLocked after 5 failures, got %v", err)
	}
}

// TC-AUTH-017: Account is NOT locked after four failed login attempts.
//
// @{"verifies": ["REQ-AUTH-006"]}
func TestTC_AUTH_017_NotLockedAfterFourFailures(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "dave", "rightpass", "student")

	svc := newTestService(15*time.Minute, 24*time.Hour)
	for i := 0; i < 4; i++ {
		_, _, err := svc.Login(ctx, "dave", "wrongpass")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: expected ErrInvalidCredentials, got %v", i+1, err)
		}
	}

	rawToken, session, err := svc.Login(ctx, "dave", "rightpass")
	if err != nil {
		t.Fatalf("expected success after 4 failures, got %v", err)
	}
	if rawToken == "" {
		t.Fatal("expected non-empty rawToken")
	}
	if session == nil {
		t.Fatal("expected session to be returned")
	}
}

// TC-AUTH-018: Locked account unlocks after lockout duration elapses.
//
// @{"verifies": ["REQ-AUTH-006"]}
func TestTC_AUTH_018_LockoutExpires(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "eve", "rightpass", "student")

	svc := newTestService(50*time.Millisecond, 24*time.Hour)
	for i := 0; i < 5; i++ {
		_, _, err := svc.Login(ctx, "eve", "wrongpass")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: expected ErrInvalidCredentials, got %v", i+1, err)
		}
	}

	time.Sleep(60 * time.Millisecond)

	rawToken, session, err := svc.Login(ctx, "eve", "rightpass")
	if err != nil {
		t.Fatalf("expected success after lockout expired, got %v", err)
	}
	if rawToken == "" {
		t.Fatal("expected non-empty rawToken")
	}
	if session == nil {
		t.Fatal("expected session to be returned")
	}
}

// TC-AUTH-019: Successful login resets failed attempt counter to zero.
//
// @{"verifies": ["REQ-AUTH-006"]}
func TestTC_AUTH_019_SuccessResetsCounter(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "frank", "rightpass", "student")

	svc := newTestService(15*time.Minute, 24*time.Hour)

	// Accumulate 3 failures.
	for i := 0; i < 3; i++ {
		_, _, err := svc.Login(ctx, "frank", "wrongpass")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: expected ErrInvalidCredentials, got %v", i+1, err)
		}
	}

	// Successful login resets counter.
	if _, _, err := svc.Login(ctx, "frank", "rightpass"); err != nil {
		t.Fatalf("expected successful login, got %v", err)
	}

	// Four more failures must not lock the account (counter was reset to 0).
	for i := 0; i < 4; i++ {
		_, _, err := svc.Login(ctx, "frank", "wrongpass")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("post-reset attempt %d: expected ErrInvalidCredentials, got %v", i+1, err)
		}
	}

	rawToken, session, err := svc.Login(ctx, "frank", "rightpass")
	if err != nil {
		t.Fatalf("expected account still unlocked after reset, got %v", err)
	}
	if rawToken == "" {
		t.Fatal("expected non-empty rawToken")
	}
	if session == nil {
		t.Fatal("expected session to be returned")
	}
}

// @{"verifies": ["REQ-AUTH-005"]}
func TestLogout_Idempotent(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	svc := newTestService(15*time.Minute, 24*time.Hour)
	// Deleting a session that was never created must not return an error.
	err := svc.Logout(context.Background(), "nonexistent-token-hash")
	if err != nil {
		t.Fatalf("Logout should be idempotent, got: %v", err)
	}
}

// @{"verifies": ["REQ-AUTH-005"]}
func TestLogout_DeletesSession(t *testing.T) {
	if pool == nil {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithPassword(ctx, t, pool, "logout_user", "pass123", "student")

	svc := newTestService(15*time.Minute, 24*time.Hour)
	rawToken, session, err := svc.Login(ctx, "logout_user", "pass123")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	tokenHash := HashToken(rawToken)
	if err := svc.Logout(ctx, tokenHash); err != nil {
		t.Fatalf("Logout failed: %v", err)
	}

	// Confirm the session is gone.
	_, err = svc.repo.GetSessionByTokenHash(ctx, session.TokenHash)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected session to be deleted, got: %v", err)
	}
}
