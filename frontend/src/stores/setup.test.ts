// @{"verifies": ["REQ-FESETUP-001", "REQ-FESETUP-002", "REQ-FESETUP-003"]}
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useSetupStore } from './setup'
import * as clientModule from '@/api/client'

describe('useSetupStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('initializes with needsSetup null', () => {
    const store = useSetupStore()
    expect(store.needsSetup).toBeNull()
  })

  it('getSetupPromise returns null before checkSetupStatus is called', () => {
    const store = useSetupStore()
    expect(store.getSetupPromise()).toBeNull()
  })

  // REQ-FESETUP-001: status check sets needsSetup from the API response
  it('sets needsSetup to true when API returns needs_setup: true', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValueOnce({ needs_setup: true })
    const store = useSetupStore()
    await store.checkSetupStatus()
    expect(store.needsSetup).toBe(true)
  })

  // REQ-FESETUP-001: status check sets needsSetup to false when setup complete
  it('sets needsSetup to false when API returns needs_setup: false', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValueOnce({ needs_setup: false })
    const store = useSetupStore()
    await store.checkSetupStatus()
    expect(store.needsSetup).toBe(false)
  })

  // REQ-FESETUP-001: network failure falls back to false (non-blocking)
  it('sets needsSetup to false on network error', async () => {
    vi.spyOn(clientModule, 'get').mockRejectedValueOnce(new Error('network down'))
    const store = useSetupStore()
    await store.checkSetupStatus()
    expect(store.needsSetup).toBe(false)
  })

  // REQ-FESETUP-001: promise is memoized — a second call does not fire a second request
  it('memoizes the in-flight promise so only one network request is made', async () => {
    const getSpy = vi.spyOn(clientModule, 'get').mockResolvedValue({ needs_setup: false })
    const store = useSetupStore()
    const p1 = store.checkSetupStatus()
    const p2 = store.checkSetupStatus()
    await Promise.all([p1, p2])
    // Only one network request should have been made regardless of how many
    // times checkSetupStatus is called.
    expect(getSpy).toHaveBeenCalledTimes(1)
  })

  // REQ-FESETUP-001: getSetupPromise returns a promise after checkSetupStatus is called
  it('getSetupPromise returns a promise after checkSetupStatus is called', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValue({ needs_setup: false })
    const store = useSetupStore()
    expect(store.getSetupPromise()).toBeNull()
    store.checkSetupStatus()
    // After the first call, getSetupPromise returns a Promise (not null)
    expect(store.getSetupPromise()).toBeInstanceOf(Promise)
  })

  // REQ-FESETUP-003: markComplete sets needsSetup to false
  it('markComplete sets needsSetup to false', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValueOnce({ needs_setup: true })
    const store = useSetupStore()
    await store.checkSetupStatus()
    expect(store.needsSetup).toBe(true)
    store.markComplete()
    expect(store.needsSetup).toBe(false)
  })

  // REQ-FESETUP-003: markComplete works even when called before checkSetupStatus
  it('markComplete sets needsSetup to false from null', () => {
    const store = useSetupStore()
    expect(store.needsSetup).toBeNull()
    store.markComplete()
    expect(store.needsSetup).toBe(false)
  })
})
