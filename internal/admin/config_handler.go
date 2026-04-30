package admin

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/valory/valory/internal/audit"
	"github.com/valory/valory/internal/auth"
)

// allowedKeys is the complete set of keys that may be modified via the PATCH
// endpoint. Any key absent from this set causes an immediate 400.
var allowedKeys = map[string]bool{
	"agent_retry_limit":                  true,
	"correction_loop_max_iterations":     true,
	"per_student_token_limit":            true,
	"late_penalty_rate":                  true,
	"homework_weight":                    true,
	"project_weight":                     true,
	"session_inactivity_seconds":         true,
	"account_lockout_seconds":            true,
	"max_upload_bytes":                   true,
	"content_generation_timeout_seconds": true,
	"audit_retention_days":               true,
	"notification_retention_days":        true,
	"consent_version":                    true,
}

// @{"req": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003", "REQ-AUDIT-001", "REQ-GRADE-002", "REQ-GRADE-003"]}
type AdminConfigHandler struct {
	config    *ConfigService
	auditRepo *audit.Repository
	pool      *pgxpool.Pool
}

// @{"req": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003", "REQ-AUDIT-001", "REQ-GRADE-002", "REQ-GRADE-003"]}
func NewConfigHandler(config *ConfigService, auditRepo *audit.Repository, pool *pgxpool.Pool) *AdminConfigHandler {
	return &AdminConfigHandler{
		config:    config,
		auditRepo: auditRepo,
		pool:      pool,
	}
}

// Routes registers the admin config endpoints on the provided router.
// The caller is responsible for applying RequireRole("admin") before mounting.
//
// @{"req": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003", "REQ-AUDIT-001", "REQ-GRADE-002", "REQ-GRADE-003"]}
func (h *AdminConfigHandler) Routes(r chi.Router) {
	r.Get("/", h.getConfig)
	r.Patch("/", h.patchConfig)
}

// updaterInfo is the JSON shape for the updated_by field in GET /config.
type updaterInfo struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// @{"req": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003", "REQ-GRADE-002", "REQ-GRADE-003"]}
func (h *AdminConfigHandler) getConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rows, err := h.pool.Query(ctx, "SELECT key, value FROM system_config")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rows.Close()

	configMap := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		configMap[key] = value
	}
	if err := rows.Err(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Query the most recent update metadata. updated_by is NULL for seed-only rows,
	// so we use pointer types and omit the fields when no admin has yet written a row.
	var updatedByID *uuid.UUID
	var updatedByUsername *string
	var updatedAt *time.Time

	err = h.pool.QueryRow(ctx, `
		SELECT s.updated_by, s.updated_at, u.username
		FROM system_config s
		LEFT JOIN users u ON u.id = s.updated_by
		ORDER BY s.updated_at DESC
		LIMIT 1
	`).Scan(&updatedByID, &updatedAt, &updatedByUsername)

	var updatedByOut *updaterInfo
	var updatedAtOut *time.Time

	// pgx returns a nil pointer for NULL UUID columns; ErrNoRows means the table
	// is empty entirely. Both cases leave updated_by/updated_at as null in the response.
	if err == nil && updatedByID != nil {
		name := ""
		if updatedByUsername != nil {
			name = *updatedByUsername
		}
		updatedByOut = &updaterInfo{ID: updatedByID.String(), Username: name}
		updatedAtOut = updatedAt
	}

	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"config":     configMap,
		"updated_by": updatedByOut,
		"updated_at": updatedAtOut,
	})
}

