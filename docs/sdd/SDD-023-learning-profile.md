# SDD-023 — Persistent Student Learning Profile and Onboarding

Sprint 23 | Status: DRAFT | Author: design-author | Date: 2026-06-12

---

## 1. Overview

### 1.1 Problem

Every course Valory generates today is personalized to the topic and the intake
conversation, but nothing persists across courses. A student who explained they
are a visual learner who prefers example-first explanations and works 10 hours
per week must repeat this every time they start a new course. The intake chat,
the professor, the reviewer, and the admin-assigned syllabus generator all
ignore prior session knowledge.

### 1.2 Approach

A single `learning_profiles` table (one row per student, RLS-enforced) stores a
natural-language summary of the student's learning style. A short onboarding
chat (3-5 turns, reusing the Chair plumbing) gathers preferences on first login,
then a single Anthropic call summarizes the answers into a concise profile text.
That text is subsequently injected into four prompt call sites: the intake system
prompt, the syllabus-from-params prompt, the professor section generation prompt,
and the regeneration prompt. When no profile exists all four call sites are
unchanged, so the feature is fully additive.

First-login UX: after the forced password change (Sprint 20), the student sees a
one-time onboarding nudge. The nudge is skippable and the onboarding can be
re-run or edited from a settings/profile surface. This is a **soft prompt**, not
a hard gate (unlike `must_change_password`).

### 1.3 Why natural-language summary, not structured JSON

The consumers are LLM system prompts. A natural-language paragraph injected as
a single sentence pair ("Learning style context: ...") is directly usable by
Claude without any template branching. A structured JSON object would require
either (a) a template that maps field keys to readable phrases at injection time,
or (b) trusting Claude to correctly parse an ad-hoc JSON blob mid-system-prompt.
The natural-language summary is simpler, equally informative, and immediately
graspable by the model. The trade-off is that it cannot be queried structurally —
but nothing in the system currently needs to do that. The `source` column
distinguishes onboarding-generated profiles from manually edited ones; nothing
more is required.

### 1.4 Why onboarding is a soft gate

Sprint 20's `must_change_password` is a hard gate because a temporary password is
a security risk that must be cleared before the student can use the system. A
missing learning profile is merely a personalization gap — the system is fully
usable without it. Making it a hard gate would harm UX for students who want to
explore the system before investing time in a questionnaire. The sprint plan
explicitly states "skip allowed (profile optional — prompts degrade gracefully)."

---

## 2. Requirements in Scope

Task 23.2 (requirements-author) will create the JSON files. Module assignments:

| ID | Module | Location | Title |
|----|--------|----------|-------|
| REQ-PROFILE-001 | PROFILE | `internal/profile/requirements/` | Learning profile table and RLS |
| REQ-PROFILE-002 | PROFILE | `internal/profile/requirements/` | Profile stored as natural-language text |
| REQ-PROFILE-003 | PROFILE | `internal/profile/requirements/` | Profile source column tracks origin |
| REQ-PROFILE-004 | PROFILE | `internal/profile/requirements/` | Student can read and update own profile |
| REQ-PROFILE-005 | PROFILE | `internal/profile/requirements/` | Server role can read any profile for prompt injection |
| REQ-PROFILE-006 | PROFILE | `internal/profile/requirements/` | Profile injection into professor generate prompt |
| REQ-PROFILE-007 | PROFILE | `internal/profile/requirements/` | Profile injection into professor regenerate prompt |
| REQ-PROFILE-008 | PROFILE | `internal/profile/requirements/` | Profile injection into intake system prompt |
| REQ-PROFILE-009 | PROFILE | `internal/profile/requirements/` | Profile injection into assignment syllabus prompt |
| REQ-PROFILE-010 | PROFILE | `internal/profile/requirements/` | Graceful degradation when profile absent |
| REQ-PROFILE-011 | PROFILE | `internal/profile/requirements/` | Onboarding chat conducts 3-5 question conversation |
| REQ-PROFILE-012 | PROFILE | `internal/profile/requirements/` | Onboarding summarization produces profile text |
| REQ-PROFILE-013 | PROFILE | `internal/profile/requirements/` | Onboarding token usage tracking (unbilled) |
| REQ-PROFILE-014 | PROFILE | `internal/profile/requirements/` | One-time onboarding nudge on first login (soft gate) |
| REQ-PROFILE-015 | PROFILE | `internal/profile/requirements/` | Student can skip onboarding |
| REQ-PROFILE-016 | PROFILE | `internal/profile/requirements/` | Student can re-run and edit profile from settings |
| REQ-FEPROFILE-001 | FEPROFILE | `frontend/src/requirements/` | OnboardingChatView renders chat UI on first login |
| REQ-FEPROFILE-002 | FEPROFILE | `frontend/src/requirements/` | ProfileSettingsView shows and edits profile text |
| REQ-FEPROFILE-003 | FEPROFILE | `frontend/src/requirements/` | Skip button dismisses onboarding and routes to courses |
| REQ-FEPROFILE-004 | FEPROFILE | `frontend/src/requirements/` | Router guard: nudge shown once on first login |

The `PROFILE` module is new. Its requirements live in `internal/profile/requirements/`
alongside the backend implementation, following the `ASSIGN` module pattern established
by SDD-022. The `FEPROFILE` module lives in `frontend/src/requirements/` following the
`FECOURSE`/`FEADMIN` pattern.

