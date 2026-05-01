// professor.go — the Professor agent.
// Generates AsciiDoc lesson content for each section, performing internet
// searches for grounding (REQ-AGENT-005) and checking the content library
// for reuse opportunities (REQ-AGENT-004).
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valory/valory/internal/db"
)

// GeneratedSection is the result of a single section generation call.
type GeneratedSection struct {
	ContentID    uuid.UUID
	SectionIndex int
	Title        string
	ContentAdoc  string
}

// Professor generates course section content using the Anthropic API,
// internet search, and the shared content library.
type Professor struct {
	client      *ThrottledClient
	pool        *pgxpool.Pool
	agentRepo   *AgentRepository
	braveAPIKey string // Brave Search API key; empty disables web search
}

// @{"req": ["REQ-AGENT-003", "REQ-AGENT-004", "REQ-AGENT-005", "REQ-AGENT-010"]}
func NewProfessor(client *ThrottledClient, pool *pgxpool.Pool, agentRepo *AgentRepository, braveAPIKey string) *Professor {
	return &Professor{client: client, pool: pool, agentRepo: agentRepo, braveAPIKey: braveAPIKey}
}

// GenerateSection creates AsciiDoc lesson content for one section.
// It checks the content library first (REQ-AGENT-004), performs an internet
// search for current sources (REQ-AGENT-005), then calls Claude to write
// the content. The generated content is inserted into lesson_content and the
// new row's ID is returned for the Reviewer to verify.
//
// @{"req": ["REQ-AGENT-003", "REQ-AGENT-004", "REQ-AGENT-005"]}
func (p *Professor) GenerateSection(ctx context.Context, runID, courseID, studentID uuid.UUID, sectionIndex int, title, syllabusAdoc string) (GeneratedSection, error) {
	// Step 1: Check content library for reusable verified content (REQ-AGENT-004).
	libraryCtx := p.libraryContext(ctx, title)

	// Step 2: Internet search for grounding (REQ-AGENT-005).
	searchCtx := p.searchInternet(ctx, title)

	// Step 3: Truncate syllabus for the prompt (avoid exceeding context window).
	syllabusSnippet := syllabusAdoc
	if len(syllabusSnippet) > 2000 {
		syllabusSnippet = syllabusSnippet[:2000]
	}

	systemPrompt := fmt.Sprintf(`You are a university professor writing lesson content for a course section.

Section title: %q
Section number: %d

Write comprehensive AsciiDoc content (200–500 lines) that:
- Opens with a clear introduction
- Covers the topic thoroughly with examples
- Includes at least one cited source in [Source: URL or title] notation — this is mandatory
- Closes with a summary and key takeaways
- Uses AsciiDoc headings: = title, == subsections

Course syllabus context:
%s
%s%s`,
		title, sectionIndex+1, syllabusSnippet, searchCtx, libraryCtx)

	msg, err := p.client.Messages(ctx, studentID, courseID, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeSonnet4_6,
		MaxTokens: 4096,
		System:    []anthropic.TextBlockParam{{Text: systemPrompt}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(
				fmt.Sprintf("Please write the full lesson content for section %d: %s", sectionIndex+1, title),
			)),
		},
	})
	if err != nil {
		return GeneratedSection{}, fmt.Errorf("professor: generate section %d: %w", sectionIndex, err)
	}
	if len(msg.Content) == 0 {
		return GeneratedSection{}, fmt.Errorf("professor: generate section %d: empty response", sectionIndex)
	}
	contentAdoc := msg.Content[0].Text

	// Step 4: Insert into lesson_content with server role so the
	// lesson_content_server_policy RLS check passes. Version is auto-incremented.
	sconn, err := db.AcquireServerConn(ctx, p.pool)
	if err != nil {
		return GeneratedSection{}, fmt.Errorf("professor: generate section %d: server conn: %w", sectionIndex, err)
	}
	defer sconn.Release()
	var contentID uuid.UUID
	err = sconn.QueryRow(ctx,
		`INSERT INTO lesson_content (course_id, section_index, title, content_adoc, version)
		 VALUES ($1, $2, $3, $4,
		     COALESCE((SELECT MAX(version) FROM lesson_content WHERE course_id = $1 AND section_index = $2), 0) + 1)
		 RETURNING id`,
		courseID, sectionIndex, title, contentAdoc,
	).Scan(&contentID)
	if err != nil {
		return GeneratedSection{}, fmt.Errorf("professor: generate section %d: insert: %w", sectionIndex, err)
	}

	return GeneratedSection{
		ContentID:    contentID,
		SectionIndex: sectionIndex,
		Title:        title,
		ContentAdoc:  contentAdoc,
	}, nil
}

