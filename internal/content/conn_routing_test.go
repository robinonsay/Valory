//go:build testing

// @{"verifies": ["REQ-CONTENT-001", "REQ-CONTENT-004"]}
package content

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/db"
)

// @{"verifies": ["REQ-CONTENT-001", "REQ-CONTENT-004"]}
func TestContentRepository_Conn_WithoutAuthContext_ReturnsPool(t *testing.T) {
	repo := NewContentRepository(pool)
	q := repo.conn(context.Background())
	// Without an auth connection in context the helper must fall back to the pool.
	var expected db.Querier = pool
	if q != expected {
		t.Error("conn(ctx) without auth context: want pool, got different Querier")
	}
}

// @{"verifies": ["REQ-CONTENT-001", "REQ-CONTENT-004"]}
func TestContentRepository_Conn_WithAuthConn_ReturnsAuthConn(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("pool.Acquire: %v", err)
	}
	defer conn.Release()

	authCtx := auth.WithTestConn(ctx, conn)
	repo := NewContentRepository(pool)
	q := repo.conn(authCtx)
	// With an auth connection injected, conn(ctx) must return that connection.
	if q != db.Querier(conn) {
		t.Error("conn(ctx) with auth conn: want auth conn, got different Querier")
	}
}

// @{"verifies": ["REQ-CONTENT-004"]}
// TestGetSectionContent_AuthConn_RLSFiltersOtherStudentContent verifies that
// GetSectionContent uses the auth connection so RLS policies prevent a student
// from reading another student's lesson content.
func TestGetSectionContent_AuthConn_RLSFiltersOtherStudentContent(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()

	// Create two students with distinct courses and lesson content rows.
	var ownerID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
		"rls_owner_"+uuid.New().String(), "testhash", "student",
	).Scan(&ownerID); err != nil {
		t.Fatalf("create owner: %v", err)
	}

	var ownerCourse uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO courses (student_id, topic) VALUES ($1, $2) RETURNING id`,
		ownerID, "owner topic",
	).Scan(&ownerCourse); err != nil {
		t.Fatalf("create owner course: %v", err)
	}

	// Insert verified lesson content with server role.
	srvConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire server conn: %v", err)
	}
	defer srvConn.Release()
	if _, err := srvConn.Exec(ctx, "SELECT set_config('app.current_role','server',false)"); err != nil {
		t.Fatalf("set server role: %v", err)
	}
	if _, err := srvConn.Exec(ctx,
		`INSERT INTO lesson_content (course_id, section_index, title, content_adoc, citation_verified)
		 VALUES ($1, 0, 'Owner Title', 'Owner content', true)`,
		ownerCourse,
	); err != nil {
		t.Fatalf("insert lesson_content: %v", err)
	}

	// Create an attacker student with their own course.
	var attackerID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
		"rls_attacker_"+uuid.New().String(), "testhash", "student",
	).Scan(&attackerID); err != nil {
		t.Fatalf("create attacker: %v", err)
	}

	// Acquire a connection with attacker identity GUCs set.
	attackerConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire attacker conn: %v", err)
	}
	defer attackerConn.Release()
	attackerHex := strings.ReplaceAll(attackerID.String(), "-", "")
	if _, err := attackerConn.Exec(ctx,
		"SELECT set_config('app.current_user_id',$1,false), set_config('app.current_role','student',false)",
		attackerHex,
	); err != nil {
		t.Fatalf("set attacker GUCs: %v", err)
	}

	// GetSectionContent through the attacker's auth connection must return
	// ErrNotFound because the owner's course is invisible under RLS.
	attackerCtx := auth.WithTestConn(ctx, attackerConn)
	repo := NewContentRepository(pool)
	_, err = repo.GetSectionContent(attackerCtx, ownerCourse, 0)
	if err == nil {
		t.Fatal("GetSectionContent: want ErrNotFound under RLS, got nil error")
	}
	if err != ErrNotFound {
		t.Errorf("GetSectionContent: want ErrNotFound, got %v", err)
	}
}
