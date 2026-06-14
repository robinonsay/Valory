//go:build integration

// generation_lifecycle_integration_test.go — level-4 acceptance tests for
// G3-S1-T5: the generation-run lifecycle (built in T3+T4) bounds the "generation
// storm" under a real PostgreSQL instance via SET ROLE valory_app (no superuser
// masking).
//
// All 8 scenarios required by the task contract are implemented. Each test is
// non-vacuous and carries a counterfactual comment naming the guard it would
// catch if removed.
//
// Run:
//
//	export PATH=/usr/local/go/bin:$PATH
//	VALORY_TEST_DATABASE_URL=postgres://valory_test:valory_test@localhost:55432/valory_test?sslmode=disable \
//	  go test -tags integration -count=1 -p 1 -run 'TestGenLifecycle' ./internal/agent/...
//
// @{"req": ["REQ-AGENT-062", "REQ-AGENT-064", "REQ-AGENT-065", "REQ-AGENT-066",
//           "REQ-AGENT-067", "REQ-AGENT-068", "REQ-AGENT-069"]}
package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/google/uuid"
	internaldb "github.com/valory/valory/internal/db"
)

// ---------------------------------------------------------------------------
// Transports for lifecycle tests.
// ---------------------------------------------------------------------------

// errorTransport always returns a network error so every Anthropic call fails.
// Used to force generation failures without any real API round-trips.
type errorTransport struct {
	Calls int
}

func (e *errorTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	e.Calls++
	return nil, fmt.Errorf("errorTransport: simulated network error (call %d)", e.Calls)
}

// zeroTransport panics if called — used to assert zero paid calls.
// If the production code respects the token-cap pre-flight guard it will never
// call the transport at all; any call means the guard was bypassed.
type zeroTransport struct {
	Calls int
}

func (z *zeroTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	z.Calls++
	// Return a 500 to not panic the test process; the assertion on Calls==0 will
	// catch it after the function under test returns.
	body := `{"type":"error","error":{"type":"api_error","message":"zeroTransport: unexpected call"}}`
	return &http.Response{
		StatusCode: 500,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

// ---------------------------------------------------------------------------
// Stubs used by lifecycle tests.
// ---------------------------------------------------------------------------

// noopSecretResolver satisfies SecretResolver for test runners where Brave/
// Anthropic keys are never needed (all API calls go through fake transports).
type noopSecretResolver struct{}

func (n *noopSecretResolver) Get(_ context.Context, _ string) string { return "" }

// ---------------------------------------------------------------------------
// Lifecycle harness builders.
// ---------------------------------------------------------------------------

// buildFailingAgentRunner constructs a full AgentRunner whose Chair/Reviewer
// HTTP transports are wired to errorTransport — every API call fails. The
// runner uses the integration pool so all DB writes land in real Postgres under
// migration 024 schema.
func buildFailingAgentRunner(t *testing.T) (*AgentRunner, *errorTransport) {
	t.Helper()
	integPool := internaldb.IntegrationPool(t)
	transport := &errorTransport{}
	cfg := &mockConfigSvc{
		int64Values: map[string]int64{
			"agent_retry_limit":                    0, // no retries on the SDK level
			"per_student_token_limit":              0, // cap disabled (tested separately)
			"content_generation_timeout_seconds":   3600,
			"correction_loop_max_iterations":       1,
			"per_layer_token_budget":               0,
			"tree_concepts_per_section":            1,
		},
	}

	tc := &ThrottledClient{
		client: anthropic.NewClient(
			option.WithAPIKey("test-lifecycle-key"),
			option.WithHTTPClient(&http.Client{Transport: transport}),
			option.WithMaxRetries(0),
		),
		pool:      integPool,
		configSvc: cfg,
	}

	agentRepo := NewAgentRepository(integPool)
	chatRepo := NewChatRepository(integPool)
	chair := NewChair(tc, integPool, agentRepo, chatRepo)
	prof := NewProfessor(tc, integPool, agentRepo, &noopSecretResolver{}, cfg)
	reviewer := NewReviewer(tc, integPool, agentRepo)

	lrCfg := &lrOnlyConfig{vals: map[string]int64{
		"per_layer_token_budget":         0,
		"tree_concepts_per_section":      1,
		"correction_loop_max_iterations": 1,
	}}
	lr := NewLayeredRunner(integPool, NewTreeRepository(), agentRepo, chair, reviewer, lrCfg)

	runner := NewAgentRunner(integPool, agentRepo, chair, prof, reviewer, nil, nil, tc, cfg)
	runner.SetLayeredRunner(lr)
	return runner, transport
}

// buildSucceedingAgentRunner constructs a full AgentRunner whose Chair/Reviewer
// HTTP transports return infinite canned 200 OK responses. Used for the
// happy-path smoke test.
func buildSucceedingAgentRunner(t *testing.T) (*AgentRunner, *infiniteTransport) {
	t.Helper()
	integPool := internaldb.IntegrationPool(t)

	// The reviewer parses the response as {"approved":bool,"feedback":string}.
	// Using this body means ALL Anthropic calls (GenerateSection + ReviewSection)
	// return the same JSON. GenerateSection will store the JSON string as the
	// content_adoc — technically invalid AsciiDoc but sufficient for a smoke test
	// (we only assert on course status and run count, not content quality).
	// With Approved=true, the reviewer short-circuits on the first review pass
	// and no correction loop is needed.
	transport := &infiniteTransport{responseBody: `{"approved":true,"feedback":""}`}
	cfg := &mockConfigSvc{
		int64Values: map[string]int64{
			// agent_retry_limit must be >= 1: the loop `for attempt < retryLimit`
			// never executes when retryLimit==0, returning ErrRateLimitExhausted.
			"agent_retry_limit":                  1,
			"per_student_token_limit":            0,
			"content_generation_timeout_seconds": 3600,
			"correction_loop_max_iterations":     2,
			"per_layer_token_budget":             0,
			"tree_concepts_per_section":          1,
		},
	}

	tc := &ThrottledClient{
		client: anthropic.NewClient(
			option.WithAPIKey("test-lifecycle-ok-key"),
			option.WithHTTPClient(&http.Client{Transport: transport}),
			option.WithMaxRetries(0),
		),
		pool:      integPool,
		configSvc: cfg,
	}

	agentRepo := NewAgentRepository(integPool)
	chatRepo := NewChatRepository(integPool)
	chair := NewChair(tc, integPool, agentRepo, chatRepo)
	prof := NewProfessor(tc, integPool, agentRepo, &noopSecretResolver{}, cfg)
	reviewer := NewReviewer(tc, integPool, agentRepo)

	lrCfg := &lrOnlyConfig{vals: map[string]int64{
		"per_layer_token_budget":         0,
		"tree_concepts_per_section":      1,
		"correction_loop_max_iterations": 2,
	}}
	lr := NewLayeredRunner(integPool, NewTreeRepository(), agentRepo, chair, reviewer, lrCfg)

	runner := NewAgentRunner(integPool, agentRepo, chair, prof, reviewer, nil, nil, tc, cfg)
	runner.SetLayeredRunner(lr)
	return runner, transport
}

// ---------------------------------------------------------------------------
// Shared fixture helpers for lifecycle tests.
// ---------------------------------------------------------------------------

// glSeed holds IDs created for one lifecycle test fixture.
type glSeed struct {
	StudentID uuid.UUID
	CourseID  uuid.UUID
}

// truncateLifecycleTables wipes all relevant tables before each lifecycle test.
func truncateLifecycleTables(t *testing.T) {
	t.Helper()
	internaldb.TruncateTables(t, internaldb.IntegrationPool(t),
		"pipeline_events",
		"agent_token_usage",
		"agent_runs",
		"node_chats",
		"course_nodes",
		"lesson_content",
		"due_date_schedules",
		"homework",
		"syllabi",
		"section_feedback",
		"notifications",
		"sessions",
		"courses",
		"users",
	)
}

// seedFlatCourse inserts a student + flat syllabus_approved course with one
// approved syllabus and one homework section. Used for flat-path lifecycle tests.
func seedFlatCourse(t *testing.T, tag string) glSeed {
	t.Helper()
	integPool := internaldb.IntegrationPool(t)
	ctx := context.Background()

	var studentID uuid.UUID
	if err := integPool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role)
		 VALUES ($1, 'x', 'student') RETURNING id`,
		"gl_"+tag+"_"+uuid.New().String()[:8],
	).Scan(&studentID); err != nil {
		t.Fatalf("seedFlatCourse(%s): user: %v", tag, err)
	}

	var courseID uuid.UUID
	if err := integPool.QueryRow(ctx,
		`INSERT INTO courses (student_id, topic, status, tree_mode)
		 VALUES ($1, $2, 'syllabus_approved', false) RETURNING id`,
		studentID, "Flat Course "+tag,
	).Scan(&courseID); err != nil {
		t.Fatalf("seedFlatCourse(%s): course: %v", tag, err)
	}

	// Insert an approved syllabus so RunContentGeneration can find one.
	if _, err := integPool.Exec(ctx,
		`INSERT INTO syllabi (course_id, content_adoc, version, approved_at)
		 VALUES ($1, '= Test Syllabus\n\n== Section 1\n\nLearn basics.', 1, now())`,
		courseID,
	); err != nil {
		t.Fatalf("seedFlatCourse(%s): syllabus: %v", tag, err)
	}

	// Pre-seed one homework row so ensureDueDates sees count > 0 and skips
	// the AssignDueDates Anthropic call. The correct homework schema (from
	// migration 003) has no student_id column — course_id + section_index +
	// title + rubric + grade_weight. Without this, ensureDueDates calls
	// AssignDueDates which requires an Anthropic call that returns valid JSON
	// for section titles — incompatible with a single-body infiniteTransport.
	var hwID uuid.UUID
	if err := integPool.QueryRow(ctx,
		`INSERT INTO homework (course_id, section_index, title, rubric, grade_weight)
		 VALUES ($1, 0, 'Section 1', 'Test rubric.', 1.0) RETURNING id`,
		courseID,
	).Scan(&hwID); err != nil {
		t.Fatalf("seedFlatCourse(%s): homework: %v", tag, err)
	}

	// Pre-seed a due_date_schedules row so the homework FK constraint is satisfied.
	if _, err := integPool.Exec(ctx,
		`INSERT INTO due_date_schedules (course_id, homework_id, due_date)
		 VALUES ($1, $2, now() + interval '7 days')`,
		courseID, hwID,
	); err != nil {
		t.Fatalf("seedFlatCourse(%s): due_date_schedules: %v", tag, err)
	}

	return glSeed{StudentID: studentID, CourseID: courseID}
}

// seedTreeCourse inserts a student + tree-mode syllabus_approved course with an
// approved syllabus. Used for tree-path lifecycle tests.
func seedTreeCourse(t *testing.T, tag string) glSeed {
	t.Helper()
	integPool := internaldb.IntegrationPool(t)
	ctx := context.Background()

	var studentID uuid.UUID
	if err := integPool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role)
		 VALUES ($1, 'x', 'student') RETURNING id`,
		"glt_"+tag+"_"+uuid.New().String()[:8],
	).Scan(&studentID); err != nil {
		t.Fatalf("seedTreeCourse(%s): user: %v", tag, err)
	}

	var courseID uuid.UUID
	if err := integPool.QueryRow(ctx,
		`INSERT INTO courses (student_id, topic, status, tree_mode)
		 VALUES ($1, $2, 'syllabus_approved', true) RETURNING id`,
		studentID, "Tree Course "+tag,
	).Scan(&courseID); err != nil {
		t.Fatalf("seedTreeCourse(%s): course: %v", tag, err)
	}

	// Insert approved syllabus for seedTreeAndGenerateRoot.
	if _, err := integPool.Exec(ctx,
		`INSERT INTO syllabi (course_id, content_adoc, version, approved_at)
		 VALUES ($1, '= Test Syllabus\n\n== Section 1\n\nLearn the basics.', 1, now())`,
		courseID,
	); err != nil {
		t.Fatalf("seedTreeCourse(%s): syllabus: %v", tag, err)
	}

	return glSeed{StudentID: studentID, CourseID: courseID}
}

