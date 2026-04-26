package audit

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository handles all audit_log database operations.
type Repository struct {
	pool *pgxpool.Pool
}

// @{"req": ["REQ-AUDIT-001"]}
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// @{"req": ["REQ-AUDIT-002"]}
func (r *Repository) Append(ctx context.Context, tx pgx.Tx, e Entry) error {
	var prevHash string

	// Step 1: Get the most recent entry's hash (use FOR UPDATE to prevent concurrent inserts)
	row := tx.QueryRow(ctx,
		`SELECT entry_hash FROM audit_log ORDER BY id DESC LIMIT 1 FOR UPDATE`)

	err := row.Scan(&prevHash)
	if err != nil {
		if err == pgx.ErrNoRows {
			// No previous entry; use Genesis hash
			prevHash = GenesisHash
		} else {
			return err
		}
	}

	// Step 2: Prepare the entry (get payloadJSON and entryHash)
	payloadJSON, entryHash, err := PrepareEntry(e, time.Now().UTC(), prevHash)
	if err != nil {
		return err
	}

	// Step 3: Insert the new entry
	_, err = tx.Exec(ctx,
		`INSERT INTO audit_log (admin_id, action, target_type, target_id, payload, prev_hash, entry_hash)
		 VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7)`,
		e.AdminID, e.Action, e.TargetType, e.TargetID, payloadJSON, prevHash, entryHash)

	return err
}

// @{"req": ["REQ-AUDIT-001"]}
func (r *Repository) ListPaginated(ctx context.Context, limit int, before int64) ([]AuditRow, error) {
	var rows pgx.Rows
	var err error

	if before == 0 {
		// No cursor; fetch the most recent limit entries
		rows, err = r.pool.Query(ctx,
			`SELECT id, admin_id, action, target_type, target_id, payload::text, prev_hash, entry_hash, created_at
			 FROM audit_log ORDER BY id DESC LIMIT $1`,
			limit)
	} else {
		// Cursor-based pagination; fetch entries before the given id
		rows, err = r.pool.Query(ctx,
			`SELECT id, admin_id, action, target_type, target_id, payload::text, prev_hash, entry_hash, created_at
			 FROM audit_log WHERE id < $1 ORDER BY id DESC LIMIT $2`,
			before, limit)
	}

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []AuditRow
	for rows.Next() {
		var row AuditRow
		err := rows.Scan(
			&row.ID,
			&row.AdminID,
			&row.Action,
			&row.TargetType,
			&row.TargetID,
			&row.PayloadJSON,
			&row.PrevHash,
			&row.EntryHash,
			&row.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		results = append(results, row)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return results, nil
}

// @{"req": ["REQ-AUDIT-002"]}
// StreamAll pages through the entire audit_log in ascending ID order and calls
// fn for each row. Rows are fetched in batches of 1000 so the repository never
// accumulates the full log in memory; it is the caller's responsibility to
// decide whether to collect, stream, or verify rows incrementally.
// fn must not retain a reference to AuditRow beyond its own call frame.
func (r *Repository) StreamAll(ctx context.Context, fn func(AuditRow) error) error {
	afterID := int64(0)

	for {
		rows, err := r.pool.Query(ctx,
			`SELECT id, admin_id, action, target_type, target_id, payload::text, prev_hash, entry_hash, created_at
			 FROM audit_log WHERE id > $1 ORDER BY id ASC LIMIT 1000`,
			afterID)
		if err != nil {
			return err
		}

		batchCount := 0
		for rows.Next() {
			var row AuditRow
			if err := rows.Scan(
				&row.ID,
				&row.AdminID,
				&row.Action,
				&row.TargetType,
				&row.TargetID,
				&row.PayloadJSON,
				&row.PrevHash,
				&row.EntryHash,
				&row.CreatedAt,
			); err != nil {
				rows.Close()
				return err
			}
			afterID = row.ID
			batchCount++
			if err := fn(row); err != nil {
				rows.Close()
				return err
			}
		}

		rows.Close()

		if err := rows.Err(); err != nil {
			return err
		}

		if batchCount < 1000 {
			break
		}
	}

	return nil
}
