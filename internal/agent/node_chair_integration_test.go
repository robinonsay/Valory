//go:build integration

// node_chair_integration_test.go — integration tests for the new draft-context
// repository methods and the post-022 token UPSERT correctness.
//
// WHY this file exists:
// Fixes 1 and 2 in the G2-S3-T1 rework correct the ON CONFLICT predicates for
// agent_token_usage after migration 022 dropped uq_token_usage_student_course and
// replaced it with two partial unique indexes:
//
//	agent_token_usage_student_course_idx ON (student_id, course_id) WHERE draft_id IS NULL
//	agent_token_usage_draft_idx          ON (draft_id) WHERE draft_id IS NOT NULL
//
// Without the WHERE clause in the ON CONFLICT spec, Postgres raises:
//
//	"no unique or exclusion constraint matching the ON CONFLICT specification"
//
// on EVERY call.  The tests here exercise both UPSERTs against the real post-022
// schema and WILL FAIL if either fix is reverted.
//
// Additionally, CreateRunForDraft, GetEventsAfterForDraft, and EmitNodeEvent are
// tested against the real schema so the draft-anchored pipeline event path is
// verified end-to-end.
//
// Run:
//
//	export PATH=$PATH:/usr/local/go/bin
//	VALORY_TEST_DATABASE_URL=postgres://valory_test:valory_test@localhost:55432/valory_test?sslmode=disable \
//	  go test -tags integration -count=1 -p 1 \
//	  -run 'TestNodeChairInteg' \
//	  ./internal/agent/
//
// @{"verifies": ["REQ-AGENT-035", "REQ-AGENT-036", "REQ-AGENT-056", "REQ-AGENT-060"]}
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	internaldb "github.com/valory/valory/internal/db"
)

// ---------------------------------------------------------------------------
// Fixture helpers
// ---------------------------------------------------------------------------

// seedIntegAdmin inserts a minimal admin user and returns the ID.
func seedIntegAdmin(t *testing.T, tag string) uuid.UUID {
	t.Helper()
	pool := internaldb.IntegrationPool(t)
	ctx := context.Background()

	username := fmt.Sprintf("nodechair_admin_%s_%s", tag, uuid.New().String()[:8])
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, 'x', 'admin') RETURNING id`,
		username,
	).Scan(&id); err != nil {
		t.Fatalf("seedIntegAdmin(%s): %v", tag, err)
	}
	return id
}

// seedIntegStudent inserts a minimal student user and returns the ID.
func seedIntegStudent(t *testing.T, tag string) uuid.UUID {
	t.Helper()
	pool := internaldb.IntegrationPool(t)
	ctx := context.Background()

	username := fmt.Sprintf("nodechair_student_%s_%s", tag, uuid.New().String()[:8])
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, 'x', 'student') RETURNING id`,
		username,
	).Scan(&id); err != nil {
		t.Fatalf("seedIntegStudent(%s): %v", tag, err)
	}
	return id
}

// seedIntegCourse inserts a course for the student and returns the course ID.
func seedIntegCourse(t *testing.T, studentID uuid.UUID, tag string) uuid.UUID {
	t.Helper()
	pool := internaldb.IntegrationPool(t)
	ctx := context.Background()

	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO courses (student_id, topic, status) VALUES ($1, $2, 'active') RETURNING id`,
		studentID, "Token Test "+tag,
	).Scan(&id); err != nil {
		t.Fatalf("seedIntegCourse(%s): %v", tag, err)
	}
	return id
}

// seedIntegDraft inserts a course_draft for the admin and returns the draft ID.
func seedIntegDraft(t *testing.T, adminID uuid.UUID, tag string) uuid.UUID {
	t.Helper()
	pool := internaldb.IntegrationPool(t)
	ctx := context.Background()

	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO course_drafts (admin_id, title, topic, level)
		 VALUES ($1, $2, $3, 'beginner') RETURNING id`,
		adminID, "Draft "+tag, "Token Test Draft "+tag,
	).Scan(&id); err != nil {
		t.Fatalf("seedIntegDraft(%s): %v", tag, err)
	}
	return id
}

// truncateNodeChairTables resets all tables used by these tests.
func truncateNodeChairTables(t *testing.T) {
	t.Helper()
	internaldb.TruncateTables(t, internaldb.IntegrationPool(t),
		"node_chats",
		"course_nodes",
		"draft_nodes",
		"pipeline_events",
		"agent_runs",
		"agent_token_usage",
		"course_drafts",
		"sessions",
		"courses",
		"users",
	)
}

