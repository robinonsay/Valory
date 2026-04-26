package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

// @{"req": ["REQ-AUTH-002", "REQ-AUTH-005"]}
func IssueToken(maxDuration time.Duration) (rawToken, tokenHash string, expiresAt time.Time, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", time.Time{}, err
	}
	rawToken = base64.RawURLEncoding.EncodeToString(buf)
	tokenHash = HashToken(rawToken)
	expiresAt = time.Now().Add(maxDuration)
	return rawToken, tokenHash, expiresAt, nil
}

// @{"req": ["REQ-AUTH-002", "REQ-AUTH-005"]}
func HashToken(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(sum[:])
}

// @{"req": ["REQ-AUTH-005"]}
func CheckExpiry(now, expiresAt, lastActiveAt time.Time, inactivityPeriod time.Duration) error {
	if !now.Before(expiresAt) {
		return errors.New("session expired")
	}
	if now.Sub(lastActiveAt) >= inactivityPeriod {
		return errors.New("session inactive")
	}
	return nil
}
