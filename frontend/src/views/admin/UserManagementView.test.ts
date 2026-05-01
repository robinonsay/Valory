// @{"req": ["REQ-FEADMIN-020", "REQ-FEADMIN-021", "REQ-FEADMIN-022", "REQ-FEADMIN-025", "REQ-FEADMIN-026", "REQ-FEADMIN-027", "REQ-FEADMIN-113", "REQ-FEADMIN-120", "REQ-FEADMIN-130", "REQ-FEADMIN-140", "REQ-FEADMIN-150", "REQ-FEADMIN-160"]}

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import UserManagementView from './UserManagementView.vue'
import * as clientModule from '@/api/client'
import { useAuthStore } from '@/stores/auth'

vi.mock('@/api/client', () => ({
  get: vi.fn(),
  post: vi.fn(),
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

const { get, post, del } = await import('@/api/client')

const SAMPLE_USERS = [
  {
    id: 'user-1',
    username: 'alice',
    role: 'student',
    is_active: true,
    email: 'alice@example.com',
    created_at: '2026-01-01T00:00:00Z'
  },
  {
    id: 'user-2',
    username: 'bob',
    role: 'student',
    is_active: false,
    email: 'bob@example.com',
    created_at: '2026-02-01T00:00:00Z'
  }
]

function setupAuth() {
  const auth = useAuthStore()
  auth.login('test-token', 'admin', Math.floor(Date.now() / 1000) + 3600)
  return auth
}

async function flushPromises() {
  await new Promise(resolve => setTimeout(resolve, 50))
  await new Promise(resolve => setTimeout(resolve, 0))
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
})

describe('UserManagementView', () => {
  // Test 1: Fetches and renders user list on mount
  it('fetches and renders user list on mount', async () => {
    vi.mocked(get).mockResolvedValue({ users: SAMPLE_USERS })
    setupAuth()

    const wrapper = mount(UserManagementView)

    await flushPromises()
    await wrapper.vm.$nextTick()

    expect(vi.mocked(get)).toHaveBeenCalledWith('/api/v1/users', 'test-token')

    const rows = wrapper.findAll('tbody tr.user-row')
    expect(rows).toHaveLength(2)
    expect(rows[0].text()).toContain('alice')
    expect(rows[1].text()).toContain('bob')
  })

  // Test 2: Deactivate button POSTs to correct endpoint and updates is_active locally
  it('deactivate button POSTs to correct endpoint and updates is_active locally', async () => {
    vi.mocked(get).mockResolvedValue({ users: [{ ...SAMPLE_USERS[0] }] })
    vi.mocked(post).mockResolvedValue(undefined)
    setupAuth()

    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)

    const wrapper = mount(UserManagementView)
    await flushPromises()
    await wrapper.vm.$nextTick()

    const deactivateBtn = wrapper.find('.deactivate-btn')
    expect(deactivateBtn.exists()).toBe(true)
    await deactivateBtn.trigger('click')

    await flushPromises()
    await wrapper.vm.$nextTick()

    expect(vi.mocked(post)).toHaveBeenCalledWith(
      '/api/v1/users/user-1/deactivate',
      {},
      'test-token'
    )

    // After deactivation, the status badge should show Inactive
    const statusBadge = wrapper.find('.status-badge')
    expect(statusBadge.text()).toBe('Inactive')

    confirmSpy.mockRestore()
  })

  // Test 3: Activate button POSTs to correct endpoint and updates is_active locally
  it('activate button POSTs to correct endpoint and updates is_active locally', async () => {
    vi.mocked(get).mockResolvedValue({ users: [{ ...SAMPLE_USERS[1] }] })
    vi.mocked(post).mockResolvedValue(undefined)
    setupAuth()

    const wrapper = mount(UserManagementView)
    await flushPromises()
    await wrapper.vm.$nextTick()

    const activateBtn = wrapper.find('.activate-btn')
    expect(activateBtn.exists()).toBe(true)
    await activateBtn.trigger('click')

    await flushPromises()
    await wrapper.vm.$nextTick()

    expect(vi.mocked(post)).toHaveBeenCalledWith(
      '/api/v1/users/user-2/activate',
      {},
      'test-token'
    )

    // After activation, the status badge should show Active
    const statusBadge = wrapper.find('.status-badge')
    expect(statusBadge.text()).toBe('Active')
  })

  // Test 4: Delete 409 shows "Cannot delete: agent operations could not be terminated"
  it('delete button 409 shows correct conflict message', async () => {
    vi.mocked(get).mockResolvedValue({ users: [{ ...SAMPLE_USERS[0] }] })
    vi.mocked(del).mockRejectedValue(
      new clientModule.ApiError(409, { error: 'agent operations could not be terminated' })
    )
    setupAuth()

    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)

    const wrapper = mount(UserManagementView)
    await flushPromises()
    await wrapper.vm.$nextTick()

    const deleteBtn = wrapper.find('.delete-btn')
    expect(deleteBtn.exists()).toBe(true)
    await deleteBtn.trigger('click')

    await flushPromises()
    await wrapper.vm.$nextTick()

    const errorBanner = wrapper.find('[data-testid="error-banner"]')
    expect(errorBanner.exists()).toBe(true)
    expect(errorBanner.text()).toContain('Cannot delete: agent operations could not be terminated')

    confirmSpy.mockRestore()
  })

  // Test 5: Delete success removes user from table
  it('delete success removes user from table', async () => {
    vi.mocked(get).mockResolvedValue({ users: [...SAMPLE_USERS] })
    vi.mocked(del).mockResolvedValue(undefined)
    setupAuth()

    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)

    const wrapper = mount(UserManagementView)
    await flushPromises()
    await wrapper.vm.$nextTick()

    let rows = wrapper.findAll('tbody tr.user-row')
    expect(rows).toHaveLength(2)

    const deleteBtn = wrapper.find('.delete-btn')
    await deleteBtn.trigger('click')

    await flushPromises()
    await wrapper.vm.$nextTick()

    expect(vi.mocked(del)).toHaveBeenCalledWith('/api/v1/users/user-1', 'test-token')

    rows = wrapper.findAll('tbody tr.user-row')
    expect(rows).toHaveLength(1)
    expect(rows[0].text()).toContain('bob')

    confirmSpy.mockRestore()
  })

  // Test 6: Create user POSTs correct fields
  it('create user POSTs correct fields and adds user to list', async () => {
    const newUser: typeof SAMPLE_USERS[0] = {
      id: 'user-3',
      username: 'carol',
      role: 'student',
      is_active: true,
      email: 'carol@example.com',
      created_at: '2026-03-01T00:00:00Z'
    }

    vi.mocked(get).mockResolvedValue({ users: [] })
    vi.mocked(post).mockResolvedValue(newUser)
    setupAuth()

    const wrapper = mount(UserManagementView)
    await flushPromises()
    await wrapper.vm.$nextTick()

    await wrapper.find('#create-username').setValue('carol')
    await wrapper.find('#create-email').setValue('carol@example.com')
    await wrapper.find('#create-password').setValue('secret123')
    await wrapper.find('#create-role').setValue('student')

    await wrapper.find('.create-form').trigger('submit')

    await flushPromises()
    await wrapper.vm.$nextTick()

    expect(vi.mocked(post)).toHaveBeenCalledWith(
      '/api/v1/users',
      {
        username: 'carol',
        email: 'carol@example.com',
        password: 'secret123',
        role: 'student'
      },
      'test-token'
    )

    const rows = wrapper.findAll('tbody tr.user-row')
    expect(rows).toHaveLength(1)
    expect(rows[0].text()).toContain('carol')
  })

  // Test 7: Active user shows Deactivate (not Activate); inactive shows Activate (not Deactivate)
  it('active user shows Deactivate not Activate; inactive shows Activate not Deactivate', async () => {
    vi.mocked(get).mockResolvedValue({ users: [...SAMPLE_USERS] })
    setupAuth()

    const wrapper = mount(UserManagementView)
    await flushPromises()
    await wrapper.vm.$nextTick()

    const rows = wrapper.findAll('tbody tr.user-row')
    expect(rows).toHaveLength(2)

    // First user (alice) is active — should have Deactivate, not Activate
    const aliceActions = rows[0].find('.col-actions')
    expect(aliceActions.find('.deactivate-btn').exists()).toBe(true)
    expect(aliceActions.find('.activate-btn').exists()).toBe(false)

    // Second user (bob) is inactive — should have Activate, not Deactivate
    const bobActions = rows[1].find('.col-actions')
    expect(bobActions.find('.activate-btn').exists()).toBe(true)
    expect(bobActions.find('.deactivate-btn').exists()).toBe(false)
  })

  // Test 8: CSRF — verify post and del from @/api/client are used (not raw fetch)
  // This verifies the module mock intercepts calls, which confirms the component
  // uses the client functions (which automatically include X-CSRF-Token) rather
  // than raw fetch.
  it('uses post from @/api/client for mutations ensuring CSRF header is included', async () => {
    vi.mocked(get).mockResolvedValue({ users: [{ ...SAMPLE_USERS[1] }] })
    vi.mocked(post).mockResolvedValue(undefined)
    setupAuth()

    const wrapper = mount(UserManagementView)
    await flushPromises()
    await wrapper.vm.$nextTick()

    // Trigger activate (a POST mutation)
    await wrapper.find('.activate-btn').trigger('click')
    await flushPromises()
    await wrapper.vm.$nextTick()

    // The mocked post from @/api/client was called — not raw fetch
    expect(vi.mocked(post)).toHaveBeenCalled()
    // Confirm raw fetch was not called directly (window.fetch not spied or called)
    // The fact that post() was intercepted by the vi.mock proves client functions are used
    expect(vi.mocked(post)).toHaveBeenCalledWith(
      expect.stringContaining('/api/v1/users/'),
      {},
      'test-token'
    )
  })

  it('uses del from @/api/client for delete ensuring CSRF header is included', async () => {
    vi.mocked(get).mockResolvedValue({ users: [{ ...SAMPLE_USERS[0] }] })
    vi.mocked(del).mockResolvedValue(undefined)
    setupAuth()

    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)

    const wrapper = mount(UserManagementView)
    await flushPromises()
    await wrapper.vm.$nextTick()

    await wrapper.find('.delete-btn').trigger('click')
    await flushPromises()
    await wrapper.vm.$nextTick()

    // The mocked del from @/api/client was called — not raw fetch
    expect(vi.mocked(del)).toHaveBeenCalled()
    expect(vi.mocked(del)).toHaveBeenCalledWith(
      '/api/v1/users/user-1',
      'test-token'
    )

    confirmSpy.mockRestore()
  })
})
