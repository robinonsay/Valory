//go:build integration

// profile_http_integration_test.go — HTTP-handler-level integration tests for
// the profile and onboarding endpoints.
//
// These tests exercise the full path: real auth middleware → real HTTP handlers
// → real PostgreSQL with FORCE RLS. They mirror the pattern established in
// internal/admin/assignment_http_rls_integration_test.go.
//
// AI-gating policy (critical):
//   - POST /onboarding/start:  calls the Anthropic API to produce the first
//     question.  Tests that invoke this endpoint are AI-gated and must be
//     skipped when VALORY_TEST_ANTHROPIC_KEY is absent.
//   - POST /onboarding/advance: also calls Anthropic.  AI-gated.
//   - POST /onboarding/complete: calls Anthropic for summarization.  AI-gated.
//   - POST /onboarding/skip:  NO Anthropic call — fully AI-free.
//   - GET  /profile:           NO Anthropic call — AI-free.
//   - PUT  /profile:           NO Anthropic call — AI-free.
//   - GET  /auth/session:      NO Anthropic call — AI-free.
//
// All AI-free paths are covered here.  The AI-gated paths (start, advance,
// complete) require a live Anthropic API key; those tests are gated behind
// t.Skipf when VALORY_TEST_ANTHROPIC_KEY is absent.
//
// Run:
//
//	make test-integration
//	# or manually:
//	export PATH=$PATH:/usr/local/go/bin
//	VALORY_TEST_DATABASE_URL=postgres://valory_test:valory_test@localhost:55432/valory_test?sslmode=disable \
//	  go test -tags integration -count=1 -p 1 ./internal/profile/...
//
// @{"verifies": ["REQ-PROFILE-004", "REQ-PROFILE-010", "REQ-PROFILE-014", "REQ-PROFILE-015", "REQ-PROFILE-016", "REQ-PROFILE-017", "REQ-PROFILE-019", "REQ-PROFILE-020"]}
package profile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	authpkg "github.com/valory/valory/internal/auth"
	internaldb "github.com/valory/valory/internal/db"
)

// ---------------------------------------------------------------------------
// HTTP stack builder
// ---------------------------------------------------------------------------

// emptySecrets satisfies profile.SecretResolver for tests that do not make
// Anthropic calls.  All AI-gated tests skip when VALORY_TEST_ANTHROPIC_KEY is
// absent and wire a real key instead.
type emptySecrets struct{}

func (e *emptySecrets) Get(_ context.Context, _ string) string { return "" }

// envSecrets resolves the Anthropic API key from VALORY_TEST_ANTHROPIC_KEY.
// Used only by AI-gated tests.
type envSecrets struct{}

func (e *envSecrets) Get(_ context.Context, name string) string {
	if name == "anthropic_api_key" {
		return os.Getenv("VALORY_TEST_ANTHROPIC_KEY")
	}
	return ""
}

