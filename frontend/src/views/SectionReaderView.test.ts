// @{"req": ["REQ-FECONTENT-001", "REQ-FECONTENT-002", "REQ-FECONTENT-010", "REQ-FECONTENT-011", "REQ-FECONTENT-015", "REQ-FECONTENT-016", "REQ-FECONTENT-017", "REQ-FECONTENT-100", "REQ-FECONTENT-110", "REQ-FECONTENT-115", "REQ-FECONTENT-130", "REQ-FECONTENT-140"]}

import { describe, it, expect, beforeEach, vi, type MockInstance } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import SectionReaderView from './SectionReaderView.vue'
import { useAuthStore } from '@/stores/auth'
import * as clientModule from '@/api/client'

vi.mock('@/api/client', () => ({
  get: vi.fn(),
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
      path: '/courses/:id/sections/:sectionId',
      component: SectionReaderView
    }
  ]
})

const COURSE_ID = 'course-abc'
// sectionId is now an integer index (as a string) matching the backend's section_index
const SECTION_INDEX = 1
const SECTION_ID = SECTION_INDEX.toString()
const ROUTE = `/courses/${COURSE_ID}/sections/${SECTION_ID}`

const mockSectionContent = {
  id: 'some-uuid',
  title: 'Introduction to Algorithms',
  content_adoc: '<p>This is section content.</p>',
  section_index: SECTION_INDEX,
  course_id: COURSE_ID,
  version: 1,
  citation_verified: false,
  created_at: '2024-01-01T00:00:00Z'
}

const mockSectionList = [
  { section_index: 0, title: 'Overview' },
  { section_index: SECTION_INDEX, title: 'Introduction to Algorithms' },
  { section_index: 2, title: 'Sorting' }
]

async function mountView() {
  await router.push(ROUTE)
  return mount(SectionReaderView, {
    global: { plugins: [router] }
  })
}

