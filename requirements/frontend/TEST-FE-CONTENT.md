# Test Plan: Frontend Content, Submission, Grades, Badges, and Notifications

**Module:** `REQ-FE-CONTENT`
**Date:** 2026-05-01
**Author:** Test Author (Valory Agent)

This document is the authoritative verification test plan for the Valory frontend content module. Every requirement in `REQ-FE-CONTENT.json` is traced to at least one test case below. Test cases are organized into four tiers: unit tests (Vitest), component integration tests (Vitest + Vue Test Utils), an E2E demonstration procedure (human-executed), and an inspection checklist.

---

## Tier 1 — Unit Tests (Vitest)

These tests cover pure functions and logic that can be exercised without a DOM or network. All tests are deterministic and use no random data.

---

### TC-CONTENT-U-001 — File format validator rejects `.pdf`

**Verifies:** REQ-FECONTENT-022, REQ-FECONTENT-112

**Description:** The client-side format validator must reject `.pdf` — it is not among the accepted extensions (`.tex`, `.md`, `.markdown`, `.adoc`, `.asciidoc`).

**Preconditions:** The `validateFileFormat(file: File): string | null` utility is importable.

**Inputs:**

| Field    | Value                  |
|----------|------------------------|
| filename | `homework.pdf`         |

**Expected result:** Non-null error string naming accepted extensions.

---

### TC-CONTENT-U-002 — File format validator rejects `.docx`

**Verifies:** REQ-FECONTENT-022, REQ-FECONTENT-112

**Description:** The client-side format validator must reject `.docx` — it is not among the accepted extensions (`.tex`, `.md`, `.markdown`, `.adoc`, `.asciidoc`).

**Inputs:**

| Field    | Value            |
|----------|------------------|
| filename | `essay.docx`     |

**Expected result:** Non-null error string naming accepted extensions.

---

### TC-CONTENT-U-003 — File format validator rejects `.zip`

**Verifies:** REQ-FECONTENT-022, REQ-FECONTENT-112

**Description:** The client-side format validator must reject `.zip` — it is not among the accepted extensions (`.tex`, `.md`, `.markdown`, `.adoc`, `.asciidoc`).

**Inputs:**

| Field    | Value           |
|----------|-----------------|
| filename | `project.zip`   |

**Expected result:** Non-null error string naming accepted extensions.

---

### TC-CONTENT-U-004 — File format validator rejects `.txt`

**Verifies:** REQ-FECONTENT-022, REQ-FECONTENT-112

**Description:** The client-side format validator must reject `.txt` — it is not among the accepted extensions (`.tex`, `.md`, `.markdown`, `.adoc`, `.asciidoc`).

**Inputs:**

| Field    | Value        |
|----------|--------------|
| filename | `notes.txt`  |

**Expected result:** Non-null error string naming accepted extensions.

---

### TC-CONTENT-U-005 — File format validator rejects `.py`

**Verifies:** REQ-FECONTENT-022, REQ-FECONTENT-112

**Description:** The client-side format validator must reject `.py` — it is not among the accepted extensions (`.tex`, `.md`, `.markdown`, `.adoc`, `.asciidoc`).

**Inputs:**

| Field    | Value          |
|----------|----------------|
| filename | `solution.py`  |

**Expected result:** Non-null error string naming accepted extensions.

---

### TC-CONTENT-U-006 — File format validator rejects `.js`

**Verifies:** REQ-FECONTENT-022, REQ-FECONTENT-112

**Description:** The client-side format validator must reject `.js` — it is not among the accepted extensions (`.tex`, `.md`, `.markdown`, `.adoc`, `.asciidoc`).

**Inputs:**

| Field    | Value          |
|----------|----------------|
| filename | `index.js`     |

**Expected result:** Non-null error string naming accepted extensions.

---

### TC-CONTENT-U-007 — File format validator accepts `.md`

**Verifies:** REQ-FECONTENT-021, REQ-FECONTENT-022, REQ-FECONTENT-111, REQ-FECONTENT-112

**Description:** The client-side format validator must return no error for `.md` (which is also a backend-accepted submission format).

**Inputs:**

| Field    | Value             |
|----------|-------------------|
| filename | `submission.md`   |

**Expected result:** Return value is `null`.

---

### TC-CONTENT-U-008 — File format validator rejects `.exe`

**Verifies:** REQ-FECONTENT-022, REQ-FECONTENT-112

**Description:** The client-side format validator must return a non-null error string when given a `.exe` file.

**Inputs:**

| Field    | Value          |
|----------|----------------|
| filename | `malware.exe`  |

**Expected result:** Return value is a non-null string that names the accepted extensions.

---

### TC-CONTENT-U-009 — File format validator rejects `.png`

**Verifies:** REQ-FECONTENT-022, REQ-FECONTENT-112

**Description:** The client-side format validator must return a non-null error string for image files.

**Inputs:**

| Field    | Value          |
|----------|----------------|
| filename | `photo.png`    |

**Expected result:** Return value is a non-null string.

---

### TC-CONTENT-U-010 — File format validator rejects `.csv`

**Verifies:** REQ-FECONTENT-022, REQ-FECONTENT-112

**Description:** The client-side format validator must return a non-null error string for spreadsheet files.

**Inputs:**

| Field    | Value         |
|----------|---------------|
| filename | `data.csv`    |

**Expected result:** Return value is a non-null string.

---

### TC-CONTENT-U-011 — File size validator accepts 10485759 bytes (limit minus 1)

**Verifies:** REQ-FECONTENT-023, REQ-FECONTENT-113

**Description:** A file whose size is exactly one byte below the 10 MB limit must pass client-side size validation.

**Inputs:**

| Field         | Value      |
|---------------|------------|
| size (bytes)  | 10485759   |

**Expected result:** `validateFileSize(10485759)` returns `null`.

---

### TC-CONTENT-U-012 — File size validator rejects 10485761 bytes (limit plus 1)

**Verifies:** REQ-FECONTENT-023, REQ-FECONTENT-113, REQ-FECONTENT-114

**Description:** A file whose size is one byte above the 10 MB limit must be rejected with the exact message "File too large. Maximum size is 10 MB."

**Inputs:**

| Field         | Value      |
|---------------|------------|
| size (bytes)  | 10485761   |

**Expected result:** `validateFileSize(10485761)` returns the string `"File too large. Maximum size is 10 MB."`.

---

### TC-CONTENT-U-013 — File size validator accepts exactly 10485760 bytes (boundary)

**Verifies:** REQ-FECONTENT-023, REQ-FECONTENT-113

**Description:** A file whose size equals the limit exactly must pass. The backend rejects files that exceed the limit, so the boundary value itself is valid.

**Inputs:**

| Field         | Value      |
|---------------|------------|
| size (bytes)  | 10485760   |

**Expected result:** `validateFileSize(10485760)` returns `null`.

---

### TC-CONTENT-U-014 — Letter grade calculator: 95 maps to A

**Verifies:** REQ-FECONTENT-032, REQ-FECONTENT-127

**Description:** A weighted score of 95 is above 90 and must produce the letter "A".

**Inputs:**

| Field          | Value |
|----------------|-------|
| weighted_score | 95    |

**Expected result:** `letterGrade(95)` returns `"A"`.

---

### TC-CONTENT-U-015 — Letter grade calculator: 90 maps to A (boundary)

**Verifies:** REQ-FECONTENT-032, REQ-FECONTENT-127

**Description:** The threshold for A is inclusive at 90. A score of exactly 90 must return "A".

**Inputs:**

| Field          | Value |
|----------------|-------|
| weighted_score | 90    |

**Expected result:** `letterGrade(90)` returns `"A"`.

---

### TC-CONTENT-U-016 — Letter grade calculator: 85 maps to B

**Verifies:** REQ-FECONTENT-032, REQ-FECONTENT-128

**Description:** A weighted score of 85 falls in the 80–89 range and must produce "B".

**Inputs:**

| Field          | Value |
|----------------|-------|
| weighted_score | 85    |

**Expected result:** `letterGrade(85)` returns `"B"`.

---

### TC-CONTENT-U-017 — Letter grade calculator: 80 maps to B (boundary)

**Verifies:** REQ-FECONTENT-032, REQ-FECONTENT-128

**Description:** The lower bound of B is inclusive at 80.

**Inputs:**

| Field          | Value |
|----------------|-------|
| weighted_score | 80    |

**Expected result:** `letterGrade(80)` returns `"B"`.

---

### TC-CONTENT-U-018 — Letter grade calculator: 89 maps to B (upper boundary)

**Verifies:** REQ-FECONTENT-032, REQ-FECONTENT-128