---

## 3. Data Model

### 3.1 New table: `learning_profiles`

```sql
CREATE TABLE IF NOT EXISTS learning_profiles (
    student_id   UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    summary      TEXT        NOT NULL DEFAULT '',
    source       TEXT        NOT NULL DEFAULT 'onboarding'
                             CHECK (source IN ('onboarding', 'manual_edit')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

GRANT SELECT, INSERT, UPDATE ON learning_profiles TO valory_app;
```

Column notes:

- `student_id` is the PRIMARY KEY: one profile per student. The FK cascades on
  delete so that removing a user cleans up the profile automatically. This mirrors
  the `student_id` FK pattern on `courses`.
- `summary` is a plain TEXT field. The onboarding flow writes a natural-language
  paragraph (target: 3-6 sentences). A manual edit replaces the entire summary;
  the UI renders it as a textarea. Max length enforced at the API layer (2000 chars).
- `source` tracks how the summary arrived: `'onboarding'` (LLM-generated from
  the chat) or `'manual_edit'` (student typed it directly). The CHECK constraint
  is the full allowed set; adding new sources requires a migration, which is
  intentional (controlled extensibility).
- `created_at` / `updated_at`: standard audit columns. `updated_at` is refreshed
  on every UPSERT via `ON CONFLICT DO UPDATE`.

No `id` UUID column is needed: `student_id` is sufficient as a primary key because
the one-profile-per-student constraint is structurally enforced. Callers always
look up by `student_id` and never need to reference a profile by a surrogate key.

### 3.2 New column: `users.onboarding_prompted`

```sql
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS onboarding_prompted BOOLEAN NOT NULL DEFAULT false;
```

This column is set to `true` the first time the onboarding nudge is shown to
the student. It is the signal used to suppress the nudge on subsequent logins.

**Why a column instead of inferring from `learning_profiles` existence:**
Inference would mean "no profile row = not yet prompted", which conflates
"has never been prompted" with "was prompted, chose to skip, and has no profile."
After a skip the student should not see the nudge again on the next login. A
separate boolean column correctly distinguishes the three states: never prompted,
prompted and completed (profile exists), prompted and skipped (profile absent,
`onboarding_prompted = true`). The column is on `users` because it is a session-
lifecycle flag, analogous to `must_change_password`.

**What the session endpoint returns:**
`GET /api/v1/auth/session` will include `"onboarding_prompted": true|false` so
the frontend guard can make the routing decision without an extra API call.

### 3.3 New table: `onboarding_messages`

```sql
CREATE TABLE IF NOT EXISTS onboarding_messages (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    student_id  UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role        TEXT        NOT NULL CHECK (role IN ('student', 'assistant')),
    content     TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS onboarding_messages_student_id_idx
    ON onboarding_messages (student_id, created_at ASC);

GRANT SELECT, INSERT, DELETE ON onboarding_messages TO valory_app;
```

This is the onboarding conversation history table, analogous to `chat_messages`
but keyed on `student_id` instead of `course_id`. It is populated turn-by-turn
during the onboarding chat and cleared (DELETE WHERE student_id = ...) after
the profile is successfully written, keeping the table small.

**Why a separate table, not reusing `chat_messages`:**
`chat_messages` is keyed on `course_id` with a mandatory FK. Onboarding has no
course. Forcing a sentinel course_id (e.g. all-zeros UUID) would violate the FK
constraint without further migration work and would require special-casing in
every query that reads `chat_messages`. A thin dedicated table is simpler, 
isolates onboarding concerns, and can be cleaned up after completion. The
alternative — a sentinel course row — would bloat `courses` and confuse the
`pollAndGenerate` pipeline.

### 3.4 Migration 020

File: `migrations/020_learning_profiles.sql`

The migration wraps all DDL in a BEGIN/COMMIT block, following the established
pattern from migrations 017-019. It includes:

1. Schema migrations idempotency row (`INSERT ... ON CONFLICT DO NOTHING`).
2. `learning_profiles` table + GRANT.
3. RLS policies on `learning_profiles` (detail in §5).
4. `onboarding_messages` table + index + GRANT.
5. RLS policies on `onboarding_messages` (detail in §5).
6. `users.onboarding_prompted` column.
7. Rollback comment block.

**Rollback path (additive-only, safe to reverse):**

```sql
-- Rollback 020_learning_profiles:
DROP TABLE IF EXISTS onboarding_messages;
DROP TABLE IF EXISTS learning_profiles;
ALTER TABLE users DROP COLUMN IF EXISTS onboarding_prompted;
DELETE FROM schema_migrations WHERE version = '020_learning_profiles';
```

No existing rows are mutated; the rollback is non-destructive to Sprint 22 and
earlier data.

---

## 4. API Contract

All endpoints are student-authenticated (the auth middleware sets
`app.current_user_id` and `app.current_role = 'student'`). The profile
endpoints use the request-scoped connection from `auth.ConnFromContext(ctx)`
exactly as the courses repository does, to pass RLS evaluation on
`learning_profiles` (FORCE ROW LEVEL SECURITY).

New handler package: `internal/profile/` with its own `Handler`, `Repository`,
and `Service`. The handler is mounted at `/api/v1/profile/...` by `cmd/server/main.go`.

### 4.1 GET /api/v1/profile

Returns the current student's profile. Returns 404 when no profile exists yet
(the frontend renders an empty "no profile yet" state). The student must be
authenticated.

