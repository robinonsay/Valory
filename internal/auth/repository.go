package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrUserNotFound    = errors.New("auth: user not found")
	ErrSessionNotFound = errors.New("auth: session not found")
	ErrPendingNotFound = errors.New("auth: pending 2FA state not found")
)

type User struct {
	ID                 [16]byte
	Username           string
	Email              string // empty string means no email on file
	PasswordHash       string
	Role               string
	IsActive           bool
	MustChangePassword bool
	FailedLoginCount   int
	LockedUntil        *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// PendingTwoFARow is a row from the pending_2fa table.
//
// @{"req": ["REQ-AUTH-015", "REQ-AUTH-016", "REQ-AUTH-017", "REQ-AUTH-026"]}
type PendingTwoFARow struct {
	ID                [16]byte
	UserID            [16]byte
	PendingTokenHash  string
	OTPHash           string
	AttemptCount      int
	LastResendAt      *time.Time
	ResendCount24h    int
	ResendWindowStart *time.Time
	ExpiresAt         time.Time
	CreatedAt         time.Time
}

type Session struct {
	ID                 [16]byte
	UserID             [16]byte
	TokenHash          string
	Role               string
	MustChangePassword bool
	LastActiveAt       time.Time
	ExpiresAt          time.Time
	CreatedAt          time.Time
}

type Repository struct {
	pool *pgxpool.Pool
}

// @{"req": ["REQ-AUTH-001"]}
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// @{"req": ["REQ-AUTH-001", "REQ-AUTH-013", "REQ-AUTH-015", "REQ-AUTH-023"]}
func (r *Repository) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT id, username, COALESCE(email, ''), password_hash, role, is_active, must_change_password, failed_login_count, locked_until, created_at, updated_at
		 FROM users WHERE username = $1`,
		username)

	var user User
	err := row.Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.PasswordHash,
		&user.Role,
		&user.IsActive,
		&user.MustChangePassword,
		&user.FailedLoginCount,
		&user.LockedUntil,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

// GetUserByID fetches a user row by UUID. Used by the 2FA verify path to
// resolve the user when only the pending_2fa.user_id is available.
//
// @{"req": ["REQ-AUTH-015", "REQ-AUTH-023"]}
func (r *Repository) GetUserByID(ctx context.Context, userID [16]byte) (*User, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT id, username, COALESCE(email, ''), password_hash, role, is_active, must_change_password, failed_login_count, locked_until, created_at, updated_at
		 FROM users WHERE id = $1`,
		userID)

	var user User
	err := row.Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.PasswordHash,
		&user.Role,
		&user.IsActive,
		&user.MustChangePassword,
		&user.FailedLoginCount,
		&user.LockedUntil,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

// @{"req": ["REQ-AUTH-006"]}
func (r *Repository) RecordLoginAttempt(ctx context.Context, userID [16]byte, success bool) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO login_attempts (user_id, success) VALUES ($1, $2)`,
		userID, success)
	return err
}

// @{"req": ["REQ-AUTH-006"]}
func (r *Repository) SetLockoutState(ctx context.Context, userID [16]byte, failedCount int, lockedUntil *time.Time) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE users SET failed_login_count = $1, locked_until = $2 WHERE id = $3`,
		failedCount, lockedUntil, userID)
	return err
}

// @{"req": ["REQ-AUTH-006"]}
func (r *Repository) ResetLoginState(ctx context.Context, userID [16]byte) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE users SET failed_login_count = 0, locked_until = NULL WHERE id = $1`,
		userID)
	return err
}

// @{"req": ["REQ-AUTH-002", "REQ-AUTH-003", "REQ-AUTH-013"]}
func (r *Repository) CreateSession(ctx context.Context, userID [16]byte, tokenHash, role string, expiresAt time.Time, mustChangePassword bool) (*Session, error) {
	var session Session
	err := r.pool.QueryRow(ctx,
		`INSERT INTO sessions (user_id, token_hash, role, expires_at)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, user_id, token_hash, role, last_active_at, expires_at, created_at`,
		userID, tokenHash, role, expiresAt).
		Scan(
			&session.ID,
			&session.UserID,
			&session.TokenHash,
			&session.Role,
			&session.LastActiveAt,
			&session.ExpiresAt,
			&session.CreatedAt,
		)
	if err != nil {
		return nil, err
	}
	// Carry must_change_password from the users row into the in-memory session
	// so the login handler can include it in the response (REQ-AUTH-013) without
	// a second DB round-trip. The field is NOT persisted in the sessions table —
	// the authoritative value lives in users.must_change_password.
	session.MustChangePassword = mustChangePassword
	return &session, nil
}

// @{"req": ["REQ-AUTH-005"]}
func (r *Repository) GetSessionByTokenHash(ctx context.Context, tokenHash string) (*Session, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT id, user_id, token_hash, role, last_active_at, expires_at, created_at
		 FROM sessions WHERE token_hash = $1`,
		tokenHash)

	var session Session
	err := row.Scan(
		&session.ID,
		&session.UserID,
		&session.TokenHash,
		&session.Role,
		&session.LastActiveAt,
		&session.ExpiresAt,
		&session.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSessionNotFound
		}
		return nil, err
	}
	return &session, nil
}

// @{"req": ["REQ-AUTH-005"]}
func (r *Repository) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM sessions WHERE token_hash = $1`,
		tokenHash)
	if err != nil {
		return err
	}
	return nil
}

// @{"req": ["REQ-AUTH-005"]}
func (r *Repository) UpdateLastActiveAt(ctx context.Context, tokenHash string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE sessions SET last_active_at = NOW() WHERE token_hash = $1`,
		tokenHash)
	return err
}

