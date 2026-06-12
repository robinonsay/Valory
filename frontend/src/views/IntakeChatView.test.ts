// @{"req": ["REQ-FECOURSE-026", "REQ-FECOURSE-027", "REQ-FECOURSE-028", "REQ-FECOURSE-070", "REQ-FECOURSE-071", "REQ-FECOURSE-220", "REQ-FECOURSE-221", "REQ-FECOURSE-222", "REQ-FECOURSE-223", "REQ-FECOURSE-224", "REQ-FECOURSE-225", "REQ-FECOURSE-230", "REQ-FECOURSE-231", "REQ-FECOURSE-240", "REQ-FECOURSE-250", "REQ-FECOURSE-251", "REQ-FECOURSE-252", "REQ-FECOURSE-260", "REQ-FECOURSE-261", "REQ-FECOURSE-262", "REQ-FECOURSE-263", "REQ-FECOURSE-264", "REQ-FECOURSE-270"]}

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import * as clientModule from '@/api/client'

// SSE mock state shared across tests — reset in beforeEach.
let capturedSSEUrl = ''
let capturedSSEOptions: { token?: string | null; onEvent: Function; onError?: Function } | null = null
const mockSSEClose = vi.fn()

// Mock the useSSE composable so tests can control event delivery and inspect
// the URL/token used for connection without opening a real fetch stream.
vi.mock('@/composables/useSSE', () => ({
  useSSE: (url: string, options: { token?: string | null; onEvent: Function; onError?: Function }) => {
    capturedSSEUrl = url
    capturedSSEOptions = options
    return { close: mockSSEClose }
  }
}))

function makeRouter(courseId = 'course-42') {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/courses/:id/intake',
        name: 'course-intake',
        component: { template: '<div></div>' }
      },
      {
        path: '/courses/:id/syllabus',
        name: 'course-syllabus',
        component: { template: '<div></div>' }
      },
      {
        path: '/courses/:id',
        name: 'course-hub',
        component: { template: '<div></div>' }
      },
      {
        path: '/courses',
        name: 'courses',
        component: { template: '<div></div>' }
      }
    ]
  })
}

async function mountView(courseId = 'course-42') {
  const { default: IntakeChatView } = await import('./IntakeChatView.vue')
  const router = makeRouter(courseId)
  await router.push(`/courses/${courseId}/intake`)
  await router.isReady()

  const wrapper = mount(IntakeChatView, {
    global: { plugins: [router] }
  })
  return { wrapper, router }
}

