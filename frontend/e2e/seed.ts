// frontend/e2e/seed.ts
//
// DB seeding helpers for the Valory AI-free e2e tier.
//
// WHY: AI-free specs need deterministic data (seeded chat history, syllabi)
// without going through the live chat endpoint (which calls Claude).  The DB
// port is NOT published to the host, so we reach it via `docker compose exec`.
//
// ESCAPING STRATEGY:
//   All values injected into SQL literals are treated as potentially untrusted
//   text coming from test fixtures.  We escape single-quotes by doubling them
//   (the standard PostgreSQL SQL-literal escaping rule) and wrap the value in
//   single-quote delimiters.  The SQL is passed to psql via STDIN (not via the
//   -c flag) so there is NO shell quoting layer on top of the SQL quoting —
//   only one escaping level is active at a time.  UUIDs are validated with a
//   regex before use so an injection attempt via a malformed UUID is rejected.
//
// KICKOFF COLLISION RULE:
//   If a course has intake_kickoff_sent=false the background kickoff goroutine
//   may insert the assistant opening message at any time.  Any spec that seeds
//   chat messages MUST call markKickoffSent(courseId) immediately after course
//   creation to atomically prevent that goroutine from inserting a duplicate
//   opening message.  See the docstring on markKickoffSent() below.
//
// WORKING DIRECTORY:
//   docker compose must be invoked from the repo root.  We derive that path
//   from __filename (this file lives at frontend/e2e/seed.ts, so two parent
//   directories up is the repo root).

import { execSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// repoRoot is derived from the location of this source file so it is correct
// regardless of the CWD at invocation time.
const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..')

// UUID_RE validates that a caller-supplied string is a standard UUID before we
// interpolate it into SQL.  A non-UUID value is rejected at call time rather
// than reaching the DB engine.
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

// sqLit escapes a string value for safe interpolation into a PostgreSQL
// single-quoted string literal.  The only transformation needed is to double
// any single-quote characters inside the value (SQL standard §4.1.2.1).
// Callers must only pass values that originate from in-repo test fixtures —
// not from any user-controlled or network-sourced input.
function sqLit(value: string): string {
  return "'" + value.replace(/'/g, "''") + "'"
}

// assertUUID throws if value is not a valid UUID string.  Call this for every
// UUID parameter before interpolating into SQL, even though UUIDs come from
// our own test code — defence in depth.
function assertUUID(value: string, name: string): void {
  if (!UUID_RE.test(value)) {
    throw new Error(`seed.ts: ${name} is not a valid UUID: ${JSON.stringify(value)}`)
  }
}

// runPsql pipes a SQL statement to psql running inside the db container via
// `docker compose exec -T`.  Using STDIN (the `input` option) means the SQL
// string is delivered byte-for-byte without any shell-quoting layer on top of
// the SQL-literal escaping we already apply inside sqLit().  This avoids the
// double-escaping problem that would arise from passing the SQL via `-c "..."`.
//
// Throws with the full psql stderr output when the command exits non-zero so
// failures surface immediately rather than silently corrupting test state.
function runPsql(sql: string): void {
  try {
    execSync(
      'docker compose exec -T db psql -U postgres -d valory',
      {
        cwd: repoRoot,
        // Pass the SQL through stdin — no shell escaping needed on the SQL text.
        input: sql,
        stdio: ['pipe', 'pipe', 'pipe']
      }
    )
  } catch (err: unknown) {
    const e = err as { stderr?: Buffer | string; stdout?: Buffer | string; status?: number }
    const stderr = e.stderr?.toString() ?? ''
    const stdout = e.stdout?.toString() ?? ''
    throw new Error(
      `seed.ts: psql command failed (exit ${e.status ?? '?'})\n` +
      `SQL: ${sql}\n` +
      `stderr: ${stderr}\n` +
      `stdout: ${stdout}`
    )
  }
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// @{"req": ["REQ-FECOURSE-023", "REQ-FECOURSE-260"]}
//
// seedChatMessage inserts a single chat message for the given course.
//
// role must be 'student' or 'assistant'.  content is the message body text.
// The INSERT is done as the postgres superuser, bypassing RLS, so the helper
// works without the app-role session variable setup.
//
// IMPORTANT: call markKickoffSent(courseId) before calling this function on a
// freshly created course.  See KICKOFF COLLISION RULE at the top of this file.
export function seedChatMessage(
  courseId: string,
  role: 'student' | 'assistant',
  content: string
): void {
  assertUUID(courseId, 'courseId')
  if (role !== 'student' && role !== 'assistant') {
    throw new Error(`seed.ts: role must be 'student' or 'assistant', got: ${JSON.stringify(role)}`)
  }
  const sql =
    `INSERT INTO chat_messages (course_id, role, content) ` +
    `VALUES (${sqLit(courseId)}, ${sqLit(role)}::chat_role, ${sqLit(content)});`
  runPsql(sql)
}

// @{"req": ["REQ-FECOURSE-300", "REQ-FECOURSE-301"]}
//
// seedSyllabus inserts a syllabus row for the given course.  The version is
// computed as MAX(version)+1 for that course so successive calls accumulate
// the correct version sequence without a race.  An INSERT...SELECT is used so
// the version arithmetic happens atomically inside the DB.
export function seedSyllabus(courseId: string, contentAdoc: string): void {
  assertUUID(courseId, 'courseId')
  // The subquery returns 1 when no prior row exists (COALESCE(MAX,0)+1).
  const sql =
    `INSERT INTO syllabi (course_id, content_adoc, version) ` +
    `SELECT ${sqLit(courseId)}, ${sqLit(contentAdoc)}, COALESCE(MAX(version), 0) + 1 ` +
    `FROM syllabi WHERE course_id = ${sqLit(courseId)};`
  runPsql(sql)
}

// @{"req": ["REQ-FECOURSE-001", "REQ-FECOURSE-002"]}
//
// setCourseStatus updates the status column on a course row.
// Only the canonical course_status enum values are accepted.
type CourseStatus =
  | 'intake'
  | 'syllabus_draft'
  | 'syllabus_approved'
  | 'generating'
  | 'active'
  | 'archived'
  | 'completed'

export function setCourseStatus(courseId: string, status: CourseStatus): void {
  assertUUID(courseId, 'courseId')
  // status is a TypeScript-typed union, so arbitrary strings cannot reach here;
  // the sqLit() call is belt-and-suspenders protection.
  const sql =
    `UPDATE courses SET status = ${sqLit(status)}::course_status, updated_at = now() ` +
    `WHERE id = ${sqLit(courseId)};`
  runPsql(sql)
}

// @{"req": ["REQ-FECOURSE-001"]}
//
// cleanupCourse archives the course unconditionally.  Use at the end of every
// spec that creates a course via DB seeding so the single-active-course
// constraint cannot block the next run.  Idempotent: calling it on an already-
// archived/completed course is a no-op (the UPDATE just touches zero rows).
export function cleanupCourse(courseId: string): void {
  assertUUID(courseId, 'courseId')
  const sql =
    `UPDATE courses SET status = 'archived'::course_status, updated_at = now() ` +
    `WHERE id = ${sqLit(courseId)} AND status NOT IN ('archived','completed');`
  runPsql(sql)
}

// @{"req": ["REQ-FECOURSE-023", "REQ-FECOURSE-260"]}
//
// markKickoffSent sets intake_kickoff_sent=true and intake_kickoff_attempts=3
// for the given course.  This atomically prevents the background kickoff
// goroutine from inserting an assistant opening message while the spec is
// seeding its own chat history.
//
// RULE: call this immediately after course creation in any AI-free spec that
// will subsequently call seedChatMessage().  Failing to call it creates a race
// where the goroutine fires before the seed INSERT lands, producing a duplicate
// assistant opening message that breaks message-count assertions.
export function markKickoffSent(courseId: string): void {
  assertUUID(courseId, 'courseId')
  const sql =
    `UPDATE courses ` +
    `SET intake_kickoff_sent = true, intake_kickoff_attempts = 3, updated_at = now() ` +
    `WHERE id = ${sqLit(courseId)};`
  runPsql(sql)
}

// @{"req": ["REQ-FEAUTH-056"]}
//
// revokeConsent deletes a user's AI-processing consent record so a spec can
// exercise the REAL first-login consent journey deterministically (the router
// guard sends unconsented students to /consent and bounces consented ones off
// it).  The spec MUST restore consent before finishing — other specs (e.g.
// ConsentSkipped_WhenAlreadyConsented) depend on demo_student being consented.
// Username is restricted to a strict allowlist pattern rather than SQL-escaped.
export function revokeConsent(username: string): void {
  if (!/^[a-zA-Z0-9_]{1,64}$/.test(username)) {
    throw new Error(`revokeConsent: unsafe username ${JSON.stringify(username)}`)
  }
  const sql =
    `DELETE FROM student_consent WHERE student_id = ` +
    `(SELECT id FROM users WHERE username = ${sqLit(username)});`
  runPsql(sql)
}
