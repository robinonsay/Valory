package auth

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

// @{"verifies": ["REQ-AUTH-008", "REQ-SYS-041"]}
func TestHashPassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
	}{
		{"basic_password", "mySecretPassword123"},
		{"different_password", "anotherPassword456"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash1, err := HashPassword(tt.password)
			if err != nil {
				t.Fatalf("HashPassword failed: %v", err)
			}

			if hash1 == "" {
				t.Fatal("expected non-empty hash")
			}

			if len(hash1) == 0 {
				t.Fatal("hash is empty")
			}

			hash2, err := HashPassword(tt.password)
			if err != nil {
				t.Fatalf("HashPassword failed on second call: %v", err)
			}

			if hash1 == hash2 {
				t.Fatal("expected different hashes due to random salt")
			}

			if !validateHashFormat(hash1) {
				t.Fatalf("hash1 does not match expected format: %s", hash1)
			}

			if !validateHashFormat(hash2) {
				t.Fatalf("hash2 does not match expected format: %s", hash2)
			}
		})
	}
}

// @{"verifies": ["REQ-AUTH-008", "REQ-SYS-041"]}
func TestCheckPasswordSuccess(t *testing.T) {
	tests := []struct {
		name     string
		password string
	}{
		{"basic_password", "mySecretPassword123"},
		{"different_password", "password"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash, err := HashPassword(tt.password)
			if err != nil {
				t.Fatalf("HashPassword failed: %v", err)
			}

			match, err := CheckPassword(tt.password, hash)
			if err != nil {
				t.Fatalf("CheckPassword failed: %v", err)
			}

			if !match {
				t.Fatal("expected password to match")
			}
		})
	}
}

// @{"verifies": ["REQ-AUTH-008", "REQ-SYS-041"]}
func TestCheckPasswordMismatch(t *testing.T) {
	plain := "mySecretPassword123"
	wrong := "wrongPassword456"

	hash, err := HashPassword(plain)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	match, err := CheckPassword(wrong, hash)
	if err != nil {
		t.Fatalf("CheckPassword failed: %v", err)
	}

	if match {
		t.Fatal("expected password mismatch")
	}
}

// @{"verifies": ["REQ-AUTH-008", "REQ-SYS-041"]}
func TestCheckPasswordMalformedHash(t *testing.T) {
	plain := "myPassword"

	tests := []struct {
		name  string
		hash  string
		valid bool
	}{
		{"empty", "", false},
		{"no_dollar", "argon2idv19m65536", false},
		{"wrong_algorithm", "$argon2i$v=19$m=65536,t=1,p=4$", false},
		{"missing_parts", "$argon2id$v=19$m=65536", false},
		{"bad_version", "$argon2id$v=18$m=65536,t=1,p=4$YWJjZGVmZ2hpamtsbW5vcA$YWJjZGVmZ2hpamtsbW5vcA", false},
		{"invalid_params", "$argon2id$v=19$invalid$YWJjZGVmZ2hpamtsbW5vcA$YWJjZGVmZ2hpamtsbW5vcA", false},
		{"invalid_salt_b64", "$argon2id$v=19$m=65536,t=1,p=4$!!!invalid!!!$YWJjZGVmZ2hpamtsbW5vcA", false},
		{"invalid_key_b64", "$argon2id$v=19$m=65536,t=1,p=4$YWJjZGVmZ2hpamtsbW5vcA$!!!invalid!!!", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			match, err := CheckPassword(plain, tt.hash)
			if tt.valid {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if match {
					t.Fatal("expected mismatch on valid but different hash")
				}
			} else {
				if err == nil {
					t.Fatal("expected error for malformed hash")
				}
				if match {
					t.Fatal("expected false on error")
				}
			}
		})
	}
}

// @{"verifies": ["REQ-AUTH-008", "REQ-SYS-041"]}
func TestCheckPasswordBoundsRejection(t *testing.T) {
	plain := "testPassword"

	salt := base64.RawStdEncoding.EncodeToString(make([]byte, 16))
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))

	tests := []struct {
		name   string
		m      int
		t      int
		p      int
		wantErr bool
	}{
		{"m_above_max", 1048577, 1, 4, true},
		{"t_above_max", 65536, 11, 4, true},
		{"p_above_max", 65536, 1, 17, true},
		{"m_zero", 0, 1, 4, true},
		{"t_zero", 65536, 0, 4, true},
		{"p_zero", 65536, 1, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash := fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", tt.m, tt.t, tt.p, salt, key)
			match, err := CheckPassword(plain, hash)
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr && err == nil {
				t.Fatal("expected error for out-of-range parameters")
			}
			if match {
				t.Fatal("expected false on error or mismatch")
			}
		})
	}
}

func validateHashFormat(hash string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 {
		return false
	}
	if parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	return true
}
