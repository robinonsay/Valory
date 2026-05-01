// handler.go — HTTP handler for the agent module.
// Exposes server-sent event streaming (REQ-AGENT-006) for pipeline status
// and the natural language chat endpoint (REQ-AGENT-015).
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/valory/valory/internal/auth"
)

// AgentHandler serves the SSE event stream and chat endpoints.
type AgentHandler struct {
	runner   *AgentRunner
	chair    *Chair
	chatRepo *ChatRepository
}

// @{"req": ["REQ-AGENT-006", "REQ-AGENT-015"]}
func NewAgentHandler(runner *AgentRunner, chair *Chair, chatRepo *ChatRepository) *AgentHandler {
	return &AgentHandler{runner: runner, chair: chair, chatRepo: chatRepo}
}

// Routes mounts agent endpoints under an already-authenticated router.
// Expected mount point: /api/v1/courses/{courseID}
//
// @{"req": ["REQ-AGENT-006", "REQ-AGENT-015"]}
func (h *AgentHandler) Routes(r chi.Router) {
	r.Get("/events", h.streamEvents)
	r.Post("/chat", h.chat)
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
					"id":          ev.ID,
					"agent_run_id": ev.AgentRunID,
					"event_type":  ev.EventType,
					"payload":     json.RawMessage(ev.Payload),
					"emitted_at":  ev.EmittedAt.Format(time.RFC3339Nano),
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
// (REQ-AGENT-015).
//
// @{"req": ["REQ-AGENT-015"]}
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

	reply, err := h.chair.Chat(r.Context(), courseID, studentID, req.Message)
	if err != nil {
		writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	writeAgentJSON(w, http.StatusOK, map[string]string{
		"reply": reply,
	})
}

// parseAgentCourseID extracts and parses the {id} URL parameter (matches the
// course router convention used in courseHandler.Routes).
//
// @{"req": ["REQ-AGENT-006", "REQ-AGENT-015"]}
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
// @{"req": ["REQ-AGENT-006", "REQ-AGENT-015"]}
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

// @{"req": ["REQ-AGENT-006", "REQ-AGENT-015"]}
func writeAgentJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// @{"req": ["REQ-AGENT-006", "REQ-AGENT-015"]}
func writeAgentError(w http.ResponseWriter, status int, code, message string) {
	writeAgentJSON(w, status, map[string]string{"error": code, "message": message})
}
