// @{"verifies": ["REQ-SYS-073", "REQ-SYS-077", "REQ-FEADMIN-709"]}

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import TreeView from './TreeView.vue'
import type { CourseNode } from '@/types/tree'

// Stub child components so TreeView tests focus on grouping/ordering logic only.
vi.mock('@/components/tree/NodeCard.vue', () => ({
  default: {
    name: 'NodeCard',
    props: ['node', 'statusOverride', 'onApprove', 'onFeedback', 'onRegenerate', 'onRefine', 'onLoadChat'],
    template: '<div class="node-card-stub" :data-node-id="node.id" :data-status-override="statusOverride">{{ node.node_type }}</div>',
  },
}))

vi.mock('@/components/tree/LayerExpandControl.vue', () => ({
  default: {
    name: 'LayerExpandControl',
    props: ['layer', 'nextLayer', 'onExpand'],
    template: '<div class="layer-expand-stub" :data-layer="layer">expand-{{ layer }}</div>',
  },
}))

// Helper to build minimal CourseNode fixtures.
function makeNode(overrides: Partial<CourseNode> & { id: string; node_type: CourseNode['node_type']; ordering: number }): CourseNode {
  return {
    course_id: 'course-1',
    parent_id: null,
    status: 'awaiting_review',
    payload: {},
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

const noopAsync = async () => {}

const defaultProps = {
  nodes: [] as CourseNode[],
  nodeStatuses: {} as Record<string, string>,
  layersAwaitingExpand: new Set<string>(),
  onApproveNode: noopAsync,
  onFeedbackNode: async (_id: string, _fb: string) => {},
  onRegenerateNode: noopAsync,
  onRefineNode: async (_id: string, _msg: string) => {},
  onLoadNodeChat: async (_id: string) => ([] as import('@/types/tree').NodeChatMessage[]),
  onExpandLayer: async (_layer: string) => {},
}

// @{"verifies": ["REQ-SYS-073", "REQ-SYS-077"]}
describe('TreeView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('TreeView_EmptyNodes_ShowsEmptyMessage', () => {
    const wrapper = mount(TreeView, {
      props: { ...defaultProps, nodes: [] },
    })
    expect(wrapper.text()).toContain('No nodes yet')
  })

  // @{"verifies": ["REQ-SYS-073", "REQ-SYS-077"]}
  it('TreeView_MultipleNodes_GroupsByLayerInCanonicalOrder', () => {
    const nodes: CourseNode[] = [
      makeNode({ id: 'concept-1', node_type: 'concept', ordering: 1 }),
      makeNode({ id: 'root-1', node_type: 'root', ordering: 1 }),
      makeNode({ id: 'section-1', node_type: 'section_goal', ordering: 1 }),
    ]
    const wrapper = mount(TreeView, {
      props: { ...defaultProps, nodes },
    })

    // Layer headers should appear in canonical order: root → section_goal → concept
    const headers = wrapper.findAll('.layer-title')
    expect(headers.length).toBe(3)
    expect(headers[0].text()).toBe('root')
    expect(headers[1].text()).toBe('section goal')
    expect(headers[2].text()).toBe('concept')
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('TreeView_MultipleNodes_RendersOneNodeCardPerNode', () => {
    const nodes: CourseNode[] = [
      makeNode({ id: 'n1', node_type: 'root', ordering: 1 }),
      makeNode({ id: 'n2', node_type: 'root', ordering: 2 }),
      makeNode({ id: 'n3', node_type: 'section_goal', ordering: 1 }),
    ]
    const wrapper = mount(TreeView, {
      props: { ...defaultProps, nodes },
    })
    const cards = wrapper.findAll('.node-card-stub')
    expect(cards.length).toBe(3)
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('TreeView_WithStatusMap_PassesStatusOverrideToEachNodeCard', () => {
    const nodes: CourseNode[] = [
      makeNode({ id: 'n1', node_type: 'root', ordering: 1 }),
    ]
    const nodeStatuses = { 'n1': 'generating' }
    const wrapper = mount(TreeView, {
      props: { ...defaultProps, nodes, nodeStatuses },
    })
    const card = wrapper.find('.node-card-stub[data-node-id="n1"]')
    expect(card.attributes('data-status-override')).toBe('generating')
  })

  // @{"verifies": ["REQ-SYS-073", "REQ-SYS-077"]}
  it('TreeView_NodeWithNoStatusOverride_PassesNullStatusOverride', () => {
    const nodes: CourseNode[] = [
      makeNode({ id: 'n1', node_type: 'root', ordering: 1 }),
    ]
    const wrapper = mount(TreeView, {
      props: { ...defaultProps, nodes, nodeStatuses: {} },
    })
    const card = wrapper.find('.node-card-stub[data-node-id="n1"]')
    // statusOverride should be null (no override in map)
    expect(card.attributes('data-status-override')).toBeFalsy()
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('TreeView_LayerAwaitingExpand_ShowsLayerExpandControl', () => {
    const nodes: CourseNode[] = [
      makeNode({ id: 'r1', node_type: 'root', ordering: 1 }),
    ]
    const layersAwaitingExpand = new Set(['root'])
    const wrapper = mount(TreeView, {
      props: { ...defaultProps, nodes, layersAwaitingExpand },
    })
    const expandStub = wrapper.find('.layer-expand-stub[data-layer="root"]')
    expect(expandStub.exists()).toBe(true)
    expect(expandStub.text()).toContain('expand-root')
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('TreeView_LayerNotAwaitingExpand_HidesLayerExpandControl', () => {
    const nodes: CourseNode[] = [
      makeNode({ id: 'r1', node_type: 'root', ordering: 1 }),
    ]
    // 'root' is NOT in the awaiting set
    const layersAwaitingExpand = new Set<string>()
    const wrapper = mount(TreeView, {
      props: { ...defaultProps, nodes, layersAwaitingExpand },
    })
    const expandStub = wrapper.find('.layer-expand-stub')
    expect(expandStub.exists()).toBe(false)
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('TreeView_MultipleNodesSameLayer_SortsByOrderingField', () => {
    const nodes: CourseNode[] = [
      makeNode({ id: 'n3', node_type: 'section_goal', ordering: 3 }),
      makeNode({ id: 'n1', node_type: 'section_goal', ordering: 1 }),
      makeNode({ id: 'n2', node_type: 'section_goal', ordering: 2 }),
    ]
    const wrapper = mount(TreeView, {
      props: { ...defaultProps, nodes },
    })
    const cards = wrapper.findAll('.node-card-stub')
    expect(cards[0].attributes('data-node-id')).toBe('n1')
    expect(cards[1].attributes('data-node-id')).toBe('n2')
    expect(cards[2].attributes('data-node-id')).toBe('n3')
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('TreeView_AllLayersPresent_RendersAllFiveLayerHeaders', () => {
    const nodes: CourseNode[] = [
      makeNode({ id: 'r1', node_type: 'root', ordering: 1 }),
      makeNode({ id: 's1', node_type: 'syllabus', ordering: 1 }),
      makeNode({ id: 'sg1', node_type: 'section_goal', ordering: 1 }),
      makeNode({ id: 'c1', node_type: 'concept', ordering: 1 }),
      makeNode({ id: 'ct1', node_type: 'content', ordering: 1 }),
    ]
    const wrapper = mount(TreeView, {
      props: { ...defaultProps, nodes },
    })
    const headers = wrapper.findAll('.layer-title')
    expect(headers.length).toBe(5)
    expect(headers[0].text()).toBe('root')
    expect(headers[1].text()).toBe('syllabus')
    expect(headers[2].text()).toBe('section goal')
    expect(headers[3].text()).toBe('concept')
    expect(headers[4].text()).toBe('content')
  })

  // @{"verifies": ["REQ-SYS-073"]}
  it('TreeView_LayerCount_ShowsCorrectNodeCount', () => {
    const nodes: CourseNode[] = [
      makeNode({ id: 'r1', node_type: 'root', ordering: 1 }),
      makeNode({ id: 'sg1', node_type: 'section_goal', ordering: 1 }),
      makeNode({ id: 'sg2', node_type: 'section_goal', ordering: 2 }),
    ]
    const wrapper = mount(TreeView, {
      props: { ...defaultProps, nodes },
    })
    const counts = wrapper.findAll('.layer-count')
    expect(counts[0].text()).toBe('1 node')
    expect(counts[1].text()).toBe('2 nodes')
  })
})
