// @{"req": ["REQ-FECONTENT-020", "REQ-FECONTENT-021", "REQ-FECONTENT-022", "REQ-FECONTENT-111", "REQ-FECONTENT-112", "REQ-FECONTENT-113", "REQ-FECONTENT-114", "REQ-FECONTENT-120", "REQ-FECONTENT-125", "REQ-FECONTENT-130"]}

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import HomeworkSubmissionView from './HomeworkSubmissionView.vue'
import { validateFile, MAX_SIZE_BYTES } from './homeworkSubmission'
import { useAuthStore } from '@/stores/auth'
import * as clientModule from '@/api/client'

// Helper to create a fake File with a given name and size
function makeFile(name: string, sizeBytes: number): File {
  // Use Blob to back the File so .size is accurately reported
  const content = new Uint8Array(sizeBytes)
  return new File([content], name, { type: 'application/octet-stream' })
}

const router = createRouter({
  history: createMemoryHistory(),
  routes: [
    {
      path: '/courses/:id/homework/:hwId',
      component: HomeworkSubmissionView
    }
  ]
})

describe('HomeworkSubmissionView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  // @{"req": ["REQ-FECONTENT-020"]}
  it('fetches homework metadata on mount and shows title and due date', async () => {
    const mockHomework = {
      id: 'hw-1',
      title: 'Homework 1',
      description: 'Write a report.',
      due_date: '2026-06-01T23:59:00Z'
    }

    const mockGet = vi.spyOn(clientModule, 'get').mockImplementation(async (path: string) => {
      if (path.includes('/submissions/latest')) {
        throw new clientModule.ApiError(404, { error: 'not found' })
      }
      return mockHomework
    })

    const authStore = useAuthStore()
    authStore.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    await router.push('/courses/course-1/homework/hw-1')

    const wrapper = mount(HomeworkSubmissionView, {
      global: { plugins: [router] }
    })

    await flushPromises()

    expect(mockGet).toHaveBeenCalledWith(
      '/api/v1/courses/course-1/homework/hw-1',
      'test-token'
    )
    expect(wrapper.text()).toContain('Homework 1')
    // Due date must be rendered (formatted value depends on locale; check raw substring)
    expect(wrapper.text()).toMatch(/Due:/)
  })

  // @{"req": ["REQ-FECONTENT-021", "REQ-FECONTENT-111", "REQ-FECONTENT-112"]}
  it('validateFile accepts .tex file and returns null', () => {
    const file = makeFile('assignment.tex', 100)
    expect(validateFile(file)).toBeNull()
  })

  // @{"req": ["REQ-FECONTENT-021", "REQ-FECONTENT-111", "REQ-FECONTENT-112"]}
  it('validateFile accepts .md file and returns null', () => {
    const file = makeFile('notes.md', 200)
    expect(validateFile(file)).toBeNull()
  })

  // @{"req": ["REQ-FECONTENT-021", "REQ-FECONTENT-111", "REQ-FECONTENT-112"]}
  it('validateFile accepts .markdown file and returns null', () => {
    const file = makeFile('report.markdown', 300)
    expect(validateFile(file)).toBeNull()
  })

  // @{"req": ["REQ-FECONTENT-021", "REQ-FECONTENT-111", "REQ-FECONTENT-112"]}
  it('validateFile accepts .adoc file and returns null', () => {
    const file = makeFile('lecture.adoc', 400)
    expect(validateFile(file)).toBeNull()
  })

  // @{"req": ["REQ-FECONTENT-021", "REQ-FECONTENT-111", "REQ-FECONTENT-112"]}
  it('validateFile accepts .asciidoc file and returns null', () => {
    const file = makeFile('chapter.asciidoc', 500)
    expect(validateFile(file)).toBeNull()
  })

  // @{"req": ["REQ-FECONTENT-022", "REQ-FECONTENT-112"]}
  it('validateFile rejects .pdf file and returns error naming accepted formats', () => {
    const file = makeFile('essay.pdf', 100)
    const result = validateFile(file)
    expect(result).not.toBeNull()
    expect(result).toContain('Accepted formats:')
    expect(result).toContain('.tex')
    expect(result).toContain('.md')
    expect(result).toContain('.markdown')
    expect(result).toContain('.adoc')
    expect(result).toContain('.asciidoc')
  })

  // @{"req": ["REQ-FECONTENT-023", "REQ-FECONTENT-113", "REQ-FECONTENT-114"]}
  it('validateFile rejects file larger than 10,485,760 bytes and returns size error', () => {
    const file = makeFile('big.tex', MAX_SIZE_BYTES + 1)
    const result = validateFile(file)
    expect(result).not.toBeNull()
    expect(result).toContain('10 MB')
  })

  // @{"req": ["REQ-FECONTENT-023", "REQ-FECONTENT-113"]}
  it('validateFile accepts file exactly at 10,485,760 bytes boundary and returns null', () => {
    const file = makeFile('boundary.tex', MAX_SIZE_BYTES)
    expect(validateFile(file)).toBeNull()
  })

  // @{"req": ["REQ-FECONTENT-020", "REQ-FECONTENT-125"]}
  it('XHR upload sets Authorization header with bearer token', async () => {
    const mockHomework = {
      id: 'hw-1',
      title: 'Homework 1',
      description: 'Write a report.',
      due_date: '2026-06-01T23:59:00Z'
    }

    vi.spyOn(clientModule, 'get').mockImplementation(async (path: string) => {
      if (path.includes('/submissions/latest')) {
        throw new clientModule.ApiError(404, { error: 'not found' })
      }
      return mockHomework
    })

    const authStore = useAuthStore()
    authStore.login('xhr-test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    await router.push('/courses/course-1/homework/hw-1')

    const wrapper = mount(HomeworkSubmissionView, {
      global: { plugins: [router] }
    })

    await flushPromises()

    // Capture the headers passed to XHR open/setRequestHeader via a mock
    const headersSet: Record<string, string> = {}
    let openUrl = ''

    const MockXHR = {
      upload: { addEventListener: vi.fn() },
      addEventListener: vi.fn(),
      open: vi.fn((_method: string, url: string) => { openUrl = url }),
      setRequestHeader: vi.fn((name: string, value: string) => { headersSet[name] = value }),
      send: vi.fn()
    }

    const XHRConstructor = vi.fn(() => MockXHR)
    vi.stubGlobal('XMLHttpRequest', XHRConstructor)

    const file = makeFile('work.tex', 100)

    // Trigger file selection via the internal uploadFile path directly via onSubmit
    const input = wrapper.find('input[type="file"]')
    // Create and dispatch a fake change event
    Object.defineProperty(input.element, 'files', {
      value: [file],
      writable: false,
      configurable: true
    })
    await input.trigger('change')
    await wrapper.vm.$nextTick()

    const submitBtn = wrapper.find('.submit-button')
    await submitBtn.trigger('click')
    await wrapper.vm.$nextTick()

    expect(XHRConstructor).toHaveBeenCalled()
    expect(MockXHR.setRequestHeader).toHaveBeenCalledWith('Authorization', 'Bearer xhr-test-token')
    expect(openUrl).toContain('/api/v1/courses/course-1/homework/hw-1/submissions')

    vi.unstubAllGlobals()
  })

  // @{"req": ["REQ-FECONTENT-022", "REQ-FECONTENT-112"]}
  it('file validation runs before XHR: invalid file prevents XHR construction', async () => {
    const mockHomework = {
      id: 'hw-1',
      title: 'Homework 1',
      description: 'Write a report.',
      due_date: '2026-06-01T23:59:00Z'
    }

    vi.spyOn(clientModule, 'get').mockImplementation(async (path: string) => {
      if (path.includes('/submissions/latest')) {
        throw new clientModule.ApiError(404, { error: 'not found' })
      }
      return mockHomework
    })

    const authStore = useAuthStore()
    authStore.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    await router.push('/courses/course-1/homework/hw-1')

    const wrapper = mount(HomeworkSubmissionView, {
      global: { plugins: [router] }
    })

    await flushPromises()

    const XHRConstructor = vi.fn()
    vi.stubGlobal('XMLHttpRequest', XHRConstructor)

    // Select an invalid .pdf file — validateFile will reject it
    const invalidFile = makeFile('report.pdf', 100)
    const input = wrapper.find('input[type="file"]')
    Object.defineProperty(input.element, 'files', {
      value: [invalidFile],
      writable: false,
      configurable: true
    })
    await input.trigger('change')
    await wrapper.vm.$nextTick()

    // The file error should be displayed; XHR should never be constructed
    expect(wrapper.text()).toContain('Accepted formats:')
    expect(XHRConstructor).not.toHaveBeenCalled()

    vi.unstubAllGlobals()
  })
})
