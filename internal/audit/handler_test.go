package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// @{"verifies": ["REQ-AUDIT-001"]}
func TestListHandler_ReturnsRows(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	adminID := createTestUser(ctx, t, "admin_"+uuid.New().String(), "hash", "admin")

	// Insert 3 entries
	for i := 1; i <= 3; i++ {
		entry := Entry{
			AdminID:    adminID,
			Action:     "ACTION_" + string(rune('0'+i)),
			TargetType: "type",
			TargetID:   nil,
			Payload:    map[string]any{"index": i},
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("failed to begin transaction: %v", err)
		}

		err = repo.Append(ctx, tx, entry)
		if err != nil {
			tx.Rollback(ctx)
			t.Fatalf("Append failed: %v", err)
		}

		err = tx.Commit(ctx)
		if err != nil {
			t.Fatalf("failed to commit transaction: %v", err)
		}
	}

	handler := NewHandler(repo)
	r := chi.NewRouter()
	r.Get("/", handler.list)

	req := httptest.NewRequest("GET", "/?limit=2", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	entries, ok := resp["entries"].([]any)
	if !ok {
		t.Fatal("expected entries to be an array")
	}

	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}

	nextBefore, ok := resp["next_before"].(float64)
	if !ok {
		t.Fatal("expected next_before to be a number")
	}

	if nextBefore == 0 {
		t.Error("expected next_before to be non-zero")
	}
}

// @{"verifies": ["REQ-AUDIT-001"]}
func TestListHandler_CursorPagination(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	if err := truncateAuditLog(ctx, pool); err != nil {
		t.Fatalf("failed to truncate audit_log: %v", err)
	}

	adminID := createTestUser(ctx, t, "admin_"+uuid.New().String(), "hash", "admin")

	// Insert exactly 3 entries
	for i := 1; i <= 3; i++ {
		entry := Entry{
			AdminID:    adminID,
			Action:     "ACTION_" + string(rune('0'+i)),
			TargetType: "type",
			TargetID:   nil,
			Payload:    map[string]any{"index": i},
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("failed to begin transaction: %v", err)
		}

		err = repo.Append(ctx, tx, entry)
		if err != nil {
			tx.Rollback(ctx)
			t.Fatalf("Append failed: %v", err)
		}

		err = tx.Commit(ctx)
		if err != nil {
			t.Fatalf("failed to commit transaction: %v", err)
		}
	}

	handler := NewHandler(repo)
	r := chi.NewRouter()
	r.Get("/", handler.list)

	// Page 1: fetch 2 entries
	req1 := httptest.NewRequest("GET", "/?limit=2", nil)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("page 1: expected 200, got %d", w1.Code)
	}

	var page1Resp map[string]any
	if err := json.NewDecoder(w1.Body).Decode(&page1Resp); err != nil {
		t.Fatalf("failed to decode page 1 response: %v", err)
	}

	page1Entries, ok := page1Resp["entries"].([]any)
	if !ok || len(page1Entries) != 2 {
		t.Errorf("expected 2 entries on page 1, got %d", len(page1Entries))
	}

	nextBefore, ok := page1Resp["next_before"].(float64)
	if !ok {
		t.Fatal("expected next_before to be a number")
	}

	// Page 2: use next_before from page 1
	req2URL := fmt.Sprintf("/?limit=2&before=%d", int64(nextBefore))
	req2 := httptest.NewRequest("GET", req2URL, nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("page 2: expected 200, got %d", w2.Code)
	}

	var page2Resp map[string]any
	if err := json.NewDecoder(w2.Body).Decode(&page2Resp); err != nil {
		t.Fatalf("failed to decode page 2 response: %v", err)
	}

	page2Entries, ok := page2Resp["entries"].([]any)
	if !ok || len(page2Entries) != 1 {
		t.Errorf("expected 1 entry on page 2, got %d", len(page2Entries))
	}
}

