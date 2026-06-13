# Valory Agentic Development Architecture

How Valory is built: a logically deep **work-decomposition tree** executed through a
structurally flat **orchestrator + workers** layer, with all coordination state persisted
to disk so it survives context compaction.

This document is the canonical reference. `CLAUDE.md` carries the compaction-durable
summary (mission, invariants, worker schema, compact instructions); the agent definitions
in `.claude/agents/` implement the roles described here.

---

## 1. Why a tree, not a flat sprint

A flat sprint throws unrelated work into one pool and loses the parent–child links that
make scope, ordering, and "done" legible. A **tree** keeps every unit of work traceable to
its parent and ultimately to the root ask, so an agent always knows *why* a task exists and
*what* completion means at its level. Intent fans out into outcomes, outcomes into batches,
batches into units, units into contracts, contracts into artifacts.

Flat still wins when work is small, highly sequential, or dominated by same-file edits with
many dependencies. Use the tree when decomposition buys parallelism and traceability;
collapse unused levels for a small ask (it may go Root → Tasks directly).

---

## 2. The decomposition tree

| Level | Node | Question it answers | Cardinality |
|-------|------|--------------------|-------------|
| 0 | **Root** — the core ask | Why are we here? | 1 |
| 1 | **Goals** — major outcomes | What outcomes satisfy the ask? | 1 → N |
| 2 | **Sprints** — work batches per goal | What batches per outcome? | 1 → N |
| 3 | **Tasks** — concrete units per sprint | What discrete pieces of work? | 1 → N |
| 4 | **Specification** — design / requirements / acceptance | How, what-for, and done-criteria | 1 → 3 facets |
| 5 | **Implementation** — the artifact | The realized work product | 1 |
| 5a | **File** — one source file (the implementation grain) | Which files realize the task? | 1 → N |
| 5b | **Unit** — a coherent chunk within a File | What chunk satisfies which requirement, and how is it checked? | 1 → N |

### The level-4 asymmetry

Levels 0–3 are pure **scope decomposition**: one node spawns smaller-scope children.
Level 4 is different — it is a **fan-out into three facets of a single task**, not a further
subdivision of scope:

- **Design** — how it will be built
- **Requirements** — what it must satisfy
- **Acceptance** — the tests / review that confirm it is done

This is the serialization boundary between orchestrator and worker. The triplet becomes the
**worker's prompt contract**: design + requirements go *in*; acceptance defines what a
successful *return* looks like.

### The implementation grain: Files and Units

Level 5 is not atomic. When a task spans more than one source file, the implementation is
planned along two finer axes that make parallelism and review structural rather than a matter
of discipline:

- **File (5a) — one source file.** A File node maps one-to-one onto a single source file the
  task creates or edits. The File is the **natural unit of parallel dispatch**: assign one
  worker per File and the "no two workers edit the same file" invariant (§12) stops being a
  rule you have to remember and becomes true *by construction*. Id `G<n>-S<m>-T<k>-F<NN>`; plan
  doc `plan/files/<task>-F<NN>-<slug>.md`.
- **Unit (5b) — a coherent chunk within a File.** A Unit is the smallest independently
  reviewable piece of a File: a function, a type, an HTTP handler, a migration block, a
  test-case group, or an AsciiDoc `include::` section. A Unit is where design intent, the
  requirement(s) it satisfies, and its acceptance check are pinned, so the SQE reviews
  chunk-by-chunk and traceability runs all the way down. Id `G<n>-S<m>-T<k>-F<NN>-U<MM>`; plan
  doc `plan/units/<task>-F<NN>-U<MM>-<slug>.md`.

**Why a File is not called a "module".** In Valory "module" already means a Go
package/directory — requirements live in `<module-dir>/requirements/REQ-<MODULE>-NNN.json` and
the systems-engineer reviews "cross-module integration." A File is finer than a module (one
file, not a package), so it carries its own name rather than overloading the term. A module may
contain many Files; a File belongs to exactly one module.

**Collapse the grain for small tasks.** A single-file task *is* one File and needs no separate
Unit planning; do not manufacture File/Unit docs where the task contract already says
everything. This mirrors the "collapse unused levels" rule for the upper tree (§1).

---

## 3. Execution reality: one orchestrator, flat workers

The tree is the **logical** structure. Claude Code's **execution** structure is constrained:
orchestration is single-level. There is exactly one orchestrator, and workers (subagents)
cannot recurse or spawn their own workers; their results land back only in the orchestrator's
context. A 6-level logical tree therefore **flattens at execution time** into one orchestrator
coordinating one layer of workers.

> The orchestrator owns the tree; workers own leaves.

Because every worker result returns to the orchestrator's context, **workers return summaries,
not transcripts** — fan out for breadth, keep each worker's mandate "report the result."

---

## 4. Level → Valory agent mapping

