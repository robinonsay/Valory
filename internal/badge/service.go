package badge

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// badgeRepository is the data-access interface that Service depends on.
// *Repository satisfies this interface automatically.
// Defining the interface here (consumer side) follows the Go convention of
// keeping interfaces small and close to the code that uses them.
type badgeRepository interface {
	GetBadgeByMilestone(ctx context.Context, milestone string) (BadgeRow, error)
	HasBadge(ctx context.Context, studentID, badgeID uuid.UUID) (bool, error)
	AwardBadge(ctx context.Context, studentID, badgeID uuid.UUID) (StudentBadgeRow, error)
	ListBadges(ctx context.Context) ([]BadgeRow, error)
	GetStudentBadges(ctx context.Context, studentID uuid.UUID) ([]StudentBadgeRow, error)
	RedeemBadge(ctx context.Context, studentID, badgeID, submissionID uuid.UUID) error
}

// Service orchestrates badge award logic. It is called by the grade module
// after a submission is graded or after lesson feedback is submitted.
type Service struct {
	repo badgeRepository
}

// @{"req": ["REQ-BADGE-001", "REQ-BADGE-002", "REQ-BADGE-003"]}
func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// CheckAndAwardMilestone checks whether the student qualifies for the badge
// associated with the given milestone and awards it if they do not already hold
// it. The call is idempotent: if the student already holds the badge the method
// does nothing and returns nil.
//
// Milestone values must match the milestone_type ENUM defined in 006_badge.sql:
//   - "first_submission"
//   - "submitted_on_time"
//   - "perfect_score"
//   - "all_sections_complete"
//
// @{"req": ["REQ-BADGE-001"]}
func (s *Service) CheckAndAwardMilestone(ctx context.Context, studentID uuid.UUID, milestone string) error {
	b, err := s.repo.GetBadgeByMilestone(ctx, milestone)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// No badge is configured for this milestone; nothing to award.
			return nil
		}
		return err
	}

	// AwardBadge is the authoritative gate: if the student already holds the
	// badge the unique constraint fires and we get ErrAlreadyAwarded, which we
	// treat as a no-op so the caller can call this method freely on every event.
	_, err = s.repo.AwardBadge(ctx, studentID, b.ID)
	if err != nil {
		if errors.Is(err, ErrAlreadyAwarded) {
			return nil
		}
		return err
	}
	return nil
}

// GetStudentBadges returns all badges held by the student with badge metadata.
//
// @{"req": ["REQ-BADGE-001", "REQ-BADGE-002", "REQ-BADGE-003"]}
func (s *Service) GetStudentBadges(ctx context.Context, studentID uuid.UUID) ([]StudentBadgeRow, error) {
	return s.repo.GetStudentBadges(ctx, studentID)
}

// ListBadges returns all available badge definitions.
//
// @{"req": ["REQ-BADGE-001", "REQ-BADGE-002", "REQ-BADGE-003"]}
func (s *Service) ListBadges(ctx context.Context) ([]BadgeRow, error) {
	return s.repo.ListBadges(ctx)
}

// RedeemBadge redeems a badge against a specific submission. The grade module
// calls this when it applies a grade_improvement or late_penalty_waiver effect.
// Returns ErrNotFound if the student does not hold the badge.
//
// @{"req": ["REQ-BADGE-002", "REQ-BADGE-003"]}
func (s *Service) RedeemBadge(ctx context.Context, studentID, badgeID, submissionID uuid.UUID) error {
	return s.repo.RedeemBadge(ctx, studentID, badgeID, submissionID)
}
