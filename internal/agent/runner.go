// runner.go — AgentRunner orchestrates the full multi-agent pipeline.
// It polls for new work, manages the content generation lifecycle, enforces
// timeouts (REQ-AGENT-014), handles API failures (REQ-AGENT-011), emits
// pipeline events for SSE (REQ-AGENT-006), and terminates in-flight
// operations before account deletion (REQ-AGENT-013).
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/db"
	"github.com/valory/valory/internal/image"
	"github.com/valory/valory/internal/notify"
)

// submissionRepository is the subset of submission.Repository the runner needs.
// Defined here (consumer side) to avoid an import cycle between agent and submission.
// ListPendingGrading is not in this interface because the runner queries the DB
// directly (joining homework and courses) to get the rubric and course_id in one
// round-trip; only the write methods are delegated to the repository.
type submissionRepository interface {
	SetRawScore(ctx context.Context, submissionID uuid.UUID, rawScore float64) error
	MarkGradingFailed(ctx context.Context, submissionID uuid.UUID) error
}

// gradeService is the subset of grade.Service the runner needs.
// Defined here to avoid an import cycle between agent and grade.
type gradeService interface {
	ComputeAndStoreGrade(ctx context.Context, submissionID, homeworkID, studentID, courseID uuid.UUID, rawScore float64) error
}

// aiMessenger is the subset of ThrottledClient that GradeSubmission needs.
// Defined here so unit tests can inject a stub without a real Anthropic API key.
type aiMessenger interface {
	Messages(ctx context.Context, studentID, courseID uuid.UUID, params anthropic.MessageNewParams) (*anthropic.Message, error)
}

// SubmissionToGrade carries the fields the grading runner needs from a pending
// submission joined with its homework row.
type SubmissionToGrade struct {
	ID          uuid.UUID
	HomeworkID  uuid.UUID
	StudentID   uuid.UUID
	CourseID    uuid.UUID
	FilePath    string
	Rubric      string
	SubmittedAt time.Time
	// ImageIDs is the ordered list of image UUIDs attached at submission time.
	// Nil means no images were attached (REQ-AGENT-025).
	ImageIDs    []uuid.UUID
}

// AgentRunner is the central orchestrator. It owns the polling goroutines and
// drives chair, professor, and reviewer through the content generation pipeline.
type AgentRunner struct {
	pool           *pgxpool.Pool
	agentRepo      *AgentRepository
	chair          *Chair
	professor      *Professor
	reviewer       *Reviewer
	layeredRunner  *LayeredRunner // nil when tree-mode feature is disabled
	submissionRepo submissionRepository
	gradeSvc       gradeService
	aiClient       aiMessenger
	imageRepo      *image.Repository // nil-safe: grading falls back to text-only when nil
	configSvc      interface {
		GetInt64(string) int64
		GetFloat64(string) float64
	}
	// inFlightLayered is an in-process guard for the layered-generation poller
	// (Bug 5 fix). Two consecutive 30-second ticks while GenerateLayer is still
	// running for a course would otherwise dispatch duplicate goroutines, causing
	// duplicate layer_awaiting_review SSE events and a staleness-boundary race.
	// Keys are course UUID strings; presence means a goroutine is in flight.
	// Single-instance server, so in-process is sufficient.
	inFlightLayered sync.Map

	// inFlightFlat guards the flat-course (content_generation) dispatch path,
	// mirroring inFlightLayered for the tree path (D19/REQ-AGENT-064). Keys are
	// course UUID strings; presence means a goroutine is already running.
	// The DB unique partial index (agent_runs_one_running_per_course_type_idx)
	// is the cross-process atomic guard; this map is a fast-path latency
	// optimisation that avoids a DB round-trip when the previous goroutine for
	// the same course is still active in this process.
	inFlightFlat sync.Map
}

// @{"req": ["REQ-AGENT-003", "REQ-AGENT-006", "REQ-AGENT-007", "REQ-AGENT-008", "REQ-AGENT-011", "REQ-AGENT-013", "REQ-AGENT-014", "REQ-AGENT-037", "REQ-AGENT-038", "REQ-AGENT-039"]}
func NewAgentRunner(
	pool *pgxpool.Pool,
	agentRepo *AgentRepository,
	chair *Chair,
	professor *Professor,
	reviewer *Reviewer,
	submissionRepo submissionRepository,
	gradeSvc gradeService,
	aiClient aiMessenger,
	configSvc interface {
		GetInt64(string) int64
		GetFloat64(string) float64
	},
) *AgentRunner {
	return &AgentRunner{
		pool:           pool,
		agentRepo:      agentRepo,
		chair:          chair,
		professor:      professor,
		reviewer:       reviewer,
		submissionRepo: submissionRepo,
		gradeSvc:       gradeSvc,
		aiClient:       aiClient,
		configSvc:      configSvc,
	}
}

// SetLayeredRunner injects the LayeredRunner so that the poller can dispatch
// tree-mode courses. Called after construction by cmd/server/main.go when the
// tree-mode feature is enabled. Nil-safe: when not set, tree-mode courses are
// skipped silently and a warning is logged.
//
// @{"req": ["REQ-AGENT-037", "REQ-AGENT-038", "REQ-AGENT-039", "REQ-SYS-073"]}
func (r *AgentRunner) SetLayeredRunner(lr *LayeredRunner) {
	r.layeredRunner = lr
}

// SetImageRepository injects the image repository so the grading runner can
// load attached images and pass them as vision blocks to Claude (REQ-AGENT-025).
// Called from main after both packages are wired. Nil-safe: when not set the
// grading call falls back to text-only.
//
// @{"req": ["REQ-AGENT-025"]}
func (r *AgentRunner) SetImageRepository(repo *image.Repository) {
	r.imageRepo = repo
}

// Start launches background polling goroutines:
//   - every 30s: detects syllabus-approved courses and starts content generation (REQ-AGENT-003)
//   - every 60s: scans for untriggered feedback and kicks off section regeneration (REQ-AGENT-010)
//   - every 30s: polls for pending-grading submissions and grades each with Claude (REQ-AGENT-003)
//   - every 30s: detects tree-mode courses in generating/awaiting_regeneration state (REQ-AGENT-037)
//
// It blocks until ctx is cancelled.
//
// @{"req": ["REQ-AGENT-003", "REQ-AGENT-010", "REQ-AGENT-037", "REQ-AGENT-038", "REQ-AGENT-041"]}
func (r *AgentRunner) Start(ctx context.Context) {
	genTicker := time.NewTicker(30 * time.Second)
	fbTicker := time.NewTicker(60 * time.Second)
	gradeTicker := time.NewTicker(30 * time.Second)
	// layerTicker drives the tree-mode layer generation pipeline (REQ-AGENT-037).
	// It does NOT call ExpandToNextLayer — that is the HTTP handler's responsibility (D11/§4.3).
	layerTicker := time.NewTicker(30 * time.Second)
	defer genTicker.Stop()
	defer fbTicker.Stop()
	defer gradeTicker.Stop()
	defer layerTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-genTicker.C:
			r.pollAndGenerate(ctx)
		case <-fbTicker.C:
			r.pollFeedback(ctx)
		case <-gradeTicker.C:
			r.pollGradingQueue(ctx)
		case <-layerTicker.C:
			r.pollLayeredGeneration(ctx)
		}
	}
}

