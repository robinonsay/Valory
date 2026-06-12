// @{"req": ["REQ-FECOURSE-065", "REQ-FECOURSE-066", "REQ-FECOURSE-067", "REQ-FECOURSE-070", "REQ-FECOURSE-530", "REQ-FECOURSE-540", "REQ-FECOURSE-550", "REQ-FECOURSE-560"]}
<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useSSE } from '@/composables/useSSE'

const router = useRouter()
const route = useRoute()
const auth = useAuthStore()

const messages = ref<string[]>([])
const progressPercent = ref(0)
const errorMessage = ref<string | null>(null)
let sse: { close: () => void } | null = null

function onEvent(eventType: string, data: string): void {
  if (eventType === 'progress') {
    try {
      const parsed = JSON.parse(data)
      progressPercent.value = parsed.percent || 0
      if (parsed.message) {
        messages.value.push(parsed.message)
      }
    } catch {
      messages.value.push(data)
    }
  } else if (eventType === 'status_change') {
    try {
      const parsed = JSON.parse(data)
      if (parsed.status === 'active') {
        router.push(`/courses/${route.params.id}/hub`)
      }
    } catch {
      messages.value.push(data)
    }
  } else {
    messages.value.push(data)
  }
}

function onError(err: Error): void {
  errorMessage.value = 'Generation failed. Please contact support.'
}

onMounted(() => {
  // REQ-AUTH-011: token is omitted; the browser sends the __Host-session cookie
  // automatically on same-origin fetch requests made by useSSE.
  sse = useSSE(`/api/v1/courses/${route.params.id}/events`, {
    onEvent,
    onError
  })
})

onUnmounted(() => {
  if (sse) {
    sse.close()
  }
})
</script>

<template>
  <div class="generating-container">
    <div class="generating-content">
      <h1>Generating Course Content</h1>
      <p class="progress-message">
        Course content generation may take several minutes. Please wait.
      </p>

      <div v-if="!errorMessage" class="progress-section">
        <div class="spinner"></div>
        <div class="progress-bar-container">
          <div class="progress-bar" :style="{ width: progressPercent + '%' }"></div>
        </div>
        <p class="progress-text">{{ progressPercent }}%</p>
      </div>

      <div v-if="errorMessage" class="error-message">
        {{ errorMessage }}
      </div>

      <div class="log-container">
        <div class="log-header">Generation Log</div>
        <div class="log-messages">
          <div v-for="(message, index) in messages" :key="index" class="log-message">
            {{ message }}
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.generating-container {
  display: flex;
  justify-content: center;
  align-items: flex-start;
  min-height: 100vh;
  padding: 20px;
  background-color: #f5f5f5;
}

.generating-content {
  max-width: 600px;
  width: 100%;
  margin-top: 40px;
}

h1 {
  text-align: center;
  color: #333;
  margin-bottom: 20px;
  font-size: 28px;
}

.progress-message {
  text-align: center;
  color: #666;
  margin-bottom: 30px;
  font-size: 16px;
}

.progress-section {
  display: flex;
  flex-direction: column;
  align-items: center;
  margin-bottom: 30px;
}

.spinner {
  width: 40px;
  height: 40px;
  border: 4px solid #e0e0e0;
  border-top: 4px solid #1976d2;
  border-radius: 50%;
  animation: spin 1s linear infinite;
  margin-bottom: 20px;
}

@keyframes spin {
  0% {
    transform: rotate(0deg);
  }
  100% {
    transform: rotate(360deg);
  }
}

.progress-bar-container {
  width: 100%;
  height: 20px;
  background-color: #e0e0e0;
  border-radius: 10px;
  overflow: hidden;
  margin-bottom: 10px;
}

.progress-bar {
  height: 100%;
  background-color: #1976d2;
  transition: width 0.3s ease;
}

.progress-text {
  text-align: center;
  color: #666;
  font-size: 14px;
  margin: 0;
}

.error-message {
  color: #d32f2f;
  padding: 15px;
  background-color: #ffebee;
  border-radius: 4px;
  margin-bottom: 20px;
  text-align: center;
}

.log-container {
  background-color: white;
  border-radius: 4px;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
  overflow: hidden;
}

.log-header {
  background-color: #f5f5f5;
  padding: 12px 16px;
  font-weight: 600;
  border-bottom: 1px solid #e0e0e0;
}

.log-messages {
  max-height: 300px;
  overflow-y: auto;
  padding: 12px 16px;
}

.log-message {
  padding: 8px 0;
  border-bottom: 1px solid #f0f0f0;
  font-size: 14px;
  color: #333;
  line-height: 1.4;
}

.log-message:last-child {
  border-bottom: none;
}
</style>
