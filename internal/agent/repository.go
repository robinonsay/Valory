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

// @{"req": ["REQ-AGENT-006", "REQ-AGENT-007", "REQ-AGENT-008", "REQ-AGENT-013", "REQ-SYS-015", "REQ-SYS-016", "REQ-SYS-045"]}
func (r *AgentRepository) ListUntriggeredApprovals(ctx context.Context) ([]CourseStudentRow, error) {
	rows, err := r.pool.Query(ctx,
		// Exclude only running or completed runs so that a previous failed run
		// does not permanently block retry (REQ-AGENT-003).
		`SELECT c.id, c.student_id FROM courses c
		 WHERE c.status = 'syllabus_approved'
		 AND NOT EXISTS (
		     SELECT 1 FROM agent_runs ar
		     WHERE ar.course_id = c.id
		     AND ar.run_type = 'content_generation'
		     AND ar.status IN ('running', 'completed')
		 )`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []CourseStudentRow
	for rows.Next() {
		var row CourseStudentRow
		if err := rows.Scan(&row.CourseID, &row.StudentID); err != nil {
			return nil, err
		}
		results = append(results, row)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return results, nil
}
