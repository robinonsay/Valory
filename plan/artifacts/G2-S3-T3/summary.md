# G2-S3-T3 — Student HITL HTTP/SSE Handlers: Implementation Summary

## Files produced

- `internal/agent/node_handler.go` (new — 770 lines)
- `cmd/server/main.go` (minimal additions: tree-repo + layered-runner + node-handler wiring, plus route mount)

## All nine endpoints implemented

| Method | Path | HTTP status |
|--------|------|-------------|
| GET | `/nodes` | 200 |
| POST | `/nodes/generate` | 202 |
| POST | `/nodes/{nodeId}/refine` | 202 |
| PATCH | `/nodes/{nodeId}/approve` | 200 |
| PATCH | `/nodes/{nodeId}/feedback` | 200 |
| POST | `/nodes/{nodeId}/regenerate` | 202 |
| GET | `/nodes/{nodeId}/chat` | 200 |
| POST | `/layers/{layer}/expand` | 200 |

409 conflict codes: NODE_ALREADY_GENERATING, NODE_NOT_REVIEWABLE, NODE_NOT_REGENERABLE,
LAYER_NOT_FULLY_APPROVED, TREE_ALREADY_COMPLETE.

## Acceptance criteria met

- All nine endpoints with exact paths/methods/codes — PASS
- Ownership via request-scoped conn (404 on non-owned); all node writes on server pool (D12); generate/refine/regenerate guarded by atomic test-and-set — PASS
- /layers/{layer}/expand delegates to LayeredRunner.ExpandToNextLayer with correct 409/400 mapping — PASS
- CSRF on mutating methods (enclosing group already applies it); background gen uses context.Background() + agent_run anchor — PASS
- go build/go vet clean; parameterised queries; requirement annotations present; admin route area untouched — PASS

## Deviations

None.

---

## Rework — SQE gate fix (2026-06-13)

### D1 (BLOCKER) — `runBackgroundRefine` error path logged

Both silent drops in the error path of `runBackgroundRefine` (~lines 694-709) were replaced
with the same logged pattern already used in `runBackgroundGenerate`:

- `fConn.Exec(UPDATE course_nodes SET status='failed' ...)` — now logs `node_handler: background refine: fail node <id>: <err>` on failure.
- `h.agentRepo.SetRunStatus(runID, "failed")` — now logs `node_handler: background refine: set run failed <id>: <err>` on failure.

Full audit of other background-goroutine silent drops performed. The success path of
`runBackgroundRefine` had three additional silent `_ =` drops (`AppendNodeChat`,
`EmitNodeEvent`, `SetRunStatus`) that were inconsistent with the logging pattern in
`runBackgroundGenerate`. All three were fixed to match.

### O1 — Student role guard on node routes

`nodeHandler.Routes(r)` in `cmd/server/main.go` was wrapped in a `r.Group` that applies
`r.Use(auth.RequireRole("student"))`, matching the `/profile` pattern. Admin route area
untouched.

### O2 — `node_type` allowlist in `generateNode`

After the non-empty check, a `switch` allowlist was added against `{syllabus, section_goal,
concept, content}` (root is implicit/not requestable). An invalid value now returns
`400 BAD_REQUEST` before any DB write, consistent with `expandLayer`'s layer allowlist.

### Build/vet

`go build ./... && go vet ./...` — clean.
