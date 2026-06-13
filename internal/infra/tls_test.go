package infra

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTempCertPair generates a self-signed ECDSA certificate pair and writes
// the PEM files into dir, returning their paths.
//
// @{"req": ["REQ-INFRA-003", "REQ-INFRA-004"]}
func writeTempCertPair(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	serialMax := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialMax)
	if err != nil {
		t.Fatalf("generate serial: %v", err)
	}

	now := time.Now()
	template := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "test"},
		NotBefore:             now,
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	privBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privBytes})

	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath
}

// TestResolveTLSMode_Precedence verifies that operator-cert beats ACME beats
// self-signed (REQ-INFRA-005) and that proxy mode is selected when
// VALORY_BEHIND_PROXY=true (REQ-INFRA-006).
//
// @{"verifies": ["REQ-INFRA-005", "REQ-INFRA-006"]}
func TestResolveTLSMode_Precedence(t *testing.T) {
	cases := []struct {
		name        string
		certFile    string
		keyFile     string
		acmeDomain  string
		behindProxy string
		wantMode    ServerMode
	}{
		{
			name:     "self-signed when nothing set",
			wantMode: ModeSelfSigned,
		},
		{
			name:       "acme when domain set",
			acmeDomain: "example.com",
			wantMode:   ModeACME,
		},
		{
			name:       "operator-cert beats acme",
			certFile:   "/fake/cert.pem",
			keyFile:    "/fake/key.pem",
			acmeDomain: "example.com",
			wantMode:   ModeOperatorCert,
		},
		{
			name:        "behind-proxy when flag set",
			behindProxy: "true",
			wantMode:    ModeBehindProxy,
		},
		{
			// Exact string "true" is required; any other value is not proxy mode.
			name:        "non-true behindProxy is self-signed",
			behindProxy: "1",
			wantMode:    ModeSelfSigned,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, err := ResolveTLSMode(tc.certFile, tc.keyFile, tc.acmeDomain, tc.behindProxy)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if mode != tc.wantMode {
				t.Errorf("got mode %q, want %q", mode, tc.wantMode)
			}
		})
	}
}

// TestResolveTLSMode_BothVarsRequired verifies that supplying only one of the
// cert/key env vars is a startup error (REQ-INFRA-004).
//
// @{"verifies": ["REQ-INFRA-004"]}
func TestResolveTLSMode_BothVarsRequired(t *testing.T) {
	cases := []struct {
		name     string
		certFile string
		keyFile  string
	}{
		{name: "cert only", certFile: "/fake/cert.pem"},
		{name: "key only", keyFile: "/fake/key.pem"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolveTLSMode(tc.certFile, tc.keyFile, "", "")
			if err == nil {
				t.Fatal("expected error when only one of cert/key is set, got nil")
			}
		})
	}
}

// TestResolveTLSMode_ProxyMutualExclusion verifies that VALORY_BEHIND_PROXY=true
// combined with any TLS configuration is a startup error (REQ-INFRA-008).
//
// @{"verifies": ["REQ-INFRA-008"]}
func TestResolveTLSMode_ProxyMutualExclusion(t *testing.T) {
	cases := []struct {
		name        string
		certFile    string
		keyFile     string
		acmeDomain  string
		behindProxy string
	}{
		{
			name:        "proxy + cert pair",
			certFile:    "/fake/cert.pem",
			keyFile:     "/fake/key.pem",
			behindProxy: "true",
		},
		{
			name:        "proxy + acme domain",
			acmeDomain:  "example.com",
			behindProxy: "true",
		},
		{
			name:        "proxy + cert file only (also triggers half-pair error, but proxy check comes first)",
			certFile:    "/fake/cert.pem",
			behindProxy: "true",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolveTLSMode(tc.certFile, tc.keyFile, tc.acmeDomain, tc.behindProxy)
			if err == nil {
				t.Fatal("expected error when proxy mode combined with TLS config, got nil")
			}
			// The error message must name the conflicting variables so the operator
			// knows what to fix (REQ-INFRA-008).
			if !strings.Contains(err.Error(), "VALORY_BEHIND_PROXY") {
				t.Errorf("error message should mention VALORY_BEHIND_PROXY, got: %v", err)
			}
		})
	}
}

