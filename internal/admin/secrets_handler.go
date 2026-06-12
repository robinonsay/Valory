package admin

// secrets_handler.go — HTTP handlers for GET/PUT/DELETE /api/v1/admin/secrets/{name}.
//
// All three endpoints require admin role + CSRF (enforced by the router group in
// main.go — handlers here do not duplicate that check).
//
// Security invariants enforced here:
//   - Values are NEVER returned in any response body (REQ-SECURITY-008).
//   - Only knownSecrets names are accepted; others receive 400 (allowlist).
//   - Audit entries carry only {name}, no value or last4 (REQ-SECURITY-008).
//   - Encryption and DB write are in the same transaction; no partial writes.

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/valory/valory/internal/audit"
	"github.com/valory/valory/internal/auth"
)

// secretMeta is the shape returned by GET and PUT — never contains a value.
//
// @{"req": ["REQ-ADMIN-007", "REQ-SECURITY-008"]}
type secretMeta struct {
	Name       string     `json:"name"`
	Configured bool       `json:"configured"`
	Last4      string     `json:"last4"`
	UpdatedAt  *time.Time `json:"updated_at"`
	Source     string     `json:"source"` // "managed" | "env" | "none"
}

// SecretsHandler handles the /api/v1/admin/secrets routes.
//
// @{"req": ["REQ-ADMIN-005", "REQ-ADMIN-006", "REQ-ADMIN-007", "REQ-ADMIN-008", "REQ-SECURITY-008"]}
type SecretsHandler struct {
	provider  *SecretProvider
	auditRepo *audit.Repository
	pool      *pgxpool.Pool
}

// NewSecretsHandler constructs a SecretsHandler.
//
// @{"req": ["REQ-ADMIN-005", "REQ-ADMIN-006", "REQ-ADMIN-007", "REQ-ADMIN-008"]}
func NewSecretsHandler(provider *SecretProvider, auditRepo *audit.Repository, pool *pgxpool.Pool) *SecretsHandler {
	return &SecretsHandler{
		provider:  provider,
		auditRepo: auditRepo,
		pool:      pool,
	}
}

// Routes registers the secrets endpoints on r.
// The caller must apply RequireRole("admin") and CSRFMiddleware before mounting.
//
// @{"req": ["REQ-ADMIN-005", "REQ-ADMIN-006", "REQ-ADMIN-007", "REQ-ADMIN-008", "REQ-SECURITY-008"]}
func (h *SecretsHandler) Routes(r chi.Router) {
	r.Get("/", h.listSecrets)
	r.Put("/{name}", h.putSecret)
	r.Delete("/{name}", h.deleteSecret)
}

// listSecrets handles GET /api/v1/admin/secrets.
// Returns metadata for every known secret name. Values are never included.
//
// @{"req": ["REQ-ADMIN-005", "REQ-ADMIN-006", "REQ-ADMIN-007", "REQ-SECURITY-008"]}
func (h *SecretsHandler) listSecrets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	type managedRow struct {
		last4     string
		updatedAt time.Time
	}

	// Fetch any existing rows for the known names.
	rows, err := h.pool.Query(ctx,
		`SELECT name, last4, updated_at FROM managed_secrets WHERE name = ANY($1)`,
		knownNames(),
	)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rows.Close()

	managed := make(map[string]managedRow)
	for rows.Next() {
		var name string
		var mr managedRow
		if err := rows.Scan(&name, &mr.last4, &mr.updatedAt); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		managed[name] = mr
	}
	if err := rows.Err(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	metas := make([]secretMeta, 0, len(knownSecrets))
	for name := range knownSecrets {
		var meta secretMeta
		if mr, ok := managed[name]; ok {
			at := mr.updatedAt
			meta = secretMeta{
				Name:       name,
				Configured: true,
				Last4:      mr.last4,
				UpdatedAt:  &at,
				Source:     "managed",
			}
		} else {
			// No managed row; classify by env var presence (never return value).
			meta = secretMeta{Name: name, Source: "none"}
			if os.Getenv(envVarName(name)) != "" {
				meta.Source = "env"
			}
		}
		metas = append(metas, meta)
	}

	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"secrets": metas,
	})
}

