package email

// mailer.go — Mailer interface, SMTPMailer implementation, NoOpMailer, and
// config resolution logic for the admin-configurable email subsystem.
//
// Design rationale for per-send config resolution (no extra cache layer):
//   Email sends are rare and not on any hot path. Reading from ConfigService is
//   an O(1) map lookup under a read lock. A second TTL cache inside SMTPMailer
//   would add reasoning complexity and delay reflecting config fixes. The
//   SecretProvider already caches the password independently (30s TTL).

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

const (
	// smtpDialTimeout bounds the TCP/TLS dial phase (REQ-EMAIL-012).
	// A non-responsive SMTP server that never completes the TCP handshake will fail
	// fast rather than blocking the calling HTTP request indefinitely.
	smtpDialTimeout = 10 * time.Second
	// smtpSendTimeout bounds the entire Send operation including dial, auth, and
	// data transfer (REQ-EMAIL-013). Even after a successful dial, a slow or
	// throttled SMTP server can stall in the DATA or QUIT phase; this deadline
	// ensures the HTTP request path is not held open past 15 seconds.
	smtpSendTimeout = 15 * time.Second
)

// sanitizeHeader strips CR and LF characters from an SMTP header value.
// Without this, a caller-supplied from/to/subject containing \r\n could inject
// arbitrary headers into the DATA section — an SMTP header injection attack
// (RFC 5321 §4.1.1.4). The envelope (conn.Mail/conn.Rcpt) is guarded by the
// smtp package itself, but the RFC 2822 header fields we write manually are not.
//
// @{"req": ["REQ-EMAIL-001"]}
func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}

// Mailer is the single outbound-email abstraction used across the application.
// Implementations must be safe for concurrent use.
//
// @{"req": ["REQ-EMAIL-001"]}
type Mailer interface {
	// Send delivers a single message. subject and body are plain text.
	Send(ctx context.Context, to, subject, body string) error

	// IsConfigured returns true when the Mailer has enough configuration to
	// attempt a send. Callers use this to surface "no email sent — email not
	// configured" notices without attempting a send.
	IsConfigured() bool
}

// ConfigLoader is satisfied by *admin.ConfigService.
// Using a narrow interface here avoids a circular import between the email
// and admin packages (admin imports email for the test-send handler).
//
// @{"req": ["REQ-EMAIL-004"]}
type ConfigLoader interface {
	GetString(key string) string
}

// SecretGetter is satisfied by *admin.SecretProvider.
// The email package depends only on this narrow Get method, not the full
// SecretProvider surface, again to avoid circular imports.
//
// @{"req": ["REQ-EMAIL-005"]}
type SecretGetter interface {
	Get(ctx context.Context, name string) string
}

// SMTPEnvVars holds the SMTP values captured from environment variables at
// process startup. Capturing them once at construction keeps env-read logic
// testable and avoids os.Getenv calls on every send.
//
// @{"req": ["REQ-EMAIL-004"]}
type SMTPEnvVars struct {
	Host       string
	Port       int
	From       string
	Username   string
	Encryption string // "none" | "starttls" | "tls"; empty treated as "starttls"
	// Password is intentionally absent: it is sourced via SecretGetter.Get at
	// send time so the SMTP_PASSWORD env fallback flows through SecretProvider's
	// existing resolve() convention (strings.ToUpper(name)).
}

// SMTPMailer is the production SMTP implementation of Mailer.
// It resolves config per-send from ConfigLoader (admin DB) with env-var
// fallback from SMTPEnvVars, and the SMTP password via SecretGetter.
//
// @{"req": ["REQ-EMAIL-001", "REQ-EMAIL-002", "REQ-EMAIL-003", "REQ-EMAIL-004", "REQ-EMAIL-005", "REQ-EMAIL-006"]}
type SMTPMailer struct {
	config  ConfigLoader
	secrets SecretGetter
	env     SMTPEnvVars
}