// buildProfileHTTPStack wires the minimal router needed by the profile HTTP
// integration tests.  It follows the same pattern as buildAssignmentHTTPStack
// in internal/admin/assignment_http_rls_integration_test.go:
//
//   - Login endpoint: POST /api/v1/auth/login  (no auth middleware)
//   - Session endpoint: GET /api/v1/auth/session  (behind authMW)
//   - Profile endpoints: mounted at /api/v1/profile/...  (behind authMW, student role)
//
// secrets is the SecretResolver used by OnboardingService.  Pass emptySecrets
// for AI-free paths; envSecrets for AI-gated paths (requires VALORY_TEST_ANTHROPIC_KEY).
func buildProfileHTTPStack(t *testing.T, secrets SecretResolver) http.Handler {
	t.Helper()

	pool := internaldb.IntegrationPool(t)

	// Auth layer (mirrors main.go).
	authRepo := authpkg.NewRepository(pool)
	authSvc := authpkg.NewService(authRepo, 15*time.Minute, 24*time.Hour)
	authMW := authpkg.NewAuthMiddleware(
		authRepo,
		pool,
		24*time.Hour,
		nil, // consent gate disabled for tests
	)

	// Profile module: repo on plain pool, service with server pool for agent
	// writes.  The serverPool here is the integration pool (superuser), which is
	// acceptable because: (a) RLS correctness is already proven by the DB-level
	// RLS tests in profile_integration_test.go, and (b) the server-role policies
	// are tested via AcquireAsServer tests. This HTTP stack tests the handler and
	// service plumbing, not RLS isolation (that is covered in the other file).
	profileRepo := NewProfileRepository(pool)
	onboardingSvc := NewOnboardingService(profileRepo, pool, secrets)
	profileHandler := NewHandler(profileRepo, onboardingSvc, pool)

	authFullHandler := authpkg.NewHandlerFull(authSvc, authRepo, pool, 24*time.Hour, nil)

	outerRouter := chi.NewRouter()

	// Login and 2FA pre-session endpoints are unauthenticated.
	outerRouter.Route("/api/v1/auth", func(r chi.Router) {
		authFullHandler.Routes(r)
	})

	// Session endpoint and profile endpoints require a valid session.
	// authMW validates the bearer token, acquires a connection, and sets GUCs.
	outerRouter.Group(func(r chi.Router) {
		r.Use(authMW)

		// GET /api/v1/auth/session — student or admin (no role restriction).
		r.Get("/api/v1/auth/session", authFullHandler.GetSession)

		// Profile endpoints are student-only.
		r.Route("/api/v1/profile", func(r chi.Router) {
			r.Use(authpkg.RequireRole("student"))
			profileHandler.Routes(r)
		})
	})

	return outerRouter
}

// ---------------------------------------------------------------------------
// HTTP fixture helpers (mirroring assignment_http_rls_integration_test.go)
// ---------------------------------------------------------------------------

