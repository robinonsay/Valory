// @{"req": ["REQ-FECOURSE-026", "REQ-FECOURSE-027", "REQ-FECOURSE-028", "REQ-FECOURSE-070", "REQ-FECOURSE-071", "REQ-FECOURSE-220", "REQ-FECOURSE-221", "REQ-FECOURSE-222", "REQ-FECOURSE-223", "REQ-FECOURSE-224", "REQ-FECOURSE-225", "REQ-FECOURSE-230", "REQ-FECOURSE-231", "REQ-FECOURSE-240", "REQ-FECOURSE-250", "REQ-FECOURSE-251", "REQ-FECOURSE-252", "REQ-FECOURSE-260", "REQ-FECOURSE-261", "REQ-FECOURSE-262", "REQ-FECOURSE-263", "REQ-FECOURSE-264", "REQ-FECOURSE-622", "REQ-FECOURSE-623", "REQ-FECOURSE-624", "REQ-FECOURSE-625", "REQ-FECOURSE-612", "REQ-FECOURSE-613", "REQ-FECOURSE-614", "REQ-FECOURSE-615", "REQ-FECOURSE-616"]}
<script setup lang="ts">
import { ref, onMounted, onUnmounted, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useSSE } from '@/composables/useSSE'
import { get, post, ApiError, uploadMultipart } from '@/api/client'
import { renderMarkdown } from '@/utils/renderMarkdown'
import { validateImage, MAX_CHAT_IMAGES, type ImageUploadResponse } from '@/utils/imageUpload'
import 'katex/dist/katex.min.css'

interface Message {
  role: 'agent' | 'user'
  content: string
  attachments?: string[] // URLs of images attached to this message (user bubbles only)
}

interface ChatHistoryResponse {
  messages: Array<{
    id: string
    role: 'assistant' | 'student'
    content: string
    created_at: string
  }>
}

interface ChatReplyResponse {
  reply: string
  course_status: string
}

interface PendingImage {
  file: File
  objectUrl: string
  uploadedId?: string
}

interface SSEEnvelope {
  id: string
  agent_run_id: string
  event_type: string
  payload: Record<string, unknown>
  emitted_at: string
}

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const messages = ref<Message[]>([])
const userInput = ref('')
const textareaRef = ref<HTMLTextAreaElement | null>(null)
const isSending = ref(false)
const connectionError = ref(false)
const sendError = ref(false)
const sendErrorMessage = ref('')
const chatContainer = ref<HTMLElement | null>(null)

const pendingImages = ref<PendingImage[]>([])
const imageValidationError = ref('')
const imageUploadError = ref('')
const fileInputRef = ref<HTMLInputElement | null>(null)

const isLoadingHistory = ref(false)
const isHistoryPolling = ref(false)
const boundedWaitExceeded = ref(false)
let historyPollInterval: NodeJS.Timeout | null = null
let boundedWaitTimer: NodeJS.Timeout | null = null
let pollingGeneration = 0
let pollInFlight = false

// @{"req": ["REQ-FECOURSE-260", "REQ-FECOURSE-261", "REQ-FECOURSE-262"]}
async function loadChatHistory(isInitialLoad = false): Promise<void> {
  try {
    const courseId = route.params.id as string
    const response = await get<ChatHistoryResponse>(
      `/api/v1/courses/${courseId}/chat/history`
    )

    if (response.messages && Array.isArray(response.messages)) {
      // Only apply the response if this is the initial load or if polling is still active
      // for this generation. Discard responses from polling ticks that arrived after
      // polling was stopped (e.g., by an in-flight send).
      if (isInitialLoad || pollingGeneration > 0) {
        messages.value = []
        response.messages.forEach((msg) => {
          messages.value.push({
            role: msg.role === 'assistant' ? 'agent' : 'user',
            content: msg.content
          })
        })
        scrollToBottom()
      }
    }
  } catch (error) {
    // If history fetch fails, proceed with empty history so student can still
    // send messages. The backend will retry the kickoff on first history fetch
    // per the design spec.
  }
}

