# Valory Frontend — E2E Traceability Test Plan

Sprint 12 — authored 2026-06-12

This document maps each user-facing journey to its governing requirements and
the e2e spec file (or planned spec) that verifies it.  Existing spec files are
those present in `frontend/e2e/` at Sprint 12.  Items marked **PLANNED** will be
added in the sprint shown; no code for those specs exists yet.

---

## Journey 1 — Authentication

| Behavior | Governing REQ IDs | E2E Spec |
|---|---|---|
| Admin login with valid credentials lands on `/admin/users` | REQ-FEAUTH-010, REQ-FEAUTH-100, REQ-FEAUTH-101, REQ-FEAUTH-102 | `auth.spec.ts` |
| Student login with valid credentials lands on `/courses` | REQ-FEAUTH-011, REQ-FEAUTH-110, REQ-FEAUTH-115 | `auth.spec.ts` |
| Wrong password shows "Invalid credentials" and stays on `/login` | REQ-FEAUTH-012, REQ-FEAUTH-119, REQ-FEAUTH-120 | `auth.spec.ts` |
| Admin logout redirects to `/login` | REQ-FEAUTH-019, REQ-FEAUTH-155 | `auth.spec.ts` |
| Student logout redirects to `/login` | REQ-FEAUTH-020, REQ-FEAUTH-156 | `auth.spec.ts` |
| Post-logout navigation to a protected route redirects to `/login` | REQ-FEAUTH-118 | `auth.spec.ts` |
| Session token stored in memory only (not localStorage/cookie) | REQ-FEAUTH-118 | `auth.spec.ts` |
| Session refresh keeps student logged in across SPA navigation | REQ-AUTH-005 | **PLANNED-Sprint13** `session-refresh.spec.ts` |

---

## Journey 2 — Consent

| Behavior | Governing REQ IDs | E2E Spec |
|---|---|---|
| Consent page renders version label as `<h1>` before student agrees | REQ-FEAUTH-034, REQ-FEAUTH-167 | `consent-statement.spec.ts` |
| Consent statement body is present and scrollable before acceptance | REQ-FEAUTH-167, REQ-FEAUTH-168 | `consent-statement.spec.ts` |
| Clicking "I agree" advances student past consent gate | REQ-FEAUTH-056, REQ-FEAUTH-057 | `consent-statement.spec.ts` |
| Student who has not consented is redirected to `/consent` on any protected route | REQ-FEAUTH-149 | `consent-statement.spec.ts` |

---

## Journey 3 — Course Creation and Intake

| Behavior | Governing REQ IDs | E2E Spec |
|---|---|---|
| Dashboard renders empty state with "New course" button for first-time student | REQ-FECOURSE-012, REQ-FECOURSE-112 | `student-course.spec.ts` |
| Clicking "New course" opens the create-course modal | REQ-FECOURSE-009, REQ-FECOURSE-113 | `student-course.spec.ts` |
| Submit button is disabled while topic input is empty | REQ-FECOURSE-901 | `student-course.spec.ts` |
| Submitting a topic navigates to `/courses/<uuid>/intake` | REQ-FECOURSE-096, REQ-FECOURSE-922 | `student-course.spec.ts` |
| 409 on create shows "You already have an active course" message | REQ-FECOURSE-093, REQ-FECOURSE-930 | `student-course.spec.ts` |
| Chat input and send button are visible within the viewport on intake load | REQ-FECOURSE-027, REQ-FECOURSE-270 | `intake-chat.spec.ts` |
| Chat history endpoint returns `200` and `messages` array (never null) | REQ-FECOURSE-026, REQ-FECOURSE-260 | `intake-chat.spec.ts` |
| Unauthenticated chat history request returns `401` | REQ-AGENT-017 | `intake-chat.spec.ts` |
| "Preparing your course" indicator visible while history is empty on intake load | REQ-FECOURSE-028 | `processing-indicators.spec.ts` — `IntakePreparingIndicator_VisibleWhileHistoryEmpty` |
| Chat send error banner shown, optimistic bubble retained, input re-enabled after failure | REQ-FECOURSE-221, REQ-FECOURSE-223, REQ-FECOURSE-225 | `processing-indicators.spec.ts` — `IntakeSendFailure_ErrorBannerShownAndDismissible` |
| Bounded-wait hint shown after 120 s of empty polling (intake) | REQ-FECOURSE-263 | Vitest unit test: `src/views/IntakeChatView.test.ts` — "shows bounded-wait hint after 120 seconds with no messages" |
| Full intake conversation → syllabus generated and rendered (approve NOT clicked — cost guard) | REQ-FECOURSE-024, REQ-FECOURSE-031, REQ-AGENT-019, REQ-AGENT-020 | `journey/intake-to-syllabus.spec.ts` › IntakeToSyllabus_FullJourney_PreparingThenChatThenSyllabusRendered (AI journey tier, DELIVERED Sprint 12) |
| SSE reconnect retries with exponential backoff on connection drop | REQ-FECOURSE-025, REQ-FECOURSE-250, REQ-FECOURSE-251 | **PLANNED-Sprint14** `sse-reconnect.spec.ts` |