// TerminateStudentOperations cancels all running agent runs for a student's
// courses and cancels their in-flight contexts (REQ-AGENT-013).
// Implements user.AgentTerminator.
//
// @{"req": ["REQ-AGENT-013"]}
func (r *AgentRunner) TerminateStudentOperations(ctx context.Context, studentID uuid.UUID) error {
	_, err := r.agentRepo.TerminateStudentRuns(ctx, studentID)
	if err != nil {
		return fmt.Errorf("runner: terminate student operations: %w", err)
	}
	return nil
}

// GetEventsAfter returns pipeline events for a course emitted after afterEventID,
// used by the SSE handler to push real-time status to the student (REQ-AGENT-006).
//
// @{"req": ["REQ-AGENT-006"]}
func (r *AgentRunner) GetEventsAfter(ctx context.Context, courseID uuid.UUID, afterEventID *uuid.UUID, limit int) ([]PipelineEventRow, error) {
	return r.agentRepo.GetEventsAfter(ctx, courseID, afterEventID, limit)
}

// HandleSectionRegen regenerates a single section in response to student feedback
// (REQ-AGENT-010). It creates a section_regen agent run, re-generates the section,
// and runs it through the reviewer.
//
// @{"req": ["REQ-AGENT-010"]}
func (r *AgentRunner) HandleSectionRegen(ctx context.Context, courseID, studentID uuid.UUID, feedbackID uuid.UUID, sectionIndex int, feedbackText string) error {
	run, err := r.agentRepo.CreateRun(ctx, courseID, "section_regen")
	if err != nil {
		return fmt.Errorf("runner: section regen: create run: %w", err)
	}

	if err := r.agentRepo.EmitEvent(ctx, run.ID, "section_regen_started", map[string]any{
		"section_index": sectionIndex,
		"feedback_id":   feedbackID,
	}); err != nil {
		log.Printf("runner: emit section_regen_started: %v", err)
	}

	section, err := r.professor.RegenerateSection(ctx, courseID, studentID, sectionIndex, feedbackText)
	if err != nil {
		errMsg := err.Error()
		_ = r.agentRepo.SetRunStatus(ctx, run.ID, "failed", &errMsg)
		return fmt.Errorf("runner: section regen: %w", err)
	}

	if pipeErr := r.runReviewLoop(ctx, run.ID, courseID, studentID, section); pipeErr != nil {
		errMsg := pipeErr.Error()
		_ = r.agentRepo.SetRunStatus(ctx, run.ID, "failed", &errMsg)
		return pipeErr
	}

	// Mark feedback as regeneration_triggered using a server-role connection.
	if fconn, fErr := db.AcquireServerConn(ctx, r.pool); fErr == nil {
		if _, dbErr := fconn.Exec(ctx,
			`UPDATE section_feedback SET regeneration_triggered = true WHERE id = $1`,
			feedbackID,
		); dbErr != nil {
			log.Printf("runner: mark feedback triggered: %v", dbErr)
		}
		fconn.Release()
	} else {
		log.Printf("runner: acquire server conn for feedback update: %v", fErr)
	}

	_ = r.agentRepo.EmitEvent(ctx, run.ID, "section_regen_complete", map[string]any{
		"section_index": sectionIndex,
	})
	_ = r.agentRepo.SetRunStatus(ctx, run.ID, "completed", nil)
	return nil
}

// pollAndGenerate queries for courses with approved syllabi that are eligible
// for a new generation run and dispatches one goroutine per course.
//
// Unified dispatch (D19/REQ-AGENT-064): both the flat path and the tree path
// use the same two-layer idempotency guard:
//   - Layer 1 (fast path): process-level sync.Map (inFlightFlat / inFlightLayered).
//     Skips the DB round-trip when a goroutine for this course is already active.
//   - Layer 2 (atomic claim): the unique partial index
//     agent_runs_one_running_per_course_type_idx enforces one 'running' row per
//     (course_id, run_type) at the database layer. CreateRun returns
//     ErrRunAlreadyClaimed (SQLSTATE 23505) on conflict; the dispatcher discards
//     silently and releases the in-flight map entry so the next tick can retry.
//
// Retry/terminal flow (§6.2/D18/REQ-AGENT-062/065): on failure, IncrementAttemptCount
// applies the backoff and, when the new count >= maxAttempts, SetCourseTerminal
// transitions the course to 'generation_failed'. On terminal success, ResetAttemptCount
// clears the counter and backoff timestamp (D21 — reset only on terminal success).
//
// @{"req": ["REQ-AGENT-003", "REQ-AGENT-037", "REQ-AGENT-043", "REQ-AGENT-062", "REQ-AGENT-064", "REQ-AGENT-065", "REQ-AGENT-067", "REQ-AGENT-068"]}
func (r *AgentRunner) pollAndGenerate(ctx context.Context) {
	// Resolve retry controls from config at call time so operator changes take
	// effect without a server restart. Defaults match the migration 024 seed.
	maxAttempts := r.configSvc.GetInt64("generation_max_attempts")
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	backoffSeconds := r.configSvc.GetInt64("generation_backoff_seconds")
	if backoffSeconds <= 0 {
		backoffSeconds = 600
	}

	courses, err := r.agentRepo.ListUntriggeredApprovals(ctx, maxAttempts)
	if err != nil {
		log.Printf("runner: poll: list untriggered approvals: %v", err)
		return
	}

	for _, c := range courses {
		key := c.CourseID.String()

		if c.TreeMode {
			if r.layeredRunner == nil {
				log.Printf("runner: tree-mode course %s skipped — LayeredRunner not wired", c.CourseID)
				continue
			}
			// Layer 1: process-level guard for tree path.
			if _, alreadyRunning := r.inFlightLayered.LoadOrStore(key, struct{}{}); alreadyRunning {
				continue
			}
			go func(course CourseStudentRow) {
				defer r.inFlightLayered.Delete(course.CourseID.String())
				r.dispatchTreeCourse(ctx, course, maxAttempts, backoffSeconds)
			}(c)
		} else {
			// Layer 1: process-level guard for flat path.
			if _, alreadyRunning := r.inFlightFlat.LoadOrStore(key, struct{}{}); alreadyRunning {
				continue
			}
			go func(course CourseStudentRow) {
				defer r.inFlightFlat.Delete(course.CourseID.String())
				r.dispatchFlatCourse(ctx, course, maxAttempts, backoffSeconds)
			}(c)
		}
	}
}

