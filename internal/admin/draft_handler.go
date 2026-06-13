// draft_handler.go — admin-authoring HITL HTTP/SSE endpoints for the knowledge-tree.
//
// Implements all 13 admin endpoints defined in knowledge-tree-api-sse.md §5.1 and
// §5.3.1, mounted under /api/v1/admin/drafts with auth.RequireRole("admin") + CSRF
// (applied by the caller in main.go).
//
// Security invariants:
//   - Admin ownership checked on every mutating endpoint via the request-scoped
//     connection (which carries app.current_user_id + app.current_role='admin').
//     Returns 403 when the draft belongs to another admin, 404 when not found.
//   - draft_nodes writes run on the server pool (D12) after the ownership read.
//   - generate/refine/regenerate use atomic TransitionDraftNodeStatus (D12/§10.4).
//   - Background goroutines use context.Background() (api-sse §6.4).
//   - CSRF is applied by the enclosing router group; GET endpoints are exempt.
//
// @{"req": ["REQ-SYS-077", "REQ-SYS-074", "REQ-AGENT-047", "REQ-AGENT-048",
//           "REQ-AGENT-049", "REQ-AGENT-050", "REQ-AGENT-051", "REQ-AGENT-052",
//           "REQ-AGENT-053", "REQ-AGENT-054", "REQ-AGENT-055", "REQ-AGENT-056",
//           "REQ-AGENT-057", "REQ-AGENT-058", "REQ-AGENT-060"]}
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valory/valory/internal/agent"
	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/db"
)

// draftNodeGenerator abstracts the Chair methods consumed by DraftHandler.
// Defined on the consumer side to keep the admin package decoupled from agent.
//
// @{"req": ["REQ-AGENT-047", "REQ-AGENT-060"]}
type draftNodeGenerator interface {
	GenerateNode(ctx context.Context, actorID, contextID uuid.UUID, isDraftContext bool, nodeType, topic, level string, parameters json.RawMessage, priorContext []agent.NodeChatMessage) (json.RawMessage, error)
	RefineNode(ctx context.Context, actorID, contextID uuid.UUID, isDraftContext bool, nodeType string, existingPayload json.RawMessage, chatHistory []agent.NodeChatMessage) (json.RawMessage, error)
}

// draftRunRepository abstracts the AgentRepository methods consumed by DraftHandler.
//
// @{"req": ["REQ-AGENT-056", "REQ-AGENT-036"]}
type draftRunRepository interface {
	CreateRunForDraft(ctx context.Context, draftID uuid.UUID, runType string) (uuid.UUID, error)
	EmitNodeEvent(ctx context.Context, runID uuid.UUID, eventType string, payload agent.NodeEventPayload) error
	SetRunStatus(ctx context.Context, runID uuid.UUID, status string, errMsg *string) error
	GetEventsAfterForDraft(ctx context.Context, draftID uuid.UUID, afterEventID *uuid.UUID, limit int) ([]agent.PipelineEventRow, error)
}

// DraftHandler serves the admin-authoring HITL surface for knowledge-tree drafts.
// All endpoints require RequireRole("admin") + CSRF, applied by the caller.
//
// @{"req": ["REQ-SYS-077", "REQ-SYS-074", "REQ-AGENT-047", "REQ-AGENT-048",
//           "REQ-AGENT-049", "REQ-AGENT-050", "REQ-AGENT-051", "REQ-AGENT-052",
//           "REQ-AGENT-053", "REQ-AGENT-054", "REQ-AGENT-055", "REQ-AGENT-056",
//           "REQ-AGENT-057", "REQ-AGENT-058", "REQ-AGENT-060"]}
type DraftHandler struct {
	// serverPool is the server-role pool used for all draft_nodes writes (D12).
	serverPool *pgxpool.Pool
	draftRepo  *DraftRepository
	chair      draftNodeGenerator
	agentRepo  draftRunRepository
}

// NewDraftHandler constructs a DraftHandler.
//
// @{"req": ["REQ-SYS-077", "REQ-AGENT-047", "REQ-AGENT-056"]}
func NewDraftHandler(
	serverPool *pgxpool.Pool,
	draftRepo *DraftRepository,
	chair draftNodeGenerator,
	agentRepo draftRunRepository,
) *DraftHandler {
	return &DraftHandler{
		serverPool: serverPool,
		draftRepo:  draftRepo,
		chair:      chair,
		agentRepo:  agentRepo,
	}
}

// Routes registers all admin draft endpoints on the provided router.
// The caller must apply RequireRole("admin") and CSRFMiddleware before mounting.
// GET endpoints (list, get, chat, events) are CSRF-exempt by the middleware's
// safe-method check.
//
// @{"req": ["REQ-SYS-077", "REQ-SYS-074", "REQ-AGENT-047", "REQ-AGENT-048",
//           "REQ-AGENT-049", "REQ-AGENT-050", "REQ-AGENT-051", "REQ-AGENT-052",
//           "REQ-AGENT-053", "REQ-AGENT-054", "REQ-AGENT-055", "REQ-AGENT-057",
//           "REQ-AGENT-058", "REQ-AGENT-060"]}
func (h *DraftHandler) Routes(r chi.Router) {
	// Draft CRUD.
	r.Post("/", h.createDraft)
	r.Get("/", h.listDrafts)
	r.Get("/{draftId}", h.getDraft)
	r.Delete("/{draftId}", h.deleteDraft)

	// Node generation/review actions.
	r.Post("/{draftId}/nodes/generate", h.generateNode)
	r.Post("/{draftId}/nodes/{nodeId}/refine", h.refineNode)
	r.Patch("/{draftId}/nodes/{nodeId}/approve", h.approveNode)
	r.Patch("/{draftId}/nodes/{nodeId}/feedback", h.feedbackNode)
	r.Post("/{draftId}/nodes/{nodeId}/regenerate", h.regenerateNode)

	// Chat history (read-only).
	r.Get("/{draftId}/nodes/{nodeId}/chat", h.nodeChatHistory)

	// Layer expansion.
	r.Post("/{draftId}/layers/{layer}/expand", h.expandLayer)

	// Publish.
	r.Post("/{draftId}/publish", h.publishDraft)

	// SSE stream (GET — CSRF exempt).
	r.Get("/{draftId}/events", h.streamEvents)
}

// --------------------------------------------------------------------------
// Wire shapes
// --------------------------------------------------------------------------

// draftResponse is the JSON shape returned for a single course_drafts row.
type draftResponse struct {
	ID                      string          `json:"id"`
	AdminID                 string          `json:"admin_id"`
	Title                   string          `json:"title"`
	Topic                   string          `json:"topic"`
	Level                   string          `json:"level"`
	Parameters              json.RawMessage `json:"parameters"`
	Status                  string          `json:"status"`
	CurrentLayer            *string         `json:"current_layer,omitempty"`
	PublishedToAssignmentID *string         `json:"published_to_assignment_id,omitempty"`
	CreatedAt               string          `json:"created_at"`
	UpdatedAt               string          `json:"updated_at"`
}

