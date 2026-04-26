package infra

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log"
	"math/big"
	"net"
	"net/http"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

// BuildTLSConfig returns a TLS configuration and an HTTP/1.1 handler for :80.
// In production (acmeDomain non-empty), the handler serves ACME HTTP-01 challenges
// and redirects all other HTTP traffic to HTTPS. In development, the handler
// redirects HTTP to HTTPS using the request's Host header.
// cacheDir is the directory for ACME certificate persistence.
//
// @{"req": ["REQ-AUTH-007", "REQ-SYS-040"]}
func BuildTLSConfig(acmeDomain, cacheDir string) (*tls.Config, http.Handler, error) {
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		CurvePreferences: []tls.CurveID{
			tls.X25519,
			tls.CurveP256,
		},
	}

	// redirectHandler always redirects plain HTTP to HTTPS using the incoming
	// Host header so it works regardless of which hostname is being served.
	redirectHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://"+r.Host+r.RequestURI, http.StatusMovedPermanently)
	})

	if acmeDomain != "" {
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(acmeDomain),
			// cacheDir is caller-controlled so the path is never relative to an
			// unknown working directory.
			Cache: autocert.DirCache(cacheDir),
		}
		tlsCfg.GetCertificate = m.GetCertificate
		// m.HTTPHandler wraps redirectHandler so ACME HTTP-01 challenges are
		// answered first; all other requests are redirected to HTTPS.
		return tlsCfg, m.HTTPHandler(redirectHandler), nil
	}

	// Dev fallback: try to load a previously persisted self-signed cert. If
	// that fails for any reason, generate a fresh one in memory.
	cert, err := tls.LoadX509KeyPair("dev-cert.pem", "dev-key.pem")
	if err != nil {
		log.Printf("tls: failed to load dev cert, generating new one: %v", err)
		cert, err = generateSelfSigned()
		if err != nil {
			return nil, nil, err
		}
	}

	tlsCfg.Certificates = []tls.Certificate{cert}
	return tlsCfg, redirectHandler, nil
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