// dispatchFlatCourse handles the full flat-course dispatch sequence for one course:
// CreateRun (Layer 2 claim), tokenCapPreFlight, RunContentGeneration,
// handleFailedRun / handleCompletedRun.
//
// RunContentGeneration is retained intact (REQ-AGENT-014 timeout, handleTimeout/
// handleAPIFailure classification, UPDATE courses SET status='active'). It manages
// its own SetRunStatus calls for failure cases internally. The outer goroutine
// (this function) calls handleFailedRun / handleCompletedRun for the courses-level
// columns only — there is no double SetRunStatus call on agent_runs.
//
// Reconciliation of the double-status-write concern (design §8.2 option a):
// RunContentGeneration calls SetRunStatus(failed) internally for timeout/API-failure/
// default error cases, and SetRunStatus(completed) for the success case. This function
// calls handleFailedRun or handleCompletedRun after RunContentGeneration returns.
// handleFailedRun does NOT call SetRunStatus — it only updates courses columns.
// handleCompletedRun does NOT call SetRunStatus — RunContentGeneration already
// stamped completed_at = now() via its own SetRunStatus(completed) call.
// Result: completed_at is stamped exactly once per run, by RunContentGeneration.
//
// @{"req": ["REQ-AGENT-062", "REQ-AGENT-064", "REQ-AGENT-065", "REQ-AGENT-066", "REQ-AGENT-068"]}
func (r *AgentRunner) dispatchFlatCourse(ctx context.Context, course CourseStudentRow, maxAttempts, backoffSeconds int64) {
	// Layer 2: atomic DB claim — unique partial index enforces one running row.
	run, err := r.agentRepo.CreateRun(ctx, course.CourseID, "content_generation")
	if errors.Is(err, ErrRunAlreadyClaimed) {
		// Another goroutine or process already holds the run; skip silently.
		log.Printf("runner: flat course %s already claimed, skipping", course.CourseID)
		return
	}
	if err != nil {
		log.Printf("runner: flat course %s: create run: %v", course.CourseID, err)
		return
	}

	// Emit generation_claimed event (design §10 / REQ-AGENT-066).
	_ = r.agentRepo.EmitEvent(ctx, run.ID, "generation_claimed", map[string]any{
		"course_id":      course.CourseID,
		"run_id":         run.ID,
		"attempt_number": 0, // current count before this run's outcome
	})

	// Pre-flight: token cap check BEFORE any paid work (§9.2 / REQ-AGENT-068).
	// A capped run fails immediately — zero per-section Brave calls are made.
	if err := r.tokenCapPreFlight(ctx, course.CourseID, course.StudentID); err != nil {
		errMsg := err.Error()
		_ = r.agentRepo.SetRunStatus(ctx, run.ID, "failed", &errMsg)
		_ = r.agentRepo.EmitEvent(ctx, run.ID, "token_cap_preflight_failed", map[string]any{
			"course_id": course.CourseID,
		})
		r.handleFailedRun(ctx, run.ID, course.CourseID, course.StudentID, err, maxAttempts, backoffSeconds)
		return
	}

	// RunContentGeneration is retained intact: it applies the REQ-AGENT-014 timeout
	// context, calls handleTimeout/handleAPIFailure for event emission and student
	// notification, runs generateAllSections, and transitions the course to 'active'
	// on success. It calls SetRunStatus internally for all terminal states so
	// completed_at is stamped exactly once inside RunContentGeneration (see
	// reconciliation note in the function comment above).
	genErr := r.RunContentGeneration(ctx, run.ID, course.CourseID, course.StudentID)
	if genErr != nil {
		// RunContentGeneration has already called SetRunStatus(failed) internally.
		// handleFailedRun updates courses columns only (no SetRunStatus here).
		r.handleFailedRun(ctx, run.ID, course.CourseID, course.StudentID, genErr, maxAttempts, backoffSeconds)
		return
	}

	// Terminal success: course is now 'active'. Reset the attempt counter and
	// backoff timestamp (D21 — reset only on terminal success, never per layer).
	r.handleCompletedRun(ctx, run.ID, course.CourseID, course.StudentID)
}

// dispatchTreeCourse handles the full tree-mode dispatch sequence for one course:
// CreateRun (Layer 2 claim), tokenCapPreFlight, seedTreeAndGenerateRoot,
// handleFailedRun / handleCompletedRun.
//
// The attempt counter is incremented at the dispatch level (this function), not
// per layer (D21). Only when the full tree-generation cycle completes is
// handleCompletedRun called to reset the counter.
//
// @{"req": ["REQ-AGENT-062", "REQ-AGENT-064", "REQ-AGENT-065", "REQ-AGENT-066", "REQ-AGENT-068"]}
func (r *AgentRunner) dispatchTreeCourse(ctx context.Context, course CourseStudentRow, maxAttempts, backoffSeconds int64) {
	// Layer 2: atomic DB claim.
	run, err := r.agentRepo.CreateRun(ctx, course.CourseID, "tree_layer_generation")
	if errors.Is(err, ErrRunAlreadyClaimed) {
		log.Printf("runner: tree course %s already claimed, skipping", course.CourseID)
		return
	}
	if err != nil {
		log.Printf("runner: tree course %s: create run: %v", course.CourseID, err)
		return
	}

	_ = r.agentRepo.EmitEvent(ctx, run.ID, "generation_claimed", map[string]any{
		"course_id": course.CourseID,
		"run_id":    run.ID,
	})

	// Pre-flight token cap check before any tree-node generation work.
	if err := r.tokenCapPreFlight(ctx, course.CourseID, course.StudentID); err != nil {
		errMsg := err.Error()
		_ = r.agentRepo.SetRunStatus(ctx, run.ID, "failed", &errMsg)
		_ = r.agentRepo.EmitEvent(ctx, run.ID, "token_cap_preflight_failed", map[string]any{
			"course_id": course.CourseID,
		})
		r.handleFailedRun(ctx, run.ID, course.CourseID, course.StudentID, err, maxAttempts, backoffSeconds)
		return
	}

	// Pass run.ID to seedTreeAndGenerateRoot so it uses the single dispatch run
	// created above. The function no longer creates its own run — doing so would
	// fire SQLSTATE 23505 on the unique partial index and produce a silent no-op
	// storm (B1 fix: exactly one tree_layer_generation run per dispatch).
	genErr := r.layeredRunner.seedTreeAndGenerateRoot(ctx, run.ID, course.CourseID, course.StudentID)
	if genErr != nil {
		errMsg := genErr.Error()
		_ = r.agentRepo.SetRunStatus(ctx, run.ID, "failed", &errMsg)
		r.handleFailedRun(ctx, run.ID, course.CourseID, course.StudentID, genErr, maxAttempts, backoffSeconds)
		return
	}

	_ = r.agentRepo.SetRunStatus(ctx, run.ID, "completed", nil)
	r.handleCompletedRun(ctx, run.ID, course.CourseID, course.StudentID)
}

