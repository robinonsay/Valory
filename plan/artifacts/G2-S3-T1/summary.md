# G2-S3-T1 Rework Summary

## What was built

node_chair.go and repository.go implement Chair entry points for knowledge-tree
node generation and draft-scoped pipeline event tracking.  This rework corrects
two runtime-crashing UPSERT bugs exposed by migration 022, adds missing unit and
integration tests, and corrects inaccurate requirement annotations.

## UPSERT fixes

Migration 022 replaced the uq_token_usage_student_course table constraint with
two partial unique indexes. Postgres requires ON CONFLICT to carry a WHERE
predicate matching the index predicate exactly.

Fix 1 (draft path, node_chair.go):
  ON CONFLICT (draft_id) WHERE draft_id IS NOT NULL

Fix 2 (student path, client.go):
  ON CONFLICT (student_id, course_id) WHERE draft_id IS NULL

The seed UPSERT in client_test.go was also updated.

## Tests added

Unit tests in node_chair_test.go: buildNodeChatMessages (7 cases) and
nodePayloadForResponse (table-driven + 3 individual = 10 cases). All pass.

Integration tests in node_chair_integration_test.go: CreateRunForDraft,
GetEventsAfterForDraft (2 cases), EmitNodeEvent, and token UPSERT regression
guards for both student and draft paths (plus mutual exclusion test). All 7 pass.

## Annotation corrections

Replaced REQ-AGENT-038 (layer pause behavior) with REQ-AGENT-044 on prompt
helpers. Removed REQ-AGENT-055 (concurrent conflict guard) from GenerateNode
and RefineNode (that guard lives in TransitionNodeStatus).
