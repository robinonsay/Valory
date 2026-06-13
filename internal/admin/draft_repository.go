package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/db"
)

var (
	// ErrDraftNotFound is returned when the requested course draft does not exist.
	ErrDraftNotFound = errors.New("admin: draft not found")

	// ErrDraftNotDeletable is returned when a DeleteDraft call targets a non-draft status.
	ErrDraftNotDeletable = errors.New("admin: draft is not in draft status and cannot be deleted")

	// ErrDraftNodeNotFound is returned when the requested draft node does not exist.
	ErrDraftNodeNotFound = errors.New("admin: draft node not found")
)

// DraftRow is the full record from course_drafts.
type DraftRow struct {
	ID                      uuid.UUID
	AdminID                 uuid.UUID
	Title                   string
	Topic                   string
	Level                   string
	Parameters              json.RawMessage
	Status                  string
	CurrentLayer            *string   // node_type_enum cast to text; nil when not set
	PublishedToAssignmentID *uuid.UUID
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

// DraftListRow is the summary shape returned by ListDrafts.
type DraftListRow struct {
	ID        uuid.UUID
	Title     string
	Topic     string
	Level     string
	Status    string
	CreatedAt time.Time
}

// DraftNodeRow is the full record from draft_nodes.
type DraftNodeRow struct {
	ID        uuid.UUID
	DraftID   uuid.UUID
	ParentID  *uuid.UUID
	NodeType  string
	Ordering  int
	Status    string
	Payload   json.RawMessage
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NodeChatMessage is one turn in a draft node's Chair conversation (node_chats row
// scoped to draft_node_id).
type NodeChatMessage struct {
	ID            uuid.UUID
	DraftNodeID   uuid.UUID
	Role          string
	Content       string
	CreatedAt     time.Time
}

// LayerAggregateResult holds the approved/failed/pending counts for a layer.
type LayerAggregateResult struct {
	Approved int
	Failed   int
	Pending  int
}

// draftCursorPayload is the opaque cursor used for paginating draft lists.
type draftCursorPayload struct {
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

// DraftListOpts controls filtering and pagination for ListDrafts.
type DraftListOpts struct {
	// StatusFilter restricts results to a specific draft status ("draft",
	// "published", etc.).  Empty string means no filter.
	StatusFilter string
	Cursor       string
	Limit        int
}

// DraftNodeListOpts controls filtering for ListDraftNodes.
type DraftNodeListOpts struct {
	// NodeTypeFilter restricts results to a specific node_type.  Empty means no filter.
	NodeTypeFilter string
	// ParentIDFilter restricts results to children of a specific parent.  nil means no filter.
	ParentIDFilter *uuid.UUID
}

// DraftRepository handles all DB operations for the admin-authoring draft tree:
// course_drafts, draft_nodes, and the draft_node_id side of node_chats.
//
// All RLS-governed access is issued through the request-scoped connection
// returned by conn(ctx), which carries app.current_user_id and
// app.current_role='admin' set by the auth middleware.  This ensures that the
// course_drafts_admin_own and draft_nodes_admin_own policies apply rather than
// relying on the superuser bare pool (REQ-SYS-075).
//
// @{"req": ["REQ-AGENT-047", "REQ-AGENT-048", "REQ-AGENT-049", "REQ-AGENT-050",
//            "REQ-AGENT-051", "REQ-AGENT-052", "REQ-AGENT-053", "REQ-AGENT-054",
//            "REQ-AGENT-055", "REQ-AGENT-057", "REQ-AGENT-058", "REQ-SYS-075",
//            "REQ-SYS-077"]}
type DraftRepository struct {
	pool *pgxpool.Pool
}

// NewDraftRepository constructs a DraftRepository backed by pool.
//
// @{"req": ["REQ-AGENT-047", "REQ-SYS-075", "REQ-SYS-077"]}
func NewDraftRepository(pool *pgxpool.Pool) *DraftRepository {
	return &DraftRepository{pool: pool}
}

// conn returns the request-scoped connection stored in ctx by the auth
// middleware when one is present.  That connection carries app.current_user_id
// and app.current_role='admin' GUCs required for RLS evaluation on
// course_drafts and draft_nodes (FORCE ROW LEVEL SECURITY).  Falls back to the
// bare pool for server-pipeline writes that provide their own scoped conn via
// auth.ContextWithConn.
//
// @{"req": ["REQ-SYS-075", "REQ-SYS-077"]}
func (r *DraftRepository) conn(ctx context.Context) db.Querier {
	if c, ok := auth.ConnFromContext(ctx); ok {
		return c
	}
	return r.pool
}

// CreateDraft inserts a course_drafts row and, in the same request-scoped
// connection, seeds the implicit root draft_node holding the topic and level in
// its payload (api-sse §10.2).  Both writes use the conn(ctx) queryer so the
// RLS policies on both tables fire correctly.
//
// The root node is inserted with node_type='root', status='pending', and
// payload={"topic":<topic>,"level":<level>}.
//
// @{"req": ["REQ-AGENT-047", "REQ-AGENT-048", "REQ-AGENT-044", "REQ-SYS-077"]}
func (r *DraftRepository) CreateDraft(ctx context.Context, conn db.Querier, adminID uuid.UUID, title, topic, level string, parameters json.RawMessage) (uuid.UUID, error) {
	if conn == nil {
		conn = r.conn(ctx)
	}

	var draftID uuid.UUID
	err := conn.QueryRow(ctx,
		`INSERT INTO course_drafts (admin_id, title, topic, level, parameters)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id`,
		adminID, title, topic, level, parameters,
	).Scan(&draftID)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("admin: create draft: insert course_drafts: %w", err)
	}

	rootPayload, err := json.Marshal(map[string]string{"topic": topic, "level": level})
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("admin: create draft: marshal root payload: %w", err)
	}

	_, err = conn.Exec(ctx,
		`INSERT INTO draft_nodes (draft_id, node_type, status, payload)
		 VALUES ($1, 'root', 'pending', $2)`,
		draftID, rootPayload,
	)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("admin: create draft: insert root draft_node: %w", err)
	}

	return draftID, nil
}

