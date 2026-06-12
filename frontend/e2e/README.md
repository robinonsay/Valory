# Valory E2E Test Suite

End-to-end tests for the Valory demo using Playwright + Chromium.  The suite
drives the **real running stack** at `https://localhost` — no mocking, no test
doubles.

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

```bash
# From the frontend/ directory
npx playwright test
```

Run with the HTML report open after completion:

```bash
npx playwright test --reporter=html
npx playwright show-report e2e-report
```

Run a single spec file:

```bash
npx playwright test e2e/auth.spec.ts
```

### Environment variable overrides

```bash
E2E_BASE_URL=https://staging.example.com \
E2E_ADMIN_USER=myAdmin \
E2E_ADMIN_PASS=myPass \
E2E_STUDENT_USER=myStudent \
E2E_STUDENT_PASS=myStudentPass \
npx playwright test
```

---

## No-AI-cost guarantee

The suite **never** sends a message into the intake chat.  The `student-course`
spec creates a course row (a cheap database insert) and then immediately
withdraws it via `POST /api/v1/courses/:id/withdraw` using the bearer token
captured from the login response.  The AI professor agent is only invoked when
the intake chat receives its first message — which these tests explicitly avoid.

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