// glCountRuns returns the number of agent_runs rows for the given course.
func glCountRuns(t *testing.T, courseID uuid.UUID) int {
	t.Helper()
	var n int
	if err := internaldb.IntegrationPool(t).QueryRow(context.Background(),
		`SELECT COUNT(*) FROM agent_runs WHERE course_id = $1`, courseID,
	).Scan(&n); err != nil {
		t.Fatalf("glCountRuns: %v", err)
	}
	return n
}

// glCourseStatus returns courses.status for the given course.
func glCourseStatus(t *testing.T, courseID uuid.UUID) string {
	t.Helper()
	var s string
	if err := internaldb.IntegrationPool(t).QueryRow(context.Background(),
		`SELECT status::text FROM courses WHERE id = $1`, courseID,
	).Scan(&s); err != nil {
		t.Fatalf("glCourseStatus: %v", err)
	}
	return s
}

// glAttemptCount returns courses.generation_attempt_count.
func glAttemptCount(t *testing.T, courseID uuid.UUID) int {
	t.Helper()
	var n int
	if err := internaldb.IntegrationPool(t).QueryRow(context.Background(),
		`SELECT generation_attempt_count FROM courses WHERE id = $1`, courseID,
	).Scan(&n); err != nil {
		t.Fatalf("glAttemptCount: %v", err)
	}
	return n
}

// glNextEligibleAt returns courses.next_eligible_at (nil = NULL = immediately eligible).
func glNextEligibleAt(t *testing.T, courseID uuid.UUID) *time.Time {
	t.Helper()
	var ts *time.Time
	if err := internaldb.IntegrationPool(t).QueryRow(context.Background(),
		`SELECT next_eligible_at FROM courses WHERE id = $1`, courseID,
	).Scan(&ts); err != nil {
		t.Fatalf("glNextEligibleAt: %v", err)
	}
	return ts
}

// glRunCountByStatus counts agent_runs for a course by status.
func glRunCountByStatus(t *testing.T, courseID uuid.UUID, status string) int {
	t.Helper()
	var n int
	if err := internaldb.IntegrationPool(t).QueryRow(context.Background(),
		`SELECT COUNT(*) FROM agent_runs WHERE course_id = $1 AND status = $2`,
		courseID, status,
	).Scan(&n); err != nil {
		t.Fatalf("glRunCountByStatus(%s): %v", status, err)
	}
	return n
}

// runDispatchCycle calls dispatchFlatCourse directly (bypassing the poller ticker)
// so tests control dispatch timing and avoid race conditions with background goroutines.
func runFlatDispatch(t *testing.T, runner *AgentRunner, seed glSeed, maxAttempts, backoffSecs int64) {
	t.Helper()
	runner.dispatchFlatCourse(context.Background(), CourseStudentRow{
		CourseID:  seed.CourseID,
		StudentID: seed.StudentID,
		TreeMode:  false,
	}, maxAttempts, backoffSecs)
}

