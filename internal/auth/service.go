package auth

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	ErrAccountDisabled    = errors.New("auth: account disabled")
	ErrAccountLocked      = errors.New("auth: account locked")
)

// AccountLockedError carries the lockout expiry time for internal use.
// Callers should treat it the same as ErrInvalidCredentials to avoid
// confirming whether a username exists in the system.
//
// @{"req": ["REQ-AUTH-006"]}
type AccountLockedError struct {
	Until time.Time
}

// @{"req": ["REQ-AUTH-006"]}
func (e *AccountLockedError) Error() string { return ErrAccountLocked.Error() }

// @{"req": ["REQ-AUTH-006"]}
func (e *AccountLockedError) Is(target error) bool { return target == ErrAccountLocked }

// dummyHash is an argon2id hash computed once at startup. It is used to
// equalize response timing for unknown or disabled accounts: we always call
// CheckPassword before returning, so all code paths for a given input take
// approximately the same time regardless of account state.
var dummyHash string

func init() {
	var err error
	dummyHash, err = HashPassword("_timing_sentinel_")
	if err != nil {
		panic("auth: failed to initialize dummy hash: " + err.Error())
	}
}

// @{"req": ["REQ-AUTH-001", "REQ-AUTH-002", "REQ-AUTH-003", "REQ-AUTH-005", "REQ-AUTH-006", "REQ-AUTH-015", "REQ-AUTH-020"]}
type Service struct {
	repo               *Repository
	lockoutDuration    time.Duration
	sessionMaxDuration time.Duration
	// twoFactor is non-nil when 2FA is wired. It is injected via SetTwoFactor
	// after construction so that Service can be constructed without audit/pool
	// dependencies when those are not needed (e.g. unit tests that only test
	// the password-check path).
	twoFactor *TwoFactorService
	// config provides the two_factor_enabled flag. Defined as a ConfigReader
	// interface to avoid a circular import with the admin package.
	config ConfigReader
}

// @{"req": ["REQ-AUTH-001", "REQ-AUTH-002", "REQ-AUTH-003", "REQ-AUTH-005", "REQ-AUTH-006"]}
func NewService(repo *Repository, lockoutDuration, sessionMaxDuration time.Duration) *Service {
	return &Service{
		repo:               repo,
		lockoutDuration:    lockoutDuration,
		sessionMaxDuration: sessionMaxDuration,
	}
}

// SetTwoFactor injects the TwoFactorService and ConfigReader. Called from
// main.go after all dependencies are constructed. Both arguments are required
// for 2FA to be active; omitting this call leaves 2FA disabled regardless of
// the config key.
//
// @{"req": ["REQ-AUTH-015", "REQ-AUTH-018"]}
func (s *Service) SetTwoFactor(tf *TwoFactorService, config ConfigReader) {
	s.twoFactor = tf
	s.config = config
}

// Login authenticates a user by username and password and returns a LoginResult.
// When 2FA is enabled globally and the password is correct, it returns a
// PendingTwoFactorResult (phase one). When 2FA is disabled or the break-glass
// env var is set for an admin, it returns a full Session (phase one = complete).
//
// @{"req": ["REQ-AUTH-001", "REQ-AUTH-002", "REQ-AUTH-003", "REQ-AUTH-006", "REQ-AUTH-015", "REQ-AUTH-020"]}
func (s *Service) Login(ctx context.Context, username, password string) (*LoginResult, error) {
	if username == "" || password == "" {
		return nil, ErrInvalidCredentials
	}

	user, err := s.repo.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// Run a dummy argon2 verification so that the response time for an
			// unknown username matches the active-user code path, preventing
			// username enumeration via response timing.
			CheckPassword(password, dummyHash) //nolint:errcheck
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}

	// Always run the argon2 password check before inspecting account state so
	// that disabled, locked, and wrong-password code paths all take approximately
	// the same time. This prevents response-timing attacks from distinguishing
	// whether a username corresponds to an active, disabled, or locked account.
	ok, err := CheckPassword(password, user.PasswordHash)
	if err != nil {
		return nil, err
	}

	if !user.IsActive {
		return nil, ErrAccountDisabled
	}

	if user.LockedUntil != nil && user.LockedUntil.After(time.Now()) {
		// Do not increment the counter during an active lockout period.
		return nil, &AccountLockedError{Until: *user.LockedUntil}
	}

	if !ok {
		newCount := user.FailedLoginCount + 1
		var lockedUntil *time.Time
		if newCount >= 5 {
			t := time.Now().Add(s.lockoutDuration)
			lockedUntil = &t
		} else {
			lockedUntil = user.LockedUntil
		}
		if setErr := s.repo.SetLockoutState(ctx, user.ID, newCount, lockedUntil); setErr != nil {
			return nil, setErr
		}
		if recErr := s.repo.RecordLoginAttempt(ctx, user.ID, false); recErr != nil {
			return nil, recErr
		}
		return nil, ErrInvalidCredentials
	}

	if err := s.repo.ResetLoginState(ctx, user.ID); err != nil {
		return nil, err
	}
	if err := s.repo.RecordLoginAttempt(ctx, user.ID, true); err != nil {
		return nil, err
	}

	// 2FA branch: only entered when the TwoFactorService is wired, 2FA is
	// enabled globally, and this is not a break-glass admin session.
	if s.twoFactor != nil && isTwoFactorEnabled(s.config) {
		// Break-glass: admins bypass 2FA when VALORY_2FA_BREAK_GLASS is set.
		// The password check above always ran first — break-glass never bypasses
		// the password (REQ-AUTH-020).
		if user.Role == "admin" && isBreakGlassActive() {
			log.Printf("SECURITY: VALORY_2FA_BREAK_GLASS used for admin %s", user.Username)
			// Append audit event for accountability. Failure is best-effort.
			adminUUID := uuid.UUID(user.ID)
			if auditErr := s.twoFactor.appendAudit(ctx, adminUUID, "2fa.break_glass_used", "user", &adminUUID, map[string]any{
				"username": user.Username,
			}); auditErr != nil {
				log.Printf("auth: 2fa.break_glass_used audit failed: %v", auditErr)
			}
			// Fall through to normal session issuance below.
		} else {
			// Normal 2FA path: issue pending state, email the OTP.
			pending, err := s.twoFactor.IssuePendingTwoFactor(ctx, user)
			if err != nil {
				return nil, err
			}
			return &LoginResult{PendingTwoFactor: pending}, nil
		}
	}

	rawToken, tokenHash, expiresAt, err := IssueToken(s.sessionMaxDuration)
	if err != nil {
		return nil, err
	}

	session, err := s.repo.CreateSession(ctx, user.ID, tokenHash, user.Role, expiresAt, user.MustChangePassword)
	if err != nil {
		return nil, err
	}

	return &LoginResult{Session: session, RawToken: rawToken}, nil
}

// @{"req": ["REQ-AUTH-005"]}
func (s *Service) Logout(ctx context.Context, tokenHash string) error {
	// DeleteSession returns nil when no row matches (pgx DELETE on 0 rows is not an
	// error), so this call is already idempotent without any additional guard.
	return s.repo.DeleteSession(ctx, tokenHash)
}
