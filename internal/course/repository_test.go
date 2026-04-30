package course

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		os.Exit(0)
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

func applyMigrations(ctx context.Context, p *pgxpool.Pool) error {
	migration001 := `
	BEGIN;

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

	CREATE INDEX IF NOT EXISTS idx_users_locked_until ON users (locked_until)
	    WHERE locked_until IS NOT NULL;

	CREATE TABLE IF NOT EXISTS sessions (
	    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
	    user_id         UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	    token_hash      TEXT        NOT NULL UNIQUE,
	    role            TEXT        NOT NULL CHECK (role IN ('student', 'admin')),
	    last_active_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	    expires_at      TIMESTAMPTZ NOT NULL,
	    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions (user_id);
	CREATE INDEX IF NOT EXISTS idx_sessions_last_active_at ON sessions (last_active_at);

	CREATE TABLE IF NOT EXISTS login_attempts (
	    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
	    user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	    attempted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	    success      BOOLEAN     NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_login_attempts_user_id ON login_attempts (user_id, attempted_at DESC);

	COMMIT;
	`

	if _, err := p.Exec(ctx, migration001); err != nil {
		return err
	}

	migration002 := `
	BEGIN;

	INSERT INTO schema_migrations (version) VALUES ('002_user_security_audit')
	    ON CONFLICT (version) DO NOTHING;

	ALTER TABLE users ADD COLUMN IF NOT EXISTS email TEXT UNIQUE;

	CREATE TABLE IF NOT EXISTS password_reset_tokens (
	    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
	    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	    token_hash  TEXT        NOT NULL UNIQUE,
	    expires_at  TIMESTAMPTZ NOT NULL,
	    used_at     TIMESTAMPTZ
	);

	CREATE INDEX IF NOT EXISTS idx_prt_user_id    ON password_reset_tokens (user_id);
	CREATE INDEX IF NOT EXISTS idx_prt_token_hash ON password_reset_tokens (token_hash);
	CREATE INDEX IF NOT EXISTS idx_prt_expires_at ON password_reset_tokens (expires_at)
	    WHERE used_at IS NULL;

	CREATE TABLE IF NOT EXISTS password_reset_attempts (
	    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
	    user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	    requested_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_pra_user_requested
	    ON password_reset_attempts (user_id, requested_at DESC);

	CREATE TABLE IF NOT EXISTS student_consent (
	    student_id       UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
	    consented_at     TIMESTAMPTZ NOT NULL,
	    consent_version  VARCHAR(16) NOT NULL
	);

	CREATE TABLE IF NOT EXISTS audit_log (
	    id           BIGSERIAL    PRIMARY KEY,
	    admin_id     UUID         NOT NULL REFERENCES users(id),
	    action       VARCHAR(64)  NOT NULL,
	    target_type  VARCHAR(64)  NOT NULL,
	    target_id    UUID,
	    payload      JSONB        NOT NULL DEFAULT '{}',
	    prev_hash    VARCHAR(64)  NOT NULL,
	    entry_hash   VARCHAR(64)  NOT NULL,
	    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_audit_log_admin_id   ON audit_log (admin_id);
	CREATE INDEX IF NOT EXISTS idx_audit_log_created_at ON audit_log (created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_audit_log_target     ON audit_log (target_type, target_id);

	COMMIT;
	`

	if _, err := p.Exec(ctx, migration002); err != nil {
		return err
	}

	migration003 := `
	BEGIN;

	INSERT INTO schema_migrations (version) VALUES ('003_course')
	    ON CONFLICT (version) DO NOTHING;

	CREATE TYPE IF NOT EXISTS course_status AS ENUM (
	    'intake',
	    'syllabus_draft',
	    'syllabus_approved',
	    'generating',
	    'active',
	    'archived',
	    'completed'
	);

	CREATE TABLE IF NOT EXISTS courses (
	    id                     UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
	    student_id             UUID          NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
	    title                  TEXT          NOT NULL DEFAULT '',
	    topic                  TEXT          NOT NULL,
	    status                 course_status NOT NULL DEFAULT 'intake',
	    pre_withdrawal_status  course_status,
	    created_at             TIMESTAMPTZ   NOT NULL DEFAULT now(),
	    updated_at             TIMESTAMPTZ   NOT NULL DEFAULT now()
	);

	CREATE UNIQUE INDEX IF NOT EXISTS courses_single_active_idx
	    ON courses (student_id)
	    WHERE status NOT IN ('archived', 'completed');

	CREATE INDEX IF NOT EXISTS courses_student_id_idx ON courses (student_id);
	CREATE INDEX IF NOT EXISTS courses_status_idx     ON courses (status);
	CREATE INDEX IF NOT EXISTS courses_created_at_id_idx ON courses (created_at DESC, id DESC);

	CREATE TABLE IF NOT EXISTS syllabi (
	    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
	    course_id    UUID        NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
	    content_adoc TEXT        NOT NULL,
	    version      INTEGER     NOT NULL DEFAULT 1,
	    approved_at  TIMESTAMPTZ,
	    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
	);

	CREATE INDEX IF NOT EXISTS syllabi_course_id_idx ON syllabi (course_id);

	CREATE TABLE IF NOT EXISTS homework (
	    id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
	    course_id     UUID         NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
	    section_index INT          NOT NULL,
	    title         VARCHAR(255) NOT NULL,
	    rubric        TEXT         NOT NULL,
	    grade_weight  NUMERIC(4,3) NOT NULL CHECK (grade_weight > 0 AND grade_weight <= 1),
	    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
	);

	CREATE INDEX IF NOT EXISTS homework_course_id_idx ON homework (course_id);

	CREATE TABLE IF NOT EXISTS due_date_schedules (
	    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
	    course_id   UUID        NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
	    homework_id UUID        NOT NULL REFERENCES homework(id) ON DELETE CASCADE,
	    due_date    TIMESTAMPTZ NOT NULL,
	    agreed_at   TIMESTAMPTZ,
	    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
	);

	CREATE INDEX IF NOT EXISTS due_date_schedules_course_id_idx ON due_date_schedules (course_id);
	CREATE UNIQUE INDEX IF NOT EXISTS due_date_schedules_unique_hw
	    ON due_date_schedules (course_id, homework_id);

	CREATE TABLE IF NOT EXISTS agent_token_usage (
	    id                 UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
	    student_id         UUID    NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
	    course_id          UUID    NOT NULL REFERENCES courses(id)  ON DELETE CASCADE,
	    total_tokens_used  BIGINT  NOT NULL DEFAULT 0
	                               CHECK (total_tokens_used >= 0),

	    CONSTRAINT uq_token_usage_student_course UNIQUE (student_id, course_id)
	);

	CREATE INDEX IF NOT EXISTS idx_token_usage_student_id ON agent_token_usage (student_id);

	COMMIT;
	`

	if _, err := p.Exec(ctx, migration003); err != nil {
		return err
	}

	return nil
}

