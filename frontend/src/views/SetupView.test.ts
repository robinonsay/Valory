// @{"verifies": ["REQ-FESETUP-004", "REQ-FESETUP-005", "REQ-FESETUP-006", "REQ-FESETUP-007"]}
import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import SetupView from './SetupView.vue'
import * as clientModule from '@/api/client'
import { useSetupStore } from '@/stores/setup'

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/setup', component: { template: '<div></div>' } },
      { path: '/login', name: 'login', component: { template: '<div></div>' } }
    ]
  })
}

describe('SetupView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  // REQ-FESETUP-004: welcome step shown on mount
  it('shows the welcome step with heading and Get Started button on mount', () => {
    const router = makeRouter()
    const wrapper = mount(SetupView, { global: { plugins: [router] } })

    expect(wrapper.text()).toContain('Welcome to Valory')
    expect(wrapper.text()).toContain('No administrator account exists yet')
    const btn = wrapper.find('button')
    expect(btn.exists()).toBe(true)
    expect(btn.text()).toBe('Get Started')
  })

  // REQ-FESETUP-004 → REQ-FESETUP-005: clicking Get Started transitions to form step
  it('transitions to the form step when Get Started is clicked', async () => {
    const router = makeRouter()
    const wrapper = mount(SetupView, { global: { plugins: [router] } })

    await wrapper.find('button').trigger('click')
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Create Administrator Account')
    expect(wrapper.find('#setup-username').exists()).toBe(true)
    expect(wrapper.find('#setup-email').exists()).toBe(true)
    expect(wrapper.find('#setup-password').exists()).toBe(true)
    expect(wrapper.find('#setup-confirm-password').exists()).toBe(true)
  })

  // REQ-FESETUP-006: blank username blocks submit
  it('shows username required error when username is blank on submit', async () => {
    const router = makeRouter()
    const wrapper = mount(SetupView, { global: { plugins: [router] } })

    await wrapper.find('button').trigger('click')
    await wrapper.vm.$nextTick()

    await wrapper.find('#setup-password').setValue('password123')
    await wrapper.find('#setup-confirm-password').setValue('password123')
    await wrapper.find('form').trigger('submit')
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Username is required.')
  })

  // REQ-FESETUP-006: short password blocks submit
  it('shows password length error when password is fewer than 8 characters', async () => {
    const router = makeRouter()
    const wrapper = mount(SetupView, { global: { plugins: [router] } })

    await wrapper.find('button').trigger('click')
    await wrapper.vm.$nextTick()

    await wrapper.find('#setup-username').setValue('admin')
    await wrapper.find('#setup-password').setValue('short')
    await wrapper.find('#setup-confirm-password').setValue('short')
    await wrapper.find('form').trigger('submit')
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Password must be at least 8 characters.')
  })

  // REQ-FESETUP-006: mismatched passwords block submit
  it('shows confirm password error when passwords do not match', async () => {
    const router = makeRouter()
    const wrapper = mount(SetupView, { global: { plugins: [router] } })

    await wrapper.find('button').trigger('click')
    await wrapper.vm.$nextTick()

    await wrapper.find('#setup-username').setValue('admin')
    await wrapper.find('#setup-password').setValue('password123')
    await wrapper.find('#setup-confirm-password').setValue('different1')
    await wrapper.find('form').trigger('submit')
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Passwords do not match.')
  })

  // REQ-FESETUP-006 + REQ-FESETUP-007: no post call when client-side validation fails
  it('does not call post when client-side validation fails', async () => {
    const postSpy = vi.spyOn(clientModule, 'post')
    const router = makeRouter()
    const wrapper = mount(SetupView, { global: { plugins: [router] } })

    await wrapper.find('button').trigger('click')
    await wrapper.vm.$nextTick()

    // blank username, short password
    await wrapper.find('#setup-password').setValue('abc')
    await wrapper.find('#setup-confirm-password').setValue('abc')
    await wrapper.find('form').trigger('submit')
    await wrapper.vm.$nextTick()

    expect(postSpy).not.toHaveBeenCalled()
  })

  // REQ-FESETUP-007: successful submit calls post, markComplete, and advances to success
  it('calls post and markComplete and shows success step on 201', async () => {
    vi.spyOn(clientModule, 'post').mockResolvedValueOnce({ username: 'admin', role: 'admin' })
    const router = makeRouter()
    const setup = useSetupStore()
    const markCompleteSpy = vi.spyOn(setup, 'markComplete')

    const wrapper = mount(SetupView, { global: { plugins: [router] } })

    await wrapper.find('button').trigger('click')
    await wrapper.vm.$nextTick()

    await wrapper.find('#setup-username').setValue('admin')
    await wrapper.find('#setup-password').setValue('password123')
    await wrapper.find('#setup-confirm-password').setValue('password123')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(clientModule.post).toHaveBeenCalledWith('/api/v1/setup', {
      username: 'admin',
      password: 'password123'
    })
    expect(markCompleteSpy).toHaveBeenCalledOnce()
    expect(wrapper.text()).toContain('Setup Complete')
    expect(wrapper.text()).toContain('Your administrator account has been created')
  })

  // REQ-FESETUP-007: after success step, router.push to /login fires after 1.5s
  it('redirects to login after 1.5s when on success step', async () => {
    vi.spyOn(clientModule, 'post').mockResolvedValueOnce({ username: 'admin', role: 'admin' })
    const router = makeRouter()
    const pushSpy = vi.spyOn(router, 'push')

    const wrapper = mount(SetupView, { global: { plugins: [router] } })

    await wrapper.find('button').trigger('click')
    await wrapper.vm.$nextTick()

    await wrapper.find('#setup-username').setValue('admin')
    await wrapper.find('#setup-password').setValue('password123')
    await wrapper.find('#setup-confirm-password').setValue('password123')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    // Timer not yet fired
    expect(pushSpy).not.toHaveBeenCalled()

    vi.advanceTimersByTime(1500)
    await wrapper.vm.$nextTick()

    expect(pushSpy).toHaveBeenCalledWith({ name: 'login' })
  })

  // REQ-FESETUP-007: already_configured (409) calls markComplete and routes to login
  it('calls markComplete and routes to login on already_configured (409)', async () => {
    vi.spyOn(clientModule, 'post').mockRejectedValueOnce(
      new clientModule.ApiError(409, { error: 'already_configured' })
    )
    const router = makeRouter()
    await router.push('/setup')
    const setup = useSetupStore()
    const markCompleteSpy = vi.spyOn(setup, 'markComplete')
    const pushSpy = vi.spyOn(router, 'push')

    const wrapper = mount(SetupView, { global: { plugins: [router] } })

    await wrapper.find('button').trigger('click')
    await wrapper.vm.$nextTick()

    await wrapper.find('#setup-username').setValue('admin')
    await wrapper.find('#setup-password').setValue('password123')
    await wrapper.find('#setup-confirm-password').setValue('password123')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(markCompleteSpy).toHaveBeenCalledOnce()
    expect(pushSpy).toHaveBeenCalledWith({ name: 'login' })
  })

  // REQ-FESETUP-005: 400 server error shows inline message without routing
  it('shows inline error message on 400 server error', async () => {
    vi.spyOn(clientModule, 'post').mockRejectedValueOnce(
      new clientModule.ApiError(400, { error: 'username_required' })
    )
    const router = makeRouter()
    const wrapper = mount(SetupView, { global: { plugins: [router] } })

    await wrapper.find('button').trigger('click')
    await wrapper.vm.$nextTick()

    await wrapper.find('#setup-username').setValue('admin')
    await wrapper.find('#setup-password').setValue('password123')
    await wrapper.find('#setup-confirm-password').setValue('password123')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(wrapper.find('.error-message').exists()).toBe(true)
    expect(wrapper.find('.error-message').text()).toContain('Invalid input')
    // Should not advance to success step
    expect(wrapper.text()).toContain('Create Administrator Account')
  })

  // REQ-FESETUP-005: email is optional — omitted from payload when empty
  it('omits email from payload when email field is empty', async () => {
    const postSpy = vi.spyOn(clientModule, 'post').mockResolvedValueOnce({ username: 'admin', role: 'admin' })
    const router = makeRouter()
    const wrapper = mount(SetupView, { global: { plugins: [router] } })

    await wrapper.find('button').trigger('click')
    await wrapper.vm.$nextTick()

    await wrapper.find('#setup-username').setValue('admin')
    await wrapper.find('#setup-password').setValue('password123')
    await wrapper.find('#setup-confirm-password').setValue('password123')
    // email is left empty
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    const [, payload] = postSpy.mock.calls[0] as [string, Record<string, unknown>]
    expect(payload).not.toHaveProperty('email')
  })

  // REQ-FESETUP-005: email is included in payload when provided
  it('includes email in payload when email field is filled', async () => {
    const postSpy = vi.spyOn(clientModule, 'post').mockResolvedValueOnce({ username: 'admin', role: 'admin' })
    const router = makeRouter()
    const wrapper = mount(SetupView, { global: { plugins: [router] } })

    await wrapper.find('button').trigger('click')
    await wrapper.vm.$nextTick()

    await wrapper.find('#setup-username').setValue('admin')
    await wrapper.find('#setup-email').setValue('admin@example.com')
    await wrapper.find('#setup-password').setValue('password123')
    await wrapper.find('#setup-confirm-password').setValue('password123')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    const [, payload] = postSpy.mock.calls[0] as [string, Record<string, unknown>]
    expect(payload).toHaveProperty('email', 'admin@example.com')
  })
})
