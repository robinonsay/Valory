// @{"req": ["REQ-FEAUTH-001", "REQ-FEAUTH-010", "REQ-FEAUTH-011", "REQ-FEAUTH-019", "REQ-FEAUTH-020", "REQ-FEAUTH-040", "REQ-FEAUTH-041", "REQ-FEAUTH-042", "REQ-FEAUTH-045", "REQ-FEAUTH-118", "REQ-FEAUTH-119", "REQ-FEAUTH-155", "REQ-FEAUTH-156", "REQ-FEAUTH-169", "REQ-FEAUTH-170", "REQ-FEAUTH-171"]}
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useAuthStore } from './auth'
import * as clientModule from '@/api/client'

// seedStore is a helper that populates auth state by resolving restoreSession
// with a mock GET /auth/session response, mirroring the real boot flow.
async function seedStore(
  store: ReturnType<typeof useAuthStore>,
  overrides: {
    userId?: string
    username?: string
    role?: 'student' | 'admin'
    consented?: boolean
    expiresAt?: number
  } = {}
) {
  const future = Math.floor(Date.now() / 1000) + 3600
  vi.spyOn(clientModule, 'get').mockResolvedValueOnce({
    user_id: overrides.userId ?? 'user-uuid-1',
    username: overrides.username ?? 'testuser',
    role: overrides.role ?? 'student',
    consented: overrides.consented ?? false,
    expires_at: new Date((overrides.expiresAt ?? future) * 1000).toISOString()
  })
  await store.restoreSession()
}