func truncateTables(ctx context.Context, p *pgxpool.Pool) error {
	statements := []string{
		`TRUNCATE TABLE agent_token_usage CASCADE`,
		`TRUNCATE TABLE due_date_schedules CASCADE`,
		`TRUNCATE TABLE homework CASCADE`,
		`TRUNCATE TABLE syllabi CASCADE`,
		`TRUNCATE TABLE courses CASCADE`,
		`TRUNCATE TABLE audit_log CASCADE`,
		`TRUNCATE TABLE student_consent CASCADE`,
		`TRUNCATE TABLE password_reset_attempts CASCADE`,
		`TRUNCATE TABLE password_reset_tokens CASCADE`,
		`TRUNCATE TABLE login_attempts CASCADE`,
		`TRUNCATE TABLE sessions CASCADE`,
		`TRUNCATE TABLE users CASCADE`,
	}
	for _, stmt := range statements {
		if _, err := p.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func createTestUser(ctx context.Context, t *testing.T, username string) uuid.UUID {
	var userID uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
		username, "testhash", "student").
		Scan(&userID)
	if err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}
	return userID
}

// @{"verifies": ["REQ-COURSE-001"]}
func TestCreateCourse_Success(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	username := "testuser_course_" + uuid.New().String()
	studentID := createTestUser(ctx, t, username)

	topic := "Introduction to Go Programming"

	course, err := repo.CreateCourse(ctx, studentID, topic)
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}

	if course.StudentID != studentID {
		t.Errorf("expected StudentID %v, got %v", studentID, course.StudentID)
	}
	if course.Topic != topic {
		t.Errorf("expected Topic %q, got %q", topic, course.Topic)
	}
	if course.Status != "intake" {
		t.Errorf("expected Status 'intake', got %q", course.Status)
	}
	if course.Title != "" {
		t.Errorf("expected empty Title, got %q", course.Title)
	}
}

// @{"verifies": ["REQ-COURSE-001"]}
func TestCreateCourse_UniqueViolation(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	username := "testuser_dup_course_" + uuid.New().String()
	studentID := createTestUser(ctx, t, username)

	topic := "Math"

	_, err1 := repo.CreateCourse(ctx, studentID, topic)
	if err1 != nil {
		t.Fatalf("first CreateCourse failed: %v", err1)
	}

	_, err2 := repo.CreateCourse(ctx, studentID, "Physics")
	if err2 == nil {
		t.Fatalf("expected error on second active course, got nil")
	}
}

