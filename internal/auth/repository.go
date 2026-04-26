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
)

type User struct {
	ID               [16]byte
	Username         string
	PasswordHash     string
	Role             string
	IsActive         bool
	FailedLoginCount int
	LockedUntil      *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Session struct {
	ID           [16]byte
	UserID       [16]byte
	TokenHash    string
	Role         string
	LastActiveAt time.Time
	ExpiresAt    time.Time
	CreatedAt    time.Time
}

type Repository struct {
	pool *pgxpool.Pool
}

// @{"req": ["REQ-AUTH-001"]}
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// @{"req": ["REQ-AUTH-001"]}
func (r *Repository) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT id, username, password_hash, role, is_active, failed_login_count, locked_until, created_at, updated_at
		 FROM users WHERE username = $1`,
		username)

	var user User
	err := row.Scan(
		&user.ID,
		&user.Username,
		&user.PasswordHash,
		&user.Role,
		&user.IsActive,
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

// @{"req": ["REQ-AUTH-002", "REQ-AUTH-003"]}
func (r *Repository) CreateSession(ctx context.Context, userID [16]byte, tokenHash, role string, expiresAt time.Time) (*Session, error) {
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
