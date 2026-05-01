// @{"req": ["REQ-FEAUTH-001", "REQ-FEAUTH-010", "REQ-FEAUTH-011", "REQ-FEAUTH-040", "REQ-FEAUTH-041", "REQ-FEAUTH-042", "REQ-FEAUTH-045", "REQ-FEAUTH-155", "REQ-FEAUTH-156"]}
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useAuthStore } from './auth'
import * as clientModule from '@/api/client'

describe('useAuthStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('should initialize with empty state', () => {
    const store = useAuthStore()
    expect(store.token).toBeNull()
    expect(store.role).toBeNull()
    expect(store.expiresAt).toBeNull()
    expect(store.isConsented).toBe(false)
  })

  it('should set token, role, and expiresAt on login', () => {
    const store = useAuthStore()
    const now = Math.floor(Date.now() / 1000)
    const expiresAt = now + 3600

    store.login('test-token', 'student', expiresAt)

    expect(store.token).toBe('test-token')
    expect(store.role).toBe('student')
    expect(store.expiresAt).toBe(expiresAt)
    expect(store.isConsented).toBe(false)
  })

  it('should clear all state on logout', () => {
    const store = useAuthStore()
    const now = Math.floor(Date.now() / 1000)
    store.login('test-token', 'admin', now + 3600)
    store.setConsented()

    expect(store.token).not.toBeNull()
    expect(store.role).not.toBeNull()
    expect(store.isConsented).toBe(true)

    store.logout()

    expect(store.token).toBeNull()
    expect(store.role).toBeNull()
    expect(store.expiresAt).toBeNull()
    expect(store.isConsented).toBe(false)
  })

  it('should set isConsented to true without changing other state', () => {
    const store = useAuthStore()
    const now = Math.floor(Date.now() / 1000)
    const expiresAt = now + 3600
    store.login('test-token', 'student', expiresAt)

    store.setConsented()

    expect(store.isConsented).toBe(true)
    expect(store.token).toBe('test-token')
    expect(store.role).toBe('student')
    expect(store.expiresAt).toBe(expiresAt)
  })

  it('isAuthenticated getter should be false when token is null', () => {
    const store = useAuthStore()
    expect(store.isAuthenticated).toBe(false)
  })

  it('isAuthenticated getter should be true when token is set', () => {
    const store = useAuthStore()
    store.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)
    expect(store.isAuthenticated).toBe(true)
  })

  it('isStudent getter should return true only for student role', () => {
    const store = useAuthStore()

    store.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)
    expect(store.isStudent).toBe(true)
    expect(store.isAdmin).toBe(false)

    store.logout()
    expect(store.isStudent).toBe(false)
  })

  it('isAdmin getter should return true only for admin role', () => {
    const store = useAuthStore()

    store.login('test-token', 'admin', Math.floor(Date.now() / 1000) + 3600)
    expect(store.isAdmin).toBe(true)
    expect(store.isStudent).toBe(false)

    store.logout()
    expect(store.isAdmin).toBe(false)
  })

  it('isExpired getter should return false when expiresAt is null', () => {
    const store = useAuthStore()
    expect(store.isExpired).toBe(false)
  })

  it('isExpired getter should return false when expiresAt is in the future', () => {
    const store = useAuthStore()
    const futureTime = Math.floor(Date.now() / 1000) + 3600
    store.login('test-token', 'student', futureTime)
    expect(store.isExpired).toBe(false)
  })

  it('isExpired getter should return true when expiresAt is in the past', () => {
    const store = useAuthStore()
    const pastTime = Math.floor(Date.now() / 1000) - 1
    store.login('test-token', 'student', pastTime)
    expect(store.isExpired).toBe(true)
  })

  it('registerUnauthorizedHandler should register a handler that calls logout', () => {
    const store = useAuthStore()
    const setSpy = vi.spyOn(clientModule, 'setUnauthorizedHandler')

    store.registerUnauthorizedHandler()

    expect(setSpy).toHaveBeenCalledWith(expect.any(Function))

    const handler = setSpy.mock.calls[0][0] as () => void
    store.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)
    expect(store.token).not.toBeNull()

    handler()

    expect(store.token).toBeNull()
    expect(store.role).toBeNull()
    expect(store.expiresAt).toBeNull()
    expect(store.isConsented).toBe(false)

    setSpy.mockRestore()
  })

  it('logout should call setUnauthorizedHandler(null)', () => {
    const store = useAuthStore()
    const setSpy = vi.spyOn(clientModule, 'setUnauthorizedHandler')

    store.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)
    store.logout()

    expect(setSpy).toHaveBeenCalledWith(null)

    setSpy.mockRestore()
  })

  it('should not write to localStorage on login', () => {
    const store = useAuthStore()
    const setItemSpy = vi.spyOn(localStorage, 'setItem')

    store.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    expect(setItemSpy).not.toHaveBeenCalled()

    setItemSpy.mockRestore()
  })
})