// runTreeDispatch calls dispatchTreeCourse directly.
func runTreeDispatch(t *testing.T, runner *AgentRunner, seed glSeed, maxAttempts, backoffSecs int64) {
	t.Helper()
	runner.dispatchTreeCourse(context.Background(), CourseStudentRow{
		CourseID:  seed.CourseID,
		StudentID: seed.StudentID,
		TreeMode:  true,
	}, maxAttempts, backoffSecs)
}

// ---------------------------------------------------------------------------
// Scenario 1: Storm cannot recur — bounded runs (flat AND tree).
//
// Counterfactual: if SetCourseTerminal / the maxAttempts guard in handleFailedRun
// were removed, the run count would be unbounded and the course would remain
// in 'syllabus_approved' forever, appearing in every ListUntriggeredApprovals
// call. The assertions on run count and course status would fail.
// ---------------------------------------------------------------------------

// @{"req": ["REQ-AGENT-064", "REQ-AGENT-067"]}
func TestGenLifecycle_StormBound_FlatCourse_RunsLimitedToMaxAttempts(t *testing.T) {
	truncateLifecycleTables(t)
	seed := seedFlatCourse(t, "storm_flat")
	runner, _ := buildFailingAgentRunner(t)

	const maxAttempts = int64(3)
	const backoffSecs = int64(1)

	// Simulate the poller's eligibility gate: check ListUntriggeredApprovals before
	// each dispatch, exactly as pollAndGenerate would. Clear next_eligible_at so the
	// backoff window is not the limiting factor — the terminal state guard is.
	//
	// Counterfactual: without handleFailedRun→SetCourseTerminal, ListUntriggeredApprovals
	// keeps returning the course (status stays 'syllabus_approved') and dispatches
	// happen for all maxAttempts+2 iterations → total run count = maxAttempts+2.
	repo := NewAgentRepository(internaldb.IntegrationPool(t))
	dispatchCount := 0

	for i := 0; i < int(maxAttempts)+2; i++ {
		// Clear backoff so only the terminal-state guard limits eligibility.
		if _, err := internaldb.IntegrationPool(t).Exec(context.Background(),
			`UPDATE courses SET next_eligible_at = NULL WHERE id = $1`, seed.CourseID,
		); err != nil {
			t.Fatalf("clear next_eligible_at: %v", err)
		}

		// Check eligibility (mirrors ListUntriggeredApprovals in pollAndGenerate).
		eligible, err := repo.ListUntriggeredApprovals(context.Background(), maxAttempts)
		if err != nil {
			t.Fatalf("ListUntriggeredApprovals iteration %d: %v", i, err)
		}
		shouldDispatch := false
		for _, c := range eligible {
			if c.CourseID == seed.CourseID {
				shouldDispatch = true
			}
		}
		if !shouldDispatch {
			break // poller would stop here; so do we
		}
		runFlatDispatch(t, runner, seed, maxAttempts, backoffSecs)
		dispatchCount++
	}

	// ASSERTION 1: total agent_runs <= maxAttempts.
	got := glCountRuns(t, seed.CourseID)
	if int64(got) > maxAttempts {
		t.Errorf("storm bound flat: agent_runs count = %d, want <= %d "+
			"(handleFailedRun must call SetCourseTerminal after maxAttempts failures; "+
			"without it the run count is unbounded — REQ-AGENT-064)",
			got, maxAttempts)
	}

	// ASSERTION 2: course ends in generation_failed.
	// Counterfactual: without SetCourseTerminal the status stays 'syllabus_approved'.
	status := glCourseStatus(t, seed.CourseID)
	if status != "generation_failed" {
		t.Errorf("storm bound flat: course status = %q, want 'generation_failed' "+
			"(SetCourseTerminal must transition course after exhausting maxAttempts — REQ-AGENT-062, dispatches=%d)",
			status, dispatchCount)
	}

	// ASSERTION 3: ListUntriggeredApprovals excludes the course.
	// Counterfactual: without the status='syllabus_approved' predicate in
	// ListUntriggeredApprovals, generation_failed courses would still be returned.
	eligible, err := repo.ListUntriggeredApprovals(context.Background(), maxAttempts)
	if err != nil {
		t.Fatalf("ListUntriggeredApprovals: %v", err)
	}
	for _, c := range eligible {
		if c.CourseID == seed.CourseID {
			t.Errorf("storm bound flat: course %s still eligible after terminal failure "+
				"(ListUntriggeredApprovals must exclude generation_failed courses — REQ-AGENT-064)",
				seed.CourseID)
		}
	}
}