// GetDraft returns the full course_drafts row for draftID, or ErrDraftNotFound.
//
// @{"req": ["REQ-AGENT-047", "REQ-AGENT-048", "REQ-SYS-075", "REQ-SYS-077"]}
func (r *DraftRepository) GetDraft(ctx context.Context, conn db.Querier, draftID uuid.UUID) (DraftRow, error) {
	if conn == nil {
		conn = r.conn(ctx)
	}

	var row DraftRow
	err := conn.QueryRow(ctx,
		`SELECT id, admin_id, title, topic, level, parameters, status,
		        current_layer::text, published_to_assignment_id, created_at, updated_at
		 FROM course_drafts
		 WHERE id = $1`,
		draftID,
	).Scan(
		&row.ID,
		&row.AdminID,
		&row.Title,
		&row.Topic,
		&row.Level,
		&row.Parameters,
		&row.Status,
		&row.CurrentLayer,
		&row.PublishedToAssignmentID,
		&row.CreatedAt,
		&row.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DraftRow{}, ErrDraftNotFound
		}
		return DraftRow{}, fmt.Errorf("admin: get draft: %w", err)
	}
	return row, nil
}

// ListDrafts returns a paginated list of course_drafts rows for adminID.
// Pagination uses a base64-encoded JSON cursor of the form {"created_at":"...","id":"..."}
// (DESC ordering on created_at, id), matching the existing ListAssignments idiom.
// An optional StatusFilter in opts restricts results to a specific draft status.
//
// @{"req": ["REQ-AGENT-047", "REQ-AGENT-048", "REQ-SYS-075", "REQ-SYS-077"]}
func (r *DraftRepository) ListDrafts(ctx context.Context, conn db.Querier, adminID uuid.UUID, opts DraftListOpts) ([]DraftListRow, string, error) {
	if conn == nil {
		conn = r.conn(ctx)
	}
	if opts.Limit <= 0 {
		opts.Limit = 20
	}

	base := `SELECT id, title, topic, level, status, created_at
	         FROM course_drafts
	         WHERE admin_id = $1`

	var query string
	var args []interface{}

	if opts.StatusFilter != "" {
		base += ` AND status = $3`
		if opts.Cursor == "" {
			query = base + `
			ORDER BY created_at DESC, id DESC
			LIMIT $2`
			args = []interface{}{adminID, opts.Limit + 1, opts.StatusFilter}
		} else {
			decoded, err := base64.StdEncoding.DecodeString(opts.Cursor)
			if err != nil {
				return nil, "", fmt.Errorf("admin: list drafts: decode cursor: %w", err)
			}
			var payload draftCursorPayload
			if err := json.Unmarshal(decoded, &payload); err != nil {
				return nil, "", fmt.Errorf("admin: list drafts: unmarshal cursor: %w", err)
			}
			createdAt, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
			if err != nil {
				return nil, "", fmt.Errorf("admin: list drafts: parse cursor time: %w", err)
			}
			cursorID, err := uuid.Parse(payload.ID)
			if err != nil {
				return nil, "", fmt.Errorf("admin: list drafts: parse cursor id: %w", err)
			}
			query = base + `
			  AND (created_at, id) < ($4, $5)
			ORDER BY created_at DESC, id DESC
			LIMIT $2`
			args = []interface{}{adminID, opts.Limit + 1, opts.StatusFilter, createdAt, cursorID}
		}
	} else {
		if opts.Cursor == "" {
			query = base + `
			ORDER BY created_at DESC, id DESC
			LIMIT $2`
			args = []interface{}{adminID, opts.Limit + 1}
		} else {
			decoded, err := base64.StdEncoding.DecodeString(opts.Cursor)
			if err != nil {
				return nil, "", fmt.Errorf("admin: list drafts: decode cursor: %w", err)
			}
			var payload draftCursorPayload
			if err := json.Unmarshal(decoded, &payload); err != nil {
				return nil, "", fmt.Errorf("admin: list drafts: unmarshal cursor: %w", err)
			}
			createdAt, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
			if err != nil {
				return nil, "", fmt.Errorf("admin: list drafts: parse cursor time: %w", err)
			}
			cursorID, err := uuid.Parse(payload.ID)
			if err != nil {
				return nil, "", fmt.Errorf("admin: list drafts: parse cursor id: %w", err)
			}
			query = base + `
			  AND (created_at, id) < ($3, $4)
			ORDER BY created_at DESC, id DESC
			LIMIT $2`
			args = []interface{}{adminID, opts.Limit + 1, createdAt, cursorID}
		}
	}

	rows, err := conn.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("admin: list drafts: query: %w", err)
	}
	defer rows.Close()

	var results []DraftListRow
	for rows.Next() {
		var d DraftListRow
		if err := rows.Scan(&d.ID, &d.Title, &d.Topic, &d.Level, &d.Status, &d.CreatedAt); err != nil {
			return nil, "", fmt.Errorf("admin: list drafts: scan: %w", err)
		}
		results = append(results, d)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("admin: list drafts: rows: %w", err)
	}

	var nextCursor string
	if len(results) > opts.Limit {
		last := results[opts.Limit]
		p := draftCursorPayload{
			CreatedAt: last.CreatedAt.Format(time.RFC3339Nano),
			ID:        last.ID.String(),
		}
		enc, err := json.Marshal(p)
		if err != nil {
			return nil, "", fmt.Errorf("admin: list drafts: encode cursor: %w", err)
		}
		nextCursor = base64.StdEncoding.EncodeToString(enc)
		results = results[:opts.Limit]
	}

	return results, nextCursor, nil
}

