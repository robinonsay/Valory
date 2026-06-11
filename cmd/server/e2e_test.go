//go:build integration

// e2e_test.go — HTTP-level end-to-end tests for Valory system flows.
//
// These tests run against a real PostgreSQL instance using the shared
// db.IntegrationPool harness and spin up an httptest.Server that replicates
// the wiring from main() — auth, course, and content modules only.
//
// SCOPE: TC-SYSFLOW-002 and TC-SYSFLOW-003 only.
//
// EXPLICITLY OUT OF SCOPE: TC-SYSFLOW-001 (full intake→generation lifecycle).
// TC-SYSFLOW-001 requires the AI client (agent.NewThrottledClient, chair,
// professor, reviewer, agentRunner) which in turn mandates a live
// ANTHROPIC_API_KEY. Refactoring those constructors to accept interface stubs
// is a recorded Sprint 9 item. Including them here would require wiring live
// AI dependencies or modifying main() constructors — both are out of scope.
//
// SERVER WIRING NOTE: the buildTestServer helper below is a deliberate, bounded
// copy of the wiring in cmd/server/main(). Extracting a shared buildServer()
// function is also a recorded Sprint 9 item. If main() routes drift (new
// middleware, path changes, additional modules), update buildTestServer to match.
//
// RLS NOTE: the integration harness pool uses the valory_test superuser login
// role. Superusers bypass row-level security on bare-pool connections. The auth
// middleware calls pool.Acquire and then issues SET ROLE valory_app + GUC
// set_config on the acquired connection before every handler call, so per-request
// RLS is still exercised for all authenticated requests flowing through the server.
// For the assertions in TC-SYSFLOW-002 and TC-SYSFLOW-003 (status codes and row
// counts), the superuser seeding pool is used only for fixture setup and row-count
// checks; handler-level ownership and uniqueness checks are exercised through the
// authenticated HTTP client path, which carries a real session and full middleware
// stack. This is valid: we are testing handler logic and DB constraint enforcement,
// not the RLS isolation policy itself (that is covered by the module-level
// integration tests in internal/course/integration_test.go).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/valory/valory/internal/admin"
	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/content"
	"github.com/valory/valory/internal/course"
	internaldb "github.com/valory/valory/internal/db"
	"github.com/valory/valory/internal/security"
)

// ---------------------------------------------------------------------------
// Server wiring (bounded copy of main(); see file header comment)
// ---------------------------------------------------------------------------

