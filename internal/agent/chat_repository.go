// @{"req": ["REQ-AGENT-015", "REQ-SYS-021"]}
package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ChatMessageRow struct {
	ID        uuid.UUID
	CourseID  uuid.UUID
	Role      string
	Content   string
	CreatedAt time.Time
}

type ChatRepository struct {
	pool *pgxpool.Pool
}

// @{"req": ["REQ-AGENT-015", "REQ-SYS-021"]}
func NewChatRepository(pool *pgxpool.Pool) *ChatRepository {
	return &ChatRepository{pool: pool}
}

// @{"req": ["REQ-AGENT-015", "REQ-SYS-021"]}
func (r *ChatRepository) InsertMessage(ctx context.Context, courseID uuid.UUID, role, content string) (ChatMessageRow, error) {
	var row ChatMessageRow
	err := r.pool.QueryRow(ctx,
		"INSERT INTO chat_messages (course_id, role, content) VALUES ($1, $2, $3) RETURNING id, course_id, role, content, created_at",
		courseID, role, content,
	).Scan(&row.ID, &row.CourseID, &row.Role, &row.Content, &row.CreatedAt)
	if err != nil {
		return ChatMessageRow{}, err
	}
	return row, nil
}

// @{"req": ["REQ-AGENT-015", "REQ-SYS-021"]}
func (r *ChatRepository) ListHistory(ctx context.Context, courseID uuid.UUID, cursor string, limit int) ([]ChatMessageRow, string, error) {
	var cursorCreatedAt time.Time
	var cursorID uuid.UUID

	if cursor != "" {
		decoded, err := base64.StdEncoding.DecodeString(cursor)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cursor: %w", err)
		}

		var cursorData struct {
			CreatedAt string `json:"created_at"`
			ID        string `json:"id"`
		}
		if err := json.Unmarshal(decoded, &cursorData); err != nil {
			return nil, "", fmt.Errorf("invalid cursor format: %w", err)
		}

		var parseErr error
		cursorCreatedAt, parseErr = time.Parse(time.RFC3339Nano, cursorData.CreatedAt)
		if parseErr != nil {
			return nil, "", fmt.Errorf("invalid cursor timestamp: %w", parseErr)
		}

		cursorID, parseErr = uuid.Parse(cursorData.ID)
		if parseErr != nil {
			return nil, "", fmt.Errorf("invalid cursor id: %w", parseErr)
		}
	}

	query := "SELECT id, course_id, role, content, created_at FROM chat_messages WHERE course_id = $1"
	args := []interface{}{courseID}

	if cursor != "" {
		query += " AND (created_at, id) > ($2, $3)"
		args = append(args, cursorCreatedAt, cursorID)
	}

	query += " ORDER BY created_at ASC, id ASC LIMIT $" + fmt.Sprintf("%d", len(args)+1)
	args = append(args, limit+1)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var messages []ChatMessageRow
	for rows.Next() {
		var msg ChatMessageRow
		if err := rows.Scan(&msg.ID, &msg.CourseID, &msg.Role, &msg.Content, &msg.CreatedAt); err != nil {
			return nil, "", err
		}
		messages = append(messages, msg)
	}

	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	nextCursor := ""
	if len(messages) > limit {
		// Cursor points to the last item RETURNED (index limit-1), not the probe row
		lastRow := messages[limit-1]
		messages = messages[:limit]

		cursorData := map[string]string{
			"created_at": lastRow.CreatedAt.Format(time.RFC3339Nano),
			"id":         lastRow.ID.String(),
		}
		cursorJSON, err := json.Marshal(cursorData)
		if err != nil {
			return nil, "", fmt.Errorf("failed to encode cursor: %w", err)
		}
		nextCursor = base64.StdEncoding.EncodeToString(cursorJSON)
	}

	return messages, nextCursor, nil
}

// @{"req": ["REQ-AGENT-015", "REQ-SYS-021"]}
func (r *ChatRepository) GetFullHistory(ctx context.Context, courseID uuid.UUID) ([]ChatMessageRow, error) {
	rows, err := r.pool.Query(ctx,
		"SELECT id, course_id, role, content, created_at FROM chat_messages WHERE course_id = $1 ORDER BY created_at ASC, id ASC",
		courseID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []ChatMessageRow
	for rows.Next() {
		var msg ChatMessageRow
		if err := rows.Scan(&msg.ID, &msg.CourseID, &msg.Role, &msg.Content, &msg.CreatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return messages, nil
}
