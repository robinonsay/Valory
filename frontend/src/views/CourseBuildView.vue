// @{"req": ["REQ-SYS-073", "REQ-FECOURSE-602"]}

<script setup lang="ts">
// CourseBuildView — student interactive layer-by-layer build view.
//
// Reached when a course has tree_mode=true. The view:
//   1. Loads the course detail + all nodes (GET /courses/{id} and GET /courses/{id}/nodes).
//   2. Subscribes to live SSE updates via useNodeEvents (GET /courses/{id}/events).
//   3. Renders T1's TreeView, wiring all actor-specific callbacks to the
//      student api/tree methods: approve / feedback / regenerate / refine (chat) /
//      getCourseNodeChat / expandCourseLayer.
//   4. Surfaces 409 conflict states that T1's LayerExpandControl / NodeCard already handle.
//
// If GET /courses/{id} returns tree_mode=false (navigated here by mistake), this
// view replace-redirects to the correct flat route so the flat flow is never broken.

import { ref, computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { get, ApiError } from '@/api/client'
import { useNodeEvents } from '@/composables/useNodeEvents'
import TreeView from '@/components/tree/TreeView.vue'
import {
  listCourseNodes,
  approveCourseNode,
  feedbackCourseNode,
  regenerateCourseNode,
  refineCourseNode,
  getCourseNodeChat,
  expandCourseLayer,
} from '@/api/tree'
import type { CourseNode, NodeChatMessage } from '@/types/tree'
import type { CourseResponse } from '@/types/course'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const courseId = route.params.id as string

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const course = ref<CourseResponse | null>(null)
const nodes = ref<CourseNode[]>([])
const loading = ref(true)
const loadError = ref<string | null>(null)

// ---------------------------------------------------------------------------
// Live SSE subscription — student event stream
// ---------------------------------------------------------------------------

// @{"req": ["REQ-SYS-073"]}
// The URL is the student events endpoint; the token is read from the auth store
// so sessions restored from cookie are also covered.
const sseUrl = `/api/v1/courses/${courseId}/events`
const { nodeStatuses, layersAwaitingExpand, error: sseError, clearLayerAwaitingExpand } =
  useNodeEvents(sseUrl, { token: auth.token ?? null })

// Merge SSE status updates back into the node list so NodeCard sees the live status
// even before a full re-fetch. When SSE changes a node to awaiting_review or approved
// we also refresh the node list so the payload is up-to-date.
watch(nodeStatuses, async (newStatuses) => {
  // Detect nodes that transitioned to awaiting_review so we can reload their payload.
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

// @{"req": ["REQ-SYS-073"]}
async function load(): Promise<void> {
  loading.value = true
  loadError.value = null
  try {
    const [courseResp, nodesResp] = await Promise.all([
      get<CourseResponse>(`/api/v1/courses/${courseId}`),
      listCourseNodes(courseId),
    ])

    // Guard: if this course is NOT tree_mode, redirect to the correct flat route.
    // This handles deep-link or stale-navigation edge cases.
    if (!courseResp.tree_mode) {
      const flatRouteMap: Record<string, string> = {
        intake: `/courses/${courseId}/intake`,
        syllabus_draft: `/courses/${courseId}/syllabus`,
        syllabus_approved: `/courses/${courseId}/generating`,
        generating: `/courses/${courseId}/generating`,
        active: `/courses/${courseId}/hub`,
        archived: `/courses/${courseId}/hub`,
        completed: `/courses/${courseId}/hub`,
      }
      const target = flatRouteMap[courseResp.status]
      if (target) {
        await router.replace(target)
        return
      }
    }

    course.value = courseResp
    nodes.value = nodesResp.nodes
  } catch (err) {
    if (err instanceof ApiError) {
      loadError.value = `Failed to load course: ${err.message}`
    } else {
      loadError.value = 'Failed to load course. Please try again.'
    }
  } finally {
    loading.value = false
  }
}

// refreshNodes reloads only the node list (called after SSE status transitions).
// @{"req": ["REQ-SYS-073"]}
async function refreshNodes(): Promise<void> {
  try {
    const resp = await listCourseNodes(courseId)
    nodes.value = resp.nodes
  } catch {
    // Non-fatal: the SSE status overlay still keeps the UI consistent until
    // the next successful refresh.
  }
}

onMounted(load)

// ---------------------------------------------------------------------------
// TreeView actor-specific callbacks — student API endpoints
// ---------------------------------------------------------------------------

// @{"req": ["REQ-SYS-073"]}
async function handleApproveNode(nodeId: string): Promise<void> {
  await approveCourseNode(courseId, nodeId)
  await refreshNodes()
}

// @{"req": ["REQ-SYS-073"]}
async function handleFeedbackNode(nodeId: string, feedback: string): Promise<void> {
  await feedbackCourseNode(courseId, nodeId, { feedback })
  await refreshNodes()
}

// @{"req": ["REQ-SYS-073"]}
async function handleRegenerateNode(nodeId: string): Promise<void> {
  await regenerateCourseNode(courseId, nodeId)
  // Node enters 'generating' — SSE will push the status; no immediate refresh needed.
}

// @{"req": ["REQ-SYS-073"]}
async function handleRefineNode(nodeId: string, message: string): Promise<void> {
  await refineCourseNode(courseId, nodeId, { message })
}

// @{"req": ["REQ-SYS-073"]}
async function handleLoadNodeChat(nodeId: string): Promise<NodeChatMessage[]> {
  const resp = await getCourseNodeChat(courseId, nodeId)
  return resp.messages
}

// @{"req": ["REQ-SYS-073"]}
// After expandCourseLayer succeeds, clear the layer from the awaiting set so
// LayerExpandControl's "Expand" button disappears immediately.
async function handleExpandLayer(layer: string): Promise<void> {
  await expandCourseLayer(courseId, layer)
  clearLayerAwaitingExpand(layer)
}

// ---------------------------------------------------------------------------
// Derived
// ---------------------------------------------------------------------------

const courseTitle = computed(() => course.value?.title || course.value?.topic || '')
</script>

<template>
  <div class="course-build">
    <!-- Loading / error states -->
    <div v-if="loading" class="build-loading">
      Loading course...
    </div>

    <div v-else-if="loadError" class="build-error">
      {{ loadError }}
    </div>

    <template v-else-if="course">
      <!-- Header -->
      <div class="build-header">
        <div class="build-header-main">
          <h1 class="build-title">{{ courseTitle }}</h1>
          <span class="build-status-badge">{{ course.status }}</span>
        </div>

        <!-- SSE connectivity indicator -->
        <div class="build-sse-status">
          <span v-if="sseError" class="sse-disconnected" :title="sseError">
            Stream disconnected
          </span>
          <span v-else class="sse-connected">Live</span>
        </div>
      </div>

      <!-- SSE error banner (non-fatal) -->
      <div v-if="sseError" class="sse-error-banner">
        Live updates disconnected: {{ sseError }}. Node status may be stale — refresh the page to reconnect.
      </div>

      <!-- Tree -->
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
.course-build {
  padding: 2rem;
  max-width: 1100px;
  margin: 0 auto;
}

.build-loading {
  text-align: center;
  padding: 2rem;
  font-size: 1.1rem;
  color: #666;
}

.build-error {
  padding: 1rem;
  background-color: #ffebee;
  color: #d32f2f;
  border-radius: 4px;
  border-left: 4px solid #d32f2f;
}

.build-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 1.5rem;
  padding-bottom: 1rem;
  border-bottom: 1px solid #e0e0e0;
}

.build-header-main {
  display: flex;
  align-items: center;
  gap: 1rem;
  flex-wrap: wrap;
}

.build-title {
  margin: 0;
  font-size: 1.8rem;
  color: #333;
}

.build-status-badge {
  padding: 0.3rem 0.8rem;
  background: #e3f2fd;
  color: #1565c0;
  border-radius: 4px;
  font-size: 0.85rem;
  font-weight: 600;
  text-transform: capitalize;
}

.build-sse-status {
  display: flex;
  align-items: center;
  font-size: 0.8rem;
  gap: 0.4rem;
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