**Response 200:**
```json
{
  "student_id": "uuid",
  "summary": "I prefer example-first explanations...",
  "source": "onboarding",
  "created_at": "2026-06-12T10:00:00Z",
  "updated_at": "2026-06-12T10:05:00Z"
}
```

**Response 404:**
```json
{ "error": "NOT_FOUND", "message": "no profile found" }
```

### 4.2 PUT /api/v1/profile

Creates or replaces the profile (upsert). Used by the settings view's manual
edit path.

**Request:**
```json
{
  "summary": "I prefer example-first explanations, work 8 hours per week..."
}
```

Validation: `summary` must be a non-empty string and at most 2000 characters.
Empty string is rejected with 400. The `source` is set to `'manual_edit'`.
The handler must use `conn(ctx)` (the request-scoped connection) so the RLS
INSERT/UPDATE policy for the student passes.

**Response 200:** same shape as GET.

**Error 400:**
```json
{ "error": "BAD_REQUEST", "message": "summary must be 1-2000 characters" }
```

### 4.3 POST /api/v1/profile/onboarding/start

Creates a new onboarding session for this student. If a prior partial session
exists (rows in `onboarding_messages`), it is cleared and restarted. This
supports re-running onboarding from settings.

This call triggers the onboarding agent to insert its first question into
`onboarding_messages` and returns it in the response.

The handler must use a server-role connection to perform the agent write (the
agent writes `onboarding_messages` as `app.current_role = 'server'`), exactly
as `chair.kickoffIntake` uses `db.AcquireServerConn`. The HTTP handler validates
auth via the request-scoped conn, then the agent operation uses the server pool.

**Response 200:**
```json
{
  "session_active": true,
  "first_message": "Hello! I'd like to learn a bit about your learning style..."
}
```

### 4.4 POST /api/v1/profile/onboarding/advance

Advances the onboarding conversation by one student turn. The student's message
is stored in `onboarding_messages` and the agent's reply is returned. When
`done = true` the conversation is complete; the client should immediately call
`/complete`.

**Request:**
```json
{ "message": "I prefer examples before theory." }
```

**Response 200:**
```json
{
  "reply": "Great! Do you have a preferred weekly time budget?...",
  "done": false
}
```

When `done = true`, `reply` contains the agent's closing acknowledgment and
the client calls `/complete` to trigger summarization.

### 4.5 POST /api/v1/profile/onboarding/complete

Triggers the summarization call: the full `onboarding_messages` history for
this student is sent to Claude (claude-haiku-4-5 for cost efficiency) with a
summarization prompt (see §6). The resulting text is written to
`learning_profiles` with `source = 'onboarding'`. The `onboarding_messages`
rows are deleted. The `users.onboarding_prompted` flag is set to `true` if not
already set.

**Response 200:**
```json
{
  "summary": "The student is an intermediate learner who prefers..."
}
```

**Error 409** (no active session):
```json
{ "error": "NO_ACTIVE_SESSION", "message": "no onboarding session in progress" }
```

### 4.6 POST /api/v1/profile/onboarding/skip

Sets `users.onboarding_prompted = true` without creating a profile. This is
the "skip" path: the nudge will not reappear on subsequent logins.

**Response 200:**
```json
{ "skipped": true }
```

### 4.7 GET /api/v1/auth/session (amendment)

The existing session endpoint must add `"onboarding_prompted": bool` to its
response body. The auth store and router guard use this flag to decide whether
to show the onboarding nudge. This requires a minor change to the session
handler in `internal/auth/` (dependency flagged in §9).

---

## 5. RLS Design

### 5.1 learning_profiles

```sql
ALTER TABLE learning_profiles ENABLE ROW LEVEL SECURITY;
ALTER TABLE learning_profiles FORCE ROW LEVEL SECURITY;

-- Student can read and write their own profile.
CREATE POLICY learning_profiles_student_policy ON learning_profiles
    USING (NULLIF(current_setting('app.current_role', true), '') = 'student'
       AND student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid);

-- Server (agent pipeline) can read any profile for prompt injection.
-- The professor, chair, and reviewer run as app.current_role = 'server'.
CREATE POLICY learning_profiles_server_select_policy ON learning_profiles
    FOR SELECT
    USING (current_setting('app.current_role', true) = 'server');

-- Server can also INSERT/UPDATE (used by the onboarding complete path,
-- which runs as a server-role operation).
CREATE POLICY learning_profiles_server_write_policy ON learning_profiles
    FOR INSERT
    WITH CHECK (current_setting('app.current_role', true) = 'server');

CREATE POLICY learning_profiles_server_update_policy ON learning_profiles
    FOR UPDATE
    USING (current_setting('app.current_role', true) = 'server');
```

**Admin read policy decision:** The sprint plan marks admin visibility as
optional and does not require it. This TDD does not include an admin read policy.
If a future sprint needs it, a migration adding a policy keyed on
`current_setting('app.current_role', true) = 'admin'` is additive and safe.
Omitting it now keeps the surface minimal and explicit: only the student and the
server role can touch `learning_profiles`.

**NULLIF guard:** All student-role policies use `NULLIF(..., '')` to match the
convention established in migrations 004 and 009. This prevents an empty GUC
string from casting to an invalid UUID and crashing the policy evaluation.

### 5.2 onboarding_messages