// profileHTTPCreateStudentUser creates a student in the DB and logs in,
// returning the student's UUID and a bearer token.
func profileHTTPCreateStudentUser(ctx context.Context, t *testing.T, handler http.Handler) (uuid.UUID, string) {
	t.Helper()
	pool := internaldb.IntegrationPool(t)
	username := fmt.Sprintf("prof_http_student_%s", uuid.New().String()[:8])
	password := "TestPass1234!"

	hash, err := authpkg.HashPassword(password)
	if err != nil {
		t.Fatalf("profileHTTPCreateStudentUser hash: %v", err)
	}

	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, 'student') RETURNING id`,
		username, hash,
	).Scan(&id); err != nil {
		t.Fatalf("profileHTTPCreateStudentUser insert: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("profileHTTPCreateStudentUser login: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("profileHTTPCreateStudentUser decode: %v", err)
	}
	if resp.Token == "" {
		t.Fatal("profileHTTPCreateStudentUser: empty token in response")
	}
	return id, resp.Token
}

// truncateProfileHTTPTables resets all tables needed for HTTP-level profile
// tests. Ordered child-to-parent to satisfy FK constraints.
func truncateProfileHTTPTables(t *testing.T) {
	t.Helper()
	internaldb.TruncateTables(t, internaldb.IntegrationPool(t),
		"onboarding_messages",
		"learning_profiles",
		"sessions",
		"login_attempts",
		"users",
	)
}

// doRequest is a thin wrapper that sends an HTTP request to the test handler,
// sets the Authorization header, and returns the recorded response.
func doRequest(handler http.Handler, method, path, token string, body interface{}) *httptest.ResponseRecorder {
	var bodyReader *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		bodyReader = bytes.NewReader(b)
	} else {
		bodyReader = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, path, bodyReader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// ---------------------------------------------------------------------------
// AI-free HTTP journey tests
// ---------------------------------------------------------------------------

// TestHTTP_Profile_GetProfile_Returns404_WhenNoProfile verifies that a freshly
// created student with no profile receives 404 from GET /api/v1/profile.
// This exercises the graceful-degradation path described in SDD-023 §4.1.
//
// @{"verifies": ["REQ-PROFILE-010", "REQ-PROFILE-004"]}
func TestHTTP_Profile_GetProfile_Returns404_WhenNoProfile(t *testing.T) {
	internaldb.IntegrationPool(t)
	truncateProfileHTTPTables(t)

	ctx := context.Background()
	handler := buildProfileHTTPStack(t, &emptySecrets{})

	_, token := profileHTTPCreateStudentUser(ctx, t, handler)

	rr := doRequest(handler, http.MethodGet, "/api/v1/profile", token, nil)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("GET /api/v1/profile (no profile): expected 404, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode 404 response: %v", err)
	}
	if resp["error"] != "NOT_FOUND" {
		t.Errorf("expected error=NOT_FOUND in body, got %v", resp["error"])
	}
}

// TestHTTP_Profile_PutProfile_CreatesSummary verifies the full PUT round-trip:
// student posts a manual summary → 200 with the saved row → GET returns it.
//
// @{"verifies": ["REQ-PROFILE-004", "REQ-PROFILE-016", "REQ-PROFILE-002", "REQ-PROFILE-003"]}
func TestHTTP_Profile_PutProfile_CreatesSummary(t *testing.T) {
	internaldb.IntegrationPool(t)
	truncateProfileHTTPTables(t)

	ctx := context.Background()
	handler := buildProfileHTTPStack(t, &emptySecrets{})

	studentID, token := profileHTTPCreateStudentUser(ctx, t, handler)

	const wantSummary = "I prefer example-first explanations and can study 8 hours per week."

	putRR := doRequest(handler, http.MethodPut, "/api/v1/profile", token, map[string]string{
		"summary": wantSummary,
	})
	if putRR.Code != http.StatusOK {
		t.Fatalf("PUT /api/v1/profile: expected 200, got %d: %s", putRR.Code, putRR.Body.String())
	}

	var putResp struct {
		StudentID string `json:"student_id"`
		Summary   string `json:"summary"`
		Source    string `json:"source"`
	}
	if err := json.NewDecoder(putRR.Body).Decode(&putResp); err != nil {
		t.Fatalf("decode PUT response: %v", err)
	}
	if putResp.Summary != wantSummary {
		t.Errorf("PUT response summary: got %q, want %q", putResp.Summary, wantSummary)
	}
	if putResp.Source != "manual_edit" {
		t.Errorf("PUT response source: got %q, want manual_edit", putResp.Source)
	}
	if putResp.StudentID == "" {
		t.Error("PUT response student_id must be non-empty")
	}

	// Confirm GET returns the same row.
	getRR := doRequest(handler, http.MethodGet, "/api/v1/profile", token, nil)
	if getRR.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/profile after PUT: expected 200, got %d: %s", getRR.Code, getRR.Body.String())
	}

	var getResp struct {
		StudentID string `json:"student_id"`
		Summary   string `json:"summary"`
		Source    string `json:"source"`
	}
	if err := json.NewDecoder(getRR.Body).Decode(&getResp); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if getResp.Summary != wantSummary {
		t.Errorf("GET summary: got %q, want %q", getResp.Summary, wantSummary)
	}
	if getResp.Source != "manual_edit" {
		t.Errorf("GET source: got %q, want manual_edit", getResp.Source)
	}
	// REQ-PROFILE-004: the student_id in the response must match the logged-in
	// student's UUID.
	if getResp.StudentID != studentID.String() {
		t.Errorf("GET student_id: got %q, want %q", getResp.StudentID, studentID.String())
	}
}

// TestHTTP_Profile_PutProfile_ValidationErrors verifies that summary validation
// (empty or > 2000 chars) returns 400 BAD_REQUEST.
//
// @{"verifies": ["REQ-PROFILE-004", "REQ-PROFILE-016"]}
func TestHTTP_Profile_PutProfile_ValidationErrors(t *testing.T) {
	internaldb.IntegrationPool(t)
	truncateProfileHTTPTables(t)

	ctx := context.Background()
	handler := buildProfileHTTPStack(t, &emptySecrets{})

	_, token := profileHTTPCreateStudentUser(ctx, t, handler)

	cases := []struct {
		name    string
		summary string
	}{
		{"EmptySummary_Returns400", ""},
		{"TooLongSummary_Returns400", string(make([]byte, 2001))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := doRequest(handler, http.MethodPut, "/api/v1/profile", token, map[string]string{
				"summary": tc.summary,
			})
			if rr.Code != http.StatusBadRequest {
				t.Errorf("PUT /api/v1/profile %s: expected 400, got %d: %s",
					tc.name, rr.Code, rr.Body.String())
			}
			var resp map[string]interface{}
			if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if resp["error"] != "BAD_REQUEST" {
				t.Errorf("expected error=BAD_REQUEST, got %v", resp["error"])
			}
		})
	}
}

// TestHTTP_Profile_PutProfile_UpdatesSummary verifies that a second PUT
// overwrites the existing profile in place (upsert behaviour).
//
// @{"verifies": ["REQ-PROFILE-004", "REQ-PROFILE-016"]}
func TestHTTP_Profile_PutProfile_UpdatesSummary(t *testing.T) {
	internaldb.IntegrationPool(t)
	truncateProfileHTTPTables(t)

	ctx := context.Background()
	handler := buildProfileHTTPStack(t, &emptySecrets{})

	_, token := profileHTTPCreateStudentUser(ctx, t, handler)

	const firstSummary = "First version of my profile."
	const secondSummary = "Updated version — now I prefer theory-first explanations."

	// PUT once.
	rr1 := doRequest(handler, http.MethodPut, "/api/v1/profile", token, map[string]string{"summary": firstSummary})
	if rr1.Code != http.StatusOK {
		t.Fatalf("first PUT: expected 200, got %d: %s", rr1.Code, rr1.Body.String())
	}

	// PUT again with a new summary (manual_edit source).
	rr2 := doRequest(handler, http.MethodPut, "/api/v1/profile", token, map[string]string{"summary": secondSummary})
	if rr2.Code != http.StatusOK {
		t.Fatalf("second PUT: expected 200, got %d: %s", rr2.Code, rr2.Body.String())
	}

	var resp struct {
		Summary string `json:"summary"`
		Source  string `json:"source"`
	}
	if err := json.NewDecoder(rr2.Body).Decode(&resp); err != nil {
		t.Fatalf("decode second PUT response: %v", err)
	}
	if resp.Summary != secondSummary {
		t.Errorf("after second PUT: summary = %q, want %q", resp.Summary, secondSummary)
	}
	if resp.Source != "manual_edit" {
		t.Errorf("after second PUT: source = %q, want manual_edit", resp.Source)
	}
}

// TestHTTP_OnboardingSkip_SetsOnboardingPrompted verifies the skip path:
// POST /api/v1/profile/onboarding/skip returns {"skipped": true} and
// subsequent GET /api/v1/auth/session shows onboarding_prompted: true.
//
// This is the critical AI-free test for the onboarding soft gate (SDD-023 §4.6,
// §8.4 skip path). No Anthropic call is made in this path.
//
// @{"verifies": ["REQ-PROFILE-014", "REQ-PROFILE-015"]}
func TestHTTP_OnboardingSkip_SetsOnboardingPrompted(t *testing.T) {
	internaldb.IntegrationPool(t)
	truncateProfileHTTPTables(t)

	ctx := context.Background()
	handler := buildProfileHTTPStack(t, &emptySecrets{})

	studentID, token := profileHTTPCreateStudentUser(ctx, t, handler)
	pool := internaldb.IntegrationPool(t)

	// Confirm onboarding_prompted = false before skip (default from migration).
	var beforePrompted bool
	if err := pool.QueryRow(ctx,
		`SELECT onboarding_prompted FROM users WHERE id = $1`, studentID,
	).Scan(&beforePrompted); err != nil {
		t.Fatalf("SELECT onboarding_prompted before skip: %v", err)
	}
	if beforePrompted {
		t.Fatal("onboarding_prompted must be false before skip")
	}

	// Call POST /api/v1/profile/onboarding/skip.
	skipRR := doRequest(handler, http.MethodPost, "/api/v1/profile/onboarding/skip", token, nil)
	if skipRR.Code != http.StatusOK {
		t.Fatalf("POST /onboarding/skip: expected 200, got %d: %s", skipRR.Code, skipRR.Body.String())
	}

	var skipResp map[string]interface{}
	if err := json.NewDecoder(skipRR.Body).Decode(&skipResp); err != nil {
		t.Fatalf("decode skip response: %v", err)
	}
	if skipResp["skipped"] != true {
		t.Errorf("skip response: expected skipped=true, got %v", skipResp["skipped"])
	}

	// DB: onboarding_prompted must now be true.
	var afterPrompted bool
	if err := pool.QueryRow(ctx,
		`SELECT onboarding_prompted FROM users WHERE id = $1`, studentID,
	).Scan(&afterPrompted); err != nil {
		t.Fatalf("SELECT onboarding_prompted after skip: %v", err)
	}
	if !afterPrompted {
		t.Error("onboarding_prompted must be true after skip (REQ-PROFILE-014, REQ-PROFILE-015)")
	}
}

// TestHTTP_OnboardingSkip_ProfileRemainsAbsent verifies that skipping onboarding
// does NOT create a learning_profiles row.  After skip, GET /profile must still
// return 404 (graceful degradation: REQ-PROFILE-010).
//
// @{"verifies": ["REQ-PROFILE-010", "REQ-PROFILE-015"]}
func TestHTTP_OnboardingSkip_ProfileRemainsAbsent(t *testing.T) {
	internaldb.IntegrationPool(t)
	truncateProfileHTTPTables(t)

	ctx := context.Background()
	handler := buildProfileHTTPStack(t, &emptySecrets{})

	studentID, token := profileHTTPCreateStudentUser(ctx, t, handler)
	pool := internaldb.IntegrationPool(t)

	// Skip onboarding.
	skipRR := doRequest(handler, http.MethodPost, "/api/v1/profile/onboarding/skip", token, nil)
	if skipRR.Code != http.StatusOK {
		t.Fatalf("POST /onboarding/skip: expected 200, got %d: %s", skipRR.Code, skipRR.Body.String())
	}

	// GET /profile must return 404 — no row was created.
	getRR := doRequest(handler, http.MethodGet, "/api/v1/profile", token, nil)
	if getRR.Code != http.StatusNotFound {
		t.Fatalf("GET /profile after skip: expected 404 (no row created), got %d: %s",
			getRR.Code, getRR.Body.String())
	}

	// Double-confirm at the DB level: no learning_profiles row for this student.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM learning_profiles WHERE student_id = $1`, studentID,
	).Scan(&count); err != nil {
		t.Fatalf("COUNT learning_profiles after skip: %v", err)
	}
	if count != 0 {
		t.Errorf("learning_profiles must have 0 rows after skip (REQ-PROFILE-010), got %d", count)
	}
}

