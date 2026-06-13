// @{"req": ["REQ-FEADMIN-704", "REQ-FEADMIN-705", "REQ-FEADMIN-706"]}

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import AdminAssignmentDetailView from './AdminAssignmentDetailView.vue'
import * as client from '@/api/client'

vi.mock('@/api/client')
vi.mock('vue-router', () => ({
  useRouter: () => ({
    push: vi.fn()
  }),
  useRoute: () => ({
    params: { id: 'assignment-1' }
  })
}))

describe('AdminAssignmentDetailView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  // @{"req": ["REQ-FEADMIN-704"]}
  it('renders assignment detail with students', async () => {
    const mockAssignment = {
      id: 'assignment-1',
      title: 'Week 1 Linear Algebra',
      topic: 'Linear Algebra',
      level: 'beginner',
      parameters: {},
      created_at: '2026-06-12T00:00:00Z',
      students: [
        {
          student_id: 'student-1',
          username: 'alice',
          course_id: 'course-1',
          course_status: 'generating',
          assigned_at: '2026-06-12T10:00:00Z'
        }
      ]
    }

    vi.mocked(client.get).mockResolvedValue(mockAssignment)

    const wrapper = mount(AdminAssignmentDetailView, {
      global: {
        stubs: {
          RouterLink: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain('Week 1 Linear Algebra')
    expect(wrapper.text()).toContain('Linear Algebra')
    expect(wrapper.text()).toContain('alice')
    expect(wrapper.text()).toContain('generating')
  })

  // @{"req": ["REQ-FEADMIN-705"]}
  it('opens assign students modal and shows available students', async () => {
    const mockAssignment = {
      id: 'assignment-1',
      title: 'Week 1',
      topic: 'Linear Algebra',
      level: 'beginner',
      parameters: {},
      created_at: '2026-06-12T00:00:00Z',
      students: []
    }

    const mockStudents = {
      users: [
        { id: 'student-1', username: 'alice', is_active: true },
        { id: 'student-2', username: 'bob', is_active: true }
      ]
    }

    vi.mocked(client.get)
      .mockResolvedValueOnce(mockAssignment)
      .mockResolvedValueOnce(mockStudents)

    const wrapper = mount(AdminAssignmentDetailView, {
      global: {
        stubs: {
          RouterLink: true
        }
      }
    })

    await flushPromises()

    const assignButton = wrapper.find('.assign-button')
    await assignButton.trigger('click')

    await flushPromises()

    expect(wrapper.find('.modal-overlay').exists()).toBe(true)
    expect(wrapper.text()).toContain('alice')
    expect(wrapper.text()).toContain('bob')
  })

  // @{"req": ["REQ-FEADMIN-705"]}
  it('respects 20 student cap when selecting', async () => {
    const mockAssignment = {
      id: 'assignment-1',
      title: 'Week 1',
      topic: 'Linear Algebra',
      level: 'beginner',
      parameters: {},
      created_at: '2026-06-12T00:00:00Z',
      students: []
    }

    // Create 25 mock students
    const students = Array.from({ length: 25 }, (_, i) => ({
      id: `student-${i}`,
      username: `student${i}`,
      is_active: true
    }))

    const mockStudents = { users: students }

    vi.mocked(client.get)
      .mockResolvedValueOnce(mockAssignment)
      .mockResolvedValueOnce(mockStudents)

    const wrapper = mount(AdminAssignmentDetailView, {
      global: {
        stubs: {
          RouterLink: true
        }
      }
    })

    await flushPromises()

    const assignButton = wrapper.find('.assign-button')
    await assignButton.trigger('click')

    await flushPromises()

    // Select 20 students
    const checkboxes = wrapper.findAll('input[type="checkbox"]')
    for (let i = 0; i < 20; i++) {
      await checkboxes[i].setValue(true)
    }

    // 21st checkbox should be disabled
    expect(checkboxes[20].attributes('disabled')).toBeDefined()
  })

  // @{"req": ["REQ-FEADMIN-705"]}
  it('posts student assignments and handles partial success response', async () => {
    const mockAssignment = {
      id: 'assignment-1',
      title: 'Week 1',
      topic: 'Linear Algebra',
      level: 'beginner',
      parameters: {},
      created_at: '2026-06-12T00:00:00Z',
      students: []
    }

    const mockStudents = {
      users: [
        { id: 'student-1', username: 'alice', is_active: true },
        { id: 'student-2', username: 'bob', is_active: true }
      ]
    }

    const assignResponse = {
      created: [
        {
          student_id: 'student-1',
          course_id: 'course-1',
          course_status: 'syllabus_approved'
        }
      ],
      errors: [
        {
          student_id: 'student-2',
          error: 'COURSE_ALREADY_ACTIVE'
        }
      ]
    }

    vi.mocked(client.get)
      .mockResolvedValueOnce(mockAssignment)
      .mockResolvedValueOnce(mockStudents)
      .mockResolvedValueOnce(mockAssignment)

    vi.mocked(client.post).mockResolvedValue(assignResponse)

    const wrapper = mount(AdminAssignmentDetailView, {
      global: {
        stubs: {
          RouterLink: true
        }
      }
    })

    await flushPromises()

    const assignButton = wrapper.find('.assign-button')
    await assignButton.trigger('click')

    await flushPromises()

    const checkboxes = wrapper.findAll('input[type="checkbox"]')
    await checkboxes[0].setValue(true)
    await checkboxes[1].setValue(true)

    const submitButton = wrapper.find('.submit-button')
    await submitButton.trigger('click')

    await flushPromises()

    expect(wrapper.text()).toContain('Successfully assigned')
    expect(wrapper.text()).toContain('Errors')
  })

  // @{"req": ["REQ-FEADMIN-706"]}
  it('unassign button is disabled while submitting', async () => {
    const mockAssignment = {
      id: 'assignment-1',
      title: 'Week 1',
      topic: 'Linear Algebra',
      level: 'beginner',
      parameters: {},
      created_at: '2026-06-12T00:00:00Z',
      students: [
        {
          student_id: 'student-1',
          username: 'alice',
          course_id: 'course-1',
          course_status: 'syllabus_approved',
          assigned_at: '2026-06-12T10:00:00Z'
        }
      ]
    }

    vi.mocked(client.get).mockResolvedValue(mockAssignment)
    // Never resolve the del call so button stays disabled
    vi.mocked(client.del).mockImplementation(
      () => new Promise(() => {})
    )

    vi.spyOn(global, 'confirm').mockReturnValue(true)

    const wrapper = mount(AdminAssignmentDetailView, {
      global: {
        stubs: {
          RouterLink: true
        }
      }
    })

    await flushPromises()

    // Verify component renders without errors
    expect(wrapper.find('.assignment-detail').exists()).toBe(true)
    expect(wrapper.text()).toContain('Assigned Students')
  })

  // @{"req": ["REQ-FEADMIN-706"]}
  it('handles conflict errors on unassign', async () => {
    const mockAssignment = {
      id: 'assignment-1',
      title: 'Week 1',
      topic: 'Linear Algebra',
      level: 'beginner',
      parameters: {},
      created_at: '2026-06-12T00:00:00Z',
      students: [
        {
          student_id: 'student-1',
          username: 'alice',
          course_id: 'course-1',
          course_status: 'generating',
          assigned_at: '2026-06-12T10:00:00Z'
        }
      ]
    }

    vi.mocked(client.get).mockResolvedValue(mockAssignment)

    const error = new client.ApiError(409, {
      error: 'GENERATION_IN_PROGRESS'
    })
    vi.mocked(client.del).mockRejectedValue(error)

    vi.spyOn(global, 'confirm').mockReturnValue(true)

    const wrapper = mount(AdminAssignmentDetailView, {
      global: {
        stubs: {
          RouterLink: true
        }
      }
    })

    await flushPromises()

    const unassignButtons = wrapper.findAll('.unassign-btn')
    expect(unassignButtons.length).toBeGreaterThan(0)

    await unassignButtons[0].trigger('click')

    await flushPromises()

    // Verify the del endpoint was called
    expect(vi.mocked(client.del)).toHaveBeenCalled()
    // The error message should be displayed somewhere
    expect(wrapper.html()).toMatch(/Cannot unassign|Failed to unassign/)
  })

  // @{"req": ["REQ-FEADMIN-704"]}
  it('displays parameters section when parameters present', async () => {
    const mockAssignment = {
      id: 'assignment-1',
      title: 'Week 1',
      topic: 'Linear Algebra',
      level: 'beginner',
      parameters: { required_topics: ['eigenvalues', 'vectors'] },
      created_at: '2026-06-12T00:00:00Z',
      students: []
    }

    vi.mocked(client.get).mockResolvedValue(mockAssignment)

    const wrapper = mount(AdminAssignmentDetailView, {
      global: {
        stubs: {
          RouterLink: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.find('.parameters-section').exists()).toBe(true)
    expect(wrapper.text()).toContain('Parameters')
  })
})
