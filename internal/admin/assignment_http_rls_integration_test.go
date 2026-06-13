//go:build integration

// assignment_http_rls_integration_test.go — HTTP-handler-level RLS isolation
// probe for admin course assignment.
//
// WHY this file exists:
// assignment_integration_test.go already proves RLS isolation at the DB
// connection level using db.AcquireAsUser (which calls SET ROLE valory_app and
// sets the GUCs manually). That level is necessary but not sufficient: it does
// not exercise the actual auth middleware path that the production system uses,
// where the GUCs are set inside NewAuthMiddleware from a real session token.
//
// This file adds the HTTP-handler-level probe: student B holds a real bearer
// token; the full auth middleware (NewAuthMiddleware) acquires a connection and
// sets app.current_user_id/app.current_role from the session; and then the
// course handler's GET /courses/{id} is invoked. RLS must still deny access,
// returning 404 (the course is not visible) rather than 200.
//
// This is the real-world isolation guarantee required by REQ-FECOURSE-603: the
// end-to-end chain from HTTP session → middleware GUC injection → RLS policy
// must prevent student B from reading student A's assigned course.
//
// Run with:
//
//	make test-integration
//	# or manually:
//	export PATH=$PATH:/usr/local/go/bin
//	VALORY_TEST_DATABASE_URL=postgres://valory_test:valory_test@localhost:55432/valory_test?sslmode=disable \
//	  go test -tags integration -count=1 -run TestHTTPRLS ./internal/admin/
//
// @{"verifies": ["REQ-FECOURSE-603", "REQ-ASSIGN-009", "REQ-ASSIGN-008", "REQ-ASSIGN-010"]}
package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/valory/valory/internal/audit"
	authpkg "github.com/valory/valory/internal/auth"
	coursepkg "github.com/valory/valory/internal/course"
	internaldb "github.com/valory/valory/internal/db"
)

// ---------------------------------------------------------------------------
// Handler stack builder
// ---------------------------------------------------------------------------

// buildAssignmentHTTPStack wires the minimal HTTP router required by the HTTP
// RLS probe tests. It mirrors the router layout in cmd/server/main.go but
// includes only the routes needed here:
//
//	POST /api/v1/auth/login                           ← obtain bearer tokens
//	POST /api/v1/admin/assignments                    ← createAssignment (admin)
//	POST /api/v1/admin/assignments/{id}/students      ← assignStudents   (admin)
//	GET  /api/v1/admin/assignments/{id}               ← getAssignment    (admin)
//	GET  /api/v1/admin/assignments                    ← listAssignments  (admin)
//	GET  /api/v1/courses/{id}                         ← getCourse        (student)
//	GET  /api/v1/courses                              ← listCourses      (student)
//
// Both admin and student routes share the same auth middleware so that session
// tokens issued by NewAuthMiddleware set app.current_user_id/app.current_role
// correctly on the connection that the handler receives via auth.ConnFromContext.
// This is the critical middleware→GUC→RLS chain under test.
//
// No CSRF middleware is applied: httptest requests do not carry CSRF tokens and
// production bearer-token flows bypass the CSRF cookie check (see handler.go).
// RequireRole("admin") is applied only on the admin routes.
func buildAssignmentHTTPStack(t *testing.T) http.Handler {
	t.Helper()

	pool := internaldb.IntegrationPool(t)

	// Auth layer.
	authRepo := authpkg.NewRepository(pool)
	authSvc := authpkg.NewService(authRepo, 15*time.Minute, 24*time.Hour)

	// Auth middleware: acquires a connection per request, sets GUCs from the
	// session record. Consent provider is nil — the consent gate is disabled for
	// tests to avoid seeding student_consent rows in every test.
	authMW := authpkg.NewAuthMiddleware(
		authRepo,
		pool,
		24*time.Hour,
		nil, // consent gate disabled
	)

	// Assignment handler (admin path). syllabusStub is defined in
	// assignment_integration_test.go and avoids Anthropic API calls.
	assignRepo := NewAssignmentRepository(pool)
	assignSvc := NewAssignmentService(assignRepo, &syllabusStub{})
	assignHandler := NewAssignmentHandler(assignSvc, audit.NewRepository(pool), pool)

	// Course handler (student/admin path). GET /courses and GET /courses/{id} are
	// the only course endpoints used by the RLS probes; no intake starter needed.
	courseRepo := coursepkg.NewRepository(pool)
	courseSvc := coursepkg.NewService(courseRepo)
	courseHandler := coursepkg.NewHandler(courseSvc)

	r := chi.NewRouter()
	r.Use(authMW)

	// Auth routes are unauthenticated — mount before the auth middleware fires
	// for the rest. Because chi evaluates routes after all Use() middleware,
	// we register /api/v1/auth OUTSIDE the main router middleware by using a
	// sub-router that skips authMW. We achieve this by registering the auth
	// routes on a separate top-level router and composing both via chi.
	//
	// Simpler approach: mount the auth login endpoint on the outer router
	// directly, before authMW. chi's Use() applies middleware to all routes
	// registered on that router AFTER the Use() call — routes registered before
	// the Use() call are unaffected. Since we call r.Use(authMW) first, all
	// routes on r will have authMW applied.
	//
	// We therefore create a second (outer) router that registers /api/v1/auth
	// before wrapping the rest.
	outerRouter := chi.NewRouter()

	// Login endpoint must be reachable WITHOUT the auth middleware
	// (the request has no bearer token yet).
	outerRouter.Route("/api/v1/auth", func(r chi.Router) {
		authpkg.NewHandlerFull(authSvc, authRepo, pool, 24*time.Hour, nil).Routes(r)
	})

	// All other routes go through authMW.
	outerRouter.Group(func(r chi.Router) {
		r.Use(authMW)

		// Admin assignment endpoints: require admin role.
		r.Route("/api/v1/admin/assignments", func(r chi.Router) {
			r.Use(authpkg.RequireRole("admin"))
			assignHandler.Routes(r)
		})

		// Course endpoints: accessible to both students and admins.
		r.Route("/api/v1/courses", func(r chi.Router) {
			courseHandler.Routes(r)
		})
	})

	return outerRouter
}

