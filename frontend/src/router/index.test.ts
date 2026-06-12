// @{"req": ["REQ-FEAUTH-040", "REQ-FEAUTH-041", "REQ-FEAUTH-042", "REQ-FEAUTH-043", "REQ-FEAUTH-148", "REQ-FEAUTH-150", "REQ-FEADMIN-014", "REQ-FEADMIN-015", "REQ-FEONBOARD-001", "REQ-FEAUTH-170"]}
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useAuthStore } from '@/stores/auth'
import { guardFn } from '@/router/index'
import router from '@/router/index'
import type { RouteLocationNormalized } from 'vue-router'
import * as clientModule from '@/api/client'

// resolvedPromise is a pre-resolved Promise<void> for use in guard tests where
// restore is already done.
const resolvedPromise = Promise.resolve()

// Helper to build a minimal RouteLocationNormalized suitable for guardFn.
// Only path and meta are read by the guard so the remaining fields are stubbed.
function makeRoute(path: string, meta: { requiresAuth?: boolean; requiredRole?: 'student' | 'admin' } = {}): RouteLocationNormalized {
  return {
    path,
    meta,
    name: undefined,
    params: {},
    query: {},
    hash: '',
    fullPath: path,
    matched: [],
    redirectedFrom: undefined
  } as unknown as RouteLocationNormalized
}

// makeAuth builds the auth param for guardFn with sensible defaults.
// restorePromise defaults to a pre-resolved promise (restore already done).
// Pass restorePromise: null to simulate no restore started yet.
function makeAuth(overrides: {
  restorePromise?: Promise<void> | null
  isAuthenticated?: boolean
  isAdmin?: boolean
  isStudent?: boolean
  isConsented?: boolean
  isExpired?: boolean
  logout?: () => void
} = {}) {
  return {
    restorePromise: 'restorePromise' in overrides ? overrides.restorePromise : resolvedPromise,
    isAuthenticated: overrides.isAuthenticated ?? false,
    isAdmin: overrides.isAdmin ?? false,
    isStudent: overrides.isStudent ?? false,
    isConsented: overrides.isConsented ?? false,
    isExpired: overrides.isExpired ?? false,
    logout: overrides.logout ?? (() => {})
  }
}