// pollFeedback scans for section_feedback rows that have not yet triggered
// regeneration and dispatches regen jobs for those whose text contains at least
// one change-request keyword (REQ-CONTENT-004 / REQ-AGENT-010).
//
// @{"req": ["REQ-AGENT-010", "REQ-CONTENT-004"]}
func (r *AgentRunner) pollFeedback(ctx context.Context) {
	type feedbackRow struct {
		ID           uuid.UUID
		StudentID    uuid.UUID
		CourseID     uuid.UUID
		SectionIndex int
		FeedbackText string
	}

	// Use a server-role connection: section_feedback and courses are RLS-protected.
	conn, err := db.AcquireServerConn(ctx, r.pool)
	if err != nil {
		log.Printf("runner: poll feedback: acquire server conn: %v", err)
		return
	}
	// AND c.tree_mode = false: tree-backed courses use the explicit HITL feedback
	// endpoint (PATCH .../nodes/{id}/feedback) instead of keyword-heuristic polling
	// (design §5.5 / REQ-CONTENT-004). Skipping them here keeps the flat-course
	// path byte-for-byte unchanged while preventing double-processing.
	rows, err := conn.Query(ctx,
		`SELECT sf.id, sf.student_id, sf.course_id, sf.section_index, sf.feedback_text
		 FROM section_feedback sf
		 JOIN courses c ON c.id = sf.course_id
		 WHERE sf.regeneration_triggered = false
		   AND c.status = 'active'
		   AND c.tree_mode = false
		 ORDER BY sf.submitted_at ASC
		 LIMIT 20`,
	)
	if err != nil {
		conn.Release()
		log.Printf("runner: poll feedback: %v", err)
		return
	}
	var pending []feedbackRow
	for rows.Next() {
		var fb feedbackRow
		if err := rows.Scan(&fb.ID, &fb.StudentID, &fb.CourseID, &fb.SectionIndex, &fb.FeedbackText); err != nil {
			log.Printf("runner: poll feedback: scan row: %v", err)
			continue
		}
		pending = append(pending, fb)
	}
	rows.Close()

	for _, fb := range pending {
		if !containsRegenKeyword(fb.FeedbackText) {
			// Mark as triggered so we don't re-evaluate it next poll.
			if _, dbErr := conn.Exec(ctx,
				`UPDATE section_feedback SET regeneration_triggered = true WHERE id = $1`,
				fb.ID,
			); dbErr != nil {
				log.Printf("runner: mark non-regen feedback triggered: %v", dbErr)
			}
			continue
		}
		go func(fb feedbackRow) {
			if err := r.HandleSectionRegen(ctx, fb.CourseID, fb.StudentID, fb.ID, fb.SectionIndex, fb.FeedbackText); err != nil {
				log.Printf("runner: section regen for feedback %s: %v", fb.ID, err)
			}
		}(fb)
	}
	conn.Release()
}

// pollLayeredGeneration finds tree-mode courses with status 'generating' or
// 'awaiting_regeneration' that have a current_layer set, runs the stuck-node
// staleness reset, and dispatches LayeredRunner.GenerateLayer per course.
//
// It does NOT call ExpandToNextLayer — that is the HTTP handler's sole
// responsibility (D11 / design §4.3 / REQ-AGENT-039). The poller only picks up
// courses where the human has already called /expand (status='generating') or
// where rejected nodes need re-generation (status='awaiting_regeneration').
//
// @{"req": ["REQ-AGENT-037", "REQ-AGENT-038", "REQ-AGENT-039", "REQ-AGENT-041", "REQ-SYS-073"]}
func (r *AgentRunner) pollLayeredGeneration(ctx context.Context) {
	if r.layeredRunner == nil {
		return
	}

	type layeredCourseRow struct {
		CourseID     uuid.UUID
		StudentID    uuid.UUID
		CurrentLayer string
	}

	conn, err := db.AcquireServerConn(ctx, r.pool)
	if err != nil {
		log.Printf("runner: poll layered generation: acquire conn: %v", err)
		return
	}
	rows, err := conn.Query(ctx,
		`SELECT c.id, c.student_id, c.current_layer::text
		 FROM courses c
		 WHERE c.tree_mode = true
		   AND c.status IN ('generating', 'awaiting_regeneration')
		   AND c.current_layer IS NOT NULL
		 LIMIT 20`,
	)
	if err != nil {
		conn.Release()
		log.Printf("runner: poll layered generation: query: %v", err)
		return
	}

	var pending []layeredCourseRow
	for rows.Next() {
		var row layeredCourseRow
		if err := rows.Scan(&row.CourseID, &row.StudentID, &row.CurrentLayer); err != nil {
			log.Printf("runner: poll layered generation: scan: %v", err)
			continue
		}
		pending = append(pending, row)
	}
	rows.Close()
	conn.Release()

	for _, c := range pending {
		layer := NodeType(c.CurrentLayer)

		// In-process guard: skip dispatch if a goroutine for this course is already
		// running (Bug 5 fix). Without this, two consecutive 30s ticks while
		// GenerateLayer is in flight would dispatch duplicate goroutines, producing
		// duplicate layer_awaiting_review SSE events and a node-reset race.
		if _, alreadyRunning := r.inFlightLayered.LoadOrStore(c.CourseID.String(), struct{}{}); alreadyRunning {
			continue
		}

		go func(courseID, studentID uuid.UUID, layer NodeType) {
			// Remove the in-flight guard when this goroutine exits so the next
			// poller tick can dispatch again after work completes.
			defer r.inFlightLayered.Delete(courseID.String())

			// Reset any stuck generating nodes before dispatching (REQ-AGENT-041).
			if err := r.layeredRunner.resetStaleGeneratingNodes(ctx, courseID); err != nil {
				log.Printf("runner: reset stale nodes for course %s: %v", courseID, err)
			}

			// For awaiting_regeneration: reset rejected nodes back to pending so
			// GenerateLayer picks them up, then advance course to 'generating'.
			sconn, connErr := db.AcquireServerConn(ctx, r.pool)
			if connErr != nil {
				log.Printf("runner: poll layered: acquire conn for regen reset course %s: %v", courseID, connErr)
				return
			}
			// If course is awaiting_regeneration, move rejected nodes → pending and
			// course status → generating so GenerateLayer treats them normally.
			_, execErr := sconn.Exec(ctx,
				`UPDATE course_nodes
				 SET status = 'pending', updated_at = now()
				 WHERE course_id = $1 AND node_type = $2::node_type_enum AND status = 'rejected'`,
				courseID, string(layer),
			)
			if execErr != nil {
				sconn.Release()
				log.Printf("runner: poll layered: reset rejected nodes for course %s: %v", courseID, execErr)
				return
			}
			// Transition course from awaiting_regeneration → generating so the poller
			// does not dispatch again before this goroutine finishes.
			_, execErr = sconn.Exec(ctx,
				`UPDATE courses
				 SET status = 'generating', updated_at = now()
				 WHERE id = $1 AND status = 'awaiting_regeneration'`,
				courseID,
			)
			sconn.Release()
			if execErr != nil {
				log.Printf("runner: poll layered: transition regen course %s: %v", courseID, execErr)
			}

			// Load course meta for GenerateLayer call.
			meta, metaErr := r.layeredRunner.loadCourseMeta(ctx, courseID)
			if metaErr != nil {
				log.Printf("runner: poll layered: load meta for course %s: %v", courseID, metaErr)
				return
			}

			// Create (or reuse) the run for this layer generation.
			// Tolerate ErrRunAlreadyClaimed (SQLSTATE 23505) as a benign skip:
			// another tick or goroutine already holds a running row for this
			// course+type pair (design §3.2.5 / REQ-AGENT-064). The in-flight
			// guard above is the fast-path; the unique index is the DB guard.
			// @{"req": ["REQ-AGENT-062", "REQ-AGENT-064"]}
			run, runErr := r.agentRepo.CreateRun(ctx, courseID, "tree_layer_generation")
			if errors.Is(runErr, ErrRunAlreadyClaimed) {
				log.Printf("runner: poll layered: course %s layer %s already claimed, skipping", courseID, layer)
				return
			}
			if runErr != nil {
				log.Printf("runner: poll layered: create run for course %s: %v", courseID, runErr)
				return
			}

			if genErr := r.layeredRunner.GenerateLayer(
				ctx, run.ID, courseID, studentID, layer,
				meta.Topic, meta.Level, meta.Parameters,
			); genErr != nil {
				log.Printf("runner: layered generation for course %s layer %s: %v", courseID, layer, genErr)
				errMsg := genErr.Error()
				_ = r.agentRepo.SetRunStatus(ctx, run.ID, "failed", &errMsg)
				return
			}
			_ = r.agentRepo.SetRunStatus(ctx, run.ID, "completed", nil)
		}(c.CourseID, c.StudentID, layer)
	}
}

