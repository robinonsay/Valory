// @{"verifies": ["REQ-FEADMIN-710", "REQ-SYS-077"]}

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import DraftListView from './DraftListView.vue'
import { useAuthStore } from '@/stores/auth'

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
  listDrafts: vi.fn(),
  createDraft: vi.fn(),
  deleteDraft: vi.fn(),
}))

function buildRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/admin/drafts', component: DraftListView },
      { path: '/admin/drafts/:id', component: { template: '<div>Editor</div>' } },
    ],
  })
}

// @{"verifies": ["REQ-FEADMIN-710", "REQ-SYS-077"]}
describe('DraftListView', () => {
  let router: ReturnType<typeof buildRouter>

  beforeEach(async () => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    router = buildRouter()
    router.push('/admin/drafts')
    await router.isReady()
  })

  // @{"verifies": ["REQ-FEADMIN-710"]}
  it('DraftListView_OnMount_LoadsDraftsFromAPI', async () => {
    const { listDrafts } = await import('@/api/tree')
    vi.mocked(listDrafts).mockResolvedValue({ drafts: [], next_cursor: null })

    mount(DraftListView, { global: { plugins: [router] } })
    await flushPromises()

    expect(listDrafts).toHaveBeenCalledOnce()
    expect(listDrafts).toHaveBeenCalledWith({ limit: 20 }, null)
  })

  // @{"verifies": ["REQ-FEADMIN-710"]}
  it('DraftListView_WithDrafts_RendersTableRows', async () => {
    const { listDrafts } = await import('@/api/tree')
    vi.mocked(listDrafts).mockResolvedValue({
      drafts: [
        { id: 'd1', title: 'Algebra Course', topic: 'Algebra', level: 'beginner', status: 'draft', created_at: '2026-01-01T00:00:00Z' },
        { id: 'd2', title: 'Physics Course', topic: 'Physics', level: 'intermediate', status: 'published', created_at: '2026-02-01T00:00:00Z' },
      ],
      next_cursor: null,
    })

    const wrapper = mount(DraftListView, { global: { plugins: [router] } })
    await flushPromises()

    expect(wrapper.text()).toContain('Algebra Course')
    expect(wrapper.text()).toContain('Physics Course')
    // Both rows should be in the table
    const rows = wrapper.findAll('.draft-row')
    expect(rows.length).toBe(2)
  })

  // @{"verifies": ["REQ-FEADMIN-710"]}
  it('DraftListView_NoDrafts_ShowsEmptyState', async () => {
    const { listDrafts } = await import('@/api/tree')
    vi.mocked(listDrafts).mockResolvedValue({ drafts: [], next_cursor: null })

    const wrapper = mount(DraftListView, { global: { plugins: [router] } })
    await flushPromises()

    expect(wrapper.text()).toContain('No drafts found')
  })

  // @{"verifies": ["REQ-FEADMIN-710"]}
  it('DraftListView_StatusFilter_ReloadsWithFilterParam', async () => {
    const { listDrafts } = await import('@/api/tree')
    vi.mocked(listDrafts).mockResolvedValue({ drafts: [], next_cursor: null })

    const wrapper = mount(DraftListView, { global: { plugins: [router] } })
    await flushPromises()

    // Change the filter select to 'draft'
    const select = wrapper.find('#status-filter')
    await select.setValue('draft')
    await select.trigger('change')
    await flushPromises()

    expect(listDrafts).toHaveBeenCalledWith({ limit: 20, status: 'draft' }, null)
  })

  // @{"verifies": ["REQ-FEADMIN-710"]}
  it('DraftListView_NextCursor_ShowsLoadMoreButton', async () => {
    const { listDrafts } = await import('@/api/tree')
    vi.mocked(listDrafts).mockResolvedValue({
      drafts: [
        { id: 'd1', title: 'Draft 1', topic: 'Topic', level: 'beginner', status: 'draft', created_at: '2026-01-01T00:00:00Z' },
      ],
      next_cursor: 'cursor-abc',
    })

    const wrapper = mount(DraftListView, { global: { plugins: [router] } })
    await flushPromises()

    const loadMoreBtn = wrapper.find('.load-more-button')
    expect(loadMoreBtn.exists()).toBe(true)
  })

  // @{"verifies": ["REQ-FEADMIN-710"]}
  it('DraftListView_LoadMoreClick_FetchesWithCursor', async () => {
    const { listDrafts } = await import('@/api/tree')
    vi.mocked(listDrafts)
      .mockResolvedValueOnce({
        drafts: [
          { id: 'd1', title: 'Draft 1', topic: 'Topic', level: 'beginner', status: 'draft', created_at: '2026-01-01T00:00:00Z' },
        ],
        next_cursor: 'cursor-abc',
      })
      .mockResolvedValueOnce({ drafts: [], next_cursor: null })

    const wrapper = mount(DraftListView, { global: { plugins: [router] } })
    await flushPromises()

    const loadMoreBtn = wrapper.find('.load-more-button')
    await loadMoreBtn.trigger('click')
    await flushPromises()

    // Second call should include cursor
    expect(listDrafts).toHaveBeenNthCalledWith(2, { limit: 20, cursor: 'cursor-abc' }, null)
  })

  // @{"verifies": ["REQ-FEADMIN-710"]}
  it('DraftListView_CreateWithEmptyTitle_SubmitButtonDisabled', async () => {
    const { listDrafts } = await import('@/api/tree')
    vi.mocked(listDrafts).mockResolvedValue({ drafts: [], next_cursor: null })

    const wrapper = mount(DraftListView, { global: { plugins: [router] } })
    await flushPromises()

    // Open create modal
    await wrapper.find('.create-button').trigger('click')
    await wrapper.vm.$nextTick()

    // Fill topic but leave title empty
    const topicInput = wrapper.find('#draft-topic')
    await topicInput.setValue('Algebra')
    await wrapper.vm.$nextTick()

    // Submit button disabled because title is empty
    const submitBtn = wrapper.find('.submit-button')
    expect((submitBtn.element as HTMLButtonElement).disabled).toBe(true)
  })

  // @{"verifies": ["REQ-FEADMIN-710"]}
  it('DraftListView_CreateWithNonEmptyTitle_CallsCreateDraft', async () => {
    const { listDrafts, createDraft } = await import('@/api/tree')
    vi.mocked(listDrafts).mockResolvedValue({ drafts: [], next_cursor: null })
    vi.mocked(createDraft).mockResolvedValue({
      draft: { id: 'new-draft', title: 'My Draft', topic: 'Algebra', level: 'beginner',
        status: 'draft', admin_id: 'admin-1', parameters: {},
        created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' },
    })

    const wrapper = mount(DraftListView, { global: { plugins: [router] } })
    await flushPromises()

    await wrapper.find('.create-button').trigger('click')
    await wrapper.vm.$nextTick()

    // Fill in title and topic
    await wrapper.find('#draft-title').setValue('Introduction to Algebra')
    await wrapper.find('#draft-topic').setValue('Algebra')
    await wrapper.vm.$nextTick()

    // Submit
    const submitBtn = wrapper.find('.submit-button')
    expect((submitBtn.element as HTMLButtonElement).disabled).toBe(false)

    await wrapper.find('.modal-form').trigger('submit')
    await flushPromises()

    expect(createDraft).toHaveBeenCalledOnce()
    expect(createDraft).toHaveBeenCalledWith(
      expect.objectContaining({ title: 'Introduction to Algebra', topic: 'Algebra' }),
      null,
    )
  })

  // @{"verifies": ["REQ-FEADMIN-710"]}
  it('DraftListView_CreateNavigatesToEditor', async () => {
    const { listDrafts, createDraft } = await import('@/api/tree')
    vi.mocked(listDrafts).mockResolvedValue({ drafts: [], next_cursor: null })
    vi.mocked(createDraft).mockResolvedValue({
      draft: { id: 'new-draft', title: 'My Draft', topic: 'Algebra', level: 'beginner',
        status: 'draft', admin_id: 'admin-1', parameters: {},
        created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' },
    })

    const pushSpy = vi.spyOn(router, 'push')

    const wrapper = mount(DraftListView, { global: { plugins: [router] } })
    await flushPromises()

    await wrapper.find('.create-button').trigger('click')
    await wrapper.vm.$nextTick()

    await wrapper.find('#draft-title').setValue('My Draft')
    await wrapper.find('#draft-topic').setValue('Algebra')
    await wrapper.vm.$nextTick()

    await wrapper.find('.modal-form').trigger('submit')
    await flushPromises()

    expect(pushSpy).toHaveBeenCalledWith('/admin/drafts/new-draft')
  })

  // @{"verifies": ["REQ-FEADMIN-710"]}
  it('DraftListView_DeletePublishedDraft_Shows409DraftAlreadyPublished', async () => {
    const { listDrafts, deleteDraft } = await import('@/api/tree')
    const { ApiError } = await import('@/api/client')

    vi.mocked(listDrafts).mockResolvedValue({
      drafts: [
        { id: 'd1', title: 'Published Draft', topic: 'Math', level: 'beginner', status: 'published', created_at: '2026-01-01T00:00:00Z' },
      ],
      next_cursor: null,
    })
    vi.mocked(deleteDraft).mockRejectedValue(
      new ApiError(409, { error: 'DRAFT_ALREADY_PUBLISHED' }),
    )

    // Mock window.confirm to return true
    vi.spyOn(window, 'confirm').mockReturnValue(true)

    const wrapper = mount(DraftListView, { global: { plugins: [router] } })
    await flushPromises()

    const deleteBtn = wrapper.find('.delete-button')
    await deleteBtn.trigger('click')
    await flushPromises()

    const errorBanner = wrapper.find('[data-testid="delete-error-banner"]')
    expect(errorBanner.exists()).toBe(true)
    expect(errorBanner.text()).toContain('already been published')
  })

  // @{"verifies": ["REQ-FEADMIN-710"]}
  it('DraftListView_DeleteSuccess_RemovesDraftFromList', async () => {
    const { listDrafts, deleteDraft } = await import('@/api/tree')

    vi.mocked(listDrafts).mockResolvedValue({
      drafts: [
        { id: 'd1', title: 'Draft One', topic: 'Math', level: 'beginner', status: 'draft', created_at: '2026-01-01T00:00:00Z' },
        { id: 'd2', title: 'Draft Two', topic: 'Physics', level: 'beginner', status: 'draft', created_at: '2026-01-01T00:00:00Z' },
      ],
      next_cursor: null,
    })
    vi.mocked(deleteDraft).mockResolvedValue(undefined)

    vi.spyOn(window, 'confirm').mockReturnValue(true)

    const wrapper = mount(DraftListView, { global: { plugins: [router] } })
    await flushPromises()

    expect(wrapper.findAll('.draft-row').length).toBe(2)

    const deleteBtn = wrapper.findAll('.delete-button')[0]
    await deleteBtn.trigger('click')
    await flushPromises()

    expect(wrapper.findAll('.draft-row').length).toBe(1)
    expect(wrapper.text()).not.toContain('Draft One')
    expect(wrapper.text()).toContain('Draft Two')
  })

  // @{"verifies": ["REQ-FEADMIN-710"]}
  it('DraftListView_CreateWithEmptyTitle_ButtonDisabledPositiveControl', async () => {
    // Positive control: button IS enabled when title is non-empty
    const { listDrafts } = await import('@/api/tree')
    vi.mocked(listDrafts).mockResolvedValue({ drafts: [], next_cursor: null })

    const wrapper = mount(DraftListView, { global: { plugins: [router] } })
    await flushPromises()

    await wrapper.find('.create-button').trigger('click')
    await wrapper.vm.$nextTick()

    await wrapper.find('#draft-title').setValue('My Title')
    await wrapper.find('#draft-topic').setValue('My Topic')
    await wrapper.vm.$nextTick()

    const submitBtn = wrapper.find('.submit-button')
    expect((submitBtn.element as HTMLButtonElement).disabled).toBe(false)
  })
})
