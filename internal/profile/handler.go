package profile

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valory/valory/internal/auth"
)

// profileResponse is the wire shape for GET /api/v1/profile and
// PUT /api/v1/profile responses.
//
// @{"req": ["REQ-PROFILE-004", "REQ-PROFILE-016"]}
type profileResponse struct {
	StudentID string    `json:"student_id"`
	Summary   string    `json:"summary"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Handler serves the profile and onboarding HTTP endpoints. The handler is
// student-authenticated: the auth middleware has already set the RLS GUCs on
// the request-scoped connection before any handler method runs.
//
// @{"req": ["REQ-PROFILE-004", "REQ-PROFILE-011", "REQ-PROFILE-012", "REQ-PROFILE-014", "REQ-PROFILE-015", "REQ-PROFILE-016"]}
type Handler struct {
	repo       *ProfileRepository
	svc        *OnboardingService
	serverPool *pgxpool.Pool
}

// @{"req": ["REQ-PROFILE-004", "REQ-PROFILE-011", "REQ-PROFILE-012", "REQ-PROFILE-014", "REQ-PROFILE-015"]}
func NewHandler(repo *ProfileRepository, svc *OnboardingService, serverPool *pgxpool.Pool) *Handler {
	return &Handler{repo: repo, svc: svc, serverPool: serverPool}
}

// Routes mounts the profile and onboarding endpoints on the provided router.
// All routes require a student-authenticated session (enforced by the enclosing
// group in main.go); the auth middleware injects the request-scoped connection.
//
// @{"req": ["REQ-PROFILE-004", "REQ-PROFILE-011", "REQ-PROFILE-014", "REQ-PROFILE-015", "REQ-PROFILE-016"]}
func (h *Handler) Routes(r chi.Router) {
	r.Get("/", h.getProfile)
	r.Put("/", h.putProfile)
	r.Post("/onboarding/start", h.onboardingStart)
	r.Post("/onboarding/advance", h.onboardingAdvance)
	r.Post("/onboarding/complete", h.onboardingComplete)
	r.Post("/onboarding/skip", h.onboardingSkip)
}

// getProfile returns the authenticated student's current learning profile.
// Returns 404 when no profile row exists yet.
//
// @{"req": ["REQ-PROFILE-004"]}
func (h *Handler) getProfile(w http.ResponseWriter, r *http.Request) {
	studentID, ok := studentIDFromRequest(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}

	// Use the request-scoped connection so FORCE RLS uses the student's GUCs.
	row, err := h.repo.GetProfile(r.Context(), studentID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "no profile found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, profileResponse{
		StudentID: row.StudentID.String(),
		Summary:   row.Summary,
		Source:    row.Source,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	})
}

// putProfile creates or replaces the authenticated student's profile with
// source='manual_edit'. Validates that summary is 1-2000 characters.
//
// @{"req": ["REQ-PROFILE-004", "REQ-PROFILE-016"]}
func (h *Handler) putProfile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)

	studentID, ok := studentIDFromRequest(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}

	var req struct {
		Summary string `json:"summary"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}

	if len(req.Summary) == 0 || len(req.Summary) > 2000 {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "summary must be 1-2000 characters")
		return
	}

	// Use the request-scoped connection so the student RLS write policy applies.
	row, err := h.repo.UpsertProfile(r.Context(), studentID, req.Summary, "manual_edit")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, profileResponse{
		StudentID: row.StudentID.String(),
		Summary:   row.Summary,
		Source:    row.Source,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	})
}

// onboardingStart clears any existing partial session and asks the opening
// onboarding question. Returns the first question text.
//
// @{"req": ["REQ-PROFILE-011", "REQ-PROFILE-016"]}
func (h *Handler) onboardingStart(w http.ResponseWriter, r *http.Request) {
	studentID, ok := studentIDFromRequest(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}

	firstMessage, err := h.svc.Start(r.Context(), studentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"session_active": true,
		"first_message":  firstMessage,
	})
}

// onboardingAdvance stores the student's message and returns the next
// question (or closing acknowledgment). When done=true the client should
// call /complete.
//
// @{"req": ["REQ-PROFILE-011"]}
func (h *Handler) onboardingAdvance(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)

	studentID, ok := studentIDFromRequest(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}

	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if req.Message == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "message is required")
		return
	}

	reply, done, err := h.svc.Advance(r.Context(), studentID, req.Message)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"reply": reply,
		"done":  done,
	})
}

// onboardingComplete triggers the summarization call, stores the profile, and
// marks the student as onboarding-prompted. Returns the generated summary.
//
// @{"req": ["REQ-PROFILE-012", "REQ-PROFILE-014"]}
func (h *Handler) onboardingComplete(w http.ResponseWriter, r *http.Request) {
	studentID, ok := studentIDFromRequest(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}

	summary, err := h.svc.Complete(r.Context(), studentID)
	if err != nil {
		// Use the package sentinel (errors.Is unwraps the chain) rather than
		// comparing error strings, which is fragile and breaks on wrapping.
		if errors.Is(err, ErrNoActiveSession) {
			writeError(w, http.StatusConflict, "NO_ACTIVE_SESSION", "no onboarding session in progress")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"summary": summary,
	})
}

// onboardingSkip marks the student as onboarding-prompted without creating a
// profile. The onboarding nudge will not reappear on subsequent logins.
//
// @{"req": ["REQ-PROFILE-015"]}
func (h *Handler) onboardingSkip(w http.ResponseWriter, r *http.Request) {
	studentID, ok := studentIDFromRequest(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}

	if err := h.repo.SetOnboardingPrompted(r.Context(), studentID); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"skipped": true,
	})
}

// studentIDFromRequest extracts the authenticated student's UUID from the
// request context. Returns false when the user ID is absent (unauthenticated).
//
// @{"req": ["REQ-PROFILE-004"]}
func studentIDFromRequest(r *http.Request) (uuid.UUID, bool) {
	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		return uuid.UUID{}, false
	}
	return uuid.UUID(rawID), true
}

// @{"req": ["REQ-PROFILE-004"]}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// @{"req": ["REQ-PROFILE-004"]}
func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": code, "message": msg})
}