// TestBuildTLSConfig_OperatorCert verifies that a valid on-disk cert pair
// produces a non-nil TLS config with no error (REQ-INFRA-003).
//
// @{"verifies": ["REQ-INFRA-003", "REQ-INFRA-004"]}
func TestBuildTLSConfig_OperatorCert(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeTempCertPair(t, dir)

	result, err := BuildTLSConfig(certPath, keyPath, "", "/tmp/acme", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Mode != ModeOperatorCert {
		t.Errorf("got mode %q, want %q", result.Mode, ModeOperatorCert)
	}
	if result.TLSConfig == nil {
		t.Error("expected non-nil TLS config for operator-cert mode")
	}
	if result.HTTPHandler == nil {
		t.Error("expected non-nil HTTP handler for operator-cert mode")
	}
}

// TestBuildTLSConfig_InvalidCertAbortsStartup verifies that a cert pair with
// bad content produces an error and does not fall back silently (REQ-INFRA-004).
//
// @{"verifies": ["REQ-INFRA-004"]}
func TestBuildTLSConfig_InvalidCertAbortsStartup(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "bad-cert.pem")
	keyPath := filepath.Join(dir, "bad-key.pem")

	if err := os.WriteFile(certPath, []byte("not a certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("not a key"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := BuildTLSConfig(certPath, keyPath, "", "/tmp/acme", "")
	if err == nil {
		t.Fatal("expected error for invalid cert pair, got nil")
	}
}

// TestBuildTLSConfig_UnreadableCertAbortsStartup verifies that a cert path
// that does not exist produces an error (REQ-INFRA-004).
//
// @{"verifies": ["REQ-INFRA-004"]}
func TestBuildTLSConfig_UnreadableCertAbortsStartup(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "nonexistent-cert.pem")
	keyPath := filepath.Join(dir, "nonexistent-key.pem")

	_, err := BuildTLSConfig(certPath, keyPath, "", "/tmp/acme", "")
	if err == nil {
		t.Fatal("expected error for unreadable cert pair, got nil")
	}
}

// TestBuildTLSConfig_BehindProxy verifies that proxy mode returns a nil TLS
// config and nil HTTP handler (REQ-INFRA-006).
//
// @{"verifies": ["REQ-INFRA-006"]}
func TestBuildTLSConfig_BehindProxy(t *testing.T) {
	result, err := BuildTLSConfig("", "", "", "/tmp/acme", "true")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Mode != ModeBehindProxy {
		t.Errorf("got mode %q, want %q", result.Mode, ModeBehindProxy)
	}
	if result.TLSConfig != nil {
		t.Error("expected nil TLS config in proxy mode")
	}
	if result.HTTPHandler != nil {
		t.Error("expected nil HTTP handler in proxy mode")
	}
}

// TestBuildTLSConfig_SelfSignedFallback verifies that the dev fallback still
// produces a working TLS config when no env vars are set (REQ-INFRA-005).
//
// @{"verifies": ["REQ-INFRA-005"]}
func TestBuildTLSConfig_SelfSignedFallback(t *testing.T) {
	// Run from a temp dir so LoadX509KeyPair("dev-cert.pem", ...) fails and the
	// in-memory self-signed path is exercised.
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}

	result, err := BuildTLSConfig("", "", "", "/tmp/acme", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Mode != ModeSelfSigned {
		t.Errorf("got mode %q, want %q", result.Mode, ModeSelfSigned)
	}
	if result.TLSConfig == nil {
		t.Error("expected non-nil TLS config in self-signed mode")
	}
	if len(result.TLSConfig.Certificates) == 0 {
		t.Error("expected at least one certificate in self-signed TLS config")
	}
}
