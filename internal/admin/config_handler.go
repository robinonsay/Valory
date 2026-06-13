package admin

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/valory/valory/internal/audit"
	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/email"
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
	"professor_max_tokens":               true,
	"audit_retention_days":               true,
	"notification_retention_days":        true,
	"consent_version":                    true,
	"anthropic_base_url":                 true,
	// REQ-EMAIL-001..011: SMTP configuration keys.
	"smtp_host":       true,
	"smtp_port":       true,
	"smtp_from":       true,
	"smtp_username":   true,
	"smtp_encryption": true,
	// REQ-AUTH-018: two-factor authentication toggle.
	// email_test_send_verified_at is intentionally absent: it is write-protected
	// and can only be set by the testEmailSend success path (direct DB write).
	// Allowing it here would let any admin forge the test-send prerequisite gate.
	"two_factor_enabled": true,
}

// @{"req": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003", "REQ-AUDIT-001", "REQ-GRADE-002", "REQ-GRADE-003", "REQ-EMAIL-008"]}
type AdminConfigHandler struct {
	config    *ConfigService
	auditRepo *audit.Repository
	pool      *pgxpool.Pool
	mailer    email.Mailer // nil-safe: test-send returns 503 when nil
}

// @{"req": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003", "REQ-AUDIT-001", "REQ-GRADE-002", "REQ-GRADE-003", "REQ-EMAIL-008"]}
func NewConfigHandler(config *ConfigService, auditRepo *audit.Repository, pool *pgxpool.Pool, mailer email.Mailer) *AdminConfigHandler {
	return &AdminConfigHandler{
		config:    config,
		auditRepo: auditRepo,
		pool:      pool,
		mailer:    mailer,
	}
}

// Routes registers the admin config endpoints on the provided router.
// The caller is responsible for applying RequireRole("admin") before mounting.
//
// @{"req": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003", "REQ-AUDIT-001", "REQ-GRADE-002", "REQ-GRADE-003", "REQ-EMAIL-008"]}
func (h *AdminConfigHandler) Routes(r chi.Router) {
	r.Get("/", h.getConfig)
	r.Patch("/", h.patchConfig)
	r.Post("/email/test", h.testEmailSend)
}

// updaterInfo is the JSON shape for the updated_by field in GET /config.
type updaterInfo struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// twoFAPrerequisites holds the prerequisite status for enabling the 2FA toggle.
// Included in GET /admin/config so the frontend can render the correct
// disabled-with-reason state for the toggle without a separate round-trip.
//
// @{"req": ["REQ-AUTH-018", "REQ-AUTH-021"]}
type twoFAPrerequisites struct {
	EmailConfigured      bool `json:"email_configured"`
	TestSendVerified     bool `json:"test_send_verified"`
	AdminsWithoutEmail   int  `json:"admins_without_email"`
	StudentsWithoutEmail int  `json:"students_without_email"`
}

// @{"req": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003", "REQ-ADMIN-009", "REQ-GRADE-002", "REQ-GRADE-003", "REQ-AUTH-018", "REQ-AUTH-021"]}
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

	// Build two_fa_prerequisites for the frontend toggle UI (REQ-AUTH-018).
	prereqs := h.buildTwoFAPrerequisites(ctx, configMap)

	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"config":               configMap,
		"updated_by":           updatedByOut,
		"updated_at":           updatedAtOut,
		"two_fa_prerequisites": prereqs,
	})
}

