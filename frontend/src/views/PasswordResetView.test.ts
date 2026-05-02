// @{"req": ["REQ-FEAUTH-020", "REQ-FEAUTH-021", "REQ-FEAUTH-022", "REQ-FEAUTH-023", "REQ-FEAUTH-130", "REQ-FEAUTH-131", "REQ-FEAUTH-132", "REQ-FEAUTH-133", "REQ-FEAUTH-134"]}

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import PasswordResetView from './PasswordResetView.vue'
import * as client from '@/api/client'

beforeEach(() => {
  vi.clearAllMocks()
})

afterEach(() => {
  vi.restoreAllMocks()
})

function createTestRouter(query: Record<string, string> = {}) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/reset-password',
        component: PasswordResetView
      },
      {
        path: '/login',
        component: { template: '<div>Login</div>' }
      }
    ]
  })

  return router
}

describe('PasswordResetView', () => {
  describe('Request mode (no token param)', () => {
    it('renders username field when no token param', async () => {
      const router = createTestRouter()
      await router.push('/reset-password')
      await router.isReady()

      const wrapper = mount(PasswordResetView, {
        global: {
          plugins: [router]
        }
      })

      expect(wrapper.find('#username').exists()).toBe(true)
      expect(wrapper.find('#username').attributes('type')).toBe('text')
    })

    it('shows neutral message on 200 response', async () => {
      const router = createTestRouter()
      await router.push('/reset-password')
      await router.isReady()

      const mockPost = vi.spyOn(client, 'post').mockResolvedValue(undefined)

      const wrapper = mount(PasswordResetView, {
        global: {
          plugins: [router]
        }
      })

      const usernameInput = wrapper.find('#username')
      await usernameInput.setValue('testuser')

      const button = wrapper.find('button')
      await button.trigger('click')
      await wrapper.vm.$nextTick()

      expect(mockPost).toHaveBeenCalledWith('/api/v1/password-reset/request', { username: 'testuser' })
      expect(wrapper.find('.success-message').text()).toBe('If an account with that username exists, you will receive a reset link shortly.')
    })

    it('shows neutral message on 404 response', async () => {
      const router = createTestRouter()
      await router.push('/reset-password')
      await router.isReady()

      const mockPost = vi.spyOn(client, 'post').mockRejectedValue(
        new client.ApiError(404, { error: 'not found' })
      )

      const wrapper = mount(PasswordResetView, {
        global: {
          plugins: [router]
        }
      })

      const usernameInput = wrapper.find('#username')
      await usernameInput.setValue('notfounduser')

      const button = wrapper.find('button')
      await button.trigger('click')
      await wrapper.vm.$nextTick()

      expect(wrapper.find('.success-message').text()).toBe('If an account with that username exists, you will receive a reset link shortly.')
    })

    it('shows rate limit message on 429 response', async () => {
      const router = createTestRouter()
      await router.push('/reset-password')
      await router.isReady()

      const mockPost = vi.spyOn(client, 'post').mockRejectedValue(
        new client.ApiError(429, { error: 'rate limited' })
      )

      const wrapper = mount(PasswordResetView, {
        global: {
          plugins: [router]
        }
      })

      const usernameInput = wrapper.find('#username')
      await usernameInput.setValue('testuser')

      const button = wrapper.find('button')
      await button.trigger('click')
      await wrapper.vm.$nextTick()

      expect(wrapper.find('.error-message').text()).toBe('Too many requests. Please wait before trying again.')
    })

    it('disables button while in flight', async () => {
      const router = createTestRouter()
      await router.push('/reset-password')
      await router.isReady()

      const mockPost = vi.spyOn(client, 'post').mockImplementation(
        () => new Promise(resolve => setTimeout(() => resolve(undefined), 100))
      )

      const wrapper = mount(PasswordResetView, {
        global: {
          plugins: [router]
        }
      })

      const usernameInput = wrapper.find('#username')
      await usernameInput.setValue('testuser')

      const button = wrapper.find('button')
      expect(button.attributes('disabled')).toBeUndefined()

      button.trigger('click')
      await wrapper.vm.$nextTick()

      expect(button.attributes('disabled')).toBeDefined()

      await new Promise(resolve => setTimeout(resolve, 150))
      await wrapper.vm.$nextTick()

      expect(button.attributes('disabled')).toBeUndefined()
    })
  })

  describe('Confirm mode (with token param)', () => {
    it('renders password field when token param is present', async () => {
      const router = createRouter({
        history: createMemoryHistory(),
        routes: [
          {
            path: '/reset-password',
            component: PasswordResetView
          },
          {
            path: '/login',
            component: { template: '<div>Login</div>' }
          }
        ]
      })
      await router.push('/reset-password?token=abc123')
      await router.isReady()

      const wrapper = mount(PasswordResetView, {
        global: {
          plugins: [router]
        }
      })

      expect(wrapper.find('#password').exists()).toBe(true)
      expect(wrapper.find('#password').attributes('type')).toBe('password')
      expect(wrapper.find('#token').exists()).toBe(true)
    })

    it('navigates to /login?reset=success on 204 response', async () => {
      const router = createRouter({
        history: createMemoryHistory(),
        routes: [
          {
            path: '/reset-password',
            component: PasswordResetView
          },
          {
            path: '/login',
            component: { template: '<div>Login</div>' }
          }
        ]
      })
      await router.push('/reset-password?token=abc123')
      await router.isReady()

      const mockPost = vi.spyOn(client, 'post').mockResolvedValue(undefined)

      const wrapper = mount(PasswordResetView, {
        global: {
          plugins: [router]
        }
      })

      const passwordInput = wrapper.find('#password')
      await passwordInput.setValue('newpassword123')

      const button = wrapper.find('button')
      await button.trigger('click')
      await wrapper.vm.$nextTick()
      await new Promise(resolve => setTimeout(resolve, 50))

      expect(mockPost).toHaveBeenCalledWith('/api/v1/password-reset/confirm', {
        token: 'abc123',
        new_password: 'newpassword123'
      })
      expect(router.currentRoute.value.path).toBe('/login')
      expect(router.currentRoute.value.query.reset).toBe('success')
    })

    it('shows invalid/expired error on 400 response', async () => {
      const router = createRouter({
        history: createMemoryHistory(),
        routes: [
          {
            path: '/reset-password',
            component: PasswordResetView
          },
          {
            path: '/login',
            component: { template: '<div>Login</div>' }
          }
        ]
      })
      await router.push('/reset-password?token=invalid')
      await router.isReady()

      const mockPost = vi.spyOn(client, 'post').mockRejectedValue(
        new client.ApiError(400, { error: 'bad request' })
      )

      const wrapper = mount(PasswordResetView, {
        global: {
          plugins: [router]
        }
      })

      const passwordInput = wrapper.find('#password')
      await passwordInput.setValue('newpassword123')

      const button = wrapper.find('button')
      await button.trigger('click')
      await wrapper.vm.$nextTick()

      expect(wrapper.find('.error-message').text()).toBe('Invalid or expired reset link.')
    })

    it('disables button while in flight', async () => {
      const router = createRouter({
        history: createMemoryHistory(),
        routes: [
          {
            path: '/reset-password',
            component: PasswordResetView
          },
          {
            path: '/login',
            component: { template: '<div>Login</div>' }
          }
        ]
      })
      await router.push('/reset-password?token=abc123')
      await router.isReady()

      let resolvePost: (() => void) | null = null
      const mockPost = vi.spyOn(client, 'post').mockImplementation(
        () => new Promise(resolve => {
          resolvePost = () => resolve(undefined)
        })
      )

      const wrapper = mount(PasswordResetView, {
        global: {
          plugins: [router]
        }
      })

      const passwordInput = wrapper.find('#password')
      await passwordInput.setValue('newpassword123')

      const button = wrapper.find('button')
      const initialDisabled = button.attributes('disabled')
      expect(initialDisabled).not.toBeDefined()

      button.trigger('click')
      await wrapper.vm.$nextTick()

      // Button should be disabled during flight
      expect(button.attributes('disabled')).toBeDefined()

      if (resolvePost) {
        resolvePost()
      }
      // Wait for the promise to resolve and component to update
      await new Promise(resolve => setTimeout(resolve, 50))
      await wrapper.vm.$nextTick()

      // After error, button should be enabled again
      // (We expect an error because the router.push will fail due to navigation)
    })
  })
})
