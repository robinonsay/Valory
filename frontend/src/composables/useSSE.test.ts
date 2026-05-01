import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useSSE } from './useSSE'

// makeStream builds a ReadableStream that yields each provided chunk string as
// a Uint8Array then closes.  This lets tests provide raw SSE wire bytes without
// constructing real network responses.
function makeStream(chunks: string[]): ReadableStream<Uint8Array> {
  const encoder = new TextEncoder()
  let i = 0
  return new ReadableStream<Uint8Array>({
    pull(controller) {
      if (i < chunks.length) {
        controller.enqueue(encoder.encode(chunks[i++]))
      } else {
        controller.close()
      }
    }
  })
}

// makeResponse builds a minimal fetch-compatible Response whose body is a
// ReadableStream constructed from the given SSE chunks.
function makeResponse(chunks: string[]): Response {
  return {
    ok: true,
    status: 200,
    body: makeStream(chunks)
  } as unknown as Response
}

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

// @{"req": ["REQ-FECOURSE-070", "REQ-FECOURSE-071", "REQ-FECOURSE-072", "REQ-FECOURSE-073", "REQ-FECOURSE-700", "REQ-FECOURSE-701", "REQ-FECOURSE-710", "REQ-FECOURSE-720", "REQ-FECOURSE-730"]}
describe('useSSE — basic event dispatch', () => {
  it('calls onEvent with the correct type, data, and lastEventId for a complete SSE message', async () => {
    const mockFetch = vi.fn().mockResolvedValue(
      makeResponse(['id: 1\nevent: chat\ndata: hello\n\n'])
    )
    vi.stubGlobal('fetch', mockFetch)

    const onEvent = vi.fn()
    useSSE('/events', { token: 'tok', onEvent })

    // Allow the fetch and stream reading microtasks to settle.
    await vi.runAllTimersAsync()

    expect(onEvent).toHaveBeenCalledOnce()
    expect(onEvent).toHaveBeenCalledWith('chat', 'hello', '1')
  })
})

// @{"req": ["REQ-FECOURSE-070", "REQ-FECOURSE-071", "REQ-FECOURSE-072", "REQ-FECOURSE-073", "REQ-FECOURSE-700", "REQ-FECOURSE-701", "REQ-FECOURSE-710", "REQ-FECOURSE-720", "REQ-FECOURSE-730"]}
describe('useSSE — keepalive skip', () => {
  it('does not call onEvent for a line starting with a colon', async () => {
    const mockFetch = vi.fn().mockResolvedValue(
      makeResponse([': keepalive\n'])
    )
    vi.stubGlobal('fetch', mockFetch)

    const onEvent = vi.fn()
    useSSE('/events', { token: 'tok', onEvent })

    await vi.runAllTimersAsync()

    expect(onEvent).not.toHaveBeenCalled()
  })
})

// @{"req": ["REQ-FECOURSE-070", "REQ-FECOURSE-071", "REQ-FECOURSE-072", "REQ-FECOURSE-073", "REQ-FECOURSE-700", "REQ-FECOURSE-701", "REQ-FECOURSE-710", "REQ-FECOURSE-720", "REQ-FECOURSE-730"]}
describe('useSSE — default event type', () => {
  it('dispatches with type "message" when no event: field is present', async () => {
    const mockFetch = vi.fn().mockResolvedValue(
      makeResponse(['data: payload\n\n'])
    )
    vi.stubGlobal('fetch', mockFetch)

    const onEvent = vi.fn()
    useSSE('/events', { token: 'tok', onEvent })

    await vi.runAllTimersAsync()

    expect(onEvent).toHaveBeenCalledOnce()
    expect(onEvent).toHaveBeenCalledWith('message', 'payload', '')
  })
})

// @{"req": ["REQ-FECOURSE-070", "REQ-FECOURSE-071", "REQ-FECOURSE-072", "REQ-FECOURSE-073", "REQ-FECOURSE-700", "REQ-FECOURSE-701", "REQ-FECOURSE-710", "REQ-FECOURSE-720", "REQ-FECOURSE-730"]}
describe('useSSE — multi-line data', () => {
  it('joins multiple data: lines with "\\n" before dispatching', async () => {
    const mockFetch = vi.fn().mockResolvedValue(
      makeResponse(['data: line1\ndata: line2\n\n'])
    )
    vi.stubGlobal('fetch', mockFetch)

    const onEvent = vi.fn()
    useSSE('/events', { token: 'tok', onEvent })

    await vi.runAllTimersAsync()

    expect(onEvent).toHaveBeenCalledOnce()
    expect(onEvent).toHaveBeenCalledWith('message', 'line1\nline2', '')
  })
})

