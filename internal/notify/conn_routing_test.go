//go:build testing

// @{"verifies": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
package notify

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/db"
)

// @{"verifies": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
func TestNotificationRepository_Conn_WithoutAuthContext_ReturnsPool(t *testing.T) {
	repo := NewRepository(pool)
	q := repo.conn(context.Background())
	var expected db.Querier = pool
	if q != expected {
		t.Error("conn(ctx) without auth context: want pool, got different Querier")
	}
}

// @{"verifies": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
func TestNotificationRepository_Conn_WithAuthConn_ReturnsAuthConn(t *testing.T) {
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
	repo := NewRepository(pool)
	q := repo.conn(authCtx)
	if q != db.Querier(conn) {
		t.Error("conn(ctx) with auth conn: want auth conn, got different Querier")
	}
}

// @{"verifies": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
// TestList_AuthConn_RLSFiltersOtherStudentNotifications verifies that List
// uses the auth connection so RLS prevents a student from reading another
// student's notifications.
func TestList_AuthConn_RLSFiltersOtherStudentNotifications(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()

	ownerID := insertTestUser(ctx, t, "rls_notify_owner_"+uuid.New().String())
	_ = insertTestNotification(ctx, t, ownerID, TypeAPIFailure, "owner's notification")

	// Attacker student.
	attackerID := insertTestUser(ctx, t, "rls_notify_attacker_"+uuid.New().String())

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

	// List through the attacker's auth connection must return empty — the owner's
	// notification is invisible under RLS.
	attackerCtx := auth.WithTestConn(ctx, attackerConn)
	repo := NewRepository(pool)
	rows, err := repo.List(attackerCtx, ownerID, false, 10, nil)
	if err != nil {
		t.Fatalf("List: unexpected error: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("List with attacker auth conn: want 0 rows (RLS filters owner's notifications), got %d", len(rows))
	}
}
