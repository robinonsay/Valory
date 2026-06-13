// @{"req": ["REQ-SYS-073", "REQ-SYS-077", "REQ-FEADMIN-709"]}

<script setup lang="ts">
// NodeCard renders a single knowledge-tree node with:
//   - Status badge reflecting live status from SSE or API
//   - Content display: AsciiDoc (via renderAsciidoc) for 'content' nodes;
//     structured JSON display for section_goal / concept / syllabus nodes.
//   - HITL controls: approve, feedback (requires non-empty comment), regenerate
//   - Per-node chat panel: lists node_chats history, sends refine messages
//
// The component is actor-agnostic: the parent view passes approve/feedback/
// regenerate/refine functions and chat-load function via props so the same
// component serves both student course nodes and admin draft nodes.

import { ref, computed, watch } from 'vue'
import { renderAsciidoc } from '@/utils/renderAsciidoc'
import type { CourseNode, DraftNode, NodeChatMessage } from '@/types/tree'

// A union type for the node prop: either a student node or an admin draft node.
// Both shapes share id, node_type, status, payload, ordering fields.
type AnyNode = CourseNode | DraftNode

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

// @{"req": ["REQ-SYS-073", "REQ-SYS-077"]}
interface Props {
  node: AnyNode
  // statusOverride: live status from the SSE composable, overrides node.status
  // while the parent hasn't re-fetched the full node list yet.
  statusOverride?: string | null
  // Actor-specific API callbacks supplied by the parent view. The parent binds
  // the correct student or admin API methods before passing them down.
  onApprove: () => Promise<void>
  onFeedback: (feedback: string) => Promise<void>
  onRegenerate: () => Promise<void>
  onRefine: (message: string) => Promise<void>
  onLoadChat: () => Promise<NodeChatMessage[]>
}

const props = defineProps<Props>()

// ---------------------------------------------------------------------------
// Computed status — SSE override wins when present
// ---------------------------------------------------------------------------

const effectiveStatus = computed(() =>
  props.statusOverride != null ? props.statusOverride : props.node.status,
)

// ---------------------------------------------------------------------------
// Content rendering
// ---------------------------------------------------------------------------

// Rendered AsciiDoc HTML — populated asynchronously for 'content' nodes.
const renderedHtml = ref<string>('')
const renderError = ref<string | null>(null)

// @{"req": ["REQ-SYS-073"]}
async function renderContent(): Promise<void> {
  renderError.value = null
  if (props.node.node_type !== 'content') return

  const payload = props.node.payload
  // The content node payload is expected to have a top-level 'content' string
  // key carrying the AsciiDoc source. Fall back to JSON stringify if absent so
  // the component never shows a blank card.
  const source =
    typeof payload['content'] === 'string'
      ? payload['content']
      : JSON.stringify(payload, null, 2)

  try {
    renderedHtml.value = await renderAsciidoc(source)
  } catch (err) {
    renderError.value = 'Failed to render content.'
    renderedHtml.value = ''
  }
}

// Re-render whenever the node's payload or status changes to awaiting_review/approved.
watch(
  () => [props.node.payload, effectiveStatus.value],
  () => {
    if (props.node.node_type === 'content') {
      renderContent()
    }
  },
  { immediate: true },
)

// ---------------------------------------------------------------------------
// HITL controls
// ---------------------------------------------------------------------------

const actionPending = ref(false)
const actionError = ref<string | null>(null)

// feedbackText is the required comment for the feedback (reject) action.
const feedbackText = ref('')
const showFeedbackInput = ref(false)

// @{"req": ["REQ-SYS-073", "REQ-SYS-077"]}
async function handleApprove(): Promise<void> {
  actionError.value = null
  actionPending.value = true
  try {
    await props.onApprove()
  } catch (err: unknown) {
    actionError.value = extractErrorMessage(err)
  } finally {
    actionPending.value = false
  }
}

// @{"req": ["REQ-SYS-073", "REQ-SYS-077"]}
async function handleFeedbackSubmit(): Promise<void> {
  if (!feedbackText.value.trim()) return
  actionError.value = null
  actionPending.value = true
  try {
    await props.onFeedback(feedbackText.value.trim())
    feedbackText.value = ''
    showFeedbackInput.value = false
  } catch (err: unknown) {
    actionError.value = extractErrorMessage(err)
  } finally {
    actionPending.value = false
  }
}

