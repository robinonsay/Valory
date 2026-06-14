# Findings — `<node-id>`

The decision-ready synthesis of this discovery pass. **This is a contract:** the PM
(Root → Goals) or `software-lead` (Sprints → Tasks, Task → impl) reads it as the *input* to its
authoring step, exactly the way a worker reads its task contract. If a section below cannot be
filled from grounded answers, the pass is **not done** — that is a coverage gap (the
discovery-gate kicks it back).

## 1. Candidate children

The nodes this node should decompose into, as justified by the answers above.

| Child | One-line scope | Backed by |
|-------|----------------|-----------|
| <e.g. G2-S3-T1> | <what it covers> | Q1, Q3 |

## 2. Falsifiable acceptance per child

For each candidate child, the observable, falsifiable criterion that says it is done.

- **<child>** — <criterion a planner can write a test/review against without further lookup>

## 3. Residual assumptions & risks

Anything proceeding on an assumption rather than evidence, plus what would invalidate it. Carry
these forward into the node's task contract so workers see them.

- **ASSUMPTION:** <…>  — *invalidated if:* <…>
- **RISK:** <…>

## 4. Evidence index

The grounding for the above, so the gate (and any later reader) can verify without re-deriving.

- Q1 → `path:line`, `REQ-…`
- Q3 → <source>