---

## Journey 4 — Syllabus

| Behavior | Governing REQ IDs | E2E Spec |
|---|---|---|
| Syllabus draft view renders with "Approve" and "Request modification" buttons | REQ-FECOURSE-031, REQ-FECOURSE-032 | `student-course.spec.ts` |
| Syllabus loading state is shown while content is being fetched | REQ-FECOURSE-033, REQ-FECOURSE-301 | `student-course.spec.ts` |
| Approving syllabus navigates to due-date schedule view | REQ-FECOURSE-031, REQ-FECOURSE-312 | **PLANNED** — blocked: withdrawal does not cancel content generation, so e2e approval is cost-unbounded (see Sprint_12.md backlog) |
| Requesting modification submits notes and reloads syllabus | REQ-FECOURSE-032, REQ-FECOURSE-323, REQ-FECOURSE-324 | **PLANNED** — AI-gated (modification triggers a regeneration call) |
| Drafting indicator shown; frontend polls and auto-renders when syllabus becomes available | REQ-FECOURSE-056, REQ-FECOURSE-057, REQ-FECOURSE-490, REQ-FECOURSE-491 | `processing-indicators.spec.ts` — `SyllabusDrafting_IndicatorThenAutoRender` |
| Error banner shown (not drafting indicator) on non-404 fetch failure | REQ-FECOURSE-490, REQ-FECOURSE-590 | `processing-indicators.spec.ts` — `SyllabusFetchFailure_NonDraftErrorStillShown` |
| Bounded-wait exceeded after 180 s of drafting polling — shows "Try Again" | REQ-FECOURSE-491 | Vitest unit test: `src/views/SyllabusView.test.ts` — "shows longer-than-expected message after 180s of drafting polling" |
| AsciiDoc / Markdown rendering in syllabus body displays formatted content | REQ-FECONTENT-013 | **PLANNED-Sprint13** `content-rendering.spec.ts` |
| LaTeX expressions in section content are rendered as math | — | **PLANNED-Sprint13** `content-rendering.spec.ts` |
| Images embedded in section content are displayed | — | **PLANNED-Sprint15** `content-rendering.spec.ts` |

---

## Journey 5 — Admin Configuration

| Behavior | Governing REQ IDs | E2E Spec |
|---|---|---|
| All 13 config fields render with non-empty values on load | REQ-FEADMIN-040, REQ-FEADMIN-300–312 | `admin-config.spec.ts` |
| Changing `agent_retry_limit` and saving persists after page reload | REQ-FEADMIN-041, REQ-FEADMIN-320, REQ-FEADMIN-324 | `admin-config.spec.ts` |
| Unsaved changes indicator appears on first edit | REQ-FEADMIN-042, REQ-FEADMIN-323 | `admin-config.spec.ts` |
| Invalid weight sum triggers client-side validation error before submit | REQ-FEADMIN-043, REQ-FEADMIN-330 | `admin-config.spec.ts` |
| Server validation error displays per-field | REQ-FEADMIN-045, REQ-FEADMIN-325 | `admin-config.spec.ts` |
| Admin enters AI API key via config form | REQ-FEADMIN-040 | **PLANNED-Sprint14** `admin-api-key.spec.ts` |
| Config field explanatory labels describe each setting | REQ-FEADMIN-005 | **PLANNED-Sprint14** `admin-api-key.spec.ts` |
| Image upload for course content is accepted and stored | REQ-SUBMISSION-001 | **PLANNED-Sprint15** `image-upload.spec.ts` |

