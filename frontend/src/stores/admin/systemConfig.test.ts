// @{"req": ["REQ-FEADMIN-500", "REQ-FEADMIN-501", "REQ-FEADMIN-502", "REQ-FEADMIN-503", "REQ-FEADMIN-504", "REQ-FEADMIN-510", "REQ-FEADMIN-511", "REQ-FEADMIN-512", "REQ-FEADMIN-513", "REQ-FEADMIN-514", "REQ-FEADMIN-515", "REQ-FEADMIN-516"]}

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useSystemConfigStore, type SecretStatus } from './systemConfig'
import * as clientModule from '@/api/client'

vi.mock('@/api/client', () => ({
  get: vi.fn(),
  put: vi.fn(),
  del: vi.fn(),
  ApiError: class extends Error {
    status: number
    body: unknown
    constructor(status: number, body: unknown) {
      super(`HTTP ${status}`)
      this.name = 'ApiError'
      this.status = status
      this.body = body
    }
  }
}))

const { get, put, del } = await import('@/api/client')

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
})

describe('useSystemConfigStore', () => {
  // @{"req": ["REQ-FEADMIN-500"]}
  it('fetchSecrets loads secret statuses from API', async () => {
    const secretsList: SecretStatus[] = [
      {
        name: 'anthropic_api_key',
        configured: true,
        last4: '5678',
        updated_at: '2024-06-12T10:00:00Z',
        source: 'managed'
      },
      {
        name: 'brave_api_key',
        configured: false,
        last4: null,
        updated_at: null,
        source: 'none'
      }
    ]
    vi.mocked(get).mockResolvedValue({ secrets: secretsList })

    const store = useSystemConfigStore()
    await store.fetchSecrets()

    expect(vi.mocked(get)).toHaveBeenCalledWith('/api/v1/admin/secrets')
    expect(store.secrets).toEqual(secretsList)
    expect(store.error).toBeNull()
  })

  // @{"req": ["REQ-FEADMIN-501", "REQ-FEADMIN-502"]}
  it('saveSecret calls PUT and refreshes secrets on success', async () => {
    vi.mocked(get).mockResolvedValue({
      secrets: [
        {
          name: 'anthropic_api_key',
          configured: true,
          last4: 'abcd',
          updated_at: '2024-06-12T10:05:00Z',
          source: 'managed'
        }
      ]
    })
    vi.mocked(put).mockResolvedValue(undefined)

    const store = useSystemConfigStore()
    const error = await store.saveSecret('anthropic_api_key', 'test-secret-value')

    expect(vi.mocked(put)).toHaveBeenCalledWith(
      '/api/v1/admin/secrets/anthropic_api_key',
      { value: 'test-secret-value' }
    )
    expect(vi.mocked(get)).toHaveBeenCalledWith('/api/v1/admin/secrets')
    expect(error).toBeNull()
    expect(store.error).toBeNull()
  })

  // @{"req": ["REQ-FEADMIN-501", "REQ-FEADMIN-502"]}
  it('saveSecret returns error message on API failure', async () => {
    const apiError = new (clientModule.ApiError as any)(400, {
      error: 'Invalid API key format'
    })
    vi.mocked(put).mockRejectedValue(apiError)

    const store = useSystemConfigStore()
    const error = await store.saveSecret('anthropic_api_key', 'invalid')

    expect(error).toBe('Invalid API key format')
    expect(store.error).toBe('Invalid API key format')
  })

  // @{"req": ["REQ-FEADMIN-504"]}
  it('deleteSecret calls DELETE and refreshes secrets on success', async () => {
    vi.mocked(get).mockResolvedValue({
      secrets: [
        {
          name: 'anthropic_api_key',
          configured: false,
          last4: null,
          updated_at: null,
          source: 'env'
        }
      ]
    })
    vi.mocked(del).mockResolvedValue(undefined)

    const store = useSystemConfigStore()
    const error = await store.deleteSecret('anthropic_api_key')

    expect(vi.mocked(del)).toHaveBeenCalledWith('/api/v1/admin/secrets/anthropic_api_key')
    expect(vi.mocked(get)).toHaveBeenCalledWith('/api/v1/admin/secrets')
    expect(error).toBeNull()
    expect(store.error).toBeNull()
  })

  // @{"req": ["REQ-FEADMIN-504"]}
  it('deleteSecret returns error message on API failure', async () => {
    const apiError = new (clientModule.ApiError as any)(403, {
      error: 'Unauthorized'
    })
    vi.mocked(del).mockRejectedValue(apiError)

    const store = useSystemConfigStore()
    const error = await store.deleteSecret('anthropic_api_key')

    expect(error).toBe('Unauthorized')
    expect(store.error).toBe('Unauthorized')
  })

  // @{"req": ["REQ-FEADMIN-500"]}
  it('fetchSecrets handles error gracefully', async () => {
    const apiError = new (clientModule.ApiError as any)(500, {
      error: 'Internal server error'
    })
    vi.mocked(get).mockRejectedValue(apiError)

    const store = useSystemConfigStore()
    await store.fetchSecrets()

    expect(store.secrets).toEqual([])
    expect(store.error).toContain('Failed to load secrets')
  })
})
