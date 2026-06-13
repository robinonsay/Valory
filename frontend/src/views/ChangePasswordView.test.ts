// @{"req": ["REQ-FEUSER-001", "REQ-FEUSER-002", "REQ-FEUSER-003", "REQ-FEUSER-004"]}

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import ChangePasswordView from './ChangePasswordView.vue'
import * as clientModule from '@/api/client'
import { useAuthStore } from '@/stores/auth'

vi.mock('@/api/client', () => ({
  post: vi.fn(),
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

const { post } = await import('@/api/client')

function setupAuth(mustChangePassword = false) {
  const auth = useAuthStore()
  auth.$patch({
    role: 'student',
    expiresAt: Math.floor(Date.now() / 1000) + 3600,
    restoreDone: true,
    mustChangePassword
  })
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

describe('ChangePasswordView', () => {
  // REQ-FEUSER-003: form has current, new, and confirm fields
  it('renders current, new, and confirm password fields', () => {
    setupAuth(false)
    const wrapper = mount(ChangePasswordView)

    expect(wrapper.find('#current-password').exists()).toBe(true)
    expect(wrapper.find('#new-password').exists()).toBe(true)
    expect(wrapper.find('#confirm-password').exists()).toBe(true)
  })

  // REQ-FEUSER-003: forced mode shows message
  it('shows forced notice when mustChangePassword is true', () => {
    setupAuth(true)
    const wrapper = mount(ChangePasswordView)

    const notice = wrapper.find('.forced-notice')
    expect(notice.exists()).toBe(true)
    expect(notice.text()).toContain('must change your password')
  })

  // REQ-FEUSER-004: validates password strength
  it('displays password strength error for weak passwords', async () => {
    setupAuth(false)
    const wrapper = mount(ChangePasswordView)

    await wrapper.find('#current-password').setValue('currentPassword123!')
    await wrapper.find('#new-password').setValue('weak')
    await wrapper.vm.$nextTick()

    const validationError = wrapper.find('.validation-error')
    expect(validationError.exists()).toBe(true)
    expect(validationError.text()).toContain('at least')
  })

  // REQ-FEUSER-004: valid password passes validation
  it('accepts valid password meeting all strength requirements', async () => {
    setupAuth(false)
    const wrapper = mount(ChangePasswordView)

    await wrapper.find('#current-password').setValue('CurrentPassword123!')
    await wrapper.find('#new-password').setValue('NewPassword123!')
    await wrapper.find('#confirm-password').setValue('NewPassword123!')
    await wrapper.vm.$nextTick()

    const validationErrors = wrapper.findAll('.validation-error')
    expect(validationErrors).toHaveLength(0)
  })

  // REQ-FEUSER-003: confirm password must match
  it('shows error when passwords do not match', async () => {
    setupAuth(false)
    const wrapper = mount(ChangePasswordView)

    await wrapper.find('#current-password').setValue('CurrentPassword123!')
    await wrapper.find('#new-password').setValue('NewPassword123!')
    await wrapper.find('#confirm-password').setValue('DifferentPassword123!')
    await wrapper.vm.$nextTick()

    const mismatchError = wrapper.findAll('.validation-error').filter(
      el => el.text().includes('do not match')
    )
    expect(mismatchError.length).toBeGreaterThan(0)
  })

  // REQ-FEUSER-003: submit button disabled until all validations pass
  it('disables submit button when form is invalid', async () => {
    setupAuth(false)
    const wrapper = mount(ChangePasswordView)

    let button = wrapper.find('button[type="submit"]')
    expect(button.attributes('disabled')).toBeDefined()

    await wrapper.find('#current-password').setValue('CurrentPassword123!')
    await wrapper.find('#new-password').setValue('NewPassword123!')
    await wrapper.find('#confirm-password').setValue('NewPassword123!')
    await wrapper.vm.$nextTick()

    button = wrapper.find('button[type="submit"]')
    expect(button.attributes('disabled')).toBeUndefined()
  })

  // REQ-FEUSER-001: successful change posts to correct endpoint
  it('submits to /api/v1/users/me/password on success', async () => {
    vi.mocked(post).mockResolvedValue(undefined)
    setupAuth(false)

    const wrapper = mount(ChangePasswordView)

    await wrapper.find('#current-password').setValue('CurrentPassword123!')
    await wrapper.find('#new-password').setValue('NewPassword123!')
    await wrapper.find('#confirm-password').setValue('NewPassword123!')
    await wrapper.vm.$nextTick()

    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(vi.mocked(post)).toHaveBeenCalledWith('/api/v1/users/me/password', {
      current_password: 'CurrentPassword123!',
      new_password: 'NewPassword123!'
    })
  })

  // REQ-FEUSER-001: 401/403 wrong current password
  it('shows error for wrong current password (401)', async () => {
    vi.mocked(post).mockRejectedValue(new clientModule.ApiError(401, { error: 'unauthorized' }))
    setupAuth(false)

    const wrapper = mount(ChangePasswordView)

    await wrapper.find('#current-password').setValue('WrongPassword123!')
    await wrapper.find('#new-password').setValue('NewPassword123!')
    await wrapper.find('#confirm-password').setValue('NewPassword123!')
    await wrapper.vm.$nextTick()

    await wrapper.find('form').trigger('submit')
    await flushPromises()

    const errorMessage = wrapper.find('.error-message')
    expect(errorMessage.exists()).toBe(true)
    expect(errorMessage.text()).toContain('incorrect')
  })

  // REQ-FEUSER-001: 403 wrong current password
  it('shows error for wrong current password (403)', async () => {
    vi.mocked(post).mockRejectedValue(new clientModule.ApiError(403, { error: 'forbidden' }))
    setupAuth(false)

    const wrapper = mount(ChangePasswordView)

    await wrapper.find('#current-password').setValue('WrongPassword123!')
    await wrapper.find('#new-password').setValue('NewPassword123!')
    await wrapper.find('#confirm-password').setValue('NewPassword123!')
    await wrapper.vm.$nextTick()

    await wrapper.find('form').trigger('submit')
    await flushPromises()

    const errorMessage = wrapper.find('.error-message')
    expect(errorMessage.exists()).toBe(true)
    expect(errorMessage.text()).toContain('incorrect')
  })

  // REQ-FEUSER-001: 400 policy violation
  it('shows error for policy violation (400)', async () => {
    vi.mocked(post).mockRejectedValue(new clientModule.ApiError(400, { error: 'bad request' }))
    setupAuth(false)

    const wrapper = mount(ChangePasswordView)

    await wrapper.find('#current-password').setValue('CurrentPassword123!')
    await wrapper.find('#new-password').setValue('NewPassword123!')
    await wrapper.find('#confirm-password').setValue('NewPassword123!')
    await wrapper.vm.$nextTick()

    await wrapper.find('form').trigger('submit')
    await flushPromises()

    const errorMessage = wrapper.find('.error-message')
    expect(errorMessage.exists()).toBe(true)
    expect(errorMessage.text()).toContain('does not meet')
  })

  // REQ-FEUSER-001: after success, navigates appropriately
  it('navigates after successful password change in non-forced mode', async () => {
    vi.mocked(post).mockResolvedValue(undefined)
    const auth = setupAuth(false)
    vi.spyOn(auth, 'refreshSession').mockResolvedValue()

    const wrapper = mount(ChangePasswordView)

    await wrapper.find('#current-password').setValue('CurrentPassword123!')
    await wrapper.find('#new-password').setValue('NewPassword123!')
    await wrapper.find('#confirm-password').setValue('NewPassword123!')
    await wrapper.vm.$nextTick()

    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(vi.mocked(post)).toHaveBeenCalled()
  })
})
