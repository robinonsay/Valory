# G3-S1-T4 Gate-1 Rework Result

## Blockers fixed

### B1 — Tree double-CreateRun silent no-op

`seedTreeAndGenerateRoot` signature changed to accept `runID uuid.UUID` as its
second parameter. The internal `CreateRun` call and `ErrRunAlreadyClaimed` tolerance
inside that function are removed. `dispatchTreeCourse` passes `run.ID` (the run it
already created) to `seedTreeAndGenerateRoot`. Net invariant: exactly one
`tree_layer_generation` run is created per tree dispatch; the unique partial index
can never fire a 23505 inside `seedTreeAndGenerateRoot` anymore.

The `pollLayeredGeneration` path has its own `CreateRun` with its own
`ErrRunAlreadyClaimed` skip — that site is untouched and remains correct.

### B2 — Startup recovery RLS violation on courses

Steps 2 and 3 in `cmd/server/main.go` now use `serverPool.Exec` instead of
`pool.Exec`. `courses` is FORCE RLS; `valory_app` is NOBYPASSRLS; the bare pool
carries `app.current_role=''` which `courses_server_*_policy` rejects. Using
`serverPool` (whose `BeforeAcquire` sets `app.current_role='server'`) satisfies
the server-side policy and makes the UPDATE actually touch rows.

Step 1 (marking `agent_runs` failed) remains on the bare pool — `agent_runs` has
no RLS and either pool works.

### B2b — Crash-loop attempt inflation

A `restartMark` string is captured before Step 1:

```go
restartMark := "server restart:" + time.Now().UTC().Format(time.RFC3339Nano)
```

Step 1 stamps `error = $1` (the mark). Step 2's subquery filters
`error = $2` (same mark). Prior-restart rows carry different marks and are
excluded, so each restart increments `generation_attempt_count` only for runs
that were genuinely in-flight at that restart.

## Preserved items (verified unchanged)

- `inFlightFlat` / `inFlightLayered` guards — untouched.
- `ErrRunAlreadyClaimed` tolerance in `pollLayeredGeneration` — untouched.
- `tokenCapPreFlight` before section work on both paths — untouched.
- `handleFailedRun` (IncrementAttemptCount → SetCourseTerminal at max; no
  SetRunStatus inside) — untouched.
- `ResetAttemptCount` on terminal success only (D21) — untouched.
- `RunContentGeneration` retained intact (timeout + active transition +
  classification) — untouched.
- Config-key whitelist (`config_handler.go`) — untouched (already had
  `generation_max_attempts` and `generation_backoff_seconds`).
- Notify types (`notify.go`) — untouched (already had TypeGenerationRetrying,
  TypeGenerationFailed, TypeGenerationRecovery).

## Build / vet

`go build ./...` — clean (no output).
`go vet ./...` — clean (no output).

## Files changed

- `internal/agent/layered_runner.go` — `seedTreeAndGenerateRoot` signature + body
- `internal/agent/runner.go` — `dispatchTreeCourse` call site + comment
- `cmd/server/main.go` — startup recovery Steps 1/2/3 (restartMark + serverPool)
