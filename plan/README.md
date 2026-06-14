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
  discovery/           # PRE-decomposition — DFS research trees per node (see plan/discovery/README.md)
  root.md              # level 0 — the core ask + whole-effort acceptance
  goals/               # level 1 — G<n>.md, major outcomes
  sprints/             # level 2 — G<n>-S<m>.md, work batches per goal
  tasks/               # level 4 — G<n>-S<m>-T<k>.md, the design/requirements/acceptance triplet
  files/               # level 5a — <task>-F<NN>-<slug>.md, ONE per source file (a File = the dispatch grain)
  units/               # level 5b — <task>-F<NN>-U<MM>-<slug>.md, ONE per chunk (a Unit = a reviewable piece of a File)
  artifacts/           # level 5 — <task-id>/, worker output + acceptance results
  .snapshots/          # PreCompact backups of state.json (gitignored)
  state.json           # LIVE coordination state — validates against schemas/plan-state.schema.json
```

(Level 3 — the task list — lives inside each sprint file and in `state.json`. Level 4 is the
per-task triplet file; level 5 is its artifact directory. Files/Units (5a/5b) are the
*implementation grain* of a task — present only when a task spans multiple files; a single-file
task is one File with no separate Unit docs.)

(`discovery/` is the *pre-decomposition* research phase — a depth-first question tree run before
a node is cut, only when uncertainty is high. Full model:
[docs/discovery-phase.md](../docs/discovery-phase.md).)

## Who owns what

| Level | Node | Owner agent |
|-------|------|-------------|
| pre Discovery | `discovery/<node>/` | `software-lead` drives; `discovery-agent` answers; `discovery-gate` gates |
| 0 Root | `root.md` | `project-manager` |
| 1 Goals | `goals/G<n>.md` | `project-manager` |
| 2 Sprints | `sprints/G<n>-S<m>.md` | `software-lead` (orchestrator) |
| 3 Tasks | task list in the sprint file + `state.json` | `software-lead` (orchestrator) |
| 4 Design / Requirements / Acceptance | `tasks/G<n>-S<m>-T<k>.md` | `design-author` / `requirements-author` / `test-author` + reviewers |
| 5 Implementation | `artifacts/<task-id>/` | `senior-engineer` / `junior-engineer` |
| 5a File (one source file) | `files/<task>-F<NN>-<slug>.md` | `design-author` breaks it down; one worker realizes each File |
| 5b Unit (a chunk within a File) | `units/<task>-F<NN>-U<MM>-<slug>.md` | `design-author` + `test-author`; realized by the worker |

## Conventions

- **IDs are canonical:** goal `G<n>`, sprint `G<n>-S<m>`, task `G<n>-S<m>-T<k>`, File
  `G<n>-S<m>-T<k>-F<NN>`, Unit `G<n>-S<m>-T<k>-F<NN>-U<MM>`. Task ids (and any File/Unit grain)
  in `state.json` must match `schemas/plan-state.schema.json`.
- **A File is one source file; a Unit is a chunk within it.** Dispatch one worker per File so
  "no two workers edit the same file" holds by construction. A Unit is the smallest reviewable
  piece (a function, type, handler, migration block, test group, or `include::` section) and
  carries its own requirement(s) + acceptance check. **"Module" is not this** — in Valory a
  module is a Go package/directory (where `requirements/` lives); a module contains many Files.
- **A task file *is* the worker's contract.** The orchestrator fills in the triplet (design +
  requirements + acceptance); the worker reads it as its entire mandate and reports back a
  pointer + acceptance verdict (never full output).
- **Only the orchestrator writes the tree.** Workers read their one task file and write only to
  their artifact directory (or the real source tree, for production-code tasks).
- **Reconcile before advancing.** After a worker reports, the orchestrator updates the task's
  status / acceptance / artifact_path in `state.json` before dispatching dependents.
- **Legacy is not migrated.** Historical `sprints/*.md` prose and the central
  `requirements/l1,l2-requirements.json` stay where they are; new efforts use this tree.
- **Discover before decomposing.** When a node is uncertain, run a discovery pass
  (`scripts/discovery.py`, [docs/discovery-phase.md](../docs/discovery-phase.md)) and let
  `findings.md` drive the cut; a node is not decomposable until its `state.json` discovery
  pointer reaches `done`. Never hand-edit `frontier.json` — the tool owns it.

## Templates

Copy `goals/_TEMPLATE.md`, `sprints/_TEMPLATE.md`, and `tasks/_TEMPLATE.md` when adding nodes.
For the implementation grain, copy `files/_TEMPLATE.md` (per source file) and
`units/_TEMPLATE.md` (per chunk) — only when a task is large enough to need them.
