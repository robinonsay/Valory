package user

// lifecycle_test.go — unit and DB-gated tests for Sprint 20 account-lifecycle
// features: temp-password generation, create-with-temp-password, resend-welcome,
// change-password, and must-change-password enforcement middleware.
//
// DB-gated tests skip automatically when TEST_DATABASE_URL is not set (the
// shared pool var is initialised by TestMain in repository_test.go).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/valory/valory/internal/audit"
	authpkg "github.com/valory/valory/internal/auth"
	emailpkg "github.com/valory/valory/internal/email"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// recordingMailer captures every send so tests can assert on subject/body
// content and, critically, that no password appears in any send payload.
//
// @{"verifies": ["REQ-USER-010", "REQ-USER-011", "REQ-USER-017", "REQ-USER-018"]}
type recordingMailer struct {
	calls []mailCall
	fail  bool
}

type mailCall struct {
	to      string
	subject string
	body    string
}

func (m *recordingMailer) Send(_ context.Context, to, subject, body string) error {
	m.calls = append(m.calls, mailCall{to: to, subject: subject, body: body})
	if m.fail {
		return fmt.Errorf("smtp: connection refused")
	}
	return nil
}

func (m *recordingMailer) IsConfigured() bool { return true }

// failMailer implements email.Mailer but always returns an error from Send.
//
// @{"verifies": ["REQ-USER-011"]}
type failMailer struct{}

func (f *failMailer) Send(_ context.Context, _, _, _ string) error {
	return fmt.Errorf("smtp: connection refused")
}
func (f *failMailer) IsConfigured() bool { return true }

// unconfiguredMailer simulates IsConfigured() == false (no SMTP host set).
//
// @{"verifies": ["REQ-USER-011"]}
type unconfiguredMailer struct{}

func (u *unconfiguredMailer) Send(_ context.Context, _, _, _ string) error { return nil }
func (u *unconfiguredMailer) IsConfigured() bool                           { return false }

// newLifecycleSvc builds a Service wired to the test pool with the supplied mailer.
// @{"verifies": ["REQ-USER-008", "REQ-USER-009", "REQ-USER-010", "REQ-USER-012", "REQ-USER-014", "REQ-USER-015", "REQ-USER-016"]}
func newLifecycleSvc(m emailpkg.Mailer) *Service {
	svc := NewService(
		pool,
		NewRepository(pool),
		audit.NewRepository(pool),
		&noopEmail{},
		1*time.Hour,
		&noopTerminator{},
	)
	svc.SetMailer(m)
	return svc
}