// RegenerateSection creates a new version of a section based on student feedback
// (REQ-AGENT-010). The revised content is inserted as a new row (higher version).
//
// @{"req": ["REQ-AGENT-010"]}
func (p *Professor) RegenerateSection(ctx context.Context, courseID, studentID uuid.UUID, sectionIndex int, feedbackText string) (GeneratedSection, error) {
	// Fetch current version using a server-role connection (lesson_content is RLS-protected).
	rconn, err := db.AcquireServerConn(ctx, p.pool)
	if err != nil {
		return GeneratedSection{}, fmt.Errorf("professor: regen section %d: server conn: %w", sectionIndex, err)
	}
	var currentTitle, currentAdoc string
	err = rconn.QueryRow(ctx,
		`SELECT title, content_adoc FROM lesson_content
		 WHERE course_id = $1 AND section_index = $2
		 ORDER BY version DESC LIMIT 1`,
		courseID, sectionIndex,
	).Scan(&currentTitle, &currentAdoc)
	rconn.Release()
	if err != nil {
		return GeneratedSection{}, fmt.Errorf("professor: regen section %d: fetch current: %w", sectionIndex, err)
	}

	searchCtx := p.searchInternet(ctx, currentTitle+" "+feedbackText)

	snippet := currentAdoc
	if len(snippet) > 3000 {
		snippet = snippet[:3000]
	}

	systemPrompt := fmt.Sprintf(`You are a university professor revising lesson content based on student feedback.

Student feedback: %q

Rewrite the content addressing the feedback. Requirements:
- Keep at least one cited source in [Source: URL or title] notation — mandatory
- Use AsciiDoc formatting (= title, == subsections)
- Be between 200–500 lines
- Directly address the student's specific concerns%s`,
		feedbackText, searchCtx)

	msg, err := p.client.Messages(ctx, studentID, courseID, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeSonnet4_6,
		MaxTokens: 4096,
		System:    []anthropic.TextBlockParam{{Text: systemPrompt}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock("Here is the current content to revise:\n\n" + snippet)),
		},
	})
	if err != nil {
		return GeneratedSection{}, fmt.Errorf("professor: regen section %d: call claude: %w", sectionIndex, err)
	}
	if len(msg.Content) == 0 {
		return GeneratedSection{}, fmt.Errorf("professor: regen section %d: empty response", sectionIndex)
	}
	contentAdoc := msg.Content[0].Text

	// Insert as a new version with server role.
	wconn, err := db.AcquireServerConn(ctx, p.pool)
	if err != nil {
		return GeneratedSection{}, fmt.Errorf("professor: regen section %d: server conn for insert: %w", sectionIndex, err)
	}
	defer wconn.Release()
	var contentID uuid.UUID
	err = wconn.QueryRow(ctx,
		`INSERT INTO lesson_content (course_id, section_index, title, content_adoc, version)
		 VALUES ($1, $2, $3, $4,
		     COALESCE((SELECT MAX(version) FROM lesson_content WHERE course_id = $1 AND section_index = $2), 0) + 1)
		 RETURNING id`,
		courseID, sectionIndex, currentTitle, contentAdoc,
	).Scan(&contentID)
	if err != nil {
		return GeneratedSection{}, fmt.Errorf("professor: regen section %d: insert: %w", sectionIndex, err)
	}

	return GeneratedSection{
		ContentID:    contentID,
		SectionIndex: sectionIndex,
		Title:        currentTitle,
		ContentAdoc:  contentAdoc,
	}, nil
}

// searchInternet performs a Brave web search and formats the top results as a
// prompt snippet (REQ-AGENT-005). Returns an empty string on error or when the
// Brave API key is not configured.
//
// @{"req": ["REQ-AGENT-005"]}
func (p *Professor) searchInternet(ctx context.Context, query string) string {
	if p.braveAPIKey == "" {
		return ""
	}

	reqURL := "https://api.search.brave.com/res/v1/web/search?q=" + url.QueryEscape(query) + "&count=3"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", p.braveAPIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return ""
	}

	var result struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return ""
	}
	if len(result.Web.Results) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n\nInternet search results (use as grounding for citations):\n")
	for _, r := range result.Web.Results {
		sb.WriteString(fmt.Sprintf("- [%s](%s): %s\n", r.Title, r.URL, r.Description))
	}
	return sb.String()
}

// libraryContext queries the lesson_content table for existing verified content
// with a similar title (REQ-AGENT-004) and formats a prompt snippet.
// The runner/handler layer is responsible for obtaining explicit student consent
// before the professor actually reuses library content; this step surfaces the
// availability so the prompt can inform Claude.
//
// @{"req": ["REQ-AGENT-004"]}
func (p *Professor) libraryContext(ctx context.Context, title string) string {
	conn, err := db.AcquireServerConn(ctx, p.pool)
	if err != nil {
		return ""
	}
	defer conn.Release()
	rows, err := conn.Query(ctx,
		`SELECT title, content_adoc FROM lesson_content
		 WHERE similarity(title, $1) > 0.3 AND citation_verified = true
		 ORDER BY similarity(title, $1) DESC LIMIT 3`,
		title,
	)
	if err != nil {
		return ""
	}
	defer rows.Close()

	type libRow struct{ Title, Content string }
	var matches []libRow
	for rows.Next() {
		var r libRow
		if err := rows.Scan(&r.Title, &r.Content); err != nil {
			continue
		}
		matches = append(matches, r)
	}
	if err := rows.Err(); err != nil || len(matches) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n\nRelated content from the library (may be referenced with student consent — REQ-AGENT-004):\n")
	for _, m := range matches {
		snippet := m.Content
		if len(snippet) > 400 {
			snippet = snippet[:400] + "…"
		}
		sb.WriteString(fmt.Sprintf("--- %s ---\n%s\n\n", m.Title, snippet))
	}
	return sb.String()
}
