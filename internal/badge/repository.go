package badge

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

var (
	ErrNotFound        = errors.New("badge: not found")
	ErrAlreadyAwarded  = errors.New("badge: already awarded to student")
	ErrAlreadyRedeemed = errors.New("badge: already redeemed")
)

// BadgeRow represents a badge definition from the badges table.
type BadgeRow struct {
	ID          uuid.UUID
	Name        string
	Description string
	Milestone   string
	Reward      string
	RewardValue float64
	CreatedAt   time.Time
}

// StudentBadgeRow represents an awarded badge joined with badge metadata.
type StudentBadgeRow struct {
	ID                      uuid.UUID
	StudentID               uuid.UUID
	BadgeID                 uuid.UUID
	AwardedAt               time.Time
	RedeemedForSubmissionID *uuid.UUID // nil until redeemed against a submission
	// Joined fields from badges table
	BadgeName        string
	BadgeReward      string
	BadgeRewardValue float64
}

// Repository wraps a pgxpool.Pool and provides data-access methods for badges.
// HTTP-serving methods route through conn(ctx) so that the request-scoped
// connection — which carries the RLS GUCs set by the auth middleware — is used
// instead of an unscoped pool connection.
type Repository struct {
	pool *pgxpool.Pool
}

// @{"req": ["REQ-BADGE-001", "REQ-BADGE-002", "REQ-BADGE-003"]}
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// conn returns the request-scoped connection stored in ctx by the auth
// middleware when one is present. This connection carries the app.current_user_id
// and app.current_role GUCs required for RLS evaluation. Falls back to the bare
// pool for background callers (e.g. agent workers) that have no auth context.
//
// @{"req": ["REQ-BADGE-001", "REQ-BADGE-002", "REQ-BADGE-003"]}
func (r *Repository) conn(ctx context.Context) db.Querier {
	if c, ok := auth.ConnFromContext(ctx); ok {
		return c
	}
	return r.pool
}