// @{"req": ["REQ-FECOURSE-262", "REQ-FECOURSE-263"]}
function startHistoryPolling(): void {
  if (messages.value.length > 0) {
    return
  }

  isHistoryPolling.value = true
  boundedWaitExceeded.value = false
  pollingGeneration++
  const currentGeneration = pollingGeneration
  const startTime = Date.now()
  const POLL_INTERVAL = 2500
  const BOUNDED_WAIT_MS = 120000

  historyPollInterval = setInterval(async () => {
    if (messages.value.length > 0 || isSending.value) {
      stopHistoryPolling()
      return
    }

    const elapsed = Date.now() - startTime
    if (elapsed > BOUNDED_WAIT_MS) {
      stopHistoryPolling()
      boundedWaitExceeded.value = true
      return
    }

    // Skip if a fetch is already in-flight
    if (pollInFlight) {
      return
    }

    pollInFlight = true
    try {
      await loadChatHistory(false)
      // Only process the response if polling is still active for this generation
      if (currentGeneration === pollingGeneration && messages.value.length > 0) {
        stopHistoryPolling()
      }
    } finally {
      pollInFlight = false
    }
  }, POLL_INTERVAL)

  boundedWaitTimer = setTimeout(() => {
    if (messages.value.length === 0) {
      stopHistoryPolling()
      boundedWaitExceeded.value = true
    }
  }, BOUNDED_WAIT_MS)
}

function stopHistoryPolling(): void {
  isHistoryPolling.value = false
  pollingGeneration = 0
  if (historyPollInterval !== null) {
    clearInterval(historyPollInterval)
    historyPollInterval = null
  }
  if (boundedWaitTimer !== null) {
    clearTimeout(boundedWaitTimer)
    boundedWaitTimer = null
  }
}

// @{"req": ["REQ-FECOURSE-240", "REQ-FECOURSE-250", "REQ-FECOURSE-251", "REQ-FECOURSE-252"]}
function onEvent(eventType: string, data: string): void {
  if (eventType === 'keepalive') {
    return
  }

  if (eventType === 'status_change') {
    try {
      const envelope = JSON.parse(data) as SSEEnvelope
      const status = envelope.payload['status'] as string | undefined
      if (status && status !== 'intake') {
        redirectOnStatusChange(status)
      }
    } catch {
      // Malformed envelope — ignore
    }
    return
  }
}

// @{"req": ["REQ-FECOURSE-240"]}
function redirectOnStatusChange(status: string): void {
  const courseId = route.params.id as string
  switch (status) {
    case 'syllabus_draft':
      router.replace(`/courses/${courseId}/syllabus`)
      break
    case 'syllabus_approved':
      router.replace(`/courses/${courseId}/syllabus`)
      break
    case 'active':
      router.replace(`/courses/${courseId}`)
      break
    case 'archived':
      router.replace('/courses')
      break
    default:
      router.replace(`/courses/${courseId}`)
  }
}

// @{"req": ["REQ-FECOURSE-250", "REQ-FECOURSE-251", "REQ-FECOURSE-252"]}
function onError(): void {
  connectionError.value = true
}

// @{"req": ["REQ-FECOURSE-622", "REQ-FECOURSE-623", "REQ-FECOURSE-624"]}
function onImageFileSelect(event: Event): void {
  const input = event.target as HTMLInputElement
  imageValidationError.value = ''
  imageUploadError.value = ''

  if (!input.files) return

  for (const file of Array.from(input.files)) {
    // Check if already at max count
    if (pendingImages.value.length >= MAX_CHAT_IMAGES) {
      imageValidationError.value = `Maximum ${MAX_CHAT_IMAGES} images per message.`
      break
    }

    // Client-side validation
    const err = validateImage(file)
    if (err) {
      imageValidationError.value = err
      break
    }

    const objectUrl = URL.createObjectURL(file)
    pendingImages.value.push({
      file,
      objectUrl
    })
  }

  // Reset the file input so the same file can be selected again
  input.value = ''
}

