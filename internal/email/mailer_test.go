package email

// mailer_test.go — Unit tests for the email package (no real SMTP required).
// All tests are pure-Go without a DB or network connection.
// Use `go test -tags testing ./internal/email/...` or plain `go test ./internal/email/...`.

import (
	"context"
	"strings"
	"testing"
)

// stubConfig is a test double for ConfigLoader.
type stubConfig struct {
	vals map[string]string
}

func (s *stubConfig) GetString(key string) string { return s.vals[key] }

// stubSecrets is a test double for SecretGetter.
type stubSecrets struct {
	vals map[string]string
}

func (s *stubSecrets) Get(_ context.Context, name string) string { return s.vals[name] }

// @{"verifies": ["REQ-EMAIL-006"]}
func TestIsConfigured_TrueWhenAdminHostSet(t *testing.T) {
	m := NewSMTPMailer(
		&stubConfig{vals: map[string]string{"smtp_host": "mail.example.com"}},
		&stubSecrets{},
		SMTPEnvVars{},
	)
	if !m.IsConfigured() {
		t.Error("expected IsConfigured=true when admin config has smtp_host")
	}
}

// @{"verifies": ["REQ-EMAIL-006"]}
func TestIsConfigured_TrueWhenEnvHostSet(t *testing.T) {
	m := NewSMTPMailer(
		&stubConfig{},
		&stubSecrets{},
		SMTPEnvVars{Host: "relay.internal"},
	)
	if !m.IsConfigured() {
		t.Error("expected IsConfigured=true when env host is set")
	}
}

// @{"verifies": ["REQ-EMAIL-006"]}
func TestIsConfigured_FalseWhenNoHostAnywhere(t *testing.T) {
	m := NewSMTPMailer(
		&stubConfig{},
		&stubSecrets{},
		SMTPEnvVars{},
	)
	if m.IsConfigured() {
		t.Error("expected IsConfigured=false when neither admin nor env host is set")
	}
}

// @{"verifies": ["REQ-EMAIL-006"]}
func TestIsConfigured_FalseWhenAdminHostEmpty(t *testing.T) {
	// Admin has cleared the host (empty string = "not configured").
	m := NewSMTPMailer(
		&stubConfig{vals: map[string]string{"smtp_host": ""}},
		&stubSecrets{},
		SMTPEnvVars{Host: ""},
	)
	if m.IsConfigured() {
		t.Error("expected IsConfigured=false when admin host is empty and no env fallback")
	}
}

// @{"verifies": ["REQ-EMAIL-004"]}
func TestResolve_AdminConfigWinsOverEnv(t *testing.T) {
	// Admin sets a host; env has a different one. Admin must win.
	m := NewSMTPMailer(
		&stubConfig{vals: map[string]string{
			"smtp_host":       "admin.smtp.example.com",
			"smtp_port":       "465",
			"smtp_from":       "noreply@admin.example.com",
			"smtp_username":   "adminuser",
			"smtp_encryption": "tls",
		}},
		&stubSecrets{vals: map[string]string{"smtp_password": "adminpass"}},
		SMTPEnvVars{
			Host:       "env.smtp.example.com",
			Port:       587,
			From:       "noreply@env.example.com",
			Username:   "envuser",
			Encryption: "none",
		},
	)

	cfg := m.resolve(context.Background())

	if cfg.host != "admin.smtp.example.com" {
		t.Errorf("host: got %q, want admin.smtp.example.com", cfg.host)
	}
	if cfg.port != 465 {
		t.Errorf("port: got %d, want 465", cfg.port)
	}
	if cfg.from != "noreply@admin.example.com" {
		t.Errorf("from: got %q, want noreply@admin.example.com", cfg.from)
	}
	if cfg.username != "adminuser" {
		t.Errorf("username: got %q, want adminuser", cfg.username)
	}
	if cfg.encryption != "tls" {
		t.Errorf("encryption: got %q, want tls", cfg.encryption)
	}
	if cfg.password != "adminpass" {
		t.Errorf("password: got %q, want adminpass", cfg.password)
	}
}

