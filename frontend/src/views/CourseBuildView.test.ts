// @{"verifies": ["REQ-SYS-073", "REQ-FECOURSE-602"]}

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import CourseBuildView from './CourseBuildView.vue'
import CourseDashboardView from './CourseDashboardView.vue'
import { useAuthStore } from '@/stores/auth'
import { useCourseStore } from '@/stores/course'

// ─── Mock the API modules ────────────────────────────────────────────────────

vi.mock('@/api/client', () => ({
  get: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
  ApiError: class extends Error {
    status: number
    body: unknown
    constructor(status: number, body: unknown) {
      super(`HTTP ${status}`)
      this.name = 'ApiError'
      this.status = status
      this.body = body
    }
  },
}))

vi.mock('@/api/tree', () => ({
  listCourseNodes: vi.fn(),
  approveCourseNode: vi.fn(),
  feedbackCourseNode: vi.fn(),
  regenerateCourseNode: vi.fn(),
  refineCourseNode: vi.fn(),
  getCourseNodeChat: vi.fn(),
  expandCourseLayer: vi.fn(),
}))

// ─── Mock the SSE composable ──────────────────────────────────────────────────
// Provide a controllable useNodeEvents mock so we can simulate live SSE events.
const mockNodeStatuses = vi.fn(() => ({ value: {} as Record<string, string> }))
const mockLayersAwaitingExpand = vi.fn(() => ({ value: new Set<string>() }))
const mockSseError = vi.fn(() => ({ value: null as string | null }))
const mockClearLayerAwaitingExpand = vi.fn()

vi.mock('@/composables/useNodeEvents', () => ({
  useNodeEvents: vi.fn(() => ({
    nodeStatuses: { value: {} },
    layersAwaitingExpand: { value: new Set() },
    error: { value: null },
    connected: { value: true },
    clearLayerAwaitingExpand: mockClearLayerAwaitingExpand,
    close: vi.fn(),
  })),
}))

// Stub TreeView so CourseBuildView tests focus on view-level behavior.
vi.mock('@/components/tree/TreeView.vue', () => ({
  default: {
    name: 'TreeView',
    props: ['nodes', 'nodeStatuses', 'layersAwaitingExpand',
      'onApproveNode', 'onFeedbackNode', 'onRegenerateNode',
      'onRefineNode', 'onLoadNodeChat', 'onExpandLayer'],
    template: '<div class="tree-view-stub">tree-view ({{ nodes.length }} nodes)</div>',
  },
}))

