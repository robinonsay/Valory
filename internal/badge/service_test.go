package badge

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// stubBadgeRepo is a minimal in-memory implementation of badgeRepository used
// by service unit tests. It allows tests to exercise the real Service methods
// without a live database.
type stubBadgeRepo struct {
	// badgesByMilestone holds available badges keyed by milestone string.
	badgesByMilestone map[string]BadgeRow
	// allBadges holds the full badge list for ListBadges.
	allBadges []BadgeRow
	// awardedBadges maps "studentID:badgeID" to StudentBadgeRow.
	awardedBadges map[string]StudentBadgeRow
	// forcedAwardErr, when non-nil, is returned by AwardBadge.
	forcedAwardErr error
	// forcedMilestoneErr, when non-nil, is returned by GetBadgeByMilestone.
	forcedMilestoneErr error
}

func newStubBadgeRepo() *stubBadgeRepo {
	badges := []BadgeRow{
		{
			ID:          uuid.MustParse("00000000-0000-0000-0000-000000000001"),
			Name:        "First Step",
			Description: "Awarded for submitting your first homework assignment.",
			Milestone:   "first_submission",
			Reward:      "grade_improvement",
			RewardValue: 2.0,
			CreatedAt:   time.Now(),
		},
		{
			ID:          uuid.MustParse("00000000-0000-0000-0000-000000000002"),
			Name:        "Perfectionist",
			Description: "Awarded for receiving a perfect score on a homework assignment.",
			Milestone:   "perfect_score",
			Reward:      "grade_improvement",
			RewardValue: 5.0,
			CreatedAt:   time.Now(),
		},
		{
			ID:          uuid.MustParse("00000000-0000-0000-0000-000000000003"),
			Name:        "Punctual",
			Description: "Awarded for submitting a homework assignment before the due date.",
			Milestone:   "submitted_on_time",
			Reward:      "late_penalty_waiver",
			RewardValue: 0.0,
			CreatedAt:   time.Now(),
		},
		{
			ID:          uuid.MustParse("00000000-0000-0000-0000-000000000004"),
			Name:        "Course Complete",
			Description: "Awarded for submitting feedback on all course sections.",
			Milestone:   "all_sections_complete",
			Reward:      "grade_improvement",
			RewardValue: 3.0,
			CreatedAt:   time.Now(),
		},
	}

	byMilestone := make(map[string]BadgeRow, len(badges))
	for _, b := range badges {
		byMilestone[b.Milestone] = b
	}

	return &stubBadgeRepo{
		badgesByMilestone: byMilestone,
		allBadges:         badges,
		awardedBadges:     map[string]StudentBadgeRow{},
	}
}

func stubKey(studentID, badgeID uuid.UUID) string {
	return studentID.String() + ":" + badgeID.String()
}

// GetBadgeByMilestone satisfies badgeRepository.
func (s *stubBadgeRepo) GetBadgeByMilestone(_ context.Context, milestone string) (BadgeRow, error) {
	if s.forcedMilestoneErr != nil {
		return BadgeRow{}, s.forcedMilestoneErr
	}
	b, ok := s.badgesByMilestone[milestone]
	if !ok {
		return BadgeRow{}, ErrNotFound
	}
	return b, nil
}

// HasBadge satisfies badgeRepository.
func (s *stubBadgeRepo) HasBadge(_ context.Context, studentID, badgeID uuid.UUID) (bool, error) {
	_, ok := s.awardedBadges[stubKey(studentID, badgeID)]
	return ok, nil
}

// AwardBadge satisfies badgeRepository.
func (s *stubBadgeRepo) AwardBadge(_ context.Context, studentID, badgeID uuid.UUID) (StudentBadgeRow, error) {
	if s.forcedAwardErr != nil {
		return StudentBadgeRow{}, s.forcedAwardErr
	}
	key := stubKey(studentID, badgeID)
	if _, exists := s.awardedBadges[key]; exists {
		return StudentBadgeRow{}, ErrAlreadyAwarded
	}
	row := StudentBadgeRow{
		ID:        uuid.New(),
		StudentID: studentID,
		BadgeID:   badgeID,
		AwardedAt: time.Now(),
	}
	s.awardedBadges[key] = row
	return row, nil
}

