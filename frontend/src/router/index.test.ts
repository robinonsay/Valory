// @{"req": ["REQ-FEAUTH-040", "REQ-FEAUTH-041", "REQ-FEAUTH-042", "REQ-FEAUTH-043", "REQ-FEAUTH-148", "REQ-FEAUTH-150", "REQ-FEADMIN-014", "REQ-FEADMIN-015"]}
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useAuthStore } from '@/stores/auth'
import { guardFn } from '@/router/index'
import type { RouteLocationNormalized } from 'vue-router'

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

describe('router beforeEach guard', () => {
  let auth: ReturnType<typeof useAuthStore>

  beforeEach(() => {
    setActivePinia(createPinia())
    auth = useAuthStore()
  })

  // 1. Unauthenticated accessing protected route → redirected to /login
  it('redirects unauthenticated user from protected route to /login', () => {
    const result = guardFn(makeRoute('/courses', { requiresAuth: true, requiredRole: 'student' }), auth)
    expect(result).toBe('/login')
  })

  // 2. Expired token accessing protected route → logout() called, redirect /login
  it('calls logout and redirects to /login when token is expired', () => {
    const pastTime = Math.floor(Date.now() / 1000) - 1
    auth.login('tok', 'student', pastTime)

    const logoutSpy = vi.spyOn(auth, 'logout')
    const result = guardFn(makeRoute('/courses', { requiresAuth: true, requiredRole: 'student' }), auth)

    expect(logoutSpy).toHaveBeenCalledOnce()
    expect(result).toBe('/login')
  })

  // 3. Authenticated admin navigating to /login → redirected to /admin/users
  it('redirects authenticated admin away from /login to /admin/users', () => {
    auth.login('tok', 'admin', Math.floor(Date.now() / 1000) + 3600)
    auth.setConsented()

    const result = guardFn(makeRoute('/login', {}), auth)
    expect(result).toBe('/admin/users')
  })

  // 4. Authenticated student navigating to /login → redirected to /courses
  it('redirects authenticated student away from /login to /courses', () => {
    auth.login('tok', 'student', Math.floor(Date.now() / 1000) + 3600)
    auth.setConsented()

    const result = guardFn(makeRoute('/login', {}), auth)
    expect(result).toBe('/courses')
  })

  // 5. Authenticated student accessing admin route → redirected to /courses
  it('redirects student attempting to reach admin route to /courses', () => {
    auth.login('tok', 'student', Math.floor(Date.now() / 1000) + 3600)
    auth.setConsented()

    const result = guardFn(makeRoute('/admin/users', { requiresAuth: true, requiredRole: 'admin' }), auth)
    expect(result).toBe('/courses')
  })

  // 6. Authenticated admin accessing student route → redirected to /admin/users
  it('redirects admin attempting to reach student route to /admin/users', () => {
    auth.login('tok', 'admin', Math.floor(Date.now() / 1000) + 3600)
    auth.setConsented()

    const result = guardFn(makeRoute('/courses', { requiresAuth: true, requiredRole: 'student' }), auth)
    expect(result).toBe('/admin/users')
  })

  // 7. Authenticated but not consented → redirected to /consent
  it('redirects authenticated but unconsented user to /consent', () => {
    auth.login('tok', 'student', Math.floor(Date.now() / 1000) + 3600)
    // isConsented is false by default

    const result = guardFn(makeRoute('/courses', { requiresAuth: true, requiredRole: 'student' }), auth)
    expect(result).toBe('/consent')
  })

  // 7b. Authenticated, not consented, navigating directly to /consent → allowed
  it('allows unconsented authenticated user to reach /consent', () => {
    auth.login('tok', 'student', Math.floor(Date.now() / 1000) + 3600)

    const result = guardFn(makeRoute('/consent', { requiresAuth: true }), auth)
    expect(result).toBeUndefined()
  })

  // 8. Authenticated, consented student accessing /courses → next() called
  it('allows authenticated consented student to reach /courses', () => {
    auth.login('tok', 'student', Math.floor(Date.now() / 1000) + 3600)
    auth.setConsented()

    const result = guardFn(makeRoute('/courses', { requiresAuth: true, requiredRole: 'student' }), auth)
    expect(result).toBeUndefined()
  })
})
