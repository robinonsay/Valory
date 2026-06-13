# `plan/` — the durable work-decomposition tree

This directory is the on-disk form of Valory's work-decomposition tree and its live
coordination state. It exists so the **single orchestrator** (`software-lead`) can survive
context compaction: the tree is the durable *spec*, `state.json` is the durable *runtime*,
and `CLAUDE.md` carries the durable *mission + rules*. Between them, a post-compaction
orchestrator rebuilds everything it needs from disk.

Full rationale: [docs/agentic-architecture.md](../docs/agentic-architecture.md).

## Layout

```
plan/
  README.md            # this file
  root.md              # level 0 — the core ask + whole-effort acceptance
  goals/               # level 1 — G<n>.md, major outcomes
  sprints/             # level 2 — G<n>-S<m>.md, work batches per goal
  tasks/               # level 4 — G<n>-S<m>-T<k>.md, the design/requirements/acceptance triplet
  artifacts/           # level 5 — <task-id>/, worker output + acceptance results
  .snapshots/          # PreCompact backups of state.json (gitignored)
  state.json           # LIVE coordination state — validates against schemas/plan-state.schema.json
```

(Level 3 — the task list — lives inside each sprint file and in `state.json`. Level 4 is the
per-task triplet file; level 5 is its artifact directory.)

## Who owns what

| Level | Node | Owner agent |
|-------|------|-------------|
| 0 Root | `root.md` | `project-manager` |
| 1 Goals | `goals/G<n>.md` | `project-manager` |
| 2 Sprints | `sprints/G<n>-S<m>.md` | `software-lead` (orchestrator) |
| 3 Tasks | task list in the sprint file + `state.json` | `software-lead` (orchestrator) |
| 4 Design / Requirements / Acceptance | `tasks/G<n>-S<m>-T<k>.md` | `design-author` / `requirements-author` / `test-author` + reviewers |
| 5 Implementation | `artifacts/<task-id>/` | `senior-engineer` / `junior-engineer` |

## Conventions

- **IDs are canonical:** goal `G<n>`, sprint `G<n>-S<m>`, task `G<n>-S<m>-T<k>`. Task ids in
  `state.json` must match `schemas/plan-state.schema.json`.
- **A task file *is* the worker's contract.** The orchestrator fills in the triplet (design +
  requirements + acceptance); the worker reads it as its entire mandate and reports back a
  pointer + acceptance verdict (never full output).
- **Only the orchestrator writes the tree.** Workers read their one task file and write only to
  their artifact directory (or the real source tree, for production-code tasks).
- **Reconcile before advancing.** After a worker reports, the orchestrator updates the task's
  status / acceptance / artifact_path in `state.json` before dispatching dependents.
- **Legacy is not migrated.** Historical `sprints/*.md` prose and the central
  `requirements/l1,l2-requirements.json` stay where they are; new efforts use this tree.

## Templates

Copy `goals/_TEMPLATE.md`, `sprints/_TEMPLATE.md`, and `tasks/_TEMPLATE.md` when adding nodes.
