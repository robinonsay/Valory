package course

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidTransition = errors.New("course: invalid state transition")
	ErrNoPendingSchedule = errors.New("course: no pending due dates to agree")
	ErrNotFound          = errors.New("course: not found")
)

type CourseRow struct {
	ID                  uuid.UUID
	StudentID           uuid.UUID
	Title               string
	Topic               string
	Status              string
	PreWithdrawalStatus *string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type SyllabusRow struct {
	ID          uuid.UUID
	CourseID    uuid.UUID
	ContentAdoc string
	Version     int
	ApprovedAt  *time.Time
	CreatedAt   time.Time
}

type HomeworkRow struct {
	ID           uuid.UUID
	CourseID     uuid.UUID
	SectionIndex int
	Title        string
	Rubric       string
	GradeWeight  float64
	CreatedAt    time.Time
}

type DueDateRow struct {
	ID         uuid.UUID
	CourseID   uuid.UUID
	HomeworkID uuid.UUID
	DueDate    time.Time
	AgreedAt   *time.Time
	CreatedAt  time.Time
}

type CourseRepository struct {
	pool *pgxpool.Pool
}

type cursorPayload struct {
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002", "REQ-COURSE-003", "REQ-COURSE-004", "REQ-COURSE-005", "REQ-COURSE-006", "REQ-COURSE-007", "REQ-COURSE-008"]}
func NewRepository(pool *pgxpool.Pool) *CourseRepository {
	return &CourseRepository{pool: pool}
}

// @{"req": ["REQ-COURSE-001"]}
func (r *CourseRepository) CreateCourse(ctx context.Context, studentID uuid.UUID, topic string) (CourseRow, error) {
	var course CourseRow
	err := r.pool.QueryRow(ctx,
		`INSERT INTO courses (student_id, topic, status)
		 VALUES ($1, $2, 'intake')
		 RETURNING id, student_id, title, topic, status, pre_withdrawal_status, created_at, updated_at`,
		studentID, topic).
		Scan(
			&course.ID,
			&course.StudentID,
			&course.Title,
			&course.Topic,
			&course.Status,
			&course.PreWithdrawalStatus,
			&course.CreatedAt,
			&course.UpdatedAt,
		)
	if err != nil {
		return CourseRow{}, err
	}
	return course, nil
}

// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002"]}
func (r *CourseRepository) GetCourseByID(ctx context.Context, id uuid.UUID) (CourseRow, error) {
	var course CourseRow
	err := r.pool.QueryRow(ctx,
		`SELECT id, student_id, title, topic, status, pre_withdrawal_status, created_at, updated_at
		 FROM courses WHERE id = $1`,
		id).
		Scan(
			&course.ID,
			&course.StudentID,
			&course.Title,
			&course.Topic,
			&course.Status,
			&course.PreWithdrawalStatus,
			&course.CreatedAt,
			&course.UpdatedAt,
		)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CourseRow{}, ErrNotFound
		}
		return CourseRow{}, err
	}
	return course, nil
}

// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002"]}
func (r *CourseRepository) ListCourses(ctx context.Context, studentID *uuid.UUID, statusFilter string, cursor string, limit int) ([]CourseRow, string, error) {
	var query string
	var args []interface{}

	baseQuery := `SELECT id, student_id, title, topic, status, pre_withdrawal_status, created_at, updated_at
	             FROM courses
	             WHERE ($1::uuid IS NULL OR student_id = $1)
	               AND ($2 = '' OR status = $2)`

	if cursor == "" {
		query = baseQuery + `
	             ORDER BY created_at DESC, id DESC
	             LIMIT $3`
		args = []interface{}{studentID, statusFilter, limit + 1}
	} else {
		var payload cursorPayload
		decoded, err := base64.StdEncoding.DecodeString(cursor)
		if err != nil {
			return nil, "", err
		}
		if err := json.Unmarshal(decoded, &payload); err != nil {
			return nil, "", err
		}

		createdAt, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
		if err != nil {
			return nil, "", err
		}
		cursorID, err := uuid.Parse(payload.ID)
		if err != nil {
			return nil, "", fmt.Errorf("course: invalid cursor id: %w", err)
		}

		query = baseQuery + `
	               AND (created_at, id) < ($4, $5)
	               ORDER BY created_at DESC, id DESC
	               LIMIT $3`
		args = []interface{}{studentID, statusFilter, limit + 1, createdAt, cursorID}
	}

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var courses []CourseRow
	for rows.Next() {
		var course CourseRow
		if err := rows.Scan(
			&course.ID,
			&course.StudentID,
			&course.Title,
			&course.Topic,
			&course.Status,
			&course.PreWithdrawalStatus,
			&course.CreatedAt,
			&course.UpdatedAt,
		); err != nil {
			return nil, "", err
		}
		courses = append(courses, course)
	}

	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(courses) > limit {
		lastCourse := courses[limit]
		payload := cursorPayload{
			CreatedAt: lastCourse.CreatedAt.Format(time.RFC3339Nano),
			ID:        lastCourse.ID.String(),
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, "", err
		}
		nextCursor = base64.StdEncoding.EncodeToString(encoded)
		courses = courses[:limit]
	}

	return courses, nextCursor, nil
}

