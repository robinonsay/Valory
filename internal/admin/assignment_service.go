package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// maxBatchSize is the maximum number of students per POST .../students call.
// Synchronous syllabus generation makes N sequential Anthropic calls, each
// ~2 seconds.  At 20 students the latency is ~40 seconds, well within a typical
// reverse-proxy timeout.  Callers needing larger assignments must issue multiple
// requests (SDD-022 §10.1).
const maxBatchSize = 20

// syllabusGenerator is the interface the service requires from Chair.
// Defined on the consumer side to keep the admin package independent of agent.
type syllabusGenerator interface {
	GenerateSyllabusFromParams(ctx context.Context, courseID, studentID uuid.UUID, topic, level string, parameters json.RawMessage) (string, error)
}

// StudentAssignResult is the per-student outcome of assignStudents.
type StudentAssignResult struct {
	StudentID uuid.UUID
	CourseID  uuid.UUID // zero on error
	Error     string    // empty on success
}

// AssignmentService encapsulates the business logic for course assignments.
//
// @{"req": ["REQ-ASSIGN-001", "REQ-ASSIGN-002", "REQ-ASSIGN-003", "REQ-ASSIGN-004", "REQ-ASSIGN-005", "REQ-ASSIGN-006", "REQ-ASSIGN-007", "REQ-ASSIGN-011"]}
type AssignmentService struct {
	repo  *AssignmentRepository
	chair syllabusGenerator
}

// @{"req": ["REQ-ASSIGN-001", "REQ-ASSIGN-002", "REQ-ASSIGN-003", "REQ-ASSIGN-004", "REQ-ASSIGN-005", "REQ-ASSIGN-006", "REQ-ASSIGN-007", "REQ-ASSIGN-011"]}
func NewAssignmentService(repo *AssignmentRepository, chair syllabusGenerator) *AssignmentService {
	return &AssignmentService{repo: repo, chair: chair}
}

// CreateAssignment creates a new course assignment record.
//
// @{"req": ["REQ-ASSIGN-001"]}
func (s *AssignmentService) CreateAssignment(ctx context.Context, adminID uuid.UUID, title, topic, level string, parameters json.RawMessage) (AssignmentRow, error) {
	if parameters == nil {
		parameters = json.RawMessage("{}")
	}
	return s.repo.CreateAssignment(ctx, adminID, title, topic, level, parameters)
}

// GetAssignment returns the assignment with per-student generation status.
//
// @{"req": ["REQ-ASSIGN-006"]}
func (s *AssignmentService) GetAssignment(ctx context.Context, assignmentID uuid.UUID) (AssignmentRow, []AssignmentStudentRow, error) {
	assignment, err := s.repo.GetAssignment(ctx, assignmentID)
	if err != nil {
		return AssignmentRow{}, nil, err
	}
	students, err := s.repo.GetAssignmentStudents(ctx, assignmentID)
	if err != nil {
		return AssignmentRow{}, nil, err
	}
	return assignment, students, nil
}

// ListAssignments returns paginated assignments for the admin.
//
// @{"req": ["REQ-ASSIGN-006"]}
func (s *AssignmentService) ListAssignments(ctx context.Context, adminID uuid.UUID, cursor string, limit int) ([]AssignmentListRow, string, error) {
	return s.repo.ListAssignments(ctx, adminID, cursor, limit)
}

// AssignStudents assigns the given students to an assignment using a
// partial-success model: each student is processed independently.  Failures for
// one student are recorded in the errors slice without aborting successful
// assignments for the remaining students (REQ-ASSIGN-011).
//
// Processing order per student (SDD-022 §6.4):
//  1. Verify student exists and is active → STUDENT_NOT_FOUND on failure
//  2. INSERT courses row at syllabus_approved → COURSE_ALREADY_ACTIVE on 23505
//  3. GenerateSyllabusFromParams → SYLLABUS_GENERATION_FAILED on failure;
//     compensating DELETE courses row on generation failure
//  4. INSERT syllabi row with approved_at = now()
//
// @{"req": ["REQ-ASSIGN-002", "REQ-ASSIGN-003", "REQ-ASSIGN-004", "REQ-ASSIGN-005", "REQ-ASSIGN-011"]}
func (s *AssignmentService) AssignStudents(ctx context.Context, assignmentID uuid.UUID, studentIDs []uuid.UUID) ([]StudentAssignResult, []StudentAssignResult) {
	assignment, err := s.repo.GetAssignment(ctx, assignmentID)
	if err != nil {
		// The caller already confirmed the assignment exists before calling here,
		// but guard defensively.
		log.Printf("admin: assign students: get assignment %s: %v", assignmentID, err)
		var errs []StudentAssignResult
		for _, sid := range studentIDs {
			errs = append(errs, StudentAssignResult{StudentID: sid, Error: "ASSIGNMENT_NOT_FOUND"})
		}
		return nil, errs
	}

	var created []StudentAssignResult
	var errs []StudentAssignResult

	for _, studentID := range studentIDs {
		result := s.assignOneStudent(ctx, assignment, studentID)
		if result.Error != "" {
			errs = append(errs, result)
		} else {
			created = append(created, result)
		}
	}

	return created, errs
}

