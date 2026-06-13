// @{"verifies": ["REQ-FEADMIN-710", "REQ-SYS-077"]}

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import DraftEditorView from './DraftEditorView.vue'

// ─── Mock API modules ─────────────────────────────────────────────────────────

vi.mock('@/api/client', () => ({
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
  getDraft: vi.fn(),
  approveDraftNode: vi.fn(),
  feedbackDraftNode: vi.fn(),
  regenerateDraftNode: vi.fn(),
  refineDraftNode: vi.fn(),
  getDraftNodeChat: vi.fn(),
  expandDraftLayer: vi.fn(),
  publishDraft: vi.fn(),
}))

// ─── Mock SSE composable — no real EventSource ────────────────────────────────

vi.mock('@/composables/useNodeEvents', () => ({
  useNodeEvents: vi.fn(() => ({
    nodeStatuses: { value: {} as Record<string, string> },
    layersAwaitingExpand: { value: new Set<string>() },
    error: { value: null as string | null },
    connected: { value: true },
    clearLayerAwaitingExpand: vi.fn(),
    close: vi.fn(),
  })),
}))

// Stub TreeView so DraftEditorView tests focus on view-level behavior.
vi.mock('@/components/tree/TreeView.vue', () => ({
  default: {
    name: 'TreeView',
    props: ['nodes', 'nodeStatuses', 'layersAwaitingExpand',
      'onApproveNode', 'onFeedbackNode', 'onRegenerateNode',
      'onRefineNode', 'onLoadNodeChat', 'onExpandLayer'],
    template: '<div class="tree-view-stub">tree-view ({{ nodes.length }} nodes)</div>',
  },
}))

// Standard draft fixture — all nodes approved (publishable state)
const approvedDraft = {
  id: 'draft-1',
  admin_id: 'admin-1',
  title: 'Algebra for Beginners',
  topic: 'Algebra',
  level: 'beginner',
  parameters: {},
  status: 'draft',
  published_to_assignment_id: null,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
}

const approvedRootNode = {
  id: 'n1',
  draft_id: 'draft-1',
  parent_id: null,
  node_type: 'root' as const,
  ordering: 1,
  status: 'approved' as const,
  payload: { title: 'Algebra' },
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
}

const unapprovedRootNode = {
  ...approvedRootNode,
  status: 'awaiting_review' as const,
}

function buildRouter(draftId = 'draft-1') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/admin/drafts/:id', component: DraftEditorView },
      { path: '/admin/drafts', component: { template: '<div>Drafts</div>' } },
      { path: '/admin/assignments/:id', component: { template: '<div>Assignment</div>' } },
    ],
  })
  router.push(`/admin/drafts/${draftId}`)
  return router
}

