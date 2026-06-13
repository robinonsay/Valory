package user

// email.go — EmailTransport interface and mailerAdapter.
//
// Sprint 19 refactor: SMTPTransport and NoOpTransport have been deleted.
// A thin mailerAdapter wraps email.Mailer so it satisfies the legacy
// EmailTransport interface without touching user.Service, user.Handler,
// or any test that injects a mock via the interface.

import (
	"context"
	"fmt"

	"github.com/valory/valory/internal/email"
)

// EmailTransport is the interface the password-reset service depends on.
//
// @{"req": ["REQ-USER-005"]}
type EmailTransport interface {
	SendPasswordReset(ctx context.Context, toAddress, rawToken string) error
}

// mailerAdapter wraps email.Mailer so it satisfies the legacy EmailTransport
// interface. This eliminates the SMTPTransport/NoOpTransport pair from the
// user package without touching user.Service or user.Handler.
//
// @{"req": ["REQ-USER-005", "REQ-EMAIL-001"]}
type mailerAdapter struct {
	m email.Mailer
}

// NewMailerAdapter constructs an EmailTransport backed by an email.Mailer.
// Callers in main.go replace the old NewEmailTransport call with this one.
//
// @{"req": ["REQ-USER-005", "REQ-EMAIL-001"]}
func NewMailerAdapter(m email.Mailer) EmailTransport {
	return &mailerAdapter{m: m}
}

// SendPasswordReset satisfies EmailTransport using the underlying Mailer.Send.
// The body is plain text matching the format previously used by SMTPTransport.
//
// @{"req": ["REQ-USER-005"]}
func (a *mailerAdapter) SendPasswordReset(ctx context.Context, toAddress, rawToken string) error {
	subject := "Password Reset"
	body := fmt.Sprintf(
		"Your password reset token: %s\r\n\r\nThis token expires in 1 hour.\r\n",
		rawToken,
	)
	return a.m.Send(ctx, toAddress, subject, body)
}
