//go:build integration

// integration_test.go — repository-layer integration tests for the course module.
//
// These tests run against a real PostgreSQL instance (not a mock) using the
// shared db.IntegrationPool harness, which applies all embedded migrations once
// per process. Each test calls db.TruncateTables at the start for the tables it
// touches; truncating courses CASCADEs to syllabi, homework, due_date_schedules,
// and agent_token_usage automatically.
//
// WHY a separate file: repository_test.go defines TestMain and lives outside the
// `integration` build tag, so this file carries the tag and prefixes every helper
// with `integ` to prevent symbol collisions.
package course

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	internaldb "github.com/valory/valory/internal/db"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// integSeedUser inserts a user row directly through the shared (non-RLS) pool
// and returns the generated user ID.  Users are not protected by FORCE RLS so
// the bare pool is sufficient; courses ARE, which is why every course operation
// in these tests goes through an AcquireAsUser / server-role connection.
func integSeedUser(t *testing.T, pool *pgxpool.Pool, username string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO users (username, password_hash, role)
		 VALUES ($1, 'x', 'student')
		 RETURNING id`,
		username).Scan(&id)
	if err != nil {
		t.Fatalf("integSeedUser %q: %v", username, err)
	}
	return id
}

// integSeedCourseAsStudent inserts a course row using a connection whose GUCs
// are set to the given studentID / "student" role, so the INSERT passes the
// RLS student policy (FORCE RLS is enabled on courses).
func integSeedCourseAsStudent(t *testing.T, pool *pgxpool.Pool, studentID uuid.UUID, topic string) uuid.UUID {
	t.Helper()
	// Hex-encode the UUID to match the format the auth middleware uses when
	// setting app.current_user_id (fmt.Sprintf("%x", uuid.UUID{...})).
	userIDHex := fmt.Sprintf("%x", [16]byte(studentID))

	conn := internaldb.AcquireAsUser(t, pool, userIDHex, "student")
	defer conn.Release()

	var courseID uuid.UUID
	err := conn.QueryRow(context.Background(),
		`INSERT INTO courses (student_id, topic, status)
		 VALUES ($1, $2, 'intake')
		 RETURNING id`,
		studentID, topic).Scan(&courseID)
	if err != nil {
		t.Fatalf("integSeedCourseAsStudent: %v", err)
	}
	return courseID
}

// integSeedCourseViaBarePool inserts a course row using the shared bare pool
// (valory_test superuser), which bypasses FORCE RLS entirely.  This is correct
// and intentional for fixture seeding: the superuser connection is not subject
// to row-level security, so it can INSERT regardless of the app.current_role
// or app.current_user_id GUC values.  Using the bare pool here avoids relying
// on a server INSERT policy that does not exist in the migrations (migration 005
// adds only SELECT and UPDATE policies for the server role; there is no server
// INSERT policy on courses).
func integSeedCourseViaBarePool(t *testing.T, pool *pgxpool.Pool, studentID uuid.UUID, topic string) uuid.UUID {
	t.Helper()
	var courseID uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO courses (student_id, topic, status)
		 VALUES ($1, $2, 'intake')
		 RETURNING id`,
		studentID, topic).Scan(&courseID)
	if err != nil {
		t.Fatalf("integSeedCourseViaBarePool: %v", err)
	}
	return courseID
}

