# TDD: Generation Run Lifecycle and Bounded Dispatch

**Task:** G3-S1-T1
**Author:** design-author
**Status:** Revised (post-Senior-SQE review)
**Date:** 2026-06-14
**Gates:** T2 (requirements derivation), T3 (migration + eligibility), T4 (runner dispatch)

---

## 1. Overview

### Problem

`pollAndGenerate` runs every 30 seconds. Each tick calls `ListUntriggeredApprovals`, which
returns every `courses` row in `syllabus_approved` that has no `running` or `completed`
agent run of the matching type. A failed run leaves the course in `syllabus_approved`,
so it is immediately re-eligible at the next tick. During a demo session this produced
**925 failed `content_generation` runs in 10 hours** (approximately 2 per minute, matching
the 30-second poll interval). The side effects were: exhaustion of a Brave Search key (~1000
calls), chronic generation timeouts, and no forward progress on the course.

Three specific defects compound to produce this outcome:

1. **No retry budget.** A failed run does not increment any counter. The eligibility query
   only excludes `running`/`completed`; `failed` is not excluded.
2. **No backoff window.** A failed run can be re-triggered immediately on the very next tick.
3. **No terminal failure state.** A persistently-failing course never reaches a state the
   poller ignores. It runs forever until the key is rotated or the process restarts.
4. **Divergent in-flight guards.** The tree path holds a `sync.Map` (`inFlightLayered`) that
   prevents duplicate goroutines. The flat path has no equivalent; it relies on the incidental
   `agent_runs.status` default `'running'` (written synchronously in `CreateRun`). Under a
   forced race (two goroutines hitting `CreateRun` simultaneously, both seeing the eligibility
   predicate return the course before either writes `'running'`), the flat path can produce two
   concurrent runs.
5. **Brave burns before Claude cap check.** In `GenerateSection`, `searchInternet` (Brave call)
   is invoked at step 2, before `ThrottledClient.Messages` (step 4), which is where
   `ErrTokenCapExceeded` is detected. A token-capped run still makes one Brave call per section
   before failing.

### Approach

This design introduces:

- An explicit **run state machine** with a terminal course-level `generation_failed` state.
- A **retry policy** (backoff window + max consecutive attempts) that is enforced durably in
  the database eligibility query, not in goroutine logic.
- A single **idempotent claim mechanism** (database-unique-constraint + process-level registry)
  shared by both flat and tree paths, removing the current divergence.
- A **cost-safety hook point** where a token-cap pre-flight check occurs before any per-section
  paid work (Brave or Claude). Full cost governance is G3-S2.
- An **event taxonomy** that covers claim, failure, terminal, and recovery events.
- An **additive migration 024** that extends `agent_runs` and `courses` without breaking
  existing consumers.

### Binding decisions (D17-D21)

| Decision | This design's realisation |
|---|---|
| **D17** Retry policy in eligibility query + durable per-course record | `courses.generation_attempt_count` and `courses.next_eligible_at` are the durable state; `ListUntriggeredApprovals` reads them to gate eligibility. |
| **D18** Exhausted retries → terminal `generation_failed` course status | New `course_status` enum value; `ListUntriggeredApprovals` excludes it; recovery is explicit re-trigger. |
| **D19** Idempotent claim, unified flat+tree | Unique partial index on `agent_runs` (one `running` run per course per type) is the atomic database guard; the process-level registry (`inFlightLayered` for tree, new `inFlightFlat` for flat) is a latency optimisation on top. |
| **D20** No cost schema yet | Migration 024 adds lifecycle columns only; no cost ledger; Brave accounting is G3-S2. |
| **D21** Retry budget applies at course-dispatch level | `generation_attempt_count` counts failed `content_generation` or `tree_layer_generation` runs **per course since the course last reached a successful generated state** (flat → `active`; tree → tree-generation-complete). Per-layer retries within a single tree run do NOT each consume the budget; only dispatch-level failures that return the course to the eligibility pool count. Reset is only on terminal SUCCESS, not on an intermediate layer `completed`. |

---

## 2. Requirements in scope

| ID | Requirement | Satisfied by this design |
|----|-------------|--------------------------|
| REQ-AGENT-003 | A failed run does not permanently block retry | Bounded auto-retry (max-attempts config) then terminal state gated by explicit re-trigger; satisfies "not permanently" without allowing infinite auto-retry. |
| REQ-AGENT-014 | Generation timeout | Unchanged; timeout still sets `failed` status on the run. Now that `failed` updates `courses.generation_attempt_count`, timeouts consume the retry budget. |
| REQ-SYS-074 | Cost safety (partial: fail-fast hook only) | Token-cap pre-flight hook point defined in §6; full gate deferred to G3-S2. |

New requirement IDs **REQ-AGENT-061 through REQ-AGENT-066** will be authored in T2; this
document identifies the semantic gap each one must fill.

---

## 3. Data Model

### 3.1 Existing schema (as of migration 023) — excerpt

The following tables and fields are relevant to this design. Shown as they exist before
migration 024; no fields are removed.

**`courses` table — existing status values (PostgreSQL enum):**

```sql
-- course_status enum values (derived from live code inspection):
-- 'intake'           — student submitted topic; intake in progress
-- 'syllabus_approved' — syllabus approved; awaiting content generation dispatch
-- 'generating'       — tree-mode generation in progress (unused for flat courses)
-- 'active'           — content generation completed; student can use course
-- 'awaiting_regeneration' — tree-mode node regeneration in progress
-- 'withdrawn'        — student or admin withdrew the course
```

**`agent_runs` table — existing status values:**

```sql
-- agent_run_status enum values:
-- 'running'    — generation goroutine active
-- 'completed'  — generation succeeded
-- 'failed'     — generation failed (includes timeout, API failure, token cap)
-- 'terminated' — explicitly cancelled (student deletion, admin termination)
```

**`agent_run_type` enum values:**

