// @{"req": ["REQ-FEPROFILE-001", "REQ-FEPROFILE-003"]}

<script setup lang="ts">
import { ref, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { post, ApiError } from '@/api/client'
import { renderMarkdown } from '@/utils/renderMarkdown'
import 'katex/dist/katex.min.css'

interface Message {
  role: 'assistant' | 'student'
  content: string
}

interface OnboardingStartResponse {
  session_active: boolean
  first_message: string
}

interface OnboardingAdvanceResponse {
  reply: string
  done: boolean
}

interface OnboardingCompleteResponse {
  summary: string
}

const router = useRouter()
const auth = useAuthStore()

const messages = ref<Message[]>([])
const userInput = ref('')
const textareaRef = ref<HTMLTextAreaElement | null>(null)
const isSending = ref(false)
const sendError = ref(false)
const sendErrorMessage = ref('')
const chatContainer = ref<HTMLElement | null>(null)
const isStarting = ref(true)
const isSkipping = ref(false)
const conversationDone = ref(false)

// @{"req": ["REQ-FEPROFILE-001"]}
async function startOnboarding(): Promise<void> {
  isStarting.value = true
  try {
    const response = await post<OnboardingStartResponse>(
      '/api/v1/profile/onboarding/start',
      {}
    )
    if (response.session_active && response.first_message) {
      messages.value = [
        {
          role: 'assistant',
          content: response.first_message
        }
      ]
      await nextTick()
      scrollToBottom()
    }
  } catch (error) {
    sendError.value = true
    if (error instanceof ApiError) {
      sendErrorMessage.value = 'Failed to start onboarding. Please try again.'
    } else {
      sendErrorMessage.value = 'An error occurred. Please try again.'
    }
  } finally {
    isStarting.value = false
  }
}

function scrollToBottom(): void {
  if (chatContainer.value && chatContainer.value.scrollTo) {
    nextTick(() => {
      chatContainer.value?.scrollTo(0, chatContainer.value.scrollHeight)
    })
  }
}

// @{"req": ["REQ-FEPROFILE-001"]}
async function sendMessage(): Promise<void> {
  const content = userInput.value.trim()
  if (!content || isSending.value) return

  isSending.value = true
  sendError.value = false

  const userMessage: Message = {
    role: 'student',
    content
  }
  messages.value.push(userMessage)
  userInput.value = ''
  await nextTick()
  scrollToBottom()

  try {
    const response = await post<OnboardingAdvanceResponse>(
      '/api/v1/profile/onboarding/advance',
      { message: content }
    )

    messages.value.push({
      role: 'assistant',
      content: response.reply
    })

    if (response.done) {
      conversationDone.value = true
    }

    await nextTick()
    scrollToBottom()
  } catch (error) {
    sendError.value = true
    if (error instanceof ApiError) {
      sendErrorMessage.value = 'Failed to send message. Please try again.'
    } else {
      sendErrorMessage.value = 'An error occurred. Please try again.'
    }
  } finally {
    isSending.value = false
  }
}

// @{"req": ["REQ-FEPROFILE-001"]}
async function completeOnboarding(): Promise<void> {
  isSending.value = true
  try {
    await post<OnboardingCompleteResponse>('/api/v1/profile/onboarding/complete', {})
    await auth.refreshSession()
    router.push('/courses')
  } catch (error) {
    sendError.value = true
    if (error instanceof ApiError) {
      sendErrorMessage.value = 'Failed to complete onboarding. Please try again.'
    } else {
      sendErrorMessage.value = 'An error occurred. Please try again.'
    }
    isSending.value = false
  }
}

// @{"req": ["REQ-FEPROFILE-003"]}
async function skipOnboarding(): Promise<void> {
  if (!confirm('Are you sure you want to skip onboarding? You can always complete it later from your profile settings.')) {
    return
  }

  isSkipping.value = true
  try {
    await post<{ skipped: boolean }>('/api/v1/profile/onboarding/skip', {})
    await auth.refreshSession()
    router.push('/courses')
  } catch (error) {
    sendError.value = true
    if (error instanceof ApiError) {
      sendErrorMessage.value = 'Failed to skip onboarding. Please try again.'
    } else {
      sendErrorMessage.value = 'An error occurred. Please try again.'
    }
    isSkipping.value = false
  }
}

// Start onboarding when the view mounts
import { onMounted } from 'vue'
onMounted(() => {
  startOnboarding()
})
</script>

<template>
  <div class="onboarding-container">
    <div class="onboarding-header">
      <h1>Let's get to know your learning style</h1>
      <p>
        Help us personalize your courses by answering a few quick questions about
        how you learn best. You can always edit this later from your profile settings.
      </p>
    </div>

    <div v-if="sendError" class="error-message">
      {{ sendErrorMessage }}
    </div>

    <div ref="chatContainer" class="chat-container">
      <div
        v-for="(msg, idx) in messages"
        :key="idx"
        :class="['message-bubble', msg.role]"
      >
        <div v-if="msg.role === 'assistant'" class="message-content" v-html="renderMarkdown(msg.content)" />
        <div v-else class="message-content">
          {{ msg.content }}
        </div>
      </div>

      <div v-if="isStarting" class="loading-indicator">
        <span>Loading...</span>
      </div>
    </div>

    <div class="input-section">
      <form @submit.prevent="sendMessage">
        <textarea
          ref="textareaRef"
          v-model="userInput"
          placeholder="Type your response..."
          :disabled="isSending || isStarting || isSkipping || conversationDone"
          class="chat-textarea"
          @keydown.enter.ctrl="sendMessage"
          @keydown.enter.meta="sendMessage"
        />
        <div class="button-group">
          <button
            v-if="!conversationDone"
            type="button"
            class="skip-button"
            @click="skipOnboarding"
            :disabled="isSending || isStarting || isSkipping"
          >
            {{ isSkipping ? 'Skipping...' : 'Skip for now' }}
          </button>
          <button
            v-if="conversationDone"
            type="button"
            class="complete-button"
            @click="completeOnboarding"
            :disabled="isSending"
          >
            {{ isSending ? 'Completing...' : 'Complete' }}
          </button>
          <button
            v-else
            type="submit"
            class="send-button"
            :disabled="!userInput.trim() || isSending || isStarting || isSkipping"
          >
            {{ isSending ? 'Sending...' : 'Send' }}
          </button>
        </div>
      </form>
    </div>
  </div>
</template>

<style scoped>
.onboarding-container {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background-color: #f5f5f5;
}

.onboarding-header {
  padding: 2rem;
  background-color: white;
  border-bottom: 1px solid #ddd;
  text-align: center;
}

.onboarding-header h1 {
  margin: 0 0 1rem;
  font-size: 1.75rem;
  color: #333;
}

.onboarding-header p {
  margin: 0;
  color: #666;
  line-height: 1.5;
  max-width: 600px;
  margin: 0 auto;
}

.error-message {
  padding: 1rem;
  background-color: #fee;
  color: #c33;
  border-left: 4px solid #c33;
  margin: 1rem;
}

.chat-container {
  flex: 1;
  overflow-y: auto;
  padding: 1.5rem;
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.message-bubble {
  display: flex;
  margin-bottom: 0.5rem;
}

.message-bubble.assistant {
  justify-content: flex-start;
}

.message-bubble.student {
  justify-content: flex-end;
}

.message-content {
  max-width: 70%;
  padding: 0.75rem 1rem;
  border-radius: 8px;
  word-wrap: break-word;
}

.message-bubble.assistant .message-content {
  background-color: #e0e0e0;
  color: #333;
}

.message-bubble.student .message-content {
  background-color: #007bff;
  color: white;
}

.loading-indicator {
  display: flex;
  justify-content: center;
  padding: 1rem;
}

.loading-indicator span {
  color: #666;
}

.input-section {
  padding: 1.5rem;
  background-color: white;
  border-top: 1px solid #ddd;
}

.chat-textarea {
  width: 100%;
  min-height: 80px;
  max-height: 120px;
  padding: 0.75rem;
  border: 1px solid #ddd;
  border-radius: 4px;
  font-size: 1rem;
  font-family: inherit;
  resize: vertical;
  margin-bottom: 1rem;
}

.chat-textarea:disabled {
  background-color: #f9f9f9;
  color: #999;
}

.button-group {
  display: flex;
  gap: 0.5rem;
  justify-content: flex-end;
}

.send-button,
.skip-button,
.complete-button {
  padding: 0.75rem 1.5rem;
  border: none;
  border-radius: 4px;
  font-size: 1rem;
  cursor: pointer;
  transition: background-color 0.2s ease;
}

.send-button,
.complete-button {
  background-color: #007bff;
  color: white;
}

.send-button:hover:not(:disabled),
.complete-button:hover:not(:disabled) {
  background-color: #0056b3;
}

.send-button:disabled,
.complete-button:disabled {
  background-color: #ccc;
  cursor: not-allowed;
}

.skip-button {
  background-color: transparent;
  color: #666;
  border: 1px solid #ddd;
}

.skip-button:hover:not(:disabled) {
  background-color: #f9f9f9;
}

.skip-button:disabled {
  color: #ccc;
  cursor: not-allowed;
}
</style>
