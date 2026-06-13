// @{"req": ["REQ-SYS-073", "REQ-SYS-077"]}
// TypeScript types that exactly mirror the G2-S3 API wire shapes.
// Field names match the Go JSON tags in node_handler.go (courseNodeResponse)
// and draft_handler.go (draftNodeResponse / draftResponse).

// ---------------------------------------------------------------------------
// Node status and type enums
// ---------------------------------------------------------------------------

export type NodeStatus =
  | 'pending'
  | 'generating'
  | 'awaiting_review'
  | 'approved'
  | 'rejected'
  | 'failed'

export type NodeType =
  | 'root'
  | 'syllabus'
  | 'section_goal'
  | 'concept'
  | 'content'

// ---------------------------------------------------------------------------
// Student course node (course_nodes table)
// Matches courseNodeResponse in internal/agent/node_handler.go
// ---------------------------------------------------------------------------

// @{"req": ["REQ-SYS-073"]}
export interface CourseNode {
  id: string
  course_id: string
  parent_id: string | null
  node_type: NodeType
  ordering: number
  status: NodeStatus
  // payload is node-type-specific JSONB. For 'content' nodes it is AsciiDoc
  // source; for section_goal/concept nodes it is structured JSON. The frontend
  // always receives it as a JSON-serialised value from the API.
  payload: Record<string, unknown>
  created_at: string
  updated_at: string
}

export interface ListNodesResponse {
  nodes: CourseNode[]
}

export interface GenerateNodeResponse {
  node: CourseNode
  run_id: string
}

export interface NodeResponse {
  node: CourseNode
}

export interface ExpandLayerResponse {
  next_layer: string
  inserted_node_count: number
}

// ---------------------------------------------------------------------------
// Admin draft (course_drafts table)
// Matches draftResponse in internal/admin/draft_handler.go
// ---------------------------------------------------------------------------

// @{"req": ["REQ-SYS-077"]}
export interface CourseDraft {
  id: string
  admin_id: string
  title: string
  topic: string
  level: 'beginner' | 'intermediate' | 'advanced' | string
  parameters: Record<string, unknown>
  status: 'draft' | 'published' | string
  // current_layer is set when the draft is actively generating a layer.
  current_layer?: string | null
  published_to_assignment_id?: string | null
  created_at: string
  updated_at: string
}

// ---------------------------------------------------------------------------
// Admin draft node (draft_nodes table)
// Matches draftNodeResponse in internal/admin/draft_handler.go
// ---------------------------------------------------------------------------

// @{"req": ["REQ-SYS-077"]}
export interface DraftNode {
  id: string
  draft_id: string
  parent_id: string | null
  node_type: NodeType
  ordering: number
  status: NodeStatus
  payload: Record<string, unknown>
  created_at: string
  updated_at: string
}

export interface CreateDraftResponse {
  draft: CourseDraft
}

export interface GetDraftResponse {
  draft: CourseDraft
  nodes: DraftNode[]
}

export interface ListDraftsResponse {
  // list items include only summary fields (id, title, topic, level, status, created_at)
  drafts: Pick<CourseDraft, 'id' | 'title' | 'topic' | 'level' | 'status' | 'created_at'>[]
  next_cursor: string | null
}

export interface GenerateDraftNodeResponse {
  node: DraftNode
  run_id: string
}

export interface DraftNodeResponse {
  node: DraftNode
}

export interface PublishDraftResponse {
  assignment_id: string
  draft: CourseDraft
}

// ---------------------------------------------------------------------------
// Node chat message (node_chats table)
// Matches nodeChatResponse in internal/agent/node_handler.go and the inline
// map shape in draft_handler.go (nodeChatHistory).
// ---------------------------------------------------------------------------

// @{"req": ["REQ-SYS-073", "REQ-SYS-077"]}
export interface NodeChatMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  created_at: string
}

export interface NodeChatHistoryResponse {
  messages: NodeChatMessage[]
}

// ---------------------------------------------------------------------------
// SSE event envelope
// Matches the pipeline_events wire format defined in api-sse §5.3.4.
// ---------------------------------------------------------------------------

// @{"req": ["REQ-SYS-073", "REQ-SYS-077"]}
export interface SSEEventEnvelope {
  id: string
  agent_run_id: string
  event_type: string
  payload: SSENodePayload | SSELayerPayload | SSEStatusPayload | Record<string, unknown>
  emitted_at: string
}

// node_generating | node_ready | node_approved | node_rejected | node_error
export interface SSENodePayload {
  node_id?: string
  node_type?: string
  course_id?: string
  draft_id?: string
  // node_ready carries a first-200-char summary of the new content
  payload_summary?: string
  // node_error carries the error message
  error?: string
}

// layer_awaiting_expand
export interface SSELayerPayload {
  layer: string
  course_id?: string
  draft_id?: string
}

// status_change, api_failure (existing course pipeline events reused on student path)
export interface SSEStatusPayload {
  status?: string
  error?: string
}

// ---------------------------------------------------------------------------
// Conflict error codes surfaced by the API
// Matches 409 error codes enumerated in api-sse §5.1 / §5.2.
// ---------------------------------------------------------------------------

// @{"req": ["REQ-SYS-073", "REQ-SYS-077"]}
export type ConflictCode =
  | 'NODE_ALREADY_GENERATING'
  | 'NODE_NOT_REVIEWABLE'
  | 'NODE_NOT_REGENERABLE'
  | 'LAYER_NOT_FULLY_APPROVED'
  | 'TREE_ALREADY_COMPLETE'
  | 'DRAFT_ALREADY_PUBLISHED'
  | 'DRAFT_HAS_UNAPPROVED_NODES'