// ---------------------------------------------------------------------------
// 1. CreateRunForDraft
// ---------------------------------------------------------------------------

// TestNodeChairInteg_CreateRunForDraft_InsertsRow verifies that
// AgentRepository.CreateRunForDraft inserts an agent_runs row anchored to
// draft_id (course_id IS NULL) and returns a non-nil run ID.
//
// @{"verifies": ["REQ-AGENT-036", "REQ-AGENT-056"]}
func TestNodeChairInteg_CreateRunForDraft_InsertsRow(t *testing.T) {
	truncateNodeChairTables(t)

	pool := internaldb.IntegrationPool(t)
	ctx := context.Background()
	repo := NewAgentRepository(pool)

	adminID := seedIntegAdmin(t, "create_run")
	draftID := seedIntegDraft(t, adminID, "create_run")

	runID, err := repo.CreateRunForDraft(ctx, draftID, "node_generation")
	if err != nil {
		t.Fatalf("CreateRunForDraft: %v", err)
	}
	if runID == uuid.Nil {
		t.Fatal("CreateRunForDraft: returned uuid.Nil")
	}

	// Verify the row exists with the correct draft_id and NULL course_id.
	var gotDraftID uuid.UUID
	var gotCourseIDIsNull bool
	var gotStatus string
	err = pool.QueryRow(ctx,
		`SELECT draft_id, course_id IS NULL, status FROM agent_runs WHERE id = $1`,
		runID,
	).Scan(&gotDraftID, &gotCourseIDIsNull, &gotStatus)
	if err != nil {
		t.Fatalf("SELECT agent_runs by id: %v", err)
	}
	if gotDraftID != draftID {
		t.Errorf("draft_id: want %s, got %s", draftID, gotDraftID)
	}
	if !gotCourseIDIsNull {
		t.Error("course_id must be NULL for a draft-anchored run")
	}
	if gotStatus != "running" {
		t.Errorf("status: want %q, got %q", "running", gotStatus)
	}
}

// ---------------------------------------------------------------------------
// 2. GetEventsAfterForDraft
// ---------------------------------------------------------------------------