// ---------------------------------------------------------------------------
// HTTP fixture helpers
// ---------------------------------------------------------------------------

// httpCreateStudentUser inserts a student row into the DB and returns their ID
// and a raw bearer token obtained via POST /api/v1/auth/login.
func httpCreateStudentUser(ctx context.Context, t *testing.T, handler http.Handler) (uuid.UUID, string) {
	t.Helper()
	p := internaldb.IntegrationPool(t)
	username := fmt.Sprintf("http_rls_student_%s", uuid.New().String()[:8])
	password := "TestPass1234!"

	hash, err := authpkg.HashPassword(password)
	if err != nil {
		t.Fatalf("httpCreateStudentUser hash: %v", err)
	}
	var id uuid.UUID
	if err := p.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, 'student') RETURNING id`,
		username, hash,
	).Scan(&id); err != nil {
		t.Fatalf("httpCreateStudentUser insert: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("httpCreateStudentUser login: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("httpCreateStudentUser decode: %v", err)
	}
	if resp.Token == "" {
		t.Fatal("httpCreateStudentUser: empty token in login response")
	}
	return id, resp.Token
}

// httpCreateAdminUser inserts an admin row into the DB and returns their ID and
// a raw bearer token obtained via POST /api/v1/auth/login.
func httpCreateAdminUser(ctx context.Context, t *testing.T, handler http.Handler) (uuid.UUID, string) {
	t.Helper()
	p := internaldb.IntegrationPool(t)
	username := fmt.Sprintf("http_rls_admin_%s", uuid.New().String()[:8])
	password := "AdminPass1234!"

	hash, err := authpkg.HashPassword(password)
	if err != nil {
		t.Fatalf("httpCreateAdminUser hash: %v", err)
	}
	var id uuid.UUID
	if err := p.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, 'admin') RETURNING id`,
		username, hash,
	).Scan(&id); err != nil {
		t.Fatalf("httpCreateAdminUser insert: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("httpCreateAdminUser login: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("httpCreateAdminUser decode: %v", err)
	}
	if resp.Token == "" {
		t.Fatal("httpCreateAdminUser: empty token in login response")
	}
	return id, resp.Token
}

// httpDoCreateAssignment calls POST /api/v1/admin/assignments and returns the
// created assignment's ID.
func httpDoCreateAssignment(t *testing.T, handler http.Handler, adminToken, topic, level string) uuid.UUID {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{
		"topic": topic,
		"level": level,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/assignments", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("httpDoCreateAssignment: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("httpDoCreateAssignment decode: %v", err)
	}
	id, err := uuid.Parse(resp.ID)
	if err != nil {
		t.Fatalf("httpDoCreateAssignment parse id %q: %v", resp.ID, err)
	}
	return id
}