// buildTestServer constructs an httptest.Server that wires the subset of
// modules needed for TC-SYSFLOW-002 and TC-SYSFLOW-003: auth, course, and
// content. The agent module is intentionally excluded — it requires a live
// ANTHROPIC_API_KEY (deferred to Sprint 9).
//
// The pool argument is the harness pool from db.IntegrationPool. Every module
// uses the same pool so that the auth middleware's per-request connection
// (which carries SET ROLE + GUC) is consistent with what production main() does.
//
// @{"req": ["REQ-SYS-008", "REQ-SYS-011", "REQ-SYS-030"]}
func buildTestServer(t *testing.T, pool *pgxpool.Pool) *httptest.Server {
	t.Helper()

	ctx := context.Background()

	// ConfigService is required by auth.NewAuthMiddleware's consent gate.
	// Load() reads system_config from the DB (populated by migrations).
	configSvc := admin.NewConfigService(pool)
	if err := configSvc.Load(ctx); err != nil {
		t.Fatalf("buildTestServer: load config service: %v", err)
	}

	// Auth wiring — matches main() exactly.
	authRepo := auth.NewRepository(pool)
	// Use short lockout/session durations so tests complete quickly and do not
	// accidentally expire mid-test on slow CI hosts.
	lockoutDuration := 15 * time.Minute
	sessionMaxDuration := 24 * time.Hour
	inactivityPeriod := 30 * time.Minute

	authSvc := auth.NewService(authRepo, lockoutDuration, sessionMaxDuration)
	authHandler := auth.NewHandler(authSvc)

	// authMW applies the consent gate (REQ-SECURITY-005). Students must have an
	// accepted consent row; we insert one during fixture seeding (see seedStudent).
	authMW := auth.NewAuthMiddleware(authRepo, pool, inactivityPeriod, configSvc)

	// Course module wiring.
	courseRepo := course.NewRepository(pool)
	courseSvc := course.NewService(courseRepo)
	courseHandler := course.NewHandler(courseSvc)

	// Content module wiring.
	contentRepo := content.NewContentRepository(pool)
	contentHandler := content.NewContentHandler(contentRepo, courseSvc)

	// Router — mirror of main()'s chi.NewRouter() setup, restricted to the
	// modules exercised by TC-SYSFLOW-002 and TC-SYSFLOW-003.
	r := chi.NewRouter()
	r.Use(chimiddleware.Recoverer)

	r.Route("/api/v1", func(r chi.Router) {
		r.Route("/auth", func(r chi.Router) {
			authHandler.Routes(r)
		})

		// All course, content routes require auth + CSRF (REQ-SECURITY-004).
		r.Group(func(r chi.Router) {
			r.Use(authMW)
			r.Use(security.CSRFMiddleware)

			r.Route("/courses", func(r chi.Router) {
				courseHandler.Routes(r)
				r.Route("/{id}", func(r chi.Router) {
					r.Route("/content", func(r chi.Router) {
						contentHandler.Routes(r)
					})
					r.Route("/sections", func(r chi.Router) {
						contentHandler.SectionListRoutes(r)
					})
				})
			})
		})
	})

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// ---------------------------------------------------------------------------
// Test client helpers
// ---------------------------------------------------------------------------

// testClient holds a logged-in session token and the CSRF token issued at
// login. All state-changing requests must include the CSRF token in the
// X-CSRF-Token header and the __Host-csrf cookie value.
//
// The CSRF cookie is flagged Secure:true by security.SetCSRFCookie, which
// means Go's standard cookiejar will not forward it over plain HTTP. We
// therefore bypass the jar entirely: after login we capture the cookie value
// from the Set-Cookie response header and inject both the header and cookie
// manually on every subsequent request. This is safe for test purposes — we
// are testing application logic, not browser cookie policy.
//
// @{"req": ["REQ-SYS-008", "REQ-SYS-011", "REQ-SYS-030"]}
type testClient struct {
	baseURL    string
	token      string // Bearer token
	csrfToken  string // value of __Host-csrf cookie / X-CSRF-Token header
	httpClient *http.Client
}

// login authenticates against the real /api/v1/auth/login endpoint and
// returns a populated testClient. It extracts the CSRF token from the
// Set-Cookie header in the 200 response.
//
// @{"req": ["REQ-SYS-008", "REQ-SYS-011", "REQ-SYS-030"]}
func login(t *testing.T, srv *httptest.Server, username, password string) *testClient {
	t.Helper()

	body, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})

	resp, err := http.Post(
		srv.URL+"/api/v1/auth/login",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("login: POST /api/v1/auth/login: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("login: expected 200, got %d: %s", resp.StatusCode, b)
	}

	var loginResp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		t.Fatalf("login: decode response: %v", err)
	}
	if loginResp.Token == "" {
		t.Fatal("login: empty token in response")
	}

	// Extract the CSRF token from the Set-Cookie header. The login handler calls
	// security.SetCSRFCookie which issues a __Host-csrf cookie whose value is
	// also the token that must appear in X-CSRF-Token on state-changing requests.
	var csrfToken string
	for _, raw := range resp.Cookies() {
		if raw.Name == "__Host-csrf" {
			csrfToken = raw.Value
			break
		}
	}
	if csrfToken == "" {
		t.Fatal("login: __Host-csrf cookie not present in login response")
	}

	return &testClient{
		baseURL:    srv.URL,
		token:      loginResp.Token,
		csrfToken:  csrfToken,
		httpClient: &http.Client{},
	}
}