// @{"req": ["REQ-COURSE-003", "REQ-COURSE-004"]}
func (r *CourseRepository) Transition(ctx context.Context, id uuid.UUID, allowedFrom []string, newStatus string, preWithdrawalStatus *string) (CourseRow, error) {
	var course CourseRow
	err := r.pool.QueryRow(ctx,
		`UPDATE courses
		 SET status = $2,
		     pre_withdrawal_status = COALESCE($4, pre_withdrawal_status),
		     updated_at = now()
		 WHERE id = $1
		   AND status = ANY($3::text[])
		 RETURNING id, student_id, title, topic, status, pre_withdrawal_status, created_at, updated_at`,
		id, newStatus, allowedFrom, preWithdrawalStatus).
		Scan(
			&course.ID,
			&course.StudentID,
			&course.Title,
			&course.Topic,
			&course.Status,
			&course.PreWithdrawalStatus,
			&course.CreatedAt,
			&course.UpdatedAt,
		)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			existing, checkErr := r.GetCourseByID(ctx, id)
			if checkErr != nil {
				if errors.Is(checkErr, ErrNotFound) {
					return CourseRow{}, ErrNotFound
				}
				return CourseRow{}, checkErr
			}
			_ = existing
			return CourseRow{}, ErrInvalidTransition
		}
		return CourseRow{}, err
	}
	return course, nil
}

// @{"req": ["REQ-COURSE-005", "REQ-COURSE-006"]}
func (r *CourseRepository) GetLatestSyllabus(ctx context.Context, courseID uuid.UUID) (SyllabusRow, error) {
	var syllabus SyllabusRow
	err := r.pool.QueryRow(ctx,
		`SELECT id, course_id, content_adoc, version, approved_at, created_at
		 FROM syllabi
		 WHERE course_id = $1
		 ORDER BY version DESC
		 LIMIT 1`,
		courseID).
		Scan(
			&syllabus.ID,
			&syllabus.CourseID,
			&syllabus.ContentAdoc,
			&syllabus.Version,
			&syllabus.ApprovedAt,
			&syllabus.CreatedAt,
		)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SyllabusRow{}, ErrNotFound
		}
		return SyllabusRow{}, err
	}
	return syllabus, nil
}

// @{"req": ["REQ-COURSE-005", "REQ-COURSE-006"]}
func (r *CourseRepository) InsertSyllabus(ctx context.Context, courseID uuid.UUID, contentAdoc string, version int) (SyllabusRow, error) {
	var syllabus SyllabusRow
	err := r.pool.QueryRow(ctx,
		`INSERT INTO syllabi (course_id, content_adoc, version)
		 VALUES ($1, $2, $3)
		 RETURNING id, course_id, content_adoc, version, approved_at, created_at`,
		courseID, contentAdoc, version).
		Scan(
			&syllabus.ID,
			&syllabus.CourseID,
			&syllabus.ContentAdoc,
			&syllabus.Version,
			&syllabus.ApprovedAt,
			&syllabus.CreatedAt,
		)
	if err != nil {
		return SyllabusRow{}, err
	}
	return syllabus, nil
}

// Pool returns the underlying pool so the service can begin transactions.
//
// @{"req": ["REQ-COURSE-005", "REQ-COURSE-006"]}
func (r *CourseRepository) Pool() *pgxpool.Pool {
	return r.pool
}

