package admin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/db"
)

// ErrCourseNotFound is returned when the requested course does not exist.
var ErrCourseNotFound = errors.New("admin: course not found")

// CourseOverviewSyllabus holds syllabus data for the admin oversight response.
type CourseOverviewSyllabus struct {
	ID          uuid.UUID
	ContentAdoc string
	Version     int
	ApprovedAt  *time.Time
	CreatedAt   time.Time
}

// CourseOverviewSection holds a single lesson-content section.
type CourseOverviewSection struct {
	ID               uuid.UUID
	SectionIndex     int
	Title            string
	ContentAdoc      string
	Version          int
	CitationVerified bool
	CreatedAt        time.Time
}

// CourseOverviewHomework holds homework + its due date for the admin oversight response.
type CourseOverviewHomework struct {
	ID           uuid.UUID
	SectionIndex int
	Title        string
	Rubric       string
	GradeWeight  float64
	CreatedAt    time.Time
	DueDate      *time.Time
}

// CourseOverviewSubmission holds a student submission row.
type CourseOverviewSubmission struct {
	ID             uuid.UUID
	HomeworkID     uuid.UUID
	StudentID      uuid.UUID
	Format         string
	FilePath       string
	FileSizeBytes  int64
	SubmittedAt    time.Time
	RawScore       *float64
	GradingStatus  string
}

// CourseOverviewGrade holds a grade row for the admin oversight response.
type CourseOverviewGrade struct {
	ID                  uuid.UUID
	SubmissionID        uuid.UUID
	HomeworkID          uuid.UUID
	RawScore            float64
	LateDays            int
	LatePenaltyRate     float64
	LatePenaltyAmount   float64
	BadgeWaiverApplied  bool
	BadgeImprovement    float64
	FinalScore          float64
	GradedAt            time.Time
}

// CourseOverviewCourse holds the top-level course summary.
type CourseOverviewCourse struct {
	ID        uuid.UUID
	StudentID uuid.UUID
	Username  string
	Topic     string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// OversightRepository provides admin-scoped read access to a student's full
// course material: syllabus, lesson content, homework, submissions, and grades.
// Every query must run on the request-scoped connection (conn(ctx)) so that the
// connection carries app.current_role='admin', satisfying the admin SELECT
// policies added by migration 021 (syllabi, homework, submissions) and those
// already present for lesson_content and grades.
//
// @{"req": ["REQ-ADMIN-011"]}
type OversightRepository struct {
	pool *pgxpool.Pool
}

// @{"req": ["REQ-ADMIN-011"]}
func NewOversightRepository(pool *pgxpool.Pool) *OversightRepository {
	return &OversightRepository{pool: pool}
}

// conn returns the request-scoped connection stored in ctx by the auth
// middleware. That connection carries app.current_role='admin' so all
// FORCE RLS tables allow the admin SELECT policies to fire correctly.
// Falls back to the bare pool for callers without a scoped connection
// (e.g. tests that drive the repository directly).
//
// @{"req": ["REQ-ADMIN-011"]}
func (r *OversightRepository) conn(ctx context.Context) db.Querier {
	if c, ok := auth.ConnFromContext(ctx); ok {
		return c
	}
	return r.pool
}

// GetCourse returns the top-level course row and the owning student's username.
// Returns ErrCourseNotFound if no course with the given id exists.
//
// @{"req": ["REQ-ADMIN-011"]}
func (r *OversightRepository) GetCourse(ctx context.Context, courseID uuid.UUID) (CourseOverviewCourse, error) {
	var row CourseOverviewCourse
	err := r.conn(ctx).QueryRow(ctx,
		`SELECT c.id, c.student_id, u.username, c.topic, c.status::text, c.created_at, c.updated_at
		 FROM courses c
		 JOIN users u ON u.id = c.student_id
		 WHERE c.id = $1`,
		courseID,
	).Scan(
		&row.ID,
		&row.StudentID,
		&row.Username,
		&row.Topic,
		&row.Status,
		&row.CreatedAt,
		&row.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CourseOverviewCourse{}, ErrCourseNotFound
		}
		return CourseOverviewCourse{}, fmt.Errorf("admin oversight: get course: %w", err)
	}
	return row, nil
}

// GetSyllabus returns the most recent syllabus for the course, or a zero value
// and no error when no syllabus exists yet.
//
// @{"req": ["REQ-ADMIN-011"]}
func (r *OversightRepository) GetSyllabus(ctx context.Context, courseID uuid.UUID) (CourseOverviewSyllabus, bool, error) {
	var row CourseOverviewSyllabus
	err := r.conn(ctx).QueryRow(ctx,
		`SELECT id, content_adoc, version, approved_at, created_at
		 FROM syllabi
		 WHERE course_id = $1
		 ORDER BY version DESC
		 LIMIT 1`,
		courseID,
	).Scan(
		&row.ID,
		&row.ContentAdoc,
		&row.Version,
		&row.ApprovedAt,
		&row.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CourseOverviewSyllabus{}, false, nil
		}
		return CourseOverviewSyllabus{}, false, fmt.Errorf("admin oversight: get syllabus: %w", err)
	}
	return row, true, nil
}

