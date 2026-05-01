// querier.go — shared interface satisfied by both *pgxpool.Pool and *pgxpool.Conn.
// Repositories that serve HTTP requests use this so they can transparently work
// with the request-scoped connection (which carries RLS GUCs set by the auth
// middleware) instead of a bare pool connection.
package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is implemented by both *pgxpool.Pool and *pgxpool.Conn.
// Repositories hold a Querier so they can be driven by either the general
// pool (for background / test usage) or a request-scoped connection (for HTTP
// handlers where the auth middleware has set app.current_user_id and
// app.current_role on the connection for RLS evaluation).
//
// @{"req": ["REQ-CONTENT-001", "REQ-CONTENT-002", "REQ-CONTENT-004", "REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}