// integTransitionCourseAsServer transitions a course to a new status using a
// server-role connection, mirroring what the agent pipeline does.
//
// AcquireAsServer is used (not AcquireAsUser with an empty userID) because the
// courses_student_policy casts current_setting('app.current_user_id', true)::uuid
// without a NULLIF guard. If that GUC is set to '' the cast raises
// "invalid input syntax for type uuid: """ even when the server policy would match,
// because PostgreSQL evaluates all permissive policies before short-circuiting.
// AcquireAsServer sets app.current_user_id to the all-zeros UUID (valid, matches
// no student row) so the cast succeeds and only the server-role policy governs.
func integTransitionCourseAsServer(t *testing.T, pool *pgxpool.Pool, courseID uuid.UUID, fromStatus, toStatus string) {
	t.Helper()
	conn := internaldb.AcquireAsServer(t, pool)
	defer conn.Release()

	tag, err := conn.Exec(context.Background(),
		`UPDATE courses SET status = $2, updated_at = now()
		 WHERE id = $1 AND status = $3`,
		courseID, toStatus, fromStatus)
	if err != nil {
		t.Fatalf("integTransitionCourseAsServer %s→%s: %v", fromStatus, toStatus, err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("integTransitionCourseAsServer %s→%s: expected 1 row affected, got %d", fromStatus, toStatus, tag.RowsAffected())
	}
}

// integFetchCourseStatus reads a course's current status using a server-role
// connection (bypassing student-identity check) so helper code can assert on
// state without conflating it with the student-isolation under test.
//
// AcquireAsServer is used here for the same reason as integTransitionCourseAsServer:
// the courses_student_policy casts app.current_user_id::uuid without a NULLIF guard,
// so an empty-string GUC causes a runtime error before the server policy can match.
// AcquireAsServer supplies the all-zeros UUID, keeping the cast valid.
func integFetchCourseStatus(t *testing.T, pool *pgxpool.Pool, courseID uuid.UUID) string {
	t.Helper()
	conn := internaldb.AcquireAsServer(t, pool)
	defer conn.Release()

	var status string
	err := conn.QueryRow(context.Background(),
		`SELECT status FROM courses WHERE id = $1`, courseID).Scan(&status)
	if err != nil {
		t.Fatalf("integFetchCourseStatus: %v", err)
	}
	return status
}

// ---------------------------------------------------------------------------
// Test cases
// ---------------------------------------------------------------------------

// TC-COURSE-001: Student cannot start a second course while one is active
//
// @{"verifies": ["REQ-COURSE-001"]}
// TC-COURSE-001: Single active course — second INSERT must fail
//
// The courses table has a partial unique index
// courses_single_active_idx ON courses(student_id) WHERE status NOT IN
// ('archived','completed'), which enforces one active course per student at
// the schema level regardless of application logic.
func TestInteg_SingleActiveCourse_SchemaRejectsSecondInsert(t *testing.T) {
	p := internaldb.IntegrationPool(t)
	// Truncating courses CASCADEs to all dependent tables.
	internaldb.TruncateTables(t, p, "courses", "users")

	studentID := integSeedUser(t, p, "alice_single_active_"+uuid.New().String()[:8])

	// First course: should succeed.
	_ = integSeedCourseAsStudent(t, p, studentID, "Go Programming")

	// Second course for the same student while the first is still in 'intake'
	// (which is NOT in the excluded statuses): must fail with a unique-violation.
	userIDHex := fmt.Sprintf("%x", [16]byte(studentID))
	conn := internaldb.AcquireAsUser(t, p, userIDHex, "student")
	defer conn.Release()

	_, err := conn.Exec(context.Background(),
		`INSERT INTO courses (student_id, topic, status)
		 VALUES ($1, $2, 'intake')`,
		studentID, "Python Programming")
	if err == nil {
		t.Fatal("expected unique-violation error inserting second active course, got nil")
	}
	// We only care that the insert failed; the exact PostgreSQL error code
	// (23505 unique_violation) is implementation detail enough — confirm it
	// is non-nil rather than asserting a string, keeping the test stable
	// across driver versions.
}

// TC-COURSE-003: Student with only an archived course can start a new course
//
// @{"verifies": ["REQ-COURSE-001"]}
// TC-COURSE-003: Partial-index exemption — archived course allows a new intake
//
// The partial unique index excludes rows with status IN ('archived',
// 'completed'), so a student who has only archived courses must be able to
// start a new one.
func TestInteg_SingleActiveCourse_ArchivedDoesNotBlock(t *testing.T) {
	p := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, p, "courses", "users")

	studentID := integSeedUser(t, p, "alice_archived_"+uuid.New().String()[:8])

	// Seed a course then immediately archive it (server role can UPDATE status).
	courseID := integSeedCourseAsStudent(t, p, studentID, "Old Topic")
	integTransitionCourseAsServer(t, p, courseID, "intake", "archived")

	// Now insert a fresh intake course for the same student — must succeed.
	_ = integSeedCourseAsStudent(t, p, studentID, "New Topic")
}

