// @{"req": ["REQ-FECOURSE-055", "REQ-FECOURSE-056", "REQ-FECOURSE-057", "REQ-FECOURSE-060", "REQ-FECOURSE-460", "REQ-FECOURSE-470", "REQ-FECOURSE-480", "REQ-FECOURSE-510", "REQ-FECOURSE-520"]}

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import SyllabusView from './SyllabusView.vue'
import * as clientModule from '@/api/client'
import { useAuthStore } from '@/stores/auth'

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
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    const wrapper = mount(SyllabusView, {
      global: {
        plugins: [router]
      }
    })

    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    expect(mockGet).toHaveBeenCalledWith('/api/v1/courses/test-course-id/syllabus', 'test-token')

    expect(wrapper.text()).toContain('= Syllabus')
    expect(wrapper.text()).toContain('== Introduction to Vue')
    expect(wrapper.text()).toContain('Learn Vue basics')
    expect(wrapper.text()).toContain('== Advanced Vue')
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
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

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
      { request: 'Please make it easier' },
      'test-token'
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
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    const wrapper = mount(SyllabusView, {
      global: {
        plugins: [router]
      }
    })

    await vi.runAllTimersAsync()
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('== Original Section')

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
    expect(wrapper.text()).toContain('== Updated Section')

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
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

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

    expect(mockPost).toHaveBeenCalledWith('/api/v1/courses/test-id/syllabus/approve', {}, 'test-token')
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
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

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
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

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
})
