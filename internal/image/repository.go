// Package image implements image upload storage and retrieval backed by
// PostgreSQL bytea. All image data lives in the images table; RLS policies
// restrict student access to their own rows while server-role connections
// bypass RLS for the grading runner and vision call paths.
package image

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

// ErrNotFound is returned when an image row is not found.
var ErrNotFound = errors.New("image: not found")

// ErrForbidden is returned when the authenticated student does not own the image.
var ErrForbidden = errors.New("image: forbidden")

// ErrCapExceeded is returned when the per-student/course upload cap is reached.
var ErrCapExceeded = errors.New("image: per-course upload cap reached")

// ImageRow is the in-memory representation of an images table row.
type ImageRow struct {
	ID           uuid.UUID
	StudentID    uuid.UUID
	CourseID     uuid.UUID
	SubmissionID *uuid.UUID
	MimeType     string
	ByteSize     int64
	SHA256       string
	Data         []byte
	CreatedAt    time.Time
}

// Repository provides database access for the images table.
type Repository struct {
	pool *pgxpool.Pool
}

// @{"req": ["REQ-AGENT-023", "REQ-AGENT-024", "REQ-AGENT-025", "REQ-SUBMISSION-004", "REQ-SUBMISSION-005", "REQ-SUBMISSION-006"]}
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// conn returns the request-scoped connection from the auth middleware when one
// is present in ctx. Falls back to the bare pool for background callers.
//
// @{"req": ["REQ-AGENT-023", "REQ-AGENT-025"]}
func (r *Repository) conn(ctx context.Context) db.Querier {
	if c, ok := auth.ConnFromContext(ctx); ok {
		return c
	}
	return r.pool
}

// Insert stores a new image row. The caller must have already enforced the
// 5 MB size cap and MIME allowlist. The per-course rate cap check is
// the caller's responsibility (CountByStudentCourse before Insert).
// Uses a server-role connection so the images_server_insert_policy passes.
//
// @{"req": ["REQ-AGENT-023", "REQ-SUBMISSION-004"]}
func (r *Repository) Insert(ctx context.Context, studentID, courseID uuid.UUID, mimeType string, data []byte, sha256hex string) (ImageRow, error) {
	conn, err := db.AcquireServerConn(ctx, r.pool)
	if err != nil {
		return ImageRow{}, err
	}
	defer conn.Release()

	var row ImageRow
	err = conn.QueryRow(ctx,
		`INSERT INTO images (student_id, course_id, mime_type, byte_size, sha256, data)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, student_id, course_id, submission_id, mime_type, byte_size, sha256, data, created_at`,
		studentID, courseID, mimeType, int64(len(data)), sha256hex, data,
	).Scan(
		&row.ID,
		&row.StudentID,
		&row.CourseID,
		&row.SubmissionID,
		&row.MimeType,
		&row.ByteSize,
		&row.SHA256,
		&row.Data,
		&row.CreatedAt,
	)
	if err != nil {
		return ImageRow{}, err
	}
	return row, nil
}

// GetByID retrieves an image by ID. Returns ErrNotFound when the row does not
// exist. Ownership enforcement is the caller's responsibility: the handler must
// compare ImageRow.StudentID with the authenticated user or use a server conn.
// Uses the request-scoped connection so that the student RLS policy applies
// when the student is the caller; the grading runner injects a server-role conn.
//
// @{"req": ["REQ-AGENT-025"]}
func (r *Repository) GetByID(ctx context.Context, imageID uuid.UUID) (ImageRow, error) {
	var row ImageRow
	err := r.conn(ctx).QueryRow(ctx,
		`SELECT id, student_id, course_id, submission_id, mime_type, byte_size, sha256, data, created_at
		 FROM images
		 WHERE id = $1`,
		imageID,
	).Scan(
		&row.ID,
		&row.StudentID,
		&row.CourseID,
		&row.SubmissionID,
		&row.MimeType,
		&row.ByteSize,
		&row.SHA256,
		&row.Data,
		&row.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ImageRow{}, ErrNotFound
		}
		return ImageRow{}, err
	}
	return row, nil
}

