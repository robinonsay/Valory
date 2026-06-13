package agent

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("agent: not found")
)

type AgentRunRow struct {
	ID             uuid.UUID
	CourseID       uuid.UUID
	RunType        string
	Status         string
	StartedAt      time.Time
	CompletedAt    *time.Time
	IterationCount int
	Error          *string
}

type PipelineEventRow struct {
	ID         uuid.UUID
	AgentRunID uuid.UUID
	EventType  string
	Payload    []byte
	EmittedAt  time.Time
}

type CourseStudentRow struct {
	CourseID  uuid.UUID
	StudentID uuid.UUID
	// TreeMode is true when the course uses the layered generation path.
	// Added in migration 022; backward-compatible (defaults to false for all
	// existing flat courses). pollAndGenerate branches on this field to route
	// the course to the correct pipeline (REQ-AGENT-037).
	TreeMode bool
}

type AgentRepository struct {
	pool *pgxpool.Pool
}

// @{"req": ["REQ-AGENT-006", "REQ-AGENT-007", "REQ-AGENT-008", "REQ-AGENT-013", "REQ-SYS-015", "REQ-SYS-016", "REQ-SYS-045"]}
func NewAgentRepository(pool *pgxpool.Pool) *AgentRepository {
	return &AgentRepository{pool: pool}
}

// @{"req": ["REQ-AGENT-006", "REQ-AGENT-007", "REQ-AGENT-008", "REQ-AGENT-013", "REQ-SYS-015", "REQ-SYS-016", "REQ-SYS-045"]}
func (r *AgentRepository) CreateRun(ctx context.Context, courseID uuid.UUID, runType string) (AgentRunRow, error) {
	var run AgentRunRow
	err := r.pool.QueryRow(ctx,
		`INSERT INTO agent_runs (course_id, run_type)
		 VALUES ($1, $2)
		 RETURNING id, course_id, run_type, status, started_at, completed_at, iteration_count, error`,
		courseID, runType).
		Scan(
			&run.ID,
			&run.CourseID,
			&run.RunType,
			&run.Status,
			&run.StartedAt,
			&run.CompletedAt,
			&run.IterationCount,
			&run.Error,
		)
	if err != nil {
		return AgentRunRow{}, err
	}
	return run, nil
}

// @{"req": ["REQ-AGENT-006", "REQ-AGENT-007", "REQ-AGENT-008", "REQ-AGENT-013", "REQ-SYS-015", "REQ-SYS-016", "REQ-SYS-045"]}
func (r *AgentRepository) SetRunStatus(ctx context.Context, runID uuid.UUID, status string, errMsg *string) error {
	shouldSetCompleted := status == "completed" || status == "failed" || status == "terminated"

	var query string
	var args []interface{}

	if shouldSetCompleted {
		query = `UPDATE agent_runs
		         SET status = $1, error = $2, completed_at = now()
		         WHERE id = $3`
		args = []interface{}{status, errMsg, runID}
	} else {
		query = `UPDATE agent_runs
		         SET status = $1, error = $2
		         WHERE id = $3`
		args = []interface{}{status, errMsg, runID}
	}

	_, err := r.pool.Exec(ctx, query, args...)
	return err
}

// @{"req": ["REQ-AGENT-006", "REQ-AGENT-007", "REQ-AGENT-008", "REQ-AGENT-013", "REQ-SYS-015", "REQ-SYS-016", "REQ-SYS-045"]}
func (r *AgentRepository) IncrementIteration(ctx context.Context, runID uuid.UUID) (int, error) {
	var iteration int
	err := r.pool.QueryRow(ctx,
		`UPDATE agent_runs
		 SET iteration_count = iteration_count + 1
		 WHERE id = $1
		 RETURNING iteration_count`,
		runID).
		Scan(&iteration)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return iteration, nil
}

// @{"req": ["REQ-AGENT-006", "REQ-AGENT-007", "REQ-AGENT-008", "REQ-AGENT-013", "REQ-SYS-015", "REQ-SYS-016", "REQ-SYS-045"]}
func (r *AgentRepository) EmitEvent(ctx context.Context, runID uuid.UUID, eventType string, payload interface{}) error {
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	_, err = r.pool.Exec(ctx,
		`INSERT INTO pipeline_events (agent_run_id, event_type, payload)
		 VALUES ($1, $2, $3)`,
		runID, eventType, jsonPayload)
	return err
}

