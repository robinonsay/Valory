package submission

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/db"
)

var ErrNotFound = errors.New("submission: not found")
var ErrAlreadySubmitted = errors.New("submission: student already submitted for this homework")

// SubmissionRow is the in-memory representation of a submissions table row.
type SubmissionRow struct {
	ID            uuid.UUID
	HomeworkID    uuid.UUID
	StudentID     uuid.UUID
	Format        string     // "latex", "markdown", "asciidoc"
	FilePath      string
	FileSizeBytes int64
	SubmittedAt   time.Time
	RawScore      *float64   // nil until graded
	GradingStatus string     // "pending", "graded", "failed"
	ImageIDs      []uuid.UUID // nil when no images were attached (REQ-SUBMISSION-004)
}

// Repository provides database access for the submissions table.
type Repository struct {
	pool *pgxpool.Pool
}

// @{"req": ["REQ-SUBMISSION-001", "REQ-SUBMISSION-002", "REQ-SUBMISSION-003"]}
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// conn returns the request-scoped connection from the auth middleware when one
// is present in ctx. The auth middleware sets app.current_user_id on the
// connection so that RLS policies can evaluate correctly. Falls back to the
// bare pool for background callers (e.g. the grading agent runner) that have
// no user context and must use the server role connection instead.
//
// @{"req": ["REQ-SUBMISSION-001"]}
func (r *Repository) conn(ctx context.Context) db.Querier {
	if c, ok := auth.ConnFromContext(ctx); ok {
		return c
	}
	return r.pool
}

// Insert stores a new submission record. imageIDs is the list of image UUIDs
// attached at submission time (REQ-SUBMISSION-004); pass nil when none were
// attached. Returns ErrAlreadySubmitted on unique constraint violation (a
// student may only have one submission per homework item).
//
// @{"req": ["REQ-SUBMISSION-001", "REQ-SUBMISSION-004"]}
func (r *Repository) Insert(ctx context.Context, homeworkID, studentID uuid.UUID, format, filePath string, fileSizeBytes int64, imageIDs []uuid.UUID) (SubmissionRow, error) {
	// Convert empty slice to nil so the DB stores NULL rather than an empty
	// array; NULL is the documented sentinel for "no images attached" (migration 012).
	var dbImageIDs interface{}
	if len(imageIDs) > 0 {
		dbImageIDs = imageIDs
	}

	var row SubmissionRow
	err := r.conn(ctx).QueryRow(ctx,
		`INSERT INTO submissions (homework_id, student_id, format, file_path, file_size_bytes, image_ids)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, homework_id, student_id, format, file_path, file_size_bytes,
		           submitted_at, raw_score, grading_status, image_ids`,
		homeworkID, studentID, format, filePath, fileSizeBytes, dbImageIDs,
	).Scan(
		&row.ID,
		&row.HomeworkID,
		&row.StudentID,
		&row.Format,
		&row.FilePath,
		&row.FileSizeBytes,
		&row.SubmittedAt,
		&row.RawScore,
		&row.GradingStatus,
		&row.ImageIDs,
	)
	if err != nil {
		// pgx surfaces unique constraint violations as *pgconn.PgError with code 23505.
		// We convert that to the domain sentinel so callers don't import pgconn.
		if pgErr, ok := err.(*pgconn.PgError); ok && pgErr.Code == "23505" {
			return SubmissionRow{}, ErrAlreadySubmitted
		}
		return SubmissionRow{}, err
	}
	return row, nil
}

// GetLatestByHomework returns the submission for a given student/homework pair.
// Because of the unique constraint there is at most one row.
//
// @{"req": ["REQ-SUBMISSION-001", "REQ-SUBMISSION-004"]}
func (r *Repository) GetLatestByHomework(ctx context.Context, homeworkID, studentID uuid.UUID) (SubmissionRow, error) {
	var row SubmissionRow
	err := r.conn(ctx).QueryRow(ctx,
		`SELECT id, homework_id, student_id, format, file_path, file_size_bytes,
		        submitted_at, raw_score, grading_status, image_ids
		 FROM submissions
		 WHERE homework_id = $1 AND student_id = $2
		 ORDER BY submitted_at DESC
		 LIMIT 1`,
		homeworkID, studentID,
	).Scan(
		&row.ID,
		&row.HomeworkID,
		&row.StudentID,
		&row.Format,
		&row.FilePath,
		&row.FileSizeBytes,
		&row.SubmittedAt,
		&row.RawScore,
		&row.GradingStatus,
		&row.ImageIDs,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SubmissionRow{}, ErrNotFound
		}
		return SubmissionRow{}, err
	}
	return row, nil
}

