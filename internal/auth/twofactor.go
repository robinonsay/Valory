package auth

// twofactor.go — Email-based two-factor authentication service logic.
//
// Design summary:
//   - Login yields either a full Session (2FA off or break-glass) or a
//     PendingTwoFactorResult (2FA on: raw pending token returned in body).
//   - The pending token is stored hashed (SHA-256 via HashToken) in
//     pending_2fa. It is NOT a session row, so the auth middleware
//     rejects it automatically.
//   - OTP is 6-digit numeric, hashed at rest (SHA-256), 10-minute TTL.
//   - Attempt cap: 5 wrong OTPs delete the pending row; login must restart.
//   - Resend throttle: 60 s / 10 per 24 h, enforced via advisory lock.
//   - Break-glass: VALORY_2FA_BREAK_GLASS env var (non-empty = active for
//     admins only). Password check always runs first.

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
	"time"

	"crypto/rand"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valory/valory/internal/audit"
	"github.com/valory/valory/internal/email"
)

const (
	otpTTL           = 10 * time.Minute
	otpAttemptCap    = 5
	resendThrottle   = 60 * time.Second
	resendDailyLimit = 10
)

// Sentinel errors surfaced by the 2FA service layer.
var (
	// ErrOTPInvalid is returned when the OTP does not match or the pending
	// token is expired/unknown. The handler maps all such cases to the same
	// opaque 401 body to prevent oracle attacks.
	ErrOTPInvalid = errors.New("auth: invalid or expired OTP")
	// ErrTooManyAttempts is returned when attempt_count reaches otpAttemptCap.
	ErrTooManyAttempts = errors.New("auth: too many OTP attempts")
	// ErrResendThrottled is returned when a resend is requested within 60 s.
	ErrResendThrottled = errors.New("auth: resend throttled")
	// ErrResendDailyCap is returned when the 10-per-24h resend cap is reached.
	ErrResendDailyCap = errors.New("auth: resend daily cap exceeded")
	// ErrNoEmailAddress is returned when a user has no email on file. Distinct
	// from "SMTP not configured" so the frontend can display a user-specific
	// error message (REQ-AUTH-023).
	ErrNoEmailAddress = errors.New("auth: user has no email address on file")
	// ErrOTPSendFailed is returned when the OTP email delivery fails (e.g. SMTP
	// unreachable). The pending row still exists; the client should retry via the
	// resend endpoint. The handler maps this to 503 so the client can distinguish
	// a transient delivery failure from an internal logic error.
	ErrOTPSendFailed = errors.New("auth: OTP email delivery failed")
)

// ResendThrottledError carries the resend-available-at timestamp so the
// handler can include it in the 429 response body.
//
// @{"req": ["REQ-AUTH-026"]}
type ResendThrottledError struct {
	ResendAvailableAt time.Time
}

func (e *ResendThrottledError) Error() string { return ErrResendThrottled.Error() }
func (e *ResendThrottledError) Is(target error) bool {
	return target == ErrResendThrottled
}

// OTPVerifyResult is returned by VerifyOTP on a wrong-code response so the
// handler can include the remaining attempt count in the 401 body.
//
// @{"req": ["REQ-AUTH-017"]}
type OTPVerifyResult struct {
	AttemptsRemaining int
}

// LoginResult is the discriminated return type of Service.Login. Exactly one
// of Session or PendingTwoFactor is non-nil.
//
// @{"req": ["REQ-AUTH-015"]}
type LoginResult struct {
	// Session is set when 2FA is off or the break-glass path is active.
	Session *Session
	// RawToken is the plaintext session token corresponding to Session.
	RawToken string
	// PendingTwoFactor is set when 2FA is on and the password was correct.
	PendingTwoFactor *PendingTwoFactorResult
}

// PendingTwoFactorResult carries the raw pending token returned to the client.
//
// @{"req": ["REQ-AUTH-015", "REQ-AUTH-016"]}
type PendingTwoFactorResult struct {
	PendingToken string // raw value sent in response body; never stored
	ExpiresAt    time.Time
}

