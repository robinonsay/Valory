# G3-S1-T3-F02-U01 — ListUntriggeredApprovals bounded eligibility

> Level 5b — a **Unit** node: the smallest independently reviewable chunk within one File. Pins
> design intent + requirement + acceptance so the SQE reviews chunk-by-chunk.

- **File:** `G3-S1-T3-F02` (`internal/agent/repository.go` — see `plan/files/G3-S1-T3-F02-repository.md`)
- **Requirement(s):** REQ-AGENT-064

## Design (what this chunk does and how)

`ListUntriggeredApprovals(ctx)` returns the courses the poller may start a run for. Today it returns
any `syllabus_approved` course with no running/completed same-type run — which lets a failed course
re-trigger every 30s (the storm). This Unit adds two declarative exclusions, keeping the existing
`(CASE WHEN c.tree_mode THEN 'tree_layer_generation' ELSE 'content_generation' END)::agent_run_type`
cast and the `tree_mode` branch unchanged:

1. **Backoff window** — exclude a course whose most recent same-type run **failed within the last
   `backoff_window`** (config key, default per the T1 TDD, e.g. 10 minutes):
   ```sql
   AND NOT EXISTS (
       SELECT 1 FROM agent_runs ar2
       WHERE ar2.course_id = c.id
         AND ar2.run_type = (CASE WHEN c.tree_mode THEN 'tree_layer_generation'
                                  ELSE 'content_generation' END)::agent_run_type
         AND ar2.status = 'failed'
         AND ar2.started_at > now() - make_interval(secs => $1)
   )
   ```
2. **Terminal state** — exclude courses already in `c.status = 'generation_failed'` (added by F01-U02).

The backoff seconds are passed as a bound parameter from config (not string-interpolated). The
function signature gains the backoff parameter (or reads config internally — match the repo's
existing config-access pattern). `CourseStudentRow` is unchanged.

Counterfactual the SQE must confirm: with a failed run 1 minute ago, the course is **absent** from
the result; with the same failed run `backoff_window + 1s` ago, it is **present** again; a
`generation_failed` course is **never** present.

## Acceptance (done-criteria for this chunk)

- [ ] Failed-within-backoff course excluded; same course eligible again after the window; terminal
      `generation_failed` never returned; clean course still returned.
- [ ] Backoff value is config-driven and passed as a query parameter (no interpolation); enum cast +
      tree_mode branch preserved.
- [ ] Carries its `@{"req": ["REQ-AGENT-064"]}` annotation.
- [ ] Covered by an integration test (T5) under `SET ROLE valory_app` that fails if either exclusion
      is removed (counterfactual proven).
