# Valory Frontend — E2E Traceability Test Plan

Sprint 13 — updated 2026-06-12

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
| Session token NOT in localStorage or sessionStorage | REQ-FEAUTH-171 | `session-refresh.spec.ts` — `NoTokenInBrowserStorage` |
| Session persists across page.reload() — cookie restores auth state | REQ-AUTH-009, REQ-AUTH-011, REQ-FEAUTH-169, REQ-FEAUTH-170 | `session-refresh.spec.ts` — `SessionPersists_AcrossPageReload` |
| Session persists across hard page.goto() navigation (impossible pre-Sprint-13) | REQ-AUTH-009, REQ-AUTH-011, REQ-FEAUTH-169, REQ-FEAUTH-170 | `session-refresh.spec.ts` — `SessionPersists_HardNavigation` |
| Logout clears server-side session; page.reload() at /login stays at /login | REQ-AUTH-010 | `session-refresh.spec.ts` — `Logout_ClearsSession` |
| Consent interstitial skipped when session restore returns consented=true | REQ-FEAUTH-172 | `session-refresh.spec.ts` — `ConsentSkipped_WhenAlreadyConsented` |

| Login page shows Valory logo (`img[alt="Valory"]` with src containing valory.svg) | REQ-FEAUTH-100, REQ-FEAUTH-101, REQ-FEAUTH-102 (closest; no formal branding REQ) | `branding.spec.ts` — `LoginPage_ShowsValoryLogo_AndCorrectTitle` |
| Document title is "Valory — AI Professor" on login page | (no formal branding REQ) | `branding.spec.ts` — `LoginPage_ShowsValoryLogo_AndCorrectTitle` |
| Student nav header shows Valory logo after login | REQ-FEAUTH-118 (closest; no formal branding REQ) | `branding.spec.ts` — `StudentNav_ShowsValoryLogoAfterLogin` |
| Admin sidebar shows Valory logo after login | REQ-FEAUTH-118 (closest; no formal branding REQ) | `branding.spec.ts` — `AdminSidebar_ShowsValoryLogoAfterLogin` |

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
| Agent chat messages render Markdown (heading, bold, list, code block) | REQ-FECOURSE-612 | `content-rendering.spec.ts` — `ChatMarkdown_RendersFormattedAgentMessage` (DELIVERED Sprint 14) |
| Agent chat messages render LaTeX math (inline and display) via KaTeX | REQ-FECOURSE-613 | `content-rendering.spec.ts` — `ChatLaTeX_RendersTypesetMath` (DELIVERED Sprint 14) |
| Agent chat messages render images with loading="lazy" | REQ-FECOURSE-614 | `content-rendering.spec.ts` — `ChatImage_RendersImgTag` (DELIVERED Sprint 14) |
| XSS payloads (script, onerror, javascript: href) in agent messages are neutralised | REQ-FECOURSE-615 | `content-rendering.spec.ts` — `ChatXSS_ScriptAndEventHandlersInert` (DELIVERED Sprint 14) |
| Student messages render as plain text — HTML and LaTeX are inert | REQ-FECOURSE-616 | `content-rendering.spec.ts` — `ChatStudentMessage_PlainTextNoHTML` (DELIVERED Sprint 14) |
| AsciiDoc / Markdown rendering in syllabus body displays formatted content | REQ-FECONTENT-013 | **PLANNED-Sprint15** `content-rendering.spec.ts` (section reader surface) |

---

## Journey 5 — Admin Configuration

