//go:build testing

package admin

// secrets_test.go — unit tests for KEK parsing, AES-256-GCM helpers, and the
// SecretProvider cache/resolution logic.
//
// These tests run entirely in-process with no database dependency. DB-dependent
// behaviour (provider resolves a managed row) is exercised separately in
// secrets_handler_test.go which requires TEST_DATABASE_URL.
//
// Run with:
//
//	go test -tags testing ./internal/admin/ -run TestKEK
//	go test -tags testing ./internal/admin/ -run TestGCM
//	go test -tags testing ./internal/admin/ -run TestProvider

import (
	"context"
	"encoding/base64"
	"os"
	"testing"
	"time"
)

// ── KEK (key-encryption key) parsing ────────────────────────────────────────

// @{"verifies": ["REQ-SECURITY-007"]}
func TestLoadKEK_Valid32ByteBase64_ReturnsKEKAndTrue(t *testing.T) {
	kek := make([]byte, 32)
	for i := range kek {
		kek[i] = byte(i)
	}
	encoded := base64.StdEncoding.EncodeToString(kek)

	t.Setenv("VALORY_SECRET_KEY", encoded)

	got, ok := LoadKEK()
	if !ok {
		t.Fatal("expected ok=true for a valid 32-byte base64 key")
	}
	if len(got) != 32 {
		t.Fatalf("expected 32-byte KEK, got %d bytes", len(got))
	}
	for i, b := range got {
		if b != kek[i] {
			t.Fatalf("KEK byte %d: expected %d, got %d", i, kek[i], b)
		}
	}
}

// @{"verifies": ["REQ-SECURITY-007"]}
func TestLoadKEK_Absent_ReturnsNilAndFalse(t *testing.T) {
	// Ensure the env var is unset regardless of test execution environment.
	t.Setenv("VALORY_SECRET_KEY", "")
	os.Unsetenv("VALORY_SECRET_KEY")

	got, ok := LoadKEK()
	if ok {
		t.Error("expected ok=false when VALORY_SECRET_KEY is not set")
	}
	if got != nil {
		t.Errorf("expected nil KEK, got %v", got)
	}
}

// @{"verifies": ["REQ-SECURITY-007"]}
func TestLoadKEK_InvalidBase64_ReturnsNilAndFalse(t *testing.T) {
	t.Setenv("VALORY_SECRET_KEY", "!!!not-valid-base64!!!")

	got, ok := LoadKEK()
	if ok {
		t.Error("expected ok=false for invalid base64")
	}
	if got != nil {
		t.Error("expected nil KEK for invalid base64")
	}
}

// @{"verifies": ["REQ-SECURITY-007"]}
func TestLoadKEK_TooShort31Bytes_ReturnsNilAndFalse(t *testing.T) {
	// 31 bytes — one short of the required 32 for AES-256.
	short := make([]byte, 31)
	t.Setenv("VALORY_SECRET_KEY", base64.StdEncoding.EncodeToString(short))

	got, ok := LoadKEK()
	if ok {
		t.Error("expected ok=false for a 31-byte key")
	}
	if got != nil {
		t.Error("expected nil KEK for a 31-byte key")
	}
}

// @{"verifies": ["REQ-SECURITY-007"]}
func TestLoadKEK_TooLong33Bytes_ReturnsNilAndFalse(t *testing.T) {
	// 33 bytes — one over; AES-256 requires exactly 32.
	long := make([]byte, 33)
	t.Setenv("VALORY_SECRET_KEY", base64.StdEncoding.EncodeToString(long))

	got, ok := LoadKEK()
	if ok {
		t.Error("expected ok=false for a 33-byte key")
	}
	if got != nil {
		t.Error("expected nil KEK for a 33-byte key")
	}
}

