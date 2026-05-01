# TEST-FE-COURSE — Student Course Lifecycle Frontend Test Plan

This document is the authoritative verification test plan for the Valory frontend student course lifecycle module. Sprint 6 contributors must satisfy every test case listed here before delivery is accepted.

Every test case includes a `Verifies:` line that maps it to one or more requirements in `REQ-FECOURSE.json`. A requirement is considered verified only when all test cases that cite it pass.

---

## API contract reference

The following shapes are derived from the live handler code and must be matched exactly by the frontend.

### Course object (response from GET /api/v1/courses and POST /api/v1/courses)

```json
{
  "id": "<uuid>",
  "student_id": "<uuid>",
  "title": "<string>",
  "topic": "<string>",
  "status": "<intake|syllabus_draft|syllabus_approved|generating|active|archived|completed>",
  "created_at": "<RFC3339>",
  "updated_at": "<RFC3339>",
  "pre_withdrawal_status": "<string | omitted>"
}
```

### List courses response (GET /api/v1/courses)

```json
{
  "courses": [ /* course objects */ ],
  "next_cursor": "<string | null>"
}
```

### Create course request body

```json
{ "topic": "<string>" }
```

### Chat request body (POST /api/v1/courses/{id}/chat)

```json
{ "message": "<string>" }
```

### Chat response

```json
{ "reply": "<string>" }
```

### Modification request body (POST /api/v1/courses/{id}/syllabus/modification)

```json
{ "request": "<string>" }
```

Note: the field name is `request`, not `notes`. Any other field name will be ignored by the server and result in a 400.

### SSE event format (GET /api/v1/courses/{id}/events)

```
event: <event_type>
data: <json>

```

Keepalive lines begin with a colon:

```
: keepalive
```

The `?after=<uuid>` query parameter may be appended on reconnect to receive only events newer than the last received event ID.

---

## Tier 1 — Unit tests (Vitest)

These tests exercise pure TypeScript/JavaScript logic with no network calls and no DOM rendering. All inputs and expected outputs are defined inline. Use fake timers where delays are involved.

---

### TC-COURSE-U-001 — Status routing: intake maps to intake view

Verifies: REQ-FECOURSE-008, REQ-FECOURSE-080, REQ-FECOURSE-014, REQ-FECOURSE-114

**Setup:** Import the `resolveViewForStatus` (or equivalent) pure function that accepts a status string and returns a route name or component identifier.

**Input:** `"intake"`

**Expected output:** The returned value equals the identifier for the intake chat view (e.g. `"CourseIntake"` or `"/courses/:id/intake"`).

---

### TC-COURSE-U-002 — Status routing: syllabus_draft maps to syllabus draft view

Verifies: REQ-FECOURSE-008, REQ-FECOURSE-081

**Input:** `"syllabus_draft"`

**Expected output:** Identifier for the syllabus draft view.

---

### TC-COURSE-U-003 — Status routing: syllabus_approved maps to schedule view

Verifies: REQ-FECOURSE-008, REQ-FECOURSE-082

**Input:** `"syllabus_approved"`

**Expected output:** Identifier for the due-date schedule view.

---

### TC-COURSE-U-004 — Status routing: generating maps to generating view

Verifies: REQ-FECOURSE-008, REQ-FECOURSE-083

**Input:** `"generating"`

**Expected output:** Identifier for the generating view.

---

### TC-COURSE-U-005 — Status routing: active maps to course hub view

Verifies: REQ-FECOURSE-008, REQ-FECOURSE-084

**Input:** `"active"`

**Expected output:** Identifier for the course hub view.

---

### TC-COURSE-U-006 — Status routing: archived maps to course hub view

Verifies: REQ-FECOURSE-008, REQ-FECOURSE-085

**Input:** `"archived"`

**Expected output:** Identifier for the course hub view (same as active).

---

### TC-COURSE-U-007 — Status routing: completed maps to course hub view

Verifies: REQ-FECOURSE-008, REQ-FECOURSE-086

**Input:** `"completed"`

**Expected output:** Identifier for the course hub view (same as active).

---

### TC-COURSE-U-008 — SSE line parser: extracts event type and data from a well-formed block

Verifies: REQ-FECOURSE-020, REQ-FECOURSE-201

**Setup:** Import the SSE line parser function (e.g. `parseSseLine` or the parser embedded in the SSE client composable). Feed it a complete SSE block as a raw string.

**Input:**

```
event: pipeline_status
data: {"status":"intake","message":"Chair agent started"}

```

**Expected output:** An object with `{ eventType: "pipeline_status", data: { status: "intake", message: "Chair agent started" } }` (or equivalent structured representation).

---

### TC-COURSE-U-009 — SSE line parser: discards keepalive comment lines

Verifies: REQ-FECOURSE-007, REQ-FECOURSE-072, REQ-FECOURSE-720

**Input:** `": keepalive"`

**Expected output:** `null` or `undefined` — the parser must return no event object for comment lines.

---

### TC-COURSE-U-010 — SSE line parser: discards blank lines between events

Verifies: REQ-FECOURSE-201

**Input:** An empty string `""`

**Expected output:** `null` or `undefined` — blank lines are SSE block delimiters and must not produce events.

---

### TC-COURSE-U-011 — SSE line parser: handles a data-only line with no event type

Verifies: REQ-FECOURSE-201

**Input:**

```
data: {"message":"hello"}

```

**Expected output:** An object where `eventType` is `"message"` (the SSE default) and `data` is the parsed JSON object.

---

### TC-COURSE-U-012 — SSE reconnect backoff: attempt 1 yields 1-second delay

Verifies: REQ-FECOURSE-025, REQ-FECOURSE-250

