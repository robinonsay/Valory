package infra

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

// ServerMode enumerates the four mutually exclusive transport modes supported
// by Valory.  The string value is used directly in startup log lines.
type ServerMode string

const (
	// ModeOperatorCert means the operator supplied VALORY_TLS_CERT_FILE and
	// VALORY_TLS_KEY_FILE; the server loads that pair and serves HTTPS.
	ModeOperatorCert ServerMode = "operator-cert"

	// ModeACME means ACME_DOMAIN is set; the server obtains a Let's Encrypt
	// certificate automatically.
	ModeACME ServerMode = "acme"

	// ModeSelfSigned is the development fallback when no operator cert or ACME
	// domain is configured.
	ModeSelfSigned ServerMode = "self-signed"

	// ModeBehindProxy means VALORY_BEHIND_PROXY=true; the server serves plain
	// HTTP and expects a TLS-terminating reverse proxy to handle encryption.
	ModeBehindProxy ServerMode = "behind-proxy"
)

// ResolveTLSMode determines which transport mode to use given the supplied
// environment-variable values.  It returns an error for any configuration that
// is ambiguous or logically inconsistent so the caller can abort startup with a
// descriptive message.
//
// Precedence (highest to lowest):
//  1. VALORY_BEHIND_PROXY=true → plain HTTP (proxy mode)
//  2. VALORY_TLS_CERT_FILE + VALORY_TLS_KEY_FILE both set → operator cert
//  3. acmeDomain non-empty → ACME/autocert
//  4. self-signed dev fallback
//
// Proxy mode combined with any TLS configuration (cert files or ACME domain)
// is a startup error per REQ-INFRA-008.  Supplying exactly one of the two cert
// file env vars is also a startup error per REQ-INFRA-004.
//
// @{"req": ["REQ-INFRA-003", "REQ-INFRA-004", "REQ-INFRA-005", "REQ-INFRA-006", "REQ-INFRA-008"]}
func ResolveTLSMode(certFile, keyFile, acmeDomain, behindProxy string) (ServerMode, error) {
	isBehindProxy := behindProxy == "true"
	hasCertFile := certFile != ""
	hasKeyFile := keyFile != ""
	hasACME := acmeDomain != ""
	hasAnyTLS := hasCertFile || hasKeyFile || hasACME

	// REQ-INFRA-008: proxy mode combined with any TLS configuration is a logical
	// contradiction — the proxy owns TLS, so cert/ACME settings are meaningless
	// and almost certainly a misconfiguration.
	if isBehindProxy && hasAnyTLS {
		return "", fmt.Errorf(
			"configuration error: VALORY_BEHIND_PROXY=true is mutually exclusive with TLS configuration " +
				"(VALORY_TLS_CERT_FILE, VALORY_TLS_KEY_FILE, ACME_DOMAIN); the reverse proxy owns TLS — " +
				"unset the conflicting variable(s) before starting",
		)
	}

	if isBehindProxy {
		return ModeBehindProxy, nil
	}

	// REQ-INFRA-004: both cert and key must be supplied together; one without the
	// other is always a misconfiguration and must not silently fall back.
	if hasCertFile != hasKeyFile {
		missing := "VALORY_TLS_KEY_FILE"
		if !hasCertFile {
			missing = "VALORY_TLS_CERT_FILE"
		}
		return "", fmt.Errorf(
			"configuration error: VALORY_TLS_CERT_FILE and VALORY_TLS_KEY_FILE must both be set; %s is missing",
			missing,
		)
	}

	// REQ-INFRA-005: operator-supplied pair beats ACME beats self-signed.
	if hasCertFile && hasKeyFile {
		return ModeOperatorCert, nil
	}

	if hasACME {
		return ModeACME, nil
	}

	return ModeSelfSigned, nil
}

// TLSResult holds everything main.go needs to start the server(s) for the
// resolved mode.  In TLS modes, TLSConfig is non-nil and HTTPHandler serves
// ACME challenges / redirects on :80.  In proxy mode both are nil and the
// caller must serve the application handler directly on plain HTTP.
type TLSResult struct {
	Mode        ServerMode
	TLSConfig   *tls.Config
	HTTPHandler http.Handler // :80 listener handler (nil in proxy mode)
}

