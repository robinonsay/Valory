// @{"req": ["REQ-NOTIFY-001", "REQ-NOTIFY-002", "REQ-SYS-035", "REQ-SYS-043"]}
package notify

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valory/valory/internal/db"
)

const (
	TypeAPIFailure        = "api_failure"
	TypeGenerationTimeout = "generation_timeout"
	TypeAdminEscalation   = "admin_escalation"
)

type Notification struct {
	StudentID uuid.UUID
	Type      string
	Message   string
}

// Write inserts a notification for the given student. It acquires a server-role
// connection so the write passes the notifications_server_policy RLS check.
//
// @{"req": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
func Write(ctx context.Context, pool *pgxpool.Pool, n Notification) error {
	conn, err := db.AcquireServerConn(ctx, pool)
	if err != nil {
		return err
	}
	defer conn.Release()
	_, err = conn.Exec(ctx, "INSERT INTO notifications (student_id, type, message) VALUES ($1, $2, $3)", n.StudentID, n.Type, n.Message)
	return err
}
