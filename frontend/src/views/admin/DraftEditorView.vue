// @{"req": ["REQ-FEADMIN-710", "REQ-SYS-077"]}

<script setup lang="ts">
// DraftEditorView — admin interactive draft tree editor.
//
// Loads a draft + its nodes (getDraft), subscribes to the admin SSE stream
// via useNodeEvents, and renders T1's actor-agnostic TreeView bound to the
// admin draft API methods. Exposes a Publish action that calls publishDraft
// and navigates to the existing assignment detail view using the returned
// assignment id.
//
// Mirrors CourseBuildView's structure with:
//   - admin SSE endpoint: GET /admin/drafts/{id}/events
//   - all mutation calls routed through admin draft methods
//   - a Publish button enabled only when the draft status is ready / all nodes approved
//   - 409 conflict surfacing for DRAFT_HAS_UNAPPROVED_NODES, LAYER_NOT_FULLY_APPROVED,
//     TREE_ALREADY_COMPLETE, NODE_ALREADY_GENERATING, etc.

import { ref, computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { ApiError } from '@/api/client'
import { useNodeEvents } from '@/composables/useNodeEvents'
import TreeView from '@/components/tree/TreeView.vue'
import {
  getDraft,
  approveDraftNode,
  feedbackDraftNode,
  regenerateDraftNode,
  refineDraftNode,
  getDraftNodeChat,
  expandDraftLayer,
  publishDraft,
} from '@/api/tree'
import type { CourseDraft, DraftNode, NodeChatMessage } from '@/types/tree'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const draftId = route.params.id as string

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const draft = ref<CourseDraft | null>(null)
const nodes = ref<DraftNode[]>([])
const loading = ref(true)
const loadError = ref<string | null>(null)

// Action error shown as a dismissible banner below the header.
const actionError = ref<string | null>(null)

// Publish state
const publishing = ref(false)
const publishError = ref<string | null>(null)

// ---------------------------------------------------------------------------
// Admin SSE subscription
// ---------------------------------------------------------------------------

// The admin events stream URL — scoped to this draft id so concurrent draft
// editors in other tabs each receive only their own stream.
const sseUrl = `/api/v1/admin/drafts/${draftId}/events`

// @{"req": ["REQ-FEADMIN-710"]}
const { nodeStatuses, layersAwaitingExpand, error: sseError, clearLayerAwaitingExpand } =
  useNodeEvents(sseUrl, { token: auth.token ?? null })

// When SSE pushes a node to awaiting_review or approved we reload the node list
// so the NodeCard displays the updated payload (summary text, etc.).
watch(nodeStatuses, async (newStatuses) => {
  const needRefresh = Object.entries(newStatuses).some(([nodeId, status]) => {
    const existing = nodes.value.find(n => n.id === nodeId)
    return (
      existing &&
      existing.status !== status &&
      (status === 'awaiting_review' || status === 'approved')
    )
  })
  if (needRefresh) {
    await refreshNodes()
  }
})

// ---------------------------------------------------------------------------
// Data loading
// ---------------------------------------------------------------------------

// @{"req": ["REQ-FEADMIN-710"]}
async function load(): Promise<void> {
  loading.value = true
  loadError.value = null
  try {
    const resp = await getDraft(draftId, auth.token ?? null)
    draft.value = resp.draft
    nodes.value = resp.nodes
  } catch (err) {
    if (err instanceof ApiError) {
      loadError.value = `Failed to load draft: ${err.message}`
    } else {
      loadError.value = 'Failed to load draft. Please try again.'
    }
  } finally {
    loading.value = false
  }
}

// refreshNodes reloads only the node list (after SSE transitions).
// @{"req": ["REQ-FEADMIN-710"]}
async function refreshNodes(): Promise<void> {
  try {
    const resp = await getDraft(draftId, auth.token ?? null)
    nodes.value = resp.nodes
    // Sync draft meta (status may have changed on the server, e.g. after publish).
    draft.value = resp.draft
  } catch {
    // Non-fatal: the SSE status overlay still keeps the UI consistent until
    // the next successful refresh.
  }
}

onMounted(load)

// ---------------------------------------------------------------------------
// TreeView actor-specific callbacks — admin draft API endpoints
// ---------------------------------------------------------------------------

// surfaceConflict translates known 409 conflict codes into human-readable messages.
function surfaceConflict(err: unknown, fallback: string): void {
  if (err instanceof ApiError && err.status === 409) {
    const body = err.body as { error?: string }
    const code = body?.error ?? ''
    const messages: Record<string, string> = {
      NODE_ALREADY_GENERATING: 'This node is already being generated. Please wait.',
      NODE_NOT_REVIEWABLE: 'This node is not in a reviewable state.',
      NODE_NOT_REGENERABLE: 'This node cannot be regenerated right now.',
      LAYER_NOT_FULLY_APPROVED: 'All nodes in the current layer must be approved before expanding.',
      TREE_ALREADY_COMPLETE: 'The course tree is already complete.',
      DRAFT_ALREADY_PUBLISHED: 'This draft has already been published.',
      DRAFT_HAS_UNAPPROVED_NODES: 'All nodes must be approved before publishing.',
    }
    actionError.value = messages[code] ?? `Conflict: ${code || err.message}`
  } else if (err instanceof ApiError) {
    actionError.value = `${fallback}: ${err.message}`
  } else {
    actionError.value = fallback
  }
}

// @{"req": ["REQ-FEADMIN-710"]}
async function handleApproveNode(nodeId: string): Promise<void> {
  actionError.value = null
  try {
    await approveDraftNode(draftId, nodeId, auth.token ?? null)
    await refreshNodes()
  } catch (err) {
    surfaceConflict(err, 'Failed to approve node')
  }
}

// @{"req": ["REQ-FEADMIN-710"]}
async function handleFeedbackNode(nodeId: string, feedback: string): Promise<void> {
  actionError.value = null
  try {
    await feedbackDraftNode(draftId, nodeId, { feedback }, auth.token ?? null)
    await refreshNodes()
  } catch (err) {
    surfaceConflict(err, 'Failed to submit feedback')
  }
}

// @{"req": ["REQ-FEADMIN-710"]}
async function handleRegenerateNode(nodeId: string): Promise<void> {
  actionError.value = null
  try {
    await regenerateDraftNode(draftId, nodeId, auth.token ?? null)
    // Node enters 'generating' — SSE will push the status; no immediate refresh needed.
  } catch (err) {
    surfaceConflict(err, 'Failed to regenerate node')
  }
}

// @{"req": ["REQ-FEADMIN-710"]}
async function handleRefineNode(nodeId: string, message: string): Promise<void> {
  actionError.value = null
  try {
    await refineDraftNode(draftId, nodeId, { message }, auth.token ?? null)
  } catch (err) {
    surfaceConflict(err, 'Failed to send refinement message')
  }
}

// @{"req": ["REQ-FEADMIN-710"]}
async function handleLoadNodeChat(nodeId: string): Promise<NodeChatMessage[]> {
  const resp = await getDraftNodeChat(draftId, nodeId, auth.token ?? null)
  return resp.messages
}

// @{"req": ["REQ-FEADMIN-710"]}
// After expandDraftLayer succeeds, clear the layer from the awaiting set so
// LayerExpandControl's button disappears immediately.
async function handleExpandLayer(layer: string): Promise<void> {
  actionError.value = null
  try {
    await expandDraftLayer(draftId, layer, auth.token ?? null)
    clearLayerAwaitingExpand(layer)
  } catch (err) {
    surfaceConflict(err, 'Failed to expand layer')
  }
}

// ---------------------------------------------------------------------------
// Publish action
// ---------------------------------------------------------------------------

// allNodesApproved is the client-side guard for the Publish button. The server
// independently validates this and returns DRAFT_HAS_UNAPPROVED_NODES if the
// precondition is not met.
const allNodesApproved = computed(() =>
  nodes.value.length > 0 &&
  nodes.value.every(n => {
    const liveStatus = nodeStatuses.value[n.id]
    return (liveStatus ?? n.status) === 'approved'
  }),
)

// @{"req": ["REQ-FEADMIN-710"]}
// publishDraft returns the created/updated assignment id. On success we route
// the admin to AdminAssignmentDetailView so they can assign students there —
// no parallel assign UI is built here.
async function handlePublish(): Promise<void> {
  publishError.value = null
  publishing.value = true
  try {
    const resp = await publishDraft(
      draftId,
      { assignment_title: draft.value?.title || draft.value?.topic || undefined },
      auth.token ?? null,
    )
    await router.push(`/admin/assignments/${resp.assignment_id}`)
  } catch (err) {
    if (err instanceof ApiError && err.status === 409) {
      const body = err.body as { error?: string }
      if (body?.error === 'DRAFT_HAS_UNAPPROVED_NODES') {
        publishError.value = 'Cannot publish: all nodes must be approved first.'
      } else if (body?.error === 'DRAFT_ALREADY_PUBLISHED') {
        publishError.value = 'This draft has already been published.'
        // Reload draft to pick up the published assignment id if present.
        await refreshNodes()
      } else {
        publishError.value = `Cannot publish: ${body?.error ?? err.message}`
      }
    } else if (err instanceof ApiError) {
      publishError.value = `Failed to publish: ${err.message}`
    } else {
      publishError.value = 'Failed to publish draft. Please try again.'
    }
  } finally {
    publishing.value = false
  }
}

// ---------------------------------------------------------------------------
// Derived
// ---------------------------------------------------------------------------

const draftTitle = computed(() => draft.value?.title || draft.value?.topic || 'Draft')
const isPublished = computed(() => draft.value?.status === 'published')
</script>

<template>
  <div class="draft-editor">
    <!-- Loading / error states -->
    <div v-if="loading" class="editor-loading">
      Loading draft...
    </div>

    <div v-else-if="loadError" class="editor-error">
      {{ loadError }}
    </div>

    <template v-else-if="draft">
      <!-- Header -->
      <div class="editor-header">
        <div class="editor-header-main">
          <button class="back-button" @click="router.push('/admin/drafts')">
            &larr; Drafts
          </button>
          <h1 class="editor-title">{{ draftTitle }}</h1>
          <span class="draft-status-badge" :class="draft.status">{{ draft.status }}</span>
          <span class="draft-meta">{{ draft.topic }} &bull; {{ draft.level }}</span>
        </div>

        <!-- Right side: SSE indicator + Publish -->
        <div class="editor-header-actions">
          <div class="sse-status">
            <span v-if="sseError" class="sse-disconnected" :title="sseError">
              Stream disconnected
            </span>
            <span v-else class="sse-connected">Live</span>
          </div>

          <button
            v-if="!isPublished"
            class="publish-button"
            :disabled="!allNodesApproved || publishing"
            :title="!allNodesApproved ? 'All nodes must be approved before publishing' : 'Publish and create assignment'"
            @click="handlePublish"
          >
            {{ publishing ? 'Publishing...' : 'Publish' }}
          </button>

          <div v-else class="published-notice">
            Published
            <RouterLink
              v-if="draft.published_to_assignment_id"
              :to="`/admin/assignments/${draft.published_to_assignment_id}`"
              class="assignment-link"
            >
              View Assignment
            </RouterLink>
          </div>
        </div>
      </div>

      <!-- Action error banner -->
      <div v-if="actionError" class="action-error-banner" data-testid="action-error-banner">
        {{ actionError }}
        <button class="dismiss-btn" @click="actionError = null">Dismiss</button>
      </div>

      <!-- Publish error banner -->
      <div v-if="publishError" class="publish-error-banner" data-testid="publish-error-banner">
        {{ publishError }}
        <button class="dismiss-btn" @click="publishError = null">Dismiss</button>
      </div>

      <!-- SSE error banner (non-fatal) -->
      <div v-if="sseError" class="sse-error-banner">
        Live updates disconnected: {{ sseError }}. Node status may be stale — refresh to reconnect.
      </div>

      <!-- Knowledge tree -->
      <TreeView
        :nodes="nodes"
        :node-statuses="nodeStatuses"
        :layers-awaiting-expand="layersAwaitingExpand"
        :on-approve-node="handleApproveNode"
        :on-feedback-node="handleFeedbackNode"
        :on-regenerate-node="handleRegenerateNode"
        :on-refine-node="handleRefineNode"
        :on-load-node-chat="handleLoadNodeChat"
        :on-expand-layer="handleExpandLayer"
      />
    </template>
  </div>
</template>

<style scoped>
.draft-editor {
  padding: 2rem;
  max-width: 1100px;
  margin: 0 auto;
}

.editor-loading {
  text-align: center;
  padding: 2rem;
  font-size: 1.1rem;
  color: #666;
}

.editor-error {
  padding: 1rem;
  background-color: #ffebee;
  color: #d32f2f;
  border-radius: 4px;
  border-left: 4px solid #d32f2f;
}

.editor-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 1.5rem;
  padding-bottom: 1rem;
  border-bottom: 1px solid #e0e0e0;
  flex-wrap: wrap;
  gap: 1rem;
}