// ConfigReader is a narrow interface to read system_config keys. Defined here
// (not in admin) to avoid a circular import: admin imports auth for
// UserIDFromContext; auth importing admin would create a cycle. This mirrors
// the email.ConfigLoader pattern.
//
// @{"req": ["REQ-AUTH-018"]}
type ConfigReader interface {
	GetString(key string) string
}

// TwoFactorService holds the dependencies for all 2FA operations. It is
// embedded in Service via composition (see service.go).
//
// @{"req": ["REQ-AUTH-015", "REQ-AUTH-016", "REQ-AUTH-017", "REQ-AUTH-019", "REQ-AUTH-020", "REQ-AUTH-026"]}
type TwoFactorService struct {
	repo      *Repository
	auditRepo *audit.Repository
	pool      *pgxpool.Pool
	config    ConfigReader
	mailer    email.Mailer
}

// NewTwoFactorService constructs a TwoFactorService.
//
// @{"req": ["REQ-AUTH-015"]}
func NewTwoFactorService(repo *Repository, auditRepo *audit.Repository, pool *pgxpool.Pool, config ConfigReader, mailer email.Mailer) *TwoFactorService {
	return &TwoFactorService{
		repo:      repo,
		auditRepo: auditRepo,
		pool:      pool,
		config:    config,
		mailer:    mailer,
	}
}

// GenerateOTP returns a cryptographically random 6-digit decimal string
// ("000000"–"999999") and its SHA-256 hash. The plain value is used only for
// email delivery and must never be stored or logged.
//
// @{"req": ["REQ-AUTH-016"]}
func GenerateOTP() (plain, hash string, err error) {
	// crypto/rand.Int generates a uniform random integer in [0, 1_000_000).
	// Format with %06d to preserve leading zeros (e.g. "042817").
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", "", fmt.Errorf("auth: generate OTP: %w", err)
	}
	plain = fmt.Sprintf("%06d", n.Int64())
	hash = HashToken(plain)
	return plain, hash, nil
}

// IssuePendingTwoFactor creates a pending_2fa row, generates and emails the
// OTP. Called from Service.Login when 2FA is globally enabled and the password
// check passed. On success only the PendingTwoFactorResult is returned; the
// plain OTP is discarded after the email send.
//
// @{"req": ["REQ-AUTH-015", "REQ-AUTH-016", "REQ-AUTH-022", "REQ-AUTH-023"]}
func (s *TwoFactorService) IssuePendingTwoFactor(ctx context.Context, user *User) (*PendingTwoFactorResult, error) {
	if user.Email == "" {
		// The user has no email address; we cannot deliver the OTP. Return a
		// distinct error so the handler can surface a user-specific message
		// (REQ-AUTH-023) rather than the generic "email not configured" 503.
		return nil, ErrNoEmailAddress
	}

	plainOTP, otpHash, err := GenerateOTP()
	if err != nil {
		return nil, err
	}

	rawPending, pendingHash, expiresAt, err := IssueToken(otpTTL)
	if err != nil {
		return nil, err
	}

	if _, err := s.repo.CreatePending(ctx, user.ID, pendingHash, otpHash, expiresAt); err != nil {
		return nil, err
	}

	// Send the OTP email. Failure here means the pending row was created but the
	// OTP was not delivered. We wrap the error as ErrOTPSendFailed so the handler
	// can map it to 503; the client should retry via the resend endpoint and the
	// pending row will expire automatically after otpTTL if not retried.
	subject := "Your Valory verification code"
	body := fmt.Sprintf(
		"Your Valory verification code is: %s\r\n\r\nThis code expires in 10 minutes and can only be used once.\r\n\r\nIf you did not attempt to sign in, contact your administrator.\r\n",
		plainOTP,
	)
	if sendErr := s.mailer.Send(ctx, user.Email, subject, body); sendErr != nil {
		return nil, fmt.Errorf("%w: %w", ErrOTPSendFailed, sendErr)
	}

	// Record the send for the daily cap table (best-effort; failure does not
	// abort the challenge issuance since the OTP is already delivered).
	_ = s.repo.RecordOTPSend(ctx, user.ID)

	// Audit: actor is the subject user's own ID (lead revision note).
	userUUID := uuid.UUID(user.ID)
	if auditErr := s.appendAudit(ctx, userUUID, "2fa.challenge_issued", "user", &userUUID, map[string]any{
		"user_id": userUUID.String(),
	}); auditErr != nil {
		log.Printf("auth: 2fa.challenge_issued audit failed: %v", auditErr)
	}

	return &PendingTwoFactorResult{
		PendingToken: rawPending,
		ExpiresAt:    expiresAt,
	}, nil
}