// @{"verifies": ["REQ-FEADMIN-710", "REQ-SYS-077"]}
describe('DraftEditorView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  // @{"verifies": ["REQ-FEADMIN-710"]}
  it('DraftEditorView_OnMount_LoadsDraftAndNodes', async () => {
    const { getDraft } = await import('@/api/tree')
    vi.mocked(getDraft).mockResolvedValue({
      draft: approvedDraft,
      nodes: [approvedRootNode],
    })

    const router = buildRouter()
    await router.isReady()

    mount(DraftEditorView, { global: { plugins: [router] } })
    await flushPromises()

    expect(getDraft).toHaveBeenCalledOnce()
    expect(getDraft).toHaveBeenCalledWith('draft-1', null)
  })

  // @{"verifies": ["REQ-FEADMIN-710"]}
  it('DraftEditorView_WithNodes_RendersTreeView', async () => {
    const { getDraft } = await import('@/api/tree')
    vi.mocked(getDraft).mockResolvedValue({
      draft: approvedDraft,
      nodes: [approvedRootNode],
    })

    const router = buildRouter()
    await router.isReady()

    const wrapper = mount(DraftEditorView, { global: { plugins: [router] } })
    await flushPromises()

    expect(wrapper.find('.tree-view-stub').exists()).toBe(true)
    expect(wrapper.find('.tree-view-stub').text()).toContain('1 nodes')
  })

  // @{"verifies": ["REQ-FEADMIN-710"]}
  it('DraftEditorView_LoadError_ShowsErrorBanner', async () => {
    const { getDraft } = await import('@/api/tree')
    const { ApiError } = await import('@/api/client')
    vi.mocked(getDraft).mockRejectedValue(new ApiError(404, { message: 'Not Found' }))

    const router = buildRouter()
    await router.isReady()

    const wrapper = mount(DraftEditorView, { global: { plugins: [router] } })
    await flushPromises()

    expect(wrapper.find('.editor-error').exists()).toBe(true)
    expect(wrapper.text()).toContain('Failed to load draft')
  })

  // @{"verifies": ["REQ-FEADMIN-710"]}
  it('DraftEditorView_UnapprovedNode_PublishButtonIsDisabled', async () => {
    const { getDraft } = await import('@/api/tree')
    vi.mocked(getDraft).mockResolvedValue({
      draft: approvedDraft,
      nodes: [unapprovedRootNode], // node is awaiting_review, NOT approved
    })

    const router = buildRouter()
    await router.isReady()

    const wrapper = mount(DraftEditorView, { global: { plugins: [router] } })
    await flushPromises()

    const publishBtn = wrapper.find('.publish-button')
    expect(publishBtn.exists()).toBe(true)
    expect((publishBtn.element as HTMLButtonElement).disabled).toBe(true)
  })

  // @{"verifies": ["REQ-FEADMIN-710"]}
  it('DraftEditorView_AllNodesApproved_PublishButtonIsEnabled', async () => {
    const { getDraft } = await import('@/api/tree')
    vi.mocked(getDraft).mockResolvedValue({
      draft: approvedDraft,
      nodes: [approvedRootNode], // node IS approved
    })

    const router = buildRouter()
    await router.isReady()

    const wrapper = mount(DraftEditorView, { global: { plugins: [router] } })
    await flushPromises()

    const publishBtn = wrapper.find('.publish-button')
    expect(publishBtn.exists()).toBe(true)
    expect((publishBtn.element as HTMLButtonElement).disabled).toBe(false)
  })

  // @{"verifies": ["REQ-FEADMIN-710"]}
  it('DraftEditorView_EmptyNodeList_PublishButtonIsDisabled', async () => {
    const { getDraft } = await import('@/api/tree')
    vi.mocked(getDraft).mockResolvedValue({
      draft: approvedDraft,
      nodes: [], // no nodes at all
    })

    const router = buildRouter()
    await router.isReady()

    const wrapper = mount(DraftEditorView, { global: { plugins: [router] } })
    await flushPromises()

    const publishBtn = wrapper.find('.publish-button')
    expect(publishBtn.exists()).toBe(true)
    // allNodesApproved requires nodes.length > 0
    expect((publishBtn.element as HTMLButtonElement).disabled).toBe(true)
  })

  // @{"verifies": ["REQ-FEADMIN-710"]}
  it('DraftEditorView_PublishSuccess_NavigatesToAssignmentRoute', async () => {
    const { getDraft, publishDraft } = await import('@/api/tree')
    vi.mocked(getDraft).mockResolvedValue({
      draft: approvedDraft,
      nodes: [approvedRootNode],
    })
    vi.mocked(publishDraft).mockResolvedValue({
      assignment_id: 'assignment-42',
      draft: { ...approvedDraft, status: 'published' },
    })

    const router = buildRouter()
    await router.isReady()
    const pushSpy = vi.spyOn(router, 'push')

    const wrapper = mount(DraftEditorView, { global: { plugins: [router] } })
    await flushPromises()

    const publishBtn = wrapper.find('.publish-button')
    await publishBtn.trigger('click')
    await flushPromises()

    expect(publishDraft).toHaveBeenCalledOnce()
    expect(pushSpy).toHaveBeenCalledWith('/admin/assignments/assignment-42')
  })

  // @{"verifies": ["REQ-FEADMIN-710"]}
  it('DraftEditorView_Publish409DraftHasUnapprovedNodes_ShowsErrorBanner', async () => {
    const { getDraft, publishDraft } = await import('@/api/tree')
    const { ApiError } = await import('@/api/client')
    vi.mocked(getDraft).mockResolvedValue({
      draft: approvedDraft,
      nodes: [approvedRootNode],
    })
    vi.mocked(publishDraft).mockRejectedValue(
      new ApiError(409, { error: 'DRAFT_HAS_UNAPPROVED_NODES' }),
    )

    const router = buildRouter()
    await router.isReady()

    const wrapper = mount(DraftEditorView, { global: { plugins: [router] } })
    await flushPromises()

    const publishBtn = wrapper.find('.publish-button')
    await publishBtn.trigger('click')
    await flushPromises()

    const errorBanner = wrapper.find('[data-testid="publish-error-banner"]')
    expect(errorBanner.exists()).toBe(true)
    // The view surfaces: "Cannot publish: all nodes must be approved first."
    expect(errorBanner.text()).toContain('approved first')
  })

  // @{"verifies": ["REQ-FEADMIN-710"]}
  it('DraftEditorView_DraftTitle_DisplaysFromTitleFallingBackToTopic', async () => {
    const { getDraft } = await import('@/api/tree')
    vi.mocked(getDraft).mockResolvedValue({
      draft: { ...approvedDraft, title: 'Abstract Algebra' },
      nodes: [approvedRootNode],
    })

    const router = buildRouter()
    await router.isReady()

    const wrapper = mount(DraftEditorView, { global: { plugins: [router] } })
    await flushPromises()

    expect(wrapper.text()).toContain('Abstract Algebra')
  })

  // @{"verifies": ["REQ-FEADMIN-710"]}
  it('DraftEditorView_PublishedDraft_HidesPublishButtonShowsPublishedNotice', async () => {
    const { getDraft } = await import('@/api/tree')
    vi.mocked(getDraft).mockResolvedValue({
      draft: { ...approvedDraft, status: 'published', published_to_assignment_id: 'asgn-1' },
      nodes: [approvedRootNode],
    })

    const router = buildRouter()
    await router.isReady()

    const wrapper = mount(DraftEditorView, { global: { plugins: [router] } })
    await flushPromises()

    // Published drafts hide the Publish button and show a "Published" notice
    expect(wrapper.find('.publish-button').exists()).toBe(false)
    expect(wrapper.find('.published-notice').exists()).toBe(true)
    expect(wrapper.find('.published-notice').text()).toContain('Published')
  })

  // @{"verifies": ["REQ-FEADMIN-710"]}
  it('DraftEditorView_MixedNodeApproval_PublishButtonDisabledPositiveControl', async () => {
    // Positive control: even ONE unapproved node disables Publish
    const { getDraft } = await import('@/api/tree')
    vi.mocked(getDraft).mockResolvedValue({
      draft: approvedDraft,
      nodes: [
        approvedRootNode,
        {
          ...approvedRootNode,
          id: 'n2',
          node_type: 'syllabus' as const,
          status: 'awaiting_review' as const,
        },
      ],
    })

    const router = buildRouter()
    await router.isReady()

    const wrapper = mount(DraftEditorView, { global: { plugins: [router] } })
    await flushPromises()

    const publishBtn = wrapper.find('.publish-button')
    expect((publishBtn.element as HTMLButtonElement).disabled).toBe(true)
  })

  // @{"verifies": ["REQ-FEADMIN-710"]}
  it('DraftEditorView_Publish409DraftAlreadyPublished_ShowsErrorMessage', async () => {
    const { getDraft, publishDraft } = await import('@/api/tree')
    const { ApiError } = await import('@/api/client')
    vi.mocked(getDraft).mockResolvedValue({
      draft: approvedDraft,
      nodes: [approvedRootNode],
    })
    vi.mocked(publishDraft).mockRejectedValue(
      new ApiError(409, { error: 'DRAFT_ALREADY_PUBLISHED' }),
    )
    // Refresh should succeed after the 409
    vi.mocked(getDraft).mockResolvedValueOnce({
      draft: approvedDraft,
      nodes: [approvedRootNode],
    })

    const router = buildRouter()
    await router.isReady()

    const wrapper = mount(DraftEditorView, { global: { plugins: [router] } })
    await flushPromises()

    const publishBtn = wrapper.find('.publish-button')
    await publishBtn.trigger('click')
    await flushPromises()

    const errorBanner = wrapper.find('[data-testid="publish-error-banner"]')
    expect(errorBanner.exists()).toBe(true)
    expect(errorBanner.text()).toContain('already been published')
  })
})