// TestHTTP_Session_IncludesOnboardingPrompted verifies that
// GET /api/v1/auth/session returns the onboarding_prompted flag.
//
// This test covers the SDD-023 §4.7 auth session amendment: the frontend router
// guard reads this flag on every SPA boot/reload without a separate round-trip.
// We check both states: before skip (false) and after skip (true).
//
// @{"verifies": ["REQ-PROFILE-014"]}
func TestHTTP_Session_IncludesOnboardingPrompted(t *testing.T) {
	internaldb.IntegrationPool(t)
	truncateProfileHTTPTables(t)

	ctx := context.Background()
	handler := buildProfileHTTPStack(t, &emptySecrets{})

	_, token := profileHTTPCreateStudentUser(ctx, t, handler)

	// Session before skip: onboarding_prompted must be false.
	sessionBefore := doRequest(handler, http.MethodGet, "/api/v1/auth/session", token, nil)
	if sessionBefore.Code != http.StatusOK {
		t.Fatalf("GET /auth/session before skip: expected 200, got %d: %s",
			sessionBefore.Code, sessionBefore.Body.String())
	}

	var beforeResp map[string]interface{}
	if err := json.NewDecoder(sessionBefore.Body).Decode(&beforeResp); err != nil {
		t.Fatalf("decode session before skip: %v", err)
	}
	// The field must exist in the response (SDD-023 §4.7).
	if _, exists := beforeResp["onboarding_prompted"]; !exists {
		t.Fatal("GET /auth/session response must contain onboarding_prompted field (SDD-023 §4.7)")
	}
	if beforeResp["onboarding_prompted"] != false {
		t.Errorf("onboarding_prompted before skip: want false, got %v", beforeResp["onboarding_prompted"])
	}

	// Skip onboarding.
	skipRR := doRequest(handler, http.MethodPost, "/api/v1/profile/onboarding/skip", token, nil)
	if skipRR.Code != http.StatusOK {
		t.Fatalf("POST /onboarding/skip: expected 200, got %d: %s", skipRR.Code, skipRR.Body.String())
	}

	// Session after skip: onboarding_prompted must now be true.
	// Need a fresh recorder since the token is still valid.
	sessionAfter := doRequest(handler, http.MethodGet, "/api/v1/auth/session", token, nil)
	if sessionAfter.Code != http.StatusOK {
		t.Fatalf("GET /auth/session after skip: expected 200, got %d: %s",
			sessionAfter.Code, sessionAfter.Body.String())
	}

	var afterResp map[string]interface{}
	if err := json.NewDecoder(sessionAfter.Body).Decode(&afterResp); err != nil {
		t.Fatalf("decode session after skip: %v", err)
	}
	if afterResp["onboarding_prompted"] != true {
		t.Errorf("onboarding_prompted after skip: want true, got %v", afterResp["onboarding_prompted"])
	}
}

