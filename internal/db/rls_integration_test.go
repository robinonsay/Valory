//go:build integration

// rls_integration_test.go — regression tests proving that migration 009's
// NULLIF guards on student RLS policies work correctly.
//
// BACKGROUND
// ----------
// Before migration 009 the student-visibility policies on student_badges (006),
// submissions (007 ×2: SELECT USING + INSERT WITH CHECK), and grades (008) all
// cast the app.current_user_id GUC to ::uuid without a NULLIF guard:
//
//   student_id = (current_setting('app.current_user_id', true))::uuid
//
// The production AfterRelease hook in pool.go resets that GUC to the empty
// string '' on every returned connection (see pool.go:22-29). When any of those
// policies evaluated while the GUC held '' PostgreSQL raised:
//
//   ERROR:  invalid input syntax for type uuid: ""  (SQLSTATE 22P02)
//
// Migration 009 replaces the cast with:
//
//   NULLIF(current_setting('app.current_user_id', true), '')::uuid
//
// so an empty string resolves to NULL, the equality evaluates to NULL (false),
// the policy denies access — and no error is raised.
//
// WHAT THESE TESTS PROVE
// ----------------------
// Each test manually reproduces the hazardous state: a connection under
// SET ROLE valory_app whose app.current_user_id GUC is the empty string ''.
// This is intentionally bypassing AcquireAsUser's empty-userID guard — that
// guard exists precisely because the pre-009 policies crashed here; these tests
// exist to prove the post-009 schema no longer crashes.
//
// Run with:
//
//   make test-integration
//   go test -tags integration -count=1 ./internal/db/
//
// @{"verifies": ["REQ-SECURITY-002"]}
package db

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Helper: rlsEmptyGUCConn
// ---------------------------------------------------------------------------

// rlsEmptyGUCConn acquires a connection from the IntegrationPool, downgrades
// it to valory_app, and then sets app.current_user_id to the empty string ''
// and app.current_role to 'student' — deliberately reproducing the hazardous
// pre-009 state.
//
// WHY this bypasses AcquireAsUser's empty-userID guard:
// AcquireAsUser rejects empty userID with t.Fatalf precisely because the
// pre-009 cast of '' to ::uuid crashed. These tests exist to prove the
// post-009 NULLIF-guarded schema no longer crashes in that situation. We
// therefore build the empty-GUC connection manually; the guard in AcquireAsUser
// is the right default for all other test authors.
//
// The caller is responsible for releasing the connection. Because the test pool's
// AfterRelease hook issues RESET ROLE followed by set_config('',false) for both
// GUCs (see newTestPool in integrationtest.go:82-97), no manual cleanup is
// needed beyond calling Release().
func rlsEmptyGUCConn(t *testing.T) *pgxpool.Conn {
	t.Helper()

	pool := IntegrationPool(t)
	ctx := context.Background()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("rlsEmptyGUCConn: acquire: %v", err)
	}

	// Downgrade from the test superuser role to valory_app so that RLS
	// policies are actually evaluated. Superusers bypass FORCE ROW LEVEL
	// SECURITY entirely regardless of GUC values; the downgrade is mandatory
	// for the test to be meaningful.
	if err := conn.Conn().PgConn().Exec(ctx, "SET ROLE valory_app").Close(); err != nil {
		conn.Release()
		t.Fatalf("rlsEmptyGUCConn: SET ROLE valory_app: %v", err)
	}

	// Set app.current_user_id to '' (empty string) — this is the exact
	// hazardous value that the production AfterRelease hook writes when it
	// resets a connection, and that the pre-009 ::uuid cast could not handle.
	// app.current_role = 'student' activates the student branch of every
	// policy on student_badges, submissions, and grades.
	_, err = conn.Exec(ctx,
		"SELECT set_config('app.current_user_id', '', false),"+
			" set_config('app.current_role', 'student', false)",
	)
	if err != nil {
		conn.Release()
		t.Fatalf("rlsEmptyGUCConn: set empty GUCs: %v", err)
	}

	return conn
}

// ---------------------------------------------------------------------------
// Fixture helpers
// ---------------------------------------------------------------------------

