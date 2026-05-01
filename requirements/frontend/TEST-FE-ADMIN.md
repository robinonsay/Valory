# TEST-FE-ADMIN — Valory Frontend Admin Views Test Plan

**Date:** 2026-05-01
**Author:** Test Author (Valory)
**Scope:** All admin views in the Valory frontend — Users, Audit Log, Config, Courses, and the shared admin layout with role guard.

---

## Traceability summary

Every test case in this document cites at least one requirement from
`requirements/frontend/REQ-FE-ADMIN.json`. Requirements with
`"verification_method": "Inspection"` are covered by the Tier 4 checklist.
Requirements with `"verification_method": "Demonstration"` are covered by the
Tier 3 procedure. Requirements with `"verification_method": "Test"` are covered
by Tier 1 or Tier 2 test cases.

---

## Tier 1 — Unit tests (Vitest)

Pure-function logic exercised without mounting any component. No network calls.
All tests live under `src/admin/__tests__/unit/`.

### TC-ADMIN-U-001 — Weight validator: valid sum within tolerance

```
// @{"verifies", ["REQ-FEADMIN-330", "REQ-FEADMIN-043"]}
```

**Function under test:** `validateWeightSum(homeworkWeight: number, projectWeight: number): string | null`

| # | homeworkWeight | projectWeight | Expected return |
|---|---------------|--------------|-----------------|
| 1 | 0.7 | 0.3 | `null` (valid) |
| 2 | 0.5 | 0.5 | `null` (valid) |
| 3 | 0.999 | 0.001 | `null` (valid — exactly at tolerance boundary) |
| 4 | 0.0009 | 0.9991 | `null` (valid — sum = 1.0000, within 0.001) |

**Pass criterion:** Return value is `null` for all rows above.

---

### TC-ADMIN-U-002 — Weight validator: invalid sum outside tolerance

```
// @{"verifies", ["REQ-FEADMIN-330", "REQ-FEADMIN-043"]}
```

**Function under test:** `validateWeightSum`

| # | homeworkWeight | projectWeight | Expected return |
|---|---------------|--------------|-----------------|
| 1 | 0.6 | 0.3 | non-null error string |
| 2 | 0.5 | 0.6 | non-null error string |
| 3 | 1.0 | 1.0 | non-null error string |
| 4 | 0.0 | 1.0 | non-null error string (0.0 is invalid for homework_weight per REQ-FEADMIN-304, but sum is 1.0 — the per-field validator catches this; here sum > 1.001 check does not apply, the per-field validator fires first) |

**Pass criterion:** Return value is a non-null, non-empty string for rows 1–3.
Row 4 error is expected to come from the per-field `homework_weight` validator
(not weight-sum), so this row may be excluded from this specific test.

---

### TC-ADMIN-U-003 — late_penalty_rate validator: boundary values

```
// @{"verifies", ["REQ-FEADMIN-303", "REQ-FEADMIN-334", "REQ-FEADMIN-043"]}
```

**Function under test:** `validateConfigField(key: 'late_penalty_rate', value: string): string | null`

| # | Input | Expected return |
|---|-------|-----------------|
| 1 | `"0.0"` | `null` |
| 2 | `"1.0"` | `null` |
| 3 | `"0.05"` | `null` |
| 4 | `"1.01"` | `"late_penalty_rate must be a float between 0.0 and 1.0 inclusive."` |
| 5 | `"-0.01"` | `"late_penalty_rate must be a float between 0.0 and 1.0 inclusive."` |
| 6 | `"abc"` | `"late_penalty_rate must be a float between 0.0 and 1.0 inclusive."` |
| 7 | `""` | non-null error |

**Pass criterion:** Return values match the expected column exactly.

---

### TC-ADMIN-U-004 — per_student_token_limit validator: zero is valid

```
// @{"verifies", ["REQ-FEADMIN-302", "REQ-FEADMIN-333", "REQ-FEADMIN-043"]}
```

**Function under test:** `validateConfigField(key: 'per_student_token_limit', value: string): string | null`

| # | Input | Expected return |
|---|-------|-----------------|
| 1 | `"0"` | `null` (zero disables limit) |
| 2 | `"1000"` | `null` |
| 3 | `"-1"` | `"per_student_token_limit must be an integer >= 0."` |
| 4 | `"1.5"` | non-null error (non-integer) |
| 5 | `""` | non-null error |

---

### TC-ADMIN-U-005 — Config field validators: integer-gte-1 fields

```
// @{"verifies", ["REQ-FEADMIN-300", "REQ-FEADMIN-301", "REQ-FEADMIN-306",
//                "REQ-FEADMIN-307", "REQ-FEADMIN-309", "REQ-FEADMIN-310",
//                "REQ-FEADMIN-311", "REQ-FEADMIN-331", "REQ-FEADMIN-332",
//                "REQ-FEADMIN-337", "REQ-FEADMIN-338", "REQ-FEADMIN-340",
//                "REQ-FEADMIN-341", "REQ-FEADMIN-342", "REQ-FEADMIN-043"]}
```

**Function under test:** `validateConfigField(key, value)`

Table-driven. For each `key` in the set
`{agent_retry_limit, correction_loop_max_iterations, session_inactivity_seconds,
account_lockout_seconds, content_generation_timeout_seconds,
audit_retention_days, notification_retention_days}`:

| # | value | Expected return |
|---|-------|-----------------|
| 1 | `"1"` | `null` |
| 2 | `"99"` | `null` |
| 3 | `"0"` | non-null error |
| 4 | `"-1"` | non-null error |
| 5 | `"abc"` | non-null error |

**Pass criterion:** The error message for each invalid row exactly matches the
backend string (e.g., `"agent_retry_limit must be an integer >= 1."`).

---

### TC-ADMIN-U-006 — max_upload_bytes validator: minimum 1024

```
// @{"verifies", ["REQ-FEADMIN-308", "REQ-FEADMIN-339", "REQ-FEADMIN-043"]}
```

**Function under test:** `validateConfigField(key: 'max_upload_bytes', value: string): string | null`

| # | Input | Expected return |
|---|-------|-----------------|
| 1 | `"1024"` | `null` |
| 2 | `"1048576"` | `null` |
| 3 | `"1023"` | `"max_upload_bytes must be an integer >= 1024."` |
| 4 | `"0"` | `"max_upload_bytes must be an integer >= 1024."` |
| 5 | `"abc"` | non-null error |

---

### TC-ADMIN-U-007 — consent_version validator: empty string is invalid

```
// @{"verifies", ["REQ-FEADMIN-312", "REQ-FEADMIN-343", "REQ-FEADMIN-043"]}
```

**Function under test:** `validateConfigField(key: 'consent_version', value: string): string | null`

| # | Input | Expected return |
|---|-------|-----------------|
| 1 | `"v1"` | `null` |
| 2 | `"2026-05-01"` | `null` |
| 3 | `""` | `"consent_version must be a non-empty string."` |
| 4 | `"   "` (whitespace-only) | non-null error (treat as empty) |

---

### TC-ADMIN-U-008 — homework_weight and project_weight validators: individual field bounds

```
// @{"verifies", ["REQ-FEADMIN-304", "REQ-FEADMIN-305", "REQ-FEADMIN-335",
//                "REQ-FEADMIN-336", "REQ-FEADMIN-043"]}
```

**Function under test:** `validateConfigField(key: 'homework_weight' | 'project_weight', value: string): string | null`

For each key `{homework_weight, project_weight}`:

| # | Input | Expected return |
|---|-------|-----------------|
| 1 | `"0.5"` | `null` |
| 2 | `"1.0"` | `null` |
| 3 | `"0.0"` | non-null error (must be > 0.0) |
| 4 | `"1.001"` | non-null error (must be <= 1.0) |
| 5 | `"-0.1"` | non-null error |
| 6 | `"abc"` | non-null error |

