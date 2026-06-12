// handler.go — HTTP handler for the agent module.
// Exposes server-sent event streaming (REQ-AGENT-006) for pipeline status
// and the natural language chat endpoint (REQ-AGENT-015).
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/db"
)

// AgentHandler serves the SSE event stream, chat, and chat-history endpoints.
type AgentHandler struct {
	runner    *AgentRunner
	chair     *Chair
	chatRepo  *ChatRepository
	agentRepo *AgentRepository

	// The following fields are narrow test seams. All default to nil in
	// production; handleIntakeChat falls back to the real implementations when
	// they are nil. Tests set them to avoid a live DB / Anthropic API.

	// launchSyllabus replaces the chair.GenerateSyllabus goroutine body
	// (REQ-AGENT-020 seam).
	launchSyllabus func(ctx context.Context, courseID, studentID uuid.UUID) error

	// runIntakeStep replaces chair.RunIntakeStep so tests can drive
	// handleIntakeChat through done=true without a real DB.
	runIntakeStep func(ctx context.Context, courseID, studentID uuid.UUID) (done bool, reply string, err error)

	// insertStudentMsg replaces chair.chatRepo.InsertMessage so tests bypass
	// the DB when storing the student turn.
	insertStudentMsg func(ctx context.Context, courseID uuid.UUID, role, content string) (ChatMessageRow, error)

	// updateLastMsg replaces chair.updateLastAssistantMessage so tests bypass
	// the DB sentinel-strip UPDATE.
	updateLastMsg func(ctx context.Context, courseID uuid.UUID, newContent string) error

	// transitionStatus replaces chair.transitionToSyllabusDraft so tests
	// control whether the transition succeeds without touching the DB.
	transitionStatus func(ctx context.Context, courseID uuid.UUID) error

	// ensureIntakeRunFn replaces ensureIntakeRun so tests supply a
	// deterministic run ID without a real agent_runs table.
	ensureIntakeRunFn func(ctx context.Context, courseID uuid.UUID) (uuid.UUID, error)

	// emitEvent replaces agentRepo.EmitEvent so tests capture emitted events
	// (REQ-AGENT-021) without a real pipeline_events table.
	emitEvent func(ctx context.Context, runID uuid.UUID, eventType string, payload interface{}) error
}

// @{"req": ["REQ-AGENT-006", "REQ-AGENT-015", "REQ-AGENT-017", "REQ-AGENT-018", "REQ-AGENT-019", "REQ-AGENT-020", "REQ-AGENT-021"]}
func NewAgentHandler(runner *AgentRunner, chair *Chair, chatRepo *ChatRepository, agentRepo *AgentRepository) *AgentHandler {
	return &AgentHandler{runner: runner, chair: chair, chatRepo: chatRepo, agentRepo: agentRepo}
}

// Routes mounts agent endpoints under an already-authenticated router.
// Expected mount point: /api/v1/courses/{courseID}
//
// @{"req": ["REQ-AGENT-006", "REQ-AGENT-015", "REQ-AGENT-017"]}
func (h *AgentHandler) Routes(r chi.Router) {
	r.Get("/events", h.streamEvents)
	r.Post("/chat", h.chat)
	r.Get("/chat/history", h.chatHistory)
}

// ChatMessageResponse is the wire shape returned by GET /chat/history.
//
// @{"req": ["REQ-AGENT-017"]}
type ChatMessageResponse struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

