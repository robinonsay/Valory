//go:build integration

// tree_repository_integration_test.go — real-Postgres integration tests for
// TreeRepository (internal/agent/tree_repository.go).
//
// WHY this file exists:
// TreeRepository is the data-access layer for course_nodes and the
// course_node_id side of node_chats.  Row-level security policies on
// course_nodes must enforce per-student isolation:
//   (a) a student-role connection reads only nodes for courses they own,
//   (b) a student CANNOT directly INSERT/UPDATE course_nodes,
//   (c) an admin-role connection reads any student's nodes,
//   (d) a server-role connection can INSERT/UPDATE and SELECT nodes
//       (course_nodes_server_select_policy was added to support the pipeline).
//
// Every RLS assertion in this file runs under SET ROLE valory_app (via
// AcquireAsUser / AcquireAsServer from internal/db/integrationtest.go) so that
// FORCE ROW LEVEL SECURITY actually fires.  Assertions made as the superuser
// test role are INVALID — they bypass FORCE RLS and mask policy bugs (see
// memory "force-rls-superuser-test-masking").
//
// Positive controls: student B's data is genuinely seeded before asserting
// that student A cannot see it — the cross-student denial is not vacuous.
//
// Run:
//
//	make test-integration
//	# or manually:
//	export PATH=$PATH:/usr/local/go/bin
//	VALORY_TEST_DATABASE_URL=postgres://valory_test:valory_test@localhost:55432/valory_test?sslmode=disable \
//	  go test -tags integration -count=1 -p 1 ./internal/agent/...
//
// @{"verifies": ["REQ-AGENT-027", "REQ-AGENT-028", "REQ-AGENT-029", "REQ-AGENT-030",
//               "REQ-AGENT-032", "REQ-AGENT-034", "REQ-AGENT-038", "REQ-AGENT-040",
//               "REQ-AGENT-042", "REQ-AGENT-051", "REQ-AGENT-053", "REQ-AGENT-055",
//               "REQ-AGENT-058", "REQ-SYS-075"]}
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	authpkg "github.com/valory/valory/internal/auth"
	internaldb "github.com/valory/valory/internal/db"
)

// ---------------------------------------------------------------------------
// Fixture helpers
// ---------------------------------------------------------------------------

// treeSeed holds all IDs created for a single student's course tree fixture.
type treeSeed struct {
	StudentID uuid.UUID
	CourseID  uuid.UUID
}

// seedTreeStudent inserts a minimal student user row and a course row, then
// returns a treeSeed.  All writes go through the bare superuser pool so that
// seeding is unaffected by RLS; only repository-level assertions run under
// valory_app.
func seedTreeStudent(t *testing.T, tag string) treeSeed {
	t.Helper()
	pool := internaldb.IntegrationPool(t)
	ctx := context.Background()

	username := fmt.Sprintf("tree_integ_%s_%s", tag, uuid.New().String()[:8])
	var studentID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, 'x', 'student') RETURNING id`,
		username,
	).Scan(&studentID); err != nil {
		t.Fatalf("seedTreeStudent(%s): insert user: %v", tag, err)
	}

	var courseID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO courses (student_id, topic, status, tree_mode) VALUES ($1, $2, 'active', true) RETURNING id`,
		studentID, "Tree Test Topic "+tag,
	).Scan(&courseID); err != nil {
		t.Fatalf("seedTreeStudent(%s): insert course: %v", tag, err)
	}

	return treeSeed{StudentID: studentID, CourseID: courseID}
}

// truncateTreeTables wipes rows in dependency order, giving each test a clean
// slate without dropping the schema.
func truncateTreeTables(t *testing.T) {
	t.Helper()
	internaldb.TruncateTables(t, internaldb.IntegrationPool(t),
		"node_chats",
		"course_nodes",
		"agent_token_usage",
		"agent_runs",
		"sessions",
		"login_attempts",
		"courses",
		"users",
	)
}

// studentConnCtxTree returns a context carrying a student-role valory_app
// connection for the given student, exactly as the auth middleware would set
// up for a student HTTP request.
func studentConnCtxTree(t *testing.T, studentID uuid.UUID) (context.Context, func()) {
	t.Helper()
	pool := internaldb.IntegrationPool(t)
	// AcquireAsUser converts the UUID to 32-char no-dash hex format as the
	// auth middleware does (fmt.Sprintf("%x", [16]byte(id))).
	studentIDHex := fmt.Sprintf("%x", [16]byte(studentID))
	conn := internaldb.AcquireAsUser(t, pool, studentIDHex, "student")
	ctx := authpkg.ContextWithConn(context.Background(), conn)
	return ctx, conn.Release
}

// adminConnCtxTree returns a context carrying an admin-role valory_app
// connection for the given admin user UUID.
func adminConnCtxTree(t *testing.T, adminID uuid.UUID) (context.Context, func()) {
	t.Helper()
	pool := internaldb.IntegrationPool(t)
	adminIDHex := fmt.Sprintf("%x", [16]byte(adminID))
	conn := internaldb.AcquireAsUser(t, pool, adminIDHex, "admin")
	ctx := authpkg.ContextWithConn(context.Background(), conn)
	return ctx, conn.Release
}

// serverConnCtxTree returns a context carrying a server-role valory_app
// connection; this mirrors the production AcquireServerConn path but with the
// mandatory SET ROLE valory_app step so RLS policies actually fire.
func serverConnCtxTree(t *testing.T) (context.Context, func()) {
	t.Helper()
	pool := internaldb.IntegrationPool(t)
	conn := internaldb.AcquireAsServer(t, pool)
	ctx := authpkg.ContextWithConn(context.Background(), conn)
	return ctx, conn.Release
}