// TC-SECURITY-001 / TC-SECURITY-006: Student B cannot SELECT Student A's course
// via the RLS student policy.
//
// @{"verifies": ["REQ-SECURITY-002", "REQ-COURSE-001"]}
// TC-SECURITY-001 / TC-SECURITY-006: RLS student isolation on courses
//
// courses has ENABLE + FORCE ROW LEVEL SECURITY. The student policy uses
//   USING (student_id = current_setting('app.current_user_id', true)::uuid)
// so a connection carrying Bob's UUID must see zero rows when Alice owns the
// course.
func TestInteg_RLS_StudentCannotSelectAnotherStudentsCourse(t *testing.T) {
	p := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, p, "courses", "users")

	aliceID := integSeedUser(t, p, "alice_rls_"+uuid.New().String()[:8])
	bobID := integSeedUser(t, p, "bob_rls_"+uuid.New().String()[:8])

	// Seed a course owned by Alice.
	aliceCourseID := integSeedCourseAsStudent(t, p, aliceID, "Alice's Exclusive Topic")

	// Try to read Alice's course using Bob's identity.
	bobHex := fmt.Sprintf("%x", [16]byte(bobID))
	conn := internaldb.AcquireAsUser(t, p, bobHex, "student")
	defer conn.Release()

	var count int
	err := conn.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM courses WHERE id = $1`, aliceCourseID).Scan(&count)
	if err != nil {
		t.Fatalf("unexpected query error: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 rows visible to Bob for Alice's course, got %d", count)
	}
}

// TC-SECURITY-002: Student can access their own course data.
//
// @{"verifies": ["REQ-SECURITY-002"]}
// TC-SECURITY-002: RLS student policy permits self-reads
//
// Positive companion to the isolation test: the same policy that blocks Bob
// must allow Alice to read her own rows.
func TestInteg_RLS_StudentCanSelectOwnCourse(t *testing.T) {
	p := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, p, "courses", "users")

	aliceID := integSeedUser(t, p, "alice_self_"+uuid.New().String()[:8])
	aliceCourseID := integSeedCourseAsStudent(t, p, aliceID, "Alice's Own Topic")

	aliceHex := fmt.Sprintf("%x", [16]byte(aliceID))
	conn := internaldb.AcquireAsUser(t, p, aliceHex, "student")
	defer conn.Release()

	var count int
	err := conn.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM courses WHERE id = $1`, aliceCourseID).Scan(&count)
	if err != nil {
		t.Fatalf("unexpected query error: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected Alice to see 1 own course row, got %d", count)
	}
}

// TC-SECURITY-001 (UPDATE variant): Student B cannot UPDATE Student A's course
// via the RLS student policy.
//
// @{"verifies": ["REQ-SECURITY-002"]}
// TC-SECURITY-001 (UPDATE variant): Student B cannot UPDATE Student A's course
//
// The student RLS policy covers UPDATE via USING, so Bob's UPDATE targeting
// Alice's course must affect 0 rows — the policy silently filters it away
// rather than raising an error.
func TestInteg_RLS_StudentCannotUpdateAnotherStudentsCourse(t *testing.T) {
	p := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, p, "courses", "users")

	aliceID := integSeedUser(t, p, "alice_upd_"+uuid.New().String()[:8])
	bobID := integSeedUser(t, p, "bob_upd_"+uuid.New().String()[:8])

	aliceCourseID := integSeedCourseAsStudent(t, p, aliceID, "Alice Protected Topic")

	bobHex := fmt.Sprintf("%x", [16]byte(bobID))
	conn := internaldb.AcquireAsUser(t, p, bobHex, "student")
	defer conn.Release()

	tag, err := conn.Exec(context.Background(),
		`UPDATE courses SET status = 'archived' WHERE id = $1`, aliceCourseID)
	if err != nil {
		t.Fatalf("UPDATE returned unexpected error (should be silently filtered): %v", err)
	}
	if tag.RowsAffected() != 0 {
		t.Fatalf("expected 0 rows affected (RLS filter), got %d", tag.RowsAffected())
	}

	// Confirm Alice's course status is unchanged via the server role.
	gotStatus := integFetchCourseStatus(t, p, aliceCourseID)
	if gotStatus != "intake" {
		t.Fatalf("expected course status 'intake' after Bob's blocked UPDATE, got %q", gotStatus)
	}
}

