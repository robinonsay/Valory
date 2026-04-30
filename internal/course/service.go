package course

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrCourseAlreadyActive = errors.New("course: student already has an active course")
	ErrForbidden           = errors.New("course: access forbidden")
	ErrBadRequest          = errors.New("course: bad request")
)

type CourseService struct {
	repo *CourseRepository
}

// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002", "REQ-COURSE-003", "REQ-COURSE-004", "REQ-COURSE-005", "REQ-COURSE-006", "REQ-COURSE-007", "REQ-COURSE-008"]}
func NewService(repo *CourseRepository) *CourseService {
	return &CourseService{repo: repo}
}

// isUniqueViolationOnActiveIdx returns true when err is a PostgreSQL
// unique-violation (23505) on the courses_single_active_idx index.
//
// @{"req": ["REQ-COURSE-001"]}
func isUniqueViolationOnActiveIdx(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "courses_single_active_idx"
}

// @{"req": ["REQ-COURSE-001"]}
func (s *CourseService) CreateCourse(ctx context.Context, studentID uuid.UUID, topic string) (CourseRow, error) {
	course, err := s.repo.CreateCourse(ctx, studentID, topic)
	if err != nil {
		if isUniqueViolationOnActiveIdx(err) {
			return CourseRow{}, ErrCourseAlreadyActive
		}
		return CourseRow{}, err
	}
	return course, nil
}

// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002"]}
func (s *CourseService) GetCourse(ctx context.Context, courseID, requesterID uuid.UUID, role string) (CourseRow, error) {
	row, err := s.repo.GetCourseByID(ctx, courseID)
	if err != nil {
		return CourseRow{}, err
	}
	if role == "student" && row.StudentID != requesterID {
		return CourseRow{}, ErrForbidden
	}
	return row, nil
}

// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002"]}
func (s *CourseService) ListCourses(ctx context.Context, requesterID uuid.UUID, role string, statusFilter string, cursor string, limit int) ([]CourseRow, string, error) {
	var studentID *uuid.UUID
	if role == "student" {
		studentID = &requesterID
	}
	return s.repo.ListCourses(ctx, studentID, statusFilter, cursor, limit)
}

// @{"req": ["REQ-COURSE-003"]}
func (s *CourseService) Withdraw(ctx context.Context, courseID, studentID uuid.UUID) (CourseRow, error) {
	course, err := s.GetCourse(ctx, courseID, studentID, "student")
	if err != nil {
		return CourseRow{}, err
	}

	preWithdrawalStatus := course.Status
	return s.repo.Transition(ctx, courseID,
		[]string{"intake", "syllabus_draft", "syllabus_approved", "generating", "active"},
		"archived",
		&preWithdrawalStatus,
	)
}

// @{"req": ["REQ-COURSE-004"]}
func (s *CourseService) Resume(ctx context.Context, courseID, studentID uuid.UUID) (CourseRow, error) {
	course, err := s.GetCourse(ctx, courseID, studentID, "student")
	if err != nil {
		return CourseRow{}, err
	}

	if course.Status != "archived" {
		return CourseRow{}, ErrInvalidTransition
	}

	restoreStatus := "active"
	if course.PreWithdrawalStatus != nil {
		restoreStatus = *course.PreWithdrawalStatus
	}

	updated, err := s.repo.Transition(ctx, courseID, []string{"archived"}, restoreStatus, nil)
	if err != nil {
		if isUniqueViolationOnActiveIdx(err) {
			return CourseRow{}, ErrCourseAlreadyActive
		}
		return CourseRow{}, err
	}
	return updated, nil
}

// @{"req": ["REQ-COURSE-006"]}
func (s *CourseService) ApproveSyllabus(ctx context.Context, courseID, studentID uuid.UUID) (SyllabusRow, CourseRow, error) {
	course, err := s.GetCourse(ctx, courseID, studentID, "student")
	if err != nil {
		return SyllabusRow{}, CourseRow{}, err
	}
	if course.Status != "syllabus_draft" {
		return SyllabusRow{}, CourseRow{}, ErrInvalidTransition
	}
	syllabus, err := s.repo.GetLatestSyllabus(ctx, courseID)
	if err != nil {
		return SyllabusRow{}, CourseRow{}, err
	}

	tx, err := s.repo.Pool().Begin(ctx)
	if err != nil {
		return SyllabusRow{}, CourseRow{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := s.repo.ApproveSyllabusTx(ctx, tx, courseID, syllabus.ID); err != nil {
		return SyllabusRow{}, CourseRow{}, err
	}
	updatedCourse, err := s.repo.TransitionTx(ctx, tx, courseID, []string{"syllabus_draft"}, "syllabus_approved", nil)
	if err != nil {
		return SyllabusRow{}, CourseRow{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SyllabusRow{}, CourseRow{}, err
	}

	// Re-fetch syllabus post-commit to capture the approved_at timestamp.
	updatedSyllabus, err := s.repo.GetLatestSyllabus(ctx, courseID)
	if err != nil {
		return SyllabusRow{}, CourseRow{}, err
	}
	return updatedSyllabus, updatedCourse, nil
}

// @{"req": ["REQ-COURSE-005"]}
func (s *CourseService) RequestModification(ctx context.Context, courseID, studentID uuid.UUID, requestText string) (SyllabusRow, CourseRow, error) {
	if strings.TrimSpace(requestText) == "" {
		return SyllabusRow{}, CourseRow{}, ErrBadRequest
	}
	_, err := s.GetCourse(ctx, courseID, studentID, "student")
	if err != nil {
		return SyllabusRow{}, CourseRow{}, err
	}
	latest, err := s.repo.GetLatestSyllabus(ctx, courseID)
	if err != nil {
		return SyllabusRow{}, CourseRow{}, err
	}

	tx, err := s.repo.Pool().Begin(ctx)
	if err != nil {
		return SyllabusRow{}, CourseRow{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	newSyllabus, err := s.repo.InsertSyllabusTx(ctx, tx, courseID, requestText, latest.Version+1)
	if err != nil {
		return SyllabusRow{}, CourseRow{}, err
	}
	updatedCourse, err := s.repo.TransitionTx(ctx, tx, courseID,
		[]string{"syllabus_draft", "syllabus_approved"}, "syllabus_draft", nil)
	if err != nil {
		return SyllabusRow{}, CourseRow{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SyllabusRow{}, CourseRow{}, err
	}
	return newSyllabus, updatedCourse, nil
}

// @{"req": ["REQ-COURSE-008"]}
func (s *CourseService) AgreeToSchedule(ctx context.Context, courseID, studentID uuid.UUID) (int, error) {
	_, err := s.GetCourse(ctx, courseID, studentID, "student")
	if err != nil {
		return 0, err
	}
	return s.repo.AgreeToSchedule(ctx, courseID)
}
