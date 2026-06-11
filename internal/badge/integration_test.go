//go:build integration

// integration_test.go — integration tests for the badge module against the
// real migrated PostgreSQL schema.
//
// These tests verify behaviour that requires the full migration history,
// specifically migration 009 which added:
//   - GRANTs on badges (SELECT) and student_badges (SELECT, INSERT, UPDATE)
//     for the valory_app role (migration 009 lines 107–108).
//   - A NULLIF guard on student_badges_student_policy to prevent a uuid cast
//     error when app.current_user_id is the empty string (migration 009
//     lines 35–50).
//
// Run with:
//
//	make test-integration
//
// or:
//
//	go test -tags integration ./internal/badge/
//
// IMPORTANT: repository_test.go in this package carries NO build tag, so it
// compiles into every test binary, including the integration binary. Its
// package-level symbols (TestMain, pool, createTestStudent, execSimple,
// applyMigrations, truncateTables) are therefore already declared. This file
// must NOT redefine any of those symbols. All helpers here are prefixed with
// "integ" and the shared harness from internal/db (IntegrationPool,
// AcquireAsUser, AcquireAsServer, TruncateTables) is used in place of the
// module-local TestMain pool.

package badge

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	// pgx/v5 returns *pgx/v5/pgconn.PgError; the identically named v1 package
	// (github.com/jackc/pgconn) silently fails errors.As against v5 errors.
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valory/valory/internal/auth"
	db "github.com/valory/valory/internal/db"
)

// ----------------------------------------------------------------------------
// Shared integration helpers
// ----------------------------------------------------------------------------

