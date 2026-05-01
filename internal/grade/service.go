// service.go — grade computation logic.
// ComputeAndStoreGrade implements the full late-penalty and badge-effect
// pipeline defined in REQ-GRADE-001 through REQ-GRADE-005.
package grade

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/badge"
	"github.com/valory/valory/internal/db"
)

// badgeServiceInterface is the subset of badge.Service the grade module needs.
// Defined here (consumer side) following Go's small-interface convention.
type badgeServiceInterface interface {
	CheckAndAwardMilestone(ctx context.Context, studentID uuid.UUID, milestone string) error
	GetStudentBadges(ctx context.Context, studentID uuid.UUID) ([]badge.StudentBadgeRow, error)
	RedeemBadge(ctx context.Context, studentID, badgeID, submissionID uuid.UUID) error
}

// gradeRepositoryInterface is the subset of Repository the service uses.
// Defined here so tests can inject a stub without a real database.
type gradeRepositoryInterface interface {
	Insert(ctx context.Context, g GradeRow) (GradeRow, error)
	GetCourseGradeSummary(ctx context.Context, courseID, studentID uuid.UUID) (CourseSummary, error)
}

// Service computes and persists grades. Wired by the agent runner; not used
// directly by HTTP handlers (students only read grades, never compute them).
type Service struct {
	repo      gradeRepositoryInterface
	// pool is used as the fallback Querier when no connection is in ctx.
	// Accepts db.Querier rather than *pgxpool.Pool so unit tests can inject a
	// stub without requiring a real PostgreSQL connection.
	pool      db.Querier
	badgeSvc  badgeServiceInterface
	configSvc interface {
		GetFloat64(string) float64
	}
}

// @{"req": ["REQ-GRADE-001", "REQ-GRADE-002", "REQ-GRADE-003", "REQ-GRADE-004", "REQ-GRADE-005"]}
func NewService(
	repo gradeRepositoryInterface,
	pool db.Querier,
	badgeSvc badgeServiceInterface,
	configSvc interface{ GetFloat64(string) float64 },
) *Service {
	return &Service{
		repo:      repo,
		pool:      pool,
		badgeSvc:  badgeSvc,
		configSvc: configSvc,
	}
}

