# Valory E2E Test Suite

End-to-end tests for the Valory demo using Playwright + Chromium.  The suite
drives the **real running stack** at `https://localhost` — no mocking, no test
doubles.

---

## Two tiers

### AI-free tier (default)

Specs live directly under `e2e/*.spec.ts` (excluding the `journey/`
subdirectory).  These tests never send a chat message to the API and never
wait on AI-generated output, so they incur **zero Anthropic API cost**.  This
is the tier that runs on every push.

### Journey tier (paid, on-demand)

Specs live under `e2e/journey/**/*.spec.ts`.  These tests drive the full
student workflow end-to-end — including sending real messages and waiting for
AI-generated syllabus content (30–90 s).  **Each run costs real money.**  Run
only when you need to verify the AI pipeline end-to-end:

| Property | AI-free | Journey |
|---|---|---|
| Directory | `e2e/*.spec.ts` | `e2e/journey/**/*.spec.ts` |
| Timeout per test | 30 s | 240 s |
| Retries | 0 local / 2 CI | 0 always (each retry = paid API call) |
| Claude API calls | 0 | 1+ per spec |

**Cost expectation:** a single journey spec that creates a course and completes
intake typically consumes 1–3 Claude API calls (intake kickoff + chat turns +
syllabus generation).  At current pricing this is roughly $0.01–$0.10 per spec
run.  Never add the journey project to CI without budget approval.

---

## Prerequisites

### 1. Stack running

```bash
# From the repo root
docker compose up -d
```

Wait for all services (db, api, frontend/nginx) to be healthy:

```bash
docker compose ps
```

### 2. Demo accounts seeded

The suite expects two accounts with the following credentials (overridable via
environment variables — see below):

| Variable | Default | Role |
|---|---|---|
| `E2E_ADMIN_USER` | `admin` | admin |
| `E2E_ADMIN_PASS` | `ValoryDemo!2026` | — |
| `E2E_STUDENT_USER` | `demo_student` | student |
| `E2E_STUDENT_PASS` | `StudentDemo!2026` | — |

#### Seeding the first admin via psql

The application uses argon2id password hashing.  The first admin must be
inserted directly into the database because there is no self-registration flow.

Generate an argon2id hash for the password (requires the `argon2` CLI — install
via `brew install argon2` on macOS or the equivalent):

```bash
echo -n 'ValoryDemo!2026' | argon2 $(openssl rand -hex 16) -id -t 3 -m 12 -p 1 -l 32
```

Copy the full `$argon2id$...` string, then open a psql session against the
running container:

```bash
docker compose exec db psql -U postgres -d valory
```

Insert the admin user:

```sql
INSERT INTO users (username, password_hash, role, is_active, consent_given)
VALUES (
  'admin',
  '$argon2id$...',   -- paste your hash here
  'admin',
  true,
  true
);
```

Create the `demo_student` account the same way (role = `'student'`, use the
`StudentDemo!2026` password hash).  Students are subject to the AI consent
interstitial on first login; the helper in `e2e/helpers.ts` handles this
automatically.

Alternatively, log in as admin in the browser and use the User Management page
(`/admin/users`) to create the `demo_student` account after the admin exists.

---

## How to run

### AI-free tier (default)

```bash
# From the frontend/ directory
npm run test:e2e
```

Run with the HTML report open after completion:

```bash
npm run test:e2e -- --reporter=html
npx playwright show-report e2e-report
```

Run a single spec file:

```bash
npx playwright test --project=chromium e2e/auth.spec.ts
```

### Journey tier (on-demand, costs money)

```bash
# From the frontend/ directory
npm run test:e2e:journey
```

Run a single journey spec:

```bash
npx playwright test --project=journey e2e/journey/my-spec.spec.ts
```

### List tests without running them

```bash
# AI-free tier
npx playwright test --project=chromium --list

# Journey tier
npx playwright test --project=journey --list
```

### Environment variable overrides

```bash
E2E_BASE_URL=https://staging.example.com \
E2E_ADMIN_USER=myAdmin \
E2E_ADMIN_PASS=myPass \
E2E_STUDENT_USER=myStudent \
E2E_STUDENT_PASS=myStudentPass \
npm run test:e2e
```

