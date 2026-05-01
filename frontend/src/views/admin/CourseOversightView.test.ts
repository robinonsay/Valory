// @{"req": ["REQ-FEADMIN-050", "REQ-FEADMIN-051", "REQ-FEADMIN-052", "REQ-FEADMIN-053", "REQ-FEADMIN-400", "REQ-FEADMIN-401", "REQ-FEADMIN-402", "REQ-FEADMIN-403", "REQ-FEADMIN-404", "REQ-FEADMIN-405", "REQ-FEADMIN-406"]}

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import CourseOversightView from './CourseOversightView.vue'
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

describe('CourseOversightView', () => {
  it('fetches and renders courses on mount', async () => {
    const mockResponse = {
      courses: [
        {
          id: 'course-1',
          title: 'JavaScript Basics',
          status: 'active',
          student_id: 'student-1',
          created_at: '2026-05-01T00:00:00Z',
          updated_at: '2026-05-01T12:00:00Z'
        },
        {
          id: 'course-2',
          title: 'Advanced JavaScript',
          status: 'archived',
          student_id: 'student-2',
          created_at: '2026-04-15T00:00:00Z',
          updated_at: '2026-05-01T12:00:00Z'
        }
      ],
      next_cursor: 'cursor-123'
    }

    vi.mocked(get).mockResolvedValue(mockResponse)

    const auth = useAuthStore()
    auth.login('test-token', 'admin', Math.floor(Date.now() / 1000) + 3600)

    const wrapper = mount(CourseOversightView)

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    expect(vi.mocked(get)).toHaveBeenCalledWith('/api/v1/courses?limit=20', 'test-token')

    const rows = wrapper.findAll('tbody tr')
    expect(rows).toHaveLength(2)
    expect(rows[0].text()).toContain('JavaScript Basics')
    expect(rows[0].text()).toContain('active')
    expect(rows[0].text()).toContain('student-1')
  })

  it('status filter triggers re-fetch with status param', async () => {
    const mockResponse = {
      courses: [
        {
          id: 'course-1',
          title: 'JavaScript Basics',
          status: 'active',
          student_id: 'student-1',
          created_at: '2026-05-01T00:00:00Z',
          updated_at: '2026-05-01T12:00:00Z'
        }
      ],
      next_cursor: ''
    }

    const mockFilteredResponse = {
      courses: [
        {
          id: 'course-3',
          title: 'Archived Course',
          status: 'archived',
          student_id: 'student-3',
          created_at: '2026-03-01T00:00:00Z',
          updated_at: '2026-05-01T12:00:00Z'
        }
      ],
      next_cursor: ''
    }

    vi.mocked(get)
      .mockResolvedValueOnce(mockResponse)
      .mockResolvedValueOnce(mockFilteredResponse)

    const auth = useAuthStore()
    auth.login('test-token', 'admin', Math.floor(Date.now() / 1000) + 3600)

    const wrapper = mount(CourseOversightView)

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    expect(vi.mocked(get)).toHaveBeenCalledWith('/api/v1/courses?limit=20', 'test-token')

    const statusFilter = wrapper.find('#status-filter')
    await statusFilter.setValue('archived')

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    expect(vi.mocked(get)).toHaveBeenCalledWith(
      '/api/v1/courses?limit=20&status=archived',
      'test-token'
    )

    const rows = wrapper.findAll('tbody tr')
    expect(rows).toHaveLength(1)
    expect(rows[0].text()).toContain('Archived Course')
  })

  it('load more appends more courses', async () => {
    const mockInitialResponse = {
      courses: [
        {
          id: 'course-1',
          title: 'Course 1',
          status: 'active',
          student_id: 'student-1',
          created_at: '2026-05-01T00:00:00Z',
          updated_at: '2026-05-01T12:00:00Z'
        }
      ],
      next_cursor: 'cursor-123'
    }

    const mockMoreResponse = {
      courses: [
        {
          id: 'course-2',
          title: 'Course 2',
          status: 'active',
          student_id: 'student-2',
          created_at: '2026-04-15T00:00:00Z',
          updated_at: '2026-05-01T12:00:00Z'
        }
      ],
      next_cursor: ''
    }

    vi.mocked(get)
      .mockResolvedValueOnce(mockInitialResponse)
      .mockResolvedValueOnce(mockMoreResponse)

    const auth = useAuthStore()
    auth.login('test-token', 'admin', Math.floor(Date.now() / 1000) + 3600)

    const wrapper = mount(CourseOversightView)

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    let rows = wrapper.findAll('tbody tr')
    expect(rows).toHaveLength(1)

    const loadMoreButton = wrapper.find('.load-more-button')
    await loadMoreButton.trigger('click')

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    rows = wrapper.findAll('tbody tr')
    expect(rows).toHaveLength(2)
    expect(rows[1].text()).toContain('Course 2')

    expect(vi.mocked(get)).toHaveBeenCalledWith(
      '/api/v1/courses?limit=20&cursor=cursor-123',
      'test-token'
    )
  })

  it('loading state shown while fetching', async () => {
    const mockResponse = {
      courses: [],
      next_cursor: ''
    }

    vi.mocked(get).mockImplementation(
      () =>
        new Promise(resolve => {
          setTimeout(() => {
            resolve(mockResponse)
          }, 100)
        })
    )

    const auth = useAuthStore()
    auth.login('test-token', 'admin', Math.floor(Date.now() / 1000) + 3600)

    const wrapper = mount(CourseOversightView)

    await wrapper.vm.$nextTick()

    expect(wrapper.find('.loading').exists()).toBe(true)
    expect(wrapper.find('.loading').text()).toBe('Loading courses...')

    await new Promise(resolve => setTimeout(resolve, 150))
    await wrapper.vm.$nextTick()

    expect(wrapper.find('.loading').exists()).toBe(false)
  })

  it('error state shown on failure', async () => {
    vi.mocked(get).mockRejectedValue(new clientModule.ApiError(500, { error: 'Internal server error' }))

    const auth = useAuthStore()
    auth.login('test-token', 'admin', Math.floor(Date.now() / 1000) + 3600)

    const wrapper = mount(CourseOversightView)

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    const errorDiv = wrapper.find('.error')
    expect(errorDiv.exists()).toBe(true)
    expect(errorDiv.text()).toContain('Failed to load courses')
  })
})
