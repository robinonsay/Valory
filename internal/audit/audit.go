package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

// GenesisHash is the sentinel prev_hash written into the first audit log entry.
// It is all-zero hex so that chain verification can identify the chain head
// without a separate out-of-band value.
const GenesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

// redactedKeys is the compile-time set of payload keys whose values must never
// be stored in plaintext. The set is intentionally unexported so that callers
// cannot widen it at runtime. Extend this list whenever a new credential or
// secret field can appear in audit payloads.
var redactedKeys = map[string]bool{
	"anthropic_api_key": true,
	"password":          true,
	"password_hash":     true,
	"token":             true,
	"token_hash":        true,
	"reset_token":       true,
	"smtp_password":     true,
}

// Entry is the data the caller provides when appending a new audit record.
//
// @{"req": ["REQ-AUDIT-001"]}
type Entry struct {
	AdminID    uuid.UUID
	Action     string     // e.g. "user.create", "user.modify", "user.deactivate", "user.activate", "user.delete", "config.change"
	TargetType string     // e.g. "user", "system_config"
	TargetID   *uuid.UUID // nil for config.change actions with no single target row
	Payload    map[string]any // changed fields (new values only); sensitive keys auto-redacted
}

// AuditRow is a row read back from the audit_log table.
//
// @{"req": ["REQ-AUDIT-001", "REQ-AUDIT-002"]}
type AuditRow struct {
	ID          int64
	AdminID     uuid.UUID
	Action      string
	TargetType  string
	TargetID    *uuid.UUID
	Payload     map[string]any
	PayloadJSON string // canonical JSON, sorted keys
	PrevHash    string
	EntryHash   string
	CreatedAt   time.Time
}

