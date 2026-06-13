package user

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/valory/valory/internal/audit"
	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/email"
	"github.com/valory/valory/internal/security"
)

// ErrDeletionBlocked is returned when AgentTerminator cannot cleanly drain
// in-flight AI operations before a student account is deleted.
var ErrDeletionBlocked = errors.New("deletion blocked: in-flight agent operations could not be terminated")

// AgentTerminator is called before deleting a student to drain in-flight AI
// operations. Sprint 2 wires a no-op that returns immediately.
//
// @{"req": ["REQ-USER-007"]}
type AgentTerminator interface {
	TerminateStudentOperations(ctx context.Context, studentID uuid.UUID) error
}

// @{"req": ["REQ-USER-001", "REQ-USER-002", "REQ-USER-003", "REQ-USER-005", "REQ-USER-006", "REQ-USER-007", "REQ-SECURITY-005", "REQ-USER-008", "REQ-USER-009", "REQ-USER-010", "REQ-USER-012", "REQ-USER-015", "REQ-USER-016"]}
type Service struct {
	repo            *Repository
	auditRepo       *audit.Repository
	emailTransport  EmailTransport
	mailer          email.Mailer
	tokenTTL        time.Duration
	agentTerminator AgentTerminator
	pool            *pgxpool.Pool
}

// @{"req": ["REQ-USER-001", "REQ-USER-002", "REQ-USER-003", "REQ-USER-005", "REQ-USER-006", "REQ-USER-007", "REQ-SECURITY-005", "REQ-USER-008", "REQ-USER-009", "REQ-USER-010", "REQ-USER-012", "REQ-USER-015", "REQ-USER-016"]}
func NewService(
	pool *pgxpool.Pool,
	repo *Repository,
	auditRepo *audit.Repository,
	emailTransport EmailTransport,
	tokenTTL time.Duration,
	agentTerminator AgentTerminator,
) *Service {
	return &Service{
		repo:            repo,
		auditRepo:       auditRepo,
		emailTransport:  emailTransport,
		tokenTTL:        tokenTTL,
		agentTerminator: agentTerminator,
		pool:            pool,
	}
}

// SetMailer injects the email.Mailer used for welcome emails. Called from
// main.go after both the user service and the mailer are constructed, avoiding
// a circular dependency at construction time.
//
// @{"req": ["REQ-USER-010", "REQ-USER-012"]}
func (s *Service) SetMailer(m email.Mailer) {
	s.mailer = m
}

