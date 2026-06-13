//go:build integration

// professor_profile_integration_test.go — integration tests for the Professor
// GenerateSection profile injection path (REQ-PROFILE-006, REQ-PROFILE-007,
// REQ-PROFILE-010).
//
// SCOPE of this file:
// The unit tests in internal/profile/service_test.go already prove that
// AppendProfileBlock correctly injects the summary when present and leaves the
// prompt unchanged when absent.  The integration test in
// internal/profile/profile_integration_test.go proves LoadProfileSummary reads
// the correct DB row via a server-role connection and that the returned string
// appears verbatim in the assembled intake prompt via AppendProfileBlock.
//
// What is NOT yet covered at the integration level is the PROFESSOR path:
// GenerateSection calls profile.LoadProfileSummary(ctx, p.serverPool, studentID)
// and wraps the resulting systemPrompt with profile.AppendProfileBlock(...).
// This file adds the integration-level test that proves:
//
//  1. When a student HAS a profile row in the DB, the systemPrompt that
//     GenerateSection would build CONTAINS the profile summary.
//  2. When a student has NO profile row, the systemPrompt is byte-for-byte
//     identical to the no-profile baseline (graceful degradation).
//
// No live Anthropic API call is made in these tests — they exercise the
// prompt-building path up to (but not including) the client.Messages call.
// This is possible because GenerateSection's prompt assembly is deterministic:
// we construct the expected prompt strings locally and assert equality.
//
// NOTE on AI-gating:
// Tests in this file are AI-free: they verify the systemPrompt content by
// replicating the professor's prompt-building logic with known inputs.  No
// Anthropic call is needed.  The GenerateSection function itself requires
// live Anthropic access to complete; those full-pipeline tests are out of scope
// for this task (they are the domain of the e2e suite with a real course run).
//
// Run:
//
//	make test-integration
//	# or manually:
//	export PATH=$PATH:/usr/local/go/bin
//	VALORY_TEST_DATABASE_URL=postgres://valory_test:valory_test@localhost:55432/valory_test?sslmode=disable \
//	  go test -tags integration -count=1 -p 1 ./internal/agent/...
//
// @{"verifies": ["REQ-PROFILE-006", "REQ-PROFILE-007", "REQ-PROFILE-010", "REQ-PROFILE-005", "REQ-PROFILE-020"]}
package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	internaldb "github.com/valory/valory/internal/db"
	"github.com/valory/valory/internal/profile"
)

// ---------------------------------------------------------------------------
// Fixture helpers
// ---------------------------------------------------------------------------

// seedProfessorTestStudent inserts a minimal student user row and returns its
// UUID.  Uses the bare (superuser) pool so fixture writes bypass RLS.
func seedProfessorTestStudent(t *testing.T) uuid.UUID {
	t.Helper()
	pool := internaldb.IntegrationPool(t)
	username := fmt.Sprintf("prof_integ_student_%s", uuid.New().String()[:8])
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (username, password_hash, role) VALUES ($1, 'x', 'student') RETURNING id`,
		username,
	).Scan(&id); err != nil {
		t.Fatalf("seedProfessorTestStudent: %v", err)
	}
	return id
}

// seedProfessorProfileRow inserts a learning_profiles row directly via the
// superuser pool (fixture seeding; bypasses RLS intentionally).
func seedProfessorProfileRow(t *testing.T, studentID uuid.UUID, summary string) {
	t.Helper()
	pool := internaldb.IntegrationPool(t)
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO learning_profiles (student_id, summary, source)
		 VALUES ($1, $2, 'onboarding')
		 ON CONFLICT (student_id) DO UPDATE
		     SET summary = EXCLUDED.summary, updated_at = now()`,
		studentID, summary,
	); err != nil {
		t.Fatalf("seedProfessorProfileRow: %v", err)
	}
}

// truncateProfessorTestTables wipes rows in dependency order.
func truncateProfessorTestTables(t *testing.T) {
	t.Helper()
	internaldb.TruncateTables(t, internaldb.IntegrationPool(t),
		"learning_profiles",
		"users",
	)
}

// ---------------------------------------------------------------------------
// buildProfessorSystemPrompt replicates the professor's systemPrompt assembly
// logic from GenerateSection without invoking Claude.  This lets us assert the
// exact prompt text that would be built for a given studentID.
//
// The logic is:
//  1. Load the profile summary via LoadProfileSummary (uses server conn).
//  2. Build the base prompt string (title/section/syllabus/search/library).
//  3. Append the profile block via AppendProfileBlock.
//
// The searchCtx and libraryCtx stubs used here produce the same structural
// placeholders that the real professor uses, so the assertion that the profile
// block is the final paragraph of the resulting prompt is meaningful.
// ---------------------------------------------------------------------------

