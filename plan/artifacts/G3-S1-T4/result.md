# G3-S1-T4 Rework-2 Result

## BLOCKER 1 — Tree terminal-success reset (D24)

**File:** `internal/agent/layered_runner.go`

**Change:** In `settleLayer`, after the `layer_awaiting_review` emit (success branch,
`approvedOrAwaiting > 0`), added a `layer == NodeTypeContent` guard that:
1. Calls `lr.agentRepo.ResetAttemptCount(ctx, courseID)` — logs on error, does not return it
   (hygiene, not critical path; the HITL gate transition already succeeded above).
2. Emits a `generation_terminal` node event via `lr.agentRepo.EmitNodeEvent(...)`.
3. Logs `"layered_runner: course %s tree-generation complete (content layer settled)"`.

**Why this fires exactly once:**
The `UPDATE courses SET status='awaiting_layer_approval'` at the top of the success branch
immediately removes the course from the `status='generating'` pool that `pollLayeredGeneration`
selects from. The poller will never call `GenerateLayer` -> `settleLayer` for this course again
(until the user expands to the next layer, but there is no next layer after content).
Therefore, this block executes at most once per tree-generation cycle. It is also gated on the
content layer being present and settled with `approvedOrAwaiting > 0`, making it deterministic.

**Updated annotation on `settleLayer`:**
`@{"req": ["REQ-AGENT-038", "REQ-AGENT-042", "REQ-AGENT-065", "REQ-SYS-073"]}`

**Inner annotation on the D24 block:**
`@{"req": ["REQ-AGENT-065", "REQ-SYS-073"]}`

The `ErrTreeComplete`/expand path in `node_handler.go` is NOT used (D24 explicitly rejects it).
The failure path (`handleFailedRun` / `IncrementAttemptCount`) is untouched — correct.
The flat-path `handleCompletedRun` (after `RunContentGeneration` reaches `status='active'`) is
unchanged.

---

## BLOCKER 2 — 4 missing `pipeline_event_type` enum values (D23)

**File:** `migrations/024_generation_run_lifecycle.sql`

**Confirmation:** `pipeline_events.event_type` IS typed as the `pipeline_event_type` enum
(confirmed by grep across migrations: `011_intake_chat.sql`, `022_tree_generation.sql`
both use `ALTER TYPE pipeline_event_type ADD VALUE`).

**Exact event-type strings verified from `runner.go`** (grep cross-checked):
- `"generation_claimed"` — EmitEvent at lines 358, 416
- `"token_cap_preflight_failed"` — EmitEvent at lines 369, 425
- `"generation_failed_with_retry"` — EmitEvent at line 828
- `"generation_terminal"` — EmitEvent at line 817 (failure-path terminal); also emitted
  by `layered_runner.go settleLayer` via `EmitNodeEvent` (BLOCKER 1 above)

**Added to 024 pre-`BEGIN` enum-additions section** (after the `notification_type` block):
```sql
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'generation_claimed';
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'token_cap_preflight_failed';
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'generation_failed_with_retry';
ALTER TYPE pipeline_event_type ADD VALUE IF NOT EXISTS 'generation_terminal';
```
All four match the Go string literals exactly. `IF NOT EXISTS` preserves idempotency.
`generation_terminal` is shared between the failure-path terminal event (runner.go) and the
tree-complete event (layered_runner.go settleLayer) — a single enum value avoids consumer
ambiguity.

---

## NITS

**Nit 1 — `isNoRows` string compare replaced with `errors.Is`** (`runner.go` ~854):
Old: `err != nil && (err.Error() == "no rows in result set")`
New: `errors.Is(err, pgx.ErrNoRows)` — uses the pgx sentinel already imported.

**Nit 2 — `handleFailedRun` annotation** (`runner.go` ~805):
Added `REQ-AGENT-069` (the `SetCourseTerminal('generation_failed')` site).
Full annotation: `@{"req": ["REQ-AGENT-062", "REQ-AGENT-065", "REQ-AGENT-066", "REQ-AGENT-069"]}`

**Nit 3 — `tokenCapPreFlight` comment** (`runner.go` ~768):
Old: `"Uses the bare pool (not AcquireServerConn) because agent_token_usage has no RLS."`
New: `"Uses r.pool directly (not AcquireServerConn) because agent_token_usage has no FORCE
ROW LEVEL SECURITY — a server-role connection is not required."`
The correctness argument (no RLS -> no need for server role) is preserved; the "bare pool"
phrasing that implied something unrelated to the actual server pool is corrected.

---

## Build verification

```
export PATH=/usr/local/go/bin:$PATH
go build ./...   # exit 0, no output
go vet ./...     # exit 0, no output
```

Both clean.

---

## Already-done items (preserved from prior session, verified unchanged)

- `github.com/jackc/pgx/v5` import added to `runner.go` — present.
- Premature `r.handleCompletedRun(...)` call removed from `dispatchTreeCourse`; seed-success
  path now calls only `SetRunStatus(run.ID, "completed", nil)` with explanatory comment — present.

## Files changed (rework-2)

- `internal/agent/layered_runner.go` — `settleLayer` success branch: D24 reset wiring
- `internal/agent/runner.go` — `isNoRows` (nit 1), `handleFailedRun` annotation (nit 2),
  `tokenCapPreFlight` comment (nit 3)
- `migrations/024_generation_run_lifecycle.sql` — 4 `pipeline_event_type` enum values (BLOCKER 2)
