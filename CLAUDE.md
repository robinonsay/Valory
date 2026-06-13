# Valory — Claude Code Project Guide

Valory is an AI professor system built on Go (backend), Vue.js (frontend), PostgreSQL, and the Anthropic SDK. It generates personalized courses, homework, and grading for any topic via a multi-agent architecture.

## Development agent architecture

Valory is built by decomposing work into a **tree** and executing it through a **single
durable orchestrator coordinating parallel workers**. The agents live in `.claude/agents/`;
the full rationale is in [docs/agentic-architecture.md](docs/agentic-architecture.md); the
live tree and coordination state live in [plan/](plan/).

### Mission (never drop this)

Build Valory by decomposing every effort into a work tree
(Root → Goals → Sprints → Tasks → spec-triplet → artifact). One orchestrator (`software-lead`)
owns levels 0–3 and the integration authority; parallel workers each own one leaf. The
orchestrator's mission, the tree (`plan/`), and the live state (`plan/state.json`) are
persisted to disk so compaction can never permanently lose them. Nothing ships without passing
the acceptance facet (SQE + Systems Engineer → Senior SQE).

### The decomposition tree

| Level | Node | Owner agent |
|---|---|---|
| 0 Root | the core ask | `project-manager` → `plan/root.md` |
| 1 Goals | major outcomes | `project-manager` → `plan/goals/*.md` |
| 2 Sprints | work batches per goal | `software-lead` (orchestrator) → `plan/sprints/*.md` |
| 3 Tasks | concrete units per sprint | `software-lead` (orchestrator) → sprint files + `plan/state.json` |
| 4 Spec triplet | **design / requirements / acceptance** | `design-author` / `requirements-author` / `test-author` + reviewers → `plan/tasks/*.md` |
| 5 Implementation | the artifact | `senior-engineer` / `junior-engineer` (workers) → `plan/artifacts/<task-id>/` |
| 5a File | one source file (the dispatch grain) | `design-author` breaks it down → `plan/files/<task>-F<NN>-<slug>.md`; one worker per File |
| 5b Unit | a coherent chunk within a File | `design-author` + `test-author` → `plan/units/<task>-F<NN>-U<MM>-<slug>.md` |

Levels 0–3 are scope decomposition. **Level 4 is the asymmetry**: not a further subdivision of
scope but a fan-out into three facets of one task. The task file (`plan/tasks/G<n>-S<m>-T<k>.md`)
**is the worker's prompt contract** — design + requirements go *in*, acceptance defines what a
successful *return* looks like.

**The implementation grain (level 5).** Level 5 is not atomic. A **File** is one source file —
the natural unit of parallel dispatch, so "one worker per File" makes the no-conflict invariant
structural. A **Unit** is a coherent chunk within a File (a function, type, handler, migration
block, test group, or `include::` section) — the smallest reviewable piece, where a Unit's
requirement(s) and acceptance check are pinned. "Module" stays reserved for a Go
package/directory (a module holds many Files); break a task into Files/Units only when it spans
multiple files, and collapse the grain for a single-file task.

### Execution: orchestrator + workers

Orchestration is single-level: one orchestrator, and workers cannot recurse. The logical tree
**flattens at execution** into the `software-lead` orchestrator coordinating one layer of
workers. The orchestrator owns the tree; workers own leaves.

### Invariants (hold these for the whole run)

- The orchestrator (`software-lead`) owns levels 0–3; each worker owns exactly one leaf.
- Dispatch only the **independent frontier**; never parallelize dependent tasks. 3–5 workers in
  flight, ~5–6 tasks each, is the default.
- One worker per file-set — no two workers edit the same file. Dispatching at the **File** grain
  (level 5a, one File = one source file) makes this structural rather than a discipline.
- Workers return a **result-file pointer + acceptance verdict, never full output** (a return
  flood refills the context window and causes compaction thrash).
- The orchestrator does not do the work itself — it dispatches and integrates.
- Reconcile `plan/state.json` (status / acceptance / artifact_path) **before** dispatching
  dependents. `plan/state.json` validates against `schemas/plan-state.schema.json`.
- Split a task that touches both backend and frontend unless they are trivially coupled.

### Worker return schema

```
task_id       G<n>-S<m>-T<k>
status        done | blocked | failed
artifact_path plan/artifacts/<task-id>/  (or the real source paths touched)
acceptance    pass | fail  + which criteria
deviations    <anything that diverged from the task contract, or "none">
```

### Acceptance facet = the review pipeline

The three review gates *are* the level-4 acceptance facet; every worker leaf passes through
them before integration.