```sql
ALTER TABLE onboarding_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE onboarding_messages FORCE ROW LEVEL SECURITY;

-- Student can read their own messages.
CREATE POLICY onboarding_messages_student_select_policy ON onboarding_messages
    FOR SELECT
    USING (NULLIF(current_setting('app.current_role', true), '') = 'student'
       AND student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid);

-- Server can read, insert, and delete onboarding messages.
-- (Delete is needed for the cleanup step in /complete.)
CREATE POLICY onboarding_messages_server_policy ON learning_profiles
    USING (current_setting('app.current_role', true) = 'server');
```

Note: the server policy on `onboarding_messages` covers SELECT/INSERT/DELETE
(all DML the onboarding agent needs) via a single permissive policy using USING
(no FOR clause means all operations). This mirrors the pattern on `chat_messages`
(migration 011).

### 5.3 CRITICAL RLS LESSON — MANDATORY for 23.3 and 23.5

This section codifies the Sprint 22 RLS lesson for `learning_profiles`.

**Rule 1: Use `conn(ctx)` in the profile repository.**
The `ProfileRepository` must implement a `conn(ctx context.Context) db.Querier`
helper identical to `CourseRepository.conn` in `internal/course/repository.go`:

```go
func (r *ProfileRepository) conn(ctx context.Context) db.Querier {
    if c, ok := auth.ConnFromContext(ctx); ok {
        return c
    }
    return r.pool
}
```

HTTP handler paths call repository methods with the middleware context (which
carries `app.current_user_id` and `app.current_role` on the dedicated
connection). Agent paths (onboarding advance, complete) call via the server pool
context, which carries `app.current_role = 'server'`.

**Rule 2: The plain pool MUST NOT be used for student-path operations.**
`r.pool` acquires a fresh connection with empty GUCs (the `AfterRelease` hook
in `pool.go` clears them). A direct pool query on a FORCE RLS table will fail
with "new row violates row-level security policy" for INSERT/UPDATE and return
zero rows silently for SELECT.

**Rule 3: Agent writes to `learning_profiles` must use the server pool.**
The onboarding complete handler writes the summarized profile via the profile
repository's `UpsertProfileAsServer(ctx, studentID, summary)` method, which
internally calls `db.AcquireServerConn(ctx, pool)` (the server pool, not the
HTTP pool). This mirrors how `professor.go` uses `db.AcquireServerConn` for
`lesson_content` writes.

**Rule 4: Tests must exercise the real path under `SET ROLE valory_app`.**
The integration test suite (23.5) must use `db.AcquireAsUser` (for student-role
probes) and `db.AcquireAsServer` (for server-role probes) to exercise real RLS
enforcement. Tests using the bare pool (valory_test superuser) bypass FORCE RLS
entirely and cannot detect RLS bugs. See §8 for the specific required tests.

---

## 6. Agent Interactions — Onboarding Chat Flow

### 6.1 Conversation protocol

The onboarding chat is a 3-5 turn questionnaire conducted by Claude. Unlike the
per-course intake chat (which uses `chat_messages` keyed on `course_id`), this
chat uses `onboarding_messages` keyed on `student_id`. The conversation agent is
a lightweight function in `internal/profile/` that reuses the `ThrottledClient`
for API calls.

**Onboarding agent uses claude-haiku-4-5** (the cheaper model used by the
reviewer), not claude-sonnet-4-6, since the questionnaire is short and the
quality bar is lower than course generation.

**Turn mechanics (mirrors `chair.RunIntakeStep`):**

1. `POST /profile/onboarding/start` — handler calls the onboarding agent to
   produce the opening question. The agent inserts the first `assistant` message
   into `onboarding_messages` and returns it.
2. `POST /profile/onboarding/advance` — handler stores the student's message as
   a `student` row, calls the agent with full history, agent produces the next
   question or a closing reply with `ONBOARDING_COMPLETE` sentinel.
3. When `reply` contains the sentinel, `done = true` is returned; client calls
   `POST /profile/onboarding/complete`.
4. `complete` calls the summarization Claude call (below), writes the profile,
   deletes `onboarding_messages`, marks `onboarding_prompted = true`.

**Opening question prompt (system prompt for the onboarding agent):**

```
You are a learning assistant at Valory. Your goal is to understand this student's
learning preferences so their courses can be personalized.

Ask 3-5 short, friendly questions ONE AT A TIME covering:
1. Their general knowledge level (beginner / intermediate / advanced in general).
2. Their preferred explanation style (examples-first vs. theory-first).
3. How many hours per week they can study.
4. Any topics or subject areas they are particularly interested in or want to avoid.
5. Any learning challenges or preferences (e.g. "I struggle with abstract math").

When you have received at least 3 substantive answers that cover points 1-3 above,
include the exact text ONBOARDING_COMPLETE on its own line at the end of your reply.
Do not include this marker until you have enough information.

Keep your responses brief and encouraging.
```

The sentinel string is `ONBOARDING_COMPLETE` (analogous to `INTAKE_COMPLETE`
in `chair.go`).

### 6.2 Summarization call

After the conversation completes, a single Claude call (claude-haiku-4-5)
summarizes the history into a profile paragraph:

**System prompt for summarization:**
```
You are a concise summarizer. Based on the following learning preference
questionnaire responses, write a 3-5 sentence natural-language summary of
the student's learning profile. The summary will be injected into course
generation prompts. Write in third person ("The student prefers...").
Focus on: knowledge level, preferred explanation style, time budget, topic
interests, and any special learning needs.

Return ONLY the summary paragraph. No preamble, no JSON.
```