**Description:** A score of 89 is below the A threshold and must produce "B".

**Inputs:**

| Field          | Value |
|----------------|-------|
| weighted_score | 89    |

**Expected result:** `letterGrade(89)` returns `"B"`.

---

### TC-CONTENT-U-019 — Letter grade calculator: 75 maps to C

**Verifies:** REQ-FECONTENT-032, REQ-FECONTENT-129

**Description:** A weighted score of 75 falls in the 70–79 range and must produce "C".

**Inputs:**

| Field          | Value |
|----------------|-------|
| weighted_score | 75    |

**Expected result:** `letterGrade(75)` returns `"C"`.

---

### TC-CONTENT-U-020 — Letter grade calculator: 65 maps to D

**Verifies:** REQ-FECONTENT-032, REQ-FECONTENT-130

**Description:** A weighted score of 65 falls in the 60–69 range and must produce "D".

**Inputs:**

| Field          | Value |
|----------------|-------|
| weighted_score | 65    |

**Expected result:** `letterGrade(65)` returns `"D"`.

---

### TC-CONTENT-U-021 — Letter grade calculator: 55 maps to F

**Verifies:** REQ-FECONTENT-032, REQ-FECONTENT-131

**Description:** A weighted score of 55 is below 60 and must produce "F".

**Inputs:**

| Field          | Value |
|----------------|-------|
| weighted_score | 55    |

**Expected result:** `letterGrade(55)` returns `"F"`.

---

### TC-CONTENT-U-022 — Letter grade calculator: 0 maps to F

**Verifies:** REQ-FECONTENT-032, REQ-FECONTENT-131

**Description:** A zero score is below 60 and must produce "F".

**Inputs:**

| Field          | Value |
|----------------|-------|
| weighted_score | 0     |

**Expected result:** `letterGrade(0)` returns `"F"`.

---

### TC-CONTENT-U-023 — Grade formatter: 87.3 formats as "87.3%" and "B"

**Verifies:** REQ-FECONTENT-033, REQ-FECONTENT-128

**Description:** The grade formatter must produce the percentage string and the correct letter grade together.

**Inputs:**

| Field          | Value |
|----------------|-------|
| weighted_score | 87.3  |

**Expected result:** `formatGrade(87.3)` returns `{ percentage: "87.3%", letter: "B" }`.

---

### TC-CONTENT-U-024 — Notification unread check: absent `read_at` key is unread

**Verifies:** REQ-FECONTENT-050, REQ-FECONTENT-150

**Description:** When a notification object has no `read_at` property at all, it must be counted as unread.

**Inputs:**

```json
{ "id": "abc", "type": "api_failure", "message": "err", "created_at": "2026-01-01T00:00:00Z" }
```

**Expected result:** `isUnread(notification)` returns `true`.

---

### TC-CONTENT-U-025 — Notification unread check: `read_at: null` is unread

**Verifies:** REQ-FECONTENT-050, REQ-FECONTENT-150

**Description:** When `read_at` is explicitly present as `null`, the notification must still be counted as unread.

**Inputs:**

```json
{ "id": "abc", "type": "api_failure", "message": "err", "read_at": null, "created_at": "2026-01-01T00:00:00Z" }
```

**Expected result:** `isUnread(notification)` returns `true`.

---

### TC-CONTENT-U-026 — Notification unread check: non-null `read_at` is read

**Verifies:** REQ-FECONTENT-050, REQ-FECONTENT-150

**Description:** When `read_at` contains a timestamp string, the notification must be considered read.

**Inputs:**

```json
{ "id": "abc", "type": "api_failure", "message": "err", "read_at": "2026-01-02T10:00:00Z", "created_at": "2026-01-01T00:00:00Z" }
```

**Expected result:** `isUnread(notification)` returns `false`.

---

### TC-CONTENT-U-027 — Unread count from mixed notification array

**Verifies:** REQ-FECONTENT-051, REQ-FECONTENT-150, REQ-FECONTENT-167

**Description:** Given a list of three notifications where two have null/absent `read_at` and one has a timestamp, the computed unread count must be 2.

**Inputs:**

```json
[
  { "id": "n1", "type": "api_failure", "message": "a", "created_at": "2026-01-01T00:00:00Z" },
  { "id": "n2", "type": "admin_escalation", "message": "b", "read_at": "2026-01-02T00:00:00Z", "created_at": "2026-01-01T00:00:00Z" },
  { "id": "n3", "type": "generation_timeout", "message": "c", "read_at": null, "created_at": "2026-01-01T00:00:00Z" }
]
```

**Expected result:** `countUnread(notifications)` returns `2`.

---

### TC-CONTENT-U-028 — Section navigation: previous disabled at index 0

**Verifies:** REQ-FECONTENT-092, REQ-FECONTENT-166

**Description:** When `sectionIndex` equals 0, the `isPreviousDisabled` derived value must be `true`.

**Inputs:**

| Field          | Value |
|----------------|-------|
| sectionIndex   | 0     |

**Expected result:** `isPreviousDisabled(0)` returns `true`.

---

### TC-CONTENT-U-029 — Section navigation: previous enabled at index 1

**Verifies:** REQ-FECONTENT-092, REQ-FECONTENT-166

**Description:** When `sectionIndex` is greater than 0, the previous button must not be disabled.

**Inputs:**

| Field          | Value |
|----------------|-------|
| sectionIndex   | 1     |

**Expected result:** `isPreviousDisabled(1)` returns `false`.

---

### TC-CONTENT-U-030 — Section navigation: next disabled at last index

**Verifies:** REQ-FECONTENT-182

**Description:** When `sectionIndex` equals `sectionCount - 1`, the next button must be disabled.

**Inputs:**

| Field          | Value |
|----------------|-------|
| sectionIndex   | 4     |
| sectionCount   | 5     |

**Expected result:** `isNextDisabled(4, 5)` returns `true`.

---

### TC-CONTENT-U-031 — Section navigation: next enabled when not at last index

**Verifies:** REQ-FECONTENT-182

**Description:** When `sectionIndex` is less than `sectionCount - 1`, the next button must be enabled.

**Inputs:**

| Field          | Value |
|----------------|-------|
| sectionIndex   | 3     |
| sectionCount   | 5     |

**Expected result:** `isNextDisabled(3, 5)` returns `false`.

---

### TC-CONTENT-U-032 — Feedback text required validation: empty string

**Verifies:** REQ-FECONTENT-075

**Description:** An empty `feedback_text` string must produce a validation error before any network request.

**Inputs:**

| Field         | Value |
|---------------|-------|
| feedback_text | `""`  |

**Expected result:** `validateFeedback("")` returns a non-null error string.

---

### TC-CONTENT-U-033 — Feedback text maximum length validation: 2000 characters accepted

**Verifies:** REQ-FECONTENT-074, REQ-FECONTENT-163

**Description:** A `feedback_text` of exactly 2000 characters must pass validation (boundary value).

**Inputs:**

| Field         | Value                             |
|---------------|-----------------------------------|
| feedback_text | string of exactly 2000 characters |

**Expected result:** `validateFeedback(twoThousandChars)` returns `null`.

---

### TC-CONTENT-U-034 — Feedback text maximum length validation: 2001 characters rejected

**Verifies:** REQ-FECONTENT-074, REQ-FECONTENT-163

**Description:** A `feedback_text` of 2001 characters must produce a validation error.

**Inputs:**

| Field         | Value                             |
|---------------|-----------------------------------|
| feedback_text | string of exactly 2001 characters |

**Expected result:** `validateFeedback(twoThousandOneChars)` returns a non-null error string.

---

## Tier 2 — Component Integration Tests (Vitest + Vue Test Utils)

These tests mount real Vue components against a mocked API layer. No real HTTP requests are made; global `fetch` is replaced with a `vi.fn()` mock. No database is accessed.

---

### TC-CONTENT-I-001 — Section reader: HTTP 200 renders content

**Verifies:** REQ-FECONTENT-010, REQ-FECONTENT-011, REQ-FECONTENT-012, REQ-FECONTENT-013, REQ-FECONTENT-014, REQ-FECONTENT-015, REQ-FECONTENT-100, REQ-FECONTENT-101, REQ-FECONTENT-102, REQ-FECONTENT-103, REQ-FECONTENT-104, REQ-FECONTENT-105, REQ-FECONTENT-106, REQ-FECONTENT-107

**Setup:** Mock `fetch` to resolve with status 200 and body:
```json
{
  "id": "sec-1",
  "course_id": "crs-1",
  "section_index": 2,
  "title": "Introduction to Algebra",
  "content_adoc": "= Introduction\nsome content",
  "version": 3,
  "citation_verified": true,
  "created_at": "2026-01-01T00:00:00Z"
}
```

