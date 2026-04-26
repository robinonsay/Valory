package audit

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// buildRow constructs an AuditRow whose EntryHash is computed from the provided
// prevHash so that chain tests can build a valid sequence without touching the
// database.
func buildRow(id int64, adminID uuid.UUID, action string, targetID *uuid.UUID, payload map[string]any, createdAt time.Time, prevHash string) AuditRow {
	safe := redactPayload(payload)
	pj, _ := canonicalJSON(safe)
	eh := computeEntryHash(
		prevHash,
		createdAt.UTC().Format(time.RFC3339Nano),
		adminID.String(),
		action,
		nullableUUID(targetID),
		pj,
	)
	return AuditRow{
		ID:          id,
		AdminID:     adminID,
		Action:      action,
		TargetType:  "user",
		TargetID:    targetID,
		Payload:     payload,
		PayloadJSON: pj,
		PrevHash:    prevHash,
		EntryHash:   eh,
		CreatedAt:   createdAt,
	}
}

// @{"verifies": ["REQ-AUDIT-001"]}
func TestComputeEntryHash_Deterministic(t *testing.T) {
	// TC-AUDIT-001: same inputs must always produce the same hash.
	args := [6]string{
		GenesisHash,
		"2024-01-15T10:00:00.000000000Z",
		"550e8400-e29b-41d4-a716-446655440000",
		"user.create",
		"6ba7b810-9dad-11d1-80b4-00c04fd430c8",
		`{"email":"user@example.com"}`,
	}

	h1 := computeEntryHash(args[0], args[1], args[2], args[3], args[4], args[5])
	h2 := computeEntryHash(args[0], args[1], args[2], args[3], args[4], args[5])
	if h1 != h2 {
		t.Errorf("computeEntryHash is not deterministic: got %q then %q", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("expected 64-char hex string, got length %d: %q", len(h1), h1)
	}
}

// @{"verifies": ["REQ-AUDIT-001"]}
func TestComputeEntryHash_SensitiveToEachField(t *testing.T) {
	// TC-AUDIT-001: changing any single input field must change the output hash.
	base := [6]string{
		GenesisHash,
		"2024-01-15T10:00:00.000000000Z",
		"550e8400-e29b-41d4-a716-446655440000",
		"user.create",
		"6ba7b810-9dad-11d1-80b4-00c04fd430c8",
		`{"email":"user@example.com"}`,
	}
	baseline := computeEntryHash(base[0], base[1], base[2], base[3], base[4], base[5])

	mutations := [][6]string{
		{"1111111111111111111111111111111111111111111111111111111111111111", base[1], base[2], base[3], base[4], base[5]},
		{base[0], "2024-01-15T11:00:00.000000000Z", base[2], base[3], base[4], base[5]},
		{base[0], base[1], "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", base[3], base[4], base[5]},
		{base[0], base[1], base[2], "user.modify", base[4], base[5]},
		{base[0], base[1], base[2], base[3], "ffffffff-ffff-ffff-ffff-ffffffffffff", base[5]},
		{base[0], base[1], base[2], base[3], base[4], `{"email":"other@example.com"}`},
	}

	fieldNames := []string{"prevHash", "createdAt", "adminID", "action", "targetID", "payloadJSON"}
	for i, m := range mutations {
		got := computeEntryHash(m[0], m[1], m[2], m[3], m[4], m[5])
		if got == baseline {
			t.Errorf("changing field %q did not change the hash", fieldNames[i])
		}
	}
}

// @{"verifies": ["REQ-AUDIT-002"]}
func TestVerifyChain_EmptySlice(t *testing.T) {
	// TC-AUDIT-006: empty log is trivially valid.
	valid, id := VerifyChain(nil)
	if !valid || id != 0 {
		t.Errorf("VerifyChain(nil) = (%v, %d), want (true, 0)", valid, id)
	}
	valid, id = VerifyChain([]AuditRow{})
	if !valid || id != 0 {
		t.Errorf("VerifyChain([]) = (%v, %d), want (true, 0)", valid, id)
	}
}

// @{"verifies": ["REQ-AUDIT-002"]}
func TestVerifyChain_SingleCorrectEntry(t *testing.T) {
	// TC-AUDIT-006: a single entry chained from GenesisHash passes verification.
	adminID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	targetID := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	createdAt := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)

	row := buildRow(1, adminID, "user.create", &targetID, map[string]any{"email": "a@b.com"}, createdAt, GenesisHash)

	valid, id := VerifyChain([]AuditRow{row})
	if !valid || id != 0 {
		t.Errorf("VerifyChain([single]) = (%v, %d), want (true, 0)", valid, id)
	}
}

