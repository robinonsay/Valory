// @{"req": ["REQ-FECOURSE-055", "REQ-FECOURSE-056", "REQ-FECOURSE-057", "REQ-FECOURSE-060", "REQ-FECOURSE-460", "REQ-FECOURSE-470", "REQ-FECOURSE-480", "REQ-FECOURSE-490", "REQ-FECOURSE-491", "REQ-FECOURSE-510", "REQ-FECOURSE-520", "REQ-FECOURSE-629"]}

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import SyllabusView from './SyllabusView.vue'
import * as clientModule from '@/api/client'
import { useAuthStore } from '@/stores/auth'

// Mock renderAsciidoc so tests do not load the real @asciidoctor/core chunk.
// The mock mimics real behavior: it converts the raw adoc content_adoc field
// into HTML that would result from Asciidoctor conversion, specifically making
// == headings into <h2> elements so tests can assert on rendered output.
vi.mock('@/utils/renderAsciidoc', () => ({
  renderAsciidoc: async (source: string) => {
    // Minimal conversion: turn '== Heading' lines into <h2>Heading</h2>
    // and '= Title' into <h1>Title</h1> so view tests can verify rendered HTML.
    return source
      .split('\n')
      .map(line => {
        if (/^== (.+)/.test(line)) return `<h2>${line.replace(/^== /, '')}</h2>`
        if (/^= (.+)/.test(line)) return `<h1>${line.replace(/^= /, '')}</h1>`
        if (line.trim()) return `<p>${line}</p>`
        return ''
      })
      .join('')
  }
}))