**User message:** the full conversation history formatted as:

```
Student's onboarding conversation:
Assistant: <opening question>
Student: <answer>
...
```

**Max tokens:** 512 (sufficient for a 3-5 sentence paragraph).

**Token budget (key decision):** Onboarding has no associated course, so the
`agent_token_usage` table (which has a UNIQUE constraint on `(student_id,
course_id)`) cannot be used directly. **Decision: onboarding tokens are
unbilled.** Justification:
- The onboarding conversation is 3-5 short turns plus one summarization call.
  Total token budget is roughly 1-2K input + 512 output per session — negligible
  relative to a full course generation (10-50K+ tokens).
- Adding a sentinel `course_id` (e.g. all-zeros UUID) to `agent_token_usage`
  would require either a migration to relax the FK constraint on `course_id`
  (currently referencing `courses.id`) or a dummy `courses` row, both of which
  add complexity for a one-time cost that is a rounding error.
- A separate `onboarding_token_usage` table would add schema complexity with no
  practical benefit — operators cannot act on this granular data (there is no
  per-student onboarding token cap in the admin config).
- The per-student token cap (`per_student_token_limit` config key) is a
  per-course budget keyed on `(student_id, course_id)`. It does not apply to
  onboarding. Onboarding calls are made directly via the `ThrottledClient` with
  a sentinel course UUID constant (`uuid.Nil`). `ThrottledClient.Messages` checks
  the cap by looking up `agent_token_usage WHERE student_id = $1 AND course_id = $2`.
  With `course_id = uuid.Nil` and no row in `agent_token_usage`, the cap check
  returns `used = 0` and always passes — which is the correct behavior.
  The token recording INSERT also uses `uuid.Nil` as `course_id`, which requires
  a one-line migration note: `agent_token_usage` already has
  `REFERENCES courses(id)`, so passing `uuid.Nil` would fail the FK check.
  Therefore onboarding calls bypass `ThrottledClient` entirely and call the
  Anthropic SDK directly (via the existing `SecretResolver` to get the key).
  This is simpler than patching `agent_token_usage`.

### 6.3 Profile injection helper

A new function `LoadProfileSummary(ctx context.Context, pool *pgxpool.Pool, studentID uuid.UUID) string`
lives in `internal/profile/`. It acquires a server-role connection
(`db.AcquireServerConn`), queries `learning_profiles WHERE student_id = $1`,
and returns the `summary` text. Returns `""` when no profile exists, which is
the graceful-degradation case.

**Injection points (all four prompt call sites in §6.4-6.7):**

The helper is called at the top of each prompt-building function. When the
return value is `""`, the prompt is identical to today's behavior (no change).
When non-empty, a profile context block is appended:

```
Student learning profile:
<summary>
```

This block is added as the last paragraph of the system prompt, after all other
context (search results, library context, etc.) but before the closing instruction.
Placing it last ensures the model reads the student context as the most proximate
signal before forming its response.

### 6.4 Injection point: `intakeSystemPrompt` (chair.go)

The `intakeSystemPrompt(topic string)` function signature changes to
`intakeSystemPrompt(topic string, profileSummary string) string`. When
`profileSummary != ""`, the following block is appended to the existing prompt:

```
Student learning profile (use this context to tailor your questions and the eventual syllabus):
<profileSummary>
```

**Caller change:** `chair.RunIntakeStepWithImages` calls `intakeSystemPrompt`. The
Chair struct receives a `profileLoader` dependency (an interface with
`LoadProfile(ctx, studentID) string`) injected at construction time, so it can
call it at each `RunIntakeStepWithImages` invocation. This avoids threading the
summary through the public method signature (backward-compatible with existing
callers).

Alternative: thread the summary through as a parameter of `RunIntakeStep`. Ruled
out because it would require changing the `IntakeStarter` interface and every
call site in `internal/course/handler.go` and tests.

### 6.5 Injection point: `syllabusSystemPrompt` and `assignmentSyllabusPrompt` (chair.go)

Same pattern as §6.4. The Sprint-22 seam comment in `GenerateSyllabusFromParams`
explicitly identifies this injection point:

```go
// Sprint 23 seam: when a per-student learning profile is available, inject it
// into the prompt via a profileRepo.GetProfile(ctx, studentID) call here and
// append the result to the system prompt alongside level/parameters.
// The studentID parameter is already threaded through; no interface change is needed.
```

`GenerateSyllabusFromParams` calls `assignmentSyllabusPrompt`, which will call
`LoadProfileSummary` and append the block if non-empty. `studentID` is already
a parameter of `GenerateSyllabusFromParams`.

`GenerateSyllabus` (student-initiated path) calls `syllabusSystemPrompt`, which
receives the same treatment.

### 6.6 Injection point: `GenerateSection` system prompt (professor.go)

The `GenerateSection` method already receives `studentID`. The profile loader is
injected into the `Professor` struct (same dependency injection pattern as
`SecretResolver`). The profile summary is loaded once at the top of
`GenerateSection` and appended to `systemPrompt` before the `client.Messages`
call. Same for `RegenerateSection`.