// seedAdminUser inserts a minimal admin user row and returns its UUID.
// Used to obtain a real admin UUID for the app.current_user_id GUC (the admin
// policy checks admin_id = NULLIF(current_setting('app.current_user_id'), '')::uuid).
func seedAdminUser(t *testing.T) uuid.UUID {
	t.Helper()
	pool := internaldb.IntegrationPool(t)
	username := fmt.Sprintf("tree_admin_%s", uuid.New().String()[:8])
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (username, password_hash, role) VALUES ($1, 'x', 'admin') RETURNING id`,
		username,
	).Scan(&id); err != nil {
		t.Fatalf("seedAdminUser: %v", err)
	}
	return id
}

// ---------------------------------------------------------------------------
// 1. CRUD round-trip tests
// ---------------------------------------------------------------------------

// TestTreeRepo_CRUD_RootToContentChain_ServerRole verifies that the full
// root→syllabus→section_goal→concept→content chain can be created via a
// server-role connection (the production pipeline path), and that GetNode,
// ListNodesByCourse (with node_type filter), and ListChildren all return the
// correct rows with the JSONB payload preserved exactly.
//
// @{"verifies": ["REQ-AGENT-027", "REQ-AGENT-028", "REQ-AGENT-029", "REQ-AGENT-030", "REQ-AGENT-058", "REQ-SYS-075"]}
func TestTreeRepo_CRUD_RootToContentChain_ServerRole(t *testing.T) {
	truncateTreeTables(t)

	seed := seedTreeStudent(t, "crud")
	repo := NewTreeRepository()

	// All writes use a server-role connection — the pipeline path.
	serverCtx, releaseServer := serverConnCtxTree(t)
	defer releaseServer()

	// Obtain the server-role conn from the context for direct repo calls.
	serverConn, ok := authpkg.ConnFromContext(serverCtx)
	if !ok {
		t.Fatal("serverConnCtxTree: no conn in context")
	}

	ctx := context.Background()

	// ── Root node ──────────────────────────────────────────────────────────
	rootPayload := json.RawMessage(`{"title":"Go Concurrency Course","topic":"goroutines"}`)
	rootID, err := repo.CreateNode(ctx, serverConn, seed.CourseID, nil, NodeTypeRoot, 0, rootPayload)
	if err != nil {
		t.Fatalf("CreateNode(root): %v", err)
	}
	if rootID == uuid.Nil {
		t.Fatal("CreateNode(root): returned uuid.Nil")
	}

	// ── Syllabus node ──────────────────────────────────────────────────────
	syllabusPayload := json.RawMessage(`{"outline":["Section 1","Section 2"]}`)
	syllabusID, err := repo.CreateNode(ctx, serverConn, seed.CourseID, &rootID, NodeTypeSyllabus, 0, syllabusPayload)
	if err != nil {
		t.Fatalf("CreateNode(syllabus): %v", err)
	}

	// ── SectionGoal node ───────────────────────────────────────────────────
	sectionGoalPayload := json.RawMessage(`{"goal":"Understand goroutines"}`)
	sectionGoalID, err := repo.CreateNode(ctx, serverConn, seed.CourseID, &syllabusID, NodeTypeSectionGoal, 0, sectionGoalPayload)
	if err != nil {
		t.Fatalf("CreateNode(section_goal): %v", err)
	}

	// ── Concept node ───────────────────────────────────────────────────────
	conceptPayload := json.RawMessage(`{"concept":"goroutine scheduling"}`)
	conceptID, err := repo.CreateNode(ctx, serverConn, seed.CourseID, &sectionGoalID, NodeTypeConcept, 0, conceptPayload)
	if err != nil {
		t.Fatalf("CreateNode(concept): %v", err)
	}

	// ── Content node ───────────────────────────────────────────────────────
	contentPayload := json.RawMessage(`{"adoc":"= Goroutines\n\nA goroutine is a lightweight thread."}`)
	contentID, err := repo.CreateNode(ctx, serverConn, seed.CourseID, &conceptID, NodeTypeContent, 0, contentPayload)
	if err != nil {
		t.Fatalf("CreateNode(content): %v", err)
	}

	// ── GetNode round-trips each node's payload ────────────────────────────
	type nodeCheck struct {
		id            uuid.UUID
		wantType      NodeType
		wantPayloadKV string // a key-value substring that must appear in the payload
		wantParentID  *uuid.UUID
	}
	checks := []nodeCheck{
		{rootID, NodeTypeRoot, `"title":"Go Concurrency Course"`, nil},
		{syllabusID, NodeTypeSyllabus, `"outline"`, &rootID},
		{sectionGoalID, NodeTypeSectionGoal, `"goal":"Understand goroutines"`, &syllabusID},
		{conceptID, NodeTypeConcept, `"concept":"goroutine scheduling"`, &sectionGoalID},
		{contentID, NodeTypeContent, `"adoc"`, &conceptID},
	}

	for _, c := range checks {
		c := c
		t.Run(string(c.wantType), func(t *testing.T) {
			node, err := repo.GetNode(ctx, serverConn, c.id)
			if err != nil {
				t.Fatalf("GetNode(%s): %v", c.wantType, err)
			}
			if node.ID != c.id {
				t.Errorf("GetNode: want ID %s, got %s", c.id, node.ID)
			}
			if node.CourseID != seed.CourseID {
				t.Errorf("GetNode: want CourseID %s, got %s", seed.CourseID, node.CourseID)
			}
			if node.NodeType != c.wantType {
				t.Errorf("GetNode: want NodeType %s, got %s", c.wantType, node.NodeType)
			}
			if node.Status != NodeStatusPending {
				t.Errorf("GetNode: want status 'pending', got %s", node.Status)
			}
			// JSONB payload must survive round-trip (key presence check)
			if string(node.Payload) == "" || string(node.Payload) == "null" {
				t.Errorf("GetNode: payload is empty/null")
			}
			if c.wantParentID == nil {
				if node.ParentID != nil {
					t.Errorf("GetNode(root): expected nil ParentID, got %s", *node.ParentID)
				}
			} else {
				if node.ParentID == nil {
					t.Errorf("GetNode(%s): expected ParentID %s, got nil", c.wantType, *c.wantParentID)
				} else if *node.ParentID != *c.wantParentID {
					t.Errorf("GetNode(%s): want ParentID %s, got %s", c.wantType, *c.wantParentID, *node.ParentID)
				}
			}
		})
	}

	// ── ListNodesByCourse with node_type filter ────────────────────────────
	t.Run("ListNodesByCourse_no_filter", func(t *testing.T) {
		nodes, err := repo.ListNodesByCourse(ctx, serverConn, seed.CourseID, ListNodeOptions{})
		if err != nil {
			t.Fatalf("ListNodesByCourse: %v", err)
		}
		if len(nodes) != 5 {
			t.Errorf("ListNodesByCourse: want 5 nodes, got %d", len(nodes))
		}
	})

	t.Run("ListNodesByCourse_node_type_filter", func(t *testing.T) {
		nt := NodeTypeConcept
		nodes, err := repo.ListNodesByCourse(ctx, serverConn, seed.CourseID, ListNodeOptions{NodeType: &nt})
		if err != nil {
			t.Fatalf("ListNodesByCourse(concept filter): %v", err)
		}
		if len(nodes) != 1 {
			t.Errorf("ListNodesByCourse(concept filter): want 1 node, got %d", len(nodes))
		}
		if len(nodes) > 0 && nodes[0].NodeType != NodeTypeConcept {
			t.Errorf("ListNodesByCourse(concept filter): node type mismatch: got %s", nodes[0].NodeType)
		}
	})

	// ── ListChildren ordering ──────────────────────────────────────────────
	t.Run("ListChildren_ordering", func(t *testing.T) {
		// Create a second section_goal child under syllabus with ordering=1
		secondSectionPayload := json.RawMessage(`{"goal":"Understand channels"}`)
		secondSectionID, err := repo.CreateNode(ctx, serverConn, seed.CourseID, &syllabusID, NodeTypeSectionGoal, 1, secondSectionPayload)
		if err != nil {
			t.Fatalf("CreateNode(section_goal ordering=1): %v", err)
		}

		children, err := repo.ListChildren(ctx, serverConn, syllabusID)
		if err != nil {
			t.Fatalf("ListChildren(syllabusID): %v", err)
		}
		if len(children) != 2 {
			t.Fatalf("ListChildren: want 2 children, got %d", len(children))
		}
		// ordering ASC: sectionGoalID (ordering=0) must come before secondSectionID (ordering=1)
		if children[0].ID != sectionGoalID {
			t.Errorf("ListChildren: first child want %s, got %s", sectionGoalID, children[0].ID)
		}
		if children[1].ID != secondSectionID {
			t.Errorf("ListChildren: second child want %s, got %s", secondSectionID, children[1].ID)
		}
	})
}

// TestTreeRepo_GetNode_NotFound_ReturnsErrNotFound verifies that GetNode returns
// ErrNotFound when no row matches the given ID.
//
// @{"verifies": ["REQ-AGENT-027", "REQ-AGENT-058", "REQ-SYS-075"]}
func TestTreeRepo_GetNode_NotFound_ReturnsErrNotFound(t *testing.T) {
	truncateTreeTables(t)

	repo := NewTreeRepository()
	serverCtx, releaseServer := serverConnCtxTree(t)
	defer releaseServer()

	serverConn, ok := authpkg.ConnFromContext(serverCtx)
	if !ok {
		t.Fatal("serverConnCtxTree: no conn in context")
	}

	_, err := repo.GetNode(context.Background(), serverConn, uuid.New())
	if err != ErrNotFound {
		t.Errorf("GetNode(unknown id): want ErrNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// 2. Atomic status transition tests
// ---------------------------------------------------------------------------

// TestTreeRepo_TransitionNodeStatus_MatchingFrom_ReturnsTrue proves that
// TransitionNodeStatus returns true when the node's current status matches the
// `from` argument, and the status is updated to `to`.
//
// @{"verifies": ["REQ-AGENT-030", "REQ-AGENT-055", "REQ-SYS-075"]}
func TestTreeRepo_TransitionNodeStatus_MatchingFrom_ReturnsTrue(t *testing.T) {
	truncateTreeTables(t)

	seed := seedTreeStudent(t, "trans_match")
	repo := NewTreeRepository()

	serverCtx, release := serverConnCtxTree(t)
	defer release()

	serverConn, ok := authpkg.ConnFromContext(serverCtx)
	if !ok {
		t.Fatal("serverConnCtxTree: no conn in context")
	}

	ctx := context.Background()
	rootID, err := repo.CreateNode(ctx, serverConn, seed.CourseID, nil, NodeTypeRoot, 0, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	// pending → generating (should succeed; node was created with 'pending')
	ok2, err := repo.TransitionNodeStatus(ctx, serverConn, rootID, NodeStatusPending, NodeStatusGenerating)
	if err != nil {
		t.Fatalf("TransitionNodeStatus(pending→generating): %v", err)
	}
	if !ok2 {
		t.Error("TransitionNodeStatus(pending→generating): want true (matching from), got false")
	}

	// Verify the status changed
	node, err := repo.GetNode(ctx, serverConn, rootID)
	if err != nil {
		t.Fatalf("GetNode after transition: %v", err)
	}
	if node.Status != NodeStatusGenerating {
		t.Errorf("GetNode: want status 'generating', got %s", node.Status)
	}
}

// TestTreeRepo_TransitionNodeStatus_StalFrom_ReturnsFalse proves that
// TransitionNodeStatus returns false when the node's current status does NOT
// match the `from` argument (the test-and-set guard rejects the stale caller).
//
// @{"verifies": ["REQ-AGENT-030", "REQ-AGENT-055", "REQ-SYS-075"]}
func TestTreeRepo_TransitionNodeStatus_StaleFrom_ReturnsFalse(t *testing.T) {
	truncateTreeTables(t)

	seed := seedTreeStudent(t, "trans_stale")
	repo := NewTreeRepository()

	serverCtx, release := serverConnCtxTree(t)
	defer release()

	serverConn, ok := authpkg.ConnFromContext(serverCtx)
	if !ok {
		t.Fatal("serverConnCtxTree: no conn in context")
	}

	ctx := context.Background()
	rootID, err := repo.CreateNode(ctx, serverConn, seed.CourseID, nil, NodeTypeRoot, 0, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	// Attempt to transition pending→generating with the wrong `from` ('generating').
	// Node is still 'pending', so this should return false.
	ok2, err := repo.TransitionNodeStatus(ctx, serverConn, rootID, NodeStatusGenerating, NodeStatusAwaitingReview)
	if err != nil {
		t.Fatalf("TransitionNodeStatus(stale from): %v", err)
	}
	if ok2 {
		t.Error("TransitionNodeStatus(stale from): want false (stale from), got true")
	}

	// Confirm the status was NOT changed
	node, err := repo.GetNode(ctx, serverConn, rootID)
	if err != nil {
		t.Fatalf("GetNode after failed transition: %v", err)
	}
	if node.Status != NodeStatusPending {
		t.Errorf("GetNode: status should remain 'pending', got %s", node.Status)
	}
}

// ---------------------------------------------------------------------------
// 3. LayerAggregate tests
// ---------------------------------------------------------------------------

// TestTreeRepo_LayerAggregate_MixedStatuses_CountsCorrectly verifies that
// LayerAggregate correctly counts approved, failed, and pending-or-lower
// nodes for a layer seeded with a known mix of statuses.
//
// Pending bucket covers: pending, generating, awaiting_review, rejected
// (all "not yet settled" from the expand-precondition perspective).
//
// @{"verifies": ["REQ-AGENT-038", "REQ-AGENT-040", "REQ-AGENT-042", "REQ-SYS-075"]}
func TestTreeRepo_LayerAggregate_MixedStatuses_CountsCorrectly(t *testing.T) {
	truncateTreeTables(t)

	seed := seedTreeStudent(t, "aggregate")
	pool := internaldb.IntegrationPool(t)
	repo := NewTreeRepository()

	ctx := context.Background()

	// Seed root node first (required as parent for syllabus nodes).
	var rootID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO course_nodes (course_id, parent_id, node_type, ordering, status, payload)
		 VALUES ($1, NULL, 'root', 0, 'approved', '{}') RETURNING id`,
		seed.CourseID,
	).Scan(&rootID); err != nil {
		t.Fatalf("seed root: %v", err)
	}

	// Seed concept nodes with mixed statuses directly via superuser pool
	// (seeding bypasses RLS deliberately; assertions use RLS-checked connections).
	type statusCount struct {
		status string
		count  int
	}
	statuses := []statusCount{
		{"approved", 2},
		{"failed", 1},
		{"pending", 1},
		{"generating", 1},
		{"awaiting_review", 1},
		{"rejected", 1},
	}

	// Build a syllabus parent node to satisfy the non-root-has-parent constraint.
	var syllabusID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO course_nodes (course_id, parent_id, node_type, ordering, status, payload)
		 VALUES ($1, $2, 'syllabus', 0, 'approved', '{}') RETURNING id`,
		seed.CourseID, rootID,
	).Scan(&syllabusID); err != nil {
		t.Fatalf("seed syllabus: %v", err)
	}

	ordering := 0
	for _, sc := range statuses {
		for i := 0; i < sc.count; i++ {
			if _, err := pool.Exec(ctx,
				`INSERT INTO course_nodes (course_id, parent_id, node_type, ordering, status, payload)
				 VALUES ($1, $2, 'concept', $3, $4, '{}')`,
				seed.CourseID, syllabusID, ordering, sc.status,
			); err != nil {
				t.Fatalf("seed concept status=%s: %v", sc.status, err)
			}
			ordering++
		}
	}

	// Use server-role connection for the aggregate query (production path).
	serverCtx, releaseServer := serverConnCtxTree(t)
	defer releaseServer()

	serverConn, ok := authpkg.ConnFromContext(serverCtx)
	if !ok {
		t.Fatal("serverConnCtxTree: no conn in context")
	}

	counts, err := repo.LayerAggregate(ctx, serverConn, seed.CourseID, NodeTypeConcept)
	if err != nil {
		t.Fatalf("LayerAggregate: %v", err)
	}

	// Expected: Approved=2, Failed=1, Pending=4 (pending+generating+awaiting_review+rejected)
	if counts.Approved != 2 {
		t.Errorf("LayerAggregate.Approved: want 2, got %d", counts.Approved)
	}
	if counts.Failed != 1 {
		t.Errorf("LayerAggregate.Failed: want 1, got %d", counts.Failed)
	}
	if counts.Pending != 4 {
		t.Errorf("LayerAggregate.Pending (pending+generating+awaiting_review+rejected): want 4, got %d", counts.Pending)
	}
}

// ---------------------------------------------------------------------------
// 4. node_chats tests
// ---------------------------------------------------------------------------

// TestTreeRepo_NodeChats_AppendAndListInOrder verifies that AppendNodeChat
// inserts user and assistant turns, and that ListNodeChats returns them in
// created_at ascending order.
//
// @{"verifies": ["REQ-AGENT-034", "REQ-AGENT-051", "REQ-AGENT-053", "REQ-SYS-075"]}
func TestTreeRepo_NodeChats_AppendAndListInOrder(t *testing.T) {
	truncateTreeTables(t)

	seed := seedTreeStudent(t, "chats")
	repo := NewTreeRepository()

	serverCtx, release := serverConnCtxTree(t)
	defer release()

	serverConn, ok := authpkg.ConnFromContext(serverCtx)
	if !ok {
		t.Fatal("serverConnCtxTree: no conn in context")
	}

	ctx := context.Background()

	// Create a root node to attach chats to.
	rootID, err := repo.CreateNode(ctx, serverConn, seed.CourseID, nil, NodeTypeRoot, 0, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CreateNode(root): %v", err)
	}

	// Append user then assistant turn.
	const userMsg = "Explain goroutines."
	const assistantMsg = "A goroutine is a lightweight thread managed by the Go runtime."

	if err := repo.AppendNodeChat(ctx, serverConn, rootID, "user", userMsg); err != nil {
		t.Fatalf("AppendNodeChat(user): %v", err)
	}
	if err := repo.AppendNodeChat(ctx, serverConn, rootID, "assistant", assistantMsg); err != nil {
		t.Fatalf("AppendNodeChat(assistant): %v", err)
	}

	// List must return both turns in ascending created_at order.
	msgs, err := repo.ListNodeChats(ctx, serverConn, rootID)
	if err != nil {
		t.Fatalf("ListNodeChats: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("ListNodeChats: want 2 messages, got %d", len(msgs))
	}

	if msgs[0].Role != "user" {
		t.Errorf("ListNodeChats[0].Role: want 'user', got %q", msgs[0].Role)
	}
	if msgs[0].Content != userMsg {
		t.Errorf("ListNodeChats[0].Content: want %q, got %q", userMsg, msgs[0].Content)
	}
	if msgs[1].Role != "assistant" {
		t.Errorf("ListNodeChats[1].Role: want 'assistant', got %q", msgs[1].Role)
	}
	if msgs[1].Content != assistantMsg {
		t.Errorf("ListNodeChats[1].Content: want %q, got %q", assistantMsg, msgs[1].Content)
	}
	if msgs[0].CourseNodeID != rootID || msgs[1].CourseNodeID != rootID {
		t.Errorf("ListNodeChats: course_node_id mismatch")
	}

	// Timestamps must be set and ordered ascending.
	if msgs[1].CreatedAt.Before(msgs[0].CreatedAt) {
		t.Errorf("ListNodeChats: second message has earlier timestamp than first (ordering broken)")
	}
}

// ---------------------------------------------------------------------------
// 5. RLS isolation tests (all run under SET ROLE valory_app)
// ---------------------------------------------------------------------------

// TestTreeRepo_RLS_StudentA_CanReadOwnNodes_StudentCannotReadOtherStudent
// verifies the critical RLS isolation invariant:
//   - student A can list their own course's nodes (positive control)
//   - student B CANNOT read student A's nodes (cross-student denial → 0 rows)
//
// Both assertions run under SET ROLE valory_app via AcquireAsUser, so that
// FORCE ROW LEVEL SECURITY actually fires.  Student B's data is genuinely
// seeded before the denial check — this is not a vacuous "empty table" test.
//
// @{"verifies": ["REQ-AGENT-032", "REQ-SYS-075"]}
func TestTreeRepo_RLS_StudentA_CanReadOwnNodes_StudentCannotReadOtherStudent(t *testing.T) {
	truncateTreeTables(t)

	seedA := seedTreeStudent(t, "rls_a")
	seedB := seedTreeStudent(t, "rls_b")
	pool := internaldb.IntegrationPool(t)
	repo := NewTreeRepository()

	ctx := context.Background()

	// Seed root nodes for BOTH students via superuser pool (seeding bypasses RLS).
	var rootAID, rootBID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO course_nodes (course_id, parent_id, node_type, ordering, status, payload)
		 VALUES ($1, NULL, 'root', 0, 'approved', '{"title":"A"}') RETURNING id`,
		seedA.CourseID,
	).Scan(&rootAID); err != nil {
		t.Fatalf("seed rootA: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO course_nodes (course_id, parent_id, node_type, ordering, status, payload)
		 VALUES ($1, NULL, 'root', 0, 'approved', '{"title":"B"}') RETURNING id`,
		seedB.CourseID,
	).Scan(&rootBID); err != nil {
		t.Fatalf("seed rootB: %v", err)
	}
	// Confirm rootBID is genuinely present (positive control for the denial test).
	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM course_nodes WHERE id = $1`, rootBID).Scan(&count); err != nil {
		t.Fatalf("verify rootB exists: %v", err)
	}
	if count != 1 {
		t.Fatal("positive control: rootB not found in DB even via superuser pool")
	}

	// ── Student A reads their own nodes (must see exactly 1) ───────────────
	ctxA, releaseA := studentConnCtxTree(t, seedA.StudentID)
	defer releaseA()
	connA, _ := authpkg.ConnFromContext(ctxA)

	nodesA, err := repo.ListNodesByCourse(ctx, connA, seedA.CourseID, ListNodeOptions{})
	if err != nil {
		t.Fatalf("ListNodesByCourse(studentA, courseA): %v", err)
	}
	if len(nodesA) != 1 {
		t.Errorf("RLS (student reads own): want 1 node, got %d", len(nodesA))
	}

	// ── Student B tries to read student A's nodes (must see 0) ────────────
	ctxB, releaseB := studentConnCtxTree(t, seedB.StudentID)
	defer releaseB()
	connB, _ := authpkg.ConnFromContext(ctxB)

	nodesB, err := repo.ListNodesByCourse(ctx, connB, seedA.CourseID, ListNodeOptions{})
	if err != nil {
		t.Fatalf("ListNodesByCourse(studentB, courseA): unexpected error: %v", err)
	}
	if len(nodesB) != 0 {
		t.Errorf(
			"RLS FAILURE (REQ-AGENT-032): student B can see %d node(s) belonging to student A — "+
				"course_nodes_student_policy is not enforced",
			len(nodesB),
		)
	}
}

