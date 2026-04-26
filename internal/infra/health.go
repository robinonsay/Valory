package infra

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

type SubsystemStatus struct {
	Database     string `json:"database"`
	AiApi        string `json:"ai_api"`
	Notification string `json:"notification"`
}

type HealthResponse struct {
	Status     string          `json:"status"`
	Subsystems SubsystemStatus `json:"subsystems"`
}

// NewHealthHandler returns an HTTP handler that reports the status of all
// registered subsystem probes. Probe functions are called with a 3-second
// timeout derived from the request context.
//
// @{"req": ["REQ-INFRA-001"]}
func NewHealthHandler(probes map[string]func(context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var ss SubsystemStatus

		runProbe := func(probe func(context.Context) error) error {
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			return probe(ctx)
		}

		// Check database subsystem
		if probe, ok := probes["database"]; ok {
			if err := runProbe(probe); err != nil {
				ss.Database = "unreachable"
			} else {
				ss.Database = "ok"
			}
		} else {
			ss.Database = "not_configured"
		}

		// Check ai_api subsystem
		if probe, ok := probes["ai_api"]; ok {
			if err := runProbe(probe); err != nil {
				ss.AiApi = "unreachable"
			} else {
				ss.AiApi = "ok"
			}
		} else {
			ss.AiApi = "not_configured"
		}

		// Check notification subsystem
		if probe, ok := probes["notification"]; ok {
			if err := runProbe(probe); err != nil {
				ss.Notification = "unreachable"
			} else {
				ss.Notification = "ok"
			}
		} else {
			ss.Notification = "not_configured"
		}

		overall := "ok"
		if ss.Database == "unreachable" || ss.AiApi == "unreachable" || ss.Notification == "unreachable" {
			overall = "degraded"
		}

		code := http.StatusOK
		if overall != "ok" {
			code = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		if err := json.NewEncoder(w).Encode(HealthResponse{Status: overall, Subsystems: ss}); err != nil {
			log.Printf("health: encode response: %v", err)
		}
	}
}