describe('SyllabusView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  function createMountOptions() {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        {
          path: '/courses/:id/syllabus',
          name: 'course-syllabus',
          component: { template: '<div></div>' }
        },
        {
          path: '/courses/:id/generating',
          name: 'course-generating',
          component: { template: '<div></div>' }
        }
      ]
    })

    return {
      global: {
        plugins: [router],
        stubs: {
          teleport: true
        }
      },
      props: {}
    }
  }

  it('fetches and renders syllabus content on mount', async () => {
    const mockGet = vi.spyOn(clientModule, 'get').mockResolvedValue({
      id: 'syll-123',
      course_id: 'course-123',
      content_adoc: '= Syllabus\n\n== Introduction to Vue\nLearn Vue basics\n\n== Advanced Vue\nLearn advanced patterns',
      version: 1,
      approved_at: null,
      created_at: '2025-01-01T00:00:00Z'
    })

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        {
          path: '/courses/:id/syllabus',
          name: 'course-syllabus',
          component: SyllabusView
        }
      ]
    })

    router.push('/courses/test-course-id/syllabus')
    await router.isReady()

    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const wrapper = mount(SyllabusView, {
      global: {
        plugins: [router]
      }
    })

    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    expect(mockGet).toHaveBeenCalledWith('/api/v1/courses/test-course-id/syllabus')

    // The view renders AsciiDoc via renderAsciidoc — raw markup (== / =) must
    // NOT appear; instead, heading elements must be present in the DOM.
    expect(wrapper.text()).not.toContain('== Introduction to Vue')
    expect(wrapper.text()).not.toContain('= Syllabus')

    // Rendered h2 headings appear as plain text inside heading elements
    const h2s = wrapper.findAll('h2')
    expect(h2s.some(el => el.text().includes('Introduction to Vue'))).toBe(true)
    expect(h2s.some(el => el.text().includes('Advanced Vue'))).toBe(true)

    // Non-heading text still renders
    expect(wrapper.text()).toContain('Learn Vue basics')
    expect(wrapper.text()).toContain('Learn advanced patterns')

    mockGet.mockRestore()
  })

  it('modification POST sends body with key "request" not "notes"', async () => {
    const mockGet = vi.spyOn(clientModule, 'get').mockResolvedValue({
      id: 'syll-123',
      course_id: 'course-123',
      content_adoc: '= Syllabus',
      version: 1,
      approved_at: null,
      created_at: '2025-01-01T00:00:00Z'
    })
    const mockPost = vi.spyOn(clientModule, 'post').mockResolvedValue({})

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        {
          path: '/courses/:id/syllabus',
          name: 'course-syllabus',
          component: SyllabusView
        }
      ]
    })

    router.push('/courses/test-id/syllabus')
    await router.isReady()

    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const wrapper = mount(SyllabusView, {
      global: {
        plugins: [router]
      }
    })

    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    const buttons = wrapper.findAll('button')
    const requestModBtn = buttons.find(btn => btn.text().includes('Request Modification'))
    await requestModBtn?.trigger('click')
    await wrapper.vm.$nextTick()

    const textarea = wrapper.find('textarea')
    await textarea.setValue('Please make it easier')
    await wrapper.vm.$nextTick()

    const allButtons = wrapper.findAll('button')
    const submitBtn = allButtons.find(btn => btn.text().includes('Submit'))
    await submitBtn?.trigger('click')

    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    expect(mockPost).toHaveBeenCalledWith(
      '/api/v1/courses/test-id/syllabus/modification',
      { request: 'Please make it easier' }
    )

    const callArgs = mockPost.mock.calls[0]
    const bodyArg = callArgs[1] as Record<string, unknown>
    expect('request' in bodyArg).toBe(true)
    expect('notes' in bodyArg).toBe(false)
    expect('modification' in bodyArg).toBe(false)

    mockGet.mockRestore()
    mockPost.mockRestore()
  })

  it('modification success re-fetches syllabus', async () => {
    const mockGetResponses = [
      {
        id: 'syll-123',
        course_id: 'course-123',
        content_adoc: '= Syllabus\n\n== Original Section\nOriginal description',
        version: 1,
        approved_at: null,
        created_at: '2025-01-01T00:00:00Z'
      },
      {
        id: 'syll-123',
        course_id: 'course-123',
        content_adoc: '= Syllabus\n\n== Updated Section\nUpdated description',
        version: 2,
        approved_at: null,
        created_at: '2025-01-01T00:00:00Z'
      }
    ]

    let callCount = 0
    const mockGet = vi.spyOn(clientModule, 'get').mockImplementation(() => {
      return Promise.resolve(mockGetResponses[callCount++])
    })
    const mockPost = vi.spyOn(clientModule, 'post').mockResolvedValue({})

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        {
          path: '/courses/:id/syllabus',
          name: 'course-syllabus',
          component: SyllabusView
        }
      ]
    })

    router.push('/courses/test-id/syllabus')
    await router.isReady()

    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const wrapper = mount(SyllabusView, {
      global: {
        plugins: [router]
      }
    })

    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    // Rendered as <h2>Original Section</h2>, not raw '== Original Section'
    const h2sBefore = wrapper.findAll('h2')
    expect(h2sBefore.some(el => el.text().includes('Original Section'))).toBe(true)

    const requestModBtn = wrapper.findAll('button').find(btn => btn.text().includes('Request Modification'))
    await requestModBtn?.trigger('click')
    await wrapper.vm.$nextTick()

    const textarea = wrapper.find('textarea')
    await textarea.setValue('Make it better')

    const submitBtn = wrapper.findAll('button').find(btn => btn.text().includes('Submit'))
    await submitBtn?.trigger('click')

    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    expect(mockPost).toHaveBeenCalledTimes(1)
    expect(mockGet).toHaveBeenCalledTimes(2)

    await wrapper.vm.$nextTick()
    // Rendered as <h2>Updated Section</h2>, not raw '== Updated Section'
    const h2sAfter = wrapper.findAll('h2')
    expect(h2sAfter.some(el => el.text().includes('Updated Section'))).toBe(true)

    mockGet.mockRestore()
    mockPost.mockRestore()
  })

  it('approve POST navigates to generating route', async () => {
    const mockGet = vi.spyOn(clientModule, 'get').mockResolvedValue({
      id: 'syll-123',
      course_id: 'course-123',
      content_adoc: '= Syllabus',
      version: 1,
      approved_at: null,
      created_at: '2025-01-01T00:00:00Z'
    })
    const mockPost = vi.spyOn(clientModule, 'post').mockResolvedValue({})

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        {
          path: '/courses/:id/syllabus',
          name: 'course-syllabus',
          component: SyllabusView
        },
        {
          path: '/courses/:id/generating',
          name: 'course-generating',
          component: { template: '<div>Generating</div>' }
        }
      ]
    })

    router.push('/courses/test-id/syllabus')
    await router.isReady()

    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const wrapper = mount(SyllabusView, {
      global: {
        plugins: [router]
      }
    })

    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    const approveBtn = wrapper.findAll('button').find(btn => btn.text().includes('Approve and Start Course'))
    await approveBtn?.trigger('click')

    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    expect(mockPost).toHaveBeenCalledWith('/api/v1/courses/test-id/syllabus/approve', {})
    expect(router.currentRoute.value.path).toBe('/courses/test-id/generating')

    mockGet.mockRestore()
    mockPost.mockRestore()
  })

  it('error handling for POST operations (modification)', async () => {
    const mockGet = vi.spyOn(clientModule, 'get').mockResolvedValue({
      id: 'syll-123',
      course_id: 'course-123',
      content_adoc: '= Syllabus',
      version: 1,
      approved_at: null,
      created_at: '2025-01-01T00:00:00Z'
    })
    const mockPost = vi.spyOn(clientModule, 'post').mockRejectedValue(
      new clientModule.ApiError(400, { error: 'bad request' })
    )

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        {
          path: '/courses/:id/syllabus',
          name: 'course-syllabus',
          component: SyllabusView
        }
      ]
    })

    router.push('/courses/test-id/syllabus')
    await router.isReady()

    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const wrapper = mount(SyllabusView, {
      global: {
        plugins: [router]
      }
    })

    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    const requestModBtn = wrapper.findAll('button').find(btn => btn.text().includes('Request Modification'))
    await requestModBtn?.trigger('click')
    await wrapper.vm.$nextTick()

    const textarea = wrapper.find('textarea')
    await textarea.setValue('Make changes')

    const submitBtn = wrapper.findAll('button').find(btn => btn.text().includes('Submit'))
    await submitBtn?.trigger('click')

    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    const errorMsg = wrapper.findAll('.error-message')
    expect(errorMsg.some(el => el.text().includes('Failed to submit modification'))).toBe(true)

    mockGet.mockRestore()
    mockPost.mockRestore()
  })

  it('error handling for POST operations (approve)', async () => {
    const mockGet = vi.spyOn(clientModule, 'get').mockResolvedValue({
      id: 'syll-123',
      course_id: 'course-123',
      content_adoc: '= Syllabus',
      version: 1,
      approved_at: null,
      created_at: '2025-01-01T00:00:00Z'
    })
    const mockPost = vi.spyOn(clientModule, 'post').mockRejectedValue(
      new clientModule.ApiError(500, { error: 'server error' })
    )

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        {
          path: '/courses/:id/syllabus',
          name: 'course-syllabus',
          component: SyllabusView
        }
      ]
    })

    router.push('/courses/test-id/syllabus')
    await router.isReady()

    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const wrapper = mount(SyllabusView, {
      global: {
        plugins: [router]
      }
    })

    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    const approveBtn = wrapper.findAll('button').find(btn => btn.text().includes('Approve and Start Course'))
    await approveBtn?.trigger('click')

    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    const errorMsg = wrapper.findAll('.error-message')
    expect(errorMsg.some(el => el.text().includes('Failed to approve syllabus'))).toBe(true)

    mockGet.mockRestore()
    mockPost.mockRestore()
  })

  it('shows drafting indicator on 404 error and polls for syllabus', async () => {
    // @{"verifies": ["REQ-FECOURSE-490", "REQ-FECOURSE-491"]}
    const mockGet = vi
      .spyOn(clientModule, 'get')
      .mockRejectedValueOnce(new clientModule.ApiError(404, { error: 'not found' }))
      .mockResolvedValueOnce({
        id: 'syll-123',
        course_id: 'course-123',
        content_adoc: '= Syllabus\n\nContent loaded!',
        version: 1,
        approved_at: null,
        created_at: '2025-01-01T00:00:00Z'
      })

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        {
          path: '/courses/:id/syllabus',
          name: 'course-syllabus',
          component: SyllabusView
        }
      ]
    })

    router.push('/courses/test-course/syllabus')
    await router.isReady()

    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const wrapper = mount(SyllabusView, {
      global: {
        plugins: [router]
      }
    })

    await vi.advanceTimersByTimeAsync(100)
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Your professor is drafting your syllabus…')
    expect(wrapper.find('.typing-indicator').exists()).toBe(true)

    await vi.advanceTimersByTimeAsync(4100)
    await wrapper.vm.$nextTick()

    // '= Syllabus' raw markup must not appear — it renders as <h1>Syllabus</h1>
    expect(wrapper.text()).not.toContain('= Syllabus')
    const h1 = wrapper.find('h1.syllabus-view h1, h1')
    expect(wrapper.find('h1').exists() || wrapper.text()).toBeTruthy()
    expect(wrapper.text()).toContain('Content loaded!')
    expect(wrapper.text()).not.toContain('Your professor is drafting your syllabus…')

    mockGet.mockRestore()
  })

  it('shows non-404 error banner, not drafting indicator', async () => {
    // @{"verifies": ["REQ-FECOURSE-490"]}
    const mockGet = vi
      .spyOn(clientModule, 'get')
      .mockRejectedValue(new clientModule.ApiError(500, { error: 'server error' }))

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        {
          path: '/courses/:id/syllabus',
          name: 'course-syllabus',
          component: SyllabusView
        }
      ]
    })

    router.push('/courses/test-course/syllabus')
    await router.isReady()

    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const wrapper = mount(SyllabusView, {
      global: {
        plugins: [router]
      }
    })

    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Failed to load syllabus')
    expect(wrapper.text()).not.toContain('Your professor is drafting your syllabus…')

    mockGet.mockRestore()
  })

  it('shows longer-than-expected message after 180s of drafting polling', async () => {
    // @{"verifies": ["REQ-FECOURSE-491"]}
    const mockGet = vi
      .spyOn(clientModule, 'get')
      .mockRejectedValue(new clientModule.ApiError(404, { error: 'not found' }))

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        {
          path: '/courses/:id/syllabus',
          name: 'course-syllabus',
          component: SyllabusView
        }
      ]
    })

    router.push('/courses/test-course/syllabus')
    await router.isReady()

    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const wrapper = mount(SyllabusView, {
      global: {
        plugins: [router]
      }
    })

    await vi.advanceTimersByTimeAsync(100)
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Your professor is drafting your syllabus…')

    await vi.advanceTimersByTimeAsync(180100)
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('generation is taking longer than expected')
    expect(wrapper.text()).toContain('Try Again')
    expect(wrapper.text()).not.toContain('Your professor is drafting your syllabus…')

    mockGet.mockRestore()
  })

  it('restarts drafting poll when Try Again is clicked', async () => {
    // @{"verifies": ["REQ-FECOURSE-491"]}
    const mockGet = vi
      .spyOn(clientModule, 'get')
      .mockRejectedValue(new clientModule.ApiError(404, { error: 'not found' }))

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        {
          path: '/courses/:id/syllabus',
          name: 'course-syllabus',
          component: SyllabusView
        }
      ]
    })

    router.push('/courses/test-course/syllabus')
    await router.isReady()

    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const wrapper = mount(SyllabusView, {
      global: {
        plugins: [router]
      }
    })

    await vi.advanceTimersByTimeAsync(100)
    await wrapper.vm.$nextTick()

    await vi.advanceTimersByTimeAsync(180100)
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('generation is taking longer than expected')

    const tryAgainBtn = wrapper
      .findAll('button')
      .find(btn => btn.text().includes('Try Again'))
    await tryAgainBtn?.trigger('click')
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Your professor is drafting your syllabus…')

    mockGet.mockRestore()
  })

  it('renders AsciiDoc as h2 elements — raw == markup does not appear in DOM', async () => {
    // @{"verifies": ["REQ-FECOURSE-629"]}
    const mockGet = vi.spyOn(clientModule, 'get').mockResolvedValue({
      id: 'syll-123',
      course_id: 'course-123',
      content_adoc: '= Course Syllabus\n\n== Week 1: Introduction\n\nSome content here.\n\n== Week 2: Deep Dive\n\nMore content.',
      version: 1,
      approved_at: null,
      created_at: '2025-01-01T00:00:00Z'
    })

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        {
          path: '/courses/:id/syllabus',
          name: 'course-syllabus',
          component: SyllabusView
        }
      ]
    })

    router.push('/courses/test-course/syllabus')
    await router.isReady()

    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const wrapper = mount(SyllabusView, {
      global: { plugins: [router] }
    })

    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    // Raw AsciiDoc markup must NOT appear in the rendered output
    expect(wrapper.text()).not.toContain('== Week 1: Introduction')
    expect(wrapper.text()).not.toContain('== Week 2: Deep Dive')
    expect(wrapper.text()).not.toContain('= Course Syllabus')

    // Heading text must appear inside h2 elements (rendered, not raw)
    const h2s = wrapper.findAll('h2')
    expect(h2s.some(el => el.text().includes('Week 1: Introduction'))).toBe(true)
    expect(h2s.some(el => el.text().includes('Week 2: Deep Dive'))).toBe(true)

    // Body text still renders
    expect(wrapper.text()).toContain('Some content here.')
    expect(wrapper.text()).toContain('More content.')

    mockGet.mockRestore()
  })
})
