// @{"req": ["REQ-FEAUTH-010", "REQ-FEAUTH-011", "REQ-FEAUTH-012", "REQ-FEAUTH-100", "REQ-FEAUTH-101", "REQ-FEAUTH-102", "REQ-FEAUTH-110", "REQ-FEAUTH-115", "REQ-FEAUTH-120", "REQ-FEAUTH-171"]}

import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import LoginView from './LoginView.vue'
import * as clientModule from '@/api/client'
import { useAuthStore } from '@/stores/auth'

// sessionResponse is the shape returned by GET /api/v1/auth/session (restoreSession)
const studentSession = {
  user_id: 'user-123',
  username: 'student1',
  role: 'student',
  consented: true,
  expires_at: new Date(Date.now() + 3600000).toISOString()
}

const adminSession = {
  user_id: 'admin-456',
  username: 'admin1',
  role: 'admin',
  consented: true,
  expires_at: new Date(Date.now() + 3600000).toISOString()
}

describe('LoginView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('should render username and password fields and a submit button', () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/', component: { template: '<div></div>' } }]
    })

    const wrapper = mount(LoginView, {
      global: {
        plugins: [router]
      }
    })

    const usernameInput = wrapper.find('#username')
    const passwordInput = wrapper.find('input[type="password"]')
    const submitButton = wrapper.find('button[type="submit"]')

    expect(usernameInput.exists()).toBe(true)
    expect(usernameInput.attributes('required')).toBeDefined()
    expect(passwordInput.exists()).toBe(true)
    expect(passwordInput.attributes('required')).toBeDefined()
    expect(submitButton.exists()).toBe(true)

    const logo = wrapper.find('.login-logo')
    expect(logo.exists()).toBe(true)
    expect(logo.attributes('src')).toContain('valory.svg')
    expect(logo.attributes('alt')).toBe('Valory')
  })


  it('should show inline error "Invalid credentials" on 401 response', async () => {
    const mockPost = vi.spyOn(clientModule, 'post').mockRejectedValue(
      new clientModule.ApiError(401, { error: 'unauthorized' })
    )

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/', component: { template: '<div></div>' } }]
    })

    const wrapper = mount(LoginView, {
      global: {
        plugins: [router]
      }
    })

    const auth = useAuthStore()

    await wrapper.find('#username').setValue('test@example.com')
    await wrapper.find('input[type="password"]').setValue('wrong')
    await wrapper.find('form').trigger('submit')

    await Promise.resolve()
    await wrapper.vm.$nextTick()

    const errorElement = wrapper.find('.error-message')
    expect(errorElement.exists()).toBe(true)
    expect(errorElement.text()).toBe('Invalid credentials')
    // No session — store should remain unauthenticated
    expect(auth.isAuthenticated).toBe(false)

    mockPost.mockRestore()
  })

  it('should show inline error on 429 response', async () => {
    const mockPost = vi.spyOn(clientModule, 'post').mockRejectedValue(
      new clientModule.ApiError(429, { error: 'rate limited' })
    )

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/', component: { template: '<div></div>' } }]
    })

    const wrapper = mount(LoginView, {
      global: {
        plugins: [router]
      }
    })

    await wrapper.find('#username').setValue('test@example.com')
    await wrapper.find('input[type="password"]').setValue('password123')
    await wrapper.find('form').trigger('submit')

    await Promise.resolve()
    await wrapper.vm.$nextTick()

    const errorElement = wrapper.find('.error-message')
    expect(errorElement.exists()).toBe(true)
    expect(errorElement.text()).toBe('Too many attempts. Please try again later.')

    mockPost.mockRestore()
  })

  it('should disable submit button while request is in flight', async () => {
    let resolvePost: (() => void) | null = null
    const mockPost = vi.spyOn(clientModule, 'post').mockImplementation(
      () => new Promise(resolve => {
        resolvePost = () => resolve(undefined)
      })
    )
    // restoreSession resolves immediately after post resolves
    const mockGet = vi.spyOn(clientModule, 'get').mockResolvedValue(studentSession)

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/login', component: { template: '<div></div>' } },
        { path: '/courses', component: { template: '<div></div>' } }
      ]
    })

    const wrapper = mount(LoginView, {
      global: {
        plugins: [router]
      }
    })

    const submitButton = wrapper.find('button[type="submit"]')
    expect(submitButton.text()).toBe('Sign in')

    await wrapper.find('#username').setValue('test@example.com')
    await wrapper.find('input[type="password"]').setValue('password123')
    await wrapper.find('form').trigger('submit')

    await wrapper.vm.$nextTick()
    expect(submitButton.text()).toBe('Signing in...')
    expect(submitButton.element.hasAttribute('disabled')).toBe(true)

    if (resolvePost) {
      resolvePost()
    }
    // Flush: post resolves → auth.login() → restoreSession() → get() resolves
    // → store updated → finally block → isLoading = false
    await flushPromises()
    await wrapper.vm.$nextTick()
    expect(submitButton.text()).toBe('Sign in')
    expect(submitButton.element.hasAttribute('disabled')).toBe(false)

    mockPost.mockRestore()
    mockGet.mockRestore()
  })

  it('should clear error when user starts typing', async () => {
    const mockPost = vi.spyOn(clientModule, 'post').mockRejectedValue(
      new clientModule.ApiError(401, { error: 'unauthorized' })
    )

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/', component: { template: '<div></div>' } }]
    })

    const wrapper = mount(LoginView, {
      global: {
        plugins: [router]
      }
    })

    await wrapper.find('#username').setValue('test@example.com')
    await wrapper.find('input[type="password"]').setValue('wrong')
    await wrapper.find('form').trigger('submit')

    await Promise.resolve()
    await wrapper.vm.$nextTick()

    let errorElement = wrapper.find('.error-message')
    expect(errorElement.exists()).toBe(true)

    await wrapper.find('#username').trigger('input')
    await wrapper.vm.$nextTick()

    errorElement = wrapper.find('.error-message')
    expect(errorElement.exists()).toBe(false)

    mockPost.mockRestore()
  })
})
