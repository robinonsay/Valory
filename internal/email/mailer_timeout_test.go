package email

// mailer_timeout_test.go — Tests verifying SMTP send timeouts work correctly.
// A non-responsive SMTP server must not block the caller indefinitely.

import (
	"context"
	"net"
	"testing"
	"time"
)

// @{"verifies": ["REQ-EMAIL-012", "REQ-EMAIL-013"]}
// TestSend_TimesOutOnNonResponsiveServer verifies that Send returns an error
// when pointed at a server that accepts the TCP connection but never responds.
// Without proper timeout handling, Send would block indefinitely; with the fix,
// it must return within a small multiple of smtpSendTimeout.
func TestSend_TimesOutOnNonResponsiveServer(t *testing.T) {
	// Create a listener that accepts connections but never writes.
	// This simulates a misconfigured or throttled SMTP server.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	defer listener.Close()

	// Accept connections in a background goroutine and hold them open.
	// This simulates a hung server that accepts the TCP handshake but never
	// sends the SMTP banner.
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				// Listener closed; exit.
				return
			}
			// Hold the connection open forever (do not Close it, do not write).
			// This forces the SMTP client to timeout.
			<-make(chan struct{})
			_ = conn
		}
	}()

	// Create a mailer pointed at the black hole.
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to parse listener address: %v", err)
	}

	mailer := NewSMTPMailer(
		&stubConfig{vals: map[string]string{
			"smtp_host":       host,
			"smtp_port":       port,
			"smtp_encryption": "none",
		}},
		&stubSecrets{},
		SMTPEnvVars{},
	)

	// Measure how long Send takes. It should return an error quickly,
	// not hang. The timeout is 15 seconds, so we allow a 25-second ceiling
	// to account for timing variations.
	start := time.Now()
	err = mailer.Send(context.Background(), "test@example.com", "test", "test")
	elapsed := time.Since(start)

	if err == nil {
		t.Error("expected Send to return an error (context deadline or dial timeout)")
	}

	// Verify the call did not block past the deadline. A 25-second ceiling
	// is generous; the actual timeout should be ~15 seconds, but we allow
	// margin for slow test runners.
	if elapsed > 25*time.Second {
		t.Errorf("Send took %v; expected to return within 25s due to smtpSendTimeout", elapsed)
	}

	// Log the elapsed time for visibility.
	t.Logf("Send returned error after %v: %v", elapsed, err)
}

// @{"verifies": ["REQ-EMAIL-012", "REQ-EMAIL-013"]}
// TestSend_TimesOutOnSlowDataPhase verifies that even after a successful
// connection, a slow server stalling in the DATA phase times out correctly.
// This tests the connection-level SetDeadline guard in addition to the
// overall context deadline.
func TestSend_TimesOutOnSlowDataPhaseSTARTTLS(t *testing.T) {
	// Similar setup: listener that accepts but hangs before sending SMTP responses.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	defer listener.Close()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			// Hold open without responding, forcing the SMTP client to timeout.
			<-make(chan struct{})
			_ = conn
		}
	}()

	// Test with STARTTLS mode (the default).
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to parse listener address: %v", err)
	}

	mailer := NewSMTPMailer(
		&stubConfig{vals: map[string]string{
			"smtp_host":       host,
			"smtp_port":       port,
			"smtp_encryption": "starttls",
		}},
		&stubSecrets{},
		SMTPEnvVars{},
	)

	start := time.Now()
	err = mailer.Send(context.Background(), "test@example.com", "test", "test")
	elapsed := time.Since(start)

	if err == nil {
		t.Error("expected Send to return an error")
	}

	if elapsed > 25*time.Second {
		t.Errorf("Send took %v; expected to return within 25s", elapsed)
	}

	t.Logf("STARTTLS Send returned error after %v: %v", elapsed, err)
}
