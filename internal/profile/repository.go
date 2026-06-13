// Package profile implements the persistent student learning profile.
// It stores a natural-language summary of a student's learning preferences
// and exposes CRUD operations that respect the FORCE ROW LEVEL SECURITY
// policies on learning_profiles and onboarding_messages.
package profile

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

var ErrNotFound = errors.New("profile: not found")

// ErrNoActiveSession is returned by Complete() when the student has no
// onboarding_messages rows (no session in progress or already completed).
// The handler maps this sentinel to HTTP 409 NO_ACTIVE_SESSION via errors.Is.
var ErrNoActiveSession = errors.New("onboarding: no active session")

// ProfileRow mirrors the learning_profiles table columns.
type ProfileRow struct {
	StudentID uuid.UUID
	Summary   string
	Source    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// OnboardingMessageRow mirrors the onboarding_messages table columns.
type OnboardingMessageRow struct {
	ID        uuid.UUID
	StudentID uuid.UUID
	Role      string
	Content   string
	CreatedAt time.Time
}

// ProfileRepository provides data access for learning_profiles and
// onboarding_messages.  The conn(ctx) helper picks up the request-scoped
// connection from the auth middleware context so that FORCE RLS evaluation uses
// the correct app.current_user_id and app.current_role GUCs — identical to the
// CourseRepository pattern in internal/course/repository.go.
type ProfileRepository struct {
	pool *pgxpool.Pool
}

// @{"req": ["REQ-PROFILE-001", "REQ-PROFILE-004", "REQ-PROFILE-005"]}
func NewProfileRepository(pool *pgxpool.Pool) *ProfileRepository {
	return &ProfileRepository{pool: pool}
}

// conn returns the request-scoped connection stored in ctx by the auth
// middleware when one is present.  That connection carries app.current_user_id
// and app.current_role GUCs required for RLS evaluation on learning_profiles
// (FORCE ROW LEVEL SECURITY).  Falls back to the bare pool for background
// callers (e.g. the profile loader's server-role path) that supply a
// server-role pool instead.
//
// CRITICAL: the bare pool's AfterRelease hook clears the RLS GUCs.  A direct
// pool query on a FORCE RLS table will silently return zero rows for SELECT and
// fail with a policy-violation error for INSERT/UPDATE.  HTTP handler paths
// MUST carry the auth middleware context so this helper picks up the
// request-scoped conn.
//
// @{"req": ["REQ-PROFILE-001", "REQ-PROFILE-004", "REQ-PROFILE-005", "REQ-PROFILE-017", "REQ-PROFILE-019"]}
func (r *ProfileRepository) conn(ctx context.Context) db.Querier {
	if c, ok := auth.ConnFromContext(ctx); ok {
		return c
	}
	return r.pool
}

// GetProfile reads the student's own profile via the request-scoped connection.
// Returns ErrNotFound when no row exists.
//
// @{"req": ["REQ-PROFILE-004"]}
func (r *ProfileRepository) GetProfile(ctx context.Context, studentID uuid.UUID) (ProfileRow, error) {
	var row ProfileRow
	err := r.conn(ctx).QueryRow(ctx,
		`SELECT student_id, summary, source, created_at, updated_at
		 FROM learning_profiles WHERE student_id = $1`,
		studentID,
	).Scan(&row.StudentID, &row.Summary, &row.Source, &row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProfileRow{}, ErrNotFound
		}
		return ProfileRow{}, fmt.Errorf("profile: get: %w", err)
	}
	return row, nil
}

// UpsertProfile creates or replaces the student's profile via the request-scoped
// connection.  The RLS student-write policy requires the correct GUCs; callers
// must ensure ctx carries the auth middleware connection.
//
// @{"req": ["REQ-PROFILE-004"]}
func (r *ProfileRepository) UpsertProfile(ctx context.Context, studentID uuid.UUID, summary, source string) (ProfileRow, error) {
	var row ProfileRow
	err := r.conn(ctx).QueryRow(ctx,
		`INSERT INTO learning_profiles (student_id, summary, source, updated_at)
		 VALUES ($1, $2, $3, now())
		 ON CONFLICT (student_id) DO UPDATE
		     SET summary = EXCLUDED.summary,
		         source  = EXCLUDED.source,
		         updated_at = now()
		 RETURNING student_id, summary, source, created_at, updated_at`,
		studentID, summary, source,
	).Scan(&row.StudentID, &row.Summary, &row.Source, &row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		return ProfileRow{}, fmt.Errorf("profile: upsert: %w", err)
	}
	return row, nil
}

// UpsertProfileAsServer writes the profile using a server-role connection
// acquired from serverPool.  This is the path used by the onboarding complete
// handler: the agent writes the summarised profile outside any HTTP request
// context, so no request-scoped conn is available.  The server RLS policies on
// learning_profiles allow INSERT and UPDATE when app.current_role = 'server'.
//
// @{"req": ["REQ-PROFILE-005", "REQ-PROFILE-012"]}
func (r *ProfileRepository) UpsertProfileAsServer(ctx context.Context, serverPool *pgxpool.Pool, studentID uuid.UUID, summary, source string) (ProfileRow, error) {
	conn, err := db.AcquireServerConn(ctx, serverPool)
	if err != nil {
		return ProfileRow{}, fmt.Errorf("profile: upsert as server: %w", err)
	}
	defer conn.Release()

	var row ProfileRow
	err = conn.QueryRow(ctx,
		`INSERT INTO learning_profiles (student_id, summary, source, updated_at)
		 VALUES ($1, $2, $3, now())
		 ON CONFLICT (student_id) DO UPDATE
		     SET summary = EXCLUDED.summary,
		         source  = EXCLUDED.source,
		         updated_at = now()
		 RETURNING student_id, summary, source, created_at, updated_at`,
		studentID, summary, source,
	).Scan(&row.StudentID, &row.Summary, &row.Source, &row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		return ProfileRow{}, fmt.Errorf("profile: upsert as server: %w", err)
	}
	return row, nil
}

