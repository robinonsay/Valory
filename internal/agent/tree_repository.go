// tree_repository.go — data-access for the student knowledge tree.
//
// Covers the course_nodes table and the course_node_id side of node_chats.
// All methods accept a db.Querier so the caller controls the connection
// source: a request-scoped connection (carrying app.current_user_id /
// app.current_role GUCs) for RLS-governed student reads, or a server-role
// connection from db.AcquireServerConn for pipeline writes.  Never use a
// bare pool directly — see memory note "force-rls-superuser-test-masking".
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/valory/valory/internal/db"
)

// NodeType mirrors node_type_enum values defined in 022_tree_generation.sql.
type NodeType string

const (
	NodeTypeRoot        NodeType = "root"
	NodeTypeSyllabus    NodeType = "syllabus"
	NodeTypeSectionGoal NodeType = "section_goal"
	NodeTypeConcept     NodeType = "concept"
	NodeTypeContent     NodeType = "content"
)

// NodeStatus mirrors node_status_enum values defined in 022_tree_generation.sql.
type NodeStatus string

const (
	NodeStatusPending        NodeStatus = "pending"
	NodeStatusGenerating     NodeStatus = "generating"
	NodeStatusAwaitingReview NodeStatus = "awaiting_review"
	NodeStatusApproved       NodeStatus = "approved"
	NodeStatusRejected       NodeStatus = "rejected"
	NodeStatusFailed         NodeStatus = "failed"
)

// CourseNode is the in-memory representation of one course_nodes row.
// Payload is kept as json.RawMessage so callers can decode into typed structs
// without a double-unmarshal through the repository layer.
type CourseNode struct {
	ID         uuid.UUID
	CourseID   uuid.UUID
	ParentID   *uuid.UUID
	NodeType   NodeType
	Ordering   int
	Status     NodeStatus
	Payload    json.RawMessage
	TokensUsed *int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// NodeChatMessage is one row from node_chats scoped to a course_node_id.
type NodeChatMessage struct {
	ID           uuid.UUID
	CourseNodeID uuid.UUID
	Role         string
	Content      string
	CreatedAt    time.Time
}

// ListNodeOptions carries optional filters for ListNodesByCourse.
// Zero values mean "no filter" for that field.
type ListNodeOptions struct {
	NodeType *NodeType  // filter by node_type when non-nil
	ParentID *uuid.UUID // filter by parent_id when non-nil
}

// LayerCounts holds the settled-layer aggregate returned by LayerAggregate.
type LayerCounts struct {
	Approved int
	Failed   int
	Pending  int
}

// TreeRepository is the data-access object for course_nodes and the
// course-node side of node_chats.  It is connection-source-agnostic: every
// method receives a db.Querier that is either a request-scoped *pgxpool.Conn
// (for RLS-governed student access) or a server-role connection acquired via
// db.AcquireServerConn (for pipeline writes).
type TreeRepository struct{}

// NewTreeRepository constructs a TreeRepository.  No pool is stored because
// every method receives the queryer from the caller — keeping the repo
// connection-source-agnostic is the key design invariant.
//
// @{"req": ["REQ-AGENT-027", "REQ-AGENT-030", "REQ-AGENT-032", "REQ-SYS-075"]}
func NewTreeRepository() *TreeRepository {
	return &TreeRepository{}
}

// CreateNode inserts a new course_nodes row with status 'pending'.
// The caller supplies course_id, parent_id (nil for root), node_type,
// ordering, and the initial payload.  Returns the generated UUID.
//
// @{"req": ["REQ-AGENT-027", "REQ-AGENT-028", "REQ-AGENT-029", "REQ-AGENT-030", "REQ-SYS-075"]}
func (r *TreeRepository) CreateNode(
	ctx context.Context,
	conn db.Querier,
	courseID uuid.UUID,
	parentID *uuid.UUID,
	nodeType NodeType,
	ordering int,
	payload json.RawMessage,
) (uuid.UUID, error) {
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}

	var id uuid.UUID
	err := conn.QueryRow(ctx,
		`INSERT INTO course_nodes
		     (course_id, parent_id, node_type, ordering, status, payload)
		 VALUES ($1, $2, $3, $4, 'pending', $5)
		 RETURNING id`,
		courseID, parentID, string(nodeType), ordering, []byte(payload),
	).Scan(&id)
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// GetNode fetches a single course_nodes row by primary key.
// Returns ErrNotFound when no row matches.
//
// @{"req": ["REQ-AGENT-027", "REQ-AGENT-030", "REQ-AGENT-032", "REQ-AGENT-058", "REQ-SYS-075"]}
func (r *TreeRepository) GetNode(
	ctx context.Context,
	conn db.Querier,
	nodeID uuid.UUID,
) (CourseNode, error) {
	var node CourseNode
	var nodeType string
	var nodeStatus string

	err := conn.QueryRow(ctx,
		`SELECT id, course_id, parent_id, node_type, ordering, status,
		        payload, tokens_used, created_at, updated_at
		 FROM course_nodes
		 WHERE id = $1`,
		nodeID,
	).Scan(
		&node.ID,
		&node.CourseID,
		&node.ParentID,
		&nodeType,
		&node.Ordering,
		&nodeStatus,
		&node.Payload,
		&node.TokensUsed,
		&node.CreatedAt,
		&node.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CourseNode{}, ErrNotFound
		}
		return CourseNode{}, err
	}
	node.NodeType = NodeType(nodeType)
	node.Status = NodeStatus(nodeStatus)
	return node, nil
}