// @{"req": ["REQ-FECOURSE-624"]}
function removeImage(index: number): void {
  const image = pendingImages.value[index]
  if (image) {
    URL.revokeObjectURL(image.objectUrl)
    pendingImages.value.splice(index, 1)
  }
}

// @{"req": ["REQ-FECOURSE-622", "REQ-FECOURSE-626"]}
async function uploadPendingImages(): Promise<string[]> {
  const courseId = route.params.id as string
  const uploadedIds: string[] = []

  for (const image of pendingImages.value) {
    try {
      const formData = new FormData()
      formData.append('image', image.file)

      const response = await uploadMultipart<ImageUploadResponse>(
        `/api/v1/courses/${courseId}/images`,
        formData
      )

      uploadedIds.push(response.image_id)
      image.uploadedId = response.image_id
    } catch (error) {
      imageUploadError.value = 'Failed to upload one or more images. Please try again.'
      throw error
    }
  }

  return uploadedIds
}

// @{"req": ["REQ-FECOURSE-220", "REQ-FECOURSE-221", "REQ-FECOURSE-222", "REQ-FECOURSE-223", "REQ-FECOURSE-224", "REQ-FECOURSE-225", "REQ-FECOURSE-262", "REQ-FECOURSE-625"]}
async function sendMessage(): Promise<void> {
  const content = userInput.value.trim()
  if (!content && pendingImages.value.length === 0) return
  if (isSending.value) return

  // Stop polling when student sends their first message
  stopHistoryPolling()

  imageValidationError.value = ''
  imageUploadError.value = ''
  isSending.value = true
  sendError.value = false

  // Upload images first if any are pending
  let attachmentIds: string[] = []
  let attachmentUrls: string[] = []
  if (pendingImages.value.length > 0) {
    try {
      attachmentIds = await uploadPendingImages()
      // Map image IDs to their API URLs for the optimistic message display
      attachmentUrls = attachmentIds.map(id => `/api/v1/images/${id}`)
    } catch {
      // Error message already set in uploadPendingImages
      sendError.value = true
      sendErrorMessage.value = imageUploadError.value || 'Image upload failed.'
      isSending.value = false
      return
    }
  }

  // Build the chat payload with optional attachments
  const payload: Record<string, unknown> = {
    message: content
  }
  if (attachmentIds.length > 0) {
    payload.attachments = attachmentIds
  }

  // Optimistically add the user message with attachments field (not in content)
  // REQ-FECOURSE-625: user message content is plain text; images rendered separately
  messages.value.push({
    role: 'user',
    content: content,
    attachments: attachmentUrls.length > 0 ? attachmentUrls : undefined
  })
  userInput.value = ''
  resetTextareaHeight()
  // Revoke object URLs before clearing pending images (LOW priority cleanup)
  for (const image of pendingImages.value) {
    URL.revokeObjectURL(image.objectUrl)
  }
  pendingImages.value = []
  scrollToBottom()

  try {
    const courseId = route.params.id as string
    const response = await post<ChatReplyResponse>(
      `/api/v1/courses/${courseId}/chat`,
      payload
    )

    // Append the agent's reply from the response body
    messages.value.push({ role: 'agent', content: response.reply })
    scrollToBottom()

    // Check if intake is complete; if so, redirect
    if (response.course_status !== 'intake') {
      redirectOnStatusChange(response.course_status)
    }
  } catch (error) {
    sendError.value = true
    if (error instanceof ApiError) {
      sendErrorMessage.value =
        'Failed to send message. Please check your connection and try again.'
    } else {
      sendErrorMessage.value = 'An unexpected error occurred. Please try again.'
    }
    // No optimistic message exists yet on this path — upload failed before the
      // message was pushed; the student keeps their pending previews and can retry.
  } finally {
    isSending.value = false
  }
}