// @{"verifies": ["REQ-SYS-073", "REQ-FECOURSE-602"]}
describe('CourseBuildView', () => {
  let router: ReturnType<typeof createRouter>

  beforeEach(async () => {
    setActivePinia(createPinia())
    vi.clearAllMocks()

    router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/courses/:id/build', component: CourseBuildView },
        { path: '/courses/:id/hub', component: { template: '<div>Hub</div>' } },
        { path: '/courses/:id/generating', component: { template: '<div>Generating</div>' } },
        { path: '/courses/:id/intake', component: { template: '<div>Intake</div>' } },
        { path: '/courses/:id/syllabus', component: { template: '<div>Syllabus</div>' } },
      ],
    })
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('CourseBuildView_TreeModeTrue_LoadsNodesAndRendersTreeView', async () => {
    const { get } = await import('@/api/client')
    const { listCourseNodes } = await import('@/api/tree')

    vi.mocked(get).mockResolvedValue({
      id: 'c1', title: 'Algebra', topic: 'Math', status: 'active', tree_mode: true,
    })
    vi.mocked(listCourseNodes).mockResolvedValue({
      nodes: [
        { id: 'n1', course_id: 'c1', parent_id: null, node_type: 'root', ordering: 1,
          status: 'awaiting_review', payload: {}, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' },
      ],
    })

    router.push('/courses/c1/build')
    await router.isReady()

    const wrapper = mount(CourseBuildView, {
      global: { plugins: [router] },
    })
    await flushPromises()

    expect(wrapper.find('.tree-view-stub').exists()).toBe(true)
    expect(wrapper.find('.tree-view-stub').text()).toContain('1 nodes')
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('CourseBuildView_LoadingState_ShowsLoadingText', async () => {
    const { get } = await import('@/api/client')
    const { listCourseNodes } = await import('@/api/tree')

    // Never resolves immediately
    vi.mocked(get).mockReturnValue(new Promise(() => {}))
    vi.mocked(listCourseNodes).mockReturnValue(new Promise(() => {}))

    router.push('/courses/c1/build')
    await router.isReady()

    const wrapper = mount(CourseBuildView, {
      global: { plugins: [router] },
    })

    // Should show loading state before promises resolve
    expect(wrapper.find('.build-loading').exists()).toBe(true)
    expect(wrapper.text()).toContain('Loading course...')
  })

  // @{"verifies": ["REQ-SYS-073", "REQ-FECOURSE-602"]}
  it('CourseBuildView_TreeModeFalse_RedirectsToFlatRoute', async () => {
    const { get } = await import('@/api/client')
    const { listCourseNodes } = await import('@/api/tree')

    vi.mocked(get).mockResolvedValue({
      id: 'c1', title: 'Algebra', topic: 'Math', status: 'active', tree_mode: false,
    })
    vi.mocked(listCourseNodes).mockResolvedValue({ nodes: [] })

    const replaceSpy = vi.spyOn(router, 'replace')

    router.push('/courses/c1/build')
    await router.isReady()

    mount(CourseBuildView, {
      global: { plugins: [router] },
    })
    await flushPromises()

    // Should have redirected to the flat hub route
    expect(replaceSpy).toHaveBeenCalledWith('/courses/c1/hub')
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('CourseBuildView_ApiError_ShowsErrorBanner', async () => {
    const { get, ApiError } = await import('@/api/client')
    const { listCourseNodes } = await import('@/api/tree')

    vi.mocked(get).mockRejectedValue(new ApiError(500, { message: 'Internal Server Error' }))
    vi.mocked(listCourseNodes).mockResolvedValue({ nodes: [] })

    router.push('/courses/c1/build')
    await router.isReady()

    const wrapper = mount(CourseBuildView, {
      global: { plugins: [router] },
    })
    await flushPromises()

    expect(wrapper.find('.build-error').exists()).toBe(true)
    expect(wrapper.text()).toContain('Failed to load course')
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('CourseBuildView_CourseTitle_DisplaysFromTitleOrTopic', async () => {
    const { get } = await import('@/api/client')
    const { listCourseNodes } = await import('@/api/tree')

    vi.mocked(get).mockResolvedValue({
      id: 'c1', title: 'Introduction to Abstract Algebra', topic: 'Math',
      status: 'active', tree_mode: true,
    })
    vi.mocked(listCourseNodes).mockResolvedValue({ nodes: [] })

    router.push('/courses/c1/build')
    await router.isReady()

    const wrapper = mount(CourseBuildView, {
      global: { plugins: [router] },
    })
    await flushPromises()

    expect(wrapper.text()).toContain('Introduction to Abstract Algebra')
  })
})

// @{"verifies": ["REQ-SYS-073", "REQ-FECOURSE-602"]}
describe('CourseDashboardView_TreeModeRouting', () => {
  let router: ReturnType<typeof createRouter>

  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()

    router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/courses', component: CourseDashboardView },
        { path: '/courses/:id/build', component: { template: '<div>Build</div>' } },
        { path: '/courses/:id/hub', component: { template: '<div>Hub</div>' } },
        { path: '/courses/:id/intake', component: { template: '<div>Intake</div>' } },
        { path: '/courses/:id/syllabus', component: { template: '<div>Syllabus</div>' } },
        { path: '/courses/:id/generating', component: { template: '<div>Generating</div>' } },
      ],
    })
  })

  // @{"verifies": ["REQ-SYS-073", "REQ-FECOURSE-602"]}
  it('CourseDashboard_TreeModeTrue_NavigatesToBuildView', async () => {
    const pushSpy = vi.spyOn(router, 'push')

    router.push('/courses')
    await router.isReady()

    const wrapper = mount(CourseDashboardView, {
      global: { plugins: [router] },
    })

    const courseStore = useCourseStore()
    courseStore.courses = [{
      id: 'c1',
      title: 'Tree Mode Course',
      topic: 'Algebra',
      status: 'active',
      tree_mode: true,
      student_id: 'student-1',
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    } as any]
    courseStore.loading = false
    await wrapper.vm.$nextTick()

    const continueBtn = wrapper.find('.continue-button')
    await continueBtn.trigger('click')

    // tree_mode=true should push to /build not the flat route
    expect(pushSpy).toHaveBeenCalledWith('/courses/c1/build')
  })

  // @{"verifies": ["REQ-FECOURSE-602"]}
  it('CourseDashboard_TreeModeFalse_ActiveCourse_NavigatesToFlatHubRoute', async () => {
    const pushSpy = vi.spyOn(router, 'push')

    router.push('/courses')
    await router.isReady()

    const wrapper = mount(CourseDashboardView, {
      global: { plugins: [router] },
    })

    const courseStore = useCourseStore()
    courseStore.courses = [{
      id: 'c2',
      title: 'Flat Mode Course',
      topic: 'Calculus',
      status: 'active',
      tree_mode: false,
      student_id: 'student-1',
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    } as any]
    courseStore.loading = false
    await wrapper.vm.$nextTick()

    const continueBtn = wrapper.find('.continue-button')
    await continueBtn.trigger('click')

    // tree_mode=false (or absent) should use the original flat route map
    expect(pushSpy).toHaveBeenCalledWith('/courses/c2/hub')
  })

  // @{"verifies": ["REQ-FECOURSE-602"]}
  it('CourseDashboard_TreeModeUndefined_IntakeCourse_NavigatesToIntakeRoute', async () => {
    const pushSpy = vi.spyOn(router, 'push')

    router.push('/courses')
    await router.isReady()

    const wrapper = mount(CourseDashboardView, {
      global: { plugins: [router] },
    })

    const courseStore = useCourseStore()
    courseStore.courses = [{
      id: 'c3',
      title: '',
      topic: 'Physics',
      status: 'intake',
      // tree_mode absent — should behave as flat
      student_id: 'student-1',
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    } as any]
    courseStore.loading = false
    await wrapper.vm.$nextTick()

    const continueBtn = wrapper.find('.continue-button')
    await continueBtn.trigger('click')

    expect(pushSpy).toHaveBeenCalledWith('/courses/c3/intake')
  })
})