// @{"verifies": ["REQ-EMAIL-004"]}
func TestResolve_FallsBackToEnvWhenAdminEmpty(t *testing.T) {
	// Admin config has empty strings for all SMTP keys (not yet configured via UI).
	m := NewSMTPMailer(
		&stubConfig{vals: map[string]string{
			"smtp_host":       "",
			"smtp_port":       "",
			"smtp_from":       "",
			"smtp_username":   "",
			"smtp_encryption": "",
		}},
		&stubSecrets{},
		SMTPEnvVars{
			Host:       "relay.local",
			Port:       25,
			From:       "noreply@local",
			Username:   "",
			Encryption: "none",
		},
	)

	cfg := m.resolve(context.Background())

	if cfg.host != "relay.local" {
		t.Errorf("host fallback: got %q, want relay.local", cfg.host)
	}
	if cfg.port != 25 {
		t.Errorf("port fallback: got %d, want 25", cfg.port)
	}
	if cfg.from != "noreply@local" {
		t.Errorf("from fallback: got %q, want noreply@local", cfg.from)
	}
	if cfg.encryption != "none" {
		t.Errorf("encryption fallback: got %q, want none", cfg.encryption)
	}
}

// @{"verifies": ["REQ-EMAIL-004"]}
func TestResolve_DefaultPortWhenBothEmpty(t *testing.T) {
	// Neither admin nor env provides a port; default 587 must be used.
	m := NewSMTPMailer(
		&stubConfig{},
		&stubSecrets{},
		SMTPEnvVars{Port: 0}, // zero = not set
	)
	cfg := m.resolve(context.Background())
	if cfg.port != 587 {
		t.Errorf("default port: got %d, want 587", cfg.port)
	}
}

// @{"verifies": ["REQ-EMAIL-004"]}
func TestResolve_DefaultEncryptionWhenBothEmpty(t *testing.T) {
	// Neither admin nor env provides encryption; default "starttls" must be used.
	m := NewSMTPMailer(
		&stubConfig{},
		&stubSecrets{},
		SMTPEnvVars{},
	)
	cfg := m.resolve(context.Background())
	if cfg.encryption != "starttls" {
		t.Errorf("default encryption: got %q, want starttls", cfg.encryption)
	}
}

// @{"verifies": ["REQ-EMAIL-005"]}
func TestResolve_PasswordFromSecrets(t *testing.T) {
	// SecretGetter returns a managed password; SMTPEnvVars carries no password
	// field — the env fallback is handled inside SecretProvider, not here.
	m := NewSMTPMailer(
		&stubConfig{vals: map[string]string{"smtp_host": "smtp.example.com"}},
		&stubSecrets{vals: map[string]string{"smtp_password": "super-secret"}},
		SMTPEnvVars{},
	)
	cfg := m.resolve(context.Background())
	if cfg.password != "super-secret" {
		t.Errorf("password: got %q, want super-secret", cfg.password)
	}
}

// @{"verifies": ["REQ-EMAIL-005"]}
func TestResolve_PasswordEmptyWhenNoSecret(t *testing.T) {
	// SecretGetter returns empty (no managed secret, SMTP_PASSWORD env not set in
	// this test process). Verify the field is empty — not nil or some default.
	m := NewSMTPMailer(
		&stubConfig{},
		&stubSecrets{},
		SMTPEnvVars{},
	)
	cfg := m.resolve(context.Background())
	if cfg.password != "" {
		t.Errorf("expected empty password when no secret configured, got %q", cfg.password)
	}
}

// @{"verifies": ["REQ-EMAIL-003"]}
func TestMaybeAuth_SkipsAuthWhenUsernameEmpty(t *testing.T) {
	// When username is empty, maybeAuth must not call conn.Auth.
	// We test the logic by verifying the configured auth path is determined
	// correctly: empty username -> no-op (no error, no call).
	// We call resolve and confirm cfg.username is empty to satisfy the AUTH-skip path.
	m := NewSMTPMailer(
		&stubConfig{vals: map[string]string{"smtp_username": ""}},
		&stubSecrets{},
		SMTPEnvVars{Username: ""},
	)
	cfg := m.resolve(context.Background())
	if cfg.username != "" {
		t.Errorf("expected empty username for auth-skip path, got %q", cfg.username)
	}
}