// do executes an authenticated request. It sets Authorization: Bearer and,
// for state-changing methods (POST, PUT, PATCH, DELETE), also attaches the
// CSRF cookie and header to satisfy security.CSRFMiddleware.
//
// @{"req": ["REQ-SYS-008", "REQ-SYS-011", "REQ-SYS-030"]}
func (c *testClient) do(t *testing.T, method, path string, body io.Reader) *http.Response {
	t.Helper()

	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		t.Fatalf("testClient.do: new request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// CSRF token dance: CSRFMiddleware checks that the __Host-csrf cookie value
	// equals the X-CSRF-Token header value. For state-changing verbs we must
	// supply both; GET/HEAD/OPTIONS are exempt (see security/csrf.go).
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
		req.AddCookie(&http.Cookie{Name: "__Host-csrf", Value: c.csrfToken})
		req.Header.Set("X-CSRF-Token", c.csrfToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		t.Fatalf("testClient.do %s %s: %v", method, path, err)
	}
	return resp
}

// ---------------------------------------------------------------------------
// Fixture helpers
// ---------------------------------------------------------------------------

// seedStudent inserts a user row (role=student) and a matching student_consent
// row (consent_version="1.0" to satisfy the authMW consent gate) using the
// bare superuser pool. The FORCE RLS bypass is intentional for fixture seeding:
// there is no RLS INSERT policy for the student role on the users table, and the
// users table does not have FORCE RLS set. The student_consent table also has no
// RLS. Using the bare pool here is correct and does not weaken the test
// assertions, which exercise handler logic through the authenticated HTTP path.
//
// @{"req": ["REQ-SYS-008", "REQ-SYS-011", "REQ-SYS-030"]}
func seedStudent(t *testing.T, pool *pgxpool.Pool, username, password string) uuid.UUID {
	t.Helper()

	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("seedStudent %q: hash password: %v", username, err)
	}

	ctx := context.Background()
	var userID uuid.UUID
	err = pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role)
		 VALUES ($1, $2, 'student')
		 RETURNING id`,
		username, hash,
	).Scan(&userID)
	if err != nil {
		t.Fatalf("seedStudent %q: insert user: %v", username, err)
	}

	// Insert matching consent so the authMW consent gate passes.
	// The consent_version "1.0" matches the default seeded by migration 002.
	if _, err := pool.Exec(ctx,
		`INSERT INTO student_consent (student_id, consented_at, consent_version)
		 VALUES ($1, NOW(), '1.0')`,
		userID,
	); err != nil {
		t.Fatalf("seedStudent %q: insert student_consent: %v", username, err)
	}

	return userID
}

// seedCourseWithStatus inserts a course row for the given student directly
// through the bare superuser pool and immediately transitions it to the
// requested status. This bypasses FORCE RLS on the courses table intentionally:
// there is no server INSERT RLS policy (migration 005 adds only SELECT and
// UPDATE server-role policies), so fixture seeding via the superuser pool is
// the correct and established approach (see internal/course/integration_test.go
// integSeedCourseViaBarePool for the same pattern and rationale).
//
// @{"req": ["REQ-SYS-008", "REQ-SYS-030"]}
func seedCourseWithStatus(t *testing.T, pool *pgxpool.Pool, studentID uuid.UUID, topic, status string) uuid.UUID {
	t.Helper()

	ctx := context.Background()
	var courseID uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO courses (student_id, topic, status)
		 VALUES ($1, $2, $3::course_status)
		 RETURNING id`,
		studentID, topic, status,
	).Scan(&courseID)
	if err != nil {
		t.Fatalf("seedCourseWithStatus: insert course (status=%q): %v", status, err)
	}
	return courseID
}

// countLessonContent returns the number of lesson_content rows for a course
// using the bare superuser pool (appropriate for assertion queries that should
// not be subject to student-identity RLS filtering).
//
// @{"req": ["REQ-SYS-011", "REQ-SYS-030"]}
func countLessonContent(t *testing.T, pool *pgxpool.Pool, courseID uuid.UUID) int {
	t.Helper()
	var n int
	err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM lesson_content WHERE course_id = $1`,
		courseID,
	).Scan(&n)
	if err != nil {
		t.Fatalf("countLessonContent: %v", err)
	}
	return n
}

// countCourses returns the total number of courses for a student using the bare
// superuser pool.
//
// @{"req": ["REQ-SYS-008"]}
func countCourses(t *testing.T, pool *pgxpool.Pool, studentID uuid.UUID) int {
	t.Helper()
	var n int
	err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM courses WHERE student_id = $1`,
		studentID,
	).Scan(&n)
	if err != nil {
		t.Fatalf("countCourses: %v", err)
	}
	return n
}

// uniqueSuffix returns a short random suffix based on a UUID, used to keep
// usernames unique across parallel test runs.
func uniqueSuffix() string {
	return uuid.New().String()[:8]
}

// ---------------------------------------------------------------------------
// TC-SYSFLOW-002: Content generation does not proceed before syllabus approval
// ---------------------------------------------------------------------------