// chatHistory returns the full chat history for a course ordered ascending by
// created_at. Owner-only: same ownership gate as chat and streamEvents.
//
// RLS path chosen: the student SELECT policy on chat_messages uses
// current_setting('app.current_user_id') to filter by course ownership.
// The request-scoped conn from auth.ConnFromContext already carries that GUC
// (set by the auth middleware), so we use it for the ownership check.
// For reading the history we use the server-role pool because migration 004's
// chat_messages_server_policy only covers INSERT, not SELECT — we added a
// server SELECT policy in migration 011. Using the server pool keeps the read
// consistent with how chair.RunIntakeStep writes messages (same serverPool),
// and avoids sending chat content through the request-scoped connection whose
// GUC lifetime is tied to the HTTP request.
//
// @{"req": ["REQ-AGENT-017"]}
func (h *AgentHandler) chatHistory(w http.ResponseWriter, r *http.Request) {
	rawUserID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeAgentError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	studentID := uuid.UUID(rawUserID)

	courseID, ok := parseAgentCourseID(r)
	if !ok {
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}

	// Ownership check via request-scoped connection (carries student RLS GUCs).
	if !h.courseOwnedBy(r.Context(), courseID, studentID) {
		writeAgentError(w, http.StatusForbidden, "FORBIDDEN", "course not found")
		return
	}

	// Lazy retry: if no messages exist and intake_kickoff_sent is false the
	// kickoff goroutine previously failed. Re-trigger synchronously so the
	// student sees the opening question on first load.
	h.retryKickoffIfNeeded(r.Context(), courseID, studentID)

	// Read history using the server-role connection. The server SELECT policy
	// added in migration 011 allows this. Using server-role here is consistent
	// with how chair.go writes messages (via serverPool) and prevents the
	// request-scoped conn's GUC lifetime from constraining a potentially slow
	// history read.
	history, err := h.chair.chatRepo.GetFullHistory(r.Context(), courseID)
	if err != nil {
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	// Initialise as empty slice so json.Marshal produces [] not null when
	// there are no rows. This was a Sprint 10 production bug class.
	messages := make([]ChatMessageResponse, 0, len(history))
	for _, m := range history {
		messages = append(messages, ChatMessageResponse{
			ID:        m.ID.String(),
			Role:      m.Role,
			Content:   m.Content,
			CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}

	writeAgentJSON(w, http.StatusOK, map[string]interface{}{
		"messages": messages,
	})
}

// retryKickoffIfNeeded fires the intake opening question synchronously when
// the course has no messages yet and intake_kickoff_sent is false, meaning
// the async kickoff goroutine failed after course creation.
//
// This is the lazy-retry path described in the design contract: the history
// endpoint is the first thing the frontend calls on mount, so retrying here
// guarantees the student always sees at least one message (or an error).
//
// Race tolerance: if two concurrent history requests both see
// intake_kickoff_sent = false, both will attempt to call RunIntakeStep.
// RunIntakeStep calls InsertMessage, so two concurrent attempts could
// produce two opening messages. This is acceptable — the design doc
// explicitly notes "duplicate-question-on-race is acceptable". In practice
// the student browser makes one request at mount time, so the race window
// is narrow.
//
// Pool safety: the connection used for the SELECT is released immediately
// after the read, before invoking kickoffIntake. Holding it across the
// 10-30s Claude round-trip inside kickoffIntake would exhaust the pgx pool
// under N concurrent history requests and deadlock kickoffIntake's own
// acquisitions.
//
// @{"req": ["REQ-AGENT-018"]}
func (h *AgentHandler) retryKickoffIfNeeded(ctx context.Context, courseID, studentID uuid.UUID) {
	// Check intake_kickoff_sent using a server-role connection. Release the
	// connection immediately after the SELECT — never hold it across the
	// kickoffIntake call which makes a 10-30s Claude round-trip.
	conn, err := db.AcquireServerConn(ctx, h.chair.pool)
	if err != nil {
		log.Printf("handler: retryKickoffIfNeeded: acquire server conn: %v", err)
		return
	}

	var sent bool
	scanErr := conn.QueryRow(ctx,
		`SELECT intake_kickoff_sent FROM courses WHERE id = $1`,
		courseID,
	).Scan(&sent)
	conn.Release() // release before any downstream I/O or Claude calls
	if scanErr != nil {
		log.Printf("handler: retryKickoffIfNeeded: query: %v", scanErr)
		return
	}

	if sent {
		return // happy path — kickoff already delivered
	}

	// kickoffIntake generates the opening question and stores it. We pass
	// a background context so the read does not inherit the request deadline.
	// If this call fails, we log and return — the caller will still return
	// an empty messages array and the frontend can display a retry prompt.
	bgCtx := context.Background()
	if err := h.chair.kickoffIntake(bgCtx, courseID, studentID); err != nil {
		log.Printf("handler: retryKickoffIfNeeded: kickoff: %v", err)
	}
}

// streamEvents streams pipeline events as server-sent events (REQ-AGENT-006).
// The client may include ?after=<eventID> to resume a dropped connection from
// the last received event ID.
//
// SSE format per event:
//
//	event: <event_type>
//	data: <json payload>
//	\n
//
// @{"req": ["REQ-AGENT-006"]}
func (h *AgentHandler) streamEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAgentError(w, http.StatusInternalServerError, "STREAMING_UNSUPPORTED", "streaming not supported")
		return
	}

	rawUserID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeAgentError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	studentID := uuid.UUID(rawUserID)

	courseID, ok := parseAgentCourseID(r)
	if !ok {
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}

	if !h.courseOwnedBy(r.Context(), courseID, studentID) {
		writeAgentError(w, http.StatusForbidden, "FORBIDDEN", "course not found")
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
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering
	w.WriteHeader(http.StatusOK)

	// Send a keepalive comment immediately so the client knows the stream is open.
	fmt.Fprintf(w, ": keepalive\n\n")
	flusher.Flush()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			events, err := h.runner.GetEventsAfter(r.Context(), courseID, afterEventID, 20)
			if err != nil {
				fmt.Fprintf(w, "event: error\ndata: {\"error\":\"internal\"}\n\n")
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
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.EventType, payload)
				id := ev.ID
				afterEventID = &id
			}
			flusher.Flush()
		}
	}
}

