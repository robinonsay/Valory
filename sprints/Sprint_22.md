# Sprint 22 — Admin Course Creation & Assignment

## Objective

PM item 3: admins create courses and assign them to students. Per resolved
decision 4, the PER-STUDENT COURSE-INSTANCE model: an admin defines an
assignment (topic, level, parameters) and assigns students; the system
generates one personalized, RLS-isolated course instance per assigned student.
Design: docs/sdd/SDD-022-admin-course-assignment.md.

## Work performed

| Task | Deliverable | Contributor | Verifier | Outcome |
|---|---|---|---|---|
| 22.1 | SDD-022: course_assignments + courses.assignment_id nullable FK (ON DELETE SET NULL); assigned courses enter at status syllabus_approved so the EXISTING pollAndGenerate poller picks them up (verified: courses_admin_policy already exists, courses_student_policy already isolates by student_id → zero RLS schema change, zero runner change); partial-success batch; unassign-before-generating; Sprint-23 learning-profile seam | design-author | SQE + systems | PASS |
| 22.2 | 19 reqs: REQ-ASSIGN-001..011 (new internal/admin/requirements/ module), REQ-FEADMIN-702..706, REQ-FECOURSE-601..603, REQ-SYS-067/068 | requirements-author | SQE + schema | PASS |
| 22.3 | migration 019; internal/admin assignment repo/service/handler (create; batch assign 20-cap + partial-success 200 {created,errors}; per-student course at syllabus_approved with Chair-generated syllabus; compensating DELETE on syllabus failure → no orphan rows; unassign 409-when-generating); chair.go GenerateSyllabusFromParams; course assignment_id additive | senior-engineer (+1 fix round) | systems (RLS focus) + SQE | PASS (round 2) |
| 22.4 | AdminAssignmentsView + AdminAssignmentDetailView (list/create/assign/unassign, partial-success + 409 display), admin nav + routes, syllabus_approved→GeneratingView routing; 528 vitest | junior-engineer | SQE + e2e | PASS |
| 22.5 | 28 integration tests (DB + HTTP-level RLS isolation probes) ran LIVE green; admin-assignment.spec.ts (skip-gated, AI-free + live-AI tiers) | test-author | senior SQE + live runs | PASS |

## Review pipeline results

- **SQE gate: PASS** with non-blocking nits (3 req-wording: REQ-ASSIGN-005
  HOW-language, REQ-FECOURSE-603 mechanism-language, REQ-SYS-067 compound;
  gofmt on a test file; dead test code — all lead-folded).
- **Systems gate: FAIL — 1 BLOCKING (the catch of the sprint):**
  AssignmentRepository used the plain pool (whose AfterRelease hook clears the
  RLS GUCs) for courses-table operations, but `courses` has FORCE ROW LEVEL
  SECURITY. Under production's non-superuser `valory_app` role, every assigned-
  course INSERT/SELECT/DELETE would have been REJECTED by RLS — the feature
  would have been dead on arrival in production. **All 28 integration tests
  masked it because the test DB role (valory_test) is a SUPERUSER that
  bypasses FORCE RLS.** Systems otherwise confirmed (all PASS): migration 019
  additive/idempotent; FK ON DELETE SET NULL (not CASCADE — a CASCADE would
  have destroyed student content on assignment deletion); no transaction held
  across the per-student Anthropic syllabus calls; 20-cap server-side;
  partial-success orphan-prevention; duplicate-assign guarded by the existing
  courses_single_active_idx.
- **Fix round:** AssignmentRepository gained conn(ctx) (request-scoped admin
  connection, app.current_role='admin' from session → courses_admin_policy
  admits it); the 5 courses-touching methods route through it; non-RLS tables
  stay on the pool. Two NEW tests run under `SET ROLE valory_app`
  (non-superuser): admin-conn CreateAssignedCourse SUCCEEDS; bare-GUC INSERT
  BLOCKED — closing the superuser-masking gap permanently. Req-wording/gofmt/
  dead-code nits folded.
- **Systems delta: PASS** — confirmed the blocked-case test would have FAILED
  against the pre-fix code (definitive regression guard); scanned for sibling
  latent instances — none.
- **Senior SQE: SHIP** — independently re-ran the live integration tier AND a
  manual SQL probe: under valory_app with empty GUCs `INSERT INTO courses` →
  "new row violates row-level security policy" (the exact production failure);
  with app.current_role='admin' it succeeds. The data-isolation guarantee is
  now genuinely real.

## Verification

- go build/vet clean; `go test -tags testing ./...` green; vitest 528/528;
  vue-tsc clean; gofmt clean; 19 reqs schema-valid.
- Integration tier ran LIVE (senior gate re-ran independently): full
  assign→syllabus→syllabus_approved journey; per-student isolation at BOTH the
  DB-connection level and the HTTP-session level (student B's real bearer token
  cannot GET student A's assigned course → 404); admin sees all; partial-
  success (valid + invalid student → valid created, invalid reported, batch not
  aborted); unassign 409 once generating; the two new non-superuser RLS tests.

## Backlog / conditions

- **(Design — track for Sprint 23)** Admin-triggered syllabus generation
  charges the STUDENT's per-student agent_token_usage budget (the correct
  consequence of the per-student-instance model, documented). A large batch
  could silently consume students' budgets. Consider charging assignment-time
  generation to an admin/system budget or surfacing consumption to the admin.
- **(Doc)** Some deliverable notes said "207 Multi-Status" for partial-success;
  the handler actually returns 200 with {created, errors} and the frontend
  reads the body fields regardless of status — no runtime issue, wording only.
- **(Standing)** Quarantine: professor_max_tokens (and the broader multi-sprint
  uncommitted working tree) untouched by assignment code; GenerateSyllabusFromParams
  uses its own fixed token literal. Stage Sprint paths explicitly at commit.

## Lesson recorded

FORCE RLS + a superuser test role is a silent-masking trap: any new repository
touching an RLS-protected table MUST use the request-scoped connection and MUST
be tested under a non-superuser role (SET ROLE valory_app), or production-only
RLS failures pass review. The two new non-superuser tests are now the template.