// ListBadges satisfies badgeRepository.
func (s *stubBadgeRepo) ListBadges(_ context.Context) ([]BadgeRow, error) {
	return s.allBadges, nil
}

// GetStudentBadges satisfies badgeRepository.
func (s *stubBadgeRepo) GetStudentBadges(_ context.Context, studentID uuid.UUID) ([]StudentBadgeRow, error) {
	var result []StudentBadgeRow
	for _, sb := range s.awardedBadges {
		if sb.StudentID == studentID {
			result = append(result, sb)
		}
	}
	return result, nil
}

// RedeemBadge satisfies badgeRepository.
func (s *stubBadgeRepo) RedeemBadge(_ context.Context, studentID, badgeID, submissionID uuid.UUID) error {
	key := stubKey(studentID, badgeID)
	row, ok := s.awardedBadges[key]
	if !ok {
		return ErrNotFound
	}
	row.RedeemedForSubmissionID = &submissionID
	s.awardedBadges[key] = row
	return nil
}

// newServiceWithStub constructs a real Service backed by the given stub.
// Because Service.repo is typed as badgeRepository (an interface), *stubBadgeRepo
// satisfies it automatically without any unsafe casting.
func newServiceWithStub(stub *stubBadgeRepo) *Service {
	return &Service{repo: stub}
}