// containsRegenKeyword returns true when the feedback text contains words that
// signal the student wants the content rewritten rather than just providing
// commentary (REQ-CONTENT-004).
//
// @{"req": ["REQ-CONTENT-004"]}
func containsRegenKeyword(text string) bool {
	keywords := []string{"rewrite", "change", "redo", "update", "incorrect", "wrong", "fix", "revise", "regenerate"}
	lower := strings.ToLower(text)
	for _, kw := range keywords {
		if len(lower) >= len(kw) {
			for i := 0; i <= len(lower)-len(kw); i++ {
				if lower[i:i+len(kw)] == kw {
					return true
				}
			}
		}
	}
	return false
}

// RunContentGeneration executes the full content generation pipeline for one
// course. It respects the configured generation timeout (REQ-AGENT-014) and
// halts on API failure (REQ-AGENT-011).
//
// runID is the agent_runs row already created by the caller (dispatchFlatCourse).
// This function owns all SetRunStatus transitions for the run it receives — it
// stamps completed_at exactly once (either on failure or on success), so the
// outer goroutine must NOT call SetRunStatus again. The outer goroutine calls
// handleFailedRun / handleCompletedRun for courses-level column updates only.
//
// @{"req": ["REQ-AGENT-003", "REQ-AGENT-006", "REQ-AGENT-011", "REQ-AGENT-014"]}
func (r *AgentRunner) RunContentGeneration(ctx context.Context, runID, courseID, studentID uuid.UUID) error {
	// Apply per-generation timeout (REQ-AGENT-014).
	timeoutSecs := r.configSvc.GetInt64("content_generation_timeout_seconds")
	if timeoutSecs <= 0 {
		timeoutSecs = 3600 // 1-hour default
	}
	genCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	defer cancel()

	if err := r.agentRepo.EmitEvent(genCtx, runID, "generation_started", map[string]any{
		"course_id": courseID,
	}); err != nil {
		log.Printf("runner: emit generation_started: %v", err)
	}

	genErr := r.generateAllSections(genCtx, runID, courseID, studentID)

	// Distinguish timeout from API failure from other errors.
	// SetRunStatus is called here (inside RunContentGeneration) for every failure
	// case so completed_at is stamped exactly once and the outer goroutine does
	// not need to call SetRunStatus (see reconciliation note in dispatchFlatCourse).
	if genErr != nil {
		switch {
		case errors.Is(genCtx.Err(), context.DeadlineExceeded):
			// Timeout (REQ-AGENT-014).
			r.handleTimeout(ctx, runID, courseID, studentID)
			errMsg := "generation timeout"
			_ = r.agentRepo.SetRunStatus(ctx, runID, "failed", &errMsg)
		case errors.Is(genErr, ErrRateLimitExhausted), errors.Is(genErr, ErrTokenCapExceeded):
			// API failure or token cap (REQ-AGENT-011).
			r.handleAPIFailure(ctx, runID, courseID, studentID, genErr)
			errMsg := genErr.Error()
			_ = r.agentRepo.SetRunStatus(ctx, runID, "failed", &errMsg)
		default:
			errMsg := genErr.Error()
			_ = r.agentRepo.SetRunStatus(ctx, runID, "failed", &errMsg)
		}
		return genErr
	}

	// Transition course to 'active' using a server-role connection (§11.5).
	if cconn, cErr := db.AcquireServerConn(ctx, r.pool); cErr == nil {
		if _, transErr := cconn.Exec(ctx,
			`UPDATE courses SET status = 'active', updated_at = now() WHERE id = $1`,
			courseID,
		); transErr != nil {
			log.Printf("runner: transition course to active: %v", transErr)
		}
		cconn.Release()
	} else {
		log.Printf("runner: acquire server conn for course status update: %v", cErr)
	}

	_ = r.agentRepo.EmitEvent(ctx, runID, "generation_complete", map[string]any{
		"course_id": courseID,
	})
	_ = r.agentRepo.SetRunStatus(ctx, runID, "completed", nil)
	return nil
}

