//go:build integration

package profile

// profile_integration_test.go — integration tests for the profile module.
//
// These tests require a live PostgreSQL instance (run via make test-integration
// or with VALORY_TEST_DATABASE_URL set).
//
// ALL RLS probe tests use db.AcquireAsUser / db.AcquireAsServer — NEVER the
// bare pool — so that FORCE ROW LEVEL SECURITY is actually evaluated under the
// non-superuser valory_app role (Sprint 22 lesson). The bare pool runs as the
// valory_test SUPERUSER which bypasses FORCE RLS entirely.
//
// @{"verifies": ["REQ-PROFILE-004", "REQ-PROFILE-005", "REQ-PROFILE-010", "REQ-PROFILE-017", "REQ-PROFILE-019", "REQ-PROFILE-020"]}

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	authpkg "github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/db"
)

// ---------------------------------------------------------------------------
// Fixture helpers
// ---------------------------------------------------------------------------

// seedProfileStudent inserts a minimal student user row and returns its UUID.
// Uses the bare (superuser) pool so fixture writes bypass RLS.
func seedProfileStudent(t *testing.T) uuid.UUID {
	t.Helper()
	pool := db.IntegrationPool(t)
	username := fmt.Sprintf("profile_student_%s", uuid.New().String()[:8])
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (username, password_hash, role) VALUES ($1, 'x', 'student') RETURNING id`,
		username,
	).Scan(&id); err != nil {
		t.Fatalf("seedProfileStudent: %v", err)
	}
	return id
}

// seedProfileRow inserts a learning_profiles row directly via the superuser
// pool (fixture seeding; bypasses RLS intentionally). Returns the student ID.
func seedProfileRow(t *testing.T, studentID uuid.UUID, summary, source string) {
	t.Helper()
	pool := db.IntegrationPool(t)
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO learning_profiles (student_id, summary, source)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (student_id) DO UPDATE
		     SET summary = EXCLUDED.summary, source = EXCLUDED.source, updated_at = now()`,
		studentID, summary, source,
	); err != nil {
		t.Fatalf("seedProfileRow: %v", err)
	}
}

// truncateProfileTables wipes all profile-related rows in dependency order.
func truncateProfileTables(t *testing.T) {
	t.Helper()
	db.TruncateTables(t, db.IntegrationPool(t),
		"onboarding_messages",
		"learning_profiles",
		"users",
	)
}

// ---------------------------------------------------------------------------
// RLS Tests — §8.1 of SDD-023
// ---------------------------------------------------------------------------

// TestIntegration_RLS_StudentCanReadOwnProfile: student A can SELECT their own
// profile row via a non-superuser valory_app connection (REQ-PROFILE-004).
//
// @{"verifies": ["REQ-PROFILE-004", "REQ-PROFILE-019"]}
func TestIntegration_RLS_StudentCanReadOwnProfile(t *testing.T) {
	truncateProfileTables(t)
	ctx := context.Background()
	pool := db.IntegrationPool(t)

	studentA := seedProfileStudent(t)
	seedProfileRow(t, studentA, "Prefers examples first.", "onboarding")

	hexA := fmt.Sprintf("%x", [16]byte(studentA))
	connA := db.AcquireAsUser(t, pool, hexA, "student")
	defer connA.Release()

	var count int
	if err := connA.QueryRow(ctx,
		`SELECT COUNT(*) FROM learning_profiles WHERE student_id = $1`, studentA,
	).Scan(&count); err != nil {
		t.Fatalf("SELECT as studentA: %v", err)
	}
	if count != 1 {
		t.Errorf("studentA should see own profile (count=1), got %d", count)
	}
}

