package user

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrUserNotFound      = errors.New("user not found")
	ErrDuplicateUsername = errors.New("username already taken")
	ErrNoFieldsToUpdate  = errors.New("no fields to update")
	ErrNotAStudent       = errors.New("target account is not a student")
	ErrTokenNotFound     = errors.New("reset token not found or expired")
	ErrConsentNotFound   = errors.New("consent record not found")
)

type UserRow struct {
	ID           uuid.UUID
	Username     string
	Email        *string
	PasswordHash string
	Role         string
	IsActive     bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type ResetTokenRow struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
}

type UpdateFields struct {
	Username     *string
	Email        *string
	PasswordHash *string
}

type Repository struct {
	pool *pgxpool.Pool
}

// @{"req": ["REQ-USER-001"]}
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// @{"req": ["REQ-USER-002"]}
func (r *Repository) CreateUser(ctx context.Context, username string, email *string, passwordHash, role string) (UserRow, error) {
	var user UserRow
	err := r.pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash, role)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, username, email, password_hash, role, is_active, created_at, updated_at`,
		username, email, passwordHash, role).
		Scan(
			&user.ID,
			&user.Username,
			&user.Email,
			&user.PasswordHash,
			&user.Role,
			&user.IsActive,
			&user.CreatedAt,
			&user.UpdatedAt,
		)
	if err != nil {
		if pgErr, ok := err.(*pgconn.PgError); ok && pgErr.Code == "23505" {
			return UserRow{}, ErrDuplicateUsername
		}
		return UserRow{}, err
	}
	return user, nil
}

// @{"req": ["REQ-USER-001"]}
func (r *Repository) GetUserByID(ctx context.Context, id uuid.UUID) (UserRow, error) {
	var user UserRow
	err := r.pool.QueryRow(ctx,
		`SELECT id, username, email, password_hash, role, is_active, created_at, updated_at
		 FROM users WHERE id = $1`,
		id).
		Scan(
			&user.ID,
			&user.Username,
			&user.Email,
			&user.PasswordHash,
			&user.Role,
			&user.IsActive,
			&user.CreatedAt,
			&user.UpdatedAt,
		)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserRow{}, ErrUserNotFound
		}
		return UserRow{}, err
	}
	return user, nil
}

// @{"req": ["REQ-USER-001"]}
func (r *Repository) GetUserByUsername(ctx context.Context, username string) (UserRow, error) {
	var user UserRow
	err := r.pool.QueryRow(ctx,
		`SELECT id, username, email, password_hash, role, is_active, created_at, updated_at
		 FROM users WHERE username = $1`,
		username).
		Scan(
			&user.ID,
			&user.Username,
			&user.Email,
			&user.PasswordHash,
			&user.Role,
			&user.IsActive,
			&user.CreatedAt,
			&user.UpdatedAt,
		)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserRow{}, ErrUserNotFound
		}
		return UserRow{}, err
	}
	return user, nil
}

// @{"req": ["REQ-USER-002"]}
func (r *Repository) UpdateUser(ctx context.Context, id uuid.UUID, fields UpdateFields) (UserRow, error) {
	if fields.Username == nil && fields.Email == nil && fields.PasswordHash == nil {
		return UserRow{}, ErrNoFieldsToUpdate
	}

	var setClauses []string
	var args []interface{}
	argIdx := 1

	if fields.Username != nil {
		setClauses = append(setClauses, fmt.Sprintf("username = $%d", argIdx))
		args = append(args, *fields.Username)
		argIdx++
	}
	if fields.Email != nil {
		setClauses = append(setClauses, fmt.Sprintf("email = $%d", argIdx))
		args = append(args, *fields.Email)
		argIdx++
	}
	if fields.PasswordHash != nil {
		setClauses = append(setClauses, fmt.Sprintf("password_hash = $%d", argIdx))
		args = append(args, *fields.PasswordHash)
		argIdx++
	}

	setClauses = append(setClauses, fmt.Sprintf("updated_at = NOW()"))

	query := fmt.Sprintf(
		`UPDATE users SET %s WHERE id = $%d
		 RETURNING id, username, email, password_hash, role, is_active, created_at, updated_at`,
		strings.Join(setClauses, ", "),
		argIdx,
	)
	args = append(args, id)

	var user UserRow
	err := r.pool.QueryRow(ctx, query, args...).
		Scan(
			&user.ID,
			&user.Username,
			&user.Email,
			&user.PasswordHash,
			&user.Role,
			&user.IsActive,
			&user.CreatedAt,
			&user.UpdatedAt,
		)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserRow{}, ErrUserNotFound
		}
		if pgErr, ok := err.(*pgconn.PgError); ok && pgErr.Code == "23505" {
			return UserRow{}, ErrDuplicateUsername
		}
		return UserRow{}, err
	}
	return user, nil
}

// @{"req": ["REQ-USER-003"]}
func (r *Repository) SetActive(ctx context.Context, id uuid.UUID, active bool) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	cmdTag, err := tx.Exec(ctx,
		`UPDATE users SET is_active = $1, updated_at = NOW() WHERE id = $2`,
		active, id)
	if err != nil {
		return err
	}
	if cmdTag.RowsAffected() == 0 {
		return ErrUserNotFound
	}

	if !active {
		_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, id)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// @{"req": ["REQ-USER-007"]}
func (r *Repository) DeleteStudent(ctx context.Context, id uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var role string
	err = tx.QueryRow(ctx, `SELECT role FROM users WHERE id = $1`, id).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUserNotFound
		}
		return err
	}
	if role != "student" {
		return ErrNotAStudent
	}

	if err := deleteIfTableExists(ctx, tx, "submissions",
		`DELETE FROM submissions WHERE course_id IN (SELECT id FROM courses WHERE student_id = $1)`, id); err != nil {
		return err
	}

	if err := deleteIfTableExists(ctx, tx, "grades",
		`DELETE FROM grades WHERE course_id IN (SELECT id FROM courses WHERE student_id = $1)`, id); err != nil {
		return err
	}

	if err := deleteIfTableExists(ctx, tx, "chat_messages",
		`DELETE FROM chat_messages WHERE course_id IN (SELECT id FROM courses WHERE student_id = $1)`, id); err != nil {
		return err
	}

	if err := deleteIfTableExists(ctx, tx, "courses",
		`DELETE FROM courses WHERE student_id = $1`, id); err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, id)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `DELETE FROM password_reset_tokens WHERE user_id = $1`, id)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// @{"req": ["REQ-USER-005"]}
func (r *Repository) CreatePasswordResetToken(ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO password_reset_tokens (user_id, token_hash, expires_at)
		 VALUES ($1, $2, $3)`,
		userID, tokenHash, expiresAt)
	return err
}