// rlsSeedUser inserts a minimal student row via the bare (superuser) pool.
// The users table has no RLS so no role downgrade is needed.
func rlsSeedUser(ctx context.Context, t *testing.T, tag string) uuid.UUID {
	t.Helper()
	pool := IntegrationPool(t)
	username := fmt.Sprintf("rls_empty_%s_%s", tag, uuid.New().String()[:8])
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role)
		 VALUES ($1, 'x', 'student') RETURNING id`,
		username,
	).Scan(&id)
	if err != nil {
		t.Fatalf("rlsSeedUser(%s): %v", tag, err)
	}
	return id
}

// rlsSeedCourse inserts a course row via the bare (superuser) pool, bypassing
// the FORCE RLS on courses. Used to build the FK chain required by submissions
// and grades rows.
func rlsSeedCourse(ctx context.Context, t *testing.T, studentID uuid.UUID) uuid.UUID {
	t.Helper()
	pool := IntegrationPool(t)
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO courses (student_id, topic, status)
		 VALUES ($1, 'RLS empty-GUC test topic', 'active') RETURNING id`,
		studentID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("rlsSeedCourse: %v", err)
	}
	return id
}

// rlsSeedHomework inserts a homework row via the bare pool (homework has no RLS).
func rlsSeedHomework(ctx context.Context, t *testing.T, courseID uuid.UUID) uuid.UUID {
	t.Helper()
	pool := IntegrationPool(t)
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO homework (course_id, section_index, title, rubric, grade_weight)
		 VALUES ($1, 1, 'RLS empty-GUC HW', 'rubric text', 0.5) RETURNING id`,
		courseID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("rlsSeedHomework: %v", err)
	}
	return id
}

// rlsSeedSubmission inserts a submission row via the bare (superuser) pool,
// bypassing the FORCE RLS on submissions. This is the fixture row that the
// empty-GUC SELECT tests try (and should succeed, returning zero rows) to read.
func rlsSeedSubmission(ctx context.Context, t *testing.T, hwID, studentID uuid.UUID) uuid.UUID {
	t.Helper()
	pool := IntegrationPool(t)
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO submissions (homework_id, student_id, format, file_path, file_size_bytes)
		 VALUES ($1, $2, 'markdown', '/tmp/rls_empty_test.md', 512) RETURNING id`,
		hwID, studentID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("rlsSeedSubmission: %v", err)
	}
	return id
}

// rlsSeedBadge retrieves the ID of any existing badge row (seeded by migration
// 006) to use as the badge_id FK in student_badges. If somehow no badge exists
// it creates a minimal one.
func rlsSeedBadge(ctx context.Context, t *testing.T) uuid.UUID {
	t.Helper()
	pool := IntegrationPool(t)
	var id uuid.UUID
	err := pool.QueryRow(ctx, `SELECT id FROM badges LIMIT 1`).Scan(&id)
	if err != nil {
		// Fallback: insert a badge via superuser — this should never happen
		// because migration 006 seeds four standard badges.
		err2 := pool.QueryRow(ctx,
			`INSERT INTO badges (name, description, milestone, reward, reward_value)
			 VALUES ('RLS Test Badge', 'Fallback test badge', 'first_submission', 'grade_improvement', 1.0)
			 RETURNING id`,
		).Scan(&id)
		if err2 != nil {
			t.Fatalf("rlsSeedBadge: could not find or create badge: first error=%v, second=%v", err, err2)
		}
	}
	return id
}

// rlsSeedStudentBadge inserts a student_badges row via the bare (superuser)
// pool. The superuser bypasses FORCE RLS on student_badges so no GUC priming
// is needed here. This is the fixture row that the empty-GUC SELECT test reads.
func rlsSeedStudentBadge(ctx context.Context, t *testing.T, studentID, badgeID uuid.UUID) uuid.UUID {
	t.Helper()
	pool := IntegrationPool(t)
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO student_badges (student_id, badge_id)
		 VALUES ($1, $2) RETURNING id`,
		studentID, badgeID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("rlsSeedStudentBadge: %v", err)
	}
	return id
}

// rlsSeedGrade inserts a grade row via the bare (superuser) pool, bypassing
// FORCE RLS on grades. This is the fixture row used in the empty-GUC SELECT test.
func rlsSeedGrade(ctx context.Context, t *testing.T, subID, hwID, studentID, courseID uuid.UUID) uuid.UUID {
	t.Helper()
	pool := IntegrationPool(t)
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO grades
		   (submission_id, homework_id, student_id, course_id,
		    raw_score, late_penalty_rate, late_penalty_amount,
		    badge_improvement, final_score)
		 VALUES ($1, $2, $3, $4, 75, 0.05, 0, 0, 75) RETURNING id`,
		subID, hwID, studentID, courseID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("rlsSeedGrade: %v", err)
	}
	return id
}

