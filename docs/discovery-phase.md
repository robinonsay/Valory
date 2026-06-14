# Valory Discovery Phase — research before decomposition

How Valory **gathers information before it plans**. The work-decomposition tree in
[agentic-architecture.md](agentic-architecture.md) assumes the structure of an effort is
knowable top-down. It usually is not. This document specifies the **discovery phase**: a
depth-first, disk-durable question tree that runs *before each decomposition boundary* and
turns a vague ask into grounded, decision-ready findings.

This is the canonical reference for discovery. Once accepted, it propagates into
`docs/agentic-architecture.md` (the flow), `CLAUDE.md` (mission + invariants), the
`software-lead` / `project-manager` / new `discovery-*` agent definitions, the plan-state and
frontier JSON schemas, and the compaction hooks.

---

## 1. Why discovery

The decomposition tree (Root → Goals → Sprints → Tasks) is built top-down in one planning pass
that assumes **perfect information**: the PM authors Root + Goals and the `software-lead`
authors Sprints + Tasks as if the right cuts were knowable upfront. The only investigation in
the current architecture is the level-4 **design facet** (`design-author`), and it happens
*after* a task is already carved out — so when the **cut itself** is wrong because it was made
blind, the design facet cannot fix it. It designs the wrong task well.

The symptom is rework: a task passes its own acceptance but is the wrong task, discovered only
during integration. A human given a vague task does not behave this way — they **gather
information first, then plan**, and the gathering is itself depth-first: a question is asked, its
answer raises sharper questions, you go deep until a line of inquiry is settled, then backtrack
to siblings. Discovery makes that explicit, durable, and reviewable.

> Discovery's job is to make decomposition **evidence-based** instead of assumption-based.

---

## 2. The discovery primitive

Discovery is a **depth-first question tree** persisted to disk. It is a reusable
sub-procedure, **not** a one-time front phase: the same primitive runs **before every
decomposition boundary** — before Root becomes Goals, before a Goal becomes Sprints, before a
Sprint becomes Tasks, before a worker implements a Task — but only **when uncertainty at that
node is high enough that a wrong cut is likely** (§3). When a node is already well understood —
the codebase plainly shows the pattern, the requirement is unambiguous — discovery is skipped
and the node is decomposed directly, exactly as today. This is the "collapse unused levels"
rule of [agentic-architecture.md](agentic-architecture.md) §1 applied to research.

Cost is therefore **proportional to uncertainty**, and it concentrates at the top of the tree
(see §13).

---

## 3. The uncertainty trigger (cost governor)

Per-level discovery only fires "when uncertain," so "uncertain" needs a test cheap enough that
the governor does not cost as much as the research it gates. At every decomposition boundary the
orchestrator runs one **triage** judgment — a single prompt, no fan-out:

> Can I write this node's children **and** each child's falsifiable acceptance right now, each
> citing concrete evidence (a file, a requirement, a prior artifact)? Or am I about to guess?

A node earns a discovery pass if **any** of these hold:

1. the decomposition would invent a name / interface / contract not yet grounded in the
   codebase or in requirements;
2. two or more plausible decompositions exist and the choice **materially changes** downstream
   work;
3. a child's acceptance cannot be made falsifiable without an unknown fact;
4. a prior sibling already hit rework for a reason that could recur here.

If none hold, decompose directly — zero added cost. The key economy: **the triage's output is
the seed question set.** "Should I research?" and "what are the root questions?" are the same
step — the governor *is* the top of the question tree.

---

## 4. The depth-first frontier loop

"Depth-first" is a **priority ordering**, not a single-threaded stack. It coexists with the
parallel fan-out the rest of the architecture prizes once you separate *what you resolve first*
from *how many you resolve at once*.

The orchestrator holds an **ordered open-question frontier** on disk (`frontier.json`, §8). Each
question carries a `weight` (`constraint_weight` — how much its answer is expected to prune or
change). The loop:

1. **Pop** the highest-weight open question — depth-first *priority*.
2. **Dispatch** it; it returns weighted **child** questions.
3. **Prune before going wider.** Reconcile the children against the *global* frontier and
   discard siblings the new answer subsumed. This prune step is the entire reason to go deep
   before wide — *you cannot prune siblings you have not yet reached.* A breadth-first search
   spends budget on branches a deeper answer would have killed.
4. **Fan out the survivors.** Dispatch a bounded batch (2–3) of the now-highest-weight open
   questions in parallel.

Go deep on the spine, prune, fan out the survivors a few at a time. The **prune must be done by
whoever holds the whole frontier** — the worker returns children + weights, the orchestrator
prunes against the global set. This is the same "orchestrator owns the tree, worker owns the
leaf" split used for execution.

---

## 5. Termination — three distinct stops

Conflating these is the central trap; keep them separate.

