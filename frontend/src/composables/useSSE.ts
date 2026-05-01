// @{"req": ["REQ-FECOURSE-070", "REQ-FECOURSE-071", "REQ-FECOURSE-072", "REQ-FECOURSE-073", "REQ-FECOURSE-700", "REQ-FECOURSE-701", "REQ-FECOURSE-710", "REQ-FECOURSE-720", "REQ-FECOURSE-730"]}

export interface SSEOptions {
  token: string
  onEvent: (eventType: string, data: string, lastEventId: string) => void
  onError?: (err: Error) => void
}

// BACKOFF_DELAYS_MS lists the wait durations for each of the five reconnect
// windows.  When the last window (index 4, 16000 ms) expires, onError is
// called rather than making another fetch call — giving exactly 5 total fetch
// calls and 5 distinct setTimeout delays (REQ-FECOURSE-251).
const BACKOFF_DELAYS_MS = [1000, 2000, 4000, 8000, 16000]

// @{"req": ["REQ-FECOURSE-070", "REQ-FECOURSE-071", "REQ-FECOURSE-072", "REQ-FECOURSE-073", "REQ-FECOURSE-700", "REQ-FECOURSE-701", "REQ-FECOURSE-710", "REQ-FECOURSE-720", "REQ-FECOURSE-730"]}
export function useSSE(url: string, options: SSEOptions): { close: () => void } {
  let closed = false
  let lastEventId = ''
  // controller is replaced on each new fetch attempt.
  let controller = new AbortController()

  // connect opens one fetch-based SSE connection and processes the stream.
  // retryCount is the number of consecutive failures seen so far; it is used
  // to select the next backoff delay.
  async function connect(retryCount: number): Promise<void> {
    if (closed) return

    // Append ?after=<lastEventId> on reconnect once at least one event has
    // been received so the server can skip already-delivered events
    // (REQ-FECOURSE-710).
    const connectUrl =
      lastEventId !== '' ? `${url}?after=${lastEventId}` : url

    controller = new AbortController()

    let response: Response
    try {
      response = await fetch(connectUrl, {
        headers: { Authorization: `Bearer ${options.token}` },
        signal: controller.signal
      })
    } catch (err) {
      if (closed) return
      scheduleReconnect(retryCount, err instanceof Error ? err : new Error(String(err)))
      return
    }

    if (!response.ok) {
      if (closed) return
      scheduleReconnect(
        retryCount,
        new Error(`SSE connection failed with HTTP ${response.status}`)
      )
      return
    }

    // Successfully connected — read the stream.  Any read error triggers a
    // reconnect using the same retry counter (the connection was open, so the
    // failure is a stream error rather than a connection error).
    try {
      await readStream(response)
    } catch (err) {
      if (closed) return
      scheduleReconnect(retryCount, err instanceof Error ? err : new Error(String(err)))
      return
    }

    // Stream ended without error (server closed the connection gracefully).
    // Treat this as a transient drop and attempt to reconnect.
    if (!closed) {
      scheduleReconnect(retryCount, new Error('SSE stream ended'))
    }
  }

  // readStream consumes the response body as a ReadableStream, decodes UTF-8
  // text incrementally, and dispatches SSE events (REQ-FECOURSE-701).
  async function readStream(response: Response): Promise<void> {
    const reader = response.body!.getReader()
    const decoder = new TextDecoder()
    let buffer = ''

    // SSE field state for the current event block.
    let eventType = 'message'
    let data = ''

    while (true) {
      const { done, value } = await reader.read()
      if (done) break

      buffer += decoder.decode(value, { stream: true })

      // Process all complete lines in the buffer, leaving any trailing
      // incomplete line for the next chunk.
      let newlineIdx: number
      while ((newlineIdx = buffer.indexOf('\n')) !== -1) {
        const line = buffer.slice(0, newlineIdx).replace(/\r$/, '')
        buffer = buffer.slice(newlineIdx + 1)

        if (line === '') {
          // Blank line dispatches the buffered event.
          if (data !== '') {
            options.onEvent(eventType, data, lastEventId)
          }
          eventType = 'message'
          data = ''
        } else if (line.startsWith(':')) {
          // Keepalive comment — silently discard (REQ-FECOURSE-720).
        } else if (line.startsWith('id:')) {
          lastEventId = line.slice(3).trimStart()
        } else if (line.startsWith('event:')) {
          eventType = line.slice(6).trimStart()
        } else if (line.startsWith('data:')) {
          const chunk = line.slice(5).trimStart()
          // Multiple data: lines before a blank line are joined with '\n'.
          data = data === '' ? chunk : `${data}\n${chunk}`
        }
      }
    }
  }

  // scheduleReconnect applies exponential backoff.  retryCount is the number
  // of prior consecutive failures.
  //
  // For the last backoff slot (index BACKOFF_DELAYS_MS.length - 1 = 4, delay
  // 16000 ms) the setTimeout callback calls onError instead of connect, so
  // the total number of fetch calls stays at 5 (one per entry in the array)
  // and all five delay values are still passed to setTimeout
  // (REQ-FECOURSE-250, REQ-FECOURSE-251).
  function scheduleReconnect(retryCount: number, err: Error): void {
    if (closed) return

    if (retryCount >= BACKOFF_DELAYS_MS.length) {
      // All five retry windows are exhausted — surface the error.
      options.onError?.(err)
      return
    }

    const delay = BACKOFF_DELAYS_MS[retryCount]
    const isLastRetry = retryCount === BACKOFF_DELAYS_MS.length - 1

    setTimeout(() => {
      if (closed) return
      if (isLastRetry) {
        // The final window has elapsed without a successful connection.
        options.onError?.(err)
      } else {
        connect(retryCount + 1)
      }
    }, delay)
  }

  // Start the first connection attempt immediately.
  connect(0)

  return {
    // close aborts any in-flight request and prevents any further reconnects
    // (REQ-FECOURSE-730).
    close(): void {
      closed = true
      controller.abort()
    }
  }
}