// @{"req": ["REQ-AGENT-064", "REQ-AGENT-067"]}
func TestGenLifecycle_StormBound_TreeCourse_RunsLimitedToMaxAttempts(t *testing.T) {
	// This test exercises the tree-course storm bound at the dispatch level.
	//
	// The storm guard for tree courses works through handleFailedRun, which
	// increments courses.generation_attempt_count on each dispatchTreeCourse
	// failure. After maxAttempts increments, SetCourseTerminal transitions the
	// course to 'generation_failed', after which ListUntriggeredApprovals excludes
	// it — the real poller would stop dispatching it.
	//
	// The tricky part: after a successful seedTreeAndGenerateRoot, the course
	// transitions to 'generating'. If seedTreeAndGenerateRoot itself fails (e.g.,
	// because GenerateLayer returns an error), dispatchTreeCourse calls
	// handleFailedRun. However, the course is then in 'generating' (not
	// 'syllabus_approved'), so ListUntriggeredApprovals would not return it for a
	// second dispatchTreeCourse call from the poller.
	//
	// To test the FULL multi-dispatch storm bound, we simulate the scenario where
	// the course is reset to 'syllabus_approved' between dispatch attempts (e.g.,
	// by an operator after investigation). This is a valid real-world path:
	// an operator retries a failed tree course by resetting it, and the storm
	// guard must still limit total runs to maxAttempts.
	//
	// Alternatively, we verify the bound via direct handleFailedRun calls and
	// assert the terminal state + poller exclusion.
	truncateLifecycleTables(t)
	seed := seedTreeCourse(t, "storm_tree")
	runner, _ := buildFailingAgentRunner(t)
	integPool := internaldb.IntegrationPool(t)
	repo := NewAgentRepository(integPool)
	ctx := context.Background()

	const maxAttempts = int64(3)
	const backoffSecs = int64(1)

	// Simulate maxAttempts+2 dispatch attempts. Between each, reset the course to
	// 'syllabus_approved' so ListUntriggeredApprovals can return it again. The
	// terminal guard (handleFailedRun → SetCourseTerminal) should stop this after
	// maxAttempts attempts, and the final iterations should see the course as
	// ineligible.
	//
	// Counterfactual: without handleFailedRun→SetCourseTerminal, the course would
	// be reset to 'syllabus_approved' indefinitely and the loop would dispatch
	// maxAttempts+2 times, violating the run count bound.
	dispatchCount := 0

	for i := 0; i < int(maxAttempts)+2; i++ {
		// Clear backoff so only the terminal-state guard limits eligibility.
		if _, err := integPool.Exec(ctx,
			`UPDATE courses SET next_eligible_at = NULL WHERE id = $1`, seed.CourseID,
		); err != nil {
			t.Fatalf("clear next_eligible_at: %v", err)
		}

		// Check poller eligibility (mirrors ListUntriggeredApprovals in pollAndGenerate).
		eligible, err := repo.ListUntriggeredApprovals(ctx, maxAttempts)
		if err != nil {
			t.Fatalf("ListUntriggeredApprovals iteration %d: %v", i, err)
		}
		shouldDispatch := false
		for _, c := range eligible {
			if c.CourseID == seed.CourseID {
				shouldDispatch = true
			}
		}
		if !shouldDispatch {
			break // poller would stop; so do we
		}

		runTreeDispatch(t, runner, seed, maxAttempts, backoffSecs)
		dispatchCount++

		// After the dispatch, if the course is now 'generating' (tree seeded but
		// GenerateLayer failed), reset it to 'syllabus_approved' to simulate the
		// operator re-trigger scenario. This lets us test maxAttempts consecutive
		// dispatch-level failures.
		curStatus := glCourseStatus(t, seed.CourseID)
		if curStatus == "generating" {
			// Reset to syllabus_approved so the next poll tick can re-dispatch.
			// This simulates an operator manually resetting a stuck 'generating'
			// course for re-dispatch (the bound must hold across these resets).
			if _, err := integPool.Exec(ctx,
				`UPDATE courses SET status = 'syllabus_approved', updated_at = now() WHERE id = $1`,
				seed.CourseID,
			); err != nil {
				t.Fatalf("reset course to syllabus_approved for tree re-dispatch: %v", err)
			}
		}
	}

	// ASSERTION 1: generation_attempt_count >= maxAttempts (all failures counted).
	// Counterfactual: if IncrementAttemptCount were not called in handleFailedRun,
	// the counter would stay 0 and SetCourseTerminal would never be triggered.
	attemptCount := glAttemptCount(t, seed.CourseID)
	if int64(attemptCount) < maxAttempts {
		t.Errorf("storm bound tree: attempt_count = %d after %d dispatches, want >= %d "+
			"(handleFailedRun must increment attempt_count — REQ-AGENT-065)",
			attemptCount, dispatchCount, maxAttempts)
	}

	// ASSERTION 2: course is terminal ('generation_failed') after maxAttempts failures.
	// Counterfactual: without SetCourseTerminal, the course stays eligible indefinitely.
	status := glCourseStatus(t, seed.CourseID)
	if status != "generation_failed" {
		t.Errorf("storm bound tree: course status = %q, want 'generation_failed' "+
			"(SetCourseTerminal must fire after maxAttempts — REQ-AGENT-062, dispatches=%d)",
			status, dispatchCount)
	}

	// ASSERTION 3: total runs <= maxAttempts (bounded by handleFailedRun terminal logic).
	got := glCountRuns(t, seed.CourseID)
	if int64(got) > maxAttempts {
		t.Errorf("storm bound tree: agent_runs count = %d, want <= %d "+
			"(handleFailedRun must stop new runs via SetCourseTerminal — REQ-AGENT-064)",
			got, maxAttempts)
	}

	// ASSERTION 4: ListUntriggeredApprovals excludes the terminal course.
	// Counterfactual: without the status='syllabus_approved' predicate in
	// ListUntriggeredApprovals, 'generation_failed' courses would be returned.
	if _, err := integPool.Exec(ctx,
		`UPDATE courses SET next_eligible_at = NULL WHERE id = $1`, seed.CourseID,
	); err != nil {
		t.Fatalf("clear next_eligible_at for final check: %v", err)
	}
	finalEligible, err := repo.ListUntriggeredApprovals(ctx, maxAttempts)
	if err != nil {
		t.Fatalf("ListUntriggeredApprovals final: %v", err)
	}
	for _, c := range finalEligible {
		if c.CourseID == seed.CourseID {
			t.Errorf("storm bound tree: course %s still eligible after terminal failure "+
				"(ListUntriggeredApprovals must exclude generation_failed — REQ-AGENT-064)",
				seed.CourseID)
		}
	}
}

// ---------------------------------------------------------------------------
// Scenario 2: Idempotent dispatch — forced race yields exactly ONE run.
//
// Counterfactual: if the unique partial index
// agent_runs_one_running_per_course_type_idx or the ErrRunAlreadyClaimed branch
// in CreateRun were removed, both goroutines would successfully INSERT and the
// assertion `runCount == 1` would fail with count == 2.
// ---------------------------------------------------------------------------

// @{"req": ["REQ-AGENT-066"]}
func TestGenLifecycle_IdempotentDispatch_ForcedRace_ExactlyOneRun(t *testing.T) {
	truncateLifecycleTables(t)
	seed := seedFlatCourse(t, "idempotent")

	integPool := internaldb.IntegrationPool(t)
	repo := NewAgentRepository(integPool)
	ctx := context.Background()

	const maxAttempts = int64(5)
	const backoffSecs = int64(600)

	// Fire two concurrent CreateRun calls for the same (course, run_type).
	// One must succeed; the other must return ErrRunAlreadyClaimed.
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		errs  []error
		runIDs []uuid.UUID
	)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run, err := repo.CreateRun(ctx, seed.CourseID, "content_generation")
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
			} else {
				runIDs = append(runIDs, run.ID)
			}
		}()
	}
	wg.Wait()

	// ASSERTION 1: exactly one run was created.
	// Counterfactual: without the partial unique index, both INSERTs succeed → 2 runs.
	if len(runIDs) != 1 {
		t.Errorf("idempotent dispatch: %d runs created, want 1 "+
			"(agent_runs_one_running_per_course_type_idx must prevent duplicate running rows — REQ-AGENT-066)",
			len(runIDs))
	}

	// ASSERTION 2: the loser returned ErrRunAlreadyClaimed (benign skip).
	// Counterfactual: without the 23505-to-ErrRunAlreadyClaimed mapping in CreateRun,
	// the caller gets a raw pgconn error and may log it as an unexpected error.
	claimErrors := 0
	for _, err := range errs {
		if err == ErrRunAlreadyClaimed {
			claimErrors++
		} else if err != nil {
			t.Errorf("idempotent dispatch: unexpected error from CreateRun: %v", err)
		}
	}
	// With 2 goroutines and 1 success, we expect exactly 1 ErrRunAlreadyClaimed.
	if len(runIDs)+len(errs) == 2 && claimErrors == 0 && len(errs) > 0 {
		t.Errorf("idempotent dispatch: losing goroutine returned %v, want ErrRunAlreadyClaimed "+
			"(CreateRun must map SQLSTATE 23505 to ErrRunAlreadyClaimed — REQ-AGENT-066)",
			errs[0])
	}

	_ = maxAttempts
	_ = backoffSecs
}

// ---------------------------------------------------------------------------
// Scenario 3: Backoff spacing.
//
// Counterfactual: if IncrementAttemptCount did not set next_eligible_at, or if
// ListUntriggeredApprovals did not filter on (next_eligible_at IS NULL OR <= NOW()),
// the course would remain eligible immediately after a failure. The assertion
// `len(eligible) == 0` would fail with len == 1.
// ---------------------------------------------------------------------------

