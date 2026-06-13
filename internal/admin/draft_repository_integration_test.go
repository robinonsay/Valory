//go:build integration

// draft_repository_integration_test.go — real-Postgres integration tests for
// DraftRepository (internal/admin/draft_repository.go).
//
// WHY this file exists:
// DraftRepository provides admin-authoring CRUD over course_drafts, draft_nodes,
// and the draft_node_id side of node_chats. Row-level security policies on all
// three tables must enforce per-admin isolation:
//
//	(a) admin A reads/modifies only drafts they own (admin_id = current_user_id),
//	(b) admin B CANNOT read admin A's drafts — cross-admin denial → 0 rows,
//	(c) a student-role connection CANNOT read any draft/draft_node (admin-only),
//	(d) server-role connections can read and write all drafts.
//
// Every RLS assertion runs under SET ROLE valory_app (via AcquireAsUser /
// AcquireAsServer from internal/db/integrationtest.go) so that FORCE ROW LEVEL
// SECURITY actually fires. Assertions made as the superuser test role (valory_test)
// are INVALID — they bypass FORCE RLS and mask policy bugs (see memory
// "force-rls-superuser-test-masking").
//
// Positive controls: admin B's drafts are genuinely seeded before asserting that
// admin A cannot see them — the cross-admin denial is not vacuous.
//
// Run:
//
//	make test-integration
//	# or manually:
//	export PATH=$PATH:/usr/local/go/bin
//	VALORY_TEST_DATABASE_URL=postgres://valory_test:valory_test@localhost:55432/valory_test?sslmode=disable \
//	  go test -tags integration -count=1 -p 1 -run TestDraftRepo ./internal/admin/
//
// @{"verifies": ["REQ-AGENT-047", "REQ-AGENT-048", "REQ-AGENT-049", "REQ-AGENT-050",
//               "REQ-AGENT-051", "REQ-AGENT-052", "REQ-AGENT-053", "REQ-AGENT-054",
//               "REQ-AGENT-055", "REQ-AGENT-057", "REQ-AGENT-058", "REQ-SYS-075",
//               "REQ-SYS-077"]}
package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	authpkg "github.com/valory/valory/internal/auth"
	internaldb "github.com/valory/valory/internal/db"
)

// ---------------------------------------------------------------------------
// Fixture helpers
// ---------------------------------------------------------------------------

// draftSeed holds the IDs created for a single admin's draft fixture.
type draftSeed struct {
	AdminID uuid.UUID
	DraftID uuid.UUID
}

// truncateDraftTables resets all tables touched by draft tests in dependency
// order. Superuser pool is used so RLS does not interfere with cleanup.
func truncateDraftTables(t *testing.T) {
	t.Helper()
	internaldb.TruncateTables(t, internaldb.IntegrationPool(t),
		"node_chats",
		"draft_nodes",
		"course_drafts",
		"agent_token_usage",
		"agent_runs",
		"sessions",
		"login_attempts",
		"course_assignments",
		"courses",
		"users",
	)
}

// seedDraftAdmin inserts an admin user row and returns its UUID. All writes
// use the bare superuser pool so seeding is unaffected by RLS.
func seedDraftAdmin(t *testing.T, tag string) uuid.UUID {
	t.Helper()
	pool := internaldb.IntegrationPool(t)
	username := fmt.Sprintf("draft_admin_%s_%s", tag, uuid.New().String()[:8])
	var adminID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (username, password_hash, role) VALUES ($1, 'x', 'admin') RETURNING id`,
		username,
	).Scan(&adminID); err != nil {
		t.Fatalf("seedDraftAdmin(%s): %v", tag, err)
	}
	return adminID
}

// seedDraftStudent inserts a student user row and returns its UUID. Used as
// a positive control to seed non-admin row data for student-cannot-read tests.
func seedDraftStudent(t *testing.T, tag string) uuid.UUID {
	t.Helper()
	pool := internaldb.IntegrationPool(t)
	username := fmt.Sprintf("draft_student_%s_%s", tag, uuid.New().String()[:8])
	var studentID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (username, password_hash, role) VALUES ($1, 'x', 'student') RETURNING id`,
		username,
	).Scan(&studentID); err != nil {
		t.Fatalf("seedDraftStudent(%s): %v", tag, err)
	}
	return studentID
}

// draftAdminConnCtx returns a context carrying an admin-role valory_app
// connection for the given admin UUID, mirroring how the auth middleware sets
// up a connection for an admin HTTP request.
//
// The caller must call the returned cleanup function (typically via defer) to
// release the connection back to the pool.
func draftAdminConnCtx(t *testing.T, adminID uuid.UUID) (context.Context, func()) {
	t.Helper()
	pool := internaldb.IntegrationPool(t)
	// AcquireAsUser expects the 32-char no-dash hex representation that the
	// auth middleware produces: fmt.Sprintf("%x", [16]byte(id)).
	adminIDHex := fmt.Sprintf("%x", [16]byte(adminID))
	conn := internaldb.AcquireAsUser(t, pool, adminIDHex, "admin")
	ctx := authpkg.ContextWithConn(context.Background(), conn)
	return ctx, conn.Release
}

// draftStudentConnCtx returns a context carrying a student-role valory_app
// connection for the given student UUID — used to prove that drafts are
// invisible to student-role callers.
func draftStudentConnCtx(t *testing.T, studentID uuid.UUID) (context.Context, func()) {
	t.Helper()
	pool := internaldb.IntegrationPool(t)
	studentIDHex := fmt.Sprintf("%x", [16]byte(studentID))
	conn := internaldb.AcquireAsUser(t, pool, studentIDHex, "student")
	ctx := authpkg.ContextWithConn(context.Background(), conn)
	return ctx, conn.Release
}

// draftServerConnCtx returns a context carrying a server-role valory_app
// connection — the pipeline path that must be able to read/write all drafts.
func draftServerConnCtx(t *testing.T) (context.Context, func()) {
	t.Helper()
	pool := internaldb.IntegrationPool(t)
	conn := internaldb.AcquireAsServer(t, pool)
	ctx := authpkg.ContextWithConn(context.Background(), conn)
	return ctx, conn.Release
}

// connFromCtx extracts the *pgxpool.Conn from a context prepared by one of the
// helper functions above, or fails the test immediately if it is absent.
func connFromCtx(t *testing.T, ctx context.Context) *pgxpool.Conn {
	t.Helper()
	conn, ok := authpkg.ConnFromContext(ctx)
	if !ok {
		t.Fatal("connFromCtx: no conn in context — use draftAdminConnCtx / draftServerConnCtx")
	}
	return conn
}

// ---------------------------------------------------------------------------
// 1. Draft CRUD
// ---------------------------------------------------------------------------

