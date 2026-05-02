// @{"req": ["REQ-FECOURSE-040", "REQ-FECOURSE-041", "REQ-FECOURSE-042", "REQ-FECOURSE-043", "REQ-FECOURSE-070", "REQ-FECOURSE-071", "REQ-FECOURSE-400", "REQ-FECOURSE-410", "REQ-FECOURSE-420"]}
<script setup lang="ts">
import { ref, onMounted, onUnmounted, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useSSE } from '@/composables/useSSE'
import { post } from '@/api/client'

interface Message {
  role: 'agent' | 'user'
  content: string
}

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const messages = ref<Message[]>([])
const userInput = ref('')
const isSending = ref(false)
const connectionError = ref(false)
const chatContainer = ref<HTMLElement | null>(null)

// @{"req": ["REQ-FECOURSE-040", "REQ-FECOURSE-041", "REQ-FECOURSE-042", "REQ-FECOURSE-043", "REQ-FECOURSE-070", "REQ-FECOURSE-071", "REQ-FECOURSE-400", "REQ-FECOURSE-410", "REQ-FECOURSE-420"]}
function onEvent(eventType: string, data: string): void {
  if (eventType === 'keepalive') {
    return
  }

  if (eventType === 'message') {
    try {
      const parsed = JSON.parse(data) as { role: 'agent' | 'user'; content: string }
      messages.value.push({ role: parsed.role, content: parsed.content })
      scrollToBottom()
    } catch {
      // Malformed message data — ignore rather than crash
    }
    return
  }

  if (eventType === 'status_change') {
    try {
      const parsed = JSON.parse(data) as { status: string }
      if (parsed.status === 'syllabus_draft') {
        router.push(`/courses/${route.params.id}/syllabus`)
      }
    } catch {
      // Malformed status_change data — ignore
    }
    return
  }
}

// @{"req": ["REQ-FECOURSE-040", "REQ-FECOURSE-041", "REQ-FECOURSE-042", "REQ-FECOURSE-043", "REQ-FECOURSE-070", "REQ-FECOURSE-071", "REQ-FECOURSE-400", "REQ-FECOURSE-410", "REQ-FECOURSE-420"]}
function onError(): void {
  connectionError.value = true
}

// @{"req": ["REQ-FECOURSE-040", "REQ-FECOURSE-041", "REQ-FECOURSE-042", "REQ-FECOURSE-043", "REQ-FECOURSE-070", "REQ-FECOURSE-071", "REQ-FECOURSE-400", "REQ-FECOURSE-410", "REQ-FECOURSE-420"]}
async function sendMessage(): Promise<void> {
  const content = userInput.value.trim()
  if (!content || isSending.value) return

  // Optimistically add the user message before the POST resolves
  messages.value.push({ role: 'user', content })
  userInput.value = ''
  isSending.value = true
  scrollToBottom()

  try {
    await post(`/api/v1/courses/${route.params.id}/chat`, { message: content }, auth.token)
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

const sse = useSSE(`/api/v1/courses/${route.params.id}/events`, {
  token: auth.token!,
  onEvent,
  onError
})

onMounted(() => {
  // SSE is started at module init time (useSSE connects immediately).
  // Nothing additional needed on mount.
})

onUnmounted(() => {
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
  height: 100vh;
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

.chat-input-area {
  display: flex;
  gap: 0.5rem;
  padding-top: 1rem;
  border-top: 1px solid #e0e0e0;
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
}

.send-button:hover:not(:disabled) {
  background-color: #1565c0;
}

.send-button:disabled {
  background-color: #ccc;
  cursor: not-allowed;
}
</style>
