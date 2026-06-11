//go:build integration

// integrationtest.go — shared harness for per-module integration tests.
//
// Tests that use this file require a live PostgreSQL instance. Run them with:
//
//	make test-integration
//
// The harness connects via VALORY_TEST_DATABASE_URL (defaults to the Docker
// Compose test service) and applies all embedded migrations exactly once per
// process before handing out the pool. Each test that needs per-test isolation
// calls TruncateTables to reset rows without dropping the schema.
package db

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valory/valory/migrations"
)

const (
	defaultTestDSN = "postgres://valory_test:valory_test@localhost:55432/valory_test?sslmode=disable"

	// connectTimeout is kept short so tests skip quickly when Docker is not
	// running rather than blocking the suite for a full TCP timeout.
	connectTimeout = 5 * time.Second
)

// integrationState holds the single process-wide pool and the once that
// creates it. Using a package-level pair (rather than init) keeps the pool
// lazily allocated: it is only created when the first integration test runs.
// integrationAttemptedDSN records the DSN resolved during the Once so that
// subsequent callers (after a failed dial) can still report the correct URL
// in their skip messages — Once.Do does not re-run, so a function-local var
// would be empty for all but the first caller.
var (
	integrationOnce         sync.Once
	integrationPool         *pgxpool.Pool
	integrationAttemptedDSN string
)

// newTestPool builds a pgxpool that is safe for the integration test harness.
// It does NOT delegate to NewPool because test sessions issue SET ROLE
// valory_app (to make RLS policies meaningful — see AcquireAsUser). When such
// a connection is returned to the pool its role must be reset to the login role
// (valory_test) so that a subsequent acquisition for fixture seeding or
// TruncateTables does not unexpectedly run under valory_app's privilege
// restrictions. Production connections never issue SET ROLE, so production's
// AfterRelease (in pool.go) only clears the two GUCs and never needs RESET ROLE.
func newTestPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("db: integrationtest: parse config: %w", err)
	}

	// AfterRelease resets the PostgreSQL role AND clears the two session-level
	// GUCs set by AcquireAsUser / AcquireAsServer. We must reset the role first
	// because valory_app lacks write access to schema_migrations and other
	// objects that TruncateTables / fixture helpers may touch.
	//
	// Two separate Exec calls are used for clarity: the first uses PgConn (simple
	// protocol) for RESET ROLE because it cannot be expressed as a set_config
	// call, while the second uses the higher-level Exec for the GUC resets. The
	// overhead of two round-trips is negligible on a connection-return path.
	//
	// IMPORTANT: do NOT capture the construction/request ctx here. AfterRelease
	// runs on pgxpool's own goroutine, potentially long after the construction
	// context (connectCtx, a 5-second WithTimeout) has been cancelled. Using a
	// cancelled context would cause Exec to fail immediately, forcing AfterRelease
	// to return false and destroy every released connection instead of recycling
	// it — silently defeating the RESET ROLE contamination guard this hook exists
	// to enforce. Always use context.Background() for release-time cleanup.
	config.AfterRelease = func(conn *pgx.Conn) bool {
		releaseCtx := context.Background()
		// Step 1: restore the login role so the next acquirer is not
		// constrained by valory_app's privilege set.
		if err := conn.PgConn().Exec(releaseCtx, "RESET ROLE").Close(); err != nil {
			// Discard the connection rather than returning a stale role to
			// the pool — mirrors pool.go's reasoning for the GUC reset.
			return false
		}
		// Step 2: clear the application GUCs so the next acquirer does not
		// inherit a stale user identity (same as production AfterRelease).
		_, err := conn.Exec(releaseCtx,
			"SELECT set_config('app.current_user_id', '', false), set_config('app.current_role', '', false)",
		)
		return err == nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("db: integrationtest: new pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: integrationtest: ping: %w", err)
	}

	return pool, nil
}