// TestHTTP_PutProfile_Source_IsManualEdit verifies the source field semantics:
// PUT always stores source='manual_edit' regardless of any prior source value.
//
// @{"verifies": ["REQ-PROFILE-003", "REQ-PROFILE-016"]}
func TestHTTP_PutProfile_Source_IsManualEdit(t *testing.T) {
	internaldb.IntegrationPool(t)
	truncateProfileHTTPTables(t)

	ctx := context.Background()
	handler := buildProfileHTTPStack(t, &emptySecrets{})
	pool := internaldb.IntegrationPool(t)

	studentID, token := profileHTTPCreateStudentUser(ctx, t, handler)

	// Seed a profile with source='onboarding' directly (simulating what the
	// onboarding complete path would write via the server pool).
	if _, err := pool.Exec(ctx,
		`INSERT INTO learning_profiles (student_id, summary, source) VALUES ($1, $2, 'onboarding')`,
		studentID, "LLM-generated profile.",
	); err != nil {
		t.Fatalf("seed onboarding profile: %v", err)
	}

	// PUT overwrites with manual_edit source.
	rr := doRequest(handler, http.MethodPut, "/api/v1/profile", token, map[string]string{
		"summary": "Manually edited profile.",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT /api/v1/profile: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Source string `json:"source"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode PUT response: %v", err)
	}
	if resp.Source != "manual_edit" {
		t.Errorf("PUT /profile: source must be manual_edit, got %q (REQ-PROFILE-003)", resp.Source)
	}
}

// TestHTTP_GetProfile_Unauthorized_Returns401 verifies that the profile
// endpoint enforces authentication — an unauthenticated request must get 401.
//
// @{"verifies": ["REQ-PROFILE-004"]}
func TestHTTP_GetProfile_Unauthorized_Returns401(t *testing.T) {
	internaldb.IntegrationPool(t)
	truncateProfileHTTPTables(t)

	handler := buildProfileHTTPStack(t, &emptySecrets{})

	// No Authorization header — must be rejected at the auth middleware level.
	rr := doRequest(handler, http.MethodGet, "/api/v1/profile", "", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("GET /profile without auth: expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ---------------------------------------------------------------------------
// AI-gated HTTP tests
// ---------------------------------------------------------------------------

// skipIfNoAnthropicKey skips the test if VALORY_TEST_ANTHROPIC_KEY is not set.
// AI-gated tests call this at the top to avoid spending tokens in CI.
func skipIfNoAnthropicKey(t *testing.T) {
	t.Helper()
	if os.Getenv("VALORY_TEST_ANTHROPIC_KEY") == "" {
		t.Skip("AI-gated: set VALORY_TEST_ANTHROPIC_KEY to run this test (makes live Anthropic API calls)")
	}
}

// TestHTTP_OnboardingStart_ReturnsFirstQuestion is AI-gated: it calls
// POST /api/v1/profile/onboarding/start and asserts the first question is a
// non-empty string.  The Anthropic call is real; skip when no key is set.
//
// @{"verifies": ["REQ-PROFILE-011", "REQ-PROFILE-014"]}
func TestHTTP_OnboardingStart_ReturnsFirstQuestion(t *testing.T) {
	skipIfNoAnthropicKey(t)
	internaldb.IntegrationPool(t)
	truncateProfileHTTPTables(t)

	ctx := context.Background()
	handler := buildProfileHTTPStack(t, &envSecrets{})

	_, token := profileHTTPCreateStudentUser(ctx, t, handler)

	startRR := doRequest(handler, http.MethodPost, "/api/v1/profile/onboarding/start", token, nil)
	if startRR.Code != http.StatusOK {
		t.Fatalf("POST /onboarding/start: expected 200, got %d: %s", startRR.Code, startRR.Body.String())
	}

	var resp struct {
		SessionActive bool   `json:"session_active"`
		FirstMessage  string `json:"first_message"`
	}
	if err := json.NewDecoder(startRR.Body).Decode(&resp); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if !resp.SessionActive {
		t.Error("POST /onboarding/start: session_active must be true")
	}
	if resp.FirstMessage == "" {
		t.Error("POST /onboarding/start: first_message must be non-empty")
	}
}

// TestHTTP_OnboardingComplete_Returns409_WhenNoSession verifies the 409 path:
// calling /complete with no active session returns NO_ACTIVE_SESSION.
// This test is AI-free (no Anthropic call needed to exercise the error path).
//
// @{"verifies": ["REQ-PROFILE-012"]}
func TestHTTP_OnboardingComplete_Returns409_WhenNoSession(t *testing.T) {
	internaldb.IntegrationPool(t)
	truncateProfileHTTPTables(t)

	ctx := context.Background()
	handler := buildProfileHTTPStack(t, &emptySecrets{})

	_, token := profileHTTPCreateStudentUser(ctx, t, handler)

	// Call /complete without a prior /start — no onboarding_messages rows exist.
	completeRR := doRequest(handler, http.MethodPost, "/api/v1/profile/onboarding/complete", token, nil)
	if completeRR.Code != http.StatusConflict {
		t.Fatalf("POST /onboarding/complete (no session): expected 409, got %d: %s",
			completeRR.Code, completeRR.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(completeRR.Body).Decode(&resp); err != nil {
		t.Fatalf("decode complete 409 response: %v", err)
	}
	if resp["error"] != "NO_ACTIVE_SESSION" {
		t.Errorf("expected error=NO_ACTIVE_SESSION, got %v", resp["error"])
	}
}
