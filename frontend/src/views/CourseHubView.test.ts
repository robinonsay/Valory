// @{"req": ["REQ-FECOURSE-075", "REQ-FECOURSE-076", "REQ-FECOURSE-077", "REQ-FECOURSE-078", "REQ-FECOURSE-570", "REQ-FECOURSE-580", "REQ-FECOURSE-590", "REQ-FECOURSE-600"]}

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import CourseHubView from './CourseHubView.vue'
import * as clientModule from '@/api/client'
import { useAuthStore } from '@/stores/auth'

vi.mock('@/api/client', () => ({
  get: vi.fn(),
  ApiError: class extends Error {
    status: number
    body: unknown
    constructor(status: number, body: unknown) {
      super(`HTTP ${status}`)
      this.name = 'ApiError'
      this.status = status
      this.body = body
    }
  }
}))

const { get } = await import('@/api/client')

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
})

describe('CourseHubView', () => {
  it('fetches course and sections on mount', async () => {
    const mockCourse = {
      id: 'course-1',
      topic: 'JavaScript Basics',
      status: 'active',
      created_at: '2026-05-01T00:00:00Z',
      updated_at: '2026-05-01T12:00:00Z'
    }

    const mockSections = [
      { section_index: 0, title: 'Introduction' },
      { section_index: 1, title: 'Variables' }
    ]

    vi.mocked(get).mockImplementation((path) => {
      if (path.includes('/sections')) {
        return Promise.resolve(mockSections)
      }
      return Promise.resolve(mockCourse)
    })

    const auth = useAuthStore()
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/courses/:id/hub', component: { template: '<div></div>' } }]
    })
    router.push('/courses/course-1/hub')
    await router.isReady()

    const wrapper = mount(CourseHubView, {
      global: {
        plugins: [router]
      }
    })

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    expect(vi.mocked(get)).toHaveBeenCalledWith('/api/v1/courses/course-1', 'test-token')
    expect(vi.mocked(get)).toHaveBeenCalledWith('/api/v1/courses/course-1/sections', 'test-token')
  })

  it('renders section list with completion indicators', async () => {
    const mockCourse = {
      id: 'course-1',
      topic: 'JavaScript Basics',
      status: 'active',
      created_at: '2026-05-01T00:00:00Z',
      updated_at: '2026-05-01T12:00:00Z'
    }

    const mockSections = [
      { section_index: 0, title: 'Introduction' },
      { section_index: 1, title: 'Variables' }
    ]

    vi.mocked(get).mockImplementation((path) => {
      if (path.includes('/sections')) {
        return Promise.resolve(mockSections)
      }
      if (path.includes('/homework')) {
        return Promise.reject(new clientModule.ApiError(404, {}))
      }
      return Promise.resolve(mockCourse)
    })

    const auth = useAuthStore()
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/courses/:id/hub', component: { template: '<div></div>' } }]
    })
    router.push('/courses/course-1/hub')
    await router.isReady()

    const wrapper = mount(CourseHubView, {
      global: {
        plugins: [router]
      }
    })

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    const sectionItems = wrapper.findAll('.section-item a')
    expect(sectionItems).toHaveLength(2)
    expect(sectionItems[0].text()).toContain('Introduction')
    expect(sectionItems[1].text()).toContain('Variables')
  })

  it('section link navigates to correct section route', async () => {
    const mockCourse = {
      id: 'course-1',
      topic: 'JavaScript Basics',
      status: 'active',
      created_at: '2026-05-01T00:00:00Z',
      updated_at: '2026-05-01T12:00:00Z'
    }

    const mockSections = [
      { section_index: 0, title: 'Introduction' }
    ]

    vi.mocked(get).mockImplementation((path) => {
      if (path.includes('/sections')) {
        return Promise.resolve(mockSections)
      }
      if (path.includes('/homework')) {
        return Promise.reject(new clientModule.ApiError(404, {}))
      }
      return Promise.resolve(mockCourse)
    })

    const auth = useAuthStore()
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/courses/:id/hub', component: { template: '<div></div>' } },
        { path: '/courses/:id/sections/:sectionId', component: { template: '<div></div>' } }
      ]
    })
    router.push('/courses/course-1/hub')
    await router.isReady()

    const wrapper = mount(CourseHubView, {
      global: {
        plugins: [router]
      }
    })

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    const sectionLink = wrapper.find('.section-item a')
    expect(sectionLink.attributes('href')).toBe('/courses/course-1/sections/0')
  })

  it('renders Grades and Badges links', async () => {
    const mockCourse = {
      id: 'course-1',
      topic: 'JavaScript Basics',
      status: 'active',
      created_at: '2026-05-01T00:00:00Z',
      updated_at: '2026-05-01T12:00:00Z'
    }

    const mockSections = []

    vi.mocked(get).mockImplementation((path) => {
      if (path.includes('/sections')) {
        return Promise.resolve(mockSections)
      }
      if (path.includes('/homework')) {
        return Promise.reject(new clientModule.ApiError(404, {}))
      }
      return Promise.resolve(mockCourse)
    })

    const auth = useAuthStore()
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/courses/:id/hub', component: { template: '<div></div>' } }]
    })
    router.push('/courses/course-1/hub')
    await router.isReady()

    const wrapper = mount(CourseHubView, {
      global: {
        plugins: [router]
      }
    })

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    const gradesLink = wrapper.findAll('.link-button').find(link => link.text() === 'Grades')
    const badgesLink = wrapper.findAll('.link-button').find(link => link.text() === 'Badges')

    expect(gradesLink).toBeDefined()
    expect(gradesLink?.attributes('href')).toBe('/courses/course-1/grades')
    expect(badgesLink).toBeDefined()
    expect(badgesLink?.attributes('href')).toBe('/courses/course-1/badges')
  })

  it('shows loading state while fetching', async () => {
    const mockCourse = {
      id: 'course-1',
      topic: 'JavaScript Basics',
      status: 'active',
      created_at: '2026-05-01T00:00:00Z',
      updated_at: '2026-05-01T12:00:00Z'
    }

    const mockSections: any[] = []

    vi.mocked(get).mockImplementation((path) => {
      return new Promise((resolve, reject) => {
        setTimeout(() => {
          if (path.includes('/sections')) {
            resolve(mockSections)
          } else if (path.includes('/homework')) {
            reject(new clientModule.ApiError(404, {}))
          } else {
            resolve(mockCourse)
          }
        }, 50)
      })
    })

    const auth = useAuthStore()
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/courses/:id/hub', component: { template: '<div></div>' } }]
    })
    router.push('/courses/course-1/hub')
    await router.isReady()

    const wrapper = mount(CourseHubView, {
      global: {
        plugins: [router]
      }
    })

    expect(wrapper.find('.loading').exists()).toBe(true)
    expect(wrapper.find('.loading').text()).toBe('Loading course...')

    await new Promise(resolve => setTimeout(resolve, 250))
    await wrapper.vm.$nextTick()

    expect(wrapper.find('.loading').exists()).toBe(false)
  })

  it('renders homework section with links when homework exists', async () => {
    const mockCourse = {
      id: 'course-1',
      topic: 'JavaScript Basics',
      status: 'active',
      created_at: '2026-05-01T00:00:00Z',
      updated_at: '2026-05-01T12:00:00Z'
    }

    const mockSections: any[] = []

    const mockHomework = [
      { id: 'hw-1', section_index: 0, title: 'Assignment 1', grade_weight: 0.1, due_date: '2026-05-15T23:59:59Z' },
      { id: 'hw-2', section_index: 1, title: 'Assignment 2', grade_weight: 0.1, due_date: '2026-05-22T23:59:59Z' }
    ]

    vi.mocked(get).mockImplementation((path) => {
      if (path.includes('/sections')) {
        return Promise.resolve(mockSections)
      }
      if (path.includes('/homework')) {
        return Promise.resolve(mockHomework)
      }
      return Promise.resolve(mockCourse)
    })

    const auth = useAuthStore()
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/courses/:id/hub', component: { template: '<div></div>' } }]
    })
    router.push('/courses/course-1/hub')
    await router.isReady()

    const wrapper = mount(CourseHubView, {
      global: {
        plugins: [router]
      }
    })

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    const homeworkItems = wrapper.findAll('.homework-item a')
    expect(homeworkItems).toHaveLength(2)
    expect(homeworkItems[0].text()).toContain('Assignment 1')
    expect(homeworkItems[0].text()).toContain('May 15, 2026')
    expect(homeworkItems[1].text()).toContain('Assignment 2')
    expect(homeworkItems[1].text()).toContain('May 22, 2026')
  })
})