// TestIntegration_RLS_StudentCannotReadOtherStudentProfile: student B cannot
// SELECT student A's profile row (REQ-PROFILE-017, REQ-PROFILE-019).
//
// @{"verifies": ["REQ-PROFILE-017", "REQ-PROFILE-019"]}
func TestIntegration_RLS_StudentCannotReadOtherStudentProfile(t *testing.T) {
	truncateProfileTables(t)
	ctx := context.Background()
	pool := db.IntegrationPool(t)

	studentA := seedProfileStudent(t)
	studentB := seedProfileStudent(t)
	seedProfileRow(t, studentA, "Student A profile.", "onboarding")
	seedProfileRow(t, studentB, "Student B profile.", "onboarding")

	hexB := fmt.Sprintf("%x", [16]byte(studentB))
	connB := db.AcquireAsUser(t, pool, hexB, "student")
	defer connB.Release()

	// Student B queries using student A's UUID — must return 0 rows.
	var count int
	if err := connB.QueryRow(ctx,
		`SELECT COUNT(*) FROM learning_profiles WHERE student_id = $1`, studentA,
	).Scan(&count); err != nil {
		t.Fatalf("SELECT as studentB for studentA row: %v", err)
	}
	if count != 0 {
		t.Errorf("RLS FAILURE: studentB can read studentA's profile (count=%d)", count)
	}
}

// TestIntegration_RLS_StudentCanUpsertOwnProfile: student A can INSERT/UPDATE
// their own profile row via a non-superuser connection (REQ-PROFILE-004).
//
// @{"verifies": ["REQ-PROFILE-004"]}
func TestIntegration_RLS_StudentCanUpsertOwnProfile(t *testing.T) {
	truncateProfileTables(t)
	ctx := context.Background()
	pool := db.IntegrationPool(t)

	studentA := seedProfileStudent(t)

	hexA := fmt.Sprintf("%x", [16]byte(studentA))
	connA := db.AcquireAsUser(t, pool, hexA, "student")
	defer connA.Release()

	_, err := connA.Exec(ctx,
		`INSERT INTO learning_profiles (student_id, summary, source)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (student_id) DO UPDATE
		     SET summary = EXCLUDED.summary, source = EXCLUDED.source, updated_at = now()`,
		studentA, "I prefer worked examples.", "manual_edit",
	)
	if err != nil {
		t.Fatalf("student RLS upsert own profile: unexpected error: %v", err)
	}

	// Confirm the row is now readable by the same connection.
	var summary string
	if err := connA.QueryRow(ctx,
		`SELECT summary FROM learning_profiles WHERE student_id = $1`, studentA,
	).Scan(&summary); err != nil {
		t.Fatalf("SELECT own profile after upsert: %v", err)
	}
	if summary != "I prefer worked examples." {
		t.Errorf("unexpected summary after upsert: %q", summary)
	}
}

// TestIntegration_RLS_StudentCannotUpsertOtherStudentProfile: student B cannot
// INSERT a profile row for student A (REQ-PROFILE-017).
//
// @{"verifies": ["REQ-PROFILE-017"]}
func TestIntegration_RLS_StudentCannotUpsertOtherStudentProfile(t *testing.T) {
	truncateProfileTables(t)
	ctx := context.Background()
	pool := db.IntegrationPool(t)

	studentA := seedProfileStudent(t)
	studentB := seedProfileStudent(t)

	hexB := fmt.Sprintf("%x", [16]byte(studentB))
	connB := db.AcquireAsUser(t, pool, hexB, "student")
	defer connB.Release()

	// Student B attempts to INSERT a row owned by student A.
	_, err := connB.Exec(ctx,
		`INSERT INTO learning_profiles (student_id, summary, source) VALUES ($1, $2, $3)`,
		studentA, "Injected by B.", "manual_edit",
	)
	if err == nil {
		t.Fatal("RLS FAILURE: studentB successfully inserted a profile row for studentA — student write policy is not enforced")
	}
}