// VerifyOTP looks up the pending_2fa row by pending_token hash, checks the OTP,
// increments attempt_count on failure, deletes the row on success or at the
// attempt cap, and issues a full session on success.
//
// Uses a PostgreSQL advisory lock on the pending row's UUID to prevent
// concurrent OTP-guess races (TOCTOU bypass of the attempt cap).
//
// @{"req": ["REQ-AUTH-015", "REQ-AUTH-016", "REQ-AUTH-017", "REQ-AUTH-022", "REQ-AUTH-024", "REQ-AUTH-025"]}
func (s *TwoFactorService) VerifyOTP(ctx context.Context, pendingToken, otp string, sessionMaxDuration time.Duration) (rawToken string, session *Session, verifyErr error) {
	tokenHash := HashToken(pendingToken)

	// Advisory-lock scope: begin a transaction, lock on the pending row's ID
	// (derived from the token hash lookup), check expiry, compare OTP, update
	// state — all under the lock so concurrent callers for the same token
	// serialize correctly.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Read the pending row inside the transaction.
	var row PendingTwoFARow
	scanErr := tx.QueryRow(ctx,
		`SELECT id, user_id, pending_token_hash, otp_hash, attempt_count,
		        last_resend_at, resend_count_24h, resend_window_start, expires_at, created_at
		 FROM pending_2fa WHERE pending_token_hash = $1`,
		tokenHash,
	).Scan(
		&row.ID,
		&row.UserID,
		&row.PendingTokenHash,
		&row.OTPHash,
		&row.AttemptCount,
		&row.LastResendAt,
		&row.ResendCount24h,
		&row.ResendWindowStart,
		&row.ExpiresAt,
		&row.CreatedAt,
	)
	if scanErr != nil {
		// No row or DB error — both map to the same opaque error to prevent oracle
		// attacks that distinguish "unknown token" from "wrong OTP".
		return "", nil, ErrOTPInvalid
	}

	// Acquire advisory lock on this pending row's ID to prevent concurrent
	// verify calls from bypassing the attempt cap. Lock key derived from the
	// UUID halves (XOR) — same derivation as checkAndRecordEmailTestSend.
	lockKey := int64(binary.BigEndian.Uint64(row.ID[:8])) ^ int64(binary.BigEndian.Uint64(row.ID[8:]))
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey); err != nil {
		return "", nil, err
	}

	// Re-read inside the lock to get the authoritative state (double-checked
	// locking pattern: the pre-lock read above was a fast path that avoids
	// taking the lock when the token simply does not exist).
	scanErr = tx.QueryRow(ctx,
		`SELECT id, user_id, pending_token_hash, otp_hash, attempt_count,
		        last_resend_at, resend_count_24h, resend_window_start, expires_at, created_at
		 FROM pending_2fa WHERE pending_token_hash = $1`,
		tokenHash,
	).Scan(
		&row.ID,
		&row.UserID,
		&row.PendingTokenHash,
		&row.OTPHash,
		&row.AttemptCount,
		&row.LastResendAt,
		&row.ResendCount24h,
		&row.ResendWindowStart,
		&row.ExpiresAt,
		&row.CreatedAt,
	)
	if scanErr != nil {
		return "", nil, ErrOTPInvalid
	}

	// Check TTL — expired tokens are invalid regardless of OTP correctness.
	if time.Now().After(row.ExpiresAt) {
		// Clean up the expired row.
		_, _ = tx.Exec(ctx, `DELETE FROM pending_2fa WHERE id = $1`, row.ID)
		_ = tx.Commit(ctx) //nolint:errcheck
		return "", nil, ErrOTPInvalid
	}

	otpHash := HashToken(otp)
	if otpHash != row.OTPHash {
		// Wrong OTP: increment attempt count inside the transaction.
		var newCount int
		if err := tx.QueryRow(ctx,
			`UPDATE pending_2fa SET attempt_count = attempt_count + 1
			 WHERE id = $1 RETURNING attempt_count`,
			row.ID,
		).Scan(&newCount); err != nil {
			return "", nil, err
		}

		if newCount >= otpAttemptCap {
			// Attempt cap reached: delete the pending row so the user must restart
			// login. This is the primary online brute-force defense (REQ-AUTH-017).
			_, _ = tx.Exec(ctx, `DELETE FROM pending_2fa WHERE id = $1`, row.ID)
			_ = tx.Commit(ctx) //nolint:errcheck

			// Audit the lockout.
			userUUID := uuid.UUID(row.UserID)
			if auditErr := s.appendAudit(ctx, userUUID, "2fa.locked", "user", &userUUID, map[string]any{
				"user_id": userUUID.String(),
			}); auditErr != nil {
				log.Printf("auth: 2fa.locked audit failed: %v", auditErr)
			}

			return "", nil, ErrTooManyAttempts
		}

		_ = tx.Commit(ctx) //nolint:errcheck

		// Audit the failed attempt.
		userUUID := uuid.UUID(row.UserID)
		if auditErr := s.appendAudit(ctx, userUUID, "2fa.verify_failed", "user", &userUUID, map[string]any{
			"user_id": userUUID.String(),
			"attempt": newCount,
		}); auditErr != nil {
			log.Printf("auth: 2fa.verify_failed audit failed: %v", auditErr)
		}

		return "", nil, &OTPWrongError{AttemptsRemaining: otpAttemptCap - newCount}
	}

	// OTP correct: delete the pending row (single-use enforcement) inside the
	// transaction, then commit before issuing a session to avoid a partial state.
	if _, err := tx.Exec(ctx, `DELETE FROM pending_2fa WHERE id = $1`, row.ID); err != nil {
		return "", nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", nil, err
	}

	// Fetch the user to get must_change_password and role for the new session.
	user, err := s.repo.GetUserByID(ctx, row.UserID)
	if err != nil {
		return "", nil, err
	}

	rawToken, tokenHashStr, expiresAt, err := IssueToken(sessionMaxDuration)
	if err != nil {
		return "", nil, err
	}

	session, err = s.repo.CreateSession(ctx, user.ID, tokenHashStr, user.Role, expiresAt, user.MustChangePassword)
	if err != nil {
		return "", nil, err
	}

	// Audit success.
	userUUID := uuid.UUID(user.ID)
	if auditErr := s.appendAudit(ctx, userUUID, "2fa.verify_success", "user", &userUUID, map[string]any{
		"user_id": userUUID.String(),
	}); auditErr != nil {
		log.Printf("auth: 2fa.verify_success audit failed: %v", auditErr)
	}

	return rawToken, session, nil
}

