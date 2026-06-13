// @{"verifies": ["REQ-SYS-073", "REQ-SYS-077"]}

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import LayerExpandControl from './LayerExpandControl.vue'

// The component imports ApiError from @/api/client — mock it so instanceof checks work.
vi.mock('@/api/client', () => ({
  ApiError: class ApiError extends Error {
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

// @{"verifies": ["REQ-SYS-073", "REQ-SYS-077"]}
describe('LayerExpandControl', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('LayerExpandControl_Default_ShowsExpandButton', () => {
    const wrapper = mount(LayerExpandControl, {
      props: {
        layer: 'section_goal',
        nextLayer: 'concept',
        onExpand: vi.fn(async () => {}),
      },
    })

    const btn = wrapper.find('.btn-expand')
    expect(btn.exists()).toBe(true)
    expect(btn.text()).toContain('Expand to concept')
    expect((btn.element as HTMLButtonElement).disabled).toBe(false)
  })

  // @{"verifies": ["REQ-SYS-073", "REQ-SYS-077"]}
  it('LayerExpandControl_ExpandClick_CallsOnExpand', async () => {
    const onExpand = vi.fn(async () => {})
    const wrapper = mount(LayerExpandControl, {
      props: {
        layer: 'root',
        nextLayer: 'syllabus',
        onExpand,
      },
    })

    await wrapper.find('.btn-expand').trigger('click')
    await flushPromises()

    expect(onExpand).toHaveBeenCalledOnce()
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('LayerExpandControl_ExpandSuccess_ShowsSuccessMessage', async () => {
    const onExpand = vi.fn(async () => {})
    const wrapper = mount(LayerExpandControl, {
      props: {
        layer: 'root',
        nextLayer: 'syllabus',
        onExpand,
      },
    })

    await wrapper.find('.btn-expand').trigger('click')
    await flushPromises()

    const success = wrapper.find('.expand-success')
    expect(success.exists()).toBe(true)
    expect(success.text()).toContain('syllabus')
  })

  // @{"verifies": ["REQ-SYS-073", "REQ-SYS-077"]}
  it('LayerExpandControl_409LayerNotFullyApproved_ShowsVisibleErrorMessage', async () => {
    const { ApiError: MockApiError } = await import('@/api/client')
    const onExpand = vi.fn(async () => {
      throw new MockApiError(409, { error: 'LAYER_NOT_FULLY_APPROVED' })
    })

    const wrapper = mount(LayerExpandControl, {
      props: {
        layer: 'root',
        nextLayer: 'syllabus',
        onExpand,
      },
    })

    await wrapper.find('.btn-expand').trigger('click')
    await flushPromises()

    const errorEl = wrapper.find('.expand-error')
    expect(errorEl.exists()).toBe(true)
    expect(errorEl.text()).toContain('Not all nodes in this layer are approved')
  })

  // @{"verifies": ["REQ-SYS-073", "REQ-SYS-077"]}
  it('LayerExpandControl_409TreeAlreadyComplete_ShowsVisibleErrorMessage', async () => {
    const { ApiError: MockApiError } = await import('@/api/client')
    const onExpand = vi.fn(async () => {
      throw new MockApiError(409, { error: 'TREE_ALREADY_COMPLETE' })
    })

    const wrapper = mount(LayerExpandControl, {
      props: {
        layer: 'concept',
        nextLayer: 'content',
        onExpand,
      },
    })

    await wrapper.find('.btn-expand').trigger('click')
    await flushPromises()

    const errorEl = wrapper.find('.expand-error')
    expect(errorEl.exists()).toBe(true)
    expect(errorEl.text()).toContain('already complete')
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('LayerExpandControl_GenericError_ShowsGenericErrorMessage', async () => {
    const onExpand = vi.fn(async () => {
      throw new Error('Network timeout')
    })

    const wrapper = mount(LayerExpandControl, {
      props: {
        layer: 'root',
        nextLayer: 'syllabus',
        onExpand,
      },
    })

    await wrapper.find('.btn-expand').trigger('click')
    await flushPromises()

    const errorEl = wrapper.find('.expand-error')
    expect(errorEl.exists()).toBe(true)
    expect(errorEl.text()).toContain('Network timeout')
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('LayerExpandControl_DuringExpand_ButtonIsDisabled', async () => {
    let resolveExpand!: () => void
    const onExpand = vi.fn(async () => {
      await new Promise<void>(resolve => { resolveExpand = resolve })
    })

    const wrapper = mount(LayerExpandControl, {
      props: {
        layer: 'root',
        nextLayer: 'syllabus',
        onExpand,
      },
    })

    const btn = wrapper.find('.btn-expand')
    // Start the expand (do not await)
    btn.trigger('click')
    await wrapper.vm.$nextTick()

    // Button should be disabled while pending
    expect((btn.element as HTMLButtonElement).disabled).toBe(true)
    expect(btn.text()).toContain('Expanding...')

    // Resolve and cleanup
    resolveExpand!()
    await flushPromises()
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('LayerExpandControl_ShowsLayerAndNextLayerNames', () => {
    const wrapper = mount(LayerExpandControl, {
      props: {
        layer: 'section_goal',
        nextLayer: 'concept',
        onExpand: vi.fn(async () => {}),
      },
    })

    const prompt = wrapper.find('.expand-prompt')
    expect(prompt.text()).toContain('section goal')
    expect(prompt.text()).toContain('concept')
  })
})