// ListBadges returns all badge definitions ordered by creation time.
//
// @{"req": ["REQ-BADGE-001", "REQ-BADGE-002", "REQ-BADGE-003"]}
func (r *Repository) ListBadges(ctx context.Context) ([]BadgeRow, error) {
	rows, err := r.conn(ctx).Query(ctx,
		`SELECT id, name, description, milestone, reward, reward_value, created_at
		 FROM badges
		 ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var badges []BadgeRow
	for rows.Next() {
		var b BadgeRow
		if err := rows.Scan(
			&b.ID,
			&b.Name,
			&b.Description,
			&b.Milestone,
			&b.Reward,
			&b.RewardValue,
			&b.CreatedAt,
		); err != nil {
			return nil, err
		}
		badges = append(badges, b)
	}
	return badges, rows.Err()
}

// GetStudentBadges returns all badges awarded to a student, joining badge metadata.
//
// @{"req": ["REQ-BADGE-001"]}
func (r *Repository) GetStudentBadges(ctx context.Context, studentID uuid.UUID) ([]StudentBadgeRow, error) {
	rows, err := r.conn(ctx).Query(ctx,
		`SELECT sb.id, sb.student_id, sb.badge_id, sb.awarded_at, sb.redeemed_for_submission_id,
		        b.name, b.reward, b.reward_value
		 FROM student_badges sb
		 JOIN badges b ON b.id = sb.badge_id
		 WHERE sb.student_id = $1
		 ORDER BY sb.awarded_at ASC`,
		studentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []StudentBadgeRow
	for rows.Next() {
		var sb StudentBadgeRow
		if err := rows.Scan(
			&sb.ID,
			&sb.StudentID,
			&sb.BadgeID,
			&sb.AwardedAt,
			&sb.RedeemedForSubmissionID,
			&sb.BadgeName,
			&sb.BadgeReward,
			&sb.BadgeRewardValue,
		); err != nil {
			return nil, err
		}
		result = append(result, sb)
	}
	return result, rows.Err()
}

// AwardBadge records that a student has earned a badge.
// Returns ErrAlreadyAwarded if the unique constraint (student_id, badge_id) fires.
//
// @{"req": ["REQ-BADGE-001"]}
func (r *Repository) AwardBadge(ctx context.Context, studentID, badgeID uuid.UUID) (StudentBadgeRow, error) {
	var sb StudentBadgeRow
	err := r.conn(ctx).QueryRow(ctx,
		`INSERT INTO student_badges (student_id, badge_id)
		 VALUES ($1, $2)
		 RETURNING id, student_id, badge_id, awarded_at, redeemed_for_submission_id`,
		studentID, badgeID).
		Scan(
			&sb.ID,
			&sb.StudentID,
			&sb.BadgeID,
			&sb.AwardedAt,
			&sb.RedeemedForSubmissionID,
		)
	if err != nil {
		// pgconn unique-violation code "23505" indicates duplicate award
		if isUniqueViolation(err) {
			return StudentBadgeRow{}, ErrAlreadyAwarded
		}
		return StudentBadgeRow{}, err
	}

	// Populate joined badge fields with a second query so the caller always
	// gets a complete row without requiring a CTE or LEFT JOIN on the INSERT.
	badge, err := r.getBadgeByID(ctx, badgeID)
	if err != nil {
		return StudentBadgeRow{}, err
	}
	sb.BadgeName = badge.Name
	sb.BadgeReward = badge.Reward
	sb.BadgeRewardValue = badge.RewardValue
	return sb, nil
}

// GetBadgeByMilestone returns the single badge associated with the given milestone.
// Returns ErrNotFound when no badge is configured for that milestone.
//
// @{"req": ["REQ-BADGE-001"]}
func (r *Repository) GetBadgeByMilestone(ctx context.Context, milestone string) (BadgeRow, error) {
	var b BadgeRow
	err := r.conn(ctx).QueryRow(ctx,
		`SELECT id, name, description, milestone, reward, reward_value, created_at
		 FROM badges
		 WHERE milestone::text = $1
		 LIMIT 1`,
		milestone).
		Scan(
			&b.ID,
			&b.Name,
			&b.Description,
			&b.Milestone,
			&b.Reward,
			&b.RewardValue,
			&b.CreatedAt,
		)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return BadgeRow{}, ErrNotFound
		}
		return BadgeRow{}, err
	}
	return b, nil
}

// HasBadge returns true if the student already holds the given badge.
//
// @{"req": ["REQ-BADGE-001"]}
func (r *Repository) HasBadge(ctx context.Context, studentID, badgeID uuid.UUID) (bool, error) {
	var exists bool
	err := r.conn(ctx).QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM student_badges
		   WHERE student_id = $1 AND badge_id = $2
		 )`,
		studentID, badgeID).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// RedeemBadge marks a student_badge as redeemed against a specific submission.
// Returns ErrNotFound if the student does not hold the badge.
// Returns ErrAlreadyRedeemed if the badge has already been redeemed for another
// submission — callers must distinguish this from "badge not held" so they can
// surface an appropriate error to the student rather than silently no-oping.
//
// @{"req": ["REQ-BADGE-002", "REQ-BADGE-003"]}
func (r *Repository) RedeemBadge(ctx context.Context, studentID, badgeID, submissionID uuid.UUID) error {
	tag, err := r.conn(ctx).Exec(ctx,
		`UPDATE student_badges
		 SET redeemed_for_submission_id = $3
		 WHERE student_id = $1 AND badge_id = $2 AND redeemed_for_submission_id IS NULL`,
		studentID, badgeID, submissionID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Distinguish "badge not held" from "badge already used" so callers can
		// surface the correct error. Check whether the row exists at all.
		var exists bool
		if err := r.conn(ctx).QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM student_badges WHERE student_id = $1 AND badge_id = $2)`,
			studentID, badgeID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return ErrAlreadyRedeemed
		}
		return ErrNotFound
	}
	return nil
}

// getBadgeByID is an internal helper that fetches a badge by its primary key.
//
// @{"req": ["REQ-BADGE-001"]}
func (r *Repository) getBadgeByID(ctx context.Context, id uuid.UUID) (BadgeRow, error) {
	var b BadgeRow
	err := r.conn(ctx).QueryRow(ctx,
		`SELECT id, name, description, milestone, reward, reward_value, created_at
		 FROM badges WHERE id = $1`,
		id).
		Scan(
			&b.ID,
			&b.Name,
			&b.Description,
			&b.Milestone,
			&b.Reward,
			&b.RewardValue,
			&b.CreatedAt,
		)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return BadgeRow{}, ErrNotFound
		}
		return BadgeRow{}, err
	}
	return b, nil
}

// isUniqueViolation returns true when err is a PostgreSQL unique-violation (23505).
// pgx v5 wraps *pgconn.PgError; we use errors.As to unwrap it and inspect the
// SQLSTATE code, consistent with the pattern used in internal/course/service.go.
//
// @{"req": ["REQ-BADGE-001"]}
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
