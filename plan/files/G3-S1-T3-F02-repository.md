# G3-S1-T3-F02 — internal/agent/repository.go

> Level 5a — a **File** node: exactly one source file. The dispatch grain — one worker owns this
> File. Owned by `design-author`; realized by the assigned worker.

- **Source file:** `internal/agent/repository.go` (modified)
- **Task:** `G3-S1-T3` (see `plan/tasks/G3-S1-T3.md`)
- **Worker:** senior-engineer
- **Depends on:** `G3-S1-T3-F01` (the `generation_failed` status + index exist before these queries
  run in production)

## Purpose (what this file is for)

The `AgentRepository` queries that make poller eligibility **bounded and declarative**: exclude
courses that failed within the backoff window or are terminally `generation_failed`, and expose the
consecutive-failure count the runner (T4) uses to decide when to give up. This is the data-access
half of the lifecycle; the dispatch/decision half is `runner.go` (G3-S1-T4). All reads run on the
server pool (the poller has no request context) and stay parameterized.

## Units (the chunks within this file — level 5b)

| Unit id | Chunk | Requirement(s) | Acceptance |
|---|---|---|---|
| `G3-S1-T3-F02-U01` | `ListUntriggeredApprovals` — add failed-within-backoff + `generation_failed` exclusions; keep enum cast + tree_mode branch (**see** `plan/units/G3-S1-T3-F02-U01-eligibility-query.md`) | REQ-AGENT-064 | A course with a recent failed run / terminal status is not returned; a clean course still is; backoff is config-driven. |
| `G3-S1-T3-F02-U02` | `ConsecutiveFailures(ctx, courseID, runType)` — count failed same-type runs since the last non-failed run | REQ-AGENT-065 | Returns N after N straight failures; resets to 0 after a completed/running run; parameterized. |

## Acceptance (what a successful return for this File looks like)

- [ ] Eligibility excludes running/completed (unchanged), failed-within-backoff, and terminal
      `generation_failed`; backoff window from config; flat+tree branch + `::agent_run_type` cast intact.
- [ ] `ConsecutiveFailures` correct across the "since last success" boundary; parameterized.
- [ ] `CourseStudentRow` shape preserved (additive only); reads on the server pool; no bare-pool RLS write.
- [ ] Every function carries a `@{"req": [...]}` tracing annotation; `go build` + `go vet` clean.