// BuildTLSConfig resolves the active transport mode from the supplied
// environment-variable values, constructs the corresponding TLS configuration,
// and returns a TLSResult describing how to start the server(s).
//
// The function aborts with an error for any invalid configuration; the caller
// should treat any error as a fatal startup failure.
//
// @{"req": ["REQ-INFRA-003", "REQ-INFRA-004", "REQ-INFRA-005", "REQ-INFRA-006", "REQ-INFRA-007", "REQ-INFRA-008", "REQ-AUTH-007", "REQ-SYS-040"]}
func BuildTLSConfig(certFile, keyFile, acmeDomain, acmeCacheDir, behindProxy string) (TLSResult, error) {
	mode, err := ResolveTLSMode(certFile, keyFile, acmeDomain, behindProxy)
	if err != nil {
		return TLSResult{}, err
	}

	switch mode {
	case ModeBehindProxy:
		// REQ-INFRA-007: log which mode is active before accepting connections.
		log.Printf("tls: mode=behind-proxy (plain HTTP; TLS terminated upstream)")
		return TLSResult{Mode: ModeBehindProxy}, nil

	case ModeOperatorCert:
		// REQ-INFRA-003, REQ-INFRA-004: load the operator-supplied pair; any
		// failure is fatal — there must be no silent fallback.
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return TLSResult{}, fmt.Errorf(
				"operator cert: failed to load VALORY_TLS_CERT_FILE=%q / VALORY_TLS_KEY_FILE=%q: %w",
				certFile, keyFile, err,
			)
		}
		log.Printf("tls: mode=operator-cert cert=%q key=%q", certFile, keyFile)
		tlsCfg := baseTLSConfig()
		tlsCfg.Certificates = []tls.Certificate{cert}
		return TLSResult{
			Mode:        ModeOperatorCert,
			TLSConfig:   tlsCfg,
			HTTPHandler: redirectHandler(),
		}, nil

	case ModeACME:
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(acmeDomain),
			// cacheDir is caller-controlled so the path is never relative to an
			// unknown working directory.
			Cache: autocert.DirCache(acmeCacheDir),
		}
		log.Printf("tls: mode=acme domain=%q cache=%q", acmeDomain, acmeCacheDir)
		tlsCfg := baseTLSConfig()
		tlsCfg.GetCertificate = m.GetCertificate
		// m.HTTPHandler wraps redirectHandler so ACME HTTP-01 challenges are
		// answered first; all other requests are redirected to HTTPS.
		return TLSResult{
			Mode:        ModeACME,
			TLSConfig:   tlsCfg,
			HTTPHandler: m.HTTPHandler(redirectHandler()),
		}, nil

	default: // ModeSelfSigned
		// Dev fallback: try to load a previously persisted self-signed cert. If
		// that fails for any reason, generate a fresh one in memory.
		cert, err := tls.LoadX509KeyPair("dev-cert.pem", "dev-key.pem")
		if err != nil {
			log.Printf("tls: failed to load dev cert, generating new one: %v", err)
			cert, err = generateSelfSigned()
			if err != nil {
				return TLSResult{}, err
			}
		}
		log.Printf("tls: mode=self-signed (development fallback — browsers will warn)")
		tlsCfg := baseTLSConfig()
		tlsCfg.Certificates = []tls.Certificate{cert}
		return TLSResult{
			Mode:        ModeSelfSigned,
			TLSConfig:   tlsCfg,
			HTTPHandler: redirectHandler(),
		}, nil
	}
}

// baseTLSConfig returns a TLS config with Valory's hardened cipher/curve
// baseline.  Callers fill in the certificate source before use.
//
// @{"req": ["REQ-AUTH-007", "REQ-SYS-040"]}
func baseTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		CurvePreferences: []tls.CurveID{
			tls.X25519,
			tls.CurveP256,
		},
	}
}

// redirectHandler returns an HTTP handler that permanently redirects any plain-
// HTTP request to its HTTPS equivalent using the incoming Host header.
//
// @{"req": ["REQ-AUTH-007", "REQ-SYS-040"]}
func redirectHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://"+r.Host+r.RequestURI, http.StatusMovedPermanently)
	})
}

// @{"req": ["REQ-AUTH-007", "REQ-SYS-040"]}
func generateSelfSigned() (tls.Certificate, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	// Use a random 128-bit serial number to avoid collisions between
	// regenerated certs and to satisfy stricter CA/Browser Forum rules.
	serialMax := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialMax)
	if err != nil {
		return tls.Certificate{}, err
	}

	now := time.Now()
	template := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "localhost",
		},
		NotBefore:             now,
		NotAfter:              now.Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		// IP literals must appear in IPAddresses, not DNSNames; otherwise TLS
		// clients that validate the SAN strictly will reject the certificate.
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: derBytes,
	})

	privBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, err
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: privBytes,
	})

	return tls.X509KeyPair(certPEM, keyPEM)
}
