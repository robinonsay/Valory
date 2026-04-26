package user

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/valory/valory/internal/audit"
	"github.com/valory/valory/internal/auth"
)

// noopTerminator is the Sprint 2 stub that always succeeds immediately.
//
// @{"verifies": ["REQ-USER-007"]}
type noopTerminator struct{}

func (n *noopTerminator) TerminateStudentOperations(_ context.Context, _ uuid.UUID) error {
	return nil
}

// failingTerminator always returns an error to exercise the ErrDeletionBlocked path.
//
// @{"verifies": ["REQ-USER-007"]}
type failingTerminator struct{}

func (f *failingTerminator) TerminateStudentOperations(_ context.Context, _ uuid.UUID) error {
	return errors.New("agent busy")
}

// noopEmail discards outbound password-reset emails silently.
//
// @{"verifies": ["REQ-USER-005"]}
type noopEmail struct{}

func (n *noopEmail) SendPasswordReset(_ context.Context, _, _ string) error { return nil }

// newTestService constructs a Service backed by the shared test pool.
func newTestService() *Service {
	return NewService(
		pool,
		NewRepository(pool),
		audit.NewRepository(pool),
		&noopEmail{},
		1*time.Hour,
		&noopTerminator{},
	)
}

// auditEntryExists returns true when audit_log contains at least one row with
// the given action and target_id.
func auditEntryExists(ctx context.Context, t *testing.T, action string, targetID uuid.UUID) bool {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE action = $1 AND target_id = $2`,
		action, targetID).Scan(&count); err != nil {
		t.Fatalf("auditEntryExists query failed: %v", err)
	}
	return count > 0
}

// issueKnownToken generates a raw/hash token pair with a 1-hour TTL so tests
// can plant the hash in the DB and exercise ConfirmPasswordReset.
func issueKnownToken() (rawToken, tokenHash string, expiresAt time.Time, err error) {
	return auth.IssueToken(1 * time.Hour)
}

// @{"verifies": ["REQ-USER-001", "REQ-USER-002"]}
func TestServiceCreateUser_Success(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	svc := newTestService()

	adminID := createTestUser(ctx, t, "admin_cu_"+uuid.New().String(), nil, "hash", "admin")
	username := "student_cu_" + uuid.New().String()

	user, err := svc.CreateUser(ctx, adminID, username, nil, "student", "s3cr3tP@ss")
	if err != nil {
		t.Fatalf("CreateUser returned unexpected error: %v", err)
	}
	if user.Username != username {
		t.Errorf("expected username %q, got %q", username, user.Username)
	}
	if user.Role != "student" {
		t.Errorf("expected role student, got %q", user.Role)
	}
	if !user.IsActive {
		t.Error("expected IsActive true")
	}
	if !auditEntryExists(ctx, t, "user.create", user.ID) {
		t.Error("expected audit entry for user.create")
	}
}

// @{"verifies": ["REQ-USER-002"]}
func TestServiceCreateUser_DuplicateUsername(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	svc := newTestService()

	adminID := createTestUser(ctx, t, "admin_dup_"+uuid.New().String(), nil, "hash", "admin")
	username := "student_dup_" + uuid.New().String()

	if _, err := svc.CreateUser(ctx, adminID, username, nil, "student", "pass1"); err != nil {
		t.Fatalf("first CreateUser failed: %v", err)
	}

	_, err := svc.CreateUser(ctx, adminID, username, nil, "student", "pass2")
	if !errors.Is(err, ErrDuplicateUsername) {
		t.Fatalf("expected ErrDuplicateUsername, got %v", err)
	}
}

// @{"verifies": ["REQ-USER-001", "REQ-USER-002", "REQ-USER-003"]}
func TestServiceModifyUser_PartialUpdate(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	svc := newTestService()

	adminID := createTestUser(ctx, t, "admin_mod_"+uuid.New().String(), nil, "hash", "admin")
	userID := createTestUser(ctx, t, "student_mod_"+uuid.New().String(), nil, "oldhash", "student")

	newUsername := "student_mod_new_" + uuid.New().String()
	updated, err := svc.ModifyUser(ctx, adminID, userID, UpdateFields{Username: &newUsername})
	if err != nil {
		t.Fatalf("ModifyUser returned unexpected error: %v", err)
	}
	if updated.Username != newUsername {
		t.Errorf("expected username %q, got %q", newUsername, updated.Username)
	}
	if updated.PasswordHash != "oldhash" {
		t.Errorf("expected password hash unchanged, got %q", updated.PasswordHash)
	}
	if !auditEntryExists(ctx, t, "user.modify", userID) {
		t.Error("expected audit entry for user.modify")
	}
}

// @{"verifies": ["REQ-USER-001", "REQ-USER-003"]}
func TestServiceDeactivateUser_DeletesSessions(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	svc := newTestService()

	adminID := createTestUser(ctx, t, "admin_deact_"+uuid.New().String(), nil, "hash", "admin")
	userID := createTestUser(ctx, t, "student_deact_"+uuid.New().String(), nil, "hash", "student")
	createTestSession(ctx, t, userID, "tok_deact_"+uuid.New().String())

	if err := svc.DeactivateUser(ctx, adminID, userID); err != nil {
		t.Fatalf("DeactivateUser returned unexpected error: %v", err)
	}

	repo := NewRepository(pool)
	u, err := repo.GetUserByID(ctx, userID)
	if err != nil {
		t.Fatalf("GetUserByID failed: %v", err)
	}
	if u.IsActive {
		t.Error("expected IsActive false after deactivation")
	}

	var sessionCount int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM sessions WHERE user_id = $1`, userID).Scan(&sessionCount); err != nil {
		t.Fatalf("session count query failed: %v", err)
	}
	if sessionCount != 0 {
		t.Errorf("expected 0 sessions after deactivation, got %d", sessionCount)
	}
	if !auditEntryExists(ctx, t, "user.deactivate", userID) {
		t.Error("expected audit entry for user.deactivate")
	}
}

