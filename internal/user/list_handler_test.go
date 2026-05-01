package user

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// stubLister is a fake userLister that returns a pre-configured slice.
// It records the last role and limit it was called with so tests can assert
// those values were passed through correctly.
//
// @{"req": ["REQ-USER-001", "REQ-USER-002"]}
type stubLister struct {
	users     []UserRow
	lastRole  string
	lastLimit int
	err       error
}

// @{"req": ["REQ-USER-001", "REQ-USER-002"]}
func (s *stubLister) ListUsers(_ context.Context, role string, limit int) ([]UserRow, error) {
	s.lastRole = role
	s.lastLimit = limit
	if s.err != nil {
		return nil, s.err
	}
	if role == "" {
		return s.users, nil
	}
	var filtered []UserRow
	for _, u := range s.users {
		if u.Role == role {
			filtered = append(filtered, u)
		}
	}
	return filtered, nil
}

// makeListRequest sends GET /users?<query> through a router wired with the stub
// lister. No real DB or auth middleware is used.
//
// @{"req": ["REQ-USER-001", "REQ-USER-002"]}
func makeListRequest(t *testing.T, query string, lister userLister) *httptest.ResponseRecorder {
	t.Helper()

	h := &Handler{lister: lister}

	req := httptest.NewRequest(http.MethodGet, "/"+query, nil)
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	r.Get("/", h.listUsers)
	r.ServeHTTP(rr, req)
	return rr
}

func fixedUsers() []UserRow {
	now := time.Now()
	return []UserRow{
		{ID: uuid.New(), Username: "alice", Role: "admin", IsActive: true, CreatedAt: now},
		{ID: uuid.New(), Username: "bob", Role: "student", IsActive: true, CreatedAt: now},
		{ID: uuid.New(), Username: "carol", Role: "student", IsActive: false, CreatedAt: now},
	}
}

// @{"req": ["REQ-USER-001", "REQ-USER-002"]}
func TestListUsers_NoFilter(t *testing.T) {
	stub := &stubLister{users: fixedUsers()}
	rr := makeListRequest(t, "", stub)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Users []map[string]interface{} `json:"users"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(resp.Users) != 3 {
		t.Errorf("expected 3 users, got %d", len(resp.Users))
	}

	// Default limit of 50 must be passed to the lister.
	if stub.lastLimit != 50 {
		t.Errorf("expected default limit 50, got %d", stub.lastLimit)
	}
	if stub.lastRole != "" {
		t.Errorf("expected empty role filter, got %q", stub.lastRole)
	}

	// Every item must contain the required fields.
	for i, u := range resp.Users {
		for _, field := range []string{"id", "username", "role", "is_active", "created_at"} {
			if u[field] == nil {
				t.Errorf("user[%d] missing field %q", i, field)
			}
		}
	}
}

// @{"req": ["REQ-USER-001", "REQ-USER-002"]}
func TestListUsers_RoleFilterStudent(t *testing.T) {
	stub := &stubLister{users: fixedUsers()}
	rr := makeListRequest(t, "?role=student", stub)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Users []map[string]interface{} `json:"users"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(resp.Users) != 2 {
		t.Errorf("expected 2 student users, got %d", len(resp.Users))
	}
	for _, u := range resp.Users {
		if u["role"] != "student" {
			t.Errorf("expected role 'student', got %v", u["role"])
		}
	}

	if stub.lastRole != "student" {
		t.Errorf("expected lister called with role 'student', got %q", stub.lastRole)
	}
}

// @{"req": ["REQ-USER-001", "REQ-USER-002"]}
func TestListUsers_RoleFilterAdmin(t *testing.T) {
	stub := &stubLister{users: fixedUsers()}
	rr := makeListRequest(t, "?role=admin", stub)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Users []map[string]interface{} `json:"users"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(resp.Users) != 1 {
		t.Errorf("expected 1 admin user, got %d", len(resp.Users))
	}
	if resp.Users[0]["role"] != "admin" {
		t.Errorf("expected role 'admin', got %v", resp.Users[0]["role"])
	}

	if stub.lastRole != "admin" {
		t.Errorf("expected lister called with role 'admin', got %q", stub.lastRole)
	}
}

// @{"req": ["REQ-USER-001", "REQ-USER-002"]}
func TestListUsers_InvalidRole(t *testing.T) {
	stub := &stubLister{users: fixedUsers()}
	rr := makeListRequest(t, "?role=superuser", stub)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid role, got %d", rr.Code)
	}
}

// @{"req": ["REQ-USER-001", "REQ-USER-002"]}
func TestListUsers_CustomLimit(t *testing.T) {
	stub := &stubLister{users: fixedUsers()}
	rr := makeListRequest(t, "?limit=10", stub)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if stub.lastLimit != 10 {
		t.Errorf("expected limit 10 forwarded to lister, got %d", stub.lastLimit)
	}
}

// @{"req": ["REQ-USER-001", "REQ-USER-002"]}
func TestListUsers_InvalidLimit(t *testing.T) {
	stub := &stubLister{users: fixedUsers()}
	rr := makeListRequest(t, "?limit=abc", stub)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid limit, got %d", rr.Code)
	}
}

// @{"req": ["REQ-USER-001", "REQ-USER-002"]}
func TestListUsers_EmptyResult(t *testing.T) {
	stub := &stubLister{users: nil}
	rr := makeListRequest(t, "", stub)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp struct {
		Users []map[string]interface{} `json:"users"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Must return an empty array, not null.
	if resp.Users == nil {
		t.Error("expected empty users array, got null")
	}
	if len(resp.Users) != 0 {
		t.Errorf("expected 0 users, got %d", len(resp.Users))
	}
}

// @{"req": ["REQ-USER-001", "REQ-USER-002"]}
func TestListUsers_LimitCapped(t *testing.T) {
	stub := &stubLister{users: fixedUsers()}
	rr := makeListRequest(t, "?limit=200", stub)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	if stub.lastLimit != 100 {
		t.Errorf("expected limit capped to 100, got %d", stub.lastLimit)
	}
}
