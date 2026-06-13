# Sprints 25–30 Plan — Admin Oversight & Interactive Course Generation (June 2026)

Status: **Decisions resolved (2026-06-13) — execution not yet started.** Authored by
Software Lead. No contributor agents have been spawned. Four scoping decisions are
resolved (see "Resolved decisions" at the bottom); the plan now reflects them. Work
begins only on an explicit go.

## PM request (5 items)

1. Deleting a user fails with HTTP 500.
2. Course Oversight shows the student **ID**, not the student **name** (unreadable).
3. When assigning a course, an admin cannot see the syllabus or interact with the
   Chair agent — admins want to add context / co-develop a custom course.
4. As an admin, I want to view a student's homework assignments and course material.
5. Re-architect course generation as an **interactive knowledge tree (DAG)** with a
   human in the loop at each level. Grow from root (course intent) → syllabus →
   per-section goals → concepts → content. Both admins and students should interact
   with the Chair at each step.

## Current-state audit (what exists today)

| Item | Existing groundwork | Gap / root cause |
|---|---|---|
| 1 | `Service.DeleteStudent` ([service.go:245](../internal/user/service.go#L245)) terminates agent work, then `Repository.DeleteStudent` ([repository.go:263](../internal/user/repository.go#L263)) deletes submissions → grades → chat_messages → courses → sessions → users in one tx. | `agent_runs.course_id` is **`ON DELETE RESTRICT`** ([004_agent.sql:63](../migrations/004_agent.sql#L63)); `courses.student_id` is also RESTRICT ([003_course.sql:30](../migrations/003_course.sql#L30)). The repo never deletes `agent_runs`, so `DELETE FROM courses` violates the FK for **any student who ever had a generation run** → error propagates → 500. Tests missed it because no fixture seeds an `agent_runs` row before deletion. |
| 2 | `CourseOversightView.vue` renders `course.student_id` ([CourseOversightView.vue:130](../frontend/src/views/admin/CourseOversightView.vue#L130)); fed by `GET /api/v1/courses` admin path → `CourseRepository.ListCourses` ([repository.go:167](../internal/course/repository.go#L167)). | `CourseRow` carries `student_id` only — no username/email. The admin list query does not join `users`. |
| 3 | Assignment creates a course and generates the syllabus **synchronously from params** with no preview/iteration: `AssignStudents` → `assignOneStudent` → `chair.GenerateSyllabusFromParams` ([assignment_service.go:124-165](../internal/admin/assignment_service.go#L124)). Chair chat exists but is **student-scoped**, mounted under `/courses/{id}` ([main.go:446-448](../cmd/server/main.go#L446)). | No admin-facing Chair interaction, no syllabus preview, no iterate-before-finalize. This is the admin slice of item 5. |
| 4 | Admin RLS policy lets admins read all `courses` ([003_course.sql:62](../migrations/003_course.sql#L62)). Content/sections/homework/submissions handlers are mounted student-scoped under `/courses/{id}` ([main.go:450-468](../cmd/server/main.go#L450)). | Need to verify which child tables (lesson_content, homework, submissions, grades, syllabi) honor an admin RLS path, and expose read-only admin oversight endpoints + a frontend course-detail browser. |
| 5 | Flat 2-level model: `syllabi` (one AsciiDoc blob) → `homework` sections → `lesson_content`. Runner generates **every section in one shot** from the whole syllabus blob, then a professor↔reviewer correction loop: `generateAllSections` ([runner.go:397](../internal/agent/runner.go#L397)), `runReviewLoop` ([runner.go:467](../internal/agent/runner.go#L467)). HITL today = post-hoc `section_feedback` keyword polling ([runner.go:246](../internal/agent/runner.go#L246)). | No knowledge-tree data model, no layered generation, no per-level HITL checkpoint/approval, no node-level Chair interaction, no interactive generation UI. Largest item — needs design before code. |

## Program shape

Two phases, **delivered as a single combined release** (PM decision 1 — no
standalone Phase A cut). **Phase A (Sprints 25–26)** clears items 1, 2, 4 and the
read-only half of oversight — small and independent. **Phase B (Sprints 27–30)** is
the interactive knowledge-tree epic (item 5), which subsumes item 3 (admin Chair
interaction is just the admin entry point into the same interactive flow). Phase B
opens with a **design + requirements contract sprint** because CLAUDE.md requires a
design contract before a non-trivial feature, and the tree touches the data model,
the agent orchestrator, the API surface, and both frontends.

```
S25  Bug-fix & oversight quick wins        (items 1, 2)        ── shippable
S26  Admin course-material visibility       (item 4)            ── shippable
        |
S27  DESIGN + REQUIREMENTS contract (item 5 + 3)  ── gated by Senior SQE on the design
        v
S28  Knowledge-tree backend foundation      (schema, repo, state machine)
        v
S29  Layered orchestrator + Chair node interaction + HITL API   (delivers item 3)
        v
S30  Interactive tree frontend (admin + student)                (delivers item 5 UX)
```

Sequencing rule honored: S28→S29→S30 are strictly dependent and run in series.
Within each sprint, independent increments run in parallel.

---

## Sprint 25 — Delete fix + Oversight names

Two independent tracks; A is backend-only, B is split BE/FE per the
"split backend and frontend" rule.

| # | Increment | Contributor | Verifier |
|---|---|---|---|
| 25.A1 | Fix `Repository.DeleteStudent`: delete `agent_runs` (and any other RESTRICT-linked child of the student's courses) before `courses`, inside the existing tx; keep RLS/role correctness (memory: test DB role masks RLS — verify under `SET ROLE valory_app`). | senior-engineer | SQE + Systems Engineer |
| 25.A2 | Regression test that seeds a course **with an `agent_runs` row + pipeline_events** then deletes the student and asserts 200 + full cascade; extend the existing lifecycle integration test. | test-author | SQE |
| 25.B1 | Backend: admin path of `ListCourses` returns `student_username` (+ email) via a `users` join; keep student path unchanged; update `CourseRow`/response shape. | junior-engineer | SQE + Systems Engineer |
| 25.B2 | Frontend: `CourseOversightView` shows student name (ID as secondary/tooltip); update column header + types + component test. | junior-engineer | SQE |

Dependencies: 25.A2 depends on 25.A1; 25.B2 depends on 25.B1. A and B tracks are
independent of each other.

Requirements: item 1 is governed by existing **REQ-USER-007** (deletion of personal
data) — requirements-author adds an acceptance criterion that deletion succeeds for
a student with completed generation runs. Item 2 → new **REQ-FEADMIN-7xx** + a
backend criterion on the course-list admin projection.

---

## Sprint 26 — Admin course-material visibility (item 4)

| # | Increment | Contributor | Verifier |
|---|---|---|---|
| 26.1 | Requirements: admin read-only oversight of a course's syllabus, sections, lesson content, homework, submissions, grades. | requirements-author | Senior SQE |
| 26.2 | Backend: confirm/repair admin RLS read path on `syllabi`, `homework`, `lesson_content`, `submissions`, `grades`; expose admin oversight read endpoints (e.g. `GET /admin/courses/{id}/overview`) — **read-only**, no mutation. | senior-engineer | SQE + Systems Engineer |
| 26.3 | Backend tests: admin can read any student's material; student still cannot read another student's (RLS isolation preserved). | test-author | SQE |
| 26.4 | Frontend: admin course-detail view reachable from Oversight — browse syllabus, section content, homework + rubric, and submission/grade status. | senior-engineer | SQE + Systems Engineer |

Dependencies: 26.2 depends on 26.1; 26.3 & 26.4 depend on 26.2. Independent of S25.
**Assumption:** scope is read-only oversight; admin editing/regenerating a student's
content is deferred to Phase B's interactive flow. (Confirm with PM.)

---

## Sprint 27 — DESIGN + REQUIREMENTS contract for interactive knowledge tree (items 5 + 3)

No production code. Output is the contract every later sprint builds against; the
**Senior SQE gate reviews the design itself** before any implementation is authorized.

| # | Increment | Contributor | Verifier |
|---|---|---|---|
| 27.1 | TDD: knowledge-tree **DAG data model** (`course_nodes`: id, course_id, parent_id, node_type ∈ {root, syllabus, section_goal, concept, content}, ordering, status, payload; coexistence with legacy `syllabi`/`homework`/`lesson_content`); migration strategy. | design-author | Senior SQE |
| 27.2 | TDD: **layered generation state machine** + per-level **HITL checkpoint protocol** (generate-layer → pause → human approve/feedback/regenerate → expand next layer), and how it replaces/wraps the current flat `generateAllSections` loop. | design-author | Senior SQE |
| 27.3 | API + SSE spec: node-level Chair interaction (chat/refine per node), approve/feedback/regenerate endpoints, node-scoped event stream; **admin-authoring vs. student-building** entry points (item 3 = admin entry). | design-author | Senior SQE + Systems Engineer (security/RLS) |
| 27.4 | L1 + L2 requirements derived from the TDDs (new `course_nodes`/orchestration module reqs; legacy L1 stays in `requirements/l1-requirements.json`). | requirements-author | Senior SQE |

Dependencies: 27.4 depends on 27.1–27.3. **This sprint is itself a PM/Senior-SQE
decision gate** — Phase B sprints 28–30 below are provisional until the design lands
and may be re-scoped.

---

## Sprint 28 — Knowledge-tree backend foundation (provisional, pending S27)

| # | Increment | Contributor | Verifier |
|---|---|---|---|
| 28.1 | Migration: `course_nodes` table (+ RLS policies mirroring `courses`: student-owns / admin-all), node-status enum, indexes; idempotent per the 003/004 idiom. | senior-engineer | SQE + Systems Engineer |
| 28.2 | Tree repository: create/read/update nodes, fetch layer, fetch children, status transitions; request-scoped conn for RLS. | senior-engineer | SQE + Systems Engineer |
| 28.3 | DB integration tests for tree CRUD + RLS isolation (real Postgres, `-p 1`, `SET ROLE valory_app`). | test-author | SQE |

Dependencies: serial after S27. 28.2→28.1; 28.3→28.2.

---

## Sprint 29 — Layered orchestrator + Chair node interaction + HITL API (provisional)

Delivers **item 3** (admin can preview + chat with the Chair while authoring).

| # | Increment | Contributor | Verifier |
|---|---|---|---|
| 29.1 | Orchestrator: layer-by-layer generation that writes `course_nodes` and **pauses at each HITL checkpoint** instead of running to completion; reuses professor/reviewer per node. | senior-engineer | SQE + Systems Engineer |
| 29.2 | Chair: per-node generate + chat/refine (`GenerateNode`, `RefineNode`); generalize `GenerateSyllabusFromParams` into the node model. | senior-engineer | SQE + Systems Engineer |
| 29.3 | HITL API + SSE: approve / feedback / regenerate per node; node-scoped events; **admin-authoring endpoints** so an assignment can be co-developed before finalizing (item 3). | senior-engineer | SQE + Systems Engineer |
| 29.4 | Orchestrator + HITL integration tests (advance through layers, human approval, regenerate-on-feedback). | test-author | SQE |

Dependencies: serial after S28. 29.4 depends on 29.1–29.3.

---

## Sprint 30 — Interactive tree frontend, admin + student (provisional)

Delivers the **item 5 UX**.

| # | Increment | Contributor | Verifier |
|---|---|---|---|
| 30.1 | Interactive generation UI: tree/layer view, per-node approve / feedback / regenerate, live SSE; shared component. | senior-engineer | SQE + Systems Engineer |
| 30.2 | Student flow: build a course interactively with the Chair, level by level. | junior-engineer | SQE |
| 30.3 | Admin flow: author/assign a course interactively (item 3) reusing 30.1; preview syllabus + chat before assigning to students. | senior-engineer | SQE + Systems Engineer |
| 30.4 | Component/e2e tests for both flows (rebuild stack before any paid e2e — memory note). | test-author | SQE |

Dependencies: serial after S29. 30.2 & 30.3 depend on 30.1; 30.4 last.

---

## Cross-cutting assumptions & risks (for PM)

- **Backward compatibility (item 5):** existing flat `syllabi`/`homework`/`lesson_content`
  and in-flight courses must keep working. Plan: `course_nodes` coexists; legacy courses
  render via the current path. No piecemeal migration of legacy data (per CLAUDE.md).
- **Paid-API exposure:** interactive, multi-layer generation increases Anthropic calls
  and changes token-cap accounting (REQ-AGENT-011 token cap, `agent_token_usage`).
  Systems Engineer must review cost/throttle implications in S29.
- **RLS correctness:** every new table/endpoint must be tested under `SET ROLE valory_app`
  (the test DB superuser masks RLS bugs — memory note `force-rls-superuser-test-masking`).
- **Scope of item 4:** read-only oversight (decision 2); admin mutation deferred to the
  Phase B interactive flow.

## Resolved decisions (2026-06-13)

1. **Single combined release** — Phase A (S25–26) is *not* cut as a standalone release;
   all six sprints ship together. Note: S25–26 remain independent and can be built and
   reviewed early, but they are held for one delivery alongside Phase B.
2. **Item 4 is read-only** — admin browses student material; editing/regeneration is
   handled by the Phase B interactive flow, not Sprint 26.
3. **Design-contract-first for item 5** — Sprint 27 produces design + requirements only
   (no production code). S28–30 stay provisional until the design lands and may be re-scoped.
4. **Execution gated** — Software Lead spawns no contributor agents until an explicit go.