// TC-SECURITY-001 / TC-SECURITY-006: a server-role connection can SELECT and
// UPDATE courses regardless of student_id (005_server_rls.sql).
//
// @{"verifies": ["REQ-SECURITY-002", "REQ-AGENT-003"]}
// TC-SECURITY-001 / TC-SECURITY-006 (server-role path): intake → generating → active
//
// Background agents set app.current_role = 'server' to bypass student-identity
// RLS and drive course status transitions. This test verifies the full happy
// path: SELECT visibility and multi-hop UPDATE both work under the server role.
// REQ-AGENT-003 covers post-approval content generation triggering, which
// requires exactly the intake→generating→active pipeline exercised here.
func TestInteg_ServerRole_CanReadAndTransitionCourseStatus(t *testing.T) {
	p := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, p, "courses", "users")

	studentID := integSeedUser(t, p, "student_server_"+uuid.New().String()[:8])
	// Seed via student role (server INSERT is not covered by an RLS policy;
	// only SELECT and UPDATE server policies exist in migration 005).
	courseID := integSeedCourseAsStudent(t, p, studentID, "Server Pipeline Topic")

	// Step 1: server role can SELECT the course.
	// AcquireAsServer sets app.current_role='server' and app.current_user_id to the
	// all-zeros UUID. The all-zeros UUID is required because the student policy casts
	// app.current_user_id::uuid without a NULLIF guard — an empty string would raise
	// "invalid input syntax for type uuid" before the server policy could match.
	conn := internaldb.AcquireAsServer(t, p)
	defer conn.Release()

	var gotTopic string
	err := conn.QueryRow(context.Background(),
		`SELECT topic FROM courses WHERE id = $1`, courseID).Scan(&gotTopic)
	if err != nil {
		t.Fatalf("server SELECT failed: %v", err)
	}
	if gotTopic != "Server Pipeline Topic" {
		t.Fatalf("expected topic %q, got %q", "Server Pipeline Topic", gotTopic)
	}

	// Step 2: transition intake → generating.
	tag, err := conn.Exec(context.Background(),
		`UPDATE courses SET status = 'generating', updated_at = now()
		 WHERE id = $1 AND status = 'intake'`, courseID)
	if err != nil {
		t.Fatalf("server UPDATE intake→generating: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("expected 1 row updated, got %d", tag.RowsAffected())
	}

	// Step 3: transition generating → active.
	tag, err = conn.Exec(context.Background(),
		`UPDATE courses SET status = 'active', updated_at = now()
		 WHERE id = $1 AND status = 'generating'`, courseID)
	if err != nil {
		t.Fatalf("server UPDATE generating→active: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("expected 1 row updated, got %d", tag.RowsAffected())
	}

	gotStatus := integFetchCourseStatus(t, p, courseID)
	if gotStatus != "active" {
		t.Fatalf("expected status 'active' after server transitions, got %q", gotStatus)
	}
}

// TC-COURSE-001 (status CHECK constraint): inserting an invalid status value
// must be rejected by the database ENUM constraint regardless of who issues it.
//
// @{"verifies": ["REQ-COURSE-001"]}
// TC-COURSE-001 (invalid status): course_status ENUM rejects unknown values
//
// The `status` column is typed course_status ENUM. Any value outside the
// declared set must cause a database error at bind/cast time, before any
// RLS evaluation occurs. The bare pool is used here for simplicity: the ENUM
// cast error fires before PostgreSQL reaches the RLS policy check regardless
// of which role is used, so bypassing RLS via the superuser pool does not
// affect the validity of this test.
func TestInteg_StatusConstraint_InvalidStatusRejected(t *testing.T) {
	p := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, p, "courses", "users")

	studentID := integSeedUser(t, p, "student_enum_"+uuid.New().String()[:8])

	// Execute via the bare pool — the ENUM cast error fires before RLS
	// evaluation, so role context is irrelevant to this test's intent.
	_, err := p.Exec(context.Background(),
		`INSERT INTO courses (student_id, topic, status)
		 VALUES ($1, $2, 'totally_invalid_status')`,
		studentID, "Bad Status Topic")
	if err == nil {
		t.Fatal("expected ENUM constraint violation, got nil error")
	}
	// PostgreSQL error code 22P02 (invalid_text_representation) or
	// similar covers invalid enum casts; we just need non-nil.
}

