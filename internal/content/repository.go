package content

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

var ErrNotFound = errors.New("content: section not found")
var ErrNotVerified = errors.New("content: section exists but citation not yet verified")

type LessonContentRow struct {
	ID               uuid.UUID
	CourseID         uuid.UUID
	SectionIndex     int
	Title            string
	ContentAdoc      string
	Version          int
	CitationVerified bool
	CreatedAt        time.Time
}

type SectionFeedbackRow struct {
	ID                    uuid.UUID
	StudentID             uuid.UUID
	CourseID              uuid.UUID
	SectionIndex          int
	FeedbackText          string
	SubmittedAt           time.Time
	RegenerationTriggered bool
}

type ContentRepository struct {
	pool *pgxpool.Pool
}

// @{"req": ["REQ-CONTENT-001", "REQ-CONTENT-004", "REQ-AGENT-004", "REQ-SYS-013", "REQ-SYS-033", "REQ-SYS-034"]}
func NewContentRepository(pool *pgxpool.Pool) *ContentRepository {
	return &ContentRepository{
		pool: pool,
	}
}

// conn returns the request-scoped connection set by the auth middleware when
// one is available. Falls back to the bare pool for background callers that
// have no auth context (auth middleware sets RLS GUCs on the connection).
func (r *ContentRepository) conn(ctx context.Context) db.Querier {
	if conn, ok := auth.ConnFromContext(ctx); ok {
		return conn
	}
	return r.pool
}

// @{"req": ["REQ-CONTENT-001", "REQ-CONTENT-004", "REQ-AGENT-004", "REQ-SYS-013", "REQ-SYS-033", "REQ-SYS-034"]}
func (r *ContentRepository) InsertLessonContent(ctx context.Context, courseID uuid.UUID, sectionIndex int, title, contentAdoc string) (LessonContentRow, error) {
	query := `
		INSERT INTO lesson_content (course_id, section_index, title, content_adoc, version)
		VALUES ($1, $2, $3, $4, COALESCE((SELECT MAX(version) FROM lesson_content WHERE course_id=$1 AND section_index=$2), 0) + 1)
		RETURNING id, course_id, section_index, title, content_adoc, version, citation_verified, created_at
	`

	var row LessonContentRow
	err := r.conn(ctx).QueryRow(ctx, query, courseID, sectionIndex, title, contentAdoc).Scan(
		&row.ID,
		&row.CourseID,
		&row.SectionIndex,
		&row.Title,
		&row.ContentAdoc,
		&row.Version,
		&row.CitationVerified,
		&row.CreatedAt,
	)
	if err != nil {
		return LessonContentRow{}, err
	}

	return row, nil
}

// @{"req": ["REQ-CONTENT-001", "REQ-CONTENT-004", "REQ-AGENT-004", "REQ-SYS-013", "REQ-SYS-033", "REQ-SYS-034"]}
func (r *ContentRepository) SetCitationVerified(ctx context.Context, contentID uuid.UUID) error {
	query := `UPDATE lesson_content SET citation_verified = true WHERE id = $1`
	_, err := r.conn(ctx).Exec(ctx, query, contentID)
	return err
}

// @{"req": ["REQ-CONTENT-001", "REQ-CONTENT-004", "REQ-AGENT-004", "REQ-SYS-013", "REQ-SYS-033", "REQ-SYS-034"]}
func (r *ContentRepository) GetSectionContent(ctx context.Context, courseID uuid.UUID, sectionIndex int) (LessonContentRow, error) {
	var row LessonContentRow
	err := r.conn(ctx).QueryRow(ctx,
		`SELECT id, course_id, section_index, title, content_adoc, version, citation_verified, created_at
		 FROM lesson_content
		 WHERE course_id = $1 AND section_index = $2
		 ORDER BY version DESC
		 LIMIT 1`,
		courseID, sectionIndex,
	).Scan(&row.ID, &row.CourseID, &row.SectionIndex, &row.Title, &row.ContentAdoc,
		&row.Version, &row.CitationVerified, &row.CreatedAt)

	if errors.Is(err, pgx.ErrNoRows) {
		return LessonContentRow{}, ErrNotFound
	}
	if err != nil {
		return LessonContentRow{}, err
	}
	if !row.CitationVerified {
		return LessonContentRow{}, ErrNotVerified
	}
	return row, nil
}