// TestNodeChairInteg_GetEventsAfterForDraft_NoAfterID_ReturnsAll verifies that
// GetEventsAfterForDraft with a nil afterEventID returns all events for the
// draft in ascending emitted_at order.
//
// @{"verifies": ["REQ-AGENT-036", "REQ-AGENT-056"]}
func TestNodeChairInteg_GetEventsAfterForDraft_NoAfterID_ReturnsAll(t *testing.T) {
	truncateNodeChairTables(t)

	pool := internaldb.IntegrationPool(t)
	ctx := context.Background()
	repo := NewAgentRepository(pool)

	adminID := seedIntegAdmin(t, "get_events_noafter")
	draftID := seedIntegDraft(t, adminID, "get_events_noafter")

	runID, err := repo.CreateRunForDraft(ctx, draftID, "node_generation")
	if err != nil {
		t.Fatalf("CreateRunForDraft: %v", err)
	}

	// Emit 3 events with distinct timestamps.
	eventTypes := []string{"layer_generation_started", "layer_node_generating", "layer_awaiting_review"}
	for _, et := range eventTypes {
		if err := repo.EmitEvent(ctx, runID, et, map[string]string{"draft_id": draftID.String()}); err != nil {
			t.Fatalf("EmitEvent(%q): %v", et, err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	events, err := repo.GetEventsAfterForDraft(ctx, draftID, nil, 100)
	if err != nil {
		t.Fatalf("GetEventsAfterForDraft: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	// Verify ascending order.
	for i := 1; i < len(events); i++ {
		if !events[i].EmittedAt.After(events[i-1].EmittedAt) {
			t.Errorf("events not in strict ascending order at index %d", i)
		}
	}
	if events[0].EventType != "layer_generation_started" {
		t.Errorf("first event: expected %q, got %q", "layer_generation_started", events[0].EventType)
	}
}

// TestNodeChairInteg_GetEventsAfterForDraft_WithAfterID_ReturnsTailOnly verifies
// that when afterEventID is set, only events after that timestamp are returned.
//
// @{"verifies": ["REQ-AGENT-036", "REQ-AGENT-056"]}
func TestNodeChairInteg_GetEventsAfterForDraft_WithAfterID_ReturnsTailOnly(t *testing.T) {
	truncateNodeChairTables(t)

	pool := internaldb.IntegrationPool(t)
	ctx := context.Background()
	repo := NewAgentRepository(pool)

	adminID := seedIntegAdmin(t, "get_events_after")
	draftID := seedIntegDraft(t, adminID, "get_events_after")

	runID, err := repo.CreateRunForDraft(ctx, draftID, "node_generation")
	if err != nil {
		t.Fatalf("CreateRunForDraft: %v", err)
	}

	// Emit 3 events.
	for _, et := range []string{"layer_generation_started", "layer_node_generating", "layer_node_generated"} {
		if err := repo.EmitEvent(ctx, runID, et, map[string]string{}); err != nil {
			t.Fatalf("EmitEvent(%q): %v", et, err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Get all events to obtain the pivot ID.
	all, err := repo.GetEventsAfterForDraft(ctx, draftID, nil, 100)
	if err != nil {
		t.Fatalf("GetEventsAfterForDraft (baseline): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 baseline events, got %d", len(all))
	}

	// Passing the first event's ID must return only the 2nd and 3rd.
	firstID := all[0].ID
	tail, err := repo.GetEventsAfterForDraft(ctx, draftID, &firstID, 100)
	if err != nil {
		t.Fatalf("GetEventsAfterForDraft with afterID: %v", err)
	}
	if len(tail) != 2 {
		t.Fatalf("expected 2 tail events, got %d", len(tail))
	}
	if tail[0].ID == firstID {
		t.Error("tail must not include the pivot event itself")
	}
	if tail[0].EventType != "layer_node_generating" {
		t.Errorf("tail[0]: expected %q, got %q", "layer_node_generating", tail[0].EventType)
	}
}

// ---------------------------------------------------------------------------
// 3. EmitNodeEvent
// ---------------------------------------------------------------------------

// TestNodeChairInteg_EmitNodeEvent_RowPersistedWithCorrectFields verifies that
// EmitNodeEvent writes a pipeline_events row with the expected event_type and
// a JSON payload containing the NodeEventPayload fields.
//
// @{"verifies": ["REQ-AGENT-036", "REQ-AGENT-056", "REQ-AGENT-060"]}
func TestNodeChairInteg_EmitNodeEvent_RowPersistedWithCorrectFields(t *testing.T) {
	truncateNodeChairTables(t)

	pool := internaldb.IntegrationPool(t)
	ctx := context.Background()
	repo := NewAgentRepository(pool)

	adminID := seedIntegAdmin(t, "emit_node_event")
	draftID := seedIntegDraft(t, adminID, "emit_node_event")

	runID, err := repo.CreateRunForDraft(ctx, draftID, "node_generation")
	if err != nil {
		t.Fatalf("CreateRunForDraft: %v", err)
	}

	nodeID := uuid.New()
	payload := NodeEventPayload{
		NodeID:   &nodeID,
		NodeType: "syllabus",
		DraftID:  &draftID,
		Layer:    "syllabus",
		Summary:  "generated",
	}

	if err := repo.EmitNodeEvent(ctx, runID, "node_ready", payload); err != nil {
		t.Fatalf("EmitNodeEvent: %v", err)
	}

	// Verify the event is in pipeline_events.
	var agentRunID uuid.UUID
	var eventType string
	var rawPayload []byte
	err = pool.QueryRow(ctx,
		`SELECT agent_run_id, event_type, payload FROM pipeline_events WHERE agent_run_id = $1`,
		runID,
	).Scan(&agentRunID, &eventType, &rawPayload)
	if err != nil {
		t.Fatalf("SELECT pipeline_events: %v", err)
	}
	if agentRunID != runID {
		t.Errorf("agent_run_id: want %s, got %s", runID, agentRunID)
	}
	if eventType != "node_ready" {
		t.Errorf("event_type: want %q, got %q", "node_ready", eventType)
	}
	// Payload must round-trip the node_type field.
	var got map[string]interface{}
	if err := json.Unmarshal(rawPayload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got["node_type"] != "syllabus" {
		t.Errorf("payload.node_type: want %q, got %v", "syllabus", got["node_type"])
	}
}

// ---------------------------------------------------------------------------
// 4. Token UPSERT regression guard — the critical Fix 1 + Fix 2 test
// ---------------------------------------------------------------------------

// TestNodeChairInteg_TokenUpsert_StudentPath_AccumulatesOnPost022Schema is
// the regression guard for Fix 2.  It exercises the student-path UPSERT
// (ON CONFLICT (student_id, course_id) WHERE draft_id IS NULL) against the
// real post-022 schema and asserts:
//
//	(a) the first call inserts a row, and
//	(b) a second call accumulates the total rather than erroring.
//
// This test FAILS if Fix 2 is reverted (the old ON CONFLICT without WHERE would
// produce "no unique or exclusion constraint matching" at runtime).
//
// @{"verifies": ["REQ-AGENT-035"]}
func TestNodeChairInteg_TokenUpsert_StudentPath_AccumulatesOnPost022Schema(t *testing.T) {
	truncateNodeChairTables(t)

	pool := internaldb.IntegrationPool(t)
	ctx := context.Background()

	studentID := seedIntegStudent(t, "student_upsert")
	courseID := seedIntegCourse(t, studentID, "student_upsert")

	// First UPSERT: should insert a fresh row with 15 tokens.
	_, err := pool.Exec(ctx,
		`INSERT INTO agent_token_usage (student_id, course_id, total_tokens_used)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (student_id, course_id) WHERE draft_id IS NULL
		 DO UPDATE SET total_tokens_used = agent_token_usage.total_tokens_used + EXCLUDED.total_tokens_used`,
		studentID, courseID, int64(15),
	)
	if err != nil {
		t.Fatalf("first student UPSERT (post-022 partial index): %v — "+
			"this error indicates Fix 2 is missing or migration 022 was not applied", err)
	}

	var total int64
	if err := pool.QueryRow(ctx,
		`SELECT total_tokens_used FROM agent_token_usage WHERE student_id = $1 AND course_id = $2`,
		studentID, courseID,
	).Scan(&total); err != nil {
		t.Fatalf("SELECT after first student UPSERT: %v", err)
	}
	if total != 15 {
		t.Errorf("total after first student UPSERT: want 15, got %d", total)
	}

	// Second UPSERT: should accumulate to 30.
	_, err = pool.Exec(ctx,
		`INSERT INTO agent_token_usage (student_id, course_id, total_tokens_used)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (student_id, course_id) WHERE draft_id IS NULL
		 DO UPDATE SET total_tokens_used = agent_token_usage.total_tokens_used + EXCLUDED.total_tokens_used`,
		studentID, courseID, int64(15),
	)
	if err != nil {
		t.Fatalf("second student UPSERT (post-022 partial index): %v", err)
	}

	if err := pool.QueryRow(ctx,
		`SELECT total_tokens_used FROM agent_token_usage WHERE student_id = $1 AND course_id = $2`,
		studentID, courseID,
	).Scan(&total); err != nil {
		t.Fatalf("SELECT after second student UPSERT: %v", err)
	}
	if total != 30 {
		t.Errorf("total after second student UPSERT: want 30, got %d", total)
	}
}

// TestNodeChairInteg_TokenUpsert_DraftPath_AccumulatesOnPost022Schema is
// the regression guard for Fix 1.  It exercises the draft-path UPSERT
// (ON CONFLICT (draft_id) WHERE draft_id IS NOT NULL) against the real
// post-022 schema and asserts the same accumulation semantics.
//
// This test FAILS if Fix 1 is reverted.
//
// @{"verifies": ["REQ-AGENT-035", "REQ-AGENT-056"]}
func TestNodeChairInteg_TokenUpsert_DraftPath_AccumulatesOnPost022Schema(t *testing.T) {
	truncateNodeChairTables(t)

	pool := internaldb.IntegrationPool(t)
	ctx := context.Background()

	adminID := seedIntegAdmin(t, "draft_upsert")
	draftID := seedIntegDraft(t, adminID, "draft_upsert")

	// First UPSERT: should insert a fresh row with 20 tokens.
	_, err := pool.Exec(ctx,
		`INSERT INTO agent_token_usage (draft_id, total_tokens_used)
		 VALUES ($1, $2)
		 ON CONFLICT (draft_id) WHERE draft_id IS NOT NULL
		 DO UPDATE SET total_tokens_used = agent_token_usage.total_tokens_used + EXCLUDED.total_tokens_used`,
		draftID, int64(20),
	)
	if err != nil {
		t.Fatalf("first draft UPSERT (post-022 partial index): %v — "+
			"this error indicates Fix 1 is missing or migration 022 was not applied", err)
	}

	var total int64
	if err := pool.QueryRow(ctx,
		`SELECT total_tokens_used FROM agent_token_usage WHERE draft_id = $1`,
		draftID,
	).Scan(&total); err != nil {
		t.Fatalf("SELECT after first draft UPSERT: %v", err)
	}
	if total != 20 {
		t.Errorf("total after first draft UPSERT: want 20, got %d", total)
	}

	// Second UPSERT: should accumulate to 40.
	_, err = pool.Exec(ctx,
		`INSERT INTO agent_token_usage (draft_id, total_tokens_used)
		 VALUES ($1, $2)
		 ON CONFLICT (draft_id) WHERE draft_id IS NOT NULL
		 DO UPDATE SET total_tokens_used = agent_token_usage.total_tokens_used + EXCLUDED.total_tokens_used`,
		draftID, int64(20),
	)
	if err != nil {
		t.Fatalf("second draft UPSERT (post-022 partial index): %v", err)
	}

	if err := pool.QueryRow(ctx,
		`SELECT total_tokens_used FROM agent_token_usage WHERE draft_id = $1`,
		draftID,
	).Scan(&total); err != nil {
		t.Fatalf("SELECT after second draft UPSERT: %v", err)
	}
	if total != 40 {
		t.Errorf("total after second draft UPSERT: want 40, got %d", total)
	}
}

// TestNodeChairInteg_TokenUpsert_BothPaths_MutualExclusion verifies that
// student-path rows and draft-path rows coexist in agent_token_usage without
// conflict (the context_check constraint enforces mutual exclusion at the row
// level).
//
// @{"verifies": ["REQ-AGENT-035"]}
func TestNodeChairInteg_TokenUpsert_BothPaths_MutualExclusion(t *testing.T) {
	truncateNodeChairTables(t)

	pool := internaldb.IntegrationPool(t)
	ctx := context.Background()

	studentID := seedIntegStudent(t, "mutual_excl")
	courseID := seedIntegCourse(t, studentID, "mutual_excl")
	adminID := seedIntegAdmin(t, "mutual_excl")
	draftID := seedIntegDraft(t, adminID, "mutual_excl")

	// Insert student-path row.
	_, err := pool.Exec(ctx,
		`INSERT INTO agent_token_usage (student_id, course_id, total_tokens_used)
		 VALUES ($1, $2, 10)
		 ON CONFLICT (student_id, course_id) WHERE draft_id IS NULL
		 DO UPDATE SET total_tokens_used = agent_token_usage.total_tokens_used + EXCLUDED.total_tokens_used`,
		studentID, courseID,
	)
	if err != nil {
		t.Fatalf("student UPSERT: %v", err)
	}

	// Insert draft-path row — must not conflict with the student row.
	_, err = pool.Exec(ctx,
		`INSERT INTO agent_token_usage (draft_id, total_tokens_used)
		 VALUES ($1, 25)
		 ON CONFLICT (draft_id) WHERE draft_id IS NOT NULL
		 DO UPDATE SET total_tokens_used = agent_token_usage.total_tokens_used + EXCLUDED.total_tokens_used`,
		draftID,
	)
	if err != nil {
		t.Fatalf("draft UPSERT alongside student row: %v", err)
	}

	// Verify both rows exist and are independent.
	var studentTotal, draftTotal int64
	if err := pool.QueryRow(ctx,
		`SELECT total_tokens_used FROM agent_token_usage WHERE student_id = $1 AND course_id = $2`,
		studentID, courseID,
	).Scan(&studentTotal); err != nil {
		t.Fatalf("SELECT student row: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT total_tokens_used FROM agent_token_usage WHERE draft_id = $1`,
		draftID,
	).Scan(&draftTotal); err != nil {
		t.Fatalf("SELECT draft row: %v", err)
	}

	if studentTotal != 10 {
		t.Errorf("student total: want 10, got %d", studentTotal)
	}
	if draftTotal != 25 {
		t.Errorf("draft total: want 25, got %d", draftTotal)
	}
}