---

### TC-ADMIN-U-009 — Dirty state detector: field changed from fetched value

```
// @{"verifies", ["REQ-FEADMIN-323", "REQ-FEADMIN-042"]}
```

**Function under test:** `isFormDirty(fetched: Record<string, string>, current: Record<string, string>): boolean`

| # | fetched | current | Expected |
|---|---------|---------|----------|
| 1 | `{late_penalty_rate: "0.05"}` | `{late_penalty_rate: "0.03"}` | `true` |
| 2 | `{late_penalty_rate: "0.05"}` | `{late_penalty_rate: "0.05"}` | `false` |
| 3 | `{a: "1", b: "2"}` | `{a: "1", b: "2"}` | `false` |
| 4 | `{a: "1", b: "2"}` | `{a: "1", b: "99"}` | `true` |
| 5 | `{}` | `{}` | `false` |

---

### TC-ADMIN-U-010 — Audit hash display: truncate to first 8 characters

```
// @{"verifies", ["REQ-FEADMIN-202", "REQ-FEADMIN-030", "REQ-FEADMIN-032"]}
```

**Function under test:** `truncateHash(hash: string): string`

| # | Input | Expected |
|---|-------|----------|
| 1 | `"abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"` | `"abcdef12"` |
| 2 | `"abc"` (shorter than 8) | `"abc"` (return as-is) |
| 3 | `""` | `""` |
| 4 | `"12345678abcdef"` | `"12345678"` |

---

### TC-ADMIN-U-011 — Role guard: admin role grants access

```
// @{"verifies", ["REQ-FEADMIN-002", "REQ-FEADMIN-014", "REQ-FEADMIN-015"]}
```

**Function under test:** `adminRouteGuard(userRole: string | null, isAuthenticated: boolean): 'allow' | '/login' | '/dashboard'`

| # | userRole | isAuthenticated | Expected |
|---|----------|-----------------|----------|
| 1 | `"admin"` | `true` | `"allow"` |
| 2 | `"student"` | `true` | `"/dashboard"` |
| 3 | `null` | `false` | `"/login"` |
| 4 | `""` | `false` | `"/login"` |
| 5 | `"student"` | `false` | `"/login"` |

---

### TC-ADMIN-U-012 — Config PATCH body: only changed keys, all values stringified

```
// @{"verifies", ["REQ-FEADMIN-320", "REQ-FEADMIN-321", "REQ-FEADMIN-041"]}
```

**Function under test:** `buildPatchBody(fetched: Record<string, string>, current: Record<string, string>): Record<string, string>`

| # | fetched | current | Expected body |
|---|---------|---------|---------------|
| 1 | `{a: "1", b: "2"}` | `{a: "1", b: "3"}` | `{b: "3"}` |
| 2 | `{a: "1", b: "2"}` | `{a: "1", b: "2"}` | `{}` (no change) |
| 3 | `{a: "1"}` | `{a: "99"}` | `{a: "99"}` and value is typeof string |
| 4 | `{a: "0.5", b: "0.5"}` | `{a: "0.7", b: "0.3"}` | `{a: "0.7", b: "0.3"}` — all values are strings |

**Pass criterion:** The returned object contains exactly and only the changed
keys. Every value in the output must be of type `string`.

---

## Tier 2 — Component integration tests (Vitest + Vue Test Utils)

These tests mount components in a jsdom environment against a mocked
`fetch`/`axios` adapter. The mock intercepts network calls and returns
controlled responses. No real HTTP server is used here — that is Tier 3.

All tests live under `src/admin/__tests__/integration/`.

---

### TC-ADMIN-I-001 — User list: GET /users 200 renders table rows

```
// @{"verifies", ["REQ-FEADMIN-100", "REQ-FEADMIN-101", "REQ-FEADMIN-102",
//                "REQ-FEADMIN-103", "REQ-FEADMIN-020"]}
```

**Setup:** Mock `GET /api/v1/users` returns HTTP 200 with body:

```json
{
  "users": [
    {
      "id": "00000000-0000-0000-0000-000000000001",
      "username": "alice",
      "role": "student",
      "is_active": true,
      "email": "alice@example.com",
      "created_at": "2026-01-01T00:00:00Z"
    },
    {
      "id": "00000000-0000-0000-0000-000000000002",
      "username": "bob",
      "role": "admin",
      "is_active": false,
      "created_at": "2026-01-02T00:00:00Z"
    }
  ]
}
```

**Assertions:**

1. The component renders exactly 2 table rows (not counting the header).
2. The first row contains the text `"alice"` in the username column.
3. The first row contains the text `"student"` in the role column.
4. The first row contains `"true"` or an active indicator in the status column.
5. The second row contains the text `"bob"`.
6. The second row contains `"admin"` in the role column.
7. The second row contains `"false"` or an inactive indicator.
8. The Authorization header sent with the GET request contains `"Bearer "`.

---

### TC-ADMIN-I-044 — GET /api/v1/users with limit exceeding cap

```
// @{"verifies", ["REQ-FEADMIN-100"]}
```

**TC-ADMIN-I-044** — GET /api/v1/users with limit exceeding cap

| Field | Value |
|---|---|
| Type | Integration |
| Verifies | REQ-FEADMIN-100 |
| Setup | Mock GET /api/v1/users?limit=200 to return HTTP 200 with a `{"users": [...]}` body containing 100 user objects (the maximum). |
| Action | The admin user list component requests the user list with limit=200. |
| Assert | The component renders without error. The server was called with limit=200. The response HTTP status is 200 (not 400). The component displays the 100 returned users. The client does not crash or show an error state. |

---

### TC-ADMIN-I-002 — User list: student role redirects to /dashboard

```
// @{"verifies", ["REQ-FEADMIN-015", "REQ-FEADMIN-002"]}
```

**Setup:** Auth store has `role = "student"`. Mount the admin Users component
via the router under `/admin/users`.

**Assertions:**

1. Vue Router's current route is `/dashboard` (redirect occurred).
2. The user table is not rendered.

---

### TC-ADMIN-I-003 — User list: unauthenticated redirects to /login

```
// @{"verifies", ["REQ-FEADMIN-014", "REQ-FEADMIN-002"]}
```

**Setup:** Auth store has `isAuthenticated = false`, `role = null`. Mount the
admin Users component via the router under `/admin/users`.

**Assertions:**

1. Vue Router's current route is `/login`.
2. The user table is not rendered.

---

### TC-ADMIN-I-004 — Create user modal: submit POST with correct body and prepend on 201

```
// @{"verifies", ["REQ-FEADMIN-114", "REQ-FEADMIN-116", "REQ-FEADMIN-021"]}
```

**Setup:**

- Initial user list contains one user `alice`.
- Mock `GET /api/v1/users` → 200 with `[alice]`.
- Mock `POST /api/v1/users` → 201 with a new user object
  `{id: "...", username: "carol", role: "student", is_active: true, ...}`.

**Steps:**

1. Mount `AdminUsersView`.
2. Click the "Create User" button.
3. Fill in `username = "carol"`, `role = "student"`, `password = "secret123"`.
4. Leave email blank.
5. Click "Submit".

**Assertions:**

1. The captured POST request body is `{"username":"carol","role":"student","password":"secret123"}` — the `email` key is absent when the field is empty (REQ-FEADMIN-115).
2. The Authorization header contains `"Bearer "`.
3. Assert: The request includes `X-CSRF-Token: <value matching the mocked __Host-csrf cookie>`.
4. After successful response the modal is no longer visible.
5. The user list now has 2 rows and the first row is `"carol"` (prepended).

