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
	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/db"
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
	// AssignmentID is non-nil when the course was created via the admin
	// assignment flow (REQ-ASSIGN-003).  Nil means student-initiated.
	AssignmentID *uuid.UUID
	CreatedAt    time.Time
	UpdatedAt    time.Time
	// TreeMode is true when the course uses the layered knowledge-tree
	// generation pipeline (REQ-SYS-073). Added in migration 022; defaults to
	// false for all existing flat courses so this is backward-compatible.
	TreeMode bool
	// StudentUsername and StudentEmail are populated by the admin path of
	// ListCourses (REQ-FEADMIN-708) via a JOIN on users. Student path
	// (studentID != nil) leaves these empty.
	StudentUsername *string
	StudentEmail    *string
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

// conn returns the request-scoped connection stored in ctx by the auth
// middleware when one is present. That connection carries app.current_user_id
// and app.current_role GUCs required for RLS evaluation on the courses table
// (FORCE ROW LEVEL SECURITY). Falls back to the bare pool for background
// callers (e.g. agent pipeline) that supply a server-role pool instead.
//
// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002", "REQ-COURSE-003", "REQ-COURSE-004", "REQ-COURSE-005", "REQ-COURSE-006", "REQ-COURSE-007", "REQ-COURSE-008", "REQ-SECURITY-002"]}
func (r *CourseRepository) conn(ctx context.Context) db.Querier {
	if c, ok := auth.ConnFromContext(ctx); ok {
		return c
	}
	return r.pool
}

// BeginTx begins a database transaction on the request-scoped connection when
// one is present in ctx, or on the bare pool otherwise. This is critical for
// HTTP-handler-driven calls (ApproveSyllabus, RequestModification): those
// operations must run the transaction on the same connection that carries the
// RLS GUCs set by the auth middleware. Beginning a transaction on r.pool would
// acquire a fresh connection with empty GUCs, causing every table write to fail
// under FORCE RLS.
//
// pgxpool.Conn satisfies pgx.Tx's Begin contract (it exposes a Begin method).
// We use the *pgxpool.Conn directly rather than introducing a new interface so
// that no speculative abstraction is needed.
//
// @{"req": ["REQ-COURSE-005", "REQ-COURSE-006", "REQ-SECURITY-002"]}
func (r *CourseRepository) BeginTx(ctx context.Context) (pgx.Tx, error) {
	if c, ok := auth.ConnFromContext(ctx); ok {
		return c.Begin(ctx)
	}
	return r.pool.Begin(ctx)
}

// DeleteCourse permanently deletes a course and all of its material. agent_runs
// is ON DELETE RESTRICT from courses, so it (and its ON DELETE CASCADE
// pipeline_events) must be removed before the course; every other course child
// (syllabi, homework, course_nodes -> node_chats, chat_messages, grades,
// due_date_schedules, images, lesson_content, section_feedback,
// agent_token_usage) is ON DELETE CASCADE and is swept by the course delete.
// Runs on the request-scoped connection (BeginTx) so RLS applies under the
// caller's role. Returns the number of course rows deleted (0 = not visible).
//
// @{"req": ["REQ-COURSE-001"]}
func (r *CourseRepository) DeleteCourse(ctx context.Context, courseID uuid.UUID) (int64, error) {
	tx, err := r.BeginTx(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, `DELETE FROM agent_runs WHERE course_id = $1`, courseID); err != nil {
		return 0, err
	}
	ct, err := tx.Exec(ctx, `DELETE FROM courses WHERE id = $1`, courseID)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// @{"req": ["REQ-COURSE-001"]}
func (r *CourseRepository) CreateCourse(ctx context.Context, studentID uuid.UUID, topic string) (CourseRow, error) {
	var course CourseRow
	err := r.conn(ctx).QueryRow(ctx,
		`INSERT INTO courses (student_id, topic, status)
		 VALUES ($1, $2, 'intake')
		 RETURNING id, student_id, title, topic, status, pre_withdrawal_status, assignment_id, created_at, updated_at`,
		studentID, topic).
		Scan(
			&course.ID,
			&course.StudentID,
			&course.Title,
			&course.Topic,
			&course.Status,
			&course.PreWithdrawalStatus,
			&course.AssignmentID,
			&course.CreatedAt,
			&course.UpdatedAt,
		)
	if err != nil {
		return CourseRow{}, err
	}
	return course, nil
}

// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002", "REQ-SYS-073", "REQ-SYS-077"]}
func (r *CourseRepository) GetCourseByID(ctx context.Context, id uuid.UUID) (CourseRow, error) {
	var course CourseRow
	err := r.conn(ctx).QueryRow(ctx,
		`SELECT id, student_id, title, topic, status, pre_withdrawal_status, assignment_id, created_at, updated_at, tree_mode
		 FROM courses WHERE id = $1`,
		id).
		Scan(
			&course.ID,
			&course.StudentID,
			&course.Title,
			&course.Topic,
			&course.Status,
			&course.PreWithdrawalStatus,
			&course.AssignmentID,
			&course.CreatedAt,
			&course.UpdatedAt,
			&course.TreeMode,
		)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CourseRow{}, ErrNotFound
		}
		return CourseRow{}, err
	}
	return course, nil
}

// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002", "REQ-FEADMIN-708", "REQ-SYS-073", "REQ-SYS-077"]}
func (r *CourseRepository) ListCourses(ctx context.Context, studentID *uuid.UUID, statusFilter string, cursor string, limit int) ([]CourseRow, string, error) {
	var query string
	var args []interface{}

	// Admin path (studentID == nil): join users to include student_username and
	// student_email. Student path: no join, StudentUsername/StudentEmail remain nil.
	var baseQuery string
	if studentID == nil {
		// Admin path: include username and email via join on users table.
		baseQuery = `SELECT c.id, c.student_id, c.title, c.topic, c.status, c.pre_withdrawal_status, c.assignment_id, c.created_at, c.updated_at, c.tree_mode, u.username, u.email
		             FROM courses c
		             LEFT JOIN users u ON u.id = c.student_id
		             WHERE ($1::uuid IS NULL OR c.student_id = $1)
		               AND ($2 = '' OR c.status::text = $2)`
	} else {
		// Student path: no join, unchanged response shape.
		baseQuery = `SELECT c.id, c.student_id, c.title, c.topic, c.status, c.pre_withdrawal_status, c.assignment_id, c.created_at, c.updated_at, c.tree_mode, NULL::text, NULL::text
		             FROM courses c
		             WHERE ($1::uuid IS NULL OR c.student_id = $1)
		               AND ($2 = '' OR c.status::text = $2)`
	}

	if cursor == "" {
		query = baseQuery + `
	             ORDER BY c.created_at DESC, c.id DESC
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
	               AND (c.created_at, c.id) < ($4, $5)
	               ORDER BY c.created_at DESC, c.id DESC
	               LIMIT $3`
		args = []interface{}{studentID, statusFilter, limit + 1, createdAt, cursorID}
	}

	rows, err := r.conn(ctx).Query(ctx, query, args...)
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
			&course.AssignmentID,
			&course.CreatedAt,
			&course.UpdatedAt,
			&course.TreeMode,
			&course.StudentUsername,
			&course.StudentEmail,
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

// @{"req": ["REQ-COURSE-003", "REQ-COURSE-004", "REQ-SYS-073", "REQ-SYS-077"]}
func (r *CourseRepository) Transition(ctx context.Context, id uuid.UUID, allowedFrom []string, newStatus string, preWithdrawalStatus *string) (CourseRow, error) {
	var course CourseRow
	err := r.conn(ctx).QueryRow(ctx,
		`UPDATE courses
		 SET status = $2,
		     pre_withdrawal_status = COALESCE($4, pre_withdrawal_status),
		     updated_at = now()
		 WHERE id = $1
		   AND status::text = ANY($3::text[])
		 RETURNING id, student_id, title, topic, status, pre_withdrawal_status, assignment_id, created_at, updated_at, tree_mode`,
		id, newStatus, allowedFrom, preWithdrawalStatus).
		Scan(
			&course.ID,
			&course.StudentID,
			&course.Title,
			&course.Topic,
			&course.Status,
			&course.PreWithdrawalStatus,
			&course.AssignmentID,
			&course.CreatedAt,
			&course.UpdatedAt,
			&course.TreeMode,
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
	err := r.conn(ctx).QueryRow(ctx,
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
	err := r.conn(ctx).QueryRow(ctx,
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

// Pool returns the underlying pool so callers outside this package that need
// direct pool access (e.g. the submission handler ownership check) can still
// reach it. HTTP-handler driven repository methods use conn(ctx) instead.
//
// @{"req": ["REQ-COURSE-005", "REQ-COURSE-006"]}
func (r *CourseRepository) Pool() *pgxpool.Pool {
	return r.pool
}

// @{"req": ["REQ-COURSE-006"]}
func (r *CourseRepository) ApproveSyllabus(ctx context.Context, courseID, syllabusID uuid.UUID) error {
	tag, err := r.conn(ctx).Exec(ctx,
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
// @{"req": ["REQ-COURSE-003", "REQ-COURSE-004", "REQ-COURSE-005", "REQ-COURSE-006", "REQ-SYS-073", "REQ-SYS-077"]}
func (r *CourseRepository) getCourseByIDTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (CourseRow, error) {
	var course CourseRow
	err := tx.QueryRow(ctx,
		`SELECT id, student_id, title, topic, status, pre_withdrawal_status, assignment_id, created_at, updated_at, tree_mode
		 FROM courses WHERE id = $1`,
		id).
		Scan(
			&course.ID,
			&course.StudentID,
			&course.Title,
			&course.Topic,
			&course.Status,
			&course.PreWithdrawalStatus,
			&course.AssignmentID,
			&course.CreatedAt,
			&course.UpdatedAt,
			&course.TreeMode,
		)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CourseRow{}, ErrNotFound
		}
		return CourseRow{}, err
	}
	return course, nil
}

// @{"req": ["REQ-COURSE-003", "REQ-COURSE-004", "REQ-COURSE-005", "REQ-COURSE-006", "REQ-SYS-073", "REQ-SYS-077"]}
func (r *CourseRepository) TransitionTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, allowedFrom []string, newStatus string, preWithdrawalStatus *string) (CourseRow, error) {
	var course CourseRow
	err := tx.QueryRow(ctx,
		`UPDATE courses
		 SET status = $2,
		     pre_withdrawal_status = COALESCE($4, pre_withdrawal_status),
		     updated_at = now()
		 WHERE id = $1
		   AND status::text = ANY($3::text[])
		 RETURNING id, student_id, title, topic, status, pre_withdrawal_status, assignment_id, created_at, updated_at, tree_mode`,
		id, newStatus, allowedFrom, preWithdrawalStatus).
		Scan(
			&course.ID,
			&course.StudentID,
			&course.Title,
			&course.Topic,
			&course.Status,
			&course.PreWithdrawalStatus,
			&course.AssignmentID,
			&course.CreatedAt,
			&course.UpdatedAt,
			&course.TreeMode,
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
	err := r.conn(ctx).QueryRow(ctx,
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
	_, err := r.conn(ctx).Exec(ctx,
		`INSERT INTO due_date_schedules (course_id, homework_id, due_date)
		 VALUES ($1, $2, $3)`,
		courseID, homeworkID, dueDate)
	return err
}

// @{"req": ["REQ-COURSE-001", "REQ-COURSE-007"]}
func (r *CourseRepository) ListHomeworkByCourseID(ctx context.Context, courseID uuid.UUID) ([]HomeworkRow, error) {
	rows, err := r.conn(ctx).Query(ctx,
		`SELECT h.id, h.course_id, h.section_index, h.title, h.rubric, h.grade_weight, h.created_at
		 FROM homework h
		 WHERE h.course_id = $1
		 ORDER BY h.section_index ASC`,
		courseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var homework []HomeworkRow
	for rows.Next() {
		var hw HomeworkRow
		if err := rows.Scan(
			&hw.ID,
			&hw.CourseID,
			&hw.SectionIndex,
			&hw.Title,
			&hw.Rubric,
			&hw.GradeWeight,
			&hw.CreatedAt,
		); err != nil {
			return nil, err
		}
		homework = append(homework, hw)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return homework, nil
}

// @{"req": ["REQ-COURSE-007"]}
func (r *CourseRepository) ListDueDatesByCourseID(ctx context.Context, courseID uuid.UUID) (map[uuid.UUID]time.Time, error) {
	rows, err := r.conn(ctx).Query(ctx,
		`SELECT homework_id, due_date
		 FROM due_date_schedules
		 WHERE course_id = $1`,
		courseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	dueDates := make(map[uuid.UUID]time.Time)
	for rows.Next() {
		var homeworkID uuid.UUID
		var dueDate time.Time
		if err := rows.Scan(&homeworkID, &dueDate); err != nil {
			return nil, err
		}
		dueDates[homeworkID] = dueDate
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return dueDates, nil
}

// @{"req": ["REQ-COURSE-008"]}
func (r *CourseRepository) AgreeToSchedule(ctx context.Context, courseID uuid.UUID) (int, error) {
	result, err := r.conn(ctx).Exec(ctx,
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