// httpDoAssignStudents calls POST /api/v1/admin/assignments/{id}/students and
// returns the created course entries from the response body.
func httpDoAssignStudents(t *testing.T, handler http.Handler, adminToken string, assignmentID uuid.UUID, studentIDs []uuid.UUID) []map[string]interface{} {
	t.Helper()
	ids := make([]string, len(studentIDs))
	for i, id := range studentIDs {
		ids[i] = id.String()
	}
	body, _ := json.Marshal(map[string]interface{}{"student_ids": ids})
	url := fmt.Sprintf("/api/v1/admin/assignments/%s/students", assignmentID)
	req := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("httpDoAssignStudents: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Created []map[string]interface{} `json:"created"`
		Errors  []map[string]interface{} `json:"errors"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("httpDoAssignStudents decode: %v", err)
	}
	return resp.Created
}

// truncateHTTPRLSTables clears all tables touched by the HTTP-level RLS tests.
func truncateHTTPRLSTables(t *testing.T) {
	t.Helper()
	internaldb.TruncateTables(t, internaldb.IntegrationPool(t),
		"sessions",
		"login_attempts",
		"syllabi",
		"agent_token_usage",
		"courses",
		"course_assignments",
		"users",
	)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestHTTPRLS_StudentB_CannotGetStudentA_AssignedCourse is the HTTP-level RLS
// isolation probe required by REQ-FECOURSE-603.
//
// The existing RLS probe in assignment_integration_test.go operates at the DB
// connection level (db.AcquireAsUser). This test proves the same guarantee
// through the full production-like auth chain:
//
//  1. Admin creates an assignment and assigns both student A and student B.
//  2. Student A's course ID is extracted from the assign response.
//  3. Student B holds a real bearer token obtained via POST /auth/login.
//  4. Student B calls GET /api/v1/courses/{courseA_id} with their token.
//  5. NewAuthMiddleware acquires a connection and sets app.current_user_id =
//     student B's UUID, app.current_role = 'student' from the session.
//  6. CourseRepository.GetCourseByID queries using that connection; the RLS
//     student policy (student_id = app.current_user_id) filters the row.
//  7. The handler returns 404 — NOT 200 with student A's course data.
//
// @{"verifies": ["REQ-FECOURSE-603", "REQ-ASSIGN-009"]}
func TestHTTPRLS_StudentB_CannotGetStudentA_AssignedCourse(t *testing.T) {
	internaldb.IntegrationPool(t) // skip if DB unavailable
	truncateHTTPRLSTables(t)

	ctx := context.Background()
	handler := buildAssignmentHTTPStack(t)

	_, adminToken := httpCreateAdminUser(ctx, t, handler)
	studentAID, _ := httpCreateStudentUser(ctx, t, handler)
	studentBID, studentBToken := httpCreateStudentUser(ctx, t, handler)

	// Admin creates assignment, assigns both students.
	assignmentID := httpDoCreateAssignment(t, handler, adminToken, "HTTP RLS Isolation Topic", "beginner")
	created := httpDoAssignStudents(t, handler, adminToken, assignmentID, []uuid.UUID{studentAID, studentBID})

	if len(created) != 2 {
		t.Fatalf("expected 2 created courses, got %d", len(created))
	}

	// Locate student A's course ID in the assign response.
	var courseAID string
	for _, c := range created {
		if sid, _ := c["student_id"].(string); sid == studentAID.String() {
			courseAID, _ = c["course_id"].(string)
		}
	}
	if courseAID == "" {
		t.Fatal("could not find student A's course ID in assign response")
	}

	// Student B uses their bearer token to call GET /courses/{courseA_id}.
	// The auth middleware will set app.current_user_id = studentB's UUID so the
	// RLS student policy must deny the read (student_id != studentB).
	req := httptest.NewRequest(http.MethodGet, "/api/v1/courses/"+courseAID, nil)
	req.Header.Set("Authorization", "Bearer "+studentBToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// RLS isolation guarantee: student B must NOT receive a 200 with course data.
	// CourseRepository returns ErrNotFound when RLS filters the row; the handler
	// maps ErrNotFound to 404. A 403 is also acceptable (service-layer ownership
	// check). Any 200 response is a security failure.
	if rr.Code == http.StatusOK {
		t.Errorf(
			"RLS FAILURE (REQ-FECOURSE-603): student B received HTTP 200 for student A's "+
				"assigned course. The auth middleware did not correctly set app.current_user_id "+
				"or the courses_student_policy is not enforced at the HTTP layer. "+
				"course_id=%s status=%d response=%s",
			courseAID, rr.Code, rr.Body.String(),
		)
		return
	}
	if rr.Code != http.StatusNotFound && rr.Code != http.StatusForbidden {
		t.Errorf(
			"TestHTTPRLS_StudentB_CannotGetStudentA_AssignedCourse: "+
				"expected 404 (RLS deny) or 403 (ownership check), got %d: %s",
			rr.Code, rr.Body.String(),
		)
	}
}

// TestHTTPRLS_StudentA_CanGetOwnAssignedCourse verifies the positive path: the
// same auth middleware chain that denies cross-student access must permit the
// owning student to read their own assigned course (REQ-ASSIGN-008).
//
// @{"verifies": ["REQ-ASSIGN-008", "REQ-ASSIGN-009"]}
func TestHTTPRLS_StudentA_CanGetOwnAssignedCourse(t *testing.T) {
	internaldb.IntegrationPool(t)
	truncateHTTPRLSTables(t)

	ctx := context.Background()
	handler := buildAssignmentHTTPStack(t)

	_, adminToken := httpCreateAdminUser(ctx, t, handler)
	studentAID, studentAToken := httpCreateStudentUser(ctx, t, handler)

	assignmentID := httpDoCreateAssignment(t, handler, adminToken, "Positive RLS Test", "intermediate")
	created := httpDoAssignStudents(t, handler, adminToken, assignmentID, []uuid.UUID{studentAID})

	if len(created) != 1 {
		t.Fatalf("expected 1 created course, got %d", len(created))
	}
	courseAID, _ := created[0]["course_id"].(string)
	if courseAID == "" {
		t.Fatal("course_id missing from assign response")
	}

	// Student A uses their own session token — must receive 200.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/courses/"+courseAID, nil)
	req.Header.Set("Authorization", "Bearer "+studentAToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf(
			"TestHTTPRLS_StudentA_CanGetOwnAssignedCourse: student A should see own course "+
				"(want 200), got %d: %s",
			rr.Code, rr.Body.String(),
		)
		return
	}

	// The response must include a non-null assignment_id (REQ-ASSIGN-008:
	// courseToResponse includes assignment_id so the frontend can render the
	// "Assigned by instructor" badge).
	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode getCourse response: %v", err)
	}
	assignmentIDVal, hasKey := resp["assignment_id"]
	if !hasKey || assignmentIDVal == nil {
		t.Errorf(
			"TestHTTPRLS_StudentA_CanGetOwnAssignedCourse: expected non-null assignment_id "+
				"in getCourse response (REQ-ASSIGN-008), got assignment_id=%v",
			resp["assignment_id"],
		)
	}
}

// TestHTTPRLS_StudentA_ListCoursesSeesOnlyOwnCourse verifies that GET /courses
// for student A returns only their own assigned course, not student B's. This
// exercises the ListCourses path where the student identity from the session
// filters the result set via RLS (REQ-ASSIGN-008).
//
// @{"verifies": ["REQ-ASSIGN-008", "REQ-ASSIGN-009"]}
func TestHTTPRLS_StudentA_ListCoursesSeesOnlyOwnCourse(t *testing.T) {
	internaldb.IntegrationPool(t)
	truncateHTTPRLSTables(t)

	ctx := context.Background()
	handler := buildAssignmentHTTPStack(t)

	_, adminToken := httpCreateAdminUser(ctx, t, handler)
	studentAID, studentAToken := httpCreateStudentUser(ctx, t, handler)
	studentBID, _ := httpCreateStudentUser(ctx, t, handler)

	assignmentID := httpDoCreateAssignment(t, handler, adminToken, "List Courses RLS", "advanced")
	created := httpDoAssignStudents(t, handler, adminToken, assignmentID, []uuid.UUID{studentAID, studentBID})
	if len(created) != 2 {
		t.Fatalf("expected 2 created courses, got %d", len(created))
	}

	// Locate student A's course ID.
	var courseAID string
	for _, c := range created {
		if sid, _ := c["student_id"].(string); sid == studentAID.String() {
			courseAID, _ = c["course_id"].(string)
		}
	}
	if courseAID == "" {
		t.Fatal("could not find student A's course ID")
	}

	// Student A calls GET /api/v1/courses.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/courses", nil)
	req.Header.Set("Authorization", "Bearer "+studentAToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("listCourses for student A: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var listResp struct {
		Courses []map[string]interface{} `json:"courses"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode listCourses response: %v", err)
	}

	// Exactly 1 course must be visible (student A's own course).
	if len(listResp.Courses) != 1 {
		t.Errorf(
			"TestHTTPRLS_StudentA_ListCoursesSeesOnlyOwnCourse: expected 1 course for "+
				"student A, got %d (courses=%v)",
			len(listResp.Courses), listResp.Courses,
		)
		return
	}

	gotCourseID, _ := listResp.Courses[0]["id"].(string)
	if gotCourseID != courseAID {
		t.Errorf(
			"student A sees course %q in list, expected course %q — "+
				"student B's course must not be visible",
			gotCourseID, courseAID,
		)
	}

	// assignment_id must be non-null (REQ-ASSIGN-008: assigned courses carry
	// assignment_id so the frontend can render the "Assigned by instructor" badge).
	if assignmentIDVal := listResp.Courses[0]["assignment_id"]; assignmentIDVal == nil {
		t.Errorf(
			"listCourses: student A's assigned course should have non-null assignment_id " +
				"(REQ-ASSIGN-008), got nil",
		)
	}
}