---

### TC-ADMIN-I-005 — Create user: email omitted from body when empty

```
// @{"verifies", ["REQ-FEADMIN-115", "REQ-FEADMIN-021"]}
```

**Setup:** Same as TC-ADMIN-I-004 but explicitly verify the POST body shape.

**Steps:** Submit create-user form with blank email field.

**Assertion:** The serialized JSON body does not contain the key `"email"`.

---

### TC-ADMIN-I-006 — Create user: HTTP 409 shows inline error

```
// @{"verifies", ["REQ-FEADMIN-117", "REQ-FEADMIN-021"]}
```

**Setup:** Mock `POST /api/v1/users` → 409.

**Steps:** Submit create-user form with `username = "alice"` (duplicate).

**Assertions:**

1. Modal remains open.
2. The text `"Username already exists."` is visible within the modal.

---

### TC-ADMIN-I-007 — Create user modal: Cancel closes without request

```
// @{"verifies", ["REQ-FEADMIN-118", "REQ-FEADMIN-021"]}
```

**Setup:** Mock does not expect any POST call.

**Steps:**

1. Click "Create User" to open modal.
2. Fill in username and password.
3. Click "Cancel".

**Assertions:**

1. Modal is no longer visible.
2. Zero POST requests were issued.

---

### TC-ADMIN-I-008 — Create user modal: Submit button disabled during in-flight request

```
// @{"verifies", ["REQ-FEADMIN-119", "REQ-FEADMIN-021"]}
```

**Setup:** Mock `POST /api/v1/users` is delayed (promise not yet resolved).

**Steps:**

1. Open modal, fill required fields, click "Submit".
2. Immediately query the Submit button's `disabled` attribute before the
   promise resolves.

**Assertion:** The Submit button has `disabled = true` while the request is
pending.

---

### TC-ADMIN-I-009 — Create user modal: field validation before submit

```
// @{"verifies", ["REQ-FEADMIN-110", "REQ-FEADMIN-111", "REQ-FEADMIN-112",
//                "REQ-FEADMIN-021"]}
```

**Steps:** Open modal and click Submit with all fields empty.

**Assertions:**

1. Text `"Username is required."` is visible.
2. Text `"Role is required."` is visible.
3. Text `"Password is required."` is visible.
4. Zero POST requests were issued.

---

### TC-ADMIN-I-010 — Deactivate user: confirm dialog shown, POST on confirm, list updated

```
// @{"verifies", ["REQ-FEADMIN-131", "REQ-FEADMIN-132", "REQ-FEADMIN-023"]}
```

**Setup:**

- User list contains `{id: "...", username: "alice", is_active: true}`.
- Mock `POST /api/v1/users/{id}/deactivate` → 204.

**Steps:**

1. Click the Deactivate button for `alice`.
2. A confirmation dialog appears.
3. Click Confirm in the dialog.

**Assertions:**

1. One POST request was sent to `/api/v1/users/{alice_id}/deactivate`.
2. The Authorization header contains `"Bearer "`.
3. Assert: The request includes `X-CSRF-Token: <value matching the mocked __Host-csrf cookie>`.
4. Alice's row now shows an inactive status (is_active = false).

---

### TC-ADMIN-I-011 — Deactivate user: cancel dialog aborts request

```
// @{"verifies", ["REQ-FEADMIN-133", "REQ-FEADMIN-023"]}
```

**Steps:**

1. Click Deactivate for `alice`.
2. Click Cancel in the confirmation dialog.

**Assertions:**

1. Zero POST requests to `/deactivate` were issued.
2. Alice's row still shows active status.

---

### TC-ADMIN-I-012 — Activate user: POST sent immediately, list updated on 204

```
// @{"verifies", ["REQ-FEADMIN-134", "REQ-FEADMIN-135", "REQ-FEADMIN-024"]}
```

**Setup:**

- User list contains `{id: "...", username: "bob", is_active: false}`.
- Mock `POST /api/v1/users/{id}/activate` → 204.

**Steps:** Click Activate for `bob` (no confirmation dialog expected).

**Assertions:**

1. One POST request to `/api/v1/users/{bob_id}/activate`.
2. No confirmation dialog was shown before the request.
3. Bob's row now shows active status.

---

### TC-ADMIN-I-013 — Delete user: confirm dialog shown, DELETE sent, row removed on 204

```
// @{"verifies", ["REQ-FEADMIN-141", "REQ-FEADMIN-142", "REQ-FEADMIN-025"]}
```

**Setup:**

- User list contains `{id: "...", username: "carol", role: "student", is_active: true}`.
- Mock `DELETE /api/v1/users/{id}` → 204.

**Steps:**

1. Click Delete for `carol`.
2. Confirmation dialog appears.
3. Click Confirm.

**Assertions:**

1. One DELETE request was sent to `/api/v1/users/{carol_id}`.
2. The Authorization header contains `"Bearer "`.
3. Assert: The request includes `X-CSRF-Token: <value matching the mocked __Host-csrf cookie>`.
4. `carol`'s row is no longer in the list.

---

### TC-ADMIN-I-014 — Delete user: cancel dialog aborts DELETE

```
// @{"verifies", ["REQ-FEADMIN-144", "REQ-FEADMIN-025"]}
```

**Steps:**

1. Click Delete for `carol`.
2. Click Cancel in the confirmation dialog.

**Assertions:**

1. Zero DELETE requests were issued.
2. `carol`'s row remains in the list.

---

### TC-ADMIN-I-015 — Delete user: HTTP 409 shows error message

```
// @{"verifies", ["REQ-FEADMIN-143", "REQ-FEADMIN-025"]}
```

**Setup:** Mock `DELETE /api/v1/users/{id}` → 409.

**Steps:** Confirm deletion of `carol`.

**Assertions:**

1. `carol`'s row is still in the list.
2. The text `"Cannot delete: student has an active course."` is visible.

---

### TC-ADMIN-I-016 — Delete button visible only for student rows

```
// @{"verifies", ["REQ-FEADMIN-026"]}
```

**Setup:** User list has one `student` row and one `admin` row.

**Assertions:**

1. The Delete button is rendered for the `student` row.
2. The Delete button is not rendered for the `admin` row.

---

### TC-ADMIN-I-017 — Deactivate button visible only for active users

```
// @{"verifies", ["REQ-FEADMIN-027"]}
```

**Setup:** Two users — one `is_active: true`, one `is_active: false`.

**Assertions:**

1. Deactivate button exists for the active user's row.
2. Deactivate button does not exist for the inactive user's row.

---

### TC-ADMIN-I-018 — Activate button visible only for inactive users

```
// @{"verifies", ["REQ-FEADMIN-028"]}
```

**Setup:** Same as TC-ADMIN-I-017.

**Assertions:**

1. Activate button exists for the inactive user's row.
2. Activate button does not exist for the active user's row.

---

### TC-ADMIN-I-019 — Edit user modal: PATCH body contains only username and email

```
// @{"verifies", ["REQ-FEADMIN-120", "REQ-FEADMIN-022"]}
```

**Setup:**

- User row: `{id: "...", username: "alice", email: "alice@example.com", role: "student"}`.
- Mock `PATCH /api/v1/users/{id}` → 200 with updated user.

**Steps:**

1. Click Edit for `alice`.
2. Change username to `"alicia"`.
3. Click "Save".

**Assertions:**

1. The PATCH request body is `{"username": "alicia", "email": "alice@example.com"}` — only these two fields.
2. The PATCH URL is `/api/v1/users/{alice_id}`.
3. Assert: The request includes `X-CSRF-Token: <value matching the mocked __Host-csrf cookie>`.

---

### TC-ADMIN-I-020 — Edit user: HTTP 200 closes modal, updates row in place