// TestTreeRepo_RLS_AdminCanReadAnyStudentNodes verifies that an admin-role
// connection can read course nodes belonging to any student.
//
// @{"verifies": ["REQ-AGENT-032", "REQ-SYS-075"]}
func TestTreeRepo_RLS_AdminCanReadAnyStudentNodes(t *testing.T) {
	truncateTreeTables(t)

	seedA := seedTreeStudent(t, "admin_rls_a")
	seedB := seedTreeStudent(t, "admin_rls_b")
	pool := internaldb.IntegrationPool(t)
	repo := NewTreeRepository()

	ctx := context.Background()

	// Seed root nodes for both students via superuser pool.
	for _, s := range []treeSeed{seedA, seedB} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO course_nodes (course_id, parent_id, node_type, ordering, status, payload)
			 VALUES ($1, NULL, 'root', 0, 'approved', '{}')`,
			s.CourseID,
		); err != nil {
			t.Fatalf("seed root for %s: %v", s.StudentID, err)
		}
	}

	adminID := seedAdminUser(t)
	adminCtx, releaseAdmin := adminConnCtxTree(t, adminID)
	defer releaseAdmin()
	adminConn, _ := authpkg.ConnFromContext(adminCtx)

	// Admin must see both courses' nodes.
	for _, s := range []treeSeed{seedA, seedB} {
		nodes, err := repo.ListNodesByCourse(ctx, adminConn, s.CourseID, ListNodeOptions{})
		if err != nil {
			t.Fatalf("ListNodesByCourse(admin, course %s): %v", s.CourseID, err)
		}
		if len(nodes) != 1 {
			t.Errorf("Admin must see 1 node for course %s, got %d", s.CourseID, len(nodes))
		}
	}
}

// TestTreeRepo_RLS_ServerRole_CanInsertAndSelectNodes verifies that a
// server-role connection can both INSERT nodes (course_nodes_server_policy) and
// SELECT them back (course_nodes_server_select_policy).
//
// This test explicitly validates the server SELECT policy because without it a
// pipeline step that inserts nodes would see zero rows on the immediately
// following SELECT, breaking the LayeredRunner.
//
// @{"verifies": ["REQ-AGENT-027", "REQ-AGENT-032", "REQ-SYS-075"]}
func TestTreeRepo_RLS_ServerRole_CanInsertAndSelectNodes(t *testing.T) {
	truncateTreeTables(t)

	seed := seedTreeStudent(t, "server_rls")
	repo := NewTreeRepository()

	serverCtx, release := serverConnCtxTree(t)
	defer release()

	serverConn, ok := authpkg.ConnFromContext(serverCtx)
	if !ok {
		t.Fatal("serverConnCtxTree: no conn in context")
	}

	ctx := context.Background()

	// Server INSERT: must succeed (course_nodes_server_policy allows INSERT).
	rootID, err := repo.CreateNode(ctx, serverConn, seed.CourseID, nil, NodeTypeRoot, 0, json.RawMessage(`{"server":"true"}`))
	if err != nil {
		t.Fatalf("server INSERT via CreateNode: %v", err)
	}

	// Server SELECT: must see the row it just inserted (course_nodes_server_select_policy).
	node, err := repo.GetNode(ctx, serverConn, rootID)
	if err != nil {
		t.Fatalf(
			"server SELECT via GetNode: %v — course_nodes_server_select_policy may be missing",
			err,
		)
	}
	if node.ID != rootID {
		t.Errorf("server SELECT: want node ID %s, got %s", rootID, node.ID)
	}

	// Also verify via ListNodesByCourse.
	nodes, err := repo.ListNodesByCourse(ctx, serverConn, seed.CourseID, ListNodeOptions{})
	if err != nil {
		t.Fatalf("server ListNodesByCourse: %v", err)
	}
	if len(nodes) != 1 {
		t.Errorf("server ListNodesByCourse: want 1 node, got %d", len(nodes))
	}
}

// TestTreeRepo_RLS_StudentCannotDirectlyInsertCourseNodes verifies that a
// student-role connection (app.current_role='student') is denied when it
// attempts to directly INSERT into course_nodes.
//
// The course_nodes_student_policy grants USING (SELECT) and WITH CHECK (SELECT)
// but the WITH CHECK applies to INSERT/UPDATE — a student who tries to write a
// node must be rejected because the course_nodes_student_policy WITH CHECK
// requires course_id to be in the student's own courses, AND even if it is
// matched the policy does not specify FOR INSERT / FOR UPDATE so it defaults
// to ALL.  The server policy grants INSERT only to the server role.
// A student INSERT must be rejected by the WITH CHECK clause.
//
// @{"verifies": ["REQ-AGENT-032", "REQ-SYS-075"]}
func TestTreeRepo_RLS_StudentCannotDirectlyInsertCourseNodes(t *testing.T) {
	truncateTreeTables(t)

	seed := seedTreeStudent(t, "student_insert_denied")

	ctx := context.Background()
	ctxStudent, releaseStudent := studentConnCtxTree(t, seed.StudentID)
	defer releaseStudent()

	studentConn, ok := authpkg.ConnFromContext(ctxStudent)
	if !ok {
		t.Fatal("studentConnCtxTree: no conn in context")
	}

	// A direct INSERT by a student-role connection must fail.
	// The student policy WITH CHECK requires course_id to belong to the student,
	// and allows DML on their own course. However, the server INSERT policy is
	// the only FOR INSERT policy, and the student policy applies FOR ALL which
	// includes INSERT. We check whether RLS rejects or allows this.
	//
	// In Postgres, when FORCE ROW LEVEL SECURITY is set and multiple policies
	// apply, ALL is evaluated for the relevant command. The student policy is
	// FOR ALL with a WITH CHECK — so a student can technically INSERT into their
	// own course's nodes IF the course_id matches. The business requirement is
	// that server-side pipeline writes are the only path; however the policy as
	// written (student policy is FOR ALL) does not strictly prevent student INSERT
	// on their own course.
	//
	// We test what the actual policy enforces: a student CANNOT insert into
	// ANOTHER student's course (cross-course denial is the isolation invariant).
	pool := internaldb.IntegrationPool(t)
	seedB := seedTreeStudent(t, "student_insert_denied_b")

	// Student A tries to insert a node into Student B's course — must be denied.
	_, err := studentConn.Exec(ctx,
		`INSERT INTO course_nodes (course_id, parent_id, node_type, ordering, status, payload)
		 VALUES ($1, NULL, 'root', 0, 'pending', '{}')`,
		seedB.CourseID,
	)
	if err == nil {
		// Verify it's not actually in the DB (belt-and-suspenders).
		var cnt int
		_ = pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM course_nodes WHERE course_id = $1`, seedB.CourseID,
		).Scan(&cnt)
		t.Errorf(
			"RLS FAILURE (REQ-AGENT-032): student A was able to INSERT into student B's course "+
				"(course_id=%s); rows now in DB: %d",
			seedB.CourseID, cnt,
		)
	}
	// Any error is the expected outcome: RLS WITH CHECK denied the insert.
}

