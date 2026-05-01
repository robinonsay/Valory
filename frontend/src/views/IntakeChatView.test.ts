// @{"req": ["REQ-FECOURSE-040", "REQ-FECOURSE-041", "REQ-FECOURSE-042", "REQ-FECOURSE-043", "REQ-FECOURSE-070", "REQ-FECOURSE-071", "REQ-FECOURSE-400", "REQ-FECOURSE-410", "REQ-FECOURSE-420"]}

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import * as clientModule from '@/api/client'

// SSE mock state shared across tests — reset in beforeEach.
let capturedSSEUrl = ''
let capturedSSEOptions: { token: string; onEvent: Function; onError?: Function } | null = null
const mockSSEClose = vi.fn()

// Mock the useSSE composable so tests can control event delivery and inspect
// the URL/token used for connection without opening a real fetch stream.
vi.mock('@/composables/useSSE', () => ({
  useSSE: (url: string, options: { token: string; onEvent: Function; onError?: Function }) => {
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
    capturedSSEUrl = ''
    capturedSSEOptions = null

    // Provide an authenticated student token for every test.
    const auth = useAuthStore()
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  // 1. Connects to SSE on mount with correct URL including course ID
  it('connects to SSE on mount with URL containing the course ID', async () => {
    await mountView('course-42')

    expect(capturedSSEUrl).toContain('course-42')
    expect(capturedSSEUrl).toBe('/api/pipeline?course_id=course-42')
    expect(capturedSSEOptions?.token).toBe('test-token')
  })

  // 2. SSE message event appends to messages array
  it('appends a message to the list when SSE message event arrives', async () => {
    const { wrapper } = await mountView('course-42')

    capturedSSEOptions!.onEvent(
      'message',
      JSON.stringify({ role: 'agent', content: 'Hello student!' }),
      ''
    )

    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Hello student!')
  })

  // 3. SSE status_change event with syllabus_draft navigates to syllabus route
  it('navigates to /courses/:id/syllabus on status_change syllabus_draft event', async () => {
    const { wrapper, router } = await mountView('course-42')
    const pushSpy = vi.spyOn(router, 'push')

    capturedSSEOptions!.onEvent(
      'status_change',
      JSON.stringify({ status: 'syllabus_draft' }),
      ''
    )

    await wrapper.vm.$nextTick()

    expect(pushSpy).toHaveBeenCalledWith('/courses/course-42/syllabus')
  })

  // 4. Send button POSTs to correct endpoint
  it('POSTs to /api/v1/courses/:id/intake/message when Send is clicked', async () => {
    const mockPost = vi.spyOn(clientModule, 'post').mockResolvedValue({})
    const { wrapper } = await mountView('course-42')

    await wrapper.find('.chat-input').setValue('What is this course about?')
    await wrapper.find('.send-button').trigger('click')
    await wrapper.vm.$nextTick()

    expect(mockPost).toHaveBeenCalledWith(
      '/api/v1/courses/course-42/intake/message',
      { content: 'What is this course about?' },
      'test-token'
    )
  })

  // 5. User message added optimistically to messages before response
  it('optimistically adds the user message before the POST resolves', async () => {
    // Keep the POST pending so we can inspect the DOM before it resolves.
    vi.spyOn(clientModule, 'post').mockImplementation(() => new Promise(() => {}))
    const { wrapper } = await mountView('course-42')

    await wrapper.find('.chat-input').setValue('My optimistic message')
    await wrapper.find('.send-button').trigger('click')
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('My optimistic message')
  })

  // 6. SSE close() called on unmount
  it('calls sse.close() when the component is unmounted', async () => {
    const { wrapper } = await mountView('course-42')

    wrapper.unmount()

    expect(mockSSEClose).toHaveBeenCalledOnce()
  })

  // 7. SSE error shows "Connection lost" message
  it('shows "Connection lost. Please refresh the page." when SSE onError fires', async () => {
    const { wrapper } = await mountView('course-42')

    capturedSSEOptions!.onError!(new Error('network failure'))

    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Connection lost. Please refresh the page.')
  })
})