```
// @{"verifies", ["REQ-FEADMIN-121", "REQ-FEADMIN-022"]}
```

**Setup:** Mock `PATCH /api/v1/users/{id}` → 200 with `{..., username: "alicia"}`.

**Steps:** Submit edit form.

**Assertions:**

1. Modal is closed.
2. The user's row shows `"alicia"` (updated in place, not appended).
3. The total number of rows has not changed.

---

### TC-ADMIN-I-021 — Edit user: HTTP 409 shows inline error in modal

```
// @{"verifies", ["REQ-FEADMIN-122", "REQ-FEADMIN-022"]}
```

**Setup:** Mock `PATCH /api/v1/users/{id}` → 409.

**Steps:** Submit edit form.

**Assertions:**

1. Modal remains open.
2. Text `"Username already exists."` is visible within the modal.

---

### TC-ADMIN-I-022 — Edit user modal: Save button disabled during in-flight request

```
// @{"verifies", ["REQ-FEADMIN-124", "REQ-FEADMIN-022"]}
```

**Setup:** PATCH mock is delayed.

**Assertion:** Save button has `disabled = true` while request is in-flight.

---

### TC-ADMIN-I-023 — Audit list: GET /audit renders entries on mount

```
// @{"verifies", ["REQ-FEADMIN-200", "REQ-FEADMIN-201", "REQ-FEADMIN-030"]}
```

**Setup:** Mock `GET /api/v1/audit` (no query params) → 200:

```json
{
  "entries": [
    {
      "id": 5,
      "admin_id": "00000000-0000-0000-0000-000000000001",
      "action": "user.create",
      "target_type": "user",
      "target_id": "00000000-0000-0000-0000-000000000002",
      "entry_hash": "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
      "created_at": "2026-01-01T10:00:00Z"
    }
  ],
  "next_before": 5
}
```

**Assertions:**

1. On mount the component issues exactly one GET to `/api/v1/audit` with no
   `before` or `limit` query parameters.
2. One row is rendered.
3. The row displays `"user.create"` as the action.
4. The row displays `"abcdef12"` (first 8 chars) as the hash — not the full
   64-character string.

---

### TC-ADMIN-I-024 — Audit list: Load More appends entries using next_before cursor

```
// @{"verifies", ["REQ-FEADMIN-203", "REQ-FEADMIN-031"]}
```

**Setup:**

- First GET `/api/v1/audit` → `{entries: [entry id=5], next_before: 5}`.
- Second GET `/api/v1/audit?before=5` → `{entries: [entry id=2], next_before: 2}`.

**Steps:**

1. Mount component (renders entry 5, Load More visible).
2. Click "Load More".

**Assertions:**

1. Second GET request URL includes `?before=5`.
2. Component now renders 2 rows (id=5 and id=2 — appended, not replaced).

---

### TC-ADMIN-I-025 — Audit list: Load More hidden when next_before is zero

```
// @{"verifies", ["REQ-FEADMIN-204", "REQ-FEADMIN-031"]}
```

**Setup:** Mock `GET /api/v1/audit` → `{entries: [...], next_before: 0}`.

**Assertion:** The "Load More" button is not rendered.

---

### TC-ADMIN-I-026 — Audit verify: GET /audit/verify called on button click

```
// @{"verifies", ["REQ-FEADMIN-210", "REQ-FEADMIN-033"]}
```

**Setup:** Mock `GET /api/v1/audit/verify` → 200 `{valid: true, count: 42}`.

**Steps:** Click "Verify Integrity".

**Assertion:** One GET request to `/api/v1/audit/verify` was issued.

---

### TC-ADMIN-I-027 — Audit verify: valid=true shows success message with count

```
// @{"verifies", ["REQ-FEADMIN-211", "REQ-FEADMIN-033"]}
```

**Setup:** Mock `/audit/verify` → `{valid: true, count: 42}`.

**Steps:** Click "Verify Integrity".

**Assertion:** Text matching
`"Audit log integrity verified. All 42 entries are intact."` is visible.

---

### TC-ADMIN-I-028 — Audit verify: valid=false shows warning with first_broken_id

```
// @{"verifies", ["REQ-FEADMIN-212", "REQ-FEADMIN-033"]}
```

**Setup:** Mock `/audit/verify` → `{valid: false, first_broken_id: 17}`.

**Steps:** Click "Verify Integrity".

**Assertion:** Text matching
`"WARNING: Audit log integrity check failed. Chain broken at entry 17."` is
visible.

---

### TC-ADMIN-I-029 — Audit verify: network error shows retry message

```
// @{"verifies", ["REQ-FEADMIN-213", "REQ-FEADMIN-033"]}
```

**Setup:** Mock `/audit/verify` → network error (rejected promise or 500).

**Steps:** Click "Verify Integrity".

**Assertion:** Text `"Verification failed. Please try again."` is visible.

---

### TC-ADMIN-I-030 — Config view: GET /admin/config populates all 13 fields

```
// @{"verifies", ["REQ-FEADMIN-300", "REQ-FEADMIN-301", "REQ-FEADMIN-302",
//                "REQ-FEADMIN-303", "REQ-FEADMIN-304", "REQ-FEADMIN-305",
//                "REQ-FEADMIN-306", "REQ-FEADMIN-307", "REQ-FEADMIN-308",
//                "REQ-FEADMIN-309", "REQ-FEADMIN-310", "REQ-FEADMIN-311",
//                "REQ-FEADMIN-312", "REQ-FEADMIN-040"]}
```

**Setup:** Mock `GET /api/v1/admin/config` → 200:

```json
{
  "config": {
    "agent_retry_limit": "3",
    "correction_loop_max_iterations": "5",
    "per_student_token_limit": "100000",
    "late_penalty_rate": "0.05",
    "homework_weight": "0.4",
    "project_weight": "0.6",
    "session_inactivity_seconds": "1800",
    "account_lockout_seconds": "300",
    "max_upload_bytes": "5242880",
    "content_generation_timeout_seconds": "120",
    "audit_retention_days": "365",
    "notification_retention_days": "30",
    "consent_version": "v1"
  }
}
```

**Assertions:**

1. Each of the 13 input fields is populated with the corresponding value from
   the response.
2. On mount a GET was sent to `/api/v1/admin/config`.

---

### TC-ADMIN-I-031 — Config view: changing a field shows dirty indicator

```
// @{"verifies", ["REQ-FEADMIN-323", "REQ-FEADMIN-042"]}
```

**Setup:** Config form loaded with initial values (from TC-ADMIN-I-030 mock).

**Steps:** Change `late_penalty_rate` field value from `"0.05"` to `"0.03"`.

**Assertion:** Text `"You have unsaved changes."` is visible.

---

### TC-ADMIN-I-032 — Config view: no dirty indicator when values match fetched

```
// @{"verifies", ["REQ-FEADMIN-323", "REQ-FEADMIN-042"]}
```

**Setup:** Config form loaded; no fields changed.

**Assertion:** Text `"You have unsaved changes."` is not present in the DOM.

---

### TC-ADMIN-I-033 — Config save: PATCH with only changed key, all values strings

```
// @{"verifies", ["REQ-FEADMIN-320", "REQ-FEADMIN-321", "REQ-FEADMIN-041"]}
```

**Setup:**

- Config loaded with `late_penalty_rate = "0.05"` and all other keys.
- Mock `PATCH /api/v1/admin/config` → 200 with updated config.

**Steps:**

1. Change `late_penalty_rate` to `"0.03"`.
2. Click "Save".

**Assertions:**

1. PATCH request body is `{"config": {"late_penalty_rate": "0.03"}}` — only
   the changed key.
