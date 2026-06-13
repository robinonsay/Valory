package setup

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/valory/valory/internal/audit"
	"github.com/valory/valory/internal/auth"
)

// setupAdvisoryLockKey serializes concurrent POST /api/v1/setup calls.
// The value 0x56414C53455455 encodes "VALSETUP" in ASCII hex.
// It must be unique among all advisory locks taken by this application.
// The audit chain lock (0x56414C41554449, "VALAUDI") is a different key.
//
// @{"req": ["REQ-SETUP-005"]}
const setupAdvisoryLockKey = int64(0x56414C53455455)

var (
	// ErrAlreadyConfigured is returned when a user already exists in the database.
	// Maps to HTTP 409 in the handler.
	ErrAlreadyConfigured = errors.New("setup: system already has at least one user")

	// ErrUsernameMissing is returned when the supplied username is blank or
	// whitespace-only after trimming. Maps to HTTP 400 in the handler.
	ErrUsernameMissing = errors.New("setup: username is blank or whitespace-only")

	// ErrPasswordTooShort is returned when the password is fewer than 8 bytes.
	// Maps to HTTP 400 in the handler.
	ErrPasswordTooShort = errors.New("setup: password must be at least 8 characters")
)

// AdminCreatedResult carries the non-sensitive fields returned after the first
// admin is created successfully. The password is deliberately excluded.
//
// @{"req": ["REQ-SETUP-003", "REQ-SETUP-004"]}
type AdminCreatedResult struct {
	Username string
	Role     string
}

// Service orchestrates first-run setup: validation, advisory locking, DB writes,
// and audit log entry creation.
//
// @{"req": ["REQ-SETUP-001", "REQ-SETUP-002", "REQ-SETUP-003", "REQ-SETUP-004", "REQ-SETUP-005", "REQ-SETUP-006", "REQ-SETUP-007", "REQ-SETUP-008", "REQ-SETUP-009", "REQ-SETUP-010"]}
type Service struct {
	repo      *Repository
	auditRepo *audit.Repository
	pool      *pgxpool.Pool
}

// @{"req": ["REQ-SETUP-001", "REQ-SETUP-003"]}
func NewService(pool *pgxpool.Pool, repo *Repository, auditRepo *audit.Repository) *Service {
	return &Service{
		repo:      repo,
		auditRepo: auditRepo,
		pool:      pool,
	}
}

// NeedsSetup returns true iff the users table contains zero rows.
// Runs a plain pool query — no transaction, no advisory lock. Read Committed
// isolation is sufficient here: a stale read that reports true when an admin
// was just created can only cause a harmless redirect to /setup, which the
// wizard's own 409 guard will immediately correct.
//
// @{"req": ["REQ-SETUP-001", "REQ-SETUP-002"]}
func (s *Service) NeedsSetup(ctx context.Context) (bool, error) {
	count, err := s.repo.CountUsers(ctx, nil)
	if err != nil {
		return false, err
	}
	return count == 0, nil
}

// CreateFirstAdmin validates input, acquires the PostgreSQL advisory lock,
// re-checks the user count inside the transaction (preventing races), inserts
// the admin row, appends the audit entry, and commits — all atomically.
//
// Step order (matches SDD-SETUP-001 §5.5 exactly):
//  1. Validate input (fast-fail before any DB contact)
//  2. Hash password via auth.HashPassword
//  3. BEGIN transaction
//  4. Acquire setupAdvisoryLockKey (blocks concurrent callers)
//  5. Re-check COUNT(*) — return ErrAlreadyConfigured if > 0
//  6. INSERT user row, RETURNING id
//  7. Append audit entry with AdminID = new admin's own ID
//  8. COMMIT
//
// @{"req": ["REQ-SETUP-003", "REQ-SETUP-004", "REQ-SETUP-005", "REQ-SETUP-006", "REQ-SETUP-007", "REQ-SETUP-008", "REQ-SETUP-009", "REQ-SETUP-010"]}
func (s *Service) CreateFirstAdmin(
	ctx context.Context,
	username string,
	email *string,
	password string,
) (AdminCreatedResult, error) {
	// Step 1: input validation — fail before touching the DB.
	username = strings.TrimSpace(username)
	if username == "" {
		return AdminCreatedResult{}, ErrUsernameMissing
	}
	// Byte length, not rune count, matching the frontend's TextEncoder behaviour
	// and the same rule applied in internal/user/service.go.
	if len(password) < 8 {
		return AdminCreatedResult{}, ErrPasswordTooShort
	}

	// Step 2: hash the password. Done outside the transaction so that the
	// CPU-intensive argon2id work does not extend the transaction hold time.
	// The raw password is discarded as soon as this returns.
	//
	// @{"req": ["REQ-SETUP-008"]}
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return AdminCreatedResult{}, err
	}

	// Step 3: begin the transaction.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AdminCreatedResult{}, err
	}
	// defer tx.Rollback is a no-op after a successful Commit (pgx ignores it).
	defer tx.Rollback(ctx) //nolint:errcheck

	// Step 4: acquire the transaction-scoped advisory lock. This serializes
	// concurrent POST /api/v1/setup requests — the second caller blocks here
	// until the first either commits or rolls back, at which point it proceeds
	// to step 5 and finds COUNT(*) == 1, returning ErrAlreadyConfigured.
	//
	// @{"req": ["REQ-SETUP-005"]}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, setupAdvisoryLockKey); err != nil {
		return AdminCreatedResult{}, err
	}

	// Step 5: re-check inside the transaction under the advisory lock.
	//
	// @{"req": ["REQ-SETUP-005"]}
	count, err := s.repo.CountUsers(ctx, tx)
	if err != nil {
		return AdminCreatedResult{}, err
	}
	if count > 0 {
		return AdminCreatedResult{}, ErrAlreadyConfigured
	}

	// Step 6: insert the admin row.
	//
	// @{"req": ["REQ-SETUP-003", "REQ-SETUP-004"]}
	adminID, err := s.repo.CreateAdmin(ctx, tx, username, email, passwordHash)
	if err != nil {
		return AdminCreatedResult{}, err
	}

	// Step 7: append the audit entry inside the same transaction.
	// AdminID is the newly created admin's own UUID — the only self-consistent
	// attribution when no prior admin exists (REQ-SETUP-010).
	// The payload contains only username + role; the password is never included
	// (REQ-SETUP-008, REQ-SETUP-009).
	//
	// @{"req": ["REQ-SETUP-009", "REQ-SETUP-010"]}
	if err := s.auditRepo.Append(ctx, tx, audit.Entry{
		AdminID:    adminID,
		Action:     "setup.initial_admin",
		TargetType: "user",
		TargetID:   &adminID,
		Payload:    map[string]any{"username": username, "role": "admin"},
	}); err != nil {
		return AdminCreatedResult{}, err
	}

	// Step 8: commit — both the user row and the audit entry land atomically.
	if err := tx.Commit(ctx); err != nil {
		return AdminCreatedResult{}, err
	}

	return AdminCreatedResult{Username: username, Role: "admin"}, nil
}