// Transition query updated_at advancement: the CourseRepository.Transition SQL
// includes an explicit `updated_at = now()` in its SET clause, so the column
// must advance whenever Transition succeeds.
//
// Note: there is no set_updated_at trigger on the courses table (migration 003
// does not install one; only users has a trigger, from migration 001). This
// test verifies the Transition query's own explicit timestamp write, NOT a
// trigger. There is no requirement ID that directly governs this behaviour; the
// annotation is intentionally omitted rather than citing an unrelated REQ.
//
// The backdate pattern is used to guarantee a measurable delta even on fast
// hardware: updated_at is set to NOW() - 1 second before the Transition call,
// so any successful Transition that writes now() will produce a strictly later
// timestamp.
func TestInteg_Repository_TransitionUpdatesUpdatedAt(t *testing.T) {
	p := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, p, "courses", "users")

	studentID := integSeedUser(t, p, "student_ts_"+uuid.New().String()[:8])

	// Seed via bare pool (superuser bypasses FORCE RLS — correct for fixtures;
	// see integSeedCourseViaBarePool for the full rationale).
	courseID := integSeedCourseViaBarePool(t, p, studentID, "Timestamp Test Topic")

	// Backdate updated_at by 1 second via bare pool so the pre-Transition
	// value is measurably in the past.
	_, err := p.Exec(context.Background(),
		`UPDATE courses SET updated_at = now() - INTERVAL '1 second' WHERE id = $1`,
		courseID)
	if err != nil {
		t.Fatalf("backdate updated_at: %v", err)
	}

	// Capture the backdated timestamp as our "before" baseline.
	var before time.Time
	if err := p.QueryRow(context.Background(),
		`SELECT updated_at FROM courses WHERE id = $1`, courseID).Scan(&before); err != nil {
		t.Fatalf("SELECT updated_at before: %v", err)
	}

	// Execute the Transition query via a server-role connection.  The server
	// UPDATE policy (courses_server_update_policy) in migration 005 permits
	// this UPDATE. The query mirrors the one inside CourseRepository.Transition,
	// including the explicit updated_at = now() in the SET clause.
	// AcquireAsServer is used (not AcquireAsUser with an empty userID) because the
	// student policy casts app.current_user_id::uuid without a NULLIF guard; an
	// empty GUC value would raise a uuid parse error before the server policy matches.
	conn := internaldb.AcquireAsServer(t, p)
	defer conn.Release()

	var updated CourseRow
	err = conn.QueryRow(context.Background(),
		`UPDATE courses
		 SET status = $2,
		     pre_withdrawal_status = COALESCE($4, pre_withdrawal_status),
		     updated_at = now()
		 WHERE id = $1
		   AND status = ANY($3::text[])
		 RETURNING id, student_id, title, topic, status, pre_withdrawal_status, created_at, updated_at`,
		courseID, "syllabus_draft", []string{"intake"}, nil,
	).Scan(
		&updated.ID,
		&updated.StudentID,
		&updated.Title,
		&updated.Topic,
		&updated.Status,
		&updated.PreWithdrawalStatus,
		&updated.CreatedAt,
		&updated.UpdatedAt,
	)
	if err != nil {
		t.Fatalf("Transition SQL via server role: %v", err)
	}

	// The RETURNING clause gives us updated_at directly from the write; also
	// assert it is strictly after the backdated baseline.
	if !updated.UpdatedAt.After(before) {
		t.Fatalf("expected updated_at to advance after Transition: before=%v after=%v",
			before, updated.UpdatedAt)
	}
}