// pgErrCode extracts the 5-character SQLSTATE code from err, which must be a
// *pgconn.PgError. If err is not a PgError it returns "". The caller typically
// uses this to distinguish error codes rather than error messages.
func pgErrCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// ---------------------------------------------------------------------------
// RLS regression tests: empty-GUC connection must not crash
// ---------------------------------------------------------------------------

// TestRLS_EmptyGUC_StudentBadgesSelect_ReturnsZeroRowsNoError proves that a
// SELECT against student_badges under an empty app.current_user_id GUC returns
// zero rows and no error after migration 009.
//
// Pre-009 behaviour: the cast (current_setting(...)::uuid) raised
// "invalid input syntax for type uuid: """ (22P02) and aborted the query.
// Post-009 behaviour: NULLIF(current_setting(...), '')::uuid evaluates the
// empty string to NULL, NULL = student_id is NULL (false), the policy denies
// the row — zero rows returned, no error.
//
// @{"verifies": ["REQ-SECURITY-002"]}
func TestRLS_EmptyGUC_StudentBadgesSelect_ReturnsZeroRowsNoError(t *testing.T) {
	pool := IntegrationPool(t)
	ctx := context.Background()

	TruncateTables(t, pool,
		"student_badges", "badges",
		"grades", "submissions",
		"due_date_schedules", "homework",
		"agent_token_usage", "syllabi", "courses", "users",
	)

	// Seed one student_badges fixture row via superuser so there is something
	// in the table for the policy to filter.
	studentID := rlsSeedUser(ctx, t, "badges_empty")
	badgeID := rlsSeedBadge(ctx, t)
	rlsSeedStudentBadge(ctx, t, studentID, badgeID)

	// Acquire a connection with empty app.current_user_id — the hazardous state.
	conn := rlsEmptyGUCConn(t)
	defer conn.Release()

	// Execute the SELECT. Pre-009 this would have raised 22P02 ("invalid input
	// syntax for type uuid: """). Post-009 it must return zero rows and no error.
	rows, err := conn.Query(ctx, `SELECT id FROM student_badges`)
	if err != nil {
		// Surface the SQLSTATE code in the message so a regression is easy to diagnose.
		t.Fatalf("SELECT student_badges under empty GUC: unexpected error (code=%s): %v",
			pgErrCode(err), err)
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err() after iterating student_badges: (code=%s): %v",
			pgErrCode(err), err)
	}

	// The policy must have filtered out the seeded row — zero rows, not an error.
	if count != 0 {
		t.Errorf("SELECT student_badges under empty GUC: want 0 rows (policy deny), got %d", count)
	}
}

// TestRLS_EmptyGUC_SubmissionsSelect_ReturnsZeroRowsNoError proves that a
// SELECT against submissions under an empty app.current_user_id GUC returns
// zero rows and no error after migration 009.
//
// @{"verifies": ["REQ-SECURITY-002"]}
func TestRLS_EmptyGUC_SubmissionsSelect_ReturnsZeroRowsNoError(t *testing.T) {
	pool := IntegrationPool(t)
	ctx := context.Background()

	TruncateTables(t, pool,
		"grades", "submissions", "student_badges", "badges",
		"due_date_schedules", "homework",
		"agent_token_usage", "syllabi", "courses", "users",
	)

	// Seed one fixture submission row so the table is not empty.
	studentID := rlsSeedUser(ctx, t, "sub_empty")
	courseID := rlsSeedCourse(ctx, t, studentID)
	hwID := rlsSeedHomework(ctx, t, courseID)
	rlsSeedSubmission(ctx, t, hwID, studentID)

	// Acquire a connection with empty app.current_user_id.
	conn := rlsEmptyGUCConn(t)
	defer conn.Release()

	// Pre-009: raised 22P02. Post-009: zero rows, no error.
	rows, err := conn.Query(ctx, `SELECT id FROM submissions`)
	if err != nil {
		t.Fatalf("SELECT submissions under empty GUC: unexpected error (code=%s): %v",
			pgErrCode(err), err)
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err() after iterating submissions: (code=%s): %v",
			pgErrCode(err), err)
	}

	if count != 0 {
		t.Errorf("SELECT submissions under empty GUC: want 0 rows (policy deny), got %d", count)
	}
}

