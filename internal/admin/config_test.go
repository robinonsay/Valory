package admin

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// newConfigServiceWithValues creates a ConfigService with pre-populated values for testing.
func newConfigServiceWithValues(vals map[string]string) *ConfigService {
	cs := &ConfigService{
		values: vals,
	}
	return cs
}

// @{"verifies": ["REQ-ADMIN-001"]}
func TestGetString_ReturnsValue(t *testing.T) {
	cs := newConfigServiceWithValues(map[string]string{
		"consent_version": "1.0",
	})

	result := cs.GetString("consent_version")
	if result != "1.0" {
		t.Errorf("expected '1.0', got '%s'", result)
	}
}

// @{"verifies": ["REQ-ADMIN-002"]}
func TestGetInt64_ParsesCorrectly(t *testing.T) {
	cs := newConfigServiceWithValues(map[string]string{
		"agent_retry_limit": "3",
	})

	result := cs.GetInt64("agent_retry_limit")
	if result != int64(3) {
		t.Errorf("expected 3, got %d", result)
	}
}

// @{"verifies": ["REQ-GRADE-002"]}
func TestGetFloat64_ParsesCorrectly(t *testing.T) {
	cs := newConfigServiceWithValues(map[string]string{
		"late_penalty_rate": "0.05",
	})

	result := cs.GetFloat64("late_penalty_rate")
	if result != 0.05 {
		t.Errorf("expected 0.05, got %f", result)
	}
}

// @{"verifies": ["REQ-ADMIN-001"]}
func TestGetInt64_ReturnsZeroOnMissing(t *testing.T) {
	cs := newConfigServiceWithValues(make(map[string]string))

	result := cs.GetInt64("nonexistent")
	if result != 0 {
		t.Errorf("expected 0, got %d", result)
	}
}

// @{"verifies": ["REQ-ADMIN-001"]}
func TestGetString_ReturnsEmptyOnMissing(t *testing.T) {
	cs := newConfigServiceWithValues(make(map[string]string))

	result := cs.GetString("nonexistent")
	if result != "" {
		t.Errorf("expected empty string, got '%s'", result)
	}
}

// @{"verifies": ["REQ-SECURITY-005"]}
func TestConsentVersionProvider_Implements(t *testing.T) {
	var _ ConsentVersionProvider = (*ConfigService)(nil)
}

// @{"verifies": ["REQ-ADMIN-001"]}
func TestConcurrentAccess(t *testing.T) {
	cs := newConfigServiceWithValues(map[string]string{
		"test_key": "value1",
	})

	var wg sync.WaitGroup
	errors := make(chan error, 10)

	// Launch 10 goroutines reading concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = cs.GetString("test_key")
			}
		}()
	}

	// Main goroutine updates the map while readers are active
	for i := 0; i < 10; i++ {
		cs.mu.Lock()
		cs.values["test_key"] = "updated"
		cs.mu.Unlock()
		time.Sleep(1 * time.Millisecond)
	}

	wg.Wait()
	close(errors)

	// If there were any errors, they would have been caught by the race detector
	// This test passes as long as no race is detected and no panic occurs
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent access error: %v", err)
		}
	}
}

// @{"verifies": ["REQ-ADMIN-001"]}
func TestLoad_ReadsSeededDefaults(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("failed to connect to database: %v", err)
	}
	defer pool.Close()

	// Ensure the table and required rows exist independently of migration order.
	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS system_config (
			key        VARCHAR(120) PRIMARY KEY,
			value      TEXT         NOT NULL,
			updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		)`)
	if err != nil {
		t.Fatalf("failed to create system_config: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO system_config (key, value) VALUES
			('consent_version',   '1.0'),
			('agent_retry_limit', '3')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	if err != nil {
		t.Fatalf("failed to seed system_config: %v", err)
	}

	cs := NewConfigService(pool)
	if err := cs.Load(ctx); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	consentVersion := cs.GetString("consent_version")
	if consentVersion != "1.0" {
		t.Errorf("expected consent_version='1.0', got '%s'", consentVersion)
	}

	retryLimit := cs.GetInt64("agent_retry_limit")
	if retryLimit != 3 {
		t.Errorf("expected agent_retry_limit=3, got %d", retryLimit)
	}
}
