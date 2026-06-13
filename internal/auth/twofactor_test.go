package auth

// twofactor_test.go — unit and DB-gated tests for the email-2FA feature.
//
// Tests that require a database self-skip when pool == nil (no TEST_DATABASE_URL
// set). Tests that only exercise in-process logic run unconditionally.
//
// @{"verifies": ["REQ-AUTH-015", "REQ-AUTH-016", "REQ-AUTH-017", "REQ-AUTH-019", "REQ-AUTH-020", "REQ-AUTH-022", "REQ-AUTH-023", "REQ-AUTH-024", "REQ-AUTH-025", "REQ-AUTH-026"]}

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/valory/valory/internal/audit"
)

// ---------------------------------------------------------------------------
// Pure unit tests (no DB required)
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-AUTH-016"]}
func TestGenerateOTP_SixDigits(t *testing.T) {
	for i := 0; i < 50; i++ {
		plain, hash, err := GenerateOTP()
		if err != nil {
			t.Fatalf("GenerateOTP error: %v", err)
		}
		if len(plain) != 6 {
			t.Errorf("expected 6-digit OTP, got %q (len %d)", plain, len(plain))
		}
		for _, ch := range plain {
			if ch < '0' || ch > '9' {
				t.Errorf("OTP %q contains non-digit character %q", plain, ch)
			}
		}
		if hash == "" {
			t.Error("expected non-empty OTP hash")
		}
		// Hash must equal HashToken of the plain value.
		if got := HashToken(plain); got != hash {
			t.Errorf("hash mismatch: HashToken(%q)=%q, GenerateOTP hash=%q", plain, got, hash)
		}
	}
}

// @{"verifies": ["REQ-AUTH-016"]}
func TestGenerateOTP_LeadingZerosPreserved(t *testing.T) {
	// Generate many OTPs; statistically some will start with 0.
	// Verify that all are exactly 6 characters (leading zeros not stripped).
	found := false
	for i := 0; i < 10_000; i++ {
		plain, _, _ := GenerateOTP()
		if len(plain) != 6 {
			t.Fatalf("OTP length %d, expected 6, value %q", len(plain), plain)
		}
		if plain[0] == '0' {
			found = true
			break
		}
	}
	if !found {
		t.Log("warning: no leading-zero OTP found in 10,000 samples (statistically unlikely but possible)")
	}
}

// @{"verifies": ["REQ-AUTH-016"]}
func TestHashToken_OTPRoundtrip(t *testing.T) {
	otp := "042817"
	h := HashToken(otp)
	if h == "" {
		t.Fatal("HashToken returned empty string")
	}
	// Same input must produce the same hash (deterministic).
	if HashToken(otp) != h {
		t.Error("HashToken is not deterministic")
	}
	// Different input must produce a different hash.
	if HashToken("000000") == HashToken("999999") {
		t.Error("HashToken collision on different OTPs")
	}
}

// @{"verifies": ["REQ-AUTH-020"]}
func TestIsBreakGlassActive_Absent(t *testing.T) {
	os.Unsetenv("VALORY_2FA_BREAK_GLASS")
	if isBreakGlassActive() {
		t.Error("expected break-glass inactive when env var is not set")
	}
}

// @{"verifies": ["REQ-AUTH-020"]}
func TestIsBreakGlassActive_Present(t *testing.T) {
	os.Setenv("VALORY_2FA_BREAK_GLASS", "1")
	defer os.Unsetenv("VALORY_2FA_BREAK_GLASS")
	if !isBreakGlassActive() {
		t.Error("expected break-glass active when env var is set")
	}
}

// ---------------------------------------------------------------------------
// DB-gated tests
// ---------------------------------------------------------------------------

// noopMailer is a minimal Mailer that records sent OTPs for test assertions.
type noopMailer struct {
	sent []string // subject lines sent
}

func (m *noopMailer) Send(_ context.Context, to, subject, body string) error {
	m.sent = append(m.sent, fmt.Sprintf("to=%s subject=%s", to, subject))
	return nil
}
func (m *noopMailer) IsConfigured() bool { return true }

// failMailer always returns an error on Send.
type failMailer struct{}

