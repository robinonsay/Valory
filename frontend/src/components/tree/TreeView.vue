// @{"req": ["REQ-SYS-073", "REQ-SYS-077", "REQ-FEADMIN-709"]}

<script setup lang="ts">
// TreeView renders the full knowledge-tree: nodes grouped by layer with live
// SSE status, HITL controls, and layer-expand prompts.
//
// Actor-agnostic design: the parent view supplies:
//   - nodes: the flat node list (from GET .../nodes or GET /admin/drafts/{id})
//   - nodeStatuses: live status map from useNodeEvents (keyed by node id)
//   - layersAwaitingExpand: set of layer names ready for expansion (from SSE)
//   - api callbacks: approve/feedback/regenerate/refine/loadChat/expandLayer
//     bound to either student or admin endpoints.
//
// This component does NOT own state — it is a pure rendering/coordination
// layer. The parent view drives it via props.

import { computed } from 'vue'
import NodeCard from '@/components/tree/NodeCard.vue'
import LayerExpandControl from '@/components/tree/LayerExpandControl.vue'
import type { CourseNode, DraftNode, NodeChatMessage, NodeType } from '@/types/tree'

type AnyNode = CourseNode | DraftNode

// The layer order matches the node_type_enum sequence.
const LAYER_ORDER: NodeType[] = ['root', 'syllabus', 'section_goal', 'concept', 'content']
const NEXT_LAYER_MAP: Partial<Record<NodeType, NodeType>> = {
  root: 'syllabus',
  syllabus: 'section_goal',
  section_goal: 'concept',
  concept: 'content',
}

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

// @{"req": ["REQ-SYS-073", "REQ-SYS-077"]}
interface Props {
  nodes: AnyNode[]
  // nodeStatuses: live status overrides from the SSE composable (node id → status).
  nodeStatuses: Record<string, string>
  // layersAwaitingExpand: layers for which layer_awaiting_expand was received.
  layersAwaitingExpand: Set<string>

  // Actor-specific API callbacks — supplied by parent; signature is identical
  // for student and admin (same shapes, different endpoints).
  onApproveNode: (nodeId: string) => Promise<void>
  onFeedbackNode: (nodeId: string, feedback: string) => Promise<void>
  onRegenerateNode: (nodeId: string) => Promise<void>
  onRefineNode: (nodeId: string, message: string) => Promise<void>
  onLoadNodeChat: (nodeId: string) => Promise<NodeChatMessage[]>
  onExpandLayer: (layer: string) => Promise<void>
}

const props = defineProps<Props>()

// ---------------------------------------------------------------------------
// Group nodes by layer in display order
// ---------------------------------------------------------------------------

// @{"req": ["REQ-SYS-073", "REQ-SYS-077"]}
const layerGroups = computed(() => {
  const byType: Partial<Record<NodeType, AnyNode[]>> = {}
  for (const node of props.nodes) {
    const t = node.node_type as NodeType
    if (!byType[t]) byType[t] = []
    byType[t]!.push(node)
  }

  // Sort each layer's nodes by ordering field.
  for (const key of Object.keys(byType) as NodeType[]) {
    byType[key]!.sort((a, b) => a.ordering - b.ordering)
  }

  // Return only layers present in the nodes list, in canonical order.
  return LAYER_ORDER.filter(l => byType[l]?.length).map(l => ({
    layer: l,
    nodes: byType[l]!,
    nextLayer: NEXT_LAYER_MAP[l] ?? null,
  }))
})
</script>

<template>
  <div class="tree-view">
    <div v-if="nodes.length === 0" class="tree-empty">
      No nodes yet. Use the generate controls to begin building the tree.
    </div>

    <div
      v-for="group in layerGroups"
      :key="group.layer"
      class="layer-group"
    >
      <!-- Layer header -->
      <div class="layer-header">
        <h3 class="layer-title">{{ group.layer.replace(/_/g, ' ') }}</h3>
        <span class="layer-count">{{ group.nodes.length }} node{{ group.nodes.length !== 1 ? 's' : '' }}</span>
      </div>

      <!-- Layer expand prompt (shown when SSE emitted layer_awaiting_expand) -->
      <LayerExpandControl
        v-if="layersAwaitingExpand.has(group.layer) && group.nextLayer"
        :layer="group.layer"
        :next-layer="group.nextLayer ?? undefined"
        :on-expand="() => onExpandLayer(group.layer)"
      />

      <!-- Nodes in this layer -->
      <NodeCard
        v-for="node in group.nodes"
        :key="node.id"
        :node="node"
        :status-override="nodeStatuses[node.id] ?? null"
        :on-approve="() => onApproveNode(node.id)"
        :on-feedback="(feedback: string) => onFeedbackNode(node.id, feedback)"
        :on-regenerate="() => onRegenerateNode(node.id)"
        :on-refine="(msg: string) => onRefineNode(node.id, msg)"
        :on-load-chat="() => onLoadNodeChat(node.id)"
      />
    </div>
  </div>
</template>

<style scoped>
.tree-view {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.tree-empty {
  text-align: center;
  color: #999;
  font-style: italic;
  padding: 2rem 0;
}

.layer-group {
  border: 1px solid #e0e0e0;
  border-radius: 8px;
  overflow: hidden;
}

.layer-header {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.75rem 1rem;
  background: #fafafa;
  border-bottom: 1px solid #e0e0e0;
}

.layer-title {
  font-size: 1rem;
  font-weight: 600;
  color: #333;
  margin: 0;
  text-transform: capitalize;
}

.layer-count {
  font-size: 0.8rem;
  color: #888;
}
</style>
