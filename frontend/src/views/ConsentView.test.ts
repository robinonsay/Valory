// @{"req": ["REQ-FEAUTH-030", "REQ-FEAUTH-031", "REQ-FEAUTH-032", "REQ-FEAUTH-140", "REQ-FEAUTH-141", "REQ-FEAUTH-142"]}
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import ConsentView from './ConsentView.vue'
import { useAuthStore } from '@/stores/auth'
import * as clientModule from '@/api/client'

const mockPush = vi.fn()

const router = createRouter({
  history: createMemoryHistory(),
  routes: [
    {
      path: '/consent',
      component: ConsentView
    },
    {
      path: '/dashboard',
      component: { template: '<div>Dashboard</div>' }
    }
  ]
})

describe('ConsentView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    mockPush.mockClear()
  })

  it('renders consent text and "I agree" button', () => {
    const wrapper = mount(ConsentView, {
      global: {
        plugins: [router]
      }
    })

    expect(wrapper.text()).toContain('By using Valory, you agree to our terms of service and privacy policy.')
    expect(wrapper.find('button').text()).toBe('I agree')
  })

  it('disables the button while request is in flight', async () => {
    vi.spyOn(clientModule, 'post').mockImplementation(() => new Promise(() => {}))

    const wrapper = mount(ConsentView, {
      global: {
        plugins: [router]
      }
    })

    const button = wrapper.find('button')
    expect(button.attributes('disabled')).toBeUndefined()

    await button.trigger('click')
    await wrapper.vm.$nextTick()

    expect(button.attributes('disabled')).toBeDefined()
  })

  it('sends POST to /api/v1/consent with { version: "1.0" } body', async () => {
    const mockPost = vi.spyOn(clientModule, 'post').mockResolvedValue({})

    const wrapper = mount(ConsentView, {
      global: {
        plugins: [router]
      }
    })

    const auth = useAuthStore()
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    await wrapper.find('button').trigger('click')
    await wrapper.vm.$nextTick()

    expect(mockPost).toHaveBeenCalledWith('/api/v1/consent', { version: '1.0' }, 'test-token')
  })

  it('calls auth.setConsented() on successful 200 response', async () => {
    vi.spyOn(clientModule, 'post').mockResolvedValue({})

    const wrapper = mount(ConsentView, {
      global: {
        plugins: [router]
      }
    })

    const auth = useAuthStore()
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    expect(auth.isConsented).toBe(false)

    await wrapper.find('button').trigger('click')
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(auth.isConsented).toBe(true)
  })

  it('navigates to /dashboard on successful response', async () => {
    vi.spyOn(clientModule, 'post').mockResolvedValue({})
    const routerPushSpy = vi.spyOn(router, 'push')

    const wrapper = mount(ConsentView, {
      global: {
        plugins: [router]
      }
    })

    const auth = useAuthStore()
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    await wrapper.find('button').trigger('click')
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(routerPushSpy).toHaveBeenCalledWith('/courses')
  })

  it('displays error message on API error', async () => {
    const errorToThrow = new clientModule.ApiError(500, {})
    vi.spyOn(clientModule, 'post').mockRejectedValue(errorToThrow)

    const wrapper = mount(ConsentView, {
      global: {
        plugins: [router]
      }
    })

    const auth = useAuthStore()
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    await wrapper.find('button').trigger('click')
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(wrapper.text()).toContain('Failed to record consent. Please try again.')
  })

  it('re-enables button after error', async () => {
    const errorToThrow = new clientModule.ApiError(500, {})
    vi.spyOn(clientModule, 'post').mockRejectedValue(errorToThrow)

    const wrapper = mount(ConsentView, {
      global: {
        plugins: [router]
      }
    })

    const auth = useAuthStore()
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    const button = wrapper.find('button')

    await button.trigger('click')
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(button.attributes('disabled')).toBeUndefined()
  })
})