The exact injection site within the professor prompt is after the STEM notation
instructions and before the closing `syllabusSnippet/searchCtx/libraryCtx`
composition. This keeps the student context close to the end of the system
prompt where the model weighs it most heavily.

### 6.7 Injection point: `ReviewSection` (reviewer.go)

The reviewer is a quality-check agent (citation completeness, coherence). The
learning profile is not relevant to its evaluation criteria — it checks
structural quality, not content personalization. **The reviewer does not receive
profile injection.** This is an intentional exclusion: injecting it would not
change the review outcome and adds token cost to every content review call.

---

## 7. First-Login UX

### 7.1 Routing sequence

Full first-login sequence after Sprint 23:

```
Login → (must_change_password?) → /change-password → (after change) → courses
                                                               ↓
                                               (onboarding_prompted = false?)
                                                               ↓
                                               /onboarding (soft nudge)
                                              skip ↓       ↓ proceed
                                              /courses    /onboarding/chat
                                                               ↓
                                               (complete or skip at any turn)
                                                               ↓
                                                          /courses
```

The `mustChangePassword` hard gate (rule 3 in `guardFn`) runs first. After the
password change, `refreshSession()` is called, and if the new session has
`onboarding_prompted = false` and `role = 'student'`, the guard redirects to
`/onboarding`.

### 7.2 Router guard change

A new rule is added to `guardFn` in `frontend/src/router/index.ts` between
rules 3 and 4:

```
3a. Student has not been prompted for onboarding (onboarding_prompted = false)
    AND is navigating to anywhere except /onboarding, /change-password, /consent,
    /login, and /logout-equivalent paths:
    → redirect to '/onboarding'
```

This rule fires only for students, only when `onboarding_prompted = false`, and
only after the `mustChangePassword` check passes. It does not fire for admins.
The auth store adds `onboardingPrompted: boolean` derived from the session
response.

**The nudge is a redirect, not a modal.** A modal would require more complex
state management (dismiss state would need to survive navigation). A dedicated
`/onboarding` route follows the same pattern as `/change-password` and `/consent`.

### 7.3 Auth store change

The auth store adds `onboardingPrompted` ref (from session response) and exposes
it as a computed property. On `skip` completion or `complete` completion, the
frontend calls `refreshSession()` to pick up the updated `onboarding_prompted = true`.

### 7.4 `/onboarding` route

A new top-level route (not nested under StudentLayout, for the same reason
`/consent` is not — it is a gate page before the student reaches the main app).
Its single component `OnboardingView.vue` renders:

- An explanation paragraph ("Help us personalize your courses")
- The chat interface (reusing the same chat bubble + textarea pattern from
  `IntakeChatView.vue`, extracted into a shared composable `useOnboardingChat`)
- A "Skip for now" button that calls `POST /profile/onboarding/skip` and redirects
  to `/courses`
- A "Start" button (before the first advance call) and then auto-advance once
  the first message arrives from the server

### 7.5 Profile settings surface

A new sub-route under the student layout: `/profile` (name: `student-profile`).
This route is added to the StudentLayout children alongside `courses` and
`getting-started`. The `ProfileView.vue` component shows:

- Current profile summary (or "No profile yet" placeholder)
- A textarea for manual editing (PUT /api/v1/profile)
- A "Re-run onboarding" button (POST /profile/onboarding/start → navigate to
  `/onboarding`)
- Save / Cancel controls

The StudentLayout nav bar receives a "Profile" link.

---

## 8. Test Plan — Task 23.5 Obligation

### 8.1 RLS integration tests (MANDATORY — flag for SQE gate)

The following tests are required. They MUST use `db.AcquireAsUser` and
`db.AcquireAsServer` — NOT the bare pool — to exercise real RLS enforcement.
Tests using the bare pool run as the `valory_test` SUPERUSER which bypasses
FORCE ROW LEVEL SECURITY entirely.

Template: `internal/admin/assignment_integration_test.go`
`TestIntegration_RLS_NonSuperuser_*` tests.

Required tests in `internal/profile/` (build tag `integration`):

**Test 1: `TestIntegration_RLS_StudentCanReadOwnProfile`**
- Seed student A with a profile row (inserted via the bare pool as a fixture).
- `connA := db.AcquireAsUser(t, pool, hexA, "student")`
- SELECT from `learning_profiles WHERE student_id = $1`.
- Assert count = 1.