// IntegrationPool returns the process-wide shared pool for integration tests.
// Migrations are applied exactly once per process using the same simple-query
// protocol as cmd/server's runMigrations, so multi-statement SQL files and
// BEGIN/COMMIT blocks are executed atomically.
//
// The pool is shared across all callers; do NOT close it in a t.Cleanup. If
// you need per-test row isolation, call TruncateTables after acquiring the pool.
//
// If the database is unreachable the test is skipped (not failed) — this keeps
// the normal `go test ./...` run green when Docker is not running.
func IntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	integrationOnce.Do(func() {
		dsn := os.Getenv("VALORY_TEST_DATABASE_URL")
		if dsn == "" {
			dsn = defaultTestDSN
		}
		// Record the resolved DSN at package level so all subsequent callers
		// (after a failed dial where Once.Do won't re-run) can still report
		// the correct attempted URL in their skip messages.
		integrationAttemptedDSN = dsn

		// Use a short-lived context for the initial connect attempt so a
		// missing Docker container skips rather than hangs.
		connectCtx, connectCancel := context.WithTimeout(context.Background(), connectTimeout)
		defer connectCancel()

		pool, err := newTestPool(connectCtx, dsn)
		if err != nil {
			// Store nil; every caller will detect this and skip.
			return
		}

		// Give migrations their own generous budget. On a cold container the
		// dial above can consume most of connectTimeout, leaving too little
		// time to apply migrations and causing a spurious (misleading) skip.
		// 30 s is sufficient for any realistic migration set.
		migrateCtx, migrateCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer migrateCancel()

		if err := applyTestMigrations(migrateCtx, pool); err != nil {
			pool.Close()
			// Store nil so callers skip rather than proceeding with a
			// schema-less database.
			return
		}

		integrationPool = pool
	})

	if integrationPool == nil {
		// Redact the password from the DSN before printing it to CI logs.
		// A skip message that leaks credentials into log files is worse than
		// not printing the DSN at all.
		safeDSN := redactDSN(integrationAttemptedDSN)
		t.Skipf("integration: database unreachable — run `make test-integration` to start the test container (attempted DSN: %s)", safeDSN)
	}

	return integrationPool
}

// redactDSN returns the DSN with the password replaced by "xxxxx" so that
// skip messages printed to CI logs do not leak credentials. Both URL-style DSNs
// (postgres://user:pass@host/db) and key=value DSNs (without a password field)
// are handled; if parsing fails the whole string is returned unchanged because
// we err on the side of a useful message over a fully-silent failure.
func redactDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	if u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = url.UserPassword(u.User.Username(), "xxxxx")
		}
	}
	return u.String()
}