| Behavior | Governing REQ IDs | E2E Spec |
|---|---|---|
| All 13 config fields render with non-empty values on load | REQ-FEADMIN-040, REQ-FEADMIN-300–312 | `admin-config.spec.ts` |
| Changing `agent_retry_limit` and saving persists after page reload | REQ-FEADMIN-041, REQ-FEADMIN-320, REQ-FEADMIN-324 | `admin-config.spec.ts` |
| Unsaved changes indicator appears on first edit | REQ-FEADMIN-042, REQ-FEADMIN-323 | `admin-config.spec.ts` |
| Invalid weight sum triggers client-side validation error before submit | REQ-FEADMIN-043, REQ-FEADMIN-330 | `admin-config.spec.ts` |
| Server validation error displays per-field | REQ-FEADMIN-045, REQ-FEADMIN-325 | `admin-config.spec.ts` |
| Admin enters AI API key via config form | REQ-FEADMIN-040 | `admin-api-key.spec.ts` — `AdminSecrets_SaveRoundTrip_NeverEchoed` (DELIVERED Sprint 16) |
| Config field explanatory labels describe each setting | REQ-FEADMIN-005 | `admin-api-key.spec.ts` — `AdminConfig_ExplanationsVisible` (DELIVERED Sprint 16) |
| API Keys section renders with per-key status badges and empty password inputs | REQ-FEADMIN-500, REQ-FEADMIN-501, REQ-FEADMIN-502 | `admin-api-key.spec.ts` — `AdminSecrets_SectionRendersWithStatuses` (DELIVERED Sprint 16) |
| Save updates badge to Configured (••••last4); full value never appears in page or GET response | REQ-FEADMIN-503, REQ-FEADMIN-504, REQ-ADMIN-007, REQ-SECURITY-008 | `admin-api-key.spec.ts` — `AdminSecrets_SaveRoundTrip_NeverEchoed` (DELIVERED Sprint 16) |
| Audit log secret.set/secret.clear entries for brave_api_key contain no plaintext secret value | REQ-SECURITY-008 | `admin-api-key.spec.ts` — `AdminSecrets_AuditRedaction` (DELIVERED Sprint 16) |
| "?" toggles expand explanations; audit_retention_days shows "No automated purge"; session_inactivity_seconds shows env-override disclosure | REQ-FEADMIN-510, REQ-FEADMIN-511, REQ-FEADMIN-512, REQ-FEADMIN-515 | `admin-api-key.spec.ts` — `AdminConfig_ExplanationsVisible` (DELIVERED Sprint 16) |
| Image upload for course content is accepted and stored | REQ-SUBMISSION-001, REQ-AGENT-024, REQ-AGENT-025 | `image-upload.spec.ts` — DELIVERED Sprint 15 |

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

## Sprint 13 — Specs Added / Updated

| File | Change | Tests |
|---|---|---|
| `frontend/e2e/session-refresh.spec.ts` | **NEW** | `SessionPersists_AcrossPageReload`, `SessionPersists_HardNavigation`, `ConsentSkipped_WhenAlreadyConsented`, `Logout_ClearsSession`, `NoTokenInBrowserStorage` |
| `frontend/e2e/branding.spec.ts` | **NEW** | `LoginPage_ShowsValoryLogo_AndCorrectTitle`, `StudentNav_ShowsValoryLogoAfterLogin`, `AdminSidebar_ShowsValoryLogoAfterLogin` |
| `frontend/e2e/auth.spec.ts` | **Updated** | Removed post-logout `page.goto()` redirect assertions (were testing memory-only token clearing; now superseded by session-refresh.spec.ts). Renamed logout tests to `AdminLogout_AfterLogin_RedirectsToLogin` / `StudentLogout_AfterLogin_RedirectsToLogin`. |
| `frontend/e2e/consent-statement.spec.ts` | **Updated** | `loginStopAtConsent` helper removed; test now logs in first then navigates directly to `/consent` (demo_student has consent recorded — session restore returns `consented:true` per REQ-FEAUTH-172, so the guard no longer forces /consent on login). |
| `frontend/e2e/getting-started.spec.ts` | **Updated** | `AdminGettingStarted` test: scoped nav link assertion to `.sidebar-nav` to avoid strict-mode collision with the new brand logo link in the sidebar (both point to `/admin/users`). |
| `frontend/e2e/intake-chat.spec.ts` | **Updated** | `IntakeChatHistory` test: unauthenticated check now uses the standalone `request` fixture instead of `page.request`; `page.request` shares the browser's cookie jar which includes the `__Host-session` cookie after login. |