describe('SectionReaderView', () => {
  let mockGet: MockInstance
  let mockPost: MockInstance

  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()

    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    mockGet = vi.spyOn(clientModule, 'get').mockImplementation((url: string) => {
      if ((url as string).endsWith('/sections')) {
        return Promise.resolve(mockSectionList)
      }
      return Promise.resolve(mockSectionContent)
    })

    mockPost = vi.spyOn(clientModule, 'post').mockResolvedValue({})
  })

  // Test 1: fetches section content on mount and renders title
  it('fetches section content on mount and renders title', async () => {
    const wrapper = await mountView()

    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(mockGet).toHaveBeenCalledWith(
      `/api/v1/courses/${COURSE_ID}/content/${SECTION_ID}`
    )
    expect(wrapper.find('h1').text()).toBe('Introduction to Algorithms')
  })

  // Test 2: previous button disabled when section is the first in the list
  it('previous button is disabled when section is the first in the list', async () => {
    mockGet.mockImplementation((url: string) => {
      if ((url as string).endsWith('/sections')) {
        return Promise.resolve([
          { section_index: 0, title: 'First Section' },
          { section_index: 1, title: 'Second Section' }
        ])
      }
      // Current section is index 0 (first)
      return Promise.resolve({ ...mockSectionContent, section_index: 0 })
    })

    // Navigate to section index 0
    await router.push(`/courses/${COURSE_ID}/sections/0`)
    const wrapper = mount(SectionReaderView, { global: { plugins: [router] } })
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    const prevButton = wrapper.find('.prev-button')
    expect(prevButton.attributes('disabled')).toBeDefined()
  })

  // Test 3: next button disabled when section is the last in the list
  it('next button is disabled when section is the last in the list', async () => {
    mockGet.mockImplementation((url: string) => {
      if ((url as string).endsWith('/sections')) {
        return Promise.resolve([
          { section_index: 0, title: 'First Section' },
          { section_index: 1, title: 'Last Section' }
        ])
      }
      // Current section is index 1 (last)
      return Promise.resolve({ ...mockSectionContent, section_index: 1 })
    })

    const wrapper = await mountView()
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    const nextButton = wrapper.find('.next-button')
    expect(nextButton.attributes('disabled')).toBeDefined()
  })

  // Test 4: Navigation to previous/next section IDs works
  it('previous button navigates to the previous section index', async () => {
    const pushSpy = vi.spyOn(router, 'push')
    const wrapper = await mountView()
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    const prevButton = wrapper.find('.prev-button')
    await prevButton.trigger('click')

    expect(pushSpy).toHaveBeenCalledWith(
      `/courses/${COURSE_ID}/sections/0`
    )
  })

  it('next button navigates to the next section index', async () => {
    const pushSpy = vi.spyOn(router, 'push')
    const wrapper = await mountView()
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    const nextButton = wrapper.find('.next-button')
    await nextButton.trigger('click')

    expect(pushSpy).toHaveBeenCalledWith(
      `/courses/${COURSE_ID}/sections/2`
    )
  })

  // Test 5: feedback modal opens on "Give Feedback" click
  it('feedback modal opens when Give Feedback is clicked', async () => {
    const wrapper = await mountView()
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(wrapper.find('.modal').exists()).toBe(false)

    const feedbackButton = wrapper.find('.feedback-button')
    await feedbackButton.trigger('click')
    await wrapper.vm.$nextTick()

    expect(wrapper.find('.modal').exists()).toBe(true)
  })

  // Test 6: feedback POST uses key "feedback_text" and correct content path
  it('feedback POST body uses key feedback_text and hits /content/ path', async () => {
    const wrapper = await mountView()
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    const feedbackButton = wrapper.find('.feedback-button')
    await feedbackButton.trigger('click')
    await wrapper.vm.$nextTick()

    const textarea = wrapper.find('.feedback-textarea')
    await textarea.setValue('This section was confusing.')

    const submitButton = wrapper.find('.submit-button')
    await submitButton.trigger('click')
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(mockPost).toHaveBeenCalledWith(
      `/api/v1/courses/${COURSE_ID}/content/${SECTION_ID}/feedback`,
      { feedback_text: 'This section was confusing.' }
    )

    // Verify the exact key "feedback_text" was used (not "text" or "feedback")
    const callArgs = mockPost.mock.calls[0]
    const body = callArgs[1] as Record<string, unknown>
    expect(Object.keys(body)).toContain('feedback_text')
    expect(Object.keys(body)).not.toContain('text')
    expect(Object.keys(body)).not.toContain('feedback')
  })

  // Test 7: feedback 201 closes modal
  it('feedback modal closes on HTTP 201 response', async () => {
    mockPost.mockResolvedValue({})

    const wrapper = await mountView()
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    const feedbackButton = wrapper.find('.feedback-button')
    await feedbackButton.trigger('click')
    await wrapper.vm.$nextTick()

    expect(wrapper.find('.modal').exists()).toBe(true)

    const textarea = wrapper.find('.feedback-textarea')
    await textarea.setValue('Great section!')

    const submitButton = wrapper.find('.submit-button')
    await submitButton.trigger('click')
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(wrapper.find('.modal').exists()).toBe(false)
    expect(wrapper.text()).toContain('Feedback submitted.')
  })

  // Test 8: export uses fetch (not anchor navigation) — mock fetch and assert blob download path
  it('export uses fetch with Authorization header and triggers blob download via createElement', async () => {
    // Mount the component first so Vue can create real DOM elements without interference
    const wrapper = await mountView()
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    const mockBlob = new Blob(['pdf content'], { type: 'application/pdf' })
    const mockObjectUrl = 'blob:http://localhost/mock-object-url'

    // jsdom does not implement URL.createObjectURL/revokeObjectURL; define before spying
    if (!URL.createObjectURL) {
      URL.createObjectURL = () => ''
    }
    if (!URL.revokeObjectURL) {
      URL.revokeObjectURL = () => {}
    }

    const createObjectURLSpy = vi.spyOn(URL, 'createObjectURL').mockReturnValue(mockObjectUrl)
    const revokeObjectURLSpy = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})

    // Track anchors created during the export call; pass through for all other tags
    const anchorsCreated: HTMLAnchorElement[] = []
    const realCreateElement = document.createElement.bind(document)
    const createElementSpy = vi.spyOn(document, 'createElement').mockImplementation((tag: string, ...args: unknown[]) => {
      const el = (realCreateElement as (...a: unknown[]) => Element)(tag, ...args)
      if (tag === 'a') {
        vi.spyOn(el as HTMLAnchorElement, 'click')
        anchorsCreated.push(el as HTMLAnchorElement)
      }
      return el as HTMLElement
    })

    const mockFetch = vi.fn().mockResolvedValue({
      ok: true,
      blob: () => Promise.resolve(mockBlob)
    })
    vi.stubGlobal('fetch', mockFetch)

    const exportPdfButton = wrapper.find('.export-button')
    await exportPdfButton.trigger('click')
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    // Verify fetch was called with correct /content/ path and credentials (cookie auth)
    expect(mockFetch).toHaveBeenCalledWith(
      `/api/v1/courses/${COURSE_ID}/content/${SECTION_ID}/export?format=pdf`,
      expect.objectContaining({
        credentials: 'same-origin'
      })
    )

    // Verify blob download path: createElement('a') was called, createObjectURL was called
    expect(createElementSpy).toHaveBeenCalledWith('a')
    expect(createObjectURLSpy).toHaveBeenCalledWith(mockBlob)
    expect(anchorsCreated.length).toBe(1)
    const anchor = anchorsCreated[0]
    expect(anchor.download).toBe(`section-${SECTION_ID}.pdf`)
    expect((anchor.click as ReturnType<typeof vi.fn>).mock.calls.length).toBeGreaterThan(0)
    expect(revokeObjectURLSpy).toHaveBeenCalledWith(mockObjectUrl)

    createElementSpy.mockRestore()
    createObjectURLSpy.mockRestore()
    revokeObjectURLSpy.mockRestore()
    vi.unstubAllGlobals()
  })
})
