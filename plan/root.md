# Root — Admin Oversight & Interactive Course Generation

> Level 0. The core ask the whole effort serves. Owned by `project-manager`.
> This is a **worked example** seeded from [sprints/Sprint_25-30_Plan.md](../sprints/Sprint_25-30_Plan.md)
> to make the tree concrete. Replace it when a new effort begins.

## The ask

Give Valory admins real oversight of student learning, and re-architect course generation as
an interactive, human-in-the-loop knowledge tree that both admins and students grow with the
Chair agent — from course intent down to content.

## Whole-effort acceptance

The effort is done when:

1. Deleting a user who has had generation runs succeeds (no HTTP 500).
2. Course Oversight shows student **names**, not raw IDs.
3. Admins can browse a student's syllabus, sections, content, homework, submissions, grades
   (read-only).
4. Admins can preview a syllabus and co-develop a course with the Chair before assigning it.
5. Course generation runs as an interactive knowledge tree (DAG) with a HITL checkpoint at
   each level, usable by both admins and students.
6. Every shipped change passes the acceptance facet (SQE + Systems Engineer → Senior SQE) and
   preserves backward compatibility with existing flat courses.

## Goals (level 1)

| Goal | Outcome | File |
|------|---------|------|
| G1 | Phase A — bug-fix & admin oversight quick wins (asks 1, 2, 3-read, 4-read) | [goals/G1.md](goals/G1.md) |
| G2 | Phase B — interactive knowledge-tree epic (asks 4-author, 5) | [goals/G2.md](goals/G2.md) |

Delivered as a **single combined release** (PM decision): G1 may be built and reviewed early
but is held for one delivery alongside G2.

## Execution gate

The orchestrator dispatches no contributor workers until an explicit go. The current frontier
is **G1-S1 (Sprint 25)**; see [state.json](state.json).