**Setup:** Import the backoff calculator (e.g. `reconnectDelay(attempt: number): number`).

**Input:** `1`

**Expected output:** `1000` (milliseconds).

---

### TC-COURSE-U-013 — SSE reconnect backoff: attempt 2 yields 2-second delay

Verifies: REQ-FECOURSE-025, REQ-FECOURSE-250

**Input:** `2`

**Expected output:** `2000`.

---

### TC-COURSE-U-014 — SSE reconnect backoff: attempt 3 yields 4-second delay

Verifies: REQ-FECOURSE-025, REQ-FECOURSE-250

**Input:** `3`

**Expected output:** `4000`.

---

### TC-COURSE-U-015 — SSE reconnect backoff: attempt 4 yields 8-second delay

Verifies: REQ-FECOURSE-025, REQ-FECOURSE-250

**Input:** `4`

**Expected output:** `8000`.

---

### TC-COURSE-U-016 — SSE reconnect backoff: attempt 5 yields 16-second delay

Verifies: REQ-FECOURSE-025, REQ-FECOURSE-250

**Input:** `5`

**Expected output:** `16000`.

---

### TC-COURSE-U-017 — Chat message ordering: student role produces right alignment class

Verifies: REQ-FECOURSE-023, REQ-FECOURSE-230

**Setup:** Import the function or computed property that resolves CSS alignment from a message role string (e.g. `messageAlignClass(role: string): string`).

**Input:** `"student"` (or `"user"`)

**Expected output:** A class name or style value that represents right-alignment (e.g. `"message--right"` or `"justify-end"`).

---

### TC-COURSE-U-018 — Chat message ordering: assistant role produces left alignment class

Verifies: REQ-FECOURSE-023, REQ-FECOURSE-230

**Input:** `"assistant"`

**Expected output:** A class name or style value that represents left-alignment (e.g. `"message--left"` or `"justify-start"`).

---

## Tier 2 — Component integration tests (Vitest + Vue Test Utils)

These tests mount real Vue components in a JSDOM environment. All HTTP and SSE calls are intercepted with `vi.stubGlobal('fetch', ...)` or a test-specific fetch mock. Vue Router is provided as a stub or in-memory instance. Tests must not make real network requests.

---

### TC-COURSE-I-001 — Dashboard: GET /api/v1/courses on mount returns 3 courses → 3 cards rendered

Verifies: REQ-FECOURSE-001, REQ-FECOURSE-010, REQ-FECOURSE-100, REQ-FECOURSE-102, REQ-FECOURSE-011, REQ-FECOURSE-110, REQ-FECOURSE-111

**Setup:**

- Stub `fetch` to return `200` with body:

```json
{
  "courses": [
    { "id": "aaa", "topic": "Machine Learning", "status": "intake", "created_at": "2026-04-01T10:00:00Z", "updated_at": "2026-04-01T10:00:00Z" },
    { "id": "bbb", "topic": "Web Development", "status": "active", "created_at": "2026-04-02T10:00:00Z", "updated_at": "2026-04-02T10:00:00Z" },
    { "id": "ccc", "topic": "Algorithms", "status": "completed", "created_at": "2026-04-03T10:00:00Z", "updated_at": "2026-04-03T10:00:00Z" }
  ],
  "next_cursor": null
}
```

- Mount the `CourseDashboard` component (or equivalent).

**Assertions:**

1. `fetch` was called with a URL that includes `/api/v1/courses`.
2. Exactly three course card elements are rendered in the DOM.
3. The first card contains the text `"Machine Learning"`.
4. The first card contains the text `"intake"`.

---

### TC-COURSE-I-002 — Dashboard: empty response → empty state shown

Verifies: REQ-FECOURSE-001, REQ-FECOURSE-012, REQ-FECOURSE-112

**Setup:**

- Stub `fetch` to return `200` with body `{ "courses": [], "next_cursor": null }`.
- Mount `CourseDashboard`.

**Assertions:**

1. Zero course card elements are rendered.
2. The text `"You have no courses yet."` is present in the DOM.

---

### TC-COURSE-I-003 — Dashboard: fetch error → error message shown

Verifies: REQ-FECOURSE-010, REQ-FECOURSE-100, REQ-FECOURSE-103

**Setup:**

- Stub `fetch` to return `500` with body `{ "error": "INTERNAL_ERROR", "message": "internal server error" }`.
- Mount `CourseDashboard`.

**Assertions:**

1. An error message element is present in the DOM (text need not match exactly, but a visible error indicator must exist).
2. Zero course card elements are rendered.

---

### TC-COURSE-I-004 — Dashboard: create course modal submit → POST called with correct body, card prepended on 201

Verifies: REQ-FECOURSE-009, REQ-FECOURSE-091, REQ-FECOURSE-092, REQ-FECOURSE-095, REQ-FECOURSE-096, REQ-FECOURSE-910, REQ-FECOURSE-912, REQ-FECOURSE-913, REQ-FECOURSE-920, REQ-FECOURSE-921, REQ-FECOURSE-922

**Setup:**

- Initial `fetch` stub for `GET /api/v1/courses` returns `{ "courses": [], "next_cursor": null }`.
- Second `fetch` stub for `POST /api/v1/courses` returns `201` with body:

```json
{
  "id": "new-uuid-123",
  "topic": "Introduction to Machine Learning",
  "status": "intake",
  "created_at": "2026-05-01T00:00:00Z",
  "updated_at": "2026-05-01T00:00:00Z"
}
```

- Mount `CourseDashboard`.
- Open the create-course modal (trigger the open button click).
- Enter `"Introduction to Machine Learning"` in the topic field.
- Click the submit button.
- Await the mock response.

**Assertions:**

