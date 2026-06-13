package admin

// secrets.go — AES-256-GCM encryption helpers and SecretProvider.
//
// Design rationale:
//   - KEK (key-encryption key) lives only in Go process memory, never in the DB.
//   - A fresh 12-byte random nonce is generated per write (GCM standard).
//   - Tamper detection: gcm.Open returns an error when the auth tag is wrong.
//   - Resolution order: managed (decryptable DB row) > env var > "".
//   - 30-second TTL cache avoids a DB round-trip on every AI call.
//   - On PUT/DELETE the handler calls Invalidate so the next Get re-decrypts.

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// knownSecrets is the compile-time allowlist of managed secret names.
// Any name absent from this map is rejected at the API boundary (HTTP 400).
//
// @{"req": ["REQ-ADMIN-005", "REQ-ADMIN-006", "REQ-EMAIL-005"]}
var knownSecrets = map[string]bool{
	"anthropic_api_key": true,
	"brave_api_key":     true,
	"smtp_password":     true, // REQ-EMAIL-005: SMTP credential stored encrypted
}

// secretCacheTTL is the maximum age of a cached plaintext value.
// 30 seconds keeps DB pressure negligible while ensuring a key rotation is
// visible within one agent polling cycle.
const secretCacheTTL = 30 * time.Second

// secretEntry holds a resolved plaintext value and the time it expires.
type secretEntry struct {
	value     string
	expiresAt time.Time
}

// clock is an interface over time.Now() so tests can inject a fake clock
// without relying on real wall-clock delays.
type clock interface {
	Now() time.Time
}

// realClock delegates to the real wall clock.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// SecretProvider resolves named secrets with a short-lived in-process cache.
// Construction: admin.NewSecretProvider(kek, pool).
// Injection: passed to ThrottledClient and Professor in main.go.
//
// @{"req": ["REQ-ADMIN-007", "REQ-ADMIN-008", "REQ-SECURITY-006", "REQ-SECURITY-007"]}
type SecretProvider struct {
	kek   []byte // nil when VALORY_SECRET_KEY absent/invalid
	pool  *pgxpool.Pool
	mu    sync.RWMutex
	cache map[string]secretEntry
	clk   clock
}

// NewSecretProvider constructs a SecretProvider.
// kek must be 32 bytes (AES-256) or nil to disable managed secrets.
//
// @{"req": ["REQ-ADMIN-007", "REQ-ADMIN-008", "REQ-SECURITY-006", "REQ-SECURITY-007"]}
func NewSecretProvider(kek []byte, pool *pgxpool.Pool) *SecretProvider {
	return &SecretProvider{
		kek:   kek,
		pool:  pool,
		cache: make(map[string]secretEntry),
		clk:   realClock{},
	}
}

// newSecretProviderWithClock is used in tests to inject a fake clock.
//
// @{"req": ["REQ-ADMIN-007", "REQ-ADMIN-008"]}
func newSecretProviderWithClock(kek []byte, pool *pgxpool.Pool, clk clock) *SecretProvider {
	return &SecretProvider{
		kek:   kek,
		pool:  pool,
		cache: make(map[string]secretEntry),
		clk:   clk,
	}
}

// Get returns the current plaintext value for name.
// Resolution order: managed DB row (if decryptable) > env var > "".
// Results are cached for secretCacheTTL to avoid per-call DB round-trips.
//
// @{"req": ["REQ-ADMIN-007", "REQ-ADMIN-008"]}
func (p *SecretProvider) Get(ctx context.Context, name string) string {
	now := p.clk.Now()
	p.mu.RLock()
	e, ok := p.cache[name]
	p.mu.RUnlock()
	if ok && now.Before(e.expiresAt) {
		return e.value
	}
	v := p.resolve(ctx, name)
	p.mu.Lock()
	p.cache[name] = secretEntry{value: v, expiresAt: now.Add(secretCacheTTL)}
	p.mu.Unlock()
	return v
}

// Invalidate drops the cache entry for name so the next Get re-decrypts from DB.
// Called by the PUT and DELETE handlers immediately after a successful write.
//
// @{"req": ["REQ-ADMIN-008"]}
func (p *SecretProvider) Invalidate(name string) {
	p.mu.Lock()
	delete(p.cache, name)
	p.mu.Unlock()
}

// HasManaged returns true when a row exists in managed_secrets for name.
// Used at startup to decide whether to warn about a missing ANTHROPIC_API_KEY.
//
// @{"req": ["REQ-ADMIN-005"]}
func (p *SecretProvider) HasManaged(name string) bool {
	if p.kek == nil || p.pool == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var ct []byte
	err := p.pool.QueryRow(ctx,
		`SELECT ciphertext FROM managed_secrets WHERE name = $1`, name,
	).Scan(&ct)
	return err == nil
}

// resolve fetches the plaintext value from the DB (if kek is set and a row
// exists) or falls back to the environment variable.
//
// @{"req": ["REQ-ADMIN-007", "REQ-ADMIN-008", "REQ-SECURITY-006"]}
func (p *SecretProvider) resolve(ctx context.Context, name string) string {
	if p.kek != nil && p.pool != nil {
		var ct, nonce []byte
		err := p.pool.QueryRow(ctx,
			`SELECT ciphertext, nonce FROM managed_secrets WHERE name = $1`, name,
		).Scan(&ct, &nonce)
		if err == nil {
			plain, err := gcmDecrypt(p.kek, ct, nonce)
			if err == nil {
				return string(plain)
			}
			// Tampered or wrong key — warn and fall through to env var.
			log.Printf("WARN: managed_secrets: decrypt failed for %q: %v", name, err)
		} else if err != pgx.ErrNoRows {
			log.Printf("WARN: managed_secrets: DB query failed for %q: %v", name, err)
		}
	}
	// Env var fallback: "brave_api_key" -> BRAVE_API_KEY
	return os.Getenv(strings.ToUpper(name))
}

// LoadKEK reads VALORY_SECRET_KEY from the environment, base64-decodes it,
// and verifies the result is exactly 32 bytes (AES-256).
// Returns (nil, false) with a WARN log when absent or invalid — callers
// continue running in env-fallback mode without crashing.
//
// @{"req": ["REQ-SECURITY-007"]}
func LoadKEK() ([]byte, bool) {
	raw := os.Getenv("VALORY_SECRET_KEY")
	if raw == "" {
		log.Printf("WARN: VALORY_SECRET_KEY not set; managed secrets disabled")
		return nil, false
	}
	kek, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(kek) != 32 {
		log.Printf("WARN: VALORY_SECRET_KEY invalid (must be base64-encoded 32 bytes); managed secrets disabled")
		return nil, false
	}
	return kek, true
}

// gcmEncrypt encrypts plaintext with AES-256-GCM using kek.
// A fresh 12-byte random nonce is generated for each call.
// The GCM authentication tag is appended to the returned ciphertext by Seal.
//
// @{"req": ["REQ-SECURITY-006"]}
func gcmEncrypt(kek, plaintext []byte) (ciphertext, nonce []byte, err error) {
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize()) // 12 bytes per GCM standard
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = gcm.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

// gcmDecrypt decrypts ciphertext using kek and nonce.
// Returns an error when the authentication tag does not match (tampered or wrong key).
//
// @{"req": ["REQ-SECURITY-006"]}
func gcmDecrypt(kek, ciphertext, nonce []byte) ([]byte, error) {
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}