**Test 2: `TestIntegration_RLS_StudentCannotReadOtherStudentProfile`**
- Seed students A and B with profiles.
- `connB := db.AcquireAsUser(t, pool, hexB, "student")`
- SELECT from `learning_profiles WHERE student_id = $1` (using A's UUID).
- Assert count = 0 (RLS denies).

**Test 3: `TestIntegration_RLS_StudentCanUpsertOwnProfile`**
- `connA := db.AcquireAsUser(t, pool, hexA, "student")`
- INSERT ON CONFLICT DO UPDATE into `learning_profiles` for student A.
- Assert no error and row exists.

**Test 4: `TestIntegration_RLS_StudentCannotUpsertOtherStudentProfile`**
- `connB := db.AcquireAsUser(t, pool, hexB, "student")`
- Attempt INSERT into `learning_profiles` with `student_id = A's UUID`.
- Assert error (RLS policy violation, pgx returns `42501` or `ErrNoRows` for
  the USING check).

**Test 5: `TestIntegration_RLS_ServerCanReadAnyProfile`**
- Seed profiles for students A and B.
- `connServer := db.AcquireAsServer(t, pool)`
- SELECT all from `learning_profiles`.
- Assert both rows visible (count = 2).

**Test 6: `TestIntegration_RLS_ServerCanWriteProfile`**
- `connServer := db.AcquireAsServer(t, pool)`
- INSERT a profile row for a new student.
- Assert success and row readable.

**Test 7: `TestIntegration_ProfileRepo_UsesConnCtx_NotPool`**
- This is the `TestIntegration_RLS_NonSuperuser_*` pattern:
  acquire `connA` via `db.AcquireAsUser`, inject it into the context via
  `auth.ContextWithConn(ctx, connA)`, call `profileRepo.GetProfile(ctx, studentAID)`,
  assert it returns the correct profile.
  This proves the repository uses `conn(ctx)` (the request-scoped conn) not the
  bare pool, and that the real production path works under FORCE RLS.

### 8.2 Unit tests

- `profileSummaryPrompt` function produces output containing the student answers.
- `onboardingSystemPrompt` contains the `ONBOARDING_COMPLETE` sentinel reference.
- Profile injection: `intakeSystemPrompt("topic", "summary text")` contains
  "Student learning profile:" and the summary.
- Profile injection: with empty summary, the prompt is identical to the no-profile
  baseline.
- `LoadProfileSummary` returns `""` for a student with no row.

### 8.3 Integration test: profile reaches prompt assembly

A test that:
1. Seeds a profile for student A.
2. Calls `LoadProfileSummary(ctx, serverPool, studentAID)`.
3. Asserts the returned string equals the seeded summary.
4. Calls `intakeSystemPrompt("Go programming", returnedSummary)`.
5. Asserts the system prompt contains the summary text.

This closes the chain between the DB row and the prompt — the "profile reaches
prompt assembly" verification required by the sprint plan (23.5).

### 8.4 E2E: onboarding journey (one budgeted live AI run)

Following the sprint plan convention:
- Student logs in (new account, `must_change_password = true`).
- Changes password (Sprint 20 path).
- Router guard redirects to `/onboarding` (asserts route = `/onboarding`).
- Student completes 3 turns of onboarding chat (live AI call).
- Completion triggers profile creation.
- Assert `GET /api/v1/profile` returns a non-empty summary.
- Navigate to `/courses`, create a course.
- Assert intake system prompt (captured via a test hook or log) contains the
  profile summary text.

This is the single budgeted live AI run. The AI-free suite covers all other paths.

---

## 9. Cross-Module Dependencies

| Dependency | Owner | Nature |
|---|---|---|
| `GET /api/v1/auth/session` must return `onboarding_prompted` | `internal/auth` handler | Additive field — auth handler reads `users.onboarding_promoted`; add to session response struct and query |
| `users.onboarding_prompted` column | migration 020 (this sprint) | Users table write from profile skip/complete endpoints (server-role conn) |
| `ThrottledClient` and `SecretResolver` | `internal/agent` | Profile package imports `agent.SecretResolver` interface OR defines its own equivalent narrow interface (preferred to avoid import cycle) |
| `db.AcquireServerConn` | `internal/db` | Used by profile repository's server-role write path |
| `auth.ConnFromContext` | `internal/auth` | Used by profile repository's `conn(ctx)` helper |
| `MustChangePasswordMiddleware` | `internal/auth` | No change required; onboarding nudge is frontend-only (router guard) |
| `cmd/server/main.go` | wiring | Mount `/api/v1/profile/...` routes; inject profile loader into Chair and Professor at construction |
| StudentLayout nav bar | `frontend/src/layouts/` | Add "Profile" link pointing to `/profile` |
| `guardFn` in router | `frontend/src/router/index.ts` | Add rule 3a for `onboarding_prompted` check |
| Auth store | `frontend/src/stores/auth.ts` | Add `onboardingPrompted` field |

The auth handler change is the only cross-module backend change. The implementing
engineer (23.3) must coordinate with the auth module owner or implement the field
addition themselves, as no other Sprint 23 task touches `internal/auth`.

---

## 10. Alternatives Considered

### 10.1 Structured JSON profile instead of natural-language text

A structured profile like `{"level": "intermediate", "style": "examples-first",
"hours_per_week": 8}` was considered. Rejected because:
- The consumers are LLM system prompts that receive free text. A JSON blob in a
  system prompt is decoded by the model but adds no structure that the model can
  act on differently from prose.
- Rendering structured fields into readable prose requires a template layer
  (`fmt.Sprintf("The student is at %s level and prefers %s-first explanations...")`).
  A natural-language summary written by the LLM is better quality and more
  nuanced than a template.
- The personalization benefit comes from the model having richer context, not
  from the context being machine-parseable.

### 10.2 Re-use `chat_messages` with a sentinel course_id

Using `uuid.Nil` as a sentinel `course_id` for onboarding messages was rejected
because `chat_messages.course_id` references `courses(id)` with a FK constraint.
Inserting `uuid.Nil` would fail unless a dummy `courses` row is first inserted.
A dedicated `onboarding_messages` table avoids the FK hack and is easier to clean
up after the session completes.

### 10.3 Hard gate (like `must_change_password`) for onboarding

Rejected per the sprint plan: "skip allowed (profile optional — prompts degrade
gracefully)." A hard gate would block students who want to immediately start
learning. The value of the profile increases over repeated courses; forcing it
before any course is premature.