// isDuplicate returns true when err is a PostgreSQL unique-violation (23505).
//
// @{"req": ["REQ-USER-002"]}
func isDuplicate(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// CreateUser hashes the password, inserts the user row and audit entry in a
// single transaction, and returns the created UserRow.
//
// @{"req": ["REQ-USER-001", "REQ-USER-002"]}
func (s *Service) CreateUser(ctx context.Context, adminID uuid.UUID, username string, email *string, role, password string) (UserRow, error) {
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return UserRow{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return UserRow{}, err
	}
	defer tx.Rollback(ctx)

	var user UserRow
	err = tx.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash, role)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, username, email, password_hash, role, is_active, created_at, updated_at`,
		username, email, passwordHash, role).
		Scan(
			&user.ID,
			&user.Username,
			&user.Email,
			&user.PasswordHash,
			&user.Role,
			&user.IsActive,
			&user.CreatedAt,
			&user.UpdatedAt,
		)
	if err != nil {
		if isDuplicate(err) {
			return UserRow{}, ErrDuplicateUsername
		}
		return UserRow{}, err
	}

	targetID := user.ID
	if err := s.auditRepo.Append(ctx, tx, audit.Entry{
		AdminID:    adminID,
		Action:     "user.create",
		TargetType: "user",
		TargetID:   &targetID,
		Payload:    map[string]any{"username": username, "role": role},
	}); err != nil {
		return UserRow{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return UserRow{}, err
	}
	return user, nil
}

// ModifyUser updates the specified fields then appends an audit entry. The two
// operations are best-effort and not in the same transaction per the SDD.
//
// @{"req": ["REQ-USER-001", "REQ-USER-002", "REQ-USER-003"]}
func (s *Service) ModifyUser(ctx context.Context, adminID, id uuid.UUID, fields UpdateFields) (UserRow, error) {
	user, err := s.repo.UpdateUser(ctx, id, fields)
	if err != nil {
		return UserRow{}, err
	}

	payload := map[string]any{}
	if fields.Username != nil {
		payload["username"] = *fields.Username
	}
	if fields.Email != nil {
		payload["email"] = *fields.Email
	}
	if fields.PasswordHash != nil {
		// audit.PrepareEntry redacts "password_hash" automatically.
		payload["password_hash"] = *fields.PasswordHash
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return user, err
	}
	defer tx.Rollback(ctx)

	targetID := id
	if auditErr := s.auditRepo.Append(ctx, tx, audit.Entry{
		AdminID:    adminID,
		Action:     "user.modify",
		TargetType: "user",
		TargetID:   &targetID,
		Payload:    payload,
	}); auditErr != nil {
		return user, auditErr
	}

	if err := tx.Commit(ctx); err != nil {
		return user, err
	}
	return user, nil
}

// DeactivateUser marks the account inactive (which also deletes all sessions)
// and records an audit entry in a separate transaction.
//
// @{"req": ["REQ-USER-001", "REQ-USER-002", "REQ-USER-003"]}
func (s *Service) DeactivateUser(ctx context.Context, adminID, id uuid.UUID) error {
	if err := s.repo.SetActive(ctx, id, false); err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	targetID := id
	if auditErr := s.auditRepo.Append(ctx, tx, audit.Entry{
		AdminID:    adminID,
		Action:     "user.deactivate",
		TargetType: "user",
		TargetID:   &targetID,
		Payload:    map[string]any{},
	}); auditErr != nil {
		return auditErr
	}

	return tx.Commit(ctx)
}

// ActivateUser marks the account active and records an audit entry.
// Reactivation is the inverse of REQ-USER-003 (deactivation lifecycle management).
//
// @{"req": ["REQ-USER-003"]}
func (s *Service) ActivateUser(ctx context.Context, adminID, id uuid.UUID) error {
	if err := s.repo.SetActive(ctx, id, true); err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	targetID := id
	if auditErr := s.auditRepo.Append(ctx, tx, audit.Entry{
		AdminID:    adminID,
		Action:     "user.activate",
		TargetType: "user",
		TargetID:   &targetID,
		Payload:    map[string]any{},
	}); auditErr != nil {
		return auditErr
	}

	return tx.Commit(ctx)
}

// DeleteStudent terminates in-flight agent work, deletes the student row, and
// records an audit entry. If the AgentTerminator returns any error the deletion
// is aborted and ErrDeletionBlocked is returned.
//
// @{"req": ["REQ-USER-007"]}
func (s *Service) DeleteStudent(ctx context.Context, adminID, id uuid.UUID) error {
	if err := s.agentTerminator.TerminateStudentOperations(ctx, id); err != nil {
		return ErrDeletionBlocked
	}

	if err := s.repo.DeleteStudent(ctx, id); err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	targetID := id
	if auditErr := s.auditRepo.Append(ctx, tx, audit.Entry{
		AdminID:    adminID,
		Action:     "user.delete",
		TargetType: "user",
		TargetID:   &targetID,
		Payload:    map[string]any{},
	}); auditErr != nil {
		return auditErr
	}

	return tx.Commit(ctx)
}

// RequestPasswordReset looks up the user, enforces a per-user rate limit, stores
// a hashed reset token, and sends the raw token by email. An unknown username
// returns nil to prevent account enumeration.
//
// @{"req": ["REQ-USER-005", "REQ-USER-006"]}
func (s *Service) RequestPasswordReset(ctx context.Context, username string) error {
	user, err := s.repo.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// Return nil so that the response time and status are identical for
			// existing and non-existing usernames, preventing enumeration.
			return nil
		}
		return err
	}

	if err := security.CheckAndRecordPasswordReset(ctx, s.pool, [16]byte(user.ID)); err != nil {
		return err
	}

	rawToken, tokenHash, expiresAt, err := auth.IssueToken(s.tokenTTL)
	if err != nil {
		return err
	}

	if err := s.repo.CreatePasswordResetToken(ctx, user.ID, tokenHash, expiresAt); err != nil {
		return err
	}

	if user.Email == nil {
		return nil
	}
	// Send errors are not returned to the caller. The token is already stored, so
	// the reset can be retried. Surfacing transport failures would let callers
	// distinguish "account exists with email" from "account exists without email"
	// or other states, breaking anti-enumeration (SDD-019 §8.1).
	if err := s.emailTransport.SendPasswordReset(ctx, *user.Email, rawToken); err != nil {
		// Log without the recipient address to avoid leaking PII to the log sink.
		log.Printf("user: password reset email send failed: %v", err)
	}
	return nil
}

// ConfirmPasswordReset validates the raw token, then atomically marks it used,
// updates the password, and deletes all active sessions in one transaction.
// All three writes are committed together so no partial state is possible.
//
// @{"req": ["REQ-USER-005", "REQ-USER-006"]}
func (s *Service) ConfirmPasswordReset(ctx context.Context, rawToken, newPassword string) error {
	tokenHash := auth.HashToken(rawToken)

	token, err := s.repo.GetValidResetToken(ctx, tokenHash)
	if err != nil {
		return err
	}

	newHash, err := auth.HashPassword(newPassword)
	if err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE password_reset_tokens SET used_at = NOW() WHERE id = $1`,
		token.ID); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx,
		`UPDATE users SET password_hash = $1, updated_at = NOW() WHERE id = $2`,
		newHash, token.UserID); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM sessions WHERE user_id = $1`,
		token.UserID); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// ListUsers returns up to limit users, optionally filtered by role.
// It is a thin delegation to the repository layer with no additional business
// rules beyond input normalisation (handled in the repository).
//
// @{"req": ["REQ-USER-001", "REQ-USER-002"]}
func (s *Service) ListUsers(ctx context.Context, role string, limit int) ([]UserRow, error) {
	return s.repo.ListUsers(ctx, role, limit)
}

// GetUserByID retrieves a user by their ID.
//
// @{"req": ["REQ-USER-001"]}
func (s *Service) GetUserByID(ctx context.Context, id uuid.UUID) (UserRow, error) {
	return s.repo.GetUserByID(ctx, id)
}

// RecordConsent upserts the student's consent record. No audit entry is required
// for consent per the SDD.
//
// @{"req": ["REQ-SECURITY-005"]}
func (s *Service) RecordConsent(ctx context.Context, studentID uuid.UUID, version string) error {
	return s.repo.UpsertConsent(ctx, studentID, version)
}

// tempPasswordAlphabet is the set of characters used when generating a
// system-issued temporary password. Characters that look alike (0/O, 1/l/I)
// are omitted to reduce transcription errors in case the admin reads the
// password aloud or the user has to type it manually.
const tempPasswordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789"

// tempPasswordLen is the length of every system-generated temporary password.
const tempPasswordLen = 16

// generateTempPassword returns a cryptographically random 16-character string
// drawn from tempPasswordAlphabet. The result is suitable for use as a
// one-time credential: it has ~93 bits of entropy (log2(56^16)).
//
// @{"req": ["REQ-USER-008", "REQ-SYS-063"]}
func generateTempPassword() (string, error) {
	alphabetLen := big.NewInt(int64(len(tempPasswordAlphabet)))
	buf := make([]byte, tempPasswordLen)
	for i := range buf {
		n, err := rand.Int(rand.Reader, alphabetLen)
		if err != nil {
			return "", fmt.Errorf("user: generate temp password: %w", err)
		}
		buf[i] = tempPasswordAlphabet[n.Int64()]
	}
	return string(buf), nil
}

// WelcomeEmailResult carries the outcome of a welcome email attempt.
//
// @{"req": ["REQ-USER-010", "REQ-USER-011"]}
type WelcomeEmailResult struct {
	// EmailSent is true when the mailer accepted the message.
	EmailSent bool
	// TempPassword is the cleartext temporary password. It is non-empty only
	// when EmailSent is false, so the admin can display it once in the UI
	// (REQ-FEADMIN-612/613). When EmailSent is true the plaintext credential
	// must never leave the server.
	TempPassword string
}

// sendWelcomeEmail attempts to deliver the welcome email. It is fail-soft:
// errors from the mailer are logged but never returned to the caller; the
// WelcomeEmailResult indicates whether the send succeeded.
//
// The email body contains the username, the cleartext temporary password, and
// a relative instruction to navigate to the login URL. The application does not
// know its own public URL (no base-URL config exists), so a relative instruction
// avoids inventing configuration. Operators can set the URL in their welcome
// email body by customising this string in a future admin template feature.
//
// @{"req": ["REQ-USER-010", "REQ-USER-011"]}
func (s *Service) sendWelcomeEmail(ctx context.Context, toAddress, username, tempPassword string) WelcomeEmailResult {
	if s.mailer == nil || !s.mailer.IsConfigured() {
		return WelcomeEmailResult{EmailSent: false, TempPassword: tempPassword}
	}

	subject := "Your Valory account credentials"
	body := fmt.Sprintf(
		"Welcome to Valory!\r\n\r\n"+
			"Username: %s\r\n"+
			"Temporary password: %s\r\n\r\n"+
			"Please log in and change your password immediately. "+
			"You will be prompted to set a new password on your first login.\r\n",
		username, tempPassword,
	)

	if err := s.mailer.Send(ctx, toAddress, subject, body); err != nil {
		log.Printf("user: welcome email send failed: %v", err)
		return WelcomeEmailResult{EmailSent: false, TempPassword: tempPassword}
	}
	return WelcomeEmailResult{EmailSent: true}
}

// CreateUserWithTempPassword generates a temporary password, inserts the user
// with must_change_password=true, and attempts to send a welcome email.
// Account creation succeeds regardless of email delivery (REQ-EMAIL-007 fail-soft
// posture). The WelcomeEmailResult in the return value tells the caller whether
// the email was sent and, when it was not, provides the cleartext temp password
// for one-time admin display.
//
// @{"req": ["REQ-USER-008", "REQ-USER-009", "REQ-USER-010", "REQ-USER-011", "REQ-USER-017"]}
func (s *Service) CreateUserWithTempPassword(ctx context.Context, adminID uuid.UUID, username string, emailAddr *string, role string) (UserRow, WelcomeEmailResult, error) {
	tempPwd, err := generateTempPassword()
	if err != nil {
		return UserRow{}, WelcomeEmailResult{}, err
	}

	passwordHash, err := auth.HashPassword(tempPwd)
	if err != nil {
		return UserRow{}, WelcomeEmailResult{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return UserRow{}, WelcomeEmailResult{}, err
	}
	defer tx.Rollback(ctx)

	var user UserRow
	err = tx.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash, role, must_change_password)
		 VALUES ($1, $2, $3, $4, true)
		 RETURNING id, username, email, password_hash, role, is_active, must_change_password, created_at, updated_at`,
		username, emailAddr, passwordHash, role).
		Scan(
			&user.ID,
			&user.Username,
			&user.Email,
			&user.PasswordHash,
			&user.Role,
			&user.IsActive,
			&user.MustChangePassword,
			&user.CreatedAt,
			&user.UpdatedAt,
		)
	if err != nil {
		if isDuplicate(err) {
			return UserRow{}, WelcomeEmailResult{}, ErrDuplicateUsername
		}
		return UserRow{}, WelcomeEmailResult{}, err
	}

	targetID := user.ID
	if err := s.auditRepo.Append(ctx, tx, audit.Entry{
		AdminID:    adminID,
		Action:     "user.create_temp",
		TargetType: "user",
		TargetID:   &targetID,
		// Payload contains name-only fields — the password is never included
		// (REQ-USER-017: audit payload must be credential-free).
		Payload: map[string]any{"username": username, "role": role},
	}); err != nil {
		return UserRow{}, WelcomeEmailResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return UserRow{}, WelcomeEmailResult{}, err
	}

	var result WelcomeEmailResult
	if emailAddr != nil && *emailAddr != "" {
		result = s.sendWelcomeEmail(ctx, *emailAddr, username, tempPwd)
	} else {
		// No email address — surface the temp password to the admin.
		result = WelcomeEmailResult{EmailSent: false, TempPassword: tempPwd}
	}

	return user, result, nil
}