describe('router beforeEach guard', () => {
  let auth: ReturnType<typeof useAuthStore>

  beforeEach(() => {
    setActivePinia(createPinia())
    auth = useAuthStore()
    vi.restoreAllMocks()
  })

  // REQ-FEAUTH-170: guard awaits restorePromise before deciding. When
  // restorePromise is null (not yet started), the guard proceeds immediately
  // with current state (unauthenticated → /login for protected routes).
  it('with null restorePromise, navigating to protected route resolves to /login', async () => {
    const result = await guardFn(
      makeRoute('/courses', { requiresAuth: true, requiredRole: 'student' }),
      makeAuth({ restorePromise: null, isAuthenticated: false })
    )
    expect(result).toBe('/login')
  })

  it('with null restorePromise, navigating to /login resolves to undefined (allowed)', async () => {
    const result = await guardFn(
      makeRoute('/login', {}),
      makeAuth({ restorePromise: null, isAuthenticated: false })
    )
    expect(result).toBeUndefined()
  })

  // Guard awaits a pending promise — unauthenticated cold navigation to
  // protected route blocks until restore resolves then redirects to /login
  // with NO intermediate component mount.
  it('awaits restore promise and then applies rules correctly', async () => {
    let resolveRestore!: () => void
    const pendingRestore = new Promise<void>(res => { resolveRestore = res })
    // Schedule the resolution after the guard starts awaiting.
    setTimeout(resolveRestore, 0)
    const result = await guardFn(
      makeRoute('/courses', { requiresAuth: true, requiredRole: 'student' }),
      makeAuth({ restorePromise: pendingRestore, isAuthenticated: false })
    )
    // After restore completes, unauthenticated user must be redirected to /login.
    expect(result).toBe('/login')
  })

  // After restore completes, existing guard rules apply.

  // 1. Unauthenticated accessing protected route → redirected to /login
  it('redirects unauthenticated user from protected route to /login', async () => {
    const result = await guardFn(
      makeRoute('/courses', { requiresAuth: true, requiredRole: 'student' }),
      makeAuth({ isAuthenticated: false })
    )
    expect(result).toBe('/login')
  })

  // 2. Expired token accessing protected route → logout() called, redirect /login
  it('calls logout and redirects to /login when token is expired', async () => {
    const logoutSpy = vi.fn()
    const result = await guardFn(
      makeRoute('/courses', { requiresAuth: true, requiredRole: 'student' }),
      makeAuth({
        isAuthenticated: true,
        isStudent: true,
        isConsented: true,
        isExpired: true,
        logout: logoutSpy
      })
    )
    expect(logoutSpy).toHaveBeenCalledOnce()
    expect(result).toBe('/login')
  })

  // 3. Authenticated admin navigating to /login → redirected to /admin/users
  it('redirects authenticated admin away from /login to /admin/users', async () => {
    const result = await guardFn(
      makeRoute('/login', {}),
      makeAuth({ isAuthenticated: true, isAdmin: true, isConsented: true })
    )
    expect(result).toBe('/admin/users')
  })

  // 4. Authenticated student navigating to /login → redirected to /courses
  it('redirects authenticated student away from /login to /courses', async () => {
    const result = await guardFn(
      makeRoute('/login', {}),
      makeAuth({ isAuthenticated: true, isStudent: true, isConsented: true })
    )
    expect(result).toBe('/courses')
  })

  // 5. Authenticated student accessing admin route → redirected to /courses
  it('redirects student attempting to reach admin route to /courses', async () => {
    const result = await guardFn(
      makeRoute('/admin/users', { requiresAuth: true, requiredRole: 'admin' }),
      makeAuth({ isAuthenticated: true, isStudent: true, isConsented: true })
    )
    expect(result).toBe('/courses')
  })

  // 6. Authenticated admin accessing student route → redirected to /admin/users
  it('redirects admin attempting to reach student route to /admin/users', async () => {
    const result = await guardFn(
      makeRoute('/courses', { requiresAuth: true, requiredRole: 'student' }),
      makeAuth({ isAuthenticated: true, isAdmin: true, isConsented: true })
    )
    expect(result).toBe('/admin/users')
  })

  // 7. Authenticated but not consented → redirected to /consent
  it('redirects authenticated but unconsented user to /consent', async () => {
    const result = await guardFn(
      makeRoute('/courses', { requiresAuth: true, requiredRole: 'student' }),
      makeAuth({ isAuthenticated: true, isStudent: true, isConsented: false })
    )
    expect(result).toBe('/consent')
  })

  // 7b. Authenticated, not consented, navigating directly to /consent → allowed
  it('allows unconsented authenticated user to reach /consent', async () => {
    const result = await guardFn(
      makeRoute('/consent', { requiresAuth: true }),
      makeAuth({ isAuthenticated: true, isStudent: true, isConsented: false })
    )
    expect(result).toBeUndefined()
  })

  // 8. Authenticated, consented student accessing /courses → next() called
  it('allows authenticated consented student to reach /courses', async () => {
    const result = await guardFn(
      makeRoute('/courses', { requiresAuth: true, requiredRole: 'student' }),
      makeAuth({ isAuthenticated: true, isStudent: true, isConsented: true })
    )
    expect(result).toBeUndefined()
  })

  // 9. getting-started — role-scoped routes
  // @{"req": ["REQ-FEONBOARD-001"]}
  it('allows authenticated consented student to reach /getting-started', async () => {
    const result = await guardFn(
      makeRoute('/getting-started', { requiresAuth: true, requiredRole: 'student' }),
      makeAuth({ isAuthenticated: true, isStudent: true, isConsented: true })
    )
    expect(result).toBeUndefined()
  })

  it('allows authenticated consented admin to reach /admin/getting-started', async () => {
    const result = await guardFn(
      makeRoute('/admin/getting-started', { requiresAuth: true, requiredRole: 'admin' }),
      makeAuth({ isAuthenticated: true, isAdmin: true, isConsented: true })
    )
    expect(result).toBeUndefined()
  })

  it('redirects an admin hitting the student /getting-started path to the admin home', async () => {
    const result = await guardFn(
      makeRoute('/getting-started', { requiresAuth: true, requiredRole: 'student' }),
      makeAuth({ isAuthenticated: true, isAdmin: true, isConsented: true })
    )
    expect(result).toBe('/admin/users')
  })

  // Rule 6: a consented admin must never land on the chrome-less /consent page
  it('redirects a consented admin navigating to /consent to /admin/users', async () => {
    const result = await guardFn(
      makeRoute('/consent', { requiresAuth: true }),
      makeAuth({ isAuthenticated: true, isAdmin: true, isConsented: true })
    )
    expect(result).toBe('/admin/users')
  })

  it('still routes an unconsented admin to /consent after restore', async () => {
    const result = await guardFn(
      makeRoute('/admin/users', { requiresAuth: true, requiredRole: 'admin' }),
      makeAuth({ isAuthenticated: true, isAdmin: true, isConsented: false })
    )
    expect(result).toBe('/consent')
  })

  it('/getting-started redirects unauthenticated user to /login', async () => {
    const result = await guardFn(
      makeRoute('/getting-started', { requiresAuth: true, requiredRole: 'student' }),
      makeAuth({ isAuthenticated: false })
    )
    expect(result).toBe('/login')
  })

  it('resolves /getting-started to the StudentLayout child and /admin/getting-started to the AdminLayout child', () => {
    const student = router.resolve('/getting-started')
    expect(student.name).toBe('getting-started')
    expect(student.matched[student.matched.length - 1].meta.requiredRole).toBe('student')

    const admin = router.resolve('/admin/getting-started')
    expect(admin.name).toBe('getting-started-admin')
    expect(admin.matched[admin.matched.length - 1].meta.requiredRole).toBe('admin')
  })

  // 10. Student path preservation
  // @{"req": ["REQ-FEAUTH-041"]}
  it('student route /courses/:id/intake still has requiredRole student', () => {
    const route = router.resolve('/courses/abc123/intake')
    const leaf = route.matched[route.matched.length - 1]
    expect(leaf.meta.requiredRole).toBe('student')
  })

  it('student route /courses/:id/hub still resolves correctly', () => {
    const route = router.resolve('/courses/abc123/hub')
    expect(route.name).toBe('course-hub')
  })

  it('student route /courses/:id/grades still resolves correctly', () => {
    const route = router.resolve('/courses/abc123/grades')
    expect(route.name).toBe('grades')
  })

  // Restore-in-flight: guard via real store awaiting getRestorePromise()
  // @{"req": ["REQ-FEAUTH-170"]}
  // Before restoreSession() is called, getRestorePromise() returns null.
  // With a null restorePromise the guard skips the await, so navigation proceeds
  // immediately with current (unauthenticated) state.
  it('guard via store with no restore started redirects unauthenticated to /login', async () => {
    // restoreSession has not been called; getRestorePromise() returns null
    expect(auth.getRestorePromise()).toBeNull()
    const result = await guardFn(
      makeRoute('/courses', { requiresAuth: true, requiredRole: 'student' }),
      {
        restorePromise: auth.getRestorePromise(),
        isAuthenticated: auth.isAuthenticated,
        isAdmin: auth.isAdmin,
        isStudent: auth.isStudent,
        isConsented: auth.isConsented,
        isExpired: auth.isExpired,
        logout: auth.logout
      }
    )
    // No session and no promise to await → unauthenticated → /login
    expect(result).toBe('/login')
  })

  // Null-token client test: client.ts must not send "Bearer null"
  // @{"req": ["REQ-FEAUTH-171"]}
  it('client.ts omits Authorization header when token is null', async () => {
    const mockFetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: () => Promise.resolve({})
    })
    vi.stubGlobal('fetch', mockFetch)

    const { get } = await import('@/api/client')
    await get('/api/test', null)

    const [, init] = mockFetch.mock.calls[0] as [string, RequestInit]
    const headers = init.headers as Record<string, string>
    expect(headers['Authorization']).toBeUndefined()
  })

  it('client.ts omits Authorization header when token is undefined', async () => {
    const mockFetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: () => Promise.resolve({})
    })
    vi.stubGlobal('fetch', mockFetch)

    const { get } = await import('@/api/client')
    await get('/api/test', undefined)

    const [, init] = mockFetch.mock.calls[0] as [string, RequestInit]
    const headers = init.headers as Record<string, string>
    expect(headers['Authorization']).toBeUndefined()
  })
})