// @{"verifies": ["REQ-AUDIT-002"]}
func TestVerifyChain_ThreeCorrectEntries(t *testing.T) {
	// TC-AUDIT-006: three correctly chained entries all pass.
	adminID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	targetID := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	t0 := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)

	r1 := buildRow(1, adminID, "user.create", &targetID, map[string]any{"email": "a@b.com"}, t0, GenesisHash)
	r2 := buildRow(2, adminID, "user.modify", &targetID, map[string]any{"role": "admin"}, t0.Add(time.Minute), r1.EntryHash)
	r3 := buildRow(3, adminID, "user.deactivate", &targetID, map[string]any{}, t0.Add(2*time.Minute), r2.EntryHash)

	valid, id := VerifyChain([]AuditRow{r1, r2, r3})
	if !valid || id != 0 {
		t.Errorf("VerifyChain([3 correct]) = (%v, %d), want (true, 0)", valid, id)
	}
}

// @{"verifies": ["REQ-AUDIT-002"]}
func TestVerifyChain_TamperedMiddleEntry(t *testing.T) {
	// TC-AUDIT-007: tampering with the middle entry's EntryHash must be detected
	// on that entry (stored hash no longer matches recomputed hash).
	adminID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	targetID := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	t0 := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)

	r1 := buildRow(1, adminID, "user.create", &targetID, map[string]any{"email": "a@b.com"}, t0, GenesisHash)
	r2 := buildRow(2, adminID, "user.modify", &targetID, map[string]any{"role": "admin"}, t0.Add(time.Minute), r1.EntryHash)
	r3 := buildRow(3, adminID, "user.deactivate", &targetID, map[string]any{}, t0.Add(2*time.Minute), r2.EntryHash)

	// Tamper: flip the stored EntryHash of the middle row.
	r2.EntryHash = strings.Repeat("a", 64)

	valid, brokenID := VerifyChain([]AuditRow{r1, r2, r3})
	if valid {
		t.Error("expected VerifyChain to detect tampered middle entry, but it returned valid=true")
	}
	// The tamper is detected on row 2 (its stored hash != recomputed hash).
	if brokenID != 2 {
		t.Errorf("expected firstBrokenID=2, got %d", brokenID)
	}
}

// @{"verifies": ["REQ-AUDIT-002"]}
func TestVerifyChain_DeletedMiddleEntry(t *testing.T) {
	// TC-AUDIT-008: when the middle entry is removed from the slice the third
	// entry's PrevHash (which pointed at the deleted entry) no longer matches
	// the actual previous row's EntryHash, so the break is detected on entry 3.
	adminID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	targetID := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	t0 := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)

	r1 := buildRow(1, adminID, "user.create", &targetID, map[string]any{"email": "a@b.com"}, t0, GenesisHash)
	r2 := buildRow(2, adminID, "user.modify", &targetID, map[string]any{"role": "admin"}, t0.Add(time.Minute), r1.EntryHash)
	r3 := buildRow(3, adminID, "user.deactivate", &targetID, map[string]any{}, t0.Add(2*time.Minute), r2.EntryHash)

	// Simulate deletion of index 1 (r2) by passing only r1 and r3.
	// When VerifyChain reaches r3 it will use r1.EntryHash as prevHash but
	// r3.PrevHash == r2.EntryHash, so the computed hash will not match r3.EntryHash.
	valid, brokenID := VerifyChain([]AuditRow{r1, r3})
	if valid {
		t.Error("expected VerifyChain to detect deleted middle entry, but it returned valid=true")
	}
	if brokenID != 3 {
		t.Errorf("expected firstBrokenID=3, got %d", brokenID)
	}
}