// TestRLS_EmptyGUC_GradesSelect_ReturnsZeroRowsNoError proves that a SELECT
// against grades under an empty app.current_user_id GUC returns zero rows and
// no error after migration 009.
//
// @{"verifies": ["REQ-SECURITY-002"]}
func TestRLS_EmptyGUC_GradesSelect_ReturnsZeroRowsNoError(t *testing.T) {
	pool := IntegrationPool(t)
	ctx := context.Background()

	TruncateTables(t, pool,
		"grades", "submissions", "student_badges", "badges",
		"due_date_schedules", "homework",
		"agent_token_usage", "syllabi", "courses", "users",
	)

	// Seed the full FK chain: user → course → homework → submission → grade.
	studentID := rlsSeedUser(ctx, t, "grade_empty")
	courseID := rlsSeedCourse(ctx, t, studentID)
	hwID := rlsSeedHomework(ctx, t, courseID)
	subID := rlsSeedSubmission(ctx, t, hwID, studentID)
	rlsSeedGrade(ctx, t, subID, hwID, studentID, courseID)

	// Acquire a connection with empty app.current_user_id.
	conn := rlsEmptyGUCConn(t)
	defer conn.Release()

	// Pre-009: raised 22P02. Post-009: zero rows, no error.
	rows, err := conn.Query(ctx, `SELECT id FROM grades`)
	if err != nil {
		t.Fatalf("SELECT grades under empty GUC: unexpected error (code=%s): %v",
			pgErrCode(err), err)
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err() after iterating grades: (code=%s): %v",
			pgErrCode(err), err)
	}

	if count != 0 {
		t.Errorf("SELECT grades under empty GUC: want 0 rows (policy deny), got %d", count)
	}
}

// TestRLS_EmptyGUC_SubmissionsInsert_FailsPolicyViolationNotCastError proves
// that an INSERT into submissions under an empty app.current_user_id GUC is
// rejected with a row-level security policy violation (SQLSTATE 42501) and NOT
// with the uuid cast error (SQLSTATE 22P02 / "invalid input syntax for type uuid").
//
// This is the key regression distinction:
//   - Pre-009: the WITH CHECK expression tried to cast '' to ::uuid → 22P02 crash
//   - Post-009: NULLIF('',...) → NULL, NULL = student_id is NULL (false),
//     PostgreSQL surfaces this as a policy violation → 42501, no crash
//
// The test explicitly asserts SQLSTATE 42501 AND explicitly asserts that the
// error is NOT 22P02, so a future regressor that silently re-introduces the
// old cast will be caught here.
//
// @{"verifies": ["REQ-SECURITY-002"]}
func TestRLS_EmptyGUC_SubmissionsInsert_FailsPolicyViolationNotCastError(t *testing.T) {
	pool := IntegrationPool(t)
	ctx := context.Background()

	TruncateTables(t, pool,
		"grades", "submissions", "student_badges", "badges",
		"due_date_schedules", "homework",
		"agent_token_usage", "syllabi", "courses", "users",
	)

	// Seed the FK prerequisite chain: we need a valid homework_id and a valid
	// student_id for the INSERT to reach the RLS policy check (if the FK failed
	// first we would get 23503, not 42501/22P02, which would obscure the test
	// intent). Both are seeded via superuser to bypass FORCE RLS.
	studentID := rlsSeedUser(ctx, t, "ins_empty")
	courseID := rlsSeedCourse(ctx, t, studentID)
	hwID := rlsSeedHomework(ctx, t, courseID)

	// Acquire the hazardous empty-GUC connection.
	conn := rlsEmptyGUCConn(t)
	defer conn.Release()

	// Attempt to INSERT a well-formed row. The student-insert policy checks:
	//   WITH CHECK (student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid)
	// With an empty GUC, NULLIF returns NULL, the equality is NULL (false),
	// PostgreSQL raises "new row violates row-level security policy" (42501).
	// Pre-009 the cast of '' to ::uuid would have crashed with 22P02 first.
	_, err := conn.Exec(ctx,
		`INSERT INTO submissions (homework_id, student_id, format, file_path, file_size_bytes)
		 VALUES ($1, $2, 'markdown', '/tmp/rls_insert_test.md', 256)`,
		hwID, studentID,
	)

	// The insert MUST fail — accepting it would mean RLS is not applied at all.
	if err == nil {
		t.Fatal("INSERT submissions under empty GUC unexpectedly succeeded; " +
			"submissions_student_insert_policy must have been bypassed — " +
			"check that SET ROLE valory_app was issued before the INSERT")
	}

	code := pgErrCode(err)

	// Primary assertion: the error is a policy violation, not a cast error.
	// 42501 = insufficient_privilege / new row violates row-level security policy
	// 22P02 = invalid_text_representation (the pre-009 crash we are guarding against)
	if code == "22P02" {
		t.Errorf("INSERT submissions under empty GUC produced SQLSTATE 22P02 "+
			"(invalid input syntax for type uuid) — this is the pre-009 crash; "+
			"migration 009 NULLIF guard is NOT in effect: %v", err)
		return
	}
	if code != "42501" {
		t.Errorf("INSERT submissions under empty GUC: want SQLSTATE 42501 "+
			"(row-level security policy violation), got %q: %v", code, err)
	}
}