describe('useAuthStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('should initialize with empty state and restoreDone=false', () => {
    const store = useAuthStore()
    expect(store.userId).toBeNull()
    expect(store.username).toBeNull()
    expect(store.role).toBeNull()
    expect(store.expiresAt).toBeNull()
    expect(store.isConsented).toBe(false)
    expect(store.restoreDone).toBe(false)
  })

  // @{"verifies": ["REQ-FEAUTH-169"]}
  // Regression: the boot-time restore (401, logged-out visitor) memoizes
  // restorePromise; login() must force a FRESH restore or the user is stuck
  // on /login with a valid session cookie. Caught live by the e2e tier.
  it('login after a failed boot restore performs a fresh session fetch', async () => {
    const store = useAuthStore()
    // Boot: restore 401s (logged out)
    vi.spyOn(clientModule, 'get').mockRejectedValueOnce(new clientModule.ApiError(401, {}))
    await store.restoreSession()
    expect(store.isAuthenticated).toBe(false)
    // Login succeeds server-side; login() must refetch the session
    vi.spyOn(clientModule, 'get').mockResolvedValueOnce({
      user_id: 'u1', username: 'demo_student', role: 'student',
      consented: true, expires_at: new Date(Date.now() + 3600_000).toISOString()
    })
    await store.login()
    expect(store.isAuthenticated).toBe(true)
    expect(store.role).toBe('student')
  })

  // REQ-FEAUTH-169: restoreSession populates state on 200
  it('restoreSession: populates state from GET /auth/session on 200', async () => {
    const store = useAuthStore()
    const future = Math.floor(Date.now() / 1000) + 3600

    vi.spyOn(clientModule, 'get').mockResolvedValueOnce({
      user_id: 'abc-123',
      username: 'alice',
      role: 'student',
      consented: true,
      expires_at: new Date(future * 1000).toISOString()
    })

    await store.restoreSession()

    expect(store.userId).toBe('abc-123')
    expect(store.username).toBe('alice')
    expect(store.role).toBe('student')
    expect(store.isConsented).toBe(true)
    expect(store.isAuthenticated).toBe(true)
    expect(store.restoreDone).toBe(true)
  })

  // REQ-FEAUTH-169: restoreSession clears state on 401
  it('restoreSession: clears state on 401 and sets restoreDone=true', async () => {
    const store = useAuthStore()

    vi.spyOn(clientModule, 'get').mockRejectedValueOnce(
      new clientModule.ApiError(401, { error: 'unauthorized' })
    )

    await store.restoreSession()

    expect(store.userId).toBeNull()
    expect(store.username).toBeNull()
    expect(store.role).toBeNull()
    expect(store.isAuthenticated).toBe(false)
    expect(store.restoreDone).toBe(true)
  })

  // REQ-FEAUTH-170: restoreDone starts false and is set to true after the call
  it('restoreDone transitions false → true after restoreSession completes', async () => {
    const store = useAuthStore()
    expect(store.restoreDone).toBe(false)

    vi.spyOn(clientModule, 'get').mockRejectedValueOnce(
      new clientModule.ApiError(401, { error: 'unauthorized' })
    )

    await store.restoreSession()
    expect(store.restoreDone).toBe(true)
  })

  it('should clear all state on logout', async () => {
    const store = useAuthStore()
    await seedStore(store, { role: 'admin' })
    store.setConsented()

    expect(store.role).not.toBeNull()
    expect(store.isConsented).toBe(true)

    store.logout()

    expect(store.userId).toBeNull()
    expect(store.username).toBeNull()
    expect(store.role).toBeNull()
    expect(store.expiresAt).toBeNull()
    expect(store.isConsented).toBe(false)
  })

  it('should set isConsented to true without changing other state', async () => {
    const store = useAuthStore()
    await seedStore(store, { role: 'student', consented: false })

    store.setConsented()

    expect(store.isConsented).toBe(true)
    expect(store.role).toBe('student')
  })

  it('isAuthenticated getter should be false when role is null', () => {
    const store = useAuthStore()
    expect(store.isAuthenticated).toBe(false)
  })

  it('isAuthenticated getter should be true after restoreSession succeeds', async () => {
    const store = useAuthStore()
    await seedStore(store)
    expect(store.isAuthenticated).toBe(true)
  })

  it('isStudent getter should return true only for student role', async () => {
    const store = useAuthStore()
    await seedStore(store, { role: 'student' })
    expect(store.isStudent).toBe(true)
    expect(store.isAdmin).toBe(false)

    store.logout()
    expect(store.isStudent).toBe(false)
  })

  it('isAdmin getter should return true only for admin role', async () => {
    const store = useAuthStore()
    await seedStore(store, { role: 'admin' })
    expect(store.isAdmin).toBe(true)
    expect(store.isStudent).toBe(false)

    store.logout()
    expect(store.isAdmin).toBe(false)
  })

  it('isExpired getter should return false when expiresAt is null', () => {
    const store = useAuthStore()
    expect(store.isExpired).toBe(false)
  })

  it('isExpired getter should return false when expiresAt is in the future', async () => {
    const store = useAuthStore()
    await seedStore(store, { expiresAt: Math.floor(Date.now() / 1000) + 3600 })
    expect(store.isExpired).toBe(false)
  })

  it('isExpired getter should return true when expiresAt is in the past', async () => {
    const store = useAuthStore()
    const past = Math.floor(Date.now() / 1000) - 1
    await seedStore(store, { expiresAt: past })
    expect(store.isExpired).toBe(true)
  })

  it('registerUnauthorizedHandler should register a handler that calls logout', async () => {
    const store = useAuthStore()
    await seedStore(store)
    const setSpy = vi.spyOn(clientModule, 'setUnauthorizedHandler')

    store.registerUnauthorizedHandler()

    expect(setSpy).toHaveBeenCalledWith(expect.any(Function))

    const handler = setSpy.mock.calls[0][0] as () => void
    handler()

    expect(store.role).toBeNull()
    expect(store.isConsented).toBe(false)

    setSpy.mockRestore()
  })

  it('logout should call setUnauthorizedHandler(null)', async () => {
    const store = useAuthStore()
    await seedStore(store)
    const setSpy = vi.spyOn(clientModule, 'setUnauthorizedHandler')

    store.logout()

    expect(setSpy).toHaveBeenCalledWith(null)

    setSpy.mockRestore()
  })

  // REQ-FEAUTH-171: no token in localStorage/sessionStorage on login
  it('should not write to localStorage after login/restoreSession', async () => {
    const store = useAuthStore()
    const setItemSpy = vi.spyOn(Storage.prototype, 'setItem')

    await seedStore(store)

    expect(setItemSpy).not.toHaveBeenCalled()
    setItemSpy.mockRestore()
  })

  // REQ-FEAUTH-171: no raw token in store state after login
  it('store has no token field after restoreSession — token is cookie-only', async () => {
    const store = useAuthStore()
    await seedStore(store)
    // The store must not expose a raw token string.
    expect((store as unknown as Record<string, unknown>)['token']).toBeUndefined()
  })

  // logoutServer tests — REQ-FEAUTH-019, REQ-FEAUTH-020, REQ-FEAUTH-118, REQ-FEAUTH-119
  describe('logoutServer', () => {
    // @{"req": ["REQ-FEAUTH-019", "REQ-FEAUTH-020", "REQ-FEAUTH-118", "REQ-FEAUTH-119"]}
    it('POSTs to /api/v1/auth/logout without a token (cookie path) then clears local state', async () => {
      const store = useAuthStore()
      await seedStore(store, { role: 'student' })
      store.setConsented()

      const postSpy = vi.spyOn(clientModule, 'post').mockResolvedValue(undefined)

      await store.logoutServer()

      // Called with no token argument — browser sends the cookie automatically.
      expect(postSpy).toHaveBeenCalledWith('/api/v1/auth/logout', {})
      expect(store.role).toBeNull()
      expect(store.isConsented).toBe(false)

      postSpy.mockRestore()
    })

    // @{"req": ["REQ-FEAUTH-019", "REQ-FEAUTH-020"]}
    it('clears local state even when the server POST fails', async () => {
      const store = useAuthStore()
      await seedStore(store, { role: 'student' })
      store.setConsented()

      const postSpy = vi.spyOn(clientModule, 'post').mockRejectedValue(new Error('Network error'))

      await store.logoutServer()

      expect(store.role).toBeNull()
      expect(store.isConsented).toBe(false)

      postSpy.mockRestore()
    })

    // @{"req": ["REQ-FEAUTH-020"]}
    it('still POSTs even when unauthenticated (cookie may still be present)', async () => {
      const store = useAuthStore()
      // Do not seed — role is null. Cookie may still be in the browser, so the
      // POST is still sent to ensure the server clears its session.
      const postSpy = vi.spyOn(clientModule, 'post').mockResolvedValue(undefined)

      await store.logoutServer()

      // POST is sent unconditionally so any residual server session is revoked.
      expect(postSpy).toHaveBeenCalledWith('/api/v1/auth/logout', {})

      postSpy.mockRestore()
    })
  })
})
