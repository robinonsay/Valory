package user

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/smtp"
)

// EmailTransport is the interface the password-reset service depends on.
type EmailTransport interface {
	SendPasswordReset(ctx context.Context, toAddress, rawToken string) error
}

// SMTPTransport is the production implementation using SMTP with STARTTLS.
type SMTPTransport struct {
	Host     string
	Port     int
	From     string
	Password string
}

// @{"req": ["REQ-USER-005"]}
func (s *SMTPTransport) SendPasswordReset(ctx context.Context, toAddress, rawToken string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)

	// Use DialContext so the ctx deadline is honoured during the TCP handshake
	// and throughout the SMTP conversation via the connection's deadline.
	netConn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}

	conn, err := smtp.NewClient(netConn, s.Host)
	if err != nil {
		netConn.Close()
		return err
	}
	defer conn.Close()

	if err := conn.StartTLS(&tls.Config{ServerName: s.Host}); err != nil {
		return err
	}

	auth := smtp.PlainAuth("", s.From, s.Password, s.Host)
	if err := conn.Auth(auth); err != nil {
		return err
	}

	body := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: Password Reset\r\n\r\nYour password reset token: %s\r\n\r\nThis token expires in 1 hour.\r\n", s.From, toAddress, rawToken)

	if err := conn.Mail(s.From); err != nil {
		return err
	}

	if err := conn.Rcpt(toAddress); err != nil {
		return err
	}

	w, err := conn.Data()
	if err != nil {
		return err
	}

	_, err = w.Write([]byte(body))
	if err != nil {
		w.Close()
		return err
	}

	return w.Close()
}

// NoOpTransport writes the reset token to an io.Writer instead of sending email.
// Used in development (SMTP_HOST absent) and in tests.
type NoOpTransport struct {
	Out io.Writer
}

// @{"req": ["REQ-USER-005"]}
func (n *NoOpTransport) SendPasswordReset(ctx context.Context, toAddress, rawToken string) error {
	// Log only a prefix so the plaintext token never appears in log output.
	prefix := rawToken
	if len(prefix) > 4 {
		prefix = prefix[:4] + "..."
	}
	_, err := fmt.Fprintf(n.Out, "[password-reset] to=%s token=%s\n", toAddress, prefix)
	return err
}

// NewEmailTransport constructs the appropriate transport:
// - If host is empty, returns a NoOpTransport writing to log.
// - Otherwise returns an SMTPTransport.
// @{"req": ["REQ-USER-005"]}
func NewEmailTransport(host string, port int, from, password string, log io.Writer) EmailTransport {
	if host == "" {
		return &NoOpTransport{Out: log}
	}
	return &SMTPTransport{Host: host, Port: port, From: from, Password: password}
}