| Level | Node | Owner agent |
|-------|------|-------------|
| 0 | Root | `project-manager` — authors `plan/root.md` |
| 1 | Goals | `project-manager` — authors `plan/goals/*.md` |
| 2 | Sprints | `software-lead` (orchestrator) — `plan/sprints/*.md` |
| 3 | Tasks | `software-lead` (orchestrator) — `plan/tasks/*.md` |
| 4 | Design facet | `design-author` |
| 4 | Requirements facet | `requirements-author` |
| 4 | Acceptance facet | `test-author` + the review pipeline (see §6) |
| 5 | Implementation | `senior-engineer` / `junior-engineer` (workers) |
| 5a | File breakdown | `design-author` (the structural part of the design facet); the orchestrator dispatches one worker per File |
| 5b | Unit breakdown | `design-author` defines the chunks; `test-author` pins each Unit's acceptance; the worker realizes them |

The `software-lead` is the single durable orchestrator (owns levels 0–3 of the live tree).
Everyone else is a worker it dispatches against one leaf. Files and Units (5a/5b) are the
*structural design* of a leaf — produced by the design facet, scheduled by the orchestrator
(one worker per File), and realized by the worker; they are not a new layer of orchestration.

---

## 5. Responsibilities

### Orchestrator (`software-lead`, owns levels 0–3)

Holds the tree on disk (`plan/`) and never delegates the *structure*:

1. Decompose Root → Goals → Sprints → Tasks (with the PM owning Root + Goals).
2. Maintain the task list and the dependency graph in `plan/state.json`.
3. Decide which tasks are independent enough to parallelize.
4. Dispatch leaf work to workers — **one task per worker**, the task file as its contract.
5. Receive worker summaries (pointer + verdict), integrate, advance the tree.
6. Hold integration/acceptance authority across workers.

### Worker (owns one level-3 task → its level-4/5 content)

Given **one task** plus its specification triplet, produces the implementation:

- Reads the **design** and **requirements** as its mandate.
- Produces the **implementation** artifact under `plan/artifacts/<task-id>/` (or the real
  source tree, when the task is production code).
- Self-checks against **acceptance** before reporting.
- Returns a structured summary — what was built, acceptance status, deviations — **never the
  full output** (see §8.5).

### Choosing the coordination primitive per sprint

- **Subagents** when tasks are independent and only the *result* matters (most fan-out).
  Lower token cost.
- **Agent teams** when tasks need cross-talk — competing-hypothesis debugging, cross-layer
  changes, or review where workers challenge each other. Higher cost.

---

## 6. The review pipeline is the acceptance facet

Valory's quality bar is unchanged: the three review gates **are** the level-4 acceptance
facet. Every worker leaf passes through them before it is integrated.

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
Senior SQE   (final gate)
      |
      v
Integrated by the orchestrator → tree advances
```

| Gate | Agent | Checks |
|---|---|---|
| 1a | `software-quality-engineer` | Code quality, correctness, requirement satisfaction, test coverage, tracing annotations |
| 1b | `systems-engineer` | Security, performance, scalability, reliability, cross-module integration |
| 2 | `senior-quality-engineer` | Holistic cross-cutting review; final pass/fail; nothing ships without it |

A reviewer returns a worker-style verdict: **pass / fail + which criteria + actionable
feedback**. Nothing ships without the Senior SQE gate.

---

## 7. Dependencies and ordering

A pure tree implies siblings are independent, but real tasks have cross-edges. Handle this at
the orchestrator level:

- Model dependencies explicitly in `plan/state.json` (`depends_on`). A task with unresolved
  dependencies is **not dispatchable**.
- Within a sprint, dispatch only the **independent frontier** in parallel; serialize the rest.
- For sequential chains, run workers in sequence, each consuming the prior result, with the
  orchestrator passing context forward.
- Never parallelize dependent tasks. Split a task that touches both backend and frontend
  unless they are trivially coupled.

The tree is the *plan*; the dependency graph drives *scheduling*.

---

## 8. Sizing

- **Tasks (level 3)** are self-contained, independently reviewable units — a function, a
  module, a test file, a review.
- **Workers in flight:** 3–5 concurrent is the default; coordination cost and token spend
  scale with worker count, with diminishing returns past that.
- **Tasks per worker:** ~5–6 keeps a worker productive without excessive context switching.
- **Files & Units (level 5):** one File = one source file (the dispatch grain — one worker per
  File); one Unit = one reviewable chunk within a file. Break a task into Files only when it
  spans multiple files; break a File into Units only when it is large enough that chunk-level
  acceptance buys clarity. Don't over-grain a small task.
- Scale up only when work genuinely parallelizes.

---

## 9. File / state layout

The tree and live state live on disk so a long-running orchestrator can lose its conversation
history and reconstruct itself.

```
plan/
  README.md            # conventions + this layout
  root.md              # the core ask + whole-effort acceptance (level 0)
  goals/               # G<n>.md — high-level goals, link to their sprints (level 1)
  sprints/             # G<n>-S<m>.md — sprint scope, task list, deps (level 2)
  tasks/               # G<n>-S<m>-T<k>.md — the design/requirements/acceptance triplet (level 4)
  files/               # G<n>-S<m>-T<k>-F<NN>-<slug>.md — one source file per File node, the dispatch grain (level 5a)
  units/               # G<n>-S<m>-T<k>-F<NN>-U<MM>-<slug>.md — one reviewable chunk per Unit node (level 5b)
  artifacts/           # <task-id>/ — implementation output + acceptance results (level 5)
  .snapshots/          # PreCompact backups of state.json (gitignored)
  state.json           # LIVE coordination state: task DAG, statuses, workers, result pointers
