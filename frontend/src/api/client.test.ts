import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  request,
  get,
  post,
  patch,
  put,
  del,
  setUnauthorizedHandler,
  ApiError
} from './client'

// Helper that builds a minimal fetch-compatible Response.
function makeResponse(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body)
  } as unknown as Response
}

beforeEach(() => {
  // Reset the unauthorizedHandler between tests so state does not leak.
  setUnauthorizedHandler(null)
  vi.restoreAllMocks()
})

afterEach(() => {
  vi.restoreAllMocks()
})

// @{"req": ["REQ-FEAUTH-045", "REQ-FEAUTH-046", "REQ-FEAUTH-157"]}
describe('GET request', () => {
  it('attaches Authorization header and does NOT attach X-CSRF-Token', async () => {
    const mockFetch = vi.fn().mockResolvedValue(makeResponse(200, { ok: true }))
    vi.stubGlobal('fetch', mockFetch)

    await get('/api/test', 'tok-abc')

    const [, init] = mockFetch.mock.calls[0] as [string, RequestInit]
    const headers = init.headers as Record<string, string>

    expect(headers['Authorization']).toBe('Bearer tok-abc')
    expect(headers['X-CSRF-Token']).toBeUndefined()
  })
})

// @{"req": ["REQ-FEAUTH-047", "REQ-FEAUTH-048", "REQ-FEAUTH-049"]}
describe('POST request — CSRF', () => {
  it('attaches both Authorization and X-CSRF-Token read from the __Host-csrf cookie', async () => {
    Object.defineProperty(document, 'cookie', {
      configurable: true,
      get: () => '__Host-csrf=secret-token-1; other=val'
    })

    const mockFetch = vi.fn().mockResolvedValue(makeResponse(200, {}))
    vi.stubGlobal('fetch', mockFetch)

    await post('/api/data', { x: 1 }, 'tok-xyz')

    const [, init] = mockFetch.mock.calls[0] as [string, RequestInit]
    const headers = init.headers as Record<string, string>

    expect(headers['Authorization']).toBe('Bearer tok-xyz')
    expect(headers['X-CSRF-Token']).toBe('secret-token-1')
  })
})

// @{"req": ["REQ-FEAUTH-047", "REQ-FEAUTH-048", "REQ-FEAUTH-049"]}
describe('CSRF cookie re-read', () => {
  it('reads the current document.cookie value on each request rather than a cached one', async () => {
    let cookieValue = '__Host-csrf=first-token'
    Object.defineProperty(document, 'cookie', {
      configurable: true,
      get: () => cookieValue
    })

    const mockFetch = vi.fn().mockResolvedValue(makeResponse(200, {}))
    vi.stubGlobal('fetch', mockFetch)

    await post('/api/a', {}, 'tok')

    // Rotate the cookie before the second call.
    cookieValue = '__Host-csrf=second-token'

    await post('/api/b', {}, 'tok')

    const headers1 = (mockFetch.mock.calls[0] as [string, RequestInit])[1]
      .headers as Record<string, string>
    const headers2 = (mockFetch.mock.calls[1] as [string, RequestInit])[1]
      .headers as Record<string, string>

    expect(headers1['X-CSRF-Token']).toBe('first-token')
    expect(headers2['X-CSRF-Token']).toBe('second-token')
  })
})

// @{"req": ["REQ-FEAUTH-155", "REQ-FEAUTH-156"]}
describe('401 handling', () => {
  it('fires the registered unauthorizedHandler callback when a 401 is received', async () => {
    const mockFetch = vi.fn().mockResolvedValue(makeResponse(401, { error: 'unauthorized' }))
    vi.stubGlobal('fetch', mockFetch)

    const handler = vi.fn()
    setUnauthorizedHandler(handler)

    await expect(get('/api/protected', 'bad-tok')).rejects.toBeInstanceOf(ApiError)
    expect(handler).toHaveBeenCalledOnce()
  })

  it('fires ApiError (not handler) when no unauthorizedHandler is registered', async () => {
    setUnauthorizedHandler(null)

    const mockFetch = vi.fn().mockResolvedValue(makeResponse(401, { error: 'unauthorized' }))
    vi.stubGlobal('fetch', mockFetch)

    const err = await request('GET', '/api/protected', { token: 'bad-tok' }).catch(e => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(401)
  })
})

// @{"req": ["REQ-FEAUTH-155", "REQ-FEAUTH-156"]}
describe('ApiError on non-2xx', () => {
  it('throws an ApiError with the correct status when fetch returns 400', async () => {
    const mockFetch = vi.fn().mockResolvedValue(makeResponse(400, { error: 'bad request' }))
    vi.stubGlobal('fetch', mockFetch)

    try {
      await get('/api/bad', 'tok')
      expect.fail('expected ApiError to be thrown')
    } catch (err) {
      expect(err).toBeInstanceOf(ApiError)
      expect((err as ApiError).status).toBe(400)
    }
  })
})

// @{"req": ["REQ-FEAUTH-045", "REQ-FEAUTH-046", "REQ-FEAUTH-047", "REQ-FEAUTH-048", "REQ-FEAUTH-049", "REQ-FEAUTH-157"]}
describe('Convenience helpers', () => {
  beforeEach(() => {
    Object.defineProperty(document, 'cookie', {
      configurable: true,
      get: () => '__Host-csrf=csrf-val'
    })
  })

  it('get calls request with GET', async () => {
    const mockFetch = vi.fn().mockResolvedValue(makeResponse(200, {}))
    vi.stubGlobal('fetch', mockFetch)

    await get('/api/x', 'tok')

    const [, init] = mockFetch.mock.calls[0] as [string, RequestInit]
    expect(init.method).toBe('GET')
  })

  it('post calls request with POST', async () => {
    const mockFetch = vi.fn().mockResolvedValue(makeResponse(200, {}))
    vi.stubGlobal('fetch', mockFetch)

    await post('/api/x', { a: 1 }, 'tok')

    const [, init] = mockFetch.mock.calls[0] as [string, RequestInit]
    expect(init.method).toBe('POST')
  })

  it('patch calls request with PATCH', async () => {
    const mockFetch = vi.fn().mockResolvedValue(makeResponse(200, {}))
    vi.stubGlobal('fetch', mockFetch)

    await patch('/api/x', { a: 1 }, 'tok')

    const [, init] = mockFetch.mock.calls[0] as [string, RequestInit]
    expect(init.method).toBe('PATCH')
  })

  // @{"req": ["REQ-FEAUTH-045", "REQ-FEAUTH-046", "REQ-FEAUTH-047", "REQ-FEAUTH-048", "REQ-FEAUTH-049", "REQ-FEAUTH-157"]}
  it('put calls request with PUT', async () => {
    const mockFetch = vi.fn().mockResolvedValue(makeResponse(200, {}))
    vi.stubGlobal('fetch', mockFetch)

    await put('/api/x', { a: 1 }, 'tok')

    const [, init] = mockFetch.mock.calls[0] as [string, RequestInit]
    expect(init.method).toBe('PUT')
  })

  it('del calls request with DELETE', async () => {
    const mockFetch = vi.fn().mockResolvedValue(makeResponse(200, {}))
    vi.stubGlobal('fetch', mockFetch)

    await del('/api/x', 'tok')

    const [, init] = mockFetch.mock.calls[0] as [string, RequestInit]
    expect(init.method).toBe('DELETE')
  })
})