2. The value `"0.03"` is a JSON string, not a number.
3. No other keys appear in the `config` object.
4. Assert: The request includes `X-CSRF-Token: <value matching the mocked __Host-csrf cookie>`.

---

### TC-ADMIN-I-034 — Config save: success banner and dirty indicator cleared on 200

```
// @{"verifies", ["REQ-FEADMIN-324", "REQ-FEADMIN-044"]}
```

**Setup:** PATCH → 200.

**Steps:** Change a field, click Save.

**Assertions:**

1. Text `"Configuration saved."` is visible after response.
2. Text `"You have unsaved changes."` is no longer visible.

---

### TC-ADMIN-I-035 — Config save: Save button disabled during in-flight PATCH

```
// @{"verifies", ["REQ-FEADMIN-322", "REQ-FEADMIN-041"]}
```

**Setup:** PATCH mock is delayed.

**Assertion:** Save button has `disabled = true` while the request is pending.

---

### TC-ADMIN-I-036 — Config save: 422 validation_errors shown per field

```
// @{"verifies", ["REQ-FEADMIN-325", "REQ-FEADMIN-045"]}
```

**Setup:** Mock `PATCH /api/v1/admin/config` → 422:

```json
{
  "validation_errors": [
    "homework_weight: homework_weight + project_weight must equal 1.0",
    "project_weight: homework_weight + project_weight must equal 1.0"
  ]
}
```

**Steps:** Change `homework_weight` and click Save.

**Assertions:**

1. The error for `homework_weight` is displayed adjacent to or beneath the
   `homework_weight` field.
2. The error for `project_weight` is displayed adjacent to or beneath the
   `project_weight` field.
3. The `"Configuration saved."` banner is not shown.

---

### TC-ADMIN-I-037 — Config form: client-side weight sum validation prevents submit

```
// @{"verifies", ["REQ-FEADMIN-330", "REQ-FEADMIN-043"]}
```

**Setup:** Config loaded with `homework_weight = "0.4"`, `project_weight = "0.6"`.

**Steps:**

1. Change `homework_weight` to `"0.3"` (sum = 0.9, outside 0.001 tolerance).
2. Click "Save".

**Assertions:**

1. No PATCH request is issued.
2. An error message referencing the weight constraint is visible on both
   `homework_weight` and `project_weight` fields.

---

### TC-ADMIN-I-038 — API error banner: HTTP 500 shows dismissible error banner

```
// @{"verifies", ["REQ-FEADMIN-018", "REQ-FEADMIN-009"]}
```

**Setup:** Mock `GET /api/v1/users` → 500 `{"error": "internal server error"}`.

**Steps:** Mount AdminUsersView.

**Assertions:**

1. An error banner is visible containing a human-readable error message.
2. The banner has a dismiss/close control.
3. Clicking the dismiss control hides the banner.

---

### TC-ADMIN-I-039 — Course list: GET /courses?limit=20 on mount, rows rendered

```
// @{"verifies", ["REQ-FEADMIN-400", "REQ-FEADMIN-401", "REQ-FEADMIN-050"]}
```

**Setup:** Mock `GET /api/v1/courses?limit=20` → 200:

```json
{
  "courses": [
    {
      "id": "00000000-0000-0000-0000-000000000010",
      "student_id": "00000000-0000-0000-0000-000000000001",
      "topic": "Linear Algebra",
      "status": "active",
      "created_at": "2026-01-01T00:00:00Z"
    }
  ],
  "next_cursor": "cursor-abc"
}
```

**Assertions:**

1. On mount, GET was sent to `/api/v1/courses` with `limit=20` (no `status`
   or `cursor` param).
2. One row is rendered.
3. The row displays `student_id`, `topic` (`"Linear Algebra"`), and `status`
   (`"active"`).

---

### TC-ADMIN-I-040 — Course list: status filter dropdown triggers new GET

```
// @{"verifies", ["REQ-FEADMIN-404", "REQ-FEADMIN-051"]}
```

**Setup:**

- Initial GET `/api/v1/courses?limit=20` returns 3 courses.
- Filtered GET `/api/v1/courses?status=active` returns 1 course.

**Steps:** Change the status dropdown to `"active"`.

**Assertions:**

1. A new GET is issued to `/api/v1/courses?status=active`.
2. The course list is replaced (not appended) — only 1 row is displayed.
3. The cursor is reset (no `cursor` param in the new request).

---

### TC-ADMIN-I-041 — Course list: Load More appends courses using next_cursor

```
// @{"verifies", ["REQ-FEADMIN-405", "REQ-FEADMIN-053"]}
```

**Setup:**

- Initial GET → `{courses: [course-1], next_cursor: "cursor-abc"}`.
- Load More GET `/api/v1/courses?cursor=cursor-abc` → `{courses: [course-2], next_cursor: ""}`.

**Steps:** Click "Load More".

**Assertions:**

1. Second GET URL contains `cursor=cursor-abc`.
2. Both courses are now displayed (appended).

---

### TC-ADMIN-I-042 — Course list: Load More hidden when next_cursor is empty

```
// @{"verifies", ["REQ-FEADMIN-406", "REQ-FEADMIN-053"]}
```

**Setup:** GET returns `next_cursor: ""`.

**Assertion:** "Load More" button is not rendered.

---

### TC-ADMIN-I-043 — Course list: Load More preserves active status filter

```
// @{"verifies", ["REQ-FEADMIN-405", "REQ-FEADMIN-053"]}
```

**Setup:**

- Status filter is set to `"active"`.
- GET `/api/v1/courses?status=active` → `{courses: [...], next_cursor: "cursor-xyz"}`.
- Load More GET expected at `/api/v1/courses?status=active&cursor=cursor-xyz`.

**Steps:** With `"active"` filter applied, click Load More.

**Assertion:** The Load More request URL contains both `status=active` and
`cursor=cursor-xyz`.

---

## Tier 3 — E2E demonstration procedure (human-executed)

This procedure is executed manually by a QA engineer against a running Valory
instance (all services via `docker compose up`). Each step lists the exact URL,
the user action, and the expected observable result. No automation tooling is
required.

**Preconditions:**

- Application is running at `http://localhost:5173` (or the configured dev
  host).
- Database is seeded with at least one admin account (`admin` / `admin123`) and
  one student account (`student1` / `student123`).
- The tester has access to browser developer tools.

---

### TC-ADMIN-E-001 — Login as admin and verify redirect to /admin

```
// @{"verifies", ["REQ-FEADMIN-001", "REQ-FEADMIN-007", "REQ-FEADMIN-010",
//                "REQ-FEADMIN-011", "REQ-FEADMIN-012", "REQ-FEADMIN-013",
//                "REQ-FEADMIN-016"]}
```

| Step | URL / action | Expected result |
|------|-------------|-----------------|
| 1 | Navigate to `http://localhost:5173/login` | Login form displayed |
| 2 | Enter username `admin`, password `admin123`, click Login | Browser redirects to `/admin` or `/admin/users` |
| 3 | Inspect the navigation bar | Links labelled **Users**, **Audit Log**, **Config**, **Courses** are all visible |
| 4 | Inspect the navigation bar | The admin username `"admin"` is displayed in the nav bar |

---

### TC-ADMIN-E-002 — Navigate to Users and verify list loads

```
// @{"verifies", ["REQ-FEADMIN-003", "REQ-FEADMIN-008", "REQ-FEADMIN-017",
//                "REQ-FEADMIN-020", "REQ-FEADMIN-101", "REQ-FEADMIN-102",
//                "REQ-FEADMIN-103"]}
```

