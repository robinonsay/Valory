// @{"req": ["REQ-FEADMIN-707"]}

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import CourseDetailView from './CourseDetailView.vue'
import * as client from '@/api/client'

// renderAsciidoc is async and depends on @asciidoctor/core; stub it so unit
// tests are fast and hermetic. The real util has its own test suite.
vi.mock('@/utils/renderAsciidoc', () => ({
  renderAsciidoc: vi.fn(async (src: string) => `<p>${src}</p>`)
}))

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
vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() }),
  useRoute: () => ({ params: { id: 'course-abc' } })
}))

const mockOverview = {
  course: {
    id: 'course-abc',
    student_id: 'student-1',
    username: 'alice',
    topic: 'Introduction to Algorithms',
    status: 'active',
    created_at: '2026-05-01T00:00:00Z',
    updated_at: '2026-05-10T12:00:00Z'
  },
  syllabus: {
    id: 'syllabus-1',
    content_adoc: '= Introduction to Algorithms\n\nThis course covers...',
    version: 2,
    created_at: '2026-05-01T01:00:00Z',
    approved_at: '2026-05-02T10:00:00Z'
  },
  sections: [
    {
      id: 'section-1',
      section_index: 1,
      title: 'Sorting Algorithms',
      content_adoc: '== Sorting Algorithms\n\nContent here.',
      version: 1,
      citation_verified: true,
      created_at: '2026-05-03T00:00:00Z'
    },
    {
      id: 'section-2',
      section_index: 2,
      title: 'Graph Theory',
      content_adoc: '== Graph Theory\n\nContent here.',
      version: 1,
      citation_verified: false,
      created_at: '2026-05-04T00:00:00Z'
    }
  ],
  homework: [
    {
      id: 'hw-1',
      section_index: 1,
      title: 'Sorting HW',
      rubric: 'Explain quicksort in detail.',
      grade_weight: 25,
      due_date: '2026-06-01T23:59:00Z',
      created_at: '2026-05-03T01:00:00Z'
    }
  ],
  submissions: [
    {
      id: 'sub-1',
      homework_id: 'hw-1',
      student_id: 'student-1',
      format: 'pdf',
      file_path: '/uploads/sub-1.pdf',
      file_size_bytes: 204800,
      submitted_at: '2026-05-30T14:00:00Z',
      grading_status: 'graded',
      raw_score: 88
    }
  ],
  grades: [
    {
      id: 'grade-1',
      submission_id: 'sub-1',
      homework_id: 'hw-1',
      raw_score: 88,
      late_days: 0,
      late_penalty_rate: 0,
      late_penalty_amount: 0,
      badge_waiver_applied: false,
      badge_improvement: false,
      final_score: 88,
      graded_at: '2026-05-31T10:00:00Z'
    }
  ]
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('CourseDetailView', () => {
  // @{"verifies": ["REQ-FEADMIN-707"]}
  it('shows loading state while fetching', async () => {
    vi.mocked(client.get).mockImplementation(() => new Promise(() => {}))

    const wrapper = mount(CourseDetailView)

    expect(wrapper.find('[data-testid="loading"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="loading"]').text()).toContain('Loading course')
  })

  // @{"verifies": ["REQ-FEADMIN-707"]}
  it('shows error state when fetch fails', async () => {
    vi.mocked(client.get).mockRejectedValue(
      new client.ApiError(500, { error: 'internal server error' })
    )

    const wrapper = mount(CourseDetailView)
    await flushPromises()

    expect(wrapper.find('[data-testid="error"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="error"]').text()).toContain('Failed to load course')
  })

  // @{"verifies": ["REQ-FEADMIN-707"]}
  it('shows 404 message when course is not found', async () => {
    vi.mocked(client.get).mockRejectedValue(
      new client.ApiError(404, { error: 'course not found' })
    )

    const wrapper = mount(CourseDetailView)
    await flushPromises()

    expect(wrapper.find('[data-testid="error"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="error"]').text()).toContain('Course not found')
  })

  // @{"verifies": ["REQ-FEADMIN-707"]}
  it('happy path: renders course summary, syllabus, sections, and grades', async () => {
    vi.mocked(client.get).mockResolvedValue(mockOverview)

    const wrapper = mount(CourseDetailView)
    await flushPromises()

    expect(wrapper.find('[data-testid="loading"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="error"]').exists()).toBe(false)

    // Course summary
    const summary = wrapper.find('[data-testid="course-summary"]')
    expect(summary.exists()).toBe(true)
    expect(summary.text()).toContain('Introduction to Algorithms')
    expect(summary.text()).toContain('alice')
    expect(summary.text()).toContain('active')

    // Syllabus panel
    const syllabusPanel = wrapper.find('[data-testid="syllabus-panel"]')
    expect(syllabusPanel.exists()).toBe(true)
    expect(syllabusPanel.text()).toContain('Version')
    expect(syllabusPanel.text()).toContain('Introduction to Algorithms')

    // Sections panel
    const sectionsPanel = wrapper.find('[data-testid="sections-panel"]')
    expect(sectionsPanel.exists()).toBe(true)
    expect(sectionsPanel.text()).toContain('Sorting Algorithms')
    expect(sectionsPanel.text()).toContain('Graph Theory')

    // Grades panel
    const gradesPanel = wrapper.find('[data-testid="grades-panel"]')
    expect(gradesPanel.exists()).toBe(true)
    expect(gradesPanel.text()).toContain('88')
  })

  // @{"verifies": ["REQ-FEADMIN-707"]}
  it('calls the correct API endpoint for the course id from route params', async () => {
    vi.mocked(client.get).mockResolvedValue(mockOverview)

    mount(CourseDetailView)
    await flushPromises()

    expect(vi.mocked(client.get)).toHaveBeenCalledWith(
      '/api/v1/admin/courses/course-abc/overview'
    )
  })

  // @{"verifies": ["REQ-FEADMIN-707"]}
  it('expanding a section renders its content via renderAsciidoc', async () => {
    vi.mocked(client.get).mockResolvedValue(mockOverview)

    const wrapper = mount(CourseDetailView)
    await flushPromises()

    const toggles = wrapper.findAll('.section-toggle')
    expect(toggles.length).toBe(2)

    // Expand the first section
    await toggles[0].trigger('click')
    await flushPromises()

    expect(wrapper.find('.section-body').exists()).toBe(true)
  })

  // @{"verifies": ["REQ-FEADMIN-707"]}
  it('shows homework, rubric, and submission inside an expanded section', async () => {
    vi.mocked(client.get).mockResolvedValue(mockOverview)

    const wrapper = mount(CourseDetailView)
    await flushPromises()

    // Expand section 1 (which has homework hw-1)
    const toggles = wrapper.findAll('.section-toggle')
    await toggles[0].trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('Sorting HW')
    expect(wrapper.text()).toContain('Explain quicksort in detail.')
    expect(wrapper.text()).toContain('graded')
    // Final score from grades
    expect(wrapper.text()).toContain('88')
  })

  // @{"verifies": ["REQ-FEADMIN-707"]}
  it('has no edit, regenerate, or delete controls', async () => {
    vi.mocked(client.get).mockResolvedValue(mockOverview)

    const wrapper = mount(CourseDetailView)
    await flushPromises()

    const html = wrapper.html().toLowerCase()
    // None of these mutation-trigger labels should appear
    expect(html).not.toContain('regenerate')
    expect(html).not.toContain('delete')
    expect(html).not.toContain('edit')
    // No form inputs (text fields etc.) that would allow mutation
    expect(wrapper.findAll('input[type="text"]').length).toBe(0)
    expect(wrapper.findAll('textarea').length).toBe(0)
  })

  // @{"verifies": ["REQ-FEADMIN-707"]}
  it('displays empty-state messages when sections, homework, and grades are absent', async () => {
    const emptyOverview = {
      ...mockOverview,
      syllabus: null,
      sections: [],
      homework: [],
      submissions: [],
      grades: []
    }

    vi.mocked(client.get).mockResolvedValue(emptyOverview)

    const wrapper = mount(CourseDetailView)
    await flushPromises()

    expect(wrapper.find('[data-testid="syllabus-panel"]').text()).toContain('No syllabus yet')
    expect(wrapper.find('[data-testid="sections-panel"]').text()).toContain('No sections yet')
    expect(wrapper.find('[data-testid="grades-panel"]').text()).toContain('No grades yet')
  })
})