---

## Sprint 14 — Specs Added / Updated

| File | Change | Tests |
|---|---|---|
| `frontend/e2e/content-rendering.spec.ts` | **NEW** | `ChatMarkdown_RendersFormattedAgentMessage`, `ChatLaTeX_RendersTypesetMath`, `ChatImage_RendersImgTag`, `ChatXSS_ScriptAndEventHandlersInert`, `ChatStudentMessage_PlainTextNoHTML` |

---

## Sprint 15 — Specs Added / Updated

| File | Change | Tests |
|---|---|---|
| `frontend/e2e/image-upload.spec.ts` | **NEW** | `ChatImageUpload_PreviewAndUploadRoundTrip`, `ChatImageValidation_WrongTypeRejected`, `ImageAccess_OwnerOnly`, `HomeworkImageAttach_ControlAndCountFeedback` |
| `frontend/e2e/seed.ts` | **Updated** | Added `seedHomework(courseId, title, sectionIndex?)` helper |

### image-upload.spec.ts — Detail

Proves the image upload, preview, access-control, and homework attachment features.
All 4 tests are in the AI-free tier (0 Anthropic API calls).

**NO-SEND / NO-SUBMIT COST RULE**: No chat message is ever sent (a chat POST
triggers a synchronous Claude call).  No submission is ever POSTed (submission
triggers the grading runner asynchronously on a background poller — posting even
once burns grading tokens).  Image upload alone (`POST /api/v1/courses/{id}/images`)
triggers no model call.

| Test | Governing REQ | What is asserted | Coverage type |
|---|---|---|---|
| `ChatImageUpload_PreviewAndUploadRoundTrip` | REQ-FECOURSE-622, REQ-FECOURSE-624, REQ-AGENT-024 | Attach PNG via `setInputFiles` → preview thumbnail (blob: src) renders; upload via `page.request` multipart → 201 `{image_id, url}`; GET image URL → 200 + `Content-Type: image/png` + `X-Content-Type-Options: nosniff`; remove preview (✕) → row disappears | E2E |
| `ChatImageValidation_WrongTypeRejected` | REQ-FECOURSE-623, REQ-AGENT-024 | `setInputFiles` with `text/plain` → `.image-error` visible, no upload network call fired (route interception); `page.request` POST with text payload → backend 422 `UNSUPPORTED_MIME` | E2E (client + server) |
| `ImageAccess_OwnerOnly` | REQ-AGENT-025 | Upload image as demo_student; fresh unauthenticated context GET image URL → 401 | E2E |
| `HomeworkImageAttach_ControlAndCountFeedback` | REQ-FECONTENT-220, REQ-FECONTENT-221, REQ-FECONTENT-222 | Seeds course + homework row; navigates to submission view; asserts homework title renders (GET /homework/{hwId} now returns 200); "Add Images" button visible and enabled; attach 2 PNGs → 2 thumbnails + "2 / 8 images attached"; attach 7 more (total would be 9) → count-limit error visible; cap is 8; button becomes disabled. No submission posted. | E2E (full) |

**Homework path — endpoint added in-sprint:**
`HomeworkSubmissionView.vue` calls `GET /api/v1/courses/{id}/homework/{hwId}` on mount.
Sprint 15 e2e initially found this endpoint missing (returned 404).  The backend
endpoint was implemented in-sprint (response: `{id, title, description, due_date?}`).
The test was updated from asserting the error-state workaround to asserting the full
working flow.

REQ-FECONTENT-220/221/222 are **fully** verified end-to-end:
- REQ-FECONTENT-220: homework title renders from GET response; Add Images button visible.
- REQ-FECONTENT-221: 2 PNG fixtures attached → 2 preview thumbnails rendered.
- REQ-FECONTENT-222: attaching 7 more when 2 already attached → cap error visible
  ("Maximum 8 images per submission."); total capped at 8; button disabled at cap.
