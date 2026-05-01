package badge

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pool is the shared connection pool for integration tests.
// It is nil when TEST_DATABASE_URL is unset, causing each test to skip.
var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		// No DB available; run tests anyway — DB-dependent ones self-skip via pool == nil guard.
		os.Exit(m.Run())
	}

	var err error
	pool, err = pgxpool.New(context.Background(), dbURL)
	if err != nil {
		panic(err)
	}
	defer pool.Close()

	ctx := context.Background()
	if err := applyMigrations(ctx, pool); err != nil {
		panic(err)
	}
	if err := truncateTables(ctx, pool); err != nil {
		panic(err)
	}

	code := m.Run()

	if err := truncateTables(ctx, pool); err != nil {
		panic(err)
	}

	os.Exit(code)
}

// execSimple runs a multi-statement SQL string using PostgreSQL's simple query
// protocol so that all statements execute atomically rather than only the first.
// pgxpool.Pool.Exec uses the extended protocol which only executes one statement.
func execSimple(ctx context.Context, p *pgxpool.Pool, sql string) error {
	conn, err := p.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	return conn.Conn().PgConn().Exec(ctx, sql).Close()
}

// applyMigrations creates the minimal schema required by badge integration tests.
// It mirrors the relevant parts of migrations 001_auth and 006_badge.
func applyMigrations(ctx context.Context, p *pgxpool.Pool) error {
	// Bootstrap: pgcrypto extension + schema_migrations tracking table + users table
	// (users is the FK parent for student_badges.student_id)
	bootstrapSQL := `
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS schema_migrations (
    version     TEXT        PRIMARY KEY,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO schema_migrations (version) VALUES ('001_auth')
    ON CONFLICT (version) DO NOTHING;

CREATE TABLE IF NOT EXISTS users (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    username            TEXT        NOT NULL UNIQUE,
    password_hash       TEXT        NOT NULL,
    role                TEXT        NOT NULL CHECK (role IN ('student', 'admin')),
    is_active           BOOLEAN     NOT NULL DEFAULT TRUE,
    failed_login_count  INTEGER     NOT NULL DEFAULT 0,
    locked_until        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`
	if err := execSimple(ctx, p, bootstrapSQL); err != nil {
		return err
	}

	// Badge schema: mirrors migrations/006_badge.sql
	badgeSQL := `
DO $$ BEGIN
    CREATE TYPE milestone_type AS ENUM (
        'first_submission',
        'submitted_on_time',
        'perfect_score',
        'all_sections_complete'
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE TYPE reward_type AS ENUM (
        'grade_improvement',
        'late_penalty_waiver'
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE TABLE IF NOT EXISTS badges (
    id            UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
    name          VARCHAR(100)   NOT NULL UNIQUE,
    description   TEXT           NOT NULL,
    milestone     milestone_type NOT NULL,
    reward        reward_type    NOT NULL,
    reward_value  NUMERIC(5,2)   NOT NULL DEFAULT 0 CHECK (reward_value >= 0),
    created_at    TIMESTAMPTZ    NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS student_badges (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    student_id  UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    badge_id    UUID        NOT NULL REFERENCES badges(id) ON DELETE CASCADE,
    awarded_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    redeemed_for_submission_id UUID,
    CONSTRAINT uq_student_badge UNIQUE (student_id, badge_id)
);

CREATE INDEX IF NOT EXISTS student_badges_student_id_idx ON student_badges (student_id);
CREATE INDEX IF NOT EXISTS student_badges_badge_id_idx   ON student_badges (badge_id);

-- Mirror the RLS setup from 006_badge.sql so integration tests exercise the
-- same security configuration as production (pool bypasses RLS via superuser).
ALTER TABLE student_badges ENABLE ROW LEVEL SECURITY;
ALTER TABLE student_badges FORCE ROW LEVEL SECURITY;

DO $$ BEGIN
    CREATE POLICY student_badges_student_policy ON student_badges
        USING (student_id = (current_setting('app.current_user_id', true))::uuid);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY student_badges_server_policy ON student_badges
        FOR ALL
        USING (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY student_badges_admin_policy ON student_badges
        FOR SELECT
        USING (current_setting('app.current_role', true) = 'admin');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

INSERT INTO badges (name, description, milestone, reward, reward_value) VALUES
    ('First Step',      'Awarded for submitting your first homework assignment.',           'first_submission',      'grade_improvement',  2.0),
    ('Punctual',        'Awarded for submitting a homework assignment before the due date.','submitted_on_time',     'late_penalty_waiver', 0.0),
    ('Perfectionist',   'Awarded for receiving a perfect score on a homework assignment.',  'perfect_score',         'grade_improvement',  5.0),
    ('Course Complete', 'Awarded for submitting feedback on all course sections.',          'all_sections_complete', 'grade_improvement',  3.0)
ON CONFLICT (name) DO NOTHING;
`
	return execSimple(ctx, p, badgeSQL)
}