// @{"verifies": ["REQ-USER-003"]}
func TestServiceActivateUser_SetsActive(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	svc := newTestService()

	adminID := createTestUser(ctx, t, "admin_act_"+uuid.New().String(), nil, "hash", "admin")
	userID := createTestUser(ctx, t, "student_act_"+uuid.New().String(), nil, "hash", "student")

	if err := svc.DeactivateUser(ctx, adminID, userID); err != nil {
		t.Fatalf("DeactivateUser (setup) failed: %v", err)
	}
	if err := svc.ActivateUser(ctx, adminID, userID); err != nil {
		t.Fatalf("ActivateUser returned unexpected error: %v", err)
	}

	repo := NewRepository(pool)
	u, err := repo.GetUserByID(ctx, userID)
	if err != nil {
		t.Fatalf("GetUserByID failed: %v", err)
	}
	if !u.IsActive {
		t.Error("expected IsActive true after activation")
	}
	if !auditEntryExists(ctx, t, "user.activate", userID) {
		t.Error("expected audit entry for user.activate")
	}
}

// @{"verifies": ["REQ-USER-007"]}
func TestServiceDeleteStudent_BlockedByTerminator(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()

	// Build a service wired with a terminator that always errors.
	svc := NewService(
		pool,
		NewRepository(pool),
		audit.NewRepository(pool),
		&noopEmail{},
		1*time.Hour,
		&failingTerminator{},
	)

	adminID := createTestUser(ctx, t, "admin_blk_"+uuid.New().String(), nil, "hash", "admin")
	studentID := createTestUser(ctx, t, "student_blk_"+uuid.New().String(), nil, "hash", "student")

	err := svc.DeleteStudent(ctx, adminID, studentID)
	if !errors.Is(err, ErrDeletionBlocked) {
		t.Fatalf("expected ErrDeletionBlocked, got %v", err)
	}

	// Student must still exist — deletion must have been aborted.
	repo := NewRepository(pool)
	if _, err := repo.GetUserByID(ctx, studentID); err != nil {
		t.Fatalf("student should still exist after blocked deletion, got error: %v", err)
	}
}

// @{"verifies": ["REQ-USER-007"]}
func TestServiceDeleteStudent_CascadesAndAudits(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	svc := newTestService()

	adminID := createTestUser(ctx, t, "admin_del_"+uuid.New().String(), nil, "hash", "admin")
	studentID := createTestUser(ctx, t, "student_del_"+uuid.New().String(), nil, "hash", "student")

	if err := svc.DeleteStudent(ctx, adminID, studentID); err != nil {
		t.Fatalf("DeleteStudent returned unexpected error: %v", err)
	}

	repo := NewRepository(pool)
	if _, err := repo.GetUserByID(ctx, studentID); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound after deletion, got %v", err)
	}
	if !auditEntryExists(ctx, t, "user.delete", studentID) {
		t.Error("expected audit entry for user.delete")
	}
}

