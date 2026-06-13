package setup

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository handles all DB operations required by the setup module.
// It writes only to the existing users table; no new tables are needed.
//
// @{"req": ["REQ-SETUP-001", "REQ-SETUP-002", "REQ-SETUP-003", "REQ-SETUP-004", "REQ-SETUP-005"]}
type Repository struct {
	pool *pgxpool.Pool
}

// @{"req": ["REQ-SETUP-001", "REQ-SETUP-003"]}
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// CountUsers returns the total number of rows in the users table.
// When tx is non-nil the query runs inside that transaction (enabling the
// in-transaction re-check required by the race-safe algorithm). When tx is nil
// the query runs directly against the pool (used by NeedsSetup outside a tx).
//
// @{"req": ["REQ-SETUP-002", "REQ-SETUP-005"]}
func (r *Repository) CountUsers(ctx context.Context, tx pgx.Tx) (int64, error) {
	var count int64
	var err error
	if tx != nil {
		err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	} else {
		err = r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	}
	if err != nil {
		return 0, err
	}
	return count, nil
}

// CreateAdmin inserts a new user row with role='admin', is_active=true, and
// must_change_password=false, returning the generated UUID.
// Must be called inside an open transaction that has already acquired the
// setupAdvisoryLockKey advisory lock.
//
// @{"req": ["REQ-SETUP-003", "REQ-SETUP-004", "REQ-SETUP-008"]}
func (r *Repository) CreateAdmin(
	ctx context.Context,
	tx pgx.Tx,
	username string,
	email *string,
	passwordHash string,
) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash, role, must_change_password, is_active)
		 VALUES ($1, $2, $3, 'admin', false, true)
		 RETURNING id`,
		username, email, passwordHash,
	).Scan(&id)
	if err != nil {
		return uuid.UUID{}, err
	}
	return id, nil
}