// @{"verifies": ["REQ-BADGE-001"]}
func TestCheckAndAwardMilestone_AwardsBadge(t *testing.T) {
	ctx := context.Background()
	stub := newStubBadgeRepo()
	svc := newServiceWithStub(stub)
	studentID := uuid.New()

	if err := svc.CheckAndAwardMilestone(ctx, studentID, "first_submission"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	earned, err := svc.GetStudentBadges(ctx, studentID)
	if err != nil {
		t.Fatalf("GetStudentBadges: %v", err)
	}
	if len(earned) != 1 {
		t.Errorf("expected 1 badge awarded, got %d", len(earned))
	}
}

// @{"verifies": ["REQ-BADGE-001"]}
func TestCheckAndAwardMilestone_Idempotent(t *testing.T) {
	ctx := context.Background()
	stub := newStubBadgeRepo()
	svc := newServiceWithStub(stub)
	studentID := uuid.New()

	// First call awards the badge.
	if err := svc.CheckAndAwardMilestone(ctx, studentID, "first_submission"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Second call on the same student+milestone must be a no-op, not an error.
	if err := svc.CheckAndAwardMilestone(ctx, studentID, "first_submission"); err != nil {
		t.Fatalf("second call (idempotent) should not error: %v", err)
	}

	earned, err := svc.GetStudentBadges(ctx, studentID)
	if err != nil {
		t.Fatalf("GetStudentBadges: %v", err)
	}
	if len(earned) != 1 {
		t.Errorf("expected exactly 1 badge after two calls, got %d", len(earned))
	}
}

// @{"verifies": ["REQ-BADGE-001"]}
func TestCheckAndAwardMilestone_UnknownMilestone_DoesNothing(t *testing.T) {
	ctx := context.Background()
	stub := newStubBadgeRepo()
	svc := newServiceWithStub(stub)
	studentID := uuid.New()

	// No badge is configured for "unknown_milestone" — should silently succeed.
	if err := svc.CheckAndAwardMilestone(ctx, studentID, "unknown_milestone"); err != nil {
		t.Fatalf("expected no error for unknown milestone, got %v", err)
	}

	earned, err := svc.GetStudentBadges(ctx, studentID)
	if err != nil {
		t.Fatalf("GetStudentBadges: %v", err)
	}
	if len(earned) != 0 {
		t.Errorf("expected 0 badges for unknown milestone, got %d", len(earned))
	}
}

// @{"verifies": ["REQ-BADGE-001"]}
func TestCheckAndAwardMilestone_MilestoneQueryError_Propagates(t *testing.T) {
	ctx := context.Background()
	stub := newStubBadgeRepo()
	stub.forcedMilestoneErr = errors.New("db connection reset")
	svc := newServiceWithStub(stub)
	studentID := uuid.New()

	err := svc.CheckAndAwardMilestone(ctx, studentID, "first_submission")
	if err == nil {
		t.Fatal("expected error to propagate, got nil")
	}
}

// @{"verifies": ["REQ-BADGE-001"]}
func TestCheckAndAwardMilestone_MultipleStudents_Independent(t *testing.T) {
	ctx := context.Background()
	stub := newStubBadgeRepo()
	svc := newServiceWithStub(stub)

	s1, s2 := uuid.New(), uuid.New()

	if err := svc.CheckAndAwardMilestone(ctx, s1, "perfect_score"); err != nil {
		t.Fatalf("s1 award: %v", err)
	}
	if err := svc.CheckAndAwardMilestone(ctx, s2, "perfect_score"); err != nil {
		t.Fatalf("s2 award: %v", err)
	}

	e1, err := svc.GetStudentBadges(ctx, s1)
	if err != nil {
		t.Fatalf("GetStudentBadges s1: %v", err)
	}
	e2, err := svc.GetStudentBadges(ctx, s2)
	if err != nil {
		t.Fatalf("GetStudentBadges s2: %v", err)
	}

	if len(e1) != 1 {
		t.Errorf("s1: expected 1 badge, got %d", len(e1))
	}
	if len(e2) != 1 {
		t.Errorf("s2: expected 1 badge, got %d", len(e2))
	}
}

// Table-driven tests for CheckAndAwardMilestone covering the key cases.
//
// @{"verifies": ["REQ-BADGE-001"]}
func TestCheckAndAwardMilestone_TableDriven(t *testing.T) {
	cases := []struct {
		name      string
		milestone string
		wantBadge bool
	}{
		{"first_submission is configured", "first_submission", true},
		{"perfect_score is configured", "perfect_score", true},
		{"submitted_on_time is configured", "submitted_on_time", true},
		{"all_sections_complete is configured", "all_sections_complete", true},
		{"unknown milestone does nothing", "never_configured", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			stub := newStubBadgeRepo()
			svc := newServiceWithStub(stub)
			studentID := uuid.New()

			if err := svc.CheckAndAwardMilestone(ctx, studentID, tc.milestone); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			earned, err := svc.GetStudentBadges(ctx, studentID)
			if err != nil {
				t.Fatalf("GetStudentBadges: %v", err)
			}
			if tc.wantBadge && len(earned) != 1 {
				t.Errorf("expected 1 badge, got %d", len(earned))
			}
			if !tc.wantBadge && len(earned) != 0 {
				t.Errorf("expected 0 badges, got %d", len(earned))
			}
		})
	}
}

// submitted_on_time uses the late_penalty_waiver reward type.
//
// @{"verifies": ["REQ-BADGE-003"]}
func TestCheckAndAwardMilestone_SubmittedOnTime_AwardsLateWaiver(t *testing.T) {
	ctx := context.Background()
	stub := newStubBadgeRepo()
	svc := newServiceWithStub(stub)
	studentID := uuid.New()

	if err := svc.CheckAndAwardMilestone(ctx, studentID, "submitted_on_time"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	earned, err := svc.GetStudentBadges(ctx, studentID)
	if err != nil {
		t.Fatalf("GetStudentBadges: %v", err)
	}
	if len(earned) != 1 {
		t.Fatalf("expected 1 badge awarded, got %d", len(earned))
	}

	// Verify the awarded badge is the submitted_on_time badge (late_penalty_waiver).
	expectedID := uuid.MustParse("00000000-0000-0000-0000-000000000003")
	if earned[0].BadgeID != expectedID {
		t.Errorf("expected Punctual badge ID %v, got %v", expectedID, earned[0].BadgeID)
	}
}

// @{"verifies": ["REQ-BADGE-001"]}
func TestCheckAndAwardMilestone_AllSectionsComplete_AwardsBadge(t *testing.T) {
	ctx := context.Background()
	stub := newStubBadgeRepo()
	svc := newServiceWithStub(stub)
	studentID := uuid.New()

	if err := svc.CheckAndAwardMilestone(ctx, studentID, "all_sections_complete"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	earned, err := svc.GetStudentBadges(ctx, studentID)
	if err != nil {
		t.Fatalf("GetStudentBadges: %v", err)
	}
	if len(earned) != 1 {
		t.Fatalf("expected 1 badge awarded, got %d", len(earned))
	}

	expectedID := uuid.MustParse("00000000-0000-0000-0000-000000000004")
	if earned[0].BadgeID != expectedID {
		t.Errorf("expected Course Complete badge ID %v, got %v", expectedID, earned[0].BadgeID)
	}
}

// @{"verifies": ["REQ-BADGE-001"]}
func TestGetStudentBadges_ReturnsCorrectRows(t *testing.T) {
	ctx := context.Background()
	stub := newStubBadgeRepo()
	svc := newServiceWithStub(stub)
	studentID := uuid.New()
	otherID := uuid.New()

	// Award two badges to studentID and one to otherID.
	if err := svc.CheckAndAwardMilestone(ctx, studentID, "first_submission"); err != nil {
		t.Fatalf("award first_submission: %v", err)
	}
	if err := svc.CheckAndAwardMilestone(ctx, studentID, "perfect_score"); err != nil {
		t.Fatalf("award perfect_score: %v", err)
	}
	if err := svc.CheckAndAwardMilestone(ctx, otherID, "first_submission"); err != nil {
		t.Fatalf("award other student: %v", err)
	}

	earned, err := svc.GetStudentBadges(ctx, studentID)
	if err != nil {
		t.Fatalf("GetStudentBadges: %v", err)
	}
	if len(earned) != 2 {
		t.Errorf("expected 2 badges for studentID, got %d", len(earned))
	}

	// Other student's badges must not bleed into studentID's results.
	for _, sb := range earned {
		if sb.StudentID != studentID {
			t.Errorf("unexpected student ID %v in results", sb.StudentID)
		}
	}
}

// @{"verifies": ["REQ-BADGE-001"]}
func TestListBadges_ReturnsAllDefinitions(t *testing.T) {
	ctx := context.Background()
	stub := newStubBadgeRepo()
	svc := newServiceWithStub(stub)

	badges, err := svc.ListBadges(ctx)
	if err != nil {
		t.Fatalf("ListBadges: %v", err)
	}
	if len(badges) != 4 {
		t.Errorf("expected 4 badge definitions, got %d", len(badges))
	}

	milestones := make(map[string]bool, len(badges))
	for _, b := range badges {
		milestones[b.Milestone] = true
	}
	for _, want := range []string{"first_submission", "submitted_on_time", "perfect_score", "all_sections_complete"} {
		if !milestones[want] {
			t.Errorf("expected milestone %q in badge list", want)
		}
	}
}

// @{"verifies": ["REQ-BADGE-002", "REQ-BADGE-003"]}
func TestRedeemBadge_HappyPath(t *testing.T) {
	ctx := context.Background()
	stub := newStubBadgeRepo()
	svc := newServiceWithStub(stub)
	studentID := uuid.New()
	submissionID := uuid.New()

	// Award the badge first.
	if err := svc.CheckAndAwardMilestone(ctx, studentID, "first_submission"); err != nil {
		t.Fatalf("award badge: %v", err)
	}

	badgeID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	if err := svc.RedeemBadge(ctx, studentID, badgeID, submissionID); err != nil {
		t.Fatalf("RedeemBadge: %v", err)
	}

	// Verify the badge is now marked as redeemed in the stub.
	earned, err := svc.GetStudentBadges(ctx, studentID)
	if err != nil {
		t.Fatalf("GetStudentBadges: %v", err)
	}
	if len(earned) != 1 {
		t.Fatalf("expected 1 badge, got %d", len(earned))
	}
	if earned[0].RedeemedForSubmissionID == nil {
		t.Fatal("expected RedeemedForSubmissionID to be set after redeem")
	}
	if *earned[0].RedeemedForSubmissionID != submissionID {
		t.Errorf("expected submission ID %v, got %v", submissionID, *earned[0].RedeemedForSubmissionID)
	}
}

// @{"verifies": ["REQ-BADGE-002", "REQ-BADGE-003"]}
func TestRedeemBadge_NotOwned_ReturnsErrNotFound(t *testing.T) {
	ctx := context.Background()
	stub := newStubBadgeRepo()
	svc := newServiceWithStub(stub)
	studentID := uuid.New()
	badgeID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	submissionID := uuid.New()

	// Student does not hold the badge — RedeemBadge must return ErrNotFound.
	err := svc.RedeemBadge(ctx, studentID, badgeID, submissionID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
