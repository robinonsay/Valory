package badge

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/valory/valory/internal/auth"
)

// Handler exposes badge-related HTTP endpoints.
type Handler struct {
	svc *Service
}

// @{"req": ["REQ-BADGE-001", "REQ-BADGE-002", "REQ-BADGE-003"]}
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Routes mounts badge endpoints on the provided router.
//
//   GET /badges          — list all available badge definitions (any authenticated user)
//   GET /users/me/badges — list the caller's earned badges (student)
//
// @{"req": ["REQ-BADGE-001", "REQ-BADGE-002", "REQ-BADGE-003"]}
func (h *Handler) Routes(r chi.Router) {
	r.Get("/badges", h.listBadges)
	r.Get("/users/me/badges", h.myBadges)
}

// listBadges returns all badge definitions as a JSON array.
//
// @{"req": ["REQ-BADGE-001", "REQ-BADGE-002", "REQ-BADGE-003"]}
func (h *Handler) listBadges(w http.ResponseWriter, r *http.Request) {
	_, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}

	badges, err := h.svc.ListBadges(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	// Map to response structs; never expose internal row types directly to
	// callers so the API shape can evolve independently of the storage model.
	type badgeResponse struct {
		ID          string  `json:"id"`
		Name        string  `json:"name"`
		Description string  `json:"description"`
		Milestone   string  `json:"milestone"`
		Reward      string  `json:"reward"`
		RewardValue float64 `json:"reward_value"`
		CreatedAt   string  `json:"created_at"`
	}

	items := make([]badgeResponse, 0, len(badges))
	for _, b := range badges {
		items = append(items, badgeResponse{
			ID:          b.ID.String(),
			Name:        b.Name,
			Description: b.Description,
			Milestone:   b.Milestone,
			Reward:      b.Reward,
			RewardValue: b.RewardValue,
			CreatedAt:   b.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}

	writeJSON(w, http.StatusOK, items)
}

// myBadges returns the authenticated student's earned badges as a JSON array.
//
// @{"req": ["REQ-BADGE-001", "REQ-BADGE-002", "REQ-BADGE-003"]}
func (h *Handler) myBadges(w http.ResponseWriter, r *http.Request) {
	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	studentID := uuid.UUID(rawID)

	earned, err := h.svc.GetStudentBadges(r.Context(), studentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	type studentBadgeResponse struct {
		ID                      string   `json:"id"`
		StudentID               string   `json:"student_id"`
		BadgeID                 string   `json:"badge_id"`
		AwardedAt               string   `json:"awarded_at"`
		RedeemedForSubmissionID *string  `json:"redeemed_for_submission_id"`
		BadgeName               string   `json:"badge_name"`
		BadgeReward             string   `json:"badge_reward"`
		BadgeRewardValue        float64  `json:"badge_reward_value"`
	}

	items := make([]studentBadgeResponse, 0, len(earned))
	for _, sb := range earned {
		var redeemedID *string
		if sb.RedeemedForSubmissionID != nil {
			s := sb.RedeemedForSubmissionID.String()
			redeemedID = &s
		}
		items = append(items, studentBadgeResponse{
			ID:                      sb.ID.String(),
			StudentID:               sb.StudentID.String(),
			BadgeID:                 sb.BadgeID.String(),
			AwardedAt:               sb.AwardedAt.Format("2006-01-02T15:04:05Z07:00"),
			RedeemedForSubmissionID: redeemedID,
			BadgeName:               sb.BadgeName,
			BadgeReward:             sb.BadgeReward,
			BadgeRewardValue:        sb.BadgeRewardValue,
		})
	}

	writeJSON(w, http.StatusOK, items)
}

// writeJSON serializes v as JSON and writes it with the given status code.
//
// @{"req": ["REQ-BADGE-001", "REQ-BADGE-002", "REQ-BADGE-003"]}
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

// writeError writes a structured JSON error response.
//
// @{"req": ["REQ-BADGE-001", "REQ-BADGE-002", "REQ-BADGE-003"]}
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
		"error":   code,
		"message": message,
	})
}