// TestRLS_EmptyGUC_Control_AuthenticatedStudentSeesOwnRows proves that the
// migration 009 NULLIF guards still admit legitimate access when a real UUID
// is present in app.current_user_id.
//
// This is the positive-path counterpart to the empty-GUC tests: after 009
// the NULLIF guards must not break the normal authenticated case. We verify
// this for all three tables — student_badges, submissions, grades — by checking
// that a connection primed with the fixture owner's actual UUID sees the seeded
// row.
//
// @{"verifies": ["REQ-SECURITY-002"]}
func TestRLS_EmptyGUC_Control_AuthenticatedStudentSeesOwnRows(t *testing.T) {
	pool := IntegrationPool(t)
	ctx := context.Background()

	TruncateTables(t, pool,
		"grades", "submissions", "student_badges", "badges",
		"due_date_schedules", "homework",
		"agent_token_usage", "syllabi", "courses", "users",
	)

	// Seed the full FK chain.
	studentID := rlsSeedUser(ctx, t, "ctrl")
	courseID := rlsSeedCourse(ctx, t, studentID)
	hwID := rlsSeedHomework(ctx, t, courseID)
	subID := rlsSeedSubmission(ctx, t, hwID, studentID)
	rlsSeedGrade(ctx, t, subID, hwID, studentID, courseID)
	badgeID := rlsSeedBadge(ctx, t)
	rlsSeedStudentBadge(ctx, t, studentID, badgeID)

	// Acquire a properly scoped student connection using the real UUID.
	// AcquireAsUser sets app.current_user_id to the student's UUID and
	// app.current_role to 'student', then issues SET ROLE valory_app so that
	// the NULLIF-guarded policies are evaluated (not bypassed by superuser).
	// The UUID is passed as a 32-char no-dash hex string — the format the
	// auth middleware uses (middleware.go formats Session.UserID, a plain
	// [16]byte, with %x). The [:] is load-bearing: uuid.UUID implements
	// Stringer, and fmt applies %x to the String() result for Stringer
	// operands — hex-encoding the dashed text form into a 72-char blob that
	// the policies' ::uuid cast rejects (22P02). Slicing to []byte hex-encodes
	// the raw bytes, matching the middleware exactly.
	userIDHex := fmt.Sprintf("%x", studentID[:])
	conn := AcquireAsUser(t, pool, userIDHex, "student")
	defer conn.Release()

	t.Run("student_badges", func(t *testing.T) {
		rows, err := conn.Query(ctx, `SELECT id FROM student_badges WHERE student_id = $1`, studentID)
		if err != nil {
			t.Fatalf("SELECT student_badges as owning student: %v", err)
		}
		defer rows.Close()
		var count int
		for rows.Next() {
			count++
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("rows.Err(): %v", err)
		}
		// The NULLIF-guarded policy must admit the owning student's own rows.
		if count != 1 {
			t.Errorf("SELECT student_badges as owner: want 1 row, got %d "+
				"(NULLIF guard may have broken legitimate access)", count)
		}
	})

	t.Run("submissions", func(t *testing.T) {
		rows, err := conn.Query(ctx, `SELECT id FROM submissions WHERE student_id = $1`, studentID)
		if err != nil {
			t.Fatalf("SELECT submissions as owning student: %v", err)
		}
		defer rows.Close()
		var count int
		for rows.Next() {
			count++
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("rows.Err(): %v", err)
		}
		if count != 1 {
			t.Errorf("SELECT submissions as owner: want 1 row, got %d "+
				"(NULLIF guard may have broken legitimate access)", count)
		}
	})

	t.Run("grades", func(t *testing.T) {
		rows, err := conn.Query(ctx, `SELECT id FROM grades WHERE student_id = $1`, studentID)
		if err != nil {
			t.Fatalf("SELECT grades as owning student: %v", err)
		}
		defer rows.Close()
		var count int
		for rows.Next() {
			count++
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("rows.Err(): %v", err)
		}
		if count != 1 {
			t.Errorf("SELECT grades as owner: want 1 row, got %d "+
				"(NULLIF guard may have broken legitimate access)", count)
		}
	})
}