// TestHTTPRLS_Admin_GetAssignment_SeesAllStudentRows verifies that an admin
// session (app.current_role='admin' set by the middleware from the session)
// can call GET /api/v1/admin/assignments/{id} and receive both student rows in
// the response, proving the admin RLS policy allows cross-student visibility
// (REQ-ASSIGN-010).
//
// @{"verifies": ["REQ-ASSIGN-010", "REQ-ASSIGN-006"]}
func TestHTTPRLS_Admin_GetAssignment_SeesAllStudentRows(t *testing.T) {
	internaldb.IntegrationPool(t)
	truncateHTTPRLSTables(t)

	ctx := context.Background()
	handler := buildAssignmentHTTPStack(t)

	_, adminToken := httpCreateAdminUser(ctx, t, handler)
	studentAID, _ := httpCreateStudentUser(ctx, t, handler)
	studentBID, _ := httpCreateStudentUser(ctx, t, handler)

	assignmentID := httpDoCreateAssignment(t, handler, adminToken, "Admin Oversight Topic", "intermediate")
	created := httpDoAssignStudents(t, handler, adminToken, assignmentID, []uuid.UUID{studentAID, studentBID})
	if len(created) != 2 {
		t.Fatalf("expected 2 created courses, got %d", len(created))
	}

	// Admin calls GET /api/v1/admin/assignments/{id}.
	url := fmt.Sprintf("/api/v1/admin/assignments/%s", assignmentID)
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("getAssignment for admin: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Students []map[string]interface{} `json:"students"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode getAssignment response: %v", err)
	}

	// Admin must see all 2 student rows (REQ-ASSIGN-010).
	if len(resp.Students) != 2 {
		t.Errorf(
			"TestHTTPRLS_Admin_GetAssignment_SeesAllStudentRows: admin should see 2 "+
				"students, got %d (REQ-ASSIGN-010)",
			len(resp.Students),
		)
	}
	for i, s := range resp.Students {
		if cs, _ := s["course_status"].(string); cs == "" {
			t.Errorf("student row %d: expected non-empty course_status", i)
		}
		if u, _ := s["username"].(string); u == "" {
			t.Errorf("student row %d: expected non-empty username", i)
		}
	}
}

