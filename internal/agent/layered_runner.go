// layered_runner.go — student knowledge-tree layer generation engine.
//
// LayeredRunner drives the layer-by-layer content generation pipeline for
// tree-mode student courses (courses.tree_mode = true). Each layer of the
// knowledge tree (section_goal → concept → content) is generated in parallel
// across nodes, paused at a HITL checkpoint, and resumed durably from DB state
// after the human issues a layer-expand call via the HTTP handler.
//
// Design decisions:
//   - D11: student path only (admin drafts handled by T4, separate goroutines).
//   - D12: all course_nodes / courses writes run on the server pool.
//   - D14: per-layer token budget enforced before each node call.
//   - D15: node content generated via Chair.GenerateNode (node-type-aware prompts);
//     reviewer correction/escalation loop reused per-node.
//
// State-machine reference: docs/design/layered-generation-state-machine.md §4-§9.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valory/valory/internal/db"
)

// ErrTreeComplete is returned by ExpandToNextLayer when the content layer is
// the current layer — the tree has no further layers to generate.
var ErrTreeComplete = errors.New("layered_runner: tree is complete (content layer is current)")

// ErrLayerExpandConflict is returned by ExpandToNextLayer when the precondition
// is not met: there are still pending/generating/awaiting_review/rejected nodes
// in the current layer, or zero approved nodes.
var ErrLayerExpandConflict = errors.New("layered_runner: layer expand precondition not met")

// layerSequence is the strict depth-first order the tree grows through.
// root and syllabus are pre-seeded approved at init; the first HITL gate is at
// section_goal (design §3.4). The sequence order is used by nextLayer().
var layerSequence = []NodeType{
	NodeTypeRoot,
	NodeTypeSyllabus,
	NodeTypeSectionGoal,
	NodeTypeConcept,
	NodeTypeContent,
}

// layeredRunnerConcurrency caps the number of nodes generated in parallel
// within a single layer call. Prevents unbounded goroutine fan-out.
const layeredRunnerConcurrency = 5

// LayeredRunner generates knowledge-tree nodes layer by layer for a tree-mode
// student course. It owns the inner generation loop but not the outer poller
// (AgentRunner.pollLayeredGeneration owns dispatch).
type LayeredRunner struct {
	pool      *pgxpool.Pool
	treeRepo  *TreeRepository
	agentRepo *AgentRepository
	chair     *Chair
	reviewer  *Reviewer
	configSvc interface {
		GetInt64(string) int64
	}
}

// NewLayeredRunner constructs a LayeredRunner.
//
// @{"req": ["REQ-AGENT-037", "REQ-AGENT-038", "REQ-AGENT-039", "REQ-AGENT-040",
//
//	"REQ-AGENT-041", "REQ-AGENT-042", "REQ-AGENT-043", "REQ-AGENT-045",
//	"REQ-SYS-073", "REQ-SYS-074"]}
func NewLayeredRunner(
	pool *pgxpool.Pool,
	treeRepo *TreeRepository,
	agentRepo *AgentRepository,
	chair *Chair,
	reviewer *Reviewer,
	configSvc interface {
		GetInt64(string) int64
	},
) *LayeredRunner {
	return &LayeredRunner{
		pool:      pool,
		treeRepo:  treeRepo,
		agentRepo: agentRepo,
		chair:     chair,
		reviewer:  reviewer,
		configSvc: configSvc,
	}
}

// nextLayer returns the layer that follows currentLayer in the fixed sequence.
// Returns NodeType("") when currentLayer is NodeTypeContent (tree complete).
func nextLayer(currentLayer NodeType) NodeType {
	for i, l := range layerSequence {
		if l == currentLayer && i+1 < len(layerSequence) {
			return layerSequence[i+1]
		}
	}
	return NodeType("")
}

// courseMetaRow holds the fields the LayeredRunner reads from courses for a
// single tree-mode course dispatch.
type courseMetaRow struct {
	CourseID     uuid.UUID
	StudentID    uuid.UUID
	Topic        string
	Level        string
	Parameters   json.RawMessage
	CurrentLayer NodeType
	Status       string
}

// loadCourseMeta queries a tree-mode course and its optional assignment to
// build a courseMetaRow. All reads use a server-role connection (D12).
//
// @{"req": ["REQ-AGENT-037", "REQ-SYS-073"]}
func (lr *LayeredRunner) loadCourseMeta(ctx context.Context, courseID uuid.UUID) (courseMetaRow, error) {
	conn, err := db.AcquireServerConn(ctx, lr.pool)
	if err != nil {
		return courseMetaRow{}, fmt.Errorf("layered_runner: load course meta: %w", err)
	}
	defer conn.Release()

	var meta courseMetaRow
	var currentLayer *string
	var level, parameters *string

	// LEFT JOIN course_assignments to pick up level and parameters for assigned
	// courses; student-initiated courses have NULL assignment_id and default values.
	err = conn.QueryRow(ctx,
		`SELECT c.id, c.student_id, c.topic, c.status,
		        c.current_layer::text,
		        COALESCE(ca.level, 'beginner'),
		        COALESCE(ca.parameters::text, '{}')
		 FROM courses c
		 LEFT JOIN course_assignments ca ON ca.id = c.assignment_id
		 WHERE c.id = $1 AND c.tree_mode = true`,
		courseID,
	).Scan(
		&meta.CourseID,
		&meta.StudentID,
		&meta.Topic,
		&meta.Status,
		&currentLayer,
		&level,
		&parameters,
	)
	if err != nil {
		return courseMetaRow{}, fmt.Errorf("layered_runner: load course meta query: %w", err)
	}

	if currentLayer != nil {
		meta.CurrentLayer = NodeType(*currentLayer)
	}
	if level != nil {
		meta.Level = *level
	}
	if parameters != nil {
		meta.Parameters = json.RawMessage(*parameters)
	}

	return meta, nil
}

