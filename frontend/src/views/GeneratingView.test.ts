// @{"req": ["REQ-FECOURSE-065", "REQ-FECOURSE-066", "REQ-FECOURSE-067", "REQ-FECOURSE-070", "REQ-FECOURSE-530", "REQ-FECOURSE-540", "REQ-FECOURSE-550", "REQ-FECOURSE-560"]}
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import GeneratingView from './GeneratingView.vue'
import { useAuthStore } from '@/stores/auth'
import * as useSSEModule from '@/composables/useSSE'

const mockRouterPush = vi.fn()

const router = createRouter({
  history: createMemoryHistory(),
  routes: [
    {
      path: '/courses/:id/generating',
      component: GeneratingView
    },
    {
      path: '/courses/:id/hub',
      component: { template: '<div>Hub</div>' }
    }
  ]
})

describe('GeneratingView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    mockRouterPush.mockClear()
  })

  it('connects to SSE on mount', async () => {
    const mockUseSSE = vi.fn().mockReturnValue({ close: vi.fn() })
    vi.spyOn(useSSEModule, 'useSSE').mockImplementation(mockUseSSE)

    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    router.push('/courses/test-course-id/generating')
    await router.isReady()

    const wrapper = mount(GeneratingView, {
      global: {
        plugins: [router]
      }
    })

    await wrapper.vm.$nextTick()

    expect(mockUseSSE).toHaveBeenCalled()
    const callArgs = mockUseSSE.mock.calls[0]
    expect(callArgs[0]).toBe('/api/v1/courses/test-course-id/events')
    // Cookie-based auth: token is not passed; SSE uses __Host-session cookie automatically
    expect(callArgs[1].token == null).toBe(true)
  })

  it('updates progress percentage on progress event', async () => {
    let capturedOnEvent: ((eventType: string, data: string) => void) | null = null

    const mockClose = vi.fn()
    vi.spyOn(useSSEModule, 'useSSE').mockImplementation((_url, options) => {
      capturedOnEvent = options.onEvent
      return { close: mockClose }
    })

    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    router.push('/courses/test-course-id/generating')
    await router.isReady()

    const wrapper = mount(GeneratingView, {
      global: {
        plugins: [router]
      }
    })

    await wrapper.vm.$nextTick()

    expect(capturedOnEvent).not.toBeNull()
    capturedOnEvent!('progress', JSON.stringify({ percent: 50, message: 'Generating content' }))
    await wrapper.vm.$nextTick()

    expect(wrapper.vm.progressPercent).toBe(50)
    expect(wrapper.text()).toContain('Generating content')
  })

  it('navigates to hub when status_change event with active status is received', async () => {
    let capturedOnEvent: ((eventType: string, data: string) => void) | null = null

    const mockClose = vi.fn()
    vi.spyOn(useSSEModule, 'useSSE').mockImplementation((_url, options) => {
      capturedOnEvent = options.onEvent
      return { close: mockClose }
    })

    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    router.push('/courses/test-course-id/generating')
    await router.isReady()

    const routerPushSpy = vi.spyOn(router, 'push')

    const wrapper = mount(GeneratingView, {
      global: {
        plugins: [router]
      }
    })

    await wrapper.vm.$nextTick()

    expect(capturedOnEvent).not.toBeNull()
    capturedOnEvent!('status_change', JSON.stringify({ status: 'active' }))
    await wrapper.vm.$nextTick()

    expect(routerPushSpy).toHaveBeenCalledWith('/courses/test-course-id/hub')
  })

  it('calls sse.close() on unmount', async () => {
    const mockClose = vi.fn()
    vi.spyOn(useSSEModule, 'useSSE').mockReturnValue({ close: mockClose })

    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    router.push('/courses/test-course-id/generating')
    await router.isReady()

    const wrapper = mount(GeneratingView, {
      global: {
        plugins: [router]
      }
    })

    await wrapper.vm.$nextTick()

    wrapper.unmount()
    await wrapper.vm.$nextTick()

    expect(mockClose).toHaveBeenCalled()
  })

  it('displays error message when SSE error occurs', async () => {
    let capturedOnError: ((err: Error) => void) | null = null

    const mockClose = vi.fn()
    vi.spyOn(useSSEModule, 'useSSE').mockImplementation((_url, options) => {
      capturedOnError = options.onError
      return { close: mockClose }
    })

    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    router.push('/courses/test-course-id/generating')
    await router.isReady()

    const wrapper = mount(GeneratingView, {
      global: {
        plugins: [router]
      }
    })

    await wrapper.vm.$nextTick()

    expect(capturedOnError).not.toBeNull()
    capturedOnError!(new Error('Connection failed'))
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Generation failed. Please contact support.')
  })
})