// assignOneStudent executes the 4-step assignment flow for a single student.
// It returns a StudentAssignResult with Error set on any failure.
//
// @{"req": ["REQ-ASSIGN-003", "REQ-ASSIGN-004", "REQ-ASSIGN-005", "REQ-ASSIGN-011"]}
func (s *AssignmentService) assignOneStudent(ctx context.Context, assignment AssignmentRow, studentID uuid.UUID) StudentAssignResult {
	// Step 1: verify student exists and is an active student-role account.
	ok, err := s.repo.StudentExistsAndActive(ctx, studentID)
	if err != nil {
		log.Printf("admin: assign student %s: check exists: %v", studentID, err)
		return StudentAssignResult{StudentID: studentID, Error: "STUDENT_NOT_FOUND"}
	}
	if !ok {
		return StudentAssignResult{StudentID: studentID, Error: "STUDENT_NOT_FOUND"}
	}

	// Step 2: INSERT the courses row at syllabus_approved.
	courseID, err := s.repo.CreateAssignedCourse(ctx, studentID, assignment.ID, assignment.Topic)
	if err != nil {
		if isUniqueViolationOnActiveIdx(err) {
			return StudentAssignResult{StudentID: studentID, Error: "COURSE_ALREADY_ACTIVE"}
		}
		log.Printf("admin: assign student %s: create course: %v", studentID, err)
		return StudentAssignResult{StudentID: studentID, Error: "INTERNAL_ERROR"}
	}

	// Step 3: generate the syllabus synchronously.
	// On failure, delete the course row as a compensating rollback so the
	// student is not stuck with a syllabus_approved course that has no syllabus
	// (which would cause the poller to pick it up and immediately fail).
	syllabusAdoc, err := s.chair.GenerateSyllabusFromParams(
		ctx, courseID, studentID, assignment.Topic, assignment.Level, assignment.Parameters,
	)
	if err != nil {
		log.Printf("admin: assign student %s: generate syllabus for course %s: %v", studentID, courseID, err)
		if delErr := s.repo.DeleteCourse(ctx, courseID); delErr != nil {
			// Compensating DELETE failed.  Log and continue — the orphan course
			// row will be visible to the admin in the assignment detail view
			// with status syllabus_approved but no syllabus.  The poller will
			// pick it up and fail content generation, which is visible to ops.
			log.Printf("admin: assign student %s: compensating DELETE course %s failed: %v", studentID, courseID, delErr)
		}
		return StudentAssignResult{StudentID: studentID, Error: "SYLLABUS_GENERATION_FAILED"}
	}

	// Step 4: persist the syllabus with approved_at = now().
	if err := s.repo.InsertApprovedSyllabus(ctx, courseID, syllabusAdoc); err != nil {
		log.Printf("admin: assign student %s: insert syllabus for course %s: %v", studentID, courseID, err)
		// Compensating DELETE: syllabus failed to persist; course would be picked
		// up by the poller with no syllabus, causing content generation to fail.
		if delErr := s.repo.DeleteCourse(ctx, courseID); delErr != nil {
			log.Printf("admin: assign student %s: compensating DELETE (syllabus insert fail) course %s: %v", studentID, courseID, delErr)
		}
		return StudentAssignResult{StudentID: studentID, Error: "SYLLABUS_GENERATION_FAILED"}
	}

	return StudentAssignResult{StudentID: studentID, CourseID: courseID}
}

// UnassignStudent removes a student from an assignment.
// Returns (deleted=true, reason="") when the course was at syllabus_approved
// and was successfully deleted.
// Returns (deleted=false, reason="not_assigned") when no course row exists.
// Returns an error when the course status does not permit deletion (REQ-ASSIGN-007).
//
// @{"req": ["REQ-ASSIGN-007"]}
func (s *AssignmentService) UnassignStudent(ctx context.Context, assignmentID, studentID uuid.UUID) (deleted bool, reason string, err error) {
	courseID, status, err := s.repo.GetCourseForAssignment(ctx, assignmentID, studentID)
	if err != nil {
		return false, "", fmt.Errorf("admin: unassign student: %w", err)
	}

	if courseID == (uuid.UUID{}) {
		// No course row found — idempotent success.
		return false, "not_assigned", nil
	}

	switch status {
	case "syllabus_approved":
		// Safe to delete: no agent run has started, no lesson content or
		// submissions exist for this course row yet.
		if err := s.repo.DeleteCourse(ctx, courseID); err != nil {
			return false, "", fmt.Errorf("admin: unassign student: delete course: %w", err)
		}
		return true, "", nil
	case "generating":
		return false, "", ErrGenerationInProgress
	default:
		// active, archived, completed — the course lifecycle has advanced beyond
		// the safe deletion window.
		return false, "", ErrCourseAlreadyActive
	}
}

// ErrGenerationInProgress is returned when unassign is attempted while the
// course is generating content.
var ErrGenerationInProgress = errors.New("admin: course is currently generating content")

// ErrCourseAlreadyActive is returned when unassign is attempted on an active
// or completed course.
var ErrCourseAlreadyActive = errors.New("admin: course is active or beyond")

// isUniqueViolationOnActiveIdx mirrors the check in course/service.go.
// It detects the courses_single_active_idx unique-violation so the service can
// return COURSE_ALREADY_ACTIVE rather than an opaque error.
//
// @{"req": ["REQ-ASSIGN-002", "REQ-ASSIGN-003"]}
func isUniqueViolationOnActiveIdx(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "courses_single_active_idx"
}
