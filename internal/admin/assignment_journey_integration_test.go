//go:build integration

// assignment_journey_integration_test.go — full admin-assignment journey tests.
//
// These tests exercise the complete lifecycle from admin creating an assignment
// through student isolation assertions, without calling the Anthropic API. The
// syllabusStub (defined in assignment_integration_test.go) replaces
// GenerateSyllabusFromParams so no live AI call is made.
//
// Journeys covered:
//
//  1. Admin creates assignment → assigns 2 valid students + 1 invalid UUID
//     (partial-success shape) → each valid student has a course row at
//     syllabus_approved with an approved syllabus → the invalid UUID yields a
//     per-student STUDENT_NOT_FOUND error.
//
//  2. Each student's session (via GET /api/v1/courses) sees only their own
//     course. Admin sees both via GET /api/v1/admin/assignments/{id}.
//
//  3. Unassign is 409 once a course is moved to 'generating' (simulated by a
//     direct DB UPDATE — no live runner needed), verifying the sentinel check
//     in UnassignStudent (REQ-ASSIGN-007).
//
//  4. courseToResponse includes a non-null assignment_id for assigned courses
//     (REQ-ASSIGN-008) and the course status is 'syllabus_approved'
//     (REQ-ASSIGN-004, REQ-ASSIGN-005) — ready for pollAndGenerate to pick up.
//
// Run with:
//
//	make test-integration
//	# or manually:
//	export PATH=$PATH:/usr/local/go/bin
//	VALORY_TEST_DATABASE_URL=postgres://valory_test:valory_test@localhost:55432/valory_test?sslmode=disable \
//	  go test -tags integration -count=1 -run TestJourney ./internal/admin/
//
// @{"verifies": ["REQ-ASSIGN-001", "REQ-ASSIGN-002", "REQ-ASSIGN-003", "REQ-ASSIGN-004", "REQ-ASSIGN-005", "REQ-ASSIGN-006", "REQ-ASSIGN-007", "REQ-ASSIGN-008", "REQ-ASSIGN-009", "REQ-ASSIGN-010", "REQ-ASSIGN-011"]}
package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	internaldb "github.com/valory/valory/internal/db"
)

