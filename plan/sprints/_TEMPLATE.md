# G<n>-S<m> — <sprint title>

> Level 2. A batch of work for goal G<n>. Owned by `software-lead` (orchestrator).

## Scope

<One paragraph: what this sprint delivers and the natural seams it breaks along.>

## Tasks (level 3)

| Task | Increment | Worker | Verifier (acceptance) | Depends on |
|------|-----------|--------|-----------------------|------------|
| G<n>-S<m>-T1 | <discrete deliverable> | senior-engineer \| junior-engineer \| test-author \| design-author \| requirements-author | SQE + Systems Engineer \| SQE | — |

Each task has a contract file at `tasks/G<n>-S<m>-T<k>.md` (the design/requirements/acceptance
triplet). The orchestrator dispatches only the **independent frontier** in parallel.

## Dependencies / sequencing

<Intra-sprint ordering. Which tasks parallelize; which serialize and why.>

## Requirements in scope

<REQ-MODULE-NNN ids this sprint implements or introduces.>

## Definition of done

<When the sprint is complete: all tasks done + acceptance pass + integrated.>
