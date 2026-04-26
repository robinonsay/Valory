package user

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

// @{"verifies": ["REQ-USER-005"]}
func TestNoOpTransport_WritesOutput(t *testing.T) {
	buf := &bytes.Buffer{}
	transport := &NoOpTransport{Out: buf}
	ctx := context.Background()

	err := transport.SendPasswordReset(ctx, "alice@example.com", "tok123")
	if err != nil {
		t.Fatalf("SendPasswordReset returned error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "alice@example.com") {
		t.Errorf("output does not contain email address: %s", output)
	}
	// Token is redacted to 4-char prefix + "..." for security; the full token must not appear.
	if strings.Contains(output, "tok123") {
		t.Errorf("output must not contain the full raw token: %s", output)
	}
	if !strings.Contains(output, "tok1...") {
		t.Errorf("output does not contain redacted token prefix: %s", output)
	}
}

// @{"verifies": ["REQ-USER-005"]}
func TestNoOpTransport_ReturnsNil(t *testing.T) {
	buf := &bytes.Buffer{}
	transport := &NoOpTransport{Out: buf}
	ctx := context.Background()

	err := transport.SendPasswordReset(ctx, "alice@example.com", "tok123")
	if err != nil {
		t.Errorf("SendPasswordReset returned non-nil error: %v", err)
	}
}

// @{"verifies": ["REQ-USER-005"]}
func TestSMTPTransport_DialError_ReturnsError(t *testing.T) {
	transport := &SMTPTransport{
		Host:     "localhost",
		Port:     1,
		From:     "noreply@example.com",
		Password: "password",
	}
	ctx := context.Background()

	err := transport.SendPasswordReset(ctx, "alice@example.com", "tok123")
	if err == nil {
		t.Error("SendPasswordReset returned nil error for non-routable address")
	}
}

// @{"verifies": ["REQ-USER-005"]}
func TestNewEmailTransport_EmptyHost_ReturnsNoOp(t *testing.T) {
	transport := NewEmailTransport("", 0, "", "", os.Stderr)

	_, ok := transport.(*NoOpTransport)
	if !ok {
		t.Errorf("expected *NoOpTransport, got %T", transport)
	}
}

// @{"verifies": ["REQ-USER-005"]}
func TestNewEmailTransport_WithHost_ReturnsSMTP(t *testing.T) {
	transport := NewEmailTransport("smtp.example.com", 587, "from@example.com", "pass", nil)

	_, ok := transport.(*SMTPTransport)
	if !ok {
		t.Errorf("expected *SMTPTransport, got %T", transport)
	}
}

// @{"verifies": ["REQ-USER-005"]}
func TestNoOpTransport_FormatConsistency(t *testing.T) {
	buf := &bytes.Buffer{}
	transport := &NoOpTransport{Out: buf}
	ctx := context.Background()

	err := transport.SendPasswordReset(ctx, "test@example.com", "abc123")
	if err != nil {
		t.Fatalf("SendPasswordReset returned error: %v", err)
	}

	expected := "[password-reset] to=test@example.com token=abc1...\n"
	output := buf.String()
	if output != expected {
		t.Errorf("output format mismatch\nexpected: %q\ngot:      %q", expected, output)
	}
}

// @{"verifies": ["REQ-USER-005"]}
func TestSMTPTransport_CancelledContext_ReturnsError(t *testing.T) {
	transport := &SMTPTransport{
		Host:     "smtp.example.com",
		Port:     587,
		From:     "noreply@example.com",
		Password: "password",
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := transport.SendPasswordReset(ctx, "alice@example.com", "tok123")
	if err == nil {
		t.Error("SendPasswordReset returned nil error for cancelled context")
	}
}
