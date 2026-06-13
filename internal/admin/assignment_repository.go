package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/db"
)

// ErrAssignmentNotFound is returned when the requested assignment does not exist.
var ErrAssignmentNotFound = errors.New("admin: assignment not found")

// AssignmentRow represents one row from the course_assignments table.
type AssignmentRow struct {
	ID         uuid.UUID
	AdminID    uuid.UUID
	Title      string
	Topic      string
	Level      string
	Parameters json.RawMessage
	// DraftID is non-nil when the assignment was published from an admin-authored
	// draft tree. The draft's nodes are copied into course_nodes on student assign
	// (api-sse §7.2 / G2-S3-T4 Publish→assign copy path).
	DraftID   *uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AssignmentListRow is the summary shape returned by ListAssignments.
// StudentCount is computed via a GROUP BY join to courses.
type AssignmentListRow struct {
	ID           uuid.UUID
	Title        string
	Topic        string
	Level        string
	StudentCount int
	CreatedAt    time.Time
}

// AssignmentStudentRow describes a single student's course for an assignment.
type AssignmentStudentRow struct {
	StudentID    uuid.UUID
	Username     string
	CourseID     uuid.UUID
	CourseStatus string
	AssignedAt   time.Time
}

// AssignmentCursorPayload is the opaque cursor for paginating assignments.
type assignmentCursorPayload struct {
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

// AssignmentRepository handles all DB operations for course_assignments and
// the admin-assigned-course path.
//
// @{"req": ["REQ-ASSIGN-001", "REQ-ASSIGN-002", "REQ-ASSIGN-003", "REQ-ASSIGN-004", "REQ-ASSIGN-005", "REQ-ASSIGN-006", "REQ-ASSIGN-007", "REQ-ASSIGN-009", "REQ-ASSIGN-010", "REQ-ASSIGN-011"]}
type AssignmentRepository struct {
	pool *pgxpool.Pool
}

// @{"req": ["REQ-ASSIGN-001", "REQ-ASSIGN-002", "REQ-ASSIGN-003", "REQ-ASSIGN-004", "REQ-ASSIGN-005", "REQ-ASSIGN-006", "REQ-ASSIGN-007", "REQ-ASSIGN-009", "REQ-ASSIGN-010", "REQ-ASSIGN-011"]}
func NewAssignmentRepository(pool *pgxpool.Pool) *AssignmentRepository {
	return &AssignmentRepository{pool: pool}
}

// conn returns the request-scoped connection stored in ctx by the auth
// middleware when one is present. That connection carries app.current_user_id
// and app.current_role GUCs required for RLS evaluation on the courses table
// (FORCE ROW LEVEL SECURITY, courses_admin_policy checks app.current_role='admin').
// Falls back to the bare pool for callers that supply no scoped connection
// (e.g. background workers or tests that use the raw pool).
//
// Only courses-table operations must go through conn(ctx). Tables without RLS
// (course_assignments, users, syllabi, agent_token_usage) may use r.pool directly.
//
// @{"req": ["REQ-ASSIGN-003", "REQ-ASSIGN-004", "REQ-ASSIGN-005", "REQ-ASSIGN-006", "REQ-ASSIGN-007", "REQ-ASSIGN-009", "REQ-ASSIGN-010", "REQ-ASSIGN-011", "REQ-SECURITY-002"]}
func (r *AssignmentRepository) conn(ctx context.Context) db.Querier {
	if c, ok := auth.ConnFromContext(ctx); ok {
		return c
	}
	return r.pool
}

// CreateAssignment inserts a new course_assignments row.
//
// @{"req": ["REQ-ASSIGN-001"]}
func (r *AssignmentRepository) CreateAssignment(ctx context.Context, adminID uuid.UUID, title, topic, level string, parameters json.RawMessage) (AssignmentRow, error) {
	var row AssignmentRow
	err := r.pool.QueryRow(ctx,
		`INSERT INTO course_assignments (admin_id, title, topic, level, parameters)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, admin_id, title, topic, level, parameters, created_at, updated_at`,
		adminID, title, topic, level, parameters,
	).Scan(
		&row.ID,
		&row.AdminID,
		&row.Title,
		&row.Topic,
		&row.Level,
		&row.Parameters,
		&row.CreatedAt,
		&row.UpdatedAt,
	)
	if err != nil {
		return AssignmentRow{}, fmt.Errorf("admin: create assignment: %w", err)
	}
	return row, nil
}

// GetAssignment returns the assignment with the given ID or ErrAssignmentNotFound.
//
// @{"req": ["REQ-ASSIGN-006"]}
func (r *AssignmentRepository) GetAssignment(ctx context.Context, id uuid.UUID) (AssignmentRow, error) {
	var row AssignmentRow
	err := r.pool.QueryRow(ctx,
		`SELECT id, admin_id, title, topic, level, parameters, draft_id, created_at, updated_at
		 FROM course_assignments WHERE id = $1`,
		id,
	).Scan(
		&row.ID,
		&row.AdminID,
		&row.Title,
		&row.Topic,
		&row.Level,
		&row.Parameters,
		&row.DraftID,
		&row.CreatedAt,
		&row.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AssignmentRow{}, ErrAssignmentNotFound
		}
		return AssignmentRow{}, fmt.Errorf("admin: get assignment: %w", err)
	}
	return row, nil
}

// ListAssignments returns a paginated list of assignments for the given admin,
// each annotated with the count of assigned student course rows.
//
// The LEFT JOIN to courses touches a FORCE RLS table, so the query must run on
// the request-scoped connection (conn(ctx)) that carries app.current_role='admin'.
// Without it, the join would execute on a bare pool connection with empty GUCs,
// causing the courses_admin_policy to evaluate to false and student_count to
// always be 0.
//
// @{"req": ["REQ-ASSIGN-006"]}
func (r *AssignmentRepository) ListAssignments(ctx context.Context, adminID uuid.UUID, cursor string, limit int) ([]AssignmentListRow, string, error) {
	var query string
	var args []interface{}

	base := `
		SELECT ca.id, ca.title, ca.topic, ca.level,
		       COUNT(c.id) AS student_count,
		       ca.created_at
		FROM course_assignments ca
		LEFT JOIN courses c ON c.assignment_id = ca.id
		WHERE ca.admin_id = $1`

	if cursor == "" {
		query = base + `
		GROUP BY ca.id
		ORDER BY ca.created_at DESC, ca.id DESC
		LIMIT $2`
		args = []interface{}{adminID, limit + 1}
	} else {
		decoded, err := base64.StdEncoding.DecodeString(cursor)
		if err != nil {
			return nil, "", fmt.Errorf("admin: list assignments: decode cursor: %w", err)
		}
		var payload assignmentCursorPayload
		if err := json.Unmarshal(decoded, &payload); err != nil {
			return nil, "", fmt.Errorf("admin: list assignments: unmarshal cursor: %w", err)
		}
		createdAt, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
		if err != nil {
			return nil, "", fmt.Errorf("admin: list assignments: parse cursor time: %w", err)
		}
		cursorID, err := uuid.Parse(payload.ID)
		if err != nil {
			return nil, "", fmt.Errorf("admin: list assignments: parse cursor id: %w", err)
		}
		query = base + `
		  AND (ca.created_at, ca.id) < ($3, $4)
		GROUP BY ca.id
		ORDER BY ca.created_at DESC, ca.id DESC
		LIMIT $2`
		args = []interface{}{adminID, limit + 1, createdAt, cursorID}
	}

	rows, err := r.conn(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("admin: list assignments: query: %w", err)
	}
	defer rows.Close()

	var results []AssignmentListRow
	for rows.Next() {
		var a AssignmentListRow
		if err := rows.Scan(&a.ID, &a.Title, &a.Topic, &a.Level, &a.StudentCount, &a.CreatedAt); err != nil {
			return nil, "", fmt.Errorf("admin: list assignments: scan: %w", err)
		}
		results = append(results, a)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("admin: list assignments: rows: %w", err)
	}

	var nextCursor string
	if len(results) > limit {
		last := results[limit]
		payload := assignmentCursorPayload{
			CreatedAt: last.CreatedAt.Format(time.RFC3339Nano),
			ID:        last.ID.String(),
		}
		enc, err := json.Marshal(payload)
		if err != nil {
			return nil, "", fmt.Errorf("admin: list assignments: encode cursor: %w", err)
		}
		nextCursor = base64.StdEncoding.EncodeToString(enc)
		results = results[:limit]
	}

	return results, nextCursor, nil
}

// GetAssignmentStudents returns the per-student generation status for an assignment.
// The query runs on the request-scoped connection (conn(ctx)), which carries
// app.current_role='admin' so courses_admin_policy allows it to see all rows
// regardless of student_id.
//
// @{"req": ["REQ-ASSIGN-006", "REQ-ASSIGN-010"]}
func (r *AssignmentRepository) GetAssignmentStudents(ctx context.Context, assignmentID uuid.UUID) ([]AssignmentStudentRow, error) {
	rows, err := r.conn(ctx).Query(ctx,
		`SELECT c.student_id, u.username, c.id AS course_id, c.status::text, c.created_at
		 FROM courses c
		 JOIN users u ON u.id = c.student_id
		 WHERE c.assignment_id = $1
		 ORDER BY c.created_at ASC`,
		assignmentID,
	)
	if err != nil {
		return nil, fmt.Errorf("admin: get assignment students: query: %w", err)
	}
	defer rows.Close()

	var results []AssignmentStudentRow
	for rows.Next() {
		var s AssignmentStudentRow
		if err := rows.Scan(&s.StudentID, &s.Username, &s.CourseID, &s.CourseStatus, &s.AssignedAt); err != nil {
			return nil, fmt.Errorf("admin: get assignment students: scan: %w", err)
		}
		results = append(results, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admin: get assignment students: rows: %w", err)
	}

	return results, nil
}

// CreateAssignedCourse inserts a courses row for an admin-assigned student.
// The row is created at status='syllabus_approved' with intake_kickoff_sent=true
// to bypass the intake phase (REQ-ASSIGN-004) and enter the generation queue
// (REQ-ASSIGN-005).
//
// Returns the new course ID, or the sentinel error isUniqueViolationOnActiveIdx
// for callers that need to map it to COURSE_ALREADY_ACTIVE.
//
// @{"req": ["REQ-ASSIGN-003", "REQ-ASSIGN-004", "REQ-ASSIGN-005"]}
func (r *AssignmentRepository) CreateAssignedCourse(ctx context.Context, studentID, assignmentID uuid.UUID, topic string) (uuid.UUID, error) {
	var courseID uuid.UUID
	err := r.conn(ctx).QueryRow(ctx,
		`INSERT INTO courses (student_id, topic, status, assignment_id, intake_kickoff_sent)
		 VALUES ($1, $2, 'syllabus_approved', $3, true)
		 RETURNING id`,
		studentID, topic, assignmentID,
	).Scan(&courseID)
	if err != nil {
		return uuid.UUID{}, err
	}
	return courseID, nil
}

// CopyDraftNodesToCourse copies all draft_nodes from draftID into course_nodes
// for courseID using the provided querier (must be a server-role connection or
// transaction). The copy resets every node's status to 'pending' (students grow
// the tree through the normal HITL flow) and preserves node_type, ordering, and
// payload. Parent references are remapped so the new course_nodes form a
// structurally equivalent tree (draft_node.id → new course_node.id).
//
// The querier must satisfy the server_write RLS policy on course_nodes (i.e., it
// must carry app.current_role='server'). The caller is responsible for wrapping
// this call in a transaction so the course creation and node copy are atomic (D13).
//
// @{"req": ["REQ-ASSIGN-003", "REQ-ASSIGN-005", "REQ-AGENT-047", "REQ-SYS-077"]}
func (r *AssignmentRepository) CopyDraftNodesToCourse(ctx context.Context, q db.Querier, draftID, courseID uuid.UUID) error {
	// 1. Load all draft nodes ordered so parents always appear before children.
	// created_at is a reliable topological order because the expand precondition
	// requires every node in a layer to be fully approved before the next layer is
	// inserted, meaning ancestors always have an earlier created_at than descendants.
	// id ASC is a stable secondary tiebreak for nodes inserted in the same instant.
	rows, err := q.Query(ctx,
		`SELECT id, parent_id, node_type, ordering, payload, created_at
		 FROM draft_nodes
		 WHERE draft_id = $1
		 ORDER BY created_at ASC, id ASC`,
		draftID,
	)
	if err != nil {
		return fmt.Errorf("admin: copy draft nodes: query draft_nodes: %w", err)
	}
	defer rows.Close()

	type draftNodeMini struct {
		ID        uuid.UUID
		ParentID  *uuid.UUID
		NodeType  string
		Ordering  int
		Payload   json.RawMessage
		CreatedAt time.Time
	}

	var draftNodes []draftNodeMini
	for rows.Next() {
		var n draftNodeMini
		if err := rows.Scan(&n.ID, &n.ParentID, &n.NodeType, &n.Ordering, &n.Payload, &n.CreatedAt); err != nil {
			return fmt.Errorf("admin: copy draft nodes: scan: %w", err)
		}
		draftNodes = append(draftNodes, n)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("admin: copy draft nodes: rows: %w", err)
	}

	// 2. Insert each draft node as a course_node, building a draft_id → course_node_id map
	//    so child parent references can be resolved to new IDs.
	idMap := make(map[uuid.UUID]uuid.UUID, len(draftNodes))
	for _, dn := range draftNodes {
		var newParentID *uuid.UUID
		if dn.ParentID != nil {
			newID, ok := idMap[*dn.ParentID]
			if !ok {
				// A missing parent means the ordering invariant was violated.
				// Return an error immediately so a future ordering regression
				// fails loudly instead of silently producing a corrupt flat tree.
				return fmt.Errorf("admin: copy draft nodes: parent draft_node %s not yet mapped (ordering invariant violated) for child %s", *dn.ParentID, dn.ID)
			}
			newParentID = &newID
		}

		payload := dn.Payload
		if len(payload) == 0 {
			payload = json.RawMessage("{}")
		}

		var newNodeID uuid.UUID
		scanErr := q.QueryRow(ctx,
			`INSERT INTO course_nodes (course_id, parent_id, node_type, ordering, status, payload)
			 VALUES ($1, $2, $3::node_type_enum, $4, 'pending', $5)
			 RETURNING id`,
			courseID, newParentID, dn.NodeType, dn.Ordering, payload,
		).Scan(&newNodeID)
		if scanErr != nil {
			return fmt.Errorf("admin: copy draft nodes: insert course_node (draft_node=%s): %w", dn.ID, scanErr)
		}
		idMap[dn.ID] = newNodeID
	}

	return nil
}

// InsertApprovedSyllabus inserts a syllabi row with approved_at = now().
// The admin's decision to assign the course substitutes for the student's
// manual approval step (REQ-ASSIGN-004).
//
// @{"req": ["REQ-ASSIGN-003", "REQ-ASSIGN-004"]}
func (r *AssignmentRepository) InsertApprovedSyllabus(ctx context.Context, courseID uuid.UUID, contentAdoc string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO syllabi (course_id, content_adoc, version, approved_at)
		 VALUES ($1, $2, 1, now())`,
		courseID, contentAdoc,
	)
	if err != nil {
		return fmt.Errorf("admin: insert approved syllabus: %w", err)
	}
	return nil
}

// DeleteCourse removes the courses row for compensating rollback when syllabus
// generation fails after the course INSERT (REQ-ASSIGN-011 partial-success).
//
// @{"req": ["REQ-ASSIGN-011"]}
func (r *AssignmentRepository) DeleteCourse(ctx context.Context, courseID uuid.UUID) error {
	_, err := r.conn(ctx).Exec(ctx, `DELETE FROM courses WHERE id = $1`, courseID)
	if err != nil {
		return fmt.Errorf("admin: delete course (compensating rollback): %w", err)
	}
	return nil
}

// GetCourseForAssignment fetches the course row for a specific student+assignment
// pair so the unassign handler can check its status.
//
// @{"req": ["REQ-ASSIGN-007"]}
func (r *AssignmentRepository) GetCourseForAssignment(ctx context.Context, assignmentID, studentID uuid.UUID) (uuid.UUID, string, error) {
	var courseID uuid.UUID
	var status string
	err := r.conn(ctx).QueryRow(ctx,
		`SELECT id, status::text FROM courses
		 WHERE assignment_id = $1 AND student_id = $2`,
		assignmentID, studentID,
	).Scan(&courseID, &status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.UUID{}, "", nil
		}
		return uuid.UUID{}, "", fmt.Errorf("admin: get course for assignment: %w", err)
	}
	return courseID, status, nil
}

// StudentExistsAndActive verifies that studentID references an active user with role='student'.
//
// @{"req": ["REQ-ASSIGN-002"]}
func (r *AssignmentRepository) StudentExistsAndActive(ctx context.Context, studentID uuid.UUID) (bool, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE id = $1 AND role = 'student' AND is_active = true`,
		studentID,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("admin: student exists: %w", err)
	}
	return count > 0, nil
}
