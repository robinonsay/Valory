// @{"req": ["REQ-SYS-073", "REQ-SYS-077"]}
// useNodeEvents — SSE composable for the knowledge-tree node event stream.
//
// Subscribes to EITHER:
//   - student path: GET /api/v1/courses/{courseId}/events
//   - admin path:   GET /api/v1/admin/drafts/{draftId}/events
//
// The URL is supplied by the parent (actor-agnostic). The composable exposes
// reactive node and layer state updated on each event and cleans up on unmount.
//
// Reconnect / ?after=<lastEventId> resumption matches the existing useSSE
// pattern (composables/useSSE.ts). The composable wraps useSSE directly to
// avoid duplicating reconnect / backoff logic.

import { ref, computed, onUnmounted } from 'vue'
import { useSSE } from '@/composables/useSSE'
import type { CourseNode, DraftNode, SSEEventEnvelope } from '@/types/tree'

// ---------------------------------------------------------------------------
// Reactive state shapes
// ---------------------------------------------------------------------------

// A union type so callers can work with either student or admin nodes
// without needing two separate composables.
export type AnyNode = CourseNode | DraftNode

// nodeState is the per-node reactive status/payload map keyed by node id.
export interface NodeEventState {
  // nodes is the live map of all node ids → latest node status received via SSE.
  // The parent view merges these status updates into the full node list obtained
  // via the REST list endpoint (GET .../nodes).
  nodeStatuses: Record<string, string>
  // layersAwaitingExpand holds the set of layer names for which
  // layer_awaiting_expand has been received and the expand endpoint has not
  // yet been called.
  layersAwaitingExpand: Set<string>
  // error is set when the SSE connection permanently fails after all retries.
  error: string | null
  // connected reflects whether the stream is currently open.
  connected: boolean
}

// ---------------------------------------------------------------------------
// Composable
// ---------------------------------------------------------------------------

// @{"req": ["REQ-SYS-073", "REQ-SYS-077"]}
export function useNodeEvents(
  url: string,
  opts?: { token?: string | null },
): {
  nodeStatuses: ReturnType<typeof ref<Record<string, string>>>
  layersAwaitingExpand: ReturnType<typeof ref<Set<string>>>
  error: ReturnType<typeof ref<string | null>>
  connected: ReturnType<typeof ref<boolean>>
  clearLayerAwaitingExpand: (layer: string) => void
  close: () => void
} {
  const nodeStatuses = ref<Record<string, string>>({})
  const layersAwaitingExpand = ref<Set<string>>(new Set())
  const error = ref<string | null>(null)
  const connected = ref(false)

  // @{"req": ["REQ-SYS-073", "REQ-SYS-077"]}
  function onEvent(_eventType: string, data: string, _lastEventId: string): void {
    connected.value = true
    let envelope: SSEEventEnvelope
    try {
      envelope = JSON.parse(data) as SSEEventEnvelope
    } catch {
      // Malformed SSE payload — skip silently.
      return
    }

    const { event_type, payload } = envelope

    switch (event_type) {
      case 'node_generating': {
        const p = payload as { node_id?: string }
        if (p.node_id) {
          nodeStatuses.value = { ...nodeStatuses.value, [p.node_id]: 'generating' }
        }
        break
      }
      case 'node_ready': {
        const p = payload as { node_id?: string }
        if (p.node_id) {
          nodeStatuses.value = { ...nodeStatuses.value, [p.node_id]: 'awaiting_review' }
        }
        break
      }
      case 'node_approved': {
        const p = payload as { node_id?: string }
        if (p.node_id) {
          nodeStatuses.value = { ...nodeStatuses.value, [p.node_id]: 'approved' }
        }
        break
      }
      case 'node_rejected': {
        const p = payload as { node_id?: string }
        if (p.node_id) {
          nodeStatuses.value = { ...nodeStatuses.value, [p.node_id]: 'rejected' }
        }
        break
      }
      case 'node_error': {
        const p = payload as { node_id?: string }
        if (p.node_id) {
          nodeStatuses.value = { ...nodeStatuses.value, [p.node_id]: 'failed' }
        }
        break
      }
      case 'layer_awaiting_expand': {
        const p = payload as { layer?: string }
        if (p.layer) {
          const next = new Set(layersAwaitingExpand.value)
          next.add(p.layer)
          layersAwaitingExpand.value = next
        }
        break
      }
      // status_change / api_failure are course-level events; ignore for node state.
      default:
        break
    }
  }

  function onError(err: Error): void {
    connected.value = false
    error.value = err.message
  }

  const sse = useSSE(url, {
    token: opts?.token ?? null,
    onEvent,
    onError,
  })

  // clearLayerAwaitingExpand is called by the parent after it triggers expand
  // so the "expand" button disappears once the action is taken.
  function clearLayerAwaitingExpand(layer: string): void {
    const next = new Set(layersAwaitingExpand.value)
    next.delete(layer)
    layersAwaitingExpand.value = next
  }

  // Clean up on component unmount to avoid memory leaks and dangling sockets.
  onUnmounted(() => {
    sse.close()
  })

  return {
    nodeStatuses,
    layersAwaitingExpand,
    error,
    connected,
    clearLayerAwaitingExpand,
    close: sse.close,
  }
}