```sql
-- 'content_generation'   — flat-course full generation run
-- 'tree_layer_generation' — tree-mode per-layer run
-- 'section_regen'        — section regeneration triggered by feedback
```

### 3.2 Migration 024 — additive schema changes

Migration 024 is purely additive. No existing enum values, columns, or constraints are
removed. The RLS policies on `courses` are unchanged (the new columns are written by the
server pool only; students cannot write them directly).

**Important note on `ALTER TYPE ... ADD VALUE` and `ALTER TYPE notification_type ADD VALUE`:**
PostgreSQL does not allow `ALTER TYPE ADD VALUE` inside a transaction block in versions before
PG 16. In PG 16 and newer, this restriction is lifted for new values. Since Valory targets
PG 16 (per docker-compose), the `ALTER TYPE` statements can run in the normal migration
transaction. However, T3 must place each `ALTER TYPE ADD VALUE` statement as a separate
statement and must not reference the newly added enum value in the same statement that adds it
(i.e., do not insert a row using the new enum value in the same transaction statement that
calls `ALTER TYPE ... ADD VALUE`; the new value becomes visible only to statements that begin
after the `ALTER TYPE` commits or to subsequent commands in the same transaction on PG 16+).
T3 must verify PG version behaviour and add a comment in the migration noting the PG 16
assumption.

#### 3.2.1 New `course_status` enum value

```sql
-- Add terminal generation failure state to the courses status enum.
-- Additive: existing enum values and CHECK constraints are unchanged.
ALTER TYPE course_status ADD VALUE IF NOT EXISTS 'generation_failed';
```

`generation_failed` is the terminal course state. The poller never selects it. Recovery
requires an explicit re-trigger action (§7.2).

#### 3.2.2 New `notification_type` enum values

The `notifications.type` column is the PostgreSQL enum `notification_type`, defined in
migration 004, with values `'api_failure'`, `'generation_timeout'`, `'admin_escalation'`.
The three new notification types (§10) require the enum to be extended. These statements must
run before any INSERT into `notifications` uses the new values.

```sql
-- Extend notification_type enum with generation lifecycle notification types.
-- Each ALTER TYPE ... ADD VALUE is a separate statement (PG 16 assumption; see §3.2 note).
ALTER TYPE notification_type ADD VALUE IF NOT EXISTS 'generation_retrying';
ALTER TYPE notification_type ADD VALUE IF NOT EXISTS 'generation_failed';
ALTER TYPE notification_type ADD VALUE IF NOT EXISTS 'generation_recovery';
```

Note: `generation_failed` as a `notification_type` value is distinct from
`generation_failed` as a `course_status` value — they live in different enum types.

#### 3.2.3 New `courses` columns

```sql
-- Durable attempt counter: incremented each time a content_generation or
-- tree_layer_generation dispatch-level run transitions to 'failed' for this course.
-- Counts consecutive dispatch-level failures since the course last reached its
-- terminal SUCCESS state (flat: 'active'; tree: tree-generation-complete).
-- Reset ONLY when the course reaches terminal SUCCESS, not on intermediate
-- layer completions. Used by the eligibility query to detect max-attempts exhaustion.
ALTER TABLE courses
    ADD COLUMN IF NOT EXISTS generation_attempt_count INTEGER NOT NULL DEFAULT 0;

-- Earliest timestamp at which this course is eligible to be dispatched again.
-- NULL means immediately eligible (no backoff in effect).
-- Set to NOW() + backoff_window when a dispatch-level run fails; cleared when the
-- course reaches terminal SUCCESS.
-- Used by the eligibility query's backoff exclusion clause.
ALTER TABLE courses
    ADD COLUMN IF NOT EXISTS next_eligible_at TIMESTAMPTZ;
```

#### 3.2.4 Eligibility index

```sql
-- Partial index to accelerate ListUntriggeredApprovals.
-- Covers the WHERE clause: status = 'syllabus_approved', attempt count < max,
-- and next_eligible_at exclusion. The eligibility query uses this index via the
-- status predicate; the other predicates are in-query filters.
CREATE INDEX IF NOT EXISTS courses_generation_eligible_idx
    ON courses (status, generation_attempt_count, next_eligible_at)
    WHERE status = 'syllabus_approved';
```

#### 3.2.5 Unique partial index on `agent_runs` (claim guard)

This index is the atomic database-level guarantee that no course can have two concurrent runs
of the same type. It enforces D19 at the storage layer.

```sql
-- Each course may have at most one 'running' run of each type at a time.
-- The partial index on status='running' keeps the index small (only active rows).
-- An INSERT that would violate this constraint is rejected with a unique_violation
-- (SQLSTATE 23505), which the dispatcher interprets as "already claimed by another
-- goroutine or process" and silently discards.
CREATE UNIQUE INDEX IF NOT EXISTS agent_runs_one_running_per_course_type_idx
    ON agent_runs (course_id, run_type)
    WHERE status = 'running' AND course_id IS NOT NULL;
```

**Why a partial unique index rather than a separate claim table:** The unique index fuses the
"create a run record" and "claim the course" operations into a single `INSERT`. A separate
lock/claim table would require two writes and a transaction, adding complexity and a failure
mode (claim written but run not created). The index is sufficient for a single-instance server
(the sole deployment model currently). If multi-instance deployment becomes a requirement, the
unique index already provides the inter-process guarantee without additional changes.

**Scope of this index for `tree_layer_generation` CreateRun sites:** The partial unique index
on `(course_id, run_type) WHERE status = 'running'` covers ALL run types, including
`tree_layer_generation`. The existing `CreateRun` call sites in `runner.go` (line 470) and
`layered_runner.go` (line 915) therefore also benefit from this constraint. T4 must ensure
those call sites handle `ErrRunAlreadyClaimed` (SQLSTATE 23505) as defence-in-depth, the same
way the unified dispatch path does (§8.2). This prevents a duplicate layer run if two
`pollLayeredGeneration` ticks race simultaneously.

