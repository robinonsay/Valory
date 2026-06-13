# G2-S2-T1 — Migration 022 Summary

## Artifact
`migrations/022_tree_generation.sql`

## Objects Created

**New enums**: `node_type_enum` (5 values), `node_status_enum` (6 values)

**Enum value additions** (outside transaction, `IF NOT EXISTS`):
- `course_status`: `awaiting_layer_approval`, `awaiting_regeneration`
- `agent_run_type`: `tree_layer_generation`, `node_generation`
- `pipeline_event_type`: 17 values (11 layer-level from state-machine §10 + 6 node-scoped from api-sse §5.3.5)

**New tables** (FK-safe order): `course_nodes`, `course_drafts`, `draft_nodes`, `node_chats`

**Indexes**: 4 on `course_nodes`, 2 on `node_chats`, 2 partial-unique on `agent_token_usage`

**ALTERs**: `courses` (tree_mode, current_layer), `agent_runs` (relax course_id NOT NULL, add draft_id, check), `agent_token_usage` (relax student_id/course_id, add draft_id, check, replace unique constraint), `course_assignments` (add draft_id)

**Triggers**: `course_nodes_parent_course_check` + `draft_nodes_parent_draft_check` (Lead D2)

**RLS**: ENABLE + FORCE on all 4 new tables; NULLIF guard on every user_id comparison; all policies wrapped in duplicate_object guard

**Config**: `enable_tree_mode_courses = 'false'` in `system_config`

## D9 Introspection Result

The existing unique constraint on `agent_token_usage` is named **`uq_token_usage_student_course`** — an inline `CONSTRAINT` (not a standalone index) defined in `003_course.sql`. The migration drops it by name with `EXCEPTION WHEN undefined_object` guard. The design docs erroneously assumed an index name `agent_token_usage_student_course_idx`; the actual name was verified from source.

## DB Application Status

No Postgres instance was reachable during implementation. The acceptance gate must verify:
- Apply migrations 001→022 on a fresh DB (no errors)
- Re-run 022 (idempotency — no errors, no duplicates)

---

## Rework — Systems Engineer defects applied (2026-06-13)

Three defects identified by Systems Engineer gate were applied to
`migrations/022_tree_generation.sql`. All changes are idempotent.

### Fix 1 — Missing server SELECT policy on `course_nodes`

Added `course_nodes_server_select_policy` FOR SELECT before the GRANT line in §3.8.
Without this policy, FORCE RLS caused the server role to see zero rows on SELECT
even after a successful INSERT, deadlocking the LayeredRunner pipeline.

### Fix 2 — Missing index on `agent_runs(draft_id)`

Added `agent_runs_draft_id_idx` partial index (WHERE draft_id IS NOT NULL)
immediately after the `agent_runs_context_check` DO block in §3.6.

### Fix 3 — Missing indexes on `draft_nodes`

Added three indexes after the `draft_nodes` table definition in §3.4:
- `draft_nodes_draft_id_idx` — full index on draft_id
- `draft_nodes_parent_id_idx` — partial index on (parent_id, ordering ASC)
- `draft_nodes_sibling_order_idx` — partial unique index on (draft_id, parent_id, ordering)

### Live Verification Results (PG16 via docker-compose.test.yml)

**Fresh apply (001→022):** All 22 migrations applied without error in sequence.

**Idempotency (022 re-run):** Exit 0; all existing objects skipped with NOTICE;
no errors. Confirmed clean no-op.

**Fix 1 (server SELECT):** Under `SET LOCAL "app.current_role" = 'server'` +
`SET LOCAL ROLE valory_app` (FORCE RLS active), `SELECT count(*) FROM course_nodes`
returned `server_sees_rows = 1` after inserting one server-owned root node.

**Fix 2 + Fix 3 (indexes):** All four new indexes confirmed present in `pg_indexes`:
- `agent_runs_draft_id_idx` ON agent_runs (draft_id) WHERE draft_id IS NOT NULL
- `draft_nodes_draft_id_idx` ON draft_nodes (draft_id)
- `draft_nodes_parent_id_idx` ON draft_nodes (parent_id, ordering ASC) WHERE parent_id IS NOT NULL
- `draft_nodes_sibling_order_idx` UNIQUE ON draft_nodes (draft_id, parent_id, ordering) WHERE parent_id IS NOT NULL

**`course_nodes_server_select_policy` in pg_policies:** Confirmed present with
cmd=SELECT and correct USING expression.

---

## D10 Hardening — course_nodes_student_policy read-only fix (2026-06-13)

### Defect

`course_nodes_student_policy` was declared without a `FOR` clause (defaulting to
`FOR ALL`) and included a `WITH CHECK` clause, allowing a student-role connection
to INSERT/UPDATE rows on its own courses. This contradicted data-model TDD §6
point 3 ("Students may not insert or update course_nodes directly") and the
system-wide precedent that students are read-only on syllabi/homework/lesson_content.

### Fix applied to `migrations/022_tree_generation.sql` (§3.8)

Two changes immediately before the DO block for `course_nodes_student_policy`:

1. Added `DROP POLICY IF EXISTS course_nodes_student_policy ON course_nodes;`
   so a re-run over an already-migrated DB replaces the defective `FOR ALL`
   policy with the corrected `FOR SELECT` policy.

2. Changed `CREATE POLICY course_nodes_student_policy`:
   - Added `FOR SELECT` (was defaulting to `FOR ALL`)
   - Removed the `WITH CHECK (...)` clause (a `FOR SELECT` policy has no WITH CHECK)
   - `USING (...)` clause with NULLIF guard is unchanged

All other policies (server_select, server_policy/INSERT, server_update_policy,
admin_policy) and all other tables (node_chats, course_drafts, draft_nodes) are
unchanged.

### Live Verification Results (PG16 via docker-compose.test.yml, valory_test_d10)

**Fresh apply (001→022):** All 22 migrations applied without error. The
`DROP POLICY IF EXISTS` emitted a NOTICE ("does not exist, skipping") and
the DO block created the new FOR SELECT policy cleanly.

**TEST 1 PASS:** Student A SELECT own course = 1 row (read still works).

**TEST 2 PASS:** Student A INSERT on own course was DENIED (the fix — was
allowed before D10).

**TEST 3 PASS:** Student A UPDATE on own course was DENIED (RLS filtered
out all target rows → 0 rows updated).

**TEST 4 PASS:** Server role INSERT/SELECT/UPDATE all work correctly (2 rows
visible after server INSERT).

**TEST 5 PASS:** Student B sees 0 nodes from student A's course (cross-tenant
isolation intact).

**TEST 6 PASS:** Admin sees all 3 nodes across all courses.

**Idempotency (022 re-run over already-migrated DB):** Exit 0. `DROP POLICY IF
EXISTS` silently dropped the existing policy; DO block recreated it as `FOR SELECT`.
Clean no-op on all other objects.

**pg_policies after re-run:**
```
course_nodes_student_policy | SELECT | course_id IN (SELECT ... WHERE student_id = NULLIF(...)::uuid) | (empty with_check)
```
`cmd=SELECT`, `with_check` empty — confirms the fix is in effect.