// NewSMTPMailer constructs an SMTPMailer.
// config and secrets must be non-nil. envVars supplies the env-fallback values
// captured at startup.
//
// @{"req": ["REQ-EMAIL-001", "REQ-EMAIL-004", "REQ-EMAIL-005"]}
func NewSMTPMailer(config ConfigLoader, secrets SecretGetter, envVars SMTPEnvVars) *SMTPMailer {
	return &SMTPMailer{
		config:  config,
		secrets: secrets,
		env:     envVars,
	}
}

// resolvedConfig holds the per-send resolved SMTP settings.
type resolvedConfig struct {
	host       string
	port       int
	from       string
	username   string
	password   string
	encryption string // "none" | "starttls" | "tls"
}

// resolve reads config from admin DB first, then falls back to env vars.
// All fields are resolved on every call so admin changes take effect
// immediately without a TTL window.
//
// @{"req": ["REQ-EMAIL-004", "REQ-EMAIL-005"]}
func (m *SMTPMailer) resolve(ctx context.Context) resolvedConfig {
	// Admin-configured value wins; fall back to env var.
	host := first(m.config.GetString("smtp_host"), m.env.Host)

	portStr := m.config.GetString("smtp_port")
	port := m.env.Port
	if portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 {
			port = p
		}
	}
	if port == 0 {
		port = 587 // safe default matching the migration seed row
	}

	from := first(m.config.GetString("smtp_from"), m.env.From)
	username := first(m.config.GetString("smtp_username"), m.env.Username)

	encryption := first(m.config.GetString("smtp_encryption"), m.env.Encryption)
	if encryption == "" {
		encryption = "starttls" // match migration default
	}

	// Password is resolved by SecretProvider which handles managed_secrets >
	// SMTP_PASSWORD env var fallback automatically.
	password := m.secrets.Get(ctx, "smtp_password")

	return resolvedConfig{
		host:       host,
		port:       port,
		from:       from,
		username:   username,
		password:   password,
		encryption: encryption,
	}
}

// ResolvePassword returns the current SMTP password from SecretGetter.
// This is called by the admin test-send handler so it can redact the password
// from any SMTP error message before returning it to the client (REQ-EMAIL-011).
// It is not part of the Mailer interface because it is only needed at the
// test-send boundary.
//
// @{"req": ["REQ-EMAIL-011"]}
func (m *SMTPMailer) ResolvePassword(ctx context.Context) string {
	return m.secrets.Get(ctx, "smtp_password")
}

// IsConfigured returns true when a non-empty SMTP host is available from
// either admin config or env vars. No connection is attempted.
//
// @{"req": ["REQ-EMAIL-006"]}
func (m *SMTPMailer) IsConfigured() bool {
	// Context is unused for the host lookup, but the Mailer interface passes
	// one for forward compatibility; use Background here.
	host := first(m.config.GetString("smtp_host"), m.env.Host)
	return host != ""
}

// Send connects to the configured SMTP server using the resolved encryption
// mode, optionally authenticates, and delivers one message.
//
// @{"req": ["REQ-EMAIL-001", "REQ-EMAIL-002", "REQ-EMAIL-003", "REQ-EMAIL-004", "REQ-EMAIL-005", "REQ-EMAIL-007", "REQ-EMAIL-012", "REQ-EMAIL-013"]}
func (m *SMTPMailer) Send(ctx context.Context, to, subject, body string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// @{"req": ["REQ-EMAIL-013"]}
	// Bound the entire send operation with a finite deadline so a misconfigured or
	// slow SMTP server cannot hold the HTTP request goroutine open indefinitely.
	// This protects request handlers (login, user creation) that wait on email sends.
	ctx, cancel := context.WithTimeout(ctx, smtpSendTimeout)
	defer cancel()

	cfg := m.resolve(ctx)
	if cfg.host == "" {
		return fmt.Errorf("email: SMTP host is not configured")
	}

	addr := fmt.Sprintf("%s:%d", cfg.host, cfg.port)

	switch cfg.encryption {
	case "tls":
		return m.sendImplicitTLS(ctx, addr, cfg, to, subject, body)
	case "none":
		return m.sendPlain(ctx, addr, cfg, to, subject, body)
	default:
		// "starttls" and any unrecognised value default to STARTTLS for safety.
		return m.sendSTARTTLS(ctx, addr, cfg, to, subject, body)
	}
}