// GetByIDServerConn retrieves an image using a server-role connection.
// Used by the GET /api/v1/images/{id} handler to serve the image bytes after
// the ownership check passes, so the server policy covers the SELECT.
//
// @{"req": ["REQ-AGENT-025"]}
func (r *Repository) GetByIDServerConn(ctx context.Context, imageID uuid.UUID) (ImageRow, error) {
	conn, err := db.AcquireServerConn(ctx, r.pool)
	if err != nil {
		return ImageRow{}, err
	}
	defer conn.Release()

	var row ImageRow
	err = conn.QueryRow(ctx,
		`SELECT id, student_id, course_id, submission_id, mime_type, byte_size, sha256, data, created_at
		 FROM images
		 WHERE id = $1`,
		imageID,
	).Scan(
		&row.ID,
		&row.StudentID,
		&row.CourseID,
		&row.SubmissionID,
		&row.MimeType,
		&row.ByteSize,
		&row.SHA256,
		&row.Data,
		&row.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ImageRow{}, ErrNotFound
		}
		return ImageRow{}, err
	}
	return row, nil
}

// CountByStudentCourse returns the number of images a student has uploaded for
// a given course. Used by the upload handler to enforce the 200-image cap.
// Uses a server-role connection so the server SELECT policy applies.
//
// @{"req": ["REQ-AGENT-023"]}
func (r *Repository) CountByStudentCourse(ctx context.Context, studentID, courseID uuid.UUID) (int, error) {
	conn, err := db.AcquireServerConn(ctx, r.pool)
	if err != nil {
		return 0, err
	}
	defer conn.Release()

	var count int
	err = conn.QueryRow(ctx,
		`SELECT COUNT(*) FROM images WHERE student_id = $1 AND course_id = $2`,
		studentID, courseID,
	).Scan(&count)
	return count, err
}

// GetByIDs loads a batch of images by their UUIDs using a server-role
// connection. Used by the chat handler to resolve attachment IDs before the
// Claude call, and by the grading runner to load submission images.
// Rows are returned in an unspecified order (callers that need ordering must
// sort). Missing IDs are silently omitted — the grading runner tolerates fewer
// rows than expected (dangling UUIDs after image deletion).
//
// @{"req": ["REQ-AGENT-023", "REQ-AGENT-025"]}
func (r *Repository) GetByIDs(ctx context.Context, ids []uuid.UUID) ([]ImageRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	conn, err := db.AcquireServerConn(ctx, r.pool)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	rows, err := conn.Query(ctx,
		`SELECT id, student_id, course_id, submission_id, mime_type, byte_size, sha256, data, created_at
		 FROM images
		 WHERE id = ANY($1)`,
		ids,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ImageRow
	for rows.Next() {
		var row ImageRow
		if err := rows.Scan(
			&row.ID,
			&row.StudentID,
			&row.CourseID,
			&row.SubmissionID,
			&row.MimeType,
			&row.ByteSize,
			&row.SHA256,
			&row.Data,
			&row.CreatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// ValidateOwnership checks that all provided image IDs exist and are owned by
// studentID in courseID. Returns ErrForbidden if any ID is invalid or
// foreign-owned. Used by the submission handler to validate image_ids before
// inserting the submission row.
//
// @{"req": ["REQ-SUBMISSION-005"]}
func (r *Repository) ValidateOwnership(ctx context.Context, ids []uuid.UUID, studentID, courseID uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	conn, err := db.AcquireServerConn(ctx, r.pool)
	if err != nil {
		return err
	}
	defer conn.Release()

	var count int
	err = conn.QueryRow(ctx,
		`SELECT COUNT(*) FROM images
		 WHERE id = ANY($1) AND student_id = $2 AND course_id = $3`,
		ids, studentID, courseID,
	).Scan(&count)
	if err != nil {
		return err
	}
	if count != len(ids) {
		return ErrForbidden
	}
	return nil
}
