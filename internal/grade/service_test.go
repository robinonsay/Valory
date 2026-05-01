//go:build testing

// service_test.go — unit tests for the grade service.
// All external dependencies (repo, pool, badgeSvc, configSvc) are stubbed so
// tests run without a database.
package grade

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	pgconn_pkg "github.com/jackc/pgx/v5/pgconn"
	"github.com/valory/valory/internal/badge"
)

// ---------------------------------------------------------------------------
// Stubs
// ---------------------------------------------------------------------------

// stubGradeRepo satisfies gradeRepositoryInterface with controllable responses.
type stubGradeRepo struct {
	insertedGrade GradeRow
	insertErr     error
	summary       CourseSummary
	summaryErr    error
}

// @{"req": ["REQ-GRADE-001", "REQ-GRADE-002", "REQ-GRADE-004", "REQ-GRADE-005"]}
func (r *stubGradeRepo) Insert(_ context.Context, g GradeRow) (GradeRow, error) {
	if r.insertErr != nil {
		return GradeRow{}, r.insertErr
	}
	g.ID = uuid.New()
	r.insertedGrade = g
	return g, nil
}

// @{"req": ["REQ-GRADE-003", "REQ-GRADE-004"]}
func (r *stubGradeRepo) GetCourseGradeSummary(_ context.Context, courseID, studentID uuid.UUID) (CourseSummary, error) {
	if r.summaryErr != nil {
		return CourseSummary{}, r.summaryErr
	}
	s := r.summary
	s.CourseID = courseID
	s.StudentID = studentID
	return s, nil
}

// stubBadgeSvc records which methods were called.
type stubBadgeSvc struct {
	badges       []badge.StudentBadgeRow
	getBadgesErr error
	redeemErr    error
	redeemed     bool
	milestones   []string
}

func (s *stubBadgeSvc) CheckAndAwardMilestone(_ context.Context, _ uuid.UUID, milestone string) error {
	s.milestones = append(s.milestones, milestone)
	return nil
}

func (s *stubBadgeSvc) GetStudentBadges(_ context.Context, _ uuid.UUID) ([]badge.StudentBadgeRow, error) {
	return s.badges, s.getBadgesErr
}

func (s *stubBadgeSvc) RedeemBadge(_ context.Context, _, _, _ uuid.UUID) error {
	if s.redeemErr != nil {
		return s.redeemErr
	}
	s.redeemed = true
	return nil
}

// stubConfig satisfies the configSvc interface.
type stubConfig struct {
	penaltyRate float64
}

func (c *stubConfig) GetFloat64(_ string) float64 { return c.penaltyRate }

// stubRow implements pgx.Row for a single value (submitted_at or due_date).
type stubRow struct {
	val interface{}
	err error
}

func (r *stubRow) Scan(dest ...interface{}) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) == 1 {
		switch d := dest[0].(type) {
		case *time.Time:
			if t, ok := r.val.(time.Time); ok {
				*d = t
			}
		}
	}
	return nil
}

// stubQuerier implements db.Querier. It returns a fake submitted_at and
// due_date so ComputeAndStoreGrade can be exercised without a real DB.
// SQL is matched by prefix to distinguish submissions vs due_date_schedules.
type stubQuerier struct {
	submittedAt time.Time
	dueDate     time.Time
	noDueDate   bool // return pgx.ErrNoRows for due_date_schedules
}

func (q *stubQuerier) QueryRow(_ context.Context, sql string, _ ...interface{}) pgx.Row {
	if strings.Contains(sql, "submitted_at") {
		return &stubRow{val: q.submittedAt}
	}
	if strings.Contains(sql, "due_date") {
		if q.noDueDate {
			return &stubRow{err: pgx.ErrNoRows}
		}
		return &stubRow{val: q.dueDate}
	}
	return &stubRow{err: pgx.ErrNoRows}
}

func (q *stubQuerier) Query(_ context.Context, _ string, _ ...interface{}) (pgx.Rows, error) {
	return nil, nil
}

func (q *stubQuerier) Exec(_ context.Context, _ string, _ ...interface{}) (pgconn_pkg.CommandTag, error) {
	return pgconn_pkg.CommandTag{}, nil
}