**Steps:**

1. Mount `SectionReaderView` with props `{ courseId: "crs-1", sectionIndex: 2 }` and a token in a Pinia store or provide-inject.
2. Await the next tick.

**Expected results (one assertion per field):**

| Assertion | Expected |
|-----------|----------|
| Section title visible | `"Introduction to Algebra"` appears in DOM |
| Section index visible | `"2"` appears in DOM |
| AsciiDoc content in preformatted block | `<pre>` or `<code>` element contains `"= Introduction\nsome content"` |
| Citation verified indicator | Element indicating `true` is visible |
| Version label | `"3"` appears in DOM |
| `created_at` value bound to state | No assertion required on DOM if field is used internally, but component state must hold `"2026-01-01T00:00:00Z"` |

---

### TC-CONTENT-I-002 — Section reader: HTTP 404 shows error state

**Verifies:** REQ-FECONTENT-016, REQ-FECONTENT-108, REQ-FECONTENT-180

**Setup:** Mock `fetch` to resolve with status 404.

**Steps:**

1. Mount `SectionReaderView` with props `{ courseId: "crs-1", sectionIndex: 99 }`.
2. Await the next tick.

**Expected result:** DOM contains the text "Section not found" and content area is not rendered.

---

### TC-CONTENT-I-003 — Section reader: HTTP 202 shows pending review state

**Verifies:** REQ-FECONTENT-017, REQ-FECONTENT-109

**Setup:** Mock `fetch` to resolve with status 202 and body:
```json
{ "status": "pending_review", "message": "content is being reviewed and will be available shortly" }
```

**Steps:**

1. Mount `SectionReaderView`.
2. Await the next tick.

**Expected result:** DOM contains a pending-review message (content of `message` field or a fixed phrase) and the AsciiDoc block is not rendered.

---

### TC-CONTENT-I-004 — Section reader: HTTP 500 shows generic error

**Verifies:** REQ-FECONTENT-016, REQ-FECONTENT-110, REQ-FECONTENT-180

**Setup:** Mock `fetch` to resolve with status 500.

**Steps:**

1. Mount `SectionReaderView`.
2. Await the next tick.

**Expected result:** DOM contains a generic error message; no content block is rendered.

---

### TC-CONTENT-I-005 — Section reader: network failure shows error state

**Verifies:** REQ-FECONTENT-016, REQ-FECONTENT-180

**Setup:** Mock `fetch` to `reject` with `new TypeError("Failed to fetch")`.

**Steps:**

1. Mount `SectionReaderView`.
2. Await the next tick.

**Expected result:** DOM contains an error message; the loading indicator is not shown.

---

### TC-CONTENT-I-006 — Feedback modal: submit sends POST with correct JSON body

**Verifies:** REQ-FECONTENT-071, REQ-FECONTENT-160, REQ-FECONTENT-161, REQ-FECONTENT-162, REQ-FECONTENT-181

**Setup:** Mock `fetch` to resolve with status 201 on the first POST call.

**Steps:**

1. Mount the feedback modal component (or `SectionReaderView` with a section already loaded).
2. Click the "Submit feedback" button to open the modal.
3. Set the textarea value to `"Please add more examples"`.
4. Click the submit button.
5. Capture the first `fetch` call arguments.

**Expected results:**

| Assertion | Expected |
|-----------|----------|
| POST body key | Captured request body, when parsed as JSON, contains exactly the key `"feedback_text"` (not `"text"` or `"feedback"`) |
| POST body value | `feedback_text` equals `"Please add more examples"` |
| Content-Type header | Request headers include `Content-Type: application/json` |
| Modal closed | Modal element is no longer visible after 201 |
| Success message | DOM contains the text `"Feedback submitted."` |

---

### TC-CONTENT-I-007 — Feedback modal: non-201 response keeps modal open with error

**Verifies:** REQ-FECONTENT-073

**Setup:** Mock `fetch` to resolve with status 500.

**Steps:**

1. Open the feedback modal.
2. Enter text and submit.
3. Await response.

**Expected results:** Modal remains visible; an error message is displayed inside the modal.

---

### TC-CONTENT-I-008 — Feedback modal: empty text blocked before POST

**Verifies:** REQ-FECONTENT-075

**Setup:** No `fetch` mock needed (request must not be made).

**Steps:**

1. Open the feedback modal.
2. Leave the textarea empty.
3. Click submit.

**Expected results:** `fetch` is never called; modal remains open; an error message about required input is visible.

---

### TC-CONTENT-I-009 — Feedback modal: text over 2000 characters blocked before POST

**Verifies:** REQ-FECONTENT-074

**Setup:** No `fetch` mock needed.

**Steps:**

1. Open the feedback modal.
2. Set textarea content to a 2001-character string.
3. Click submit.

**Expected results:** `fetch` is never called; modal remains open; a character limit error message is visible.

---

### TC-CONTENT-I-010 — Export PDF: fetch triggered with Authorization header

**Verifies:** REQ-FECONTENT-060, REQ-FECONTENT-157

**Setup:** Mock `fetch` to resolve with status 200 and a Blob body. Mock `URL.createObjectURL` and `document.createElement`.

**Steps:**

1. Mount `SectionReaderView` with a loaded section (courseId `crs-1`, sectionIndex `2`).
2. Click the "Download PDF" button.
3. Capture the `fetch` call.

**Expected results:**

| Assertion | Expected |
|-----------|----------|
| URL | Fetch called with `/api/v1/courses/crs-1/content/2/export?format=pdf` |
| Authorization header | Request headers include `Authorization: Bearer <token>` |

---

### TC-CONTENT-I-011 — Export PDF: blob downloaded programmatically

**Verifies:** REQ-FECONTENT-159, REQ-FECONTENT-060

**Setup:** Mock `fetch` returning a successful Blob response. Spy on `URL.createObjectURL` and intercept the anchor click.

**Steps:**

1. Mount and click "Download PDF".
2. Await the async download handler.

**Expected results:** `URL.createObjectURL` was called with a `Blob` argument; a programmatic click on a temporary anchor element was triggered; no `<a href="...">` navigation to the export URL occurred directly.

---

### TC-CONTENT-I-012 — Export HTML: fetch triggered with correct URL and Authorization header

**Verifies:** REQ-FECONTENT-061, REQ-FECONTENT-158

**Setup:** Mock `fetch` to resolve with status 200.

**Steps:**

1. Mount `SectionReaderView` with a loaded section.
2. Click the "Download HTML" button.

**Expected result:** Fetch called with `/api/v1/courses/crs-1/content/2/export?format=html` and the `Authorization` header present.

---

### TC-CONTENT-I-013 — Export error: HTTP 202 shows pending message

**Verifies:** REQ-FECONTENT-062, REQ-FECONTENT-173

**Setup:** Mock `fetch` to resolve with status 202.

**Steps:**

1. Click "Download PDF".

**Expected result:** DOM displays a message indicating the export is pending citation review; no download occurs.

---

### TC-CONTENT-I-014 — Export error: HTTP 404 shows "Section not found."

**Verifies:** REQ-FECONTENT-062, REQ-FECONTENT-174

**Setup:** Mock `fetch` to resolve with status 404.

**Steps:**

1. Click "Download PDF".

**Expected result:** DOM contains `"Section not found."`.

---

### TC-CONTENT-I-015 — Submission: valid file selected — no client error shown

**Verifies:** REQ-FECONTENT-021, REQ-FECONTENT-022, REQ-FECONTENT-111

**Setup:** No fetch mock needed for validation check.

**Steps:**

1. Mount `SubmissionView` for a homework.
2. Simulate selecting a `.md` file via the file input.

**Expected result:** No client-side validation error is displayed; the submit button is enabled.

---

### TC-CONTENT-I-016 — Submission: unsupported format rejected before upload

**Verifies:** REQ-FECONTENT-022, REQ-FECONTENT-112

**Setup:** Spy on `fetch`; it must never be called.

**Steps:**

1. Mount `SubmissionView`.
2. Simulate selecting a file named `solution.exe`.
3. Attempt to submit.

**Expected results:** `fetch` is not called; DOM contains a validation error message that names the accepted extensions.

---

### TC-CONTENT-I-017 — Submission: file size 10 MB + 1 byte rejected before upload

**Verifies:** REQ-FECONTENT-023, REQ-FECONTENT-113, REQ-FECONTENT-114

**Setup:** Spy on `fetch`; it must never be called.

**Steps:**