// ResendWelcome regenerates a temporary password for an existing account,
// overwrites the old hash (invalidating the previous credential per REQ-USER-013),
// sets must_change_password=true, and attempts to send a welcome email.
// Fails with ErrUserNotFound when the target user does not exist.
//
// @{"req": ["REQ-USER-012", "REQ-USER-013", "REQ-USER-018"]}
func (s *Service) ResendWelcome(ctx context.Context, adminID, targetID uuid.UUID) (WelcomeEmailResult, error) {
	user, err := s.repo.GetUserByID(ctx, targetID)
	if err != nil {
		return WelcomeEmailResult{}, err
	}

	tempPwd, err := generateTempPassword()
	if err != nil {
		return WelcomeEmailResult{}, err
	}

	newHash, err := auth.HashPassword(tempPwd)
	if err != nil {
		return WelcomeEmailResult{}, err
	}

	// Overwrite the old hash and re-set the flag atomically; the old password
	// is no longer valid the moment this UPDATE commits (REQ-USER-013).
	if err := s.repo.SetTempPassword(ctx, targetID, newHash); err != nil {
		return WelcomeEmailResult{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WelcomeEmailResult{}, err
	}
	defer tx.Rollback(ctx)

	tid := targetID
	if err := s.auditRepo.Append(ctx, tx, audit.Entry{
		AdminID:    adminID,
		Action:     "user.resend_welcome",
		TargetType: "user",
		TargetID:   &tid,
		// Credential-free payload (REQ-USER-018).
		Payload: map[string]any{"username": user.Username},
	}); err != nil {
		return WelcomeEmailResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return WelcomeEmailResult{}, err
	}

	var result WelcomeEmailResult
	if user.Email != nil && *user.Email != "" {
		result = s.sendWelcomeEmail(ctx, *user.Email, user.Username, tempPwd)
	} else {
		result = WelcomeEmailResult{EmailSent: false, TempPassword: tempPwd}
	}

	return result, nil
}

// ErrWrongPassword is returned by ChangePassword when current_password does not
// match the stored hash.
var ErrWrongPassword = errors.New("user: wrong current password")

// ErrPasswordTooShort is returned when new_password is fewer than 8 characters.
var ErrPasswordTooShort = errors.New("user: password must be at least 8 characters")

// ChangePassword verifies the caller's current password, enforces the minimum
// length policy on the new password, then atomically updates the hash, clears
// must_change_password, and deletes all other sessions. The current session
// (identified by currentTokenHash) is preserved so the caller stays logged in.
//
// @{"req": ["REQ-USER-014", "REQ-USER-015", "REQ-USER-016", "REQ-USER-019"]}
func (s *Service) ChangePassword(ctx context.Context, userID uuid.UUID, currentPassword, newPassword, currentTokenHash string) error {
	user, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}

	ok, err := auth.CheckPassword(currentPassword, user.PasswordHash)
	if err != nil {
		return err
	}
	if !ok {
		return ErrWrongPassword
	}

	// Mirror the frontend's minimum-length check (PasswordResetView enforces
	// client-side validation but has no server-side policy; we enforce 8 chars
	// here for consistency across all password-change paths).
	if len(newPassword) < 8 {
		return ErrPasswordTooShort
	}

	newHash, err := auth.HashPassword(newPassword)
	if err != nil {
		return err
	}

	// ChangePassword atomically updates hash, clears flag, removes other sessions.
	if err := s.repo.ChangePassword(ctx, userID, newHash, currentTokenHash); err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	uid := userID
	if err := s.auditRepo.Append(ctx, tx, audit.Entry{
		AdminID:    userID, // actor is the user themselves
		Action:     "user.password_change",
		TargetType: "user",
		TargetID:   &uid,
		// Credential-free payload (REQ-USER-019).
		Payload: map[string]any{"username": user.Username},
	}); err != nil {
		return err
	}

	return tx.Commit(ctx)
}