// putSecret handles PUT /api/v1/admin/secrets/{name}.
// Validates, encrypts, upserts, writes an audit entry (all in one transaction),
// then invalidates the provider cache.  Returns the new metadata (no value).
//
// @{"req": ["REQ-ADMIN-005", "REQ-ADMIN-006", "REQ-ADMIN-007", "REQ-ADMIN-008", "REQ-SECURITY-008"]}
func (h *SecretsHandler) putSecret(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := chi.URLParam(r, "name")

	if !knownSecrets[name] {
		writeJSONError(w, http.StatusBadRequest, "unknown secret name")
		return
	}

	rawID, ok := auth.UserIDFromContext(ctx)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	adminID := uuid.UUID(rawID)

	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Value == "" {
		writeJSONError(w, http.StatusBadRequest, "value must not be empty")
		return
	}
	if len(req.Value) > 512 {
		writeJSONError(w, http.StatusUnprocessableEntity, "value must not exceed 512 characters")
		return
	}

	// Managed secrets are disabled when VALORY_SECRET_KEY is absent/invalid.
	if h.provider.kek == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "managed secrets are disabled: VALORY_SECRET_KEY not configured")
		return
	}

	ct, nonce, err := gcmEncrypt(h.provider.kek, []byte(req.Value))
	if err != nil {
		log.Printf("secrets: encrypt failed for %q: %v", name, err)
		writeJSONError(w, http.StatusInternalServerError, "encryption failed")
		return
	}

	// last4: final 4 runes of the plaintext value (safe for UI badge display).
	runes := []rune(req.Value)
	start := len(runes) - 4
	if start < 0 {
		start = 0
	}
	last4 := string(runes[start:])

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var updatedAt time.Time
	err = tx.QueryRow(ctx,
		`INSERT INTO managed_secrets (name, ciphertext, nonce, last4, updated_at, updated_by)
		 VALUES ($1, $2, $3, $4, now(), $5)
		 ON CONFLICT (name) DO UPDATE
		   SET ciphertext  = EXCLUDED.ciphertext,
		       nonce       = EXCLUDED.nonce,
		       last4       = EXCLUDED.last4,
		       updated_at  = now(),
		       updated_by  = EXCLUDED.updated_by
		 RETURNING updated_at`,
		name, ct, nonce, last4, adminID,
	).Scan(&updatedAt)
	if err != nil {
		log.Printf("secrets: upsert failed for %q: %v", name, err)
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Audit payload contains only the secret name — no value, no last4.
	auditEntry := audit.Entry{
		AdminID:    adminID,
		Action:     "secret.set",
		TargetType: "managed_secrets",
		TargetID:   nil,
		Payload:    map[string]any{"name": name},
	}
	if err := h.auditRepo.Append(ctx, tx, auditEntry); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Cache invalidation happens after the commit so a concurrent Get that
	// races between Invalidate and the next resolve sees the fresh row.
	h.provider.Invalidate(name)

	meta := secretMeta{
		Name:       name,
		Configured: true,
		Last4:      last4,
		UpdatedAt:  &updatedAt,
		Source:     "managed",
	}
	writeJSONResponse(w, http.StatusOK, meta)
}

// deleteSecret handles DELETE /api/v1/admin/secrets/{name}.
// Always returns 204 — even when no row existed — to avoid leaking existence
// information (idempotent; REQ-SECURITY-008).
//
// @{"req": ["REQ-ADMIN-005", "REQ-ADMIN-006", "REQ-ADMIN-008", "REQ-SECURITY-008"]}
func (h *SecretsHandler) deleteSecret(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := chi.URLParam(r, "name")

	if !knownSecrets[name] {
		writeJSONError(w, http.StatusBadRequest, "unknown secret name")
		return
	}

	rawID, ok := auth.UserIDFromContext(ctx)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	adminID := uuid.UUID(rawID)

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// DELETE is unconditional — we do not inspect rowsAffected to avoid leaking
	// whether the row existed before this call.
	if _, err := tx.Exec(ctx,
		`DELETE FROM managed_secrets WHERE name = $1`, name,
	); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	auditEntry := audit.Entry{
		AdminID:    adminID,
		Action:     "secret.clear",
		TargetType: "managed_secrets",
		TargetID:   nil,
		Payload:    map[string]any{"name": name},
	}
	if err := h.auditRepo.Append(ctx, tx, auditEntry); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	h.provider.Invalidate(name)

	w.WriteHeader(http.StatusNoContent)
}

// knownNames returns the names slice for SQL $ANY(...) queries.
func knownNames() []string {
	names := make([]string, 0, len(knownSecrets))
	for n := range knownSecrets {
		names = append(names, n)
	}
	return names
}

// envVarName maps a secret name to its environment variable counterpart.
// "brave_api_key" -> "BRAVE_API_KEY", etc.
func envVarName(name string) string {
	switch name {
	case "anthropic_api_key":
		return "ANTHROPIC_API_KEY"
	case "brave_api_key":
		return "BRAVE_API_KEY"
	default:
		// Unreachable for names that passed the knownSecrets gate.
		return ""
	}
}