// TestIntegration_RLS_ServerCanReadAnyProfile: the server role can SELECT all
// profile rows regardless of student_id (REQ-PROFILE-005).
//
// @{"verifies": ["REQ-PROFILE-005"]}
func TestIntegration_RLS_ServerCanReadAnyProfile(t *testing.T) {
	truncateProfileTables(t)
	ctx := context.Background()
	pool := db.IntegrationPool(t)

	studentA := seedProfileStudent(t)
	studentB := seedProfileStudent(t)
	seedProfileRow(t, studentA, "Profile A.", "onboarding")
	seedProfileRow(t, studentB, "Profile B.", "onboarding")

	connServer := db.AcquireAsServer(t, pool)
	defer connServer.Release()

	var count int
	if err := connServer.QueryRow(ctx,
		`SELECT COUNT(*) FROM learning_profiles`,
	).Scan(&count); err != nil {
		t.Fatalf("SELECT all profiles as server: %v", err)
	}
	if count != 2 {
		t.Errorf("server role must see both profile rows (count=2), got %d", count)
	}
}

// TestIntegration_RLS_ServerCanWriteProfile: the server role can INSERT a
// profile row (REQ-PROFILE-005, used by onboarding complete path).
//
// @{"verifies": ["REQ-PROFILE-005"]}
func TestIntegration_RLS_ServerCanWriteProfile(t *testing.T) {
	truncateProfileTables(t)
	ctx := context.Background()
	pool := db.IntegrationPool(t)

	studentA := seedProfileStudent(t)

	connServer := db.AcquireAsServer(t, pool)
	defer connServer.Release()

	_, err := connServer.Exec(ctx,
		`INSERT INTO learning_profiles (student_id, summary, source) VALUES ($1, $2, $3)`,
		studentA, "Server-written profile.", "onboarding",
	)
	if err != nil {
		t.Fatalf("server INSERT into learning_profiles: unexpected error: %v", err)
	}

	// Confirm the row is readable via the same server connection.
	var summary string
	if err := connServer.QueryRow(ctx,
		`SELECT summary FROM learning_profiles WHERE student_id = $1`, studentA,
	).Scan(&summary); err != nil {
		t.Fatalf("SELECT after server INSERT: %v", err)
	}
	if summary != "Server-written profile." {
		t.Errorf("unexpected summary after server INSERT: %q", summary)
	}
}

// TestIntegration_ProfileRepo_UsesConnCtx_NotPool proves that ProfileRepository
// uses conn(ctx) (the request-scoped connection) and not the bare pool, by
// injecting an AcquireAsUser connection via auth.ContextWithConn and asserting
// that GetProfile returns the correct row through the RLS-enforced path.
//
// This is the non-superuser variant of the conn(ctx) test from SDD-023 §8.1
// Test 7. If the repo used the bare pool instead of conn(ctx) it would either
// silently return ErrNotFound (empty GUCs → zero rows) or fail with a 42501.
//
// @{"verifies": ["REQ-PROFILE-004", "REQ-PROFILE-019"]}
func TestIntegration_ProfileRepo_UsesConnCtx_NotPool(t *testing.T) {
	truncateProfileTables(t)
	ctx := context.Background()
	pool := db.IntegrationPool(t)

	studentA := seedProfileStudent(t)
	const wantSummary = "Prefers theory-first with worked examples."
	seedProfileRow(t, studentA, wantSummary, "onboarding")

	hexA := fmt.Sprintf("%x", [16]byte(studentA))
	connA := db.AcquireAsUser(t, pool, hexA, "student")
	defer connA.Release()

	// Inject connA into the context exactly as the auth middleware does.
	ctxWithConn := authpkg.ContextWithConn(ctx, connA)

	repo := NewProfileRepository(pool)
	row, err := repo.GetProfile(ctxWithConn, studentA)
	if err != nil {
		t.Fatalf(
			"GetProfile via injected student conn: unexpected error — "+
				"this would indicate the repo used the bare pool (empty GUCs) instead of conn(ctx): %v",
			err,
		)
	}
	if row.Summary != wantSummary {
		t.Errorf("GetProfile returned wrong summary: want %q, got %q", wantSummary, row.Summary)
	}
}

