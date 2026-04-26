package auth

import (
	"context"
	"encoding/json"
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

// ConsentVersionProvider is satisfied by *admin.ConfigService via duck typing.
// Defined locally to avoid an auth → admin import cycle.
type ConsentVersionProvider interface {
	GetString(key string) string
}

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

// semverLess reports whether version string a is strictly less than b.
// Versions are parsed as dot-separated integer components (e.g. "1.10" > "1.9").
// An unparseable component falls back to 0, so malformed strings are treated
// as the lowest possible version and will always fail the consent gate.
//
// @{"req": ["REQ-SECURITY-005"]}
func semverLess(a, b string) bool {
	partsA := strings.SplitN(a, ".", 4)
	partsB := strings.SplitN(b, ".", 4)
	max := len(partsA)
	if len(partsB) > max {
		max = len(partsB)
	}
	for i := 0; i < max; i++ {
		var va, vb int
		if i < len(partsA) {
			fmt.Sscanf(partsA[i], "%d", &va)
		}
		if i < len(partsB) {
			fmt.Sscanf(partsB[i], "%d", &vb)
		}
		if va != vb {
			return va < vb
		}
	}
	return false
}

// writeConsentError writes a 403 response with both the error code and the
// current consent version so the client knows which version to present to the
// user for acceptance.
//
// @{"req": ["REQ-SECURITY-005"]}
func writeConsentError(w http.ResponseWriter, currentVersion string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]string{
		"error":           "CONSENT_REQUIRED",
		"current_version": currentVersion,
	})
}

// @{"req": ["REQ-AUTH-004", "REQ-AUTH-005", "REQ-SYS-002"]}
func NewAuthMiddleware(
	repo *Repository,
	pool *pgxpool.Pool,
	inactivityPeriod time.Duration,
	// consentProvider is nil-safe: passing nil disables the consent gate entirely.
	consentProvider ConsentVersionProvider,
) func(http.Handler) http.Handler {
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

			// @{"req": ["REQ-SECURITY-005"]}
			// Students must have accepted the current consent version before they can
			// access any protected endpoint. Admins are exempt so they can always
			// manage the system regardless of consent state.
			if consentProvider != nil && session.Role == "student" {
				currentVersion := consentProvider.GetString("consent_version")
				var storedVersion string
				// pgx.ErrNoRows leaves storedVersion as "", which is always < any
				// real version string, so both missing and stale rows are rejected.
				_ = pool.QueryRow(r.WithContext(ctx).Context(),
					`SELECT consent_version FROM student_consent WHERE student_id = $1`,
					session.UserID).Scan(&storedVersion)
				if semverLess(storedVersion, currentVersion) {
					writeConsentError(w, currentVersion)
					return
				}
			}

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
