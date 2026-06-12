package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// afterReleaseReset is the shared AfterRelease hook used by both the HTTP pool
// and the server pool. It clears the two application GUCs so a released
// connection never carries stale identity or role into the next acquisition.
//
// Returning false discards the connection rather than recycling it with stale
// state — the pool will open a fresh one on demand.
//
// @{"req": ["REQ-SECURITY-002"]}
func afterReleaseReset(conn *pgx.Conn) bool {
	_, err := conn.Exec(context.Background(),
		"SELECT set_config('app.current_user_id', '', false), set_config('app.current_role', '', false)",
	)
	return err == nil
}

// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002", "REQ-COURSE-003", "REQ-COURSE-004", "REQ-COURSE-005", "REQ-COURSE-006", "REQ-COURSE-007", "REQ-COURSE-008"]}
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("db: %w", err)
	}

	// Clear session-level GUCs when a connection is returned to the pool.
	// The auth middleware uses set_config('app.current_user_id', ..., false) to
	// bind the current user's identity to a connection for RLS evaluation. Without
	// this hook, a released connection would carry that identity into the next
	// request that acquires it from the pool.
	config.AfterRelease = afterReleaseReset

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("db: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: %w", err)
	}

	return pool, nil
}

// NewServerPool builds a pool whose every acquired connection is pre-configured
// with the 'server' RLS role before it is returned to the caller. This is
// required for the agent pipeline (chair, professor, reviewer, runner, grading
// runner, notify writes, badge awards driven by the pipeline): those goroutines
// write to tables that have FORCE ROW LEVEL SECURITY with server-side policies
// (courses 005, lesson_content, section_feedback, chat_messages, notifications,
// submissions, grades, student_badges). A bare pool connection has empty GUCs
// (cleared by AfterRelease) so those server policies evaluate to false and every
// write is blocked.
//
// BeforeAcquire sets both GUCs in a single round-trip:
//   - app.current_role = 'server'  — activates server-side RLS policies
//   - app.current_user_id = '00000000-0000-0000-0000-000000000000' — the
//     all-zeros sentinel UUID. The courses student policy casts this GUC to
//     ::uuid WITHOUT a NULLIF guard (migration 004 pattern), so an empty string
//     would raise "invalid input syntax for type uuid" before the server policy
//     could match. The all-zeros UUID is a valid, non-null UUID that never
//     matches any real student_id row, so the student-policy branch evaluates to
//     false and only the server-role policies govern. This mirrors the sentinel
//     used in internal/db/integrationtest.go:AcquireAsServer.
//
// AfterRelease clears the GUCs back to '' so a connection that is re-acquired
// for any non-server purpose (shouldn't happen with separate pools, but provides
// defense-in-depth) starts from a clean state. BeforeAcquire then re-applies
// the server role before it reaches any caller.
//
// Production note: the login role is valory_app (NOSUPERUSER, NOBYPASSRLS).
// No "SET ROLE" is issued here — the login role already IS valory_app, and
// BeforeAcquire runs as that role, so FORCE RLS is fully active.
//
// @{"req": ["REQ-AGENT-003", "REQ-AGENT-007", "REQ-AGENT-008", "REQ-CONTENT-001", "REQ-NOTIFY-001", "REQ-SECURITY-002"]}
func NewServerPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("db: server pool: %w", err)
	}

	// BeforeAcquire stamps every connection with the server role GUCs before
	// handing it to the caller. Returning false discards the connection if the
	// GUC write fails, preventing a connection with empty GUCs from escaping to
	// an agent caller that would then fail RLS silently.
	//
	// The hook signature matches pgxpool.Config.BeforeAcquire: func(context.Context, *pgx.Conn) bool.
	// We use conn.Exec (the higher-level pgx.Conn method, not PgConn) so the
	// GUC set_config call runs through the extended query protocol consistently
	// with how AfterRelease and AcquireServerConn issue GUC writes elsewhere.
	config.BeforeAcquire = func(acquireCtx context.Context, conn *pgx.Conn) bool {
		// Single round-trip: set both GUCs. is_local=false makes them session-
		// scoped so they persist for the full connection lifetime, not just the
		// implicit BEGIN that would wrap a single statement.
		_, err := conn.Exec(acquireCtx,
			"SELECT set_config('app.current_role', 'server', false),"+
				" set_config('app.current_user_id', '00000000-0000-0000-0000-000000000000', false)",
		)
		return err == nil
	}

	// AfterRelease resets the GUCs to '' consistent with the HTTP pool's behaviour.
	// BeforeAcquire will re-apply the server role on the next acquisition, so the
	// reset here is just defense-in-depth to keep the connection clean at rest.
	config.AfterRelease = afterReleaseReset

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("db: server pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: server pool: %w", err)
	}

	return pool, nil
}
