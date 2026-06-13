// node_handler.go — student-building HITL HTTP handlers for the knowledge tree.
//
// Implements the nine student-facing endpoints defined in knowledge-tree-api-sse.md
// §5.2. Mounted under /api/v1/courses/{id} (the existing authMW + CSRF group).
//
// Security invariants:
//   - D12: all course_nodes writes run on the server pool. Status transitions and
//     payload writes acquire a server-role connection via db.AcquireServerConn;
//     reads (list, get, chat) use the request-scoped connection from the auth
//     middleware (which carries app.current_user_id / app.current_role='student').
//   - Ownership: every endpoint calls courseOwnedBy (using the request-scoped conn)
//     before any DB write. A course the student does not own returns 404 (existence
//     leak prevention matches the existing student-surface idiom in AgentHandler).
//   - Concurrency guard: generate, refine, and regenerate use TransitionNodeStatus
//     as an atomic test-and-set; rowsAffected != 1 maps to the appropriate 409 code.
//   - Background goroutines detach via context.Background() so the HTTP connection
//     being closed does not cancel mid-flight generation (api-sse §6.4).
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/db"
)

// NodeHandler serves the student-building HITL surface for knowledge-tree nodes.
type NodeHandler struct {
	pool          *pgxpool.Pool // server pool — for writes (D12)
	treeRepo      *TreeRepository
	agentRepo     *AgentRepository
	chair         *Chair
	layeredRunner *LayeredRunner
}

// NewNodeHandler constructs a NodeHandler. All node writes go through the
// server pool; reads use the request-scoped connection injected by the auth
// middleware (passed in per-call via db.Querier from auth.ConnFromContext).
//
// @{"req": ["REQ-AGENT-032", "REQ-AGENT-034", "REQ-AGENT-039", "REQ-AGENT-040",
//
//	"REQ-AGENT-050", "REQ-AGENT-051", "REQ-AGENT-052", "REQ-AGENT-053",
//	"REQ-AGENT-055", "REQ-AGENT-058", "REQ-SYS-073"]}
func NewNodeHandler(
	pool *pgxpool.Pool,
	treeRepo *TreeRepository,
	agentRepo *AgentRepository,
	chair *Chair,
	layeredRunner *LayeredRunner,
) *NodeHandler {
	return &NodeHandler{
		pool:          pool,
		treeRepo:      treeRepo,
		agentRepo:     agentRepo,
		chair:         chair,
		layeredRunner: layeredRunner,
	}
}

// Routes mounts the student-building endpoints under an already-authenticated
// router group. Expected mount point: /api/v1/courses/{id}.
// CSRF is already applied by the enclosing group; GET endpoints are exempt.
//
// @{"req": ["REQ-AGENT-032", "REQ-AGENT-034", "REQ-AGENT-039", "REQ-AGENT-040",
//
//	"REQ-AGENT-050", "REQ-AGENT-051", "REQ-AGENT-052", "REQ-AGENT-053",
//	"REQ-AGENT-055", "REQ-AGENT-058", "REQ-SYS-073"]}
func (h *NodeHandler) Routes(r chi.Router) {
	// Node-collection endpoints.
	r.Get("/nodes", h.listNodes)
	r.Post("/nodes/generate", h.generateNode)

	// Per-node endpoints.
	r.Post("/nodes/{nodeId}/refine", h.refineNode)
	r.Patch("/nodes/{nodeId}/approve", h.approveNode)
	r.Patch("/nodes/{nodeId}/feedback", h.feedbackNode)
	r.Post("/nodes/{nodeId}/regenerate", h.regenerateNode)
	r.Get("/nodes/{nodeId}/chat", h.nodeChatHistory)

	// Layer endpoints.
	r.Post("/layers/{layer}/expand", h.expandLayer)
}

// --------------------------------------------------------------------------
// courseNodeResponse is the JSON shape returned for a single course_node.
// --------------------------------------------------------------------------

