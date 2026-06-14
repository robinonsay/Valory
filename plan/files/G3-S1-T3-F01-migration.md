# G3-S1-T3-F01 — migrations/024_generation_run_lifecycle.sql

> Level 5a — a **File** node: exactly one source file. The dispatch grain — one worker owns this
> File. Owned by `design-author` (structural design facet); realized by the assigned worker.

- **Source file:** `migrations/024_generation_run_lifecycle.sql` (new)
- **Task:** `G3-S1-T3` (see `plan/tasks/G3-S1-T3.md`)
- **Worker:** senior-engineer
- **Depends on:** none (additive schema; runs before F02's queries rely on it at runtime)

## Purpose (what this file is for)

The additive, idempotent schema change that backs the bounded run lifecycle: the terminal
`generation_failed` course status and a covering index for the new eligibility sub-selects. It adds
no cost columns (G3-S2) and must apply cleanly on both a fresh DB and the current populated DB,
following the 021–023 convention (BEGIN/COMMIT, self-record in `schema_migrations`, idempotent
guards). It must not alter existing rows' behavior — existing flat/tree courses keep their current
status values and flows.

## Units (the chunks within this file — level 5b)

| Unit id | Chunk | Requirement(s) | Acceptance |
|---|---|---|---|
| `G3-S1-T3-F01-U01` | `schema_migrations` self-record + `BEGIN`/`COMMIT` wrapper (convention) | REQ-AGENT-061 | Re-running the migration is a no-op; row present in `schema_migrations`. |
| `G3-S1-T3-F01-U02` | Add terminal `generation_failed` to the `courses.status` enum/CHECK (match existing constraint form) | REQ-AGENT-062 | A course can be set to `generation_failed`; existing status values unaffected; eligibility never selects it. |
| `G3-S1-T3-F01-U03` | Covering index on `agent_runs (course_id, run_type, status, started_at)` for the eligibility sub-selects | REQ-AGENT-063 | `EXPLAIN` of `ListUntriggeredApprovals` uses the index; idempotent (`IF NOT EXISTS`). |

## Acceptance (what a successful return for this File looks like)

- [ ] Applies cleanly on a fresh DB and on the current populated DB; fully idempotent on re-run.
- [ ] Adds only `generation_failed` + the index; no cost columns; no destructive change.
- [ ] Matches the 021–023 convention (transaction wrapper, self-record, guarded DDL).
- [ ] No secrets, no destructive `DROP`, no data migration that could lose rows.
