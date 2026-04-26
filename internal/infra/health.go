package infra

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// anthropicClient is a package-level client shared across health checks to
// avoid repeatedly allocating transports. The 5-second timeout matches the
// SDD specification for the Anthropic reachability probe.
var anthropicClient = &http.Client{Timeout: 5 * time.Second}

// @{"req": ["REQ-INFRA-001"]}
type SubsystemStatus struct {
	Postgres  string `json:"postgres"`
	Anthropic string `json:"anthropic"`
	Disk      string `json:"disk"`
}

// @{"req": ["REQ-INFRA-001"]}
type HealthResponse struct {
	Status     string          `json:"status"`
	Subsystems SubsystemStatus `json:"subsystems"`
}

// NewHealthHandler returns an HTTP handler that reports the status of the three
// core subsystems (postgres, anthropic, disk) in parallel via a sync.WaitGroup.
// If all three report "ok", the response is HTTP 200; otherwise HTTP 503.
//
// @{"req": ["REQ-INFRA-001"]}
func NewHealthHandler(db *pgxpool.Pool, uploadsDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var ss SubsystemStatus
		var wg sync.WaitGroup

		// check runs fn in a goroutine, writing "ok" or "degraded" into dest.
		check := func(dest *string, fn func() error) {
			defer wg.Done()
			if err := fn(); err != nil {
				*dest = "degraded"
			} else {
				*dest = "ok"
			}
		}

		wg.Add(3)
		go check(&ss.Postgres, func() error { return checkPostgres(r.Context(), db) })
		go check(&ss.Anthropic, func() error { return checkAnthropic(r.Context()) })
		go check(&ss.Disk, func() error { return checkDisk(uploadsDir) })
		wg.Wait()

		overall := "ok"
		if ss.Postgres != "ok" || ss.Anthropic != "ok" || ss.Disk != "ok" {
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

// checkPostgres pings the database with a 3-second timeout.
func checkPostgres(ctx context.Context, db *pgxpool.Pool) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return db.Ping(ctx)
}

// checkAnthropic performs a HEAD request to the Anthropic API to verify
// network reachability. Any HTTP response (including 4xx/5xx) proves the
// network path is open; only transport-level errors indicate unreachability.
// No API key is sent — authentication is not required for this probe.
func checkAnthropic(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, "https://api.anthropic.com", nil)
	if err != nil {
		return err
	}
	resp, err := anthropicClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// checkDisk writes and then deletes a temporary file under uploadsDir to
// verify that the uploads directory is writable.
func checkDisk(uploadsDir string) error {
	f, err := os.CreateTemp(uploadsDir, "health-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(filepath.Clean(name))
}