// TestHTTPRLS_StudentCannotCallAdminAssignmentEndpoints verifies that a student
// session is denied access to admin assignment endpoints with 403 Forbidden.
// This guards against accidental misconfiguration of RequireRole("admin").
//
// @{"verifies": ["REQ-ASSIGN-001", "REQ-ASSIGN-002"]}
func TestHTTPRLS_StudentCannotCallAdminAssignmentEndpoints(t *testing.T) {
	internaldb.IntegrationPool(t)
	truncateHTTPRLSTables(t)

	ctx := context.Background()
	handler := buildAssignmentHTTPStack(t)

	_, studentToken := httpCreateStudentUser(ctx, t, handler)

	tests := []struct {
		name   string
		method string
		path   string
		body   interface{}
	}{
		{
			name:   "POST_admin_assignments",
			method: http.MethodPost,
			path:   "/api/v1/admin/assignments",
			body:   map[string]string{"topic": "blocked", "level": "beginner"},
		},
		{
			name:   "GET_admin_assignments",
			method: http.MethodGet,
			path:   "/api/v1/admin/assignments",
		},
		{
			name:   "GET_admin_assignment_by_id",
			method: http.MethodGet,
			path:   "/api/v1/admin/assignments/" + uuid.New().String(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var body []byte
			if tc.body != nil {
				body, _ = json.Marshal(tc.body)
			}
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+studentToken)
			if tc.body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusForbidden {
				t.Errorf(
					"%s: student should be forbidden from admin endpoint (want 403), got %d: %s",
					tc.name, rr.Code, rr.Body.String(),
				)
			}
		})
	}
}