// ComputeAndStoreGrade calculates the final grade for a submission and persists
// it. This is the primary entry point called by the agent grading runner.
//
// Calculation pipeline:
//  1. Load submitted_at from the submission and due_date from due_date_schedules.
//  2. Count late_days = MAX(0, CEIL((submitted_at - due_date) / 24h)).
//  3. Read late_penalty_rate from configSvc (REQ-GRADE-002).
//  4. Compute late_penalty_amount = MIN(raw_score, raw_score * rate * days) (REQ-GRADE-001).
//  5. If student has an unredeemed late_penalty_waiver badge, zero the penalty
//     and redeem the badge atomically (REQ-GRADE-005).
//  6. final_score = CLAMP(raw_score - penalty, 0, 100).
//  7. Persist the grade row.
//  8. Award milestone badges based on outcome (first submission, on-time, perfect).
//
// @{"req": ["REQ-GRADE-001", "REQ-GRADE-002", "REQ-GRADE-004", "REQ-GRADE-005"]}
func (s *Service) ComputeAndStoreGrade(
	ctx context.Context,
	submissionID, homeworkID, studentID, courseID uuid.UUID,
	rawScore float64,
) error {
	// Step 1: load timing data so we can compute lateness.
	submittedAt, dueDate, err := s.loadTiming(ctx, submissionID, homeworkID)
	if err != nil {
		return fmt.Errorf("grade: load timing: %w", err)
	}

	// Step 2: late_days = MAX(0, CEIL((submitted - due) / 24h)).
	// Negative values (submitted early) are clamped to zero.
	// A zero dueDate means no schedule entry exists — treat as on-time to avoid
	// computing ~739,000 late days against the year-0001 zero value, which would
	// clamp every score to 0 (SQE-1 fix).
	lateDays := 0
	if !dueDate.IsZero() && submittedAt.After(dueDate) {
		diff := submittedAt.Sub(dueDate)
		lateDays = int(math.Ceil(diff.Hours() / 24))
	}

	// Step 3: read configurable penalty rate; fall back to 5 % per day.
	// REQ-GRADE-002: rate must come from admin config, not be hardcoded.
	penaltyRate := s.configSvc.GetFloat64("late_penalty_rate_per_day")
	if penaltyRate == 0 {
		penaltyRate = 0.05
	}

	// Step 4: compute penalty as a fraction of raw_score, capped at raw_score.
	// REQ-GRADE-001: 5% (or configured rate) deducted per calendar day late.
	latePenaltyAmount := 0.0
	if lateDays > 0 {
		latePenaltyAmount = rawScore * penaltyRate * float64(lateDays)
		if latePenaltyAmount > rawScore {
			latePenaltyAmount = rawScore
		}
	}

	// Step 5: check for an unredeemed late_penalty_waiver badge.
	// REQ-GRADE-005: waiver zeroes the penalty for this submission.
	badgeWaiverApplied := false
	if latePenaltyAmount > 0 {
		waiverBadgeID, found, err := s.findUnredeemedWaiverBadge(ctx, studentID)
		if err != nil {
			// Non-fatal: log and continue without waiver to avoid blocking grading.
			log.Printf("grade: find waiver badge for student %s: %v", studentID, err)
		} else if found {
			// Redeem the badge atomically before zeroing the penalty so that a
			// partial failure leaves the badge unredeemed and the penalty intact.
			if redeemErr := s.badgeSvc.RedeemBadge(ctx, studentID, waiverBadgeID, submissionID); redeemErr != nil {
				if !errors.Is(redeemErr, badge.ErrAlreadyRedeemed) {
					log.Printf("grade: redeem waiver badge %s: %v", waiverBadgeID, redeemErr)
				}
			} else {
				latePenaltyAmount = 0
				badgeWaiverApplied = true
			}
		}
	}

	// Step 6: final_score = CLAMP(raw_score - penalty, 0, 100).
	finalScore := rawScore - latePenaltyAmount
	if finalScore < 0 {
		finalScore = 0
	}
	if finalScore > 100 {
		finalScore = 100
	}

	// Step 7: persist the grade record.
	if _, err := s.repo.Insert(ctx, GradeRow{
		SubmissionID:       submissionID,
		HomeworkID:         homeworkID,
		StudentID:          studentID,
		CourseID:           courseID,
		RawScore:           rawScore,
		LateDays:           lateDays,
		LatePenaltyRate:    penaltyRate,
		LatePenaltyAmount:  latePenaltyAmount,
		BadgeWaiverApplied: badgeWaiverApplied,
		BadgeImprovement:   0, // homework-level badge improvement not applied here; see GetCourseGradeSummary
		FinalScore:         finalScore,
	}); err != nil {
		return fmt.Errorf("grade: insert: %w", err)
	}

	// Step 8: award milestone badges. These are best-effort; grading is not
	// blocked if badge award fails (e.g. if badge seed data is missing).
	s.awardMilestoneBadges(ctx, studentID, submissionID, lateDays, rawScore)

	return nil
}

// GetCourseGradeSummary returns the weighted average homework score for a
// student in a course. It also applies any unredeemed grade_improvement badge
// rewards to the final weighted score, capped at 100.
// REQ-GRADE-003: homework weight distribution from admin config (grade_weight column).
// REQ-GRADE-004: badge improvements added to the overall course final score.
//
// @{"req": ["REQ-GRADE-003", "REQ-GRADE-004"]}
func (s *Service) GetCourseGradeSummary(ctx context.Context, courseID, studentID uuid.UUID) (CourseSummary, error) {
	summary, err := s.repo.GetCourseGradeSummary(ctx, courseID, studentID)
	if err != nil {
		return CourseSummary{}, fmt.Errorf("grade: course summary: %w", err)
	}

	// REQ-GRADE-004: add badge_improvement points from any earned grade_improvement badges.
	// These are NOT redeemed here — they are passive boosts recorded in the summary.
	badges, err := s.badgeSvc.GetStudentBadges(ctx, studentID)
	if err != nil {
		// Non-fatal; return summary without improvement rather than blocking the read.
		log.Printf("grade: get badges for course summary (student %s): %v", studentID, err)
		return summary, nil
	}

	badgeImprovement := 0.0
	for _, b := range badges {
		// Apply grade_improvement badges that have not yet been redeemed against
		// any specific submission (unredeemed = global course-level bonus).
		if b.BadgeReward == "grade_improvement" && b.RedeemedForSubmissionID == nil {
			badgeImprovement += b.BadgeRewardValue
		}
	}

	if badgeImprovement > 0 {
		adjusted := summary.WeightedScore + badgeImprovement
		if adjusted > 100 {
			adjusted = 100
		}
		summary.WeightedScore = adjusted
	}

	return summary, nil
}

