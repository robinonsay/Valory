// server_role.go — helpers for acquiring pool connections that carry the
// 'server' RLS role needed by background agent operations.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AcquireServerConn acquires a connection from the pool and sets
// app.current_role = 'server' so that background agent writes can pass the
// server-side RLS policies on lesson_content, courses, section_feedback, and
// notifications. The caller must defer conn.Release() after the call succeeds.
//
// @{"req": ["REQ-AGENT-003", "REQ-AGENT-007", "REQ-AGENT-008", "REQ-CONTENT-001", "REQ-NOTIFY-001"]}
func AcquireServerConn(ctx context.Context, pool *pgxpool.Pool) (*pgxpool.Conn, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("db: acquire server conn: %w", err)
	}
	if _, err := conn.Exec(ctx, "SELECT set_config('app.current_role','server',false)"); err != nil {
		conn.Release()
		return nil, fmt.Errorf("db: set server role: %w", err)
	}
	return conn, nil
}