1. The `POST /api/v1/courses` call was made exactly once.
2. The request body JSON contains `{ "topic": "Introduction to Machine Learning" }`.
3. The request includes an `Authorization` header.
4. The request includes `Content-Type: application/json`.
5. The modal is no longer visible after the 201 response.
6. The course list now contains a card with topic `"Introduction to Machine Learning"` at the top of the list.
7. The router navigated to the intake route for `"new-uuid-123"`.

---

### TC-COURSE-I-005 — Dashboard: create course modal — 409 response shows error message

Verifies: REQ-FECOURSE-009, REQ-FECOURSE-093, REQ-FECOURSE-930

**Setup:**

- GET stub returns empty courses list.
- POST stub returns `409` with body `{ "error": "COURSE_ALREADY_ACTIVE", "message": "student already has an active course" }`.
- Mount `CourseDashboard`, open modal, enter a topic, click submit.

**Assertions:**

1. The modal remains open.
2. The text `"You already have an active course. Complete or archive it first."` is present in the modal DOM.

---

### TC-COURSE-I-006 — Dashboard: create course modal — dismiss without submitting clears topic field

Verifies: REQ-FECOURSE-009, REQ-FECOURSE-094, REQ-FECOURSE-940

**Setup:**

- Mount `CourseDashboard` (GET stub returns empty list).
- Open the modal.
- Type `"Partial topic text"` into the topic field.
- Click the cancel/dismiss button.
- Re-open the modal.

**Assertions:**

1. The topic input field is empty (value is `""`).

---

### TC-COURSE-I-007 — Dashboard: create course modal — empty topic prevents submission

Verifies: REQ-FECOURSE-090, REQ-FECOURSE-901

**Setup:**

- Mount `CourseDashboard`, open modal.
- Leave the topic field empty.
- Click the submit button.

**Assertions:**

1. `fetch` was not called with a POST to `/api/v1/courses`.
2. A validation message is visible in the modal.

---

### TC-COURSE-I-008 — Dashboard: card click navigates to status-appropriate view

Verifies: REQ-FECOURSE-001, REQ-FECOURSE-014, REQ-FECOURSE-114, REQ-FECOURSE-008

**Setup:**

- GET stub returns a single course with `status: "syllabus_draft"` and `id: "course-xyz"`.
- Mount `CourseDashboard` with an in-memory router.
- Click the course card.

**Assertions:**

1. The router navigated to the syllabus draft route for `"course-xyz"`.

---

### TC-COURSE-I-009 — Intake view: SSE connection opened with Authorization header on mount

Verifies: REQ-FECOURSE-002, REQ-FECOURSE-020, REQ-FECOURSE-070, REQ-FECOURSE-200, REQ-FECOURSE-202, REQ-FECOURSE-700

**Setup:**

- Stub `fetch` to return a streaming response with `Content-Type: text/event-stream` and an initially empty body (use a `ReadableStream` that never resolves additional chunks).
- Provide a course ID of `"course-abc"` via router params.
- Mount the `CourseIntake` component.

**Assertions:**

1. `fetch` was called exactly once on mount.
2. The URL contains `/api/v1/courses/course-abc/events`.
3. The request headers include `Authorization: Bearer <token>` (any non-empty token value).
4. `fetch` was NOT called with `new EventSource(...)` — the global `EventSource` constructor must not have been invoked.

---

### TC-COURSE-I-010 — Intake view: SSE event "intake_complete" / syllabus_draft status → navigation to syllabus view

Verifies: REQ-FECOURSE-002, REQ-FECOURSE-024, REQ-FECOURSE-240

**Setup:**

- Stub `fetch` SSE stream to emit one event after mount:

```
event: pipeline_status
data: {"status":"syllabus_draft","message":"Syllabus drafted"}

```

- Mount `CourseIntake` with router params `{ id: "course-abc" }`.
- Await the event processing.

**Assertions:**

1. The router navigated to the syllabus draft route for `"course-abc"`.

---

### TC-COURSE-I-011 — Intake view: chat submit → POST called with correct body, assistant reply appended

Verifies: REQ-FECOURSE-002, REQ-FECOURSE-022, REQ-FECOURSE-220, REQ-FECOURSE-224

**Setup:**

- SSE fetch stub returns an empty stream (no events).
- Chat POST stub returns `200` with body `{ "reply": "Hello, I am the Chair agent." }`.
- Mount `CourseIntake`.
- Type `"Tell me about this course"` into the chat input.
- Click send.
- Await the mock response.

**Assertions:**

1. The POST to `/api/v1/courses/{id}/chat` was called once.
2. The request body is `{ "message": "Tell me about this course" }`.
3. The student message `"Tell me about this course"` appears in the conversation DOM.
4. The assistant message `"Hello, I am the Chair agent."` appears in the conversation DOM after the student message.

---

### TC-COURSE-I-012 — Intake view: chat POST error → error displayed, input re-enabled

Verifies: REQ-FECOURSE-022, REQ-FECOURSE-225

**Setup:**

- SSE stub returns empty stream.
- Chat POST stub returns `500`.
- Mount `CourseIntake`, submit a message.

**Assertions:**

1. An error message element is visible in the DOM.
2. The chat input field is not disabled.
3. The send button is not disabled.

---

### TC-COURSE-I-013 — SSE: connection drop → reconnect attempted after 1 second

Verifies: REQ-FECOURSE-007, REQ-FECOURSE-025, REQ-FECOURSE-250, REQ-FECOURSE-251

**Setup:**

- Use fake timers (`vi.useFakeTimers()`).
- First `fetch` call throws a network error (rejected promise).
- Second `fetch` call returns a valid empty SSE stream.
- Mount `CourseIntake`.

**Assertions:**