---

## Journey 6 — Admin User Management

| Behavior | Governing REQ IDs | E2E Spec |
|---|---|---|
| User list renders with username, role, status, email, created columns | REQ-FEADMIN-020, REQ-FEADMIN-101, REQ-FEADMIN-102, REQ-FEADMIN-103, REQ-FEADMIN-150 | `admin-users.spec.ts` |
| Create student account — row appears in table | REQ-FEADMIN-021, REQ-FEADMIN-113–116 | `admin-users.spec.ts` |
| Deactivate button changes status badge to "Inactive" | REQ-FEADMIN-023, REQ-FEADMIN-130–132 | `admin-users.spec.ts` |
| Delete student removes row from table | REQ-FEADMIN-025, REQ-FEADMIN-140–142 | `admin-users.spec.ts` |
| Error banner dismiss button hides the banner | REQ-FEADMIN-160 | `admin-users.spec.ts` |
| Student role restriction: Delete button absent for admin users | REQ-FEADMIN-026 | `admin-users.spec.ts` |

---

## Journey 7 — Getting Started

| Behavior | Governing REQ IDs | E2E Spec |
|---|---|---|
| Student `/getting-started` renders 7 steps inside StudentLayout | REQ-FEONBOARD-001, REQ-FEONBOARD-002, REQ-FEONBOARD-010–016 | `getting-started.spec.ts` |
| Admin `/admin/getting-started` renders 4 steps inside AdminLayout | REQ-FEONBOARD-003, REQ-FEONBOARD-004, REQ-FEONBOARD-020–023 | `getting-started.spec.ts` |
| Student steps contain no admin-only content | REQ-FEONBOARD-002 | `getting-started.spec.ts` |

---

## Sprint 12 — Specs Added

| File | Tests |
|---|---|
| `frontend/e2e/processing-indicators.spec.ts` | `IntakePreparingIndicator_VisibleWhileHistoryEmpty`, `IntakeSendFailure_ErrorBannerShownAndDismissible`, `SyllabusDrafting_IndicatorThenAutoRender`, `SyllabusFetchFailure_NonDraftErrorStillShown` |
| `frontend/e2e/seed-smoke.spec.ts` | `SeedSmoke_SeededAssistantMessageRendersOnIntakePage` (also proves the seed.ts helpers) |
| `frontend/e2e/journey/intake-to-syllabus.spec.ts` | `IntakeToSyllabus_FullJourney_PreparingThenChatThenSyllabusRendered` (paid journey tier) |

---

## Planned Spec Summary

| Planned File | Target Sprint | Journeys Covered |
|---|---|---|
| `frontend/e2e/session-refresh.spec.ts` | Sprint 13 | Session persistence across SPA navigation |
| `frontend/e2e/content-rendering.spec.ts` | Sprint 13/15 | Markdown, LaTeX, image rendering in sections |
| `frontend/e2e/sse-reconnect.spec.ts` | Sprint 14 | SSE reconnect / exponential backoff |
| `frontend/e2e/admin-api-key.spec.ts` | Sprint 14 | Admin API key entry, config explanations |
| `frontend/e2e/image-upload.spec.ts` | Sprint 15 | Image upload for course content |

---

## Notes

- Specs listed as existing are confirmed present in `frontend/e2e/` at Sprint 12.
- REQ IDs in the "Governing REQ IDs" column point to `REQ-FE-COURSE.json`,
  `REQ-FE-CONTENT.json`, `REQ-FE-ADMIN.json`, and `requirements/l2-requirements.json`.
- The session-refresh and SSE-reconnect behaviors require live-server e2e fixtures
  (WebSocket proxy or network-intercept) and are deferred to Sprint 13–14
  infrastructure work.
- Content-rendering specs for LaTeX and images depend on Sprint 15 backend
  support for image storage and LaTeX pre-rendering.