// ListNodesByCourse returns all course_nodes for a given course, with optional
// filtering by node_type and/or parent_id.  Order is not guaranteed by this
// method; callers that need ordering should use ListChildren.
//
// @{"req": ["REQ-AGENT-027", "REQ-AGENT-032", "REQ-AGENT-058", "REQ-SYS-075"]}
func (r *TreeRepository) ListNodesByCourse(
	ctx context.Context,
	conn db.Querier,
	courseID uuid.UUID,
	opts ListNodeOptions,
) ([]CourseNode, error) {
	args := []any{courseID}
	query := `SELECT id, course_id, parent_id, node_type, ordering, status,
	                 payload, tokens_used, created_at, updated_at
	          FROM course_nodes
	          WHERE course_id = $1`

	if opts.NodeType != nil {
		args = append(args, string(*opts.NodeType))
		query += ` AND node_type = $` + paramIndex(len(args))
	}
	if opts.ParentID != nil {
		args = append(args, *opts.ParentID)
		query += ` AND parent_id = $` + paramIndex(len(args))
	}

	rows, err := conn.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanNodes(rows)
}

// ListChildren returns the direct children of parentID ordered by ordering ASC,
// matching the course_nodes_parent_id_idx index for efficiency.
//
// @{"req": ["REQ-AGENT-027", "REQ-AGENT-032", "REQ-AGENT-058", "REQ-SYS-075"]}
func (r *TreeRepository) ListChildren(
	ctx context.Context,
	conn db.Querier,
	parentID uuid.UUID,
) ([]CourseNode, error) {
	rows, err := conn.Query(ctx,
		`SELECT id, course_id, parent_id, node_type, ordering, status,
		        payload, tokens_used, created_at, updated_at
		 FROM course_nodes
		 WHERE parent_id = $1
		 ORDER BY ordering ASC`,
		parentID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanNodes(rows)
}

// TransitionNodeStatus is an atomic test-and-set status transition.
// It issues a single UPDATE … WHERE id=$1 AND status=$from and reports
// whether exactly one row was affected.  Returning false (not one row) means
// either the node does not exist or its status is not $from — the caller
// should treat this as a concurrent-generation conflict and abort
// (REQ-AGENT-055).
//
// @{"req": ["REQ-AGENT-030", "REQ-AGENT-055", "REQ-SYS-075"]}
func (r *TreeRepository) TransitionNodeStatus(
	ctx context.Context,
	conn db.Querier,
	nodeID uuid.UUID,
	from NodeStatus,
	to NodeStatus,
) (bool, error) {
	tag, err := conn.Exec(ctx,
		`UPDATE course_nodes
		 SET status = $1, updated_at = now()
		 WHERE id = $2 AND status = $3`,
		string(to), nodeID, string(from),
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// SetNodePayload updates the payload and tokens_used columns for a node, and
// also transitions status to awaiting_review atomically.  This is the
// completion write issued by the LayeredRunner after a successful generation
// call.  tokensUsed may be nil when the caller does not track per-node cost.
//
// @{"req": ["REQ-AGENT-027", "REQ-AGENT-030", "REQ-AGENT-038", "REQ-SYS-075"]}
func (r *TreeRepository) SetNodePayload(
	ctx context.Context,
	conn db.Querier,
	nodeID uuid.UUID,
	payload json.RawMessage,
	tokensUsed *int,
) error {
	_, err := conn.Exec(ctx,
		`UPDATE course_nodes
		 SET payload = $1, tokens_used = $2, status = 'awaiting_review', updated_at = now()
		 WHERE id = $3`,
		[]byte(payload), tokensUsed, nodeID,
	)
	return err
}

// LayerAggregate returns the count of approved, failed, and pending-or-lower
// nodes for a given (courseID, nodeType) layer.  The pending bucket covers
// both 'pending' and 'generating' so the caller can determine whether any
// nodes are still in flight (REQ-AGENT-038, REQ-AGENT-040).
//
// @{"req": ["REQ-AGENT-038", "REQ-AGENT-040", "REQ-AGENT-042", "REQ-SYS-075"]}
func (r *TreeRepository) LayerAggregate(
	ctx context.Context,
	conn db.Querier,
	courseID uuid.UUID,
	nodeType NodeType,
) (LayerCounts, error) {
	rows, err := conn.Query(ctx,
		`SELECT status, COUNT(*)::int
		 FROM course_nodes
		 WHERE course_id = $1 AND node_type = $2
		 GROUP BY status`,
		courseID, string(nodeType),
	)
	if err != nil {
		return LayerCounts{}, err
	}
	defer rows.Close()

	var counts LayerCounts
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return LayerCounts{}, err
		}
		switch NodeStatus(status) {
		case NodeStatusApproved:
			counts.Approved += n
		case NodeStatusFailed:
			counts.Failed += n
		case NodeStatusPending, NodeStatusGenerating, NodeStatusAwaitingReview, NodeStatusRejected:
			// pending + generating + awaiting_review + rejected are all
			// "not yet settled" from the expand-precondition perspective;
			// the caller inspects the full breakdown via individual status
			// queries when it needs finer granularity.
			counts.Pending += n
		}
	}
	if err := rows.Err(); err != nil {
		return LayerCounts{}, err
	}
	return counts, nil
}

// LayerSettleCounts returns the aggregate counts needed to determine whether a
// layer has fully settled for the HITL transition (Bug 1 / D15b fix).
// Unlike LayerAggregate — which buckets awaiting_review into Pending to block
// ExpandToNextLayer — this method counts ONLY pending+generating as in-flight.
// awaiting_review is treated as settled (it is waiting for human review, not for
// AI generation). The settle check in settleLayer must use this method, not
// LayerAggregate, so the course transitions to awaiting_layer_approval as soon
// as all generation goroutines are done (REQ-AGENT-038).
//
// @{"req": ["REQ-AGENT-038", "REQ-AGENT-042", "REQ-SYS-075"]}
func (r *TreeRepository) LayerSettleCounts(
	ctx context.Context,
	conn db.Querier,
	courseID uuid.UUID,
	nodeType NodeType,
) (inFlight int, approvedOrAwaiting int, allFailed bool, err error) {
	rows, queryErr := conn.Query(ctx,
		`SELECT status, COUNT(*)::int
		 FROM course_nodes
		 WHERE course_id = $1 AND node_type = $2
		 GROUP BY status`,
		courseID, string(nodeType),
	)
	if queryErr != nil {
		return 0, 0, false, queryErr
	}
	defer rows.Close()

	var totalNodes int
	for rows.Next() {
		var status string
		var n int
		if scanErr := rows.Scan(&status, &n); scanErr != nil {
			return 0, 0, false, scanErr
		}
		totalNodes += n
		switch NodeStatus(status) {
		case NodeStatusPending, NodeStatusGenerating:
			// Still in flight — generation goroutines have not completed yet.
			inFlight += n
		case NodeStatusApproved, NodeStatusAwaitingReview:
			// Settled from the generation perspective: either human-approved or
			// waiting for human review. Both count as "done generating."
			approvedOrAwaiting += n
		case NodeStatusFailed:
			// Terminal failure.
		// NodeStatusRejected is treated as in-flight (awaiting_regeneration path)
		// if it occurs here, but normal settle only fires after GenerateLayer
		// returns, at which point rejected nodes have been reset to pending.
		case NodeStatusRejected:
			inFlight += n
		}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return 0, 0, false, rowsErr
	}

	// All nodes failed when every node is failed (no approved/awaiting/in-flight).
	allFailed = totalNodes > 0 && inFlight == 0 && approvedOrAwaiting == 0
	return inFlight, approvedOrAwaiting, allFailed, nil
}

// AppendNodeChat inserts one turn into node_chats for a course node.
// role must be "user" or "assistant" (enforced by the DB CHECK constraint).
//
// @{"req": ["REQ-AGENT-034", "REQ-AGENT-051", "REQ-AGENT-053", "REQ-SYS-075"]}
func (r *TreeRepository) AppendNodeChat(
	ctx context.Context,
	conn db.Querier,
	courseNodeID uuid.UUID,
	role string,
	content string,
) error {
	_, err := conn.Exec(ctx,
		`INSERT INTO node_chats (course_node_id, role, content)
		 VALUES ($1, $2, $3)`,
		courseNodeID, role, content,
	)
	return err
}

// ListNodeChats returns all chat turns for a course node in ascending
// chronological order, matching the node_chats_course_node_idx index.
//
// @{"req": ["REQ-AGENT-034", "REQ-AGENT-053", "REQ-SYS-075"]}
func (r *TreeRepository) ListNodeChats(
	ctx context.Context,
	conn db.Querier,
	courseNodeID uuid.UUID,
) ([]NodeChatMessage, error) {
	rows, err := conn.Query(ctx,
		`SELECT id, course_node_id, role, content, created_at
		 FROM node_chats
		 WHERE course_node_id = $1
		 ORDER BY created_at ASC`,
		courseNodeID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []NodeChatMessage
	for rows.Next() {
		var m NodeChatMessage
		if err := rows.Scan(&m.ID, &m.CourseNodeID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return msgs, nil
}

// scanNodes drains a pgx.Rows cursor into a []CourseNode slice.  Used by
// ListNodesByCourse and ListChildren to avoid duplicating the scan loop.
//
// @{"req": ["REQ-AGENT-027", "REQ-AGENT-032", "REQ-SYS-075"]}
func scanNodes(rows pgx.Rows) ([]CourseNode, error) {
	var nodes []CourseNode
	for rows.Next() {
		var node CourseNode
		var nodeType string
		var nodeStatus string

		if err := rows.Scan(
			&node.ID,
			&node.CourseID,
			&node.ParentID,
			&nodeType,
			&node.Ordering,
			&nodeStatus,
			&node.Payload,
			&node.TokensUsed,
			&node.CreatedAt,
			&node.UpdatedAt,
		); err != nil {
			return nil, err
		}
		node.NodeType = NodeType(nodeType)
		node.Status = NodeStatus(nodeStatus)
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return nodes, nil
}

// paramIndex converts an argument count to its PostgreSQL placeholder string
// (e.g. 2 → "2"), used when building dynamic WHERE clauses.
//
// @{"req": ["REQ-AGENT-027", "REQ-AGENT-058"]}
func paramIndex(n int) string {
	return strconv.Itoa(n)
}
