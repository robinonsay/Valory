# Admin Configuration Guide

**Audience:** operators and administrators managing a Valory deployment.

**Satisfies:** REQ-FEONBOARD-030, REQ-FEONBOARD-031, REQ-FEONBOARD-032, REQ-FEONBOARD-033.

Every configuration change is written to the database and simultaneously
reloads the in-memory `ConfigService`, so new values take effect for all
subsequent requests without an API restart. Each successful change also
produces a tamper-evident audit log entry — plan your changes accordingly.

---

## Navigating to the Config page

1. Log in with an account that has the `admin` role.
2. Select **Config** in the left-hand navigation sidebar.
3. The page loads the current values of all thirteen `system_config` keys from
   `GET /api/v1/admin/config`.

---

## Making a change

1. Edit one or more fields in the Config form.
2. The UI tracks unsaved changes; a banner appears while edits are pending.
3. Click **Save**. The frontend sends a `PATCH /api/v1/admin/config` request
   with only the keys you edited.
4. On success the form refreshes with the server-confirmed values and the
   unsaved-changes banner clears.

### Validation errors

If a value fails server-side validation the API returns HTTP `422
Unprocessable Entity` with a body of the form:

```json
{
  "validation_errors": [
    "homework_weight: homework_weight + project_weight must equal 1.0",
    "project_weight: homework_weight + project_weight must equal 1.0"
  ]
}
```

Errors are sorted alphabetically. The form surfaces each message inline next
to its field. Correct the values and save again — no partial writes occur
because all updates run in a single database transaction.

---

## Key reference

| Key | UI label | Default | Validation rule | What it controls |
|---|---|---|---|---|
| `agent_retry_limit` | Agent Retry Limit | `3` | integer >= 1 | Maximum number of Anthropic API call attempts per request before raising `ErrRateLimitExhausted`. Exponential backoff with jitter is applied between attempts. |
| `correction_loop_max_iterations` | Correction Loop Max Iterations | `5` | integer >= 1 | Maximum reviewer–professor correction cycles per generated section before the loop is escalated to the admin without blocking content delivery. |
| `per_student_token_limit` | Per Student Token Limit | `500000` | integer >= 0 | Cumulative Anthropic token cap (input + output) per student per course. Set to `0` to disable the cap entirely. Enforced before every AI call. |
| `late_penalty_rate` | Late Penalty Rate | `0.05` | float in [0.0, 1.0] | Fraction of raw score deducted per calendar day a submission is late. At the default `0.05` a submission one day late loses 5 % of its raw score. |
| `homework_weight` | Homework Weight | `0.7` | float > 0.0 and <= 1.0; must sum to 1.0 with `project_weight` | Weight applied to homework scores when computing the course weighted average. |
| `project_weight` | Project Weight | `0.3` | float > 0.0 and <= 1.0; must sum to 1.0 with `homework_weight` | Weight applied to project scores when computing the course weighted average. |
| `session_inactivity_seconds` | Session Inactivity (seconds) | `1800` | integer >= 1 | Reserved. The key is persisted and validated but the running server reads the inactivity period from the `AUTH_INACTIVITY_PERIOD` environment variable, not from this key. Changing it has no runtime effect until wired. |
| `account_lockout_seconds` | Account Lockout (seconds) | `900` | integer >= 1 | Reserved. The key is persisted and validated but the running server reads the lockout duration from the `AUTH_LOCKOUT_DURATION` environment variable, not from this key. Changing it has no runtime effect until wired. |
| `max_upload_bytes` | Max Upload Size (bytes) | `10485760` | integer >= 1024 | Maximum file size accepted for homework submission uploads. The HTTP body reader is capped at this value + 1 byte so over-limit requests are rejected before disk write. Default is 10 MiB. |
| `content_generation_timeout_seconds` | Content Generation Timeout (seconds) | `300` | integer >= 1 | Per-course content generation deadline. If the full section pipeline does not complete within this duration, the run is cancelled, a `generation_timeout` event is emitted, and the student is notified. |
| `audit_retention_days` | Audit Retention (days) | `365` | integer >= 1 | Reserved. The key is persisted and validated but no background worker currently purges aged audit log entries. The audit log is append-only by design (UPDATE and DELETE are revoked from the `valory_app` role); automated purging has not yet been implemented. |
| `notification_retention_days` | Notification Retention (days) | `90` | integer >= 1 | Notifications older than this many days are deleted by a background worker that runs every 24 hours. |
| `consent_version` | Consent Version | `1.0` | non-empty string | Current AI consent version that students must have accepted. See the Consent Version section below for bump semantics. |

