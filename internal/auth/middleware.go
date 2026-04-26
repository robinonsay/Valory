package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type contextKey int

const (
	contextKeyUserID contextKey = iota
	contextKeyRole
	contextKeyConn
)

// @{"req": ["REQ-AUTH-004", "REQ-AUTH-005", "REQ-SYS-002"]}
func UserIDFromContext(ctx context.Context) ([16]byte, bool) {
	v, ok := ctx.Value(contextKeyUserID).([16]byte)
	return v, ok
}

// @{"req": ["REQ-AUTH-004", "REQ-AUTH-005", "REQ-SYS-002"]}
func RoleFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(contextKeyRole).(string)
	return v, ok
}

// @{"req": ["REQ-AUTH-004", "REQ-SYS-002"]}
func ConnFromContext(ctx context.Context) (*pgxpool.Conn, bool) {
	v, ok := ctx.Value(contextKeyConn).(*pgxpool.Conn)
	return v, ok
}

// @{"req": ["REQ-AUTH-004", "REQ-AUTH-005", "REQ-SYS-002"]}
func NewAuthMiddleware(repo *Repository, pool *pgxpool.Pool, inactivityPeriod time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			rawToken := strings.TrimPrefix(authHeader, "Bearer ")

			tokenHash := HashToken(rawToken)
			session, err := repo.GetSessionByTokenHash(r.Context(), tokenHash)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}

			now := time.Now()
			if checkErr := CheckExpiry(now, session.ExpiresAt, session.LastActiveAt, inactivityPeriod); checkErr != nil {
				// Best-effort deletion; ignore errors to avoid masking the expiry response.
				_ = repo.DeleteSession(r.Context(), tokenHash)
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}

			if err := repo.UpdateLastActiveAt(r.Context(), tokenHash); err != nil {
				writeError(w, http.StatusInternalServerError, "internal server error")
				return
			}

			// Acquire a dedicated connection for this request so that SET LOCAL
			// parameters persist for the full handler lifetime without being
			// returned mid-request to the pool where another goroutine could
			// pick up the wrong session settings.
			conn, err := pool.Acquire(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal server error")
				return
			}
			defer conn.Release()

			// hex(UUID bytes) matches what current_setting('app.current_user_id')::UUID
			// expects in RLS policies. Using is_local=false so the setting persists
			// for the full connection lifetime, not just the implicit transaction that
			// wraps the SET statement.
			userIDHex := fmt.Sprintf("%x", session.UserID)
			if _, err := conn.Exec(r.Context(),
				"SELECT set_config('app.current_user_id', $1, false), set_config('app.current_role', $2, false)",
				userIDHex, session.Role,
			); err != nil {
				writeError(w, http.StatusInternalServerError, "internal server error")
				return
			}

			ctx := r.Context()
			ctx = context.WithValue(ctx, contextKeyUserID, session.UserID)
			ctx = context.WithValue(ctx, contextKeyRole, session.Role)
			ctx = context.WithValue(ctx, contextKeyConn, conn)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// @{"req": ["REQ-AUTH-004", "REQ-SYS-002"]}
func RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got, ok := RoleFromContext(r.Context())
			if !ok || got != role {
				writeError(w, http.StatusForbidden, "forbidden")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