| Stop | Fires when | Meaning |
|---|---|---|
| **Branch stop** | a single question's answer is **decision-stable** — no remaining child question would change a build decision | that question is a **leaf**; record `leaf_reason` |
| **Pass stop** | the frontier is empty **and** findings suffice to enumerate this node's children + write each child's acceptance | discovery for this node is **done**; hand off to decomposition |
| **Budget stop** | a depth / open-question / token cap is hit | **inconclusive** — escalate, never a clean exit |

- The **branch stop**'s operational test, applied by the answering worker: *"does this answer
  let me write a falsifiable statement a planner can act on without further lookup?"* Yes →
  leaf, and it returns **why** (so the gate can catch premature stops).
- A **pass stop** requires *both* conditions. If the frontier empties but you still cannot write
  a child's acceptance, that is a **coverage gap**, not a finish — it goes back to discovery (the
  gate enforces this, §7).
- A **budget stop** is a backstop, never a clean exit. It returns "inconclusive, here is what is
  still open" and escalates: proceed on **explicitly-logged assumptions** or widen the budget.
  Never let a budget stop masquerade as a pass stop — that is exactly how a confident wrong plan
  is produced.

The decision-relevance rule (branch stop) does the work; the budget is a safety net. If branches
routinely die by **budget** rather than **decision-relevance**, the relevance test is too greedy
— that is the calibration signal.

---

## 6. Who drives it — durability comes from disk, not from the context

The platform forbids agent recursion (agentic-architecture.md §3: subagents cannot spawn
subagents), so discovery cannot nest agent-in-agent. Resolution:

- **Cross-level loop** lives in `software-lead`. It already owns a frontier loop and disk
  reconciliation. Discovery adds a mode: before decomposing any node, **triage → (if uncertain)
  run the discovery frontier loop → then decompose.**
- **Answering** is dispatched to a read-only `discovery-agent` (Read / Grep / WebSearch — the
  `Explore`-style toolset) so question-answering chatter stays **out** of the orchestrator's
  context, avoiding the return-flood failure (agentic-architecture.md §12).
- **Durability** comes from the disk, not from which context holds the loop. The
  `discovery-agent` **checkpoints each answer to `plan/discovery/<node>/Q<NN>.md` as it goes**,
  not at the end. The DFS tree is therefore on disk regardless of which context drives it — the
  same principle that makes `state.json` durable, applied one level down. Compaction cannot lose
  it.

---

## 7. The discovery gate

Discovery now *drives* the plan, so it needs its own quality gate — otherwise the single point
of failure moves from "planning blind" to "planning on hallucinated answers." This is a **new
gate**, not the SQE (which reviews code). It checks two things:

- **Groundedness** — every finding cites concrete evidence (`file:line`, a requirement id, a real
  doc / URL) **or** is flagged as an explicit assumption. Confident ungrounded claims fail.
- **Coverage** — the unasked-question audit. The reviewer tries to write the node's children +
  acceptance *from the findings alone*; every place it cannot without guessing is a **coverage
  gap** → back to discovery. This is the check that stops a budget stop from sneaking through
  disguised as a pass stop.

The gate audits the **shape** of the search, not just the findings, using `leaf_reason` (catches
**premature** stops) and `pruned_by` (catches **over-eager** prunes — a sibling killed by an
answer that did not actually subsume it). The gate is cheap relative to what it prevents: a blind
decomposition that causes multi-task rework.

---

## 8. On-disk layout

The discovery tree mirrors the work tree and lives under `plan/discovery/`, one directory per
tree node that earns a pass:

```
plan/discovery/<node-id>/        # node-id = root, G2, G2-S3, G2-S3-T4 …
  frontier.json                  # ordered open questions + weights + status — the durable DFS stack
  Q<NN>.md                       # one per answered question: question, answer, evidence cites, children, leaf_reason
  findings.md                    # decision-ready synthesis that feeds decomposition
  review.md                      # discovery-gate verdict
```

`frontier.json` is to discovery what `state.json` is to execution — the durable runtime of the
DFS. It validates against `schemas/discovery-frontier.schema.json`. Shape:

```jsonc
{
  "node": "G2-S3",                 // which tree node this discovery serves (or "root")
  "status": "discovering",         // discovering | gated | done | escalated
  "budget": { "max_depth": 4, "max_open": 20, "token_cap": 150000 },
  "questions": [{
    "id": "Q3",
    "parent": "Q1",                // null for triage-seeded roots
    "depth": 2,
    "weight": 0.8,                 // constraint_weight → DFS pop order
    "status": "answered",          // open | answered | pruned | inconclusive
    "answer_ref": "Q3.md",         // the full answer + evidence lives in the file, not here
    "children": ["Q7", "Q8"],
    "leaf_reason": null,           // why no children (decision-stable) when it is a leaf
    "pruned_by": null              // id of the answer that subsumed this one
  }]
}
```

`leaf_reason` and `pruned_by` exist for the gate (§7): they make the *shape* of the search
auditable, not just the answers.

**`findings.md` is a contract.** It must emit exactly what the decomposer consumes:

1. candidate **children** of the node,
2. each child's **falsifiable acceptance**,
3. residual **assumptions / risks**,
4. **evidence** cites.

The PM (Root → Goals) or `software-lead` (Sprints → Tasks) reads `findings.md` as its authoring
input, the way a worker reads its task contract today.

---

## 9. Relationship to `state.json`

Discovery is a **separate runtime with a pointer in `state.json`**, not merged into it. Folding
question-level detail into `state.json` would bloat its schema and mix two cadences (discovery
churns fast, execution slowly). Instead, each tree node carries one thin pointer:

```jsonc
"discovery": { "status": "done", "path": "plan/discovery/G2-S3/", "gate": "pass" }
```

`state.json` stays the single **index** — where everything is, and whether a node's discovery is
resolved before it is decomposed. `frontier.json` holds the heavy DFS detail. This is a small
**additive** change to `schemas/plan-state.schema.json`.

The decomposition rule then becomes a hard gate, exactly parallel to the existing `depends_on`
rule:

> A node is **not decomposable** while its `discovery.status` is `discovering`, `gated`, or
> `escalated` — only `done` clears the gate (and `done` requires `gate: pass`), the same way a
> task with unresolved dependencies is not dispatchable.

The pointer's `status` mirrors the frontier's status vocabulary (§8) exactly, so there is one
state vocabulary, not two.

---

## 10. Surviving compaction

Disk-durability is only real if a compacted orchestrator **reloads** it — durable-on-disk
without a reload hook just means the data outlives the agent's awareness of it. Discovery rides
the same rails as `state.json`:

- **PreCompact** (`.claude/hooks/precompact-snapshot.sh`) also snapshots any in-flight
  `frontier.json` into `plan/.snapshots/`.
- **SessionStart** (`.claude/hooks/sessionstart-reload.sh`, matchers `compact` / `resume`) also
  surfaces any `frontier.json` with `status: discovering`, so a post-compaction orchestrator
  knows "there is an unfinished discovery pass on node X — resume it" instead of re-triaging from
  scratch and duplicating research.

---

## 11. End-to-end flow

```
Vague ask
  → Discovery (DFS question tree, persisted, gated)          ← NEW
  → grounded findings.md (children + falsifiable acceptance)
  → Root + Goals (project-manager, now evidence-based)
  → [before each decompose: triage → discovery if uncertain] ← NEW, recurses per level
  → Sprints → Tasks → spec triplet → implementation → acceptance   (existing pipeline)
```

Discovery slots **in front of** every decomposition step that the triage flags as uncertain. The
existing execution pipeline (workers, the three review gates, integration) is unchanged.

---

## 12. Failure modes to watch

- **Researching the obvious** — a discovery pass fires on a node the codebase already answers.
  The triage (§3) was too eager; tighten the four criteria.
- **Budget stop disguised as done** — an inconclusive pass is handed off as if complete. The
  coverage check (§7) and the `escalated` status (§5) exist to prevent this.
- **Premature leaf** — a question stops spawning children while a decision-changing answer is
  still missing. `leaf_reason` + the gate catch it.
- **Over-eager prune** — a sibling killed by an answer that did not actually subsume it.
  `pruned_by` + the gate catch it.
- **Breadth-first creep** — fanning out before pruning, so budget is spent on branches a deeper
  answer would have killed. Enforce the prune-before-widen order (§4).
- **Discovery flood** — verbose answer transcripts returned to the orchestrator refill its
  window. Answers go to disk (`Q<NN>.md`); the orchestrator sees pointers + child questions.
- **Discovery lost to compaction** — the loop's context is summarized away. The disk tree + the
  reload hook (§10) make this recoverable.
- **Uncertainty leaking downward** — lower nodes keep needing real discovery. That is the signal
  a higher pass was too shallow (§13), not that discovery is too expensive.

---

## 13. Sizing and the cost expectation

With the governor in place, **discovery cost concentrates at the top of the tree and falls
toward the leaves**: a substantial pass at Root, a smaller one before each Goal → Sprints,
usually **zero** before a Task → implementation. If lower nodes keep needing real discovery,
uncertainty is leaking downward — the higher pass was too shallow. That is the diagnostic that
tells you the design is (or is not) working, and it is the opposite of "discovery is too
expensive": a correct top-level pass *buys* cheap lower levels.

---

## Appendix: one-line summary

A depth-first, disk-durable question tree (`plan/discovery/`, driven by `software-lead`,
answered by a read-only `discovery-agent` that checkpoints each answer to disk) runs before
every decomposition boundary the triage flags as uncertain; it resolves the most-constraining
question first and prunes siblings before fanning out, stops a branch by decision-relevance and
a pass by sufficiency-to-decompose (escalating on budget, never faking completion), is checked
by a groundedness + coverage gate, points into `state.json` so a node is not decomposable until
its discovery resolves, and rides the existing compaction hooks — so decomposition becomes
evidence-based and the cost concentrates at the top of the tree.