1. After advancing fake timers by 999 ms, `fetch` has been called only once (no premature reconnect).
2. After advancing fake timers by 1001 ms total, `fetch` has been called a second time (reconnect at 1 s).

---

### TC-COURSE-I-014 — SSE: 5 failed reconnects → exhaustion message shown, no sixth attempt

Verifies: REQ-FECOURSE-007, REQ-FECOURSE-025, REQ-FECOURSE-251, REQ-FECOURSE-252

**Setup:**

- Use fake timers.
- All `fetch` calls throw network errors.
- Mount `CourseIntake`.
- Advance timers through all five backoff intervals (1 s + 2 s + 4 s + 8 s + 16 s = 31 s total).

**Assertions:**

1. `fetch` was called exactly 6 times (1 initial + 5 retries).
2. After the sixth call fails, `fetch` is not called again even after advancing timers by an additional 60 s.
3. A connection-failure message is visible in the DOM.

---

### TC-COURSE-I-015 — SSE: reconnect appends ?after=lastEventId query parameter

Verifies: REQ-FECOURSE-007, REQ-FECOURSE-071, REQ-FECOURSE-710

**Setup:**

- Use fake timers.
- First `fetch` returns an SSE stream that emits one event with `id: "event-uuid-001"`, then the stream closes (connection drop simulated by the stream ending).
- Second `fetch` returns an empty SSE stream.
- Mount `CourseIntake`, process first stream, advance timers past the 1-second backoff.

**Assertions:**

1. The second `fetch` call URL contains `?after=event-uuid-001`.

---

### TC-COURSE-I-016 — SSE: AbortController.abort() called on component unmount

Verifies: REQ-FECOURSE-007, REQ-FECOURSE-073, REQ-FECOURSE-730

**Setup:**

- Stub `fetch` to return a never-ending SSE stream.
- Spy on `AbortController.prototype.abort`.
- Mount `CourseIntake`.
- Unmount the component.

**Assertions:**

1. `AbortController.prototype.abort` was called exactly once after unmount.

---

### TC-COURSE-I-017 — Syllabus draft view: syllabus content fetched on mount

Verifies: REQ-FECOURSE-003, REQ-FECOURSE-033, REQ-FECOURSE-300, REQ-FECOURSE-330

**Setup:**

- Stub `fetch` for `GET /api/v1/courses/{id}/syllabus/latest` (or equivalent syllabus endpoint) to return `200` with:

```json
{ "id": "syl-001", "version": 1, "content_adoc": "= Introduction\n\nThis is the syllabus." }
```

- Mount `CourseSyllabus` (or equivalent syllabus draft component) with `id: "course-abc"`.

**Assertions:**

1. `fetch` was called on mount with a URL containing `syllabus`.
2. The text `"= Introduction"` or `"This is the syllabus."` is present in the rendered DOM.

---

### TC-COURSE-I-018 — Syllabus draft view: fetch error → error message shown

Verifies: REQ-FECOURSE-033, REQ-FECOURSE-331

**Setup:**

- Stub `fetch` for the syllabus endpoint to return `404`.
- Mount `CourseSyllabus`.

**Assertions:**

1. A fetch-error message is present in the DOM.

---

### TC-COURSE-I-019 — Syllabus draft view: approve button → POST /syllabus/approve called, navigate to schedule view on 200

Verifies: REQ-FECOURSE-003, REQ-FECOURSE-031, REQ-FECOURSE-310, REQ-FECOURSE-312

**Setup:**

- Syllabus GET stub returns `200` with valid content.
- Approve POST stub returns `200` with body `{ "id": "course-abc", "status": "syllabus_approved", "syllabus_version": 1 }`.
- Mount `CourseSyllabus` with `id: "course-abc"`.
- Click the approve button.

**Assertions:**

1. `fetch` was called with `POST /api/v1/courses/course-abc/syllabus/approve`.
2. The request includes an `Authorization` header.
3. The router navigated to the schedule view for `"course-abc"`.

---

### TC-COURSE-I-020 — Syllabus draft view: approve → 409 displays invalid-transition error

Verifies: REQ-FECOURSE-031, REQ-FECOURSE-313

**Setup:**

- Approve POST stub returns `409`.
- Mount `CourseSyllabus`, click approve.

**Assertions:**

1. An error message related to invalid transition is visible.
2. The router did not navigate away.

---

### TC-COURSE-I-021 — Syllabus draft view: approve → non-200/non-409 displays generic error

Verifies: REQ-FECOURSE-031, REQ-FECOURSE-314

**Setup:**

- Approve POST stub returns `500`.
- Mount `CourseSyllabus`, click approve.

**Assertions:**

1. A generic error message is visible.
2. The router did not navigate away.

---

### TC-COURSE-I-022 — Syllabus draft view: modification modal submit → POST called with `request` field

Verifies: REQ-FECOURSE-003, REQ-FECOURSE-032, REQ-FECOURSE-321

**Setup:**

- Syllabus GET stub returns valid content.
- Modification POST stub returns `200` with body `{ "id": "course-abc", "status": "syllabus_draft", "syllabus_version": 2 }`.
- Mount `CourseSyllabus`.
- Click the "Request modification" button to open the modal.
- Enter `"Please add more examples"` in the text area.
- Click the modal submit button.

**Assertions:**

1. The POST to `/api/v1/courses/course-abc/syllabus/modification` was called.
2. The request body contains `{ "request": "Please add more examples" }`. The key must be `request`, not `notes` or any other name.

---

### TC-COURSE-I-023 — Syllabus draft view: modification 200 → modal closed, confirmation message shown, syllabus reloaded

Verifies: REQ-FECOURSE-032, REQ-FECOURSE-323, REQ-FECOURSE-324

**Setup:** (Continue from TC-COURSE-I-022 successful flow)