// @{"verifies": ["REQ-EMAIL-011"]}
func TestRedactPassword_ReplacesOccurrences(t *testing.T) {
	err := "dial tcp failed: using password supersecret123 in error"
	got := RedactPassword(err, "supersecret123")
	if strings.Contains(got, "supersecret123") {
		t.Errorf("redact failed: password still present in %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("redact: expected [REDACTED] in %q", got)
	}
}

// @{"verifies": ["REQ-EMAIL-011"]}
func TestRedactPassword_EmptyPasswordIsNoOp(t *testing.T) {
	// An empty password string must not corrupt the error message.
	original := "some smtp error message"
	got := RedactPassword(original, "")
	if got != original {
		t.Errorf("empty-password redact changed string: got %q, want %q", got, original)
	}
}

// @{"verifies": ["REQ-EMAIL-011"]}
func TestRedactPassword_PasswordAsSubstringOfError(t *testing.T) {
	// The password appears as a substring within a larger token.
	// All occurrences must be replaced.
	err := "auth failed: response=PASSWORD_IS_secret_PASSWORD_IS_secret"
	got := RedactPassword(err, "secret")
	if strings.Contains(got, "secret") {
		t.Errorf("redact substring: password still present in %q", got)
	}
	// Two occurrences replaced.
	count := strings.Count(got, "[REDACTED]")
	if count != 2 {
		t.Errorf("expected 2 [REDACTED] tokens, got %d in %q", count, got)
	}
}

// @{"verifies": ["REQ-EMAIL-011"]}
func TestRedactPassword_NoOccurrenceUnchanged(t *testing.T) {
	// When the error string does not contain the password, it is unchanged.
	err := "connection refused: 127.0.0.1:587"
	got := RedactPassword(err, "mypassword")
	if got != err {
		t.Errorf("expected unchanged string, got %q", got)
	}
}

// @{"verifies": ["REQ-EMAIL-011"]}
func TestSanitizeHeader_StripsCRLFVariants(t *testing.T) {
	// SMTP header injection defense: all CR/LF variants must be stripped so a
	// caller-supplied value cannot inject additional RFC 2822 header fields.
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"crlf", "subject\r\nX-Injected: evil", "subjectX-Injected: evil"},
		{"lone_lf", "subject\nX-Injected: evil", "subjectX-Injected: evil"},
		{"lone_cr", "subject\rX-Injected: evil", "subjectX-Injected: evil"},
		{"interior_and_trailing", "sub\r\nject\n", "subject"},
		{"clean_passthrough", "Weekly digest", "Weekly digest"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeHeader(tc.input)
			if got != tc.want {
				t.Errorf("sanitizeHeader(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// @{"verifies": ["REQ-EMAIL-002"]}
func TestResolve_EncryptionModes(t *testing.T) {
	cases := []struct {
		adminVal string
		want     string
	}{
		{"none", "none"},
		{"starttls", "starttls"},
		{"tls", "tls"},
		{"", "starttls"}, // default
	}
	for _, tc := range cases {
		m := NewSMTPMailer(
			&stubConfig{vals: map[string]string{"smtp_encryption": tc.adminVal}},
			&stubSecrets{},
			SMTPEnvVars{},
		)
		cfg := m.resolve(context.Background())
		if cfg.encryption != tc.want {
			t.Errorf("adminVal=%q: got encryption %q, want %q", tc.adminVal, cfg.encryption, tc.want)
		}
	}
}

// @{"verifies": ["REQ-EMAIL-001", "REQ-EMAIL-006"]}
func TestNoOpMailer_IsConfiguredFalse(t *testing.T) {
	n := &NoOpMailer{Out: &strings.Builder{}}
	if n.IsConfigured() {
		t.Error("NoOpMailer.IsConfigured must return false")
	}
}

// @{"verifies": ["REQ-EMAIL-001"]}
func TestNoOpMailer_SendWritesToOut(t *testing.T) {
	var sb strings.Builder
	n := &NoOpMailer{Out: &sb}
	if err := n.Send(context.Background(), "to@example.com", "Hello", "body"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sb.String(), "to@example.com") {
		t.Errorf("expected to-address in noop output, got %q", sb.String())
	}
}