// tokenCapPreFlight checks the per-student token cap BEFORE any paid work begins
// (§9.2 / REQ-AGENT-068). Returns ErrTokenCapExceeded when the cap is set and
// already exceeded. A capped run stops immediately — zero per-section Brave calls
// are made, fixing the Brave over-spend defect described in the design.
//
// The existing per-call check in ThrottledClient.Messages remains as defence-in-depth
// for the case where token usage is written by a concurrent run between the pre-flight
// and the first section call.
//
// Uses the bare pool (not AcquireServerConn) because agent_token_usage has no RLS.
// The AND draft_id IS NULL filter restricts to the production token record.
//
// @{"req": ["REQ-AGENT-068", "REQ-SYS-074"]}
func (r *AgentRunner) tokenCapPreFlight(ctx context.Context, courseID, studentID uuid.UUID) error {
	cap := r.configSvc.GetInt64("per_student_token_limit")
	if cap <= 0 {
		return nil // cap disabled
	}
	var used int64
	err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(total_tokens_used, 0)
		 FROM agent_token_usage
		 WHERE student_id = $1 AND course_id = $2 AND draft_id IS NULL`,
		studentID, courseID,
	).Scan(&used)
	// ErrNoRows means no usage record yet — treat as zero.
	if err != nil && !isNoRows(err) {
		return err
	}
	if used >= cap {
		return ErrTokenCapExceeded
	}
	return nil
}

// handleFailedRun updates courses-level lifecycle columns after a dispatch-level
// run fails (REQ-AGENT-062/065). It does NOT call SetRunStatus — the caller must
// stamp the run 'failed' before calling this function to avoid a double UPDATE on
// agent_runs.completed_at (design §6.2 option a).
//
// Sequence:
//  1. IncrementAttemptCount — sets next_eligible_at = NOW() + backoffSeconds.
//  2. If new count >= maxAttempts: SetCourseTerminal → 'generation_failed' + events.
//  3. Else: emit generation_failed_with_retry event + notify student.
//
// @{"req": ["REQ-AGENT-062", "REQ-AGENT-065", "REQ-AGENT-066"]}
func (r *AgentRunner) handleFailedRun(ctx context.Context, runID, courseID, studentID uuid.UUID, runErr error, maxAttempts, backoffSeconds int64) {
	newCount, err := r.agentRepo.IncrementAttemptCount(ctx, courseID, backoffSeconds)
	if err != nil {
		log.Printf("runner: increment attempt count for course %s: %v", courseID, err)
		return
	}

	if int64(newCount) >= maxAttempts {
		// Max attempts exhausted — transition course to terminal 'generation_failed'.
		if termErr := r.agentRepo.SetCourseTerminal(ctx, courseID); termErr != nil {
			log.Printf("runner: set course terminal for course %s: %v", courseID, termErr)
		}
		_ = r.agentRepo.EmitEvent(ctx, runID, "generation_terminal", map[string]any{
			"course_id":      courseID,
			"attempt_number": newCount,
			"max_attempts":   maxAttempts,
		})
		_ = notify.Write(ctx, r.pool, notify.Notification{
			StudentID: studentID,
			Type:      notify.TypeGenerationFailed,
			Message:   fmt.Sprintf("Content generation for your course has failed after %d attempts and cannot be automatically retried. Please contact support.", newCount),
		})
	} else {
		_ = r.agentRepo.EmitEvent(ctx, runID, "generation_failed_with_retry", map[string]any{
			"course_id":      courseID,
			"attempt_number": newCount,
			"max_attempts":   maxAttempts,
		})
		_ = notify.Write(ctx, r.pool, notify.Notification{
			StudentID: studentID,
			Type:      notify.TypeGenerationRetrying,
			Message:   fmt.Sprintf("Content generation attempt %d of %d failed; the system will retry automatically.", newCount, maxAttempts),
		})
	}
}

// handleCompletedRun resets the courses-level attempt counter and backoff
// timestamp when a course reaches terminal SUCCESS (flat → 'active'; tree →
// tree-generation-complete). Called only on terminal success — never on
// intermediate tree_layer_generation completions (D21/REQ-AGENT-065).
//
// @{"req": ["REQ-AGENT-065", "REQ-AGENT-066"]}
func (r *AgentRunner) handleCompletedRun(ctx context.Context, runID, courseID, studentID uuid.UUID) {
	if err := r.agentRepo.ResetAttemptCount(ctx, courseID); err != nil {
		log.Printf("runner: reset attempt count for course %s: %v", courseID, err)
	}
}

// isNoRows returns true when err is the pgx no-rows sentinel. Using a helper
// avoids importing pgx directly in the runner beyond what is already imported.
func isNoRows(err error) bool {
	// pgx wraps pgx.ErrNoRows; check the error string as a last resort.
	const pgxNoRows = "no rows in result set"
	return err != nil && (err.Error() == pgxNoRows)
}

// generateAllSections iterates over the course's homework sections and generates
// content for each. Returns the first unrecoverable error encountered.
//
// @{"req": ["REQ-AGENT-003", "REQ-AGENT-005", "REQ-AGENT-009"]}
func (r *AgentRunner) generateAllSections(ctx context.Context, runID, courseID, studentID uuid.UUID) error {
	// Ensure homework/due-date entries exist (idempotent).
	if err := r.ensureDueDates(ctx, courseID, studentID); err != nil {
		return fmt.Errorf("runner: ensure due dates: %w", err)
	}

	// Load section metadata from homework table.
	type sectionMeta struct {
		Index int
		Title string
	}
	rows, err := r.pool.Query(ctx,
		`SELECT section_index, title FROM homework WHERE course_id = $1 ORDER BY section_index ASC`,
		courseID,
	)
	if err != nil {
		return fmt.Errorf("runner: load sections: %w", err)
	}
	var sections []sectionMeta
	for rows.Next() {
		var s sectionMeta
		if err := rows.Scan(&s.Index, &s.Title); err != nil {
			rows.Close()
			return err
		}
		sections = append(sections, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// Load approved syllabus for professor context.
	var syllabusAdoc string
	if err := r.pool.QueryRow(ctx,
		`SELECT content_adoc FROM syllabi WHERE course_id = $1 AND approved_at IS NOT NULL ORDER BY version DESC LIMIT 1`,
		courseID,
	).Scan(&syllabusAdoc); err != nil {
		return fmt.Errorf("runner: load syllabus: %w", err)
	}

	for _, s := range sections {
		if err := ctx.Err(); err != nil {
			return err
		}

		if emitErr := r.agentRepo.EmitEvent(ctx, runID, "section_generating", map[string]any{
			"section_index": s.Index,
			"title":         s.Title,
		}); emitErr != nil {
			log.Printf("runner: emit section_generating: %v", emitErr)
		}

		section, err := r.professor.GenerateSection(ctx, runID, courseID, studentID, s.Index, s.Title, syllabusAdoc)
		if err != nil {
			return err
		}

		if err := r.runReviewLoop(ctx, runID, courseID, studentID, section); err != nil {
			return err
		}
	}
	return nil
}

// runReviewLoop drives the review-correction cycle for one section.
// It calls reviewer.ReviewSection, and on failure increments iterations.
// When maxIterations is reached, it escalates and moves on (non-fatal).
//
// @{"req": ["REQ-AGENT-007", "REQ-AGENT-008", "REQ-CONTENT-001"]}
func (r *AgentRunner) runReviewLoop(ctx context.Context, runID, courseID, studentID uuid.UUID, section GeneratedSection) error {
	// Key must match migration 002's seed and the admin allowedKeys whitelist in config_handler.go.
	maxIterations := r.configSvc.GetInt64("correction_loop_max_iterations")
	if maxIterations <= 0 {
		// Fallback matches the migration 002 seeded default so behavior is
		// consistent whether the config row is present, absent, or mangled
		// by a direct DB edit (the admin API validates >= 1).
		maxIterations = 5
	}

	current := section
	for {
		result, err := r.reviewer.ReviewSection(ctx, runID, courseID, studentID, current.ContentID, current.ContentAdoc)
		if err != nil {
			return err
		}
		if result.Approved {
			_ = r.agentRepo.EmitEvent(ctx, runID, "section_review_passed", map[string]any{
				"section_index": current.SectionIndex,
				"content_id":    current.ContentID,
			})
			return nil
		}

		_ = r.agentRepo.EmitEvent(ctx, runID, "section_review_failed", map[string]any{
			"section_index": current.SectionIndex,
			"feedback":      result.Feedback,
		})

		iterations, iterErr := r.agentRepo.IncrementIteration(ctx, runID)
		if iterErr != nil {
			return fmt.Errorf("runner: increment iteration: %w", iterErr)
		}

		if int64(iterations) >= maxIterations {
			// REQ-AGENT-007/008: loop exhausted — escalate without blocking generation.
			r.escalate(ctx, runID, courseID, studentID, current.ContentID, iterations, result.Feedback)
			return nil
		}

		// Regenerate with professor and continue loop.
		current, err = r.professor.RegenerateSection(ctx, courseID, studentID, current.SectionIndex, result.Feedback)
		if err != nil {
			return err
		}
	}
}

// escalate emits a correction_escalated event and notifies the admin (REQ-AGENT-008).
//
// @{"req": ["REQ-AGENT-008"]}
func (r *AgentRunner) escalate(ctx context.Context, runID, courseID, studentID, contentID uuid.UUID, iterations int, feedback string) {
	_ = r.agentRepo.EmitEvent(ctx, runID, "correction_escalated", map[string]any{
		"content_id": contentID,
		"iterations": iterations,
		"feedback":   feedback,
	})

	adminID, err := r.lookupAdminID(ctx)
	if err != nil {
		log.Printf("runner: escalate: lookup admin: %v", err)
		return
	}
	_ = notify.Write(ctx, r.pool, notify.Notification{
		StudentID: adminID,
		Type:      notify.TypeAdminEscalation,
		Message: fmt.Sprintf(
			"Correction loop for course %s reached %d iterations without passing review. Last feedback: %s",
			courseID, iterations, feedback,
		),
	})
}

// handleTimeout emits a generation_timeout event and notifies the student (REQ-AGENT-014).
//
// @{"req": ["REQ-AGENT-014"]}
func (r *AgentRunner) handleTimeout(ctx context.Context, runID, courseID, studentID uuid.UUID) {
	_ = r.agentRepo.EmitEvent(ctx, runID, "generation_timeout", map[string]any{
		"course_id": courseID,
	})
	_ = notify.Write(ctx, r.pool, notify.Notification{
		StudentID: studentID,
		Type:      notify.TypeGenerationTimeout,
		Message:   "Content generation for your course has timed out. Please contact support.",
	})
}

// handleAPIFailure emits an api_failure event and notifies the student (REQ-AGENT-011).
//
// @{"req": ["REQ-AGENT-011"]}
func (r *AgentRunner) handleAPIFailure(ctx context.Context, runID, courseID, studentID uuid.UUID, apiErr error) {
	_ = r.agentRepo.EmitEvent(ctx, runID, "api_failure", map[string]any{
		"course_id": courseID,
		"error":     apiErr.Error(),
	})
	_ = notify.Write(ctx, r.pool, notify.Notification{
		StudentID: studentID,
		Type:      notify.TypeAPIFailure,
		Message:   "Content generation was halted due to an AI service error. Please try again later.",
	})
}

// ensureDueDates calls chair.AssignDueDates only when no homework rows exist yet,
// making the call idempotent across runner restarts.
//
// @{"req": ["REQ-AGENT-009"]}
func (r *AgentRunner) ensureDueDates(ctx context.Context, courseID, studentID uuid.UUID) error {
	var count int
	if err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM homework WHERE course_id = $1`, courseID,
	).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil // already assigned
	}

	var syllabusAdoc string
	if err := r.pool.QueryRow(ctx,
		`SELECT content_adoc FROM syllabi WHERE course_id = $1 AND approved_at IS NOT NULL ORDER BY version DESC LIMIT 1`,
		courseID,
	).Scan(&syllabusAdoc); err != nil {
		return fmt.Errorf("runner: load approved syllabus: %w", err)
	}

	return r.chair.AssignDueDates(ctx, courseID, studentID, syllabusAdoc)
}