// draftNodeResponse is the JSON shape returned for a single draft_nodes row.
type draftNodeResponse struct {
	ID        string          `json:"id"`
	DraftID   string          `json:"draft_id"`
	ParentID  *string         `json:"parent_id"`
	NodeType  string          `json:"node_type"`
	Ordering  int             `json:"ordering"`
	Status    string          `json:"status"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

// toDraftResponse converts a DraftRow to its wire representation.
//
// @{"req": ["REQ-AGENT-047", "REQ-AGENT-048"]}
func toDraftResponse(d DraftRow) draftResponse {
	resp := draftResponse{
		ID:           d.ID.String(),
		AdminID:      d.AdminID.String(),
		Title:        d.Title,
		Topic:        d.Topic,
		Level:        d.Level,
		Parameters:   d.Parameters,
		Status:       d.Status,
		CurrentLayer: d.CurrentLayer,
		CreatedAt:    d.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:    d.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if d.PublishedToAssignmentID != nil {
		s := d.PublishedToAssignmentID.String()
		resp.PublishedToAssignmentID = &s
	}
	if len(resp.Parameters) == 0 {
		resp.Parameters = json.RawMessage("{}")
	}
	return resp
}

// toDraftNodeResponse converts a DraftNodeRow to its wire representation.
//
// @{"req": ["REQ-AGENT-047", "REQ-AGENT-049"]}
func toDraftNodeResponse(n DraftNodeRow) draftNodeResponse {
	resp := draftNodeResponse{
		ID:        n.ID.String(),
		DraftID:   n.DraftID.String(),
		NodeType:  n.NodeType,
		Ordering:  n.Ordering,
		Status:    n.Status,
		Payload:   n.Payload,
		CreatedAt: n.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: n.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if n.ParentID != nil {
		s := n.ParentID.String()
		resp.ParentID = &s
	}
	if len(resp.Payload) == 0 {
		resp.Payload = json.RawMessage("{}")
	}
	return resp
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

// requestAdmin extracts the authenticated admin's UUID from the request context.
// Writes an error response and returns false on failure.
//
// @{"req": ["REQ-SYS-077", "REQ-AGENT-047"]}
func requestAdmin(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeDraftError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return uuid.UUID{}, false
	}
	return uuid.UUID(rawID), true
}

// parseDraftID extracts and parses the {draftId} URL parameter.
//
// @{"req": ["REQ-AGENT-047", "REQ-AGENT-048"]}
func parseDraftID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "draftId"))
	if err != nil {
		writeDraftError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid draft id")
		return uuid.UUID{}, false
	}
	return id, true
}

// parseDraftNodeID extracts and parses the {nodeId} URL parameter.
//
// @{"req": ["REQ-AGENT-049", "REQ-AGENT-050", "REQ-AGENT-055"]}
func parseDraftNodeID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "nodeId"))
	if err != nil {
		writeDraftError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid node id")
		return uuid.UUID{}, false
	}
	return id, true
}

// checkDraftOwnership fetches the draft using the request-scoped connection
// (carries app.current_user_id + app.current_role='admin' for RLS). Returns
// the DraftRow on success. Per api-sse §8.6:
//   - 404 when the row does not exist.
//   - 403 when the row exists but belongs to another admin (adminID mismatch).
//
// @{"req": ["REQ-SYS-077", "REQ-AGENT-047", "REQ-AGENT-048"]}
func (h *DraftHandler) checkDraftOwnership(w http.ResponseWriter, r *http.Request, draftID, adminID uuid.UUID) (DraftRow, bool) {
	// Read via the request-scoped conn (course_drafts_admin_own RLS policy fires).
	// The policy already gates on admin_id=current_user_id AND role='admin', so
	// a foreign admin's draft returns no rows. We do an explicit adminID check
	// after the read to distinguish 403 (other admin) from 404 (not found).
	draft, err := h.draftRepo.GetDraft(r.Context(), nil, draftID)
	if err != nil {
		if errors.Is(err, ErrDraftNotFound) {
			writeDraftError(w, http.StatusNotFound, "NOT_FOUND", "draft not found")
		} else {
			log.Printf("draft_handler: checkDraftOwnership draft=%s: %v", draftID, err)
			writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		}
		return DraftRow{}, false
	}
	if draft.AdminID != adminID {
		// The draft exists but belongs to another admin.
		writeDraftError(w, http.StatusForbidden, "FORBIDDEN", "you do not own this draft")
		return DraftRow{}, false
	}
	return draft, true
}

// checkNodeBelongsToDraft fetches the draft_node and verifies it belongs to
// draftID. Uses the request-scoped connection for the read (draft_nodes_admin_own
// RLS policy). Returns 404 if not found or if the node belongs to a different draft.
//
// @{"req": ["REQ-SYS-077", "REQ-AGENT-049", "REQ-AGENT-055"]}
func (h *DraftHandler) checkNodeBelongsToDraft(w http.ResponseWriter, r *http.Request, nodeID, draftID uuid.UUID) (DraftNodeRow, bool) {
	node, err := h.draftRepo.GetDraftNode(r.Context(), nil, nodeID)
	if err != nil {
		if errors.Is(err, ErrDraftNodeNotFound) {
			writeDraftError(w, http.StatusNotFound, "NOT_FOUND", "node not found")
		} else {
			log.Printf("draft_handler: checkNodeBelongsToDraft node=%s: %v", nodeID, err)
			writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		}
		return DraftNodeRow{}, false
	}
	if node.DraftID != draftID {
		// Node exists but belongs to a different draft — return 404 to avoid
		// leaking cross-draft existence.
		writeDraftError(w, http.StatusNotFound, "NOT_FOUND", "node not found")
		return DraftNodeRow{}, false
	}
	return node, true
}

// writeDraftError writes the standard error envelope (api-sse §5 error shape).
//
// @{"req": ["REQ-SYS-077"]}
func writeDraftError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": code, "message": message}) //nolint:errcheck
}

// writeDraftJSON writes a JSON response with the given status code.
//
// @{"req": ["REQ-SYS-077"]}
func writeDraftJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

// validNodeType returns true when s is one of the four directly-creatable
// node types (root is implicit and not allowed via the API).
//
// @{"req": ["REQ-AGENT-044", "REQ-AGENT-060"]}
func validNodeType(s string) bool {
	switch s {
	case "syllabus", "section_goal", "concept", "content":
		return true
	}
	return false
}

// validLayer returns true when s is one of the five node_type_enum values
// (used for layer-expand validation).
//
// @{"req": ["REQ-AGENT-039", "REQ-SYS-074"]}
func validLayer(s string) bool {
	switch s {
	case "root", "syllabus", "section_goal", "concept", "content":
		return true
	}
	return false
}

// isContentLayer returns true when the given layer is the final (content) layer.
// The expand endpoint returns 409 TREE_ALREADY_COMPLETE when the current layer is
// 'content' (no further layers can be generated).
//
// @{"req": ["REQ-AGENT-039", "REQ-SYS-074"]}
func isContentLayer(layer string) bool {
	return layer == "content"
}

// nextDraftLayer returns the layer that follows currentLayer in the generation
// sequence. Returns "" when currentLayer is "content" (tree complete).
//
// @{"req": ["REQ-AGENT-039", "REQ-SYS-074"]}
func nextDraftLayer(current string) string {
	seq := []string{"root", "syllabus", "section_goal", "concept", "content"}
	for i, l := range seq {
		if l == current && i+1 < len(seq) {
			return seq[i+1]
		}
	}
	return ""
}

// convertAdminChats converts admin NodeChatMessage rows to the agent.NodeChatMessage
// slice expected by Chair.GenerateNode/RefineNode.
//
// @{"req": ["REQ-AGENT-051", "REQ-AGENT-060"]}
func convertAdminChats(in []NodeChatMessage) []agent.NodeChatMessage {
	out := make([]agent.NodeChatMessage, 0, len(in))
	for _, m := range in {
		out = append(out, agent.NodeChatMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}
	return out
}

// mergeFeedbackIntoDraftPayload merges the feedback string into the existing JSONB
// payload under the "feedback" key, returning a new JSON object.
//
// @{"req": ["REQ-AGENT-051", "REQ-AGENT-057"]}
func mergeFeedbackIntoDraftPayload(existing json.RawMessage, feedback string) (json.RawMessage, error) {
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
		return nil, fmt.Errorf("merge feedback into draft payload: %w", err)
	}
	return json.RawMessage(b), nil
}

// --------------------------------------------------------------------------
// Handlers
// --------------------------------------------------------------------------

// createDraft handles POST /api/v1/admin/drafts (api-sse §5.1.1).
// Tx-wrapped: inserts course_drafts + implicit root draft_node atomically (D13).
//
// @{"req": ["REQ-SYS-077", "REQ-AGENT-047", "REQ-AGENT-048", "REQ-AGENT-044"]}
func (h *DraftHandler) createDraft(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)

	adminID, ok := requestAdmin(w, r)
	if !ok {
		return
	}

	var req struct {
		Title      string          `json:"title"`
		Topic      string          `json:"topic"`
		Level      string          `json:"level"`
		Parameters json.RawMessage `json:"parameters"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDraftError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}

	req.Title = strings.TrimSpace(req.Title)
	req.Topic = strings.TrimSpace(req.Topic)
	if req.Title == "" {
		writeDraftError(w, http.StatusBadRequest, "BAD_REQUEST", "title is required")
		return
	}
	if req.Topic == "" {
		writeDraftError(w, http.StatusBadRequest, "BAD_REQUEST", "topic is required")
		return
	}
	if req.Level == "" {
		req.Level = "beginner"
	}
	switch req.Level {
	case "beginner", "intermediate", "advanced":
	default:
		writeDraftError(w, http.StatusBadRequest, "BAD_REQUEST", "level must be one of: beginner, intermediate, advanced")
		return
	}
	if len(req.Parameters) > 0 && !isJSONObject(req.Parameters) {
		writeDraftError(w, http.StatusBadRequest, "BAD_REQUEST", "parameters must be a JSON object")
		return
	}
	if len(req.Parameters) == 0 {
		req.Parameters = json.RawMessage("{}")
	}

	// Tx-wrap the draft+root insert (D13): use a transaction so the course_drafts
	// and draft_nodes rows are committed atomically. Both writes use the request-
	// scoped conn (conn(ctx) inside CreateDraft picks it up).
	conn, ok2 := auth.ConnFromContext(r.Context())
	if !ok2 {
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	tx, err := conn.Begin(r.Context())
	if err != nil {
		log.Printf("draft_handler: createDraft: begin tx: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck

	draftID, err := h.draftRepo.CreateDraft(r.Context(), tx, adminID, req.Title, req.Topic, req.Level, req.Parameters)
	if err != nil {
		log.Printf("draft_handler: createDraft: create: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		log.Printf("draft_handler: createDraft: commit: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	// Fetch the newly-created draft for the response (re-read via request-scoped conn).
	draft, err := h.draftRepo.GetDraft(r.Context(), nil, draftID)
	if err != nil {
		log.Printf("draft_handler: createDraft: get after create: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	writeDraftJSON(w, http.StatusCreated, map[string]interface{}{
		"draft": toDraftResponse(draft),
	})
}

// listDrafts handles GET /api/v1/admin/drafts (api-sse §5.1.2).
// Supports optional ?status, ?limit, ?cursor query parameters.
//
// @{"req": ["REQ-SYS-077", "REQ-AGENT-047", "REQ-AGENT-048"]}
func (h *DraftHandler) listDrafts(w http.ResponseWriter, r *http.Request) {
	adminID, ok := requestAdmin(w, r)
	if !ok {
		return
	}

	opts := DraftListOpts{
		StatusFilter: r.URL.Query().Get("status"),
		Cursor:       r.URL.Query().Get("cursor"),
	}
	limit := 20
	if ls := r.URL.Query().Get("limit"); ls != "" {
		if n, err := strconv.Atoi(ls); err == nil && n >= 1 && n <= 100 {
			limit = n
		}
	}
	opts.Limit = limit

	drafts, nextCursor, err := h.draftRepo.ListDrafts(r.Context(), nil, adminID, opts)
	if err != nil {
		log.Printf("draft_handler: listDrafts admin=%s: %v", adminID, err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	items := make([]map[string]interface{}, 0, len(drafts))
	for _, d := range drafts {
		items = append(items, map[string]interface{}{
			"id":         d.ID.String(),
			"title":      d.Title,
			"topic":      d.Topic,
			"level":      d.Level,
			"status":     d.Status,
			"created_at": d.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}

	writeDraftJSON(w, http.StatusOK, map[string]interface{}{
		"drafts":      items,
		"next_cursor": nextCursor,
	})
}

// getDraft handles GET /api/v1/admin/drafts/{draftId} (api-sse §5.1.3).
// Returns the draft + all its nodes.
//
// @{"req": ["REQ-SYS-077", "REQ-AGENT-047", "REQ-AGENT-048", "REQ-AGENT-058"]}
func (h *DraftHandler) getDraft(w http.ResponseWriter, r *http.Request) {
	adminID, ok := requestAdmin(w, r)
	if !ok {
		return
	}
	draftID, ok := parseDraftID(w, r)
	if !ok {
		return
	}

	draft, ok := h.checkDraftOwnership(w, r, draftID, adminID)
	if !ok {
		return
	}

	nodes, err := h.draftRepo.ListDraftNodes(r.Context(), nil, draftID, DraftNodeListOpts{})
	if err != nil {
		log.Printf("draft_handler: getDraft draft=%s: list nodes: %v", draftID, err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	nodeItems := make([]draftNodeResponse, 0, len(nodes))
	for _, n := range nodes {
		nodeItems = append(nodeItems, toDraftNodeResponse(n))
	}

	writeDraftJSON(w, http.StatusOK, map[string]interface{}{
		"draft": toDraftResponse(draft),
		"nodes": nodeItems,
	})
}

// deleteDraft handles DELETE /api/v1/admin/drafts/{draftId} (api-sse §5.1.4).
// Only drafts with status='draft' can be deleted; cascades to draft_nodes/node_chats.
//
// @{"req": ["REQ-SYS-077", "REQ-AGENT-047", "REQ-AGENT-057"]}
func (h *DraftHandler) deleteDraft(w http.ResponseWriter, r *http.Request) {
	adminID, ok := requestAdmin(w, r)
	if !ok {
		return
	}
	draftID, ok := parseDraftID(w, r)
	if !ok {
		return
	}

	// Ownership check via request-scoped conn (RLS admin_own).
	_, ok = h.checkDraftOwnership(w, r, draftID, adminID)
	if !ok {
		return
	}

	if err := h.draftRepo.DeleteDraft(r.Context(), nil, draftID); err != nil {
		if errors.Is(err, ErrDraftNotFound) {
			writeDraftError(w, http.StatusNotFound, "NOT_FOUND", "draft not found")
			return
		}
		if errors.Is(err, ErrDraftNotDeletable) {
			writeDraftError(w, http.StatusConflict, "DRAFT_ALREADY_PUBLISHED", "draft has already been published and cannot be deleted")
			return
		}
		log.Printf("draft_handler: deleteDraft draft=%s: %v", draftID, err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// generateNode handles POST /api/v1/admin/drafts/{draftId}/nodes/generate (api-sse §5.1.5).
// Creates a pending draft_node, transitions it to generating via atomic CAS, then
// dispatches Chair.GenerateNode(isDraftContext=true) in a background goroutine (D11).
//
// @{"req": ["REQ-SYS-077", "REQ-AGENT-044", "REQ-AGENT-047", "REQ-AGENT-048",
//           "REQ-AGENT-049", "REQ-AGENT-055", "REQ-AGENT-056", "REQ-AGENT-060"]}
func (h *DraftHandler) generateNode(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)

	adminID, ok := requestAdmin(w, r)
	if !ok {
		return
	}
	draftID, ok := parseDraftID(w, r)
	if !ok {
		return
	}

	draft, ok := h.checkDraftOwnership(w, r, draftID, adminID)
	if !ok {
		return
	}

	var req struct {
		NodeType   string          `json:"node_type"`
		ParentID   *string         `json:"parent_id"`
		Parameters json.RawMessage `json:"parameters"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDraftError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if !validNodeType(req.NodeType) {
		writeDraftError(w, http.StatusBadRequest, "BAD_REQUEST", "node_type must be one of: syllabus, section_goal, concept, content")
		return
	}
	if len(req.Parameters) == 0 {
		req.Parameters = json.RawMessage("{}")
	}

	var parentID *uuid.UUID
	if req.ParentID != nil {
		pid, err := uuid.Parse(*req.ParentID)
		if err != nil {
			writeDraftError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid parent_id")
			return
		}
		parentID = &pid
	}

	// Insert the new draft node via the server pool (D12 — server-pool write
	// after ownership confirmed above).
	serverConn, err := db.AcquireServerConn(r.Context(), h.serverPool)
	if err != nil {
		log.Printf("draft_handler: generateNode: acquire server conn: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	node, err := h.draftRepo.CreateDraftNode(r.Context(), serverConn, draftID, parentID, req.NodeType, 0, nil)
	serverConn.Release()
	if err != nil {
		log.Printf("draft_handler: generateNode: create node: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	// Atomic CAS: pending → generating (concurrency guard §10.4).
	transConn, err := db.AcquireServerConn(r.Context(), h.serverPool)
	if err != nil {
		log.Printf("draft_handler: generateNode: acquire conn for transition: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	ok2, err := h.draftRepo.TransitionDraftNodeStatus(r.Context(), transConn, node.ID, "pending", "generating")
	transConn.Release()
	if err != nil {
		log.Printf("draft_handler: generateNode: transition: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if !ok2 {
		writeDraftError(w, http.StatusConflict, "NODE_ALREADY_GENERATING", "node generation already in progress")
		return
	}

	// Create agent_run for SSE event anchoring (draft-anchored, not course-anchored).
	runID, err := h.agentRepo.CreateRunForDraft(r.Context(), draftID, "node_generation")
	if err != nil {
		log.Printf("draft_handler: generateNode: create run: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	draftIDCopy := draftID
	nodeIDCopy := node.ID
	if emitErr := h.agentRepo.EmitNodeEvent(r.Context(), runID, "node_generating", agent.NodeEventPayload{
		NodeID:   &nodeIDCopy,
		NodeType: req.NodeType,
		DraftID:  &draftIDCopy,
	}); emitErr != nil {
		log.Printf("draft_handler: generateNode: emit node_generating: %v", emitErr)
	}

	// Re-fetch the node via request-scoped conn so the response shows the
	// 'generating' status (the node was just transitioned via server pool).
	updatedNode, fetchErr := h.draftRepo.GetDraftNode(r.Context(), nil, node.ID)
	if fetchErr != nil {
		// Non-fatal: return the originally-created node shape with status from the
		// transition (just flip to generating for the response).
		updatedNode = node
		updatedNode.Status = "generating"
	}

	go h.runBackgroundGenerate(context.Background(), runID, updatedNode.ID, draftID, adminID, draft.Topic, draft.Level, req.Parameters, req.NodeType)

	writeDraftJSON(w, http.StatusAccepted, map[string]interface{}{
		"node":   toDraftNodeResponse(updatedNode),
		"run_id": runID.String(),
	})
}

// runBackgroundGenerate is the goroutine body for generate and regenerate.
// Calls Chair.GenerateNode(isDraftContext=true), writes the result, and emits
// node_ready / node_error. Uses context.Background() so HTTP conn close does not
// cancel generation (api-sse §6.4).
//
// @{"req": ["REQ-AGENT-035", "REQ-AGENT-036", "REQ-AGENT-044", "REQ-AGENT-056",
//           "REQ-AGENT-060", "REQ-SYS-077"]}
func (h *DraftHandler) runBackgroundGenerate(
	ctx context.Context,
	runID uuid.UUID,
	nodeID uuid.UUID,
	draftID uuid.UUID,
	adminID uuid.UUID,
	topic string,
	level string,
	parameters json.RawMessage,
	nodeType string,
) {
	// Load prior node_chats for context (hint / prior feedback) via server pool.
	var priorContext []agent.NodeChatMessage
	if chatConn, err := db.AcquireServerConn(ctx, h.serverPool); err == nil {
		msgs, _ := h.draftRepo.ListDraftNodeChats(ctx, chatConn, nodeID)
		chatConn.Release()
		priorContext = convertAdminChats(msgs)
	}

	newPayload, err := h.chair.GenerateNode(
		ctx,
		adminID,
		draftID,
		true, // isDraftContext
		nodeType,
		topic,
		level,
		parameters,
		priorContext,
	)
	if err != nil {
		log.Printf("draft_handler: background generate node %s: %v", nodeID, err)
		if fc, fErr := db.AcquireServerConn(ctx, h.serverPool); fErr == nil {
			if _, tErr := fc.Exec(ctx,
				`UPDATE draft_nodes SET status = 'failed', updated_at = now() WHERE id = $1`,
				nodeID,
			); tErr != nil {
				log.Printf("draft_handler: fail draft node %s: %v", nodeID, tErr)
			}
			fc.Release()
		}
		draftIDCopy := draftID
		nodeIDCopy := nodeID
		if emitErr := h.agentRepo.EmitNodeEvent(ctx, runID, "node_error", agent.NodeEventPayload{
			NodeID:  &nodeIDCopy,
			DraftID: &draftIDCopy,
			Error:   fmt.Sprintf("generation failed: %v", err),
		}); emitErr != nil {
			log.Printf("draft_handler: emit node_error %s: %v", nodeID, emitErr)
		}
		if setErr := h.agentRepo.SetRunStatus(ctx, runID, "failed", nil); setErr != nil {
			log.Printf("draft_handler: set run failed %s: %v", runID, setErr)
		}
		return
	}

	// Persist the payload (atomically transitions to awaiting_review via SetDraftNodePayload).
	if writeConn, writeErr := db.AcquireServerConn(ctx, h.serverPool); writeErr == nil {
		if setErr := h.draftRepo.SetDraftNodePayload(ctx, writeConn, nodeID, newPayload); setErr != nil {
			log.Printf("draft_handler: background generate: set payload node %s: %v", nodeID, setErr)
		}
		writeConn.Release()
	}

	// Append the assistant turn to node_chats so future refinements see history.
	if chatConn2, err2 := db.AcquireServerConn(ctx, h.serverPool); err2 == nil {
		if _, appendErr := h.draftRepo.AppendDraftNodeChat(ctx, chatConn2, nodeID, "assistant", string(newPayload)); appendErr != nil {
			log.Printf("draft_handler: background generate: append assistant chat %s: %v", nodeID, appendErr)
		}
		chatConn2.Release()
	}

	summary := string(newPayload)
	if len(summary) > 200 {
		summary = summary[:200]
	}

	draftIDCopy := draftID
	nodeIDCopy := nodeID
	if emitErr := h.agentRepo.EmitNodeEvent(ctx, runID, "node_ready", agent.NodeEventPayload{
		NodeID:  &nodeIDCopy,
		DraftID: &draftIDCopy,
		Summary: summary,
	}); emitErr != nil {
		log.Printf("draft_handler: emit node_ready %s: %v", nodeID, emitErr)
	}
	if setErr := h.agentRepo.SetRunStatus(ctx, runID, "completed", nil); setErr != nil {
		log.Printf("draft_handler: set run completed %s: %v", runID, setErr)
	}
}

// refineNode handles POST /api/v1/admin/drafts/{draftId}/nodes/{nodeId}/refine (api-sse §5.1.6).
// Stores the required message in node_chats, transitions to generating, dispatches
// Chair.RefineNode in a background goroutine.
//
// @{"req": ["REQ-SYS-077", "REQ-AGENT-047", "REQ-AGENT-050", "REQ-AGENT-051",
//           "REQ-AGENT-055", "REQ-AGENT-056", "REQ-AGENT-060"]}
func (h *DraftHandler) refineNode(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)

	adminID, ok := requestAdmin(w, r)
	if !ok {
		return
	}
	draftID, ok := parseDraftID(w, r)
	if !ok {
		return
	}
	nodeID, ok := parseDraftNodeID(w, r)
	if !ok {
		return
	}

	draft, ok := h.checkDraftOwnership(w, r, draftID, adminID)
	if !ok {
		return
	}

	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDraftError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		writeDraftError(w, http.StatusBadRequest, "BAD_REQUEST", "message is required")
		return
	}

	node, ok := h.checkNodeBelongsToDraft(w, r, nodeID, draftID)
	if !ok {
		return
	}

	// Reject if already generating.
	if node.Status == "generating" {
		writeDraftError(w, http.StatusConflict, "NODE_ALREADY_GENERATING", "node generation already in progress")
		return
	}

	// Persist the user message BEFORE the transition so the Chair sees it
	// in the chat history when building the refinement prompt (REQ-AGENT-051).
	chatConn, chatErr := db.AcquireServerConn(r.Context(), h.serverPool)
	if chatErr != nil {
		log.Printf("draft_handler: refineNode: acquire chat conn: %v", chatErr)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if _, appendErr := h.draftRepo.AppendDraftNodeChat(r.Context(), chatConn, nodeID, "user", req.Message); appendErr != nil {
		chatConn.Release()
		log.Printf("draft_handler: refineNode: append user chat: %v", appendErr)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	chatConn.Release()

	// Atomic CAS: current status → generating.
	transConn, err := db.AcquireServerConn(r.Context(), h.serverPool)
	if err != nil {
		log.Printf("draft_handler: refineNode: acquire transition conn: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	ok2, err := h.draftRepo.TransitionDraftNodeStatus(r.Context(), transConn, nodeID, node.Status, "generating")
	transConn.Release()
	if err != nil {
		log.Printf("draft_handler: refineNode: transition: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if !ok2 {
		writeDraftError(w, http.StatusConflict, "NODE_ALREADY_GENERATING", "node generation already in progress")
		return
	}

	runID, err := h.agentRepo.CreateRunForDraft(r.Context(), draftID, "node_generation")
	if err != nil {
		log.Printf("draft_handler: refineNode: create run: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	draftIDCopy := draftID
	nodeIDCopy := nodeID
	if emitErr := h.agentRepo.EmitNodeEvent(r.Context(), runID, "node_generating", agent.NodeEventPayload{
		NodeID:   &nodeIDCopy,
		NodeType: node.NodeType,
		DraftID:  &draftIDCopy,
	}); emitErr != nil {
		log.Printf("draft_handler: refineNode: emit node_generating: %v", emitErr)
	}

	// Re-fetch updated node for the response.
	updatedNode, fetchErr := h.draftRepo.GetDraftNode(r.Context(), nil, nodeID)
	if fetchErr != nil {
		updatedNode = node
		updatedNode.Status = "generating"
	}

	go h.runBackgroundRefine(context.Background(), runID, nodeID, draftID, adminID, draft.Topic, draft.Level, node.NodeType, node.Payload)

	writeDraftJSON(w, http.StatusAccepted, map[string]interface{}{
		"node":   toDraftNodeResponse(updatedNode),
		"run_id": runID.String(),
	})
}

// runBackgroundRefine is the goroutine body for refine.
//
// @{"req": ["REQ-AGENT-034", "REQ-AGENT-050", "REQ-AGENT-051", "REQ-AGENT-056",
//           "REQ-AGENT-060", "REQ-SYS-077"]}
func (h *DraftHandler) runBackgroundRefine(
	ctx context.Context,
	runID uuid.UUID,
	nodeID uuid.UUID,
	draftID uuid.UUID,
	adminID uuid.UUID,
	topic string,
	level string,
	nodeType string,
	existingPayload json.RawMessage,
) {
	// Load full chat history (includes the user message just appended).
	var chatHistory []agent.NodeChatMessage
	if chatConn, err := db.AcquireServerConn(ctx, h.serverPool); err == nil {
		msgs, _ := h.draftRepo.ListDraftNodeChats(ctx, chatConn, nodeID)
		chatConn.Release()
		chatHistory = convertAdminChats(msgs)
	}

	newPayload, err := h.chair.RefineNode(
		ctx,
		adminID,
		draftID,
		true,
		nodeType,
		existingPayload,
		chatHistory,
	)
	if err != nil {
		log.Printf("draft_handler: background refine node %s: %v", nodeID, err)
		if fc, fErr := db.AcquireServerConn(ctx, h.serverPool); fErr == nil {
			if _, tErr := fc.Exec(ctx,
				`UPDATE draft_nodes SET status = 'failed', updated_at = now() WHERE id = $1`,
				nodeID,
			); tErr != nil {
				log.Printf("draft_handler: background refine: fail node %s: %v", nodeID, tErr)
			}
			fc.Release()
		}
		draftIDCopy := draftID
		nodeIDCopy := nodeID
		if emitErr := h.agentRepo.EmitNodeEvent(ctx, runID, "node_error", agent.NodeEventPayload{
			NodeID:  &nodeIDCopy,
			DraftID: &draftIDCopy,
			Error:   fmt.Sprintf("refinement failed: %v", err),
		}); emitErr != nil {
			log.Printf("draft_handler: emit node_error refine %s: %v", nodeID, emitErr)
		}
		if setErr := h.agentRepo.SetRunStatus(ctx, runID, "failed", nil); setErr != nil {
			log.Printf("draft_handler: background refine: set run failed %s: %v", runID, setErr)
		}
		return
	}

	if writeConn, writeErr := db.AcquireServerConn(ctx, h.serverPool); writeErr == nil {
		if setErr := h.draftRepo.SetDraftNodePayload(ctx, writeConn, nodeID, newPayload); setErr != nil {
			log.Printf("draft_handler: background refine: set payload %s: %v", nodeID, setErr)
		}
		writeConn.Release()
	}

	if c2, e2 := db.AcquireServerConn(ctx, h.serverPool); e2 == nil {
		if _, appendErr := h.draftRepo.AppendDraftNodeChat(ctx, c2, nodeID, "assistant", string(newPayload)); appendErr != nil {
			log.Printf("draft_handler: background refine: append assistant chat %s: %v", nodeID, appendErr)
		}
		c2.Release()
	}

	summary := string(newPayload)
	if len(summary) > 200 {
		summary = summary[:200]
	}
	draftIDCopy := draftID
	nodeIDCopy := nodeID
	if emitErr := h.agentRepo.EmitNodeEvent(ctx, runID, "node_ready", agent.NodeEventPayload{
		NodeID:  &nodeIDCopy,
		DraftID: &draftIDCopy,
		Summary: summary,
	}); emitErr != nil {
		log.Printf("draft_handler: emit node_ready refine %s: %v", nodeID, emitErr)
	}
	if setErr := h.agentRepo.SetRunStatus(ctx, runID, "completed", nil); setErr != nil {
		log.Printf("draft_handler: background refine: set run completed %s: %v", runID, setErr)
	}
}

// approveNode handles PATCH /api/v1/admin/drafts/{draftId}/nodes/{nodeId}/approve (api-sse §5.1.7).
// Transitions awaiting_review → approved.
//
// @{"req": ["REQ-SYS-077", "REQ-AGENT-047", "REQ-AGENT-049", "REQ-AGENT-055"]}
func (h *DraftHandler) approveNode(w http.ResponseWriter, r *http.Request) {
	adminID, ok := requestAdmin(w, r)
	if !ok {
		return
	}
	draftID, ok := parseDraftID(w, r)
	if !ok {
		return
	}
	nodeID, ok := parseDraftNodeID(w, r)
	if !ok {
		return
	}

	_, ok = h.checkDraftOwnership(w, r, draftID, adminID)
	if !ok {
		return
	}

	_, ok = h.checkNodeBelongsToDraft(w, r, nodeID, draftID)
	if !ok {
		return
	}

	// Atomic CAS: awaiting_review → approved (D12 — server pool).
	serverConn, err := db.AcquireServerConn(r.Context(), h.serverPool)
	if err != nil {
		log.Printf("draft_handler: approveNode: acquire conn: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	ok2, err := h.draftRepo.TransitionDraftNodeStatus(r.Context(), serverConn, nodeID, "awaiting_review", "approved")
	serverConn.Release()
	if err != nil {
		log.Printf("draft_handler: approveNode: transition: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if !ok2 {
		writeDraftError(w, http.StatusConflict, "NODE_NOT_REVIEWABLE", "node is not awaiting review")
		return
	}

	// Re-fetch for the response.
	approved, err := h.draftRepo.GetDraftNode(r.Context(), nil, nodeID)
	if err != nil {
		log.Printf("draft_handler: approveNode: re-fetch: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	writeDraftJSON(w, http.StatusOK, map[string]interface{}{
		"node": toDraftNodeResponse(approved),
	})
}

// feedbackNode handles PATCH /api/v1/admin/drafts/{draftId}/nodes/{nodeId}/feedback (api-sse §5.1.8).
// The ONLY rejection path. Requires non-empty feedback, stores it in node_chats AND
// in payload.feedback, then transitions awaiting_review → rejected.
//
// @{"req": ["REQ-SYS-077", "REQ-AGENT-047", "REQ-AGENT-051", "REQ-AGENT-054", "REQ-AGENT-057"]}
func (h *DraftHandler) feedbackNode(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)

	adminID, ok := requestAdmin(w, r)
	if !ok {
		return
	}
	draftID, ok := parseDraftID(w, r)
	if !ok {
		return
	}
	nodeID, ok := parseDraftNodeID(w, r)
	if !ok {
		return
	}

	_, ok = h.checkDraftOwnership(w, r, draftID, adminID)
	if !ok {
		return
	}

	var req struct {
		Feedback string `json:"feedback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDraftError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if strings.TrimSpace(req.Feedback) == "" {
		writeDraftError(w, http.StatusBadRequest, "BAD_REQUEST", "feedback is required and must not be empty")
		return
	}

	node, ok := h.checkNodeBelongsToDraft(w, r, nodeID, draftID)
	if !ok {
		return
	}

	// Pre-check: only awaiting_review nodes can receive feedback.
	// Guard here BEFORE the chat INSERT so no orphaned chat rows are committed
	// when the node is in an incompatible status (mirrors refineNode's pattern).
	if node.Status != "awaiting_review" {
		writeDraftError(w, http.StatusConflict, "NODE_NOT_REVIEWABLE", "node is not awaiting review")
		return
	}

	// 1. Store feedback as a 'user' node_chats row (D12 — server pool).
	chatConn, chatErr := db.AcquireServerConn(r.Context(), h.serverPool)
	if chatErr != nil {
		log.Printf("draft_handler: feedbackNode: acquire chat conn: %v", chatErr)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if _, appendErr := h.draftRepo.AppendDraftNodeChat(r.Context(), chatConn, nodeID, "user", req.Feedback); appendErr != nil {
		chatConn.Release()
		log.Printf("draft_handler: feedbackNode: append chat: %v", appendErr)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	chatConn.Release()

	// 2. Merge feedback into payload.feedback.
	newPayload, mergeErr := mergeFeedbackIntoDraftPayload(node.Payload, req.Feedback)
	if mergeErr != nil {
		log.Printf("draft_handler: feedbackNode: merge payload: %v", mergeErr)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	// 3. Write merged payload + transition to rejected atomically (D12).
	serverConn, err := db.AcquireServerConn(r.Context(), h.serverPool)
	if err != nil {
		log.Printf("draft_handler: feedbackNode: acquire server conn: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	tag, err := serverConn.Exec(r.Context(),
		`UPDATE draft_nodes
		 SET status = 'rejected', payload = $1, updated_at = now()
		 WHERE id = $2 AND status = 'awaiting_review'`,
		[]byte(newPayload), nodeID,
	)
	serverConn.Release()
	if err != nil {
		log.Printf("draft_handler: feedbackNode: update: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if tag.RowsAffected() != 1 {
		writeDraftError(w, http.StatusConflict, "NODE_NOT_REVIEWABLE", "node is not awaiting review")
		return
	}

	// Re-fetch for the response.
	rejected, err := h.draftRepo.GetDraftNode(r.Context(), nil, nodeID)
	if err != nil {
		log.Printf("draft_handler: feedbackNode: re-fetch: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	writeDraftJSON(w, http.StatusOK, map[string]interface{}{
		"node": toDraftNodeResponse(rejected),
	})
}

// regenerateNode handles POST /api/v1/admin/drafts/{draftId}/nodes/{nodeId}/regenerate (api-sse §5.1.9).
// Transitions rejected|failed → generating, then dispatches Chair.GenerateNode.
//
// @{"req": ["REQ-SYS-077", "REQ-AGENT-047", "REQ-AGENT-052", "REQ-AGENT-055",
//           "REQ-AGENT-056", "REQ-AGENT-060"]}
func (h *DraftHandler) regenerateNode(w http.ResponseWriter, r *http.Request) {
	adminID, ok := requestAdmin(w, r)
	if !ok {
		return
	}
	draftID, ok := parseDraftID(w, r)
	if !ok {
		return
	}
	nodeID, ok := parseDraftNodeID(w, r)
	if !ok {
		return
	}

	draft, ok := h.checkDraftOwnership(w, r, draftID, adminID)
	if !ok {
		return
	}

	node, ok := h.checkNodeBelongsToDraft(w, r, nodeID, draftID)
	if !ok {
		return
	}

	// Only rejected or failed nodes can be regenerated.
	if node.Status != "rejected" && node.Status != "failed" {
		writeDraftError(w, http.StatusConflict, "NODE_NOT_REGENERABLE", "node is not in rejected or failed status")
		return
	}

	// Atomic CAS to generating.
	serverConn, err := db.AcquireServerConn(r.Context(), h.serverPool)
	if err != nil {
		log.Printf("draft_handler: regenerateNode: acquire conn: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	ok2, err := h.draftRepo.TransitionDraftNodeStatus(r.Context(), serverConn, nodeID, node.Status, "generating")
	serverConn.Release()
	if err != nil {
		log.Printf("draft_handler: regenerateNode: transition: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if !ok2 {
		writeDraftError(w, http.StatusConflict, "NODE_NOT_REGENERABLE", "node is not in rejected or failed status")
		return
	}

	runID, err := h.agentRepo.CreateRunForDraft(r.Context(), draftID, "node_generation")
	if err != nil {
		log.Printf("draft_handler: regenerateNode: create run: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	draftIDCopy := draftID
	nodeIDCopy := nodeID
	if emitErr := h.agentRepo.EmitNodeEvent(r.Context(), runID, "node_generating", agent.NodeEventPayload{
		NodeID:   &nodeIDCopy,
		NodeType: node.NodeType,
		DraftID:  &draftIDCopy,
	}); emitErr != nil {
		log.Printf("draft_handler: regenerateNode: emit node_generating: %v", emitErr)
	}

	// Re-fetch for the response.
	updated, fetchErr := h.draftRepo.GetDraftNode(r.Context(), nil, nodeID)
	if fetchErr != nil {
		updated = node
		updated.Status = "generating"
	}

	// Use payload from node (which includes payload.feedback for the re-prompt).
	params := json.RawMessage("{}")
	go h.runBackgroundGenerate(context.Background(), runID, nodeID, draftID, adminID, draft.Topic, draft.Level, params, node.NodeType)

	writeDraftJSON(w, http.StatusAccepted, map[string]interface{}{
		"node":   toDraftNodeResponse(updated),
		"run_id": runID.String(),
	})
}

// nodeChatHistory handles GET /api/v1/admin/drafts/{draftId}/nodes/{nodeId}/chat (api-sse §5.1.12).
// Returns the node_chats history for the given draft node in ascending created_at order.
//
// @{"req": ["REQ-SYS-077", "REQ-AGENT-047", "REQ-AGENT-053", "REQ-AGENT-054"]}
func (h *DraftHandler) nodeChatHistory(w http.ResponseWriter, r *http.Request) {
	adminID, ok := requestAdmin(w, r)
	if !ok {
		return
	}
	draftID, ok := parseDraftID(w, r)
	if !ok {
		return
	}
	nodeID, ok := parseDraftNodeID(w, r)
	if !ok {
		return
	}

	_, ok = h.checkDraftOwnership(w, r, draftID, adminID)
	if !ok {
		return
	}

	_, ok = h.checkNodeBelongsToDraft(w, r, nodeID, draftID)
	if !ok {
		return
	}

	// Read via the request-scoped connection (node_chats_admin_own RLS policy).
	chats, err := h.draftRepo.ListDraftNodeChats(r.Context(), nil, nodeID)
	if err != nil {
		log.Printf("draft_handler: nodeChatHistory node=%s: %v", nodeID, err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	msgs := make([]map[string]interface{}, 0, len(chats))
	for _, c := range chats {
		msgs = append(msgs, map[string]interface{}{
			"id":         c.ID.String(),
			"role":       c.Role,
			"content":    c.Content,
			"created_at": c.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}

	writeDraftJSON(w, http.StatusOK, map[string]interface{}{
		"messages": msgs,
	})
}

// expandLayer handles POST /api/v1/admin/drafts/{draftId}/layers/{layer}/expand (api-sse §5.1.11).
// Per D11: inserts child draft_nodes (pending) then enqueues ONE background goroutine
// per child calling Chair.GenerateNode(isDraftContext=true). NOT the poller/LayeredRunner.
//
// @{"req": ["REQ-SYS-074", "REQ-SYS-077", "REQ-AGENT-039", "REQ-AGENT-047",
//           "REQ-AGENT-048", "REQ-AGENT-060"]}
func (h *DraftHandler) expandLayer(w http.ResponseWriter, r *http.Request) {
	adminID, ok := requestAdmin(w, r)
	if !ok {
		return
	}
	draftID, ok := parseDraftID(w, r)
	if !ok {
		return
	}

	draft, ok := h.checkDraftOwnership(w, r, draftID, adminID)
	if !ok {
		return
	}

	layerStr := chi.URLParam(r, "layer")
	if !validLayer(layerStr) {
		writeDraftError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid layer; must be one of root, syllabus, section_goal, concept, content")
		return
	}

	// TREE_ALREADY_COMPLETE: content is the final layer — no further expansion.
	if isContentLayer(layerStr) {
		writeDraftError(w, http.StatusConflict, "TREE_ALREADY_COMPLETE", "the tree is already complete; no further layers can be generated")
		return
	}

	// Precondition: all nodes in the current layer must be 'approved'.
	approved, failed, pending, err := h.draftRepo.LayerAggregate(r.Context(), nil, draftID, layerStr)
	if err != nil {
		log.Printf("draft_handler: expandLayer: layer aggregate draft=%s layer=%s: %v", draftID, layerStr, err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	// Any non-approved nodes (pending/generating/awaiting_review/rejected/failed)
	// block expansion. failed counts as unapproved per the precondition spec.
	if approved == 0 || pending > 0 || failed > 0 {
		writeDraftError(w, http.StatusConflict, "LAYER_NOT_FULLY_APPROVED", "not all nodes in the current layer are approved")
		return
	}

	// Determine the next layer.
	nextLayer := nextDraftLayer(layerStr)
	if nextLayer == "" {
		writeDraftError(w, http.StatusConflict, "TREE_ALREADY_COMPLETE", "the tree is already complete")
		return
	}

	// Fetch all approved nodes in the current layer to use as parents for the next layer.
	currentNodes, err := h.draftRepo.ListDraftNodes(r.Context(), nil, draftID, DraftNodeListOpts{NodeTypeFilter: layerStr})
	if err != nil {
		log.Printf("draft_handler: expandLayer: list current layer nodes: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if len(currentNodes) == 0 {
		writeDraftError(w, http.StatusNotFound, "NOT_FOUND", "no nodes found for the specified layer")
		return
	}

	// Insert child draft_nodes (pending) for the next layer (D12 — server pool).
	// One child node per approved parent node in the current layer.
	var insertedNodes []DraftNodeRow
	for i, parent := range currentNodes {
		parentID := parent.ID
		serverConn, connErr := db.AcquireServerConn(r.Context(), h.serverPool)
		if connErr != nil {
			log.Printf("draft_handler: expandLayer: acquire conn for child insert %d: %v", i, connErr)
			continue
		}
		child, createErr := h.draftRepo.CreateDraftNode(r.Context(), serverConn, draftID, &parentID, nextLayer, i, nil)
		serverConn.Release()
		if createErr != nil {
			log.Printf("draft_handler: expandLayer: create child node parent=%s: %v", parentID, createErr)
			continue
		}
		insertedNodes = append(insertedNodes, child)
	}

	if len(insertedNodes) == 0 {
		log.Printf("draft_handler: expandLayer: no children inserted for draft=%s layer=%s", draftID, nextLayer)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to insert child nodes")
		return
	}

	// D11: enqueue ONE background goroutine per child (NOT the poller/LayeredRunner).
	// REQ-SYS-074: bound concurrency with a buffered-channel semaphore (cap=5) so a
	// large concept layer (30+ nodes) cannot saturate the pgx pool or Anthropic rate
	// limits. Each goroutine acquires a token before calling GenerateNode and releases
	// it on completion.
	const expandSemCap = 5
	sem := make(chan struct{}, expandSemCap)
	for _, child := range insertedNodes {
		childCopy := child
		go func(c DraftNodeRow) {
			// Acquire semaphore token before the Anthropic call.
			sem <- struct{}{}
			defer func() { <-sem }()

			bgCtx := context.Background()

			// Transition child: pending → generating.
			transConn, err := db.AcquireServerConn(bgCtx, h.serverPool)
			if err != nil {
				log.Printf("draft_handler: expandLayer: goroutine acquire conn child=%s: %v", c.ID, err)
				return
			}
			ok2, err := h.draftRepo.TransitionDraftNodeStatus(bgCtx, transConn, c.ID, "pending", "generating")
			transConn.Release()
			if err != nil || !ok2 {
				log.Printf("draft_handler: expandLayer: goroutine transition child=%s: ok=%v err=%v", c.ID, ok2, err)
				return
			}

			// Create agent_run for SSE anchoring.
			runID, runErr := h.agentRepo.CreateRunForDraft(bgCtx, draftID, "node_generation")
			if runErr != nil {
				log.Printf("draft_handler: expandLayer: goroutine create run child=%s: %v", c.ID, runErr)
				return
			}

			draftIDCopy := draftID
			childIDCopy := c.ID
			if emitErr := h.agentRepo.EmitNodeEvent(bgCtx, runID, "node_generating", agent.NodeEventPayload{
				NodeID:   &childIDCopy,
				NodeType: c.NodeType,
				DraftID:  &draftIDCopy,
			}); emitErr != nil {
				log.Printf("draft_handler: expandLayer: goroutine emit node_generating child=%s: %v", c.ID, emitErr)
			}

			h.runBackgroundGenerate(bgCtx, runID, c.ID, draftID, adminID, draft.Topic, draft.Level, json.RawMessage("{}"), c.NodeType)
		}(childCopy)
	}

	writeDraftJSON(w, http.StatusOK, map[string]interface{}{
		"next_layer":           nextLayer,
		"inserted_node_count":  len(insertedNodes),
	})
}

// publishDraft handles POST /api/v1/admin/drafts/{draftId}/publish (api-sse §5.1.10).
// Precondition: ALL nodes approved. Creates/updates a course_assignments row recording
// draft_id, sets draft status='published'. Tx-wrapped (D13).
//
// @{"req": ["REQ-SYS-077", "REQ-AGENT-047", "REQ-AGENT-048", "REQ-AGENT-057"]}
func (h *DraftHandler) publishDraft(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)

	adminID, ok := requestAdmin(w, r)
	if !ok {
		return
	}
	draftID, ok := parseDraftID(w, r)
	if !ok {
		return
	}

	draft, ok := h.checkDraftOwnership(w, r, draftID, adminID)
	if !ok {
		return
	}

	if draft.Status == "published" {
		writeDraftError(w, http.StatusConflict, "DRAFT_ALREADY_PUBLISHED", "draft has already been published")
		return
	}

	var req struct {
		AssignmentTitle string  `json:"assignment_title"`
		AssignmentID    *string `json:"assignment_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDraftError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}

	// Precondition: all nodes must be approved.
	// Check all layers. Collect nodes and check for any non-approved nodes.
	allNodes, err := h.draftRepo.ListDraftNodes(r.Context(), nil, draftID, DraftNodeListOpts{})
	if err != nil {
		log.Printf("draft_handler: publishDraft: list nodes: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	for _, n := range allNodes {
		if n.Status != "approved" && n.NodeType != "root" {
			// Root nodes are implicitly pending; all other nodes must be approved.
			writeDraftError(w, http.StatusConflict, "DRAFT_HAS_UNAPPROVED_NODES", "all nodes must be approved before publishing")
			return
		}
	}

	// Tx-wrap the assignment create/update + draft status update (D13).
	// Use the server pool for writes to course_assignments (no RLS on that table).
	serverConn, err := db.AcquireServerConn(r.Context(), h.serverPool)
	if err != nil {
		log.Printf("draft_handler: publishDraft: acquire server conn: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	tx, err := serverConn.Begin(r.Context())
	if err != nil {
		serverConn.Release()
		log.Printf("draft_handler: publishDraft: begin tx: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	defer func() {
		tx.Rollback(r.Context()) //nolint:errcheck
		serverConn.Release()
	}()

	var assignmentID uuid.UUID

	if req.AssignmentID != nil {
		// Link to an existing assignment.
		aid, parseErr := uuid.Parse(*req.AssignmentID)
		if parseErr != nil {
			writeDraftError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid assignment_id")
			return
		}
		assignmentID = aid
		// Update the existing assignment to record the draft_id.
		if _, execErr := tx.Exec(r.Context(),
			`UPDATE course_assignments SET draft_id = $1, updated_at = now() WHERE id = $2`,
			draftID, assignmentID,
		); execErr != nil {
			log.Printf("draft_handler: publishDraft: update assignment: %v", execErr)
			writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}
	} else {
		// Create a new assignment row referencing this draft.
		title := req.AssignmentTitle
		if title == "" {
			title = draft.Title
		}
		scanErr := tx.QueryRow(r.Context(),
			`INSERT INTO course_assignments (admin_id, title, topic, level, parameters, draft_id)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 RETURNING id`,
			adminID, title, draft.Topic, draft.Level, draft.Parameters, draftID,
		).Scan(&assignmentID)
		if scanErr != nil {
			log.Printf("draft_handler: publishDraft: create assignment: %v", scanErr)
			writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}
	}

	// Mark the draft as published and record the assignment.
	if _, execErr := tx.Exec(r.Context(),
		`UPDATE course_drafts
		 SET status = 'published', published_to_assignment_id = $1, updated_at = now()
		 WHERE id = $2`,
		assignmentID, draftID,
	); execErr != nil {
		log.Printf("draft_handler: publishDraft: update draft status: %v", execErr)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		log.Printf("draft_handler: publishDraft: commit: %v", err)
		writeDraftError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	// Re-fetch the published draft for the response.
	publishedDraft, fetchErr := h.draftRepo.GetDraft(r.Context(), nil, draftID)
	if fetchErr != nil {
		log.Printf("draft_handler: publishDraft: re-fetch draft: %v", fetchErr)
		publishedDraft = draft
		publishedDraft.Status = "published"
	}

	writeDraftJSON(w, http.StatusOK, map[string]interface{}{
		"assignment_id": assignmentID.String(),
		"draft":         toDraftResponse(publishedDraft),
	})
}

// streamEvents handles GET /api/v1/admin/drafts/{draftId}/events (api-sse §5.3.1).
// Streams pipeline events for a draft via SSE; GET is CSRF-exempt.
//
// @{"req": ["REQ-SYS-077", "REQ-AGENT-036", "REQ-AGENT-056"]}
func (h *DraftHandler) streamEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeDraftError(w, http.StatusInternalServerError, "STREAMING_UNSUPPORTED", "streaming not supported")
		return
	}

	adminID, ok := requestAdmin(w, r)
	if !ok {
		return
	}
	draftID, ok := parseDraftID(w, r)
	if !ok {
		return
	}

	_, ok = h.checkDraftOwnership(w, r, draftID, adminID)
	if !ok {
		return
	}

	var afterEventID *uuid.UUID
	if after := r.URL.Query().Get("after"); after != "" {
		if id, err := uuid.Parse(after); err == nil {
			afterEventID = &id
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Send initial keepalive so the client knows the stream is open.
	fmt.Fprintf(w, ": keepalive\n\n") //nolint:errcheck
	flusher.Flush()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			events, err := h.agentRepo.GetEventsAfterForDraft(r.Context(), draftID, afterEventID, 20)
			if err != nil {
				fmt.Fprintf(w, "event: error\ndata: {\"error\":\"internal\"}\n\n") //nolint:errcheck
				flusher.Flush()
				return
			}
			for _, ev := range events {
				payload, jsonErr := json.Marshal(map[string]interface{}{
					"id":           ev.ID,
					"agent_run_id": ev.AgentRunID,
					"event_type":   ev.EventType,
					"payload":      json.RawMessage(ev.Payload),
					"emitted_at":   ev.EmittedAt.Format(time.RFC3339Nano),
				})
				if jsonErr != nil {
					continue
				}
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.EventType, payload) //nolint:errcheck
				id := ev.ID
				afterEventID = &id
			}
			flusher.Flush()
		}
	}
}