---

## DB seeding helpers (`e2e/seed.ts`)

The seeding helpers let AI-free specs fixture deterministic data (chat history,
syllabi) into the database without going through the live AI endpoints.  They
work by shelling out to `docker compose exec -T db psql` from the repo root —
the DB port is not published to the host, so there is no direct connection.

### Available functions

```ts
import {
  seedChatMessage,
  seedSyllabus,
  setCourseStatus,
  cleanupCourse,
  markKickoffSent
} from './seed'
```

| Function | Description |
|---|---|
| `seedChatMessage(courseId, role, content)` | INSERT one chat message (role: `'student'` or `'assistant'`) |
| `seedSyllabus(courseId, contentAdoc)` | INSERT a syllabus row (auto-increments version) |
| `setCourseStatus(courseId, status)` | UPDATE courses.status to a valid enum value |
| `cleanupCourse(courseId)` | Archive the course unconditionally (idempotent) |
| `markKickoffSent(courseId)` | Set `intake_kickoff_sent=true, intake_kickoff_attempts=3` |

### The markKickoffSent rule

**Any AI-free spec that calls `seedChatMessage()` MUST call `markKickoffSent(courseId)` first, immediately after course creation.**

Reason: a freshly-created course has `intake_kickoff_sent=false`.  The backend
runs a background goroutine that watches for this flag and inserts the AI
professor's opening message as soon as a student loads the intake page.  If
your spec seeds its own assistant message before blocking that goroutine, the
kickoff goroutine may also insert a message, producing two assistant messages
and breaking any assertion on message count or content.

`markKickoffSent()` executes:

```sql
UPDATE courses
SET intake_kickoff_sent = true, intake_kickoff_attempts = 3, updated_at = now()
WHERE id = '<courseId>';
```

Setting `intake_kickoff_attempts=3` satisfies the hard cap check in
`kickoffIntake()` so the goroutine will not retry even if it already observed
`intake_kickoff_sent=false` before the UPDATE landed.

### Escaping strategy

All string values are passed through `sqLit()` which doubles every single-quote
character (`'` → `''`) — the standard PostgreSQL SQL-literal escaping rule.
UUID parameters are validated against a strict regex before interpolation so a
malformed UUID is rejected at call time.  Content strings must originate from
in-repo test fixtures, not from any user-controlled or network-sourced input.

### Usage example

```ts
// In your spec:
import { markKickoffSent, seedChatMessage, cleanupCourse } from './seed'

test('MySpec', async ({ page }) => {
  // ... create course via UI, extract courseId from URL ...

  // Block the kickoff goroutine BEFORE seeding chat.
  markKickoffSent(courseId)

  // Seed deterministic content.
  seedChatMessage(courseId, 'assistant', 'Welcome! What would you like to learn?')

  // ... reload, assert content, cleanup ...
  cleanupCourse(courseId)
})
```

---

## No-AI-cost guarantee (default tier)

The default tier **never** sends a message into the intake chat.  The
`student-course` and `intake-chat` specs create course rows and then
immediately withdraw them via `POST /api/v1/courses/:id/withdraw`.  The AI
professor agent is only invoked when the intake chat receives its first message
— which these tests explicitly avoid.

The `seed-smoke` spec uses `markKickoffSent()` to block the background kickoff
goroutine before seeding a deterministic assistant message, so it also incurs
zero AI cost.

---

## Artifacts

| Path | Contents |
|---|---|
| `e2e-report/` | HTML report from the last run (gitignored) |
| `test-results/` | Screenshots and traces from failed tests (gitignored) |

---

## Troubleshooting

**"You already have an active course" error in student-course.spec.ts**

This means a previous test run did not clean up.  Withdraw the stale course
manually:

```bash
# Get the course ID
docker compose exec db psql -U postgres -d valory \
  -c "SELECT id, status FROM courses WHERE student_id = (SELECT id FROM users WHERE username = 'demo_student');"

# Withdraw it
docker compose exec db psql -U postgres -d valory \
  -c "UPDATE courses SET status = 'archived' WHERE id = '<course-id>';"
```

Or use the admin Course Oversight view at `/admin/courses` to locate and manage
the stale course.