// lookupAdminID returns the UUID of any active admin user for escalation.
//
// @{"req": ["REQ-AGENT-008"]}
func (r *AgentRunner) lookupAdminID(ctx context.Context) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx,
		`SELECT id FROM users WHERE role = 'admin' AND is_active = true LIMIT 1`,
	).Scan(&id)
	return id, err
}

// pollGradingQueue runs every 30s, queries submissions with grading_status = 'pending',
// and for each dispatches GradeSubmission in a goroutine.
// A server-role connection is required because submissions is RLS-protected and
// the runner has no user session; the server policy covers SELECT and UPDATE.
//
// @{"req": ["REQ-AGENT-003", "REQ-GRADE-001", "REQ-GRADE-002"]}
func (r *AgentRunner) pollGradingQueue(ctx context.Context) {
	if r.submissionRepo == nil || r.gradeSvc == nil {
		// Runner wired without grading support — skip silently.
		return
	}

	// Acquire a server-role connection so the JOIN across submissions → homework → courses
	// passes RLS. The submission repository's ListPendingGrading runs as the bare pool
	// (server writes policy), so we use the repo's built-in method directly.
	conn, err := db.AcquireServerConn(ctx, r.pool)
	if err != nil {
		log.Printf("runner: poll grading: acquire server conn: %v", err)
		return
	}

	// Load pending submissions joined with their homework rubric and course_id so
	// GradeSubmission has everything it needs without additional queries.
	// image_ids (UUID[]) is included so the grading vision path can load the
	// attached images without a separate query (REQ-AGENT-025).
	rows, err := conn.Query(ctx,
		`SELECT s.id, s.homework_id, s.student_id, c.id AS course_id,
		        s.file_path, h.rubric, s.submitted_at, s.image_ids
		 FROM submissions s
		 JOIN homework h ON h.id = s.homework_id
		 JOIN courses c ON c.id = h.course_id
		 WHERE s.grading_status = 'pending'
		 ORDER BY s.submitted_at ASC
		 LIMIT 20`,
	)
	if err != nil {
		conn.Release()
		log.Printf("runner: poll grading: query pending: %v", err)
		return
	}

	var pending []SubmissionToGrade
	for rows.Next() {
		var sub SubmissionToGrade
		if err := rows.Scan(
			&sub.ID,
			&sub.HomeworkID,
			&sub.StudentID,
			&sub.CourseID,
			&sub.FilePath,
			&sub.Rubric,
			&sub.SubmittedAt,
			&sub.ImageIDs,
		); err != nil {
			log.Printf("runner: poll grading: scan row: %v", err)
			continue
		}
		pending = append(pending, sub)
	}
	rows.Close()
	conn.Release()

	for _, sub := range pending {
		// Acquire a per-goroutine server-role connection. submissions and grades
		// both have FORCE ROW LEVEL SECURITY; the bare pool carries an empty
		// app.current_role (cleared by the AfterRelease hook), which causes every
		// write to be blocked by RLS. A dedicated connection with
		// app.current_role = 'server' satisfies the server-access policies.
		sconn, connErr := db.AcquireServerConn(ctx, r.pool)
		if connErr != nil {
			log.Printf("runner: grade submission %s: acquire server conn: %v", sub.ID, connErr)
			continue
		}
		go func(sub SubmissionToGrade, sconn *pgxpool.Conn) {
			defer sconn.Release()
			// Inject the server-role connection into context so that all
			// downstream conn(ctx) calls (submission repo, grade repo, loadTiming)
			// use the same RLS-bypassing connection instead of the bare pool.
			gradeCtx := auth.ContextWithConn(ctx, sconn)
			if err := r.GradeSubmission(gradeCtx, sub); err != nil {
				log.Printf("runner: grade submission %s: %v", sub.ID, err)
			}
		}(sub, sconn)
	}
}

