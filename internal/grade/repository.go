// repository.go — database access for the grades table.
// Grades are written by the agent grading runner (server role, no RLS) and
// read by students through HTTP handlers (RLS-gated to the requesting student).
package grade

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/db"
)

// ErrNotFound is returned when no grade row exists for the requested key.
var ErrNotFound = errors.New("grade: not found")

// GradeRow is the in-memory representation of a grades table row.
type GradeRow struct {
	ID                 uuid.UUID
	SubmissionID       uuid.UUID
	HomeworkID         uuid.UUID
	StudentID          uuid.UUID
	CourseID           uuid.UUID
	RawScore           float64
	LateDays           int
	LatePenaltyRate    float64
	LatePenaltyAmount  float64
	BadgeWaiverApplied bool
	BadgeImprovement   float64
	FinalScore         float64
	GradedAt           time.Time
}

// CourseSummary carries the weighted grade average for a student's course.
// WeightedScore is the sum of (final_score * grade_weight) / SUM(grade_weight)
// over all graded homework assignments. TotalWeight is the denominator.
type CourseSummary struct {
	CourseID      uuid.UUID
	StudentID     uuid.UUID
	WeightedScore float64
	TotalWeight   float64
}

// Repository provides database access for grades.
// HTTP-serving methods route through conn(ctx) so that the request-scoped
// connection — which carries the RLS GUCs — is used when answering student
// requests. Background callers (agent runner) use r.pool directly.
type Repository struct {
	pool *pgxpool.Pool
}

// @{"req": ["REQ-GRADE-001", "REQ-GRADE-002", "REQ-GRADE-003", "REQ-GRADE-004", "REQ-GRADE-005"]}
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// conn returns the request-scoped connection from ctx when one is present
// (set by the auth middleware for HTTP handlers). Falls back to the bare pool
// for background callers such as the agent grading runner.
//
// @{"req": ["REQ-GRADE-001", "REQ-GRADE-002", "REQ-GRADE-004", "REQ-GRADE-005"]}
func (r *Repository) conn(ctx context.Context) db.Querier {
	if c, ok := auth.ConnFromContext(ctx); ok {
		return c
	}
	return r.pool
}

// Insert stores a computed grade record. The caller (grading service) populates
// all fields after calculating late penalties and badge effects.
// Uses conn(ctx) so that when the grading runner injects a server-role
// connection via auth.ContextWithConn, the INSERT passes FORCE RLS on grades
// (the server policy requires app.current_role = 'server'; the bare pool has
// that GUC cleared by the AfterRelease hook).
//
// @{"req": ["REQ-GRADE-001", "REQ-GRADE-002", "REQ-GRADE-004", "REQ-GRADE-005"]}
func (r *Repository) Insert(ctx context.Context, g GradeRow) (GradeRow, error) {
	err := r.conn(ctx).QueryRow(ctx,
		`INSERT INTO grades
		   (submission_id, homework_id, student_id, course_id,
		    raw_score, late_days, late_penalty_rate, late_penalty_amount,
		    badge_waiver_applied, badge_improvement, final_score)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 ON CONFLICT (submission_id) DO UPDATE
		   SET raw_score            = EXCLUDED.raw_score,
		       late_days            = EXCLUDED.late_days,
		       late_penalty_rate    = EXCLUDED.late_penalty_rate,
		       late_penalty_amount  = EXCLUDED.late_penalty_amount,
		       badge_waiver_applied = EXCLUDED.badge_waiver_applied,
		       badge_improvement    = EXCLUDED.badge_improvement,
		       final_score          = EXCLUDED.final_score,
		       graded_at            = now()
		 RETURNING id, submission_id, homework_id, student_id, course_id,
		           raw_score, late_days, late_penalty_rate, late_penalty_amount,
		           badge_waiver_applied, badge_improvement, final_score, graded_at`,
		g.SubmissionID,
		g.HomeworkID,
		g.StudentID,
		g.CourseID,
		g.RawScore,
		g.LateDays,
		g.LatePenaltyRate,
		g.LatePenaltyAmount,
		g.BadgeWaiverApplied,
		g.BadgeImprovement,
		g.FinalScore,
	).Scan(
		&g.ID,
		&g.SubmissionID,
		&g.HomeworkID,
		&g.StudentID,
		&g.CourseID,
		&g.RawScore,
		&g.LateDays,
		&g.LatePenaltyRate,
		&g.LatePenaltyAmount,
		&g.BadgeWaiverApplied,
		&g.BadgeImprovement,
		&g.FinalScore,
		&g.GradedAt,
	)
	if err != nil {
		return GradeRow{}, err
	}
	return g, nil
}