// @{"verifies": ["REQ-USER-005"]}
func TestServiceRequestPasswordReset_NoEnumeration(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	svc := newTestService()

	// A non-existent username must return nil, not ErrUserNotFound, so callers
	// cannot distinguish existing from non-existing accounts.
	if err := svc.RequestPasswordReset(ctx, "nonexistent_"+uuid.New().String()); err != nil {
		t.Fatalf("expected nil for unknown username, got %v", err)
	}
}

// @{"verifies": ["REQ-USER-005"]}
func TestServiceRequestPasswordReset_StoresToken(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	svc := newTestService()

	email := "reset_" + uuid.New().String() + "@example.com"
	username := "student_pr_" + uuid.New().String()
	userID := createTestUser(ctx, t, username, &email, "hash", "student")

	if err := svc.RequestPasswordReset(ctx, username); err != nil {
		t.Fatalf("RequestPasswordReset returned unexpected error: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM password_reset_tokens WHERE user_id = $1 AND used_at IS NULL AND expires_at > NOW()`,
		userID).Scan(&count); err != nil {
		t.Fatalf("token count query failed: %v", err)
	}
	if count == 0 {
		t.Error("expected at least one valid reset token in DB")
	}
}

// @{"verifies": ["REQ-USER-005", "REQ-USER-006"]}
func TestServiceConfirmPasswordReset_UpdatesPassword(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	svc := newTestService()
	repo := NewRepository(pool)

	username := "student_cp_" + uuid.New().String()
	userID := createTestUser(ctx, t, username, nil, "oldhash", "student")

	// Plant a known token so we can call ConfirmPasswordReset with the raw value.
	rawToken, tokenHash, expiresAt, err := issueKnownToken()
	if err != nil {
		t.Fatalf("issueKnownToken failed: %v", err)
	}
	if err := repo.CreatePasswordResetToken(ctx, userID, tokenHash, expiresAt); err != nil {
		t.Fatalf("CreatePasswordResetToken failed: %v", err)
	}

	if err := svc.ConfirmPasswordReset(ctx, rawToken, "newP@ssw0rd"); err != nil {
		t.Fatalf("ConfirmPasswordReset returned unexpected error: %v", err)
	}

	updated, err := repo.GetUserByID(ctx, userID)
	if err != nil {
		t.Fatalf("GetUserByID failed: %v", err)
	}
	if updated.PasswordHash == "oldhash" {
		t.Error("expected password hash to be updated after ConfirmPasswordReset")
	}
}

// @{"verifies": ["REQ-USER-005", "REQ-USER-006"]}
func TestServiceConfirmPasswordReset_RejectsUsedToken(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	svc := newTestService()
	repo := NewRepository(pool)

	username := "student_ut_" + uuid.New().String()
	userID := createTestUser(ctx, t, username, nil, "hash", "student")

	rawToken, tokenHash, expiresAt, err := issueKnownToken()
	if err != nil {
		t.Fatalf("issueKnownToken failed: %v", err)
	}
	if err := repo.CreatePasswordResetToken(ctx, userID, tokenHash, expiresAt); err != nil {
		t.Fatalf("CreatePasswordResetToken failed: %v", err)
	}

	if err := svc.ConfirmPasswordReset(ctx, rawToken, "p@ssw0rd1"); err != nil {
		t.Fatalf("first ConfirmPasswordReset failed: %v", err)
	}

	if err := svc.ConfirmPasswordReset(ctx, rawToken, "p@ssw0rd2"); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("expected ErrTokenNotFound on second use, got %v", err)
	}
}

// @{"verifies": ["REQ-SECURITY-005"]}
func TestServiceRecordConsent(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	svc := newTestService()
	repo := NewRepository(pool)

	studentID := createTestUser(ctx, t, "student_cons_"+uuid.New().String(), nil, "hash", "student")

	if err := svc.RecordConsent(ctx, studentID, "1.0"); err != nil {
		t.Fatalf("RecordConsent returned unexpected error: %v", err)
	}

	version, err := repo.GetConsentVersion(ctx, studentID)
	if err != nil {
		t.Fatalf("GetConsentVersion failed: %v", err)
	}
	if version != "1.0" {
		t.Errorf("expected consent version 1.0, got %q", version)
	}

	if err := svc.RecordConsent(ctx, studentID, "2.0"); err != nil {
		t.Fatalf("second RecordConsent returned unexpected error: %v", err)
	}

	version, err = repo.GetConsentVersion(ctx, studentID)
	if err != nil {
		t.Fatalf("GetConsentVersion (v2) failed: %v", err)
	}
	if version != "2.0" {
		t.Errorf("expected consent version 2.0, got %q", version)
	}
}