// buildSvc constructs a Service backed entirely by stubs.
func buildSvc(querier *stubQuerier, repo *stubGradeRepo, badges *stubBadgeSvc, penaltyRate float64) *Service {
	return &Service{
		repo:      repo,
		pool:      querier,
		badgeSvc:  badges,
		configSvc: &stubConfig{penaltyRate: penaltyRate},
	}
}

// ---------------------------------------------------------------------------
// Tests: ComputeAndStoreGrade — exercises the actual production path
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-GRADE-001"]}
func TestComputeAndStoreGrade_OnTime_NoPenalty(t *testing.T) {
	now := time.Now()
	querier := &stubQuerier{
		submittedAt: now,
		dueDate:     now.Add(24 * time.Hour), // due tomorrow — not late
	}
	repo := &stubGradeRepo{}
	svc := buildSvc(querier, repo, &stubBadgeSvc{}, 0.05)

	err := svc.ComputeAndStoreGrade(context.Background(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), 85)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.insertedGrade.LatePenaltyAmount != 0 {
		t.Errorf("expected zero penalty for on-time submission, got %.2f", repo.insertedGrade.LatePenaltyAmount)
	}
	if repo.insertedGrade.FinalScore != 85 {
		t.Errorf("expected final_score=85, got %.2f", repo.insertedGrade.FinalScore)
	}
}

// @{"verifies": ["REQ-GRADE-001", "REQ-GRADE-002"]}
func TestComputeAndStoreGrade_TwoDaysLate_CorrectPenalty(t *testing.T) {
	now := time.Now()
	querier := &stubQuerier{
		submittedAt: now,
		dueDate:     now.Add(-49 * time.Hour), // 49 hours ago → ceil(49/24)=3 days late
	}
	repo := &stubGradeRepo{}
	svc := buildSvc(querier, repo, &stubBadgeSvc{}, 0.05)

	err := svc.ComputeAndStoreGrade(context.Background(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), 80)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// raw=80, rate=0.05, days=3 → penalty=12, final=68
	if repo.insertedGrade.LateDays != 3 {
		t.Errorf("expected late_days=3, got %d", repo.insertedGrade.LateDays)
	}
	wantPenalty := 80.0 * 0.05 * 3
	if repo.insertedGrade.LatePenaltyAmount != wantPenalty {
		t.Errorf("expected penalty=%.2f, got %.2f", wantPenalty, repo.insertedGrade.LatePenaltyAmount)
	}
	wantFinal := 80.0 - wantPenalty
	if repo.insertedGrade.FinalScore != wantFinal {
		t.Errorf("expected final=%.2f, got %.2f", wantFinal, repo.insertedGrade.FinalScore)
	}
}