// resetStaleGeneratingNodes resets course_nodes stuck in 'generating' for more
// than 10 minutes back to 'pending' so the next poller tick re-dispatches them.
// This handles goroutine crashes without requiring external intervention (§6.3).
//
// @{"req": ["REQ-AGENT-041"]}
func (lr *LayeredRunner) resetStaleGeneratingNodes(ctx context.Context, courseID uuid.UUID) error {
	conn, err := db.AcquireServerConn(ctx, lr.pool)
	if err != nil {
		return fmt.Errorf("layered_runner: reset stale nodes: acquire conn: %w", err)
	}
	defer conn.Release()

	tag, err := conn.Exec(ctx,
		`UPDATE course_nodes
		 SET status = 'pending', updated_at = now()
		 WHERE course_id = $1
		   AND status = 'generating'
		   AND updated_at < now() - interval '10 minutes'`,
		courseID,
	)
	if err != nil {
		return fmt.Errorf("layered_runner: reset stale generating nodes: %w", err)
	}
	if tag.RowsAffected() > 0 {
		log.Printf("layered_runner: reset %d stale generating node(s) for course %s",
			tag.RowsAffected(), courseID)
	}
	return nil
}

// GenerateLayer generates all pending/rejected nodes at the given layer for
// the course. Nodes are dispatched in parallel (bounded concurrency). After all
// nodes settle (no pending/generating left), the course is transitioned to
// 'awaiting_layer_approval' and a layer_awaiting_review event is emitted.
//
// Per D15: uses Chair.GenerateNode (node-type-aware prompts) then runs the
// reviewer correction/escalation loop per node.
// Per D12: all writes use server-role connections.
// Per D14: per-layer token budget enforced before each node call.
//
// @{"req": ["REQ-AGENT-037", "REQ-AGENT-038", "REQ-AGENT-042", "REQ-AGENT-045",
//
//	"REQ-AGENT-046", "REQ-SYS-073", "REQ-SYS-074"]}
func (lr *LayeredRunner) GenerateLayer(
	ctx context.Context,
	runID uuid.UUID,
	courseID uuid.UUID,
	studentID uuid.UUID,
	layer NodeType,
	topic string,
	level string,
	parameters json.RawMessage,
) error {
	// Emit layer_generation_started event.
	if err := lr.agentRepo.EmitNodeEvent(ctx, runID, "layer_generation_started", NodeEventPayload{
		CourseID: &courseID,
		Layer:    string(layer),
	}); err != nil {
		log.Printf("layered_runner: emit layer_generation_started: %v", err)
	}

	// Load all pending/rejected nodes for this layer (server-role read via pool).
	conn, err := db.AcquireServerConn(ctx, lr.pool)
	if err != nil {
		return fmt.Errorf("layered_runner: generate layer: acquire conn: %w", err)
	}
	nodeType := layer
	nodes, err := lr.treeRepo.ListNodesByCourse(ctx, conn, courseID, ListNodeOptions{
		NodeType: &nodeType,
	})
	conn.Release()
	if err != nil {
		return fmt.Errorf("layered_runner: generate layer: list nodes: %w", err)
	}

	// Filter to only nodes eligible for generation: pending or rejected.
	var eligible []CourseNode
	for _, n := range nodes {
		if n.Status == NodeStatusPending || n.Status == NodeStatusRejected {
			eligible = append(eligible, n)
		}
	}

	if len(eligible) == 0 {
		// All nodes are already in a settled state; check if we should transition.
		log.Printf("layered_runner: no eligible nodes for layer %s course %s; checking settle state", layer, courseID)
		return lr.settleLayer(ctx, runID, courseID, studentID, layer)
	}

	// Per-layer token budget (D14 / REQ-AGENT-045).
	layerBudget := lr.configSvc.GetInt64("per_layer_token_budget")

	// Semaphore for bounded parallelism.
	sem := make(chan struct{}, layeredRunnerConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for _, node := range eligible {
		if ctx.Err() != nil {
			break
		}

		wg.Add(1)
		sem <- struct{}{}

		go func(n CourseNode) {
			defer wg.Done()
			defer func() { <-sem }()

			if genErr := lr.generateNode(ctx, runID, courseID, studentID, n, layer, topic, level, parameters, layerBudget); genErr != nil {
				// Non-fatal for the layer: log and continue. Individual node
				// failures are recorded as 'failed' status in generateNode.
				log.Printf("layered_runner: generate node %s: %v", n.ID, genErr)
				mu.Lock()
				if firstErr == nil {
					firstErr = genErr
				}
				mu.Unlock()
			}
		}(node)
	}

	wg.Wait()

	// After all goroutines complete, check whether the layer is fully settled.
	return lr.settleLayer(ctx, runID, courseID, studentID, layer)
}

// checkTokenBudget queries the current token usage for the (studentID, courseID)
// pair and returns an error when the per-layer budget would be exceeded by one
// more node generation call. On budget exceeded it marks the node failed and
// returns a sentinel error so the caller can short-circuit immediately.
//
// Extracted from generateNode so the same check can be applied before BOTH the
// initial Chair.GenerateNode call and each correction-loop re-call (Bug 4 /
// D14 fix — REQ-SYS-074).
//
// @{"req": ["REQ-AGENT-045", "REQ-SYS-074"]}
func (lr *LayeredRunner) checkTokenBudget(
	ctx context.Context,
	runID uuid.UUID,
	courseID uuid.UUID,
	studentID uuid.UUID,
	nodeID uuid.UUID,
	layer NodeType,
	layerBudget int64,
) error {
	if layerBudget <= 0 {
		return nil
	}

	var usedTokens int64
	if qErr := lr.pool.QueryRow(ctx,
		`SELECT COALESCE(total_tokens_used, 0) FROM agent_token_usage
		 WHERE student_id = $1 AND course_id = $2`,
		studentID, courseID,
	).Scan(&usedTokens); qErr != nil {
		// If no row exists yet, used = 0 which is fine; log other errors but
		// treat as non-fatal to avoid blocking generation on a metrics read.
		log.Printf("layered_runner: check token budget for node %s: %v", nodeID, qErr)
	}

	const estimatedNodeTokens = nodeGenerationMaxTokens
	if usedTokens+estimatedNodeTokens > layerBudget {
		if fErr := lr.failNode(ctx, runID, courseID, studentID, nodeID, layer, "token_budget_exceeded"); fErr != nil {
			log.Printf("layered_runner: fail node token budget for %s: %v", nodeID, fErr)
		}
		return fmt.Errorf("generate node %s: per-layer token budget exceeded", nodeID)
	}
	return nil
}

// generateNode runs the Chair.GenerateNode + reviewer loop for a single node.
// Transitions: pending/rejected → generating → awaiting_review (or failed).
// All writes use server-role connections (D12).
//
// @{"req": ["REQ-AGENT-030", "REQ-AGENT-038", "REQ-AGENT-045", "REQ-AGENT-046",
//
//	"REQ-SYS-073", "REQ-SYS-074"]}
func (lr *LayeredRunner) generateNode(
	ctx context.Context,
	runID uuid.UUID,
	courseID uuid.UUID,
	studentID uuid.UUID,
	node CourseNode,
	layer NodeType,
	topic string,
	level string,
	parameters json.RawMessage,
	layerBudget int64,
) error {
	// Acquire server connection for atomic pending/rejected → generating transition.
	conn, err := db.AcquireServerConn(ctx, lr.pool)
	if err != nil {
		return fmt.Errorf("generate node %s: acquire conn: %w", node.ID, err)
	}

	// Atomic test-and-set: only proceed when status is still pending or rejected.
	ok, err := lr.treeRepo.TransitionNodeStatus(ctx, conn, node.ID, node.Status, NodeStatusGenerating)
	conn.Release()
	if err != nil {
		return fmt.Errorf("generate node %s: transition to generating: %w", node.ID, err)
	}
	if !ok {
		// Another goroutine claimed this node concurrently; skip it silently.
		return nil
	}

	// Emit layer_node_generating event.
	if emitErr := lr.agentRepo.EmitNodeEvent(ctx, runID, "layer_node_generating", NodeEventPayload{
		NodeID:   &node.ID,
		CourseID: &courseID,
		Layer:    string(layer),
	}); emitErr != nil {
		log.Printf("layered_runner: emit layer_node_generating: %v", emitErr)
	}

	// Per-layer token budget check before initial generation (D14 / REQ-AGENT-045 /
	// REQ-SYS-074). Delegated to checkTokenBudget helper so the same check can be
	// applied before the correction-loop re-call too (Bug 4 fix).
	if budgetErr := lr.checkTokenBudget(ctx, runID, courseID, studentID, node.ID, layer, layerBudget); budgetErr != nil {
		return budgetErr
	}

	// Load node_chats history for context continuity (feedback on rejected nodes).
	chatConn, err := db.AcquireServerConn(ctx, lr.pool)
	if err != nil {
		// Non-fatal for chat history; proceed without prior context.
		log.Printf("layered_runner: load node chats for %s: %v", node.ID, err)
	}
	var priorContext []NodeChatMessage
	if chatConn != nil {
		priorContext, err = lr.treeRepo.ListNodeChats(ctx, chatConn, node.ID)
		chatConn.Release()
		if err != nil {
			log.Printf("layered_runner: list node chats for %s: %v", node.ID, err)
			priorContext = nil
		}
	}

	// Generate node content via Chair.GenerateNode (D15 — node-type-aware prompts).
	// isDraftContext = false: student path (D11).
	newPayload, err := lr.chair.GenerateNode(
		ctx,
		studentID,
		courseID,
		false, // isDraftContext: student path (D11)
		string(layer),
		topic,
		level,
		parameters,
		priorContext,
	)
	if err != nil {
		// Generation failed: transition node to failed and return.
		reason := "generation_error"
		if errors.Is(err, ErrTokenCapExceeded) {
			reason = "token_cap_exceeded"
		} else if errors.Is(err, ErrRateLimitExhausted) {
			reason = "rate_limit_exhausted"
		}
		if fErr := lr.failNode(ctx, runID, courseID, studentID, node.ID, layer, reason); fErr != nil {
			log.Printf("layered_runner: fail node after generation error: %v", fErr)
		}
		return fmt.Errorf("generate node %s: %w", node.ID, err)
	}

	// Store the generated payload and transition to awaiting_review.
	// Compute approximate tokens used for this call (read from last token usage delta).
	// tokensUsed is optional; we set nil and rely on ThrottledClient's UPSERT.
	writeConn, err := db.AcquireServerConn(ctx, lr.pool)
	if err != nil {
		return fmt.Errorf("generate node %s: acquire conn for payload write: %w", node.ID, err)
	}
	if err := lr.treeRepo.SetNodePayload(ctx, writeConn, node.ID, newPayload, nil); err != nil {
		writeConn.Release()
		return fmt.Errorf("generate node %s: set payload: %w", node.ID, err)
	}
	writeConn.Release()

	// Append the generated content to node_chats as an 'assistant' turn so the
	// Chair sees it in future refinement calls.
	chatWriteConn, chatErr := db.AcquireServerConn(ctx, lr.pool)
	if chatErr == nil {
		if appendErr := lr.treeRepo.AppendNodeChat(ctx, chatWriteConn, node.ID, "assistant", string(newPayload)); appendErr != nil {
			log.Printf("layered_runner: append node chat for %s: %v", node.ID, appendErr)
		}
		chatWriteConn.Release()
	}

	// Run the AI reviewer correction/escalation loop ONLY for content nodes (D15a fix).
	// The citation/quality reviewer is designed for prose lesson content with citations.
	// Running it on section_goal or concept nodes (structured JSON, no citations) causes
	// them to always fail the citation check and escalate, blocking the pipeline.
	// section_goal and concept nodes transition straight to awaiting_review here and
	// wait for the human HITL gate. root/syllabus are pre-seeded approved (§3.4).
	if layer == NodeTypeContent {
		if reviewErr := lr.runNodeReviewLoop(ctx, runID, courseID, studentID, node.ID, layer, string(newPayload), layerBudget); reviewErr != nil {
			// Review loop error does not fail the node directly; the loop already
			// transitions to failed on unrecoverable errors.
			log.Printf("layered_runner: review loop for node %s: %v", node.ID, reviewErr)
		}
	}

	// Emit layer_node_generated event.
	if emitErr := lr.agentRepo.EmitNodeEvent(ctx, runID, "layer_node_generated", NodeEventPayload{
		NodeID:   &node.ID,
		CourseID: &courseID,
		Layer:    string(layer),
	}); emitErr != nil {
		log.Printf("layered_runner: emit layer_node_generated: %v", emitErr)
	}

	return nil
}

// runNodeReviewLoop is the per-node reviewer correction/escalation loop for
// tree nodes. It mirrors runner.go's runReviewLoop but:
//   - Sources content from course_nodes.payload (not lesson_content).
//   - Transitions the node to 'failed' (not 'escalated') on max iterations.
//   - Regenerates by calling Chair.GenerateNode with feedback (not professor.RegenerateSection).
//   - Enforces the per-layer token budget before EACH Chair.GenerateNode re-call
//     (Bug 4 / D14 fix — REQ-SYS-074); exceeding budget marks the node failed.
//
// Only called for content nodes (D15a); section_goal and concept nodes skip
// straight to awaiting_review for human HITL approval.
//
// On max iterations the node is escalated via escalateNode() and left in
// 'awaiting_review' (not failed) matching the existing flat-course behaviour
// where max-iteration exhaustion does not block the pipeline.
//
// @{"req": ["REQ-AGENT-007", "REQ-AGENT-008", "REQ-AGENT-038", "REQ-SYS-073", "REQ-SYS-074"]}
func (lr *LayeredRunner) runNodeReviewLoop(
	ctx context.Context,
	runID uuid.UUID,
	courseID uuid.UUID,
	studentID uuid.UUID,
	nodeID uuid.UUID,
	layer NodeType,
	contentAdoc string,
	layerBudget int64,
) error {
	maxIterations := lr.configSvc.GetInt64("correction_loop_max_iterations")
	if maxIterations <= 0 {
		maxIterations = 5
	}

	current := contentAdoc
	for {
		// The reviewer uses lesson_content IDs as contentID in the flat path; for
		// tree nodes we pass nodeID as the contentID. The reviewer's citation_verified
		// UPDATE targets lesson_content, which does not apply here — but the call
		// is still valid because the reviewer only updates citation_verified when
		// approved, and that UPDATE simply affects zero rows for nodeID (not a UUID
		// in lesson_content). The review decision is what matters.
		result, err := lr.reviewer.ReviewSection(ctx, runID, courseID, studentID, nodeID, current)
		if err != nil {
			return fmt.Errorf("node %s: reviewer: %w", nodeID, err)
		}

		if result.Approved {
			// Node content passed review; status is already 'awaiting_review' from
			// SetNodePayload — leave it there for human HITL approval.
			if emitErr := lr.agentRepo.EmitEvent(ctx, runID, "section_review_passed", map[string]any{
				"node_id": nodeID,
				"layer":   string(layer),
			}); emitErr != nil {
				log.Printf("layered_runner: emit section_review_passed: %v", emitErr)
			}
			return nil
		}

		// Review failed — increment iteration count.
		if emitErr := lr.agentRepo.EmitEvent(ctx, runID, "section_review_failed", map[string]any{
			"node_id":  nodeID,
			"layer":    string(layer),
			"feedback": result.Feedback,
		}); emitErr != nil {
			log.Printf("layered_runner: emit section_review_failed: %v", emitErr)
		}

		iterations, iterErr := lr.agentRepo.IncrementIteration(ctx, runID)
		if iterErr != nil {
			return fmt.Errorf("node %s: increment iteration: %w", nodeID, iterErr)
		}

		if int64(iterations) >= maxIterations {
			// Max iterations reached — escalate per REQ-AGENT-008.
			// Node stays in 'awaiting_review'; human can still approve or reject.
			lr.escalateNode(ctx, runID, courseID, studentID, nodeID, layer, iterations, result.Feedback)
			return nil
		}

		// Per-layer token budget check before correction re-call (Bug 4 / D14 fix).
		// The initial budget check fires before generateNode; without this second
		// check a node that fails review up to maxIterations times can spend ~5×
		// the per-node budget, escaping the layer budget cap (REQ-SYS-074).
		if budgetErr := lr.checkTokenBudget(ctx, runID, courseID, studentID, nodeID, layer, layerBudget); budgetErr != nil {
			// checkTokenBudget already transitions the node to failed.
			return budgetErr
		}

		// Regenerate: build feedback chat history and call Chair.GenerateNode again.
		chatConn, chatErr := db.AcquireServerConn(ctx, lr.pool)
		if chatErr != nil {
			return fmt.Errorf("node %s: acquire conn for chat on correction: %w", nodeID, chatErr)
		}
		// Append reviewer feedback as a 'user' turn for context continuity.
		if appendErr := lr.treeRepo.AppendNodeChat(ctx, chatConn, nodeID, "user", result.Feedback); appendErr != nil {
			log.Printf("layered_runner: append reviewer feedback for %s: %v", nodeID, appendErr)
		}
		priorContext, listErr := lr.treeRepo.ListNodeChats(ctx, chatConn, nodeID)
		chatConn.Release()
		if listErr != nil {
			return fmt.Errorf("node %s: list chats for correction: %w", nodeID, listErr)
		}

		// We need the topic/level/parameters for regeneration — load from meta.
		meta, metaErr := lr.loadCourseMeta(ctx, courseID)
		if metaErr != nil {
			return fmt.Errorf("node %s: load course meta for correction: %w", nodeID, metaErr)
		}

		refined, refineErr := lr.chair.GenerateNode(
			ctx,
			studentID,
			courseID,
			false,
			string(layer),
			meta.Topic,
			meta.Level,
			meta.Parameters,
			priorContext,
		)
		if refineErr != nil {
			return fmt.Errorf("node %s: correction regeneration: %w", nodeID, refineErr)
		}

		// Update the node payload with the corrected content.
		writeConn, wErr := db.AcquireServerConn(ctx, lr.pool)
		if wErr != nil {
			return fmt.Errorf("node %s: acquire conn for correction write: %w", nodeID, wErr)
		}
		if setErr := lr.treeRepo.SetNodePayload(ctx, writeConn, nodeID, refined, nil); setErr != nil {
			writeConn.Release()
			return fmt.Errorf("node %s: set corrected payload: %w", nodeID, setErr)
		}
		writeConn.Release()

		current = string(refined)
	}
}

// failNode transitions a node to 'failed', emits a layer_node_failed event,
// and logs the escalation. The 'failed' status is terminal (REQ-AGENT-031).
//
// @{"req": ["REQ-AGENT-031", "REQ-AGENT-042", "REQ-AGENT-045", "REQ-AGENT-046",
//
//	"REQ-SYS-074"]}
func (lr *LayeredRunner) failNode(
	ctx context.Context,
	runID uuid.UUID,
	courseID uuid.UUID,
	studentID uuid.UUID,
	nodeID uuid.UUID,
	layer NodeType,
	reason string,
) error {
	conn, err := db.AcquireServerConn(ctx, lr.pool)
	if err != nil {
		return fmt.Errorf("fail node %s: acquire conn: %w", nodeID, err)
	}
	defer conn.Release()

	// Force transition to 'failed' regardless of current status.
	if _, err := conn.Exec(ctx,
		`UPDATE course_nodes SET status = 'failed', updated_at = now() WHERE id = $1`,
		nodeID,
	); err != nil {
		return fmt.Errorf("fail node %s: update status: %w", nodeID, err)
	}

	if emitErr := lr.agentRepo.EmitNodeEvent(ctx, runID, "layer_node_failed", NodeEventPayload{
		NodeID:   &nodeID,
		CourseID: &courseID,
		Layer:    string(layer),
		Error:    reason,
	}); emitErr != nil {
		log.Printf("layered_runner: emit layer_node_failed: %v", emitErr)
	}

	return nil
}

// escalateNode emits a correction_escalated event for a node that exhausted
// the review loop max iterations without passing. The node stays in
// awaiting_review so the human can still approve or reject it.
//
// @{"req": ["REQ-AGENT-008", "REQ-AGENT-042"]}
func (lr *LayeredRunner) escalateNode(
	ctx context.Context,
	runID uuid.UUID,
	courseID uuid.UUID,
	studentID uuid.UUID,
	nodeID uuid.UUID,
	layer NodeType,
	iterations int,
	feedback string,
) {
	if emitErr := lr.agentRepo.EmitEvent(ctx, runID, "correction_escalated", map[string]any{
		"node_id":    nodeID,
		"layer":      string(layer),
		"iterations": iterations,
		"feedback":   feedback,
	}); emitErr != nil {
		log.Printf("layered_runner: emit correction_escalated for node %s: %v", nodeID, emitErr)
	}
	log.Printf("layered_runner: node %s layer %s escalated after %d iterations: %s",
		nodeID, layer, iterations, feedback)
}

// settleLayer checks whether all nodes in the layer have settled from the
// generation perspective: no nodes remain in 'pending' or 'generating'. It uses
// LayerSettleCounts (not LayerAggregate) so that nodes in 'awaiting_review'
// count as settled — they are waiting for the human HITL gate, not for AI
// generation. Without this distinction the course would be trapped polling
// forever once generation completes and all nodes enter awaiting_review
// (Bug 1 / D15b fix — REQ-AGENT-038).
//
// When fully settled:
//   - If all nodes failed → emit layer_generation_failed and return an error.
//   - If at least one node is approved or awaiting_review → transition course to
//     'awaiting_layer_approval' and emit layer_awaiting_review for the SSE stream.
//
// @{"req": ["REQ-AGENT-038", "REQ-AGENT-042", "REQ-SYS-073"]}
func (lr *LayeredRunner) settleLayer(
	ctx context.Context,
	runID uuid.UUID,
	courseID uuid.UUID,
	studentID uuid.UUID,
	layer NodeType,
) error {
	conn, err := db.AcquireServerConn(ctx, lr.pool)
	if err != nil {
		return fmt.Errorf("layered_runner: settle layer: acquire conn: %w", err)
	}
	// Use LayerSettleCounts: only pending+generating count as in-flight.
	// awaiting_review is treated as settled (waiting for human, not for AI).
	inFlight, approvedOrAwaiting, allFailed, err := lr.treeRepo.LayerSettleCounts(ctx, conn, courseID, layer)
	conn.Release()
	if err != nil {
		return fmt.Errorf("layered_runner: settle layer: settle counts: %w", err)
	}

	if inFlight > 0 {
		// Generation goroutines still running for some nodes; not yet settled.
		// The next poller tick will call GenerateLayer again to pick up remaining work.
		return nil
	}

	if allFailed {
		// Every node in this layer failed — emit layer_generation_failed and halt.
		if emitErr := lr.agentRepo.EmitNodeEvent(ctx, runID, "layer_generation_failed", NodeEventPayload{
			CourseID: &courseID,
			Layer:    string(layer),
		}); emitErr != nil {
			log.Printf("layered_runner: emit layer_generation_failed: %v", emitErr)
		}
		log.Printf("layered_runner: course %s layer %s: all nodes failed", courseID, layer)
		return fmt.Errorf("layered_runner: layer %s: all nodes failed", layer)
	}

	if approvedOrAwaiting == 0 {
		// Should not happen when inFlight==0 and !allFailed, but guard defensively.
		return nil
	}

	// At least one node has content (approved or awaiting human review) — transition
	// course to awaiting_layer_approval and emit the HITL checkpoint event.
	updateConn, err := db.AcquireServerConn(ctx, lr.pool)
	if err != nil {
		return fmt.Errorf("layered_runner: settle layer: acquire update conn: %w", err)
	}
	if _, err := updateConn.Exec(ctx,
		`UPDATE courses
		 SET status = 'awaiting_layer_approval', current_layer = $1::node_type_enum, updated_at = now()
		 WHERE id = $2`,
		string(layer), courseID,
	); err != nil {
		updateConn.Release()
		return fmt.Errorf("layered_runner: settle layer: update course status: %w", err)
	}
	updateConn.Release()

	// Emit layer_awaiting_review checkpoint event (SSE → student sees HITL gate).
	if emitErr := lr.agentRepo.EmitNodeEvent(ctx, runID, "layer_awaiting_review", NodeEventPayload{
		CourseID: &courseID,
		Layer:    string(layer),
	}); emitErr != nil {
		log.Printf("layered_runner: emit layer_awaiting_review: %v", emitErr)
	}

	log.Printf("layered_runner: course %s layer %s awaiting human review (approvedOrAwaiting=%d)",
		courseID, layer, approvedOrAwaiting)
	return nil
}

// ExpandToNextLayer inserts the child nodes (pending) for the layer after
// currentLayer and sets courses.status='generating' + courses.current_layer=<next>.
// Called by the HTTP handler (T3) after the human has approved all nodes in
// the current layer — NOT by the poller (D11 / §4.3).
//
// Precondition: pending_count == 0 AND approved_count > 0 for currentLayer.
// Returns ErrLayerExpandConflict when precondition not met.
// Returns ErrTreeComplete when currentLayer == 'content' (no further layers).
//
// Child node insertion is idempotent via the unique index on
// (course_id, node_type, parent_id, ordering) — concurrent calls cannot
// double-insert (§6.4 / REQ-AGENT-040).
//
// @{"req": ["REQ-AGENT-039", "REQ-AGENT-040", "REQ-SYS-073"]}
func (lr *LayeredRunner) ExpandToNextLayer(
	ctx context.Context,
	courseID uuid.UUID,
	currentLayer NodeType,
) error {
	if currentLayer == NodeTypeContent {
		return ErrTreeComplete
	}

	// Check precondition: no pending/generating/awaiting_review/rejected nodes.
	conn, err := db.AcquireServerConn(ctx, lr.pool)
	if err != nil {
		return fmt.Errorf("layered_runner: expand: acquire conn: %w", err)
	}
	counts, err := lr.treeRepo.LayerAggregate(ctx, conn, courseID, currentLayer)
	conn.Release()
	if err != nil {
		return fmt.Errorf("layered_runner: expand: layer aggregate: %w", err)
	}
	if counts.Pending > 0 || counts.Approved == 0 {
		return ErrLayerExpandConflict
	}

	next := nextLayer(currentLayer)
	if next == NodeType("") {
		return ErrTreeComplete
	}

	// Load current layer's approved nodes to use as parents for next-layer nodes.
	parentConn, err := db.AcquireServerConn(ctx, lr.pool)
	if err != nil {
		return fmt.Errorf("layered_runner: expand: acquire parent conn: %w", err)
	}
	currentLayerType := currentLayer
	parents, err := lr.treeRepo.ListNodesByCourse(ctx, parentConn, courseID, ListNodeOptions{
		NodeType: &currentLayerType,
	})
	parentConn.Release()
	if err != nil {
		return fmt.Errorf("layered_runner: expand: list parent nodes: %w", err)
	}

	// Default child counts per parent node, per layer type.
	// concept_count_per_section and content_count_per_concept are configurable
	// via the same configSvc. Default 3 mirrors the cost model in design §8.1.
	conceptsPerSection := lr.configSvc.GetInt64("tree_concepts_per_section")
	if conceptsPerSection <= 0 {
		conceptsPerSection = 3
	}

	insertConn, err := db.AcquireServerConn(ctx, lr.pool)
	if err != nil {
		return fmt.Errorf("layered_runner: expand: acquire insert conn: %w", err)
	}
	defer insertConn.Release()

	for _, parent := range parents {
		if parent.Status != NodeStatusApproved {
			continue // skip failed nodes
		}

		childCount := 1 // content: one content node per concept
		if next == NodeTypeConcept {
			childCount = int(conceptsPerSection)
		}

		for i := 0; i < childCount; i++ {
			parentID := parent.ID
			// ON CONFLICT DO NOTHING via the sibling_order unique index.
			// The partial unique index is on (course_id, parent_id, ordering) WHERE parent_id IS NOT NULL.
			if _, insErr := insertConn.Exec(ctx,
				`INSERT INTO course_nodes (course_id, parent_id, node_type, ordering, status, payload)
				 VALUES ($1, $2, $3::node_type_enum, $4, 'pending', '{}')
				 ON CONFLICT (course_id, parent_id, ordering) WHERE parent_id IS NOT NULL DO NOTHING`,
				courseID, parentID, string(next), i,
			); insErr != nil {
				return fmt.Errorf("layered_runner: expand: insert child node (parent=%s, i=%d): %w",
					parentID, i, insErr)
			}
		}
	}

	// Transition course to generating + advance current_layer to next.
	if _, err := insertConn.Exec(ctx,
		`UPDATE courses
		 SET status = 'generating', current_layer = $1::node_type_enum, updated_at = now()
		 WHERE id = $2`,
		string(next), courseID,
	); err != nil {
		return fmt.Errorf("layered_runner: expand: update course status: %w", err)
	}

	log.Printf("layered_runner: course %s expanded from %s to %s", courseID, currentLayer, next)
	return nil
}

// seedTreeAndGenerateRoot initialises a tree-mode course on first activation:
//  1. Inserts the root node (status='approved', payload=topic/intent).
//  2. Inserts the syllabus node (status='approved', payload=flat syllabus AsciiDoc).
//  3. Inserts section_goal children (status='pending') — one per syllabus section.
//  4. Sets courses.status='generating', current_layer='section_goal'.
//  5. Calls GenerateLayer to immediately start generating section_goal nodes.
//
// This function is called by the poller in a goroutine on the first tick after
// a tree-mode course transitions to 'syllabus_approved'. It is idempotent:
// duplicate inserts are guarded by ON CONFLICT DO NOTHING.
//
// The first HITL gate is at section_goal (design §3.4 / REQ-AGENT-043).
//
// @{"req": ["REQ-AGENT-043", "REQ-AGENT-037", "REQ-AGENT-038", "REQ-SYS-073"]}
func (lr *LayeredRunner) seedTreeAndGenerateRoot(
	ctx context.Context,
	courseID uuid.UUID,
	studentID uuid.UUID,
) error {
	// Load course meta: topic, level, parameters.
	meta, err := lr.loadCourseMeta(ctx, courseID)
	if err != nil {
		return fmt.Errorf("layered_runner: seed tree: %w", err)
	}

	// Create the agent run for this tree layer generation.
	run, err := lr.agentRepo.CreateRun(ctx, courseID, "tree_layer_generation")
	if err != nil {
		return fmt.Errorf("layered_runner: seed tree: create run: %w", err)
	}

	// Load the approved syllabus AsciiDoc via a server-role connection.
	// syllabi has FORCE RLS; a bare pool connection carries no app.current_role
	// and would be blocked by the policy in production (Bug 3 / D12 fix).
	syllabusConn, err := db.AcquireServerConn(ctx, lr.pool)
	if err != nil {
		return fmt.Errorf("layered_runner: seed tree: acquire conn for syllabus: %w", err)
	}
	var syllabusAdoc string
	if err := syllabusConn.QueryRow(ctx,
		`SELECT content_adoc FROM syllabi
		 WHERE course_id = $1 AND approved_at IS NOT NULL
		 ORDER BY version DESC LIMIT 1`,
		courseID,
	).Scan(&syllabusAdoc); err != nil {
		syllabusConn.Release()
		return fmt.Errorf("layered_runner: seed tree: load approved syllabus: %w", err)
	}
	syllabusConn.Release()

	conn, err := db.AcquireServerConn(ctx, lr.pool)
	if err != nil {
		return fmt.Errorf("layered_runner: seed tree: acquire conn: %w", err)
	}

	// Insert root node (pre-seeded approved; payload = topic intent).
	// ON CONFLICT uses the partial-index predicate (WHERE node_type = 'root') — the
	// migration creates a partial unique INDEX (not a named CONSTRAINT), so the
	// ON CONFLICT clause must reference the column list + WHERE, not a constraint name
	// (Bug 2 fix).
	rootPayload, _ := json.Marshal(map[string]string{"topic": meta.Topic, "intent": meta.Topic})
	var rootID uuid.UUID
	if err := conn.QueryRow(ctx,
		`INSERT INTO course_nodes (course_id, parent_id, node_type, ordering, status, payload)
		 VALUES ($1, NULL, 'root', 0, 'approved', $2)
		 ON CONFLICT (course_id) WHERE node_type = 'root' DO UPDATE
		   SET updated_at = course_nodes.updated_at
		 RETURNING id`,
		courseID, rootPayload,
	).Scan(&rootID); err != nil {
		conn.Release()
		return fmt.Errorf("layered_runner: seed tree: insert root: %w", err)
	}

	// Insert syllabus node (pre-seeded approved; payload = syllabus AsciiDoc).
	// The syllabus was already approved in the intake/assignment flow; no HITL gate here.
	syllabusPayload, _ := json.Marshal(map[string]string{"content_adoc": syllabusAdoc})
	var syllabusID uuid.UUID
	if err := conn.QueryRow(ctx,
		`INSERT INTO course_nodes (course_id, parent_id, node_type, ordering, status, payload)
		 VALUES ($1, $2, 'syllabus', 0, 'approved', $3)
		 ON CONFLICT (course_id, parent_id, ordering) WHERE parent_id IS NOT NULL
		 DO UPDATE SET updated_at = course_nodes.updated_at
		 RETURNING id`,
		courseID, rootID, syllabusPayload,
	).Scan(&syllabusID); err != nil {
		conn.Release()
		return fmt.Errorf("layered_runner: seed tree: insert syllabus: %w", err)
	}

	// Count how many syllabus sections exist (from the homework table, which was
	// populated by Chair.AssignDueDates during content generation setup).
	// Use that count to determine how many section_goal nodes to create.
	var sectionCount int
	if err := conn.QueryRow(ctx,
		`SELECT COUNT(*) FROM homework WHERE course_id = $1`,
		courseID,
	).Scan(&sectionCount); err != nil || sectionCount == 0 {
		// Fall back to counting from the syllabus text (AsciiDoc == sections).
		// The Chair parsed sections when assigning due dates; use the homework count.
		// If homework isn't set up yet, default to 5 sections.
		sectionCount = 5
		log.Printf("layered_runner: seed tree: using default %d sections for course %s", sectionCount, courseID)
	}

	// Insert section_goal nodes (pending) — one per syllabus section.
	for i := 0; i < sectionCount; i++ {
		if _, insErr := conn.Exec(ctx,
			`INSERT INTO course_nodes (course_id, parent_id, node_type, ordering, status, payload)
			 VALUES ($1, $2, 'section_goal', $3, 'pending', '{}')
			 ON CONFLICT (course_id, parent_id, ordering) WHERE parent_id IS NOT NULL DO NOTHING`,
			courseID, syllabusID, i,
		); insErr != nil {
			conn.Release()
			return fmt.Errorf("layered_runner: seed tree: insert section_goal %d: %w", i, insErr)
		}
	}

	// Transition course to generating + set current_layer = 'section_goal'.
	if _, err := conn.Exec(ctx,
		`UPDATE courses
		 SET status = 'generating', current_layer = 'section_goal', updated_at = now()
		 WHERE id = $1`,
		courseID,
	); err != nil {
		conn.Release()
		return fmt.Errorf("layered_runner: seed tree: update course status: %w", err)
	}
	conn.Release()

	log.Printf("layered_runner: course %s tree seeded (root=%s, syllabus=%s, sections=%d)",
		courseID, rootID, syllabusID, sectionCount)

	// Immediately generate the section_goal layer (first HITL gate).
	return lr.GenerateLayer(ctx, run.ID, courseID, studentID, NodeTypeSectionGoal,
		meta.Topic, meta.Level, meta.Parameters)
}