// ListSections returns all lesson_content rows for the course ordered by
// section_index, keeping only the latest version of each section.
//
// @{"req": ["REQ-ADMIN-011"]}
func (r *OversightRepository) ListSections(ctx context.Context, courseID uuid.UUID) ([]CourseOverviewSection, error) {
	// Distinct-on selects the row with the highest version per section_index.
	rows, err := r.conn(ctx).Query(ctx,
		`SELECT DISTINCT ON (section_index)
		        id, section_index, title, content_adoc, version, citation_verified, created_at
		 FROM lesson_content
		 WHERE course_id = $1
		 ORDER BY section_index ASC, version DESC`,
		courseID,
	)
	if err != nil {
		return nil, fmt.Errorf("admin oversight: list sections: query: %w", err)
	}
	defer rows.Close()

	var results []CourseOverviewSection
	for rows.Next() {
		var s CourseOverviewSection
		if err := rows.Scan(
			&s.ID, &s.SectionIndex, &s.Title, &s.ContentAdoc,
			&s.Version, &s.CitationVerified, &s.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("admin oversight: list sections: scan: %w", err)
		}
		results = append(results, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admin oversight: list sections: rows: %w", err)
	}
	return results, nil
}

// ListHomework returns all homework rows for the course with their due dates,
// ordered by section_index.
//
// @{"req": ["REQ-ADMIN-011"]}
func (r *OversightRepository) ListHomework(ctx context.Context, courseID uuid.UUID) ([]CourseOverviewHomework, error) {
	rows, err := r.conn(ctx).Query(ctx,
		`SELECT h.id, h.section_index, h.title, h.rubric, h.grade_weight, h.created_at,
		        d.due_date
		 FROM homework h
		 LEFT JOIN due_date_schedules d ON d.homework_id = h.id AND d.course_id = h.course_id
		 WHERE h.course_id = $1
		 ORDER BY h.section_index ASC`,
		courseID,
	)
	if err != nil {
		return nil, fmt.Errorf("admin oversight: list homework: query: %w", err)
	}
	defer rows.Close()

	var results []CourseOverviewHomework
	for rows.Next() {
		var h CourseOverviewHomework
		if err := rows.Scan(
			&h.ID, &h.SectionIndex, &h.Title, &h.Rubric, &h.GradeWeight, &h.CreatedAt,
			&h.DueDate,
		); err != nil {
			return nil, fmt.Errorf("admin oversight: list homework: scan: %w", err)
		}
		results = append(results, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admin oversight: list homework: rows: %w", err)
	}
	return results, nil
}

// ListSubmissions returns all submission rows for the course's student,
// scoped to homework that belongs to this course.
//
// @{"req": ["REQ-ADMIN-011"]}
func (r *OversightRepository) ListSubmissions(ctx context.Context, courseID uuid.UUID) ([]CourseOverviewSubmission, error) {
	rows, err := r.conn(ctx).Query(ctx,
		`SELECT s.id, s.homework_id, s.student_id, s.format::text,
		        s.file_path, s.file_size_bytes, s.submitted_at, s.raw_score, s.grading_status::text
		 FROM submissions s
		 JOIN homework h ON h.id = s.homework_id
		 WHERE h.course_id = $1
		 ORDER BY s.submitted_at ASC`,
		courseID,
	)
	if err != nil {
		return nil, fmt.Errorf("admin oversight: list submissions: query: %w", err)
	}
	defer rows.Close()

	var results []CourseOverviewSubmission
	for rows.Next() {
		var sub CourseOverviewSubmission
		if err := rows.Scan(
			&sub.ID, &sub.HomeworkID, &sub.StudentID, &sub.Format,
			&sub.FilePath, &sub.FileSizeBytes, &sub.SubmittedAt, &sub.RawScore, &sub.GradingStatus,
		); err != nil {
			return nil, fmt.Errorf("admin oversight: list submissions: scan: %w", err)
		}
		results = append(results, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admin oversight: list submissions: rows: %w", err)
	}
	return results, nil
}

// ListGrades returns all grade rows for the course, ordered by graded_at.
//
// @{"req": ["REQ-ADMIN-011"]}
func (r *OversightRepository) ListGrades(ctx context.Context, courseID uuid.UUID) ([]CourseOverviewGrade, error) {
	rows, err := r.conn(ctx).Query(ctx,
		`SELECT id, submission_id, homework_id, raw_score, late_days,
		        late_penalty_rate, late_penalty_amount, badge_waiver_applied,
		        badge_improvement, final_score, graded_at
		 FROM grades
		 WHERE course_id = $1
		 ORDER BY graded_at ASC`,
		courseID,
	)
	if err != nil {
		return nil, fmt.Errorf("admin oversight: list grades: query: %w", err)
	}
	defer rows.Close()

	var results []CourseOverviewGrade
	for rows.Next() {
		var g CourseOverviewGrade
		if err := rows.Scan(
			&g.ID, &g.SubmissionID, &g.HomeworkID, &g.RawScore, &g.LateDays,
			&g.LatePenaltyRate, &g.LatePenaltyAmount, &g.BadgeWaiverApplied,
			&g.BadgeImprovement, &g.FinalScore, &g.GradedAt,
		); err != nil {
			return nil, fmt.Errorf("admin oversight: list grades: scan: %w", err)
		}
		results = append(results, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admin oversight: list grades: rows: %w", err)
	}
	return results, nil
}