### 3.3 Attempt counter — definition (per D21)

`courses.generation_attempt_count` counts **dispatch-level failures** per course, measured
since the course last reached its terminal SUCCESS state. Per D21:

- **Incremented by 1** when a `content_generation` run (flat path) transitions to `failed`,
  OR when a `tree_layer_generation` run that was dispatched by `pollAndGenerate` transitions
  to `failed` in a way that leaves the course unable to make further progress in the current
  tree generation cycle (i.e., the dispatch-level failure, not a per-layer intermediate retry).
  Concretely: T4 calls `handleFailedRun` at the `pollAndGenerate` dispatch goroutine level,
  which increments the counter. This is the only call site for `handleFailedRun` for this
  counter.
- **Reset to 0** ONLY when the course reaches terminal SUCCESS:
  - Flat path: when `RunContentGeneration` succeeds and `courses.status` transitions to
    `'active'` (inside `handleCompletedRun`, called after the `active` transition).
  - Tree path: when the full tree-generation cycle completes (tree-generation-complete event),
    not when an individual layer completes. An intermediate `tree_layer_generation` run
    transitioning to `completed` does NOT reset the counter — resetting on each layer
    completion would allow a course that fails layer 5 forever to never reach
    `generation_failed` (the reset after layer 4 completes would always zero the budget
    before layer 5's failure is counted).
- **Not touched** by `section_regen` runs (student-feedback-driven; separate escalation path
  via `runReviewLoop`).
- **Not touched** by `terminated` runs (external cancellation, not a generation failure).

T2 must make this scope explicit in the new REQ-AGENT requirements.

### 3.4 `next_eligible_at` update semantics (per D21)

| Event | `next_eligible_at` set to | `generation_attempt_count` |
|---|---|---|
| Dispatch-level run transitions to `failed` | `NOW() + backoff_window` | incremented by 1 |
| Course reaches terminal SUCCESS (flat → `active`; tree → generation-complete) | `NULL` | reset to 0 |
| Intermediate `tree_layer_generation` run transitions to `completed` | unchanged | unchanged |
| Course transitions to `generation_failed` (terminal) | unchanged (irrelevant; poller excludes this status) | remains at max |
| Explicit re-trigger (§7.2) | `NULL` | reset to 0 |

The server startup recovery code (main.go line 106) marks stale `running` runs as `failed`
with `error = 'server restart'`. The T4 runner must ensure the corresponding course columns
are updated in this recovery path as well (see §8.3).

---

## 4. API Contract

No new HTTP endpoints are added in G3-S1. The recovery re-trigger (§7.2) is an internal
action only in S1; a frontend endpoint for manual re-trigger is deferred to a future sprint
when the UI surface is defined. The admin can invoke recovery directly via a database update
or via a future admin API endpoint.

The existing SSE event stream (`GET /courses/{id}/events`) is extended with new event types
(see §10).

---

## 5. Run State Machine

### 5.1 `agent_runs` status transitions

The existing status values (`running`, `completed`, `failed`, `terminated`) are unchanged.
Their meaning is clarified:

```
           CreateRun (atomic claim)
                  |
                  v
            [running]  ←--- startup recovery marks stale 'running' → 'failed'
             /     \
     success/       \failure (timeout / API error / token cap / panic)
           /         \
    [completed]    [failed]
                      |
              (update courses columns via handleFailedRun)
              generation_attempt_count += 1
              next_eligible_at = NOW() + backoff_window
                      |
              IF attempt_count >= max_attempts:
                  courses.status = 'generation_failed'
```

**`terminated`** is set by `TerminateStudentOperations` and is not affected by this design.
A terminated run does not increment `generation_attempt_count` (it was cancelled externally,
not a generation failure). T4 must implement this distinction.

### 5.2 `courses` status transitions (additive)

Existing transitions are unchanged. New transitions added:

```
[syllabus_approved]  -- max-attempts exhausted on 'failed' run -->  [generation_failed]
[generation_failed]  -- explicit re-trigger (§7.2) -->              [syllabus_approved]
```

The `generation_failed` state has no forward path the poller will ever trigger. It is strictly
terminal from the poller's perspective. Only an explicit human action transitions it back.

### 5.3 State machine for the dispatch / claim sequence

The claim sequence is:

```
pollAndGenerate tick (every 30s)
    |
    v
ListUntriggeredApprovals
    -- returns: courses in 'syllabus_approved' WHERE
    --   no 'running'/'completed' run of matching type EXISTS
    --   AND (next_eligible_at IS NULL OR next_eligible_at <= NOW())
    --   AND generation_attempt_count < max_attempts_config
    |
    for each returned course:
        |
        v
    [process-level in-flight check]
        -- LoadOrStore in inFlightFlat (flat) or inFlightLayered (tree)
        -- if already in-flight: skip (fast path, avoids DB round-trip)
        |
        v
    CreateRun (INSERT agent_runs ... RETURNING id)
        -- UNIQUE INDEX agent_runs_one_running_per_course_type_idx
        -- enforces: only one 'running' row per (course_id, run_type)
        -- on conflict (23505): log and discard; release in-flight entry
        |
        v
    [run goroutine]
        ...generation work...
        |
       success: handleCompletedRun → reset courses columns (only on terminal SUCCESS)
       failure: handleFailedRun → update courses columns → maybe terminal
```

---

## 6. Eligibility Predicate

### 6.1 Revised `ListUntriggeredApprovals`

The following is the authoritative SQL predicate. T3 implements this exactly.

```sql
SELECT c.id, c.student_id, c.tree_mode
FROM courses c
WHERE c.status = 'syllabus_approved'
  -- Exclude courses that already have a running or completed generation run.
  -- The run_type branch uses the existing (CASE...END)::agent_run_type cast.
  AND NOT EXISTS (
      SELECT 1 FROM agent_runs ar
      WHERE ar.course_id = c.id
        AND ar.run_type = (CASE WHEN c.tree_mode
                               THEN 'tree_layer_generation'
                               ELSE 'content_generation'
                          END)::agent_run_type
        AND ar.status IN ('running', 'completed')
  )
  -- Exclude courses still within the backoff window.
  AND (c.next_eligible_at IS NULL OR c.next_eligible_at <= NOW())
  -- Exclude courses that have exhausted max attempts.
  -- NOTE: the max-attempts threshold is a runtime config value.
  -- The query cannot embed a Go variable; T3 uses a parameterised query:
  --   WHERE c.generation_attempt_count < $1
  -- where $1 is resolved from config at call time (default: 5).
  AND c.generation_attempt_count < $1  -- runtime config: generation_max_attempts
```

**Parameter `$1`:** resolved at call time from `configSvc.GetInt64("generation_max_attempts")`,
defaulting to 5. The `AgentRepository.ListUntriggeredApprovals` signature gains this parameter.

**The transition to `generation_failed` is NOT performed inside this query.** The eligibility
query is read-only. The transition happens in the runner's failure handler (T4) after the
attempt count is incremented. This keeps the query idempotent and avoids a write-inside-read
pattern that is hard to test.

**Preserving the `(CASE...END)::agent_run_type` cast:** the tree-mode branch is retained
exactly as it exists today. Only the two new `AND` clauses are appended.

### 6.2 Max-attempts exhaustion — transition flow

When a dispatch-level run fails and `handleFailedRun` is called, it MUST call `SetRunStatus`
to mark the run `failed` (setting `completed_at`) **before** calling `IncrementAttemptCount`.
`handleFailedRun` itself does NOT call `SetRunStatus` — the caller sets status first (via
`r.agentRepo.SetRunStatus`), then calls `handleFailedRun`. This avoids a double UPDATE on
`agent_runs` and ensures `completed_at` is stamped exactly once.

```
[caller in dispatch goroutine]:
    errMsg = err.Error()
    _ = r.agentRepo.SetRunStatus(ctx, run.ID, "failed", &errMsg)
    r.handleFailedRun(ctx, run.ID, course.CourseID, course.StudentID, err)

[handleFailedRun]:
    IncrementAttemptCount(courseID)
        UPDATE courses
           SET generation_attempt_count = generation_attempt_count + 1,
               next_eligible_at = NOW() + make_interval(secs => $2)
         WHERE id = $1
      RETURNING generation_attempt_count

    if returnedCount >= generation_max_attempts:
        SetCourseTerminal(courseID)
            UPDATE courses SET status = 'generation_failed' WHERE id = $1
            EmitEvent(runID, 'generation_terminal', {...})
            notify student (TypeGenerationFailed)
    else:
        EmitEvent(runID, 'generation_failed_with_retry', {attempt: count, next_eligible_at: ...})
        notify student (TypeGenerationRetrying)
```

Both `IncrementAttemptCount` and `SetCourseTerminal` use the server pool
(`db.AcquireServerConn`) because `courses` has FORCE ROW LEVEL SECURITY and the poller has
no request context. This mirrors the existing `UPDATE courses SET status='active'` call in
`RunContentGeneration` (runner.go line 558).

---

## 7. Retry Policy

### 7.1 Configuration keys

| Config key | Default | Description |
|---|---|---|
| `generation_backoff_seconds` | `600` (10 minutes) | Duration of the backoff window after a failed run. Added to `NOW()` to compute `next_eligible_at`. |
| `generation_max_attempts` | `5` | Maximum consecutive dispatch-level failures before the course transitions to `generation_failed`. |

These keys are read from `system_config` via `configSvc.GetInt64`. If absent or ≤ 0, the
defaults above apply. The admin UI can update them via the existing config endpoint (the
allowed-key whitelist in `config_handler.go` must be extended in T4 to include these two
keys).

**REQ-AGENT-003 reconciliation:** REQ-AGENT-003 states "a failed run does not permanently
block retry." This design satisfies that requirement by providing bounded auto-retry
(up to `generation_max_attempts`) followed by a manual re-trigger gate, not permanent
blockage. After the manual re-trigger, the attempt count is reset to 0, so the course can
again reach `active` through the normal path. D18 explicitly reinterprets REQ-AGENT-003 as
"bounded auto-retry, then an explicit manual gate" rather than "unlimited auto-retry."

### 7.2 Recovery: explicit re-trigger

When a course is in `generation_failed`, an operator or student can re-trigger generation
via the following internal action (T4 exposes this; a future sprint may add an HTTP endpoint):

```
RecoverGenerationFailed(courseID):
    -- Require: course.status == 'generation_failed'
    UPDATE courses
       SET status = 'syllabus_approved',
           generation_attempt_count = 0,
           next_eligible_at = NULL
     WHERE id = $1 AND status = 'generation_failed'
    EmitEvent(runID_or_none, 'generation_recovery_triggered', {course_id: courseID})
    notify student (TypeGenerationRecovery)
```

The next `pollAndGenerate` tick will pick up the course and dispatch a new run. The attempt
counter starts from zero, giving the course a fresh budget of `generation_max_attempts`.

In S1, `RecoverGenerationFailed` is an internal function callable by a future admin HTTP
handler. A placeholder method should be added to `AgentRepository` so T3/T4 implement the
SQL; the HTTP handler wiring is out of scope for this sprint.

---

## 8. Idempotent Unified Dispatch (D19)

### 8.1 Two-layer guard

The existing flat path has no in-flight guard; the tree path has `inFlightLayered sync.Map`.
This design unifies to a **two-layer guard** for both paths:

**Layer 1 — Process-level registry (fast path):**

Two `sync.Map` fields on `AgentRunner`:

```go
// inFlightFlat guards the flat-course (content_generation) dispatch path.
// Keys are course UUID strings; presence means a goroutine is in flight.
// Added to match the existing inFlightLayered used for the tree path.
inFlightFlat sync.Map

// inFlightLayered (existing) guards the tree-mode dispatch path.
// Unchanged; retained as-is.
inFlightLayered sync.Map
```

Before dispatching a goroutine, `pollAndGenerate` calls `LoadOrStore(courseID.String(), struct{}{})`.
If the course is already in-flight (map entry present), it skips silently. This avoids a
database round-trip when the previous goroutine is still running.

**Layer 2 — Database unique index (atomic claim):**

```sql
CREATE UNIQUE INDEX agent_runs_one_running_per_course_type_idx
    ON agent_runs (course_id, run_type)
    WHERE status = 'running' AND course_id IS NOT NULL;
```

`CreateRun` (an `INSERT INTO agent_runs ... RETURNING ...`) will fail with `unique_violation`
(SQLSTATE 23505) if another process or goroutine has already inserted a `running` row for the
same `(course_id, run_type)`. The dispatcher must handle this error:

```go
func (r *AgentRepository) CreateRun(ctx context.Context, courseID uuid.UUID, runType string) (AgentRunRow, error) {
    // Existing INSERT. On unique_violation: return ErrRunAlreadyClaimed (new sentinel error).
    // Caller (runner.go): log at DEBUG, release in-flight map entry, return nil error (not a bug).
    ...
}

var ErrRunAlreadyClaimed = errors.New("agent: run already claimed for this course and type")
```

### 8.2 Dispatch sequence (updated `pollAndGenerate`)

The flat path retains `RunContentGeneration` as the generation body. `RunContentGeneration`
(runner.go lines 515–575) is NOT replaced or bypassed — it applies the REQ-AGENT-014 timeout
context, the `handleTimeout`/`handleAPIFailure` error classification, and the
`UPDATE courses SET status='active'` transition that makes a course usable. Dropping or
bypassing it would be a happy-path regression and a REQ-AGENT-014 violation.

The new dispatch hooks (`tokenCapPreFlight`, `handleFailedRun`, `handleCompletedRun`) wrap
`RunContentGeneration` in the goroutine body, not inside it. The relationship is:

1. `CreateRun` — atomic claim (new)
2. `tokenCapPreFlight` — fail-fast check before any paid work (new; §9)
3. `RunContentGeneration` — timeout ctx, `generateAllSections`, `handleTimeout`/
   `handleAPIFailure`, `UPDATE courses SET status='active'` (retained, unchanged)
4. On `RunContentGeneration` error: caller calls `SetRunStatus(failed)` then `handleFailedRun`
5. On `RunContentGeneration` success: caller calls `handleCompletedRun`

Note: `RunContentGeneration` already calls `SetRunStatus` internally for failure cases
(timeout, API failure). T4 must reconcile the double-status-write: either
(a) `RunContentGeneration` handles all `SetRunStatus` calls for the run it manages (preferred:
retain its internal `SetRunStatus` calls, and have the outer goroutine only call
`handleFailedRun`/`handleCompletedRun` for the courses-level updates), or (b) refactor
`RunContentGeneration` to return a richer error type that the outer goroutine uses to call
`SetRunStatus` once. Option (a) is preferred to minimise change to `RunContentGeneration`.
T4 must specify the chosen approach in code comments.

```
for each course in ListUntriggeredApprovals(ctx, maxAttempts):
    key = course.CourseID.String()
    if course.TreeMode:
        _, alreadyRunning = r.inFlightLayered.LoadOrStore(key, struct{}{})
    else:
        _, alreadyRunning = r.inFlightFlat.LoadOrStore(key, struct{}{})
    if alreadyRunning: continue

    go func(course CourseStudentRow):
        defer r.inFlight[course.TreeMode].Delete(key)  // conceptual; use correct map

        run, err = r.agentRepo.CreateRun(ctx, course.CourseID, runType)
        if errors.Is(err, ErrRunAlreadyClaimed):
            log.Printf("runner: course %s already claimed, skipping", course.CourseID)
            return
        if err != nil:
            log.Printf("runner: create run: %v", err)
            return

        // Pre-flight: token cap check BEFORE any paid work (§9).
        if err = r.tokenCapPreFlight(ctx, course.CourseID, course.StudentID); err != nil:
            errMsg = err.Error()
            _ = r.agentRepo.SetRunStatus(ctx, run.ID, "failed", &errMsg)
            r.handleFailedRun(ctx, run.ID, course.CourseID, course.StudentID, err)
            return

        if course.TreeMode:
            err = r.layeredRunner.seedTreeAndGenerateRoot(ctx, course.CourseID, course.StudentID)
        else:
            // Flat path: retain RunContentGeneration (timeout + active-transition intact).
            // RunContentGeneration sets its own SetRunStatus for failure cases internally.
            // The outer goroutine calls handleFailedRun / handleCompletedRun for the
            // courses-level columns only.
            err = r.RunContentGeneration(ctx, course.CourseID, course.StudentID)

        if err != nil:
            // NOTE: For the flat path, RunContentGeneration has already called
            // SetRunStatus(failed) internally. handleFailedRun here updates courses
            // columns only; it does NOT call SetRunStatus again.
            r.handleFailedRun(ctx, run.ID, course.CourseID, course.StudentID, err)
            return

        // Success path.
        r.handleCompletedRun(ctx, run.ID, course.CourseID, course.StudentID)
    ()
```

`handleFailedRun` and `handleCompletedRun` are new private methods in T4 (runner.go) that
centralise the attempt-count and `next_eligible_at` writes. They are not called from
existing handler paths (`HandleSectionRegen`, `pollLayeredGeneration`), which have their own
completion logic.

`handleFailedRun` does NOT call `SetRunStatus` — the caller is responsible for calling
`SetRunStatus` before `handleFailedRun`. This avoids double-stamping `completed_at` on the
`agent_runs` row.

### 8.3 Startup recovery compatibility

The startup recovery in `main.go` (line 106) currently runs **before** the config service
loads (line 114):

```go
// main.go line 106 — current order:
if _, err := pool.Exec(ctx,
    `UPDATE agent_runs SET status = 'failed', error = 'server restart' WHERE status = 'running'`,
); err != nil { ... }
// main.go line 114 — config loads AFTER:
configSvc := admin.NewConfigService(pool)
if err := configSvc.Load(ctx); err != nil { ... }
```

This ordering is **incorrect** for the new recovery path: the attempt-count update requires
the `generation_backoff_seconds` config value (`make_interval(secs => $1)`), which is not
available until after `configSvc.Load`. T4 must fix the startup sequence to:

1. Run DB migrations (`runMigrations`).
2. Load config service (`configSvc.Load`).
3. Mark stale `running` runs failed (the existing `UPDATE agent_runs SET status='failed'`).
4. Apply attempt-count and `next_eligible_at` updates for the server-restart-failed runs,
   using the now-loaded backoff config.
5. Check for max-attempts exhaustion; transition terminal courses.

Steps 3–5 together replace the current single-line recovery. The bulk SQL for step 4:

```sql
-- Startup recovery: increment attempt count for courses with server-restart-failed runs.
-- next_eligible_at is set to NOW() + backoff_window (loaded from config in step 2).
-- Only affects courses still in 'syllabus_approved' (not yet terminal or active).
UPDATE courses c
   SET generation_attempt_count = generation_attempt_count + 1,
       next_eligible_at = NOW() + make_interval(secs => $1)
 WHERE id IN (
     SELECT course_id FROM agent_runs
     WHERE status = 'failed' AND error = 'server restart'
       AND course_id IS NOT NULL
 )
   AND status = 'syllabus_approved';
```

Then check for max-attempts exhaustion and transition terminal courses. The cross-reference
to "§5.4" in previous drafts was incorrect; this is the full recovery specification (no
dangling reference).

---

## 9. Cost-Safety Hook (S1 Scope: Fail-Fast Only)

### 9.1 Hook point definition

The token-cap pre-flight check is the **sole S1 cost-safety deliverable**. It must fire
before any per-section paid work: before the first `professor.GenerateSection` call (which
calls `searchInternet` then `client.Messages`) and before any tree-mode node generation.

The hook is placed in the **dispatch goroutine**, immediately after `CreateRun` succeeds and
before the generation function is called (see §8.2 pseudocode, `tokenCapPreFlight`).

### 9.2 `tokenCapPreFlight` definition

```go
// tokenCapPreFlight checks the per-student token cap BEFORE any paid work begins.
// Returns ErrTokenCapExceeded when the cap is set and already exceeded.
// This is the S1 fail-fast hook; full cost gating (per-run budget, Brave accounting)
// is G3-S2.
//
// The existing per-call check in ThrottledClient.Messages runs AFTER searchInternet
// (Brave call) for each section. This pre-flight check stops the run before section
// iteration begins, eliminating Brave calls on a capped run.
//
// Uses the bare pool (not AcquireServerConn) because agent_token_usage has no RLS.
// The AND draft_id IS NULL filter restricts to the production token record, not
// any draft/preview record.
func (r *AgentRunner) tokenCapPreFlight(ctx context.Context, courseID, studentID uuid.UUID) error {
    cap := r.configSvc.GetInt64("per_student_token_limit")
    if cap <= 0 {
        return nil // cap disabled
    }
    var used int64
    err := r.pool.QueryRow(ctx,
        `SELECT COALESCE(total_tokens_used, 0)
         FROM agent_token_usage
         WHERE student_id = $1 AND course_id = $2 AND draft_id IS NULL`,
        studentID, courseID,
    ).Scan(&used)
    if err != nil && !errors.Is(err, pgx.ErrNoRows) {
        return err
    }
    if used >= cap {
        return ErrTokenCapExceeded
    }
    return nil
}
```

### 9.3 What this hook does NOT do (deferred to G3-S2)

- Does not account for Brave calls or count them toward any budget.
- Does not enforce a per-run token budget (only checks the existing lifetime aggregate).
- Does not pre-deduct a reservation to prevent over-spending.
- Does not block on Brave key availability.

These are G3-S2 concerns. T4 must not implement them in S1; it must leave the hook point
clean so S2 can replace `tokenCapPreFlight` with a richer `costPreFlight` call.

### 9.4 Why this ordering fixes the Brave over-spend

Today: `searchInternet` (Brave) → `profile.LoadProfileSummary` → `client.Messages` (token
cap check inside `ThrottledClient.Messages`).

After S1: `tokenCapPreFlight` (before section loop) → (if pass) → `searchInternet` → ...

A capped run hits `tokenCapPreFlight` in the dispatch goroutine, transitions to `failed`
via `handleFailedRun`, and exits without entering `generateAllSections`. Zero Brave calls are
made. The per-section token-cap check inside `ThrottledClient.Messages` remains as a defence-
in-depth guard for the case where token usage is written by a concurrent run between the
pre-flight and the section call.

---

## 10. Event Taxonomy

All events are emitted via the existing `AgentRepository.EmitEvent` into `pipeline_events`.
New event types are additive.

| Event type | Emitted when | Key payload fields |
|---|---|---|
| `generation_started` | (existing) Run begins | `course_id` |
| `generation_complete` | (existing) Run succeeds | `course_id` |
| `generation_timeout` | (existing) Run timed out | `course_id` |
| `api_failure` | (existing) API error | `course_id`, `error` |
| `generation_claimed` | **new** CreateRun succeeds (claim acquired) | `course_id`, `run_id`, `attempt_number` |
| `generation_failed_with_retry` | **new** Run failed; retry will occur | `course_id`, `attempt_number`, `next_eligible_at`, `max_attempts` |
| `generation_terminal` | **new** Run failed; max-attempts exhausted; course → `generation_failed` | `course_id`, `attempt_number`, `max_attempts` |
| `generation_recovery_triggered` | **new** Operator/student re-triggered a `generation_failed` course | `course_id`, `triggered_by` |
| `token_cap_preflight_failed` | **new** Token cap exceeded before any paid work | `course_id`, `tokens_used`, `cap` |

**Notification events (via `notify.Write`):**

The `notifications.type` column uses the `notification_type` PostgreSQL enum. The new
types below require the enum extensions defined in §3.2.2 to be applied in migration 024
before any INSERT using them.

| Scenario | Notification type constant | `notification_type` enum value | Recipient |
|---|---|---|---|
| Run fails with retry remaining | `TypeGenerationRetrying` | `'generation_retrying'` | student |
| Course reaches `generation_failed` | `TypeGenerationFailed` | `'generation_failed'` | student |
| Course recovered from `generation_failed` | `TypeGenerationRecovery` | `'generation_recovery'` | student |

T4 defines the new `notify.Type*` constants; T2 derives the notification requirements.

---

## 11. Backward Compatibility

### 11.1 Flat-course happy path — unchanged

A flat course that succeeds on the first attempt follows exactly the existing path:

1. `ListUntriggeredApprovals` returns the course (existing predicate still satisfied; new
   clauses evaluate trivially: `next_eligible_at IS NULL → true`, `0 < 5 → true`).
2. `CreateRun` inserts a `running` row (new unique index is satisfied; no conflict).
3. `tokenCapPreFlight` passes (no cap set or under cap).
4. `RunContentGeneration` runs — timeout context applied, `generateAllSections` executes,
   `UPDATE courses SET status='active'` fires on success (all unchanged from today).
5. `handleCompletedRun` resets `generation_attempt_count = 0`, `next_eligible_at = NULL`.

No behavior change for the happy path. The two new `courses` columns are NULL/0 for all
existing courses after migration; the eligibility predicates evaluate to `true` as expected.
`RunContentGeneration` is retained intact; its timeout wrapper (REQ-AGENT-014) and the
`active` transition are not removed.

### 11.2 Tree-mode dispatch — bounded by D21

The tree path is dispatched by `seedTreeAndGenerateRoot` from `pollAndGenerate`. Per D21,
the attempt counter applies at the dispatch level: if `seedTreeAndGenerateRoot` fails (or
the overall tree-generation cycle fails), `handleFailedRun` is called and the counter is
incremented. Individual layer completions do NOT reset the counter; only the full
tree-generation-complete event (the terminal SUCCESS state) causes `handleCompletedRun` to
reset the counter to 0.

This guarantees that a tree course which persistently fails at layer 5 will eventually
exhaust its `generation_max_attempts` budget and transition to `generation_failed` —
the storm is bounded.

The `inFlightLayered` guard remains for the `pollLayeredGeneration` path (layer-by-layer
ticks). The unique index on `agent_runs` covers `tree_layer_generation` runs as well,
providing defence-in-depth at the DB layer for the existing `layered_runner.go` CreateRun
call sites (see §3.2.5).

### 11.3 `section_regen` and `tree_layer_generation` runs

`section_regen` runs (from `pollFeedback`) are not affected by this design:

- They are not dispatched via `pollAndGenerate`.
- They do not increment `generation_attempt_count`.
- They are not counted toward `generation_max_attempts`.
- The unique index (`WHERE status='running'`) would block a second concurrent `section_regen`
  for the same course, which is desirable (prevents duplicate regen goroutines).

### 11.4 `TerminateStudentOperations`

`terminated` runs do not increment `generation_attempt_count` (see §5.1). T4 must ensure
the transition in `handleFailedRun` is only called when status is being set to `'failed'`,
not `'terminated'`.

### 11.5 RLS and server-pool discipline

All writes to `courses` (the new columns) must use a server-pool connection (`db.AcquireServerConn`)
because `courses` has FORCE ROW LEVEL SECURITY and the poller operates without a request
context. This mirrors the existing `UPDATE courses SET status='active'` pattern on line 558 of
`runner.go`. T4 must not use the bare pool for any of the new `UPDATE courses ...` statements.

The unique-index guard on `agent_runs` is enforced at the database layer regardless of which
pool connection is used. `agent_runs` does not have FORCE RLS (confirmed by repository.go
usage patterns — `CreateRun` uses `r.pool` directly); the unique index is therefore
universally enforced.

---

## 12. Alternatives Considered

### 12.1 Retry counter in `agent_runs` rather than `courses`

Counting failures per-run is straightforward, but determining "consecutive failures since last
success" requires a subquery over all `agent_runs` for the course ordered by `started_at`.
This is a read-amplification penalty on every eligibility check. Storing the counter directly
on `courses` makes the eligibility query O(1) per course row. The downside (counter divergence
on crash) is mitigated by the startup recovery path (§8.3).

### 12.2 Separate claim/lease table

A dedicated `course_generation_leases` table would provide richer metadata (lease holder,
expiry). Rejected because: (a) a partial unique index on `agent_runs` achieves the same
guarantee with zero extra tables; (b) a lease table requires a separate expiry-cleanup job;
(c) the system is single-instance, so inter-process contention is not a current concern.

### 12.3 Backoff computed at query time from `agent_runs`

The last-failure timestamp could be computed as `MAX(completed_at) WHERE status='failed'`
over `agent_runs`, avoiding a `next_eligible_at` column on `courses`. Rejected because:
(a) it requires a correlated subquery into `agent_runs` per-course in the eligibility query;
(b) the backoff window cannot be changed without re-querying historical timestamps;
(c) an explicit `next_eligible_at` is directly readable by monitoring queries.

### 12.4 Process-level registry only (no DB unique index)

The `sync.Map` alone would be sufficient for a single-process deployment if `CreateRun`'s
INSERT is fast enough. Rejected because: (a) server restarts clear the map, creating a window
between process start and the eligibility query where multiple goroutines could race to claim
the same course; (b) future multi-instance deployment would require the DB guard anyway;
(c) the unique index is zero-cost at query time and only adds latency on the (exceptional)
conflict path.

### 12.5 Reset attempt counter on each intermediate tree-layer completion

An earlier draft reset `generation_attempt_count` to 0 on every `tree_layer_generation`
`completed` event. This is unsound: a course that fails layer 5 forever would never exhaust
the budget because each successful layer 1–4 completion would zero the counter. Resetting only
on terminal SUCCESS (per D21) bounds the storm correctly for both flat and tree courses.

---

## 13. Open Questions

| # | Question | Owner | Impact |
|---|---|---|---|
| OQ-1 | Should `generation_attempt_count` be per-course-lifecycle (reset on each `syllabus_approved` transition) or globally cumulative? Current design resets on terminal SUCCESS; if a course is manually reset to `syllabus_approved` without calling `RecoverGenerationFailed`, the count is not reset. | T2/Lead | Scope of `RecoverGenerationFailed` |
| OQ-3 | Does the admin need a UI endpoint to trigger `RecoverGenerationFailed` in S1, or is a database-level operation sufficient for the sprint? | PM/Lead | Whether T4 must wire an HTTP handler |
| OQ-4 | Should the `generation_backoff_seconds` and `generation_max_attempts` config keys be seeded in the migration (like `correction_loop_max_iterations` in migration 002) or left to require explicit admin configuration? Seeding is safer for first-run. Resolved below in §14 (seeded). | T3 | Migration seed data |

OQ-2 is resolved by D21 (see §1 binding decisions table and §3.3).

---

## 14. Migration Plan

Migration 024 is strictly additive. Rollback path: remove the new `generation_failed` enum
value (PostgreSQL does not support `ALTER TYPE ... DROP VALUE` directly in older versions,
but since no data will yet carry `generation_failed` at rollback time, the column can be
checked and the row removed if needed — see notes below), remove the two `courses` columns,
remove the two new indexes.

**Rollback constraint:** PostgreSQL does not allow removing an enum value once added. The
rollback path for the `generation_failed` enum value is: (a) ensure no `courses` row has
`status = 'generation_failed'` (guaranteed if S1 has not been deployed), then (b) replace
the enum with a new enum type omitting the value. This is a two-step DDL that requires a
brief application pause. Given that migration 024 is never deployed without S1 being
deployed, the practical rollback is: revert S1 code, then run a compensation migration (025)
that adds no new values and does not reference `generation_failed`.

**Migration order within 024:**

1. `ALTER TYPE course_status ADD VALUE IF NOT EXISTS 'generation_failed';`
2. `ALTER TYPE notification_type ADD VALUE IF NOT EXISTS 'generation_retrying';`
3. `ALTER TYPE notification_type ADD VALUE IF NOT EXISTS 'generation_failed';`
4. `ALTER TYPE notification_type ADD VALUE IF NOT EXISTS 'generation_recovery';`
5. `ALTER TABLE courses ADD COLUMN IF NOT EXISTS generation_attempt_count ...;`
6. `ALTER TABLE courses ADD COLUMN IF NOT EXISTS next_eligible_at ...;`
7. `CREATE INDEX IF NOT EXISTS courses_generation_eligible_idx ...;`
8. `CREATE UNIQUE INDEX IF NOT EXISTS agent_runs_one_running_per_course_type_idx ...;`
9. Seed `system_config` (see below).

Steps 7 and 8 are index creations; they can be run `CONCURRENTLY` in a manual migration
to avoid table locks, but since migration 024 runs at server startup (while the server is
not serving traffic), the default non-concurrent build is acceptable.

Steps 1–4 are `ALTER TYPE ADD VALUE` statements. These must be separate statements (not
combined with INSERT statements that reference the new values in the same command). On PG 16+,
these can run inside a transaction block; however, T3 must not reference the new enum values
in the same statement that adds them — any INSERT using the new values must be a subsequent
statement. T3 must add a comment noting this PG 16 assumption.

**Seed data (resolving OQ-4):** Migration 024 seeds the two new config keys in `system_config`
to match the pattern established by migration 002. The `system_config` table has columns
`(key, value, updated_by, updated_at)` — there is no `description` column. The seed INSERT
must match this shape:

```sql
INSERT INTO system_config (key, value)
VALUES
    ('generation_backoff_seconds', '600'),
    ('generation_max_attempts',    '5')
ON CONFLICT (key) DO NOTHING;
```

---

## 15. Appendix — File-Set Impact Summary

| File | Change in S1 | Owner task |
|---|---|---|
| `migrations/024_generation_run_lifecycle.sql` | New migration (§3.2, §14): course_status enum, notification_type enum extensions, courses columns, indexes, seed data | T3 |
| `internal/agent/repository.go` | `ListUntriggeredApprovals` + `$1` param; `CreateRun` ErrRunAlreadyClaimed; new `IncrementAttemptCount`, `SetCourseTerminal`, `RecoverGenerationFailed` methods | T3 |
| `internal/agent/runner.go` | `inFlightFlat sync.Map`; `tokenCapPreFlight`; `handleFailedRun` (no SetRunStatus inside); `handleCompletedRun` (reset only on terminal SUCCESS); unified `pollAndGenerate`; startup recovery extension (after config load); `RunContentGeneration` retained intact | T4 |
| `internal/agent/layered_runner.go` | `CreateRun` call site at line 915 must handle `ErrRunAlreadyClaimed` (23505) as defence-in-depth | T4 |
| `internal/agent/client.go` | No change in S1 | — |
| `internal/agent/professor.go` | No change in S1 (Brave ordering fix deferred to S2) | — |
| `internal/admin/config_handler.go` | Whitelist two new config keys | T4 |
| `internal/notify/notify.go` | Three new `Type*` constants (`TypeGenerationRetrying`, `TypeGenerationFailed`, `TypeGenerationRecovery`) | T4 |
| `cmd/server/main.go` | Move startup recovery (agent_runs mark-failed) to AFTER config load; add courses attempt-count bulk update + terminal check | T4 |
| `docs/design/generation-run-lifecycle.md` | This document | T1 (design) |