// truncateTables clears test data between runs to keep tests independent.
// Badges are re-seeded after truncation so milestone lookups always succeed.
func truncateTables(ctx context.Context, p *pgxpool.Pool) error {
	// student_badges references users and badges; truncate it first.
	truncateSQL := `
TRUNCATE TABLE student_badges CASCADE;
TRUNCATE TABLE badges CASCADE;
TRUNCATE TABLE users CASCADE;

INSERT INTO badges (name, description, milestone, reward, reward_value) VALUES
    ('First Step',      'Awarded for submitting your first homework assignment.',           'first_submission',      'grade_improvement',  2.0),
    ('Punctual',        'Awarded for submitting a homework assignment before the due date.','submitted_on_time',     'late_penalty_waiver', 0.0),
    ('Perfectionist',   'Awarded for receiving a perfect score on a homework assignment.',  'perfect_score',         'grade_improvement',  5.0),
    ('Course Complete', 'Awarded for submitting feedback on all course sections.',          'all_sections_complete', 'grade_improvement',  3.0)
ON CONFLICT (name) DO NOTHING;
`
	return execSimple(ctx, p, truncateSQL)
}

// createTestStudent inserts a student user and returns its ID.
func createTestStudent(ctx context.Context, t *testing.T, username string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, 'student') RETURNING id`,
		username, "testhash").Scan(&id)
	if err != nil {
		t.Fatalf("createTestStudent: %v", err)
	}
	return id
}

// @{"verifies": ["REQ-BADGE-001", "REQ-BADGE-002", "REQ-BADGE-003"]}
func TestListBadges_ReturnsSeeded(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	badges, err := repo.ListBadges(ctx)
	if err != nil {
		t.Fatalf("ListBadges failed: %v", err)
	}
	if len(badges) < 4 {
		t.Errorf("expected at least 4 seeded badges, got %d", len(badges))
	}

	names := make(map[string]bool, len(badges))
	for _, b := range badges {
		names[b.Name] = true
	}
	for _, want := range []string{"First Step", "Punctual", "Perfectionist", "Course Complete"} {
		if !names[want] {
			t.Errorf("expected badge %q to be present", want)
		}
	}
}

// @{"verifies": ["REQ-BADGE-001"]}
func TestAwardBadge_InsertsRecord(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	studentID := createTestStudent(ctx, t, "award_insert_"+uuid.New().String())

	b, err := repo.GetBadgeByMilestone(ctx, "first_submission")
	if err != nil {
		t.Fatalf("GetBadgeByMilestone: %v", err)
	}

	sb, err := repo.AwardBadge(ctx, studentID, b.ID)
	if err != nil {
		t.Fatalf("AwardBadge: %v", err)
	}
	if sb.StudentID != studentID {
		t.Errorf("expected StudentID %v, got %v", studentID, sb.StudentID)
	}
	if sb.BadgeID != b.ID {
		t.Errorf("expected BadgeID %v, got %v", b.ID, sb.BadgeID)
	}
	if sb.RedeemedForSubmissionID != nil {
		t.Error("expected RedeemedForSubmissionID to be nil on fresh award")
	}
	if sb.BadgeName == "" {
		t.Error("expected BadgeName to be populated after award")
	}
}

// @{"verifies": ["REQ-BADGE-001"]}
func TestAwardBadge_DuplicateReturnsErrAlreadyAwarded(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	studentID := createTestStudent(ctx, t, "award_dup_"+uuid.New().String())

	b, err := repo.GetBadgeByMilestone(ctx, "perfect_score")
	if err != nil {
		t.Fatalf("GetBadgeByMilestone: %v", err)
	}

	if _, err := repo.AwardBadge(ctx, studentID, b.ID); err != nil {
		t.Fatalf("first AwardBadge: %v", err)
	}

	_, err = repo.AwardBadge(ctx, studentID, b.ID)
	if !errors.Is(err, ErrAlreadyAwarded) {
		t.Fatalf("expected ErrAlreadyAwarded, got %v", err)
	}
}

// @{"verifies": ["REQ-BADGE-001"]}
func TestGetStudentBadges_ReturnsAwardedBadge(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	studentID := createTestStudent(ctx, t, "get_student_badges_"+uuid.New().String())

	b, err := repo.GetBadgeByMilestone(ctx, "submitted_on_time")
	if err != nil {
		t.Fatalf("GetBadgeByMilestone: %v", err)
	}
	if _, err := repo.AwardBadge(ctx, studentID, b.ID); err != nil {
		t.Fatalf("AwardBadge: %v", err)
	}

	earned, err := repo.GetStudentBadges(ctx, studentID)
	if err != nil {
		t.Fatalf("GetStudentBadges: %v", err)
	}
	if len(earned) != 1 {
		t.Fatalf("expected 1 badge, got %d", len(earned))
	}
	if earned[0].BadgeID != b.ID {
		t.Errorf("expected BadgeID %v, got %v", b.ID, earned[0].BadgeID)
	}
}

// @{"verifies": ["REQ-BADGE-001"]}
func TestHasBadge_ReturnsTrueAfterAward(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	studentID := createTestStudent(ctx, t, "has_badge_"+uuid.New().String())

	b, err := repo.GetBadgeByMilestone(ctx, "all_sections_complete")
	if err != nil {
		t.Fatalf("GetBadgeByMilestone: %v", err)
	}

	has, err := repo.HasBadge(ctx, studentID, b.ID)
	if err != nil {
		t.Fatalf("HasBadge before award: %v", err)
	}
	if has {
		t.Error("expected HasBadge to return false before award")
	}

	if _, err := repo.AwardBadge(ctx, studentID, b.ID); err != nil {
		t.Fatalf("AwardBadge: %v", err)
	}

	has, err = repo.HasBadge(ctx, studentID, b.ID)
	if err != nil {
		t.Fatalf("HasBadge after award: %v", err)
	}
	if !has {
		t.Error("expected HasBadge to return true after award")
	}
}

// @{"verifies": ["REQ-BADGE-002", "REQ-BADGE-003"]}
func TestRedeemBadge_SetsRedeemedForSubmissionID(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	studentID := createTestStudent(ctx, t, "redeem_badge_"+uuid.New().String())

	b, err := repo.GetBadgeByMilestone(ctx, "first_submission")
	if err != nil {
		t.Fatalf("GetBadgeByMilestone: %v", err)
	}
	if _, err := repo.AwardBadge(ctx, studentID, b.ID); err != nil {
		t.Fatalf("AwardBadge: %v", err)
	}

	// The submissions table does not exist in this test schema (created in
	// 007_submission.sql). student_badges has no FK to submissions in migration
	// 006, so we can store any UUID as a redeemed_for_submission_id here.
	fakeSubmissionID := uuid.New()

	if err := repo.RedeemBadge(ctx, studentID, b.ID, fakeSubmissionID); err != nil {
		t.Fatalf("RedeemBadge: %v", err)
	}

	earned, err := repo.GetStudentBadges(ctx, studentID)
	if err != nil {
		t.Fatalf("GetStudentBadges after redeem: %v", err)
	}
	if len(earned) != 1 {
		t.Fatalf("expected 1 badge, got %d", len(earned))
	}
	if earned[0].RedeemedForSubmissionID == nil {
		t.Fatal("expected RedeemedForSubmissionID to be set after redeem")
	}
	if *earned[0].RedeemedForSubmissionID != fakeSubmissionID {
		t.Errorf("expected submission ID %v, got %v", fakeSubmissionID, *earned[0].RedeemedForSubmissionID)
	}
}

// @{"verifies": ["REQ-BADGE-002", "REQ-BADGE-003"]}
func TestRedeemBadge_NotHeld_ReturnsErrNotFound(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	studentID := createTestStudent(ctx, t, "redeem_notheld_"+uuid.New().String())

	b, err := repo.GetBadgeByMilestone(ctx, "perfect_score")
	if err != nil {
		t.Fatalf("GetBadgeByMilestone: %v", err)
	}

	err = repo.RedeemBadge(ctx, studentID, b.ID, uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// TestRedeemBadge_DoubleRedemption_ReturnsErrAlreadyRedeemed verifies that
// attempting to redeem a badge that has already been redeemed returns
// ErrAlreadyRedeemed rather than ErrNotFound, so callers can surface the
// correct error to the student instead of silently allowing overwrite.
//
// @{"verifies": ["REQ-BADGE-002", "REQ-BADGE-003"]}
func TestRedeemBadge_DoubleRedemption_ReturnsErrAlreadyRedeemed(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	studentID := createTestStudent(ctx, t, "redeem_double_"+uuid.New().String())

	b, err := repo.GetBadgeByMilestone(ctx, "submitted_on_time")
	if err != nil {
		t.Fatalf("GetBadgeByMilestone: %v", err)
	}
	if _, err := repo.AwardBadge(ctx, studentID, b.ID); err != nil {
		t.Fatalf("AwardBadge: %v", err)
	}

	firstSubmissionID := uuid.New()
	if err := repo.RedeemBadge(ctx, studentID, b.ID, firstSubmissionID); err != nil {
		t.Fatalf("first RedeemBadge: %v", err)
	}

	// Second redemption against a different submission must be rejected.
	err = repo.RedeemBadge(ctx, studentID, b.ID, uuid.New())
	if !errors.Is(err, ErrAlreadyRedeemed) {
		t.Fatalf("expected ErrAlreadyRedeemed on second redemption, got %v", err)
	}
}