// integSeedStudent inserts a test student row via the superuser pool (bypasses
// RLS) and returns the new user UUID. It is intentionally distinct from
// createTestStudent in repository_test.go, which relies on the module-level
// `pool` variable initialised by that file's TestMain.
func integSeedStudent(ctx context.Context, t *testing.T, p *pgxpool.Pool, username string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := p.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, 'testhash', 'student') RETURNING id`,
		username,
	).Scan(&id); err != nil {
		t.Fatalf("integSeedStudent %q: %v", username, err)
	}
	return id
}

// integLookupBadge returns the UUID of the seeded badge for the given milestone.
// It uses the superuser pool so no RLS applies, and the lookup is always against
// the migrations-seeded rows.
func integLookupBadge(ctx context.Context, t *testing.T, p *pgxpool.Pool, milestone string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := p.QueryRow(ctx,
		`SELECT id FROM badges WHERE milestone = $1 LIMIT 1`, milestone,
	).Scan(&id); err != nil {
		t.Fatalf("integLookupBadge %q: %v", milestone, err)
	}
	return id
}

// integSuffix returns a short unique string suitable for use in usernames.
func integSuffix() string {
	return uuid.New().String()[:8]
}

// ----------------------------------------------------------------------------
// Test 1 — GRANT regression (migration 009 finding-3 fix)
//
// Pre-009, migration 006 created badges and student_badges but never issued
// any GRANTs to the valory_app role. Any valory_app connection attempting to
// SELECT from badges received "permission denied for table badges"
// (SQLSTATE 42501). Migration 009 line 107 adds:
//
//	GRANT SELECT ON badges TO valory_app;
//
// This test pins that fix: a policy-checked connection (SET ROLE valory_app)
// must be able to SELECT from badges without a permission error.
//
// @{"verifies": ["REQ-BADGE-001", "REQ-BADGE-002", "REQ-BADGE-003"]}
func TestInteg_Badges_SelectGranted_ValoryAppCanReadCatalog(t *testing.T) {
	integPool := db.IntegrationPool(t)
	ctx := context.Background()

	// Truncate per-test mutable tables; badges itself is not truncated because
	// its seed rows come from migration 006 and are needed for all tests.
	db.TruncateTables(t, integPool, "student_badges", "users")

	// AcquireAsUser issues SET ROLE valory_app, downgrading from the superuser
	// login role. badges has no RLS (no ALTER TABLE badges ENABLE ROW LEVEL
	// SECURITY in 006_badge.sql), so the test purely exercises the GRANT.
	studentID := uuid.New()
	conn := db.AcquireAsUser(t, integPool, studentID.String(), "student")
	defer conn.Release()

	// A plain SELECT from badges must succeed. Pre-009 this failed with:
	//   ERROR: permission denied for table badges (SQLSTATE 42501)
	rows, err := conn.Query(ctx, `SELECT id, name FROM badges LIMIT 1`)
	if err != nil {
		t.Fatalf(
			"GRANT regression: SELECT from badges as valory_app failed: %v\n"+
				"(pre-009 this was permission denied; migration 009 line 107 must be applied)",
			err,
		)
	}
	rows.Close()
}

// ----------------------------------------------------------------------------
// Test 2 — Server-role INSERT path (AwardBadge via server policy)
//
// student_badges_server_policy (migration 006 lines 63–68) permits FOR ALL
// when app.current_role = 'server'. Migration 009 line 108 adds:
//
//	GRANT SELECT, INSERT, UPDATE ON student_badges TO valory_app;
//
// Without this GRANT the INSERT would fail with permission denied even when
// the server policy's USING clause evaluates to true, because PostgreSQL
// checks object privileges before evaluating RLS policies.
//
// @{"verifies": ["REQ-BADGE-001"]}
func TestInteg_StudentBadges_ServerRoleAward_InsertsRow(t *testing.T) {
	integPool := db.IntegrationPool(t)
	ctx := context.Background()

	db.TruncateTables(t, integPool, "student_badges", "users")

	studentID := integSeedStudent(ctx, t, integPool,
		fmt.Sprintf("integ_server_award_%s", integSuffix()))
	badgeID := integLookupBadge(ctx, t, integPool, "first_submission")

	// AcquireAsServer issues SET ROLE valory_app and sets:
	//   app.current_role = 'server'
	//   app.current_user_id = '00000000-0000-0000-0000-000000000000'
	// matching the production agent-worker connection pattern.
	serverConn := db.AcquireAsServer(t, integPool)
	defer serverConn.Release()

	// Route the repository through the policy-checked connection by embedding
	// it via auth.ContextWithConn — the same mechanism the agent runner uses.
	serverCtx := auth.ContextWithConn(ctx, serverConn)
	repo := NewRepository(integPool)

	sb, err := repo.AwardBadge(serverCtx, studentID, badgeID)
	if err != nil {
		t.Fatalf(
			"AwardBadge under server policy: %v\n"+
				"(migration 009 line 108 must grant INSERT on student_badges to valory_app)",
			err,
		)
	}

	if sb.StudentID != studentID {
		t.Errorf("StudentID: want %v, got %v", studentID, sb.StudentID)
	}
	if sb.BadgeID != badgeID {
		t.Errorf("BadgeID: want %v, got %v", badgeID, sb.BadgeID)
	}
	if sb.RedeemedForSubmissionID != nil {
		t.Error("RedeemedForSubmissionID: expected nil on fresh award")
	}
	if sb.BadgeName == "" {
		t.Error("BadgeName: expected non-empty after award")
	}
}

// ----------------------------------------------------------------------------
// Test 3 — RLS isolation: student A cannot see student B's badges
//
// student_badges_student_policy (migration 006 lines 57–61, updated by
// migration 009 lines 35–50 to add the NULLIF guard) uses:
//
//	USING (student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid)
//
// Because the policy has no explicit FOR clause it defaults to FOR ALL, so it
// applies to SELECT, INSERT, UPDATE, and DELETE. Under a student-role
// connection with app.current_user_id = A, the USING clause evaluates to
// student_id = A, meaning only rows where student_id matches A are visible.
// Queries for B's rows return 0 rows (not an error).
//
// Positive control: student B can see their own row.
//
// @{"verifies": ["REQ-BADGE-001", "REQ-SECURITY-002"]}
func TestInteg_StudentBadges_RLSIsolation_StudentCannotSeeOtherRows(t *testing.T) {
	integPool := db.IntegrationPool(t)
	ctx := context.Background()

	db.TruncateTables(t, integPool, "student_badges", "users")

	studentA := integSeedStudent(ctx, t, integPool,
		fmt.Sprintf("integ_rls_a_%s", integSuffix()))
	studentB := integSeedStudent(ctx, t, integPool,
		fmt.Sprintf("integ_rls_b_%s", integSuffix()))

	badgeID := integLookupBadge(ctx, t, integPool, "perfect_score")

	// Seed badge for student B only via superuser pool (bypasses RLS).
	if _, err := integPool.Exec(ctx,
		`INSERT INTO student_badges (student_id, badge_id) VALUES ($1, $2)`,
		studentB, badgeID,
	); err != nil {
		t.Fatalf("seed student_badges for B: %v", err)
	}

	repo := NewRepository(integPool)

	// --- Negative control: student A queries B's studentID ---
	// The policy USING clause (student_id = A's UUID) filters out B's row.
	connA := db.AcquireAsUser(t, integPool, studentA.String(), "student")
	defer connA.Release()
	ctxA := auth.ContextWithConn(ctx, connA)

	rowsA, err := repo.GetStudentBadges(ctxA, studentB)
	if err != nil {
		t.Fatalf("GetStudentBadges as A querying B: unexpected error: %v", err)
	}
	if len(rowsA) != 0 {
		t.Errorf(
			"RLS isolation failure: student A can see %d of student B's badges (expected 0)",
			len(rowsA),
		)
	}

	// --- Positive control: student B sees their own row ---
	connB := db.AcquireAsUser(t, integPool, studentB.String(), "student")
	defer connB.Release()
	ctxB := auth.ContextWithConn(ctx, connB)

	rowsB, err := repo.GetStudentBadges(ctxB, studentB)
	if err != nil {
		t.Fatalf("GetStudentBadges as B: %v", err)
	}
	if len(rowsB) != 1 {
		t.Errorf("positive control: student B expected 1 badge row, got %d", len(rowsB))
	}
}

// ----------------------------------------------------------------------------
// Test 4 — Student UPDATE path: owning student can redeem, intruder cannot
//
// student_badges_student_policy is FOR ALL (no FOR clause in migration 006
// line 57), so it governs UPDATE as well as SELECT. The NULLIF guard
// (migration 009 lines 35–50) ensures the uuid cast does not raise on empty
// strings. Under RedeemBadge:
//
//   UPDATE student_badges
//   SET redeemed_for_submission_id = $3
//   WHERE student_id = $1 AND badge_id = $2 AND redeemed_for_submission_id IS NULL
//
// If the session's app.current_user_id does not match student_id, the RLS
// USING clause filters the row out before the WHERE clause even applies, so
// RowsAffected = 0 and RedeemBadge returns ErrNotFound.
//
// @{"verifies": ["REQ-BADGE-002", "REQ-BADGE-003", "REQ-SECURITY-002"]}
func TestInteg_StudentBadges_RedeemBadge_OwnerCanUpdateIntruderCannot(t *testing.T) {
	integPool := db.IntegrationPool(t)
	ctx := context.Background()

	db.TruncateTables(t, integPool, "student_badges", "users")

	owner := integSeedStudent(ctx, t, integPool,
		fmt.Sprintf("integ_redeem_owner_%s", integSuffix()))
	intruder := integSeedStudent(ctx, t, integPool,
		fmt.Sprintf("integ_redeem_intruder_%s", integSuffix()))

	badgeID := integLookupBadge(ctx, t, integPool, "submitted_on_time")

	// Award badge to owner via superuser pool (fixture, bypasses RLS).
	if _, err := integPool.Exec(ctx,
		`INSERT INTO student_badges (student_id, badge_id) VALUES ($1, $2)`,
		owner, badgeID,
	); err != nil {
		t.Fatalf("seed student_badges for owner: %v", err)
	}

	// fk_student_badges_submission (migration 007) requires the redemption
	// target to be a real submissions row, so seed the owner's full
	// course → homework → submission chain via the superuser pool (fixture
	// seeding deliberately bypasses FORCE RLS).
	var courseID uuid.UUID
	if err := integPool.QueryRow(ctx,
		`INSERT INTO courses (student_id, topic, status)
		 VALUES ($1, 'Badge Redeem Topic', 'active') RETURNING id`,
		owner,
	).Scan(&courseID); err != nil {
		t.Fatalf("seed course: %v", err)
	}
	var homeworkID uuid.UUID
	if err := integPool.QueryRow(ctx,
		`INSERT INTO homework (course_id, section_index, title, rubric, grade_weight)
		 VALUES ($1, 0, 'Badge Redeem HW', 'rubric', 0.5) RETURNING id`,
		courseID,
	).Scan(&homeworkID); err != nil {
		t.Fatalf("seed homework: %v", err)
	}
	var submissionID uuid.UUID
	if err := integPool.QueryRow(ctx,
		`INSERT INTO submissions (homework_id, student_id, format, file_path, file_size_bytes)
		 VALUES ($1, $2, 'markdown', '/tmp/badge_redeem_test.md', 128) RETURNING id`,
		homeworkID, owner,
	).Scan(&submissionID); err != nil {
		t.Fatalf("seed submission: %v", err)
	}

	repo := NewRepository(integPool)

	// --- Negative: intruder cannot UPDATE owner's row ---
	// Under intruder's GUC the USING clause evaluates student_id = intruder,
	// which is false for owner's row. UPDATE affects 0 rows → ErrNotFound.
	connIntruder := db.AcquireAsUser(t, integPool, intruder.String(), "student")
	defer connIntruder.Release()
	ctxIntruder := auth.ContextWithConn(ctx, connIntruder)

	if err := repo.RedeemBadge(ctxIntruder, owner, badgeID, submissionID); !errors.Is(err, ErrNotFound) {
		t.Errorf("intruder redeem: expected ErrNotFound, got %v", err)
	}

	// --- Positive: owner can UPDATE their own row ---
	connOwner := db.AcquireAsUser(t, integPool, owner.String(), "student")
	defer connOwner.Release()
	ctxOwner := auth.ContextWithConn(ctx, connOwner)

	if err := repo.RedeemBadge(ctxOwner, owner, badgeID, submissionID); err != nil {
		t.Fatalf("owner redeem: expected success, got %v", err)
	}

	// Verify via superuser pool that redeemed_for_submission_id was persisted.
	var gotSub uuid.UUID
	if err := integPool.QueryRow(ctx,
		`SELECT redeemed_for_submission_id FROM student_badges WHERE student_id = $1 AND badge_id = $2`,
		owner, badgeID,
	).Scan(&gotSub); err != nil {
		t.Fatalf("verify redemption: %v", err)
	}
	if gotSub != submissionID {
		t.Errorf("redeemed_for_submission_id: want %v, got %v", submissionID, gotSub)
	}
}

// ----------------------------------------------------------------------------
// Test 5 — Foreign-key violation: awarding a badge for a nonexistent badge ID
//
// student_badges.badge_id REFERENCES badges(id) ON DELETE CASCADE
// (migration 006 line 40). Inserting a row with a badge_id that has no
// corresponding row in badges must be rejected with SQLSTATE 23503
// (foreign_key_violation).
//
// @{"verifies": ["REQ-BADGE-001"]}
func TestInteg_StudentBadges_AwardBadge_NonexistentBadgeID_ReturnsFKError(t *testing.T) {
	integPool := db.IntegrationPool(t)
	ctx := context.Background()

	db.TruncateTables(t, integPool, "student_badges", "users")

	student := integSeedStudent(ctx, t, integPool,
		fmt.Sprintf("integ_fk_%s", integSuffix()))

	// Use a random UUID that has no row in badges.
	phantomBadgeID := uuid.New()

	// Server-role connection: the server policy permits the INSERT, so if it
	// goes through it proves the FK is enforced at the database level.
	serverConn := db.AcquireAsServer(t, integPool)
	defer serverConn.Release()
	serverCtx := auth.ContextWithConn(ctx, serverConn)

	repo := NewRepository(integPool)
	_, err := repo.AwardBadge(serverCtx, student, phantomBadgeID)
	if err == nil {
		t.Fatal("expected FK violation error, got nil")
	}

	// Verify the error is specifically SQLSTATE 23503.
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Errorf("expected SQLSTATE 23503 (foreign_key_violation), got: %v", err)
	}
}

// ----------------------------------------------------------------------------
// Test 6 — NULLIF guard: server-role connection's sentinel UUID does not cause
// a uuid cast error in the student RLS policy
//
// Migration 009 lines 35–50 replace the original cast in
// student_badges_student_policy:
//
//	-- before: (current_setting('app.current_user_id', true))::uuid
//	-- after:  NULLIF(current_setting('app.current_user_id', true), '')::uuid
//
// AcquireAsServer sets app.current_user_id to the all-zeros sentinel UUID
// '00000000-0000-0000-0000-000000000000' (not the empty string), which is a
// valid uuid. Even so, this test confirms that the server-role path does not
// crash with a uuid cast error and that the server policy (FOR ALL USING
// app.current_role = 'server') allows the SELECT to return the seeded row.
//
// @{"verifies": ["REQ-BADGE-001", "REQ-SECURITY-002"]}
func TestInteg_StudentBadges_NULLIFGuard_ServerRoleSelectSucceeds(t *testing.T) {
	integPool := db.IntegrationPool(t)
	ctx := context.Background()

	db.TruncateTables(t, integPool, "student_badges", "users")

	student := integSeedStudent(ctx, t, integPool,
		fmt.Sprintf("integ_nullif_%s", integSuffix()))
	badgeID := integLookupBadge(ctx, t, integPool, "all_sections_complete")

	// Seed via superuser (bypasses RLS — fixture only).
	if _, err := integPool.Exec(ctx,
		`INSERT INTO student_badges (student_id, badge_id) VALUES ($1, $2)`,
		student, badgeID,
	); err != nil {
		t.Fatalf("seed student_badges: %v", err)
	}

	// Server-role connection: app.current_user_id = all-zeros sentinel.
	// The student policy evaluates:
	//   student_id = NULLIF('00000000-0000-0000-0000-000000000000', '')::uuid
	// which is student_id = '00000000-0000-0000-0000-000000000000' — false for
	// all real students but does not raise. The server policy (app.current_role
	// = 'server') covers this connection, so the row is visible.
	serverConn := db.AcquireAsServer(t, integPool)
	defer serverConn.Release()
	serverCtx := auth.ContextWithConn(ctx, serverConn)

	repo := NewRepository(integPool)
	rows, err := repo.GetStudentBadges(serverCtx, student)
	if err != nil {
		t.Fatalf(
			"GetStudentBadges under server role (NULLIF guard test): %v\n"+
				"(pre-009 this path could raise 'invalid input syntax for type uuid')",
			err,
		)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 row via server policy, got %d", len(rows))
	}
}
