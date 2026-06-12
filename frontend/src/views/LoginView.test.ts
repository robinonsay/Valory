// @{"req": ["REQ-FEAUTH-010", "REQ-FEAUTH-011", "REQ-FEAUTH-012", "REQ-FEAUTH-100", "REQ-FEAUTH-101", "REQ-FEAUTH-102", "REQ-FEAUTH-110", "REQ-FEAUTH-115", "REQ-FEAUTH-120"]}

import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import LoginView from './LoginView.vue'
import * as clientModule from '@/api/client'
import { useAuthStore } from '@/stores/auth'

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
  })

  it('should submit with valid credentials (200 response) and call auth.login, then redirect to /courses for student', async () => {
    const mockPost = vi.spyOn(clientModule, 'post').mockResolvedValue({
      token: 'test-token',
      role: 'student',
      expires_at: new Date(Date.now() + 3600000).toISOString()
    })
    const routerPushSpy = vi.fn()

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/login', component: { template: '<div></div>' } },
        { path: '/courses', component: { template: '<div></div>' } }
      ]
    })
    router.push = routerPushSpy

    const wrapper = mount(LoginView, {
      global: {
        plugins: [router]
      }
    })

    const auth = useAuthStore()

    await wrapper.find('#username').setValue('test@example.com')
    await wrapper.find('input[type="password"]').setValue('password123')
    await wrapper.find('form').trigger('submit')

    await Promise.resolve()
    await wrapper.vm.$nextTick()

    expect(mockPost).toHaveBeenCalledWith('/api/v1/auth/login', {
      username: 'test@example.com',
      password: 'password123'
    })
    expect(auth.token).toBe('test-token')
    expect(auth.role).toBe('student')
    expect(routerPushSpy).toHaveBeenCalledWith('/courses')

    mockPost.mockRestore()
  })

  it('should submit with admin credentials (200 response) and redirect to /admin/users', async () => {
    const mockPost = vi.spyOn(clientModule, 'post').mockResolvedValue({
      token: 'admin-token',
      role: 'admin',
      expires_at: new Date(Date.now() + 3600000).toISOString()
    })
    const routerPushSpy = vi.fn()

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/login', component: { template: '<div></div>' } },
        { path: '/admin/users', component: { template: '<div></div>' } }
      ]
    })
    router.push = routerPushSpy

    const wrapper = mount(LoginView, {
      global: {
        plugins: [router]
      }
    })

    const auth = useAuthStore()

    await wrapper.find('#username').setValue('admin@example.com')
    await wrapper.find('input[type="password"]').setValue('adminpass')
    await wrapper.find('form').trigger('submit')

    await Promise.resolve()
    await wrapper.vm.$nextTick()

    expect(auth.token).toBe('admin-token')
    expect(auth.role).toBe('admin')
    expect(routerPushSpy).toHaveBeenCalledWith('/admin/users')

    mockPost.mockRestore()
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
    expect(auth.token).toBeNull()

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
    let resolvePromise: (() => void) | null = null
    const mockPost = vi.spyOn(clientModule, 'post').mockImplementation(
      () => new Promise(resolve => {
        resolvePromise = () => resolve({
          token: 'test-token',
          role: 'student',
          expires_at: new Date(Date.now() + 3600000).toISOString()
        })
      })
    )

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

    if (resolvePromise) {
      resolvePromise()
    }
    await Promise.resolve()
    await Promise.resolve()
    await Promise.resolve()
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()
    expect(submitButton.text()).toBe('Sign in')
    expect(submitButton.element.hasAttribute('disabled')).toBe(false)

    mockPost.mockRestore()
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
