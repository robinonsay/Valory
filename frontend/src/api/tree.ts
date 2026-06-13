// @{"req": ["REQ-SYS-073", "REQ-SYS-077"]}
// Typed API-client methods for the knowledge-tree endpoints (G2-S3 API).
//
// GET endpoints use the bare `get` helper (no CSRF required per api-sse §5).
// POST / PATCH / DELETE use `post`, `patch`, `del` (CSRF handled by the
// client wrapper via getCsrfToken + X-CSRF-Token header).
//
// 409 conflict codes are surfaced to callers via ApiError.body.error — the
// caller should check err instanceof ApiError && err.status === 409 and read
// (err.body as { error: ConflictCode }).error.

import { get, post, patch, del } from '@/api/client'
import type {
  ListNodesResponse,
  GenerateNodeResponse,
  NodeResponse,
  ExpandLayerResponse,
  NodeChatHistoryResponse,
  CreateDraftResponse,
  ListDraftsResponse,
  GetDraftResponse,
  GenerateDraftNodeResponse,
  DraftNodeResponse,
  PublishDraftResponse,
} from '@/types/tree'

// ---------------------------------------------------------------------------
// Student node surface — /api/v1/courses/{courseId}/nodes and /layers
// ---------------------------------------------------------------------------

// @{"req": ["REQ-SYS-073"]}
export function listCourseNodes(
  courseId: string,
  opts?: { nodeType?: string; parentId?: string },
  token?: string | null,
): Promise<ListNodesResponse> {
  const params = new URLSearchParams()
  if (opts?.nodeType) params.set('node_type', opts.nodeType)
  if (opts?.parentId) params.set('parent_id', opts.parentId)
  const qs = params.toString()
  return get<ListNodesResponse>(
    `/api/v1/courses/${courseId}/nodes${qs ? `?${qs}` : ''}`,
    token,
  )
}

// @{"req": ["REQ-SYS-073"]}
export function generateCourseNode(
  courseId: string,
  body: { node_type: string; parent_id?: string | null; hint?: string },
  token?: string | null,
): Promise<GenerateNodeResponse> {
  return post<GenerateNodeResponse>(
    `/api/v1/courses/${courseId}/nodes/generate`,
    body,
    token,
  )
}

// @{"req": ["REQ-SYS-073"]}
export function refineCourseNode(
  courseId: string,
  nodeId: string,
  body: { message: string },
  token?: string | null,
): Promise<GenerateNodeResponse> {
  return post<GenerateNodeResponse>(
    `/api/v1/courses/${courseId}/nodes/${nodeId}/refine`,
    body,
    token,
  )
}

// @{"req": ["REQ-SYS-073"]}
export function approveCourseNode(
  courseId: string,
  nodeId: string,
  token?: string | null,
): Promise<NodeResponse> {
  return patch<NodeResponse>(
    `/api/v1/courses/${courseId}/nodes/${nodeId}/approve`,
    {},
    token,
  )
}

// @{"req": ["REQ-SYS-073"]}
export function feedbackCourseNode(
  courseId: string,
  nodeId: string,
  body: { feedback: string },
  token?: string | null,
): Promise<NodeResponse> {
  return patch<NodeResponse>(
    `/api/v1/courses/${courseId}/nodes/${nodeId}/feedback`,
    body,
    token,
  )
}

// @{"req": ["REQ-SYS-073"]}
export function regenerateCourseNode(
  courseId: string,
  nodeId: string,
  token?: string | null,
): Promise<GenerateNodeResponse> {
  return post<GenerateNodeResponse>(
    `/api/v1/courses/${courseId}/nodes/${nodeId}/regenerate`,
    {},
    token,
  )
}

// @{"req": ["REQ-SYS-073"]}
export function getCourseNodeChat(
  courseId: string,
  nodeId: string,
  token?: string | null,
): Promise<NodeChatHistoryResponse> {
  return get<NodeChatHistoryResponse>(
    `/api/v1/courses/${courseId}/nodes/${nodeId}/chat`,
    token,
  )
}

// @{"req": ["REQ-SYS-073"]}
export function expandCourseLayer(
  courseId: string,
  layer: string,
  token?: string | null,
): Promise<ExpandLayerResponse> {
  return post<ExpandLayerResponse>(
    `/api/v1/courses/${courseId}/layers/${layer}/expand`,
    {},
    token,
  )
}

// ---------------------------------------------------------------------------
// Admin draft surface — /api/v1/admin/drafts
// ---------------------------------------------------------------------------