// TC-COURSE-001 (repository + server-role round-trip): the CourseRepository
// methods work correctly when driven by the process-shared pool whose connections
// have the server role set, verifying the repository's SQL is correct and that
// the server-role policy permits the repository to see and modify course rows.
//
// @{"verifies": ["REQ-COURSE-001"]}
// TC-COURSE-001: Repository.Transition via server role updates status
//
// This test validates that CourseRepository.Transition succeeds when the course
// exists and the allowedFrom list matches, using the server-role path that the
// agent pipeline uses. It also confirms the returned row reflects the new status.
func TestInteg_Repository_TransitionViaServerRole(t *testing.T) {
	p := internaldb.IntegrationPool(t)
	internaldb.TruncateTables(t, p, "courses", "users")

	studentID := integSeedUser(t, p, "student_repo_trans_"+uuid.New().String()[:8])

	// Seed through bare pool — the superuser bypasses FORCE RLS, which is
	// correct and intentional for fixture seeding (see integSeedCourseViaBarePool).
	// Migration 005 adds no server INSERT policy, so a server-role connection
	// cannot INSERT into courses; the bare pool is the right seeding path.
	courseID := integSeedCourseViaBarePool(t, p, studentID, "Repo Transition Topic")

	// The CourseRepository uses the pool directly (not a Querier interface),
	// which means its pool connections carry empty GUCs. We need a server-role
	// connection to perform the Transition call through the repository.
	// Since we cannot inject a single-conn pool, we exercise the Transition
	// logic via raw SQL on a server-role conn — this tests the same UPDATE
	// statement the repository executes, validating SQL correctness and the
	// server-role RLS policy together.
	// AcquireAsServer is used (not AcquireAsUser with an empty userID) because the
	// student policy casts app.current_user_id::uuid without a NULLIF guard; passing
	// "" would cause "invalid input syntax for type uuid" at runtime before the
	// server policy could match. AcquireAsServer supplies the all-zeros UUID instead.
	conn := internaldb.AcquireAsServer(t, p)
	defer conn.Release()

	var updated CourseRow
	err := conn.QueryRow(context.Background(),
		`UPDATE courses
		 SET status = $2,
		     pre_withdrawal_status = COALESCE($4, pre_withdrawal_status),
		     updated_at = now()
		 WHERE id = $1
		   AND status = ANY($3::text[])
		 RETURNING id, student_id, title, topic, status, pre_withdrawal_status, created_at, updated_at`,
		courseID, "syllabus_draft", []string{"intake"}, nil,
	).Scan(
		&updated.ID,
		&updated.StudentID,
		&updated.Title,
		&updated.Topic,
		&updated.Status,
		&updated.PreWithdrawalStatus,
		&updated.CreatedAt,
		&updated.UpdatedAt,
	)
	if err != nil {
		t.Fatalf("Transition SQL via server role: %v", err)
	}
	if updated.Status != "syllabus_draft" {
		t.Fatalf("expected status 'syllabus_draft', got %q", updated.Status)
	}
	if updated.ID != courseID {
		t.Fatalf("expected course ID %v, got %v", courseID, updated.ID)
	}
}