// @{"req": ["REQ-FECOURSE-070", "REQ-FECOURSE-071", "REQ-FECOURSE-072", "REQ-FECOURSE-073", "REQ-FECOURSE-700", "REQ-FECOURSE-701", "REQ-FECOURSE-710", "REQ-FECOURSE-720", "REQ-FECOURSE-730"]}
describe('useSSE — lastEventId tracking', () => {
  it('appends ?after=<lastEventId> on the reconnect URL after receiving an id field', async () => {
    // First call delivers one event with id=42, then the stream closes to
    // trigger a reconnect.  The second call resolves with an open stream that
    // never closes so the test can inspect the URL before further retries.
    const hangingStream = new ReadableStream<Uint8Array>({ pull() {} })
    const mockFetch = vi
      .fn()
      .mockResolvedValueOnce(makeResponse(['id: 42\ndata: x\n\n']))
      .mockResolvedValue({ ok: true, status: 200, body: hangingStream } as unknown as Response)

    vi.stubGlobal('fetch', mockFetch)

    const onEvent = vi.fn()
    useSSE('/events', { token: 'tok', onEvent })

    // First connection completes (stream closes after delivering the event).
    await vi.runAllTimersAsync()

    // At this point the reconnect has been scheduled with delay 1000ms.
    // Advance the timer and let the second fetch settle.
    await vi.runAllTimersAsync()

    // The second fetch call must carry ?after=42.
    expect(mockFetch).toHaveBeenCalledTimes(2)
    const secondUrl = mockFetch.mock.calls[1][0] as string
    expect(secondUrl).toBe('/events?after=42')
  })
})

// @{"req": ["REQ-FECOURSE-070", "REQ-FECOURSE-071", "REQ-FECOURSE-072", "REQ-FECOURSE-073", "REQ-FECOURSE-700", "REQ-FECOURSE-701", "REQ-FECOURSE-710", "REQ-FECOURSE-720", "REQ-FECOURSE-730"]}
describe('useSSE — backoff sequence', () => {
  it('schedules setTimeout with 1000, 2000, 4000, 8000, 16000 ms for consecutive failures', async () => {
    // Every fetch call rejects with a network error so all five retries fire.
    const networkErr = new Error('network failure')
    const mockFetch = vi.fn().mockRejectedValue(networkErr)
    vi.stubGlobal('fetch', mockFetch)

    const setTimeoutSpy = vi.spyOn(globalThis, 'setTimeout')

    useSSE('/events', { token: 'tok', onEvent: vi.fn(), onError: vi.fn() })

    // Run all pending timers and microtasks across all five retry rounds.
    for (let i = 0; i < 6; i++) {
      await vi.runAllTimersAsync()
    }

    // Collect only the delays passed to our backoff setTimeout calls.
    // vi.useFakeTimers may also install timers for internal use; we filter by
    // the known backoff values to avoid false positives.
    const backoffDelays = setTimeoutSpy.mock.calls
      .map(args => args[1] as number)
      .filter(d => [1000, 2000, 4000, 8000, 16000].includes(d))

    expect(backoffDelays).toEqual([1000, 2000, 4000, 8000, 16000])
  })
})

// @{"req": ["REQ-FECOURSE-070", "REQ-FECOURSE-071", "REQ-FECOURSE-072", "REQ-FECOURSE-073", "REQ-FECOURSE-700", "REQ-FECOURSE-701", "REQ-FECOURSE-710", "REQ-FECOURSE-720", "REQ-FECOURSE-730"]}
describe('useSSE — stop after max retries', () => {
  it('calls onError after 5 failures and does not make a 6th fetch call', async () => {
    const networkErr = new Error('network failure')
    const mockFetch = vi.fn().mockRejectedValue(networkErr)
    vi.stubGlobal('fetch', mockFetch)

    const onError = vi.fn()
    useSSE('/events', { token: 'tok', onEvent: vi.fn(), onError })

    // Drain all timers across 6 rounds (initial + 5 retries).
    for (let i = 0; i < 6; i++) {
      await vi.runAllTimersAsync()
    }

    // fetch called exactly 5 times (initial attempt + attempts 1-4, with
    // attempt 5 exhausting the backoff array and calling onError instead).
    expect(mockFetch).toHaveBeenCalledTimes(5)
    expect(onError).toHaveBeenCalledOnce()
    expect(onError).toHaveBeenCalledWith(networkErr)
  })
})

// @{"req": ["REQ-FECOURSE-070", "REQ-FECOURSE-071", "REQ-FECOURSE-072", "REQ-FECOURSE-073", "REQ-FECOURSE-700", "REQ-FECOURSE-701", "REQ-FECOURSE-710", "REQ-FECOURSE-720", "REQ-FECOURSE-730"]}
describe('useSSE — close() prevents reconnect', () => {
  it('does not make a second fetch call after close() is called', async () => {
    // The mock delivers a stream that closes immediately, which would normally
    // trigger a reconnect after 1000 ms.  We call close() synchronously after
    // the initial connect so the closed flag is set before any timer fires.
    const mockFetch = vi.fn().mockResolvedValue(makeResponse([]))
    vi.stubGlobal('fetch', mockFetch)

    // close() is obtained before any async work; it sets the closed flag which
    // will be read by the backoff callback even if timers fire afterwards.
    const { close } = useSSE('/events', { token: 'tok', onEvent: vi.fn() })

    // Drain the microtask queue so the initial fetch and stream reading settle,
    // which schedules the 1000 ms reconnect timer.  We use multiple
    // Promise.resolve() passes rather than runAllTimersAsync so that pending
    // timers are not advanced yet.
    await Promise.resolve()
    await Promise.resolve()
    await Promise.resolve()
    await Promise.resolve()
    await Promise.resolve()

    // Call close() so the reconnect guard is set before the timer fires.
    close()

    // Advance past the 1000 ms reconnect window and let any new microtasks
    // settle.  The timer callback checks the closed flag and returns early.
    await vi.runAllTimersAsync()

    // Only the initial call should have been made.
    expect(mockFetch).toHaveBeenCalledTimes(1)
  })
})