| Step | URL / action | Expected result |
|------|-------------|-----------------|
| 1 | Click **Users** in the admin nav bar | URL changes to `/admin/users` |
| 2 | Observe the view while data loads | A loading indicator (spinner) is visible before the table appears |
| 3 | After load completes | Table rows are visible; each row shows username, role, and is_active status |
| 4 | Inspect a row | The `created_at` value is formatted as a human-readable local date-time |

---

### TC-ADMIN-E-003 — Create new student user and verify it appears in list

```
// @{"verifies", ["REQ-FEADMIN-021", "REQ-FEADMIN-116", "REQ-FEADMIN-113"]}
```

| Step | URL / action | Expected result |
|------|-------------|-----------------|
| 1 | On `/admin/users`, click **Create User** | A modal dialog opens with Username, Email, Role, Password fields |
| 2 | Inspect the Role dropdown | Exactly two options: **student** and **admin** |
| 3 | Fill in Username = `testuser99`, Role = `student`, Password = `pass1234` | Fields accepted |
| 4 | Click **Submit** | Modal closes; `testuser99` appears as the first row in the user list |

---

### TC-ADMIN-E-004 — Deactivate a user and verify status changes to inactive

```
// @{"verifies", ["REQ-FEADMIN-023", "REQ-FEADMIN-130", "REQ-FEADMIN-132"]}
```

| Step | URL / action | Expected result |
|------|-------------|-----------------|
| 1 | Locate `testuser99` in the user list (active) | Deactivate button is visible |
| 2 | Click **Deactivate** | A confirmation dialog appears with text: `Deactivate testuser99? They will not be able to log in.` |
| 3 | Click **Confirm** | Dialog closes; `testuser99` row shows inactive status |
| 4 | Verify Activate button now visible | Activate button present; Deactivate button absent |

---

### TC-ADMIN-E-005 — Activate user and verify status changes to active

```
// @{"verifies", ["REQ-FEADMIN-024", "REQ-FEADMIN-134", "REQ-FEADMIN-135"]}
```

| Step | URL / action | Expected result |
|------|-------------|-----------------|
| 1 | `testuser99` is inactive (from TC-ADMIN-E-004) | Activate button visible |
| 2 | Click **Activate** | No confirmation dialog; request sent immediately |
| 3 | After response | `testuser99` row shows active status; Deactivate button reappears |

---

### TC-ADMIN-E-006 — Delete user and verify removal

```
// @{"verifies", ["REQ-FEADMIN-025", "REQ-FEADMIN-140", "REQ-FEADMIN-142"]}
```

| Step | URL / action | Expected result |
|------|-------------|-----------------|
| 1 | `testuser99` is active and a student | Delete button visible |
| 2 | Click **Delete** | Confirmation dialog appears with text: `Permanently delete testuser99 and all their data? This cannot be undone.` |
| 3 | Click **Confirm** | Dialog closes; `testuser99` row is gone from the list |

---

### TC-ADMIN-E-007 — Navigate to Audit Log and verify recent actions visible

```
// @{"verifies", ["REQ-FEADMIN-004", "REQ-FEADMIN-030", "REQ-FEADMIN-032"]}
```

| Step | URL / action | Expected result |
|------|-------------|-----------------|
| 1 | Click **Audit Log** in the nav bar | URL changes to `/admin/audit` |
| 2 | Observe the table after load | Rows display: admin_id, action, target_type, target_id, created_at, and a hash preview |
| 3 | Inspect any hash cell | Only the first 8 characters of the hash are displayed |
| 4 | Verify recent admin actions are present | At minimum the `user.create` and `user.deactivate` actions from E-003/E-004 appear |

---

### TC-ADMIN-E-008 — Verify Integrity on Audit Log

```
// @{"verifies", ["REQ-FEADMIN-033", "REQ-FEADMIN-211"]}
```

| Step | URL / action | Expected result |
|------|-------------|-----------------|
| 1 | On `/admin/audit`, click **Verify Integrity** | Button text changes or a spinner appears |
| 2 | After response | Success message displayed: `Audit log integrity verified. All N entries are intact.` (where N > 0) |

---

### TC-ADMIN-E-009 — Navigate to Config, change late_penalty_rate, and save

```
// @{"verifies", ["REQ-FEADMIN-005", "REQ-FEADMIN-040", "REQ-FEADMIN-041",
//                "REQ-FEADMIN-042", "REQ-FEADMIN-044"]}
```

| Step | URL / action | Expected result |
|------|-------------|-----------------|
| 1 | Click **Config** in the nav bar | URL changes to `/admin/config` |
| 2 | Observe the form after load | All 13 fields are populated with current values |
| 3 | Change `late_penalty_rate` to `0.03` | Text `You have unsaved changes.` appears immediately |
| 4 | Click **Save** | `You have unsaved changes.` disappears; `Configuration saved.` banner appears |
| 5 | Refresh the page and return to Config | `late_penalty_rate` field shows `0.03` |

---

### TC-ADMIN-E-010 — Navigate to Courses and verify all courses visible with filter

```
// @{"verifies", ["REQ-FEADMIN-006", "REQ-FEADMIN-050", "REQ-FEADMIN-051",
//                "REQ-FEADMIN-052"]}
```

| Step | URL / action | Expected result |
|------|-------------|-----------------|
| 1 | Click **Courses** in the nav bar | URL changes to `/admin/courses` |
| 2 | Observe the table after load | All system-wide courses visible; each row shows student_id, topic, status, created_at |
| 3 | Verify no action buttons in any row | No Edit, Delete, Withdraw, or other mutation buttons are present |
| 4 | Change status dropdown to `active` | Course list refreshes; only courses with status `active` shown |
| 5 | Change status dropdown back to `all` | Full unfiltered course list restored |

---

## Tier 4 — Inspection checklist

These items are verified by code review and static inspection of the frontend
source. Each item maps to one or more requirements.

---

### TC-ADMIN-X-001 — Admin routes wrapped in role guard

```
// @{"verifies", ["REQ-FEADMIN-002", "REQ-FEADMIN-014", "REQ-FEADMIN-015"]}
```

**Method:** Open the Vue Router configuration file.

**Check:**

- [ ] All routes under the `/admin` prefix use a navigation guard (e.g.,
      `beforeEnter` or a global guard).
- [ ] The guard reads the authenticated user's role from the auth store.
- [ ] Unauthenticated users (no valid session) are redirected to `/login`.
- [ ] Users with `role !== "admin"` are redirected to `/dashboard`.

---

### TC-ADMIN-X-002 — Mutation requests include Authorization: Bearer header

```
// @{"verifies", ["REQ-FEADMIN-100", "REQ-FEADMIN-020"]}
```

**Method:** Inspect the API client module or composable used by admin views.

**Check:**

- [ ] `POST /api/v1/users` includes `Authorization: Bearer <token>`.
- [ ] `PATCH /api/v1/users/{id}` includes `Authorization: Bearer <token>`.
- [ ] `POST /api/v1/users/{id}/deactivate` includes `Authorization: Bearer <token>`.
- [ ] `POST /api/v1/users/{id}/activate` includes `Authorization: Bearer <token>`.
- [ ] `DELETE /api/v1/users/{id}` includes `Authorization: Bearer <token>`.
- [ ] All POST, PATCH, DELETE requests include the `X-CSRF-Token` header whose value matches the `__Host-csrf` cookie.

---

### TC-ADMIN-X-003 — GET /users includes Authorization: Bearer header (not session cookie only)

```
// @{"verifies", ["REQ-FEADMIN-100", "REQ-FEADMIN-020"]}
```

**Method:** Inspect the network layer used by `AdminUsersView` on mount.

**Check:**

- [ ] `GET /api/v1/users` is sent with `Authorization: Bearer <token>` in the
      request header.
- [ ] The token is sourced from the auth store, not from a cookie set by the
      browser automatically.

---