- `src/utils/imageUpload.test.ts` unit-tests the `validateImage` function and the
  `MAX_HOMEWORK_IMAGES` constant — 17 unit tests, all passing.

**Seed helper added — `seedHomework(courseId, title, sectionIndex?)`:**
Uses `spawnSync` with `--tuples-only --no-align` psql flags to execute a CTE
(`WITH inserted AS (INSERT ... RETURNING id) SELECT id FROM inserted`) and parse
the returned UUID from stdout.  The homework table has no RLS so the postgres
superuser connection used by all seed helpers can insert directly.

### content-rendering.spec.ts — Detail

Proves the markdown→KaTeX→DOMPurify rendering pipeline in `IntakeChatView`.
All 5 tests are in the AI-free tier (0 Anthropic API calls).
2 courses are created per run (1 kickoff call silenced each = 2 total).

| Test | Governing REQ | What is asserted |
|---|---|---|
| `ChatMarkdown_RendersFormattedAgentMessage` | REQ-FECOURSE-612 | Agent bubble renders `h1`, `strong`, `ul li`, `pre code`; raw markdown syntax (`# `, `**`, `- `) absent from visible text |
| `ChatLaTeX_RendersTypesetMath` | REQ-FECOURSE-613 | `.katex` elements exist in the agent bubble; raw `$...$` and `$$...$$` delimiters absent from visible text |
| `ChatImage_RendersImgTag` | REQ-FECOURSE-614 | `img[alt="Valory logo"]` renders with `src="https://localhost/valory.svg"` and `loading="lazy"` |
| `ChatXSS_ScriptAndEventHandlersInert` | REQ-FECOURSE-615 | `window.__pwned` undefined (script/onerror/javascript: all inert); no `script` element in bubble; no `javascript:` href |
| `ChatStudentMessage_PlainTextNoHTML` | REQ-FECOURSE-616 | Student bubble contains literal `<b>not bold</b>` text; no `b`/`strong`/`.katex` DOM elements inside the bubble |

XSS coverage notes:
- `<script>window.__pwned=1</script>` — stripped by DOMPurify unconditionally
- `<img src=x onerror="window.__pwned=2">` — `onerror` stripped by `afterSanitizeAttributes` hook in `renderMarkdown.ts`
- `[click](javascript:window.__pwned=3)` — `javascript:` href removed by `afterSanitizeAttributes` hook

Image src note: `renderMarkdown.ts` strips any `img src` that is not `https?://` or
`data:image/`.  The image test uses `https://localhost/valory.svg` so the src
survives the hook.

---

## Planned Spec Summary

| Planned File | Target Sprint | Status | Journeys Covered |
|---|---|---|---|
| `frontend/e2e/session-refresh.spec.ts` | Sprint 13 | **DELIVERED Sprint 13** | Session persistence across SPA navigation (5 tests) |
| `frontend/e2e/branding.spec.ts` | Sprint 13 | **DELIVERED Sprint 13** | Valory logo on login/nav/sidebar; document title (3 tests) |
| `frontend/e2e/content-rendering.spec.ts` | Sprint 13/15 | **DELIVERED Sprint 14** | Markdown, LaTeX, image, XSS in agent chat bubbles; student plain-text (5 tests) |
| `frontend/e2e/sse-reconnect.spec.ts` | Sprint 14 | PLANNED | SSE reconnect / exponential backoff |
| `frontend/e2e/admin-api-key.spec.ts` | Sprint 14 | **DELIVERED Sprint 16** | Admin API key entry, config explanations, audit redaction, secret status badges |
| `frontend/e2e/image-upload.spec.ts` | Sprint 15 | **DELIVERED Sprint 15** | Image upload (chat preview, validation, owner-only access, homework attach) |

---

---

## Sprint 16 — Specs Added / Updated