// truncateJourneyTables resets all tables needed for journey tests. Includes
// audit_log because the assignment handler emits audit rows.
func truncateJourneyTables(t *testing.T) {
	t.Helper()
	internaldb.TruncateTables(t, internaldb.IntegrationPool(t),
		"audit_log",
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
// Journey 1 — Partial-success batch, per-student isolation, admin oversight
// ---------------------------------------------------------------------------

// TestJourney_AdminAssignsStudents_PartialSuccessAndIsolation exercises the
// full admin-to-student pipeline without live AI:
//
//  1. Admin creates assignment via POST /api/v1/admin/assignments.
//  2. Admin assigns 2 valid students + 1 invalid UUID (service layer).
//  3. Both valid students have course rows at syllabus_approved with syllabi.
//  4. The invalid UUID is reported as STUDENT_NOT_FOUND.
//  5. Student A's and student B's HTTP sessions each see only their own course.
//  6. Admin GET /api/v1/admin/assignments/{id} returns both students.
//
// @{"verifies": ["REQ-ASSIGN-001", "REQ-ASSIGN-002", "REQ-ASSIGN-003", "REQ-ASSIGN-004", "REQ-ASSIGN-005", "REQ-ASSIGN-006", "REQ-ASSIGN-008", "REQ-ASSIGN-009", "REQ-ASSIGN-010", "REQ-ASSIGN-011"]}
func TestJourney_AdminAssignsStudents_PartialSuccessAndIsolation(t *testing.T) {
	internaldb.IntegrationPool(t) // skip if DB unavailable
	truncateJourneyTables(t)

	ctx := context.Background()
	handler := buildAssignmentHTTPStack(t)
	pool := internaldb.IntegrationPool(t)

	// Create actors via the HTTP login flow.
	_, adminToken := httpCreateAdminUser(ctx, t, handler)
	studentAID, studentAToken := httpCreateStudentUser(ctx, t, handler)
	studentBID, studentBToken := httpCreateStudentUser(ctx, t, handler)
	invalidStudentID := uuid.New() // not in the database

	// --- Step 1: Admin creates assignment ---

	assignmentID := httpDoCreateAssignment(t, handler, adminToken, "Journey Partial Success", "intermediate")

	// --- Step 2: Assign via the service layer (avoids CSRF on POST handler) ---

	assignRepo := NewAssignmentRepository(pool)
	assignSvc := NewAssignmentService(assignRepo, &syllabusStub{})

	created, errs := assignSvc.AssignStudents(ctx, assignmentID, []uuid.UUID{
		studentAID,
		studentBID,
		invalidStudentID,
	})

	// REQ-ASSIGN-011: partial-success — 2 created, 1 error.
	if len(created) != 2 {
		t.Fatalf("expected 2 created, got %d", len(created))
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if errs[0].StudentID != invalidStudentID {
		t.Errorf("error should be for invalidStudentID, got %s", errs[0].StudentID)
	}
	if errs[0].Error != "STUDENT_NOT_FOUND" {
		t.Errorf("expected STUDENT_NOT_FOUND, got %q", errs[0].Error)
	}

	// --- Step 3: Verify DB state for each valid student ---

	for _, c := range created {
		// status must be syllabus_approved (REQ-ASSIGN-004, REQ-ASSIGN-005).
		var status string
		if err := pool.QueryRow(ctx,
			`SELECT status::text FROM courses WHERE id = $1`, c.CourseID,
		).Scan(&status); err != nil {
			t.Fatalf("SELECT status for course %s: %v", c.CourseID, err)
		}
		if status != "syllabus_approved" {
			t.Errorf("course %s: expected syllabus_approved (REQ-ASSIGN-004/005), got %q", c.CourseID, status)
		}

		// Exactly one approved syllabus row (REQ-ASSIGN-003).
		var syllabusCount int
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM syllabi WHERE course_id = $1 AND approved_at IS NOT NULL`,
			c.CourseID,
		).Scan(&syllabusCount); err != nil {
			t.Fatalf("COUNT syllabi for course %s: %v", c.CourseID, err)
		}
		if syllabusCount != 1 {
			t.Errorf("course %s: expected 1 approved syllabus (REQ-ASSIGN-003), got %d", c.CourseID, syllabusCount)
		}

		// intake_kickoff_sent = true means the intake phase is bypassed (REQ-ASSIGN-004).
		var kickoffSent bool
		if err := pool.QueryRow(ctx,
			`SELECT intake_kickoff_sent FROM courses WHERE id = $1`, c.CourseID,
		).Scan(&kickoffSent); err != nil {
			t.Fatalf("SELECT intake_kickoff_sent for course %s: %v", c.CourseID, err)
		}
		if !kickoffSent {
			t.Errorf("course %s: intake_kickoff_sent must be true (REQ-ASSIGN-004)", c.CourseID)
		}

		// assignment_id FK must point to the created assignment.
		var dbAssignmentID uuid.UUID
		if err := pool.QueryRow(ctx,
			`SELECT assignment_id FROM courses WHERE id = $1`, c.CourseID,
		).Scan(&dbAssignmentID); err != nil {
			t.Fatalf("SELECT assignment_id for course %s: %v", c.CourseID, err)
		}
		if dbAssignmentID != assignmentID {
			t.Errorf("course %s: assignment_id = %s, want %s", c.CourseID, dbAssignmentID, assignmentID)
		}
	}

	// --- Step 4: Student A sees only their own course (REQ-ASSIGN-008, REQ-ASSIGN-009) ---

	rrA := journeyListCourses(t, handler, studentAToken)
	journeyAssertOneCourse(t, "student A", rrA, studentAID, created)

	// --- Step 5: Student B sees only their own course ---

	rrB := journeyListCourses(t, handler, studentBToken)
	journeyAssertOneCourse(t, "student B", rrB, studentBID, created)

	// --- Step 6: Admin sees both students (REQ-ASSIGN-006, REQ-ASSIGN-010) ---

	detailResp := journeyGetAssignmentDetail(t, handler, adminToken, assignmentID)
	if len(detailResp.Students) != 2 {
		t.Errorf(
			"admin oversight: expected 2 students, got %d (REQ-ASSIGN-006, REQ-ASSIGN-010)",
			len(detailResp.Students),
		)
	}
	for i, s := range detailResp.Students {
		if cs, _ := s["course_status"].(string); cs == "" {
			t.Errorf("student row %d: missing course_status", i)
		}
	}
}

// ---------------------------------------------------------------------------
// Journey 2 — Unassign returns 409 when course is generating
// ---------------------------------------------------------------------------

// TestJourney_Unassign_Returns409WhenGenerating verifies the unassign guard:
// once a course transitions to 'generating' (simulated by a direct DB UPDATE —
// the real runner is not invoked), UnassignStudent returns ErrGenerationInProgress
// and the HTTP handler responds with 409 Conflict (REQ-ASSIGN-007).
//
// @{"verifies": ["REQ-ASSIGN-007"]}
func TestJourney_Unassign_Returns409WhenGenerating(t *testing.T) {
	internaldb.IntegrationPool(t)
	truncateJourneyTables(t)

	ctx := context.Background()
	handler := buildAssignmentHTTPStack(t)
	pool := internaldb.IntegrationPool(t)

	_, adminToken := httpCreateAdminUser(ctx, t, handler)
	studentID, _ := httpCreateStudentUser(ctx, t, handler)

	assignmentID := httpDoCreateAssignment(t, handler, adminToken, "Unassign 409 Journey", "beginner")

	assignRepo := NewAssignmentRepository(pool)
	assignSvc := NewAssignmentService(assignRepo, &syllabusStub{})

	created, errs := assignSvc.AssignStudents(ctx, assignmentID, []uuid.UUID{studentID})
	if len(errs) != 0 || len(created) != 1 {
		t.Fatalf("AssignStudents: created=%d errs=%v", len(created), errs)
	}
	courseID := created[0].CourseID

	// Simulate the agent runner transitioning the course to 'generating'.
	// (pollAndGenerate is not started; we mutate the DB directly to exercise
	// the unassign guard without a full generation run.)
	if _, err := pool.Exec(ctx,
		`UPDATE courses SET status = 'generating' WHERE id = $1`, courseID,
	); err != nil {
		t.Fatalf("simulate generating status: %v", err)
	}

	// Service level: UnassignStudent must return ErrGenerationInProgress.
	_, _, err := assignSvc.UnassignStudent(ctx, assignmentID, studentID)
	if !errors.Is(err, ErrGenerationInProgress) {
		t.Errorf(
			"expected ErrGenerationInProgress, got %v (REQ-ASSIGN-007)",
			err,
		)
	}

	// HTTP level: DELETE .../students/{studentId} must return 409 Conflict.
	url := fmt.Sprintf("/api/v1/admin/assignments/%s/students/%s", assignmentID, studentID)
	req := httptest.NewRequest(http.MethodDelete, url, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf(
			"unassign when generating: expected 409 Conflict, got %d: %s (REQ-ASSIGN-007)",
			rr.Code, rr.Body.String(),
		)
	}

	var resp map[string]interface{}
	if err := journeyDecodeJSON(rr, &resp); err != nil {
		t.Fatalf("decode unassign 409 response: %v", err)
	}
	if resp["error"] != "GENERATION_IN_PROGRESS" {
		t.Errorf("expected error=GENERATION_IN_PROGRESS in body, got %v", resp["error"])
	}
}

// ---------------------------------------------------------------------------
// Journey 3 — courseToResponse includes non-null assignment_id
// ---------------------------------------------------------------------------

// TestJourney_CourseToResponse_IncludesAssignmentID verifies that when a
// student fetches their assigned course via GET /api/v1/courses/{id}, the
// JSON response body contains a non-null assignment_id matching the assignment
// (REQ-ASSIGN-008). This proves the courseToResponse function in
// internal/course/handler.go correctly populates the assignment_id field.
//
// @{"verifies": ["REQ-ASSIGN-008"]}
func TestJourney_CourseToResponse_IncludesAssignmentID(t *testing.T) {
	internaldb.IntegrationPool(t)
	truncateJourneyTables(t)

	ctx := context.Background()
	handler := buildAssignmentHTTPStack(t)
	pool := internaldb.IntegrationPool(t)

	_, adminToken := httpCreateAdminUser(ctx, t, handler)
	studentID, studentToken := httpCreateStudentUser(ctx, t, handler)

	assignmentID := httpDoCreateAssignment(t, handler, adminToken, "CourseToResponse Test", "intermediate")

	assignRepo := NewAssignmentRepository(pool)
	assignSvc := NewAssignmentService(assignRepo, &syllabusStub{})

	created, errs := assignSvc.AssignStudents(ctx, assignmentID, []uuid.UUID{studentID})
	if len(errs) != 0 || len(created) != 1 {
		t.Fatalf("AssignStudents: created=%d errs=%v", len(created), errs)
	}
	courseID := created[0].CourseID

	// Student fetches their course via the HTTP handler.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/courses/"+courseID.String(), nil)
	req.Header.Set("Authorization", "Bearer "+studentToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("getCourse: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := journeyDecodeJSON(rr, &resp); err != nil {
		t.Fatalf("decode getCourse response: %v", err)
	}

	// assignment_id must be present and non-null.
	rawAssignmentID, hasKey := resp["assignment_id"]
	if !hasKey {
		t.Fatal("getCourse response missing assignment_id field (REQ-ASSIGN-008)")
	}
	if rawAssignmentID == nil {
		t.Fatal("getCourse response has null assignment_id for admin-assigned course (REQ-ASSIGN-008)")
	}
	gotAssignmentIDStr, ok := rawAssignmentID.(string)
	if !ok {
		t.Fatalf("assignment_id is not a string: %T %v", rawAssignmentID, rawAssignmentID)
	}
	gotAssignmentID, err := uuid.Parse(gotAssignmentIDStr)
	if err != nil {
		t.Fatalf("assignment_id %q is not a valid UUID: %v", gotAssignmentIDStr, err)
	}
	if gotAssignmentID != assignmentID {
		t.Errorf(
			"assignment_id mismatch: got %s, want %s (REQ-ASSIGN-008)",
			gotAssignmentID, assignmentID,
		)
	}

	// status must be syllabus_approved (REQ-ASSIGN-004/005).
	if status, _ := resp["status"].(string); status != "syllabus_approved" {
		t.Errorf("getCourse: expected status=syllabus_approved, got %q", status)
	}
}

// ---------------------------------------------------------------------------
// Journey helpers
// ---------------------------------------------------------------------------

// journeyListCourses calls GET /api/v1/courses with the given bearer token.
func journeyListCourses(t *testing.T, handler http.Handler, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/courses", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("journeyListCourses: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	return rr
}

// journeyAssertOneCourse asserts that the listCourses response for the given
// student contains exactly 1 course, that it belongs to that student, and that
// the assignment_id field is non-null (REQ-ASSIGN-008).
func journeyAssertOneCourse(t *testing.T, label string, rr *httptest.ResponseRecorder, studentID uuid.UUID, allCreated []StudentAssignResult) {
	t.Helper()

	var resp struct {
		Courses []map[string]interface{} `json:"courses"`
	}
	if err := journeyDecodeJSON(rr, &resp); err != nil {
		t.Fatalf("journeyAssertOneCourse(%s): decode: %v", label, err)
	}

	if len(resp.Courses) != 1 {
		t.Errorf(
			"journeyAssertOneCourse(%s): expected 1 course (RLS hides other students), got %d",
			label, len(resp.Courses),
		)
		return
	}

	// Find the expected course for this student.
	var expectedCourseID uuid.UUID
	for _, c := range allCreated {
		if c.StudentID == studentID {
			expectedCourseID = c.CourseID
		}
	}
	if expectedCourseID == (uuid.UUID{}) {
		t.Fatalf("journeyAssertOneCourse(%s): student not found in allCreated", label)
	}

	gotIDStr, _ := resp.Courses[0]["id"].(string)
	gotID, _ := uuid.Parse(gotIDStr)
	if gotID != expectedCourseID {
		t.Errorf(
			"journeyAssertOneCourse(%s): visible course %s, expected %s (RLS failure)",
			label, gotID, expectedCourseID,
		)
	}

	// assignment_id must be non-null (REQ-ASSIGN-008).
	if resp.Courses[0]["assignment_id"] == nil {
		t.Errorf(
			"journeyAssertOneCourse(%s): assignment_id must be non-null for admin-assigned course (REQ-ASSIGN-008)",
			label,
		)
	}

	// status must be syllabus_approved (REQ-ASSIGN-004/005: inserted at that status).
	if status, _ := resp.Courses[0]["status"].(string); status != "syllabus_approved" {
		t.Errorf(
			"journeyAssertOneCourse(%s): expected status=syllabus_approved, got %q (REQ-ASSIGN-004/005)",
			label, status,
		)
	}
}

// assignmentDetailResp is the shape of GET /api/v1/admin/assignments/{id}.
type assignmentDetailResp struct {
	ID       string                   `json:"id"`
	Topic    string                   `json:"topic"`
	Level    string                   `json:"level"`
	Students []map[string]interface{} `json:"students"`
}

// journeyGetAssignmentDetail calls GET /api/v1/admin/assignments/{id} and
// decodes the response into an assignmentDetailResp.
func journeyGetAssignmentDetail(t *testing.T, handler http.Handler, adminToken string, assignmentID uuid.UUID) assignmentDetailResp {
	t.Helper()
	url := fmt.Sprintf("/api/v1/admin/assignments/%s", assignmentID)
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("journeyGetAssignmentDetail: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp assignmentDetailResp
	if err := journeyDecodeJSON(rr, &resp); err != nil {
		t.Fatalf("journeyGetAssignmentDetail decode: %v", err)
	}
	return resp
}

// journeyDecodeJSON decodes the response recorder body into v.
func journeyDecodeJSON(rr *httptest.ResponseRecorder, v interface{}) error {
	b := rr.Body.Bytes()
	if len(b) == 0 {
		return fmt.Errorf("journeyDecodeJSON: empty response body")
	}
	return json.NewDecoder(bytes.NewReader(b)).Decode(v)
}