// sendSTARTTLS dials a plain TCP connection and upgrades it to TLS via
// SMTP STARTTLS. This is the default and recommended mode for external providers.
//
// @{"req": ["REQ-EMAIL-002", "REQ-EMAIL-012"]}
func (m *SMTPMailer) sendSTARTTLS(ctx context.Context, addr string, cfg resolvedConfig, to, subject, body string) error {
	// @{"req": ["REQ-EMAIL-012"]}
	// Dial with a finite timeout so a non-responsive SMTP server fails fast.
	netConn, err := (&net.Dialer{Timeout: smtpDialTimeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}

	// @{"req": ["REQ-EMAIL-013"]}
	// Set a write/read deadline on the connection so SMTP protocol steps
	// (EHLO, STARTTLS, AUTH, MAIL, RCPT, DATA) cannot block past the overall
	// context deadline. This guards against slow SMTP servers that accept the
	// connection but stall in subsequent phases.
	if deadline, ok := ctx.Deadline(); ok {
		_ = netConn.SetDeadline(deadline)
	}

	conn, err := smtp.NewClient(netConn, cfg.host)
	if err != nil {
		netConn.Close()
		return err
	}
	defer conn.Close()

	if err := conn.StartTLS(&tls.Config{ServerName: cfg.host}); err != nil {
		return err
	}

	if err := m.maybeAuth(conn, cfg); err != nil {
		return err
	}

	return sendMessage(conn, cfg.from, to, subject, body)
}

// sendImplicitTLS dials a TLS connection from the first byte (SMTPS, port 465).
// Required by some providers. Uses tls.DialWithDialer so ctx deadline is honoured.
//
// @{"req": ["REQ-EMAIL-002", "REQ-EMAIL-012"]}
func (m *SMTPMailer) sendImplicitTLS(ctx context.Context, addr string, cfg resolvedConfig, to, subject, body string) error {
	// @{"req": ["REQ-EMAIL-012"]}
	// Dial with a finite timeout so a non-responsive SMTP server fails fast.
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: smtpDialTimeout},
		Config:    &tls.Config{ServerName: cfg.host},
	}
	tlsConn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}

	// @{"req": ["REQ-EMAIL-013"]}
	// Set a write/read deadline on the connection so SMTP protocol steps
	// (AUTH, MAIL, RCPT, DATA) cannot block past the overall context deadline.
	// This guards against slow SMTP servers that accept the connection but stall
	// in subsequent phases.
	if deadline, ok := ctx.Deadline(); ok {
		_ = tlsConn.SetDeadline(deadline)
	}

	conn, err := smtp.NewClient(tlsConn, cfg.host)
	if err != nil {
		tlsConn.Close()
		return err
	}
	defer conn.Close()

	if err := m.maybeAuth(conn, cfg); err != nil {
		return err
	}

	return sendMessage(conn, cfg.from, to, subject, body)
}

