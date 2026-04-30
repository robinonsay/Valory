package course

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// createTestCourse is a helper that inserts a user and a course in 'intake' status,
// returning both IDs. It calls t.Fatal on any setup failure.
//
// @{"req": ["REQ-COURSE-001"]}
func createTestCourse(ctx context.Context, t *testing.T, usernamePrefix string) (studentID uuid.UUID, courseID uuid.UUID) {
	t.Helper()
	studentID = createTestUser(ctx, t, usernamePrefix+"_"+uuid.New().String())
	repo := NewRepository(pool)
	course, err := repo.CreateCourse(ctx, studentID, "Test Topic")
	if err != nil {
		t.Fatalf("createTestCourse: CreateCourse failed: %v", err)
	}
	return studentID, course.ID
}

// @{"verifies": ["REQ-COURSE-001"]}
func TestCreateCourse_Success_Service(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	svc := NewService(NewRepository(pool))

	studentID := createTestUser(ctx, t, "svc_create_"+uuid.New().String())

	course, err := svc.CreateCourse(ctx, studentID, "Algebra")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}
	if course.Status != "intake" {
		t.Errorf("expected status 'intake', got %q", course.Status)
	}
	if course.StudentID != studentID {
		t.Errorf("expected StudentID %v, got %v", studentID, course.StudentID)
	}
}

// @{"verifies": ["REQ-COURSE-001"]}
func TestCreateCourse_UniqueViolation_MapsToErrCourseAlreadyActive(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	svc := NewService(NewRepository(pool))

	studentID := createTestUser(ctx, t, "svc_dup_"+uuid.New().String())

	if _, err := svc.CreateCourse(ctx, studentID, "Math"); err != nil {
		t.Fatalf("first CreateCourse failed: %v", err)
	}

	_, err := svc.CreateCourse(ctx, studentID, "Physics")
	if !errors.Is(err, ErrCourseAlreadyActive) {
		t.Fatalf("expected ErrCourseAlreadyActive, got %v", err)
	}
}