```

Each task file **is** the worker's contract. The orchestrator reads/writes the whole tree;
workers read their one task file and write to their artifact directory. Cross-cutting guidance
lives in `CLAUDE.md` — every worker loads it automatically.

`plan/state.json` validates against `schemas/plan-state.schema.json`.

**Relationship to legacy `sprints/` and `requirements/`:** historical sprint prose
(`sprints/*.md`) and the legacy central requirements (`requirements/l1-requirements.json`,
`l2-requirements.json`) are **not** migrated into the tree — that mirrors CLAUDE.md's
legacy-requirements rule. New efforts use `plan/`; new L2 requirements stay co-located with
their module per CLAUDE.md.

---

## 10. Surviving compaction (orchestrator durability)

A swarm orchestrator runs long, so it *will* hit compaction. Governing principle:

> **Anything in conversation history is vulnerable to compaction; anything reloaded from disk
> at startup is not.**

### 10.1 Mission and invariants live in CLAUDE.md, not the opening prompt
The orchestrator's mission, invariants, worker return schema, and compact instructions sit in
`CLAUDE.md` so they reload into the system prompt on every compaction. Never rely on the
opening conversational turn to carry a rule needed for the whole run.

### 10.2 Compact Instructions section
A `# Compact Instructions` block in `CLAUDE.md` steers the summarizer to preserve verbatim:
the mission, the current task DAG / phase, dispatched workers + task_ids, completed task_ids +
artifact paths, and pending task_ids + blockers. Intermediate reasoning and already-persisted
tool output are explicitly discardable.

### 10.3 Compact at task boundaries
Run `/compact` proactively at phase transitions (e.g. a sprint completing) so the summary is
clean and the orchestrator never operates in the degraded zone near the auto-trigger.

### 10.4 Hooks snapshot and reload live state
- **PreCompact** (`.claude/hooks/precompact-snapshot.sh`) — copies `plan/state.json` to a
  timestamped `plan/.snapshots/` backup before compaction. (PreCompact stdout is not injected
  into context, so it cannot remind the model — it is a pure safety-net snapshot.)
- **SessionStart** with matcher `compact`/`resume`
  (`.claude/hooks/sessionstart-reload.sh`) — prints `plan/state.json` and a pointer to
  `plan/root.md` as `additionalContext` so the post-compaction orchestrator reconciles against
  disk instead of a lossy prose summary.

The plan tree on disk is the *durable spec*; `state.json` is the *durable runtime*. Between
them and `CLAUDE.md`, a post-compaction orchestrator rebuilds everything it needs from disk.

### 10.5 Keep worker output off the orchestrator's window
Each worker returns a terse, structured summary — a pointer to an artifact plus an acceptance
verdict, never the full output. One large worker return can refill the window right after a
summary and cause compaction thrash. "Report the result, not everything" is both a
context-budget rule and a compaction-stability rule.

---

## 11. Worker return schema

Every worker (contributor or reviewer) returns this shape, never raw output:

```
task_id      G<n>-S<m>-T<k>
status       done | blocked | failed
artifact_path  plan/artifacts/<task-id>/  (or the real source paths touched)
acceptance   pass | fail  + which criteria
deviations   anything that diverged from the task contract (or "none")
```

---

## 12. Failure modes to watch

- **Orchestrator does the work itself** instead of dispatching — it must wait for and delegate
  to workers.
- **Return flood** — verbose worker reports refill the protected window. Enforce summary-only
  returns (§10.5).
- **Rule lost to compaction** — a rule lived only in the opening prompt. Move it to CLAUDE.md.
- **Summary drift** — trusting a lossy summary instead of reconciling against disk. Use the
  SessionStart reload.
- **Compaction thrash** — a large tool output refills context right after a summary. Keep
  returns to pointers.
- **File conflicts** — two workers editing one file. Partition files; one worker per file-set.
- **Stale tree** — workers finish but status lags, blocking dependents. Reconcile `state.json`
  before advancing.
- **Over-decomposition** — not every ask needs six levels, and not every task needs File/Unit
  docs. Collapse unused levels; a single-file task is one File with no separate Unit planning.

---

## Appendix: one-line summary

A logically deep tree (intent → outcome → batch → task → contract → artifact, the artifact
itself grained into Files and Units), executed through a flat layer (one `software-lead`
orchestrator owning levels 0–3, parallel workers owning each leaf's design → implementation →
acceptance, dispatched one worker per File so file conflicts cannot arise), with dependencies
scheduled by the orchestrator, worker returns kept to pointers, the three review gates serving
as the acceptance facet, and the orchestrator's mission/structure/state persisted to `plan/` +
`CLAUDE.md` so compaction can never permanently lose them.