// @{"verifies": ["REQ-COURSE-001"]}
func TestGetCourseByID_Found(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	username := "testuser_getid_" + uuid.New().String()
	studentID := createTestUser(ctx, t, username)

	created, err := repo.CreateCourse(ctx, studentID, "Python Basics")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}

	course, err := repo.GetCourseByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetCourseByID failed: %v", err)
	}

	if course.ID != created.ID {
		t.Errorf("expected ID %v, got %v", created.ID, course.ID)
	}
	if course.StudentID != studentID {
		t.Errorf("expected StudentID %v, got %v", studentID, course.StudentID)
	}
}

// @{"verifies": ["REQ-COURSE-001"]}
func TestGetCourseByID_NotFound(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	randomID := uuid.New()

	_, err := repo.GetCourseByID(ctx, randomID)
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// @{"verifies": ["REQ-COURSE-001"]}
func TestListCourses_StudentFilter(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	student1 := createTestUser(ctx, t, "user_student1_"+uuid.New().String())
	student2 := createTestUser(ctx, t, "user_student2_"+uuid.New().String())

	_, err := repo.CreateCourse(ctx, student1, "Math")
	if err != nil {
		t.Fatalf("CreateCourse for student1 failed: %v", err)
	}

	_, err = repo.CreateCourse(ctx, student2, "Physics")
	if err != nil {
		t.Fatalf("CreateCourse for student2 failed: %v", err)
	}

	courses, _, err := repo.ListCourses(ctx, &student1, "", "", 100)
	if err != nil {
		t.Fatalf("ListCourses failed: %v", err)
	}

	if len(courses) != 1 {
		t.Errorf("expected 1 course, got %d", len(courses))
	}
	if courses[0].StudentID != student1 {
		t.Errorf("expected StudentID %v, got %v", student1, courses[0].StudentID)
	}
}

// @{"verifies": ["REQ-COURSE-001", "REQ-COURSE-002"]}
func TestListCourses_AdminNoFilter(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	student1 := createTestUser(ctx, t, "user_admin1_"+uuid.New().String())
	student2 := createTestUser(ctx, t, "user_admin2_"+uuid.New().String())

	c1, err := repo.CreateCourse(ctx, student1, "Math")
	if err != nil {
		t.Fatalf("CreateCourse for student1 failed: %v", err)
	}

	c2, err := repo.CreateCourse(ctx, student2, "Physics")
	if err != nil {
		t.Fatalf("CreateCourse for student2 failed: %v", err)
	}

	courses, _, err := repo.ListCourses(ctx, nil, "", "", 100)
	if err != nil {
		t.Fatalf("ListCourses failed: %v", err)
	}

	// Admin query must return at least the two courses just inserted.
	// Other tests may have inserted additional courses; use >= to avoid
	// a fragile exact-count assertion.
	if len(courses) < 2 {
		t.Errorf("expected at least 2 courses, got %d", len(courses))
	}
	ids := make(map[uuid.UUID]bool, len(courses))
	for _, c := range courses {
		ids[c.ID] = true
	}
	if !ids[c1.ID] {
		t.Errorf("student1 course %v missing from admin listing", c1.ID)
	}
	if !ids[c2.ID] {
		t.Errorf("student2 course %v missing from admin listing", c2.ID)
	}
}

// @{"verifies": ["REQ-COURSE-001", "REQ-COURSE-002"]}
func TestListCourses_StatusFilter(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	student := createTestUser(ctx, t, "user_status_"+uuid.New().String())

	course1, err := repo.CreateCourse(ctx, student, "Math")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}

	_, err = repo.Transition(ctx, course1.ID, []string{"intake"}, "syllabus_draft", nil)
	if err != nil {
		t.Fatalf("Transition failed: %v", err)
	}

	// Scope by student so the assertion is independent of other tests that
	// may leave syllabus_draft courses in the shared database.
	courses, _, err := repo.ListCourses(ctx, &student, "syllabus_draft", "", 100)
	if err != nil {
		t.Fatalf("ListCourses failed: %v", err)
	}

	if len(courses) != 1 {
		t.Errorf("expected 1 course for this student, got %d", len(courses))
	}
	if len(courses) > 0 {
		if courses[0].ID != course1.ID {
			t.Errorf("expected course ID %v, got %v", course1.ID, courses[0].ID)
		}
		if courses[0].Status != "syllabus_draft" {
			t.Errorf("expected Status 'syllabus_draft', got %q", courses[0].Status)
		}
	}
}