// @{"verifies": ["REQ-COURSE-001"]}
func TestGetCourse_NotFound(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	svc := NewService(NewRepository(pool))

	requesterID := uuid.New()
	_, err := svc.GetCourse(ctx, uuid.New(), requesterID, "student")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// @{"verifies": ["REQ-COURSE-001", "REQ-COURSE-002"]}
func TestGetCourse_ForbiddenForOtherStudent(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	svc := NewService(NewRepository(pool))

	_, courseID := createTestCourse(ctx, t, "svc_owner")
	otherStudentID := createTestUser(ctx, t, "svc_other_"+uuid.New().String())

	_, err := svc.GetCourse(ctx, courseID, otherStudentID, "student")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

// @{"verifies": ["REQ-COURSE-003"]}
func TestWithdraw_SetsArchived(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	svc := NewService(NewRepository(pool))

	studentID, courseID := createTestCourse(ctx, t, "svc_withdraw")

	updated, err := svc.Withdraw(ctx, courseID, studentID)
	if err != nil {
		t.Fatalf("Withdraw failed: %v", err)
	}
	if updated.Status != "archived" {
		t.Errorf("expected status 'archived', got %q", updated.Status)
	}
	if updated.PreWithdrawalStatus == nil {
		t.Error("expected PreWithdrawalStatus to be set")
	} else if *updated.PreWithdrawalStatus != "intake" {
		t.Errorf("expected PreWithdrawalStatus 'intake', got %q", *updated.PreWithdrawalStatus)
	}
}

// @{"verifies": ["REQ-COURSE-003"]}
func TestWithdraw_WrongStudent_Forbidden(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	svc := NewService(NewRepository(pool))

	_, courseID := createTestCourse(ctx, t, "svc_withdraw_owner")
	otherStudentID := createTestUser(ctx, t, "svc_withdraw_other_"+uuid.New().String())

	_, err := svc.Withdraw(ctx, courseID, otherStudentID)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

// @{"verifies": ["REQ-COURSE-003"]}
func TestWithdraw_AlreadyArchived_ReturnsInvalidTransition(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	svc := NewService(NewRepository(pool))

	studentID, courseID := createTestCourse(ctx, t, "svc_withdraw_archived")

	// First withdrawal should succeed.
	if _, err := svc.Withdraw(ctx, courseID, studentID); err != nil {
		t.Fatalf("first Withdraw failed: %v", err)
	}

	// Second withdrawal on already-archived course must fail.
	_, err := svc.Withdraw(ctx, courseID, studentID)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition, got %v", err)
	}
}

// @{"verifies": ["REQ-COURSE-004"]}
func TestResume_RestoresPreWithdrawalStatus(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)
	svc := NewService(repo)

	studentID, courseID := createTestCourse(ctx, t, "svc_resume_pre")

	// Advance to syllabus_draft so pre_withdrawal_status will be that.
	if _, err := repo.Transition(ctx, courseID, []string{"intake"}, "syllabus_draft", nil); err != nil {
		t.Fatalf("Transition to syllabus_draft failed: %v", err)
	}

	if _, err := svc.Withdraw(ctx, courseID, studentID); err != nil {
		t.Fatalf("Withdraw failed: %v", err)
	}

	resumed, err := svc.Resume(ctx, courseID, studentID)
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	if resumed.Status != "syllabus_draft" {
		t.Errorf("expected restored status 'syllabus_draft', got %q", resumed.Status)
	}
}

// @{"verifies": ["REQ-COURSE-004"]}
func TestResume_NullPreWithdrawal_FallsBackToActive(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)
	svc := NewService(repo)

	studentID, courseID := createTestCourse(ctx, t, "svc_resume_null")

	// Archive without a pre_withdrawal_status by direct repo transition
	// (passing nil so the column stays NULL).
	if _, err := repo.Transition(ctx, courseID, []string{"intake"}, "archived", nil); err != nil {
		t.Fatalf("Transition to archived failed: %v", err)
	}

	resumed, err := svc.Resume(ctx, courseID, studentID)
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	if resumed.Status != "active" {
		t.Errorf("expected fallback status 'active', got %q", resumed.Status)
	}
}

// @{"verifies": ["REQ-COURSE-001", "REQ-COURSE-004"]}
func TestResume_ActiveCourseExists_ReturnsErrCourseAlreadyActive(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)
	svc := NewService(repo)

	studentID, courseID := createTestCourse(ctx, t, "svc_resume_conflict")

	// Archive the first course (pre_withdrawal_status = "intake").
	if _, err := svc.Withdraw(ctx, courseID, studentID); err != nil {
		t.Fatalf("Withdraw failed: %v", err)
	}

	// Create a second active course for the same student (possible because
	// the first is now archived).
	if _, err := repo.CreateCourse(ctx, studentID, "New Active Course"); err != nil {
		t.Fatalf("CreateCourse for second course failed: %v", err)
	}

	// Resuming the archived course now conflicts with the new active one.
	_, err := svc.Resume(ctx, courseID, studentID)
	if !errors.Is(err, ErrCourseAlreadyActive) {
		t.Fatalf("expected ErrCourseAlreadyActive, got %v", err)
	}
}

// @{"verifies": ["REQ-COURSE-006"]}
func TestApproveSyllabus_TransitionsSyllabusApproved(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)
	svc := NewService(repo)

	studentID, courseID := createTestCourse(ctx, t, "svc_approve")

	// Advance to syllabus_draft and insert a syllabus.
	if _, err := repo.Transition(ctx, courseID, []string{"intake"}, "syllabus_draft", nil); err != nil {
		t.Fatalf("Transition to syllabus_draft failed: %v", err)
	}
	if _, err := repo.InsertSyllabus(ctx, courseID, "= Course Syllabus\n\nChapter 1", 1); err != nil {
		t.Fatalf("InsertSyllabus failed: %v", err)
	}

	syllabus, course, err := svc.ApproveSyllabus(ctx, courseID, studentID)
	if err != nil {
		t.Fatalf("ApproveSyllabus failed: %v", err)
	}
	if course.Status != "syllabus_approved" {
		t.Errorf("expected course status 'syllabus_approved', got %q", course.Status)
	}
	if syllabus.ApprovedAt == nil {
		t.Error("expected syllabus ApprovedAt to be set")
	}
}