// @{"req": ["REQ-SYS-073", "REQ-SYS-077"]}
async function handleRegenerate(): Promise<void> {
  actionError.value = null
  actionPending.value = true
  try {
    await props.onRegenerate()
  } catch (err: unknown) {
    actionError.value = extractErrorMessage(err)
  } finally {
    actionPending.value = false
  }
}

// ---------------------------------------------------------------------------
// Per-node chat panel
// ---------------------------------------------------------------------------

const chatOpen = ref(false)
const chatMessages = ref<NodeChatMessage[]>([])
const chatLoading = ref(false)
const chatError = ref<string | null>(null)
const chatInput = ref('')
const chatSending = ref(false)

// @{"req": ["REQ-SYS-073", "REQ-SYS-077"]}
async function openChat(): Promise<void> {
  chatOpen.value = true
  chatLoading.value = true
  chatError.value = null
  try {
    chatMessages.value = await props.onLoadChat()
  } catch (err: unknown) {
    chatError.value = extractErrorMessage(err)
  } finally {
    chatLoading.value = false
  }
}

// @{"req": ["REQ-SYS-073", "REQ-SYS-077"]}
async function sendChatMessage(): Promise<void> {
  const text = chatInput.value.trim()
  if (!text) return
  chatSending.value = true
  chatError.value = null
  try {
    await props.onRefine(text)
    chatInput.value = ''
    // Reload chat history to show the new message and assistant response once ready.
    chatMessages.value = await props.onLoadChat()
  } catch (err: unknown) {
    chatError.value = extractErrorMessage(err)
  } finally {
    chatSending.value = false
  }
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function extractErrorMessage(err: unknown): string {
  if (err && typeof err === 'object' && 'body' in err) {
    const body = (err as { body?: { message?: string; error?: string } }).body
    if (body?.message) return body.message
    if (body?.error) return body.error
  }
  if (err instanceof Error) return err.message
  return 'An unexpected error occurred.'
}

// Formats a structured JSON payload (section_goal / concept / syllabus) into
// a readable string for display. The payload is an arbitrary JSON object so
// we simply pretty-print the relevant fields rather than hardcoding keys.
function formatStructuredPayload(payload: Record<string, unknown>): string {
  return JSON.stringify(payload, null, 2)
}

// Status badge CSS class mapping.
const statusClass = computed(() => `status-${effectiveStatus.value.replace(/_/g, '-')}`)
</script>

<template>
  <div :class="['node-card', `node-type-${node.node_type}`, statusClass]">
    <!-- Header row: node type + status badge -->
    <div class="node-card-header">
      <span class="node-type-label">{{ node.node_type.replace(/_/g, ' ') }}</span>
      <span :class="['status-badge', statusClass]">{{ effectiveStatus.replace(/_/g, ' ') }}</span>
      <button
        class="chat-toggle-btn"
        :aria-expanded="chatOpen"
        @click="chatOpen ? (chatOpen = false) : openChat()"
      >
        {{ chatOpen ? 'Close Chat' : 'Chat' }}
      </button>
    </div>

    <!-- Content area -->
    <div class="node-card-body">
      <!-- Content node: render AsciiDoc via the existing renderAsciidoc pipeline -->
      <template v-if="node.node_type === 'content'">
        <div v-if="renderError" class="render-error">{{ renderError }}</div>
        <div
          v-else-if="renderedHtml"
          class="adoc-content"
          v-html="renderedHtml"
        />
        <div v-else-if="effectiveStatus === 'generating' || effectiveStatus === 'pending'" class="generating-placeholder">
          Generating content...
        </div>
      </template>

      <!-- Non-content nodes: show structured JSON payload -->
      <template v-else-if="effectiveStatus === 'awaiting_review' || effectiveStatus === 'approved'">
        <pre class="structured-payload">{{ formatStructuredPayload(node.payload) }}</pre>
      </template>

      <template v-else-if="effectiveStatus === 'generating' || effectiveStatus === 'pending'">
        <p class="generating-placeholder">Generating...</p>
      </template>

      <template v-else-if="effectiveStatus === 'failed'">
        <p class="error-state">Generation failed. You can regenerate this node.</p>
      </template>

      <template v-else-if="effectiveStatus === 'rejected'">
        <p class="rejected-state">Rejected — regenerate to retry with the stored feedback.</p>
      </template>
    </div>

    <!-- HITL controls — only shown when the node is in a reviewable state -->
    <div
      v-if="effectiveStatus === 'awaiting_review' || effectiveStatus === 'rejected' || effectiveStatus === 'failed'"
      class="node-card-controls"
    >
      <div v-if="actionError" class="action-error">{{ actionError }}</div>

      <!-- Approve: only valid from awaiting_review -->
      <button
        v-if="effectiveStatus === 'awaiting_review'"
        class="btn btn-approve"
        :disabled="actionPending"
        @click="handleApprove"
      >
        Approve
      </button>

      <!-- Feedback / reject: toggle an inline comment input, only from awaiting_review -->
      <template v-if="effectiveStatus === 'awaiting_review'">
        <button
          v-if="!showFeedbackInput"
          class="btn btn-feedback"
          :disabled="actionPending"
          @click="showFeedbackInput = true"
        >
          Request Changes
        </button>
        <div v-else class="feedback-input-row">
          <textarea
            v-model="feedbackText"
            class="feedback-textarea"
            placeholder="Describe what needs to change (required)"
            rows="3"
          />
          <div class="feedback-actions">
            <button
              class="btn btn-feedback-submit"
              :disabled="actionPending || !feedbackText.trim()"
              @click="handleFeedbackSubmit"
            >
              Submit Feedback
            </button>
            <button
              class="btn btn-cancel"
              :disabled="actionPending"
              @click="showFeedbackInput = false; feedbackText = ''"
            >
              Cancel
            </button>
          </div>
        </div>
      </template>

      <!-- Regenerate: valid from rejected or failed -->
      <button
        v-if="effectiveStatus === 'rejected' || effectiveStatus === 'failed'"
        class="btn btn-regenerate"
        :disabled="actionPending"
        @click="handleRegenerate"
      >
        Regenerate
      </button>
    </div>

    <!-- Per-node chat panel -->
    <div v-if="chatOpen" class="chat-panel">
      <div v-if="chatLoading" class="chat-loading">Loading chat history...</div>
      <div v-else-if="chatError" class="chat-error">{{ chatError }}</div>
      <div v-else class="chat-messages">
        <div
          v-for="msg in chatMessages"
          :key="msg.id"
          :class="['chat-message', `chat-role-${msg.role}`]"
        >
          <span class="chat-role-label">{{ msg.role }}</span>
          <p class="chat-content">{{ msg.content }}</p>
        </div>
        <div v-if="chatMessages.length === 0" class="chat-empty">No messages yet.</div>
      </div>

      <!-- Refine input: send a message to the Chair, which triggers a re-generation -->
      <div class="chat-input-row">
        <textarea
          v-model="chatInput"
          class="chat-textarea"
          placeholder="Ask the AI to revise this node..."
          rows="2"
        />
        <button
          class="btn btn-send"
          :disabled="chatSending || !chatInput.trim()"
          @click="sendChatMessage"
        >
          {{ chatSending ? 'Sending...' : 'Send' }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.node-card {
  border: 1px solid #e0e0e0;
  border-radius: 6px;
  margin-bottom: 0.75rem;
  overflow: hidden;
  background: #fff;
}

.node-card-header {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.6rem 1rem;
  background: #fafafa;
  border-bottom: 1px solid #eee;
}

.node-type-label {
  font-size: 0.8rem;
  font-weight: 600;
  text-transform: uppercase;
  color: #666;
  letter-spacing: 0.04em;
}

.status-badge {
  font-size: 0.75rem;
  padding: 0.15rem 0.5rem;
  border-radius: 3px;
  text-transform: capitalize;
}

.status-pending { background: #f5f5f5; color: #888; }
.status-generating { background: #e3f2fd; color: #1565c0; }
.status-awaiting-review { background: #fff8e1; color: #f57c00; }
.status-approved { background: #e8f5e9; color: #2e7d32; }
.status-rejected { background: #fce4ec; color: #c62828; }
.status-failed { background: #ffebee; color: #c62828; }

.chat-toggle-btn {
  margin-left: auto;
  background: none;
  border: 1px solid #bbb;
  border-radius: 4px;
  padding: 0.2rem 0.6rem;
  font-size: 0.8rem;
  cursor: pointer;
  color: #555;
}
.chat-toggle-btn:hover { background: #f0f0f0; }

.node-card-body {
  padding: 1rem;
}

.structured-payload {
  background: #f6f8fa;
  border: 1px solid #e0e0e0;
  border-radius: 4px;
  padding: 0.75rem;
  font-size: 0.82rem;
  overflow-x: auto;
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
  max-height: 300px;
  overflow-y: auto;
}

.generating-placeholder,
.error-state,
.rejected-state {
  color: #888;
  font-style: italic;
  font-size: 0.9rem;
  margin: 0;
}

.render-error { color: #c62828; font-size: 0.85rem; }

/* Rendered AsciiDoc styles (mirrors CourseDetailView adoc-content) */
.adoc-content {
  line-height: 1.7;
  color: #444;
}
.adoc-content :deep(h1),
.adoc-content :deep(h2) { margin: 1rem 0 0.5rem; color: #333; }
.adoc-content :deep(h1) { font-size: 1.3rem; }
.adoc-content :deep(h2) { font-size: 1.1rem; border-bottom: 1px solid #eee; padding-bottom: 0.2rem; }
.adoc-content :deep(pre) {
  background: #f6f8fa; border: 1px solid #e0e0e0; border-radius: 4px;
  padding: 0.75rem; overflow-x: auto; font-size: 0.88rem;
}
.adoc-content :deep(code) {
  font-family: 'Courier New', monospace; background: #f0f0f0;
  padding: 0.1em 0.3em; border-radius: 3px; font-size: 0.88em;
}
.adoc-content :deep(pre code) { background: transparent; padding: 0; }
.adoc-content :deep(ul), .adoc-content :deep(ol) { margin: 0.4rem 0 0.4rem 1.5rem; }
.adoc-content :deep(li) { margin-bottom: 0.2rem; }

/* HITL controls */
.node-card-controls {
  padding: 0.75rem 1rem;
  border-top: 1px solid #eee;
  background: #fcfcfc;
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
  align-items: flex-start;
}

.btn {
  padding: 0.35rem 0.9rem;
  border-radius: 4px;
  border: 1px solid transparent;
  font-size: 0.85rem;
  cursor: pointer;
  transition: opacity 0.1s;
}
.btn:disabled { opacity: 0.5; cursor: not-allowed; }

.btn-approve { background: #2e7d32; color: #fff; border-color: #2e7d32; }
.btn-approve:hover:not(:disabled) { background: #1b5e20; }

.btn-feedback { background: #f57c00; color: #fff; border-color: #f57c00; }
.btn-feedback:hover:not(:disabled) { background: #e65100; }

.btn-feedback-submit { background: #c62828; color: #fff; border-color: #c62828; }
.btn-feedback-submit:hover:not(:disabled) { background: #b71c1c; }

.btn-cancel { background: #eee; color: #444; border-color: #ccc; }
.btn-cancel:hover:not(:disabled) { background: #e0e0e0; }

.btn-regenerate { background: #1565c0; color: #fff; border-color: #1565c0; }
.btn-regenerate:hover:not(:disabled) { background: #0d47a1; }

.btn-send { background: #1565c0; color: #fff; border-color: #1565c0; white-space: nowrap; }
.btn-send:hover:not(:disabled) { background: #0d47a1; }

.feedback-input-row {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  width: 100%;
}

.feedback-textarea {
  width: 100%;
  border: 1px solid #ccc;
  border-radius: 4px;
  padding: 0.4rem;
  font-size: 0.85rem;
  resize: vertical;
  box-sizing: border-box;
}

.feedback-actions {
  display: flex;
  gap: 0.5rem;
}

.action-error {
  color: #c62828;
  font-size: 0.8rem;
  width: 100%;
}

/* Chat panel */
.chat-panel {
  border-top: 1px solid #e0e0e0;
  padding: 0.75rem 1rem;
  background: #f9f9f9;
}

.chat-loading, .chat-error, .chat-empty {
  font-size: 0.85rem;
  color: #888;
  font-style: italic;
  padding: 0.5rem 0;
}

.chat-error { color: #c62828; }

.chat-messages {
  max-height: 250px;
  overflow-y: auto;
  margin-bottom: 0.75rem;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.chat-message {
  padding: 0.5rem 0.75rem;
  border-radius: 6px;
  font-size: 0.85rem;
}

.chat-role-user { background: #e3f2fd; align-self: flex-end; }
.chat-role-assistant { background: #f5f5f5; align-self: flex-start; }

.chat-role-label {
  display: block;
  font-size: 0.7rem;
  font-weight: 600;
  text-transform: uppercase;
  color: #999;
  margin-bottom: 0.2rem;
}

.chat-content { margin: 0; white-space: pre-wrap; word-break: break-word; }

.chat-input-row {
  display: flex;
  gap: 0.5rem;
  align-items: flex-end;
}

.chat-textarea {
  flex: 1;
  border: 1px solid #ccc;
  border-radius: 4px;
  padding: 0.4rem;
  font-size: 0.85rem;
  resize: none;
}
</style>
