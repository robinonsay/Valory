// @{"req": ["REQ-FEADMIN-035", "REQ-FEADMIN-036", "REQ-FEADMIN-037", "REQ-FEADMIN-038", "REQ-FEADMIN-170", "REQ-FEADMIN-175", "REQ-FEADMIN-180", "REQ-FEADMIN-185", "REQ-FEADMIN-190", "REQ-FEADMIN-195", "REQ-FEADMIN-200"]}

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import AuditLogView from './AuditLogView.vue'
import * as clientModule from '@/api/client'
import { useAuthStore } from '@/stores/auth'

vi.mock('@/api/client', () => ({
  get: vi.fn(),
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

const { get } = await import('@/api/client')

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
})

describe('AuditLogView', () => {
  // Test 1: Fetches initial log on mount and renders entries
  it('fetches initial audit log on mount and renders entries', async () => {
    const mockResponse = {
      entries: [
        {
          id: 'entry-1',
          actor_id: 'admin-1',
          actor_username: 'alice',
          action: 'user.create',
          target_id: 'user-42',
          target_type: 'user',
          created_at: '2026-05-01T10:00:00Z',
          sequence: 1
        },
        {
          id: 'entry-2',
          actor_id: 'admin-1',
          actor_username: 'alice',
          action: 'config.change',
          target_id: null,
          target_type: null,
          created_at: '2026-05-01T11:00:00Z',
          sequence: 2
        }
      ],
      next_before: 1234567890
    }

    vi.mocked(get).mockResolvedValue(mockResponse)

    const auth = useAuthStore()
    auth.login('test-token', 'admin', Math.floor(Date.now() / 1000) + 3600)

    const wrapper = mount(AuditLogView)

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    expect(vi.mocked(get)).toHaveBeenCalledWith('/api/v1/admin/audit?limit=50', 'test-token')

    const rows = wrapper.findAll('tbody tr')
    expect(rows).toHaveLength(2)
    expect(rows[0].text()).toContain('alice')
    expect(rows[0].text()).toContain('user.create')
    expect(rows[1].text()).toContain('config.change')
  })

  // Test 2: "Load more" button visible when next_before !== 0
  it('shows Load More button when next_before is non-zero', async () => {
    const mockResponse = {
      entries: [
        {
          id: 'entry-1',
          actor_id: 'admin-1',
          actor_username: 'alice',
          action: 'user.create',
          target_id: 'user-42',
          target_type: 'user',
          created_at: '2026-05-01T10:00:00Z',
          sequence: 1
        }
      ],
      next_before: 9876543210
    }

    vi.mocked(get).mockResolvedValue(mockResponse)

    const auth = useAuthStore()
    auth.login('test-token', 'admin', Math.floor(Date.now() / 1000) + 3600)

    const wrapper = mount(AuditLogView)

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    const loadMoreButton = wrapper.find('.load-more-button')
    expect(loadMoreButton.exists()).toBe(true)
  })

  // Test 3: "Load more" click fetches with ?before=${cursor} and appends results
  it('Load More click fetches with before cursor and appends entries', async () => {
    const cursorValue = 1746100000000000000

    const mockInitialResponse = {
      entries: [
        {
          id: 'entry-1',
          actor_id: 'admin-1',
          actor_username: 'alice',
          action: 'user.create',
          target_id: 'user-1',
          target_type: 'user',
          created_at: '2026-05-01T12:00:00Z',
          sequence: 1
        }
      ],
      next_before: cursorValue
    }

    const mockMoreResponse = {
      entries: [
        {
          id: 'entry-2',
          actor_id: 'admin-1',
          actor_username: 'alice',
          action: 'user.deactivate',
          target_id: 'user-2',
          target_type: 'user',
          created_at: '2026-04-30T09:00:00Z',
          sequence: 2
        }
      ],
      next_before: 0
    }

    vi.mocked(get)
      .mockResolvedValueOnce(mockInitialResponse)
      .mockResolvedValueOnce(mockMoreResponse)

    const auth = useAuthStore()
    auth.login('test-token', 'admin', Math.floor(Date.now() / 1000) + 3600)

    const wrapper = mount(AuditLogView)

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    let rows = wrapper.findAll('tbody tr')
    expect(rows).toHaveLength(1)

    const loadMoreButton = wrapper.find('.load-more-button')
    await loadMoreButton.trigger('click')

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    rows = wrapper.findAll('tbody tr')
    expect(rows).toHaveLength(2)
    expect(rows[1].text()).toContain('user.deactivate')

    expect(vi.mocked(get)).toHaveBeenCalledWith(
      `/api/v1/admin/audit?limit=50&before=${cursorValue}`,
      'test-token'
    )
  })

  // Test 4: "Load more" button hidden when next_before === 0 (log exhausted)
  it('hides Load More button when next_before is 0', async () => {
    const mockResponse = {
      entries: [
        {
          id: 'entry-1',
          actor_id: 'admin-1',
          actor_username: 'alice',
          action: 'user.create',
          target_id: 'user-1',
          target_type: 'user',
          created_at: '2026-05-01T10:00:00Z',
          sequence: 1
        }
      ],
      next_before: 0
    }

    vi.mocked(get).mockResolvedValue(mockResponse)

    const auth = useAuthStore()
    auth.login('test-token', 'admin', Math.floor(Date.now() / 1000) + 3600)

    const wrapper = mount(AuditLogView)

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    const loadMoreButton = wrapper.find('.load-more-button')
    expect(loadMoreButton.exists()).toBe(false)
  })

  // Test 5: Button disabled during loading
  it('disables Load More button while loading', async () => {
    const cursorValue = 1746100000000000000

    const mockInitialResponse = {
      entries: [
        {
          id: 'entry-1',
          actor_id: 'admin-1',
          actor_username: 'alice',
          action: 'user.create',
          target_id: 'user-1',
          target_type: 'user',
          created_at: '2026-05-01T12:00:00Z',
          sequence: 1
        }
      ],
      next_before: cursorValue
    }

    let resolveLoadMore: (value: unknown) => void

    vi.mocked(get)
      .mockResolvedValueOnce(mockInitialResponse)
      .mockImplementationOnce(
        () =>
          new Promise(resolve => {
            resolveLoadMore = resolve
          })
      )

    const auth = useAuthStore()
    auth.login('test-token', 'admin', Math.floor(Date.now() / 1000) + 3600)

    const wrapper = mount(AuditLogView)

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    const loadMoreButton = wrapper.find('.load-more-button')
    expect(loadMoreButton.exists()).toBe(true)
    expect((loadMoreButton.element as HTMLButtonElement).disabled).toBe(false)

    await loadMoreButton.trigger('click')
    await wrapper.vm.$nextTick()

    // While the request is in flight, the button should be disabled
    expect((wrapper.find('.load-more-button').element as HTMLButtonElement).disabled).toBe(true)

    // Resolve the pending request to clean up
    resolveLoadMore!({ entries: [], next_before: 0 })
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()
  })

  // Test 6: Error state shown on fetch failure
  it('shows error message when fetch fails', async () => {
    vi.mocked(get).mockRejectedValue(
      new clientModule.ApiError(500, { error: 'Internal server error' })
    )

    const auth = useAuthStore()
    auth.login('test-token', 'admin', Math.floor(Date.now() / 1000) + 3600)

    const wrapper = mount(AuditLogView)

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    const errorDiv = wrapper.find('.error')
    expect(errorDiv.exists()).toBe(true)
    expect(errorDiv.text()).toContain('Failed to load audit log')
  })
})