### 10.4 Charge onboarding tokens to a sentinel course row

Creating a dummy `courses` row to anchor `agent_token_usage` for onboarding calls
would require: a migration (dummy FK resolution), a service-level sentinel UUID
constant, and special-casing in the token reporting UI (admins would see a phantom
course row). The onboarding token cost (~1-2K tokens total) is negligible, and
the per-student token cap (`per_student_token_limit`) is a per-course cap that
does not apply to cross-course infrastructure. Unbilled is the simplest correct
choice.

### 10.5 Modal nudge instead of a dedicated route

A "complete your learning profile" modal on the `/courses` page was considered.
Rejected because:
- Modals require persistent dismiss-state management (which flag do we set? when?
  on what event?).
- The pattern established by `/consent` and `/change-password` is a dedicated full-
  screen route for first-login interpositions. Consistency with that pattern is
  worth more than avoiding an extra route.
- The chat UI is complex enough that embedding it in a modal is awkward.

### 10.6 Inject profile into the reviewer

Rejected (see §6.7). The reviewer checks citation completeness and structural
coherence — neither depends on student learning style. Adding the profile there
adds token cost with zero benefit.

---

## 11. Open Questions

1. **Profile summary length cap.** This TDD specifies 2000 characters at the API
   layer. Is this sufficient? A typical LLM-generated 4-sentence summary is ~300-400
   characters; the 2000-character cap exists mainly to bound manual edits.

2. **Re-onboarding replaces or appends?** This TDD specifies that re-running
   onboarding replaces the `summary` (full overwrite via UPSERT). A "blend" option
   (LLM merges old and new summaries) is not specified. If the PM wants a merge
   option, it requires a prompt change and is a nice-to-have for a future sprint.

3. **Admin visibility of student profiles.** The sprint plan says optional. This
   TDD omits an admin read policy. If the PM later requires admins to view
   student profiles (e.g. for support or moderation), a migration adding an admin
   read policy is fully backward-compatible. Flag for PM review.

4. **`onboarding_prompted` name collision check.** The migration adds
   `users.onboarding_prompted`. Verify there is no existing column with this name
   (the current migration 017 adds only `must_change_password`; this appears safe
   but the implementing engineer must confirm by inspecting the actual `users` table
   column list before writing migration 020).

---

## 12. Acceptance Criteria

### Task 23.3 (Backend) Checklist

- [ ] `migrations/020_learning_profiles.sql` applies cleanly against the existing schema.
- [ ] `learning_profiles` table exists with FORCE RLS and all four policies
      (student USING, server SELECT, server INSERT WITH CHECK, server UPDATE).
- [ ] `onboarding_messages` table exists with FORCE RLS and policies.
- [ ] `users.onboarding_prompted` column exists (DEFAULT false).
- [ ] `internal/profile/` package contains Handler, Repository, Service, and
      onboarding agent.
- [ ] All six profile endpoints respond correctly (GET, PUT, start, advance,
      complete, skip).
- [ ] `GET /api/v1/auth/session` returns `"onboarding_prompted": bool`.
- [ ] `ProfileRepository.conn(ctx)` uses `auth.ConnFromContext` correctly.
- [ ] `LoadProfileSummary` returns `""` for missing profiles.
- [ ] Profile injection is wired into `intakeSystemPrompt`, `syllabusSystemPrompt`,
      `assignmentSyllabusPrompt`, `GenerateSection`, and `RegenerateSection`.
- [ ] No profile injection in `ReviewSection`.
- [ ] Onboarding tokens are unbilled (no `agent_token_usage` writes).
- [ ] No secrets appear in any response body, log, or audit payload.

### Task 23.4 (Frontend) Checklist

- [ ] `/onboarding` route exists and is reachable only when `onboarding_prompted = false`.
- [ ] `guardFn` rule 3a redirects students with `onboarding_prompted = false` to
      `/onboarding` (after password change, after consent).
- [ ] Auth store exposes `onboardingPrompted` from session data.
- [ ] `OnboardingView.vue` renders chat bubbles, send input, and a skip button.
- [ ] Skip button calls `POST /profile/onboarding/skip` and redirects to `/courses`.
- [ ] Complete path calls `/complete` and redirects to `/courses`.
- [ ] `/profile` route (student settings) shows current summary, textarea for edit,
      re-run button, save/cancel.
- [ ] StudentLayout nav bar includes a "Profile" link.
- [ ] When no profile exists, GET returns 404 and the UI shows "No profile yet."
- [ ] `mustChangePassword` gate takes precedence over onboarding nudge (rule 3
      runs before rule 3a in `guardFn`).

### Task 23.5 (Tests) Checklist

- [ ] All 7 RLS integration tests listed in §8.1 exist under `//go:build integration`.
- [ ] All tests use `db.AcquireAsUser` or `db.AcquireAsServer` — no bare-pool RLS probes.
- [ ] Unit tests for prompt injection (with and without profile summary).
- [ ] Integration test: `LoadProfileSummary` end-to-end from DB row to prompt text.
- [ ] E2E onboarding journey (one budgeted live AI run).
- [ ] Test for `guardFn`: `onboarding_prompted = false` redirects to `/onboarding`.
- [ ] Test for `guardFn`: `mustChangePassword = true` takes precedence.

---

*Sprint 23 — Final sprint. This profile injection closes the personalization loop the entire system is built around.*