// @{"verifies": ["REQ-COURSE-001"]}
func TestListCourses_CursorPagination(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	student := createTestUser(ctx, t, "user_cursor_"+uuid.New().String())

	var courseIDs []uuid.UUID
	for i := 0; i < 3; i++ {
		course, err := repo.CreateCourse(ctx, student, "Topic"+string(rune(i)))
		if err != nil {
			t.Fatalf("CreateCourse failed: %v", err)
		}
		courseIDs = append(courseIDs, course.ID)
		time.Sleep(10 * time.Millisecond)
	}

	page1, nextCursor, err := repo.ListCourses(ctx, &student, "", "", 2)
	if err != nil {
		t.Fatalf("ListCourses page 1 failed: %v", err)
	}

	if len(page1) != 2 {
		t.Errorf("expected 2 courses on page 1, got %d", len(page1))
	}

	if nextCursor == "" {
		t.Errorf("expected non-empty cursor for second page")
	}

	page2, nextCursor2, err := repo.ListCourses(ctx, &student, "", nextCursor, 2)
	if err != nil {
		t.Fatalf("ListCourses page 2 failed: %v", err)
	}

	if len(page2) != 1 {
		t.Errorf("expected 1 course on page 2, got %d", len(page2))
	}

	if nextCursor2 != "" {
		t.Errorf("expected empty cursor after last page")
	}

	if page1[0].ID == page2[0].ID {
		t.Errorf("page 1 and page 2 should not contain the same course")
	}
}

// @{"verifies": ["REQ-COURSE-003", "REQ-COURSE-004"]}
func TestTransition_HappyPath(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	student := createTestUser(ctx, t, "user_transition_"+uuid.New().String())
	course, err := repo.CreateCourse(ctx, student, "Math")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}

	updated, err := repo.Transition(ctx, course.ID, []string{"intake"}, "syllabus_draft", nil)
	if err != nil {
		t.Fatalf("Transition failed: %v", err)
	}

	if updated.Status != "syllabus_draft" {
		t.Errorf("expected Status 'syllabus_draft', got %q", updated.Status)
	}
}

// @{"verifies": ["REQ-COURSE-003", "REQ-COURSE-004"]}
func TestTransition_InvalidState(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	student := createTestUser(ctx, t, "user_invalid_"+uuid.New().String())
	course, err := repo.CreateCourse(ctx, student, "Math")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}

	_, err = repo.Transition(ctx, course.ID, []string{"syllabus_draft"}, "active", nil)
	if err != ErrInvalidTransition {
		t.Fatalf("expected ErrInvalidTransition, got %v", err)
	}
}

// @{"verifies": ["REQ-COURSE-003", "REQ-COURSE-004"]}
func TestTransition_PreWithdrawalStatus(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	student := createTestUser(ctx, t, "user_prewithdraw_"+uuid.New().String())
	course, err := repo.CreateCourse(ctx, student, "Math")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}

	preWithdrawal := "active"
	updated, err := repo.Transition(ctx, course.ID, []string{"intake"}, "archived", &preWithdrawal)
	if err != nil {
		t.Fatalf("Transition failed: %v", err)
	}

	if updated.PreWithdrawalStatus == nil {
		t.Errorf("expected PreWithdrawalStatus to be set")
	} else if *updated.PreWithdrawalStatus != "active" {
		t.Errorf("expected PreWithdrawalStatus 'active', got %q", *updated.PreWithdrawalStatus)
	}
}

// @{"verifies": ["REQ-COURSE-005", "REQ-COURSE-006"]}
func TestGetLatestSyllabus_NotFound(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	randomID := uuid.New()

	_, err := repo.GetLatestSyllabus(ctx, randomID)
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// @{"verifies": ["REQ-COURSE-005", "REQ-COURSE-006"]}
func TestInsertSyllabus_AndApproveSyllabus(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	student := createTestUser(ctx, t, "user_syllabus_"+uuid.New().String())
	course, err := repo.CreateCourse(ctx, student, "Math")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}

	content := "= Introduction to Math\n\nChapter 1: Numbers"
	syllabus, err := repo.InsertSyllabus(ctx, course.ID, content, 1)
	if err != nil {
		t.Fatalf("InsertSyllabus failed: %v", err)
	}

	if syllabus.ContentAdoc != content {
		t.Errorf("expected ContentAdoc %q, got %q", content, syllabus.ContentAdoc)
	}
	if syllabus.Version != 1 {
		t.Errorf("expected Version 1, got %d", syllabus.Version)
	}
	if syllabus.ApprovedAt != nil {
		t.Errorf("expected ApprovedAt to be nil")
	}

	err = repo.ApproveSyllabus(ctx, course.ID, syllabus.ID)
	if err != nil {
		t.Fatalf("ApproveSyllabus failed: %v", err)
	}

	approved, err := repo.GetLatestSyllabus(ctx, course.ID)
	if err != nil {
		t.Fatalf("GetLatestSyllabus failed: %v", err)
	}

	if approved.ApprovedAt == nil {
		t.Errorf("expected ApprovedAt to be set after ApproveSyllabus")
	}
}