// @{"req": ["REQ-AGENT-064", "REQ-AGENT-067"]}
func TestGenLifecycle_BackoffSpacing_CourseExcludedDuringWindow_ThenReeligible(t *testing.T) {
	truncateLifecycleTables(t)
	seed := seedFlatCourse(t, "backoff")

	integPool := internaldb.IntegrationPool(t)
	repo := NewAgentRepository(integPool)
	ctx := context.Background()

	// Use a large backoff so the course stays ineligible without time-warping.
	const maxAttempts = int64(5)
	const backoffSecs = int64(3600) // 1 hour

	// Manually increment the attempt count (as handleFailedRun does) which also
	// sets next_eligible_at = NOW() + 3600s.
	_, err := repo.IncrementAttemptCount(ctx, seed.CourseID, backoffSecs)
	if err != nil {
		t.Fatalf("IncrementAttemptCount: %v", err)
	}

	// ASSERTION 1: next_eligible_at is set to a future timestamp.
	// Counterfactual: if IncrementAttemptCount did not SET next_eligible_at, it would
	// be NULL (immediately eligible) and the test would fail at assertion 2.
	nextEligible := glNextEligibleAt(t, seed.CourseID)
	if nextEligible == nil {
		t.Fatalf("backoff: next_eligible_at is NULL after IncrementAttemptCount "+
			"(IncrementAttemptCount must set next_eligible_at = NOW() + backoffSecs — REQ-AGENT-067)")
	}
	if !nextEligible.After(time.Now()) {
		t.Errorf("backoff: next_eligible_at=%v is not in the future (want NOW() + %ds — REQ-AGENT-067)",
			*nextEligible, backoffSecs)
	}

	// ASSERTION 2: course excluded from eligibility during backoff window.
	// Counterfactual: if ListUntriggeredApprovals dropped the next_eligible_at
	// predicate, the course would appear here even with a future next_eligible_at.
	eligible, err := repo.ListUntriggeredApprovals(ctx, maxAttempts)
	if err != nil {
		t.Fatalf("ListUntriggeredApprovals (during backoff): %v", err)
	}
	for _, c := range eligible {
		if c.CourseID == seed.CourseID {
			t.Errorf("backoff: course %s returned while in backoff window "+
				"(next_eligible_at predicate in ListUntriggeredApprovals must exclude it — REQ-AGENT-064)",
				seed.CourseID)
		}
	}

	// ASSERTION 3: course becomes eligible again after clearing the backoff.
	// We simulate elapsed time by manually resetting next_eligible_at to the past.
	if _, err := integPool.Exec(ctx,
		`UPDATE courses SET next_eligible_at = now() - interval '1 second' WHERE id = $1`,
		seed.CourseID,
	); err != nil {
		t.Fatalf("reset next_eligible_at to past: %v", err)
	}

	eligibleAfter, err := repo.ListUntriggeredApprovals(ctx, maxAttempts)
	if err != nil {
		t.Fatalf("ListUntriggeredApprovals (after backoff expired): %v", err)
	}
	found := false
	for _, c := range eligibleAfter {
		if c.CourseID == seed.CourseID {
			found = true
		}
	}
	if !found {
		t.Errorf("backoff: course %s not returned after backoff window expired "+
			"(course must become eligible again when next_eligible_at <= NOW() — REQ-AGENT-067)",
			seed.CourseID)
	}
}

// ---------------------------------------------------------------------------
// Scenario 4: Max-attempts → terminal generation_failed (+D22: does not block
// new course creation).
//
// Counterfactual (terminal): if handleFailedRun never called SetCourseTerminal,
// the course stays in 'syllabus_approved' and the run count is unbounded.
// Counterfactual (D22): if courses_single_active_idx still excluded
// generation_failed from its WHERE NOT IN predicate, inserting a new course
// for the same student while an old course is in generation_failed would violate
// the index and return a unique_violation error.
// ---------------------------------------------------------------------------

// @{"req": ["REQ-AGENT-062", "REQ-AGENT-064"]}
func TestGenLifecycle_MaxAttempts_Terminal_D22_DoesNotBlockNewCourse(t *testing.T) {
	truncateLifecycleTables(t)
	seed := seedFlatCourse(t, "terminal_d22")
	runner, _ := buildFailingAgentRunner(t)

	const maxAttempts = int64(2)
	const backoffSecs = int64(1)

	// Exhaust attempts.
	for i := 0; i < int(maxAttempts)+1; i++ {
		if _, err := internaldb.IntegrationPool(t).Exec(context.Background(),
			`UPDATE courses SET next_eligible_at = NULL WHERE id = $1`, seed.CourseID,
		); err != nil {
			t.Fatalf("clear next_eligible_at: %v", err)
		}
		runFlatDispatch(t, runner, seed, maxAttempts, backoffSecs)
	}

	// ASSERTION 1: course is terminal.
	status := glCourseStatus(t, seed.CourseID)
	if status != "generation_failed" {
		t.Errorf("terminal D22: course status = %q, want 'generation_failed' (REQ-AGENT-062)", status)
	}

	// ASSERTION 2 (D22): student can create a new course while old is generation_failed.
	// Counterfactual: without the widened courses_single_active_idx (which now
	// excludes 'generation_failed'), this INSERT would violate the unique index.
	var newCourseID uuid.UUID
	err := internaldb.IntegrationPool(t).QueryRow(context.Background(),
		`INSERT INTO courses (student_id, topic, status, tree_mode)
		 VALUES ($1, $2, 'intake', false) RETURNING id`,
		seed.StudentID, "New Course after failure",
	).Scan(&newCourseID)
	if err != nil {
		t.Errorf("terminal D22: student cannot create new course while old is generation_failed: %v "+
			"(courses_single_active_idx must exclude 'generation_failed' — D22)", err)
	}
}

// ---------------------------------------------------------------------------
// Scenario 5: Recovery — RecoverGenerationFailed returns course to eligibility.
//
// Counterfactual: if RecoverGenerationFailed did not reset generation_attempt_count
// and next_eligible_at (only doing a bare status flip), the course would
// immediately re-exhaust its budget on the next run and never re-enter the
// success path. The assertions on attempt count and next_eligible_at == nil
// would fail if those fields were not reset.
// ---------------------------------------------------------------------------

// @{"req": ["REQ-AGENT-069"]}
func TestGenLifecycle_Recovery_RecoverGenerationFailed_ReturnsToEligibility(t *testing.T) {
	truncateLifecycleTables(t)
	seed := seedFlatCourse(t, "recovery")
	integPool := internaldb.IntegrationPool(t)
	repo := NewAgentRepository(integPool)
	ctx := context.Background()

	// Manually put the course into the terminal state with stale lifecycle fields.
	const maxAttempts = int64(3)
	if _, err := integPool.Exec(ctx,
		`UPDATE courses
		 SET status = 'generation_failed',
		     generation_attempt_count = 3,
		     next_eligible_at = now() + interval '1 hour'
		 WHERE id = $1`, seed.CourseID,
	); err != nil {
		t.Fatalf("set terminal: %v", err)
	}

	// PRECONDITION: course is terminal, excluded from eligibility.
	priorEligible, err := repo.ListUntriggeredApprovals(ctx, maxAttempts)
	if err != nil {
		t.Fatalf("ListUntriggeredApprovals pre-recovery: %v", err)
	}
	for _, c := range priorEligible {
		if c.CourseID == seed.CourseID {
			t.Fatalf("recovery precondition: generation_failed course should not be eligible pre-recovery")
		}
	}

	// Execute recovery.
	if err := repo.RecoverGenerationFailed(ctx, seed.CourseID); err != nil {
		t.Fatalf("RecoverGenerationFailed: %v", err)
	}

	// ASSERTION 1: course status is back to syllabus_approved.
	// Counterfactual: without the SET status='syllabus_approved' in RecoverGenerationFailed,
	// course would stay 'generation_failed' and never appear in ListUntriggeredApprovals.
	status := glCourseStatus(t, seed.CourseID)
	if status != "syllabus_approved" {
		t.Errorf("recovery: status after recovery = %q, want 'syllabus_approved' (REQ-AGENT-069)", status)
	}

	// ASSERTION 2: attempt count reset to 0.
	// Counterfactual: if RecoverGenerationFailed only flipped the status and left
	// attempt_count=3 (== maxAttempts), the very next dispatch would immediately
	// call SetCourseTerminal again — re-exhaustion on the first run.
	attemptCount := glAttemptCount(t, seed.CourseID)
	if attemptCount != 0 {
		t.Errorf("recovery: generation_attempt_count after recovery = %d, want 0 "+
			"(RecoverGenerationFailed must reset counter — REQ-AGENT-069)", attemptCount)
	}

	// ASSERTION 3: next_eligible_at is NULL (immediately eligible).
	// Counterfactual: without the SET next_eligible_at=NULL, the old backoff window
	// (1 hour in the future) would still block the course from being dispatched.
	nextEligible := glNextEligibleAt(t, seed.CourseID)
	if nextEligible != nil {
		t.Errorf("recovery: next_eligible_at after recovery = %v, want nil "+
			"(RecoverGenerationFailed must clear backoff timestamp — REQ-AGENT-069)", *nextEligible)
	}

	// ASSERTION 4: course is dispatchable again after recovery.
	postEligible, err := repo.ListUntriggeredApprovals(ctx, maxAttempts)
	if err != nil {
		t.Fatalf("ListUntriggeredApprovals post-recovery: %v", err)
	}
	found := false
	for _, c := range postEligible {
		if c.CourseID == seed.CourseID {
			found = true
		}
	}
	if !found {
		t.Errorf("recovery: course %s not eligible after RecoverGenerationFailed "+
			"(REQ-AGENT-069)", seed.CourseID)
	}
}

