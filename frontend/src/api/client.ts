// @{"req": ["REQ-FEAUTH-045", "REQ-FEAUTH-046", "REQ-FEAUTH-047", "REQ-FEAUTH-048", "REQ-FEAUTH-049", "REQ-FEAUTH-155", "REQ-FEAUTH-156", "REQ-FEAUTH-157"]}

// ApiError is thrown for any non-2xx HTTP response so callers can distinguish
// network/application errors from successful responses.
export class ApiError extends Error {
  status: number
  body: unknown

  constructor(status: number, body: unknown) {
    super(`HTTP ${status}`)
    this.name = 'ApiError'
    this.status = status
    this.body = body
  }
}

// unauthorizedHandler is invoked whenever a 401 response is received so the
// router/store layer can clear session state and redirect without this module
// needing to know about Vue internals.
let unauthorizedHandler: (() => void) | null = null

// @{"req": ["REQ-FEAUTH-155", "REQ-FEAUTH-156", "REQ-FEAUTH-157"]}
export function setUnauthorizedHandler(cb: (() => void) | null): void {
  unauthorizedHandler = cb
}

// getCsrfToken reads __Host-csrf from document.cookie on every call so that a
// freshly-rotated cookie value is always used (REQ-FEAUTH-047).
function getCsrfToken(): string | undefined {
  return document.cookie
    .split(';')
    .map(c => c.trim())
    .find(c => c.startsWith('__Host-csrf='))
    ?.split('=')[1]
}

const MUTATING_METHODS = new Set(['POST', 'PATCH', 'PUT', 'DELETE'])

// @{"req": ["REQ-FEAUTH-045", "REQ-FEAUTH-046", "REQ-FEAUTH-047", "REQ-FEAUTH-048", "REQ-FEAUTH-049", "REQ-FEAUTH-155", "REQ-FEAUTH-156", "REQ-FEAUTH-157"]}
export async function request<T>(
  method: 'GET' | 'POST' | 'PATCH' | 'PUT' | 'DELETE',
  path: string,
  options?: {
    token?: string | null
    body?: unknown
    headers?: Record<string, string>
  }
): Promise<T> {
  const headers: Record<string, string> = { ...options?.headers }

  // REQ-FEAUTH-045, REQ-FEAUTH-046: attach bearer token when provided.
  if (options?.token) {
    headers['Authorization'] = `Bearer ${options.token}`
  }

  // REQ-FEAUTH-047, REQ-FEAUTH-048, REQ-FEAUTH-049: read CSRF cookie fresh on
  // every mutating request so a rotated cookie value is always picked up.
  if (MUTATING_METHODS.has(method)) {
    const csrf = getCsrfToken()
    if (csrf !== undefined) {
      headers['X-CSRF-Token'] = csrf
    }
  }

  let bodyStr: string | undefined
  if (options?.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    bodyStr = JSON.stringify(options.body)
  }

  const response = await fetch(path, {
    method,
    headers,
    body: bodyStr
  })

  // REQ-FEAUTH-155, REQ-FEAUTH-156: fire the registered callback before
  // throwing so the store/router can clear auth state synchronously.
  // Clear the handler before calling it so that concurrent 401 responses
  // only trigger the redirect once (idempotent clear).
  if (response.status === 401) {
    if (unauthorizedHandler !== null) {
      const h = unauthorizedHandler
      unauthorizedHandler = null
      h()
    }
    const errBody = await response.json().catch(() => null)
    throw new ApiError(response.status, errBody)
  }

  if (!response.ok) {
    const errBody = await response.json().catch(() => null)
    throw new ApiError(response.status, errBody)
  }

  return response.json() as Promise<T>
}

// @{"req": ["REQ-FEAUTH-045", "REQ-FEAUTH-046", "REQ-FEAUTH-157"]}
export function get<T>(path: string, token?: string | null): Promise<T> {
  return request<T>('GET', path, { token })
}

// @{"req": ["REQ-FEAUTH-045", "REQ-FEAUTH-046", "REQ-FEAUTH-047", "REQ-FEAUTH-048", "REQ-FEAUTH-049", "REQ-FEAUTH-157"]}
export function post<T>(path: string, body: unknown, token?: string | null): Promise<T> {
  return request<T>('POST', path, { body, token })
}

// @{"req": ["REQ-FEAUTH-045", "REQ-FEAUTH-046", "REQ-FEAUTH-047", "REQ-FEAUTH-048", "REQ-FEAUTH-049", "REQ-FEAUTH-157"]}
export function patch<T>(path: string, body: unknown, token?: string | null): Promise<T> {
  return request<T>('PATCH', path, { body, token })
}

// @{"req": ["REQ-FEAUTH-045", "REQ-FEAUTH-046", "REQ-FEAUTH-047", "REQ-FEAUTH-048", "REQ-FEAUTH-049", "REQ-FEAUTH-157"]}
export function put<T>(path: string, body: unknown, token?: string | null): Promise<T> {
  return request<T>('PUT', path, { body, token })
}

// @{"req": ["REQ-FEAUTH-045", "REQ-FEAUTH-046", "REQ-FEAUTH-047", "REQ-FEAUTH-048", "REQ-FEAUTH-049", "REQ-FEAUTH-157"]}
export function del<T>(path: string, token?: string | null): Promise<T> {
  return request<T>('DELETE', path, { token })
}