// @{"req": ["REQ-USER-005", "REQ-USER-006"]}
func (r *Repository) GetValidResetToken(ctx context.Context, tokenHash string) (ResetTokenRow, error) {
	var token ResetTokenRow
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, token_hash, expires_at, used_at
		 FROM password_reset_tokens
		 WHERE token_hash = $1 AND used_at IS NULL AND expires_at > NOW()`,
		tokenHash).
		Scan(
			&token.ID,
			&token.UserID,
			&token.TokenHash,
			&token.ExpiresAt,
			&token.UsedAt,
		)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ResetTokenRow{}, ErrTokenNotFound
		}
		return ResetTokenRow{}, err
	}
	return token, nil
}

// @{"req": ["REQ-USER-005", "REQ-USER-006"]}
func (r *Repository) MarkResetTokenUsed(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE password_reset_tokens SET used_at = NOW() WHERE id = $1`,
		id)
	return err
}

// @{"req": ["REQ-USER-005", "REQ-USER-006"]}
func (r *Repository) UpdatePasswordHash(ctx context.Context, userID uuid.UUID, newHash string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx,
		`UPDATE users SET password_hash = $1, updated_at = NOW() WHERE id = $2`,
		newHash, userID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// @{"req": ["REQ-SECURITY-005"]}
func (r *Repository) UpsertConsent(ctx context.Context, studentID uuid.UUID, version string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO student_consent (student_id, consented_at, consent_version)
		 VALUES ($1, NOW(), $2)
		 ON CONFLICT (student_id) DO UPDATE SET consented_at = NOW(), consent_version = $2`,
		studentID, version)
	return err
}

// @{"req": ["REQ-SECURITY-005"]}
func (r *Repository) GetConsentVersion(ctx context.Context, studentID uuid.UUID) (string, error) {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT consent_version FROM student_consent WHERE student_id = $1`,
		studentID).
		Scan(&version)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrConsentNotFound
		}
		return "", err
	}
	return version, nil
}

// @{"req": ["REQ-USER-007"]}
// deleteIfTableExists executes a DELETE statement using a savepoint so that a
// "relation does not exist" error (42P01) can be caught without aborting the
// surrounding transaction. The savepoint is rolled back on 42P01 and released
// on any other outcome, keeping the transaction usable in either case.
func deleteIfTableExists(ctx context.Context, tx pgx.Tx, tableName, query string, args ...interface{}) error {
	sp := "sp_" + tableName
	if _, err := tx.Exec(ctx, "SAVEPOINT "+sp); err != nil {
		return err
	}
	_, execErr := tx.Exec(ctx, query, args...)
	if execErr != nil {
		if pgErr, ok := execErr.(*pgconn.PgError); ok && pgErr.Code == "42P01" {
			// Table does not exist yet — roll back only this savepoint.
			if _, rbErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+sp); rbErr != nil {
				return rbErr
			}
			_, _ = tx.Exec(ctx, "RELEASE SAVEPOINT "+sp)
			return nil
		}
		return execErr
	}
	_, _ = tx.Exec(ctx, "RELEASE SAVEPOINT "+sp)
	return nil
}