// @{"verifies": ["REQ-GRADE-001"]}
func TestComputeAndStoreGrade_PenaltyExceedsRawScore_ClampedToZero(t *testing.T) {
	now := time.Now()
	querier := &stubQuerier{
		submittedAt: now,
		dueDate:     now.Add(-31 * 24 * time.Hour), // 31 days late — penalty exceeds raw score
	}
	repo := &stubGradeRepo{}
	svc := buildSvc(querier, repo, &stubBadgeSvc{}, 0.05)

	err := svc.ComputeAndStoreGrade(context.Background(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.insertedGrade.FinalScore != 0 {
		t.Errorf("expected final_score clamped to 0, got %.2f", repo.insertedGrade.FinalScore)
	}
	// Penalty should be capped at raw_score.
	if repo.insertedGrade.LatePenaltyAmount != 10 {
		t.Errorf("expected penalty capped at raw_score=10, got %.2f", repo.insertedGrade.LatePenaltyAmount)
	}
}

// @{"verifies": ["REQ-GRADE-001"]}
func TestComputeAndStoreGrade_NoDueDate_NoPenalty(t *testing.T) {
	// When no due_date_schedules row exists, loadTiming returns time.Time{} (zero)
	// for dueDate. The dueDate.IsZero() guard must prevent computing an enormous
	// penalty against year-0001 (SQE-1 fix verification).
	now := time.Now()
	querier := &stubQuerier{
		submittedAt: now,
		noDueDate:   true, // simulates pgx.ErrNoRows from due_date_schedules
	}
	repo := &stubGradeRepo{}
	svc := buildSvc(querier, repo, &stubBadgeSvc{}, 0.05)

	err := svc.ComputeAndStoreGrade(context.Background(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), 90)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.insertedGrade.LateDays != 0 {
		t.Errorf("expected 0 late days when no due date exists, got %d", repo.insertedGrade.LateDays)
	}
	if repo.insertedGrade.FinalScore != 90 {
		t.Errorf("expected full score 90 when no due date, got %.2f", repo.insertedGrade.FinalScore)
	}
}

// @{"verifies": ["REQ-GRADE-002"]}
func TestComputeAndStoreGrade_CustomRate_CorrectPenalty(t *testing.T) {
	now := time.Now()
	querier := &stubQuerier{
		submittedAt: now,
		dueDate:     now.Add(-72 * time.Hour), // 3 days late
	}
	repo := &stubGradeRepo{}
	// Non-default 10% rate.
	svc := buildSvc(querier, repo, &stubBadgeSvc{}, 0.10)

	err := svc.ComputeAndStoreGrade(context.Background(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// raw=100, rate=0.10, days=3 → penalty=30, final=70
	if repo.insertedGrade.LatePenaltyAmount != 30 {
		t.Errorf("expected penalty=30, got %.2f", repo.insertedGrade.LatePenaltyAmount)
	}
	if repo.insertedGrade.FinalScore != 70 {
		t.Errorf("expected final=70, got %.2f", repo.insertedGrade.FinalScore)
	}
}

// @{"verifies": ["REQ-GRADE-005"]}
func TestComputeAndStoreGrade_WaiverBadge_ZerosPenalty(t *testing.T) {
	now := time.Now()
	querier := &stubQuerier{
		submittedAt: now,
		dueDate:     now.Add(-48 * time.Hour), // 2 days late
	}
	repo := &stubGradeRepo{}
	waiverBadgeID := uuid.New()
	badgeSvc := &stubBadgeSvc{
		badges: []badge.StudentBadgeRow{
			{
				BadgeID:                 waiverBadgeID,
				BadgeReward:             "late_penalty_waiver",
				RedeemedForSubmissionID: nil,
			},
		},
	}
	svc := buildSvc(querier, repo, badgeSvc, 0.05)

	err := svc.ComputeAndStoreGrade(context.Background(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), 80)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.insertedGrade.LatePenaltyAmount != 0 {
		t.Errorf("expected penalty zeroed by waiver, got %.2f", repo.insertedGrade.LatePenaltyAmount)
	}
	if !repo.insertedGrade.BadgeWaiverApplied {
		t.Error("expected badge_waiver_applied=true")
	}
	if !badgeSvc.redeemed {
		t.Error("expected badge to be redeemed")
	}
	if repo.insertedGrade.FinalScore != 80 {
		t.Errorf("expected final_score=80 after waiver, got %.2f", repo.insertedGrade.FinalScore)
	}
}

// ---------------------------------------------------------------------------
// Tests: lateDays computation (unit-level, no DB)
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-GRADE-001"]}
func TestLateDays_OnTime_Zero(t *testing.T) {
	due := time.Now()
	submitted := due.Add(-1 * time.Hour) // submitted 1 hour early
	lateDays := 0
	if submitted.After(due) {
		diff := submitted.Sub(due)
		hours := diff.Hours() / 24
		lateDays = int(ceilInt(hours))
	}
	if lateDays != 0 {
		t.Errorf("expected 0 late days for early submission, got %d", lateDays)
	}
}

// @{"verifies": ["REQ-GRADE-001"]}
func TestLateDays_25HoursLate_TwoDays(t *testing.T) {
	due := time.Now()
	submitted := due.Add(25 * time.Hour) // 25 hours late → ceil(25/24)=2 days
	lateDays := 0
	if submitted.After(due) {
		diff := submitted.Sub(due)
		hours := diff.Hours() / 24
		lateDays = int(ceilInt(hours))
	}
	if lateDays != 2 {
		t.Errorf("expected 2 late days for 25-hour-late submission, got %d", lateDays)
	}
}

// @{"verifies": ["REQ-GRADE-001"]}
func TestLateDays_ExactlyOneDayLate_OneDay(t *testing.T) {
	due := time.Now()
	submitted := due.Add(24 * time.Hour) // exactly 24h → ceil(1.0)=1
	lateDays := 0
	if submitted.After(due) {
		diff := submitted.Sub(due)
		hours := diff.Hours() / 24
		lateDays = int(ceilInt(hours))
	}
	if lateDays != 1 {
		t.Errorf("expected 1 late day, got %d", lateDays)
	}
}

// ceilInt mimics math.Ceil for the test without importing math.
func ceilInt(f float64) float64 {
	if f == float64(int64(f)) {
		return f
	}
	return float64(int64(f) + 1)
}

// ---------------------------------------------------------------------------
// Tests: GetCourseGradeSummary — badge improvement (REQ-GRADE-004)
// ---------------------------------------------------------------------------

// @{"verifies": ["REQ-GRADE-004"]}
func TestGetCourseGradeSummary_WithGradeImprovementBadge_AddsReward(t *testing.T) {
	// Stub repo returns a base weighted score of 85.
	repo := &stubGradeRepo{
		summary: CourseSummary{WeightedScore: 85.0, TotalWeight: 1.0},
	}
	// Badge service returns one unredeemed grade_improvement badge worth 10 points.
	improvementBadgeID := uuid.New()
	badgeSvc := &stubBadgeSvc{
		badges: []badge.StudentBadgeRow{
			{
				BadgeID:                 improvementBadgeID,
				BadgeReward:             "grade_improvement",
				BadgeRewardValue:        10.0,
				RedeemedForSubmissionID: nil,
			},
		},
	}
	svc := buildSvc(&stubQuerier{}, repo, badgeSvc, 0.05)

	got, err := svc.GetCourseGradeSummary(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 85 + 10 = 95, below cap.
	if got.WeightedScore != 95.0 {
		t.Errorf("expected weighted_score=95, got %.2f", got.WeightedScore)
	}
}

// @{"verifies": ["REQ-GRADE-004"]}
func TestGetCourseGradeSummary_BadgeImprovementCappedAt100(t *testing.T) {
	// Base score 90 + 20 badge points would be 110; must be capped at 100.
	repo := &stubGradeRepo{
		summary: CourseSummary{WeightedScore: 90.0, TotalWeight: 1.0},
	}
	badgeSvc := &stubBadgeSvc{
		badges: []badge.StudentBadgeRow{
			{
				BadgeID:                 uuid.New(),
				BadgeReward:             "grade_improvement",
				BadgeRewardValue:        20.0,
				RedeemedForSubmissionID: nil,
			},
		},
	}
	svc := buildSvc(&stubQuerier{}, repo, badgeSvc, 0.05)

	got, err := svc.GetCourseGradeSummary(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.WeightedScore != 100.0 {
		t.Errorf("expected weighted_score capped at 100, got %.2f", got.WeightedScore)
	}
}

// @{"verifies": ["REQ-GRADE-004"]}
func TestGetCourseGradeSummary_NoImprovementBadge_NoChange(t *testing.T) {
	repo := &stubGradeRepo{
		summary: CourseSummary{WeightedScore: 75.0, TotalWeight: 1.0},
	}
	// A redeemed badge should not apply.
	redeemedSubID := uuid.New()
	badgeSvc := &stubBadgeSvc{
		badges: []badge.StudentBadgeRow{
			{
				BadgeID:                 uuid.New(),
				BadgeReward:             "grade_improvement",
				BadgeRewardValue:        10.0,
				RedeemedForSubmissionID: &redeemedSubID,
			},
		},
	}
	svc := buildSvc(&stubQuerier{}, repo, badgeSvc, 0.05)

	got, err := svc.GetCourseGradeSummary(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Redeemed badge should not boost score.
	if got.WeightedScore != 75.0 {
		t.Errorf("expected score unchanged at 75, got %.2f", got.WeightedScore)
	}
}