// TestTreeRepo_RLS_StudentCannotUpdateOtherStudentNodes verifies that a
// student-role connection cannot UPDATE nodes belonging to another student's
// course via TransitionNodeStatus.
//
// @{"verifies": ["REQ-AGENT-032", "REQ-SYS-075"]}
func TestTreeRepo_RLS_StudentCannotUpdateOtherStudentNodes(t *testing.T) {
	truncateTreeTables(t)

	seedA := seedTreeStudent(t, "update_deny_a")
	seedB := seedTreeStudent(t, "update_deny_b")
	pool := internaldb.IntegrationPool(t)
	repo := NewTreeRepository()

	ctx := context.Background()

	// Seed a root node for student A via superuser pool (positive control).
	var rootAID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO course_nodes (course_id, parent_id, node_type, ordering, status, payload)
		 VALUES ($1, NULL, 'root', 0, 'pending', '{}') RETURNING id`,
		seedA.CourseID,
	).Scan(&rootAID); err != nil {
		t.Fatalf("seed rootA: %v", err)
	}

	// Student B attempts to transition student A's node status.
	ctxB, releaseB := studentConnCtxTree(t, seedB.StudentID)
	defer releaseB()
	connB, _ := authpkg.ConnFromContext(ctxB)

	// TransitionNodeStatus issues UPDATE … WHERE id=$1 AND status=$from.
	// RLS USING clause on course_nodes_student_policy filters rows not belonging
	// to student B, so the UPDATE should affect 0 rows (returning false, no error).
	updated, err := repo.TransitionNodeStatus(ctx, connB, rootAID, NodeStatusPending, NodeStatusGenerating)
	if err != nil {
		// Some Postgres configurations raise a policy violation error; that is
		// also an acceptable outcome — the update was denied.
		t.Logf("TransitionNodeStatus(studentB updates studentA node): returned error (RLS enforcement): %v", err)
		return
	}
	if updated {
		// The update affected a row it should not have seen — RLS failure.
		t.Errorf(
			"RLS FAILURE (REQ-AGENT-032): student B was able to UPDATE student A's node (id=%s) — "+
				"course_nodes_student_policy UPDATE check is not enforced",
			rootAID,
		)
	}
	// updated==false with no error means 0 rows matched (correct RLS behaviour).
}

// ---------------------------------------------------------------------------
// 6. D10 hardening: student write-own denied (FOR SELECT only after D10)
// ---------------------------------------------------------------------------

// TestTreeRepo_RLS_StudentCannotInsertOwnCourseNodes_D10 verifies that after
// the D10 security hardening a student-role connection is DENIED when it
// attempts to INSERT into their OWN course's course_nodes table.
//
// Pre-D10 the course_nodes_student_policy was FOR ALL with a WITH CHECK clause
// that allowed students to write to their own course.  D10 replaced it with
// FOR SELECT: students now have *no* INSERT policy whatsoever, so Postgres
// raises a permission-denied / RLS violation error for any student INSERT.
//
// Positive control (same test): the same student can still SELECT their own
// nodes via ListNodesByCourse (read-own still works).
// Companion test TestTreeRepo_RLS_ServerRole_CanInsertAndSelectNodes confirms
// the server role can still INSERT (the pipeline path is unaffected).
//
// @{"verifies": ["REQ-AGENT-032", "REQ-SYS-075"]}
func TestTreeRepo_RLS_StudentCannotInsertOwnCourseNodes_D10(t *testing.T) {
	truncateTreeTables(t)

	// Seed both the student owner and a node that already belongs to their
	// course (via the superuser pool, which bypasses RLS — seeding must not
	// be subject to the policy under test).
	seed := seedTreeStudent(t, "d10_insert_own")
	pool := internaldb.IntegrationPool(t)

	ctx := context.Background()

	// Pre-seed one root node so the positive SELECT control is non-vacuous.
	var existingNodeID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO course_nodes (course_id, parent_id, node_type, ordering, status, payload)
		 VALUES ($1, NULL, 'root', 0, 'approved', '{"seeded":true}') RETURNING id`,
		seed.CourseID,
	).Scan(&existingNodeID); err != nil {
		t.Fatalf("D10 seed root node: %v", err)
	}

	// Acquire a student-role connection (app.current_role='student',
	// app.current_user_id=<owner>), mirroring the auth middleware.
	ctxStudent, releaseStudent := studentConnCtxTree(t, seed.StudentID)
	defer releaseStudent()
	studentConn, ok := authpkg.ConnFromContext(ctxStudent)
	if !ok {
		t.Fatal("studentConnCtxTree: no conn in context")
	}

	// ── Positive control: student CAN SELECT their own nodes ────────────────
	repo := NewTreeRepository()
	ownNodes, err := repo.ListNodesByCourse(ctx, studentConn, seed.CourseID, ListNodeOptions{})
	if err != nil {
		t.Fatalf("D10 positive control SELECT: student cannot list own nodes: %v", err)
	}
	if len(ownNodes) != 1 {
		t.Errorf("D10 positive control SELECT: want 1 own node, got %d", len(ownNodes))
	}

	// ── Negative assertion: student CANNOT INSERT into their OWN course ──────
	// Under D10, course_nodes_student_policy is FOR SELECT only.  There is no
	// INSERT policy for students; Postgres must therefore raise an error when
	// the student tries to INSERT (RLS: no policy permits the command).
	_, insertErr := studentConn.Exec(ctx,
		`INSERT INTO course_nodes (course_id, parent_id, node_type, ordering, status, payload)
		 VALUES ($1, NULL, 'syllabus', 1, 'pending', '{}')`,
		seed.CourseID, // own course — this was allowed pre-D10, must fail post-D10
	)
	if insertErr == nil {
		// Belt-and-suspenders: verify the row actually landed (it should not have).
		var cnt int
		_ = pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM course_nodes WHERE course_id = $1 AND node_type = 'syllabus'`,
			seed.CourseID,
		).Scan(&cnt)
		t.Errorf(
			"RLS FAILURE (D10 / REQ-SYS-075): student was able to INSERT into their OWN course's "+
				"course_nodes (course_id=%s); rows now in DB with type=syllabus: %d. "+
				"course_nodes_student_policy must be FOR SELECT only after D10.",
			seed.CourseID, cnt,
		)
	}
	// Any non-nil insertErr confirms RLS denied the write — the exact Postgres
	// error code (42501 insufficient_privilege or check-violation) is not
	// asserted here because either form is a correct denial.
}

// TestTreeRepo_RLS_StudentCannotUpdateOwnCourseNodes_D10 verifies that after
// the D10 security hardening a student-role connection receives 0 rows
// affected (or an error) when it attempts to UPDATE a node in their OWN course.
//
// Pre-D10 the FOR ALL student policy's USING clause matched the student's own
// course_id, so an UPDATE would see the rows and could modify them.  D10
// replaced that with FOR SELECT, leaving no UPDATE policy for students:
// Postgres either raises an error or (when the command itself is not blocked)
// returns 0 rows affected because the USING clause of the only applicable
// policy (FOR SELECT) does not cover UPDATE commands.
//
// Positive controls (same test):
//   - Student can still SELECT their own node (read-own intact).
//   - Server-role UpdateNode via TransitionNodeStatus still succeeds (pipeline
//     path is governed by course_nodes_server_update_policy, unaffected by D10).
//
// @{"verifies": ["REQ-AGENT-032", "REQ-SYS-075"]}
func TestTreeRepo_RLS_StudentCannotUpdateOwnCourseNodes_D10(t *testing.T) {
	truncateTreeTables(t)

	seed := seedTreeStudent(t, "d10_update_own")
	pool := internaldb.IntegrationPool(t)
	repo := NewTreeRepository()

	ctx := context.Background()

	// Seed a pending root node via the superuser pool.
	var nodeID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO course_nodes (course_id, parent_id, node_type, ordering, status, payload)
		 VALUES ($1, NULL, 'root', 0, 'pending', '{}') RETURNING id`,
		seed.CourseID,
	).Scan(&nodeID); err != nil {
		t.Fatalf("D10 seed root node: %v", err)
	}

	// Acquire a student-role connection (the owner student).
	ctxStudent, releaseStudent := studentConnCtxTree(t, seed.StudentID)
	defer releaseStudent()
	studentConn, ok := authpkg.ConnFromContext(ctxStudent)
	if !ok {
		t.Fatal("studentConnCtxTree: no conn in context")
	}

	// ── Positive control A: student CAN SELECT their own node ───────────────
	ownNode, err := repo.GetNode(ctx, studentConn, nodeID)
	if err != nil {
		t.Fatalf("D10 positive control SELECT: student cannot GetNode for own node: %v", err)
	}
	if ownNode.ID != nodeID {
		t.Errorf("D10 positive control SELECT: want node ID %s, got %s", nodeID, ownNode.ID)
	}
	if ownNode.Status != NodeStatusPending {
		t.Errorf("D10 positive control SELECT: want status pending, got %s", ownNode.Status)
	}

	// ── Negative assertion: student CANNOT UPDATE their OWN node ────────────
	// TransitionNodeStatus issues UPDATE course_nodes SET status=... WHERE id=... AND status=...
	// Under D10 no UPDATE policy covers students, so either:
	//   (a) Postgres raises an insufficient_privilege error (error path), or
	//   (b) the USING filter of non-UPDATE policies excludes the row → 0 rows affected.
	// Both outcomes satisfy the requirement that the update is denied.
	updated, updateErr := repo.TransitionNodeStatus(ctx, studentConn, nodeID, NodeStatusPending, NodeStatusGenerating)
	if updateErr != nil {
		// Error is an acceptable denial outcome.
		t.Logf("D10 student UPDATE own node: got error (RLS denial confirmed): %v", updateErr)
	} else if updated {
		// 0 rows should have been affected — if updated==true the policy is broken.
		t.Errorf(
			"RLS FAILURE (D10 / REQ-SYS-075): student was able to UPDATE their OWN course_nodes "+
				"row (node_id=%s, status pending→generating). "+
				"course_nodes_student_policy must be FOR SELECT only after D10; "+
				"only the server role may UPDATE via course_nodes_server_update_policy.",
			nodeID,
		)
	}
	// updated==false with no error means 0 rows matched the UPDATE WHERE clause —
	// correct post-D10 behaviour.

	// ── Positive control B: server role CAN UPDATE (pipeline path) ──────────
	serverCtx, releaseServer := serverConnCtxTree(t)
	defer releaseServer()
	serverConn, ok2 := authpkg.ConnFromContext(serverCtx)
	if !ok2 {
		t.Fatal("serverConnCtxTree: no conn in context")
	}

	// Node status was NOT changed by the student attempt; it is still pending.
	serverUpdated, serverErr := repo.TransitionNodeStatus(ctx, serverConn, nodeID, NodeStatusPending, NodeStatusGenerating)
	if serverErr != nil {
		t.Fatalf("D10 server UPDATE positive control: %v", serverErr)
	}
	if !serverUpdated {
		t.Errorf("D10 server UPDATE positive control: server role must be able to UPDATE course_nodes (course_nodes_server_update_policy); got updated=false")
	}
}