// applyTestMigrations applies all embedded migrations in lexicographic order,
// mirroring cmd/server/main.go's runMigrations exactly. Each file is sent via
// pgconn's simple query protocol so that multi-statement files (BEGIN/COMMIT
// blocks, multiple DDL statements) execute atomically. The schema_migrations
// idempotency table prevents re-applying files across test runs.
func applyTestMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	entries, err := migrations.Files.ReadDir(".")
	if err != nil {
		return fmt.Errorf("db: integrationtest: read migrations dir: %w", err)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("db: integrationtest: acquire migration conn: %w", err)
	}
	defer conn.Release()

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		sql, err := migrations.Files.ReadFile(entry.Name())
		if err != nil {
			return fmt.Errorf("db: integrationtest: read migration %s: %w", entry.Name(), err)
		}
		// Use pgconn (simple query protocol) to support multi-statement SQL.
		// The extended protocol (used by pool.Exec) only executes the first
		// statement in a string, which would silently skip the rest of each file.
		if err := conn.Conn().PgConn().Exec(ctx, string(sql)).Close(); err != nil {
			return fmt.Errorf("db: integrationtest: apply migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}

// TruncateTables truncates the given tables with RESTART IDENTITY CASCADE to
// give each test a clean slate without tearing down the schema.
//
// The valory_test database role owns all tables (created by the Docker Compose
// test service), so this runs without needing to bypass RLS. CASCADE handles
// foreign-key referential cycles so callers do not need to worry about ordering.
//
// Panics if tables is empty to catch test authors who forget to pass table
// names — silent no-ops here would make isolation bugs very hard to find.
func TruncateTables(t *testing.T, pool *pgxpool.Pool, tables ...string) {
	t.Helper()

	if len(tables) == 0 {
		// Explicit panic rather than a silent no-op: a caller that passes zero
		// tables has almost certainly made a mistake.
		panic("db: TruncateTables called with no table names")
	}

	// Build TRUNCATE t1, t2, ... RESTART IDENTITY CASCADE.
	// We do NOT use parameterized queries here because table names cannot be
	// passed as query parameters. The table names come exclusively from test
	// code (not user input) so there is no injection risk.
	stmt := fmt.Sprintf("TRUNCATE %s RESTART IDENTITY CASCADE", strings.Join(tables, ", "))

	ctx := context.Background()
	if _, err := pool.Exec(ctx, stmt); err != nil {
		t.Fatalf("db: TruncateTables: %v", err)
	}
}

// AcquireAsUser acquires a connection from the pool, downgrades it from the
// superuser login role to the policy-checked valory_app role, and then sets the
// GUCs app.current_user_id and app.current_role on it, mirroring the GUC-setting
// behaviour of the auth middleware (internal/auth/middleware.go). This lets
// repository tests exercise row-level security policies on tables such as
// courses and lesson_content under realistic session conditions.
//
// Why SET ROLE is mandatory for meaningful RLS tests:
// The test bootstrap user (valory_test, from POSTGRES_USER in
// docker-compose.test.yml) is a PostgreSQL SUPERUSER. Superusers bypass
// row-level security entirely — even when FORCE ROW LEVEL SECURITY is set on a
// table — so any RLS policy check performed on a bare superuser connection is
// vacuous: it will pass unconditionally, making RLS-isolation tests worthless.
// Executing SET ROLE valory_app before the GUC assignments downgrades the
// connection to a non-superuser role (NOLOGIN, NOBYPASSRLS) for the duration of
// the acquisition. RLS policies are then enforced normally, and the GUC-based
// predicates (current_setting('app.current_user_id', true)) have genuine effect.
// When the connection is released, AfterRelease issues RESET ROLE (restoring the
// superuser login role) so the pool remains safe for fixture seeding and
// TruncateTables calls that need unrestricted access.
//
// Accepted userID formats: the auth middleware passes a 32-char no-dash hex
// string (fmt.Sprintf("%x", uuid.UUID{...})). Test authors may also pass a
// standard dashed UUID string (e.g. "550e8400-e29b-41d4-a716-446655440000").
// Both forms are accepted by PostgreSQL's ::uuid cast used in RLS policies, so
// either format exercises the same isolation logic.
//
// The caller is responsible for releasing the connection. The idiomatic pattern is:
//
//	conn := db.AcquireAsUser(t, pool, userID, "student")
//	defer conn.Release()
//
// Alternatively, register the release in t.Cleanup if the connection should
// live for the full sub-test:
//
//	t.Cleanup(conn.Release)
//
// The GUC values are set as session-local (false = not transaction-local), which
// matches the auth middleware and pool.AfterRelease reset in pool.go. When the
// connection is returned to the pool, AfterRelease clears both GUCs and resets
// the role automatically.
//
// @{"req": ["REQ-SECURITY-002"]}
func AcquireAsUser(t *testing.T, pool *pgxpool.Pool, userID string, role string) *pgxpool.Conn {
	t.Helper()

	// Guard against an empty userID even though migration 009 applied NULLIF
	// guards to every student policy (006 student_badges, 007 submissions ×2,
	// 008 grades — completing the pattern that 004 established for courses).
	// Post-009 an empty GUC no longer crashes; the NULLIF turns '' to NULL and
	// the equality resolves to NULL→false, so the query would silently succeed
	// while returning zero rows — the test would pass vacuously as if querying
	// as "nobody". Failing loudly here steers test authors toward either
	// AcquireAsServer (for server-role connections) or a real userID.
	// This is also defense-in-depth: any future policy written without the
	// NULLIF idiom (violating the convention from 004) would still crash on
	// an empty GUC; catching that here surfaces the error at test-setup time
	// rather than inside a policy evaluation.
	if userID == "" {
		t.Fatalf("db: AcquireAsUser: userID must not be empty — " +
			"for server-role connections use AcquireAsServer, which sets " +
			"app.current_user_id to the all-zeros sentinel UUID")
	}

	ctx := context.Background()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("db: AcquireAsUser: acquire: %v", err)
	}

	// Downgrade from the superuser login role to valory_app so that RLS
	// policies are enforced. Without this, the superuser bypasses all RLS
	// checks and the GUC-based policy predicates have no effect — tests would
	// pass vacuously even when the isolation logic is broken.
	if err := conn.Conn().PgConn().Exec(ctx, "SET ROLE valory_app").Close(); err != nil {
		conn.Release()
		t.Fatalf("db: AcquireAsUser: SET ROLE valory_app: %v", err)
	}

	// Set both GUCs in a single round-trip, consistent with how AcquireServerConn
	// in server_role.go issues its set_config call.
	_, err = conn.Exec(ctx,
		"SELECT set_config('app.current_user_id', $1, false), set_config('app.current_role', $2, false)",
		userID, role,
	)
	if err != nil {
		conn.Release()
		t.Fatalf("db: AcquireAsUser: set GUCs: %v", err)
	}

	return conn
}

// AcquireAsServer acquires a connection from the pool and configures it as the
// 'server' role, mirroring the production AcquireServerConn in server_role.go
// but with the mandatory SET ROLE valory_app step that production code omits.
//
// Test code MUST use this helper instead of calling db.AcquireServerConn
// directly. AcquireServerConn skips SET ROLE and therefore runs under the
// superuser login role (valory_test), which bypasses all RLS policies — any
// RLS test written against it would be vacuous. This helper issues SET ROLE
// first so that the server-role RLS policies defined across the migration
// files (e.g. courses in 005, submissions in 007) are actually enforced.
//
// The caller is responsible for releasing the connection:
//
//	conn := db.AcquireAsServer(t, pool)
//	defer conn.Release()
//
// app.current_user_id is set to the all-zeros UUID rather than left as '' or
// unset. Migration 009 retrofitted NULLIF guards onto all previously unguarded
// student policies (006 student_badges, 007 submissions ×2, 008 grades),
// completing the idiom that 004 established for the courses policy. Post-009
// an empty string would no longer crash but would make every student-policy
// comparison NULL→false, silently denying access without error. The all-zeros
// UUID is a valid, evaluable UUID that can never match any real student_id row,
// so student-policy branches resolve to false as intended and server-role
// policies (005 courses, 007 submissions server policy) evaluate normally.
// Using a real sentinel rather than '' also provides defense-in-depth: any
// future policy written without the NULLIF idiom (violating the convention
// from 004) stays evaluable rather than crashing, and the sentinel value
// clearly attributes server-role connections in pg_stat_activity / pg_logs.
// AfterRelease resets the GUC to '' rather than the sentinel, but that is
// safe because AfterRelease runs under the superuser (RESET ROLE has already
// been issued), which bypasses RLS entirely — the empty string only appears
// on bare-pool connections where policies never fire. Any policy-evaluating
// query must go through AcquireAsServer or AcquireAsUser, both of which
// overwrite the GUC before use.
//
// @{"req": ["REQ-SECURITY-002"]}
func AcquireAsServer(t *testing.T, pool *pgxpool.Pool) *pgxpool.Conn {
	t.Helper()

	ctx := context.Background()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("db: AcquireAsServer: acquire: %v", err)
	}

	// Downgrade to valory_app so that RLS policies are enforced — same
	// reasoning as AcquireAsUser. The superuser login role bypasses all RLS.
	if err := conn.Conn().PgConn().Exec(ctx, "SET ROLE valory_app").Close(); err != nil {
		conn.Release()
		t.Fatalf("db: AcquireAsServer: SET ROLE valory_app: %v", err)
	}

	// Set both GUCs in a single round-trip.
	// app.current_role = 'server' activates the server-side RLS policies
	// (courses_server_select_policy, etc.) defined in migration 005.
	// app.current_user_id = '00000000-0000-0000-0000-000000000000' is the
	// all-zeros sentinel UUID. Post-009 all student policies carry NULLIF guards
	// (004 courses, 009→006/007/008 student_badges/submissions/grades), so the
	// sentinel is defense-in-depth rather than a crash fix — it ensures that
	// any future policy written without the NULLIF idiom remains evaluable, and
	// it makes server-role connections clearly attributable in pg logs. The
	// all-zeros value never matches a real student_id, so student-policy
	// branches resolve to false and server-role policies evaluate normally.
	_, err = conn.Exec(ctx,
		"SELECT set_config('app.current_role', 'server', false),"+
			" set_config('app.current_user_id', '00000000-0000-0000-0000-000000000000', false)",
	)
	if err != nil {
		conn.Release()
		t.Fatalf("db: AcquireAsServer: set GUCs: %v", err)
	}

	return conn
}