```
Worker artifact
      |
      v
SQE  +  Systems Engineer   (parallel — first gate)
      |
 Pass? +-- no --> back to the worker with feedback --> review again
      |
     yes
      v
Senior SQE   (final gate; nothing ships without it)
      |
      v
Orchestrator integrates → reconciles state.json → tree advances
```

| Agent file | Role |
|---|---|
| `project-manager` | Owns Root + Goals; authors `plan/root.md`, `plan/goals/*.md`, and requirement JSON |
| `software-lead` | The durable orchestrator; owns the tree (levels 0–3), dispatches the frontier, integrates |
| `design-author` | Level-4 **design** facet: TDDs, API specs, data models |
| `requirements-author` | Level-4 **requirements** facet: authors/validates requirement JSON |
| `test-author` | Level-4 **acceptance** facet: test plans, unit/integration tests |
| `senior-engineer` | Worker: complex or cross-cutting leaves |
| `junior-engineer` | Worker: well-scoped, clearly-defined leaves |
| `software-quality-engineer` | Acceptance gate 1a: code quality, correctness, test coverage |
| `systems-engineer` | Acceptance gate 1b (parallel): security, performance, integration |
| `senior-quality-engineer` | Acceptance gate 2: final cross-cutting quality and delivery approval |

## Requirements

Requirements live as JSON files alongside the code they govern:

```
<module-directory>/requirements/REQ-<MODULE>-<NNN>.json
```

Legacy exception (Sprint ≤17): requirements authored before Sprint 24 live in
the central `requirements/l1-requirements.json` and
`requirements/l2-requirements.json` files. All NEW L2 requirements go in
module directories per the rule above; L1 (system-level) requirements stay in
`requirements/l1-requirements.json`. Do not migrate the legacy entries
piecemeal — that reconciliation is a dedicated future refactor.

All requirement files must validate against `schemas/requirements.schema.json`. The `requirements-author` agent owns this schema and all requirement files.

## Tech stack

- **Backend:** Go — idiomatic, explicit error handling, `context.Context` throughout
- **Frontend:** Vue.js — Composition API, `<script setup>`, TypeScript
- **AI:** Anthropic SDK — Claude models via the Claude API
- **Database:** PostgreSQL — parameterized queries only, no SQL injection vectors
- **Infrastructure:** Docker + docker-compose

## Key conventions

- Always add comments so the *why* is obvious to a future reader
- No speculative abstractions — implement exactly what requirements specify
- No database mocks in integration tests — use a real PostgreSQL instance via Docker Compose
- AsciiDoc course content: max 500 lines per document, use `include::` for composition
- Secrets and API keys come from environment variables by default and must never be hardcoded. The `VALORY_SECRET_KEY` environment variable (a 32-byte base64-encoded master key set once at install) enables the managed-secrets subsystem, which allows admins to supply `ANTHROPIC_API_KEY` and `BRAVE_API_KEY` via the admin config UI. When set via the UI, managed secrets are stored AES-256-GCM encrypted in the `managed_secrets` table. The managed secret takes precedence over the env var; if `VALORY_SECRET_KEY` is absent or invalid, the system falls back to env vars and logs a single WARN at startup without crashing. No plaintext secret value may appear in source code, logs, audit payloads, or any HTTP response body.

## Orchestrator durability & compaction

The `software-lead` orchestrator runs long and will hit compaction. Durability is by
construction: anything in conversation history is vulnerable; anything reloaded from disk is
not. The mission, invariants, and worker schema above live in this file precisely so they
reload into the system prompt on every compaction.

- Keep `plan/state.json` continuously up to date as work progresses — it is the durable runtime.
- Compact proactively at phase boundaries (e.g. a sprint completing), not at the auto-threshold.
- **Hooks** (`.claude/settings.json`):
  - `PreCompact` → `.claude/hooks/precompact-snapshot.sh` backs up `plan/state.json` to a
    timestamped file in `plan/.snapshots/` before compaction.
  - `SessionStart` (matchers `compact`, `resume`) → `.claude/hooks/sessionstart-reload.sh`
    re-injects `plan/state.json` + a pointer to `plan/root.md` as context so the orchestrator
    reconciles against disk, not a lossy summary.

## Compact Instructions

When summarizing this conversation, ALWAYS preserve verbatim:

- The **Mission** and **Invariants** from the Development agent architecture section above.
- The current task DAG / phase (the mirror of `plan/state.json`).
- Which workers are dispatched and their assigned `task_id`s.
- Completed `task_id`s and their `artifact_path` values.
- Pending `task_id`s and their blockers.

Discard: intermediate reasoning and raw tool outputs already written to disk (the plan tree,
`plan/state.json`, requirement files, and artifacts are all recoverable from disk).
