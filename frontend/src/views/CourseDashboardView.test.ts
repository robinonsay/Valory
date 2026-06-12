// @{"req": ["REQ-FECOURSE-001", "REQ-FECOURSE-002", "REQ-FECOURSE-003", "REQ-FECOURSE-010", "REQ-FECOURSE-011", "REQ-FECOURSE-012", "REQ-FECOURSE-300", "REQ-FECOURSE-301"]}

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import CourseDashboardView from './CourseDashboardView.vue'
import { useAuthStore } from '@/stores/auth'
import { useCourseStore } from '@/stores/course'
import * as clientModule from '@/api/client'
import type { Course, CourseResponse } from '@/types/course'

vi.mock('@/api/client', () => ({
  post: vi.fn(),
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

const router = createRouter({
  history: createMemoryHistory(),
  routes: [
    {
      path: '/courses',
      component: CourseDashboardView
    },
    {
      path: '/courses/:id/intake',
      name: 'course-intake',
      component: { template: '<div>Intake</div>' }
    },
    {
      path: '/courses/:id/syllabus',
      name: 'course-syllabus',
      component: { template: '<div>Syllabus</div>' }
    },
    {
      path: '/courses/:id/generating',
      name: 'course-generating',
      component: { template: '<div>Generating</div>' }
    },
    {
      path: '/courses/:id/hub',
      name: 'course-hub',
      component: { template: '<div>Hub</div>' }
    }
  ]
})

describe('CourseDashboardView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    router.currentRoute.value.path = '/courses'
  })

  it('renders loading state while store is loading', async () => {
    const wrapper = mount(CourseDashboardView, {
      global: {
        plugins: [router]
      }
    })

    const courseStore = useCourseStore()
    courseStore.loading = true

    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Loading courses...')
  })

  it('renders course cards from store', async () => {
    const wrapper = mount(CourseDashboardView, {
      global: {
        plugins: [router]
      }
    })

    const courseStore = useCourseStore()
    const mockCourses: Course[] = [
      {
        id: '1',
        title: 'Math 101',
        topic: 'Mathematics',
        status: 'active',
        student_id: 'student1',
        created_at: '2026-05-01T00:00:00Z',
        updated_at: '2026-05-01T12:00:00Z'
      },
      {
        id: '2',
        title: 'Physics 101',
        topic: 'Physics',
        status: 'intake',
        student_id: 'student1',
        created_at: '2026-05-01T01:00:00Z',
        updated_at: '2026-05-01T13:00:00Z'
      }
    ]

    courseStore.courses = mockCourses
    courseStore.loading = false

    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Math 101')
    expect(wrapper.text()).toContain('Physics 101')
  })

  it('continue button for active course navigates to /courses/:id/hub', async () => {
    const pushSpy = vi.spyOn(router, 'push')

    const wrapper = mount(CourseDashboardView, {
      global: {
        plugins: [router]
      }
    })

    const courseStore = useCourseStore()
    courseStore.courses = [
      {
        id: 'course-123',
        title: 'Active Course',
        topic: 'Mathematics',
        status: 'active',
        student_id: 'student1',
        created_at: '2026-05-01T00:00:00Z',
        updated_at: '2026-05-01T12:00:00Z'
      }
    ]

    await wrapper.vm.$nextTick()

    const continueButton = wrapper.find('.continue-button')
    await continueButton.trigger('click')

    expect(pushSpy).toHaveBeenCalledWith('/courses/course-123/hub')
  })

  it('continue button for intake course navigates to /courses/:id/intake', async () => {
    const pushSpy = vi.spyOn(router, 'push')

    const wrapper = mount(CourseDashboardView, {
      global: {
        plugins: [router]
      }
    })

    const courseStore = useCourseStore()
    courseStore.courses = [
      {
        id: 'course-456',
        title: 'Intake Course',
        topic: 'Physics',
        status: 'intake',
        student_id: 'student1',
        created_at: '2026-05-01T00:00:00Z',
        updated_at: '2026-05-01T12:00:00Z'
      }
    ]

    await wrapper.vm.$nextTick()

    const continueButton = wrapper.find('.continue-button')
    await continueButton.trigger('click')

    expect(pushSpy).toHaveBeenCalledWith('/courses/course-456/intake')
  })

  it('new course button POSTs and navigates to intake on success', async () => {
    const mockPost = vi.spyOn(clientModule, 'post').mockResolvedValue({
        id: 'new-course-id',
        title: '',
        topic: 'Linear Algebra',
        status: 'intake',
        student_id: 'student1',
        created_at: '2026-05-01T10:00:00Z',
        updated_at: '2026-05-01T10:00:00Z'
      } as CourseResponse)

    const pushSpy = vi.spyOn(router, 'push')

    const wrapper = mount(CourseDashboardView, {
      global: {
        plugins: [router]
      }
    })

    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const createBtn = wrapper.find('.create-button')
    await createBtn.trigger('click')
    await wrapper.vm.$nextTick()

    const input = wrapper.find('input#course-topic')
    await input.setValue('Linear Algebra')
    await wrapper.vm.$nextTick()

    const form = wrapper.find('form')
    await form.trigger('submit')
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(mockPost).toHaveBeenCalledWith('/api/v1/courses', { topic: 'Linear Algebra' })
    expect(pushSpy).toHaveBeenCalledWith('/courses/new-course-id/intake')
  })

  it('error state shown when store has error', async () => {
    const wrapper = mount(CourseDashboardView, {
      global: {
        plugins: [router]
      }
    })

    const courseStore = useCourseStore()
    courseStore.error = 'Failed to load courses'
    courseStore.loading = false

    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Failed to load courses')
  })

  it('displays 409 conflict error when creating course with active course', async () => {
    const errorToThrow = new clientModule.ApiError(409, { error: 'conflict' })
    vi.spyOn(clientModule, 'post').mockRejectedValue(errorToThrow)

    const wrapper = mount(CourseDashboardView, {
      global: {
        plugins: [router]
      }
    })

    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const createBtn = wrapper.find('.create-button')
    await createBtn.trigger('click')
    await wrapper.vm.$nextTick()

    const form = wrapper.find('form')
    await form.trigger('submit')
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(wrapper.text()).toContain('You already have an active course')
  })

  it('displays empty state when no courses exist', async () => {
    const wrapper = mount(CourseDashboardView, {
      global: {
        plugins: [router]
      }
    })

    const courseStore = useCourseStore()
    courseStore.courses = []
    courseStore.loading = false

    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('You have no courses yet.')
  })

  it('closes modal when cancel button is clicked', async () => {
    const wrapper = mount(CourseDashboardView, {
      global: {
        plugins: [router]
      }
    })

    const createBtn = wrapper.find('.create-button')
    await createBtn.trigger('click')
    await wrapper.vm.$nextTick()

    expect(wrapper.find('.modal').exists()).toBe(true)

    const cancelButton = wrapper.find('.cancel-button')
    await cancelButton.trigger('click')
    await wrapper.vm.$nextTick()

    expect(wrapper.find('.modal').exists()).toBe(false)
  })

  it('adds new course to list on successful creation', async () => {
    const newCourse: Course = {
      id: 'new-id',
      title: '',
      topic: 'Linear Algebra',
      status: 'intake',
      student_id: 'student1',
      created_at: '2026-05-01T10:00:00Z',
      updated_at: '2026-05-01T10:00:00Z'
    }

    vi.spyOn(clientModule, 'post').mockResolvedValue(newCourse as CourseResponse)

    const wrapper = mount(CourseDashboardView, {
      global: {
        plugins: [router]
      }
    })

    const courseStore = useCourseStore()
    courseStore.courses = [
      {
        id: '1',
        title: 'Existing Course',
        topic: 'Mathematics',
        status: 'active',
        student_id: 'student1',
        created_at: '2026-05-01T00:00:00Z',
        updated_at: '2026-05-01T12:00:00Z'
      }
    ]

    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const createBtn = wrapper.find('.create-button')
    await createBtn.trigger('click')
    await wrapper.vm.$nextTick()

    const input = wrapper.find('input#course-topic')
    await input.setValue('Linear Algebra')
    await wrapper.vm.$nextTick()

    const form = wrapper.find('form')
    await form.trigger('submit')
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(courseStore.courses[0].id).toBe('new-id')
  })

  it('course card click navigates to correct route based on status', async () => {
    const pushSpy = vi.spyOn(router, 'push')

    const wrapper = mount(CourseDashboardView, {
      global: {
        plugins: [router]
      }
    })

    const courseStore = useCourseStore()
    courseStore.courses = [
      {
        id: 'syllabus-course',
        title: 'Syllabus Course',
        topic: 'Calculus',
        status: 'syllabus_draft',
        student_id: 'student1',
        created_at: '2026-05-01T00:00:00Z',
        updated_at: '2026-05-01T12:00:00Z'
      }
    ]

    await wrapper.vm.$nextTick()

    const courseCard = wrapper.find('.course-card')
    await courseCard.trigger('click')

    expect(pushSpy).toHaveBeenCalledWith('/courses/syllabus-course/syllabus')
  })

  it('submit button is disabled when topic field is empty', async () => {
    const wrapper = mount(CourseDashboardView, {
      global: {
        plugins: [router]
      }
    })

    const createBtn = wrapper.find('.create-button')
    await createBtn.trigger('click')
    await wrapper.vm.$nextTick()

    const submitButton = wrapper.find('.submit-button')
    expect((submitButton.element as HTMLButtonElement).disabled).toBe(true)
  })

  it('submit button is disabled when topic field contains only whitespace', async () => {
    const wrapper = mount(CourseDashboardView, {
      global: {
        plugins: [router]
      }
    })

    const createBtn = wrapper.find('.create-button')
    await createBtn.trigger('click')
    await wrapper.vm.$nextTick()

    const input = wrapper.find('input#course-topic')
    await input.setValue('   ')
    await wrapper.vm.$nextTick()

    const submitButton = wrapper.find('.submit-button')
    expect((submitButton.element as HTMLButtonElement).disabled).toBe(true)
  })

  it('submit button is enabled when topic field has non-whitespace text', async () => {
    const wrapper = mount(CourseDashboardView, {
      global: {
        plugins: [router]
      }
    })

    const createBtn = wrapper.find('.create-button')
    await createBtn.trigger('click')
    await wrapper.vm.$nextTick()

    const input = wrapper.find('input#course-topic')
    await input.setValue('Linear Algebra')
    await wrapper.vm.$nextTick()

    const submitButton = wrapper.find('.submit-button')
    expect((submitButton.element as HTMLButtonElement).disabled).toBe(false)
  })

  it('POST body includes trimmed topic value', async () => {
    const mockPost = vi.spyOn(clientModule, 'post').mockResolvedValue({
        id: 'new-course-id',
        title: '',
        topic: 'Linear Algebra',
        status: 'intake',
        student_id: 'student1',
        created_at: '2026-05-01T10:00:00Z',
        updated_at: '2026-05-01T10:00:00Z'
      } as CourseResponse)

    const wrapper = mount(CourseDashboardView, {
      global: {
        plugins: [router]
      }
    })

    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const createBtn = wrapper.find('.create-button')
    await createBtn.trigger('click')
    await wrapper.vm.$nextTick()

    const input = wrapper.find('input#course-topic')
    await input.setValue('  Linear Algebra  ')
    await wrapper.vm.$nextTick()

    const form = wrapper.find('form')
    await form.trigger('submit')
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(mockPost).toHaveBeenCalledWith('/api/v1/courses', { topic: 'Linear Algebra' })
  })

  it('course card renders topic when title is empty', async () => {
    const wrapper = mount(CourseDashboardView, {
      global: {
        plugins: [router]
      }
    })

    const courseStore = useCourseStore()
    courseStore.courses = [
      {
        id: 'new-course',
        title: '',
        topic: 'Linear Algebra',
        status: 'intake',
        student_id: 'student1',
        created_at: '2026-05-01T10:00:00Z',
        updated_at: '2026-05-01T10:00:00Z'
      }
    ]

    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Linear Algebra')
  })

  it('course card renders title when title is present', async () => {
    const wrapper = mount(CourseDashboardView, {
      global: {
        plugins: [router]
      }
    })

    const courseStore = useCourseStore()
    courseStore.courses = [
      {
        id: 'course',
        title: 'Math 101',
        topic: 'Mathematics',
        status: 'active',
        student_id: 'student1',
        created_at: '2026-05-01T00:00:00Z',
        updated_at: '2026-05-01T12:00:00Z'
      }
    ]

    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Math 101')
  })

  it('modal closes and resets topic on cancel', async () => {
    const wrapper = mount(CourseDashboardView, {
      global: {
        plugins: [router]
      }
    })

    const createBtn = wrapper.find('.create-button')
    await createBtn.trigger('click')
    await wrapper.vm.$nextTick()

    const input = wrapper.find('input#course-topic')
    await input.setValue('Linear Algebra')
    await wrapper.vm.$nextTick()

    const cancelButton = wrapper.find('.cancel-button')
    await cancelButton.trigger('click')
    await wrapper.vm.$nextTick()

    expect(wrapper.find('.modal').exists()).toBe(false)

    await createBtn.trigger('click')
    await wrapper.vm.$nextTick()

    const newInput = wrapper.find('input#course-topic')
    expect((newInput.element as HTMLInputElement).value).toBe('')
  })
})