// @{"verifies": ["REQ-COURSE-006"]}
func TestApproveSyllabus_WrongCourseID_ReturnsNotFound(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	repo := NewRepository(pool)

	student := createTestUser(ctx, t, "user_approve_wrong_"+uuid.New().String())
	course, err := repo.CreateCourse(ctx, student, "Test Course")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}
	syllabus, err := repo.InsertSyllabus(ctx, course.ID, "# Content", 1)
	if err != nil {
		t.Fatalf("InsertSyllabus failed: %v", err)
	}

	wrongCourseID := uuid.New()
	err = repo.ApproveSyllabus(ctx, wrongCourseID, syllabus.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for wrong course_id, got %v", err)
	}
}

// @{"verifies": ["REQ-COURSE-006"]}
func TestApproveSyllabusTx_WrongCourseID_ReturnsNotFound(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	repo := NewRepository(pool)

	student := createTestUser(ctx, t, "user_approve_tx_wrong_"+uuid.New().String())
	course, err := repo.CreateCourse(ctx, student, "Test Course")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}
	syllabus, err := repo.InsertSyllabus(ctx, course.ID, "# Content", 1)
	if err != nil {
		t.Fatalf("InsertSyllabus failed: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	wrongCourseID := uuid.New()
	err = repo.ApproveSyllabusTx(ctx, tx, wrongCourseID, syllabus.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for wrong course_id in Tx, got %v", err)
	}
}

// @{"verifies": ["REQ-COURSE-008"]}
func TestAgreeToSchedule_HappyPath(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	student := createTestUser(ctx, t, "user_agree_"+uuid.New().String())
	course, err := repo.CreateCourse(ctx, student, "Math")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}

	hw, err := repo.InsertHomework(ctx, course.ID, 1, "Problem Set 1", "Grade everything", 0.25)
	if err != nil {
		t.Fatalf("InsertHomework failed: %v", err)
	}

	dueDate := time.Now().AddDate(0, 0, 7)
	err = repo.InsertDueDateSchedule(ctx, course.ID, hw.ID, dueDate)
	if err != nil {
		t.Fatalf("InsertDueDateSchedule failed: %v", err)
	}

	count, err := repo.AgreeToSchedule(ctx, course.ID)
	if err != nil {
		t.Fatalf("AgreeToSchedule failed: %v", err)
	}

	if count != 1 {
		t.Errorf("expected 1 row updated, got %d", count)
	}
}

// @{"verifies": ["REQ-COURSE-008"]}
func TestAgreeToSchedule_NoPending(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	student := createTestUser(ctx, t, "user_nopending_"+uuid.New().String())
	course, err := repo.CreateCourse(ctx, student, "Math")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}

	_, err = repo.AgreeToSchedule(ctx, course.ID)
	if err != ErrNoPendingSchedule {
		t.Fatalf("expected ErrNoPendingSchedule, got %v", err)
	}
}

// @{"verifies": ["REQ-COURSE-001", "REQ-COURSE-002"]}
func TestCursorPayloadEncoding(t *testing.T) {
	now := time.Now().UTC()
	idStr := uuid.New().String()

	payload := cursorPayload{
		CreatedAt: now.Format(time.RFC3339Nano),
		ID:        idStr,
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	cursor := base64.StdEncoding.EncodeToString(encoded)

	decoded, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		t.Fatalf("base64 decode failed: %v", err)
	}

	var decodedPayload cursorPayload
	if err := json.Unmarshal(decoded, &decodedPayload); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if decodedPayload.ID != idStr {
		t.Errorf("expected ID %q, got %q", idStr, decodedPayload.ID)
	}

	parsedTime, err := time.Parse(time.RFC3339Nano, decodedPayload.CreatedAt)
	if err != nil {
		t.Fatalf("time.Parse failed: %v", err)
	}

	if !parsedTime.Equal(now) {
		t.Errorf("expected time %v, got %v", now, parsedTime)
	}
}