// ---------------------------------------------------------------------------
// Scenario 6: tokenCapPreFlight fail-fast — zero paid calls (flat AND tree).
//
// Counterfactual: if tokenCapPreFlight were removed from dispatchFlatCourse and
// dispatchTreeCourse, the transport.Calls assertion `== 0` would fail because
// the runner would proceed to call generateAllSections / seedTreeAndGenerateRoot,
// each of which makes Anthropic API calls via the fake transport.
// ---------------------------------------------------------------------------

// buildZeroCallAgentRunner builds a runner whose Chair/Reviewer transport is a
// zeroTransport (panics on first call) and the token cap is set over the used
// amount, forcing tokenCapPreFlight to return ErrTokenCapExceeded immediately.
func buildCapExceededRunner(t *testing.T, zeroTrans *zeroTransport) *AgentRunner {
	t.Helper()
	integPool := internaldb.IntegrationPool(t)

	cfg := &mockConfigSvc{
		int64Values: map[string]int64{
			"agent_retry_limit":                  0,
			"per_student_token_limit":            1, // cap = 1 token — will be exceeded
			"content_generation_timeout_seconds": 3600,
			"correction_loop_max_iterations":     1,
			"per_layer_token_budget":             0,
			"tree_concepts_per_section":          1,
		},
	}

	tc := &ThrottledClient{
		client: anthropic.NewClient(
			option.WithAPIKey("test-cap-key"),
			option.WithHTTPClient(&http.Client{Transport: zeroTrans}),
			option.WithMaxRetries(0),
		),
		pool:      integPool,
		configSvc: cfg,
	}

	agentRepo := NewAgentRepository(integPool)
	chatRepo := NewChatRepository(integPool)
	chair := NewChair(tc, integPool, agentRepo, chatRepo)
	prof := NewProfessor(tc, integPool, agentRepo, &noopSecretResolver{}, cfg)
	reviewer := NewReviewer(tc, integPool, agentRepo)

	lrCfg := &lrOnlyConfig{vals: map[string]int64{
		"per_layer_token_budget":         0,
		"tree_concepts_per_section":      1,
		"correction_loop_max_iterations": 1,
	}}
	lr := NewLayeredRunner(integPool, NewTreeRepository(), agentRepo, chair, reviewer, lrCfg)

	runner := NewAgentRunner(integPool, agentRepo, chair, prof, reviewer, nil, nil, tc, cfg)
	runner.SetLayeredRunner(lr)
	return runner
}

// @{"req": ["REQ-AGENT-068"]}
func TestGenLifecycle_TokenCapPreFlight_FlatCourse_ZeroPaidCalls(t *testing.T) {
	truncateLifecycleTables(t)
	seed := seedFlatCourse(t, "cap_flat")

	integPool := internaldb.IntegrationPool(t)
	ctx := context.Background()

	// Pre-seed token usage > cap so tokenCapPreFlight returns ErrTokenCapExceeded.
	// cap=1 token, usage=100 → pre-flight fires immediately.
	if _, err := integPool.Exec(ctx,
		`INSERT INTO agent_token_usage (student_id, course_id, total_tokens_used)
		 VALUES ($1, $2, 100)
		 ON CONFLICT (student_id, course_id) WHERE draft_id IS NULL
		 DO UPDATE SET total_tokens_used = EXCLUDED.total_tokens_used`,
		seed.StudentID, seed.CourseID,
	); err != nil {
		t.Fatalf("seed token usage: %v", err)
	}

	zeroTrans := &zeroTransport{}
	runner := buildCapExceededRunner(t, zeroTrans)

	runFlatDispatch(t, runner, seed, 5, 1)

	// ASSERTION 1: zero transport calls (no paid Anthropic work done).
	// Counterfactual: without tokenCapPreFlight, dispatchFlatCourse would call
	// generateAllSections → professor.GenerateSection → ThrottledClient.Messages
	// → zeroTrans.RoundTrip → Calls=1. The assertion would fail.
	if zeroTrans.Calls != 0 {
		t.Errorf("token cap flat: transport.Calls = %d, want 0 "+
			"(tokenCapPreFlight must return before ANY API call — REQ-AGENT-068)",
			zeroTrans.Calls)
	}

	// ASSERTION 2: run is recorded as failed (not missing).
	// Counterfactual: if the run was not created before the pre-flight check,
	// there would be no agent_runs row to inspect.
	failedRuns := glRunCountByStatus(t, seed.CourseID, "failed")
	if failedRuns == 0 {
		t.Errorf("token cap flat: no failed run recorded (want 1 failed agent_run row — REQ-AGENT-068)")
	}

	// ASSERTION 3: backoff applied (next_eligible_at set in the future).
	// Counterfactual: if tokenCapPreFlight returned without calling handleFailedRun,
	// no backoff would be applied and the course would spin in a hot retry loop
	// (the token cap never changes, so every dispatch would fail instantly).
	nextEligible := glNextEligibleAt(t, seed.CourseID)
	if nextEligible == nil {
		t.Errorf("token cap flat: next_eligible_at not set after cap pre-flight failure "+
			"(handleFailedRun must apply backoff even for cap failures — REQ-AGENT-068)")
	}
}

// @{"req": ["REQ-AGENT-068"]}
func TestGenLifecycle_TokenCapPreFlight_TreeCourse_ZeroPaidCalls(t *testing.T) {
	truncateLifecycleTables(t)
	seed := seedTreeCourse(t, "cap_tree")

	integPool := internaldb.IntegrationPool(t)
	ctx := context.Background()

	if _, err := integPool.Exec(ctx,
		`INSERT INTO agent_token_usage (student_id, course_id, total_tokens_used)
		 VALUES ($1, $2, 100)
		 ON CONFLICT (student_id, course_id) WHERE draft_id IS NULL
		 DO UPDATE SET total_tokens_used = EXCLUDED.total_tokens_used`,
		seed.StudentID, seed.CourseID,
	); err != nil {
		t.Fatalf("seed token usage: %v", err)
	}

	zeroTrans := &zeroTransport{}
	runner := buildCapExceededRunner(t, zeroTrans)

	runTreeDispatch(t, runner, seed, 5, 1)

	// ASSERTION 1: zero transport calls.
	// Counterfactual: without tokenCapPreFlight in dispatchTreeCourse, the function
	// would call seedTreeAndGenerateRoot → chair.GenerateNode → ThrottledClient →
	// zeroTrans.Calls=1+. The assertion would fail.
	if zeroTrans.Calls != 0 {
		t.Errorf("token cap tree: transport.Calls = %d, want 0 "+
			"(tokenCapPreFlight must fire before seedTreeAndGenerateRoot — REQ-AGENT-068)",
			zeroTrans.Calls)
	}

	failedRuns := glRunCountByStatus(t, seed.CourseID, "failed")
	if failedRuns == 0 {
		t.Errorf("token cap tree: no failed run recorded — REQ-AGENT-068")
	}

	nextEligible := glNextEligibleAt(t, seed.CourseID)
	if nextEligible == nil {
		t.Errorf("token cap tree: backoff not applied after cap failure — REQ-AGENT-068")
	}
}