// OTPWrongError is returned when the OTP is wrong but the attempt cap has not
// yet been reached. It carries the remaining attempt count for the response body.
//
// @{"req": ["REQ-AUTH-017"]}
type OTPWrongError struct {
	AttemptsRemaining int
}

func (e *OTPWrongError) Error() string { return ErrOTPInvalid.Error() }
func (e *OTPWrongError) Is(target error) bool {
	return target == ErrOTPInvalid
}

// ResendOTP generates a new OTP for the given pending token, updates the
// pending_2fa row, and sends the email. Rate-limited by the 60-second throttle
// and 10-per-24h daily cap.
//
// @{"req": ["REQ-AUTH-026", "REQ-AUTH-022", "REQ-AUTH-023"]}
func (s *TwoFactorService) ResendOTP(ctx context.Context, pendingToken string, sessionMaxDuration time.Duration) (expiresAt time.Time, resendAvailableAt *time.Time, retErr error) {
	tokenHash := HashToken(pendingToken)

	// Advisory lock on the pending row's UUID to prevent concurrent resend
	// races that could bypass the throttle check.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return time.Time{}, nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var row PendingTwoFARow
	scanErr := tx.QueryRow(ctx,
		`SELECT id, user_id, pending_token_hash, otp_hash, attempt_count,
		        last_resend_at, resend_count_24h, resend_window_start, expires_at, created_at
		 FROM pending_2fa WHERE pending_token_hash = $1`,
		tokenHash,
	).Scan(
		&row.ID,
		&row.UserID,
		&row.PendingTokenHash,
		&row.OTPHash,
		&row.AttemptCount,
		&row.LastResendAt,
		&row.ResendCount24h,
		&row.ResendWindowStart,
		&row.ExpiresAt,
		&row.CreatedAt,
	)
	if scanErr != nil {
		return time.Time{}, nil, ErrOTPInvalid
	}

	lockKey := int64(binary.BigEndian.Uint64(row.UserID[:8])) ^ int64(binary.BigEndian.Uint64(row.UserID[8:]))
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey); err != nil {
		return time.Time{}, nil, err
	}

	// Re-read under the lock for TOCTOU safety.
	scanErr = tx.QueryRow(ctx,
		`SELECT id, user_id, pending_token_hash, otp_hash, attempt_count,
		        last_resend_at, resend_count_24h, resend_window_start, expires_at, created_at
		 FROM pending_2fa WHERE pending_token_hash = $1`,
		tokenHash,
	).Scan(
		&row.ID,
		&row.UserID,
		&row.PendingTokenHash,
		&row.OTPHash,
		&row.AttemptCount,
		&row.LastResendAt,
		&row.ResendCount24h,
		&row.ResendWindowStart,
		&row.ExpiresAt,
		&row.CreatedAt,
	)
	if scanErr != nil {
		return time.Time{}, nil, ErrOTPInvalid
	}

	now := time.Now()

	// Check TTL.
	if now.After(row.ExpiresAt) {
		_, _ = tx.Exec(ctx, `DELETE FROM pending_2fa WHERE id = $1`, row.ID)
		_ = tx.Commit(ctx) //nolint:errcheck
		return time.Time{}, nil, ErrOTPInvalid
	}

	// 60-second throttle check.
	if row.LastResendAt != nil && now.Sub(*row.LastResendAt) < resendThrottle {
		_ = tx.Rollback(ctx) //nolint:errcheck
		availAt := row.LastResendAt.Add(resendThrottle)
		return time.Time{}, nil, &ResendThrottledError{ResendAvailableAt: availAt}
	}

	// 24-hour daily cap check. If the window start is more than 24h ago (or nil),
	// reset the counter before checking.
	count24h := row.ResendCount24h
	windowStart := row.ResendWindowStart
	if windowStart == nil || now.Sub(*windowStart) > 24*time.Hour {
		count24h = 0
		windowStart = &now
	}
	if count24h >= resendDailyLimit {
		_ = tx.Rollback(ctx) //nolint:errcheck
		return time.Time{}, nil, ErrResendDailyCap
	}
	count24h++

	// Generate new OTP.
	plainOTP, otpHash, err := GenerateOTP()
	if err != nil {
		return time.Time{}, nil, err
	}

	// Update the row with the new OTP hash and throttle state.
	if _, err := tx.Exec(ctx,
		`UPDATE pending_2fa
		 SET otp_hash = $1, last_resend_at = $2, resend_count_24h = $3, resend_window_start = $4
		 WHERE id = $5`,
		otpHash, now, count24h, windowStart, row.ID,
	); err != nil {
		return time.Time{}, nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return time.Time{}, nil, err
	}

	// Fetch user email for delivery.
	user, err := s.repo.GetUserByID(ctx, row.UserID)
	if err != nil {
		return time.Time{}, nil, err
	}
	if user.Email == "" {
		return time.Time{}, nil, ErrNoEmailAddress
	}

	subject := "Your Valory verification code"
	body := fmt.Sprintf(
		"Your Valory verification code is: %s\r\n\r\nThis code expires in 10 minutes and can only be used once.\r\n\r\nIf you did not attempt to sign in, contact your administrator.\r\n",
		plainOTP,
	)
	if sendErr := s.mailer.Send(ctx, user.Email, subject, body); sendErr != nil {
		return time.Time{}, nil, fmt.Errorf("auth: resend OTP email: %w", sendErr)
	}

	_ = s.repo.RecordOTPSend(ctx, row.UserID)

	userUUID := uuid.UUID(row.UserID)
	if auditErr := s.appendAudit(ctx, userUUID, "2fa.resend", "user", &userUUID, map[string]any{
		"user_id": userUUID.String(),
	}); auditErr != nil {
		log.Printf("auth: 2fa.resend audit failed: %v", auditErr)
	}

	return row.ExpiresAt, nil, nil
}

