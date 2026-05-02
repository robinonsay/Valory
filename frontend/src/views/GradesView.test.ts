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

const mockSummary = {
  course_id: 'test-course-id',
  student_id: 'test-student-id',
  weighted_score: 85.5,
  total_weight: 0.6
}

describe('GradesView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('fetches grades on mount using correct URL and token', async () => {
    const mockGet = vi.spyOn(clientModule, 'get').mockResolvedValue(mockSummary)

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
      '/api/v1/courses/test-course-id/grade',
      'test-token'
    )
  })

  it('renders weighted score and total weight from summary', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValue(mockSummary)

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
    expect(wrapper.text()).toContain('0.60')
    expect(wrapper.text()).toContain('Weighted Score')
  })

  it('shows "No grades yet" when summary is null', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValue(null)

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

    expect(wrapper.text()).toContain('No grades yet.')
  })

  it('shows error message when API call fails', async () => {
    vi.spyOn(clientModule, 'get').mockRejectedValue(
      new clientModule.ApiError(500, { error: 'internal server error' })
    )

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

    expect(wrapper.text()).toContain('Failed to load grades.')
  })
})
