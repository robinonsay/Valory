package user

// email_test.go — Tests for the mailerAdapter (Sprint 19 refactor).
// SMTPTransport and NoOpTransport have been replaced by mailerAdapter;
// these tests verify the adapter satisfies EmailTransport correctly.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// mockMailer is a test double for email.Mailer.
// It lives in this file (package user) to avoid a circular import — the email
// package is imported by user/email.go, and an import of email here would be
// fine, but using a local mock keeps the test dependency surface minimal.
type mockMailer struct {
	sendErr     error
	configured  bool
	lastTo      string
	lastSubject string
	lastBody    string
}

func (m *mockMailer) Send(_ context.Context, to, subject, body string) error {
	m.lastTo = to
	m.lastSubject = subject
	m.lastBody = body
	return m.sendErr
}

func (m *mockMailer) IsConfigured() bool { return m.configured }

// @{"verifies": ["REQ-USER-005"]}
func TestMailerAdapter_SendPasswordReset_ForwardsToMailer(t *testing.T) {
	mock := &mockMailer{configured: true}
	transport := NewMailerAdapter(mock)

	ctx := context.Background()
	err := transport.SendPasswordReset(ctx, "alice@example.com", "rawtoken123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.lastTo != "alice@example.com" {
		t.Errorf("to: got %q, want alice@example.com", mock.lastTo)
	}
	if mock.lastSubject != "Password Reset" {
		t.Errorf("subject: got %q, want Password Reset", mock.lastSubject)
	}
	// Body must contain the raw token.
	if !strings.Contains(mock.lastBody, "rawtoken123") {
		t.Errorf("body does not contain token: %q", mock.lastBody)
	}
}

// @{"verifies": ["REQ-USER-005"]}
func TestMailerAdapter_SendPasswordReset_PropagatesError(t *testing.T) {
	// The adapter propagates the error to the caller (user.Service discards it
	// for anti-enumeration, but the adapter itself must not swallow it).
	sendErr := errors.New("connection refused")
	mock := &mockMailer{configured: true, sendErr: sendErr}
	transport := NewMailerAdapter(mock)

	ctx := context.Background()
	err := transport.SendPasswordReset(ctx, "bob@example.com", "tok456")
	if !errors.Is(err, sendErr) {
		t.Errorf("expected sendErr propagated, got %v", err)
	}
}

// @{"verifies": ["REQ-USER-005"]}
func TestMailerAdapter_SendPasswordReset_BodyContainsExpiryNotice(t *testing.T) {
	mock := &mockMailer{configured: true}
	transport := NewMailerAdapter(mock)

	ctx := context.Background()
	if err := transport.SendPasswordReset(ctx, "c@example.com", "tok789"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(mock.lastBody, "expires in 1 hour") {
		t.Errorf("body missing expiry notice: %q", mock.lastBody)
	}
}

// @{"verifies": ["REQ-USER-005"]}
func TestMailerAdapter_SendPasswordReset_CancelledContext(t *testing.T) {
	// When the context is already cancelled the mailer should return an error.
	mock := &mockMailer{
		configured: true,
		sendErr:    context.Canceled,
	}
	transport := NewMailerAdapter(mock)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := transport.SendPasswordReset(ctx, "d@example.com", "tok000")
	if err == nil {
		t.Error("expected error for cancelled context, got nil")
	}
}