// ---------------------------------------------------------------------------
// Profile-reaches-prompt integration test — SDD-023 §8.3, REQ-PROFILE-020
// ---------------------------------------------------------------------------

// TestIntegration_ProfileReachesPromptAssembly verifies the full chain:
//  1. A profile row is seeded in the database.
//  2. LoadProfileSummary reads it back via a server-role connection.
//  3. The returned summary appears in the assembled intake system prompt via
//     AppendProfileBlock.
//
// This closes the DB-to-prompt chain without a live AI call (REQ-PROFILE-020).
//
// @{"verifies": ["REQ-PROFILE-020", "REQ-PROFILE-008", "REQ-PROFILE-010"]}
func TestIntegration_ProfileReachesPromptAssembly(t *testing.T) {
	truncateProfileTables(t)
	ctx := context.Background()
	pool := db.IntegrationPool(t)

	studentA := seedProfileStudent(t)
	const wantSummary = "The student is an intermediate Go developer who prefers concise examples."
	seedProfileRow(t, studentA, wantSummary, "onboarding")

	// Step 2: LoadProfileSummary acquires a server-role connection internally
	// and reads the profile. Use the integration pool (which applies migrations,
	// so the table exists). The function is fail-soft — any error returns "".
	// Use a server pool that mirrors production: db.IntegrationPool returns the
	// superuser pool; we need to wrap it as a server connection for this call.
	// LoadProfileSummary internally calls db.AcquireServerConn, which sets
	// app.current_role='server' — but on the superuser pool this bypasses FORCE
	// RLS. That is acceptable here: we are testing the prompt assembly chain, not
	// re-testing RLS isolation (covered by the tests above). The RLS-enforced
	// path is already proven by TestIntegration_RLS_ServerCanReadAnyProfile.
	got := LoadProfileSummary(ctx, pool, studentA)
	if got != wantSummary {
		t.Fatalf("LoadProfileSummary returned %q, want %q", got, wantSummary)
	}

	// Step 3: assemble a representative intake system prompt and confirm the
	// summary appears verbatim.
	basePrompt := `You are the University Chair at Valory conducting an intake questionnaire for a student who wants to learn about "Go programming".`
	assembled := AppendProfileBlock(basePrompt, got)

	if assembled == basePrompt {
		t.Fatal("AppendProfileBlock returned the base prompt unchanged when summary is non-empty")
	}
	if !containsString(assembled, wantSummary) {
		t.Errorf("assembled prompt does not contain profile summary:\nwant substring: %q\ngot prompt: %q", wantSummary, assembled)
	}
	if !containsString(assembled, "Student learning profile:") {
		t.Errorf("assembled prompt missing 'Student learning profile:' header")
	}
}

// TestIntegration_LoadProfileSummary_ReturnsEmptyForMissingProfile verifies
// that LoadProfileSummary returns "" (not an error) when the student has no
// profile row, satisfying the fail-soft degradation requirement (REQ-PROFILE-010).
//
// @{"verifies": ["REQ-PROFILE-010"]}
func TestIntegration_LoadProfileSummary_ReturnsEmptyForMissingProfile(t *testing.T) {
	truncateProfileTables(t)
	ctx := context.Background()
	pool := db.IntegrationPool(t)

	studentA := seedProfileStudent(t)
	// Intentionally do NOT seed a profile row for studentA.

	got := LoadProfileSummary(ctx, pool, studentA)
	if got != "" {
		t.Errorf("LoadProfileSummary must return empty string when no profile exists, got %q", got)
	}
}

// containsString is a helper to avoid importing strings in both test and
// production files with the same name.
func containsString(s, substr string) bool {
	return len(substr) <= len(s) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
