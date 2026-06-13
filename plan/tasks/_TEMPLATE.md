# G<n>-S<m>-T<k> — <task title>

> Level 4 — the task contract. This file **is** the worker's prompt: the orchestrator fills in
> Design + Requirements (what goes *in*) and Acceptance (what a successful *return* looks like);
> the worker reads it as its entire mandate. Owned/assembled by `software-lead`.

- **Worker:** <senior-engineer | junior-engineer | test-author | design-author | requirements-author>
- **Verifier (acceptance gate):** <SQE + Systems Engineer | SQE | Senior SQE>
- **Depends on:** <task ids, or none>
- **Artifact path:** `plan/artifacts/G<n>-S<m>-T<k>/` (and/or the source paths the task touches)

## Design (how — the mandate)

<The approach the worker must follow: which files/functions, the data flow, the chosen pattern,
constraints. Links to any TDD in docs/. Enough that the worker does not invent scope.>

## Requirements (what-for)

<The REQ-MODULE-NNN ids this task implements, and any acceptance criteria added to them. The
behavior the artifact must satisfy.>

## Acceptance (done-criteria — what a successful return looks like)

A checklist the worker self-verifies before reporting, and the verifier confirms:

- [ ] <observable, falsifiable criterion>
- [ ] Every function carries a `@{"req", [...]}` tracing annotation; every test a `@{"verifies", [...]}`
- [ ] Tests pass (`go test ./...` / `vitest run`); no lint errors
- [ ] No secrets, SQL injection, or XSS introduced

## Worker return schema (what the worker reports back — pointer, not full output)

```
task_id       G<n>-S<m>-T<k>
status        done | blocked | failed
artifact_path <path>
acceptance    pass | fail  + which criteria
deviations    <anything that diverged from this contract, or "none">
```