func (m *failMailer) Send(_ context.Context, _, _, _ string) error {
	return fmt.Errorf("SMTP connection refused")
}
func (m *failMailer) IsConfigured() bool { return true }

// noopWriter satisfies io.Writer for audit.Repository in test (uses NoOpMailer
// pattern — we route audit to /dev/null in unit tests so the audit chain table
// is not required in the minimal migration).
type noopWriter struct{}

func (noopWriter) Write(b []byte) (int, error) { return len(b), nil }

// staticConfig is a minimal ConfigReader for tests.
type staticConfig struct{ vals map[string]string }

func (c *staticConfig) GetString(key string) string { return c.vals[key] }

// createTestUserWithEmail inserts a user with an email address.
//
// @{"req": ["REQ-AUTH-015", "REQ-AUTH-023"]}
func createTestUserWithEmail(ctx context.Context, t *testing.T, username, password, role, email string) [16]byte {
	t.Helper()
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	var id [16]byte
	err = pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role, email) VALUES ($1, $2, $3, $4) RETURNING id`,
		username, hash, role, email,
	).Scan(&id)
	if err != nil {
		t.Fatalf("createTestUserWithEmail %q: %v", username, err)
	}
	return id
}

// newTestTwoFactorService constructs a TwoFactorService backed by the shared
// test pool.
func newTestTwoFactorService(m interface {
	Send(context.Context, string, string, string) error
	IsConfigured() bool
}) *TwoFactorService {
	return NewTwoFactorService(
		NewRepository(pool),
		audit.NewRepository(pool),
		pool,
		&staticConfig{vals: map[string]string{"two_factor_enabled": "true"}},
		m,
	)
}

// @{"verifies": ["REQ-AUTH-015", "REQ-AUTH-016", "REQ-AUTH-024"]}
func TestIssuePendingTwoFactor_Success(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithEmail(ctx, t, "2fa_issue_ok", "pass", "student", "user@example.com")

	repo := NewRepository(pool)
	user, err := repo.GetUserByUsername(ctx, "2fa_issue_ok")
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}

	mailer := &noopMailer{}
	tf := newTestTwoFactorService(mailer)

	result, err := tf.IssuePendingTwoFactor(ctx, user)
	if err != nil {
		t.Fatalf("IssuePendingTwoFactor: %v", err)
	}
	if result.PendingToken == "" {
		t.Error("expected non-empty pending token")
	}
	if result.ExpiresAt.IsZero() {
		t.Error("expected non-zero expires_at")
	}
	if len(mailer.sent) != 1 {
		t.Errorf("expected 1 email, got %d", len(mailer.sent))
	}

	// Verify the row exists in pending_2fa.
	pendingHash := HashToken(result.PendingToken)
	row, err := repo.GetPendingByTokenHash(ctx, pendingHash)
	if err != nil {
		t.Fatalf("GetPendingByTokenHash: %v", err)
	}
	if row.AttemptCount != 0 {
		t.Errorf("expected 0 attempts, got %d", row.AttemptCount)
	}
}

// @{"verifies": ["REQ-AUTH-023"]}
func TestIssuePendingTwoFactor_NoEmail_ReturnsDistinctError(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	// Create user without email.
	createTestUserWithPassword(ctx, t, pool, "2fa_noemail", "pass", "student")

	repo := NewRepository(pool)
	user, err := repo.GetUserByUsername(ctx, "2fa_noemail")
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}

	tf := newTestTwoFactorService(&noopMailer{})
	_, err = tf.IssuePendingTwoFactor(ctx, user)
	if !errors.Is(err, ErrNoEmailAddress) {
		t.Fatalf("expected ErrNoEmailAddress, got %v", err)
	}
}

// @{"verifies": ["REQ-AUTH-016", "REQ-AUTH-024"]}
func TestVerifyOTP_CorrectCode_IssuesSession(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithEmail(ctx, t, "2fa_verify_ok", "pass", "student", "v@example.com")

	repo := NewRepository(pool)
	user, _ := repo.GetUserByUsername(ctx, "2fa_verify_ok")

	// Capture the OTP by intercepting via a recording mailer.
	var capturedOTP string
	recMailer := &recordMailer{onSend: func(body string) { capturedOTP = extractOTPFromBody(body) }}
	tf := newTestTwoFactorService(recMailer)

	pending, err := tf.IssuePendingTwoFactor(ctx, user)
	if err != nil {
		t.Fatalf("IssuePendingTwoFactor: %v", err)
	}

	rawToken, session, err := tf.VerifyOTP(ctx, pending.PendingToken, capturedOTP, 24*time.Hour)
	if err != nil {
		t.Fatalf("VerifyOTP: %v", err)
	}
	if rawToken == "" {
		t.Error("expected non-empty session token")
	}
	if session == nil {
		t.Error("expected session")
	}

	// The pending row must be deleted (single-use).
	pendingHash := HashToken(pending.PendingToken)
	_, err = repo.GetPendingByTokenHash(ctx, pendingHash)
	if !errors.Is(err, ErrPendingNotFound) {
		t.Errorf("expected pending row deleted after successful verify, got: %v", err)
	}
}

// @{"verifies": ["REQ-AUTH-016"]}
func TestVerifyOTP_ExpiredPending_ReturnsInvalid(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithEmail(ctx, t, "2fa_expired", "pass", "student", "exp@example.com")

	repo := NewRepository(pool)
	user, _ := repo.GetUserByUsername(ctx, "2fa_expired")

	// Manually insert a pending row that is already expired.
	expiredAt := time.Now().Add(-1 * time.Minute)
	_, otpHash, _ := GenerateOTP()
	rawPending, pendingHash, _, _ := IssueToken(1 * time.Hour)
	if _, err := repo.CreatePending(ctx, user.ID, pendingHash, otpHash, expiredAt); err != nil {
		t.Fatalf("CreatePending: %v", err)
	}

	tf := newTestTwoFactorService(&noopMailer{})
	_, _, err := tf.VerifyOTP(ctx, rawPending, "000000", 24*time.Hour)
	if !errors.Is(err, ErrOTPInvalid) {
		t.Fatalf("expected ErrOTPInvalid for expired token, got %v", err)
	}
}

// @{"verifies": ["REQ-AUTH-016"]}
func TestVerifyOTP_SingleUse_SecondCallReturnsInvalid(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithEmail(ctx, t, "2fa_singleuse", "pass", "student", "su@example.com")

	repo := NewRepository(pool)
	user, _ := repo.GetUserByUsername(ctx, "2fa_singleuse")

	var capturedOTP string
	recMailer := &recordMailer{onSend: func(body string) { capturedOTP = extractOTPFromBody(body) }}
	tf := newTestTwoFactorService(recMailer)

	pending, _ := tf.IssuePendingTwoFactor(ctx, user)

	// First verify: must succeed.
	_, _, err := tf.VerifyOTP(ctx, pending.PendingToken, capturedOTP, 24*time.Hour)
	if err != nil {
		t.Fatalf("first VerifyOTP failed: %v", err)
	}

	// Second verify with the same token must fail (row deleted).
	_, _, err = tf.VerifyOTP(ctx, pending.PendingToken, capturedOTP, 24*time.Hour)
	if !errors.Is(err, ErrOTPInvalid) {
		t.Fatalf("expected ErrOTPInvalid on second use, got %v", err)
	}
}

// @{"verifies": ["REQ-AUTH-017", "REQ-AUTH-025"]}
func TestVerifyOTP_AttemptCap_FiveWrongLocksOut(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithEmail(ctx, t, "2fa_cap", "pass", "student", "cap@example.com")

	repo := NewRepository(pool)
	user, _ := repo.GetUserByUsername(ctx, "2fa_cap")

	tf := newTestTwoFactorService(&noopMailer{})
	pending, _ := tf.IssuePendingTwoFactor(ctx, user)

	// 4 wrong attempts: each must return a wrong-code error.
	for i := 0; i < 4; i++ {
		_, _, err := tf.VerifyOTP(ctx, pending.PendingToken, "000000", 24*time.Hour)
		var wrongErr *OTPWrongError
		if !errors.As(err, &wrongErr) {
			t.Fatalf("attempt %d: expected OTPWrongError, got %v", i+1, err)
		}
		want := otpAttemptCap - (i + 1)
		if wrongErr.AttemptsRemaining != want {
			t.Errorf("attempt %d: expected %d remaining, got %d", i+1, want, wrongErr.AttemptsRemaining)
		}
	}

	// 5th wrong attempt: must return ErrTooManyAttempts and delete the row.
	_, _, err := tf.VerifyOTP(ctx, pending.PendingToken, "000000", 24*time.Hour)
	if !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("5th wrong attempt: expected ErrTooManyAttempts, got %v", err)
	}

	// 6th attempt: row is gone, must return ErrOTPInvalid.
	_, _, err = tf.VerifyOTP(ctx, pending.PendingToken, "000000", 24*time.Hour)
	if !errors.Is(err, ErrOTPInvalid) {
		t.Fatalf("6th attempt: expected ErrOTPInvalid (no row), got %v", err)
	}
}

// @{"verifies": ["REQ-AUTH-026"]}
func TestResendOTP_ThrottleWithin60s(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithEmail(ctx, t, "2fa_resend_throttle", "pass", "student", "rt@example.com")

	repo := NewRepository(pool)
	user, _ := repo.GetUserByUsername(ctx, "2fa_resend_throttle")

	tf := newTestTwoFactorService(&noopMailer{})
	pending, _ := tf.IssuePendingTwoFactor(ctx, user)

	// First resend should succeed.
	_, _, err := tf.ResendOTP(ctx, pending.PendingToken, 24*time.Hour)
	if err != nil {
		t.Fatalf("first resend: %v", err)
	}

	// Second resend within 60 s must be throttled.
	_, _, err = tf.ResendOTP(ctx, pending.PendingToken, 24*time.Hour)
	var throttleErr *ResendThrottledError
	if !errors.As(err, &throttleErr) {
		t.Fatalf("second resend: expected ResendThrottledError, got %v", err)
	}
	if throttleErr.ResendAvailableAt.IsZero() {
		t.Error("ResendAvailableAt must not be zero")
	}
}

// @{"verifies": ["REQ-AUTH-026"]}
func TestResendOTP_DailyCap(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithEmail(ctx, t, "2fa_dailycap", "pass", "student", "dc@example.com")

	repo := NewRepository(pool)
	user, _ := repo.GetUserByUsername(ctx, "2fa_dailycap")

	tf := newTestTwoFactorService(&noopMailer{})
	pending, _ := tf.IssuePendingTwoFactor(ctx, user)

	// Directly set resend_count_24h = 10 on the pending row to simulate the cap.
	pendingHash := HashToken(pending.PendingToken)
	row, _ := repo.GetPendingByTokenHash(ctx, pendingHash)
	now := time.Now()
	if err := repo.UpdateOTPHash(ctx, row.ID, row.OTPHash, now.Add(-61*time.Second), resendDailyLimit, &now); err != nil {
		t.Fatalf("UpdateOTPHash: %v", err)
	}
	// Also set window start to now (not expired) so count is still active.
	if _, err := pool.Exec(ctx,
		`UPDATE pending_2fa SET resend_window_start = $1 WHERE id = $2`,
		now.Add(-1*time.Hour), row.ID,
	); err != nil {
		t.Fatalf("set resend_window_start: %v", err)
	}

	// Re-fetch to confirm the state.
	row2, _ := repo.GetPendingByTokenHash(ctx, pendingHash)
	if row2.ResendCount24h != resendDailyLimit {
		t.Fatalf("expected count %d, got %d", resendDailyLimit, row2.ResendCount24h)
	}

	_, _, err := tf.ResendOTP(ctx, pending.PendingToken, 24*time.Hour)
	if !errors.Is(err, ErrResendDailyCap) {
		t.Fatalf("expected ErrResendDailyCap, got %v", err)
	}
}

// @{"verifies": ["REQ-AUTH-019"]}
func TestResetUserTwoFactor_ClearsPendingRow(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	userID := createTestUserWithEmail(ctx, t, "2fa_reset_test", "pass", "student", "rst@example.com")

	repo := NewRepository(pool)
	user, _ := repo.GetUserByUsername(ctx, "2fa_reset_test")

	tf := newTestTwoFactorService(&noopMailer{})
	pending, _ := tf.IssuePendingTwoFactor(ctx, user)

	// Verify the pending row exists.
	pendingHash := HashToken(pending.PendingToken)
	if _, err := repo.GetPendingByTokenHash(ctx, pendingHash); err != nil {
		t.Fatalf("pending row should exist before reset: %v", err)
	}

	adminID := uuid.New()
	if err := tf.ResetUserTwoFactor(ctx, adminID, userID); err != nil {
		t.Fatalf("ResetUserTwoFactor: %v", err)
	}

	// Row must be gone.
	_, err := repo.GetPendingByTokenHash(ctx, pendingHash)
	if !errors.Is(err, ErrPendingNotFound) {
		t.Fatalf("expected row deleted after reset, got: %v", err)
	}
}

// @{"verifies": ["REQ-AUTH-020"]}
func TestLogin_BreakGlass_AdminBypassesOTP(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithEmail(ctx, t, "bg_admin", "pass", "admin", "bg@example.com")

	os.Setenv("VALORY_2FA_BREAK_GLASS", "active")
	defer os.Unsetenv("VALORY_2FA_BREAK_GLASS")

	repo := NewRepository(pool)
	auditR := audit.NewRepository(pool)
	mailer := &noopMailer{}
	tf := NewTwoFactorService(repo, auditR, pool, &staticConfig{vals: map[string]string{"two_factor_enabled": "true"}}, mailer)

	svc := NewService(repo, 15*time.Minute, 24*time.Hour)
	svc.SetTwoFactor(tf, &staticConfig{vals: map[string]string{"two_factor_enabled": "true"}})

	result, err := svc.Login(ctx, "bg_admin", "pass")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	// Break-glass: must return a full session, NOT a pending token.
	if result.Session == nil {
		t.Fatal("expected full session for break-glass admin")
	}
	if result.PendingTwoFactor != nil {
		t.Fatal("expected no pending 2FA for break-glass admin")
	}
	if result.RawToken == "" {
		t.Fatal("expected non-empty raw token")
	}
	// No OTP email must have been sent.
	if len(mailer.sent) != 0 {
		t.Errorf("expected no OTP emails sent for break-glass, got %d", len(mailer.sent))
	}
}

// @{"verifies": ["REQ-AUTH-020"]}
func TestLogin_BreakGlass_StudentStillRequires2FA(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithEmail(ctx, t, "bg_student", "pass", "student", "bgs@example.com")

	os.Setenv("VALORY_2FA_BREAK_GLASS", "active")
	defer os.Unsetenv("VALORY_2FA_BREAK_GLASS")

	repo := NewRepository(pool)
	auditR := audit.NewRepository(pool)
	mailer := &noopMailer{}
	tf := NewTwoFactorService(repo, auditR, pool, &staticConfig{vals: map[string]string{"two_factor_enabled": "true"}}, mailer)

	svc := NewService(repo, 15*time.Minute, 24*time.Hour)
	svc.SetTwoFactor(tf, &staticConfig{vals: map[string]string{"two_factor_enabled": "true"}})

	result, err := svc.Login(ctx, "bg_student", "pass")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	// Student must NOT bypass 2FA even when break-glass is set.
	if result.PendingTwoFactor == nil {
		t.Fatal("expected pending 2FA for student even when break-glass is set")
	}
	if result.Session != nil {
		t.Fatal("expected no full session for break-glass student")
	}
}

// @{"verifies": ["REQ-AUTH-015"]}
func TestLogin_TwoFactorOff_BehaviorUnchanged(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithEmail(ctx, t, "2fa_off_user", "pass", "student", "off@example.com")

	// Service with 2FA wired but config says disabled.
	repo := NewRepository(pool)
	auditR := audit.NewRepository(pool)
	tf := NewTwoFactorService(repo, auditR, pool,
		&staticConfig{vals: map[string]string{"two_factor_enabled": "false"}},
		&noopMailer{},
	)
	svc := NewService(repo, 15*time.Minute, 24*time.Hour)
	svc.SetTwoFactor(tf, &staticConfig{vals: map[string]string{"two_factor_enabled": "false"}})

	result, err := svc.Login(ctx, "2fa_off_user", "pass")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	// 2FA off: must return a full session immediately.
	if result.Session == nil {
		t.Fatal("expected full session when 2FA is disabled")
	}
	if result.PendingTwoFactor != nil {
		t.Fatal("expected no pending 2FA when disabled")
	}
}

// @{"verifies": ["REQ-AUTH-022"]}
func TestAuditPayload_ContainsNoOTPValue(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithEmail(ctx, t, "2fa_audit_check", "pass", "student", "ac@example.com")

	repo := NewRepository(pool)
	auditR := audit.NewRepository(pool)
	mailer := &noopMailer{}
	tf := NewTwoFactorService(repo, auditR, pool,
		&staticConfig{vals: map[string]string{"two_factor_enabled": "true"}},
		mailer,
	)

	user, _ := repo.GetUserByUsername(ctx, "2fa_audit_check")
	pending, _ := tf.IssuePendingTwoFactor(ctx, user)

	// Verify the OTP that was sent.
	var capturedBody string
	recMailer := &recordMailer{onSend: func(body string) { capturedBody = body }}
	tf2 := NewTwoFactorService(repo, auditR, pool,
		&staticConfig{vals: map[string]string{"two_factor_enabled": "true"}},
		recMailer,
	)
	createTestUserWithEmail(ctx, t, "2fa_audit_check2", "pass", "student", "ac2@example.com")
	user2, _ := repo.GetUserByUsername(ctx, "2fa_audit_check2")
	pending2, _ := tf2.IssuePendingTwoFactor(ctx, user2)
	otp := extractOTPFromBody(capturedBody)

	// Confirm the OTP hash is not returned in audit rows by checking the
	// audit chain for any payload containing the raw OTP value.
	// (This is defense-in-depth: the audit payload should only have user_id.)
	tf2.VerifyOTP(ctx, pending2.PendingToken, otp, 24*time.Hour) //nolint:errcheck

	// The check: ensure no audit row payload contains the literal OTP value.
	auditRows, _ := auditR.ListPaginated(ctx, 50, 0)
	for _, row := range auditRows {
		if strings.Contains(row.PayloadJSON, otp) {
			t.Errorf("audit payload contains raw OTP value %q: %s", otp, row.PayloadJSON)
		}
		_ = pending // use pending to suppress unused warning
	}
}

// @{"verifies": ["REQ-AUTH-015"]}
func TestPendingToken_RejectedBySessionRepository(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	createTestUserWithEmail(ctx, t, "2fa_pending_reject", "pass", "student", "pr@example.com")

	repo := NewRepository(pool)
	auditR := audit.NewRepository(pool)
	mailer := &noopMailer{}
	tf := NewTwoFactorService(repo, auditR, pool,
		&staticConfig{vals: map[string]string{"two_factor_enabled": "true"}},
		mailer,
	)

	user, _ := repo.GetUserByUsername(ctx, "2fa_pending_reject")
	pending, err := tf.IssuePendingTwoFactor(ctx, user)
	if err != nil {
		t.Fatalf("IssuePendingTwoFactor: %v", err)
	}

	// The pending token must NOT be found in the sessions table.
	pendingTokenHash := HashToken(pending.PendingToken)
	_, err = repo.GetSessionByTokenHash(ctx, pendingTokenHash)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("pending token must not be a valid session, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// recordMailer captures the body of each Send call for OTP extraction.
type recordMailer struct {
	onSend func(body string)
}

func (m *recordMailer) Send(_ context.Context, _, _, body string) error {
	if m.onSend != nil {
		m.onSend(body)
	}
	return nil
}
func (m *recordMailer) IsConfigured() bool { return true }

// extractOTPFromBody extracts the 6-digit OTP from an OTP email body.
// The template is: "Your Valory verification code is: {OTP}\r\n".
func extractOTPFromBody(body string) string {
	prefix := "Your Valory verification code is: "
	idx := strings.Index(body, prefix)
	if idx == -1 {
		return ""
	}
	start := idx + len(prefix)
	if start+6 > len(body) {
		return ""
	}
	return body[start : start+6]
}

// Ensure noopWriter satisfies io.Writer (compile-time check).
var _ io.Writer = noopWriter{}
