# G<n>-S<m>-T<k>-F<NN> — <source file path>

> Level 5a — a **File** node: exactly one source file the task creates or edits. The File is
> the **dispatch grain** — one worker owns this File, so "no two workers edit the same file"
> holds by construction. Author the breakdown only when the task spans multiple files; a
> single-file task *is* one File and needs no separate File/Unit docs. Owned by `design-author`
> (structural design facet); realized by the worker the orchestrator assigns.
>
> NOTE: "File" is finer than "module" — a module is a Go package/directory, and a module holds
> many Files. Don't conflate the two.

- **Source file:** `<e.g. internal/admin/oversight_handler.go>`
- **Task:** `G<n>-S<m>-T<k>` (this File's parent contract — see `plan/tasks/G<n>-S<m>-T<k>.md`)
- **Worker:** <senior-engineer | junior-engineer>
- **Depends on:** <other File ids whose output this File consumes, or none>

## Purpose (what this file is for)

<One paragraph: the responsibility of this single source file and how it fits the task's design.
Which package it lives in, what it exports, what it must not do.>

## Units (the chunks within this file — level 5b)

One row per coherent chunk. Link to a `plan/units/…` doc only when a Unit is large enough to
need its own contract; otherwise the row here is enough.

| Unit id | Chunk | Requirement(s) | Acceptance |
|---|---|---|---|
| `G<n>-S<m>-T<k>-F<NN>-U01` | <function / type / handler / migration block / test group> | REQ-<MODULE>-<NNN> | <how it's checked> |

## Acceptance (what a successful return for this File looks like)

- [ ] <observable, falsifiable criterion for this file>
- [ ] Every function carries a `@{"req", [...]}` tracing annotation; every test a `@{"verifies", [...]}`
- [ ] Tests pass; no lint errors; no secrets, SQL injection, or XSS introduced