// TestDraftRepo_CreateDraft_SeedsImplicitRootNode verifies that CreateDraft
// inserts both the course_drafts row and an implicit 'root' draft_node with
// the correct payload (api-sse §10.2) in a single call.
//
// @{"verifies": ["REQ-AGENT-047", "REQ-AGENT-048", "REQ-AGENT-044", "REQ-SYS-077"]}
func TestDraftRepo_CreateDraft_SeedsImplicitRootNode(t *testing.T) {
	truncateDraftTables(t)

	adminID := seedDraftAdmin(t, "create")
	pool := internaldb.IntegrationPool(t)
	repo := NewDraftRepository(pool)

	// CreateDraft must run under an admin-role connection so the RLS INSERT
	// policy (course_drafts_admin_own) fires correctly.
	adminCtx, releaseAdmin := draftAdminConnCtx(t, adminID)
	defer releaseAdmin()
	adminConn := connFromCtx(t, adminCtx)

	params := json.RawMessage(`{"weeks":8}`)
	draftID, err := repo.CreateDraft(adminCtx, adminConn, adminID, "Go Basics", "goroutines", "beginner", params)
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if draftID == uuid.Nil {
		t.Fatal("CreateDraft: returned uuid.Nil")
	}

	// Verify the course_drafts row is readable via the same admin connection.
	draft, err := repo.GetDraft(adminCtx, adminConn, draftID)
	if err != nil {
		t.Fatalf("GetDraft after CreateDraft: %v", err)
	}
	if draft.ID != draftID {
		t.Errorf("GetDraft: want ID %s, got %s", draftID, draft.ID)
	}
	if draft.AdminID != adminID {
		t.Errorf("GetDraft: want AdminID %s, got %s", adminID, draft.AdminID)
	}
	if draft.Title != "Go Basics" {
		t.Errorf("GetDraft: want Title 'Go Basics', got %q", draft.Title)
	}
	if draft.Topic != "goroutines" {
		t.Errorf("GetDraft: want Topic 'goroutines', got %q", draft.Topic)
	}
	if draft.Level != "beginner" {
		t.Errorf("GetDraft: want Level 'beginner', got %q", draft.Level)
	}
	if draft.Status != "draft" {
		t.Errorf("GetDraft: want Status 'draft', got %q", draft.Status)
	}

	// Verify the implicit root draft_node was seeded (api-sse §10.2).
	nodes, err := repo.ListDraftNodes(adminCtx, adminConn, draftID, DraftNodeListOpts{NodeTypeFilter: "root"})
	if err != nil {
		t.Fatalf("ListDraftNodes(root filter) after CreateDraft: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("implicit root node: want 1 root node, got %d", len(nodes))
	}
	rootNode := nodes[0]
	if rootNode.NodeType != "root" {
		t.Errorf("root node type: want 'root', got %q", rootNode.NodeType)
	}
	if rootNode.Status != "pending" {
		t.Errorf("root node status: want 'pending', got %q", rootNode.Status)
	}
	if rootNode.ParentID != nil {
		t.Errorf("root node parent_id: want nil, got %v", rootNode.ParentID)
	}
	// Payload must contain topic and level.
	var rootPayload map[string]string
	if err := json.Unmarshal(rootNode.Payload, &rootPayload); err != nil {
		t.Fatalf("unmarshal root node payload: %v", err)
	}
	if rootPayload["topic"] != "goroutines" {
		t.Errorf("root node payload.topic: want 'goroutines', got %q", rootPayload["topic"])
	}
	if rootPayload["level"] != "beginner" {
		t.Errorf("root node payload.level: want 'beginner', got %q", rootPayload["level"])
	}
}

// TestDraftRepo_GetDraft_NotFound verifies that GetDraft returns ErrDraftNotFound
// for an unknown draft UUID.
//
// @{"verifies": ["REQ-AGENT-047", "REQ-AGENT-048", "REQ-SYS-075", "REQ-SYS-077"]}
func TestDraftRepo_GetDraft_NotFound(t *testing.T) {
	truncateDraftTables(t)

	adminID := seedDraftAdmin(t, "notfound")
	pool := internaldb.IntegrationPool(t)
	repo := NewDraftRepository(pool)

	adminCtx, release := draftAdminConnCtx(t, adminID)
	defer release()
	adminConn := connFromCtx(t, adminCtx)

	_, err := repo.GetDraft(adminCtx, adminConn, uuid.New())
	if err != ErrDraftNotFound {
		t.Errorf("GetDraft(unknown id): want ErrDraftNotFound, got %v", err)
	}
}

// TestDraftRepo_ListDrafts_StatusFilter_And_CursorPagination verifies that
// ListDrafts correctly filters by status and paginates using the opaque cursor.
//
// @{"verifies": ["REQ-AGENT-047", "REQ-AGENT-048", "REQ-SYS-075", "REQ-SYS-077"]}
func TestDraftRepo_ListDrafts_StatusFilter_And_CursorPagination(t *testing.T) {
	truncateDraftTables(t)

	adminID := seedDraftAdmin(t, "list")
	pool := internaldb.IntegrationPool(t)
	repo := NewDraftRepository(pool)

	adminCtx, release := draftAdminConnCtx(t, adminID)
	defer release()
	adminConn := connFromCtx(t, adminCtx)

	// Insert rows via the superuser pool with explicit, strictly increasing
	// created_at timestamps so keyset pagination is deterministic regardless of
	// wall-clock resolution. Five total rows: 4 'draft' + 1 'generating'.
	//
	// NOTE on the cursor semantics: ListDrafts uses keyset pagination with the
	// condition (created_at, id) < (cursor). The cursor is built from
	// results[opts.Limit] (the first overflow item, i.e. the (limit+1)th item
	// in DESC order). Page 2 returns rows strictly OLDER (lower created_at) than
	// that overflow item. With 4 'draft' rows in DESC order [R0,R1,R2,R3] and
	// limit=2: page1 fetches [R0,R1,R2], cursor=R2, returns [R0,R1]; page2
	// fetches rows older than R2 → [R3] → 1 row, no further cursor.
	baseTime := "2024-01-01 10:00:00+00"
	type rowSpec struct{ title, status string; offset int }
	rowSpecs := []rowSpec{
		{"Draft oldest", "draft", 0},
		{"Draft 2nd", "draft", 1},
		{"Draft 3rd", "draft", 2},
		{"Draft newest", "draft", 3},
		{"Generating one", "generating", 4},
	}
	for _, s := range rowSpecs {
		if _, err := pool.Exec(context.Background(),
			`INSERT INTO course_drafts (admin_id, title, topic, level, parameters, status, created_at, updated_at)
			 VALUES ($1, $2, 'topic', 'beginner', '{}', $3,
			         $4::timestamptz + ($5 * INTERVAL '1 second'),
			         $4::timestamptz + ($5 * INTERVAL '1 second'))`,
			adminID, s.title, s.status, baseTime, s.offset,
		); err != nil {
			t.Fatalf("insert draft %q: %v", s.title, err)
		}
	}

	// --- Status filter: only 'draft' rows (4 of the 5) ---
	draftRows, _, err := repo.ListDrafts(adminCtx, adminConn, adminID, DraftListOpts{StatusFilter: "draft"})
	if err != nil {
		t.Fatalf("ListDrafts(status=draft): %v", err)
	}
	if len(draftRows) != 4 {
		t.Errorf("ListDrafts(status=draft): want 4 rows, got %d", len(draftRows))
	}

	// --- No filter: all 5 rows ---
	allRows, _, err := repo.ListDrafts(adminCtx, adminConn, adminID, DraftListOpts{})
	if err != nil {
		t.Fatalf("ListDrafts(no filter): %v", err)
	}
	if len(allRows) != 5 {
		t.Errorf("ListDrafts(no filter): want 5 rows, got %d", len(allRows))
	}

	// --- Cursor pagination: limit=2 on 4 'draft' rows ---
	// DESC order: R3(newest, offset=3), R2(offset=2), R1(offset=1), R0(oldest, offset=0).
	// Page 1 fetches limit+1=3 → [R3, R2, R1]. Cursor = R1 (overflow item).
	// Returns [R3, R2].
	page1, nextCursor, err := repo.ListDrafts(adminCtx, adminConn, adminID, DraftListOpts{StatusFilter: "draft", Limit: 2})
	if err != nil {
		t.Fatalf("ListDrafts page 1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("ListDrafts page 1: want 2 rows, got %d", len(page1))
	}
	if nextCursor == "" {
		t.Fatal("ListDrafts page 1: want non-empty next cursor (4 'draft' rows, limit 2)")
	}

	// Page 2: rows with (created_at, id) < cursor (R1) → older than R1 → finds R0.
	page2, nextCursor2, err := repo.ListDrafts(adminCtx, adminConn, adminID, DraftListOpts{StatusFilter: "draft", Limit: 2, Cursor: nextCursor})
	if err != nil {
		t.Fatalf("ListDrafts page 2: %v", err)
	}
	if len(page2) != 1 {
		t.Errorf("ListDrafts page 2: want 1 row, got %d", len(page2))
	}
	if nextCursor2 != "" {
		t.Errorf("ListDrafts page 2: want empty cursor (last page), got %q", nextCursor2)
	}

	// No overlap between pages: page2's row must not appear in page1.
	if len(page2) > 0 {
		p1IDs := map[uuid.UUID]bool{page1[0].ID: true, page1[1].ID: true}
		if p1IDs[page2[0].ID] {
			t.Errorf("ListDrafts pagination: page2 row %s appeared in page1 (cursor not working)", page2[0].ID)
		}
	}
}

// TestDraftRepo_DeleteDraft_SucceedsOnDraft_RefusesPublished verifies the
// published-guard: DeleteDraft must return ErrDraftNotDeletable for a draft not
// in 'draft' status, and must succeed (cascading to draft_nodes and node_chats)
// for a draft in 'draft' status.
//
// @{"verifies": ["REQ-AGENT-047", "REQ-AGENT-057", "REQ-SYS-075", "REQ-SYS-077"]}
func TestDraftRepo_DeleteDraft_SucceedsOnDraft_RefusesPublished(t *testing.T) {
	truncateDraftTables(t)

	adminID := seedDraftAdmin(t, "delete")
	pool := internaldb.IntegrationPool(t)
	repo := NewDraftRepository(pool)

	adminCtx, release := draftAdminConnCtx(t, adminID)
	defer release()
	adminConn := connFromCtx(t, adminCtx)

	// Create a draft and a child node with a chat message so we can assert
	// cascade delete works.
	draftID, err := repo.CreateDraft(adminCtx, adminConn, adminID, "Cascade Test", "topic", "beginner", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	childNode, err := repo.CreateDraftNode(adminCtx, adminConn, draftID, nil, "syllabus", 0, json.RawMessage(`{"test":true}`))
	if err != nil {
		t.Fatalf("CreateDraftNode (syllabus): %v", err)
	}
	// Attach a root node parent for the child so we can use the child's ID
	// without worrying about the sibling-order constraint.
	_ = childNode

	// Append a chat message so cascade delete is verified at the node_chats level.
	rootNodes, err := repo.ListDraftNodes(adminCtx, adminConn, draftID, DraftNodeListOpts{NodeTypeFilter: "root"})
	if err != nil || len(rootNodes) == 0 {
		t.Fatalf("ListDraftNodes(root): %v (count=%d)", err, len(rootNodes))
	}
	rootNodeID := rootNodes[0].ID
	_, err = repo.AppendDraftNodeChat(adminCtx, adminConn, rootNodeID, "user", "Hello from cascade test")
	if err != nil {
		t.Fatalf("AppendDraftNodeChat: %v", err)
	}

	// --- Promote to 'published' via superuser so the guard fires ---
	if _, err := pool.Exec(context.Background(),
		`UPDATE course_drafts SET status = 'published' WHERE id = $1`, draftID,
	); err != nil {
		t.Fatalf("promote draft to published: %v", err)
	}

	// Attempt to delete a published draft — must return ErrDraftNotDeletable.
	err = repo.DeleteDraft(adminCtx, adminConn, draftID)
	if err != ErrDraftNotDeletable {
		t.Errorf("DeleteDraft(published): want ErrDraftNotDeletable, got %v", err)
	}

	// Restore to 'draft' status so we can exercise the happy path.
	if _, err := pool.Exec(context.Background(),
		`UPDATE course_drafts SET status = 'draft' WHERE id = $1`, draftID,
	); err != nil {
		t.Fatalf("restore draft status: %v", err)
	}

	// Delete must now succeed.
	if err := repo.DeleteDraft(adminCtx, adminConn, draftID); err != nil {
		t.Fatalf("DeleteDraft(draft status): %v", err)
	}

	// The draft must no longer be visible.
	_, err = repo.GetDraft(adminCtx, adminConn, draftID)
	if err != ErrDraftNotFound {
		t.Errorf("GetDraft after DeleteDraft: want ErrDraftNotFound, got %v", err)
	}

	// CASCADE must have removed child draft_nodes — query the superuser pool
	// directly to verify (the admin connection would return 0 rows via RLS, which
	// would be vacuously correct here; use the superuser pool to confirm absence
	// at the storage layer).
	var nodeCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM draft_nodes WHERE draft_id = $1`, draftID,
	).Scan(&nodeCount); err != nil {
		t.Fatalf("count draft_nodes after delete: %v", err)
	}
	if nodeCount != 0 {
		t.Errorf("cascade delete: want 0 draft_nodes, got %d", nodeCount)
	}

	// CASCADE must have removed node_chats rows.
	var chatCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM node_chats WHERE draft_node_id = $1`, rootNodeID,
	).Scan(&chatCount); err != nil {
		t.Fatalf("count node_chats after delete: %v", err)
	}
	if chatCount != 0 {
		t.Errorf("cascade delete: want 0 node_chats, got %d", chatCount)
	}
}

// ---------------------------------------------------------------------------
// 2. Draft node CRUD
// ---------------------------------------------------------------------------

// TestDraftRepo_DraftNodeCRUD_TreeChain verifies create → list by type/parent →
// ordering → JSONB payload round-trip for a root→syllabus→section chain.
//
// @{"verifies": ["REQ-AGENT-044", "REQ-AGENT-047", "REQ-AGENT-049", "REQ-AGENT-058",
//               "REQ-SYS-075", "REQ-SYS-077"]}
func TestDraftRepo_DraftNodeCRUD_TreeChain(t *testing.T) {
	truncateDraftTables(t)

	adminID := seedDraftAdmin(t, "chain")
	pool := internaldb.IntegrationPool(t)
	repo := NewDraftRepository(pool)

	adminCtx, release := draftAdminConnCtx(t, adminID)
	defer release()
	adminConn := connFromCtx(t, adminCtx)

	draftID, err := repo.CreateDraft(adminCtx, adminConn, adminID, "Chain Draft", "channels", "intermediate", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	// Retrieve the implicit root node created by CreateDraft.
	rootNodes, err := repo.ListDraftNodes(adminCtx, adminConn, draftID, DraftNodeListOpts{NodeTypeFilter: "root"})
	if err != nil || len(rootNodes) == 0 {
		t.Fatalf("ListDraftNodes(root): %v (count=%d)", err, len(rootNodes))
	}
	rootID := rootNodes[0].ID

	// Create a syllabus node as a child of root.
	syllabusPayload := json.RawMessage(`{"sections":["Intro","Goroutines","Channels"]}`)
	syllabusNode, err := repo.CreateDraftNode(adminCtx, adminConn, draftID, &rootID, "syllabus", 0, syllabusPayload)
	if err != nil {
		t.Fatalf("CreateDraftNode(syllabus): %v", err)
	}

	// Create two section_goal children of the syllabus node.
	sg1Payload := json.RawMessage(`{"goal":"Understand goroutine scheduling"}`)
	sg1, err := repo.CreateDraftNode(adminCtx, adminConn, draftID, &syllabusNode.ID, "section_goal", 0, sg1Payload)
	if err != nil {
		t.Fatalf("CreateDraftNode(section_goal 0): %v", err)
	}

	sg2Payload := json.RawMessage(`{"goal":"Master channel patterns"}`)
	sg2, err := repo.CreateDraftNode(adminCtx, adminConn, draftID, &syllabusNode.ID, "section_goal", 1, sg2Payload)
	if err != nil {
		t.Fatalf("CreateDraftNode(section_goal 1): %v", err)
	}

	// --- GetDraftNode round-trip ---
	for _, tc := range []struct {
		id       uuid.UUID
		wantType string
		payloadKey string
	}{
		{syllabusNode.ID, "syllabus", "sections"},
		{sg1.ID, "section_goal", "goal"},
		{sg2.ID, "section_goal", "goal"},
	} {
		tc := tc
		t.Run(fmt.Sprintf("GetDraftNode_%s_%s", tc.wantType, tc.id.String()[:8]), func(t *testing.T) {
			got, err := repo.GetDraftNode(adminCtx, adminConn, tc.id)
			if err != nil {
				t.Fatalf("GetDraftNode: %v", err)
			}
			if got.ID != tc.id {
				t.Errorf("GetDraftNode: want ID %s, got %s", tc.id, got.ID)
			}
			if got.NodeType != tc.wantType {
				t.Errorf("GetDraftNode: want NodeType %q, got %q", tc.wantType, got.NodeType)
			}
			// JSONB payload must survive the round-trip.
			var payload map[string]interface{}
			if err := json.Unmarshal(got.Payload, &payload); err != nil {
				t.Fatalf("GetDraftNode: unmarshal payload: %v", err)
			}
			if _, ok := payload[tc.payloadKey]; !ok {
				t.Errorf("GetDraftNode: payload missing key %q", tc.payloadKey)
			}
		})
	}

	// --- ListDraftNodes by type filter ---
	sectionGoals, err := repo.ListDraftNodes(adminCtx, adminConn, draftID, DraftNodeListOpts{NodeTypeFilter: "section_goal"})
	if err != nil {
		t.Fatalf("ListDraftNodes(section_goal): %v", err)
	}
	if len(sectionGoals) != 2 {
		t.Errorf("ListDraftNodes(section_goal): want 2 rows, got %d", len(sectionGoals))
	}

	// --- ListDraftNodes by parent filter ---
	syllabusChildren, err := repo.ListDraftNodes(adminCtx, adminConn, draftID, DraftNodeListOpts{ParentIDFilter: &syllabusNode.ID})
	if err != nil {
		t.Fatalf("ListDraftNodes(parent=syllabus): %v", err)
	}
	if len(syllabusChildren) != 2 {
		t.Fatalf("ListDraftNodes(parent=syllabus): want 2 children, got %d", len(syllabusChildren))
	}

	// --- Ordering: children must come back ordering ASC ---
	if syllabusChildren[0].Ordering != 0 {
		t.Errorf("ordering[0]: want 0, got %d", syllabusChildren[0].Ordering)
	}
	if syllabusChildren[1].Ordering != 1 {
		t.Errorf("ordering[1]: want 1, got %d", syllabusChildren[1].Ordering)
	}

	// --- ListDraftNodeChildren ---
	children, err := repo.ListDraftNodeChildren(adminCtx, adminConn, draftID, syllabusNode.ID)
	if err != nil {
		t.Fatalf("ListDraftNodeChildren: %v", err)
	}
	if len(children) != 2 {
		t.Errorf("ListDraftNodeChildren: want 2 children, got %d", len(children))
	}
}

// TestDraftRepo_SetDraftCurrentLayer verifies that SetDraftCurrentLayer updates
// the current_layer column and that the change is visible via GetDraft.
//
// @{"verifies": ["REQ-AGENT-038", "REQ-AGENT-047", "REQ-AGENT-048", "REQ-SYS-077"]}
func TestDraftRepo_SetDraftCurrentLayer(t *testing.T) {
	truncateDraftTables(t)

	adminID := seedDraftAdmin(t, "layer")
	pool := internaldb.IntegrationPool(t)
	repo := NewDraftRepository(pool)

	adminCtx, release := draftAdminConnCtx(t, adminID)
	defer release()
	adminConn := connFromCtx(t, adminCtx)

	draftID, err := repo.CreateDraft(adminCtx, adminConn, adminID, "Layer Draft", "concurrency", "advanced", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	// Set current_layer to 'syllabus'.
	if err := repo.SetDraftCurrentLayer(adminCtx, adminConn, draftID, "syllabus"); err != nil {
		t.Fatalf("SetDraftCurrentLayer: %v", err)
	}

	draft, err := repo.GetDraft(adminCtx, adminConn, draftID)
	if err != nil {
		t.Fatalf("GetDraft after SetDraftCurrentLayer: %v", err)
	}
	if draft.CurrentLayer == nil {
		t.Fatal("GetDraft: CurrentLayer is nil after SetDraftCurrentLayer")
	}
	if *draft.CurrentLayer != "syllabus" {
		t.Errorf("GetDraft: CurrentLayer want 'syllabus', got %q", *draft.CurrentLayer)
	}
}

// ---------------------------------------------------------------------------
// 3. Atomic status transition
// ---------------------------------------------------------------------------

// TestDraftRepo_TransitionDraftNodeStatus_TrueOnMatch_FalseOnStale verifies
// the optimistic-concurrency guard: TransitionDraftNodeStatus returns true when
// the current status matches fromStatus and false when it does not (stale guard).
//
// @{"verifies": ["REQ-AGENT-030", "REQ-AGENT-055", "REQ-SYS-075", "REQ-SYS-077"]}
func TestDraftRepo_TransitionDraftNodeStatus_TrueOnMatch_FalseOnStale(t *testing.T) {
	truncateDraftTables(t)

	adminID := seedDraftAdmin(t, "transition")
	pool := internaldb.IntegrationPool(t)
	repo := NewDraftRepository(pool)

	adminCtx, release := draftAdminConnCtx(t, adminID)
	defer release()
	adminConn := connFromCtx(t, adminCtx)

	draftID, err := repo.CreateDraft(adminCtx, adminConn, adminID, "Transition Draft", "topic", "beginner", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	rootNodes, err := repo.ListDraftNodes(adminCtx, adminConn, draftID, DraftNodeListOpts{NodeTypeFilter: "root"})
	if err != nil || len(rootNodes) == 0 {
		t.Fatalf("ListDraftNodes(root): %v (count=%d)", err, len(rootNodes))
	}
	nodeID := rootNodes[0].ID

	tests := []struct {
		name       string
		from       string
		to         string
		wantResult bool
	}{
		{
			name:       "pending_to_generating_succeeds",
			from:       "pending",
			to:         "generating",
			wantResult: true,
		},
		{
			name:       "stale_from_pending_fails",
			from:       "pending",    // node is now 'generating' — stale
			to:         "generating",
			wantResult: false,
		},
		{
			name:       "generating_to_awaiting_review_succeeds",
			from:       "generating",
			to:         "awaiting_review",
			wantResult: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ok, err := repo.TransitionDraftNodeStatus(adminCtx, adminConn, nodeID, tc.from, tc.to)
			if err != nil {
				t.Fatalf("TransitionDraftNodeStatus(%s→%s): %v", tc.from, tc.to, err)
			}
			if ok != tc.wantResult {
				t.Errorf("TransitionDraftNodeStatus(%s→%s): want %v, got %v", tc.from, tc.to, tc.wantResult, ok)
			}
		})
	}
}

// TestDraftRepo_SetDraftNodePayload_MovesToAwaitingReview verifies that
// SetDraftNodePayload atomically sets the payload AND transitions the node to
// 'awaiting_review' (the rework fix for nodes stuck in 'generating').
//
// @{"verifies": ["REQ-AGENT-047", "REQ-AGENT-051", "REQ-SYS-075", "REQ-SYS-077"]}
func TestDraftRepo_SetDraftNodePayload_MovesToAwaitingReview(t *testing.T) {
	truncateDraftTables(t)

	adminID := seedDraftAdmin(t, "payload")
	pool := internaldb.IntegrationPool(t)
	repo := NewDraftRepository(pool)

	adminCtx, release := draftAdminConnCtx(t, adminID)
	defer release()
	adminConn := connFromCtx(t, adminCtx)

	draftID, err := repo.CreateDraft(adminCtx, adminConn, adminID, "Payload Draft", "topic", "beginner", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	rootNodes, err := repo.ListDraftNodes(adminCtx, adminConn, draftID, DraftNodeListOpts{NodeTypeFilter: "root"})
	if err != nil || len(rootNodes) == 0 {
		t.Fatalf("ListDraftNodes(root): %v", err)
	}
	nodeID := rootNodes[0].ID

	// Transition the node to 'generating' first so we can confirm the payload
	// write also changes the status from 'generating' to 'awaiting_review'.
	if _, err := repo.TransitionDraftNodeStatus(adminCtx, adminConn, nodeID, "pending", "generating"); err != nil {
		t.Fatalf("TransitionDraftNodeStatus(pending→generating): %v", err)
	}

	newPayload := json.RawMessage(`{"adoc":"= Generated Content\n\nBody.","tokens":150}`)
	if err := repo.SetDraftNodePayload(adminCtx, adminConn, nodeID, newPayload); err != nil {
		t.Fatalf("SetDraftNodePayload: %v", err)
	}

	// Verify both the payload and status after the atomic write.
	got, err := repo.GetDraftNode(adminCtx, adminConn, nodeID)
	if err != nil {
		t.Fatalf("GetDraftNode after SetDraftNodePayload: %v", err)
	}
	if got.Status != "awaiting_review" {
		t.Errorf("SetDraftNodePayload: want status 'awaiting_review', got %q", got.Status)
	}
	var pl map[string]interface{}
	if err := json.Unmarshal(got.Payload, &pl); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if _, ok := pl["adoc"]; !ok {
		t.Error("SetDraftNodePayload: payload missing 'adoc' key after write")
	}
	if _, ok := pl["tokens"]; !ok {
		t.Error("SetDraftNodePayload: payload missing 'tokens' key after write")
	}
}

// ---------------------------------------------------------------------------
// 4. Layer aggregate
// ---------------------------------------------------------------------------

// TestDraftRepo_LayerAggregate_MixedStatuses verifies that LayerAggregate
// correctly counts approved, failed, and pending nodes for a given node_type
// layer when nodes carry mixed statuses.
//
// @{"verifies": ["REQ-AGENT-038", "REQ-AGENT-039", "REQ-AGENT-040", "REQ-AGENT-057",
//               "REQ-SYS-075", "REQ-SYS-077"]}
func TestDraftRepo_LayerAggregate_MixedStatuses(t *testing.T) {
	truncateDraftTables(t)

	adminID := seedDraftAdmin(t, "aggregate")
	pool := internaldb.IntegrationPool(t)
	repo := NewDraftRepository(pool)

	adminCtx, release := draftAdminConnCtx(t, adminID)
	defer release()
	adminConn := connFromCtx(t, adminCtx)

	draftID, err := repo.CreateDraft(adminCtx, adminConn, adminID, "Agg Draft", "topic", "beginner", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	// Retrieve the root node so we can attach section_goal children.
	rootNodes, err := repo.ListDraftNodes(adminCtx, adminConn, draftID, DraftNodeListOpts{NodeTypeFilter: "root"})
	if err != nil || len(rootNodes) == 0 {
		t.Fatalf("ListDraftNodes(root): %v", err)
	}
	rootID := rootNodes[0].ID

	// Insert a syllabus node as an intermediary parent.
	syllabusNode, err := repo.CreateDraftNode(adminCtx, adminConn, draftID, &rootID, "syllabus", 0, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CreateDraftNode(syllabus): %v", err)
	}

	// Create 5 section_goal nodes: 2 approved, 1 failed, 2 pending.
	type sgSpec struct {
		ordering int
		status   string
	}
	specs := []sgSpec{
		{0, "approved"},
		{1, "approved"},
		{2, "failed"},
		{3, "pending"},
		{4, "awaiting_review"}, // non-terminal, non-approved → counts as pending
	}

	var sgIDs []uuid.UUID
	for _, s := range specs {
		sg, err := repo.CreateDraftNode(adminCtx, adminConn, draftID, &syllabusNode.ID, "section_goal", s.ordering, json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("CreateDraftNode(section_goal %d): %v", s.ordering, err)
		}
		sgIDs = append(sgIDs, sg.ID)
	}

	// Update statuses via superuser so we avoid needing a full state-machine walk.
	for i, s := range specs {
		if _, err := pool.Exec(context.Background(),
			`UPDATE draft_nodes SET status = $1 WHERE id = $2`, s.status, sgIDs[i],
		); err != nil {
			t.Fatalf("update section_goal[%d] status to %s: %v", i, s.status, err)
		}
	}

	approved, failed, pending, err := repo.LayerAggregate(adminCtx, adminConn, draftID, "section_goal")
	if err != nil {
		t.Fatalf("LayerAggregate: %v", err)
	}
	if approved != 2 {
		t.Errorf("LayerAggregate: want approved=2, got %d", approved)
	}
	if failed != 1 {
		t.Errorf("LayerAggregate: want failed=1, got %d", failed)
	}
	if pending != 2 {
		t.Errorf("LayerAggregate: want pending=2, got %d", pending)
	}
}

// ---------------------------------------------------------------------------
// 5. node_chats (draft side)
// ---------------------------------------------------------------------------

// TestDraftRepo_NodeChats_AppendAndListInOrder verifies AppendDraftNodeChat
// returns the inserted message and that ListDraftNodeChats returns all messages
// in ascending chronological order.
//
// @{"verifies": ["REQ-AGENT-034", "REQ-AGENT-051", "REQ-AGENT-053", "REQ-AGENT-054",
//               "REQ-SYS-075", "REQ-SYS-077"]}
func TestDraftRepo_NodeChats_AppendAndListInOrder(t *testing.T) {
	truncateDraftTables(t)

	adminID := seedDraftAdmin(t, "chat")
	pool := internaldb.IntegrationPool(t)
	repo := NewDraftRepository(pool)

	adminCtx, release := draftAdminConnCtx(t, adminID)
	defer release()
	adminConn := connFromCtx(t, adminCtx)

	draftID, err := repo.CreateDraft(adminCtx, adminConn, adminID, "Chat Draft", "topic", "beginner", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	rootNodes, err := repo.ListDraftNodes(adminCtx, adminConn, draftID, DraftNodeListOpts{NodeTypeFilter: "root"})
	if err != nil || len(rootNodes) == 0 {
		t.Fatalf("ListDraftNodes(root): %v", err)
	}
	nodeID := rootNodes[0].ID

	// Append three chat turns in alternating roles.
	type turnSpec struct {
		role    string
		content string
	}
	turns := []turnSpec{
		{"user", "Please generate a syllabus for Go channels"},
		{"assistant", "Sure! Here is the syllabus ..."},
		{"user", "Add one more section on select statements"},
	}

	for i, turn := range turns {
		msg, err := repo.AppendDraftNodeChat(adminCtx, adminConn, nodeID, turn.role, turn.content)
		if err != nil {
			t.Fatalf("AppendDraftNodeChat[%d]: %v", i, err)
		}
		if msg.ID == uuid.Nil {
			t.Errorf("AppendDraftNodeChat[%d]: returned uuid.Nil", i)
		}
		if msg.Role != turn.role {
			t.Errorf("AppendDraftNodeChat[%d]: want role %q, got %q", i, turn.role, msg.Role)
		}
		if msg.Content != turn.content {
			t.Errorf("AppendDraftNodeChat[%d]: content mismatch", i)
		}
	}

	// ListDraftNodeChats must return all three in created_at ASC order.
	msgs, err := repo.ListDraftNodeChats(adminCtx, adminConn, nodeID)
	if err != nil {
		t.Fatalf("ListDraftNodeChats: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("ListDraftNodeChats: want 3 messages, got %d", len(msgs))
	}
	for i, m := range msgs {
		if m.Role != turns[i].role {
			t.Errorf("ListDraftNodeChats[%d]: want role %q, got %q", i, turns[i].role, m.Role)
		}
		if m.Content != turns[i].content {
			t.Errorf("ListDraftNodeChats[%d]: content mismatch", i)
		}
		// Verify ascending time ordering (each message must be >= the previous).
		if i > 0 && msgs[i].CreatedAt.Before(msgs[i-1].CreatedAt) {
			t.Errorf("ListDraftNodeChats: message[%d] created_at is before message[%d]", i, i-1)
		}
	}
}

// TestDraftRepo_NodeChats_CascadeDeleteWithDraft verifies that deleting a draft
// removes its associated node_chats rows via CASCADE (draft_nodes.draft_id →
// node_chats.draft_node_id both cascade).
//
// @{"verifies": ["REQ-AGENT-034", "REQ-AGENT-057", "REQ-SYS-075", "REQ-SYS-077"]}
func TestDraftRepo_NodeChats_CascadeDeleteWithDraft(t *testing.T) {
	truncateDraftTables(t)

	adminID := seedDraftAdmin(t, "chat_cascade")
	pool := internaldb.IntegrationPool(t)
	repo := NewDraftRepository(pool)

	adminCtx, release := draftAdminConnCtx(t, adminID)
	defer release()
	adminConn := connFromCtx(t, adminCtx)

	draftID, err := repo.CreateDraft(adminCtx, adminConn, adminID, "Chat Cascade Draft", "topic", "beginner", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	rootNodes, err := repo.ListDraftNodes(adminCtx, adminConn, draftID, DraftNodeListOpts{NodeTypeFilter: "root"})
	if err != nil || len(rootNodes) == 0 {
		t.Fatalf("ListDraftNodes(root): %v", err)
	}
	nodeID := rootNodes[0].ID

	if _, err := repo.AppendDraftNodeChat(adminCtx, adminConn, nodeID, "user", "cascade me"); err != nil {
		t.Fatalf("AppendDraftNodeChat: %v", err)
	}

	// Delete the draft (status is 'draft' so it is allowed).
	if err := repo.DeleteDraft(adminCtx, adminConn, draftID); err != nil {
		t.Fatalf("DeleteDraft: %v", err)
	}

	// Verify node_chats are gone using the superuser pool.
	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM node_chats WHERE draft_node_id = $1`, nodeID,
	).Scan(&count); err != nil {
		t.Fatalf("count node_chats after cascade: %v", err)
	}
	if count != 0 {
		t.Errorf("node_chats cascade: want 0 rows after draft delete, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// 6. RLS isolation — the critical facet
// ---------------------------------------------------------------------------

// TestDraftRepo_RLS_AdminA_CannotReadAdminB_Drafts is the cross-admin denial
// test: admin A's connection must return 0 rows when querying admin B's drafts,
// even though admin B's drafts genuinely exist (positive control).
//
// @{"verifies": ["REQ-AGENT-047", "REQ-AGENT-048", "REQ-SYS-075", "REQ-SYS-077"]}
func TestDraftRepo_RLS_AdminA_CannotReadAdminB_Drafts(t *testing.T) {
	truncateDraftTables(t)

	adminAID := seedDraftAdmin(t, "rls_a")
	adminBID := seedDraftAdmin(t, "rls_b")
	pool := internaldb.IntegrationPool(t)
	repo := NewDraftRepository(pool)

	// Seed admin B's draft using admin B's connection (positive control — the
	// draft must exist in the DB for the cross-admin denial to be non-vacuous).
	ctxB, releaseB := draftAdminConnCtx(t, adminBID)
	defer releaseB()
	connB := connFromCtx(t, ctxB)

	draftBID, err := repo.CreateDraft(ctxB, connB, adminBID, "Admin B Draft", "B topic", "beginner", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CreateDraft (admin B): %v", err)
	}

	// Verify admin B can actually see the draft (proves the positive control is valid).
	_, err = repo.GetDraft(ctxB, connB, draftBID)
	if err != nil {
		t.Fatalf("GetDraft (admin B self-check): %v — positive control failure", err)
	}

	// Switch to admin A's connection.
	ctxA, releaseA := draftAdminConnCtx(t, adminAID)
	defer releaseA()
	connA := connFromCtx(t, ctxA)

	// Admin A must NOT see admin B's draft via GetDraft.
	_, err = repo.GetDraft(ctxA, connA, draftBID)
	if err != ErrDraftNotFound {
		t.Errorf("RLS FAILURE: admin A GetDraft(admin B's draft): want ErrDraftNotFound, got %v", err)
	}

	// Admin A must NOT see admin B's draft via ListDrafts.
	rows, _, err := repo.ListDrafts(ctxA, connA, adminBID, DraftListOpts{})
	if err != nil {
		t.Fatalf("ListDrafts (admin A querying admin B's ID): %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("RLS FAILURE: admin A can list admin B's drafts — got %d rows (want 0)", len(rows))
	}
}

// TestDraftRepo_RLS_AdminA_CannotReadAdminB_DraftNodes is the cross-admin
// denial test for draft_nodes: admin A's connection must see 0 draft_nodes
// belonging to admin B's draft.
//
// @{"verifies": ["REQ-AGENT-047", "REQ-AGENT-049", "REQ-SYS-075", "REQ-SYS-077"]}
func TestDraftRepo_RLS_AdminA_CannotReadAdminB_DraftNodes(t *testing.T) {
	truncateDraftTables(t)

	adminAID := seedDraftAdmin(t, "rls_nodes_a")
	adminBID := seedDraftAdmin(t, "rls_nodes_b")
	pool := internaldb.IntegrationPool(t)
	repo := NewDraftRepository(pool)

	// Seed admin B's draft and verify its root node exists.
	ctxB, releaseB := draftAdminConnCtx(t, adminBID)
	defer releaseB()
	connB := connFromCtx(t, ctxB)

	draftBID, err := repo.CreateDraft(ctxB, connB, adminBID, "B Node Draft", "b topic", "beginner", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CreateDraft (admin B): %v", err)
	}

	// Positive control: admin B should see 1 root node.
	bNodes, err := repo.ListDraftNodes(ctxB, connB, draftBID, DraftNodeListOpts{})
	if err != nil || len(bNodes) == 0 {
		t.Fatalf("ListDraftNodes (admin B self-check): %v (count=%d) — positive control failure", err, len(bNodes))
	}

	// Admin A's connection must see 0 nodes for admin B's draft.
	ctxA, releaseA := draftAdminConnCtx(t, adminAID)
	defer releaseA()
	connA := connFromCtx(t, ctxA)

	aNodes, err := repo.ListDraftNodes(ctxA, connA, draftBID, DraftNodeListOpts{})
	if err != nil {
		t.Fatalf("ListDraftNodes (admin A querying B's draft): %v", err)
	}
	if len(aNodes) != 0 {
		t.Errorf("RLS FAILURE: admin A can see %d draft_node(s) belonging to admin B (want 0)", len(aNodes))
	}

	// GetDraftNode for admin B's root node must be invisible to admin A.
	bRootID := bNodes[0].ID
	_, err = repo.GetDraftNode(ctxA, connA, bRootID)
	if err != ErrDraftNodeNotFound {
		t.Errorf("RLS FAILURE: admin A GetDraftNode(admin B's node): want ErrDraftNodeNotFound, got %v", err)
	}
}

// TestDraftRepo_RLS_Student_CannotReadDrafts verifies that a student-role
// connection cannot read any course_drafts row (drafts are admin-only objects).
//
// @{"verifies": ["REQ-AGENT-047", "REQ-SYS-075", "REQ-SYS-077"]}
func TestDraftRepo_RLS_Student_CannotReadDrafts(t *testing.T) {
	truncateDraftTables(t)

	adminID := seedDraftAdmin(t, "rls_student_admin")
	studentID := seedDraftStudent(t, "rls_student")
	pool := internaldb.IntegrationPool(t)
	repo := NewDraftRepository(pool)

	// Seed a real draft using the admin's connection (positive control).
	ctxAdmin, releaseAdmin := draftAdminConnCtx(t, adminID)
	defer releaseAdmin()
	connAdmin := connFromCtx(t, ctxAdmin)

	draftID, err := repo.CreateDraft(ctxAdmin, connAdmin, adminID, "Student-Blocked Draft", "topic", "beginner", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CreateDraft (admin positive control): %v", err)
	}

	// Positive control: admin can read the draft.
	if _, err := repo.GetDraft(ctxAdmin, connAdmin, draftID); err != nil {
		t.Fatalf("GetDraft (admin positive control): %v", err)
	}

	// Student's connection must NOT see the draft via GetDraft.
	ctxStudent, releaseStudent := draftStudentConnCtx(t, studentID)
	defer releaseStudent()
	connStudent := connFromCtx(t, ctxStudent)

	_, err = repo.GetDraft(ctxStudent, connStudent, draftID)
	if err != ErrDraftNotFound {
		t.Errorf("RLS FAILURE: student GetDraft: want ErrDraftNotFound, got %v", err)
	}

	// Student must NOT see the draft via ListDrafts either.
	rows, _, err := repo.ListDrafts(ctxStudent, connStudent, adminID, DraftListOpts{})
	if err != nil {
		t.Fatalf("ListDrafts (student connection): %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("RLS FAILURE: student can list %d draft(s) (want 0)", len(rows))
	}
}

// TestDraftRepo_RLS_Student_CannotReadDraftNodes verifies that a student-role
// connection sees 0 draft_nodes for any draft (the draft_nodes_admin_own policy
// requires app.current_role='admin' via an EXISTS subquery on course_drafts).
//
// @{"verifies": ["REQ-AGENT-044", "REQ-AGENT-049", "REQ-SYS-075", "REQ-SYS-077"]}
func TestDraftRepo_RLS_Student_CannotReadDraftNodes(t *testing.T) {
	truncateDraftTables(t)

	adminID := seedDraftAdmin(t, "rls_node_admin")
	studentID := seedDraftStudent(t, "rls_node_student")
	pool := internaldb.IntegrationPool(t)
	repo := NewDraftRepository(pool)

	ctxAdmin, releaseAdmin := draftAdminConnCtx(t, adminID)
	defer releaseAdmin()
	connAdmin := connFromCtx(t, ctxAdmin)

	draftID, err := repo.CreateDraft(ctxAdmin, connAdmin, adminID, "Node-Blocked Draft", "topic", "beginner", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	// Positive control: admin sees the root node.
	adminNodes, err := repo.ListDraftNodes(ctxAdmin, connAdmin, draftID, DraftNodeListOpts{})
	if err != nil || len(adminNodes) == 0 {
		t.Fatalf("ListDraftNodes (admin positive control): %v (count=%d)", err, len(adminNodes))
	}

	ctxStudent, releaseStudent := draftStudentConnCtx(t, studentID)
	defer releaseStudent()
	connStudent := connFromCtx(t, ctxStudent)

	studentNodes, err := repo.ListDraftNodes(ctxStudent, connStudent, draftID, DraftNodeListOpts{})
	if err != nil {
		t.Fatalf("ListDraftNodes (student connection): %v", err)
	}
	if len(studentNodes) != 0 {
		t.Errorf("RLS FAILURE: student sees %d draft_node(s) (want 0)", len(studentNodes))
	}

	// GetDraftNode must also be invisible to a student-role connection.
	rootID := adminNodes[0].ID
	_, err = repo.GetDraftNode(ctxStudent, connStudent, rootID)
	if err != ErrDraftNodeNotFound {
		t.Errorf("RLS FAILURE: student GetDraftNode: want ErrDraftNodeNotFound, got %v", err)
	}
}

// TestDraftRepo_RLS_ServerRole_CanReadAndWriteDrafts verifies that a
// server-role connection (the generation pipeline path) can insert and read
// drafts and draft_nodes across all admins (course_drafts_server_write /
// draft_nodes_server_write policies).
//
// @{"verifies": ["REQ-AGENT-047", "REQ-AGENT-048", "REQ-AGENT-049", "REQ-SYS-075",
//               "REQ-SYS-077"]}
func TestDraftRepo_RLS_ServerRole_CanReadAndWriteDrafts(t *testing.T) {
	truncateDraftTables(t)

	adminID := seedDraftAdmin(t, "rls_server")
	pool := internaldb.IntegrationPool(t)
	repo := NewDraftRepository(pool)

	// Use the server-role connection for both the create and the subsequent read.
	serverCtx, releaseServer := draftServerConnCtx(t)
	defer releaseServer()
	serverConn := connFromCtx(t, serverCtx)

	// Server role creates a draft on behalf of the admin.
	draftID, err := repo.CreateDraft(serverCtx, serverConn, adminID, "Server Draft", "topic", "beginner", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CreateDraft (server role): %v", err)
	}

	// Server role must be able to read the draft back.
	draft, err := repo.GetDraft(serverCtx, serverConn, draftID)
	if err != nil {
		t.Fatalf("GetDraft (server role): %v", err)
	}
	if draft.ID != draftID {
		t.Errorf("GetDraft (server role): want ID %s, got %s", draftID, draft.ID)
	}

	// Server role must be able to read draft_nodes.
	nodes, err := repo.ListDraftNodes(serverCtx, serverConn, draftID, DraftNodeListOpts{})
	if err != nil {
		t.Fatalf("ListDraftNodes (server role): %v", err)
	}
	if len(nodes) == 0 {
		t.Error("ListDraftNodes (server role): want at least 1 node (root), got 0")
	}

	// Server role must be able to create additional draft_nodes.
	rootID := nodes[0].ID
	childNode, err := repo.CreateDraftNode(serverCtx, serverConn, draftID, &rootID, "syllabus", 0, json.RawMessage(`{"server":true}`))
	if err != nil {
		t.Fatalf("CreateDraftNode (server role): %v", err)
	}
	if childNode.ID == uuid.Nil {
		t.Error("CreateDraftNode (server role): returned uuid.Nil")
	}

	// Server role must be able to append node chat messages.
	msg, err := repo.AppendDraftNodeChat(serverCtx, serverConn, rootID, "assistant", "server says hello")
	if err != nil {
		t.Fatalf("AppendDraftNodeChat (server role): %v", err)
	}
	if msg.ID == uuid.Nil {
		t.Error("AppendDraftNodeChat (server role): returned uuid.Nil")
	}
}
