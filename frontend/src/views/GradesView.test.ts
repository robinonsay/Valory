// @{"req": ["REQ-FECONTENT-040", "REQ-FECONTENT-041", "REQ-FECONTENT-042", "REQ-FECONTENT-043", "REQ-FECONTENT-145", "REQ-FECONTENT-150", "REQ-FECONTENT-155", "REQ-FECONTENT-160"]}

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import GradesView from './GradesView.vue'
import { useAuthStore } from '@/stores/auth'
import * as clientModule from '@/api/client'

const router = createRouter({
  history: createMemoryHistory(),
  routes: [
    {
      path: '/courses/:id/grades',
      component: GradesView
    }
  ]
})

describe('GradesView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('fetches grades on mount', async () => {
    const mockGrades = {
      grades: [
        {
          homework_id: 'hw1',
          homework_title: 'Homework 1',
          score: 85,
          max_score: 100,
          late_penalty: 0,
          final_score: 85,
          submitted_at: '2024-01-01T10:00:00Z',
          graded_at: '2024-01-02T10:00:00Z'
        }
      ],
      overall_average: 85.5
    }

    const mockGet = vi.spyOn(clientModule, 'get').mockResolvedValue(mockGrades)

    const auth = useAuthStore()
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    await router.push('/courses/test-course-id/grades')

    const wrapper = mount(GradesView, {
      global: {
        plugins: [router]
      }
    })

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(mockGet).toHaveBeenCalledWith(
      '/api/v1/courses/test-course-id/grades',
      'test-token'
    )
  })

  it('renders per-homework scores', async () => {
    const mockGrades = {
      grades: [
        {
          homework_id: 'hw1',
          homework_title: 'Homework 1',
          score: 85,
          max_score: 100,
          late_penalty: 0,
          final_score: 85,
          submitted_at: '2024-01-01T10:00:00Z',
          graded_at: '2024-01-02T10:00:00Z'
        }
      ],
      overall_average: 85.5
    }

    vi.spyOn(clientModule, 'get').mockResolvedValue(mockGrades)

    const auth = useAuthStore()
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    await router.push('/courses/test-course-id/grades')

    const wrapper = mount(GradesView, {
      global: {
        plugins: [router]
      }
    })

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(wrapper.text()).toContain('Homework 1')
    expect(wrapper.text()).toContain('85.00 / 100.00')
  })

  it('shows "Pending" for ungraded items with null score', async () => {
    const mockGrades = {
      grades: [
        {
          homework_id: 'hw1',
          homework_title: 'Homework 1',
          score: null,
          max_score: 100,
          late_penalty: 0,
          final_score: null,
          submitted_at: '2024-01-01T10:00:00Z',
          graded_at: null
        }
      ],
      overall_average: null
    }

    vi.spyOn(clientModule, 'get').mockResolvedValue(mockGrades)

    const auth = useAuthStore()
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    await router.push('/courses/test-course-id/grades')

    const wrapper = mount(GradesView, {
      global: {
        plugins: [router]
      }
    })

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(wrapper.text()).toContain('Pending')
  })

  it('shows late penalty when greater than 0', async () => {
    const mockGrades = {
      grades: [
        {
          homework_id: 'hw1',
          homework_title: 'Homework 1',
          score: 85,
          max_score: 100,
          late_penalty: 5,
          final_score: 80,
          submitted_at: '2024-01-01T10:00:00Z',
          graded_at: '2024-01-02T10:00:00Z'
        }
      ],
      overall_average: 80
    }

    vi.spyOn(clientModule, 'get').mockResolvedValue(mockGrades)

    const auth = useAuthStore()
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    await router.push('/courses/test-course-id/grades')

    const wrapper = mount(GradesView, {
      global: {
        plugins: [router]
      }
    })

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(wrapper.text()).toContain('Late penalty: -5%')
  })

  it('shows "N/A" for null overall average', async () => {
    const mockGrades = {
      grades: [
        {
          homework_id: 'hw1',
          homework_title: 'Homework 1',
          score: null,
          max_score: 100,
          late_penalty: 0,
          final_score: null,
          submitted_at: '2024-01-01T10:00:00Z',
          graded_at: null
        }
      ],
      overall_average: null
    }

    vi.spyOn(clientModule, 'get').mockResolvedValue(mockGrades)

    const auth = useAuthStore()
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    await router.push('/courses/test-course-id/grades')

    const wrapper = mount(GradesView, {
      global: {
        plugins: [router]
      }
    })

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(wrapper.text()).toContain('N/A')
  })

  it('shows overall average when present', async () => {
    const mockGrades = {
      grades: [
        {
          homework_id: 'hw1',
          homework_title: 'Homework 1',
          score: 85,
          max_score: 100,
          late_penalty: 0,
          final_score: 85,
          submitted_at: '2024-01-01T10:00:00Z',
          graded_at: '2024-01-02T10:00:00Z'
        }
      ],
      overall_average: 85.5
    }

    vi.spyOn(clientModule, 'get').mockResolvedValue(mockGrades)

    const auth = useAuthStore()
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    await router.push('/courses/test-course-id/grades')

    const wrapper = mount(GradesView, {
      global: {
        plugins: [router]
      }
    })

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(wrapper.text()).toContain('85.50')
  })
})