// @{"verifies": ["REQ-COURSE-006"]}
func TestApproveSyllabus_WrongState_ReturnsInvalidTransition(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	svc := NewService(NewRepository(pool))

	// Course is still in 'intake' — not 'syllabus_draft'.
	studentID, courseID := createTestCourse(ctx, t, "svc_approve_wrong")

	_, _, err := svc.ApproveSyllabus(ctx, courseID, studentID)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition, got %v", err)
	}
}

// @{"verifies": ["REQ-COURSE-005"]}
func TestRequestModification_IncrementsSyllabusVersion(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)
	svc := NewService(repo)

	studentID, courseID := createTestCourse(ctx, t, "svc_modreq")

	// Bootstrap: need a syllabus_draft state and an initial syllabus.
	if _, err := repo.Transition(ctx, courseID, []string{"intake"}, "syllabus_draft", nil); err != nil {
		t.Fatalf("Transition failed: %v", err)
	}
	if _, err := repo.InsertSyllabus(ctx, courseID, "= Initial Syllabus", 1); err != nil {
		t.Fatalf("InsertSyllabus failed: %v", err)
	}

	newSyllabus, updatedCourse, err := svc.RequestModification(ctx, courseID, studentID, "Please add a chapter on graphs.")
	if err != nil {
		t.Fatalf("RequestModification failed: %v", err)
	}
	if newSyllabus.Version != 2 {
		t.Errorf("expected syllabus version 2, got %d", newSyllabus.Version)
	}
	if newSyllabus.ContentAdoc != "Please add a chapter on graphs." {
		t.Errorf("unexpected ContentAdoc: %q", newSyllabus.ContentAdoc)
	}
	if updatedCourse.Status != "syllabus_draft" {
		t.Errorf("expected course status 'syllabus_draft', got %q", updatedCourse.Status)
	}
}

// @{"verifies": ["REQ-COURSE-005"]}
func TestRequestModification_EmptyText_ReturnsBadRequest(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	svc := NewService(NewRepository(pool))

	studentID, courseID := createTestCourse(ctx, t, "svc_modreq_empty")

	_, _, err := svc.RequestModification(ctx, courseID, studentID, "   ")
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("expected ErrBadRequest, got %v", err)
	}
}

// @{"verifies": ["REQ-COURSE-008"]}
func TestAgreeToSchedule_CountReturned(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)
	svc := NewService(repo)

	studentID, courseID := createTestCourse(ctx, t, "svc_agree")

	hw, err := repo.InsertHomework(ctx, courseID, 1, "Problem Set 1", "Grade all problems", 0.25)
	if err != nil {
		t.Fatalf("InsertHomework failed: %v", err)
	}
	if err := repo.InsertDueDateSchedule(ctx, courseID, hw.ID, time.Now().AddDate(0, 0, 7)); err != nil {
		t.Fatalf("InsertDueDateSchedule failed: %v", err)
	}

	count, err := svc.AgreeToSchedule(ctx, courseID, studentID)
	if err != nil {
		t.Fatalf("AgreeToSchedule failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected count 1, got %d", count)
	}
}

// @{"verifies": ["REQ-COURSE-008"]}
func TestAgreeToSchedule_NoPending_ReturnsErrNoPendingSchedule(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	svc := NewService(NewRepository(pool))

	studentID, courseID := createTestCourse(ctx, t, "svc_agree_nopending")

	_, err := svc.AgreeToSchedule(ctx, courseID, studentID)
	if !errors.Is(err, ErrNoPendingSchedule) {
		t.Fatalf("expected ErrNoPendingSchedule, got %v", err)
	}
}