// sendPlain dials a plain TCP SMTP connection with no TLS.
// Appropriate for loopback relays (postfix/exim on 127.0.0.1) or trusted
// internal networks. Credentials and message content travel in plaintext
// between Valory and the relay — operators using this mode over a routable
// network accept that exposure.
//
// @{"req": ["REQ-EMAIL-002", "REQ-EMAIL-012"]}
func (m *SMTPMailer) sendPlain(ctx context.Context, addr string, cfg resolvedConfig, to, subject, body string) error {
	// @{"req": ["REQ-EMAIL-012"]}
	// Dial with a finite timeout so a non-responsive SMTP server fails fast.
	netConn, err := (&net.Dialer{Timeout: smtpDialTimeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}

	// @{"req": ["REQ-EMAIL-013"]}
	// Set a write/read deadline on the connection so SMTP protocol steps
	// (AUTH, MAIL, RCPT, DATA) cannot block past the overall context deadline.
	// This guards against slow SMTP servers that accept the connection but stall
	// in subsequent phases.
	if deadline, ok := ctx.Deadline(); ok {
		_ = netConn.SetDeadline(deadline)
	}

	conn, err := smtp.NewClient(netConn, cfg.host)
	if err != nil {
		netConn.Close()
		return err
	}
	defer conn.Close()

	if err := m.maybeAuth(conn, cfg); err != nil {
		return err
	}

	return sendMessage(conn, cfg.from, to, subject, body)
}

// maybeAuth calls smtp.PlainAuth when a username is configured.
// An empty username means no AUTH — required for unauthenticated loopback
// relays (REQ-EMAIL-003).
//
// @{"req": ["REQ-EMAIL-003"]}
func (m *SMTPMailer) maybeAuth(conn *smtp.Client, cfg resolvedConfig) error {
	if cfg.username == "" {
		// Skip AUTH entirely. Unauthenticated loopback relays (postfix, exim)
		// do not issue an AUTH challenge; attempting it would cause an error.
		return nil
	}
	auth := smtp.PlainAuth("", cfg.username, cfg.password, cfg.host)
	return conn.Auth(auth)
}

// sendMessage sets the envelope and writes the message data to an open
// smtp.Client. Called by all three dial paths after TLS/auth setup.
// Header values are sanitized before being written to guard against SMTP header
// injection (RFC 5321 §4.1.1.4); the body is intentionally not sanitized
// because it is not an RFC 2822 header field.
//
// @{"req": ["REQ-EMAIL-001"]}
func sendMessage(conn *smtp.Client, from, to, subject, body string) error {
	if err := conn.Mail(from); err != nil {
		return err
	}
	if err := conn.Rcpt(to); err != nil {
		return err
	}

	w, err := conn.Data()
	if err != nil {
		return err
	}

	msg := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s",
		sanitizeHeader(from), sanitizeHeader(to), sanitizeHeader(subject), body,
	)
	if _, err := w.Write([]byte(msg)); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

// RedactPassword replaces any occurrence of password in s with [REDACTED].
// This is a defense-in-depth measure: SMTP library errors in practice never
// echo the password, but we sanitize before including error strings in HTTP
// responses or log lines to guarantee no credential leakage.
// An empty password is a no-op so callers need not guard the call site.
//
// @{"req": ["REQ-EMAIL-011"]}
func RedactPassword(s, password string) string {
	if password == "" {
		return s
	}
	return strings.ReplaceAll(s, password, "[REDACTED]")
}

// NoOpMailer logs sends to an io.Writer instead of connecting to SMTP.
// Used when IsConfigured() is false or in development/test environments where
// no SMTP host is set.
//
// @{"req": ["REQ-EMAIL-001", "REQ-EMAIL-006"]}
type NoOpMailer struct {
	Out io.Writer
}

// Send writes a one-line log entry to Out instead of sending mail.
//
// @{"req": ["REQ-EMAIL-001"]}
func (n *NoOpMailer) Send(_ context.Context, to, subject, _ string) error {
	_, err := fmt.Fprintf(n.Out, "[email] to=%s subject=%q (noop — not configured)\n", to, subject)
	return err
}

// IsConfigured always returns false for NoOpMailer.
//
// @{"req": ["REQ-EMAIL-006"]}
func (n *NoOpMailer) IsConfigured() bool { return false }

// first returns the first non-empty string from the arguments.
// Used for admin-config-wins-over-env precedence.
func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