// @{"req": ["REQ-USER-003"]}
func (r *Repository) DeleteAllUserSessions(ctx context.Context, userID [16]byte) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM sessions WHERE user_id = $1`,
		userID)
	return err
}

// --- Pending-2FA repository methods ---

// CreatePending inserts a new pending_2fa row. It first deletes any existing
// pending row for this user (one pending state per user) and prunes globally
// expired rows older than 1 hour as opportunistic cleanup.
//
// @{"req": ["REQ-AUTH-015", "REQ-AUTH-016"]}
func (r *Repository) CreatePending(ctx context.Context, userID [16]byte, pendingTokenHash, otpHash string, expiresAt time.Time) (*PendingTwoFARow, error) {
	// Delete any prior pending row for this user so they cannot accumulate stale
	// pending tokens. A user can only have one active pending-2FA state at a time.
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM pending_2fa WHERE user_id = $1`, userID,
	); err != nil {
		return nil, err
	}

	// Opportunistic cleanup: remove globally expired rows older than 1 hour.
	// The grace period avoids a full-table scan on every login.
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM pending_2fa WHERE expires_at < NOW() - INTERVAL '1 hour'`,
	); err != nil {
		return nil, err
	}

	var row PendingTwoFARow
	err := r.pool.QueryRow(ctx,
		`INSERT INTO pending_2fa (user_id, pending_token_hash, otp_hash, expires_at)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, user_id, pending_token_hash, otp_hash, attempt_count,
		           last_resend_at, resend_count_24h, resend_window_start, expires_at, created_at`,
		userID, pendingTokenHash, otpHash, expiresAt,
	).Scan(
		&row.ID,
		&row.UserID,
		&row.PendingTokenHash,
		&row.OTPHash,
		&row.AttemptCount,
		&row.LastResendAt,
		&row.ResendCount24h,
		&row.ResendWindowStart,
		&row.ExpiresAt,
		&row.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// GetPendingByTokenHash looks up a pending_2fa row by the pending token hash.
// Returns ErrPendingNotFound when no row matches.
//
// @{"req": ["REQ-AUTH-015", "REQ-AUTH-016", "REQ-AUTH-017"]}
func (r *Repository) GetPendingByTokenHash(ctx context.Context, tokenHash string) (*PendingTwoFARow, error) {
	var row PendingTwoFARow
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, pending_token_hash, otp_hash, attempt_count,
		        last_resend_at, resend_count_24h, resend_window_start, expires_at, created_at
		 FROM pending_2fa WHERE pending_token_hash = $1`,
		tokenHash,
	).Scan(
		&row.ID,
		&row.UserID,
		&row.PendingTokenHash,
		&row.OTPHash,
		&row.AttemptCount,
		&row.LastResendAt,
		&row.ResendCount24h,
		&row.ResendWindowStart,
		&row.ExpiresAt,
		&row.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPendingNotFound
		}
		return nil, err
	}
	return &row, nil
}

// IncrementAttempt atomically increments the attempt_count for the pending row
// identified by its primary key. Returns the updated count.
//
// @{"req": ["REQ-AUTH-017"]}
func (r *Repository) IncrementAttempt(ctx context.Context, pendingID [16]byte) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`UPDATE pending_2fa SET attempt_count = attempt_count + 1
		 WHERE id = $1 RETURNING attempt_count`,
		pendingID,
	).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// DeletePending removes a pending_2fa row by primary key (used on successful
// verify and on attempt cap).
//
// @{"req": ["REQ-AUTH-015", "REQ-AUTH-016", "REQ-AUTH-017"]}
func (r *Repository) DeletePending(ctx context.Context, pendingID [16]byte) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM pending_2fa WHERE id = $1`, pendingID)
	return err
}

// DeletePendingByUserID removes any pending_2fa row for a given user (admin
// reset path). It is idempotent: no error when no row exists.
//
// @{"req": ["REQ-AUTH-019"]}
func (r *Repository) DeletePendingByUserID(ctx context.Context, userID [16]byte) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM pending_2fa WHERE user_id = $1`, userID)
	return err
}

// UpdateOTPHash replaces the otp_hash on a pending row (resend path) and
// updates resend throttle counters. The daily window resets when
// resend_window_start is more than 24 hours in the past.
//
// @{"req": ["REQ-AUTH-026"]}
func (r *Repository) UpdateOTPHash(ctx context.Context, pendingID [16]byte, otpHash string, now time.Time, resendCount24h int, windowStart *time.Time) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE pending_2fa
		 SET otp_hash = $1, last_resend_at = $2, resend_count_24h = $3, resend_window_start = $4
		 WHERE id = $5`,
		otpHash, now, resendCount24h, windowStart, pendingID,
	)
	return err
}

// RecordOTPSend inserts a row in otp_rate_limits for audit/cap purposes and
// prunes rows older than 24 hours for this user in the same call.
//
// @{"req": ["REQ-AUTH-026"]}
func (r *Repository) RecordOTPSend(ctx context.Context, userID [16]byte) error {
	// Prune stale rows first so the table does not grow unboundedly.
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM otp_rate_limits WHERE user_id = $1 AND sent_at < NOW() - INTERVAL '24 hours'`,
		userID,
	); err != nil {
		return err
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO otp_rate_limits (user_id) VALUES ($1)`, userID)
	return err
}
