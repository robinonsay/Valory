package user

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/valory/valory/internal/audit"
	"github.com/valory/valory/internal/auth"
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

// @{"req": ["REQ-USER-001", "REQ-USER-002", "REQ-USER-003", "REQ-USER-005", "REQ-USER-006", "REQ-USER-007", "REQ-SECURITY-005"]}
type Service struct {
	repo            *Repository
	auditRepo       *audit.Repository
	emailTransport  EmailTransport
	tokenTTL        time.Duration
	agentTerminator AgentTerminator
	pool            *pgxpool.Pool
}

// @{"req": ["REQ-USER-001", "REQ-USER-002", "REQ-USER-003", "REQ-USER-005", "REQ-USER-006", "REQ-USER-007", "REQ-SECURITY-005"]}
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
	return s.emailTransport.SendPasswordReset(ctx, *user.Email, rawToken)
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

// RecordConsent upserts the student's consent record. No audit entry is required
// for consent per the SDD.
//
// @{"req": ["REQ-SECURITY-005"]}
func (s *Service) RecordConsent(ctx context.Context, studentID uuid.UUID, version string) error {
	return s.repo.UpsertConsent(ctx, studentID, version)
}