// ---------------------------------------------------------------------------
// Scenario 7: D21/D24 tree reset fires ONLY at the content layer.
//
// Counterfactual: the "premature-reset bug" would fire ResetAttemptCount after
// the root/section_goal layer completed (inside dispatchTreeCourse, before the
// tree is truly done). The assertion `glAttemptCount == preResetCount` (no
// change after the seed step) would fail if ResetAttemptCount were called there.
//
// The positive assertion (reset fires at content layer) is verified by checking
// that settleLayer triggers ResetAttemptCount when layer==content. We test this
// by directly calling settleLayer on a content-layer course that has a non-zero
// attempt count and asserting the count resets to 0 afterwards.
// ---------------------------------------------------------------------------

// @{"req": ["REQ-AGENT-065"]}
func TestGenLifecycle_D21_D24_TreeResetFiresOnlyAtContentLayer(t *testing.T) {
	truncateLifecycleTables(t)
	integPool := internaldb.IntegrationPool(t)
	ctx := context.Background()

	// We need a tree-mode course at the content layer stage to simulate the
	// D24 branch in settleLayer. Seed a student + tree course manually.
	var studentID uuid.UUID
	if err := integPool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, 'x', 'student') RETURNING id`,
		"gl_d2124_"+uuid.New().String()[:8],
	).Scan(&studentID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	var courseID uuid.UUID
	if err := integPool.QueryRow(ctx,
		`INSERT INTO courses (student_id, topic, status, tree_mode, current_layer,
		                     generation_attempt_count)
		 VALUES ($1, 'D21 Test', 'generating', true, 'content', 2) RETURNING id`,
		studentID,
	).Scan(&courseID); err != nil {
		t.Fatalf("insert course: %v", err)
	}

	// Insert a root, syllabus, concept (approved) and content node (awaiting_review)
	// so that settleLayer sees 0 in-flight, !allFailed, approvedOrAwaiting > 0.
	var rootID uuid.UUID
	if err := integPool.QueryRow(ctx,
		`INSERT INTO course_nodes (course_id, parent_id, node_type, ordering, status, payload)
		 VALUES ($1, NULL, 'root', 0, 'approved', '{}') RETURNING id`, courseID,
	).Scan(&rootID); err != nil {
		t.Fatalf("insert root: %v", err)
	}
	var syllabusID uuid.UUID
	if err := integPool.QueryRow(ctx,
		`INSERT INTO course_nodes (course_id, parent_id, node_type, ordering, status, payload)
		 VALUES ($1, $2, 'syllabus', 0, 'approved', '{}') RETURNING id`, courseID, rootID,
	).Scan(&syllabusID); err != nil {
		t.Fatalf("insert syllabus: %v", err)
	}
	var conceptID uuid.UUID
	if err := integPool.QueryRow(ctx,
		`INSERT INTO course_nodes (course_id, parent_id, node_type, ordering, status, payload)
		 VALUES ($1, $2, 'concept', 0, 'approved', '{}') RETURNING id`, courseID, syllabusID,
	).Scan(&conceptID); err != nil {
		t.Fatalf("insert concept: %v", err)
	}
	// Content node in awaiting_review = fully settled, not in-flight, not failed.
	if _, err := integPool.Exec(ctx,
		`INSERT INTO course_nodes (course_id, parent_id, node_type, ordering, status, payload)
		 VALUES ($1, $2, 'content', 0, 'awaiting_review', '{}')`,
		courseID, conceptID,
	); err != nil {
		t.Fatalf("insert content node: %v", err)
	}

	// Verify attempt count is 2 BEFORE calling settleLayer (at content layer).
	attemptBefore := glAttemptCount(t, courseID)
	if attemptBefore != 2 {
		t.Fatalf("D21/D24: precondition: attempt_count should be 2, got %d", attemptBefore)
	}

	// Build a runner and call settleLayer directly on the content layer.
	// settleLayer with all content nodes in awaiting_review (0 in-flight, !allFailed)
	// triggers the D24 branch: ResetAttemptCount fires.
	chair, _ := buildFakeLRChair(t, `{}`)
	lrCfg := &lrOnlyConfig{vals: map[string]int64{
		"per_layer_token_budget":         0,
		"tree_concepts_per_section":      1,
		"correction_loop_max_iterations": 1,
	}}
	lr := buildFakeLRRunner(t, chair, lrCfg)

	// Create an agent_run so EmitEvent calls in settleLayer have a target row.
	runID, err := NewAgentRepository(integPool).CreateRun(ctx, courseID, "tree_layer_generation")
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	if settleErr := lr.settleLayer(ctx, runID.ID, courseID, studentID, NodeTypeContent); settleErr != nil {
		t.Fatalf("settleLayer(content): %v", settleErr)
	}

	// ASSERTION 1 (D24): attempt count reset to 0 after content layer settles.
	// Counterfactual: if the ResetAttemptCount call were moved OUT of the
	// `if layer == NodeTypeContent` branch and into the outer settleLayer block,
	// it would fire after every layer (including root/section_goal), which would
	// mask failures that happen on a later layer after an earlier layer succeeded.
	// The premature-reset bug specifically refers to resetting in dispatchTreeCourse
	// (before ANY layer generation). Here we confirm the correct location: inside
	// settleLayer ONLY when layer==content.
	attemptAfter := glAttemptCount(t, courseID)
	if attemptAfter != 0 {
		t.Errorf("D21/D24: attempt_count after content settle = %d, want 0 "+
			"(ResetAttemptCount must fire in settleLayer only when layer==NodeTypeContent — REQ-AGENT-065)",
			attemptAfter)
	}

	// ASSERTION 2: course transitions to awaiting_layer_approval (settleLayer fires).
	status := glCourseStatus(t, courseID)
	if status != "awaiting_layer_approval" {
		t.Errorf("D21/D24: course status after content settle = %q, want 'awaiting_layer_approval'",
			status)
	}

	// ASSERTION 3: a generation_terminal event was emitted.
	var evtCount int
	if err := integPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM pipeline_events pe
		 JOIN agent_runs ar ON ar.id = pe.agent_run_id
		 WHERE pe.event_type = 'generation_terminal' AND ar.course_id = $1`,
		courseID,
	).Scan(&evtCount); err != nil {
		t.Fatalf("query generation_terminal events: %v", err)
	}
	if evtCount == 0 {
		t.Errorf("D21/D24: generation_terminal event not emitted at content layer (D24 — REQ-AGENT-065)")
	}
}

