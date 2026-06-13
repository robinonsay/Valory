# G<n>-S<m>-T<k>-F<NN>-U<MM> — <chunk name>

> Level 5b — a **Unit** node: the smallest independently reviewable chunk within one File — a
> function, a type, an HTTP handler, a migration block, a test-case group, or an AsciiDoc
> `include::` section. A Unit pins design intent, the requirement(s) it satisfies, and its
> acceptance check so the SQE can review chunk-by-chunk and traceability runs all the way down.
> Author a Unit doc only when the chunk is large or subtle enough to warrant its own contract;
> otherwise the row in the parent File doc suffices.

- **File:** `G<n>-S<m>-T<k>-F<NN>` (`<source file path>` — see `plan/files/…`)
- **Requirement(s):** REQ-<MODULE>-<NNN>

## Design (what this chunk does and how)

<The signature/shape, the data flow, the chosen pattern, the constraints. Enough that the worker
implements exactly this and invents no scope.>

## Acceptance (done-criteria for this chunk)

- [ ] <observable, falsifiable behavior of this chunk>
- [ ] Carries its tracing annotation (`@{"req", [...]}`, or `@{"verifies", [...]}` for a test)
- [ ] Covered by a test that fails if the behavior regresses
