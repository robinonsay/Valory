// @{"req": ["REQ-FEADMIN-702", "REQ-FEADMIN-703"]}

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import AdminAssignmentsView from './AdminAssignmentsView.vue'
import * as client from '@/api/client'

vi.mock('@/api/client')
vi.mock('vue-router', () => ({
  useRouter: () => ({
    push: vi.fn()
  }),
  useRoute: () => ({
    params: {}
  })
}))

describe('AdminAssignmentsView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  // @{"req": ["REQ-FEADMIN-702"]}
  it('renders assignments list on mount', async () => {
    const mockAssignments = {
      assignments: [
        {
          id: '1',
          title: 'Week 1',
          topic: 'Linear Algebra',
          level: 'beginner',
          student_count: 5,
          created_at: '2026-06-12T00:00:00Z'
        }
      ],
      next_cursor: null
    }

    vi.mocked(client.get).mockResolvedValue(mockAssignments)

    const wrapper = mount(AdminAssignmentsView, {
      global: {
        stubs: {
          RouterLink: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain('Week 1')
    expect(wrapper.text()).toContain('Linear Algebra')
    expect(wrapper.text()).toContain('beginner')
    expect(wrapper.text()).toContain('5')
  })

  // @{"req": ["REQ-FEADMIN-703"]}
  it('opens create modal when New Assignment button is clicked', async () => {
    vi.mocked(client.get).mockResolvedValue({
      assignments: [],
      next_cursor: null
    })

    const wrapper = mount(AdminAssignmentsView, {
      global: {
        stubs: {
          RouterLink: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.find('.modal-overlay').exists()).toBe(false)

    const createButton = wrapper.find('.create-button')
    await createButton.trigger('click')

    expect(wrapper.find('.modal-overlay').exists()).toBe(true)
    expect(wrapper.text()).toContain('Create Assignment')
  })

  // @{"req": ["REQ-FEADMIN-703"]}
  it('creates assignment and adds to list', async () => {
    vi.mocked(client.get).mockResolvedValue({
      assignments: [],
      next_cursor: null
    })

    const newAssignment = {
      id: 'new-1',
      admin_id: 'admin-1',
      title: 'Week 2',
      topic: 'Calculus',
      level: 'intermediate',
      parameters: {},
      created_at: '2026-06-12T10:00:00Z',
      updated_at: '2026-06-12T10:00:00Z'
    }

    vi.mocked(client.post).mockResolvedValue(newAssignment)

    const wrapper = mount(AdminAssignmentsView, {
      global: {
        stubs: {
          RouterLink: true
        }
      }
    })

    await flushPromises()

    // Open modal
    await wrapper.find('.create-button').trigger('click')
    await flushPromises()

    // Fill form
    const titleInput = wrapper.find('#create-title')
    await titleInput.setValue('Week 2')

    const topicInput = wrapper.find('#create-topic')
    await topicInput.setValue('Calculus')

    const levelSelect = wrapper.find('#create-level')
    await levelSelect.setValue('intermediate')

    await flushPromises()

    // Submit form
    const form = wrapper.find('.modal-form')
    await form.trigger('submit')

    await flushPromises()

    expect(vi.mocked(client.post)).toHaveBeenCalledWith(
      '/api/v1/admin/assignments',
      expect.objectContaining({
        topic: 'Calculus',
        level: 'intermediate',
        title: 'Week 2'
      })
    )
  })

  it('validates topic is required', async () => {
    vi.mocked(client.get).mockResolvedValue({
      assignments: [],
      next_cursor: null
    })

    const wrapper = mount(AdminAssignmentsView, {
      global: {
        stubs: {
          RouterLink: true
        }
      }
    })

    await flushPromises()

    // Open modal
    await wrapper.find('.create-button').trigger('click')
    await flushPromises()

    // Try to submit without topic
    const submitButton = wrapper.find('.submit-button')
    expect(submitButton.attributes('disabled')).toBeDefined()
  })

  // @{"req": ["REQ-FEADMIN-702"]}
  it('handles API errors gracefully', async () => {
    const error = new client.ApiError(500, { error: 'Server error' })
    vi.mocked(client.get).mockRejectedValue(error)

    const wrapper = mount(AdminAssignmentsView, {
      global: {
        stubs: {
          RouterLink: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain('Failed to load assignments')
  })
})