// Negative half of D21/D24: settleLayer at the section_goal layer does NOT
// reset the attempt count (premature-reset guard).
//
// @{"req": ["REQ-AGENT-065"]}
func TestGenLifecycle_D21_ResetDoesNotFireAtSectionGoalLayer(t *testing.T) {
	truncateLifecycleTables(t)
	integPool := internaldb.IntegrationPool(t)
	ctx := context.Background()

	var studentID uuid.UUID
	if err := integPool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, 'x', 'student') RETURNING id`,
		"gl_d21neg_"+uuid.New().String()[:8],
	).Scan(&studentID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	var courseID uuid.UUID
	if err := integPool.QueryRow(ctx,
		`INSERT INTO courses (student_id, topic, status, tree_mode, current_layer,
		                     generation_attempt_count)
		 VALUES ($1, 'D21 Neg', 'generating', true, 'section_goal', 2) RETURNING id`,
		studentID,
	).Scan(&courseID); err != nil {
		t.Fatalf("insert course: %v", err)
	}

	// Add a section_goal node in awaiting_review so settleLayer is satisfied.
	var rootID uuid.UUID
	if err := integPool.QueryRow(ctx,
		`INSERT INTO course_nodes (course_id, parent_id, node_type, ordering, status, payload)
		 VALUES ($1, NULL, 'root', 0, 'approved', '{}') RETURNING id`, courseID,
	).Scan(&rootID); err != nil {
		t.Fatalf("insert root: %v", err)
	}
	var syllabusID uuid.UUID
	if err := integPool.QueryRow(ctx,
		`INSERT INTO course_nodes (course_id, parent_id, node_type, ordering, status, payload)
		 VALUES ($1, $2, 'syllabus', 0, 'approved', '{}') RETURNING id`, courseID, rootID,
	).Scan(&syllabusID); err != nil {
		t.Fatalf("insert syllabus: %v", err)
	}
	if _, err := integPool.Exec(ctx,
		`INSERT INTO course_nodes (course_id, parent_id, node_type, ordering, status, payload)
		 VALUES ($1, $2, 'section_goal', 0, 'awaiting_review', '{}')`,
		courseID, syllabusID,
	); err != nil {
		t.Fatalf("insert section_goal node: %v", err)
	}

	chair, _ := buildFakeLRChair(t, `{}`)
	lrCfg := &lrOnlyConfig{vals: map[string]int64{
		"per_layer_token_budget":         0,
		"tree_concepts_per_section":      1,
		"correction_loop_max_iterations": 1,
	}}
	lr := buildFakeLRRunner(t, chair, lrCfg)

	runID, err := NewAgentRepository(integPool).CreateRun(ctx, courseID, "tree_layer_generation")
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	if settleErr := lr.settleLayer(ctx, runID.ID, courseID, studentID, NodeTypeSectionGoal); settleErr != nil {
		t.Fatalf("settleLayer(section_goal): %v", settleErr)
	}

	// ASSERTION: attempt count unchanged (still 2) after section_goal settle.
	// Counterfactual (premature-reset bug): if ResetAttemptCount were called outside
	// the `if layer == NodeTypeContent` guard, it would fire here and zero the counter
	// after a non-terminal layer, hiding true dispatch-level failure counts.
	attemptAfter := glAttemptCount(t, courseID)
	if attemptAfter != 2 {
		t.Errorf("D21 negative: attempt_count after section_goal settle = %d, want 2 "+
			"(ResetAttemptCount must NOT fire at non-content layers — REQ-AGENT-065)",
			attemptAfter)
	}
}

// ---------------------------------------------------------------------------
// Scenario 8: Happy path — no regression.
//
// A flat course whose generation succeeds (canned 200 responses) reaches
// status='active' with generation_attempt_count=0 and the run 'completed'.
// A tree seed reaches 'awaiting_layer_approval'. Confirms lifecycle hooks did
// not break the success path.
//
// Counterfactual: if handleCompletedRun or the status='active' UPDATE in
// RunContentGeneration were removed, the course would remain in 'generating'
// or 'syllabus_approved' and the assertion on course status would fail.
// ---------------------------------------------------------------------------

// @{"req": ["REQ-AGENT-064", "REQ-AGENT-065", "REQ-AGENT-066"]}
func TestGenLifecycle_HappyPath_FlatCourse_ReachesActive(t *testing.T) {
	truncateLifecycleTables(t)
	seed := seedFlatCourse(t, "happy_flat")
	runner, _ := buildSucceedingAgentRunner(t)

	runFlatDispatch(t, runner, seed, 5, 600)

	// ASSERTION 1: course reached 'active'.
	// Counterfactual: if RunContentGeneration did not UPDATE courses SET status='active',
	// the status would stay 'generating' (the temporary state set during generation)
	// or 'syllabus_approved' if the UPDATE was never reached.
	status := glCourseStatus(t, seed.CourseID)
	if status != "active" {
		t.Errorf("happy flat: course status = %q, want 'active' "+
			"(RunContentGeneration must transition to active on success — REQ-AGENT-064)", status)
	}

	// ASSERTION 2: attempt count reset to 0 (handleCompletedRun fired).
	// Counterfactual: if handleCompletedRun / ResetAttemptCount were not called,
	// the count would remain at whatever it was before (0 for a fresh course,
	// but non-zero after prior failures). For a fresh course this would be 0
	// regardless — but we also verify completed run exists so the full path ran.
	attemptCount := glAttemptCount(t, seed.CourseID)
	if attemptCount != 0 {
		t.Errorf("happy flat: attempt_count = %d, want 0 "+
			"(handleCompletedRun must call ResetAttemptCount — REQ-AGENT-065)", attemptCount)
	}

	// ASSERTION 3: a completed agent_run exists.
	completedRuns := glRunCountByStatus(t, seed.CourseID, "completed")
	if completedRuns == 0 {
		t.Errorf("happy flat: no completed agent_run row (want at least 1 — REQ-AGENT-066)")
	}
}

// @{"req": ["REQ-AGENT-064", "REQ-AGENT-065", "REQ-AGENT-066"]}
func TestGenLifecycle_HappyPath_TreeSeed_ReachesAwaitingLayerApproval(t *testing.T) {
	truncateLifecycleTables(t)
	seed := seedTreeCourse(t, "happy_tree")
	runner, _ := buildSucceedingAgentRunner(t)

	runTreeDispatch(t, runner, seed, 5, 600)

	// ASSERTION 1: course transitioned out of 'syllabus_approved'.
	// The tree seed path: dispatchTreeCourse → seedTreeAndGenerateRoot → GenerateLayer
	// → settleLayer → course becomes 'awaiting_layer_approval'.
	// Counterfactual: if seedTreeAndGenerateRoot returned an error or if settleLayer
	// didn't fire, the course would remain in 'syllabus_approved' (or become
	// 'generation_failed' if the seed step errored).
	status := glCourseStatus(t, seed.CourseID)
	if status != "awaiting_layer_approval" {
		t.Errorf("happy tree: course status = %q, want 'awaiting_layer_approval' "+
			"(tree seed must complete section_goal layer and emit HITL gate — REQ-AGENT-064)", status)
	}

	// ASSERTION 2: at least one completed tree_layer_generation run.
	completedRuns := glRunCountByStatus(t, seed.CourseID, "completed")
	if completedRuns == 0 {
		t.Errorf("happy tree: no completed run (dispatchTreeCourse must mark run completed " +
			"after seedTreeAndGenerateRoot — REQ-AGENT-066)")
	}

	// ASSERTION 3: attempt count is 0 (no failures occurred; D21 not invoked yet).
	// This confirms the tree seed path does NOT call ResetAttemptCount
	// (D21 fires only in settleLayer at the content layer — tested in Scenario 7).
	attemptCount := glAttemptCount(t, seed.CourseID)
	if attemptCount != 0 {
		t.Errorf("happy tree: attempt_count = %d, want 0 (no failures occurred — REQ-AGENT-065)",
			attemptCount)
	}
}