// @{"verifies": ["REQ-AUDIT-001"]}
func TestListHandler_InvalidLimit(t *testing.T) {
	// No DB needed — the handler rejects invalid limits before executing any query.
	repo := NewRepository(nil)

	handler := NewHandler(repo)
	r := chi.NewRouter()
	r.Get("/", handler.list)

	tests := []struct {
		name  string
		limit string
	}{
		{"limit 0", "?limit=0"},
		{"limit 201", "?limit=201"},
		{"limit negative", "?limit=-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/"+tt.limit, nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d", w.Code)
			}

			var resp map[string]string
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			errMsg, ok := resp["error"]
			if !ok || errMsg != "limit must be between 1 and 200" {
				t.Errorf("expected error message 'limit must be between 1 and 200', got %q", errMsg)
			}
		})
	}
}

// @{"verifies": ["REQ-AUDIT-002"]}
func TestVerifyHandler_ValidChain(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	adminID := createTestUser(ctx, t, "admin_"+uuid.New().String(), "hash", "admin")

	// Insert 3 entries to create a valid chain
	for i := 1; i <= 3; i++ {
		entry := Entry{
			AdminID:    adminID,
			Action:     "ACTION_" + string(rune('0'+i)),
			TargetType: "type",
			TargetID:   nil,
			Payload:    map[string]any{"index": i},
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("failed to begin transaction: %v", err)
		}

		err = repo.Append(ctx, tx, entry)
		if err != nil {
			tx.Rollback(ctx)
			t.Fatalf("Append failed: %v", err)
		}

		err = tx.Commit(ctx)
		if err != nil {
			t.Fatalf("failed to commit transaction: %v", err)
		}
	}

	handler := NewHandler(repo)
	r := chi.NewRouter()
	r.Get("/verify", handler.verify)

	req := httptest.NewRequest("GET", "/verify", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	valid, ok := resp["valid"].(bool)
	if !ok || !valid {
		t.Error("expected valid to be true")
	}

	firstBrokenID, ok := resp["first_broken_id"]
	if ok && firstBrokenID != nil {
		t.Error("expected first_broken_id to not be present for valid chain")
	}
}

// @{"verifies": ["REQ-AUDIT-002"]}
func TestVerifyHandler_BrokenChain(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	if err := truncateAuditLog(ctx, pool); err != nil {
		t.Fatalf("failed to truncate audit_log: %v", err)
	}

	adminID := createTestUser(ctx, t, "admin_"+uuid.New().String(), "hash", "admin")

	// Insert an entry
	entry := Entry{
		AdminID:    adminID,
		Action:     "TEST_ACTION",
		TargetType: "test",
		TargetID:   nil,
		Payload:    map[string]any{"test": true},
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("failed to begin transaction: %v", err)
	}

	err = repo.Append(ctx, tx, entry)
	if err != nil {
		tx.Rollback(ctx)
		t.Fatalf("Append failed: %v", err)
	}

	err = tx.Commit(ctx)
	if err != nil {
		t.Fatalf("failed to commit transaction: %v", err)
	}

	// Get the entry ID
	var entryID int64
	err = pool.QueryRow(ctx,
		`SELECT id FROM audit_log WHERE admin_id = $1 ORDER BY id DESC LIMIT 1`,
		adminID).
		Scan(&entryID)
	if err != nil {
		t.Fatalf("failed to get entry ID: %v", err)
	}

	// Corrupt the entry_hash
	_, err = pool.Exec(ctx,
		`UPDATE audit_log SET entry_hash = 'corrupted' WHERE id = $1`,
		entryID)
	if err != nil {
		t.Fatalf("failed to corrupt entry: %v", err)
	}

	handler := NewHandler(repo)
	r := chi.NewRouter()
	r.Get("/verify", handler.verify)

	req := httptest.NewRequest("GET", "/verify", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	valid, ok := resp["valid"].(bool)
	if !ok || valid {
		t.Error("expected valid to be false")
	}

	firstBrokenID, ok := resp["first_broken_id"].(float64)
	if !ok || int64(firstBrokenID) != entryID {
		t.Errorf("expected first_broken_id to be %d, got %v", entryID, firstBrokenID)
	}
}