1. Mount `SubmissionView`.
2. Simulate selecting a file with `.md` extension and `size = 10485761`.
3. Attempt to submit.

**Expected results:** `fetch` is not called; DOM contains `"File too large. Maximum size is 10 MB."`.

---

### TC-CONTENT-I-018 — Submission: HTTP 201 shows success message

**Verifies:** REQ-FECONTENT-024, REQ-FECONTENT-026, REQ-FECONTENT-115, REQ-FECONTENT-175

**Setup:** Mock `fetch` (or XHR via `vi.fn` on `XMLHttpRequest`) to succeed with status 201 and body:
```json
{
  "id": "sub-1",
  "format": "markdown",
  "file_size_bytes": 1024,
  "submitted_at": "2026-05-01T10:00:00Z",
  "grading_status": "pending"
}
```

**Steps:**

1. Mount `SubmissionView`.
2. Select a valid `.md` file.
3. Click the upload button.
4. Await completion.

**Expected results:**

| Assertion | Expected |
|-----------|----------|
| Success message | `"Submission received."` visible |
| Latest submission format | `"markdown"` visible |
| Latest submission file_size_bytes | `"1024"` visible |
| Latest submission submitted_at | `"2026-05-01T10:00:00Z"` visible |
| Latest submission grading_status | `"pending"` visible |

---

### TC-CONTENT-I-019 — Submission: multipart field name is "file"

**Verifies:** REQ-FECONTENT-024, REQ-FECONTENT-115

**Setup:** Capture the `FormData` sent in the upload request.

**Steps:**

1. Mount `SubmissionView`.
2. Select a valid `.md` file named `work.md`.
3. Submit.
4. Inspect the `FormData` arguments passed to `XMLHttpRequest.send` (or `fetch`).

**Expected result:** `FormData.get("file")` returns the selected `File` object; no other field name is used.

---

### TC-CONTENT-I-020 — Submission: HTTP 413 shows "File too large" message

**Verifies:** REQ-FECONTENT-024, REQ-FECONTENT-116

**Setup:** Mock upload to respond with status 413.

**Steps:**

1. Select a valid file and submit.

**Expected result:** DOM contains `"File too large. Maximum size is 10 MB."`.

---

### TC-CONTENT-I-021 — Submission: HTTP 422 UNSUPPORTED_FORMAT shows format error

**Verifies:** REQ-FECONTENT-024, REQ-FECONTENT-117

**Setup:** Mock upload to respond with status 422 and body `{ "error": "UNSUPPORTED_FORMAT" }`.

**Steps:**

1. Select a file and submit.

**Expected result:** DOM contains a message indicating unsupported file format.

---

### TC-CONTENT-I-022 — Submission: HTTP 422 INVALID_CONTENT shows content mismatch error

**Verifies:** REQ-FECONTENT-024, REQ-FECONTENT-118

**Setup:** Mock upload to respond with status 422 and body `{ "error": "INVALID_CONTENT" }`.

**Steps:**

1. Select a file and submit.

**Expected result:** DOM contains a message indicating the file content does not match the declared format.

---

### TC-CONTENT-I-023 — Submission: HTTP 409 shows already-submitted message

**Verifies:** REQ-FECONTENT-024, REQ-FECONTENT-119

**Setup:** Mock upload to respond with status 409.

**Steps:**

1. Select a file and submit.

**Expected result:** DOM contains `"You have already submitted for this homework."`.

---

### TC-CONTENT-I-024 — Submission: unhandled HTTP error shows generic fallback

**Verifies:** REQ-FECONTENT-081, REQ-FECONTENT-120

**Setup:** Mock upload to respond with status 500.

**Steps:**

1. Select a file and submit.

**Expected result:** DOM contains `"Submission failed. Please try again."`.

---

### TC-CONTENT-I-025 — Latest submission fetched on mount — fields rendered

**Verifies:** REQ-FECONTENT-027, REQ-FECONTENT-028, REQ-FECONTENT-121, REQ-FECONTENT-122, REQ-FECONTENT-123, REQ-FECONTENT-124

**Setup:** Mock the latest submission GET endpoint to return 200 with:
```json
{
  "id": "sub-99",
  "format": "asciidoc",
  "file_size_bytes": 2048,
  "submitted_at": "2026-04-30T08:00:00Z",
  "grading_status": "graded"
}
```

**Steps:**

1. Mount `SubmissionView`.
2. Await mount fetch.

**Expected results:** All four fields (`format`, `file_size_bytes`, `submitted_at`, `grading_status`) are visible in the DOM.

---

### TC-CONTENT-I-026 — Latest submission 404 shows no-prior-submission state

**Verifies:** REQ-FECONTENT-029

**Setup:** Mock GET latest submission to return 404.

**Steps:**

1. Mount `SubmissionView`.
2. Await mount fetch.

**Expected result:** DOM contains a "no prior submission" message; no submission detail fields are displayed.

---

### TC-CONTENT-I-027 — Grades view: weighted score and letter grade rendered

**Verifies:** REQ-FECONTENT-030, REQ-FECONTENT-031, REQ-FECONTENT-032, REQ-FECONTENT-033, REQ-FECONTENT-125, REQ-FECONTENT-126

**Setup:** Mock GET `/api/v1/courses/{id}/grades` to return 200 with:
```json
{
  "course_id": "crs-1",
  "student_id": "stu-1",
  "weighted_score": 87.3,
  "total_weight": 100
}
```

**Steps:**

1. Mount `GradesView` with `courseId: "crs-1"`.
2. Await fetch.

**Expected results:**

| Assertion | Expected |
|-----------|----------|
| Weighted score percentage | `"87.3%"` visible |
| Letter grade | `"B"` visible |
| Total weight | `"100"` visible |

---

### TC-CONTENT-I-028 — Grades view: per-homework grade fields rendered

**Verifies:** REQ-FECONTENT-034, REQ-FECONTENT-035, REQ-FECONTENT-132, REQ-FECONTENT-133, REQ-FECONTENT-134, REQ-FECONTENT-135, REQ-FECONTENT-136, REQ-FECONTENT-176, REQ-FECONTENT-177

**Setup:** Mock GET per-homework grade to return 200 with:
```json
{
  "id": "gr-1",
  "submission_id": "sub-1",
  "homework_id": "hw-1",
  "student_id": "stu-1",
  "course_id": "crs-1",
  "raw_score": 90,
  "late_days": 1,
  "late_penalty_rate": 5,
  "late_penalty_amount": 5,
  "badge_waiver_applied": false,
  "badge_improvement": 0,
  "final_score": 85,
  "graded_at": "2026-04-30T09:00:00Z"
}
```

**Steps:**

1. Mount the per-homework grade component/row.
2. Await fetch.

**Expected results:** All of `raw_score`, `late_penalty_amount`, `final_score`, `badge_waiver_applied`, `graded_at`, `late_days`, and `badge_improvement` are visible in the DOM.

---

### TC-CONTENT-I-029 — Grades view: per-homework 404 shows "Not yet graded"

**Verifies:** REQ-FECONTENT-036, REQ-FECONTENT-137

**Setup:** Mock GET per-homework grade to return 404.

**Steps:**

1. Mount the per-homework grade component.

**Expected result:** DOM contains `"Not yet graded"`.

---

### TC-CONTENT-I-030 — Grades view: no grades empty state

**Verifies:** REQ-FECONTENT-036, REQ-FECONTENT-169

**Setup:** Mock GET course grade to return `{ "weighted_score": 0, "total_weight": 0 }`.

**Steps:**

1. Mount `GradesView`.
2. Await fetch.

**Expected result:** DOM contains `"No grades yet."`.

---

### TC-CONTENT-I-031 — Grades fetch error state

**Verifies:** REQ-FECONTENT-082

**Setup:** Mock GET course grade to return 500.

**Steps:**

1. Mount `GradesView`.
2. Await fetch.

**Expected result:** DOM contains an error message; grade fields are not rendered.

---

### TC-CONTENT-I-032 — Badges view: available badges rendered with definition fields

**Verifies:** REQ-FECONTENT-040, REQ-FECONTENT-042, REQ-FECONTENT-138, REQ-FECONTENT-139, REQ-FECONTENT-140, REQ-FECONTENT-141, REQ-FECONTENT-142, REQ-FECONTENT-143

**Setup:** Mock GET `/api/v1/badges` to return 200 with:
```json
[
  {
    "id": "bdg-1",
    "name": "Perfect Score",
    "description": "Achieve 100% on any homework",
    "milestone": "perfect_score",
    "reward": "grade_improvement",
    "reward_value": 5,
    "created_at": "2026-01-01T00:00:00Z"
  }
]
```