// buildTwoFAPrerequisites queries the DB and config to populate the prerequisites
// struct. Errors are treated as "not met" — worst case the admin sees a false
// negative on a transient DB error, which is safe (they cannot enable an unsafe state).
//
// @{"req": ["REQ-AUTH-018", "REQ-AUTH-021"]}
func (h *AdminConfigHandler) buildTwoFAPrerequisites(ctx context.Context, configMap map[string]string) twoFAPrerequisites {
	emailConfigured := h.mailer != nil && h.mailer.IsConfigured()

	testSendVerifiedAt := configMap["email_test_send_verified_at"]
	testSendVerified := false
	if testSendVerifiedAt != "" {
		if _, parseErr := time.Parse(time.RFC3339, testSendVerifiedAt); parseErr == nil {
			testSendVerified = true
		}
	}

	var adminsWithoutEmail int
	_ = h.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE is_active = true AND role = 'admin' AND (email IS NULL OR email = '')`,
	).Scan(&adminsWithoutEmail)

	var studentsWithoutEmail int
	_ = h.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE is_active = true AND role = 'student' AND (email IS NULL OR email = '')`,
	).Scan(&studentsWithoutEmail)

	return twoFAPrerequisites{
		EmailConfigured:      emailConfigured,
		TestSendVerified:     testSendVerified,
		AdminsWithoutEmail:   adminsWithoutEmail,
		StudentsWithoutEmail: studentsWithoutEmail,
	}
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

	// Cross-key: two_factor_enabled prerequisites (REQ-AUTH-018, REQ-AUTH-021).
	// Reject enabling 2FA unless: email is configured, a test-send has succeeded,
	// and no active admin account is missing an email address.
	if newVal, has2FA := req.Config["two_factor_enabled"]; has2FA && newVal == "true" {
		// 1. SMTP must be configured.
		if h.mailer == nil || !h.mailer.IsConfigured() {
			validationErrors = append(validationErrors,
				"two_factor_enabled: email must be configured and a test-send must have succeeded before enabling 2FA",
			)
		} else {
			// 2. A successful test-send must have been previously recorded.
			testSendVerifiedAt := h.config.GetString("email_test_send_verified_at")
			if testSendVerifiedAt == "" {
				validationErrors = append(validationErrors,
					"two_factor_enabled: email must be configured and a test-send must have succeeded before enabling 2FA",
				)
			} else if _, parseErr := time.Parse(time.RFC3339, testSendVerifiedAt); parseErr != nil {
				validationErrors = append(validationErrors,
					"two_factor_enabled: email must be configured and a test-send must have succeeded before enabling 2FA",
				)
			}
		}

		// 3. All active admin accounts must have an email address.
		var adminsWithoutEmail int
		if dbErr := h.pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM users WHERE is_active = true AND role = 'admin' AND (email IS NULL OR email = '')`,
		).Scan(&adminsWithoutEmail); dbErr == nil && adminsWithoutEmail > 0 {
			validationErrors = append(validationErrors,
				"two_factor_enabled: all active admin accounts must have an email address before enabling 2FA",
			)
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

	// Append a dedicated semantic audit event when the 2FA toggle changes so
	// security reviewers can filter for these events without parsing the generic
	// config.change payload (REQ-AUTH-022).
	if newVal, changed := req.Config["two_factor_enabled"]; changed {
		semanticAction := "2fa.config.disabled"
		if newVal == "true" {
			semanticAction = "2fa.config.enabled"
		}
		semanticEntry := audit.Entry{
			AdminID:    adminID,
			Action:     semanticAction,
			TargetType: "system_config",
			TargetID:   nil,
			Payload:    map[string]any{},
		}
		if err := h.auditRepo.Append(ctx, tx, semanticEntry); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal server error")
			return
		}
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
// @{"req": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003", "REQ-ADMIN-009", "REQ-GRADE-002", "REQ-GRADE-003", "REQ-SUBMISSION-002", "REQ-AUTH-005", "REQ-AUTH-006"]}
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
	case "professor_max_tokens":
		// Floor: a 200-line section needs well over 1024 output tokens, and
		// anything lower guarantees mid-word truncation that fails review.
		// Ceiling: the Professor uses non-streaming API calls, which risk SDK
		// HTTP timeouts above ~16K output tokens; raising the ceiling requires
		// switching GenerateSection/RegenerateSection to the streaming API.
		n, err := strconv.Atoi(value)
		if err != nil || n < 1024 || n > 16384 {
			return fmt.Errorf("professor_max_tokens must be an integer between 1024 and 16384")
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
	case "anthropic_base_url":
		// Empty string is valid: it means "use the SDK default endpoint".
		// Non-empty values must be absolute http(s) URLs with a non-empty host
		// so that option.WithBaseURL receives a well-formed target.
		// Userinfo tricks (https://api.anthropic.com@evil.com) parse with host
		// evil.com and are deliberately allowed: only admins can write this key,
		// and a redirected endpoint fails Anthropic auth rather than leaking.
		if value != "" {
			u, err := url.Parse(value)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return fmt.Errorf("anthropic_base_url must be empty or an absolute http(s) URL")
			}
		}
	// REQ-EMAIL-001..011: SMTP configuration keys.
	case "smtp_host":
		// Empty is valid — it means "SMTP not configured".
	case "smtp_port":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("smtp_port must be an integer between 1 and 65535")
		}
	case "smtp_from":
		// Any string; validated at send time by the SMTP server.
	case "smtp_username":
		// Any string; empty means no AUTH (REQ-EMAIL-003).
	case "smtp_encryption":
		switch value {
		case "none", "starttls", "tls":
			// valid
		default:
			return fmt.Errorf("smtp_encryption must be one of: none, starttls, tls")
		}
	// REQ-AUTH-018: two-factor toggle — boolean flag.
	case "two_factor_enabled":
		if value != "true" && value != "false" {
			return fmt.Errorf("two_factor_enabled must be \"true\" or \"false\"")
		}
	}
	return nil
}

// testEmailSend handles POST /api/v1/admin/config/email/test.
// It enforces a 5-per-60s rate limit per admin, sends a test message via the
// configured Mailer, records an audit event, and returns SMTP error detail
// with the password redacted.
//
// @{"req": ["REQ-EMAIL-008", "REQ-EMAIL-009", "REQ-EMAIL-010", "REQ-EMAIL-011"]}
func (h *AdminConfigHandler) testEmailSend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rawID, ok := auth.UserIDFromContext(ctx)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	adminID := uuid.UUID(rawID)

	r.Body = http.MaxBytesReader(w, r.Body, 1<<10) // 1 KiB max
	var req struct {
		To string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.To == "" {
		writeJSONError(w, http.StatusBadRequest, "to must not be empty")
		return
	}

	// Rate limit: 5 attempts per admin per 60-second window.
	if err := checkAndRecordEmailTestSend(ctx, h.pool, adminID); err != nil {
		writeJSONError(w, http.StatusTooManyRequests,
			"test-send rate limit exceeded; try again in 60 seconds")
		return
	}

	// Resolved password is needed only for redaction; fetch it once here so the
	// error sanitiser can scrub it from any SMTP error message before responding.
	// We obtain it by calling the mailer's internal resolve path indirectly via a
	// type assertion — if the mailer is an SMTPMailer we can get the password; if
	// not (NoOpMailer, mock) we redact with empty string (safe no-op).
	password := resolvedPasswordFromMailer(ctx, h.mailer)

	var sendErr error
	configured := h.mailer != nil && h.mailer.IsConfigured()
	if !configured {
		sendErr = fmt.Errorf("email is not configured")
	} else {
		subject := "Valory test email"
		body := "This is a test message sent from the Valory admin panel.\r\n"
		sendErr = h.mailer.Send(ctx, req.To, subject, body)
	}

	sendOK := sendErr == nil

	// Audit: payload contains only the to-address and outcome — no password.
	// When sendOK, also persist email_test_send_verified_at in the same
	// transaction so the 2FA toggle enable path can see the verification
	// timestamp immediately (REQ-AUTH-018, SDD-021 §4.4).
	auditEntry := audit.Entry{
		AdminID:    adminID,
		Action:     "email.test_send",
		TargetType: "system_config",
		TargetID:   nil,
		Payload:    map[string]any{"to": req.To, "ok": sendOK},
	}
	auditTx, txErr := h.pool.Begin(ctx)
	if txErr == nil {
		if err := h.auditRepo.Append(ctx, auditTx, auditEntry); err != nil {
			log.Printf("admin: email.test_send audit failed: %v", err)
			auditTx.Rollback(ctx) //nolint:errcheck
		} else {
			if sendOK {
				// Persist the verified-at timestamp so the 2FA toggle gate can confirm
				// a successful test-send occurred. adminID is the updated_by actor.
				verifiedAt := time.Now().UTC().Format(time.RFC3339)
				if _, upsertErr := auditTx.Exec(ctx,
					`UPDATE system_config SET value = $1, updated_by = $2, updated_at = NOW()
					 WHERE key = 'email_test_send_verified_at'`,
					verifiedAt, adminID,
				); upsertErr != nil {
					log.Printf("admin: email_test_send_verified_at upsert failed: %v", upsertErr)
					// Non-fatal: the audit commit still proceeds; 2FA prereq check will
					// show "not verified" until a retry succeeds.
				}
			}
			if err := auditTx.Commit(ctx); err != nil {
				log.Printf("admin: email.test_send audit commit failed: %v", err)
			}
		}
	} else {
		log.Printf("admin: email.test_send audit tx begin failed: %v", txErr)
	}

	// Reload in-memory config so the test_send_verified flag is visible immediately.
	if sendOK {
		if err := h.config.Load(ctx); err != nil {
			log.Printf("admin: config reload after test-send failed: %v", err)
		}
	}

	if sendOK {
		writeJSONResponse(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"message": "Test email sent successfully.",
		})
		return
	}

	// Sanitise the SMTP error before sending to the client (REQ-EMAIL-011).
	rawErrStr := sendErr.Error()
	safeErr := email.RedactPassword(rawErrStr, password)

	status := http.StatusBadGateway
	if !configured {
		status = http.StatusServiceUnavailable
	}
	writeJSONResponse(w, status, map[string]interface{}{
		"ok":         false,
		"smtp_error": safeErr,
	})
}

// checkAndRecordEmailTestSend atomically checks and records a test-send attempt
// for the given admin, rejecting the 6th attempt within a 60-second window.
// Uses a per-admin pg advisory lock to prevent TOCTOU bypass under concurrent
// requests (mirrors security.CheckAndRecordPasswordReset).
//
// @{"req": ["REQ-EMAIL-009"]}
func checkAndRecordEmailTestSend(ctx context.Context, pool *pgxpool.Pool, adminID uuid.UUID) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Derive a stable int64 advisory lock key from the 16-byte admin UUID by
	// XOR-ing the two halves — same derivation used in security.CheckAndRecordPasswordReset.
	lockKey := int64(binary.BigEndian.Uint64(adminID[:8])) ^ int64(binary.BigEndian.Uint64(adminID[8:]))
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey); err != nil {
		return err
	}

	var count int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM email_test_send_attempts
		 WHERE admin_id = $1 AND attempted_at > NOW() - INTERVAL '60 seconds'`,
		adminID,
	).Scan(&count); err != nil {
		return err
	}

	if count >= 5 {
		// Do not record — just signal the caller to return 429.
		return fmt.Errorf("rate limit exceeded")
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO email_test_send_attempts (admin_id) VALUES ($1)`, adminID,
	); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// resolvedPasswordFromMailer extracts the SMTP password from an SMTPMailer so
// the test-send handler can redact it from error messages before responding.
// For any other Mailer implementation (NoOpMailer, mocks) it returns "" which
// makes RedactPassword a no-op.
//
// @{"req": ["REQ-EMAIL-011"]}
func resolvedPasswordFromMailer(ctx context.Context, m email.Mailer) string {
	type passwordResolver interface {
		ResolvePassword(ctx context.Context) string
	}
	if pr, ok := m.(passwordResolver); ok {
		return pr.ResolvePassword(ctx)
	}
	return ""
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