// createUserWithPasswordHash directly inserts a user row with a known
// must_change_password value; used to seed DB state for middleware tests.
// @{"verifies": ["REQ-AUTH-014"]}
func createUserWithFlag(ctx context.Context, t *testing.T, mustChange bool) uuid.UUID {
	t.Helper()
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role, must_change_password)
		 VALUES ($1, 'hash', 'student', $2) RETURNING id`,
		"flag_user_"+uuid.New().String(), mustChange,
	).Scan(&id)
	if err != nil {
		t.Fatalf("createUserWithFlag: %v", err)
	}
	return id
}

// ── tempPasswordAlphabet / generateTempPassword ───────────────────────────────

// @{"verifies": ["REQ-USER-008", "REQ-SYS-063"]}
func TestGenerateTempPassword_Length(t *testing.T) {
	pwd, err := generateTempPassword()
	if err != nil {
		t.Fatalf("generateTempPassword returned error: %v", err)
	}
	if len(pwd) != tempPasswordLen {
		t.Errorf("expected length %d, got %d (password=%q)", tempPasswordLen, len(pwd), pwd)
	}
}

// @{"verifies": ["REQ-USER-008", "REQ-SYS-063"]}
func TestGenerateTempPassword_AlphabetOnly(t *testing.T) {
	for i := 0; i < 50; i++ {
		pwd, err := generateTempPassword()
		if err != nil {
			t.Fatalf("generateTempPassword returned error: %v", err)
		}
		for _, ch := range pwd {
			if !strings.ContainsRune(tempPasswordAlphabet, ch) {
				t.Errorf("character %q not in alphabet (password=%q)", ch, pwd)
			}
		}
	}
}

// @{"verifies": ["REQ-USER-008", "REQ-SYS-063"]}
func TestGenerateTempPassword_Uniqueness(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		pwd, err := generateTempPassword()
		if err != nil {
			t.Fatalf("generateTempPassword returned error: %v", err)
		}
		if seen[pwd] {
			t.Errorf("duplicate temp password generated at iteration %d: %q", i, pwd)
		}
		seen[pwd] = true
	}
}

// @{"verifies": ["REQ-USER-008", "REQ-SYS-063"]}
func TestGenerateTempPassword_Entropy(t *testing.T) {
	// Confirm the alphabet size matches expectations: log2(56^16) ≈ 93 bits.
	alphabetLen := big.NewInt(int64(len(tempPasswordAlphabet)))
	maxVal := new(big.Int).Exp(alphabetLen, big.NewInt(tempPasswordLen), nil)
	// Require at least 2^80 possible values (80 bits of entropy minimum).
	threshold := new(big.Int).Exp(big.NewInt(2), big.NewInt(80), nil)
	if maxVal.Cmp(threshold) < 0 {
		t.Errorf("temp password entropy too low: %d possible values, need >= 2^80", maxVal)
	}
}

// ── createUser — generate_password vs password validation matrix ──────────────

// @{"verifies": ["REQ-USER-008"]}
func TestCreateUser_GenerateAndExplicitMutuallyExclusive(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	adminID := createTestUser(ctx, t, "admin_mutex_"+uuid.New().String(), nil, "hash", "admin")

	handler := NewHandler(newLifecycleSvc(&unconfiguredMailer{}))

	// Both provided → 400.
	body, _ := json.Marshal(map[string]interface{}{
		"username":          "u_both_" + uuid.New().String(),
		"role":              "student",
		"password":          "secret123",
		"generate_password": true,
	})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req = req.WithContext(authpkg.ContextWithUserID(req.Context(), [16]byte(adminID)))
	w := httptest.NewRecorder()

	r := chi.NewRouter()
	r.Post("/", handler.createUser)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("both provided: expected 400, got %d", w.Code)
	}

	// Neither provided → 400.
	body2, _ := json.Marshal(map[string]interface{}{
		"username": "u_neither_" + uuid.New().String(),
		"role":     "student",
	})
	req2 := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body2))
	req2 = req2.WithContext(authpkg.ContextWithUserID(req2.Context(), [16]byte(adminID)))
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("neither provided: expected 400, got %d", w2.Code)
	}
}

// ── CreateUserWithTempPassword service ────────────────────────────────────────

// @{"verifies": ["REQ-USER-008", "REQ-USER-009", "REQ-USER-017"]}
func TestCreateUserWithTempPassword_SetsFlag(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	svc := newLifecycleSvc(&unconfiguredMailer{})
	adminID := createTestUser(ctx, t, "admin_ctmp_"+uuid.New().String(), nil, "hash", "admin")
	email := "tmp_" + uuid.New().String() + "@example.com"

	user, _, err := svc.CreateUserWithTempPassword(ctx, adminID, "stu_ctmp_"+uuid.New().String(), &email, "student")
	if err != nil {
		t.Fatalf("CreateUserWithTempPassword failed: %v", err)
	}
	if !user.MustChangePassword {
		t.Error("expected MustChangePassword=true on created user")
	}

	// Verify persisted in DB.
	repo := NewRepository(pool)
	u, err := repo.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUserByID failed: %v", err)
	}
	if !u.MustChangePassword {
		t.Error("expected must_change_password=true in DB")
	}
}

// @{"verifies": ["REQ-USER-017"]}
func TestCreateUserWithTempPassword_AuditNeverContainsPassword(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	rec := &recordingMailer{}
	svc := newLifecycleSvc(rec)
	adminID := createTestUser(ctx, t, "admin_auditpwd_"+uuid.New().String(), nil, "hash", "admin")
	email := "auditpwd_" + uuid.New().String() + "@example.com"

	user, result, err := svc.CreateUserWithTempPassword(ctx, adminID, "stu_auditpwd_"+uuid.New().String(), &email, "student")
	if err != nil {
		t.Fatalf("CreateUserWithTempPassword failed: %v", err)
	}
	tempPwd := result.TempPassword
	if result.EmailSent {
		// Email was sent; get the password from the email body instead.
		if len(rec.calls) == 0 {
			t.Fatal("email_sent=true but no email recorded")
		}
		tempPwd = extractTempPasswordFromBody(rec.calls[0].body)
	}

	// Scan all audit_log rows for this user to confirm no password appears.
	rows, err := pool.Query(ctx,
		`SELECT payload FROM audit_log WHERE target_id = $1`, user.ID)
	if err != nil {
		t.Fatalf("audit query failed: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			t.Fatalf("scan payload: %v", err)
		}
		if tempPwd != "" && strings.Contains(payload, tempPwd) {
			t.Errorf("audit payload contains temp password: %s", payload)
		}
	}
}

// extractTempPasswordFromBody parses the cleartext temp password out of a
// welcome email body for test-assertion purposes only.
func extractTempPasswordFromBody(body string) string {
	const prefix = "Temporary password: "
	idx := strings.Index(body, prefix)
	if idx < 0 {
		return ""
	}
	rest := body[idx+len(prefix):]
	end := strings.IndexAny(rest, "\r\n")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// @{"verifies": ["REQ-USER-010", "REQ-USER-011"]}
func TestCreateUserWithTempPassword_EmailSent(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	rec := &recordingMailer{}
	svc := newLifecycleSvc(rec)
	adminID := createTestUser(ctx, t, "admin_esent_"+uuid.New().String(), nil, "hash", "admin")
	email := "esent_" + uuid.New().String() + "@example.com"

	_, result, err := svc.CreateUserWithTempPassword(ctx, adminID, "stu_esent_"+uuid.New().String(), &email, "student")
	if err != nil {
		t.Fatalf("CreateUserWithTempPassword failed: %v", err)
	}
	if !result.EmailSent {
		t.Error("expected email_sent=true when mailer is configured and succeeds")
	}
	if result.TempPassword != "" {
		t.Error("temp_password must not be returned when email was sent")
	}
	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 email send, got %d", len(rec.calls))
	}
	if rec.calls[0].to != email {
		t.Errorf("email sent to wrong address: got %q, want %q", rec.calls[0].to, email)
	}
}

// @{"verifies": ["REQ-USER-011"]}
func TestCreateUserWithTempPassword_EmailFailSoft(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	svc := newLifecycleSvc(&failMailer{})
	adminID := createTestUser(ctx, t, "admin_esoft_"+uuid.New().String(), nil, "hash", "admin")
	email := "esoft_" + uuid.New().String() + "@example.com"

	user, result, err := svc.CreateUserWithTempPassword(ctx, adminID, "stu_esoft_"+uuid.New().String(), &email, "student")
	if err != nil {
		t.Fatalf("CreateUserWithTempPassword must succeed even when email fails, got: %v", err)
	}
	if result.EmailSent {
		t.Error("expected email_sent=false when mailer returns error")
	}
	if result.TempPassword == "" {
		t.Error("expected temp_password in response when email was not sent")
	}
	// User must exist in DB.
	repo := NewRepository(pool)
	if _, err := repo.GetUserByID(ctx, user.ID); err != nil {
		t.Fatalf("user must exist in DB after email failure, got: %v", err)
	}
}

// @{"verifies": ["REQ-USER-011"]}
func TestCreateUserWithTempPassword_UnconfiguredMailer(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	svc := newLifecycleSvc(&unconfiguredMailer{})
	adminID := createTestUser(ctx, t, "admin_nomail_"+uuid.New().String(), nil, "hash", "admin")
	email := "nomail_" + uuid.New().String() + "@example.com"

	_, result, err := svc.CreateUserWithTempPassword(ctx, adminID, "stu_nomail_"+uuid.New().String(), &email, "student")
	if err != nil {
		t.Fatalf("CreateUserWithTempPassword failed: %v", err)
	}
	if result.EmailSent {
		t.Error("expected email_sent=false when mailer is not configured")
	}
	if result.TempPassword == "" {
		t.Error("expected temp_password when email not sent")
	}
}

// ── ResendWelcome ──────────────────────────────────────────────────────────────

// @{"verifies": ["REQ-USER-012", "REQ-USER-013"]}
func TestResendWelcome_InvalidatesOldPassword(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	rec := &recordingMailer{}
	svc := newLifecycleSvc(rec)
	adminID := createTestUser(ctx, t, "admin_resend_"+uuid.New().String(), nil, "hash", "admin")
	email := "resend_" + uuid.New().String() + "@example.com"

	// Create with temp password — captures first hash via the mailer body.
	user, firstResult, err := svc.CreateUserWithTempPassword(ctx, adminID, "stu_resend_"+uuid.New().String(), &email, "student")
	if err != nil {
		t.Fatalf("CreateUserWithTempPassword failed: %v", err)
	}
	firstPwd := firstResult.TempPassword
	if firstPwd == "" {
		firstPwd = extractTempPasswordFromBody(rec.calls[0].body)
	}
	firstHash := user.PasswordHash

	// Resend — should overwrite the hash.
	result, err := svc.ResendWelcome(ctx, adminID, user.ID)
	if err != nil {
		t.Fatalf("ResendWelcome failed: %v", err)
	}
	secondPwd := result.TempPassword
	if secondPwd == "" && len(rec.calls) >= 2 {
		secondPwd = extractTempPasswordFromBody(rec.calls[1].body)
	}

	// The two cleartext passwords must differ.
	if firstPwd != "" && secondPwd != "" && firstPwd == secondPwd {
		t.Error("resend should generate a different temp password")
	}

	// The DB hash must have changed.
	repo := NewRepository(pool)
	updated, err := repo.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUserByID after resend: %v", err)
	}
	if updated.PasswordHash == firstHash {
		t.Error("expected password hash to change after resend")
	}
	if !updated.MustChangePassword {
		t.Error("expected must_change_password=true after resend")
	}
}

// @{"verifies": ["REQ-USER-018"]}
func TestResendWelcome_AuditNeverContainsPassword(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	rec := &recordingMailer{}
	svc := newLifecycleSvc(rec)
	adminID := createTestUser(ctx, t, "admin_rawelcome_"+uuid.New().String(), nil, "hash", "admin")
	email := "rawelcome_" + uuid.New().String() + "@example.com"

	user, _, err := svc.CreateUserWithTempPassword(ctx, adminID, "stu_rawelcome_"+uuid.New().String(), &email, "student")
	if err != nil {
		t.Fatalf("CreateUserWithTempPassword failed: %v", err)
	}

	result, err := svc.ResendWelcome(ctx, adminID, user.ID)
	if err != nil {
		t.Fatalf("ResendWelcome failed: %v", err)
	}
	newPwd := result.TempPassword
	if newPwd == "" && len(rec.calls) >= 2 {
		newPwd = extractTempPasswordFromBody(rec.calls[1].body)
	}

	// Scan audit rows for the resend action.
	rows, err := pool.Query(ctx,
		`SELECT payload FROM audit_log WHERE target_id = $1 AND action = 'user.resend_welcome'`,
		user.ID)
	if err != nil {
		t.Fatalf("audit query: %v", err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		found = true
		var payload string
		if err := rows.Scan(&payload); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if newPwd != "" && strings.Contains(payload, newPwd) {
			t.Errorf("resend audit payload contains temp password")
		}
	}
	if !found {
		t.Error("expected user.resend_welcome audit entry")
	}
}

// @{"verifies": ["REQ-USER-012"]}
func TestResendWelcome_NotFound(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	svc := newLifecycleSvc(&unconfiguredMailer{})
	adminID := createTestUser(ctx, t, "admin_rwnf_"+uuid.New().String(), nil, "hash", "admin")

	_, err := svc.ResendWelcome(ctx, adminID, uuid.New())
	if !errors.Is(err, ErrUserNotFound) {
		t.Errorf("expected ErrUserNotFound for non-existent user, got %v", err)
	}
}

// ── ChangePassword ────────────────────────────────────────────────────────────

// @{"verifies": ["REQ-USER-014"]}
func TestChangePassword_WrongCurrentPassword(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	svc := newLifecycleSvc(&unconfiguredMailer{})

	userID, _ := createTestUserWithPassword(ctx, t, "stu_wrongpwd_"+uuid.New().String(), nil, "correct123", "student")
	tok := "tok_wrongpwd_" + uuid.New().String()
	createTestSession(ctx, t, userID, tok)

	err := svc.ChangePassword(ctx, userID, "wrong_password", "newpassword1", authpkg.HashToken(tok))
	if !errors.Is(err, ErrWrongPassword) {
		t.Errorf("expected ErrWrongPassword, got %v", err)
	}
}

// @{"verifies": ["REQ-USER-014"]}
func TestChangePassword_TooShort(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	svc := newLifecycleSvc(&unconfiguredMailer{})

	userID, _ := createTestUserWithPassword(ctx, t, "stu_short_"+uuid.New().String(), nil, "correct123", "student")
	tok := "tok_short_" + uuid.New().String()
	createTestSession(ctx, t, userID, tok)

	err := svc.ChangePassword(ctx, userID, "correct123", "short", authpkg.HashToken(tok))
	if !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("expected ErrPasswordTooShort, got %v", err)
	}
}

// @{"verifies": ["REQ-USER-015"]}
func TestChangePassword_ClearsMustChangeFlag(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	svc := newLifecycleSvc(&unconfiguredMailer{})
	adminID := createTestUser(ctx, t, "admin_clearflag_"+uuid.New().String(), nil, "hash", "admin")
	email := "clearflag_" + uuid.New().String() + "@example.com"

	// Create with temp password so flag is set.
	user, result, err := svc.CreateUserWithTempPassword(ctx, adminID, "stu_clearflag_"+uuid.New().String(), &email, "student")
	if err != nil {
		t.Fatalf("CreateUserWithTempPassword failed: %v", err)
	}
	tempPwd := result.TempPassword

	// Create a session for the user.
	tok := "tok_clearflag_" + uuid.New().String()
	createTestSession(ctx, t, user.ID, tok)

	if err := svc.ChangePassword(ctx, user.ID, tempPwd, "newSecure123", authpkg.HashToken(tok)); err != nil {
		t.Fatalf("ChangePassword failed: %v", err)
	}

	repo := NewRepository(pool)
	updated, err := repo.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUserByID failed: %v", err)
	}
	if updated.MustChangePassword {
		t.Error("expected must_change_password=false after successful ChangePassword")
	}
}

// @{"verifies": ["REQ-USER-016"]}
func TestChangePassword_DeletesOtherSessionsKeepsCurrent(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	svc := newLifecycleSvc(&unconfiguredMailer{})

	userID, _ := createTestUserWithPassword(ctx, t, "stu_sessions_"+uuid.New().String(), nil, "correct123", "student")

	currentTok := "tok_current_" + uuid.New().String()
	otherTok1 := "tok_other1_" + uuid.New().String()
	otherTok2 := "tok_other2_" + uuid.New().String()

	createTestSession(ctx, t, userID, currentTok)
	createTestSession(ctx, t, userID, otherTok1)
	createTestSession(ctx, t, userID, otherTok2)

	if err := svc.ChangePassword(ctx, userID, "correct123", "newpassword99", authpkg.HashToken(currentTok)); err != nil {
		t.Fatalf("ChangePassword failed: %v", err)
	}

	// Only the current session should remain.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM sessions WHERE user_id = $1`, userID,
	).Scan(&count); err != nil {
		t.Fatalf("session count query: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 session after ChangePassword, got %d", count)
	}

	// Confirm the surviving session is the current one.
	var remainingHash string
	if err := pool.QueryRow(ctx,
		`SELECT token_hash FROM sessions WHERE user_id = $1`, userID,
	).Scan(&remainingHash); err != nil {
		t.Fatalf("surviving session query: %v", err)
	}
	if remainingHash != authpkg.HashToken(currentTok) {
		t.Errorf("wrong session survived: got hash %q, want hash of currentTok", remainingHash)
	}
}