**Steps:**

1. Mount `BadgesView`.
2. Await fetch.

**Expected results:** All of `name`, `description`, `milestone`, `reward`, and `reward_value` are visible in the DOM.

---

### TC-CONTENT-I-033 — Badges view: earned badges rendered with awarded_at and redeemed status

**Verifies:** REQ-FECONTENT-041, REQ-FECONTENT-043, REQ-FECONTENT-144, REQ-FECONTENT-145, REQ-FECONTENT-146, REQ-FECONTENT-170, REQ-FECONTENT-171

**Setup:** Mock GET `/api/v1/users/me/badges` to return 200 with:
```json
[
  {
    "id": "sbdg-1",
    "student_id": "stu-1",
    "badge_id": "bdg-1",
    "awarded_at": "2026-02-15T12:00:00Z",
    "redeemed_for_submission_id": "sub-42",
    "badge_name": "Perfect Score",
    "badge_reward": "grade_improvement",
    "badge_reward_value": 5
  }
]
```

**Steps:**

1. Mount `BadgesView` with both GET mocks active.
2. Await fetches.

**Expected results:**

| Assertion | Expected |
|-----------|----------|
| `awarded_at` displayed | `"2026-02-15T12:00:00Z"` visible |
| Redeemed indicator shown | DOM contains text or indicator for "Redeemed" |
| `badge_name` displayed | `"Perfect Score"` visible |
| `badge_reward` displayed | `"grade_improvement"` visible |
| `badge_reward_value` displayed | `"5"` visible |
| Cross-reference by `badge_id` | Earned badge card is matched to the definition with id `"bdg-1"` |

---

### TC-CONTENT-I-034 — Badges view: unearned badge shows milestone as unlock requirement

**Verifies:** REQ-FECONTENT-044, REQ-FECONTENT-147

**Setup:** Mock GET `/api/v1/badges` with one badge definition. Mock GET `/api/v1/users/me/badges` with an empty array.

**Steps:**

1. Mount `BadgesView`.
2. Await fetches.

**Expected result:** The badge card is rendered in a visually distinct (locked/greyed) state and the `milestone` value is displayed as the unlock requirement.

---

### TC-CONTENT-I-035 — Badges fetch error state

**Verifies:** REQ-FECONTENT-083

**Setup:** Mock GET `/api/v1/badges` to return 500.

**Steps:**

1. Mount `BadgesView`.
2. Await fetch.

**Expected result:** DOM contains an error message; no badge cards are rendered.

---

### TC-CONTENT-I-036 — Notifications: unread count badge shows 2

**Verifies:** REQ-FECONTENT-051, REQ-FECONTENT-150, REQ-FECONTENT-167

**Setup:** Mock GET `/api/v1/notifications?unread=true&limit=10` to return:
```json
{
  "notifications": [
    { "id": "n1", "type": "api_failure", "message": "err1", "created_at": "2026-01-01T00:00:00Z" },
    { "id": "n2", "type": "generation_timeout", "message": "err2", "read_at": null, "created_at": "2026-01-01T00:00:00Z" },
    { "id": "n3", "type": "admin_escalation", "message": "ok", "read_at": "2026-01-02T00:00:00Z", "created_at": "2026-01-01T00:00:00Z" }
  ]
}
```

**Steps:**

1. Mount the `NotificationsPanel` component with `vi.useFakeTimers()` so polling does not actually fire.
2. Await the mount-time fetch.

**Expected result:** The unread count badge element shows `2`.

---

### TC-CONTENT-I-037 — Notifications: count badge hidden when unread is zero

**Verifies:** REQ-FECONTENT-051, REQ-FECONTENT-167

**Setup:** Mock notifications endpoint to return:
```json
{ "notifications": [{ "id": "n1", "type": "api_failure", "message": "ok", "read_at": "2026-01-02T00:00:00Z", "created_at": "2026-01-01T00:00:00Z" }] }
```

**Steps:**

1. Mount `NotificationsPanel`.
2. Await fetch.

**Expected result:** The unread count badge element is hidden (not in DOM or has `display: none`).

---

### TC-CONTENT-I-038 — Notifications: notification fields displayed in dropdown

**Verifies:** REQ-FECONTENT-052, REQ-FECONTENT-053, REQ-FECONTENT-151, REQ-FECONTENT-152, REQ-FECONTENT-153, REQ-FECONTENT-178

**Setup:** Mock notifications endpoint to return a single notification with `type`, `message`, and `created_at` fields. Mock `JSON.parse` is not needed; rely on actual JSON parsing.

**Steps:**

1. Mount `NotificationsPanel`.
2. Await fetch.
3. Click the bell icon to expand the dropdown.

**Expected results:** `type`, `message`, and `created_at` values are all visible in the dropdown list.

---

### TC-CONTENT-I-039 — Notifications: click notification triggers mark-read POST

**Verifies:** REQ-FECONTENT-054, REQ-FECONTENT-154, REQ-FECONTENT-172

**Setup:**
- Mock GET notifications to return two unread notifications (`n1`, `n2`).
- Mock POST `/api/v1/notifications/n1/read` to return 200 with the notification updated (non-null `read_at`).

**Steps:**

1. Mount `NotificationsPanel`.
2. Await fetch.
3. Click the first notification item in the dropdown.
4. Await the POST response.

**Expected results:**

| Assertion | Expected |
|-----------|----------|
| POST called | `fetch` called with URL ending in `/notifications/n1/read` and method `POST` |
| Local state updated | Unread count decrements from 2 to 1 |

---

### TC-CONTENT-I-040 — Notifications: mark-read 404 silently ignored

**Verifies:** REQ-FECONTENT-054, REQ-FECONTENT-156

**Setup:** Mock POST mark-read to return 404.

**Steps:**

1. Click a notification item.
2. Await response.

**Expected result:** No error message is displayed; the component remains functional.

---

### TC-CONTENT-I-041 — Notifications: mark-read 403 shows error message

**Verifies:** REQ-FECONTENT-054, REQ-FECONTENT-172

**Setup:** Mock POST mark-read to return 403.

**Steps:**

1. Click a notification item.
2. Await response.

**Expected result:** DOM contains an error message indicating access was forbidden.

---

### TC-CONTENT-I-042 — Notifications: mark all read issues POST per unread notification

**Verifies:** REQ-FECONTENT-055, REQ-FECONTENT-155

**Setup:**
- Mock GET notifications to return three notifications: two unread (`n1`, `n2`) and one read (`n3`).
- Mock POST mark-read for `n1` and `n2` to return 200.

**Steps:**

1. Mount `NotificationsPanel`.
2. Await fetch.
3. Click "Mark all read".
4. Await all POSTs.

**Expected results:** POST was called exactly twice (once for `n1`, once for `n2`); POST was never called for `n3`.

---

### TC-CONTENT-I-043 — Notifications: empty state shown when no unread notifications

**Verifies:** REQ-FECONTENT-056

**Setup:** Mock GET notifications to return `{ "notifications": [] }`.

**Steps:**

1. Mount `NotificationsPanel`.
2. Await fetch.
3. Click bell icon.

**Expected result:** DOM contains `"No new notifications."`.

---

### TC-CONTENT-I-044 — Notifications: poll interval fires every 30 seconds

**Verifies:** REQ-FECONTENT-050, REQ-FECONTENT-148

**Setup:** Use `vi.useFakeTimers()`. Mock GET notifications. Count `fetch` calls.

**Steps:**

1. Mount `NotificationsPanel`. Fetch count = 1.
2. Advance fake timer by 30000 ms. Fetch count should become 2.
3. Advance timer by another 30000 ms. Fetch count should become 3.

**Expected result:** `fetch` is called once on mount and once per 30000 ms interval thereafter.

---

### TC-CONTENT-I-045 — Notifications: poll interval cleared on unmount

**Verifies:** REQ-FECONTENT-050, REQ-FECONTENT-149

**Setup:** Use `vi.useFakeTimers()`. Spy on `clearInterval`.

**Steps:**

1. Mount `NotificationsPanel`.
2. Unmount the component.
3. Advance timer by 30000 ms.

**Expected results:** `clearInterval` was called on unmount; `fetch` was not called again after unmount.

---

### TC-CONTENT-I-046 — Notifications: response envelope parsed from "notifications" key

**Verifies:** REQ-FECONTENT-050, REQ-FECONTENT-178

**Setup:** Mock GET notifications to return `{ "notifications": [{ "id": "n1", "type": "api_failure", "message": "m", "created_at": "2026-01-01T00:00:00Z" }] }`.

**Steps:**

1. Mount `NotificationsPanel`.
2. Await fetch.

