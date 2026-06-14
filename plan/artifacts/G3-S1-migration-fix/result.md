# G3-S1-migration-024-025-split — Result

## What was removed from migration 024

The entire "6a. Widen courses_single_active_idx" section was removed:

```sql
-- ─────────────────────────────────────────────────────────────────────────────
-- 6a. Widen courses_single_active_idx to exclude 'generation_failed' (D22)
-- ─────────────────────────────────────────────────────────────────────────────
DROP INDEX IF EXISTS courses_single_active_idx;
CREATE UNIQUE INDEX IF NOT EXISTS courses_single_active_idx
    ON courses (student_id)
    WHERE status NOT IN ('archived', 'completed', 'generation_failed');
```

The section heading was renumbered (old "7. Seed system_config" → "6. Seed system_config") and a NOTE was added to the file header explaining that D22 lives in 025 and why.

## Migration 024 post-edit: no in-file use of newly-added enum values in DDL

All occurrences of 'generation_failed' in 024 are either:
- Comments
- `ALTER TYPE course_status ADD VALUE IF NOT EXISTS 'generation_failed'` (adding it, not using it in a WHERE predicate or data statement)
- `ALTER TYPE notification_type ADD VALUE IF NOT EXISTS 'generation_failed'` (same — different enum type, only used at runtime by Go)

The `notification_type` and `pipeline_event_type` values are only consumed at runtime by Go code (runner.go EmitEvent, notification inserts). None appear in DDL WHERE predicates or DEFAULT expressions within the same file. Confirmed clean.

## Migration 025 rationale

File: `migrations/025_courses_single_active_idx_generation_failed.sql`

Purpose: widen `courses_single_active_idx` to exclude the terminal `generation_failed` status (D22) so a student whose course is stuck in `generation_failed` is not blocked from creating a new course.

Why its own file: `runMigrations` (cmd/server/main.go) sends each whole migration file as a single simple-query string via `conn.Conn().PgConn().Exec(ctx, string(sql))`. PostgreSQL treats this as one implicit transaction. PostgreSQL raises SQLSTATE 55P04 ("unsafe use of new value of enum type") when a newly-added enum value is used in the same transaction that adds it. By placing this index in a separate file, it runs in a separate `Exec` call after migration 024 has fully committed, making `generation_failed` visible to the planner before it appears in the WHERE predicate.

Contents: `BEGIN; INSERT schema_migrations ON CONFLICT DO NOTHING; DROP INDEX IF EXISTS courses_single_active_idx; CREATE UNIQUE INDEX IF NOT EXISTS courses_single_active_idx ON courses (student_id) WHERE status NOT IN ('archived','completed','generation_failed'); COMMIT;`

## Fresh-DB verification

Commands run (in order):

```
docker compose -f docker-compose.test.yml down -v
docker compose -f docker-compose.test.yml up -d --wait
```

DB confirmed healthy (postgres-test-1 and mailpit-1 both started from scratch with volumes deleted).

```
export PATH=/usr/local/go/bin:$PATH
export VALORY_TEST_DATABASE_URL=postgres://valory_test:valory_test@localhost:55432/valory_test?sslmode=disable
export TEST_DATABASE_URL=postgres://valory_test:valory_test@localhost:55432/valory_test?sslmode=disable

go test -tags integration -count=1 -p 1 -timeout 360s \
  -run 'TestGenLifecycle_MaxAttempts_Terminal_D22_DoesNotBlockNewCourse' \
  ./internal/agent/
```

Output:
```
ok  	github.com/valory/valory/internal/agent	0.635s
```

The test harness applies migrations 001–025 via the same `PgConn().Exec(wholeFile)` path as production on a fresh DB. No 55P04. D22 behavior verified.

```
go test -tags integration -count=1 -p 1 -timeout 360s ./internal/agent/
```

Output:
```
ok  	github.com/valory/valory/internal/agent	14.831s
```

Full suite: all pass, no regressions.

```
go build ./...
go vet ./...
```

Output: (no output — clean)

## Test DB state

Left UP as instructed for gate re-verification.
