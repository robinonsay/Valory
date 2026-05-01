package notify

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// insertTestNotification inserts a notification row for the given student and
// returns the generated notification UUID. The notification is created unread
// (read_at IS NULL) so callers can selectively mark rows read via direct SQL
// when a test needs a mix of read/unread rows.
func insertTestNotification(ctx context.Context, t *testing.T, studentID uuid.UUID, notifType, message string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO notifications (student_id, type, message) VALUES ($1, $2, $3) RETURNING id`,
		studentID, notifType, message,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insertTestNotification: %v", err)
	}
	return id
}

// markNotificationReadDirect marks a notification as read via a direct SQL
// UPDATE, bypassing the repository. This lets tests set up pre-read state
// without going through the code under test.
func markNotificationReadDirect(ctx context.Context, t *testing.T, notifID uuid.UUID) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`UPDATE notifications SET read_at = now() WHERE id = $1`,
		notifID,
	)
	if err != nil {
		t.Fatalf("markNotificationReadDirect: %v", err)
	}
}

// @{"verifies": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
func TestList_UnreadOnly(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := insertTestUser(ctx, t, "list_unread_"+uuid.New().String())

	repo := NewRepository(pool)

	// Insert two notifications: one will be marked read, one stays unread.
	readID := insertTestNotification(ctx, t, studentID, TypeAPIFailure, "already read")
	_ = insertTestNotification(ctx, t, studentID, TypeGenerationTimeout, "still unread")

	markNotificationReadDirect(ctx, t, readID)

	rows, err := repo.List(ctx, studentID, true, 10, nil)
	if err != nil {
		t.Fatalf("List(unreadOnly=true) returned unexpected error: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("row count: want 1 unread notification, got %d", len(rows))
	}

	if rows[0].ReadAt != nil {
		t.Errorf("returned notification has non-nil ReadAt; expected unread row only")
	}

	if rows[0].Message != "still unread" {
		t.Errorf("wrong notification returned: got message %q", rows[0].Message)
	}
}

// @{"verifies": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
func TestList_AllNotifications(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := insertTestUser(ctx, t, "list_all_"+uuid.New().String())

	repo := NewRepository(pool)

	// Insert three notifications with small sleeps so created_at ordering is
	// deterministic. A seeded sequence of sub-millisecond inserts could share the
	// same timestamp on fast hardware; sleeping 5 ms guarantees strict ordering.
	messages := []string{"first", "second", "third"}
	for _, msg := range messages {
		_ = insertTestNotification(ctx, t, studentID, TypeAPIFailure, msg)
		time.Sleep(5 * time.Millisecond)
	}

	rows, err := repo.List(ctx, studentID, false, 10, nil)
	if err != nil {
		t.Fatalf("List(unreadOnly=false) returned unexpected error: %v", err)
	}

	if len(rows) != 3 {
		t.Fatalf("row count: want 3, got %d", len(rows))
	}

	// List returns rows DESC by created_at, so the newest row comes first.
	if rows[0].Message != "third" {
		t.Errorf("first result: want message %q, got %q", "third", rows[0].Message)
	}
	if rows[1].Message != "second" {
		t.Errorf("second result: want message %q, got %q", "second", rows[1].Message)
	}
	if rows[2].Message != "first" {
		t.Errorf("third result: want message %q, got %q", "first", rows[2].Message)
	}

	// Confirm rows are actually in descending created_at order.
	for i := 1; i < len(rows); i++ {
		if rows[i].CreatedAt.After(rows[i-1].CreatedAt) {
			t.Errorf("row %d has created_at after row %d: not DESC order", i, i-1)
		}
	}
}

// @{"verifies": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
func TestMarkRead_Success(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := insertTestUser(ctx, t, "markread_ok_"+uuid.New().String())

	repo := NewRepository(pool)

	notifID := insertTestNotification(ctx, t, studentID, TypeAPIFailure, "mark me read")

	result, err := repo.MarkRead(ctx, notifID, studentID)
	if err != nil {
		t.Fatalf("MarkRead returned unexpected error: %v", err)
	}

	if result == nil {
		t.Fatal("MarkRead returned nil NotificationRow")
	}

	if result.ReadAt == nil {
		t.Error("ReadAt is nil after MarkRead; expected a non-nil timestamp")
	}

	if result.ID != notifID {
		t.Errorf("returned ID: want %s, got %s", notifID, result.ID)
	}

	if result.StudentID != studentID {
		t.Errorf("returned StudentID: want %s, got %s", studentID, result.StudentID)
	}
}

// @{"verifies": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
func TestMarkRead_Idempotent(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := insertTestUser(ctx, t, "markread_idem_"+uuid.New().String())

	repo := NewRepository(pool)

	notifID := insertTestNotification(ctx, t, studentID, TypeGenerationTimeout, "idempotent test")

	first, err := repo.MarkRead(ctx, notifID, studentID)
	if err != nil {
		t.Fatalf("first MarkRead returned unexpected error: %v", err)
	}
	if first.ReadAt == nil {
		t.Fatal("first MarkRead: ReadAt is nil")
	}

	second, err := repo.MarkRead(ctx, notifID, studentID)
	if err != nil {
		t.Fatalf("second MarkRead returned unexpected error: %v", err)
	}
	if second.ReadAt == nil {
		t.Fatal("second MarkRead: ReadAt is nil")
	}

	// The read_at timestamp must not change on repeated calls — idempotent means
	// no mutation after the first successful mark.
	if !first.ReadAt.Equal(*second.ReadAt) {
		t.Errorf("ReadAt changed between calls: first=%v second=%v", first.ReadAt, second.ReadAt)
	}
}

// @{"verifies": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
func TestMarkRead_WrongStudent(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()

	// Two distinct students: the notification belongs to studentA.
	studentA := insertTestUser(ctx, t, "markread_wrongA_"+uuid.New().String())
	studentB := insertTestUser(ctx, t, "markread_wrongB_"+uuid.New().String())

	repo := NewRepository(pool)

	notifID := insertTestNotification(ctx, t, studentA, TypeAdminEscalation, "belongs to A")

	_, err := repo.MarkRead(ctx, notifID, studentB)
	if err == nil {
		t.Fatal("MarkRead with wrong studentID: want ErrForbidden, got nil")
	}

	if !errors.Is(err, ErrForbidden) {
		t.Errorf("error type: want ErrForbidden, got %v", err)
	}
}

// @{"verifies": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
func TestMarkRead_NotFound(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := insertTestUser(ctx, t, "markread_nf_"+uuid.New().String())

	repo := NewRepository(pool)

	// Use a random UUID that has no matching row in the notifications table.
	nonExistentID := uuid.New()

	_, err := repo.MarkRead(ctx, nonExistentID, studentID)
	if err == nil {
		t.Fatal("MarkRead with non-existent notification: want ErrNotFound, got nil")
	}

	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error type: want ErrNotFound, got %v", err)
	}
}