- After the 200 response:

**Assertions:**

1. The modification modal is no longer visible.
2. A modification-submitted confirmation message is present in the DOM.
3. `fetch` was called a second time for the syllabus endpoint (reload).

---

### TC-COURSE-I-024 — Syllabus draft view: modification non-200 → error shown inside modal

Verifies: REQ-FECOURSE-032, REQ-FECOURSE-325

**Setup:**

- Modification POST stub returns `500`.
- Mount `CourseSyllabus`, open modification modal, enter text, submit.

**Assertions:**

1. The modal remains open.
2. An error message is visible inside the modal.

---

### TC-COURSE-I-025 — Schedule view: agree button → POST /schedule/agree called, navigate to generating on 200

Verifies: REQ-FECOURSE-004, REQ-FECOURSE-042, REQ-FECOURSE-420, REQ-FECOURSE-422

**Setup:**

- Schedule agree POST stub returns `200` with body `{ "id": "course-abc", "agreed_count": 3 }`.
- Mount the `CourseSchedule` component with `id: "course-abc"`.
- Click the agree button.

**Assertions:**

1. `fetch` was called with `POST /api/v1/courses/course-abc/schedule/agree`.
2. The request includes an `Authorization` header.
3. The router navigated to the generating view for `"course-abc"`.

---

### TC-COURSE-I-026 — Schedule view: agree → 409 displays no-pending-schedule error

Verifies: REQ-FECOURSE-042, REQ-FECOURSE-423

**Setup:**

- Schedule agree POST stub returns `409`.
- Mount `CourseSchedule`, click agree.

**Assertions:**

1. An error message about no pending schedule is visible.
2. The router did not navigate away.

---

### TC-COURSE-I-027 — Schedule view: agree → non-200/non-409 displays generic error

Verifies: REQ-FECOURSE-042, REQ-FECOURSE-424

**Setup:**

- Schedule agree POST stub returns `500`.
- Mount `CourseSchedule`, click agree.

**Assertions:**

1. A generic error message is visible.

---

### TC-COURSE-I-028 — Generating view: SSE connection opened on mount, active event triggers navigation

Verifies: REQ-FECOURSE-005, REQ-FECOURSE-051, REQ-FECOURSE-052, REQ-FECOURSE-510, REQ-FECOURSE-520

**Setup:**

- Stub `fetch` SSE stream to emit one event:

```
event: pipeline_status
data: {"status":"active","message":"Content generation complete"}

```

- Mount the `CourseGenerating` component with `id: "course-abc"`.

**Assertions:**

1. `fetch` was called with a URL containing `/api/v1/courses/course-abc/events`.
2. After the event is processed, the router navigated to the course hub view for `"course-abc"`.

---

### TC-COURSE-I-029 — Course hub view: withdraw button visible only when status is active

Verifies: REQ-FECOURSE-006, REQ-FECOURSE-063, REQ-FECOURSE-630

**Setup:** Mount `CourseHub` component twice: once with course `status: "active"` and once with `status: "archived"`.

**Assertions:**

1. When status is `"active"`: the withdraw button is present in the DOM.
2. When status is `"archived"`: the withdraw button is NOT present in the DOM.

---

### TC-COURSE-I-030 — Course hub view: resume button visible only when status is archived

Verifies: REQ-FECOURSE-006, REQ-FECOURSE-063, REQ-FECOURSE-634

**Setup:** Mount `CourseHub` twice: once with `status: "archived"` and once with `status: "active"`.

**Assertions:**

1. When status is `"archived"`: the resume button is present in the DOM.
2. When status is `"active"`: the resume button is NOT present in the DOM.

---

### TC-COURSE-I-031 — Course hub view: withdraw → POST /withdraw called, navigate to dashboard on 200

Verifies: REQ-FECOURSE-063, REQ-FECOURSE-631, REQ-FECOURSE-632

**Setup:**

- Withdraw POST stub returns `200` with body `{ "id": "course-abc", "status": "archived" }`.
- Mount `CourseHub` with `status: "active"`, `id: "course-abc"`.
- Click the withdraw button.

**Assertions:**

1. `fetch` was called with `POST /api/v1/courses/course-abc/withdraw`.
2. The request includes an `Authorization` header.
3. The router navigated to the dashboard route.

---

### TC-COURSE-I-032 — Course hub view: withdraw → non-200 shows error

Verifies: REQ-FECOURSE-063, REQ-FECOURSE-633

**Setup:**

- Withdraw POST stub returns `500`.
- Mount `CourseHub` with `status: "active"`, click withdraw.

**Assertions:**

1. An error message is visible.
2. The router did not navigate away.

---

### TC-COURSE-I-033 — Course hub view: resume → POST /resume called, navigate to status-appropriate view on 200

Verifies: REQ-FECOURSE-063, REQ-FECOURSE-635, REQ-FECOURSE-636

**Setup:**

- Resume POST stub returns `200` with body `{ "id": "course-abc", "status": "syllabus_draft" }`.
- Mount `CourseHub` with `status: "archived"`, `id: "course-abc"`.
- Click the resume button.

**Assertions:**

1. `fetch` was called with `POST /api/v1/courses/course-abc/resume`.
2. The request includes an `Authorization` header.
3. The router navigated to the syllabus draft view for `"course-abc"` (the view matching `"syllabus_draft"` status).

---

### TC-COURSE-I-034 — Course hub view: resume → 409 shows already-active-course error

Verifies: REQ-FECOURSE-063, REQ-FECOURSE-637

**Setup:**

- Resume POST stub returns `409`.
- Mount `CourseHub` with `status: "archived"`, click resume.

**Assertions:**

1. An error message about already having an active course is visible.
2. The router did not navigate away.

---

