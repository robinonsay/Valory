package security

import (
	"context"
	"encoding/binary"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

// @{"req": ["REQ-SECURITY-003"]}
var ErrRateLimitExceeded = errors.New("password reset rate limit exceeded")

// @{"req": ["REQ-SECURITY-003"]}
// CheckAndRecordPasswordReset atomically checks whether the given userID has made
// fewer than 3 password-reset requests in the past hour. If so, it records a new
// attempt and returns nil. If the limit is reached, it returns ErrRateLimitExceeded
// without recording an attempt.
//
// A per-user advisory lock is acquired inside the transaction so that concurrent
// callers for the same user serialize their count-check and insert, preventing a
// Read Committed TOCTOU bypass of the three-per-hour limit.
func CheckAndRecordPasswordReset(ctx context.Context, pool *pgxpool.Pool, userID [16]byte) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Derive a stable int64 lock key from the 16-byte user ID by XOR-ing the
	// two halves. pg_advisory_xact_lock serializes concurrent callers that share
	// the same key, preventing TOCTOU bypass of the count-3 guard.
	lockKey := int64(binary.BigEndian.Uint64(userID[:8])) ^ int64(binary.BigEndian.Uint64(userID[8:]))
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey); err != nil {
		return err
	}

	var count int
	err = tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM password_reset_attempts WHERE user_id = $1 AND requested_at > NOW() - INTERVAL '1 hour'`,
		userID).Scan(&count)
	if err != nil {
		return err
	}

	if count >= 3 {
		return ErrRateLimitExceeded
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO password_reset_attempts (user_id) VALUES ($1)`,
		userID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// @{"req": ["REQ-SECURITY-003"]}
// PruneOldResetAttempts deletes attempt rows older than 7 days.
// Intended to be called by a nightly maintenance routine.
func PruneOldResetAttempts(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx,
		`DELETE FROM password_reset_attempts WHERE requested_at < NOW() - INTERVAL '7 days'`)
	return err
}