// @{"verifies": ["REQ-USER-019"]}
func TestChangePassword_AuditNeverContainsPassword(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	svc := newLifecycleSvc(&unconfiguredMailer{})

	userID, _ := createTestUserWithPassword(ctx, t, "stu_cpaudit_"+uuid.New().String(), nil, "correct123", "student")
	tok := "tok_cpaudit_" + uuid.New().String()
	createTestSession(ctx, t, userID, tok)

	newPwd := "newpassword_audit_probe"
	if err := svc.ChangePassword(ctx, userID, "correct123", newPwd, authpkg.HashToken(tok)); err != nil {
		t.Fatalf("ChangePassword failed: %v", err)
	}

	rows, err := pool.Query(ctx,
		`SELECT payload FROM audit_log WHERE target_id = $1 AND action = 'user.password_change'`,
		userID)
	if err != nil {
		t.Fatalf("audit query: %v", err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		found = true
		var payload string
		if err := rows.Scan(&payload); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if strings.Contains(payload, "correct123") {
			t.Errorf("audit payload contains old password")
		}
		if strings.Contains(payload, newPwd) {
			t.Errorf("audit payload contains new password")
		}
	}
	if !found {
		t.Error("expected user.password_change audit entry")
	}
}

// ── MustChangePasswordMiddleware ──────────────────────────────────────────────

// makeMiddlewareRequest builds a request authenticated as userID through the
// MustChangePasswordMiddleware, serving it against a trivial 200 handler, and
// returns the response code.
func makeMiddlewareRequest(t *testing.T, userID uuid.UUID, method, path string) int {
	t.Helper()
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	mw := authpkg.MustChangePasswordMiddleware(pool)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(method, path, nil)
	req = req.WithContext(authpkg.ContextWithUserID(req.Context(), [16]byte(userID)))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w.Code
}

// @{"verifies": ["REQ-AUTH-014", "REQ-SYS-064"]}
func TestMustChangePasswordMiddleware_BlocksWhenFlagged(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	userID := createUserWithFlag(ctx, t, true)

	code := makeMiddlewareRequest(t, userID, http.MethodGet, "/api/v1/courses")
	if code != http.StatusForbidden {
		t.Errorf("expected 403 for flagged user on /courses, got %d", code)
	}
}

// @{"verifies": ["REQ-AUTH-014"]}
func TestMustChangePasswordMiddleware_AllowsUnflaggedUser(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	userID := createUserWithFlag(ctx, t, false)

	code := makeMiddlewareRequest(t, userID, http.MethodGet, "/api/v1/courses")
	if code != http.StatusOK {
		t.Errorf("expected 200 for unflagged user, got %d", code)
	}
}

// @{"verifies": ["REQ-AUTH-014"]}
func TestMustChangePasswordMiddleware_ExemptPaths(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	userID := createUserWithFlag(ctx, t, true)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/users/me/password"},
		{http.MethodPost, "/api/v1/auth/logout"},
		{http.MethodGet, "/api/v1/users/me"},
	}

	for _, tc := range cases {
		code := makeMiddlewareRequest(t, userID, tc.method, tc.path)
		if code != http.StatusOK {
			t.Errorf("exempt path %s %s: expected 200, got %d", tc.method, tc.path, code)
		}
	}
}

// @{"verifies": ["REQ-AUTH-014"]}
func TestMustChangePasswordMiddleware_ErrorBodyCode(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	userID := createUserWithFlag(ctx, t, true)

	mw := authpkg.MustChangePasswordMiddleware(pool)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/courses", nil)
	req = req.WithContext(authpkg.ContextWithUserID(req.Context(), [16]byte(userID)))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	if body["error"] != "MUST_CHANGE_PASSWORD" {
		t.Errorf("expected error=MUST_CHANGE_PASSWORD, got %q", body["error"])
	}
}