.editor-header-main {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  flex-wrap: wrap;
}

.back-button {
  background: none;
  border: none;
  color: #1976d2;
  cursor: pointer;
  font-size: 0.9rem;
  padding: 0;
  font-weight: 500;
}

.back-button:hover {
  text-decoration: underline;
}

.editor-title {
  margin: 0;
  font-size: 1.8rem;
  color: #333;
}

.draft-status-badge {
  padding: 0.3rem 0.8rem;
  border-radius: 4px;
  font-size: 0.85rem;
  font-weight: 600;
  text-transform: capitalize;
}

.draft-status-badge.draft {
  background: #e3f2fd;
  color: #1565c0;
}

.draft-status-badge.published {
  background: #e8f5e9;
  color: #2e7d32;
}

.draft-meta {
  font-size: 0.9rem;
  color: #666;
}

.editor-header-actions {
  display: flex;
  align-items: center;
  gap: 1rem;
  flex-shrink: 0;
}

.sse-status {
  font-size: 0.8rem;
}

.sse-connected {
  color: #2e7d32;
  font-weight: 600;
}

.sse-connected::before {
  content: '●';
  margin-right: 0.3rem;
}

.sse-disconnected {
  color: #b71c1c;
  font-weight: 600;
  cursor: help;
}

.sse-disconnected::before {
  content: '●';
  margin-right: 0.3rem;
}

