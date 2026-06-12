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
	submissionRepo submissionRepository
	gradeSvc       gradeService
	aiClient       aiMessenger
	imageRepo      *image.Repository // nil-safe: grading falls back to text-only when nil
	configSvc      interface {
		GetInt64(string) int64
		GetFloat64(string) float64
	}
}

// @{"req": ["REQ-AGENT-003", "REQ-AGENT-006", "REQ-AGENT-007", "REQ-AGENT-008", "REQ-AGENT-011", "REQ-AGENT-013", "REQ-AGENT-014"]}
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
//
// It blocks until ctx is cancelled.
//
// @{"req": ["REQ-AGENT-003", "REQ-AGENT-010"]}
func (r *AgentRunner) Start(ctx context.Context) {
	genTicker := time.NewTicker(30 * time.Second)
	fbTicker := time.NewTicker(60 * time.Second)
	gradeTicker := time.NewTicker(30 * time.Second)
	defer genTicker.Stop()
	defer fbTicker.Stop()
	defer gradeTicker.Stop()

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

// pollAndGenerate queries for courses with approved syllabi that have no
// content_generation run and starts one for each.
//
// @{"req": ["REQ-AGENT-003"]}
func (r *AgentRunner) pollAndGenerate(ctx context.Context) {
	courses, err := r.agentRepo.ListUntriggeredApprovals(ctx)
	if err != nil {
		log.Printf("runner: poll: list untriggered approvals: %v", err)
		return
	}
	for _, c := range courses {
		go func(courseID, studentID uuid.UUID) {
			if err := r.RunContentGeneration(ctx, courseID, studentID); err != nil {
				log.Printf("runner: content generation for course %s: %v", courseID, err)
			}
		}(c.CourseID, c.StudentID)
	}
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
	rows, err := conn.Query(ctx,
		`SELECT sf.id, sf.student_id, sf.course_id, sf.section_index, sf.feedback_text
		 FROM section_feedback sf
		 JOIN courses c ON c.id = sf.course_id
		 WHERE sf.regeneration_triggered = false
		   AND c.status = 'active'
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
// @{"req": ["REQ-AGENT-003", "REQ-AGENT-006", "REQ-AGENT-011", "REQ-AGENT-014"]}
func (r *AgentRunner) RunContentGeneration(ctx context.Context, courseID, studentID uuid.UUID) error {
	// Apply per-generation timeout (REQ-AGENT-014).
	timeoutSecs := r.configSvc.GetInt64("content_generation_timeout_seconds")
	if timeoutSecs <= 0 {
		timeoutSecs = 3600 // 1-hour default
	}
	genCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	defer cancel()

	run, err := r.agentRepo.CreateRun(ctx, courseID, "content_generation")
	if err != nil {
		return fmt.Errorf("runner: create run: %w", err)
	}

	if err := r.agentRepo.EmitEvent(genCtx, run.ID, "generation_started", map[string]any{
		"course_id": courseID,
	}); err != nil {
		log.Printf("runner: emit generation_started: %v", err)
	}

	genErr := r.generateAllSections(genCtx, run.ID, courseID, studentID)

	// Distinguish timeout from API failure from other errors.
	if genErr != nil {
		switch {
		case errors.Is(genCtx.Err(), context.DeadlineExceeded):
			// Timeout (REQ-AGENT-014).
			r.handleTimeout(ctx, run.ID, courseID, studentID)
			errMsg := "generation timeout"
			_ = r.agentRepo.SetRunStatus(ctx, run.ID, "failed", &errMsg)
		case errors.Is(genErr, ErrRateLimitExhausted), errors.Is(genErr, ErrTokenCapExceeded):
			// API failure or token cap (REQ-AGENT-011).
			r.handleAPIFailure(ctx, run.ID, courseID, studentID, genErr)
			errMsg := genErr.Error()
			_ = r.agentRepo.SetRunStatus(ctx, run.ID, "failed", &errMsg)
		default:
			errMsg := genErr.Error()
			_ = r.agentRepo.SetRunStatus(ctx, run.ID, "failed", &errMsg)
		}
		return genErr
	}

	// Transition course to 'active' using a server-role connection.
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

	_ = r.agentRepo.EmitEvent(ctx, run.ID, "generation_complete", map[string]any{
		"course_id": courseID,
	})
	_ = r.agentRepo.SetRunStatus(ctx, run.ID, "completed", nil)
	return nil
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
