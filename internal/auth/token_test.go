package auth

import (
	"testing"
	"time"
)

// @{"verifies": ["REQ-AUTH-005"]}
func TestCheckExpiry_AbsoluteExpiry(t *testing.T) {
	// TC-AUTH-013: token issued with maxDuration=2s, checked 3s later => expired
	rawToken, _, expiresAt, err := IssueToken(2 * time.Second)
	if err != nil {
		t.Fatalf("IssueToken error: %v", err)
	}
	_ = rawToken

	now := expiresAt.Add(time.Second)
	lastActiveAt := expiresAt.Add(-time.Second)
	if err := CheckExpiry(now, expiresAt, lastActiveAt, 30*time.Minute); err == nil {
		t.Error("expected expiry error for token checked 1s past expiresAt, got nil")
	}
}

// @{"verifies": ["REQ-AUTH-005"]}
func TestCheckExpiry_WithinInactivityWindow(t *testing.T) {
	// TC-AUTH-014: inactivityPeriod=30m, checked 5min after lastActiveAt => valid
	_, _, expiresAt, err := IssueToken(time.Hour)
	if err != nil {
		t.Fatalf("IssueToken error: %v", err)
	}

	lastActiveAt := time.Now()
	now := lastActiveAt.Add(5 * time.Minute)

	if err := CheckExpiry(now, expiresAt, lastActiveAt, 30*time.Minute); err != nil {
		t.Errorf("expected no error for active session within inactivity window, got: %v", err)
	}
}

// @{"verifies": ["REQ-AUTH-005"]}
func TestCheckExpiry_ExactBoundary(t *testing.T) {
	// TC-AUTH-015: now == expiresAt => expired (>= boundary is expired)
	_, _, expiresAt, err := IssueToken(time.Hour)
	if err != nil {
		t.Fatalf("IssueToken error: %v", err)
	}

	lastActiveAt := expiresAt.Add(-time.Hour)
	if err := CheckExpiry(expiresAt, expiresAt, lastActiveAt, 30*time.Minute); err == nil {
		t.Error("expected expiry error when now == expiresAt, got nil")
	}
}

// @{"verifies": ["REQ-AUTH-005"]}
func TestCheckExpiry_ExactInactivityBoundary(t *testing.T) {
	// inactivity: now - lastActiveAt == inactivityPeriod => expired
	_, _, expiresAt, err := IssueToken(time.Hour)
	if err != nil {
		t.Fatalf("IssueToken error: %v", err)
	}

	lastActiveAt := time.Now()
	now := lastActiveAt.Add(30 * time.Minute)

	if err := CheckExpiry(now, expiresAt, lastActiveAt, 30*time.Minute); err == nil {
		t.Error("expected expiry error when inactivity duration == inactivityPeriod, got nil")
	}
}

// @{"verifies": ["REQ-AUTH-002"]}
func TestIssueToken_HashRoundTrip(t *testing.T) {
	rawToken, tokenHash, _, err := IssueToken(time.Hour)
	if err != nil {
		t.Fatalf("IssueToken error: %v", err)
	}
	if got := HashToken(rawToken); got != tokenHash {
		t.Errorf("HashToken(%q) = %q, want %q", rawToken, got, tokenHash)
	}
}

// @{"verifies": ["REQ-AUTH-002"]}
func TestIssueToken_Uniqueness(t *testing.T) {
	raw1, hash1, _, err := IssueToken(time.Hour)
	if err != nil {
		t.Fatalf("IssueToken error: %v", err)
	}
	raw2, hash2, _, err := IssueToken(time.Hour)
	if err != nil {
		t.Fatalf("IssueToken error: %v", err)
	}
	if raw1 == raw2 {
		t.Error("two calls to IssueToken produced identical raw tokens")
	}
	if hash1 == hash2 {
		t.Error("two calls to IssueToken produced identical token hashes")
	}
}

// @{"verifies": ["REQ-AUTH-002"]}
func TestIssueToken_HappyPath(t *testing.T) {
	rawToken, tokenHash, expiresAt, err := IssueToken(time.Hour)
	if err != nil {
		t.Fatalf("IssueToken returned unexpected error: %v", err)
	}
	if rawToken == "" {
		t.Error("rawToken is empty")
	}
	if tokenHash == "" {
		t.Error("tokenHash is empty")
	}
	if expiresAt.IsZero() {
		t.Error("expiresAt is zero")
	}
	if !time.Now().Before(expiresAt) {
		t.Error("expiresAt is not in the future")
	}
}
