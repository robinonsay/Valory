//go:build testing

package auth

import "context"

// SetTestContext injects userID and role into ctx using the same context keys
// that UserIDFromContext and RoleFromContext read. For use in tests only.
//
// @{"req": ["REQ-AUTH-004", "REQ-AUTH-005"]}
func SetTestContext(ctx context.Context, userID [16]byte, role string) context.Context {
	ctx = context.WithValue(ctx, contextKeyUserID, userID)
	ctx = context.WithValue(ctx, contextKeyRole, role)
	return ctx
}