describe('IntakeChatView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    vi.useFakeTimers()
    capturedSSEUrl = ''
    capturedSSEOptions = null

    // Provide an authenticated student token for every test.
    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  // 1. Loads chat history on mount
  // @{"verifies": ["REQ-FECOURSE-026", "REQ-FECOURSE-260", "REQ-FECOURSE-261"]}
  it('loads and renders chat history on mount', async () => {
    const mockGet = vi.spyOn(clientModule, 'get').mockResolvedValue({
      messages: [
        {
          id: 'msg-1',
          role: 'assistant',
          content: 'Welcome to the intake!',
          created_at: '2026-06-12T10:00:00Z'
        },
        {
          id: 'msg-2',
          role: 'student',
          content: 'I want to learn about AI.',
          created_at: '2026-06-12T10:00:05Z'
        }
      ]
    })

    const { wrapper } = await mountView('course-42')
    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    expect(mockGet).toHaveBeenCalledWith(
      '/api/v1/courses/course-42/chat/history'
    )
    expect(wrapper.text()).toContain('Welcome to the intake!')
    expect(wrapper.text()).toContain('I want to learn about AI.')
  })

  // 2. Handles empty history gracefully
  // @{"verifies": ["REQ-FECOURSE-260", "REQ-FECOURSE-261"]}
  it('handles empty history response without error', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValue({ messages: [] })

    const { wrapper } = await mountView('course-42')
    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    expect(wrapper.find('.chat-container').exists()).toBe(true)
  })

  // 3. Continues on history fetch failure
  // @{"verifies": ["REQ-FECOURSE-260"]}
  it('continues rendering even if history fetch fails', async () => {
    vi.spyOn(clientModule, 'get').mockRejectedValue(
      new clientModule.ApiError(500, { error: 'server error' })
    )

    const { wrapper } = await mountView('course-42')
    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    expect(wrapper.find('.chat-container').exists()).toBe(true)
  })

  // 4. Connects to SSE on mount
  // @{"verifies": ["REQ-FECOURSE-020", "REQ-FECOURSE-200"]}
  it('connects to SSE on mount with correct URL and token', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValue({ messages: [] })
    const { wrapper } = await mountView('course-42')
    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    expect(capturedSSEUrl).toBe('/api/v1/courses/course-42/events')
    // Cookie-based auth: token is null/undefined; SSE uses __Host-session cookie
    expect(capturedSSEOptions?.token == null).toBe(true)
  })

  // 5. Send button POSTs to correct endpoint and appends reply
  // @{"verifies": ["REQ-FECOURSE-022", "REQ-FECOURSE-220", "REQ-FECOURSE-224"]}
  it('POSTs message and appends reply from response body', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValue({ messages: [] })
    const mockPost = vi.spyOn(clientModule, 'post').mockResolvedValue({
      reply: "Great choice! What's your experience level?",
      course_status: 'intake'
    })

    const { wrapper } = await mountView('course-42')
    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    await wrapper.find('.chat-input').setValue('I want to learn Python.')
    await wrapper.find('.send-button').trigger('click')
    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    expect(mockPost).toHaveBeenCalledWith(
      '/api/v1/courses/course-42/chat',
      { message: 'I want to learn Python.' }
    )
    expect(wrapper.text()).toContain("Great choice! What's your experience level?")
  })

  // 6. User message added optimistically
  // @{"verifies": ["REQ-FECOURSE-223"]}
  it('optimistically adds the user message before POST resolves', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValue({ messages: [] })
    vi.spyOn(clientModule, 'post').mockImplementation(() => new Promise(() => {}))

    const { wrapper } = await mountView('course-42')
    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    await wrapper.find('.chat-input').setValue('My optimistic message')
    await wrapper.find('.send-button').trigger('click')
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('My optimistic message')
  })

  // 7. Shows typing indicator while sending
  // @{"verifies": ["REQ-FECOURSE-222"]}
  it('displays typing indicator while POST is in flight', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValue({ messages: [] })
    vi.spyOn(clientModule, 'post').mockImplementation(() => new Promise(() => {}))

    const { wrapper } = await mountView('course-42')
    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    await wrapper.find('.chat-input').setValue('Pending message')
    await wrapper.find('.send-button').trigger('click')
    await wrapper.vm.$nextTick()

    const typingIndicators = wrapper.findAll('.typing-indicator')
    expect(typingIndicators.length).toBeGreaterThan(0)
  })

  // 8. Disables input while sending
  // @{"verifies": ["REQ-FECOURSE-221"]}
  it('disables input while POST is in flight', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValue({ messages: [] })
    vi.spyOn(clientModule, 'post').mockImplementation(() => new Promise(() => {}))

    const { wrapper } = await mountView('course-42')
    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    const input = wrapper.find('.chat-input')
    const button = wrapper.find('.send-button')

    expect(input.attributes('disabled')).toBeUndefined()
    expect(button.attributes('disabled')).toBeUndefined()

    await input.setValue('Message')
    await button.trigger('click')
    await wrapper.vm.$nextTick()

    expect(input.attributes('disabled')).toBeDefined()
    expect(button.attributes('disabled')).toBeDefined()
  })

  // 9. Shows error message on POST failure
  // @{"verifies": ["REQ-FECOURSE-225"]}
  it('displays error message when POST fails and does not remove optimistic message', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValue({ messages: [] })
    vi.spyOn(clientModule, 'post').mockRejectedValue(
      new clientModule.ApiError(500, { error: 'server error' })
    )

    const { wrapper } = await mountView('course-42')
    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    await wrapper.find('.chat-input').setValue('Failed message')
    await wrapper.find('.send-button').trigger('click')
    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Failed to send message')
    expect(wrapper.text()).toContain('Failed message')
  })

  // 10. Clears error message on dismiss
  // @{"verifies": ["REQ-FECOURSE-225"]}
  it('dismisses error message when dismiss button is clicked', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValue({ messages: [] })
    vi.spyOn(clientModule, 'post').mockRejectedValue(
      new clientModule.ApiError(500, { error: 'server error' })
    )

    const { wrapper } = await mountView('course-42')
    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    await wrapper.find('.chat-input').setValue('Failed message')
    await wrapper.find('.send-button').trigger('click')
    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Failed to send message')

    await wrapper.find('.error-dismiss').trigger('click')
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).not.toContain('Failed to send message')
  })

  // 11. Redirects on POST course_status change
  // @{"verifies": ["REQ-FECOURSE-024"]}
  it('redirects to syllabus when POST returns course_status syllabus_draft', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValue({ messages: [] })
    vi.spyOn(clientModule, 'post').mockResolvedValue({
      reply: 'INTAKE_COMPLETE',
      course_status: 'syllabus_draft'
    })

    const { wrapper, router } = await mountView('course-42')
    const replaceSpy = vi.spyOn(router, 'replace')
    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    await wrapper.find('.chat-input').setValue('Final answer')
    await wrapper.find('.send-button').trigger('click')
    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    expect(replaceSpy).toHaveBeenCalledWith('/courses/course-42/syllabus')
  })

  // 12. Parses SSE envelope correctly and redirects on status_change
  // @{"verifies": ["REQ-FECOURSE-024"]}
  it('parses SSE envelope and redirects on status_change event', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValue({ messages: [] })
    const { wrapper, router } = await mountView('course-42')
    await vi.runAllTimersAsync()
    const replaceSpy = vi.spyOn(router, 'replace')

    const envelope = {
      id: 'evt-1',
      agent_run_id: 'run-1',
      event_type: 'status_change',
      payload: { status: 'syllabus_draft' },
      emitted_at: '2026-06-12T10:00:10Z'
    }

    capturedSSEOptions!.onEvent('status_change', JSON.stringify(envelope), '')
    await wrapper.vm.$nextTick()

    expect(replaceSpy).toHaveBeenCalledWith('/courses/course-42/syllabus')
  })

  // 13. Redirects to correct route per status_change status
  // @{"verifies": ["REQ-FECOURSE-024"]}
  it('redirects to appropriate route based on status_change payload', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValue({ messages: [] })
    const { wrapper, router } = await mountView('course-42')
    await vi.runAllTimersAsync()
    const replaceSpy = vi.spyOn(router, 'replace')

    const envelope = {
      id: 'evt-1',
      agent_run_id: 'run-1',
      event_type: 'status_change',
      payload: { status: 'active' },
      emitted_at: '2026-06-12T10:00:10Z'
    }

    capturedSSEOptions!.onEvent('status_change', JSON.stringify(envelope), '')
    await wrapper.vm.$nextTick()

    expect(replaceSpy).toHaveBeenCalledWith('/courses/course-42')
  })

  // 14. SSE close() called on unmount
  // @{"verifies": ["REQ-FECOURSE-073", "REQ-FECOURSE-730"]}
  it('calls sse.close() when the component is unmounted', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValue({ messages: [] })
    const { wrapper } = await mountView('course-42')
    await vi.runAllTimersAsync()

    wrapper.unmount()

    expect(mockSSEClose).toHaveBeenCalledOnce()
  })

  // 15. SSE error shows "Connection lost" message
  // @{"verifies": ["REQ-FECOURSE-025"]}
  it('shows "Connection lost. Please refresh the page." when SSE onError fires', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValue({ messages: [] })
    const { wrapper } = await mountView('course-42')
    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    capturedSSEOptions!.onError!(new Error('network failure'))
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Connection lost. Please refresh the page.')
  })

  // 16. Shows preparing indicator when history is empty and polls
  // @{"verifies": ["REQ-FECOURSE-028", "REQ-FECOURSE-262"]}
  it('shows preparing indicator and polls when history is empty on mount', async () => {
    const mockGet = vi.spyOn(clientModule, 'get').mockResolvedValue({ messages: [] })
    const { wrapper } = await mountView('course-42')
    // Allow initial load to complete
    await vi.advanceTimersByTimeAsync(100)
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Your professor is preparing your course…')
    expect(wrapper.find('.typing-indicator').exists()).toBe(true)

    // Advance timers to trigger first poll interval
    await vi.advanceTimersByTimeAsync(2600)
    await wrapper.vm.$nextTick()

    // Should have called get twice: once on mount, once on first poll
    expect(mockGet).toHaveBeenCalledTimes(2)
  })

  // 17. Stops polling when message arrives via history fetch
  // @{"verifies": ["REQ-FECOURSE-262"]}
  it('stops polling and renders message when history is fetched during polling', async () => {
    const mockGet = vi
      .spyOn(clientModule, 'get')
      .mockResolvedValueOnce({ messages: [] })
      .mockResolvedValueOnce({
        messages: [
          {
            id: 'msg-1',
            role: 'assistant',
            content: "Hello! I'm your professor.",
            created_at: '2026-06-12T10:00:00Z'
          }
        ]
      })

    const { wrapper } = await mountView('course-42')
    await vi.advanceTimersByTimeAsync(100)
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Your professor is preparing your course…')

    await vi.advanceTimersByTimeAsync(2600)
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain("Hello! I'm your professor.")
    expect(wrapper.text()).not.toContain('Your professor is preparing your course…')
  })

  // 18. In-flight poll response is discarded when student sends during preparation
  // @{"verifies": ["REQ-FECOURSE-262"]}
  it('discards stale poll response when student sends message during in-flight GET', async () => {
    let resolvePendingGet: Function | null = null
    const pendingGetPromise = new Promise<{ messages: [] }>(resolve => {
      resolvePendingGet = resolve
    })

    let callCount = 0
    const mockGet = vi.spyOn(clientModule, 'get').mockImplementation(() => {
      callCount++
      // First call on mount returns empty; subsequent calls hang until test resolves them
      if (callCount === 1) {
        return Promise.resolve({ messages: [] })
      }
      return pendingGetPromise
    })

    const mockPost = vi.spyOn(clientModule, 'post').mockResolvedValue({
      reply: 'Great!',
      course_status: 'intake'
    })

    const { wrapper } = await mountView('course-42')
    await vi.advanceTimersByTimeAsync(100)
    await wrapper.vm.$nextTick()

    // Confirm polling started
    expect(wrapper.text()).toContain('Your professor is preparing your course…')
    expect(mockGet).toHaveBeenCalledOnce()

    // Advance to trigger the first poll tick (interval is 2500ms)
    await vi.advanceTimersByTimeAsync(2600)
    await wrapper.vm.$nextTick()

    // Verify the GET is now in-flight (second call is pending)
    expect(mockGet).toHaveBeenCalledTimes(2)

    // Student sends a message — this adds an optimistic user message
    await wrapper.find('.chat-input').setValue('Hello!')
    await wrapper.find('.send-button').trigger('click')
    await vi.advanceTimersByTimeAsync(100)
    await wrapper.vm.$nextTick()

    // Verify optimistic message appears
    expect(wrapper.text()).toContain('Hello!')

    // Now resolve the pending GET with empty history
    // This simulates a late response that should be discarded
    resolvePendingGet!({ messages: [] })
    await vi.advanceTimersByTimeAsync(100)
    await wrapper.vm.$nextTick()

    // Verify the optimistic message is still there (not clobbered)
    expect(wrapper.text()).toContain('Hello!')

    // Advance past the next poll interval to verify no more GETs are made
    await vi.advanceTimersByTimeAsync(3000)
    await wrapper.vm.$nextTick()

    // POST should be called once, and no further GETs after the late response
    expect(mockPost).toHaveBeenCalledOnce()
    // Should have exactly 2 GETs: mount + first poll tick; no third poll attempt
    expect(mockGet).toHaveBeenCalledTimes(2)
  })

  // 19. Shows bounded wait hint after 120s of empty polling
  // @{"verifies": ["REQ-FECOURSE-263"]}
  it('shows bounded-wait hint after 120 seconds with no messages', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValue({ messages: [] })
    const { wrapper } = await mountView('course-42')
    await vi.advanceTimersByTimeAsync(100)
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Your professor is preparing your course…')

    // Advance past the 120 second bounded wait timeout
    await vi.advanceTimersByTimeAsync(120100)
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Your professor is taking longer than expected')
    expect(wrapper.text()).toContain('feel free to introduce yourself')
  })
})
