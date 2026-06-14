# `plan/discovery/` — the durable discovery (research) trees

This directory holds the **discovery phase** trees: a depth-first, disk-durable question tree run
*before a decomposition boundary* whenever the triage flags uncertainty, so decomposition is
**evidence-based** instead of assumption-based. Full model:
[docs/discovery-phase.md](../../docs/discovery-phase.md).

`frontier.json` is to discovery what `plan/state.json` is to execution — the durable DFS runtime,
so the loop survives compaction. Never hand-edit it; the deterministic mechanics
(pop-highest-weight, attach children, prune subsumed siblings, budget backstop) are owned by
[scripts/discovery.py](../../scripts/discovery.py).

## Layout

```
plan/discovery/
  README.md            # this file
  _TEMPLATE/           # copy this when starting a pass for a node
    frontier.json      # the durable DFS stack — schemas/discovery-frontier.schema.json
    Q01.md             # one file per answered question (question + answer + evidence + outcome)
    findings.md        # the decision-ready synthesis that feeds decomposition (a contract)
  <node-id>/           # one dir per node that earned a pass: root, G2, G2-S3, G2-S3-T4 …
    frontier.json
    Q<NN>.md
    findings.md
    review.md          # the discovery-gate verdict (groundedness + coverage)
```

`<node-id>` matches the tree node the pass serves: `root`, a goal `G<n>`, a sprint `G<n>-S<m>`,
or a task `G<n>-S<m>-T<k>`.

## The loop (orchestrator side)

```
triage (uncertain?) ─ no ─→ decompose directly (zero discovery cost)
        │ yes
        ▼
discovery.py init <node>            seed the frontier with the triage's open questions (add --weight)
  repeat:
    discovery.py next               pop the highest-weight open question (DFS priority)
    → dispatch discovery-agent      it answers, writes Q<NN>.md, returns child questions + weights
    discovery.py add --parent …     attach the children
    discovery.py prune <id> --by …  discard siblings the answer subsumed (BEFORE fanning wider)
    discovery.py answer <id> …      mark resolved (leaf w/ --leaf-reason, or a node w/ children)
    discovery.py check              budget backstop + structural audit (exit 4 → escalate)
  until `next` reports the frontier empty
discovery.py done --to done         refused unless 0 open + clean check
→ write findings.md → discovery-gate → review.md → set state.json discovery pointer → decompose
```

## Rules

- **A node is not decomposable** while its `state.json` discovery pointer is `in_progress` or
  `escalated` — the same hard gate as an unresolved `depends_on`.
- **A budget stop is never a clean exit.** Hitting a cap → `status: escalated`, residual open
  questions logged as explicit assumptions; widen the budget or proceed deliberately.
- **Cost concentrates at the top.** Big pass at Root, smaller before each Goal→Sprints, usually
  none before a Task→impl. Lower nodes that keep needing real discovery signal a too-shallow
  higher pass.
- Discovery dirs are durable plan state and are committed; only `plan/.snapshots/` is gitignored.
