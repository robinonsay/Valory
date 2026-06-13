// @{"verifies": ["REQ-SYS-073", "REQ-SYS-077"]}

import { describe, it, expect, vi, beforeEach } from 'vitest'
import type { SSEOptions } from '@/composables/useSSE'

// ─── Mock useSSE so no real EventSource / fetch calls are made ────────────────

// We capture the onEvent callback that useNodeEvents passes to useSSE so we
// can simulate SSE event delivery directly in tests.
let capturedOnEvent: ((eventType: string, data: string, lastEventId: string) => void) | null = null
let capturedOnError: ((err: Error) => void) | null = null
const mockClose = vi.fn()

vi.mock('@/composables/useSSE', () => ({
  useSSE: vi.fn((_url: string, options: SSEOptions) => {
    capturedOnEvent = options.onEvent
    capturedOnError = options.onError ?? null
    return { close: mockClose }
  }),
}))

// ─── Mock Vue's onUnmounted so we can verify cleanup ─────────────────────────

const mockOnUnmounted = vi.fn()

vi.mock('vue', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue')>()
  return {
    ...actual,
    onUnmounted: (fn: () => void) => {
      mockOnUnmounted(fn)
    },
  }
})

// @{"verifies": ["REQ-SYS-073", "REQ-SYS-077"]}
describe('useNodeEvents', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    capturedOnEvent = null
    capturedOnError = null
  })

  function fireEvent(eventType: string, payload: unknown): void {
    if (!capturedOnEvent) throw new Error('SSE onEvent was not captured')
    capturedOnEvent(eventType, JSON.stringify({
      id: 'evt-1',
      agent_run_id: 'run-1',
      event_type: eventType,
      payload,
      emitted_at: '2026-01-01T00:00:00Z',
    }), 'evt-1')
  }

  // @{"verifies": ["REQ-SYS-073"]}
  it('useNodeEvents_NodeGenerating_UpdatesNodeStatusToGenerating', async () => {
    const { useNodeEvents } = await import('@/composables/useNodeEvents')
    const { nodeStatuses } = useNodeEvents('/api/v1/courses/c1/events')

    fireEvent('node_generating', { node_id: 'n1' })

    expect(nodeStatuses.value['n1']).toBe('generating')
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('useNodeEvents_NodeReady_UpdatesNodeStatusToAwaitingReview', async () => {
    const { useNodeEvents } = await import('@/composables/useNodeEvents')
    const { nodeStatuses } = useNodeEvents('/api/v1/courses/c1/events')

    fireEvent('node_ready', { node_id: 'n1' })

    expect(nodeStatuses.value['n1']).toBe('awaiting_review')
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('useNodeEvents_NodeApproved_UpdatesNodeStatusToApproved', async () => {
    const { useNodeEvents } = await import('@/composables/useNodeEvents')
    const { nodeStatuses } = useNodeEvents('/api/v1/courses/c1/events')

    fireEvent('node_approved', { node_id: 'n2' })

    expect(nodeStatuses.value['n2']).toBe('approved')
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('useNodeEvents_NodeRejected_UpdatesNodeStatusToRejected', async () => {
    const { useNodeEvents } = await import('@/composables/useNodeEvents')
    const { nodeStatuses } = useNodeEvents('/api/v1/courses/c1/events')

    fireEvent('node_rejected', { node_id: 'n3' })

    expect(nodeStatuses.value['n3']).toBe('rejected')
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('useNodeEvents_NodeError_UpdatesNodeStatusToFailed', async () => {
    const { useNodeEvents } = await import('@/composables/useNodeEvents')
    const { nodeStatuses } = useNodeEvents('/api/v1/courses/c1/events')

    fireEvent('node_error', { node_id: 'n4' })

    expect(nodeStatuses.value['n4']).toBe('failed')
  })

  // @{"verifies": ["REQ-SYS-073", "REQ-SYS-077"]}
  it('useNodeEvents_LayerAwaitingExpand_AddsLayerToSet', async () => {
    const { useNodeEvents } = await import('@/composables/useNodeEvents')
    const { layersAwaitingExpand } = useNodeEvents('/api/v1/courses/c1/events')

    fireEvent('layer_awaiting_expand', { layer: 'section_goal' })

    expect(layersAwaitingExpand.value.has('section_goal')).toBe(true)
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('useNodeEvents_MultipleLayerEvents_AccumulatesLayers', async () => {
    const { useNodeEvents } = await import('@/composables/useNodeEvents')
    const { layersAwaitingExpand } = useNodeEvents('/api/v1/courses/c1/events')

    fireEvent('layer_awaiting_expand', { layer: 'root' })
    fireEvent('layer_awaiting_expand', { layer: 'section_goal' })

    expect(layersAwaitingExpand.value.has('root')).toBe(true)
    expect(layersAwaitingExpand.value.has('section_goal')).toBe(true)
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('useNodeEvents_ClearLayerAwaitingExpand_RemovesLayerFromSet', async () => {
    const { useNodeEvents } = await import('@/composables/useNodeEvents')
    const { layersAwaitingExpand, clearLayerAwaitingExpand } = useNodeEvents('/api/v1/courses/c1/events')

    fireEvent('layer_awaiting_expand', { layer: 'root' })
    expect(layersAwaitingExpand.value.has('root')).toBe(true)

    clearLayerAwaitingExpand('root')
    expect(layersAwaitingExpand.value.has('root')).toBe(false)
  })

  // @{"verifies": ["REQ-SYS-073", "REQ-SYS-077"]}
  it('useNodeEvents_MultipleNodeEvents_TracksAllNodes', async () => {
    const { useNodeEvents } = await import('@/composables/useNodeEvents')
    const { nodeStatuses } = useNodeEvents('/api/v1/courses/c1/events')

    fireEvent('node_generating', { node_id: 'n1' })
    fireEvent('node_ready', { node_id: 'n2' })
    fireEvent('node_approved', { node_id: 'n3' })

    expect(nodeStatuses.value['n1']).toBe('generating')
    expect(nodeStatuses.value['n2']).toBe('awaiting_review')
    expect(nodeStatuses.value['n3']).toBe('approved')
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('useNodeEvents_NodeStatusTransition_LatestEventWins', async () => {
    const { useNodeEvents } = await import('@/composables/useNodeEvents')
    const { nodeStatuses } = useNodeEvents('/api/v1/courses/c1/events')

    // First generating, then ready — latest should win
    fireEvent('node_generating', { node_id: 'n1' })
    expect(nodeStatuses.value['n1']).toBe('generating')

    fireEvent('node_ready', { node_id: 'n1' })
    expect(nodeStatuses.value['n1']).toBe('awaiting_review')
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('useNodeEvents_MalformedJsonPayload_DoesNotThrow', async () => {
    const { useNodeEvents } = await import('@/composables/useNodeEvents')
    const { nodeStatuses } = useNodeEvents('/api/v1/courses/c1/events')

    // Firing malformed JSON should silently skip
    expect(() => {
      capturedOnEvent!('message', 'not-valid-json', '')
    }).not.toThrow()

    // State should remain unchanged
    expect(Object.keys(nodeStatuses.value).length).toBe(0)
  })

  // @{"verifies": ["REQ-SYS-073", "REQ-SYS-077"]}
  it('useNodeEvents_OnError_SetsErrorState', async () => {
    const { useNodeEvents } = await import('@/composables/useNodeEvents')
    const { error } = useNodeEvents('/api/v1/courses/c1/events')

    capturedOnError!(new Error('Connection refused'))

    expect(error.value).toBe('Connection refused')
  })

  // @{"verifies": ["REQ-SYS-073", "REQ-SYS-077"]}
  it('useNodeEvents_OnUnmounted_CloseIsRegistered', async () => {
    const { useNodeEvents } = await import('@/composables/useNodeEvents')
    useNodeEvents('/api/v1/courses/c1/events')

    // onUnmounted should have been called with the close function
    expect(mockOnUnmounted).toHaveBeenCalledOnce()
    // The registered cleanup should call sse.close()
    const cleanupFn = mockOnUnmounted.mock.calls[0][0] as () => void
    cleanupFn()
    expect(mockClose).toHaveBeenCalledOnce()
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('useNodeEvents_UnknownEventType_DoesNotUpdateAnyState', async () => {
    const { useNodeEvents } = await import('@/composables/useNodeEvents')
    const { nodeStatuses, layersAwaitingExpand } = useNodeEvents('/api/v1/courses/c1/events')

    // Unknown event type should be silently ignored
    fireEvent('status_change', { status: 'active' })

    expect(Object.keys(nodeStatuses.value).length).toBe(0)
    expect(layersAwaitingExpand.value.size).toBe(0)
  })
})