function scrollToBottom(): void {
  nextTick(() => {
    if (chatContainer.value) {
      chatContainer.value.scrollTop = chatContainer.value.scrollHeight
    }
  })
}

// @{"req": ["REQ-FECOURSE-627"]}
function autoGrowTextarea(): void {
  if (!textareaRef.value) return
  // Reset height to auto to get the correct scrollHeight for the current content
  textareaRef.value.style.height = 'auto'
  // Cap height at 6 rows (~144px with 24px line-height), then internal scroll
  const maxHeight = 144
  const newHeight = Math.min(textareaRef.value.scrollHeight, maxHeight)
  textareaRef.value.style.height = `${newHeight}px`
}

function resetTextareaHeight(): void {
  if (textareaRef.value) {
    textareaRef.value.style.height = 'auto'
  }
}

function dismissSendError(): void {
  sendError.value = false
  sendErrorMessage.value = ''
}

// REQ-AUTH-011: token omitted; browser sends __Host-session cookie automatically.
const sse = useSSE(`/api/v1/courses/${route.params.id}/events`, {
  onEvent,
  onError
})

// @{"req": ["REQ-FECOURSE-260", "REQ-FECOURSE-261", "REQ-FECOURSE-262", "REQ-FECOURSE-263"]}
onMounted(async () => {
  await loadChatHistory(true)
  if (messages.value.length === 0) {
    startHistoryPolling()
  }
})

onUnmounted(() => {
  stopHistoryPolling()
  sse.close()
  // Clean up object URLs for pending images
  for (const image of pendingImages.value) {
    URL.revokeObjectURL(image.objectUrl)
  }
})
</script>

<template>
  <div class="intake-chat">
    <div v-if="connectionError" class="connection-error">
      Connection lost. Please refresh the page.
    </div>

    <div class="chat-container" ref="chatContainer">
      <div
        v-for="(message, index) in messages"
        :key="index"
        class="message"
        :class="message.role === 'user' ? 'message--user' : 'message--agent'"
      >
        <!--
          REQ-FECOURSE-612, -613, -614, -615: agent messages are rendered
          through the markdown→KaTeX→DOMPurify pipeline and injected as HTML.
          REQ-FECOURSE-616 (AMENDED): student (user) messages are now also rendered
          through the same sanitized pipeline, supporting Markdown and LaTeX.
          REQ-FECOURSE-625: user message attachments are rendered as bound <img> elements
          alongside the rendered content.
        -->
        <div
          v-if="message.role === 'agent'"
          class="message-bubble message-bubble--markdown"
          v-html="renderMarkdown(message.content)"
        ></div>
        <div
          v-else
          class="message-bubble message-bubble--markdown"
        >
          <div v-html="renderMarkdown(message.content)"></div>
          <div v-if="message.attachments && message.attachments.length > 0" class="message-attachments">
            <img
              v-for="(url, idx) in message.attachments"
              :key="idx"
              :src="url"
              :alt="`Attached image ${idx + 1}`"
              class="message-attachment-img"
            />
          </div>
        </div>
      </div>

      <div v-if="isHistoryPolling && messages.length === 0" class="message message--agent">
        <div class="message-bubble typing-indicator">
          <span></span><span></span><span></span>
        </div>
        <div class="preparing-label">Your professor is preparing your course…</div>
      </div>

      <div v-if="boundedWaitExceeded && messages.length === 0" class="preparing-hint">
        Your professor is taking longer than expected — feel free to introduce yourself and what you'd like to learn to get started.
      </div>

      <div v-if="isSending" class="message message--agent">
        <div class="message-bubble typing-indicator">
          <span></span><span></span><span></span>
        </div>
      </div>
    </div>

    <div v-if="sendError" class="send-error">
      <div class="error-content">
        <span>{{ sendErrorMessage }}</span>
        <button class="error-dismiss" @click="dismissSendError">Dismiss</button>
      </div>
    </div>

    <div class="chat-input-area">
      <div class="input-section">
        <!-- @{"req": ["REQ-FECOURSE-627", "REQ-FECOURSE-628"]}
             REQ-FECOURSE-627: textarea allows multi-line input; Shift+Enter inserts newline.
             REQ-FECOURSE-628: Enter without modifier sends the message.
        -->
        <textarea
          ref="textareaRef"
          v-model="userInput"
          class="chat-input"
          placeholder="Type a message..."
          :disabled="isSending"
          rows="1"
          @keydown.enter.exact.prevent="sendMessage"
          @input="autoGrowTextarea"
        ></textarea>
        <button
          class="attach-button"
          :disabled="isSending || pendingImages.length >= MAX_CHAT_IMAGES"
          @click="fileInputRef?.click()"
          title="Attach images (max 4)"
        >
          📎
        </button>
        <button
          class="send-button"
          :disabled="isSending"
          @click="sendMessage"
        >
          Send
        </button>
      </div>

      <input
        ref="fileInputRef"
        type="file"
        multiple
        accept="image/png,image/jpeg,image/gif,image/webp"
        class="file-input"
        style="display: none"
        @change="onImageFileSelect"
      />

      <div v-if="imageValidationError" class="image-error">
        {{ imageValidationError }}
      </div>
      <div v-if="imageUploadError" class="image-error">
        {{ imageUploadError }}
      </div>

      <div v-if="pendingImages.length > 0" class="image-preview-row">
        <div
          v-for="(image, idx) in pendingImages"
          :key="idx"
          class="image-preview-item"
        >
          <img :src="image.objectUrl" :alt="image.file.name" class="preview-img" />
          <button
            class="remove-button"
            @click="removeImage(idx)"
            :disabled="isSending"
            title="Remove image"
          >
            ✕
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.intake-chat {
  display: flex;
  flex-direction: column;
  height: 100%;
  max-width: 800px;
  margin: 0 auto;
  padding: 1rem;
  box-sizing: border-box;
}