### TC-ADMIN-X-004 — Config PATCH body shape: {"config": {"key": "value"}} with all string values

```
// @{"verifies", ["REQ-FEADMIN-320", "REQ-FEADMIN-321", "REQ-FEADMIN-041"]}
```

**Method:** Inspect the save handler in `AdminConfigView`.

**Check:**

- [ ] The PATCH body is serialized as `{"config": {...}}` (top-level `config`
      key wrapping a flat object).
- [ ] All values in the inner object are coerced to strings before serialization
      (e.g., numeric fields are converted with `.toString()` or `String(v)`).
- [ ] Only keys whose current value differs from the fetched value are included.

---

### TC-ADMIN-X-005 — Audit pagination cursor is the integer field `before`, stops at 0

```
// @{"verifies", ["REQ-FEADMIN-203", "REQ-FEADMIN-204", "REQ-FEADMIN-031"]}
```

**Method:** Inspect the audit list composable or component.

**Check:**

- [ ] The pagination cursor is the integer field `next_before` from the
      response, not a UUID or string cursor.
- [ ] The Load More button is hidden (or disabled) when `next_before === 0`.
- [ ] Subsequent page requests use `?before={next_before}` as the query
      parameter name.

---

### TC-ADMIN-X-006 — Config weight cross-field validation uses 0.001 tolerance

```
// @{"verifies", ["REQ-FEADMIN-330", "REQ-FEADMIN-043"]}
```

**Method:** Inspect the config validator function.

**Check:**

- [ ] The sum check is `Math.abs(homeworkWeight + projectWeight - 1.0) >= 0.001`.
- [ ] The check uses `>= 0.001` (inclusive) so that a sum of exactly 0.999 or
      1.001 is flagged as invalid.
- [ ] Both weight fields are flagged simultaneously when the sum is out of range.

---

### TC-ADMIN-X-007 — Delete button rendered only for student role rows

```
// @{"verifies", ["REQ-FEADMIN-026"]}
```

**Method:** Inspect the template of `AdminUsersView` (or the user row
sub-component).

**Check:**

- [ ] The Delete button (or its container) is wrapped in a `v-if` (or
      equivalent) that evaluates to `true` only when `user.role === 'student'`.

---

### TC-ADMIN-X-008 — Create user role dropdown offers exactly student and admin

```
// @{"verifies", ["REQ-FEADMIN-113"]}
```

**Method:** Inspect the template of the create-user modal.

**Check:**

- [ ] The role `<select>` element has exactly two `<option>` children with
      values `"student"` and `"admin"`.
- [ ] No other option values (e.g., `"moderator"`) are present.

---

### TC-ADMIN-X-009 — Deactivate confirm dialog text contains username and consequence

```
// @{"verifies", ["REQ-FEADMIN-130"]}
```

**Method:** Inspect the deactivate confirmation dialog template or the string
literal passed to the confirm prompt.

**Check:**

- [ ] Dialog text template is
      `"Deactivate {username}? They will not be able to log in."` where
      `{username}` is replaced with the actual username at runtime.

---

### TC-ADMIN-X-010 — Delete confirm dialog text states permanent consequence

```
// @{"verifies", ["REQ-FEADMIN-140"]}
```

**Method:** Inspect the delete confirmation dialog template.

**Check:**

- [ ] Dialog text template is
      `"Permanently delete {username} and all their data? This cannot be undone."`
      where `{username}` is replaced with the actual username.

---

### TC-ADMIN-X-011 — Course oversight status dropdown contains all valid values

```
// @{"verifies", ["REQ-FEADMIN-403"]}
```

**Method:** Inspect the template of `AdminCoursesView`.

**Check:**

- [ ] The status filter dropdown contains options for all of:
      `all`, `intake`, `syllabus_draft`, `syllabus_approved`, `generating`,
      `active`, `archived`, `completed`.
- [ ] No other status values are present.

---

### TC-ADMIN-X-012 — Course oversight view renders no action buttons

```
// @{"verifies", ["REQ-FEADMIN-402"]}
```

**Method:** Inspect the template of `AdminCoursesView` and any course-row
sub-component.

**Check:**

- [ ] No `<button>` elements with labels Edit, Delete, Withdraw, Approve, or
      similar mutation-intent labels appear in the course row template.

---

### TC-ADMIN-X-013 — Edit user modal pre-fills from current row data

```
// @{"verifies", ["REQ-FEADMIN-123"]}
```

**Method:** Inspect the edit-user modal's initialization logic.

**Check:**

- [ ] When the edit modal opens for a user, the `username` input is initialized
      from `user.username`.
- [ ] The `email` input is initialized from `user.email` (or left blank if the
      user has no email).

---

## Coverage matrix