// @{"req": ["REQ-SYS-077"]}
export function createDraft(
  body: { title: string; topic: string; level: string; parameters?: Record<string, unknown> },
  token?: string | null,
): Promise<CreateDraftResponse> {
  return post<CreateDraftResponse>('/api/v1/admin/drafts', body, token)
}

// @{"req": ["REQ-SYS-077"]}
export function listDrafts(
  opts?: { status?: string; limit?: number; cursor?: string },
  token?: string | null,
): Promise<ListDraftsResponse> {
  const params = new URLSearchParams()
  if (opts?.status) params.set('status', opts.status)
  if (opts?.limit != null) params.set('limit', String(opts.limit))
  if (opts?.cursor) params.set('cursor', opts.cursor)
  const qs = params.toString()
  return get<ListDraftsResponse>(
    `/api/v1/admin/drafts${qs ? `?${qs}` : ''}`,
    token,
  )
}

// @{"req": ["REQ-SYS-077"]}
export function getDraft(draftId: string, token?: string | null): Promise<GetDraftResponse> {
  return get<GetDraftResponse>(`/api/v1/admin/drafts/${draftId}`, token)
}

// @{"req": ["REQ-SYS-077"]}
export function deleteDraft(draftId: string, token?: string | null): Promise<void> {
  return del<void>(`/api/v1/admin/drafts/${draftId}`, token)
}

// @{"req": ["REQ-SYS-077"]}
export function generateDraftNode(
  draftId: string,
  body: { node_type: string; parent_id?: string | null; parameters?: Record<string, unknown> },
  token?: string | null,
): Promise<GenerateDraftNodeResponse> {
  return post<GenerateDraftNodeResponse>(
    `/api/v1/admin/drafts/${draftId}/nodes/generate`,
    body,
    token,
  )
}

// @{"req": ["REQ-SYS-077"]}
export function refineDraftNode(
  draftId: string,
  nodeId: string,
  body: { message: string },
  token?: string | null,
): Promise<GenerateDraftNodeResponse> {
  return post<GenerateDraftNodeResponse>(
    `/api/v1/admin/drafts/${draftId}/nodes/${nodeId}/refine`,
    body,
    token,
  )
}

// @{"req": ["REQ-SYS-077"]}
export function approveDraftNode(
  draftId: string,
  nodeId: string,
  token?: string | null,
): Promise<DraftNodeResponse> {
  return patch<DraftNodeResponse>(
    `/api/v1/admin/drafts/${draftId}/nodes/${nodeId}/approve`,
    {},
    token,
  )
}

// @{"req": ["REQ-SYS-077"]}
export function feedbackDraftNode(
  draftId: string,
  nodeId: string,
  body: { feedback: string },
  token?: string | null,
): Promise<DraftNodeResponse> {
  return patch<DraftNodeResponse>(
    `/api/v1/admin/drafts/${draftId}/nodes/${nodeId}/feedback`,
    body,
    token,
  )
}

// @{"req": ["REQ-SYS-077"]}
export function regenerateDraftNode(
  draftId: string,
  nodeId: string,
  token?: string | null,
): Promise<GenerateDraftNodeResponse> {
  return post<GenerateDraftNodeResponse>(
    `/api/v1/admin/drafts/${draftId}/nodes/${nodeId}/regenerate`,
    {},
    token,
  )
}

// @{"req": ["REQ-SYS-077"]}
export function getDraftNodeChat(
  draftId: string,
  nodeId: string,
  token?: string | null,
): Promise<NodeChatHistoryResponse> {
  return get<NodeChatHistoryResponse>(
    `/api/v1/admin/drafts/${draftId}/nodes/${nodeId}/chat`,
    token,
  )
}

// @{"req": ["REQ-SYS-077"]}
export function expandDraftLayer(
  draftId: string,
  layer: string,
  token?: string | null,
): Promise<ExpandLayerResponse> {
  return post<ExpandLayerResponse>(
    `/api/v1/admin/drafts/${draftId}/layers/${layer}/expand`,
    {},
    token,
  )
}

// @{"req": ["REQ-SYS-077"]}
export function publishDraft(
  draftId: string,
  body: { assignment_title?: string; assignment_id?: string | null },
  token?: string | null,
): Promise<PublishDraftResponse> {
  return post<PublishDraftResponse>(
    `/api/v1/admin/drafts/${draftId}/publish`,
    body,
    token,
  )
}
