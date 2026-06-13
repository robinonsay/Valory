// @{"req": ["REQ-FEADMIN-050", "REQ-FEADMIN-051", "REQ-FEADMIN-052", "REQ-FEADMIN-053", "REQ-FEADMIN-400", "REQ-FEADMIN-401", "REQ-FEADMIN-402", "REQ-FEADMIN-403", "REQ-FEADMIN-404", "REQ-FEADMIN-405", "REQ-FEADMIN-406", "REQ-FEADMIN-708"]}

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
          student_username: 'alice',
          student_email: 'alice@example.com',
          created_at: '2026-05-01T00:00:00Z',
          updated_at: '2026-05-01T12:00:00Z'
        },
        {
          id: 'course-2',
          title: 'Advanced JavaScript',
          status: 'archived',
          student_id: 'student-2',
          student_username: 'bob',
          student_email: 'bob@example.com',
          created_at: '2026-04-15T00:00:00Z',
          updated_at: '2026-05-01T12:00:00Z'
        }
      ],
      next_cursor: 'cursor-123'
    }

    vi.mocked(get).mockResolvedValue(mockResponse)

    const auth = useAuthStore()
    auth.$patch({ role: 'admin', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const wrapper = mount(CourseOversightView)

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    expect(vi.mocked(get)).toHaveBeenCalledWith('/api/v1/courses?limit=20')

    const rows = wrapper.findAll('tbody tr')
    expect(rows).toHaveLength(2)
    expect(rows[0].text()).toContain('JavaScript Basics')
    expect(rows[0].text()).toContain('active')
    expect(rows[0].text()).toContain('alice')
  })

  it('status filter triggers re-fetch with status param', async () => {
    const mockResponse = {
      courses: [
        {
          id: 'course-1',
          title: 'JavaScript Basics',
          status: 'active',
          student_id: 'student-1',
          student_username: 'alice',
          student_email: 'alice@example.com',
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
          student_username: 'charlie',
          student_email: 'charlie@example.com',
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
    auth.$patch({ role: 'admin', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const wrapper = mount(CourseOversightView)

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    expect(vi.mocked(get)).toHaveBeenCalledWith('/api/v1/courses?limit=20')

    const statusFilter = wrapper.find('#status-filter')
    await statusFilter.setValue('archived')

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    expect(vi.mocked(get)).toHaveBeenCalledWith(
      '/api/v1/courses?limit=20&status=archived'
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
          student_username: 'alice',
          student_email: 'alice@example.com',
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
          student_username: 'bob',
          student_email: 'bob@example.com',
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
    auth.$patch({ role: 'admin', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

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
      '/api/v1/courses?limit=20&cursor=cursor-123'
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
    auth.$patch({ role: 'admin', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

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
    auth.$patch({ role: 'admin', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const wrapper = mount(CourseOversightView)

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    const errorDiv = wrapper.find('.error')
    expect(errorDiv.exists()).toBe(true)
    expect(errorDiv.text()).toContain('Failed to load courses')
  })

  // @{"verifies": ["REQ-FEADMIN-708"]}
  it('renders student username as primary identifier', async () => {
    const mockResponse = {
      courses: [
        {
          id: 'course-1',
          title: 'Course with username',
          status: 'active',
          student_id: '550e8400-e29b-41d4-a716-446655440000',
          student_username: 'john_doe',
          student_email: 'john@example.com',
          created_at: '2026-05-01T00:00:00Z',
          updated_at: '2026-05-01T12:00:00Z'
        }
      ],
      next_cursor: ''
    }

    vi.mocked(get).mockResolvedValue(mockResponse)

    const auth = useAuthStore()
    auth.$patch({ role: 'admin', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const wrapper = mount(CourseOversightView)

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    const rows = wrapper.findAll('tbody tr')
    expect(rows).toHaveLength(1)
    const studentCell = rows[0].find('.student')
    expect(studentCell.text()).toContain('john_doe')
    expect(studentCell.find('.student-name').exists()).toBe(true)
    expect(studentCell.find('.student-name').text()).toBe('john_doe')
    expect(studentCell.find('.student-id-secondary').exists()).toBe(true)
  })

  // @{"verifies": ["REQ-FEADMIN-708"]}
  it('falls back to student_id when student_username is absent', async () => {
    const mockResponse = {
      courses: [
        {
          id: 'course-1',
          title: 'Course without username',
          status: 'active',
          student_id: '550e8400-e29b-41d4-a716-446655440001',
          created_at: '2026-05-01T00:00:00Z',
          updated_at: '2026-05-01T12:00:00Z'
        }
      ],
      next_cursor: ''
    }

    vi.mocked(get).mockResolvedValue(mockResponse)

    const auth = useAuthStore()
    auth.$patch({ role: 'admin', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const wrapper = mount(CourseOversightView)

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    const rows = wrapper.findAll('tbody tr')
    expect(rows).toHaveLength(1)
    const studentCell = rows[0].find('.student')
    expect(studentCell.text()).toContain('550e8400-e29b-41d4-a716-446655440001')
    expect(studentCell.find('.student-id-secondary').exists()).toBe(false)
  })
})