| File | Change | Tests |
|---|---|---|
| `frontend/e2e/admin-api-key.spec.ts` | **NEW** | `AdminSecrets_SectionRendersWithStatuses`, `AdminSecrets_SaveRoundTrip_NeverEchoed`, `AdminSecrets_AuditRedaction`, `AdminConfig_ExplanationsVisible` |

### admin-api-key.spec.ts — Detail

Proves the managed API Keys admin UI (Sprint 16 feature) and the config-field explanation toggles.
All 4 tests are in the AI-free tier (0 Anthropic API calls).

**ZERO ANTHROPIC TOUCHES**: `anthropic_api_key` is never written or read in any test.  A fake value
entered via UI would replace the live key and break all AI features for the test's duration.
`brave_api_key` is used instead: a fake Brave key silently disables web-search grounding but does
not interrupt content generation, grading, or chat.  All mutations are cleaned up in `finally`-blocks
via `DELETE /api/v1/admin/secrets/brave_api_key`.

| Test | Governing REQ | What is asserted | Coverage type |
|---|---|---|---|
| `AdminSecrets_SectionRendersWithStatuses` | REQ-FEADMIN-500, REQ-FEADMIN-501, REQ-FEADMIN-502 | `.secrets-card h2` = "API Keys"; both key labels visible; each badge text is one of the 3 valid states; all password inputs are `type=password` and initially empty | E2E |
| `AdminSecrets_SaveRoundTrip_NeverEchoed` | REQ-FEADMIN-503, REQ-FEADMIN-504, REQ-ADMIN-007, REQ-SECURITY-008 | Save fake Brave key → badge = "Configured (••••9999)"; `page.content()` does not contain full value; `GET /api/v1/admin/secrets` response body does not contain full value; input cleared after save; Clear button → confirm dialog → badge returns to non-configured state; Clear button hidden; restored in `finally` | E2E |
| `AdminSecrets_AuditRedaction` | REQ-SECURITY-008 | PUT + DELETE via `page.request`; `GET /api/v1/audit` response text does not contain fake value; `secret.set` entry exists with `payload = {name: "brave_api_key"}`; `secret.clear` entry exists with `payload = {name: "brave_api_key"}`; no extra payload fields (no value, no last4) | E2E |
| `AdminConfig_ExplanationsVisible` | REQ-FEADMIN-510, REQ-FEADMIN-511, REQ-FEADMIN-512, REQ-FEADMIN-515 | brave_api_key `.secret-explanation` always visible (no toggle); `agent_retry_limit` toggle expands explanation mentioning "AI agent retries"; `audit_retention_days` explanation contains "No automated purge"; `session_inactivity_seconds` explanation contains "has no effect on the running server"; toggle collapses on second click | E2E |

---

## Notes

- Specs listed as existing are confirmed present in `frontend/e2e/` at Sprint 14.
- REQ IDs in the "Governing REQ IDs" column point to `REQ-FE-COURSE.json`,
  `REQ-FE-CONTENT.json`, `REQ-FE-ADMIN.json`, and `requirements/l2-requirements.json`.
- Session-refresh specs (Sprint 13) use the live production stack with real cookie
  auth — no mocking. The `request` fixture is used for cookie-free API calls.
- Content-rendering specs (Sprint 14) use the DB seed helpers (no AI calls).
  In-SPA navigation (click "Courses" nav → click course card) is used instead of
  hard `page.goto()` for intake pages to avoid a router-guard redirect that can
  occur during SPA boot before auth state is fully restored.
- Branding has no formal requirement (no REQ-BRAND-* IDs). Branding tests are
  traced to the closest layout/login requirements that share the same Vue component.
- Sprint 13 auth change: `helpers.ts archiveOpenCourses` pattern continues to
  use Bearer token (captured from login response body which still includes `token`
  per `internal/auth/handler.go`). The API accepts both cookie and Bearer header;
  e2e helpers use the Bearer path so cookie-jar state does not affect API calls.