// TestE2E_TC_SYSFLOW_002_ContentNotAvailableBeforeSyllabusApproval verifies
// that lesson content endpoints return 404 / empty when a course is in a
// pre-approval status (syllabus_draft), and that no lesson_content rows exist.
//
// Test case: TC-SYSFLOW-002
// Expected result: No lesson content or homework assignments exist for the
// course. The API returns 404 for section content. Course status remains in
// syllabus-pending state.
//
// RLS note: the HTTP request path exercises RLS through the auth middleware's
// own GUC mechanism (SET ROLE valory_app + app.current_user_id). The superuser
// test pool is used only for fixture seeding and row-count assertions; those
// queries bypass RLS intentionally, which does not affect the validity of the
// handler-level status-code and row-count assertions being made here.
//
// @{"verifies": ["REQ-SYS-030", "REQ-SYS-011"]}
func TestE2E_TC_SYSFLOW_002_ContentNotAvailableBeforeSyllabusApproval(t *testing.T) {
	pool := internaldb.IntegrationPool(t)

	// Clean state: truncate all tables touched by this test. CASCADE handles
	// lesson_content, syllabi, homework, and other FK children.
	internaldb.TruncateTables(t, pool,
		"lesson_content",
		"courses",
		"sessions",
		"login_attempts",
		"student_consent",
		"users",
	)

	srv := buildTestServer(t, pool)

	// --- Preconditions ---
	// Seed a student and a course in syllabus_draft status (chair has presented
	// a syllabus but the student has not approved it yet).
	suffix := uniqueSuffix()
	studentUsername := fmt.Sprintf("tc002_student_%s", suffix)
	studentPassword := "test-password-002"
	studentID := seedStudent(t, pool, studentUsername, studentPassword)
	courseID := seedCourseWithStatus(t, pool, studentID, "Introduction to Go", "syllabus_draft")

	// --- Authenticate through the real login endpoint ---
	client := login(t, srv, studentUsername, studentPassword)

	// --- Step 1: Confirm no lesson content rows exist in the DB ---
	// This is an upfront assertion that the fixture is correct: the course is in
	// syllabus_draft, so the agent pipeline must not have run yet.
	if n := countLessonContent(t, pool, courseID); n != 0 {
		t.Fatalf("TC-SYSFLOW-002: expected 0 lesson_content rows before syllabus approval, got %d", n)
	}

	// --- Step 2: Attempt to access generated section content via GET ---
	// GET /api/v1/courses/{id}/sections — should return an empty array because
	// no verified content exists. The listSections handler returns [] when no
	// citation_verified rows match.
	sectionsPath := fmt.Sprintf("/api/v1/courses/%s/sections", courseID)
	resp := client.do(t, http.MethodGet, sectionsPath, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("TC-SYSFLOW-002: GET /sections: expected 200 (empty list), got %d: %s",
			resp.StatusCode, b)
	}

	var sections []interface{}
	if err := json.NewDecoder(resp.Body).Decode(&sections); err != nil {
		t.Fatalf("TC-SYSFLOW-002: decode /sections response: %v", err)
	}
	if len(sections) != 0 {
		t.Fatalf("TC-SYSFLOW-002: expected 0 sections, got %d", len(sections))
	}

	// --- Step 3: Attempt to access a specific section's content via GET ---
	// GET /api/v1/courses/{id}/content/{sectionIndex} — section 0 does not exist
	// so the handler must return 404.
	contentPath := fmt.Sprintf("/api/v1/courses/%s/content/0", courseID)
	resp2 := client.do(t, http.MethodGet, contentPath, nil)
	defer resp2.Body.Close()

	// The expected_result for TC-SYSFLOW-002 specifies "404 or equivalent error".
	// content.GetSectionContent returns ErrNotFound when no row exists, which
	// the handler maps to HTTP 404. This is the primary assertion.
	if resp2.StatusCode != http.StatusNotFound {
		b, _ := io.ReadAll(resp2.Body)
		t.Fatalf("TC-SYSFLOW-002: GET /content/0: expected 404 (no content before approval), got %d: %s",
			resp2.StatusCode, b)
	}

	// --- Step 4: Confirm no lesson_content rows exist after the HTTP probes ---
	// The HTTP probes are read-only (GET) and must not trigger content generation.
	if n := countLessonContent(t, pool, courseID); n != 0 {
		t.Fatalf("TC-SYSFLOW-002: expected 0 lesson_content rows after GET probes, got %d (content must not be generated before syllabus approval)", n)
	}

	// --- Step 5: Verify course status is still syllabus_draft ---
	// The GET probes must not transition the course.
	var gotStatus string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM courses WHERE id = $1`, courseID,
	).Scan(&gotStatus); err != nil {
		t.Fatalf("TC-SYSFLOW-002: fetch course status: %v", err)
	}
	if gotStatus != "syllabus_draft" {
		t.Fatalf("TC-SYSFLOW-002: expected course status 'syllabus_draft', got %q", gotStatus)
	}
}

// ---------------------------------------------------------------------------
// TC-SYSFLOW-003: Single active course enforcement
// ---------------------------------------------------------------------------

// TestE2E_TC_SYSFLOW_003_SingleActiveCourseEnforced verifies that a student
// who already has an active course cannot create a second one, and that the API
// rejects the request with HTTP 409 while leaving the existing course intact.
//
// Test case: TC-SYSFLOW-003
// Expected result: The system rejects the new course request with an error
// indicating the student already has an active course. No new course record is
// created. The existing active course is unaffected.
//
// RLS note: the HTTP request path exercises RLS through the auth middleware's
// GUC mechanism (per-request SET ROLE valory_app + app.current_user_id). The
// superuser pool is used only for fixture seeding and row-count assertions,
// which bypass RLS intentionally. The conflict detection exercised here is the
// courses_single_active_idx partial unique index (enforced at the PostgreSQL
// schema level) plus the application's ErrCourseAlreadyActive handler logic —
// both are fully exercised through the authenticated HTTP path regardless of
// whether the superuser pool or valory_app role is used for the assertions.
//
// @{"verifies": ["REQ-SYS-008"]}
func TestE2E_TC_SYSFLOW_003_SingleActiveCourseEnforced(t *testing.T) {
	pool := internaldb.IntegrationPool(t)

	// Clean state.
	internaldb.TruncateTables(t, pool,
		"lesson_content",
		"courses",
		"sessions",
		"login_attempts",
		"student_consent",
		"users",
	)

	srv := buildTestServer(t, pool)

	// --- Preconditions ---
	// Seed a student with an existing active course.
	suffix := uniqueSuffix()
	studentUsername := fmt.Sprintf("tc003_student_%s", suffix)
	studentPassword := "test-password-003"
	studentID := seedStudent(t, pool, studentUsername, studentPassword)

	// The courses_single_active_idx partial unique index covers all statuses
	// except 'archived' and 'completed'. We seed an 'active' course to ensure
	// the student is firmly in the blocked state when the second request arrives.
	existingCourseID := seedCourseWithStatus(t, pool, studentID, "Machine Learning Foundations", "active")

	// Confirm the fixture is correct before proceeding.
	if n := countCourses(t, pool, studentID); n != 1 {
		t.Fatalf("TC-SYSFLOW-003: expected 1 course before second attempt, got %d", n)
	}

	// --- Authenticate through the real login endpoint ---
	client := login(t, srv, studentUsername, studentPassword)

	// --- Attempt to create a second course ---
	// POST /api/v1/courses with a valid topic body.
	newCourseBody, _ := json.Marshal(map[string]string{
		"topic": "Deep Learning Advanced",
	})
	resp := client.do(t, http.MethodPost, "/api/v1/courses",
		bytes.NewReader(newCourseBody))
	defer resp.Body.Close()

	// --- Assert rejection ---
	// The course handler maps ErrCourseAlreadyActive → HTTP 409 Conflict
	// (see internal/course/handler.go createCourse). TC-SYSFLOW-003 expected
	// result says "error indicating the student already has an active course".
	if resp.StatusCode != http.StatusConflict {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("TC-SYSFLOW-003: expected 409 Conflict, got %d: %s", resp.StatusCode, b)
	}

	// Verify the response body carries the expected error code.
	var errResp struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("TC-SYSFLOW-003: decode error response: %v", err)
	}
	if errResp.Error != "COURSE_ALREADY_ACTIVE" {
		t.Fatalf("TC-SYSFLOW-003: expected error code 'COURSE_ALREADY_ACTIVE', got %q", errResp.Error)
	}

	// --- Assert no second course was created ---
	if n := countCourses(t, pool, studentID); n != 1 {
		t.Fatalf("TC-SYSFLOW-003: expected 1 course after rejected second attempt, got %d (second course must not be persisted)", n)
	}

	// --- Assert existing course is unaffected ---
	var gotStatus string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM courses WHERE id = $1`, existingCourseID,
	).Scan(&gotStatus); err != nil {
		t.Fatalf("TC-SYSFLOW-003: fetch existing course status: %v", err)
	}
	if gotStatus != "active" {
		t.Fatalf("TC-SYSFLOW-003: expected existing course status 'active', got %q (must be unaffected by rejected second request)", gotStatus)
	}
}