.publish-button {
  padding: 0.6rem 1.4rem;
  background-color: #2e7d32;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 0.95rem;
  font-weight: 600;
  transition: background-color 0.2s;
}

.publish-button:hover:not(:disabled) {
  background-color: #1b5e20;
}

.publish-button:disabled {
  background-color: #a5d6a7;
  cursor: not-allowed;
}

.published-notice {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  font-size: 0.9rem;
  color: #2e7d32;
  font-weight: 600;
}

.assignment-link {
  padding: 0.4rem 0.9rem;
  background-color: #1976d2;
  color: white;
  border-radius: 4px;
  text-decoration: none;
  font-size: 0.85rem;
  font-weight: 500;
}

.assignment-link:hover {
  background-color: #1565c0;
}

.action-error-banner,
.publish-error-banner {
  padding: 0.75rem 1rem;
  background: #ffebee;
  color: #d32f2f;
  border: 1px solid #ffcdd2;
  border-left: 4px solid #d32f2f;
  border-radius: 4px;
  font-size: 0.9rem;
  margin-bottom: 1rem;
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.dismiss-btn {
  background: none;
  border: none;
  color: #d32f2f;
  cursor: pointer;
  font-size: 0.85rem;
  text-decoration: underline;
  white-space: nowrap;
}

.sse-error-banner {
  padding: 0.75rem 1rem;
  background: #fff3e0;
  color: #e65100;
  border: 1px solid #ffcc80;
  border-radius: 4px;
  font-size: 0.9rem;
  margin-bottom: 1.25rem;
}
</style>