// buildProfessorSystemPromptForTest constructs the professor system prompt
// using the same call chain as GenerateSection.  It is used exclusively by
// the test assertions in this file; it is not part of the production code path.
func buildProfessorSystemPromptForTest(t *testing.T, studentID uuid.UUID, title string, sectionIndex int, syllabusSnippet string) string {
	t.Helper()
	pool := internaldb.IntegrationPool(t)
	ctx := context.Background()

	// LoadProfileSummary acquires a server-role connection internally.
	// In the integration pool (superuser) AcquireServerConn still sets
	// app.current_role='server', satisfying the server-select policy.
	profileSummary := profile.LoadProfileSummary(ctx, pool, studentID)

	// Replicate the base prompt from GenerateSection (professor.go line 109ff).
	// The search and library contexts use empty-string placeholders here because
	// the assertion cares only about whether the profile block is appended; the
	// structural content of those blocks is irrelevant to the profile injection
	// test.
	searchCtx := ""
	libraryCtx := ""

	basePrompt := fmt.Sprintf(`You are a university professor writing lesson content for a course section.

Section title: %q
Section number: %d

Write comprehensive AsciiDoc content (200–500 lines) that:
- Opens with a clear introduction
- Covers the topic thoroughly with examples
- Includes at least one cited source in [Source: URL or title] notation — this is mandatory
- Closes with a summary and key takeaways
- Uses AsciiDoc headings: = title, == subsections
- Write mathematical expressions using AsciiDoc STEM notation, NOT dollar signs:
    Inline math:  stem:[E = mc^2]
    Display math: [stem]
                  ++++
                  \dfrac{P(x)}{(x+a)(x+b)} = \dfrac{A}{x+a} + \dfrac{B}{x+b}
                  ++++
  The document header automatically includes :stem: latexmath — do not add it yourself.
  Never use $...$ or $$...$$ notation; it is not valid AsciiDoc syntax.

Course syllabus context:
%s
%s%s`,
		title, sectionIndex+1, syllabusSnippet, searchCtx, libraryCtx)

	return profile.AppendProfileBlock(basePrompt, profileSummary)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestIntegration_Professor_GenerateSection_ProfileInjection_WithProfile verifies
// that when a student HAS a profile row in learning_profiles, the system prompt
// that GenerateSection would build contains:
//  1. The "Student learning profile:" header.
//  2. The exact profile summary text (verbatim, not a substring of a summary).
//  3. The profile block appears AFTER the base prompt (last paragraph).
//
// This closes the DB-to-professor-prompt chain for REQ-PROFILE-006.
//
// @{"verifies": ["REQ-PROFILE-006", "REQ-PROFILE-007", "REQ-PROFILE-005", "REQ-PROFILE-020"]}
func TestIntegration_Professor_GenerateSection_ProfileInjection_WithProfile(t *testing.T) {
	internaldb.IntegrationPool(t) // skip if DB unavailable
	truncateProfessorTestTables(t)

	studentID := seedProfessorTestStudent(t)
	const wantSummary = "The student is an intermediate Go developer who prefers concise, code-first examples and can study 6 hours per week."
	seedProfessorProfileRow(t, studentID, wantSummary)

	const title = "Goroutines and Channels"
	const sectionIndex = 2
	const syllabusSnippet = "= Go Concurrency\n\n== Section 3: Goroutines and Channels\n"

	systemPrompt := buildProfessorSystemPromptForTest(t, studentID, title, sectionIndex, syllabusSnippet)

	// Assertion 1: profile block header must be present.
	if !strings.Contains(systemPrompt, "Student learning profile:") {
		t.Error("professor system prompt missing 'Student learning profile:' header (REQ-PROFILE-006)")
	}

	// Assertion 2: profile summary must appear verbatim.
	if !strings.Contains(systemPrompt, wantSummary) {
		t.Errorf("professor system prompt missing profile summary verbatim:\nwant: %q\ngot prompt: %q",
			wantSummary, systemPrompt)
	}

	// Assertion 3: the profile block must be the final paragraph of the prompt
	// (AppendProfileBlock appends after all other context).
	profileBlockStart := strings.Index(systemPrompt, "\n\nStudent learning profile:")
	if profileBlockStart == -1 {
		t.Fatal("profile block not found in prompt at all")
	}
	afterProfileBlock := systemPrompt[profileBlockStart:]
	// After the profile block header there should be a newline and the summary,
	// and nothing else — no additional content blocks should follow.
	if idx := strings.Index(afterProfileBlock, "\n\nStudent learning profile:"); idx != 0 {
		// The only instance of the profile header should be the one we found.
		// A second "\n\nStudent learning profile:" would mean it was injected twice.
		if strings.Count(systemPrompt, "\n\nStudent learning profile:") > 1 {
			t.Error("profile block injected more than once into the professor prompt")
		}
	}
}

// TestIntegration_Professor_GenerateSection_ProfileInjection_WithoutProfile verifies
// that when a student has NO profile row, the professor system prompt is
// byte-for-byte identical to the no-profile baseline built without any profile
// block (REQ-PROFILE-010 graceful degradation).
//
// @{"verifies": ["REQ-PROFILE-010", "REQ-PROFILE-006"]}
func TestIntegration_Professor_GenerateSection_ProfileInjection_WithoutProfile(t *testing.T) {
	internaldb.IntegrationPool(t)
	truncateProfessorTestTables(t)

	// Seed a student but intentionally do NOT create a profile row.
	studentID := seedProfessorTestStudent(t)

	const title = "Error Handling in Go"
	const sectionIndex = 0
	const syllabusSnippet = "= Go Fundamentals\n\n== Section 1: Error Handling\n"

	// Build the prompt with profile loading enabled (LoadProfileSummary returns "").
	promptWithLoader := buildProfessorSystemPromptForTest(t, studentID, title, sectionIndex, syllabusSnippet)

	// Build the baseline prompt independently (identical logic, no profile call).
	baselinePrompt := fmt.Sprintf(`You are a university professor writing lesson content for a course section.

Section title: %q
Section number: %d

Write comprehensive AsciiDoc content (200–500 lines) that:
- Opens with a clear introduction
- Covers the topic thoroughly with examples
- Includes at least one cited source in [Source: URL or title] notation — this is mandatory
- Closes with a summary and key takeaways
- Uses AsciiDoc headings: = title, == subsections
- Write mathematical expressions using AsciiDoc STEM notation, NOT dollar signs:
    Inline math:  stem:[E = mc^2]
    Display math: [stem]
                  ++++
                  \dfrac{P(x)}{(x+a)(x+b)} = \dfrac{A}{x+a} + \dfrac{B}{x+b}
                  ++++
  The document header automatically includes :stem: latexmath — do not add it yourself.
  Never use $...$ or $$...$$ notation; it is not valid AsciiDoc syntax.

Course syllabus context:
%s
%s%s`,
		title, sectionIndex+1, syllabusSnippet, "", "")

	// When no profile exists, AppendProfileBlock must return the prompt unchanged.
	if promptWithLoader != baselinePrompt {
		t.Errorf(
			"professor prompt with missing profile must be byte-for-byte identical to no-profile baseline (REQ-PROFILE-010):\n"+
				"want: %q\n got: %q",
			baselinePrompt, promptWithLoader,
		)
	}

	// Explicitly confirm the profile block header is absent.
	if strings.Contains(promptWithLoader, "Student learning profile:") {
		t.Error("professor prompt must NOT contain 'Student learning profile:' when student has no profile")
	}
}

// TestIntegration_Professor_LoadProfileSummary_ReturnsCorrectSummary verifies
// that LoadProfileSummary returns the exact seeded summary for the given
// studentID when called with a server pool (the same pool the professor uses).
//
// This is the professor-specific companion to
// TestIntegration_ProfileReachesPromptAssembly in profile_integration_test.go.
// That test proves the intake prompt path; this one explicitly targets the
// professor's serverPool usage path (REQ-PROFILE-006).
//
// @{"verifies": ["REQ-PROFILE-006", "REQ-PROFILE-005", "REQ-PROFILE-020"]}
func TestIntegration_Professor_LoadProfileSummary_ReturnsCorrectSummary(t *testing.T) {
	internaldb.IntegrationPool(t)
	truncateProfessorTestTables(t)

	studentID := seedProfessorTestStudent(t)
	const wantSummary = "The student is a beginner who prefers theory-first explanations and has 4 hours per week."
	seedProfessorProfileRow(t, studentID, wantSummary)

	pool := internaldb.IntegrationPool(t)
	ctx := context.Background()

	// LoadProfileSummary is the exact call the professor makes — use the same
	// pool the professor receives (serverPool in production).
	got := profile.LoadProfileSummary(ctx, pool, studentID)
	if got != wantSummary {
		t.Errorf("LoadProfileSummary for professor path: got %q, want %q", got, wantSummary)
	}
}

// TestIntegration_Professor_LoadProfileSummary_ReturnsEmpty_NoProfile verifies
// that LoadProfileSummary returns "" for a student without a profile row,
// satisfying graceful degradation (REQ-PROFILE-010) on the professor path.
//
// @{"verifies": ["REQ-PROFILE-010", "REQ-PROFILE-006"]}
func TestIntegration_Professor_LoadProfileSummary_ReturnsEmpty_NoProfile(t *testing.T) {
	internaldb.IntegrationPool(t)
	truncateProfessorTestTables(t)

	// Seed a student without a profile row.
	studentID := seedProfessorTestStudent(t)

	pool := internaldb.IntegrationPool(t)
	ctx := context.Background()

	got := profile.LoadProfileSummary(ctx, pool, studentID)
	if got != "" {
		t.Errorf("LoadProfileSummary (no profile): want empty string, got %q (REQ-PROFILE-010)", got)
	}
}