---

## Weight cross-rule

`homework_weight` and `project_weight` must sum to exactly `1.0` within a
tolerance of `0.001`. The server enforces this cross-key constraint whenever
either weight is included in a PATCH request: if only one is supplied, the
server substitutes the current stored value of the other before checking the
sum.

**Example — raising homework weight from 0.7 to 0.8:**

The current values are `homework_weight = 0.7`, `project_weight = 0.3`.

- Sending `{"config": {"homework_weight": "0.8"}}` alone will fail: the server
  computes `0.8 + 0.3 = 1.1`, which exceeds the 0.001 tolerance.
- Send both keys together: `{"config": {"homework_weight": "0.8", "project_weight": "0.2"}}`.
  The server computes `0.8 + 0.2 = 1.0`, which passes, and both rows are
  written in the same transaction.

The UI enforces the same rule client-side before submitting, but the server
check is authoritative.

---

## `consent_version` semantics

`consent_version` is a dot-separated version string (e.g. `1.0`, `1.1`,
`2.0`). The auth middleware compares each student's stored consent version
against the current value using semantic component ordering (`1.10 > 1.9`).

**Bumping the version is a gate action.** Any student whose stored version is
strictly less than the current `consent_version` receives a `403
CONSENT_REQUIRED` response on every protected endpoint until they accept the
new version at `POST /api/v1/consent`. Admins are exempt from the consent gate
regardless of the configured version.

Before bumping:
- Notify students in advance that re-consent will be required.
- Confirm the new consent text is published and reachable at the consent
  acceptance page.
- A bump cannot be undone without lowering the version string, which would
  re-admit students who had not yet accepted the new version.

---

## Audit trail

Every successful `PATCH /api/v1/admin/config` call writes one entry to the
tamper-evident `audit_log` table with:

| Field | Value |
|---|---|
| `action` | `config.change` |
| `target_type` | `system_config` |
| `target_id` | `NULL` |
| `payload` | `{"keys_changed": ["key1", "key2", ...]}` (sorted alphabetically) |
| `admin_id` | UUID of the admin who made the change |

The payload records which keys were changed but not the old or new values.
To see the before/after values, read the `system_config` table directly or
compare successive GET responses.

To view audit entries in the UI, navigate to **Audit Log** in the left-hand
sidebar. The Audit Log page supports cursor-based pagination and chain
integrity verification.

---

## Immediate effect

Changes take effect immediately for all new requests. After `tx.Commit`
succeeds, the handler calls `configSvc.Load(ctx)`, which re-reads
`system_config` from the database and atomically swaps the in-memory map
under a write lock. No API restart is required.

Background workers (the agent runner polling loops, the notification retention
worker) read from `ConfigService` on each poll cycle, so they also pick up new
values within their next cycle without restart.

---

## API alternative (curl)

The Config page is the preferred interface, but the endpoint is accessible
directly. The CSRF middleware requires both the `__Host-csrf` cookie and a
matching `X-CSRF-Token` header on all non-GET requests. Obtain a CSRF token
from the login response cookie.

```bash
curl -X PATCH https://<host>/api/v1/admin/config \
  -H "Authorization: Bearer <token>" \
  -H "X-CSRF-Token: <csrf_token>" \
  -H "Cookie: __Host-csrf=<csrf_token>" \
  -H "Content-Type: application/json" \
  -d '{"config": {"agent_retry_limit": "5"}}'
```

The `__Host-csrf` cookie is `Secure; SameSite=Strict` and carries no
`HttpOnly` flag, so JavaScript can read it for the header. The cookie value
and the header value must be identical byte-for-byte; the middleware uses
`hmac.Equal` for the comparison.

**Response (200 OK):**

```json
{
  "updated_keys": ["agent_retry_limit"],
  "config": { ... }
}
```

**Response (422 Unprocessable Entity):**

```json
{
  "validation_errors": ["agent_retry_limit must be an integer >= 1"]
}
```

**Response (400 Bad Request):** returned when a key is not in the allowed set
or the request body is malformed. Unknown keys are rejected before validation
runs.