// DeleteDraft removes a course_drafts row only when its status is 'draft'.
// The ON DELETE CASCADE on draft_nodes and node_chats handles child cleanup.
// Returns ErrDraftNotDeletable when the draft is in any other status.
//
// @{"req": ["REQ-AGENT-047", "REQ-AGENT-057", "REQ-SYS-075", "REQ-SYS-077"]}
func (r *DraftRepository) DeleteDraft(ctx context.Context, conn db.Querier, draftID uuid.UUID) error {
	if conn == nil {
		conn = r.conn(ctx)
	}

	tag, err := conn.Exec(ctx,
		`DELETE FROM course_drafts WHERE id = $1 AND status = 'draft'`,
		draftID,
	)
	if err != nil {
		return fmt.Errorf("admin: delete draft: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Either draftID does not exist or status != 'draft'. Distinguish by
		// checking for existence: if not found, return ErrDraftNotFound.
		var exists bool
		// Use the already-resolved conn so RLS GUCs remain in effect for the
		// existence check; re-calling r.conn(ctx) could fall back to the bare
		// pool (no GUCs) under FORCE RLS, producing a false-negative.
		checkErr := conn.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM course_drafts WHERE id = $1)`, draftID,
		).Scan(&exists)
		if checkErr != nil {
			return fmt.Errorf("admin: delete draft: existence check: %w", checkErr)
		}
		if !exists {
			return ErrDraftNotFound
		}
		return ErrDraftNotDeletable
	}
	return nil
}

// UpdateDraftStatus sets the status column on a course_drafts row.
//
// @{"req": ["REQ-AGENT-047", "REQ-AGENT-048", "REQ-SYS-075", "REQ-SYS-077"]}
func (r *DraftRepository) UpdateDraftStatus(ctx context.Context, conn db.Querier, draftID uuid.UUID, status string) error {
	if conn == nil {
		conn = r.conn(ctx)
	}

	tag, err := conn.Exec(ctx,
		`UPDATE course_drafts SET status = $1, updated_at = now() WHERE id = $2`,
		status, draftID,
	)
	if err != nil {
		return fmt.Errorf("admin: update draft status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDraftNotFound
	}
	return nil
}

// SetDraftCurrentLayer sets the current_layer column (a node_type_enum) on a
// course_drafts row, tracking the active generation layer for the poller.
//
// @{"req": ["REQ-AGENT-047", "REQ-AGENT-048", "REQ-AGENT-038", "REQ-SYS-077"]}
func (r *DraftRepository) SetDraftCurrentLayer(ctx context.Context, conn db.Querier, draftID uuid.UUID, layer string) error {
	if conn == nil {
		conn = r.conn(ctx)
	}

	tag, err := conn.Exec(ctx,
		`UPDATE course_drafts
		 SET current_layer = $1::node_type_enum, updated_at = now()
		 WHERE id = $2`,
		layer, draftID,
	)
	if err != nil {
		return fmt.Errorf("admin: set draft current layer: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDraftNotFound
	}
	return nil
}

// CreateDraftNode inserts a draft_nodes row and returns the new row.
//
// @{"req": ["REQ-AGENT-047", "REQ-AGENT-044", "REQ-SYS-075", "REQ-SYS-077"]}
func (r *DraftRepository) CreateDraftNode(ctx context.Context, conn db.Querier, draftID uuid.UUID, parentID *uuid.UUID, nodeType string, ordering int, payload json.RawMessage) (DraftNodeRow, error) {
	if conn == nil {
		conn = r.conn(ctx)
	}
	if payload == nil {
		payload = json.RawMessage("{}")
	}

	var row DraftNodeRow
	err := conn.QueryRow(ctx,
		`INSERT INTO draft_nodes (draft_id, parent_id, node_type, ordering, payload)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, draft_id, parent_id, node_type, ordering, status, payload, created_at, updated_at`,
		draftID, parentID, nodeType, ordering, payload,
	).Scan(
		&row.ID,
		&row.DraftID,
		&row.ParentID,
		&row.NodeType,
		&row.Ordering,
		&row.Status,
		&row.Payload,
		&row.CreatedAt,
		&row.UpdatedAt,
	)
	if err != nil {
		return DraftNodeRow{}, fmt.Errorf("admin: create draft node: %w", err)
	}
	return row, nil
}

// GetDraftNode returns the draft_nodes row for nodeID, or ErrDraftNodeNotFound.
//
// @{"req": ["REQ-AGENT-047", "REQ-AGENT-049", "REQ-SYS-075", "REQ-SYS-077"]}
func (r *DraftRepository) GetDraftNode(ctx context.Context, conn db.Querier, nodeID uuid.UUID) (DraftNodeRow, error) {
	if conn == nil {
		conn = r.conn(ctx)
	}

	var row DraftNodeRow
	err := conn.QueryRow(ctx,
		`SELECT id, draft_id, parent_id, node_type, ordering, status, payload, created_at, updated_at
		 FROM draft_nodes
		 WHERE id = $1`,
		nodeID,
	).Scan(
		&row.ID,
		&row.DraftID,
		&row.ParentID,
		&row.NodeType,
		&row.Ordering,
		&row.Status,
		&row.Payload,
		&row.CreatedAt,
		&row.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DraftNodeRow{}, ErrDraftNodeNotFound
		}
		return DraftNodeRow{}, fmt.Errorf("admin: get draft node: %w", err)
	}
	return row, nil
}

// ListDraftNodes returns all draft_nodes for draftID with optional type/parent
// filtering.  Results are ordered by ordering ASC so the caller receives nodes
// in their authoring sequence.
//
// @{"req": ["REQ-AGENT-047", "REQ-AGENT-058", "REQ-SYS-075", "REQ-SYS-077"]}
func (r *DraftRepository) ListDraftNodes(ctx context.Context, conn db.Querier, draftID uuid.UUID, opts DraftNodeListOpts) ([]DraftNodeRow, error) {
	if conn == nil {
		conn = r.conn(ctx)
	}

	query := `SELECT id, draft_id, parent_id, node_type, ordering, status, payload, created_at, updated_at
	          FROM draft_nodes
	          WHERE draft_id = $1`
	args := []interface{}{draftID}
	argIdx := 2

	if opts.NodeTypeFilter != "" {
		query += fmt.Sprintf(" AND node_type = $%d", argIdx)
		args = append(args, opts.NodeTypeFilter)
		argIdx++
	}
	if opts.ParentIDFilter != nil {
		query += fmt.Sprintf(" AND parent_id = $%d", argIdx)
		args = append(args, *opts.ParentIDFilter)
	}
	query += " ORDER BY ordering ASC"

	rows, err := conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("admin: list draft nodes: query: %w", err)
	}
	defer rows.Close()

	var results []DraftNodeRow
	for rows.Next() {
		var n DraftNodeRow
		if err := rows.Scan(
			&n.ID, &n.DraftID, &n.ParentID, &n.NodeType,
			&n.Ordering, &n.Status, &n.Payload, &n.CreatedAt, &n.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("admin: list draft nodes: scan: %w", err)
		}
		results = append(results, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admin: list draft nodes: rows: %w", err)
	}
	return results, nil
}

// ListDraftNodeChildren returns the direct children of parentID within draftID,
// ordered by ordering ASC.  The parent_id index (draft_nodes_parent_id_idx)
// covers this query efficiently.
//
// @{"req": ["REQ-AGENT-047", "REQ-AGENT-058", "REQ-SYS-075", "REQ-SYS-077"]}
func (r *DraftRepository) ListDraftNodeChildren(ctx context.Context, conn db.Querier, draftID, parentID uuid.UUID) ([]DraftNodeRow, error) {
	if conn == nil {
		conn = r.conn(ctx)
	}

	rows, err := conn.Query(ctx,
		`SELECT id, draft_id, parent_id, node_type, ordering, status, payload, created_at, updated_at
		 FROM draft_nodes
		 WHERE draft_id = $1 AND parent_id = $2
		 ORDER BY ordering ASC`,
		draftID, parentID,
	)
	if err != nil {
		return nil, fmt.Errorf("admin: list draft node children: query: %w", err)
	}
	defer rows.Close()

	var results []DraftNodeRow
	for rows.Next() {
		var n DraftNodeRow
		if err := rows.Scan(
			&n.ID, &n.DraftID, &n.ParentID, &n.NodeType,
			&n.Ordering, &n.Status, &n.Payload, &n.CreatedAt, &n.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("admin: list draft node children: scan: %w", err)
		}
		results = append(results, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admin: list draft node children: rows: %w", err)
	}
	return results, nil
}

// TransitionDraftNodeStatus is an atomic test-and-set that transitions nodeID
// from status fromStatus to toStatus only when the current status matches
// fromStatus.  Returns true when the transition succeeded and false when the
// current status did not match (optimistic concurrency guard — same semantics
// as the student-tree repo, api-sse §10.4).
//
// This prevents concurrent generation from overwriting a node already in a
// later state (REQ-AGENT-055).
//
// @{"req": ["REQ-AGENT-030", "REQ-AGENT-055", "REQ-SYS-075", "REQ-SYS-077"]}
func (r *DraftRepository) TransitionDraftNodeStatus(ctx context.Context, conn db.Querier, nodeID uuid.UUID, fromStatus, toStatus string) (bool, error) {
	if conn == nil {
		conn = r.conn(ctx)
	}

	tag, err := conn.Exec(ctx,
		`UPDATE draft_nodes
		 SET status = $1, updated_at = now()
		 WHERE id = $2 AND status = $3`,
		toStatus, nodeID, fromStatus,
	)
	if err != nil {
		return false, fmt.Errorf("admin: transition draft node status: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// SetDraftNodePayload replaces the payload JSONB of nodeID and bumps updated_at.
// Called by the generation pipeline after producing node content.
//
// @{"req": ["REQ-AGENT-047", "REQ-AGENT-051", "REQ-SYS-075", "REQ-SYS-077"]}
func (r *DraftRepository) SetDraftNodePayload(ctx context.Context, conn db.Querier, nodeID uuid.UUID, payload json.RawMessage) error {
	if conn == nil {
		conn = r.conn(ctx)
	}

	// Atomically transition to 'awaiting_review' alongside the payload write,
	// keeping parity with SetNodePayload in internal/agent/tree_repository.go and
	// preventing a node with a populated payload from remaining stuck in 'generating'.
	tag, err := conn.Exec(ctx,
		`UPDATE draft_nodes SET payload = $1, status = 'awaiting_review', updated_at = now() WHERE id = $2`,
		payload, nodeID,
	)
	if err != nil {
		return fmt.Errorf("admin: set draft node payload: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDraftNodeNotFound
	}
	return nil
}

// LayerAggregate counts the approved, failed, and pending-or-generating nodes
// for a specific node_type layer within draftID.  "pending" here aggregates
// all non-terminal, non-approved statuses (pending, generating, awaiting_review,
// rejected) for the caller to use in HITL gating checks.
//
// @{"req": ["REQ-AGENT-038", "REQ-AGENT-039", "REQ-AGENT-040", "REQ-AGENT-057",
//            "REQ-SYS-075", "REQ-SYS-077"]}
func (r *DraftRepository) LayerAggregate(ctx context.Context, conn db.Querier, draftID uuid.UUID, nodeType string) (approved, failed, pending int, err error) {
	if conn == nil {
		conn = r.conn(ctx)
	}

	rows, err := conn.Query(ctx,
		`SELECT
		     COUNT(*) FILTER (WHERE status = 'approved')                                                     AS approved,
		     COUNT(*) FILTER (WHERE status = 'failed')                                                       AS failed,
		     COUNT(*) FILTER (WHERE status NOT IN ('approved', 'failed'))                                    AS pending
		 FROM draft_nodes
		 WHERE draft_id = $1 AND node_type = $2`,
		draftID, nodeType,
	)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("admin: layer aggregate: query: %w", err)
	}
	defer rows.Close()

	if rows.Next() {
		if scanErr := rows.Scan(&approved, &failed, &pending); scanErr != nil {
			return 0, 0, 0, fmt.Errorf("admin: layer aggregate: scan: %w", scanErr)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, 0, fmt.Errorf("admin: layer aggregate: rows: %w", err)
	}
	return approved, failed, pending, nil
}

// AppendDraftNodeChat inserts a node_chats row on the admin draft-node side
// (draft_node_id set, course_node_id null).  The exactly-one-owner constraint
// in the DB enforces the invariant at the storage layer.
//
// @{"req": ["REQ-AGENT-034", "REQ-AGENT-051", "REQ-AGENT-054", "REQ-SYS-075",
//            "REQ-SYS-077"]}
func (r *DraftRepository) AppendDraftNodeChat(ctx context.Context, conn db.Querier, draftNodeID uuid.UUID, role, content string) (NodeChatMessage, error) {
	if conn == nil {
		conn = r.conn(ctx)
	}

	var msg NodeChatMessage
	err := conn.QueryRow(ctx,
		`INSERT INTO node_chats (draft_node_id, role, content)
		 VALUES ($1, $2, $3)
		 RETURNING id, draft_node_id, role, content, created_at`,
		draftNodeID, role, content,
	).Scan(&msg.ID, &msg.DraftNodeID, &msg.Role, &msg.Content, &msg.CreatedAt)
	if err != nil {
		return NodeChatMessage{}, fmt.Errorf("admin: append draft node chat: %w", err)
	}
	return msg, nil
}

// ListDraftNodeChats returns all node_chats rows for draftNodeID in ascending
// chronological order (created_at ASC), covering the admin chat GET endpoint
// (api-sse §5.1.12).
//
// @{"req": ["REQ-AGENT-034", "REQ-AGENT-053", "REQ-AGENT-054", "REQ-SYS-075",
//            "REQ-SYS-077"]}
func (r *DraftRepository) ListDraftNodeChats(ctx context.Context, conn db.Querier, draftNodeID uuid.UUID) ([]NodeChatMessage, error) {
	if conn == nil {
		conn = r.conn(ctx)
	}

	rows, err := conn.Query(ctx,
		`SELECT id, draft_node_id, role, content, created_at
		 FROM node_chats
		 WHERE draft_node_id = $1
		 ORDER BY created_at ASC`,
		draftNodeID,
	)
	if err != nil {
		return nil, fmt.Errorf("admin: list draft node chats: query: %w", err)
	}
	defer rows.Close()

	var results []NodeChatMessage
	for rows.Next() {
		var msg NodeChatMessage
		if err := rows.Scan(&msg.ID, &msg.DraftNodeID, &msg.Role, &msg.Content, &msg.CreatedAt); err != nil {
			return nil, fmt.Errorf("admin: list draft node chats: scan: %w", err)
		}
		results = append(results, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admin: list draft node chats: rows: %w", err)
	}
	return results, nil
}