// @{"verifies": ["REQ-SECURITY-007"]}
func TestLoadKEK_EmptyString_ReturnsNilAndFalse(t *testing.T) {
	t.Setenv("VALORY_SECRET_KEY", "")

	got, ok := LoadKEK()
	if ok {
		t.Error("expected ok=false for empty VALORY_SECRET_KEY")
	}
	if got != nil {
		t.Error("expected nil KEK for empty env var")
	}
}

// ── AES-256-GCM encrypt / decrypt ───────────────────────────────────────────

// validTestKEK returns a deterministic 32-byte key for crypto unit tests.
func validTestKEK() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i + 1)
	}
	return k
}

// @{"verifies": ["REQ-SECURITY-006"]}
func TestGCM_SealOpenRoundTrip_ReturnsOriginalPlaintext(t *testing.T) {
	kek := validTestKEK()
	plaintext := []byte("sk-test-anthropic-key-value")

	ct, nonce, err := gcmEncrypt(kek, plaintext)
	if err != nil {
		t.Fatalf("gcmEncrypt: %v", err)
	}

	got, err := gcmDecrypt(kek, ct, nonce)
	if err != nil {
		t.Fatalf("gcmDecrypt: %v", err)
	}

	if string(got) != string(plaintext) {
		t.Errorf("round-trip: expected %q, got %q", plaintext, got)
	}
}

// @{"verifies": ["REQ-SECURITY-006"]}
func TestGCM_TamperedCiphertext_ReturnsError(t *testing.T) {
	kek := validTestKEK()
	plaintext := []byte("some-secret-value")

	ct, nonce, err := gcmEncrypt(kek, plaintext)
	if err != nil {
		t.Fatalf("gcmEncrypt: %v", err)
	}

	// Flip one byte in the middle of the ciphertext (GCM auth tag is appended,
	// so flipping a body byte also corrupts the tag check).
	tampered := make([]byte, len(ct))
	copy(tampered, ct)
	tampered[len(tampered)/2] ^= 0xFF

	_, err = gcmDecrypt(kek, tampered, nonce)
	if err == nil {
		t.Fatal("expected gcmDecrypt to return an error for tampered ciphertext; got nil")
	}
}

// @{"verifies": ["REQ-SECURITY-006"]}
func TestGCM_TamperedNonce_ReturnsError(t *testing.T) {
	kek := validTestKEK()
	plaintext := []byte("some-secret-value")

	ct, nonce, err := gcmEncrypt(kek, plaintext)
	if err != nil {
		t.Fatalf("gcmEncrypt: %v", err)
	}

	// Flip the first byte of the nonce.
	badNonce := make([]byte, len(nonce))
	copy(badNonce, nonce)
	badNonce[0] ^= 0xFF

	_, err = gcmDecrypt(kek, ct, badNonce)
	if err == nil {
		t.Fatal("expected gcmDecrypt to return an error for tampered nonce; got nil")
	}
}

// @{"verifies": ["REQ-SECURITY-006"]}
func TestGCM_WrongKey_ReturnsError(t *testing.T) {
	kek := validTestKEK()
	plaintext := []byte("another-secret")

	ct, nonce, err := gcmEncrypt(kek, plaintext)
	if err != nil {
		t.Fatalf("gcmEncrypt: %v", err)
	}

	wrongKEK := make([]byte, 32)
	// wrongKEK is all zeros — distinct from validTestKEK.

	_, err = gcmDecrypt(wrongKEK, ct, nonce)
	if err == nil {
		t.Fatal("expected gcmDecrypt to return an error when the wrong key is used; got nil")
	}
}

// @{"verifies": ["REQ-SECURITY-006"]}
func TestGCM_TwoEncryptionsDifferInNonceAndCiphertext(t *testing.T) {
	// AES-256-GCM is a nonce-based cipher. Using a fresh random nonce on each
	// Seal call means two encryptions of the same plaintext must produce
	// different ciphertexts (IND-CPA). This guards against nonce reuse.
	kek := validTestKEK()
	plaintext := []byte("same-plaintext-value")

	ct1, nonce1, err := gcmEncrypt(kek, plaintext)
	if err != nil {
		t.Fatalf("gcmEncrypt #1: %v", err)
	}
	ct2, nonce2, err := gcmEncrypt(kek, plaintext)
	if err != nil {
		t.Fatalf("gcmEncrypt #2: %v", err)
	}

	if string(nonce1) == string(nonce2) {
		t.Error("nonces must differ across two encryptions of the same plaintext")
	}
	if string(ct1) == string(ct2) {
		t.Error("ciphertexts must differ when nonces differ (IND-CPA)")
	}
}