### TC-COURSE-I-035 — Course hub view: section list renders one item per section

Verifies: REQ-FECOURSE-006, REQ-FECOURSE-061, REQ-FECOURSE-610

**Setup:**

- Mount `CourseHub` with a course that has 4 sections (passed as props or loaded via a stubbed fetch).

**Assertions:**

1. Exactly four section list items are rendered.

---

## Tier 3 — E2E demonstration procedure (human-executed)

This procedure must be performed against a running Valory development environment (`docker compose up`). A human tester executes each step in a browser and records the outcome.

Assumptions:

- The application is running at `http://localhost:5173`.
- A student account exists with email `student@example.com` / password `password`.
- No active course exists for this student before the test begins.

---

### TC-COURSE-E-001 — Full student course lifecycle walkthrough

Verifies: REQ-FECOURSE-001, REQ-FECOURSE-002, REQ-FECOURSE-003, REQ-FECOURSE-004, REQ-FECOURSE-005, REQ-FECOURSE-006, REQ-FECOURSE-007, REQ-FECOURSE-008, REQ-FECOURSE-009

**Steps:**

1. Navigate to `http://localhost:5173/login`.
   - Enter email `student@example.com` and password `password`.
   - Click the "Sign in" button.
   - **Expected:** Redirected to the course dashboard at `http://localhost:5173/courses`. The page heading reads "My Courses" or equivalent.

2. On the dashboard, click the "Create new course" button.
   - **Expected:** A modal dialog opens containing a single text input labelled "Topic".

3. Enter `"Introduction to Machine Learning"` in the Topic field.
   - Click the "Create" (or "Submit") button.
   - **Expected:** The modal closes. The dashboard now shows a course card with topic "Introduction to Machine Learning" and status "intake" at the top of the list.

4. Click the "Introduction to Machine Learning" course card.
   - **Expected:** The browser navigates to a URL of the form `http://localhost:5173/courses/<uuid>/intake`. The page shows the intake chat view with a text input and send button.

5. In the chat input, type `"I want to learn the fundamentals of supervised and unsupervised learning."` and click "Send".
   - **Expected:** The student's message appears on the right side of the conversation. A typing indicator appears while the agent processes the message.

6. Wait for the Chair agent's response to appear.
   - **Expected:** The agent's reply appears on the left side of the conversation below the student message.

7. Continue the intake conversation until the Chair agent signals that intake is complete. The completion is signalled by the course status changing to `syllabus_draft` via the SSE event stream.
   - **Expected:** The browser automatically navigates to the syllabus draft view at a URL of the form `http://localhost:5173/courses/<uuid>/syllabus`. The page displays the syllabus text.

8. Read the generated syllabus. Click the "Approve" button.
   - **Expected:** The page navigates to the due-date schedule view at `http://localhost:5173/courses/<uuid>/schedule`. A list of homework assignments with due dates is displayed. The text "Once you agree, due dates cannot be changed." is visible above the agree button.

9. Read the schedule. Click the "I Agree" (or "Agree") button.
   - **Expected:** The page navigates to the generating view at `http://localhost:5173/courses/<uuid>/generating`. The page displays a message such as "Generating your course content. This may take several minutes." and shows pipeline events as they arrive.

10. Wait for content generation to complete. The SSE stream will emit an event with `status: "active"`.
    - **Expected:** The browser automatically navigates to the course hub at `http://localhost:5173/courses/<uuid>`. The header displays the topic "Introduction to Machine Learning" and status "active". A list of sections is visible. A list of homework assignments with due dates and submission status is visible.

11. Click the first section link in the section list.
    - **Expected:** The browser navigates to a section reader view. The section's content is displayed.

---

### TC-COURSE-E-002 — Withdraw and resume lifecycle

Verifies: REQ-FECOURSE-006, REQ-FECOURSE-063

**Steps:**

1. From the course hub of an active course, click the "Withdraw" button.
   - **Expected:** The browser navigates to the dashboard. The course card now shows status "archived".

2. Click the archived course card.
   - **Expected:** The browser navigates to the course hub for that course. A "Resume" button is visible. The "Withdraw" button is not visible.

3. Click the "Resume" button.
   - **Expected:** The browser navigates to the view appropriate for the pre-withdrawal status (e.g. syllabus draft view if the course was in `syllabus_draft`). The course is once again accessible for the relevant workflow step.

---

### TC-COURSE-E-003 — Modification request flow

Verifies: REQ-FECOURSE-003, REQ-FECOURSE-032

**Steps:**

1. Navigate to the syllabus draft view of a course in `syllabus_draft` status.
   - Click the "Request modification" button.
   - **Expected:** A modal opens with a text area.

2. Enter `"Please add two more sections on neural networks."` in the text area.
   - Click the submit button.
   - **Expected:** The modal closes. A confirmation message such as "Modification request submitted." appears. The syllabus content reloads and the version number increments.

---

## Tier 4 — Inspection checklist

These items are verified by code review, not by automated tests. Each item must be checked by the reviewer against the submitted implementation source files. Mark each item Pass or Fail.

---

### TC-COURSE-X-001 — SSE connection uses fetch, not EventSource

Verifies: REQ-FECOURSE-007, REQ-FECOURSE-070, REQ-FECOURSE-700

**Criterion:** Search the SSE client composable and all view components for the string `new EventSource`. It must not appear. The SSE stream must be opened with `fetch(url, { headers: { Authorization: ... }, signal: controller.signal })`. A grep returning zero matches for `new EventSource` is a pass.

Pass / Fail

---

### TC-COURSE-X-002 — SSE connection sends Authorization: Bearer <token> header

Verifies: REQ-FECOURSE-007, REQ-FECOURSE-070, REQ-FECOURSE-700

