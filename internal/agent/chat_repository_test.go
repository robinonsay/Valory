package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

// insertChatMessage is a helper that inserts a single chat message for the
// given course and sleeps briefly so consecutive messages get distinct
// created_at timestamps (PostgreSQL TIMESTAMPTZ has microsecond precision,
// but tight loops can still land on the same tick).
func insertChatMessage(ctx context.Context, t *testing.T, repo *ChatRepository, courseID uuid.UUID, role, content string) ChatMessageRow {
	t.Helper()
	// Small sleep to ensure each message has a strictly ascending created_at
	// so ordering-sensitive assertions are deterministic.
	time.Sleep(2 * time.Millisecond)
	row, err := repo.InsertMessage(ctx, courseID, role, content)
	if err != nil {
		t.Fatalf("insertChatMessage: %v", err)
	}
	return row
}

// decodeCursor base64-decodes a pagination cursor and returns the embedded
// created_at string and id string.
func decodeCursor(t *testing.T, cursor string) (createdAt string, id string) {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		t.Fatalf("decodeCursor base64: %v", err)
	}
	var data struct {
		CreatedAt string `json:"created_at"`
		ID        string `json:"id"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("decodeCursor json: %v", err)
	}
	return data.CreatedAt, data.ID
}

// @{"verifies": ["REQ-AGENT-015", "REQ-SYS-021"]}
func TestInsertMessage_Success(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewChatRepository(pool)

	studentID := createTestUser(ctx, t, "chat_insert_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Introduction to Go", "active")

	row, err := repo.InsertMessage(ctx, courseID, "assistant", "Hello, student!")
	if err != nil {
		t.Fatalf("InsertMessage failed: %v", err)
	}

	// Verify each field independently so a failure names the exact property.
	if row.CourseID != courseID {
		t.Errorf("CourseID: expected %v, got %v", courseID, row.CourseID)
	}
	if row.Role != "assistant" {
		t.Errorf("Role: expected %q, got %q", "assistant", row.Role)
	}
	if row.Content != "Hello, student!" {
		t.Errorf("Content: expected %q, got %q", "Hello, student!", row.Content)
	}
	// created_at must be a non-zero timestamp set by the database.
	if row.CreatedAt.IsZero() {
		t.Errorf("CreatedAt: expected non-zero timestamp, got zero")
	}
	// The returned ID must be a valid, non-nil UUID.
	if row.ID == uuid.Nil {
		t.Errorf("ID: expected non-nil UUID")
	}
}

// @{"verifies": ["REQ-AGENT-015", "REQ-SYS-021"]}
func TestListHistory_EmptyCursor(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewChatRepository(pool)

	studentID := createTestUser(ctx, t, "chat_list_empty_cursor_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Data Structures", "active")

	// Insert 3 messages; ordering must follow ascending created_at.
	insertChatMessage(ctx, t, repo, courseID, "student", "msg-1")
	insertChatMessage(ctx, t, repo, courseID, "assistant", "msg-2")
	insertChatMessage(ctx, t, repo, courseID, "student", "msg-3")

	messages, nextCursor, err := repo.ListHistory(ctx, courseID, "", 10)
	if err != nil {
		t.Fatalf("ListHistory failed: %v", err)
	}

	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(messages))
	}

	// Verify ascending created_at order.
	for i := 1; i < len(messages); i++ {
		if !messages[i].CreatedAt.After(messages[i-1].CreatedAt) {
			t.Errorf("messages not in ascending created_at order at index %d", i)
		}
	}

	// Content must appear in insertion order.
	if messages[0].Content != "msg-1" {
		t.Errorf("messages[0].Content: expected %q, got %q", "msg-1", messages[0].Content)
	}
	if messages[1].Content != "msg-2" {
		t.Errorf("messages[1].Content: expected %q, got %q", "msg-2", messages[1].Content)
	}
	if messages[2].Content != "msg-3" {
		t.Errorf("messages[2].Content: expected %q, got %q", "msg-3", messages[2].Content)
	}

	// No next page expected when all rows fit within the limit.
	if nextCursor != "" {
		t.Errorf("nextCursor: expected empty, got %q", nextCursor)
	}
}

// @{"verifies": ["REQ-AGENT-015", "REQ-SYS-021"]}
func TestListHistory_Pagination(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewChatRepository(pool)

	studentID := createTestUser(ctx, t, "chat_list_pagination_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Algorithms", "active")

	// Insert 5 messages with guaranteed distinct timestamps.
	msgs := make([]ChatMessageRow, 5)
	for i := 0; i < 5; i++ {
		msgs[i] = insertChatMessage(ctx, t, repo, courseID, "student", "page-msg-"+string(rune('1'+i)))
	}

	// --- First page: limit=2, empty cursor ---
	page1, nextCursor, err := repo.ListHistory(ctx, courseID, "", 2)
	if err != nil {
		t.Fatalf("ListHistory (page 1) failed: %v", err)
	}

	if len(page1) != 2 {
		t.Fatalf("page1: expected 2 messages, got %d", len(page1))
	}
	if nextCursor == "" {
		t.Fatalf("page1: expected non-empty nextCursor")
	}

	// Decode the cursor and verify it encodes the 2nd message's (created_at, id).
	// The cursor points to the last item returned so the next call resumes after it.
	cursorCreatedAt, cursorID := decodeCursor(t, nextCursor)

	expectedCreatedAt := msgs[1].CreatedAt.Format(time.RFC3339Nano)
	if cursorCreatedAt != expectedCreatedAt {
		t.Errorf("cursor created_at: expected %q, got %q", expectedCreatedAt, cursorCreatedAt)
	}
	if cursorID != msgs[1].ID.String() {
		t.Errorf("cursor id: expected %q, got %q", msgs[1].ID.String(), cursorID)
	}

	// --- Second page: limit=2, cursor from first page ---
	page2, nextCursor2, err := repo.ListHistory(ctx, courseID, nextCursor, 2)
	if err != nil {
		t.Fatalf("ListHistory (page 2) failed: %v", err)
	}

	if len(page2) != 2 {
		t.Fatalf("page2: expected 2 messages, got %d", len(page2))
	}

	// Page 2 must contain the 3rd and 4th messages.
	if page2[0].ID != msgs[2].ID {
		t.Errorf("page2[0].ID: expected %v, got %v", msgs[2].ID, page2[0].ID)
	}
	if page2[1].ID != msgs[3].ID {
		t.Errorf("page2[1].ID: expected %v, got %v", msgs[3].ID, page2[1].ID)
	}

	// There is still one more message (msg 5), so a next cursor must be present.
	if nextCursor2 == "" {
		t.Errorf("page2: expected non-empty nextCursor (message 5 remains)")
	}
}

// @{"verifies": ["REQ-AGENT-015", "REQ-SYS-021"]}
func TestListHistory_LastPage(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewChatRepository(pool)

	studentID := createTestUser(ctx, t, "chat_list_lastpage_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Operating Systems", "active")

	insertChatMessage(ctx, t, repo, courseID, "student", "a")
	insertChatMessage(ctx, t, repo, courseID, "assistant", "b")
	insertChatMessage(ctx, t, repo, courseID, "student", "c")

	// limit equals the exact number of messages — no next page expected.
	messages, nextCursor, err := repo.ListHistory(ctx, courseID, "", 3)
	if err != nil {
		t.Fatalf("ListHistory failed: %v", err)
	}

	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(messages))
	}
	if nextCursor != "" {
		t.Errorf("nextCursor: expected empty on last page, got %q", nextCursor)
	}
}

// @{"verifies": ["REQ-AGENT-015", "REQ-SYS-021"]}
func TestListHistory_InvalidCursor(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewChatRepository(pool)

	studentID := createTestUser(ctx, t, "chat_list_invalid_cursor_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Compilers", "active")

	// "notbase64!!" is not valid base64; ListHistory must return an error.
	_, _, err := repo.ListHistory(ctx, courseID, "notbase64!!", 10)
	if err == nil {
		t.Errorf("expected an error for invalid cursor, got nil")
	}
}

// @{"verifies": ["REQ-AGENT-015", "REQ-SYS-021"]}
func TestGetFullHistory(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewChatRepository(pool)

	studentID := createTestUser(ctx, t, "chat_full_history_"+uuid.New().String())
	courseID := createTestCourse(ctx, t, studentID, "Computer Networks", "active")

	contents := []string{"first", "second", "third", "fourth", "fifth"}
	for i, c := range contents {
		role := "student"
		if i%2 == 1 {
			role = "assistant"
		}
		insertChatMessage(ctx, t, repo, courseID, role, c)
	}

	messages, err := repo.GetFullHistory(ctx, courseID)
	if err != nil {
		t.Fatalf("GetFullHistory failed: %v", err)
	}

	if len(messages) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(messages))
	}

	// Verify messages are returned in ascending created_at order.
	for i := 1; i < len(messages); i++ {
		if !messages[i].CreatedAt.After(messages[i-1].CreatedAt) {
			t.Errorf("messages not in ascending created_at order at index %d", i)
		}
	}

	// Verify content matches insertion order.
	for i, want := range contents {
		if messages[i].Content != want {
			t.Errorf("messages[%d].Content: expected %q, got %q", i, want, messages[i].Content)
		}
	}
}