// GetProfileAsServer reads any student's profile using a server-role connection.
// Used by the profile loader (LoadProfileSummary) for prompt injection.
// Returns ErrNotFound when the student has no profile row.
//
// @{"req": ["REQ-PROFILE-005"]}
func (r *ProfileRepository) GetProfileAsServer(ctx context.Context, serverPool *pgxpool.Pool, studentID uuid.UUID) (ProfileRow, error) {
	conn, err := db.AcquireServerConn(ctx, serverPool)
	if err != nil {
		return ProfileRow{}, fmt.Errorf("profile: get as server: %w", err)
	}
	defer conn.Release()

	var row ProfileRow
	err = conn.QueryRow(ctx,
		`SELECT student_id, summary, source, created_at, updated_at
		 FROM learning_profiles WHERE student_id = $1`,
		studentID,
	).Scan(&row.StudentID, &row.Summary, &row.Source, &row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProfileRow{}, ErrNotFound
		}
		return ProfileRow{}, fmt.Errorf("profile: get as server: %w", err)
	}
	return row, nil
}

// InsertOnboardingMessage stores a single turn in the onboarding conversation.
// Uses the server pool directly (all onboarding agent writes run server-side).
//
// @{"req": ["REQ-PROFILE-011"]}
func (r *ProfileRepository) InsertOnboardingMessage(ctx context.Context, serverPool *pgxpool.Pool, studentID uuid.UUID, role, content string) error {
	conn, err := db.AcquireServerConn(ctx, serverPool)
	if err != nil {
		return fmt.Errorf("profile: insert onboarding message: %w", err)
	}
	defer conn.Release()

	_, err = conn.Exec(ctx,
		`INSERT INTO onboarding_messages (student_id, role, content) VALUES ($1, $2, $3)`,
		studentID, role, content,
	)
	if err != nil {
		return fmt.Errorf("profile: insert onboarding message: %w", err)
	}
	return nil
}

// GetOnboardingHistory returns all messages for a student in creation order.
// Uses the server pool so the agent can read the full conversation history.
//
// @{"req": ["REQ-PROFILE-011", "REQ-PROFILE-012"]}
func (r *ProfileRepository) GetOnboardingHistory(ctx context.Context, serverPool *pgxpool.Pool, studentID uuid.UUID) ([]OnboardingMessageRow, error) {
	conn, err := db.AcquireServerConn(ctx, serverPool)
	if err != nil {
		return nil, fmt.Errorf("profile: get onboarding history: %w", err)
	}
	defer conn.Release()

	rows, err := conn.Query(ctx,
		`SELECT id, student_id, role, content, created_at
		 FROM onboarding_messages
		 WHERE student_id = $1
		 ORDER BY created_at ASC, id ASC`,
		studentID,
	)
	if err != nil {
		return nil, fmt.Errorf("profile: get onboarding history: %w", err)
	}
	defer rows.Close()

	var msgs []OnboardingMessageRow
	for rows.Next() {
		var m OnboardingMessageRow
		if err := rows.Scan(&m.ID, &m.StudentID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("profile: get onboarding history: scan: %w", err)
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("profile: get onboarding history: rows: %w", err)
	}
	return msgs, nil
}

// DeleteOnboardingMessages removes all onboarding_messages rows for a student.
// Called after the profile is successfully written so the table stays small.
//
// @{"req": ["REQ-PROFILE-012"]}
func (r *ProfileRepository) DeleteOnboardingMessages(ctx context.Context, serverPool *pgxpool.Pool, studentID uuid.UUID) error {
	conn, err := db.AcquireServerConn(ctx, serverPool)
	if err != nil {
		return fmt.Errorf("profile: delete onboarding messages: %w", err)
	}
	defer conn.Release()

	_, err = conn.Exec(ctx,
		`DELETE FROM onboarding_messages WHERE student_id = $1`,
		studentID,
	)
	if err != nil {
		return fmt.Errorf("profile: delete onboarding messages: %w", err)
	}
	return nil
}

// HasOnboardingMessages reports whether the student has any onboarding_messages
// rows (i.e. an active session). Uses a server conn for the read.
//
// @{"req": ["REQ-PROFILE-011"]}
func (r *ProfileRepository) HasOnboardingMessages(ctx context.Context, serverPool *pgxpool.Pool, studentID uuid.UUID) (bool, error) {
	conn, err := db.AcquireServerConn(ctx, serverPool)
	if err != nil {
		return false, fmt.Errorf("profile: has onboarding messages: %w", err)
	}
	defer conn.Release()

	var count int
	err = conn.QueryRow(ctx,
		`SELECT COUNT(*) FROM onboarding_messages WHERE student_id = $1`,
		studentID,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("profile: has onboarding messages: %w", err)
	}
	return count > 0, nil
}

// SetOnboardingPrompted updates users.onboarding_prompted = true for the
// student.  Uses the bare pool (users has no FORCE RLS, and the flag must be
// writable by both student and server paths).
//
// @{"req": ["REQ-PROFILE-014", "REQ-PROFILE-015"]}
func (r *ProfileRepository) SetOnboardingPrompted(ctx context.Context, studentID uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE users SET onboarding_prompted = true WHERE id = $1`,
		studentID,
	)
	if err != nil {
		return fmt.Errorf("profile: set onboarding prompted: %w", err)
	}
	return nil
}
