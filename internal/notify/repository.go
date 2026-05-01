// @{"req": ["REQ-NOTIFY-001", "REQ-NOTIFY-002", "REQ-SYS-035", "REQ-SYS-043"]}
package notify

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/db"
)

var (
	ErrForbidden = errors.New("notification does not belong to the requesting student")
	ErrNotFound  = errors.New("notification not found")
)

type NotificationRow struct {
	ID        uuid.UUID
	StudentID uuid.UUID
	Type      string
	Message   string
	ReadAt    *time.Time
	CreatedAt time.Time
}

type NotificationRepository struct {
	pool *pgxpool.Pool
}

// @{"req": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
func NewRepository(pool *pgxpool.Pool) *NotificationRepository {
	return &NotificationRepository{pool: pool}
}

// conn returns the request-scoped connection set by the auth middleware when
// one is available. Falls back to the bare pool for background callers.
func (r *NotificationRepository) conn(ctx context.Context) db.Querier {
	if conn, ok := auth.ConnFromContext(ctx); ok {
		return conn
	}
	return r.pool
}

// @{"req": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
func (r *NotificationRepository) List(ctx context.Context, studentID uuid.UUID, unreadOnly bool, limit int, beforeID *uuid.UUID) ([]NotificationRow, error) {
	query := "SELECT id, student_id, type, message, read_at, created_at FROM notifications WHERE student_id = $1"
	args := []interface{}{studentID}
	argCount := 1

	if unreadOnly {
		query += " AND read_at IS NULL"
	}

	if beforeID != nil {
		argCount++
		query += fmt.Sprintf(" AND created_at < (SELECT created_at FROM notifications WHERE id = $%d AND student_id = $1)", argCount)
		args = append(args, *beforeID)
	}

	argCount++
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", argCount)
	args = append(args, limit)

	rows, err := r.conn(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notifications []NotificationRow
	for rows.Next() {
		var n NotificationRow
		if err := rows.Scan(&n.ID, &n.StudentID, &n.Type, &n.Message, &n.ReadAt, &n.CreatedAt); err != nil {
			return nil, err
		}
		notifications = append(notifications, n)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return notifications, nil
}

// @{"req": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
func (r *NotificationRepository) MarkRead(ctx context.Context, notificationID, studentID uuid.UUID) (*NotificationRow, error) {
	// First attempt: UPDATE only unread rows belonging to this student
	row := &NotificationRow{}
	err := r.conn(ctx).QueryRow(ctx,
		`UPDATE notifications
		 SET read_at = now()
		 WHERE id = $1 AND student_id = $2 AND read_at IS NULL
		 RETURNING id, student_id, type, message, read_at, created_at`,
		notificationID, studentID,
	).Scan(&row.ID, &row.StudentID, &row.Type, &row.Message, &row.ReadAt, &row.CreatedAt)

	if err == nil {
		return row, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err // real DB error
	}

	// UPDATE matched 0 rows. A single fetch distinguishes not-found, wrong-owner,
	// and already-read cases atomically without additional round trips.
	row2 := &NotificationRow{}
	err2 := r.conn(ctx).QueryRow(ctx,
		`SELECT id, student_id, type, message, read_at, created_at FROM notifications WHERE id = $1`,
		notificationID,
	).Scan(&row2.ID, &row2.StudentID, &row2.Type, &row2.Message, &row2.ReadAt, &row2.CreatedAt)
	if err2 != nil {
		if errors.Is(err2, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err2
	}
	if row2.StudentID != studentID {
		return nil, ErrForbidden
	}
	// Row belongs to this student but read_at is not null — already read, return idempotent.
	return row2, nil
}

// @{"req": ["REQ-SYS-035", "REQ-SYS-043"]}
func (r *NotificationRepository) StartRetentionWorker(ctx context.Context, configSvc interface{ GetInt64(string) int64 }) {
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				retentionDays := configSvc.GetInt64("notification_retention_days")
				if retentionDays <= 0 {
					log.Printf("notify: retention days is %d, skipping cleanup", retentionDays)
					continue
				}
				for {
					result, err := r.pool.Exec(ctx, `
						DELETE FROM notifications WHERE id IN (
							SELECT id FROM notifications
							WHERE created_at < NOW() - ($1 * INTERVAL '1 day')
							LIMIT 1000
						)
					`, retentionDays)
					if err != nil {
						log.Printf("notify: retention delete error: %v", err)
						break
					}

					if result.RowsAffected() == 0 {
						break
					}
				}
			}
		}
	}()
}