**Criterion:** Inspect the `fetch` call that opens the SSE stream. The `headers` object passed to `fetch` must contain an `Authorization` key whose value is `"Bearer " + token` where `token` is the authenticated user's JWT retrieved from the auth store. A hardcoded empty string or missing header is a fail.

Pass / Fail

---

### TC-COURSE-X-003 — AbortController is called on component unmount

Verifies: REQ-FECOURSE-007, REQ-FECOURSE-073, REQ-FECOURSE-730

**Criterion:** Inspect the `onUnmounted` lifecycle hook (or equivalent cleanup callback such as the return value of `onMounted` or a `watchEffect` cleanup) in every view that uses the SSE composable. Each must call `controller.abort()`. Confirm that `abort` is not omitted, not conditionally skipped, and is called before the component is removed from the DOM. Missing `abort` call is a fail.

Pass / Fail

---

### TC-COURSE-X-004 — ?after=lastEventId appended on reconnect

Verifies: REQ-FECOURSE-007, REQ-FECOURSE-071, REQ-FECOURSE-710

**Criterion:** Inspect the reconnect logic in the SSE composable. When the composable has received at least one event (i.e. `lastEventId` is non-null), the reconnect `fetch` call must construct the URL with the query string `?after=<lastEventId>`. Verify that on the first connection (no prior events) the `after` parameter is omitted. Reconnect without the `after` parameter when events were received is a fail.

Pass / Fail

---

### TC-COURSE-X-005 — Modification endpoint body uses field name `request`

Verifies: REQ-FECOURSE-032, REQ-FECOURSE-321

**Criterion:** Inspect the `fetch` call for `POST /api/v1/courses/{id}/syllabus/modification`. The JSON body must be serialised with the key `request` (e.g. `JSON.stringify({ request: notesValue })`). Any other key name (`notes`, `text`, `body`, etc.) is a fail because the backend decodes only the `request` field.

Pass / Fail

---

### TC-COURSE-X-006 — SSE keepalive comment lines are silently discarded

Verifies: REQ-FECOURSE-007, REQ-FECOURSE-072, REQ-FECOURSE-720

**Criterion:** Inspect the SSE line-processing loop. Lines that begin with `:` (colon) must be skipped with a `continue` or equivalent early return before any event dispatch or state mutation occurs. A pass requires that no code path processes a colon-prefixed line as a data event.

Pass / Fail

---

### TC-COURSE-X-007 — ReadableStream used to process SSE response body

Verifies: REQ-FECOURSE-070, REQ-FECOURSE-701

**Criterion:** Inspect the SSE client composable. The response body must be consumed via `response.body.getReader()` and processed line by line from the `ReadableStream`. Use of `response.text()` or `response.json()` (which buffer the entire body) is a fail.

Pass / Fail

---

### TC-COURSE-X-008 — Schedule view displays irreversibility warning text

Verifies: REQ-FECOURSE-041, REQ-FECOURSE-410

**Criterion:** Inspect the schedule view template. The exact text `"Once you agree, due dates cannot be changed."` must appear in a visible element above the agree button. Minor rewording is a fail.

Pass / Fail

---

### TC-COURSE-X-009 — Generating view displays static progress duration message

Verifies: REQ-FECOURSE-050, REQ-FECOURSE-500

**Criterion:** Inspect the generating view template. A static message explaining that content generation may take several minutes must be present. The message must be rendered unconditionally (not hidden behind a loading flag). A missing or conditional message is a fail.

Pass / Fail

---

### TC-COURSE-X-010 — Dashboard create button is present and opens the modal

Verifies: REQ-FECOURSE-013, REQ-FECOURSE-113

**Criterion:** Inspect the dashboard component template. A button or link labelled "Create new course" (or equivalent) must be present unconditionally in the dashboard header or toolbar — not only in the empty state. Clicking it must toggle a reactive flag that causes the create-course modal to become visible. A missing button or a button that only appears in the empty state is a fail.

Pass / Fail

---

### TC-COURSE-X-011 — Intake view pipeline events render as status messages in arrival order

Verifies: REQ-FECOURSE-021, REQ-FECOURSE-210, REQ-FECOURSE-211

**Criterion:** Inspect the intake view template and the event-append logic. Each SSE pipeline event payload must be appended to an ordered list (array push or equivalent) so that events render in the order they were received, and each event's `payload.message` (or equivalent field) is displayed as readable text. Events must not replace each other — all received events must accumulate. A fail is any implementation that overwrites the previous event or renders them in non-arrival order.

Pass / Fail

---

### TC-COURSE-X-012 — Chat input and send button disabled while awaiting reply; typing indicator shown

Verifies: REQ-FECOURSE-022, REQ-FECOURSE-221, REQ-FECOURSE-222, REQ-FECOURSE-223

**Criterion:** Inspect the chat submit handler in the intake view. Before the chat POST resolves:
- The text input element must have `:disabled="true"` (or equivalent) bound to the in-flight state.
- The send button must have `:disabled="true"` bound to the same flag.
- A typing indicator element (e.g. a "..." animation or "Agent is typing" text) must be conditionally rendered.
- The student's message must be appended to the conversation array before `await fetch(...)` is called (optimistic display).

Any of the above missing is a fail.

Pass / Fail

---

### TC-COURSE-X-013 — Conversation auto-scrolls to latest message

Verifies: REQ-FECOURSE-023, REQ-FECOURSE-231

**Criterion:** Inspect the intake view. After each message is added (student or assistant), the component must call `scrollIntoView()` or set `scrollTop = scrollHeight` on the conversation container. A `watch` or `nextTick` callback that scrolls the container after the DOM updates is acceptable. Absence of any scroll logic is a fail.

Pass / Fail