// @{"req": ["REQ-COURSE-006"]}
func (r *CourseRepository) ApproveSyllabus(ctx context.Context, courseID, syllabusID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE syllabi SET approved_at = NOW() WHERE id = $1 AND course_id = $2`,
		syllabusID, courseID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// @{"req": ["REQ-COURSE-006"]}
func (r *CourseRepository) ApproveSyllabusTx(ctx context.Context, tx pgx.Tx, courseID, syllabusID uuid.UUID) error {
	tag, err := tx.Exec(ctx,
		`UPDATE syllabi SET approved_at = NOW() WHERE id = $1 AND course_id = $2`,
		syllabusID, courseID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// @{"req": ["REQ-COURSE-005", "REQ-COURSE-006"]}
func (r *CourseRepository) InsertSyllabusTx(ctx context.Context, tx pgx.Tx, courseID uuid.UUID, contentAdoc string, version int) (SyllabusRow, error) {
	var syllabus SyllabusRow
	err := tx.QueryRow(ctx,
		`INSERT INTO syllabi (course_id, content_adoc, version)
		 VALUES ($1, $2, $3)
		 RETURNING id, course_id, content_adoc, version, approved_at, created_at`,
		courseID, contentAdoc, version).
		Scan(
			&syllabus.ID,
			&syllabus.CourseID,
			&syllabus.ContentAdoc,
			&syllabus.Version,
			&syllabus.ApprovedAt,
			&syllabus.CreatedAt,
		)
	if err != nil {
		return SyllabusRow{}, err
	}
	return syllabus, nil
}

// getCourseByIDTx fetches a course by ID within a transaction.
//
// @{"req": ["REQ-COURSE-003", "REQ-COURSE-004", "REQ-COURSE-005", "REQ-COURSE-006"]}
func (r *CourseRepository) getCourseByIDTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (CourseRow, error) {
	var course CourseRow
	err := tx.QueryRow(ctx,
		`SELECT id, student_id, title, topic, status, pre_withdrawal_status, created_at, updated_at
		 FROM courses WHERE id = $1`,
		id).
		Scan(
			&course.ID,
			&course.StudentID,
			&course.Title,
			&course.Topic,
			&course.Status,
			&course.PreWithdrawalStatus,
			&course.CreatedAt,
			&course.UpdatedAt,
		)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CourseRow{}, ErrNotFound
		}
		return CourseRow{}, err
	}
	return course, nil
}

// @{"req": ["REQ-COURSE-003", "REQ-COURSE-004", "REQ-COURSE-005", "REQ-COURSE-006"]}
func (r *CourseRepository) TransitionTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, allowedFrom []string, newStatus string, preWithdrawalStatus *string) (CourseRow, error) {
	var course CourseRow
	err := tx.QueryRow(ctx,
		`UPDATE courses
		 SET status = $2,
		     pre_withdrawal_status = COALESCE($4, pre_withdrawal_status),
		     updated_at = now()
		 WHERE id = $1
		   AND status = ANY($3::text[])
		 RETURNING id, student_id, title, topic, status, pre_withdrawal_status, created_at, updated_at`,
		id, newStatus, allowedFrom, preWithdrawalStatus).
		Scan(
			&course.ID,
			&course.StudentID,
			&course.Title,
			&course.Topic,
			&course.Status,
			&course.PreWithdrawalStatus,
			&course.CreatedAt,
			&course.UpdatedAt,
		)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_, checkErr := r.getCourseByIDTx(ctx, tx, id)
			if checkErr != nil {
				if errors.Is(checkErr, ErrNotFound) {
					return CourseRow{}, ErrNotFound
				}
				return CourseRow{}, checkErr
			}
			return CourseRow{}, ErrInvalidTransition
		}
		return CourseRow{}, err
	}
	return course, nil
}

// @{"req": ["REQ-COURSE-007"]}
func (r *CourseRepository) InsertHomework(ctx context.Context, courseID uuid.UUID, sectionIndex int, title, rubric string, gradeWeight float64) (HomeworkRow, error) {
	var homework HomeworkRow
	err := r.pool.QueryRow(ctx,
		`INSERT INTO homework (course_id, section_index, title, rubric, grade_weight)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, course_id, section_index, title, rubric, grade_weight, created_at`,
		courseID, sectionIndex, title, rubric, gradeWeight).
		Scan(
			&homework.ID,
			&homework.CourseID,
			&homework.SectionIndex,
			&homework.Title,
			&homework.Rubric,
			&homework.GradeWeight,
			&homework.CreatedAt,
		)
	if err != nil {
		return HomeworkRow{}, err
	}
	return homework, nil
}

// @{"req": ["REQ-COURSE-007"]}
func (r *CourseRepository) InsertDueDateSchedule(ctx context.Context, courseID, homeworkID uuid.UUID, dueDate time.Time) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO due_date_schedules (course_id, homework_id, due_date)
		 VALUES ($1, $2, $3)`,
		courseID, homeworkID, dueDate)
	return err
}

// @{"req": ["REQ-COURSE-008"]}
func (r *CourseRepository) AgreeToSchedule(ctx context.Context, courseID uuid.UUID) (int, error) {
	result, err := r.pool.Exec(ctx,
		`UPDATE due_date_schedules
		 SET agreed_at = NOW()
		 WHERE course_id = $1 AND agreed_at IS NULL`,
		courseID)
	if err != nil {
		return 0, err
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		return 0, ErrNoPendingSchedule
	}

	return int(rowsAffected), nil
}