.connection-error {
  color: #d32f2f;
  padding: 0.75rem 1rem;
  background-color: #ffebee;
  border-radius: 4px;
  border-left: 4px solid #d32f2f;
  margin-bottom: 1rem;
}

.chat-container {
  flex: 1;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  padding: 0.5rem 0;
}

.message {
  display: flex;
}

.message--agent {
  justify-content: flex-start;
}

.message--user {
  justify-content: flex-end;
}

.message-bubble {
  max-width: 70%;
  padding: 0.75rem 1rem;
  border-radius: 12px;
  line-height: 1.4;
  word-break: break-word;
}

.message--agent .message-bubble {
  background-color: #f0f0f0;
  color: #333;
  border-bottom-left-radius: 4px;
}

.message--user .message-bubble {
  background-color: #1976d2;
  color: white;
  border-bottom-right-radius: 4px;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.message-attachments {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
  margin-top: 0.25rem;
}

.message-attachment-img {
  max-width: 150px;
  max-height: 150px;
  border-radius: 6px;
  object-fit: cover;
}

.typing-indicator {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 0.75rem 1rem;
}

.typing-indicator span {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background-color: #999;
  animation: typing 1.4s infinite;
}

.typing-indicator span:nth-child(2) {
  animation-delay: 0.2s;
}

.typing-indicator span:nth-child(3) {
  animation-delay: 0.4s;
}

@keyframes typing {
  0%,
  60%,
  100% {
    opacity: 0.5;
    transform: translateY(0);
  }
  30% {
    opacity: 1;
    transform: translateY(-10px);
  }
}

.preparing-label {
  font-size: 0.875rem;
  color: #666;
  margin-top: 0.5rem;
  text-align: center;
  font-style: italic;
}

.preparing-hint {
  padding: 1rem;
  margin: 1rem 0;
  background-color: #f5f5f5;
  border-left: 4px solid #1976d2;
  border-radius: 4px;
  color: #666;
  text-align: center;
  line-height: 1.5;
}

.send-error {
  color: #d32f2f;
  padding: 0.75rem 1rem;
  background-color: #ffebee;
  border-radius: 4px;
  border-left: 4px solid #d32f2f;
  margin-bottom: 0.75rem;
}

.error-content {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
}

.error-dismiss {
  padding: 0.25rem 0.75rem;
  background-color: transparent;
  color: #d32f2f;
  border: 1px solid #d32f2f;
  border-radius: 4px;
  cursor: pointer;
  font-size: 0.875rem;
  white-space: nowrap;
  transition: background-color 0.2s;
}

.error-dismiss:hover {
  background-color: rgba(211, 47, 47, 0.05);
}

.chat-input-area {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  padding-top: 1rem;
  border-top: 1px solid #e0e0e0;
  flex-shrink: 0;
}

.input-section {
  display: flex;
  gap: 0.5rem;
}

.chat-input {
  flex: 1;
  padding: 0.75rem;
  border: 1px solid #ddd;
  border-radius: 4px;
  font-size: 1rem;
  box-sizing: border-box;
  font-family: inherit;
  resize: none;
  overflow-y: auto;
}

.chat-input:focus {
  outline: none;
  border-color: #1976d2;
  box-shadow: 0 0 0 3px rgba(25, 118, 210, 0.1);
}

.chat-input:disabled {
  background-color: #f5f5f5;
  cursor: not-allowed;
}

.attach-button {
  padding: 0.75rem 1rem;
  background-color: white;
  color: #666;
  border: 1px solid #ddd;
  border-radius: 4px;
  font-size: 1.2rem;
  cursor: pointer;
  transition: background-color 0.2s, border-color 0.2s;
  white-space: nowrap;
  flex-shrink: 0;
}

.attach-button:hover:not(:disabled) {
  background-color: #f5f5f5;
  border-color: #999;
}

.attach-button:disabled {
  color: #ccc;
  border-color: #e0e0e0;
  cursor: not-allowed;
}

.send-button {
  padding: 0.75rem 1.5rem;
  background-color: #1976d2;
  color: white;
  border: none;
  border-radius: 4px;
  font-size: 1rem;
  font-weight: 500;
  cursor: pointer;
  transition: background-color 0.2s;
  white-space: nowrap;
  flex-shrink: 0;
}

.send-button:hover:not(:disabled) {
  background-color: #1565c0;
}

.send-button:disabled {
  background-color: #ccc;
  cursor: not-allowed;
}

.image-error {
  color: #d32f2f;
  font-size: 0.875rem;
  padding: 0.5rem 0.75rem;
  background-color: #ffebee;
  border-radius: 4px;
  border-left: 3px solid #d32f2f;
}

.image-preview-row {
  display: flex;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.image-preview-item {
  position: relative;
  width: 60px;
  height: 60px;
  border-radius: 4px;
  overflow: hidden;
  border: 1px solid #ddd;
}

.preview-img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.remove-button {
  position: absolute;
  top: -1px;
  right: -1px;
  width: 20px;
  height: 20px;
  padding: 0;
  background-color: rgba(0, 0, 0, 0.5);
  color: white;
  border: none;
  border-radius: 0;
  font-size: 0.9rem;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: background-color 0.2s;
}

.remove-button:hover:not(:disabled) {
  background-color: rgba(0, 0, 0, 0.8);
}

.remove-button:disabled {
  cursor: not-allowed;
}

/* Markdown-rendered agent bubble: scope all generated elements so they don't
   bleed into the surrounding layout. deep() is needed because v-html content
   is not processed by Vue's scoped-attribute injection.
   REQ-FECOURSE-612 */
.message-bubble--markdown :deep(p) {
  margin: 0 0 0.5em;
}

.message-bubble--markdown :deep(p:last-child) {
  margin-bottom: 0;
}

.message-bubble--markdown :deep(h1),
.message-bubble--markdown :deep(h2),
.message-bubble--markdown :deep(h3),
.message-bubble--markdown :deep(h4),
.message-bubble--markdown :deep(h5),
.message-bubble--markdown :deep(h6) {
  margin: 0.75em 0 0.25em;
  font-weight: 600;
  line-height: 1.3;
}

.message-bubble--markdown :deep(ul),
.message-bubble--markdown :deep(ol) {
  margin: 0.25em 0 0.5em;
  padding-left: 1.4em;
}

.message-bubble--markdown :deep(li) {
  margin-bottom: 0.2em;
}

.message-bubble--markdown :deep(code) {
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, monospace;
  font-size: 0.875em;
  background-color: rgba(0, 0, 0, 0.08);
  border-radius: 3px;
  padding: 0.1em 0.35em;
}

.message-bubble--markdown :deep(pre) {
  background-color: rgba(0, 0, 0, 0.08);
  border-radius: 6px;
  padding: 0.75em 1em;
  overflow-x: auto;
  margin: 0.5em 0;
}

.message-bubble--markdown :deep(pre code) {
  background-color: transparent;
  padding: 0;
  border-radius: 0;
  font-size: 0.875em;
}

.message-bubble--markdown :deep(blockquote) {
  border-left: 3px solid rgba(0, 0, 0, 0.2);
  margin: 0.5em 0;
  padding: 0.25em 0.75em;
  color: #555;
}

.message-bubble--markdown :deep(table) {
  border-collapse: collapse;
  margin: 0.5em 0;
  width: 100%;
  font-size: 0.9em;
}

.message-bubble--markdown :deep(th),
.message-bubble--markdown :deep(td) {
  border: 1px solid rgba(0, 0, 0, 0.15);
  padding: 0.3em 0.6em;
  text-align: left;
}

.message-bubble--markdown :deep(th) {
  background-color: rgba(0, 0, 0, 0.05);
  font-weight: 600;
}

/* Images from agent content: constrain width, REQ-FECOURSE-614 */
.message-bubble--markdown :deep(img) {
  max-width: 100%;
  height: auto;
  border-radius: 4px;
  display: block;
  margin: 0.5em 0;
}

/* KaTeX display-mode block: center it, REQ-FECOURSE-613 */
.message-bubble--markdown :deep(.katex-display) {
  overflow-x: auto;
  overflow-y: hidden;
  margin: 0.5em 0;
}

/* User message bubble markdown styling: ensure visibility on blue background.
   REQ-FECOURSE-616: user messages rendered through sanitized markdown pipeline.
*/
.message--user.message-bubble--markdown :deep(code) {
  background-color: rgba(0, 0, 0, 0.2);
  padding: 0.1em 0.35em;
  border-radius: 3px;
}

.message--user.message-bubble--markdown :deep(pre) {
  background-color: rgba(0, 0, 0, 0.15);
  padding: 0.75em 1em;
  border-radius: 6px;
  overflow-x: auto;
  margin: 0.5em 0;
}

.message--user.message-bubble--markdown :deep(pre code) {
  background-color: transparent;
  padding: 0;
  border-radius: 0;
}

.message--user.message-bubble--markdown :deep(a) {
  color: #e3f2fd;
  text-decoration: underline;
}

.message--user.message-bubble--markdown :deep(a:hover) {
  color: #fff;
}

.message--user.message-bubble--markdown :deep(blockquote) {
  border-left: 3px solid rgba(255, 255, 255, 0.3);
  padding: 0.25em 0.75em;
  color: rgba(255, 255, 255, 0.9);
}

.message--user.message-bubble--markdown :deep(strong),
.message--user.message-bubble--markdown :deep(b) {
  font-weight: 600;
}

.message--user.message-bubble--markdown :deep(em),
.message--user.message-bubble--markdown :deep(i) {
  font-style: italic;
}
</style>
