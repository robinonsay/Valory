package auth

import (
	"context"
	"errors"
	"time"
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

// @{"req": ["REQ-AUTH-001", "REQ-AUTH-002", "REQ-AUTH-003", "REQ-AUTH-005", "REQ-AUTH-006"]}
type Service struct {
	repo               *Repository
	lockoutDuration    time.Duration
	sessionMaxDuration time.Duration
}

// @{"req": ["REQ-AUTH-001", "REQ-AUTH-002", "REQ-AUTH-003", "REQ-AUTH-005", "REQ-AUTH-006"]}
func NewService(repo *Repository, lockoutDuration, sessionMaxDuration time.Duration) *Service {
	return &Service{
		repo:               repo,
		lockoutDuration:    lockoutDuration,
		sessionMaxDuration: sessionMaxDuration,
	}
}

// @{"req": ["REQ-AUTH-001", "REQ-AUTH-002", "REQ-AUTH-003", "REQ-AUTH-006"]}
func (s *Service) Login(ctx context.Context, username, password string) (rawToken string, session *Session, err error) {
	if username == "" || password == "" {
		return "", nil, ErrInvalidCredentials
	}

	user, err := s.repo.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// Run a dummy argon2 verification so that the response time for an
			// unknown username matches the active-user code path, preventing
			// username enumeration via response timing.
			CheckPassword(password, dummyHash) //nolint:errcheck
			return "", nil, ErrInvalidCredentials
		}
		return "", nil, err
	}

	// Always run the argon2 password check before inspecting account state so
	// that disabled, locked, and wrong-password code paths all take approximately
	// the same time. This prevents response-timing attacks from distinguishing
	// whether a username corresponds to an active, disabled, or locked account.
	ok, err := CheckPassword(password, user.PasswordHash)
	if err != nil {
		return "", nil, err
	}

	if !user.IsActive {
		return "", nil, ErrAccountDisabled
	}

	if user.LockedUntil != nil && user.LockedUntil.After(time.Now()) {
		// Do not increment the counter during an active lockout period.
		return "", nil, &AccountLockedError{Until: *user.LockedUntil}
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
			return "", nil, setErr
		}
		if recErr := s.repo.RecordLoginAttempt(ctx, user.ID, false); recErr != nil {
			return "", nil, recErr
		}
		return "", nil, ErrInvalidCredentials
	}

	if err := s.repo.ResetLoginState(ctx, user.ID); err != nil {
		return "", nil, err
	}
	if err := s.repo.RecordLoginAttempt(ctx, user.ID, true); err != nil {
		return "", nil, err
	}

	rawToken, tokenHash, expiresAt, err := IssueToken(s.sessionMaxDuration)
	if err != nil {
		return "", nil, err
	}

	session, err = s.repo.CreateSession(ctx, user.ID, tokenHash, user.Role, expiresAt)
	if err != nil {
		return "", nil, err
	}

	return rawToken, session, nil
}

// @{"req": ["REQ-AUTH-005"]}
func (s *Service) Logout(ctx context.Context, tokenHash string) error {
	// DeleteSession returns nil when no row matches (pgx DELETE on 0 rows is not an
	// error), so this call is already idempotent without any additional guard.
	return s.repo.DeleteSession(ctx, tokenHash)
}