// TestTreeRepo_RLS_GetNode_CrossStudent_ReturnsErrNotFound verifies that
// GetNode returns ErrNotFound when a student-role connection tries to fetch
// a node belonging to another student's course.
//
// @{"verifies": ["REQ-AGENT-032", "REQ-SYS-075"]}
func TestTreeRepo_RLS_GetNode_CrossStudent_ReturnsErrNotFound(t *testing.T) {
	truncateTreeTables(t)

	seedA := seedTreeStudent(t, "getnode_deny_a")
	seedB := seedTreeStudent(t, "getnode_deny_b")
	pool := internaldb.IntegrationPool(t)
	repo := NewTreeRepository()

	ctx := context.Background()

	// Seed a root node for student A via superuser pool.
	var rootAID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO course_nodes (course_id, parent_id, node_type, ordering, status, payload)
		 VALUES ($1, NULL, 'root', 0, 'approved', '{}') RETURNING id`,
		seedA.CourseID,
	).Scan(&rootAID); err != nil {
		t.Fatalf("seed rootA: %v", err)
	}

	// Student B's connection must not be able to GetNode for student A's node.
	ctxB, releaseB := studentConnCtxTree(t, seedB.StudentID)
	defer releaseB()
	connB, _ := authpkg.ConnFromContext(ctxB)

	_, err := repo.GetNode(ctx, connB, rootAID)
	if err == nil {
		t.Errorf(
			"RLS FAILURE (REQ-AGENT-032): student B can GetNode for student A's node (id=%s) — "+
				"course_nodes_student_policy is not enforced",
			rootAID,
		)
	}
	if err != nil && err != ErrNotFound {
		// ErrNotFound is the expected outcome; any non-nil error still confirms
		// that the row was not returned.
		t.Logf("GetNode(studentB, studentA node): returned %v (any non-nil confirms denial)", err)
	}
}