// nullableUUID returns the string form of id when non-nil, or the empty string
// when id is nil. This produces a stable, unambiguous contribution to the hash
// input for entries that have no target row (e.g. config.change).
func nullableUUID(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

// computeEntryHash returns the SHA-256 hex digest of the six string fields that
// uniquely identify a log entry's content and position in the chain.
//
// Fields are written sequentially to the hasher without a separator.
// createdAt must be formatted as RFC 3339 Nano UTC by the caller.
// targetID must be "" when the TargetID pointer is nil.
// payloadJSON must be canonical (keys sorted) — the caller is responsible.
//
// @{"req": ["REQ-AUDIT-001", "REQ-AUDIT-002"]}
func computeEntryHash(prevHash, createdAt, adminID, action, targetID, payloadJSON string) string {
	h := sha256.New()
	// Write each field in a fixed order; no separator is used because the fields
	// themselves have deterministic lengths or termination (UUIDs, hex strings,
	// RFC 3339 timestamps) that prevent ambiguous concatenations in practice.
	fmt.Fprint(h, prevHash)
	fmt.Fprint(h, createdAt)
	fmt.Fprint(h, adminID)
	fmt.Fprint(h, action)
	fmt.Fprint(h, targetID)
	fmt.Fprint(h, payloadJSON)
	return hex.EncodeToString(h.Sum(nil))
}

// canonicalJSON marshals v to JSON with keys sorted alphabetically so that two
// maps with the same content always produce identical byte sequences.
//
// @{"req": ["REQ-AUDIT-001"]}
func canonicalJSON(v map[string]any) (string, error) {
	if v == nil {
		return "{}", nil
	}

	// Collect and sort the keys so the output is deterministic regardless of
	// the iteration order of the input map.
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	ordered := make(map[string]any, len(v))
	for _, k := range keys {
		ordered[k] = v[k]
	}

	// encoding/json does not guarantee key order for map[string]any in Go 1.23
	// but the standard library actually sorts map keys during Marshal as of
	// Go 1.12+.  We re-build the map anyway to be explicit about our intent.
	b, err := json.Marshal(ordered)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// redactPayload returns a shallow copy of payload where every key that appears
// in redactedKeys has its value replaced with the literal string "[REDACTED]".
// The original map is never mutated.
//
// @{"req": ["REQ-AUDIT-001"]}
func redactPayload(payload map[string]any) map[string]any {
	out := make(map[string]any, len(payload))
	for k, v := range payload {
		if redactedKeys[k] {
			out[k] = "[REDACTED]"
		} else {
			out[k] = v
		}
	}
	return out
}

// ComputeHash re-derives the SHA-256 entry hash for row using the same inputs
// that PrepareEntry used when the row was originally written. It is exported so
// that the repository and tests can use it to verify stored entries without
// duplicating the hashing logic.
//
// @{"req": ["REQ-AUDIT-002"]}
func ComputeHash(prevHash string, row AuditRow) (string, error) {
	createdAt := row.CreatedAt.UTC().Format(time.RFC3339Nano)
	adminID := row.AdminID.String()
	targetID := nullableUUID(row.TargetID)
	return computeEntryHash(prevHash, createdAt, adminID, row.Action, targetID, row.PayloadJSON), nil
}

// VerifyChain walks rows (expected to be sorted by id ASC) and checks that each
// stored EntryHash matches the hash recomputed from its fields. The first row
// must have PrevHash == GenesisHash; every subsequent row's PrevHash must equal
// the preceding row's EntryHash.
//
// Returns (true, 0) when the chain is intact or the slice is empty.
// Returns (false, id) identifying the first row where the check fails.
//
// @{"req": ["REQ-AUDIT-002"]}
func VerifyChain(rows []AuditRow) (valid bool, firstBrokenID int64) {
	prevHash := GenesisHash
	for _, row := range rows {
		expected, err := ComputeHash(prevHash, row)
		if err != nil || expected != row.EntryHash {
			return false, row.ID
		}
		prevHash = row.EntryHash
	}
	return true, 0
}

// ChainVerifier maintains rolling hash-chain state so callers can verify the
// chain incrementally without buffering the full log in memory. Intended for
// use with Repository.StreamAll:
//
//	v := audit.NewChainVerifier()
//	err := repo.StreamAll(ctx, func(row audit.AuditRow) error { v.Push(row); return nil })
//	valid, brokenID := v.Done()
//
// @{"req": ["REQ-AUDIT-002"]}
type ChainVerifier struct {
	prevHash  string
	broken    bool
	brokenID  int64
}

// NewChainVerifier returns a verifier initialised at the genesis hash.
func NewChainVerifier() *ChainVerifier {
	return &ChainVerifier{prevHash: GenesisHash}
}

// Push checks one row against the rolling chain state. Once a broken link is
// detected all subsequent rows are ignored.
func (v *ChainVerifier) Push(row AuditRow) {
	if v.broken {
		return
	}
	expected, err := ComputeHash(v.prevHash, row)
	if err != nil || expected != row.EntryHash {
		v.broken = true
		v.brokenID = row.ID
		return
	}
	v.prevHash = row.EntryHash
}

// Done returns the verification result. Call after all rows have been pushed.
// Returns (true, 0) when the chain is intact or no rows were pushed.
func (v *ChainVerifier) Done() (valid bool, firstBrokenID int64) {
	if v.broken {
		return false, v.brokenID
	}
	return true, 0
}

// PrepareEntry redacts sensitive payload keys, computes canonical JSON, and
// derives the entry hash. It is called by the repository layer immediately
// before inserting a new row so that the hash and JSON string stored in the
// database are produced by a single, authoritative code path.
//
// @{"req": ["REQ-AUDIT-001"]}
func PrepareEntry(e Entry, createdAt time.Time, prevHash string) (payloadJSON, entryHash string, err error) {
	safe := redactPayload(e.Payload)
	payloadJSON, err = canonicalJSON(safe)
	if err != nil {
		return "", "", err
	}

	createdAtStr := createdAt.UTC().Format(time.RFC3339Nano)
	adminID := e.AdminID.String()
	targetID := nullableUUID(e.TargetID)

	entryHash = computeEntryHash(prevHash, createdAtStr, adminID, e.Action, targetID, payloadJSON)
	return payloadJSON, entryHash, nil
}