// ── SecretProvider cache and TTL ─────────────────────────────────────────────

// fakeClock implements the clock interface with a controllable current time.
type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time { return c.now }

// @{"verifies": ["REQ-ADMIN-008"]}
func TestProvider_Get_CachesResolvedValue(t *testing.T) {
	// Provider with no pool and no KEK — falls back to env var.
	t.Setenv("ANTHROPIC_API_KEY", "env-key-value")
	defer os.Unsetenv("ANTHROPIC_API_KEY")

	clk := &fakeClock{now: time.Now()}
	p := newSecretProviderWithClock(nil, nil, clk)

	first := p.Get(context.Background(), "anthropic_api_key")
	if first != "env-key-value" {
		t.Fatalf("expected 'env-key-value', got %q", first)
	}

	// Mutate env — the cached value must still be returned within TTL.
	t.Setenv("ANTHROPIC_API_KEY", "different-value")

	second := p.Get(context.Background(), "anthropic_api_key")
	if second != "env-key-value" {
		t.Errorf("cache miss within TTL: expected 'env-key-value', got %q", second)
	}
}

// @{"verifies": ["REQ-ADMIN-008"]}
func TestProvider_Get_CacheExpiresAfterTTL(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "original-value")
	defer os.Unsetenv("ANTHROPIC_API_KEY")

	clk := &fakeClock{now: time.Now()}
	p := newSecretProviderWithClock(nil, nil, clk)

	// Prime cache.
	first := p.Get(context.Background(), "anthropic_api_key")
	if first != "original-value" {
		t.Fatalf("prime: expected 'original-value', got %q", first)
	}

	// Advance clock past the TTL, then mutate env.
	clk.now = clk.now.Add(secretCacheTTL + time.Millisecond)
	t.Setenv("ANTHROPIC_API_KEY", "rotated-value")

	second := p.Get(context.Background(), "anthropic_api_key")
	if second != "rotated-value" {
		t.Errorf("after TTL: expected 'rotated-value', got %q", second)
	}
}

// @{"verifies": ["REQ-ADMIN-008"]}
func TestProvider_Invalidate_ForcesReResolution(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "before-invalidate")
	defer os.Unsetenv("ANTHROPIC_API_KEY")

	clk := &fakeClock{now: time.Now()}
	p := newSecretProviderWithClock(nil, nil, clk)

	// Prime cache.
	p.Get(context.Background(), "anthropic_api_key")

	// Rotate the env var and call Invalidate — clock has NOT advanced past TTL.
	t.Setenv("ANTHROPIC_API_KEY", "after-invalidate")
	p.Invalidate("anthropic_api_key")

	got := p.Get(context.Background(), "anthropic_api_key")
	if got != "after-invalidate" {
		t.Errorf("after Invalidate: expected 'after-invalidate', got %q", got)
	}
}

// @{"verifies": ["REQ-ADMIN-007", "REQ-ADMIN-008"]}
func TestProvider_Get_DisabledMode_FallsBackToEnv(t *testing.T) {
	// When KEK is nil (VALORY_SECRET_KEY absent/invalid) the provider must
	// silently fall back to env vars without panicking or returning an error.
	t.Setenv("BRAVE_API_KEY", "env-brave-key")
	defer os.Unsetenv("BRAVE_API_KEY")

	p := NewSecretProvider(nil, nil) // disabled mode

	got := p.Get(context.Background(), "brave_api_key")
	if got != "env-brave-key" {
		t.Errorf("disabled mode fallback: expected 'env-brave-key', got %q", got)
	}
}

