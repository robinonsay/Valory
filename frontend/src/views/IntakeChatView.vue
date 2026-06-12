// @{"req": ["REQ-FECOURSE-026", "REQ-FECOURSE-027", "REQ-FECOURSE-028", "REQ-FECOURSE-070", "REQ-FECOURSE-071", "REQ-FECOURSE-220", "REQ-FECOURSE-221", "REQ-FECOURSE-222", "REQ-FECOURSE-223", "REQ-FECOURSE-224", "REQ-FECOURSE-225", "REQ-FECOURSE-230", "REQ-FECOURSE-231", "REQ-FECOURSE-240", "REQ-FECOURSE-250", "REQ-FECOURSE-251", "REQ-FECOURSE-252", "REQ-FECOURSE-260", "REQ-FECOURSE-261", "REQ-FECOURSE-262", "REQ-FECOURSE-263", "REQ-FECOURSE-264", "REQ-FECOURSE-270"]}
<script setup lang="ts">
import { ref, onMounted, onUnmounted, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useSSE } from '@/composables/useSSE'
import { get, post, ApiError } from '@/api/client'

interface Message {
  role: 'agent' | 'user'
  content: string
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
const isSending = ref(false)
const connectionError = ref(false)
const sendError = ref(false)
const sendErrorMessage = ref('')
const chatContainer = ref<HTMLElement | null>(null)

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

// @{"req": ["REQ-FECOURSE-220", "REQ-FECOURSE-221", "REQ-FECOURSE-222", "REQ-FECOURSE-223", "REQ-FECOURSE-224", "REQ-FECOURSE-225", "REQ-FECOURSE-262"]}
async function sendMessage(): Promise<void> {
  const content = userInput.value.trim()
  if (!content || isSending.value) return

  // Stop polling when student sends their first message
  stopHistoryPolling()

  // Optimistically add the user message before the POST resolves
  messages.value.push({ role: 'user', content })
  userInput.value = ''
  isSending.value = true
  sendError.value = false
  scrollToBottom()

  try {
    const courseId = route.params.id as string
    const response = await post<ChatReplyResponse>(
      `/api/v1/courses/${courseId}/chat`,
      { message: content }
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
    // Do not remove the optimistic message; let the student retry
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
        <div class="message-bubble">
          {{ message.content }}
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
      <input
        v-model="userInput"
        type="text"
        class="chat-input"
        placeholder="Type a message..."
        :disabled="isSending"
        @keydown.enter="sendMessage"
      />
      <button
        class="send-button"
        :disabled="isSending"
        @click="sendMessage"
      >
        Send
      </button>
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
  gap: 0.5rem;
  padding-top: 1rem;
  border-top: 1px solid #e0e0e0;
  flex-shrink: 0;
}

.chat-input {
  flex: 1;
  padding: 0.75rem;
  border: 1px solid #ddd;
  border-radius: 4px;
  font-size: 1rem;
  box-sizing: border-box;
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
</style>