---

### TC-COURSE-X-014 — Syllabus draft view shows loading indicator and displays content_adoc field

Verifies: REQ-FECOURSE-030, REQ-FECOURSE-300, REQ-FECOURSE-301

**Criterion:** Inspect the syllabus draft view template.
- A loading indicator must be conditionally rendered while the syllabus fetch is in flight (e.g. `v-if="loading"`).
- The fetched syllabus content must be taken from the `content_adoc` field of the API response and displayed in the template. Using any other field name (`content`, `body`, etc.) is a fail.

Pass / Fail

---

### TC-COURSE-X-015 — Approve button is disabled during the POST request

Verifies: REQ-FECOURSE-031, REQ-FECOURSE-311

**Criterion:** Inspect the syllabus draft view. The approve button must have `:disabled="approving"` (or equivalent) bound to the in-flight state that is set to `true` before the `fetch` call and reset after the promise resolves or rejects. A button that is never disabled during the request is a fail.

Pass / Fail

---

### TC-COURSE-X-016 — Modification modal opens on button click with a text area

Verifies: REQ-FECOURSE-032, REQ-FECOURSE-320

**Criterion:** Inspect the syllabus draft view template. A "Request modification" button must be present. Clicking it must set a reactive flag (e.g. `showModificationModal = true`) that reveals a modal element containing a `<textarea>`. A missing text area or a modal that uses only a single-line `<input>` is a fail.

Pass / Fail

---

### TC-COURSE-X-017 — Modification modal submit button disabled during POST

Verifies: REQ-FECOURSE-032, REQ-FECOURSE-322

**Criterion:** Inspect the modification modal submit handler. The submit button inside the modal must be disabled (`:disabled="submitting"` or equivalent) while the POST is in flight. A submit button that remains enabled during the request is a fail.

Pass / Fail

---

### TC-COURSE-X-018 — Schedule view displays homework titles and formatted due dates

Verifies: REQ-FECOURSE-040, REQ-FECOURSE-400, REQ-FECOURSE-401

**Criterion:** Inspect the schedule view template. For each homework item in the list:
- The item's `title` field must be rendered as text.
- The item's `due_date` field must be formatted as a human-readable date (e.g. `"May 15, 2026"` or `"2026-05-15"`) — not rendered as a raw ISO 8601 timestamp string ending in `Z`. Using the raw timestamp directly is a fail.

Pass / Fail

---

### TC-COURSE-X-019 — Agree button disabled during POST

Verifies: REQ-FECOURSE-042, REQ-FECOURSE-421

**Criterion:** Inspect the schedule view agree handler. The agree button must be disabled while the schedule/agree POST is in flight. A button that remains enabled during the request is a fail.

Pass / Fail

---

### TC-COURSE-X-020 — Generating view renders pipeline events as they arrive

Verifies: REQ-FECOURSE-051, REQ-FECOURSE-511

**Criterion:** Inspect the generating view. Pipeline events received from the SSE stream must be appended to an ordered list and rendered in the template. The same arrival-order and accumulation rule applies as in TC-COURSE-X-011. A view that ignores SSE payloads and only shows a spinner is a fail.

Pass / Fail

---

### TC-COURSE-X-021 — Course hub header displays topic and status

Verifies: REQ-FECOURSE-060, REQ-FECOURSE-600

**Criterion:** Inspect the course hub view template. The component header (or equivalent top-level element) must render both:
- The course `topic` field as visible text.
- The course `status` field as visible text (a badge or label is acceptable).

Missing either field in the header is a fail.

Pass / Fail

---

### TC-COURSE-X-022 — Course hub section list items are navigation links

Verifies: REQ-FECOURSE-061, REQ-FECOURSE-611

**Criterion:** Inspect the course hub view template section list. Each section item must be rendered as a `<router-link>` or `<a>` element that navigates to the section reader view for that section. Plain `<span>` or `<div>` items with no click handler are a fail.

Pass / Fail

---

### TC-COURSE-X-023 — Course hub homework list shows title, due date, and submission status

Verifies: REQ-FECOURSE-062, REQ-FECOURSE-620, REQ-FECOURSE-621

**Criterion:** Inspect the course hub view template homework list. Each homework item must display:
- The assignment `title`.
- The `due_date` (formatted as human-readable, same rule as TC-COURSE-X-018).
- The submission status (e.g. "Submitted" / "Not submitted" or a boolean badge derived from the submission data).

Missing any of the three fields per item is a fail.

Pass / Fail

---

### TC-COURSE-X-024 — Dashboard loading state shown while fetch is in flight

Verifies: REQ-FECOURSE-010, REQ-FECOURSE-101

**Criterion:** Inspect the dashboard component. A loading indicator (spinner, skeleton, or text) must be conditionally rendered via a reactive flag that is set to `true` before `fetch` is called and set to `false` after the response is handled. Absence of any loading indicator is a fail.

Pass / Fail

---

### TC-COURSE-X-025 — Create course modal has a single Topic input labelled "Topic"

Verifies: REQ-FECOURSE-090, REQ-FECOURSE-900

**Criterion:** Inspect the create-course modal template. It must contain exactly one `<input>` (or `<textarea>`) for the topic. That input must be associated with a visible label whose text is "Topic". Multiple inputs, or a missing label, is a fail.

Pass / Fail

---

### TC-COURSE-X-026 — Create course modal submit button disabled during POST

Verifies: REQ-FECOURSE-091, REQ-FECOURSE-911

**Criterion:** Inspect the create-course modal submit handler. The form submit button must be disabled (`:disabled="creating"` or equivalent) while the create-course POST is in flight. A submit button that remains enabled during the request is a fail.

Pass / Fail

---

*End of TEST-FE-COURSE*
