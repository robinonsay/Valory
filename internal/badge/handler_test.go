//go:build testing

package badge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/valory/valory/internal/auth"
)

// buildRouter wires the Handler into a chi router for testing.
func buildRouter(svc *Service) chi.Router {
	r := chi.NewRouter()
	h := NewHandler(svc)
	h.Routes(r)
	return r
}

// @{"verifies": ["REQ-BADGE-001"]}
func TestListBadges_OK(t *testing.T) {
	stub := newStubBadgeRepo()
	svc := newServiceWithStub(stub)
	router := buildRouter(svc)

	userID := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/badges", nil)
	req = req.WithContext(auth.SetTestContext(req.Context(), [16]byte(userID), "student"))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var badges []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&badges); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(badges) != 4 {
		t.Errorf("expected 4 badge definitions, got %d", len(badges))
	}
}

// @{"verifies": ["REQ-BADGE-001"]}
func TestListBadges_Unauthenticated(t *testing.T) {
	stub := newStubBadgeRepo()
	svc := newServiceWithStub(stub)
	router := buildRouter(svc)

	// No auth context injected — handler must reject with 401.
	req := httptest.NewRequest(http.MethodGet, "/badges", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// @{"verifies": ["REQ-BADGE-001"]}
func TestMyBadges_OK(t *testing.T) {
	stub := newStubBadgeRepo()
	svc := newServiceWithStub(stub)
	router := buildRouter(svc)

	studentID := uuid.New()
	// Pre-award a badge directly via the stub so GetStudentBadges returns one row.
	badgeID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	_, err := stub.AwardBadge(nil, studentID, badgeID) //nolint:staticcheck
	if err != nil {
		t.Fatalf("stub AwardBadge: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/users/me/badges", nil)
	req = req.WithContext(auth.SetTestContext(req.Context(), [16]byte(studentID), "student"))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var items []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&items); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("expected 1 badge for student, got %d", len(items))
	}
}

// @{"verifies": ["REQ-BADGE-001"]}
func TestMyBadges_Unauthenticated(t *testing.T) {
	stub := newStubBadgeRepo()
	svc := newServiceWithStub(stub)
	router := buildRouter(svc)

	// No auth context — handler must reject with 401.
	req := httptest.NewRequest(http.MethodGet, "/users/me/badges", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}