// @{"verifies": ["REQ-ADMIN-007", "REQ-ADMIN-008"]}
func TestProvider_Get_NoEnvNoManaged_ReturnsEmptyString(t *testing.T) {
	// Ensure neither env var is set for this test.
	os.Unsetenv("BRAVE_API_KEY")
	os.Unsetenv("ANTHROPIC_API_KEY")

	p := NewSecretProvider(nil, nil) // disabled mode

	got := p.Get(context.Background(), "brave_api_key")
	if got != "" {
		t.Errorf("expected empty string when no managed row and no env var, got %q", got)
	}
}

// ── HasManaged with nil pool ─────────────────────────────────────────────────

// @{"verifies": ["REQ-ADMIN-005"]}
func TestProvider_HasManaged_NilPool_ReturnsFalse(t *testing.T) {
	p := NewSecretProvider(validTestKEK(), nil)
	if p.HasManaged("anthropic_api_key") {
		t.Error("HasManaged should return false when pool is nil")
	}
}

// @{"verifies": ["REQ-ADMIN-005"]}
func TestProvider_HasManaged_NilKEK_ReturnsFalse(t *testing.T) {
	p := NewSecretProvider(nil, nil)
	if p.HasManaged("anthropic_api_key") {
		t.Error("HasManaged should return false when KEK is nil")
	}
}

// ── Table-driven KEK tests ───────────────────────────────────────────────────

// @{"verifies": ["REQ-SECURITY-007"]}
func TestLoadKEK_TableDriven(t *testing.T) {
	// Generate a valid 32-byte key once.
	validKey := make([]byte, 32)
	for i := range validKey {
		validKey[i] = byte(i + 10)
	}
	validEncoded := base64.StdEncoding.EncodeToString(validKey)

	// 31-byte and 33-byte encodings.
	short := base64.StdEncoding.EncodeToString(make([]byte, 31))
	long := base64.StdEncoding.EncodeToString(make([]byte, 33))

	cases := []struct {
		name       string
		envVal     string
		wantOK     bool
		wantLen    int
		unsetFirst bool
	}{
		{"valid 32 bytes", validEncoded, true, 32, false},
		{"empty env var", "", false, 0, false},
		{"31 bytes (too short)", short, false, 0, false},
		{"33 bytes (too long)", long, false, 0, false},
		{"not base64", "not-base64!!!", false, 0, false},
		{"env var unset", "", false, 0, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.unsetFirst {
				os.Unsetenv("VALORY_SECRET_KEY")
			} else {
				t.Setenv("VALORY_SECRET_KEY", tc.envVal)
			}

			got, ok := LoadKEK()
			if ok != tc.wantOK {
				t.Errorf("ok: expected %v, got %v", tc.wantOK, ok)
			}
			if len(got) != tc.wantLen {
				t.Errorf("len: expected %d, got %d", tc.wantLen, len(got))
			}
		})
	}
}

// @{"verifies": ["REQ-SECURITY-006"]}
func TestGCM_DecryptTamperedCiphertextDoesNotReturnPlaintext(t *testing.T) {
	// Security invariant: a tampered ciphertext must NEVER yield any plaintext.
	// The test verifies that gcmDecrypt returns (nil, err) — no partial plaintext.
	kek := validTestKEK()
	plaintext := []byte("my-api-key-12345")

	ct, nonce, err := gcmEncrypt(kek, plaintext)
	if err != nil {
		t.Fatalf("gcmEncrypt: %v", err)
	}

	tampered := make([]byte, len(ct))
	copy(tampered, ct)
	tampered[0] ^= 0xFF

	got, err := gcmDecrypt(kek, tampered, nonce)
	if err == nil {
		t.Error("expected error for tampered ciphertext; got nil")
	}
	if got != nil {
		t.Errorf("must not return plaintext on auth failure; got %q", got)
	}
}