// GetHomeworkCourseID returns the course_id for the given homework row.
// Used by the upload handler to verify the homework belongs to the course in
// the URL, preventing IDOR attacks where a student supplies a valid courseId
// but a homeworkId from another course.
//
// @{"req": ["REQ-SUBMISSION-001"]}
func (r *Repository) GetHomeworkCourseID(ctx context.Context, homeworkID uuid.UUID) (uuid.UUID, error) {
	var courseID uuid.UUID
	err := r.pool.QueryRow(ctx,
		`SELECT course_id FROM homework WHERE id = $1`,
		homeworkID,
	).Scan(&courseID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.UUID{}, ErrNotFound
		}
		return uuid.UUID{}, err
	}
	return courseID, nil
}

// HomeworkRow is the in-memory representation of a homework row joined with
// its optional due_date_schedules entry.
type HomeworkRow struct {
	ID       uuid.UUID
	CourseID uuid.UUID
	Title    string
	Rubric   string
	DueDate  *time.Time // nil when no due_date_schedules row exists for this homework
}

// GetHomework fetches a single homework row together with its due date (if
// one has been scheduled). The homework table has no RLS, so the bare pool is
// used rather than the RLS-scoped conn(ctx) helper. Callers are responsible
// for verifying course/student ownership before returning this data.
//
// @{"req": ["REQ-FECONTENT-020", "REQ-SUBMISSION-001"]}
func (r *Repository) GetHomework(ctx context.Context, homeworkID uuid.UUID) (HomeworkRow, error) {
	var row HomeworkRow
	err := r.pool.QueryRow(ctx,
		`SELECT h.id, h.course_id, h.title, h.rubric, d.due_date
		 FROM homework h
		 LEFT JOIN due_date_schedules d ON d.homework_id = h.id
		 WHERE h.id = $1`,
		homeworkID,
	).Scan(&row.ID, &row.CourseID, &row.Title, &row.Rubric, &row.DueDate)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return HomeworkRow{}, ErrNotFound
		}
		return HomeworkRow{}, err
	}
	return row, nil
}

// ListPendingGrading returns all submissions with grading_status = 'pending'.
// Used by the agent grading runner. Runs as server role (no RLS filter).
//
// @{"req": ["REQ-SUBMISSION-001", "REQ-SUBMISSION-004"]}
func (r *Repository) ListPendingGrading(ctx context.Context) ([]SubmissionRow, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, homework_id, student_id, format, file_path, file_size_bytes,
		        submitted_at, raw_score, grading_status, image_ids
		 FROM submissions
		 WHERE grading_status = 'pending'`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []SubmissionRow
	for rows.Next() {
		var row SubmissionRow
		if err := rows.Scan(
			&row.ID,
			&row.HomeworkID,
			&row.StudentID,
			&row.Format,
			&row.FilePath,
			&row.FileSizeBytes,
			&row.SubmittedAt,
			&row.RawScore,
			&row.GradingStatus,
			&row.ImageIDs,
		); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// SetRawScore stores the AI-assigned raw score and transitions grading_status
// to 'graded'. Called by the agent grading runner after a successful evaluation.
//
// @{"req": ["REQ-SUBMISSION-001"]}
func (r *Repository) SetRawScore(ctx context.Context, submissionID uuid.UUID, rawScore float64) error {
	// Use conn(ctx) so that when the grading runner injects a server-role
	// connection via auth.ContextWithConn, this UPDATE passes FORCE RLS on
	// submissions. The bare pool carries app.current_role='' which is blocked.
	tag, err := r.conn(ctx).Exec(ctx,
		`UPDATE submissions
		 SET raw_score = $1, grading_status = 'graded'
		 WHERE id = $2`,
		rawScore, submissionID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkGradingFailed transitions grading_status to 'failed'.
// Called by the agent grading runner when the AI API returns an error.
//
// @{"req": ["REQ-SUBMISSION-001"]}
func (r *Repository) MarkGradingFailed(ctx context.Context, submissionID uuid.UUID) error {
	// Use conn(ctx) for the same RLS reason as SetRawScore above.
	tag, err := r.conn(ctx).Exec(ctx,
		`UPDATE submissions
		 SET grading_status = 'failed'
		 WHERE id = $1`,
		submissionID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