// @{"req": ["REQ-CONTENT-001", "REQ-CONTENT-004", "REQ-AGENT-004", "REQ-SYS-013", "REQ-SYS-033", "REQ-SYS-034"]}
func (r *ContentRepository) FindMatchingContent(ctx context.Context, topic string, threshold float32) ([]LessonContentRow, error) {
	query := `
		SELECT id, course_id, section_index, title, content_adoc, version, citation_verified, created_at
		FROM lesson_content
		WHERE similarity(title, $1) > $2
		  AND citation_verified = true
		ORDER BY similarity(title, $1) DESC
		LIMIT 5
	`

	rows, err := r.conn(ctx).Query(ctx, query, topic, threshold)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []LessonContentRow
	for rows.Next() {
		var row LessonContentRow
		err := rows.Scan(
			&row.ID,
			&row.CourseID,
			&row.SectionIndex,
			&row.Title,
			&row.ContentAdoc,
			&row.Version,
			&row.CitationVerified,
			&row.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		results = append(results, row)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return results, nil
}

// @{"req": ["REQ-CONTENT-001", "REQ-CONTENT-004", "REQ-AGENT-004", "REQ-SYS-013", "REQ-SYS-033", "REQ-SYS-034"]}
func (r *ContentRepository) InsertFeedback(ctx context.Context, studentID, courseID uuid.UUID, sectionIndex int, feedbackText string) (SectionFeedbackRow, error) {
	query := `
		INSERT INTO section_feedback (student_id, course_id, section_index, feedback_text)
		VALUES ($1, $2, $3, $4)
		RETURNING id, student_id, course_id, section_index, feedback_text, submitted_at, regeneration_triggered
	`

	var row SectionFeedbackRow
	err := r.conn(ctx).QueryRow(ctx, query, studentID, courseID, sectionIndex, feedbackText).Scan(
		&row.ID,
		&row.StudentID,
		&row.CourseID,
		&row.SectionIndex,
		&row.FeedbackText,
		&row.SubmittedAt,
		&row.RegenerationTriggered,
	)
	if err != nil {
		return SectionFeedbackRow{}, err
	}

	return row, nil
}

// @{"req": ["REQ-CONTENT-001", "REQ-CONTENT-004", "REQ-AGENT-004", "REQ-SYS-013", "REQ-SYS-033", "REQ-SYS-034"]}
func (r *ContentRepository) SetRegenerationTriggered(ctx context.Context, feedbackID uuid.UUID) error {
	query := `UPDATE section_feedback SET regeneration_triggered = true WHERE id = $1`
	_, err := r.conn(ctx).Exec(ctx, query, feedbackID)
	return err
}

// @{"req": ["REQ-CONTENT-001", "REQ-CONTENT-004", "REQ-AGENT-004", "REQ-SYS-013", "REQ-SYS-033", "REQ-SYS-034"]}
func (r *ContentRepository) ListFeedback(ctx context.Context, studentID, courseID uuid.UUID, sectionIndex int) ([]SectionFeedbackRow, error) {
	query := `
		SELECT id, student_id, course_id, section_index, feedback_text, submitted_at, regeneration_triggered
		FROM section_feedback
		WHERE student_id=$1 AND course_id=$2 AND section_index=$3
		ORDER BY submitted_at DESC
	`

	rows, err := r.conn(ctx).Query(ctx, query, studentID, courseID, sectionIndex)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SectionFeedbackRow
	for rows.Next() {
		var row SectionFeedbackRow
		err := rows.Scan(
			&row.ID,
			&row.StudentID,
			&row.CourseID,
			&row.SectionIndex,
			&row.FeedbackText,
			&row.SubmittedAt,
			&row.RegenerationTriggered,
		)
		if err != nil {
			return nil, err
		}
		results = append(results, row)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return results, nil
}
