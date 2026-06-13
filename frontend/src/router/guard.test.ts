// @{"req": ["REQ-FEAUTH-202"]}

import { describe, it, expect } from 'vitest'
import { guardFn } from './index'
import { createMemoryHistory, createRouter } from 'vue-router'

describe('Router Guard - 2FA Pending State', () => {
  // @{"req": ["REQ-FEAUTH-202"]}
  it('should redirect pending-2FA user to /login/verify when accessing protected route', async () => {
    const to = createRouter({ history: createMemoryHistory(), routes: [] }).resolve({ path: '/courses' })

    const result = await guardFn(to, {
      isAuthenticated: false,
      isAdmin: false,
      isStudent: false,
      isConsented: false,
      isExpired: false,
      mustChangePassword: false,
      pendingTwoFactor: {
        token: 'pending-token',
        expiresAt: Math.floor(Date.now() / 1000) + 600
      },
      logout: () => {}
    })

    expect(result).toBe('/login/verify')
  })

  // @{"req": ["REQ-FEAUTH-202"]}
  it('should allow pending-2FA user to stay on /login/verify', async () => {
    const to = createRouter({ history: createMemoryHistory(), routes: [] }).resolve({ path: '/login/verify' })

    const result = await guardFn(to, {
      isAuthenticated: false,
      isAdmin: false,
      isStudent: false,
      isConsented: false,
      isExpired: false,
      mustChangePassword: false,
      pendingTwoFactor: {
        token: 'pending-token',
        expiresAt: Math.floor(Date.now() / 1000) + 600
      },
      logout: () => {}
    })

    expect(result).toBeUndefined()
  })

  // @{"req": ["REQ-FEAUTH-202"]}
  it('should allow pending-2FA user to return to /login', async () => {
    const to = createRouter({ history: createMemoryHistory(), routes: [] }).resolve({ path: '/login' })

    const result = await guardFn(to, {
      isAuthenticated: false,
      isAdmin: false,
      isStudent: false,
      isConsented: false,
      isExpired: false,
      mustChangePassword: false,
      pendingTwoFactor: {
        token: 'pending-token',
        expiresAt: Math.floor(Date.now() / 1000) + 600
      },
      logout: () => {}
    })

    expect(result).toBeUndefined()
  })

  // @{"req": ["REQ-FEAUTH-202"]}
  it('should not treat pending-2FA as authenticated', async () => {
    const to = createRouter({ history: createMemoryHistory(), routes: [] }).resolve({ path: '/courses' })

    const result = await guardFn(to, {
      isAuthenticated: false, // pending is NOT authenticated
      isAdmin: false,
      isStudent: false,
      isConsented: false,
      isExpired: false,
      mustChangePassword: false,
      pendingTwoFactor: {
        token: 'pending-token',
        expiresAt: Math.floor(Date.now() / 1000) + 600
      },
      logout: () => {}
    })

    expect(result).toBe('/login/verify')
  })

  // @{"req": ["REQ-FEAUTH-202"]}
  it('should redirect authenticated user away from /login/verify', async () => {
    const to = createRouter({ history: createMemoryHistory(), routes: [] }).resolve({ path: '/login/verify' })

    const result = await guardFn(to, {
      isAuthenticated: true,
      isAdmin: false,
      isStudent: true,
      isConsented: true,
      isExpired: false,
      mustChangePassword: false,
      pendingTwoFactor: null,
      logout: () => {}
    })

    expect(result).toBe('/courses')
  })

  // @{"req": ["REQ-FEAUTH-202"]}
  it('should redirect authenticated admin away from /login/verify', async () => {
    const to = createRouter({ history: createMemoryHistory(), routes: [] }).resolve({ path: '/login/verify' })

    const result = await guardFn(to, {
      isAuthenticated: true,
      isAdmin: true,
      isStudent: false,
      isConsented: true,
      isExpired: false,
      mustChangePassword: false,
      pendingTwoFactor: null,
      logout: () => {}
    })

    expect(result).toBe('/admin/users')
  })
})