// GetBySubmission returns the grade row for a given submission.
// Routes through conn(ctx) so RLS is applied when called from an HTTP handler,
// restricting students to their own rows.
//
// @{"req": ["REQ-GRADE-001"]}
func (r *Repository) GetBySubmission(ctx context.Context, submissionID uuid.UUID) (GradeRow, error) {
	var g GradeRow
	err := r.conn(ctx).QueryRow(ctx,
		`SELECT id, submission_id, homework_id, student_id, course_id,
		        raw_score, late_days, late_penalty_rate, late_penalty_amount,
		        badge_waiver_applied, badge_improvement, final_score, graded_at
		 FROM grades
		 WHERE submission_id = $1`,
		submissionID,
	).Scan(
		&g.ID,
		&g.SubmissionID,
		&g.HomeworkID,
		&g.StudentID,
		&g.CourseID,
		&g.RawScore,
		&g.LateDays,
		&g.LatePenaltyRate,
		&g.LatePenaltyAmount,
		&g.BadgeWaiverApplied,
		&g.BadgeImprovement,
		&g.FinalScore,
		&g.GradedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GradeRow{}, ErrNotFound
		}
		return GradeRow{}, err
	}
	return g, nil
}

// getByHomeworkAndStudent returns the grade for the student's submission on a
// specific homework assignment. Routes through conn(ctx) for RLS.
// Lowercase because it is only used internally by the handler via the interface.
//
// @{"req": ["REQ-GRADE-001", "REQ-GRADE-002", "REQ-GRADE-005"]}
func (r *Repository) getByHomeworkAndStudent(ctx context.Context, homeworkID, studentID uuid.UUID) (GradeRow, error) {
	var g GradeRow
	err := r.conn(ctx).QueryRow(ctx,
		`SELECT id, submission_id, homework_id, student_id, course_id,
		        raw_score, late_days, late_penalty_rate, late_penalty_amount,
		        badge_waiver_applied, badge_improvement, final_score, graded_at
		 FROM grades
		 WHERE homework_id = $1 AND student_id = $2
		 LIMIT 1`,
		homeworkID, studentID,
	).Scan(
		&g.ID,
		&g.SubmissionID,
		&g.HomeworkID,
		&g.StudentID,
		&g.CourseID,
		&g.RawScore,
		&g.LateDays,
		&g.LatePenaltyRate,
		&g.LatePenaltyAmount,
		&g.BadgeWaiverApplied,
		&g.BadgeImprovement,
		&g.FinalScore,
		&g.GradedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GradeRow{}, ErrNotFound
		}
		return GradeRow{}, err
	}
	return g, nil
}

// GetCourseGradeSummary returns the weighted average final score for all graded
// homework in the student's course. The query joins grades with homework so that
// each homework's configured grade_weight drives the average. Only homework that
// has been graded (i.e. a grade row exists) contributes; ungraded assignments are
// excluded from both numerator and denominator to avoid dragging the average down
// before grading completes.
// Uses conn(ctx) so that HTTP handlers (which carry the student's RLS-scoped
// connection via auth middleware) see only their own rows. The bare pool has
// app.current_role='' and app.current_user_id='' after AfterRelease, causing
// the student-select policy to block all rows and return zeros.
//
// @{"req": ["REQ-GRADE-003"]}
func (r *Repository) GetCourseGradeSummary(ctx context.Context, courseID, studentID uuid.UUID) (CourseSummary, error) {
	var summary CourseSummary
	summary.CourseID = courseID
	summary.StudentID = studentID

	err := r.conn(ctx).QueryRow(ctx,
		`SELECT
		   COALESCE(SUM(g.final_score * h.grade_weight) / NULLIF(SUM(h.grade_weight), 0), 0),
		   COALESCE(SUM(h.grade_weight), 0)
		 FROM grades g
		 JOIN homework h ON h.id = g.homework_id
		 WHERE g.course_id  = $1
		   AND g.student_id = $2`,
		courseID, studentID,
	).Scan(&summary.WeightedScore, &summary.TotalWeight)
	if err != nil {
		return CourseSummary{}, err
	}
	return summary, nil
}