// @{"verifies": ["REQ-AUDIT-001"]}
func TestRedactPayload_RedactsApiKey(t *testing.T) {
	// TC-AUDIT-001: anthropic_api_key must appear as [REDACTED] in the copy.
	input := map[string]any{
		"anthropic_api_key": "sk-secret-key-123",
		"email":             "user@example.com",
		"role":              "admin",
	}
	out := redactPayload(input)

	if out["anthropic_api_key"] != "[REDACTED]" {
		t.Errorf("anthropic_api_key: got %v, want \"[REDACTED]\"", out["anthropic_api_key"])
	}
	if out["email"] != "user@example.com" {
		t.Errorf("email: got %v, want \"user@example.com\"", out["email"])
	}
	if out["role"] != "admin" {
		t.Errorf("role: got %v, want \"admin\"", out["role"])
	}
	// The original map must not be mutated.
	if input["anthropic_api_key"] != "sk-secret-key-123" {
		t.Error("redactPayload mutated the input map")
	}
}

// @{"verifies": ["REQ-AUDIT-001"]}
func TestCanonicalJSON_SortedKeys(t *testing.T) {
	// TC-AUDIT-001: keys must appear in alphabetical order.
	input := map[string]any{
		"z_last":  "value_z",
		"a_first": "value_a",
		"m_mid":   "value_m",
	}
	got, err := canonicalJSON(input)
	if err != nil {
		t.Fatalf("canonicalJSON error: %v", err)
	}

	// The positions of the keys in the JSON string must be ascending alphabetically.
	posA := strings.Index(got, `"a_first"`)
	posM := strings.Index(got, `"m_mid"`)
	posZ := strings.Index(got, `"z_last"`)

	if posA < 0 || posM < 0 || posZ < 0 {
		t.Fatalf("one or more expected keys not found in JSON: %s", got)
	}
	if !(posA < posM && posM < posZ) {
		t.Errorf("keys are not in alphabetical order in canonical JSON: %s", got)
	}
}

// @{"verifies": ["REQ-AUDIT-001"]}
func TestPrepareEntry_RedactedKeyInPayloadJSON(t *testing.T) {
	// TC-AUDIT-001: PrepareEntry must embed [REDACTED] in the stored JSON for
	// any key in redactedKeys.
	adminID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	targetID := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")

	e := Entry{
		AdminID:    adminID,
		Action:     "config.change",
		TargetType: "system_config",
		TargetID:   &targetID,
		Payload: map[string]any{
			"anthropic_api_key": "sk-prod-key-abc",
			"retry_limit":       3,
		},
	}

	createdAt := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	payloadJSON, entryHash, err := PrepareEntry(e, createdAt, GenesisHash)
	if err != nil {
		t.Fatalf("PrepareEntry error: %v", err)
	}

	if !strings.Contains(payloadJSON, `"[REDACTED]"`) {
		t.Errorf("payloadJSON does not contain [REDACTED]: %s", payloadJSON)
	}
	if strings.Contains(payloadJSON, "sk-prod-key-abc") {
		t.Errorf("payloadJSON contains raw API key: %s", payloadJSON)
	}
	if entryHash == "" {
		t.Error("entryHash is empty")
	}
	if len(entryHash) != 64 {
		t.Errorf("entryHash length %d, want 64", len(entryHash))
	}
}