type courseNodeResponse struct {
	ID        string          `json:"id"`
	CourseID  string          `json:"course_id"`
	ParentID  *string         `json:"parent_id"`
	NodeType  string          `json:"node_type"`
	Ordering  int             `json:"ordering"`
	Status    string          `json:"status"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

// nodeChatResponse is the JSON shape for a single node_chats row.
type nodeChatResponse struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

// toNodeResponse converts a CourseNode into its wire representation.
//
// @{"req": ["REQ-AGENT-058"]}
func toNodeResponse(n CourseNode) courseNodeResponse {
	resp := courseNodeResponse{
		ID:        n.ID.String(),
		CourseID:  n.CourseID.String(),
		NodeType:  string(n.NodeType),
		Ordering:  n.Ordering,
		Status:    string(n.Status),
		Payload:   n.Payload,
		CreatedAt: n.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: n.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if n.ParentID != nil {
		s := n.ParentID.String()
		resp.ParentID = &s
	}
	return resp
}

// --------------------------------------------------------------------------
// Helper: ownership + connection extraction.
// --------------------------------------------------------------------------

// requestStudent returns the authenticated student's UUID from the request
// context, or writes an error response and returns false.
//
// @{"req": ["REQ-AGENT-032", "REQ-AGENT-055", "REQ-SYS-073"]}
func requestStudent(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeAgentError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return uuid.UUID{}, false
	}
	return uuid.UUID(rawID), true
}

// parseCourseID extracts the {id} URL parameter from the chi context.
// The parameter name matches the parent router group in main.go (/{id}).
//
// @{"req": ["REQ-AGENT-032", "REQ-AGENT-058", "REQ-SYS-073"]}
func parseCourseID(r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.UUID{}, false
	}
	return id, true
}

// parseNodeID extracts the {nodeId} URL parameter from the chi context.
//
// @{"req": ["REQ-AGENT-030", "REQ-AGENT-050", "REQ-AGENT-051", "REQ-AGENT-052",
//
//	"REQ-AGENT-053", "REQ-AGENT-055"]}
func parseNodeID(r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "nodeId"))
	if err != nil {
		return uuid.UUID{}, false
	}
	return id, true
}

// checkNodeCourseOwnership verifies the node belongs to the given course using
// the request-scoped connection (which carries the student's RLS GUCs). It
// fetches the node through the student-readable conn; if the node does not
// belong to courseID, returns false. Returns (node, true) on success.
//
// The node is read via the request-scoped conn so that RLS read-own applies.
//
// @{"req": ["REQ-AGENT-032", "REQ-AGENT-033"]}
func (h *NodeHandler) checkNodeCourseOwnership(
	ctx context.Context,
	w http.ResponseWriter,
	nodeID uuid.UUID,
	courseID uuid.UUID,
) (CourseNode, bool) {
	conn, ok := auth.ConnFromContext(ctx)
	if !ok {
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return CourseNode{}, false
	}
	node, err := h.treeRepo.GetNode(ctx, conn, nodeID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeAgentError(w, http.StatusNotFound, "NOT_FOUND", "node not found")
		} else {
			writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		}
		return CourseNode{}, false
	}
	if node.CourseID != courseID {
		// Existence leak prevention: 404 instead of 403.
		writeAgentError(w, http.StatusNotFound, "NOT_FOUND", "node not found")
		return CourseNode{}, false
	}
	return node, true
}

// --------------------------------------------------------------------------
// Handlers
// --------------------------------------------------------------------------

// listNodes handles GET /courses/{id}/nodes
// Returns all course_nodes for the course, with optional node_type / parent_id filters.
// Uses the request-scoped connection (RLS read-own).
//
// @{"req": ["REQ-AGENT-032", "REQ-AGENT-033", "REQ-AGENT-058", "REQ-SYS-073", "REQ-SYS-075"]}
func (h *NodeHandler) listNodes(w http.ResponseWriter, r *http.Request) {
	studentID, ok := requestStudent(w, r)
	if !ok {
		return
	}
	courseID, ok := parseCourseID(r)
	if !ok {
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}

	// Ownership check via request-scoped conn (carries student GUCs).
	if !h.courseOwnedBy(r.Context(), courseID, studentID) {
		writeAgentError(w, http.StatusNotFound, "NOT_FOUND", "course not found")
		return
	}

	// Build filter options from query string.
	var opts ListNodeOptions
	if nt := r.URL.Query().Get("node_type"); nt != "" {
		nt2 := NodeType(nt)
		opts.NodeType = &nt2
	}
	if pid := r.URL.Query().Get("parent_id"); pid != "" {
		parsedPID, err := uuid.Parse(pid)
		if err != nil {
			writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid parent_id")
			return
		}
		opts.ParentID = &parsedPID
	}

	// Read via request-scoped connection (RLS read-own).
	conn, ok := auth.ConnFromContext(r.Context())
	if !ok {
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	nodes, err := h.treeRepo.ListNodesByCourse(r.Context(), conn, courseID, opts)
	if err != nil {
		log.Printf("node_handler: list nodes course=%s: %v", courseID, err)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	// Initialise as empty slice so the JSON encodes as [] not null.
	resp := make([]courseNodeResponse, 0, len(nodes))
	for _, n := range nodes {
		resp = append(resp, toNodeResponse(n))
	}
	writeAgentJSON(w, http.StatusOK, map[string]interface{}{"nodes": resp})
}

// generateNode handles POST /courses/{id}/nodes/generate
// Creates a new node in "generating" status, stores the optional hint, then
// dispatches Chair.GenerateNode in a background goroutine.
//
// @{"req": ["REQ-AGENT-029", "REQ-AGENT-030", "REQ-AGENT-033", "REQ-AGENT-034",
//
//	"REQ-AGENT-035", "REQ-AGENT-036", "REQ-AGENT-044", "REQ-AGENT-051",
//	"REQ-AGENT-055", "REQ-AGENT-058", "REQ-AGENT-060", "REQ-SYS-073", "REQ-SYS-075"]}
func (h *NodeHandler) generateNode(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)

	studentID, ok := requestStudent(w, r)
	if !ok {
		return
	}
	courseID, ok := parseCourseID(r)
	if !ok {
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}
	if !h.courseOwnedBy(r.Context(), courseID, studentID) {
		writeAgentError(w, http.StatusNotFound, "NOT_FOUND", "course not found")
		return
	}

	var req struct {
		NodeType string  `json:"node_type"`
		ParentID *string `json:"parent_id"`
		Hint     string  `json:"hint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if req.NodeType == "" {
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "node_type is required")
		return
	}
	// Allowlist node_type against valid student-creatable values (root is implicit,
	// not directly requestable). An invalid value would otherwise reach the DB and
	// surface as a 500 from the enum constraint. Return 400 early instead.
	switch NodeType(req.NodeType) {
	case NodeTypeSyllabus, NodeTypeSectionGoal, NodeTypeConcept, NodeTypeContent:
		// valid
	default:
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid node_type; must be one of syllabus, section_goal, concept, content")
		return
	}

	var parentID *uuid.UUID
	if req.ParentID != nil {
		pid, err := uuid.Parse(*req.ParentID)
		if err != nil {
			writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid parent_id")
			return
		}
		parentID = &pid
	}

	// All node writes use the server pool (D12).
	// Step 1: insert the node in 'pending' status.
	conn, err := db.AcquireServerConn(r.Context(), h.pool)
	if err != nil {
		log.Printf("node_handler: generate: acquire server conn: %v", err)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	// Validate cross-course parent if supplied (REQ-AGENT-029).
	if parentID != nil {
		var parentCourseID uuid.UUID
		err := conn.QueryRow(r.Context(),
			`SELECT course_id FROM course_nodes WHERE id = $1`,
			*parentID,
		).Scan(&parentCourseID)
		if err != nil {
			conn.Release()
			writeAgentError(w, http.StatusNotFound, "NOT_FOUND", "parent node not found")
			return
		}
		if parentCourseID != courseID {
			conn.Release()
			writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "parent node belongs to a different course")
			return
		}
	}

	nodeID, err := h.treeRepo.CreateNode(r.Context(), conn, courseID, parentID, NodeType(req.NodeType), 0, nil)
	conn.Release()
	if err != nil {
		log.Printf("node_handler: generate: create node: %v", err)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	// Atomic test-and-set: pending → generating (concurrency guard REQ-AGENT-055).
	serverConn, err := db.AcquireServerConn(r.Context(), h.pool)
	if err != nil {
		log.Printf("node_handler: generate: acquire server conn for transition: %v", err)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	ok2, err := h.treeRepo.TransitionNodeStatus(r.Context(), serverConn, nodeID, NodeStatusPending, NodeStatusGenerating)
	serverConn.Release()
	if err != nil {
		log.Printf("node_handler: generate: transition to generating: %v", err)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if !ok2 {
		writeAgentError(w, http.StatusConflict, "NODE_ALREADY_GENERATING", "node generation already in progress")
		return
	}

	// Store the optional hint as a 'user' node_chats row (REQ-AGENT-051).
	if hint := strings.TrimSpace(req.Hint); hint != "" {
		chatConn, chatErr := db.AcquireServerConn(r.Context(), h.pool)
		if chatErr == nil {
			if appendErr := h.treeRepo.AppendNodeChat(r.Context(), chatConn, nodeID, "user", hint); appendErr != nil {
				log.Printf("node_handler: generate: append hint chat: %v", appendErr)
			}
			chatConn.Release()
		}
	}

	// Create the agent_run for SSE event anchoring.
	run, err := h.agentRepo.CreateRun(r.Context(), courseID, "node_generation")
	if err != nil {
		log.Printf("node_handler: generate: create run: %v", err)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	// Emit node_generating before launching the goroutine so the SSE stream
	// carries the event before the response lands in the browser.
	if emitErr := h.agentRepo.EmitNodeEvent(r.Context(), run.ID, "node_generating", NodeEventPayload{
		NodeID:   &nodeID,
		NodeType: req.NodeType,
		CourseID: &courseID,
	}); emitErr != nil {
		log.Printf("node_handler: generate: emit node_generating: %v", emitErr)
	}

	// Fetch the freshly-inserted node via request-scoped conn for the response.
	rConn, rOk := auth.ConnFromContext(r.Context())
	var node CourseNode
	if rOk {
		node, err = h.treeRepo.GetNode(r.Context(), rConn, nodeID)
		if err != nil {
			log.Printf("node_handler: generate: get node for response: %v", err)
		}
	}

	// Load course meta for the background goroutine.
	meta, metaErr := h.layeredRunner.loadCourseMeta(r.Context(), courseID)
	if metaErr != nil {
		log.Printf("node_handler: generate: load course meta: %v", metaErr)
		// Non-fatal for the response; the goroutine will fail gracefully.
	}

	// Dispatch background generation — detached context (api-sse §6.4).
	go h.runBackgroundGenerate(context.Background(), run.ID, nodeID, courseID, studentID, meta, NodeType(req.NodeType))

	writeAgentJSON(w, http.StatusAccepted, map[string]interface{}{
		"node":   toNodeResponse(node),
		"run_id": run.ID.String(),
	})
}

// runBackgroundGenerate is the goroutine body for generate and regenerate.
// It calls Chair.GenerateNode, writes the result, and emits node_ready / node_error.
//
// @{"req": ["REQ-AGENT-035", "REQ-AGENT-036", "REQ-AGENT-044", "REQ-AGENT-060", "REQ-SYS-073"]}
func (h *NodeHandler) runBackgroundGenerate(
	ctx context.Context,
	runID uuid.UUID,
	nodeID uuid.UUID,
	courseID uuid.UUID,
	studentID uuid.UUID,
	meta courseMetaRow,
	nodeType NodeType,
) {
	// Load node_chats for context (hint / prior feedback).
	chatConn, chatErr := db.AcquireServerConn(ctx, h.pool)
	var priorContext []NodeChatMessage
	if chatErr == nil {
		priorContext, _ = h.treeRepo.ListNodeChats(ctx, chatConn, nodeID)
		chatConn.Release()
	}

	newPayload, err := h.chair.GenerateNode(
		ctx,
		studentID,
		courseID,
		false, // isDraftContext — student path
		string(nodeType),
		meta.Topic,
		meta.Level,
		meta.Parameters,
		priorContext,
	)
	if err != nil {
		log.Printf("node_handler: background generate node %s: %v", nodeID, err)
		// Transition to failed and emit node_error.
		if fConn, fErr := db.AcquireServerConn(ctx, h.pool); fErr == nil {
			if _, tErr := fConn.Exec(ctx,
				`UPDATE course_nodes SET status = 'failed', updated_at = now() WHERE id = $1`,
				nodeID,
			); tErr != nil {
				log.Printf("node_handler: fail node %s: %v", nodeID, tErr)
			}
			fConn.Release()
		}
		if emitErr := h.agentRepo.EmitNodeEvent(ctx, runID, "node_error", NodeEventPayload{
			NodeID:   &nodeID,
			NodeType: string(nodeType),
			CourseID: &courseID,
			Error:    fmt.Sprintf("generation failed: %v", err),
		}); emitErr != nil {
			log.Printf("node_handler: emit node_error for %s: %v", nodeID, emitErr)
		}
		if setErr := h.agentRepo.SetRunStatus(ctx, runID, "failed", nil); setErr != nil {
			log.Printf("node_handler: set run failed for %s: %v", runID, setErr)
		}
		return
	}

	// Write the payload (transitions to awaiting_review atomically via SetNodePayload).
	writeConn, writeErr := db.AcquireServerConn(ctx, h.pool)
	if writeErr != nil {
		log.Printf("node_handler: background generate: acquire write conn for node %s: %v", nodeID, writeErr)
		return
	}
	if setErr := h.treeRepo.SetNodePayload(ctx, writeConn, nodeID, newPayload, nil); setErr != nil {
		writeConn.Release()
		log.Printf("node_handler: background generate: set payload for node %s: %v", nodeID, setErr)
		return
	}
	writeConn.Release()

	// Append assistant turn to node_chats so future refinements see the history.
	if chatConn2, err2 := db.AcquireServerConn(ctx, h.pool); err2 == nil {
		if appendErr := h.treeRepo.AppendNodeChat(ctx, chatConn2, nodeID, "assistant", string(newPayload)); appendErr != nil {
			log.Printf("node_handler: background generate: append assistant chat for %s: %v", nodeID, appendErr)
		}
		chatConn2.Release()
	}

	// Payload summary for the SSE envelope (first 200 chars).
	summary := string(newPayload)
	if len(summary) > 200 {
		summary = summary[:200]
	}

	if emitErr := h.agentRepo.EmitNodeEvent(ctx, runID, "node_ready", NodeEventPayload{
		NodeID:   &nodeID,
		NodeType: string(nodeType),
		CourseID: &courseID,
		Summary:  summary,
	}); emitErr != nil {
		log.Printf("node_handler: emit node_ready for %s: %v", nodeID, emitErr)
	}
	if setErr := h.agentRepo.SetRunStatus(ctx, runID, "completed", nil); setErr != nil {
		log.Printf("node_handler: set run completed for %s: %v", runID, setErr)
	}
}

// refineNode handles POST /courses/{id}/nodes/{nodeId}/refine
// Stores the required message in node_chats, transitions to generating, and
// dispatches Chair.RefineNode in a background goroutine.
//
// @{"req": ["REQ-AGENT-030", "REQ-AGENT-034", "REQ-AGENT-036", "REQ-AGENT-050",
//
//	"REQ-AGENT-051", "REQ-AGENT-055", "REQ-SYS-073"]}
func (h *NodeHandler) refineNode(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)

	studentID, ok := requestStudent(w, r)
	if !ok {
		return
	}
	courseID, ok := parseCourseID(r)
	if !ok {
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}
	nodeID, ok := parseNodeID(r)
	if !ok {
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid node id")
		return
	}

	if !h.courseOwnedBy(r.Context(), courseID, studentID) {
		writeAgentError(w, http.StatusNotFound, "NOT_FOUND", "course not found")
		return
	}

	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "message is required")
		return
	}

	// Verify the node belongs to this course.
	node, ok := h.checkNodeCourseOwnership(r.Context(), w, nodeID, courseID)
	if !ok {
		return
	}

	// Store the user message BEFORE the transition so the Chair sees it when
	// building chat history in the background goroutine (REQ-AGENT-051).
	chatConn, chatErr := db.AcquireServerConn(r.Context(), h.pool)
	if chatErr != nil {
		log.Printf("node_handler: refine: acquire chat conn: %v", chatErr)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if appendErr := h.treeRepo.AppendNodeChat(r.Context(), chatConn, nodeID, "user", req.Message); appendErr != nil {
		chatConn.Release()
		log.Printf("node_handler: refine: append user chat: %v", appendErr)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	chatConn.Release()

	// Concurrency guard: reject if the node is already generating (REQ-AGENT-055).
	// We must check before the atomic transition because TransitionNodeStatus with
	// from=generating, to=generating would match the WHERE clause and return
	// rowsAffected=1 incorrectly, appearing to succeed when it should conflict.
	if node.Status == NodeStatusGenerating {
		writeAgentError(w, http.StatusConflict, "NODE_ALREADY_GENERATING", "node generation already in progress")
		return
	}

	// Atomic test-and-set: current status → generating (D12 — server conn).
	// The WHERE status=$from guard ensures only one concurrent refine wins when
	// two requests arrive for the same node simultaneously.
	serverConn, err := db.AcquireServerConn(r.Context(), h.pool)
	if err != nil {
		log.Printf("node_handler: refine: acquire server conn: %v", err)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	ok2, err := h.treeRepo.TransitionNodeStatus(r.Context(), serverConn, nodeID, node.Status, NodeStatusGenerating)
	serverConn.Release()
	if err != nil {
		log.Printf("node_handler: refine: transition to generating: %v", err)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if !ok2 {
		writeAgentError(w, http.StatusConflict, "NODE_ALREADY_GENERATING", "node generation already in progress")
		return
	}

	// Create the agent_run for SSE anchoring.
	run, err := h.agentRepo.CreateRun(r.Context(), courseID, "node_generation")
	if err != nil {
		log.Printf("node_handler: refine: create run: %v", err)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	if emitErr := h.agentRepo.EmitNodeEvent(r.Context(), run.ID, "node_generating", NodeEventPayload{
		NodeID:   &nodeID,
		NodeType: string(node.NodeType),
		CourseID: &courseID,
	}); emitErr != nil {
		log.Printf("node_handler: refine: emit node_generating: %v", emitErr)
	}

	// Re-fetch updated node for the response (status is now generating).
	var updatedNode CourseNode
	if rConn, rOk := auth.ConnFromContext(r.Context()); rOk {
		updatedNode, _ = h.treeRepo.GetNode(r.Context(), rConn, nodeID)
	}

	meta, _ := h.layeredRunner.loadCourseMeta(r.Context(), courseID)

	go h.runBackgroundRefine(context.Background(), run.ID, nodeID, courseID, studentID, meta, node.NodeType, node.Payload)

	writeAgentJSON(w, http.StatusAccepted, map[string]interface{}{
		"node":   toNodeResponse(updatedNode),
		"run_id": run.ID.String(),
	})
}

// runBackgroundRefine is the goroutine body for refine.
//
// @{"req": ["REQ-AGENT-034", "REQ-AGENT-050", "REQ-AGENT-051", "REQ-AGENT-060", "REQ-SYS-073"]}
func (h *NodeHandler) runBackgroundRefine(
	ctx context.Context,
	runID uuid.UUID,
	nodeID uuid.UUID,
	courseID uuid.UUID,
	studentID uuid.UUID,
	meta courseMetaRow,
	nodeType NodeType,
	existingPayload json.RawMessage,
) {
	// Load full chat history (includes the user message just appended).
	chatConn, chatErr := db.AcquireServerConn(ctx, h.pool)
	var chatHistory []NodeChatMessage
	if chatErr == nil {
		chatHistory, _ = h.treeRepo.ListNodeChats(ctx, chatConn, nodeID)
		chatConn.Release()
	}

	newPayload, err := h.chair.RefineNode(
		ctx,
		studentID,
		courseID,
		false,
		string(nodeType),
		existingPayload,
		chatHistory,
	)
	if err != nil {
		log.Printf("node_handler: background refine node %s: %v", nodeID, err)
		if fConn, fErr := db.AcquireServerConn(ctx, h.pool); fErr == nil {
			if _, tErr := fConn.Exec(ctx,
				`UPDATE course_nodes SET status = 'failed', updated_at = now() WHERE id = $1`,
				nodeID,
			); tErr != nil {
				log.Printf("node_handler: background refine: fail node %s: %v", nodeID, tErr)
			}
			fConn.Release()
		}
		if emitErr := h.agentRepo.EmitNodeEvent(ctx, runID, "node_error", NodeEventPayload{
			NodeID:   &nodeID,
			NodeType: string(nodeType),
			CourseID: &courseID,
			Error:    fmt.Sprintf("refinement failed: %v", err),
		}); emitErr != nil {
			log.Printf("node_handler: emit node_error for refine %s: %v", nodeID, emitErr)
		}
		if setErr := h.agentRepo.SetRunStatus(ctx, runID, "failed", nil); setErr != nil {
			log.Printf("node_handler: background refine: set run failed %s: %v", runID, setErr)
		}
		return
	}

	writeConn, writeErr := db.AcquireServerConn(ctx, h.pool)
	if writeErr != nil {
		log.Printf("node_handler: background refine: acquire write conn: %v", writeErr)
		return
	}
	if setErr := h.treeRepo.SetNodePayload(ctx, writeConn, nodeID, newPayload, nil); setErr != nil {
		writeConn.Release()
		log.Printf("node_handler: background refine: set payload: %v", setErr)
		return
	}
	writeConn.Release()

	// Append the assistant turn so the conversation stays coherent.
	if c2, e2 := db.AcquireServerConn(ctx, h.pool); e2 == nil {
		if appendErr := h.treeRepo.AppendNodeChat(ctx, c2, nodeID, "assistant", string(newPayload)); appendErr != nil {
			log.Printf("node_handler: background refine: append assistant chat for %s: %v", nodeID, appendErr)
		}
		c2.Release()
	}

	summary := string(newPayload)
	if len(summary) > 200 {
		summary = summary[:200]
	}
	if emitErr := h.agentRepo.EmitNodeEvent(ctx, runID, "node_ready", NodeEventPayload{
		NodeID:   &nodeID,
		NodeType: string(nodeType),
		CourseID: &courseID,
		Summary:  summary,
	}); emitErr != nil {
		log.Printf("node_handler: emit node_ready for refine %s: %v", nodeID, emitErr)
	}
	if setErr := h.agentRepo.SetRunStatus(ctx, runID, "completed", nil); setErr != nil {
		log.Printf("node_handler: background refine: set run completed %s: %v", runID, setErr)
	}
}

// approveNode handles PATCH /courses/{id}/nodes/{nodeId}/approve
// Transitions awaiting_review → approved. After approval, if the layer is
// fully settled (all nodes approved), emits layer_awaiting_expand.
//
// @{"req": ["REQ-AGENT-030", "REQ-AGENT-038", "REQ-AGENT-039", "REQ-AGENT-040",
//
//	"REQ-AGENT-060", "REQ-SYS-073", "REQ-SYS-075"]}
func (h *NodeHandler) approveNode(w http.ResponseWriter, r *http.Request) {
	studentID, ok := requestStudent(w, r)
	if !ok {
		return
	}
	courseID, ok := parseCourseID(r)
	if !ok {
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}
	nodeID, ok := parseNodeID(r)
	if !ok {
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid node id")
		return
	}
	if !h.courseOwnedBy(r.Context(), courseID, studentID) {
		writeAgentError(w, http.StatusNotFound, "NOT_FOUND", "course not found")
		return
	}

	node, ok := h.checkNodeCourseOwnership(r.Context(), w, nodeID, courseID)
	if !ok {
		return
	}

	// Atomic test-and-set: awaiting_review → approved (D12 — server conn).
	serverConn, err := db.AcquireServerConn(r.Context(), h.pool)
	if err != nil {
		log.Printf("node_handler: approve: acquire server conn: %v", err)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	ok2, err := h.treeRepo.TransitionNodeStatus(r.Context(), serverConn, nodeID, NodeStatusAwaitingReview, NodeStatusApproved)
	serverConn.Release()
	if err != nil {
		log.Printf("node_handler: approve: transition: %v", err)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if !ok2 {
		writeAgentError(w, http.StatusConflict, "NODE_NOT_REVIEWABLE", "node is not awaiting review")
		return
	}

	// Check if the whole layer is now fully approved (REQ-AGENT-038, REQ-AGENT-040).
	go h.emitLayerExpandIfSettled(context.Background(), courseID, node.NodeType)

	// Re-fetch the node for the response.
	var approved CourseNode
	if rConn, rOk := auth.ConnFromContext(r.Context()); rOk {
		approved, _ = h.treeRepo.GetNode(r.Context(), rConn, nodeID)
	}

	writeAgentJSON(w, http.StatusOK, map[string]interface{}{
		"node": toNodeResponse(approved),
	})
}

// emitLayerExpandIfSettled checks whether all nodes in the layer have status
// 'approved' (none pending, none awaiting_review, none rejected). When the
// layer is fully approved it emits a layer_awaiting_expand SSE event so the
// frontend can show the "expand to next layer" button.
//
// Runs in a background goroutine so it does not block the approve response.
//
// @{"req": ["REQ-AGENT-038", "REQ-AGENT-039", "REQ-AGENT-040", "REQ-AGENT-060", "REQ-SYS-073"]}
func (h *NodeHandler) emitLayerExpandIfSettled(ctx context.Context, courseID uuid.UUID, nodeType NodeType) {
	conn, err := db.AcquireServerConn(ctx, h.pool)
	if err != nil {
		log.Printf("node_handler: settle check: acquire conn: %v", err)
		return
	}
	counts, err := h.treeRepo.LayerAggregate(ctx, conn, courseID, nodeType)
	conn.Release()
	if err != nil {
		log.Printf("node_handler: settle check: layer aggregate: %v", err)
		return
	}

	// All settled = Pending==0 AND Approved>0 (no nodes awaiting_review, generating, rejected, etc.)
	if counts.Pending > 0 || counts.Approved == 0 {
		return
	}

	// Create a short-lived run to anchor the event.
	run, err := h.agentRepo.CreateRun(ctx, courseID, "node_generation")
	if err != nil {
		log.Printf("node_handler: settle check: create run: %v", err)
		return
	}
	_ = h.agentRepo.EmitNodeEvent(ctx, run.ID, "layer_awaiting_expand", NodeEventPayload{
		CourseID: &courseID,
		Layer:    string(nodeType),
	})
	if setErr := h.agentRepo.SetRunStatus(ctx, run.ID, "completed", nil); setErr != nil {
		log.Printf("node_handler: settle check: set run completed: %v", setErr)
	}
}

// feedbackNode handles PATCH /courses/{id}/nodes/{nodeId}/feedback
// The only rejection path. Requires non-empty feedback, stores it in node_chats
// AND in payload.feedback, then transitions awaiting_review → rejected.
//
// @{"req": ["REQ-AGENT-030", "REQ-AGENT-034", "REQ-AGENT-050", "REQ-AGENT-051",
//
//	"REQ-SYS-073", "REQ-SYS-075"]}
func (h *NodeHandler) feedbackNode(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)

	studentID, ok := requestStudent(w, r)
	if !ok {
		return
	}
	courseID, ok := parseCourseID(r)
	if !ok {
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}
	nodeID, ok := parseNodeID(r)
	if !ok {
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid node id")
		return
	}
	if !h.courseOwnedBy(r.Context(), courseID, studentID) {
		writeAgentError(w, http.StatusNotFound, "NOT_FOUND", "course not found")
		return
	}

	var req struct {
		Feedback string `json:"feedback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if strings.TrimSpace(req.Feedback) == "" {
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "feedback is required and must not be empty")
		return
	}

	node, ok := h.checkNodeCourseOwnership(r.Context(), w, nodeID, courseID)
	if !ok {
		return
	}

	// 1. Store feedback as a 'user' node_chats row (D12 — server conn).
	chatConn, chatErr := db.AcquireServerConn(r.Context(), h.pool)
	if chatErr != nil {
		log.Printf("node_handler: feedback: acquire chat conn: %v", chatErr)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if appendErr := h.treeRepo.AppendNodeChat(r.Context(), chatConn, nodeID, "user", req.Feedback); appendErr != nil {
		chatConn.Release()
		log.Printf("node_handler: feedback: append chat: %v", appendErr)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	chatConn.Release()

	// 2. Merge feedback into payload.feedback (D12 — server conn).
	newPayload, mergeErr := mergeFeedbackIntoPayload(node.Payload, req.Feedback)
	if mergeErr != nil {
		log.Printf("node_handler: feedback: merge payload: %v", mergeErr)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	// Write the merged payload and transition to rejected atomically. We use a
	// direct UPDATE rather than SetNodePayload (which sets awaiting_review) because
	// we need to set rejected and write payload in one operation.
	serverConn, err := db.AcquireServerConn(r.Context(), h.pool)
	if err != nil {
		log.Printf("node_handler: feedback: acquire server conn: %v", err)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	tag, err := serverConn.Exec(r.Context(),
		`UPDATE course_nodes
		 SET status = 'rejected', payload = $1, updated_at = now()
		 WHERE id = $2 AND status = 'awaiting_review'`,
		[]byte(newPayload), nodeID,
	)
	serverConn.Release()
	if err != nil {
		log.Printf("node_handler: feedback: update: %v", err)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if tag.RowsAffected() != 1 {
		// Node was not in awaiting_review — return current node state.
		log.Printf("node_handler: feedback: node %s not in awaiting_review (rows=%d)", nodeID, tag.RowsAffected())
		writeAgentError(w, http.StatusConflict, "NODE_NOT_REVIEWABLE", "node is not awaiting review")
		return
	}

	// Re-fetch the node for the response.
	var rejected CourseNode
	if rConn, rOk := auth.ConnFromContext(r.Context()); rOk {
		rejected, _ = h.treeRepo.GetNode(r.Context(), rConn, nodeID)
	}

	writeAgentJSON(w, http.StatusOK, map[string]interface{}{
		"node": toNodeResponse(rejected),
	})
}

// mergeFeedbackIntoPayload merges the feedback string into the existing JSONB
// payload under the "feedback" key. The result is a new JSON object that
// includes all existing payload fields plus the feedback key.
//
// @{"req": ["REQ-AGENT-051"]}
func mergeFeedbackIntoPayload(existing json.RawMessage, feedback string) (json.RawMessage, error) {
	var m map[string]interface{}
	if len(existing) > 0 && string(existing) != "{}" && string(existing) != "null" {
		if err := json.Unmarshal(existing, &m); err != nil {
			m = make(map[string]interface{})
		}
	} else {
		m = make(map[string]interface{})
	}
	m["feedback"] = feedback
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("merge feedback into payload: %w", err)
	}
	return json.RawMessage(b), nil
}

// regenerateNode handles POST /courses/{id}/nodes/{nodeId}/regenerate
// Transitions rejected|failed → generating, then dispatches Chair.GenerateNode.
//
// @{"req": ["REQ-AGENT-030", "REQ-AGENT-036", "REQ-AGENT-044", "REQ-AGENT-052",
//
//	"REQ-AGENT-055", "REQ-AGENT-060", "REQ-SYS-073"]}
func (h *NodeHandler) regenerateNode(w http.ResponseWriter, r *http.Request) {
	studentID, ok := requestStudent(w, r)
	if !ok {
		return
	}
	courseID, ok := parseCourseID(r)
	if !ok {
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}
	nodeID, ok := parseNodeID(r)
	if !ok {
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid node id")
		return
	}
	if !h.courseOwnedBy(r.Context(), courseID, studentID) {
		writeAgentError(w, http.StatusNotFound, "NOT_FOUND", "course not found")
		return
	}

	node, ok := h.checkNodeCourseOwnership(r.Context(), w, nodeID, courseID)
	if !ok {
		return
	}

	// Only rejected or failed nodes can be regenerated (REQ-AGENT-052).
	if node.Status != NodeStatusRejected && node.Status != NodeStatusFailed {
		writeAgentError(w, http.StatusConflict, "NODE_NOT_REGENERABLE", "node is not in rejected or failed status")
		return
	}

	// Atomic test-and-set to generating.
	serverConn, err := db.AcquireServerConn(r.Context(), h.pool)
	if err != nil {
		log.Printf("node_handler: regenerate: acquire server conn: %v", err)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	ok2, err := h.treeRepo.TransitionNodeStatus(r.Context(), serverConn, nodeID, node.Status, NodeStatusGenerating)
	serverConn.Release()
	if err != nil {
		log.Printf("node_handler: regenerate: transition: %v", err)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if !ok2 {
		writeAgentError(w, http.StatusConflict, "NODE_NOT_REGENERABLE", "node is not in rejected or failed status")
		return
	}

	run, err := h.agentRepo.CreateRun(r.Context(), courseID, "node_generation")
	if err != nil {
		log.Printf("node_handler: regenerate: create run: %v", err)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	if emitErr := h.agentRepo.EmitNodeEvent(r.Context(), run.ID, "node_generating", NodeEventPayload{
		NodeID:   &nodeID,
		NodeType: string(node.NodeType),
		CourseID: &courseID,
	}); emitErr != nil {
		log.Printf("node_handler: regenerate: emit node_generating: %v", emitErr)
	}

	var updated CourseNode
	if rConn, rOk := auth.ConnFromContext(r.Context()); rOk {
		updated, _ = h.treeRepo.GetNode(r.Context(), rConn, nodeID)
	}

	meta, _ := h.layeredRunner.loadCourseMeta(r.Context(), courseID)

	go h.runBackgroundGenerate(context.Background(), run.ID, nodeID, courseID, studentID, meta, node.NodeType)

	writeAgentJSON(w, http.StatusAccepted, map[string]interface{}{
		"node":   toNodeResponse(updated),
		"run_id": run.ID.String(),
	})
}

// nodeChatHistory handles GET /courses/{id}/nodes/{nodeId}/chat
// Returns all node_chats for the node in ascending created_at order.
// Uses the request-scoped connection (RLS read-own).
//
// @{"req": ["REQ-AGENT-034", "REQ-AGENT-053", "REQ-SYS-073", "REQ-SYS-075"]}
func (h *NodeHandler) nodeChatHistory(w http.ResponseWriter, r *http.Request) {
	studentID, ok := requestStudent(w, r)
	if !ok {
		return
	}
	courseID, ok := parseCourseID(r)
	if !ok {
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}
	nodeID, ok := parseNodeID(r)
	if !ok {
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid node id")
		return
	}
	if !h.courseOwnedBy(r.Context(), courseID, studentID) {
		writeAgentError(w, http.StatusNotFound, "NOT_FOUND", "course not found")
		return
	}

	_, ok = h.checkNodeCourseOwnership(r.Context(), w, nodeID, courseID)
	if !ok {
		return
	}

	// Read via request-scoped connection (RLS read-own).
	conn, ok := auth.ConnFromContext(r.Context())
	if !ok {
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	chats, err := h.treeRepo.ListNodeChats(r.Context(), conn, nodeID)
	if err != nil {
		log.Printf("node_handler: chat history node=%s: %v", nodeID, err)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	msgs := make([]nodeChatResponse, 0, len(chats))
	for _, c := range chats {
		msgs = append(msgs, nodeChatResponse{
			ID:        c.ID.String(),
			Role:      c.Role,
			Content:   c.Content,
			CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	writeAgentJSON(w, http.StatusOK, map[string]interface{}{"messages": msgs})
}

// expandLayer handles POST /courses/{id}/layers/{layer}/expand
// Delegates to LayeredRunner.ExpandToNextLayer and maps errors to HTTP codes.
//
// @{"req": ["REQ-AGENT-039", "REQ-AGENT-040", "REQ-SYS-073", "REQ-SYS-074"]}
func (h *NodeHandler) expandLayer(w http.ResponseWriter, r *http.Request) {
	studentID, ok := requestStudent(w, r)
	if !ok {
		return
	}
	courseID, ok := parseCourseID(r)
	if !ok {
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}
	if !h.courseOwnedBy(r.Context(), courseID, studentID) {
		writeAgentError(w, http.StatusNotFound, "NOT_FOUND", "course not found")
		return
	}

	layerStr := chi.URLParam(r, "layer")
	if layerStr == "" {
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "layer is required")
		return
	}

	// Validate layer value against the node_type enum.
	currentLayer := NodeType(layerStr)
	switch currentLayer {
	case NodeTypeRoot, NodeTypeSyllabus, NodeTypeSectionGoal, NodeTypeConcept, NodeTypeContent:
		// valid
	default:
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid layer; must be one of root, syllabus, section_goal, concept, content")
		return
	}

	// Count nodes inserted to return in the response (before expand so we can
	// diff; simpler to count after the expand below).
	err := h.layeredRunner.ExpandToNextLayer(r.Context(), courseID, currentLayer)
	if err != nil {
		switch {
		case errors.Is(err, ErrTreeComplete):
			writeAgentError(w, http.StatusConflict, "TREE_ALREADY_COMPLETE", "the tree is already complete; no further layers can be generated")
		case errors.Is(err, ErrLayerExpandConflict):
			writeAgentError(w, http.StatusConflict, "LAYER_NOT_FULLY_APPROVED", "not all nodes in the current layer are approved")
		default:
			log.Printf("node_handler: expand layer course=%s layer=%s: %v", courseID, layerStr, err)
			writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		}
		return
	}

	// Determine the next layer name and count newly-inserted nodes (server pool).
	nextLayer := nextLayer(currentLayer)

	serverConn2, err := db.AcquireServerConn(r.Context(), h.pool)
	if err != nil {
		log.Printf("node_handler: expand layer: acquire conn for count: %v", err)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	nextLayerType := nextLayer
	nodes, err := h.treeRepo.ListNodesByCourse(r.Context(), serverConn2, courseID, ListNodeOptions{
		NodeType: &nextLayerType,
	})
	serverConn2.Release()
	if err != nil {
		log.Printf("node_handler: expand layer: count next layer nodes: %v", err)
	}

	writeAgentJSON(w, http.StatusOK, map[string]interface{}{
		"next_layer":           string(nextLayer),
		"inserted_node_count":  len(nodes),
	})
}

// --------------------------------------------------------------------------
// courseOwnedBy — mirrors the pattern in AgentHandler.courseOwnedBy (handler.go:674).
// Uses the request-scoped conn (carries app.current_user_id for RLS).
// --------------------------------------------------------------------------

// courseOwnedBy returns true when the given course belongs to studentID.
// Uses the auth middleware's request-scoped connection (which already has
// app.current_user_id set) so the courses_student_policy RLS check passes.
// Returns false on any error (including pgx.ErrNoRows) to default to deny.
//
// @{"req": ["REQ-AGENT-032", "REQ-AGENT-033", "REQ-SYS-073"]}
func (h *NodeHandler) courseOwnedBy(ctx context.Context, courseID, studentID uuid.UUID) bool {
	conn, ok := auth.ConnFromContext(ctx)
	if !ok {
		return false
	}
	var exists int
	err := conn.QueryRow(ctx,
		`SELECT 1 FROM courses WHERE id = $1 AND student_id = $2`,
		courseID, studentID,
	).Scan(&exists)
	return err == nil
}