// GradeSubmission calls Claude with the homework rubric and submission file
// content, parses the raw_score from the JSON response, stores it on the
// submission, and triggers grade computation via gradeSvc.
//
// When the submission has image_ids attached (REQ-AGENT-025) the grading call
// uses a multi-block user message: [imageBlock..., textBlock]. Images are
// capped at 8; the grading runner slices defensively even though the cap is
// enforced at submission time. Missing image rows (dangling UUIDs after
// account deletion) are tolerated per the SDD deletion cascade note.
//
// Claude is instructed to return ONLY {"score": <0-100>}. Any non-JSON or
// out-of-range response is treated as a grading failure and the submission is
// marked 'failed' so the grading loop does not retry it indefinitely.
//
// @{"req": ["REQ-GRADE-001", "REQ-GRADE-002", "REQ-AGENT-025"]}
func (r *AgentRunner) GradeSubmission(ctx context.Context, sub SubmissionToGrade) error {
	// Read the submission file. File content validation already happened at
	// upload time, so we only need to surface read errors here.
	fileBytes, err := os.ReadFile(sub.FilePath)
	if err != nil {
		if markErr := r.submissionRepo.MarkGradingFailed(ctx, sub.ID); markErr != nil {
			log.Printf("runner: grade submission %s: mark failed: %v", sub.ID, markErr)
		}
		return fmt.Errorf("runner: grade submission %s: read file: %w", sub.ID, err)
	}

	prompt := fmt.Sprintf(
		"You are a strict academic grader. Grade the following student submission against the rubric below.\n\n"+
			"Rubric:\n%s\n\n"+
			"Student submission:\n%s\n\n"+
			"Respond with ONLY a JSON object in the format: {\"score\": <integer 0-100>}. "+
			"Do not include any explanation, markdown, or extra text.",
		sub.Rubric,
		string(fileBytes),
	)

	// Build the user message. When images are attached, prepend vision blocks
	// before the text block so Claude sees image context first (REQ-AGENT-025).
	// Defensively cap to 8 even though the submission handler enforces this at
	// upload time; dangling UUIDs are tolerated (rows simply won't appear in
	// the GetByIDs result).
	var userContent []anthropic.ContentBlockParamUnion
	if r.imageRepo != nil && len(sub.ImageIDs) > 0 {
		imageIDs := sub.ImageIDs
		if len(imageIDs) > 8 {
			imageIDs = imageIDs[:8]
		}
		imgRows, imgErr := r.imageRepo.GetByIDs(ctx, imageIDs)
		if imgErr != nil {
			log.Printf("runner: grade submission %s: load images: %v", sub.ID, imgErr)
			// Non-fatal: fall through to text-only grading rather than failing.
		} else {
			// Warn when fewer rows come back than requested — the missing UUIDs are
			// likely dangling references from a cascade delete or a data inconsistency.
			// Grading proceeds with the available images so the student is not blocked,
			// but the discrepancy is logged so operators can investigate data quality.
			if len(imgRows) < len(imageIDs) {
				log.Printf("runner: grade submission %s: expected %d image rows, got %d (dangling UUIDs after deletion?)",
					sub.ID, len(imageIDs), len(imgRows))
			}
			userContent = append(userContent, image.ImageRowsToBlocks(imgRows)...)
		}
	}
	userContent = append(userContent, anthropic.NewTextBlock(prompt))

	msg, err := r.aiClient.Messages(ctx, sub.StudentID, sub.CourseID, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeSonnet4_6,
		MaxTokens: 256,
		Messages: []anthropic.MessageParam{
			{
				Role:    anthropic.MessageParamRoleUser,
				Content: userContent,
			},
		},
	})
	if err != nil {
		if markErr := r.submissionRepo.MarkGradingFailed(ctx, sub.ID); markErr != nil {
			log.Printf("runner: grade submission %s: mark failed after API error: %v", sub.ID, markErr)
		}
		return fmt.Errorf("runner: grade submission %s: AI call: %w", sub.ID, err)
	}

	// Extract the text block from the response. Anthropic always returns at
	// least one content block when the call succeeds.
	if len(msg.Content) == 0 {
		if markErr := r.submissionRepo.MarkGradingFailed(ctx, sub.ID); markErr != nil {
			log.Printf("runner: grade submission %s: mark failed (empty content): %v", sub.ID, markErr)
		}
		return fmt.Errorf("runner: grade submission %s: empty AI response", sub.ID)
	}
	rawText := msg.Content[0].Text

	// Parse {"score": <number>}. We treat any parse failure or out-of-range
	// value as a grading failure rather than silently accepting a bad score.
	var scorePayload struct {
		Score float64 `json:"score"`
	}
	if err := json.Unmarshal([]byte(rawText), &scorePayload); err != nil {
		if markErr := r.submissionRepo.MarkGradingFailed(ctx, sub.ID); markErr != nil {
			log.Printf("runner: grade submission %s: mark failed (bad JSON): %v", sub.ID, markErr)
		}
		return fmt.Errorf("runner: grade submission %s: parse score response %q: %w", sub.ID, rawText, err)
	}
	if scorePayload.Score < 0 || scorePayload.Score > 100 {
		if markErr := r.submissionRepo.MarkGradingFailed(ctx, sub.ID); markErr != nil {
			log.Printf("runner: grade submission %s: mark failed (score out of range): %v", sub.ID, markErr)
		}
		return fmt.Errorf("runner: grade submission %s: score %.1f out of [0,100]", sub.ID, scorePayload.Score)
	}

	// Store the raw_score on the submission and transition status to 'graded'.
	if err := r.submissionRepo.SetRawScore(ctx, sub.ID, scorePayload.Score); err != nil {
		return fmt.Errorf("runner: grade submission %s: set raw score: %w", sub.ID, err)
	}

	// Compute and persist the final grade (applies late penalty, badge effects).
	if err := r.gradeSvc.ComputeAndStoreGrade(ctx, sub.ID, sub.HomeworkID, sub.StudentID, sub.CourseID, scorePayload.Score); err != nil {
		// Grade computation failure is logged but does not mark the submission
		// as failed — the raw_score is already stored and the grade can be
		// recomputed on the next poll cycle.
		return fmt.Errorf("runner: grade submission %s: compute grade: %w", sub.ID, err)
	}

	return nil
}