**Expected result:** The notification with `type: "api_failure"` appears in the dropdown; the component did not attempt to iterate the root response object as an array.

---

### TC-CONTENT-I-047 — Section navigation: previous button decrements index

**Verifies:** REQ-FECONTENT-090, REQ-FECONTENT-164

**Setup:** Mock `fetch` for both the initial section (index 2) and the previous section (index 1).

**Steps:**

1. Mount `SectionReaderView` with `sectionIndex: 2`.
2. Await initial fetch.
3. Click "Previous section".
4. Capture the second `fetch` call URL.

**Expected result:** Second fetch URL ends in `/content/1` (index decremented from 2 to 1).

---

### TC-CONTENT-I-048 — Section navigation: next button increments index

**Verifies:** REQ-FECONTENT-091, REQ-FECONTENT-165

**Setup:** Mock `fetch` for sections at index 2 and 3.

**Steps:**

1. Mount `SectionReaderView` with `sectionIndex: 2`.
2. Click "Next section".

**Expected result:** Fetch URL ends in `/content/3`.

---

### TC-CONTENT-I-049 — Section navigation: previous button disabled at index 0

**Verifies:** REQ-FECONTENT-092, REQ-FECONTENT-166

**Setup:** Mock `fetch` returning a section with `section_index: 0`.

**Steps:**

1. Mount `SectionReaderView` at index 0.
2. Await fetch.

**Expected result:** The "Previous section" button element has the `disabled` attribute or `aria-disabled="true"`.

---

### TC-CONTENT-I-050 — Section navigation: next button disabled at last index

**Verifies:** REQ-FECONTENT-182

**Setup:** Mock `fetch` returning a section with `section_index` equal to the known last index (e.g., 4 of 5 total).

**Steps:**

1. Mount `SectionReaderView` with the last section index.
2. Await fetch.

**Expected result:** The "Next section" button element has the `disabled` attribute.

---

## Tier 3 — E2E Demonstration Procedure (Human-Executed)

**Prerequisites:**
- The full Valory stack is running via `docker compose up`.
- At least one user with the `student` role has been created.
- That student is enrolled in at least one active course with at least one section generated and citation-verified.
- The student's course has at least one homework assignment.

---

### TC-CONTENT-E-001 — Login as student with active course

**Verifies:** REQ-FECONTENT-001

**URL:** `http://localhost:5173/login`

**Button / action:** Enter student email and password; click "Sign in"

**Expected result:** Redirected to the dashboard or course hub; the student's name is shown in the navigation bar.

---

### TC-CONTENT-E-002 — Navigate to course hub — section list shown

**Verifies:** REQ-FECONTENT-001, REQ-FECONTENT-009

**URL:** `http://localhost:5173/courses/<courseId>`

**Button / action:** Click the course card from the dashboard

**Expected result:** A list of course sections is displayed with section titles and their sequential index numbers visible.

---

### TC-CONTENT-E-003 — Click first section — AsciiDoc content displayed

**Verifies:** REQ-FECONTENT-001, REQ-FECONTENT-010, REQ-FECONTENT-011, REQ-FECONTENT-012, REQ-FECONTENT-013, REQ-FECONTENT-014, REQ-FECONTENT-015

**URL:** `http://localhost:5173/courses/<courseId>/content/0`

**Button / action:** Click the first section in the list

**Expected result:** The section title, index (0), AsciiDoc content block (preformatted text), citation verified status, and version number are all visible.

---

### TC-CONTENT-E-004 — Submit section feedback — success message

**Verifies:** REQ-FECONTENT-007, REQ-FECONTENT-070, REQ-FECONTENT-071, REQ-FECONTENT-072, REQ-FECONTENT-073, REQ-FECONTENT-074, REQ-FECONTENT-075, REQ-FECONTENT-181

**URL:** Current section view

**Button / action:**
1. Click "Submit feedback"
2. Modal opens; type "Please add more examples" in the textarea
3. Click "Submit"

**Expected result:** Modal closes; page displays "Feedback submitted."

---

### TC-CONTENT-E-005 — Download PDF — file download triggered

**Verifies:** REQ-FECONTENT-006, REQ-FECONTENT-060, REQ-FECONTENT-157, REQ-FECONTENT-159

**URL:** Current section view

**Button / action:** Click "Download PDF"

**Expected result:** The browser initiates a file download for a PDF named `section-0.pdf`. The browser's download indicator activates. No navigation away from the section view occurs.

---

### TC-CONTENT-E-006 — Navigate to first homework — upload submission — success

**Verifies:** REQ-FECONTENT-002, REQ-FECONTENT-024, REQ-FECONTENT-025, REQ-FECONTENT-026

**URL:** `http://localhost:5173/courses/<courseId>/homework/<homeworkId>/submit`

**Button / action:**
1. Navigate to the homework submission view
2. Click "Upload submission"
3. Select a valid `.md` file from the local filesystem
4. Click "Submit"

**Expected result:** An upload progress indicator is shown during transfer. On completion, "Submission received." appears on screen. The latest submission panel shows the file format, file size, submission timestamp, and grading status.

---

### TC-CONTENT-E-007 — Upload `.exe` file — client-side error, no upload

**Verifies:** REQ-FECONTENT-022, REQ-FECONTENT-112

**URL:** Homework submission view

**Button / action:**
1. Click "Upload submission"
2. Select a `.exe` file
3. Attempt to click "Submit"