| Test case | Tier | Requirement(s) verified |
|---|---|---|
| TC-ADMIN-U-001 | Unit | REQ-FEADMIN-330, REQ-FEADMIN-043 |
| TC-ADMIN-U-002 | Unit | REQ-FEADMIN-330, REQ-FEADMIN-043 |
| TC-ADMIN-U-003 | Unit | REQ-FEADMIN-303, REQ-FEADMIN-334, REQ-FEADMIN-043 |
| TC-ADMIN-U-004 | Unit | REQ-FEADMIN-302, REQ-FEADMIN-333, REQ-FEADMIN-043 |
| TC-ADMIN-U-005 | Unit | REQ-FEADMIN-300, REQ-FEADMIN-301, REQ-FEADMIN-306, REQ-FEADMIN-307, REQ-FEADMIN-309, REQ-FEADMIN-310, REQ-FEADMIN-311, REQ-FEADMIN-331, REQ-FEADMIN-332, REQ-FEADMIN-337, REQ-FEADMIN-338, REQ-FEADMIN-340, REQ-FEADMIN-341, REQ-FEADMIN-342, REQ-FEADMIN-043 |
| TC-ADMIN-U-006 | Unit | REQ-FEADMIN-308, REQ-FEADMIN-339, REQ-FEADMIN-043 |
| TC-ADMIN-U-007 | Unit | REQ-FEADMIN-312, REQ-FEADMIN-343, REQ-FEADMIN-043 |
| TC-ADMIN-U-008 | Unit | REQ-FEADMIN-304, REQ-FEADMIN-305, REQ-FEADMIN-335, REQ-FEADMIN-336, REQ-FEADMIN-043 |
| TC-ADMIN-U-009 | Unit | REQ-FEADMIN-323, REQ-FEADMIN-042 |
| TC-ADMIN-U-010 | Unit | REQ-FEADMIN-202, REQ-FEADMIN-030, REQ-FEADMIN-032 |
| TC-ADMIN-U-011 | Unit | REQ-FEADMIN-002, REQ-FEADMIN-014, REQ-FEADMIN-015 |
| TC-ADMIN-U-012 | Unit | REQ-FEADMIN-320, REQ-FEADMIN-321, REQ-FEADMIN-041 |
| TC-ADMIN-I-001 | Integration | REQ-FEADMIN-100, REQ-FEADMIN-101, REQ-FEADMIN-102, REQ-FEADMIN-103, REQ-FEADMIN-020 |
| TC-ADMIN-I-044 | Integration | REQ-FEADMIN-100 |
| TC-ADMIN-I-002 | Integration | REQ-FEADMIN-015, REQ-FEADMIN-002 |
| TC-ADMIN-I-003 | Integration | REQ-FEADMIN-014, REQ-FEADMIN-002 |
| TC-ADMIN-I-004 | Integration | REQ-FEADMIN-114, REQ-FEADMIN-116, REQ-FEADMIN-021 |
| TC-ADMIN-I-005 | Integration | REQ-FEADMIN-115, REQ-FEADMIN-021 |
| TC-ADMIN-I-006 | Integration | REQ-FEADMIN-117, REQ-FEADMIN-021 |
| TC-ADMIN-I-007 | Integration | REQ-FEADMIN-118, REQ-FEADMIN-021 |
| TC-ADMIN-I-008 | Integration | REQ-FEADMIN-119, REQ-FEADMIN-021 |
| TC-ADMIN-I-009 | Integration | REQ-FEADMIN-110, REQ-FEADMIN-111, REQ-FEADMIN-112, REQ-FEADMIN-021 |
| TC-ADMIN-I-010 | Integration | REQ-FEADMIN-131, REQ-FEADMIN-132, REQ-FEADMIN-023 |
| TC-ADMIN-I-011 | Integration | REQ-FEADMIN-133, REQ-FEADMIN-023 |
| TC-ADMIN-I-012 | Integration | REQ-FEADMIN-134, REQ-FEADMIN-135, REQ-FEADMIN-024 |
| TC-ADMIN-I-013 | Integration | REQ-FEADMIN-141, REQ-FEADMIN-142, REQ-FEADMIN-025 |
| TC-ADMIN-I-014 | Integration | REQ-FEADMIN-144, REQ-FEADMIN-025 |
| TC-ADMIN-I-015 | Integration | REQ-FEADMIN-143, REQ-FEADMIN-025 |
| TC-ADMIN-I-016 | Integration | REQ-FEADMIN-026 |
| TC-ADMIN-I-017 | Integration | REQ-FEADMIN-027 |
| TC-ADMIN-I-018 | Integration | REQ-FEADMIN-028 |
| TC-ADMIN-I-019 | Integration | REQ-FEADMIN-120, REQ-FEADMIN-022 |
| TC-ADMIN-I-020 | Integration | REQ-FEADMIN-121, REQ-FEADMIN-022 |
| TC-ADMIN-I-021 | Integration | REQ-FEADMIN-122, REQ-FEADMIN-022 |
| TC-ADMIN-I-022 | Integration | REQ-FEADMIN-124, REQ-FEADMIN-022 |
| TC-ADMIN-I-023 | Integration | REQ-FEADMIN-200, REQ-FEADMIN-201, REQ-FEADMIN-030 |
| TC-ADMIN-I-024 | Integration | REQ-FEADMIN-203, REQ-FEADMIN-031 |
| TC-ADMIN-I-025 | Integration | REQ-FEADMIN-204, REQ-FEADMIN-031 |
| TC-ADMIN-I-026 | Integration | REQ-FEADMIN-210, REQ-FEADMIN-033 |
| TC-ADMIN-I-027 | Integration | REQ-FEADMIN-211, REQ-FEADMIN-033 |
| TC-ADMIN-I-028 | Integration | REQ-FEADMIN-212, REQ-FEADMIN-033 |
| TC-ADMIN-I-029 | Integration | REQ-FEADMIN-213, REQ-FEADMIN-033 |
| TC-ADMIN-I-030 | Integration | REQ-FEADMIN-300–REQ-FEADMIN-312, REQ-FEADMIN-040 |
| TC-ADMIN-I-031 | Integration | REQ-FEADMIN-323, REQ-FEADMIN-042 |
| TC-ADMIN-I-032 | Integration | REQ-FEADMIN-323, REQ-FEADMIN-042 |
| TC-ADMIN-I-033 | Integration | REQ-FEADMIN-320, REQ-FEADMIN-321, REQ-FEADMIN-041 |
| TC-ADMIN-I-034 | Integration | REQ-FEADMIN-324, REQ-FEADMIN-044 |
| TC-ADMIN-I-035 | Integration | REQ-FEADMIN-322, REQ-FEADMIN-041 |
| TC-ADMIN-I-036 | Integration | REQ-FEADMIN-325, REQ-FEADMIN-045 |
| TC-ADMIN-I-037 | Integration | REQ-FEADMIN-330, REQ-FEADMIN-043 |
| TC-ADMIN-I-038 | Integration | REQ-FEADMIN-018, REQ-FEADMIN-009 |
| TC-ADMIN-I-039 | Integration | REQ-FEADMIN-400, REQ-FEADMIN-401, REQ-FEADMIN-050 |
| TC-ADMIN-I-040 | Integration | REQ-FEADMIN-404, REQ-FEADMIN-051 |
| TC-ADMIN-I-041 | Integration | REQ-FEADMIN-405, REQ-FEADMIN-053 |
| TC-ADMIN-I-042 | Integration | REQ-FEADMIN-406, REQ-FEADMIN-053 |
| TC-ADMIN-I-043 | Integration | REQ-FEADMIN-405, REQ-FEADMIN-053 |
| TC-ADMIN-E-001 | E2E | REQ-FEADMIN-001, REQ-FEADMIN-007, REQ-FEADMIN-010–REQ-FEADMIN-013, REQ-FEADMIN-016 |
| TC-ADMIN-E-002 | E2E | REQ-FEADMIN-003, REQ-FEADMIN-008, REQ-FEADMIN-017, REQ-FEADMIN-020, REQ-FEADMIN-101–REQ-FEADMIN-103 |
| TC-ADMIN-E-003 | E2E | REQ-FEADMIN-021, REQ-FEADMIN-116, REQ-FEADMIN-113 |
| TC-ADMIN-E-004 | E2E | REQ-FEADMIN-023, REQ-FEADMIN-130, REQ-FEADMIN-132 |
| TC-ADMIN-E-005 | E2E | REQ-FEADMIN-024, REQ-FEADMIN-134, REQ-FEADMIN-135 |
| TC-ADMIN-E-006 | E2E | REQ-FEADMIN-025, REQ-FEADMIN-140, REQ-FEADMIN-142 |
| TC-ADMIN-E-007 | E2E | REQ-FEADMIN-004, REQ-FEADMIN-030, REQ-FEADMIN-032 |
| TC-ADMIN-E-008 | E2E | REQ-FEADMIN-033, REQ-FEADMIN-211 |
| TC-ADMIN-E-009 | E2E | REQ-FEADMIN-005, REQ-FEADMIN-040–REQ-FEADMIN-042, REQ-FEADMIN-044 |
| TC-ADMIN-E-010 | E2E | REQ-FEADMIN-006, REQ-FEADMIN-050–REQ-FEADMIN-052 |
| TC-ADMIN-X-001 | Inspection | REQ-FEADMIN-002, REQ-FEADMIN-014, REQ-FEADMIN-015 |
| TC-ADMIN-X-002 | Inspection | REQ-FEADMIN-100, REQ-FEADMIN-020 |
| TC-ADMIN-X-003 | Inspection | REQ-FEADMIN-100, REQ-FEADMIN-020 |
| TC-ADMIN-X-004 | Inspection | REQ-FEADMIN-320, REQ-FEADMIN-321, REQ-FEADMIN-041 |
| TC-ADMIN-X-005 | Inspection | REQ-FEADMIN-203, REQ-FEADMIN-204, REQ-FEADMIN-031 |
| TC-ADMIN-X-006 | Inspection | REQ-FEADMIN-330, REQ-FEADMIN-043 |
| TC-ADMIN-X-007 | Inspection | REQ-FEADMIN-026 |
| TC-ADMIN-X-008 | Inspection | REQ-FEADMIN-113 |
| TC-ADMIN-X-009 | Inspection | REQ-FEADMIN-130 |
| TC-ADMIN-X-010 | Inspection | REQ-FEADMIN-140 |
| TC-ADMIN-X-011 | Inspection | REQ-FEADMIN-403 |
| TC-ADMIN-X-012 | Inspection | REQ-FEADMIN-402 |
| TC-ADMIN-X-013 | Inspection | REQ-FEADMIN-123 |

*End of TEST-FE-ADMIN*