// @{"req": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003", "REQ-AUDIT-001", "REQ-GRADE-002", "REQ-GRADE-003", "REQ-SUBMISSION-002", "REQ-AUTH-005", "REQ-AUTH-006"]}
func (h *AdminConfigHandler) patchConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rawID, ok := auth.UserIDFromContext(ctx)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	adminID := uuid.UUID(rawID)

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)

	var req struct {
		Config map[string]string `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Config) == 0 {
		writeJSONError(w, http.StatusBadRequest, "config map is required and must not be empty")
		return
	}

	// Unknown-key check — reject immediately before any other validation.
	for k := range req.Config {
		if !allowedKeys[k] {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("unknown config key: %s", k))
			return
		}
	}

	// Collect all validation errors before returning.
	var validationErrors []string

	for k, v := range req.Config {
		if err := validateConfigValue(k, v); err != nil {
			validationErrors = append(validationErrors, err.Error())
		}
	}

	// Cross-key: if either weight is in the request, verify the effective sum equals 1.
	_, hasHW := req.Config["homework_weight"]
	_, hasPW := req.Config["project_weight"]
	if hasHW || hasPW {
		hwStr := req.Config["homework_weight"]
		if hwStr == "" {
			hwStr = h.config.GetString("homework_weight")
		}
		pwStr := req.Config["project_weight"]
		if pwStr == "" {
			pwStr = h.config.GetString("project_weight")
		}
		hw, hwErr := strconv.ParseFloat(hwStr, 64)
		pw, pwErr := strconv.ParseFloat(pwStr, 64)
		if hwErr == nil && pwErr == nil {
			if math.Abs(hw+pw-1.0) >= 0.001 {
				validationErrors = append(validationErrors,
					"homework_weight: homework_weight + project_weight must equal 1.0",
					"project_weight: homework_weight + project_weight must equal 1.0",
				)
			}
		}
		// If either parse failed the per-key validation already appended an error;
		// skip the cross-key check so we don't double-report.
	}

	if len(validationErrors) > 0 {
		sort.Strings(validationErrors)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"validation_errors": validationErrors,
		})
		return
	}

	// Build a sorted slice of changed keys for the audit payload.
	changedKeys := make([]string, 0, len(req.Config))
	for k := range req.Config {
		changedKeys = append(changedKeys, k)
	}
	sort.Strings(changedKeys)

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	// Roll back if we return before an explicit commit.
	defer tx.Rollback(ctx) //nolint:errcheck

	for _, k := range changedKeys {
		v := req.Config[k]
		if _, err := tx.Exec(ctx,
			`UPDATE system_config SET value = $1, updated_by = $2, updated_at = now() WHERE key = $3`,
			v, adminID, k,
		); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal server error")
			return
		}
	}

	auditEntry := audit.Entry{
		AdminID:    adminID,
		Action:     "config.change",
		TargetType: "system_config",
		TargetID:   nil,
		Payload: map[string]any{
			"keys_changed": changedKeys,
		},
	}
	if err := h.auditRepo.Append(ctx, tx, auditEntry); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Reload in-memory config after the transaction commits so subsequent reads
	// reflect the new values without delay.
	if err := h.config.Load(ctx); err != nil {
		log.Printf("admin: config reload failed after PATCH (DB write succeeded): %v", err)
	}

	fullConfig := h.config.Snapshot()

	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"updated_keys": changedKeys,
		"config":       fullConfig,
	})
}

// validateConfigValue checks a single key/value pair against the validation
// rules table. It returns a descriptive error when the value is invalid.
//
// @{"req": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003", "REQ-GRADE-002", "REQ-GRADE-003", "REQ-SUBMISSION-002", "REQ-AUTH-005", "REQ-AUTH-006"]}
func validateConfigValue(key, value string) error {
	switch key {
	case "agent_retry_limit":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			return fmt.Errorf("agent_retry_limit must be an integer >= 1")
		}
	case "correction_loop_max_iterations":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			return fmt.Errorf("correction_loop_max_iterations must be an integer >= 1")
		}
	case "per_student_token_limit":
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 0 {
			return fmt.Errorf("per_student_token_limit must be an integer >= 0")
		}
	case "late_penalty_rate":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil || f < 0.0 || f > 1.0 {
			return fmt.Errorf("late_penalty_rate must be a float between 0.0 and 1.0 inclusive")
		}
	case "homework_weight":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil || f <= 0.0 || f > 1.0 {
			return fmt.Errorf("homework_weight must be a float > 0.0 and <= 1.0")
		}
	case "project_weight":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil || f <= 0.0 || f > 1.0 {
			return fmt.Errorf("project_weight must be a float > 0.0 and <= 1.0")
		}
	case "session_inactivity_seconds":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			return fmt.Errorf("session_inactivity_seconds must be an integer >= 1")
		}
	case "account_lockout_seconds":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			return fmt.Errorf("account_lockout_seconds must be an integer >= 1")
		}
	case "max_upload_bytes":
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 1024 {
			return fmt.Errorf("max_upload_bytes must be an integer >= 1024")
		}
	case "content_generation_timeout_seconds":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			return fmt.Errorf("content_generation_timeout_seconds must be an integer >= 1")
		}
	case "audit_retention_days":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			return fmt.Errorf("audit_retention_days must be an integer >= 1")
		}
	case "notification_retention_days":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			return fmt.Errorf("notification_retention_days must be an integer >= 1")
		}
	case "consent_version":
		if value == "" {
			return fmt.Errorf("consent_version must be a non-empty string")
		}
	}
	return nil
}

// @{"req": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003", "REQ-AUDIT-001", "REQ-GRADE-002", "REQ-GRADE-003", "REQ-SUBMISSION-002", "REQ-AUTH-005", "REQ-AUTH-006"]}
func writeJSONResponse(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// @{"req": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003", "REQ-AUDIT-001", "REQ-GRADE-002", "REQ-GRADE-003", "REQ-SUBMISSION-002", "REQ-AUTH-005", "REQ-AUTH-006"]}
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