// conn returns the server-role connection from ctx when one is present (injected
// by the grading runner via auth.ContextWithConn). Falls back to the bare pool
// for callers that have not injected a connection.
//
// submissions has FORCE ROW LEVEL SECURITY. The grading runner must inject a
// server-role connection so that the SELECT on submitted_at passes RLS.
// due_date_schedules has no RLS, so the fallback pool is safe for that query.
//
// @{"req": ["REQ-GRADE-001", "REQ-GRADE-002"]}
func (s *Service) conn(ctx context.Context) db.Querier {
	if c, ok := auth.ConnFromContext(ctx); ok {
		return c
	}
	return s.pool
}

// loadTiming returns submitted_at for the submission and due_date for the
// homework's due_date_schedules entry. Uses conn(ctx) for the submissions query
// so that the server-role connection injected by the grading runner bypasses
// FORCE RLS on submissions (the student-select policy's UUID cast fails with an
// empty app.current_user_id, returning an error on every call via bare pool).
//
// @{"req": ["REQ-GRADE-001", "REQ-GRADE-002", "REQ-GRADE-005"]}
func (s *Service) loadTiming(ctx context.Context, submissionID, homeworkID uuid.UUID) (submittedAt, dueDate time.Time, err error) {
	err = s.conn(ctx).QueryRow(ctx,
		`SELECT submitted_at FROM submissions WHERE id = $1`,
		submissionID,
	).Scan(&submittedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, time.Time{}, fmt.Errorf("submission %s not found", submissionID)
		}
		return time.Time{}, time.Time{}, err
	}

	err = s.pool.QueryRow(ctx,
		`SELECT due_date FROM due_date_schedules WHERE homework_id = $1`,
		homeworkID,
	).Scan(&dueDate)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No due date means no lateness can be computed; treat as on-time.
			return submittedAt, time.Time{}, nil
		}
		return time.Time{}, time.Time{}, err
	}

	return submittedAt, dueDate, nil
}

// findUnredeemedWaiverBadge looks for a late_penalty_waiver badge held by the
// student that has not yet been redeemed. Returns the badge UUID and true when
// found, or uuid.Nil and false when none is available.
//
// @{"req": ["REQ-GRADE-005"]}
func (s *Service) findUnredeemedWaiverBadge(ctx context.Context, studentID uuid.UUID) (uuid.UUID, bool, error) {
	badges, err := s.badgeSvc.GetStudentBadges(ctx, studentID)
	if err != nil {
		return uuid.Nil, false, err
	}
	for _, b := range badges {
		if b.BadgeReward == "late_penalty_waiver" && b.RedeemedForSubmissionID == nil {
			return b.BadgeID, true, nil
		}
	}
	return uuid.Nil, false, nil
}

// awardMilestoneBadges fires off the relevant CheckAndAwardMilestone calls after
// a submission is graded. Failures are logged but do not affect grade persistence
// because badge awards are supplementary to the core grading contract.
//
// @{"req": ["REQ-GRADE-001", "REQ-GRADE-004"]}
func (s *Service) awardMilestoneBadges(ctx context.Context, studentID, submissionID uuid.UUID, lateDays int, rawScore float64) {
	// "first_submission" is always checked regardless of score or lateness.
	if err := s.badgeSvc.CheckAndAwardMilestone(ctx, studentID, "first_submission"); err != nil {
		log.Printf("grade: award first_submission badge: %v", err)
	}

	// "submitted_on_time" only when the submission was not late.
	if lateDays == 0 {
		if err := s.badgeSvc.CheckAndAwardMilestone(ctx, studentID, "submitted_on_time"); err != nil {
			log.Printf("grade: award submitted_on_time badge: %v", err)
		}
	}

	// "perfect_score" when the raw score is exactly 100.
	if rawScore == 100 {
		if err := s.badgeSvc.CheckAndAwardMilestone(ctx, studentID, "perfect_score"); err != nil {
			log.Printf("grade: award perfect_score badge: %v", err)
		}
	}
}