// @{"req": ["REQ-AGENT-006", "REQ-AGENT-007", "REQ-AGENT-008", "REQ-AGENT-013", "REQ-SYS-015", "REQ-SYS-016", "REQ-SYS-045"]}
func (r *AgentRepository) GetEventsAfter(ctx context.Context, courseID uuid.UUID, afterEventID *uuid.UUID, limit int) ([]PipelineEventRow, error) {
	var query string
	var args []interface{}

	baseQuery := `SELECT pe.id, pe.agent_run_id, pe.event_type, pe.payload, pe.emitted_at
	              FROM pipeline_events pe
	              JOIN agent_runs ar ON ar.id = pe.agent_run_id
	              WHERE ar.course_id = $1`

	if afterEventID != nil {
		query = baseQuery + `
		         AND pe.emitted_at > (SELECT emitted_at FROM pipeline_events WHERE id = $2)
		         ORDER BY pe.emitted_at ASC
		         LIMIT $3`
		args = []interface{}{courseID, *afterEventID, limit}
	} else {
		query = baseQuery + `
		         ORDER BY pe.emitted_at ASC
		         LIMIT $2`
		args = []interface{}{courseID, limit}
	}

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []PipelineEventRow
	for rows.Next() {
		var event PipelineEventRow
		if err := rows.Scan(
			&event.ID,
			&event.AgentRunID,
			&event.EventType,
			&event.Payload,
			&event.EmittedAt,
		); err != nil {
			return nil, err
		}
		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return events, nil
}

// @{"req": ["REQ-AGENT-006", "REQ-AGENT-007", "REQ-AGENT-008", "REQ-AGENT-013", "REQ-SYS-015", "REQ-SYS-016", "REQ-SYS-045"]}
func (r *AgentRepository) TerminateStudentRuns(ctx context.Context, studentID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx,
		`UPDATE agent_runs
		 SET status = 'terminated', completed_at = now()
		 WHERE course_id IN (SELECT id FROM courses WHERE student_id = $1)
		   AND status = 'running'
		 RETURNING id`,
		studentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return ids, nil
}

// @{"req": ["REQ-AGENT-006", "REQ-AGENT-007", "REQ-AGENT-008", "REQ-AGENT-013", "REQ-SYS-015", "REQ-SYS-016", "REQ-SYS-045"]}
func (r *AgentRepository) ListRunningContentGenerations(ctx context.Context) ([]AgentRunRow, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, course_id, run_type, status, started_at, completed_at, iteration_count, error
		 FROM agent_runs
		 WHERE run_type = 'content_generation' AND status = 'running'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []AgentRunRow
	for rows.Next() {
		var run AgentRunRow
		if err := rows.Scan(
			&run.ID,
			&run.CourseID,
			&run.RunType,
			&run.Status,
			&run.StartedAt,
			&run.CompletedAt,
			&run.IterationCount,
			&run.Error,
		); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return runs, nil
}

// CreateRunForDraft inserts an agent_runs row anchored to a draft_id (course_id
// NULL) for admin-draft pipeline event tracking. The context_check constraint on
// agent_runs requires exactly one of (course_id, draft_id) to be non-null; this
// method satisfies it by supplying only draft_id.
//
// Returns the new agent run ID so callers can anchor pipeline_events to it via
// EmitEvent without needing the full AgentRunRow (which carries CourseID, a field
// that is meaningless in the draft context).
//
// @{"req": ["REQ-AGENT-036", "REQ-AGENT-056", "REQ-AGENT-060"]}
func (r *AgentRepository) CreateRunForDraft(ctx context.Context, draftID uuid.UUID, runType string) (uuid.UUID, error) {
	var runID uuid.UUID
	err := r.pool.QueryRow(ctx,
		`INSERT INTO agent_runs (draft_id, run_type)
		 VALUES ($1, $2)
		 RETURNING id`,
		draftID, runType,
	).Scan(&runID)
	if err != nil {
		return uuid.Nil, err
	}
	return runID, nil
}

// GetEventsAfterForDraft is the draft_id-scoped overload of GetEventsAfter.
// It joins pipeline_events → agent_runs on agent_run_id and filters by draft_id,
// returning events in ascending emitted_at order. The existing GetEventsAfter
// (courseID-scoped) is unchanged; flat-course and student-tree paths continue to
// use it without modification.
//
// afterEventID is optional: when non-nil, only events emitted after that event's
// timestamp are returned. limit caps the result set.
//
// @{"req": ["REQ-AGENT-036", "REQ-AGENT-056"]}
func (r *AgentRepository) GetEventsAfterForDraft(ctx context.Context, draftID uuid.UUID, afterEventID *uuid.UUID, limit int) ([]PipelineEventRow, error) {
	baseQuery := `SELECT pe.id, pe.agent_run_id, pe.event_type, pe.payload, pe.emitted_at
	              FROM pipeline_events pe
	              JOIN agent_runs ar ON ar.id = pe.agent_run_id
	              WHERE ar.draft_id = $1`

	var query string
	var args []interface{}

	if afterEventID != nil {
		query = baseQuery + `
		         AND pe.emitted_at > (SELECT emitted_at FROM pipeline_events WHERE id = $2)
		         ORDER BY pe.emitted_at ASC
		         LIMIT $3`
		args = []interface{}{draftID, *afterEventID, limit}
	} else {
		query = baseQuery + `
		         ORDER BY pe.emitted_at ASC
		         LIMIT $2`
		args = []interface{}{draftID, limit}
	}

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []PipelineEventRow
	for rows.Next() {
		var event PipelineEventRow
		if err := rows.Scan(
			&event.ID,
			&event.AgentRunID,
			&event.EventType,
			&event.Payload,
			&event.EmittedAt,
		); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

// NodeEventPayload is the common payload shape for node-scoped pipeline events.
// Fields are optional; callers populate only those relevant to the event type.
// The json tags use omitempty so unpopulated fields are omitted from the wire
// JSON rather than serialised as empty strings or null.
type NodeEventPayload struct {
	NodeID     *uuid.UUID `json:"node_id,omitempty"`
	NodeType   string     `json:"node_type,omitempty"`
	CourseID   *uuid.UUID `json:"course_id,omitempty"`
	DraftID    *uuid.UUID `json:"draft_id,omitempty"`
	Layer      string     `json:"layer,omitempty"`
	Summary    string     `json:"payload_summary,omitempty"`
	Error      string     `json:"error,omitempty"`
}

// EmitNodeEvent emits a node-scoped pipeline event into pipeline_events via the
// existing EmitEvent path. It is a thin convenience wrapper that constructs a
// NodeEventPayload so T2/T3/T4 emit node events consistently without coupling to
// the payload shape.
//
// Supported event types (api-sse §5.3.5 + state-machine §10):
//
//	node_generating, node_ready, node_approved, node_rejected, node_error,
//	layer_awaiting_expand,
//	layer_generation_started, layer_node_generating, layer_node_generated,
//	layer_node_failed, layer_awaiting_review, layer_approved, layer_node_approved,
//	layer_node_rejected, layer_regeneration_started, layer_generation_failed,
//	tree_generation_complete
//
// @{"req": ["REQ-AGENT-036", "REQ-AGENT-056", "REQ-AGENT-060"]}
func (r *AgentRepository) EmitNodeEvent(ctx context.Context, runID uuid.UUID, eventType string, payload NodeEventPayload) error {
	return r.EmitEvent(ctx, runID, eventType, payload)
}

// ListUntriggeredApprovals returns courses with approved syllabi that have not
// yet had a generation run triggered. It returns tree_mode so that pollAndGenerate
// can branch to the correct pipeline (flat vs tree-mode).
//
// For flat courses (tree_mode=false): excludes courses that already have a
// running or completed 'content_generation' run.
// For tree-mode courses (tree_mode=true): excludes courses that already have a
// running or completed 'tree_layer_generation' run.
// A previous failed run does not permanently block retry (REQ-AGENT-003).
//
// @{"req": ["REQ-AGENT-003", "REQ-AGENT-037", "REQ-SYS-073"]}
func (r *AgentRepository) ListUntriggeredApprovals(ctx context.Context) ([]CourseStudentRow, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT c.id, c.student_id, c.tree_mode FROM courses c
		 WHERE c.status = 'syllabus_approved'
		 AND NOT EXISTS (
		     SELECT 1 FROM agent_runs ar
		     WHERE ar.course_id = c.id
		     AND ar.run_type = CASE WHEN c.tree_mode THEN 'tree_layer_generation' ELSE 'content_generation' END
		     AND ar.status IN ('running', 'completed')
		 )`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []CourseStudentRow
	for rows.Next() {
		var row CourseStudentRow
		if err := rows.Scan(&row.CourseID, &row.StudentID, &row.TreeMode); err != nil {
			return nil, err
		}
		results = append(results, row)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return results, nil
}
