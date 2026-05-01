// @{"req": ["REQ-FECOURSE-001", "REQ-FECOURSE-002", "REQ-FECOURSE-003", "REQ-FECOURSE-004", "REQ-FECOURSE-005"]}
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useCourseStore } from './course'
import { ApiError } from '@/api/client'
import type { CourseListResponse, CourseResponse } from '@/types/course'

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

describe('Initial state', () => {
  it('courses should be empty', () => {
    const store = useCourseStore()
    expect(store.courses).toEqual([])
  })

  it('currentCourse should be null', () => {
    const store = useCourseStore()
    expect(store.currentCourse).toBeNull()
  })

  it('loading should be false', () => {
    const store = useCourseStore()
    expect(store.loading).toBe(false)
  })

  it('error should be null', () => {
    const store = useCourseStore()
    expect(store.error).toBeNull()
  })
})

describe('fetchCourses', () => {
  it('success — sets courses array from response, loading false after', async () => {
    const store = useCourseStore()
    const mockCourses = [
      {
        id: '1',
        title: 'Math 101',
        status: 'active' as const,
        student_id: 'student1',
        created_at: '2026-05-01T00:00:00Z',
        updated_at: '2026-05-01T12:00:00Z'
      },
      {
        id: '2',
        title: 'Physics 101',
        status: 'intake' as const,
        student_id: 'student1',
        created_at: '2026-05-01T01:00:00Z',
        updated_at: '2026-05-01T13:00:00Z'
      }
    ]

    vi.mocked(get).mockResolvedValue({ courses: mockCourses } as CourseListResponse)

    expect(store.loading).toBe(false)
    const promise = store.fetchCourses('token123')
    expect(store.loading).toBe(true)

    await promise

    expect(store.courses).toEqual(mockCourses)
    expect(store.loading).toBe(false)
    expect(store.error).toBeNull()
  })

  it('error — sets error string, loading false after, courses unchanged', async () => {
    const store = useCourseStore()
    const initialCourses = store.courses

    const mockError = new ApiError(500, { error: 'server error' })
    vi.mocked(get).mockRejectedValue(mockError)

    await store.fetchCourses('token123')

    expect(store.error).toBe('HTTP 500')
    expect(store.loading).toBe(false)
    expect(store.courses).toBe(initialCourses)
  })
})

describe('fetchCourse', () => {
  it('success — sets currentCourse', async () => {
    const store = useCourseStore()
    const mockCourse = {
      id: '1',
      title: 'Biology 101',
      status: 'active' as const,
      student_id: 'student1',
      created_at: '2026-05-01T00:00:00Z',
      updated_at: '2026-05-01T12:00:00Z'
    }

    vi.mocked(get).mockResolvedValue({ course: mockCourse } as CourseResponse)

    await store.fetchCourse('1', 'token123')

    expect(store.currentCourse).toEqual(mockCourse)
    expect(store.loading).toBe(false)
    expect(store.error).toBeNull()
  })

  it('error — sets error, currentCourse unchanged', async () => {
    const store = useCourseStore()
    const initialCurrentCourse = store.currentCourse

    const mockError = new ApiError(404, { error: 'not found' })
    vi.mocked(get).mockRejectedValue(mockError)

    await store.fetchCourse('999', 'token123')

    expect(store.error).toBe('HTTP 404')
    expect(store.loading).toBe(false)
    expect(store.currentCourse).toBe(initialCurrentCourse)
  })
})

describe('setCurrent', () => {
  it('sets currentCourse directly', () => {
    const store = useCourseStore()
    const mockCourse = {
      id: '1',
      title: 'History 101',
      status: 'generating' as const,
      student_id: 'student1',
      created_at: '2026-05-01T00:00:00Z',
      updated_at: '2026-05-01T12:00:00Z'
    }

    store.setCurrent(mockCourse)

    expect(store.currentCourse).toEqual(mockCourse)
  })
})

describe('reset', () => {
  it('clears all state', () => {
    const store = useCourseStore()

    store.courses = [
      {
        id: '1',
        title: 'Test',
        status: 'active' as const,
        student_id: 'student1',
        created_at: '2026-05-01T00:00:00Z',
        updated_at: '2026-05-01T12:00:00Z'
      }
    ]
    store.currentCourse = {
      id: '1',
      title: 'Test',
      status: 'active' as const,
      student_id: 'student1',
      created_at: '2026-05-01T00:00:00Z',
      updated_at: '2026-05-01T12:00:00Z'
    }
    store.error = 'Some error'

    store.reset()

    expect(store.courses).toEqual([])
    expect(store.currentCourse).toBeNull()
    expect(store.loading).toBe(false)
    expect(store.error).toBeNull()
  })
})

describe('sortedCourses getter', () => {
  it('returns courses sorted by updated_at descending', () => {
    const store = useCourseStore()
    store.courses = [
      {
        id: '1',
        title: 'Old Course',
        status: 'active' as const,
        student_id: 'student1',
        created_at: '2026-05-01T00:00:00Z',
        updated_at: '2026-05-01T10:00:00Z'
      },
      {
        id: '2',
        title: 'Newest Course',
        status: 'intake' as const,
        student_id: 'student1',
        created_at: '2026-05-01T02:00:00Z',
        updated_at: '2026-05-01T14:00:00Z'
      },
      {
        id: '3',
        title: 'Middle Course',
        status: 'syllabus_draft' as const,
        student_id: 'student1',
        created_at: '2026-05-01T01:00:00Z',
        updated_at: '2026-05-01T12:00:00Z'
      }
    ]

    const sorted = store.sortedCourses

    expect(sorted[0].id).toBe('2')
    expect(sorted[1].id).toBe('3')
    expect(sorted[2].id).toBe('1')
  })
})

describe('activeCourse getter', () => {
  it('returns currentCourse only when status is active', () => {
    const store = useCourseStore()
    const activeCourse = {
      id: '1',
      title: 'Active Course',
      status: 'active' as const,
      student_id: 'student1',
      created_at: '2026-05-01T00:00:00Z',
      updated_at: '2026-05-01T12:00:00Z'
    }

    store.currentCourse = activeCourse

    expect(store.activeCourse).toEqual(activeCourse)
  })

  it('returns null when currentCourse is null', () => {
    const store = useCourseStore()
    store.currentCourse = null

    expect(store.activeCourse).toBeNull()
  })

  it('returns null when currentCourse status is not active', () => {
    const store = useCourseStore()
    store.currentCourse = {
      id: '1',
      title: 'Intake Course',
      status: 'intake' as const,
      student_id: 'student1',
      created_at: '2026-05-01T00:00:00Z',
      updated_at: '2026-05-01T12:00:00Z'
    }

    expect(store.activeCourse).toBeNull()
  })
})
