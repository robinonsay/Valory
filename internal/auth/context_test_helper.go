//go:build testing

package auth

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SetTestContext injects userID and role into ctx using the same context keys
// that UserIDFromContext and RoleFromContext read. For use in tests only.
//
// @{"req": ["REQ-AUTH-004", "REQ-AUTH-005"]}
func SetTestContext(ctx context.Context, userID [16]byte, role string) context.Context {
	ctx = context.WithValue(ctx, contextKeyUserID, userID)
	ctx = context.WithValue(ctx, contextKeyRole, role)
	return ctx
}

// WithTestConn injects a pool connection into ctx using the same key that
// ConnFromContext reads. For use in tests only — allows repository tests to
// exercise the auth-connection path without going through the full HTTP middleware.
//
// @{"req": ["REQ-AUTH-004"]}
func WithTestConn(ctx context.Context, conn *pgxpool.Conn) context.Context {
	return context.WithValue(ctx, contextKeyConn, conn)
}