**Expected result:** A client-side format error message is shown immediately listing the accepted extensions. No upload request is made (verified by the browser's Network tab showing no POST request).

---

### TC-CONTENT-E-008 — Upload 15 MB file — client-side size error

**Verifies:** REQ-FECONTENT-023, REQ-FECONTENT-113, REQ-FECONTENT-114

**URL:** Homework submission view

**Button / action:**
1. Select a `.md` file larger than 10 MB (e.g., 15 MB)
2. Attempt to submit

**Expected result:** "File too large. Maximum size is 10 MB." is shown immediately. No upload request is made.

---

### TC-CONTENT-E-009 — Navigate to grades — overall grade shown

**Verifies:** REQ-FECONTENT-003, REQ-FECONTENT-030, REQ-FECONTENT-031, REQ-FECONTENT-032, REQ-FECONTENT-033

**URL:** `http://localhost:5173/courses/<courseId>/grades`

**Button / action:** Click the "Grades" link in the course navigation

**Expected result:** The overall weighted score (as a percentage) and the corresponding letter grade are both visible. If graded homework exists, individual homework grade rows are also shown with raw score, late penalty, and final score.

---

### TC-CONTENT-E-010 — Navigate to badges — available badges listed

**Verifies:** REQ-FECONTENT-004, REQ-FECONTENT-040, REQ-FECONTENT-041, REQ-FECONTENT-042, REQ-FECONTENT-043, REQ-FECONTENT-044

**URL:** `http://localhost:5173/badges`

**Button / action:** Click the "Badges" link in the navigation bar

**Expected result:** All available badge definitions are displayed with name, description, milestone, reward, and reward value. Earned badges show `awarded_at` and redeemed status. Unearned badges are visually distinct (greyed out) and display the milestone requirement.

---

## Tier 4 — Inspection Checklist

These items must be verified by code review of the Vue component source files. They cannot be tested automatically without brittle DOM or implementation coupling.

---

### TC-CONTENT-X-001 — Export download uses Fetch + Blob + `URL.createObjectURL` + programmatic click

**Verifies:** REQ-FECONTENT-006, REQ-FECONTENT-060, REQ-FECONTENT-061, REQ-FECONTENT-159

**Location:** Export handler in the section reader component (e.g., `SectionReaderView.vue` or a composable it uses)

**Pass criteria:**

1. The export function calls `fetch(url, { headers: { Authorization: ... } })`.
2. On a successful response, it calls `await response.blob()` to convert the body.
3. It calls `URL.createObjectURL(blob)` to produce a temporary object URL.
4. It creates a temporary `<a>` element, sets its `href` to the object URL and its `download` attribute, appends it to the document, calls `.click()` on it, and then removes it.
5. There is no `<a href="/api/v1/courses/.../export?format=...">` anchor in the component template that the user could click directly — such a bare link would omit the `Authorization` header.

**Fail criteria:** Any use of `window.location.href = ...` or an `<a>` tag whose `href` points directly to the export URL without the authenticated fetch intermediary.

---

### TC-CONTENT-X-002 — Upload uses XMLHttpRequest with upload ProgressEvent, not plain fetch

**Verifies:** REQ-FECONTENT-025, REQ-FECONTENT-168

**Location:** Submission upload function in `SubmissionView.vue` or its composable

**Pass criteria:**

1. Upload is implemented using `new XMLHttpRequest()`.
2. The code attaches a handler to `xhr.upload.addEventListener("progress", handler)` or `xhr.upload.onprogress = handler`.
3. The handler uses `event.loaded` and `event.total` to compute progress percentage.
4. The progress value is bound to a reactive variable that drives the progress indicator in the template.

**Fail criteria:** Upload implemented exclusively with `fetch()` and no `ReadableStream` body progress tracking; or progress UI driven by a static spinner with no byte-level reporting.

---

### TC-CONTENT-X-003 — Notification poll interval is 30000 ms and cleared on unmount

**Verifies:** REQ-FECONTENT-050, REQ-FECONTENT-148, REQ-FECONTENT-149

**Location:** `NotificationsPanel.vue` (or equivalent) `onMounted` / `onUnmounted` hooks

**Pass criteria:**

1. `setInterval(fetchNotifications, 30000)` (or equivalent with the numeric literal `30000`) is called in `onMounted`.
2. The return value of `setInterval` is stored in a variable accessible to the unmount hook.
3. `clearInterval(intervalId)` is called inside `onUnmounted` using that stored variable.
4. The constant `30000` is used, not `3000` or `60000`.

**Fail criteria:** Interval interval is not 30000; `clearInterval` is not called in `onUnmounted`; the interval ID is not stored and `clearInterval` cannot be invoked.

---

### TC-CONTENT-X-004 — `feedback_text` is the exact JSON key in the feedback POST body

**Verifies:** REQ-FECONTENT-071, REQ-FECONTENT-160

**Location:** Feedback submit handler in `SectionReaderView.vue` or feedback modal component

**Pass criteria:**

1. The POST body is serialized as `JSON.stringify({ feedback_text: feedbackValue })`.
2. The key is `feedback_text` — not `text`, `feedback`, `feedbackText`, or any camelCase variant.
3. The `Content-Type` header is set to `application/json`.

**Fail criteria:** Any other key name is used; the body is not JSON (e.g., `FormData`).

---

### TC-CONTENT-X-005 — `read_at` is checked for null-or-absent, not strict `=== null` only

**Verifies:** REQ-FECONTENT-050, REQ-FECONTENT-150

**Location:** Unread count computation in `NotificationsPanel.vue` or a shared utility

**Pass criteria:**

The unread filter uses a check of the form `notification.read_at == null` (loose equality) **or** `!notification.read_at` **or** an explicit `notification.read_at === null || notification.read_at === undefined`. Any of these forms correctly handles both the absent-key case and the explicit-null case.

**Fail criteria:** The check uses only `notification.read_at === null` (strict equality) without also handling the absent/undefined case.

---

## Requirement Coverage Matrix

| Requirement ID | Covered by |
|---|---|
| REQ-FECONTENT-001 | TC-CONTENT-I-001, TC-CONTENT-I-002, TC-CONTENT-I-003, TC-CONTENT-I-004, TC-CONTENT-I-005, TC-CONTENT-E-001, TC-CONTENT-E-002, TC-CONTENT-E-003 |
| REQ-FECONTENT-002 | TC-CONTENT-I-015 – TC-CONTENT-I-026, TC-CONTENT-E-006, TC-CONTENT-E-007, TC-CONTENT-E-008 |
| REQ-FECONTENT-003 | TC-CONTENT-I-027 – TC-CONTENT-I-031, TC-CONTENT-E-009 |
| REQ-FECONTENT-004 | TC-CONTENT-I-032 – TC-CONTENT-I-035, TC-CONTENT-E-010 |
| REQ-FECONTENT-005 | TC-CONTENT-I-036 – TC-CONTENT-I-046, TC-CONTENT-X-003 |
| REQ-FECONTENT-006 | TC-CONTENT-I-010 – TC-CONTENT-I-014, TC-CONTENT-E-005, TC-CONTENT-X-001 |
| REQ-FECONTENT-007 | TC-CONTENT-I-006 – TC-CONTENT-I-009, TC-CONTENT-E-004 |
| REQ-FECONTENT-008 | TC-CONTENT-I-002, TC-CONTENT-I-004, TC-CONTENT-I-024, TC-CONTENT-I-031, TC-CONTENT-I-035 |
| REQ-FECONTENT-009 | TC-CONTENT-I-047 – TC-CONTENT-I-050, TC-CONTENT-E-002 |
| REQ-FECONTENT-010 | TC-CONTENT-I-001 |
| REQ-FECONTENT-011 | TC-CONTENT-I-001, TC-CONTENT-E-003 |
| REQ-FECONTENT-012 | TC-CONTENT-I-001, TC-CONTENT-E-003 |
| REQ-FECONTENT-013 | TC-CONTENT-I-001, TC-CONTENT-E-003 |
| REQ-FECONTENT-014 | TC-CONTENT-I-001, TC-CONTENT-E-003 |
| REQ-FECONTENT-015 | TC-CONTENT-I-001, TC-CONTENT-E-003 |
| REQ-FECONTENT-016 | TC-CONTENT-I-002, TC-CONTENT-I-004, TC-CONTENT-I-005 |
| REQ-FECONTENT-017 | TC-CONTENT-I-003 |
| REQ-FECONTENT-018 | TC-CONTENT-E-003 |
| REQ-FECONTENT-020 | TC-CONTENT-E-006 |
| REQ-FECONTENT-021 | TC-CONTENT-U-007, TC-CONTENT-I-015, TC-CONTENT-X-001 |
| REQ-FECONTENT-022 | TC-CONTENT-U-001 – TC-CONTENT-U-010, TC-CONTENT-I-016, TC-CONTENT-E-007 |
| REQ-FECONTENT-023 | TC-CONTENT-U-011 – TC-CONTENT-U-013, TC-CONTENT-I-017, TC-CONTENT-E-008 |
| REQ-FECONTENT-024 | TC-CONTENT-I-018 – TC-CONTENT-I-023 |
| REQ-FECONTENT-025 | TC-CONTENT-E-006, TC-CONTENT-X-002 |
| REQ-FECONTENT-026 | TC-CONTENT-I-018, TC-CONTENT-E-006 |
| REQ-FECONTENT-027 | TC-CONTENT-I-025 |
| REQ-FECONTENT-028 | TC-CONTENT-I-025 |
| REQ-FECONTENT-029 | TC-CONTENT-I-026 |
| REQ-FECONTENT-030 | TC-CONTENT-I-027, TC-CONTENT-E-009 |
| REQ-FECONTENT-031 | TC-CONTENT-I-027, TC-CONTENT-E-009 |
| REQ-FECONTENT-032 | TC-CONTENT-U-014 – TC-CONTENT-U-022, TC-CONTENT-I-027, TC-CONTENT-E-009 |
| REQ-FECONTENT-033 | TC-CONTENT-U-023, TC-CONTENT-I-027, TC-CONTENT-E-009 |
| REQ-FECONTENT-034 | TC-CONTENT-I-028 |
| REQ-FECONTENT-035 | TC-CONTENT-I-028 |
| REQ-FECONTENT-036 | TC-CONTENT-I-029, TC-CONTENT-I-030 |
| REQ-FECONTENT-040 | TC-CONTENT-I-032, TC-CONTENT-E-010 |
| REQ-FECONTENT-041 | TC-CONTENT-I-033, TC-CONTENT-E-010 |
| REQ-FECONTENT-042 | TC-CONTENT-I-032, TC-CONTENT-E-010 |
| REQ-FECONTENT-043 | TC-CONTENT-I-033, TC-CONTENT-E-010 |
| REQ-FECONTENT-044 | TC-CONTENT-I-034, TC-CONTENT-E-010 |
| REQ-FECONTENT-050 | TC-CONTENT-I-044, TC-CONTENT-I-045, TC-CONTENT-I-046, TC-CONTENT-X-003, TC-CONTENT-X-005 |
| REQ-FECONTENT-051 | TC-CONTENT-U-027, TC-CONTENT-I-036, TC-CONTENT-I-037 |
| REQ-FECONTENT-052 | TC-CONTENT-I-038 |
| REQ-FECONTENT-053 | TC-CONTENT-I-038 |
| REQ-FECONTENT-054 | TC-CONTENT-I-039, TC-CONTENT-I-040, TC-CONTENT-I-041 |
| REQ-FECONTENT-055 | TC-CONTENT-I-042 |
| REQ-FECONTENT-056 | TC-CONTENT-I-043 |
| REQ-FECONTENT-060 | TC-CONTENT-I-010, TC-CONTENT-I-011, TC-CONTENT-E-005, TC-CONTENT-X-001 |
| REQ-FECONTENT-061 | TC-CONTENT-I-012, TC-CONTENT-X-001 |
| REQ-FECONTENT-062 | TC-CONTENT-I-013, TC-CONTENT-I-014 |
| REQ-FECONTENT-070 | TC-CONTENT-I-006, TC-CONTENT-E-004 |
| REQ-FECONTENT-071 | TC-CONTENT-I-006, TC-CONTENT-X-004 |
| REQ-FECONTENT-072 | TC-CONTENT-I-006 |
| REQ-FECONTENT-073 | TC-CONTENT-I-007 |
| REQ-FECONTENT-074 | TC-CONTENT-U-033, TC-CONTENT-U-034, TC-CONTENT-I-009 |
| REQ-FECONTENT-075 | TC-CONTENT-U-032, TC-CONTENT-I-008 |
| REQ-FECONTENT-080 | TC-CONTENT-E-003 |
| REQ-FECONTENT-081 | TC-CONTENT-I-024 |
| REQ-FECONTENT-082 | TC-CONTENT-I-031 |
| REQ-FECONTENT-083 | TC-CONTENT-I-035 |
| REQ-FECONTENT-090 | TC-CONTENT-I-047, TC-CONTENT-E-002 |
| REQ-FECONTENT-091 | TC-CONTENT-I-048 |
| REQ-FECONTENT-092 | TC-CONTENT-U-028, TC-CONTENT-U-029, TC-CONTENT-I-049 |
| REQ-FECONTENT-100 | TC-CONTENT-I-001 |
| REQ-FECONTENT-101 | TC-CONTENT-I-001 |
| REQ-FECONTENT-102 | TC-CONTENT-I-001 |
| REQ-FECONTENT-103 | TC-CONTENT-I-001 |
| REQ-FECONTENT-104 | TC-CONTENT-I-001 |
| REQ-FECONTENT-105 | TC-CONTENT-I-001 |
| REQ-FECONTENT-106 | TC-CONTENT-I-001 |
| REQ-FECONTENT-107 | TC-CONTENT-I-001 |
| REQ-FECONTENT-108 | TC-CONTENT-I-002 |
| REQ-FECONTENT-109 | TC-CONTENT-I-003 |
| REQ-FECONTENT-110 | TC-CONTENT-I-004 |
| REQ-FECONTENT-111 | TC-CONTENT-U-007, TC-CONTENT-I-015 |
| REQ-FECONTENT-112 | TC-CONTENT-U-001 – TC-CONTENT-U-010, TC-CONTENT-I-016 |
| REQ-FECONTENT-113 | TC-CONTENT-U-011 – TC-CONTENT-U-013, TC-CONTENT-I-017 |
| REQ-FECONTENT-114 | TC-CONTENT-U-012, TC-CONTENT-I-017 |
| REQ-FECONTENT-115 | TC-CONTENT-I-019 |
| REQ-FECONTENT-116 | TC-CONTENT-I-020 |
| REQ-FECONTENT-117 | TC-CONTENT-I-021 |
| REQ-FECONTENT-118 | TC-CONTENT-I-022 |
| REQ-FECONTENT-119 | TC-CONTENT-I-023 |
| REQ-FECONTENT-120 | TC-CONTENT-I-024 |
| REQ-FECONTENT-121 | TC-CONTENT-I-025 |
| REQ-FECONTENT-122 | TC-CONTENT-I-025 |
| REQ-FECONTENT-123 | TC-CONTENT-I-025 |
| REQ-FECONTENT-124 | TC-CONTENT-I-025 |
| REQ-FECONTENT-125 | TC-CONTENT-I-027 |
| REQ-FECONTENT-126 | TC-CONTENT-I-027 |
| REQ-FECONTENT-127 | TC-CONTENT-U-014, TC-CONTENT-U-015 |
| REQ-FECONTENT-128 | TC-CONTENT-U-016 – TC-CONTENT-U-018, TC-CONTENT-U-023 |
| REQ-FECONTENT-129 | TC-CONTENT-U-019 |
| REQ-FECONTENT-130 | TC-CONTENT-U-020 |
| REQ-FECONTENT-131 | TC-CONTENT-U-021, TC-CONTENT-U-022 |
| REQ-FECONTENT-132 | TC-CONTENT-I-028 |
| REQ-FECONTENT-133 | TC-CONTENT-I-028 |
| REQ-FECONTENT-134 | TC-CONTENT-I-028 |
| REQ-FECONTENT-135 | TC-CONTENT-I-028 |
| REQ-FECONTENT-136 | TC-CONTENT-I-028 |
| REQ-FECONTENT-137 | TC-CONTENT-I-029 |
| REQ-FECONTENT-138 | TC-CONTENT-I-032 |
| REQ-FECONTENT-139 | TC-CONTENT-I-032 |
| REQ-FECONTENT-140 | TC-CONTENT-I-032 |
| REQ-FECONTENT-141 | TC-CONTENT-I-032 |
| REQ-FECONTENT-142 | TC-CONTENT-I-032 |
| REQ-FECONTENT-143 | TC-CONTENT-I-032 |
| REQ-FECONTENT-144 | TC-CONTENT-I-033 |
| REQ-FECONTENT-145 | TC-CONTENT-I-033 |
| REQ-FECONTENT-146 | TC-CONTENT-I-033 |
| REQ-FECONTENT-147 | TC-CONTENT-I-034 |
| REQ-FECONTENT-148 | TC-CONTENT-I-044, TC-CONTENT-X-003 |
| REQ-FECONTENT-149 | TC-CONTENT-I-045, TC-CONTENT-X-003 |
| REQ-FECONTENT-150 | TC-CONTENT-U-024 – TC-CONTENT-U-027, TC-CONTENT-I-036, TC-CONTENT-X-005 |
| REQ-FECONTENT-151 | TC-CONTENT-I-038 |
| REQ-FECONTENT-152 | TC-CONTENT-I-038 |
| REQ-FECONTENT-153 | TC-CONTENT-I-038 |
| REQ-FECONTENT-154 | TC-CONTENT-I-039 |
| REQ-FECONTENT-155 | TC-CONTENT-I-042 |
| REQ-FECONTENT-156 | TC-CONTENT-I-040 |
| REQ-FECONTENT-157 | TC-CONTENT-I-010, TC-CONTENT-E-005 |
| REQ-FECONTENT-158 | TC-CONTENT-I-012 |
| REQ-FECONTENT-159 | TC-CONTENT-I-011, TC-CONTENT-X-001 |
| REQ-FECONTENT-160 | TC-CONTENT-I-006, TC-CONTENT-X-004 |
| REQ-FECONTENT-161 | TC-CONTENT-I-006, TC-CONTENT-X-004 |
| REQ-FECONTENT-162 | TC-CONTENT-I-006 |
| REQ-FECONTENT-163 | TC-CONTENT-U-033, TC-CONTENT-U-034, TC-CONTENT-I-009 |
| REQ-FECONTENT-164 | TC-CONTENT-I-047 |
| REQ-FECONTENT-165 | TC-CONTENT-I-048 |
| REQ-FECONTENT-166 | TC-CONTENT-U-028, TC-CONTENT-U-029, TC-CONTENT-I-049 |
| REQ-FECONTENT-167 | TC-CONTENT-U-027, TC-CONTENT-I-037 |
| REQ-FECONTENT-168 | TC-CONTENT-X-002 |
| REQ-FECONTENT-169 | TC-CONTENT-I-030 |
| REQ-FECONTENT-170 | TC-CONTENT-I-033 |
| REQ-FECONTENT-171 | TC-CONTENT-I-033 |
| REQ-FECONTENT-172 | TC-CONTENT-I-041 |
| REQ-FECONTENT-173 | TC-CONTENT-I-013 |
| REQ-FECONTENT-174 | TC-CONTENT-I-014 |
| REQ-FECONTENT-175 | TC-CONTENT-I-018 |
| REQ-FECONTENT-176 | TC-CONTENT-I-028 |
| REQ-FECONTENT-177 | TC-CONTENT-I-028 |
| REQ-FECONTENT-178 | TC-CONTENT-I-038, TC-CONTENT-I-046 |
| REQ-FECONTENT-179 | TC-CONTENT-E-004 |
| REQ-FECONTENT-180 | TC-CONTENT-I-002, TC-CONTENT-I-005 |
| REQ-FECONTENT-181 | TC-CONTENT-I-006, TC-CONTENT-E-004 |
| REQ-FECONTENT-182 | TC-CONTENT-U-030, TC-CONTENT-U-031, TC-CONTENT-I-050 |
