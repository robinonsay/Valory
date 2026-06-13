//go:build integration

// mailpit.go — Test helper for asserting email delivery via the Mailpit
// REST API (axllent/mailpit). Used exclusively by integration tests that
// target the real SMTP sink added to docker-compose.test.yml in Sprint 19.
//
// Skip philosophy mirrors db.IntegrationPool: if MAILPIT_API is not set and
// the default endpoint is unreachable, tests that call MailpitPool are skipped
// rather than failed. When MAILPIT_API is explicitly set (as the Makefile's
// test-integration target will arrange), failures are fatal — the operator has
// deliberately provisioned the stack and expects the tests to run.
//
// SDD reference: SDD-019-email-subsystem.md §10.2
package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

const (
	// defaultMailpitAPI is the REST API base URL for the Mailpit container
	// started by docker-compose.test.yml. The host port 18025 is chosen in the
	// SDD to avoid collisions with common local services.
	defaultMailpitAPI = "http://localhost:18025"

	// pollInterval is how often WaitForMessage re-queries the Mailpit API while
	// waiting for a message to appear. 100 ms keeps total wait short while
	// avoiding a tight-loop that floods logs.
	pollInterval = 100 * time.Millisecond

	// pollTimeout is the maximum time WaitForMessage will wait before failing.
	// 5 seconds is generous for loopback SMTP — local delivery is typically
	// sub-100 ms. A longer timeout would slow the suite when the mailer is broken.
	pollTimeout = 5 * time.Second
)

// MailpitMessage represents one message returned by the Mailpit /api/v1/messages
// endpoint. Only the fields consumed by test assertions are decoded; the full
// message schema is richer but not needed here.
//
// Shape (Mailpit v1 API):
//
//	{
//	  "ID":      "abc123",
//	  "From":    { "Address": "test@valory.test" },
//	  "To":      [{ "Address": "student@example.com" }],
//	  "Subject": "Password Reset",
//	  "Created": "2026-06-12T..."
//	}
type MailpitMessage struct {
	ID      string           `json:"ID"`
	From    mailpitAddress   `json:"From"`
	To      []mailpitAddress `json:"To"`
	Subject string           `json:"Subject"`
	Created string           `json:"Created"`
}

type mailpitAddress struct {
	Address string `json:"Address"`
}

// mailpitListResponse is the top-level shape returned by GET /api/v1/messages.
type mailpitListResponse struct {
	Messages []MailpitMessage `json:"messages"`
	Total    int              `json:"total"`
}

// MailpitAPIBase returns the base URL for the Mailpit REST API, reading
// MAILPIT_API from the environment or falling back to the default.
// Tests should call this rather than hard-coding the URL.
func MailpitAPIBase() string {
	if v := os.Getenv("MAILPIT_API"); v != "" {
		return v
	}
	return defaultMailpitAPI
}

// CheckMailpitReachable probes the Mailpit API and skips t if unreachable.
// Call this at the top of any test that uses Mailpit helpers. It applies the
// same skip-vs-fatal logic as db.IntegrationPool:
//   - MAILPIT_API env set → fatal (stack is deliberately provisioned)
//   - MAILPIT_API env absent → skip (developer machine without Docker is fine)
func CheckMailpitReachable(t *testing.T) {
	t.Helper()

	apiBase := MailpitAPIBase()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/api/v1/info", nil)
	if err != nil {
		if os.Getenv("MAILPIT_API") != "" {
			t.Fatalf("mailpit: cannot construct probe request: %v", err)
		}
		t.Skipf("mailpit: unreachable at %s — run `make test-integration` to start the stack: %v", apiBase, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if os.Getenv("MAILPIT_API") != "" {
			t.Fatalf("mailpit: unreachable at %s (MAILPIT_API is set): %v", apiBase, err)
		}
		t.Skipf("mailpit: unreachable at %s — run `make test-integration` to start the stack: %v", apiBase, err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if os.Getenv("MAILPIT_API") != "" {
			t.Fatalf("mailpit: probe returned %d at %s (MAILPIT_API is set)", resp.StatusCode, apiBase)
		}
		t.Skipf("mailpit: probe returned %d at %s — run `make test-integration`", resp.StatusCode, apiBase)
	}
}

// ClearMessages deletes all messages from the Mailpit inbox, giving the
// next test a clean slate. Call this at the start of each test case that
// sends email so prior test runs do not produce false-positive matches.
//
// t.Fatal is called on any HTTP error so callers need not check the return.
func ClearMessages(t *testing.T, apiBase string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, apiBase+"/api/v1/messages", nil)
	if err != nil {
		t.Fatalf("mailpit: ClearMessages: build request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("mailpit: ClearMessages: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mailpit: ClearMessages: unexpected status %d", resp.StatusCode)
	}
}

// WaitForMessage polls the Mailpit REST API until a message arrives whose
// To address and Subject match toAddr and subject, or until pollTimeout elapses.
//
// Matching is done by scanning all messages in the inbox — the inbox is small
// (bounded by ClearMessages at test setup) so a linear scan is fine.
//
// The function returns the matched MailpitMessage so callers can assert
// additional fields (e.g. From address). t.Fatal is called if no matching
// message appears within pollTimeout, so callers need not check the return.
func WaitForMessage(t *testing.T, apiBase, toAddr, subject string) MailpitMessage {
	t.Helper()

	deadline := time.Now().Add(pollTimeout)
	for time.Now().Before(deadline) {
		msgs, err := listMessages(apiBase)
		if err != nil {
			// Transient; keep polling until the deadline.
			time.Sleep(pollInterval)
			continue
		}

		for _, m := range msgs {
			if m.Subject != subject {
				continue
			}
			for _, addr := range m.To {
				if addr.Address == toAddr {
					return m
				}
			}
		}
		time.Sleep(pollInterval)
	}

	// Fetch final state for a helpful failure message.
	msgs, _ := listMessages(apiBase)
	t.Fatalf("mailpit: timed out after %s waiting for message to=%q subject=%q; inbox has %d message(s): %v",
		pollTimeout, toAddr, subject, len(msgs), summarise(msgs))
	return MailpitMessage{} // unreachable; keeps the compiler happy
}

// GetMessageBody fetches the full text body of a message by its Mailpit ID.
// Returns the raw plain-text body for content assertions.
func GetMessageBody(t *testing.T, apiBase, messageID string) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/api/v1/message/%s", apiBase, messageID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("mailpit: GetMessageBody: build request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("mailpit: GetMessageBody: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mailpit: GetMessageBody: unexpected status %d for id=%s", resp.StatusCode, messageID)
	}

	var msg struct {
		Text string `json:"Text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		t.Fatalf("mailpit: GetMessageBody: decode: %v", err)
	}
	return msg.Text
}

// listMessages queries GET /api/v1/messages and returns the message slice.
func listMessages(apiBase string) ([]MailpitMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/api/v1/messages", nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("mailpit: list messages: status %d: %s", resp.StatusCode, body)
	}

	var result mailpitListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("mailpit: list messages: decode: %w", err)
	}
	return result.Messages, nil
}

// summarise returns a compact human-readable representation of the inbox for
// use in failure messages.
func summarise(msgs []MailpitMessage) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		to := ""
		if len(m.To) > 0 {
			to = m.To[0].Address
		}
		out[i] = fmt.Sprintf("{to=%q subject=%q}", to, m.Subject)
	}
	return out
}