// ResetUserTwoFactor deletes any pending_2fa row for userID (admin action).
// It is idempotent: no error when no row existed.
//
// @{"req": ["REQ-AUTH-019", "REQ-AUTH-022"]}
func (s *TwoFactorService) ResetUserTwoFactor(ctx context.Context, adminID uuid.UUID, userID [16]byte) error {
	if err := s.repo.DeletePendingByUserID(ctx, userID); err != nil {
		return err
	}

	targetUUID := uuid.UUID(userID)
	if auditErr := s.appendAudit(ctx, adminID, "2fa.reset", "user", &targetUUID, map[string]any{
		"target_user_id": targetUUID.String(),
	}); auditErr != nil {
		log.Printf("auth: 2fa.reset audit failed: %v", auditErr)
	}

	return nil
}

// appendAudit writes an audit entry in its own transaction (best-effort: errors
// are logged but never surface to the caller — the primary operation has already
// succeeded at the call site).
//
// @{"req": ["REQ-AUTH-022"]}
func (s *TwoFactorService) appendAudit(ctx context.Context, adminID uuid.UUID, action, targetType string, targetID *uuid.UUID, payload map[string]any) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	entry := audit.Entry{
		AdminID:    adminID,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Payload:    payload,
	}
	if err := s.auditRepo.Append(ctx, tx, entry); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// isTwoFactorEnabled reads the two_factor_enabled config key. Returns false
// when the key is absent or not "true".
//
// @{"req": ["REQ-AUTH-015", "REQ-AUTH-018"]}
func isTwoFactorEnabled(config ConfigReader) bool {
	return config != nil && config.GetString("two_factor_enabled") == "true"
}

// isBreakGlassActive returns true when the VALORY_2FA_BREAK_GLASS environment
// variable is non-empty. The break-glass bypass is intentionally presence-based
// (any non-empty value activates it) rather than requiring a specific secret
// value. Rationale: an operator with shell access to set the env var already
// has root/container access; requiring a specific value would mean storing that
// value somewhere discoverable. The audit trail provides accountability.
//
// @{"req": ["REQ-AUTH-020"]}
func isBreakGlassActive() bool {
	return os.Getenv("VALORY_2FA_BREAK_GLASS") != ""
}