// chat handles a single student chat turn, returning the assistant's reply
// along with the current course status (REQ-AGENT-015, REQ-AGENT-019).
//
// When the course is in "intake" status the handler routes through
// RunIntakeStep which detects the INTAKE_COMPLETE sentinel. On sentinel
// detection the handler:
//   - strips the sentinel from the reply text
//   - transitions the course to syllabus_draft
//   - emits a status_change pipeline event
//   - launches GenerateSyllabus in a background goroutine
//
// When the course is not in "intake" status the handler calls chair.Chat
// (the generic post-intake path) unchanged.
//
// @{"req": ["REQ-AGENT-015", "REQ-AGENT-019", "REQ-AGENT-020", "REQ-AGENT-021"]}
func (h *AgentHandler) chat(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)

	rawUserID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeAgentError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	studentID := uuid.UUID(rawUserID)

	courseID, ok := parseAgentCourseID(r)
	if !ok {
		writeAgentError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}

	if !h.courseOwnedBy(r.Context(), courseID, studentID) {
		writeAgentError(w, http.StatusForbidden, "FORBIDDEN", "course not found")
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

	// Resolve course status via a server-role connection (same pattern as
	// chair.courseTopic) to read the RLS-protected courses table without
	// depending on the request-scoped conn's GUC lifetime.
	status, err := h.chair.courseStatus(r.Context(), courseID)
	if err != nil {
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	if status == "intake" {
		h.handleIntakeChat(w, r, courseID, studentID, req.Message)
	} else {
		h.handleGenericChat(w, r, courseID, studentID, req.Message, status)
	}
}

// handleIntakeChat processes one turn of the intake questionnaire.
// It stores the student message, calls RunIntakeStep, strips the sentinel
// if present, transitions the course, and returns {reply, course_status}.
//
// Injectable seams (set by tests, nil in production):
//   - h.runIntakeStep    — replaces chair.RunIntakeStep
//   - h.ensureIntakeRunFn — replaces ensureIntakeRun
//   - h.emitEvent        — replaces agentRepo.EmitEvent
//   - h.launchSyllabus   — replaces the chair.GenerateSyllabus goroutine body
//
// @{"req": ["REQ-AGENT-015", "REQ-AGENT-019", "REQ-AGENT-020", "REQ-AGENT-021"]}
func (h *AgentHandler) handleIntakeChat(w http.ResponseWriter, r *http.Request, courseID, studentID uuid.UUID, message string) {
	// insertMsg resolves to the injectable seam or the real chatRepo method.
	insertMsg := h.insertStudentMsg
	if insertMsg == nil {
		insertMsg = h.chair.chatRepo.InsertMessage
	}

	// Store the student's message before calling RunIntakeStep so the history
	// passed to the Claude call includes this turn. RunIntakeStep reads
	// GetFullHistory before calling Claude; inserting first ensures the message
	// is visible in that read.
	if _, err := insertMsg(r.Context(), courseID, "student", message); err != nil {
		log.Printf("handler: intake chat: store student message: %v", err)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	// runStep resolves to the injectable seam or the real chair method.
	runStep := h.runIntakeStep
	if runStep == nil {
		runStep = h.chair.RunIntakeStep
	}
	done, reply, err := runStep(r.Context(), courseID, studentID)
	if err != nil {
		log.Printf("handler: intake chat: run intake step: %v", err)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	// emitFn resolves to the injectable seam or the real agentRepo method.
	emitFn := h.emitEvent
	if emitFn == nil {
		emitFn = h.agentRepo.EmitEvent
	}

	// ensureRunFn resolves to the injectable seam or the real method.
	ensureRunFn := h.ensureIntakeRunFn
	if ensureRunFn == nil {
		ensureRunFn = h.ensureIntakeRun
	}

	// updateMsg resolves to the injectable seam or the real chair method.
	updateMsg := h.updateLastMsg
	if updateMsg == nil {
		updateMsg = h.chair.updateLastAssistantMessage
	}

	// transitionFn resolves to the injectable seam or the real chair method.
	transitionFn := h.transitionStatus
	if transitionFn == nil {
		transitionFn = h.chair.transitionToSyllabusDraft
	}

	courseStatus := "intake"

	if done {
		// Strip the sentinel before storing and returning the reply so students
		// never see it. The sentinel may appear anywhere in the reply text.
		reply = strings.TrimSpace(strings.ReplaceAll(reply, intakeSentinel, ""))

		// Overwrite the assistant message that RunIntakeStep already stored with
		// the sentinel-stripped version so the history is clean for downstream
		// consumers (syllabus generation, future chat).
		if err := updateMsg(r.Context(), courseID, reply); err != nil {
			log.Printf("handler: intake chat: update sentinel-stripped message: %v", err)
			// Non-fatal: the sentinel is already stripped in the response; the
			// stored message may still contain it — GenerateSyllabus reads
			// history but does not look for the sentinel, so this is safe.
		}

		// Transition course status synchronously before returning so the
		// course_status field in the response reflects the new state.
		if transErr := transitionFn(r.Context(), courseID); transErr != nil {
			log.Printf("handler: intake chat: transition course: %v", transErr)
			// Continue — return intake status rather than 500 since the reply
			// was generated successfully. The frontend will see intake and retry.
		} else {
			courseStatus = "syllabus_draft"

			// Emit status_change event with the new status. We need an agent_run
			// row to attach the event to. Create one idempotently for the intake
			// lifecycle; the run is immediately completed because the intake
			// conversation itself is already over.
			intakeRunID, runErr := ensureRunFn(r.Context(), courseID)
			if runErr != nil {
				log.Printf("handler: intake chat: ensure intake run: %v", runErr)
				// Non-fatal: the SSE stream will simply not emit the event.
			} else {
				if emitErr := emitFn(r.Context(), intakeRunID, "status_change", map[string]string{
					"status": "syllabus_draft",
				}); emitErr != nil {
					log.Printf("handler: intake chat: emit status_change: %v", emitErr)
				}
			}

			// Launch GenerateSyllabus in a background goroutine. A background
			// context is used (not the request context) because the HTTP response
			// will be sent before syllabus generation completes — the request
			// context would be cancelled at response-write time, killing the
			// goroutine mid-work. The server pool connection ensures the goroutine
			// has proper server-role credentials (same pattern as runner.go
			// pollAndGenerate). This is an unregistered short-lived goroutine
			// (not in the runner's registry) because wiring it into the registry
			// would require significant plumbing for a one-shot operation.
			// Accepted risk: if the student deletes their account immediately after
			// intake completion, TerminateStudentOperations will not cancel this
			// goroutine's context. The DB write will simply fail harmlessly (the
			// course row will have been deleted).
			//
			// launchFn is injectable via h.launchSyllabus for unit tests (seam for
			// REQ-AGENT-020); production always uses chair.GenerateSyllabus.
			launchFn := h.launchSyllabus
			if launchFn == nil {
				launchFn = func(bCtx context.Context, cID, sID uuid.UUID) error {
					_, err := h.chair.GenerateSyllabus(bCtx, cID, sID)
					return err
				}
			}
			bgCtx := context.Background()
			go func(bCtx context.Context, cID, sID, runID uuid.UUID, fn func(context.Context, uuid.UUID, uuid.UUID) error) {
				if genErr := fn(bCtx, cID, sID); genErr != nil {
					log.Printf("handler: generate syllabus for course %s: %v", cID, genErr)
					// Emit an api_failure pipeline event so the SSE stream exposes
					// the failure. The course is intentionally left in syllabus_draft
					// rather than reverted — an operator can inspect and retry without
					// losing the intake data. The intake run is reused as the event
					// anchor (zero UUID means ensureIntakeRun failed earlier, so we
					// skip the emit rather than inserting a row with a bogus FK).
					if runID != (uuid.UUID{}) {
						if emitErr := emitFn(bCtx, runID, "api_failure", map[string]string{
							"error": genErr.Error(),
						}); emitErr != nil {
							log.Printf("handler: generate syllabus: emit api_failure for course %s: %v", cID, emitErr)
						}
					}
				}
			}(bgCtx, courseID, studentID, intakeRunID, launchFn)
		}
	}

	writeAgentJSON(w, http.StatusOK, map[string]string{
		"reply":         reply,
		"course_status": courseStatus,
	})
}

// handleGenericChat processes a student chat turn via chair.Chat (the generic
// post-intake path). Returns {reply, course_status} where course_status is
// the current status unchanged.
//
// @{"req": ["REQ-AGENT-015"]}
func (h *AgentHandler) handleGenericChat(w http.ResponseWriter, r *http.Request, courseID, studentID uuid.UUID, message, currentStatus string) {
	reply, err := h.chair.Chat(r.Context(), courseID, studentID, message)
	if err != nil {
		log.Printf("handler: generic chat: %v", err)
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	writeAgentJSON(w, http.StatusOK, map[string]string{
		"reply":         reply,
		"course_status": currentStatus,
	})
}

// ensureIntakeRun returns an existing completed intake run for the course, or
// creates a new one and immediately marks it completed. The run is needed as
// the foreign key anchor for the status_change pipeline event.
//
// We look for any existing intake run first (completed or running) to avoid
// creating duplicate runs. If none exists, create and complete one.
//
// @{"req": ["REQ-AGENT-019", "REQ-AGENT-021"]}
func (h *AgentHandler) ensureIntakeRun(ctx context.Context, courseID uuid.UUID) (uuid.UUID, error) {
	var runID uuid.UUID
	err := h.agentRepo.pool.QueryRow(ctx,
		`SELECT id FROM agent_runs WHERE course_id = $1 AND run_type = 'intake' ORDER BY started_at DESC LIMIT 1`,
		courseID,
	).Scan(&runID)
	if err == nil {
		return runID, nil // reuse the existing run created at kickoff
	}
	if err != pgx.ErrNoRows {
		return uuid.UUID{}, fmt.Errorf("handler: ensure intake run: query: %w", err)
	}

	// No intake run exists (e.g. kickoff failed before creating one). Create one.
	run, err := h.agentRepo.CreateRun(ctx, courseID, "intake")
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("handler: ensure intake run: create: %w", err)
	}
	if err := h.agentRepo.SetRunStatus(ctx, run.ID, "completed", nil); err != nil {
		log.Printf("handler: ensure intake run: set status completed: %v", err)
		// Non-fatal — the run is created; the status mismatch is cosmetic.
	}
	return run.ID, nil
}

// parseAgentCourseID extracts and parses the {id} URL parameter (matches the
// course router convention used in courseHandler.Routes).
//
// @{"req": ["REQ-AGENT-006", "REQ-AGENT-015", "REQ-AGENT-017"]}
func parseAgentCourseID(r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.UUID{}, false
	}
	return id, true
}

// courseOwnedBy returns true when the given course belongs to studentID.
// It uses the auth middleware's request-scoped connection (which already has
// app.current_user_id set) so the courses_student_policy RLS check passes.
// Returns false on any error (including pgx.ErrNoRows) to default to deny.
//
// @{"req": ["REQ-AGENT-006", "REQ-AGENT-015", "REQ-AGENT-017"]}
func (h *AgentHandler) courseOwnedBy(ctx context.Context, courseID, studentID uuid.UUID) bool {
	conn, ok := auth.ConnFromContext(ctx)
	if !ok {
		return false
	}
	var exists int
	err := conn.QueryRow(ctx,
		`SELECT 1 FROM courses WHERE id = $1 AND student_id = $2`,
		courseID, studentID,
	).Scan(&exists)
	if err != nil {
		if err != pgx.ErrNoRows {
			_ = err
		}
		return false
	}
	return true
}

// @{"req": ["REQ-AGENT-006", "REQ-AGENT-015", "REQ-AGENT-017", "REQ-AGENT-019", "REQ-AGENT-021"]}
func writeAgentJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// @{"req": ["REQ-AGENT-006", "REQ-AGENT-015", "REQ-AGENT-017", "REQ-AGENT-019", "REQ-AGENT-021"]}
func writeAgentError(w http.ResponseWriter, status int, code, message string) {
	writeAgentJSON(w, status, map[string]string{"error": code, "message": message})
}
