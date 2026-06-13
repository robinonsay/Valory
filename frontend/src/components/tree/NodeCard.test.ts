// @{"verifies": ["REQ-SYS-073", "REQ-SYS-077"]}

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import NodeCard from './NodeCard.vue'
import type { CourseNode } from '@/types/tree'

// Stub the asciidoc renderer — the real one loads @asciidoctor/core which is
// heavy and unavailable in jsdom. The test asserts that the component calls
// renderAsciidoc and renders its output (not a second renderer).
vi.mock('@/utils/renderAsciidoc', () => ({
  renderAsciidoc: vi.fn(async (src: string) => `<p data-testid="adoc-rendered">${src}</p>`),
}))

// Helper to build minimal CourseNode fixtures.
function makeNode(overrides: Partial<CourseNode> & { id: string; node_type: CourseNode['node_type'] }): CourseNode {
  return {
    course_id: 'course-1',
    parent_id: null,
    ordering: 1,
    status: 'awaiting_review',
    payload: {},
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}


function defaultProps(overrides: Partial<{
  node: CourseNode
  statusOverride: string | null
  onApprove: () => Promise<void>
  onFeedback: (feedback: string) => Promise<void>
  onRegenerate: () => Promise<void>
  onRefine: (message: string) => Promise<void>
  onLoadChat: () => Promise<{ id: string; role: 'user' | 'assistant'; content: string; created_at: string }[]>
}> = {}) {
  return {
    node: makeNode({ id: 'node-1', node_type: 'root' }),
    statusOverride: null,
    onApprove: vi.fn(async () => {}),
    onFeedback: vi.fn(async (_fb: string) => {}),
    onRegenerate: vi.fn(async () => {}),
    onRefine: vi.fn(async (_msg: string) => {}),
    onLoadChat: vi.fn(async () => []),
    ...overrides,
  }
}

// @{"verifies": ["REQ-SYS-073", "REQ-SYS-077"]}
describe('NodeCard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('NodeCard_ContentNode_RendersViaRenderAsciidocNotSecondRenderer', async () => {
    const { renderAsciidoc } = await import('@/utils/renderAsciidoc')

    const node = makeNode({
      id: 'n1',
      node_type: 'content',
      status: 'awaiting_review',
      payload: { content: '= My Section\n\nHello world.' },
    })

    const wrapper = mount(NodeCard, {
      props: defaultProps({ node }),
    })
    await flushPromises()

    // renderAsciidoc must have been called with the content payload
    expect(renderAsciidoc).toHaveBeenCalledWith('= My Section\n\nHello world.')

    // The rendered HTML should appear in the adoc-content div (via v-html)
    const adocDiv = wrapper.find('.adoc-content')
    expect(adocDiv.exists()).toBe(true)
    expect(adocDiv.html()).toContain('My Section')
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('NodeCard_SectionGoalNode_RendersStructuredPayloadAsPreTag', () => {
    const node = makeNode({
      id: 'sg1',
      node_type: 'section_goal',
      status: 'awaiting_review',
      payload: { goal: 'Learn algebraic structures', objectives: ['rings', 'fields'] },
    })

    const wrapper = mount(NodeCard, {
      props: defaultProps({ node }),
    })

    const pre = wrapper.find('.structured-payload')
    expect(pre.exists()).toBe(true)
    expect(pre.text()).toContain('goal')
    expect(pre.text()).toContain('algebraic structures')
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('NodeCard_ConceptNode_RendersStructuredPayloadAsPreTag', () => {
    const node = makeNode({
      id: 'c1',
      node_type: 'concept',
      status: 'awaiting_review',
      payload: { title: 'Closure', explanation: 'A set closed under an operation.' },
    })

    const wrapper = mount(NodeCard, {
      props: defaultProps({ node }),
    })

    const pre = wrapper.find('.structured-payload')
    expect(pre.exists()).toBe(true)
    expect(pre.text()).toContain('Closure')
  })

  // @{"verifies": ["REQ-SYS-073", "REQ-SYS-077"]}
  it('NodeCard_AwaitingReview_ApproveButtonCallsOnApprove', async () => {
    const onApprove = vi.fn(async () => {})
    const node = makeNode({ id: 'n1', node_type: 'root', status: 'awaiting_review' })

    const wrapper = mount(NodeCard, {
      props: defaultProps({ node, onApprove }),
    })

    const approveBtn = wrapper.find('.btn-approve')
    expect(approveBtn.exists()).toBe(true)
    await approveBtn.trigger('click')
    await flushPromises()

    expect(onApprove).toHaveBeenCalledOnce()
  })

  // @{"verifies": ["REQ-SYS-073", "REQ-SYS-077"]}
  it('NodeCard_FeedbackWithEmptyComment_DoesNotCallOnFeedback', async () => {
    const onFeedback = vi.fn(async (_fb: string) => {})
    const node = makeNode({ id: 'n1', node_type: 'root', status: 'awaiting_review' })

    const wrapper = mount(NodeCard, {
      props: defaultProps({ node, onFeedback }),
    })

    // Open the feedback input
    const requestChangesBtn = wrapper.find('.btn-feedback')
    await requestChangesBtn.trigger('click')
    await wrapper.vm.$nextTick()

    // textarea should be present and empty
    const textarea = wrapper.find('.feedback-textarea')
    expect(textarea.exists()).toBe(true)
    expect((textarea.element as HTMLTextAreaElement).value).toBe('')

    // The submit button should be DISABLED when comment is empty
    const submitBtn = wrapper.find('.btn-feedback-submit')
    expect((submitBtn.element as HTMLButtonElement).disabled).toBe(true)

    // Clicking the disabled submit should NOT call onFeedback
    await submitBtn.trigger('click')
    await flushPromises()
    expect(onFeedback).not.toHaveBeenCalled()
  })

  // @{"verifies": ["REQ-SYS-073", "REQ-SYS-077"]}
  it('NodeCard_FeedbackWithNonEmptyComment_CallsOnFeedbackWithTrimmedText', async () => {
    const onFeedback = vi.fn(async (_fb: string) => {})
    const node = makeNode({ id: 'n1', node_type: 'root', status: 'awaiting_review' })

    const wrapper = mount(NodeCard, {
      props: defaultProps({ node, onFeedback }),
    })

    // Open feedback input
    await wrapper.find('.btn-feedback').trigger('click')
    await wrapper.vm.$nextTick()

    // Enter a non-empty comment
    const textarea = wrapper.find('.feedback-textarea')
    await textarea.setValue('This needs more detail about ring axioms.')
    await wrapper.vm.$nextTick()

    // Submit button should be ENABLED
    const submitBtn = wrapper.find('.btn-feedback-submit')
    expect((submitBtn.element as HTMLButtonElement).disabled).toBe(false)

    await submitBtn.trigger('click')
    await flushPromises()

    expect(onFeedback).toHaveBeenCalledOnce()
    expect(onFeedback).toHaveBeenCalledWith('This needs more detail about ring axioms.')
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('NodeCard_FeedbackWhitespaceOnly_DoesNotCallOnFeedback', async () => {
    const onFeedback = vi.fn(async (_fb: string) => {})
    const node = makeNode({ id: 'n1', node_type: 'root', status: 'awaiting_review' })

    const wrapper = mount(NodeCard, {
      props: defaultProps({ node, onFeedback }),
    })

    await wrapper.find('.btn-feedback').trigger('click')
    await wrapper.vm.$nextTick()

    const textarea = wrapper.find('.feedback-textarea')
    await textarea.setValue('   ')
    await wrapper.vm.$nextTick()

    // Whitespace-only input keeps button disabled
    const submitBtn = wrapper.find('.btn-feedback-submit')
    expect((submitBtn.element as HTMLButtonElement).disabled).toBe(true)

    await submitBtn.trigger('click')
    await flushPromises()
    expect(onFeedback).not.toHaveBeenCalled()
  })

  // @{"verifies": ["REQ-SYS-073", "REQ-SYS-077"]}
  it('NodeCard_RejectedNode_RegenerateButtonCallsOnRegenerate', async () => {
    const onRegenerate = vi.fn(async () => {})
    const node = makeNode({ id: 'n1', node_type: 'root', status: 'rejected' })

    const wrapper = mount(NodeCard, {
      props: defaultProps({ node, onRegenerate }),
    })

    const regenBtn = wrapper.find('.btn-regenerate')
    expect(regenBtn.exists()).toBe(true)
    await regenBtn.trigger('click')
    await flushPromises()

    expect(onRegenerate).toHaveBeenCalledOnce()
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('NodeCard_FailedNode_RegenerateButtonCallsOnRegenerate', async () => {
    const onRegenerate = vi.fn(async () => {})
    const node = makeNode({ id: 'n1', node_type: 'root', status: 'failed' })

    const wrapper = mount(NodeCard, {
      props: defaultProps({ node, onRegenerate }),
    })

    const regenBtn = wrapper.find('.btn-regenerate')
    expect(regenBtn.exists()).toBe(true)
    await regenBtn.trigger('click')
    await flushPromises()

    expect(onRegenerate).toHaveBeenCalledOnce()
  })

  // @{"verifies": ["REQ-SYS-073", "REQ-SYS-077"]}
  it('NodeCard_ChatOpen_LoadsHistoryViaOnLoadChat', async () => {
    const chatHistory = [
      { id: 'm1', role: 'user' as const, content: 'Please add more examples.', created_at: '2026-01-01T00:00:00Z' },
      { id: 'm2', role: 'assistant' as const, content: 'Sure, I added three examples.', created_at: '2026-01-01T00:01:00Z' },
    ]
    const onLoadChat = vi.fn(async () => chatHistory)
    const node = makeNode({ id: 'n1', node_type: 'root', status: 'awaiting_review' })

    const wrapper = mount(NodeCard, {
      props: defaultProps({ node, onLoadChat }),
    })

    // Open chat panel
    const chatToggle = wrapper.find('.chat-toggle-btn')
    await chatToggle.trigger('click')
    await flushPromises()

    expect(onLoadChat).toHaveBeenCalledOnce()

    // Chat messages should appear
    const messages = wrapper.findAll('.chat-message')
    expect(messages.length).toBe(2)
    expect(messages[0].text()).toContain('Please add more examples.')
    expect(messages[1].text()).toContain('Sure, I added three examples.')
  })

  // @{"verifies": ["REQ-SYS-073", "REQ-SYS-077"]}
  it('NodeCard_ChatSendRefineMessage_CallsOnRefineAndReloadsHistory', async () => {
    const onRefine = vi.fn(async (_msg: string) => {})
    const onLoadChat = vi.fn(async () => [])
    const node = makeNode({ id: 'n1', node_type: 'root', status: 'awaiting_review' })

    const wrapper = mount(NodeCard, {
      props: defaultProps({ node, onRefine, onLoadChat }),
    })

    // Open chat
    await wrapper.find('.chat-toggle-btn').trigger('click')
    await flushPromises()

    // Clear call count from open
    onLoadChat.mockClear()

    // Type a message
    const chatTextarea = wrapper.find('.chat-textarea')
    await chatTextarea.setValue('Can you make this more concise?')
    await wrapper.vm.$nextTick()

    // Send button should be enabled
    const sendBtn = wrapper.find('.btn-send')
    expect((sendBtn.element as HTMLButtonElement).disabled).toBe(false)

    await sendBtn.trigger('click')
    await flushPromises()

    expect(onRefine).toHaveBeenCalledOnce()
    expect(onRefine).toHaveBeenCalledWith('Can you make this more concise?')
    // onLoadChat is called again to reload history after refine
    expect(onLoadChat).toHaveBeenCalledOnce()
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('NodeCard_StatusOverride_TakesPrecedenceOverNodeStatus', () => {
    const node = makeNode({ id: 'n1', node_type: 'root', status: 'pending' })

    const wrapper = mount(NodeCard, {
      props: defaultProps({ node, statusOverride: 'generating' }),
    })

    // The status badge should reflect the SSE override, not the stored status
    const badge = wrapper.find('.status-badge')
    expect(badge.text()).toContain('generating')
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('NodeCard_ApprovedNode_DoesNotShowHITLControls', () => {
    const node = makeNode({ id: 'n1', node_type: 'root', status: 'approved' })

    const wrapper = mount(NodeCard, {
      props: defaultProps({ node }),
    })

    // No HITL controls for approved nodes
    expect(wrapper.find('.btn-approve').exists()).toBe(false)
    expect(wrapper.find('.btn-feedback').exists()).toBe(false)
    expect(wrapper.find('.btn-regenerate').exists()).toBe(false)
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('NodeCard_AwaitingReview_ShowsApproveAndFeedbackButNotRegenerate', () => {
    const node = makeNode({ id: 'n1', node_type: 'root', status: 'awaiting_review' })

    const wrapper = mount(NodeCard, {
      props: defaultProps({ node }),
    })

    expect(wrapper.find('.btn-approve').exists()).toBe(true)
    expect(wrapper.find('.btn-feedback').exists()).toBe(true)
    expect(wrapper.find('.btn-regenerate').exists()).toBe(false)
  })
})
