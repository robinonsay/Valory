// @{"req": ["REQ-FECOURSE-055", "REQ-FECOURSE-056", "REQ-FECOURSE-057", "REQ-FECOURSE-060", "REQ-FECOURSE-460", "REQ-FECOURSE-470", "REQ-FECOURSE-480", "REQ-FECOURSE-490", "REQ-FECOURSE-491", "REQ-FECOURSE-510", "REQ-FECOURSE-520"]}

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { get, post, ApiError } from '@/api/client'

interface SyllabusResponse {
  id: string
  course_id: string
  content_adoc: string
  version: number
  approved_at: string | null
  created_at: string
}

const router = useRouter()
const route = useRoute()
const auth = useAuthStore()

const courseId = route.params.id as string

const isLoading = ref(true)
const fetchError = ref('')
const syllabusContent = ref('')
const isDrafting = ref(false)
const isDraftingWaitExceeded = ref(false)

const modificationText = ref('')
const isModificationModalOpen = ref(false)
const isSubmittingModification = ref(false)
const modificationError = ref('')
const modificationSuccess = ref('')

const isApprovingLoading = ref(false)
const approveError = ref('')

let draftingPollInterval: NodeJS.Timeout | null = null
let draftingBoundedWaitTimer: NodeJS.Timeout | null = null

// @{"req": ["REQ-FECOURSE-055", "REQ-FECOURSE-056", "REQ-FECOURSE-057", "REQ-FECOURSE-060", "REQ-FECOURSE-490", "REQ-FECOURSE-491"]}
async function fetchSyllabus() {
  isLoading.value = true
  fetchError.value = ''
  isDrafting.value = false
  isDraftingWaitExceeded.value = false
  try {
    const response = await get<SyllabusResponse>(
      `/api/v1/courses/${courseId}/syllabus`,
      auth.token
    )
    syllabusContent.value = response.content_adoc
    stopDraftingPolling()
  } catch (err) {
    if (err instanceof ApiError) {
      if (err.status === 404) {
        // Syllabus not yet generated; show drafting indicator and poll
        isDrafting.value = true
        isLoading.value = false
        startDraftingPolling()
        return
      }
      fetchError.value = 'Failed to load syllabus. Please try again.'
    } else {
      fetchError.value = 'An error occurred. Please try again.'
    }
  } finally {
    if (!isDrafting.value) {
      isLoading.value = false
    }
  }
}

// @{"req": ["REQ-FECOURSE-490", "REQ-FECOURSE-491"]}
function startDraftingPolling(): void {
  const startTime = Date.now()
  const POLL_INTERVAL = 4000
  const BOUNDED_WAIT_MS = 180000

  draftingPollInterval = setInterval(async () => {
    const elapsed = Date.now() - startTime
    if (elapsed > BOUNDED_WAIT_MS) {
      stopDraftingPolling()
      isDraftingWaitExceeded.value = true
      return
    }

    try {
      const response = await get<SyllabusResponse>(
        `/api/v1/courses/${courseId}/syllabus`,
        auth.token
      )
      syllabusContent.value = response.content_adoc
      isDrafting.value = false
      isDraftingWaitExceeded.value = false
      isLoading.value = false
      stopDraftingPolling()
    } catch (err) {
      if (!(err instanceof ApiError) || err.status !== 404) {
        stopDraftingPolling()
        isDrafting.value = false
        if (err instanceof ApiError) {
          fetchError.value = 'Failed to load syllabus. Please try again.'
        } else {
          fetchError.value = 'An error occurred. Please try again.'
        }
      }
    }
  }, POLL_INTERVAL)

  draftingBoundedWaitTimer = setTimeout(() => {
    if (isDrafting.value) {
      stopDraftingPolling()
      isDraftingWaitExceeded.value = true
    }
  }, BOUNDED_WAIT_MS)
}

function stopDraftingPolling(): void {
  if (draftingPollInterval !== null) {
    clearInterval(draftingPollInterval)
    draftingPollInterval = null
  }
  if (draftingBoundedWaitTimer !== null) {
    clearTimeout(draftingBoundedWaitTimer)
    draftingBoundedWaitTimer = null
  }
}

// @{"req": ["REQ-FECOURSE-470", "REQ-FECOURSE-480"]}
async function submitModification() {
  if (!modificationText.value.trim()) {
    modificationError.value = 'Please enter a modification request.'
    return
  }

  modificationError.value = ''
  modificationSuccess.value = ''
  isSubmittingModification.value = true

  try {
    await post(
      `/api/v1/courses/${courseId}/syllabus/modification`,
      { request: modificationText.value },
      auth.token
    )

    modificationSuccess.value = 'Modification request submitted. Reloading syllabus...'
    modificationText.value = ''

    await new Promise(resolve => setTimeout(resolve, 1000))
    await fetchSyllabus()

    isModificationModalOpen.value = false
  } catch (err) {
    if (err instanceof ApiError) {
      modificationError.value = 'Failed to submit modification. Please try again.'
    } else {
      modificationError.value = 'An error occurred. Please try again.'
    }
  } finally {
    isSubmittingModification.value = false
  }
}

// @{"req": ["REQ-FECOURSE-510", "REQ-FECOURSE-520"]}
async function handleApprove() {
  approveError.value = ''
  isApprovingLoading.value = true

  try {
    await post(
      `/api/v1/courses/${courseId}/syllabus/approve`,
      {},
      auth.token
    )

    router.push(`/courses/${courseId}/generating`)
  } catch (err) {
    if (err instanceof ApiError) {
      if (err.status === 409) {
        approveError.value = 'Cannot approve syllabus at this time.'
      } else {
        approveError.value = 'Failed to approve syllabus. Please try again.'
      }
    } else {
      approveError.value = 'An error occurred. Please try again.'
    }
    isApprovingLoading.value = false
  }
}

function openModificationModal() {
  modificationText.value = ''
  modificationError.value = ''
  modificationSuccess.value = ''
  isModificationModalOpen.value = true
}

function closeModificationModal() {
  isModificationModalOpen.value = false
  modificationText.value = ''
  modificationError.value = ''
  modificationSuccess.value = ''
}

onMounted(() => {
  fetchSyllabus()
})

onUnmounted(() => {
  stopDraftingPolling()
})
</script>

<template>
  <div class="syllabus-view">
    <h1>Syllabus Draft</h1>

    <div v-if="isLoading" class="loading">
      Loading syllabus...
    </div>

    <div v-if="isDrafting && !isDraftingWaitExceeded" class="drafting-indicator">
      <div class="typing-indicator">
        <span></span><span></span><span></span>
      </div>
      <p>Your professor is drafting your syllabus…</p>
    </div>

    <div v-if="isDraftingWaitExceeded" class="drafting-wait-exceeded">
      <p>Syllabus generation is taking longer than expected.</p>
      <button @click="fetchSyllabus()" class="btn btn-primary">
        Try Again
      </button>
    </div>

    <div v-if="fetchError" class="error-message">
      {{ fetchError }}
    </div>

    <template v-if="!isLoading && !isDrafting && !fetchError">
      <div class="syllabus-content">
        <div class="syllabus-text">
          <pre>{{ syllabusContent }}</pre>
        </div>
      </div>

      <div v-if="approveError" class="error-message">
        {{ approveError }}
      </div>

      <div class="actions">
        <button
          @click="openModificationModal"
          class="btn btn-secondary"
          :disabled="isApprovingLoading"
        >
          Request Modification
        </button>
        <button
          @click="handleApprove"
          class="btn btn-primary"
          :disabled="isApprovingLoading"
        >
          {{ isApprovingLoading ? 'Approving...' : 'Approve and Start Course' }}
        </button>
      </div>
    </template>

    <div v-if="isModificationModalOpen" class="modal-overlay" @click="closeModificationModal">
      <div class="modal-content" @click.stop>
        <h2>Request Modification</h2>

        <div v-if="modificationError" class="error-message">
          {{ modificationError }}
        </div>

        <div v-if="modificationSuccess" class="success-message">
          {{ modificationSuccess }}
        </div>

        <textarea
          v-model="modificationText"
          placeholder="Describe the changes you'd like to make..."
          class="modification-textarea"
          :disabled="isSubmittingModification"
        ></textarea>

        <div class="modal-actions">
          <button
            @click="closeModificationModal"
            class="btn btn-secondary"
            :disabled="isSubmittingModification"
          >
            Cancel
          </button>
          <button
            @click="submitModification"
            class="btn btn-primary"
            :disabled="isSubmittingModification"
          >
            {{ isSubmittingModification ? 'Submitting...' : 'Submit Request' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.syllabus-view {
  max-width: 900px;
  margin: 0 auto;
  padding: 2rem;
}

h1 {
  font-size: 2rem;
  margin-bottom: 2rem;
  color: #333;
}

h2 {
  font-size: 1.5rem;
  margin-bottom: 1rem;
  color: #555;
}

h3 {
  font-size: 1.1rem;
  margin-bottom: 0.5rem;
  color: #666;
}

.loading {
  padding: 2rem;
  text-align: center;
  color: #666;
  font-size: 1.1rem;
}

.drafting-indicator {
  padding: 2rem;
  text-align: center;
  color: #666;
  font-size: 1.1rem;
}

.typing-indicator {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  margin-bottom: 1rem;
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

.drafting-wait-exceeded {
  padding: 2rem;
  text-align: center;
  background-color: #f5f5f5;
  border-radius: 8px;
  margin: 2rem 0;
}

.drafting-wait-exceeded p {
  color: #666;
  font-size: 1.1rem;
  margin-bottom: 1.5rem;
}

.error-message {
  color: #d32f2f;
  padding: 1rem;
  margin-bottom: 1rem;
  background-color: #ffebee;
  border-radius: 4px;
  border-left: 4px solid #d32f2f;
}

.success-message {
  color: #388e3c;
  padding: 1rem;
  margin-bottom: 1rem;
  background-color: #e8f5e9;
  border-radius: 4px;
  border-left: 4px solid #388e3c;
}

.syllabus-content {
  margin-bottom: 2rem;
  padding: 1.5rem;
  background-color: #f9f9f9;
  border-radius: 8px;
}

.syllabus-text {
  background-color: white;
  padding: 1rem;
  border-radius: 4px;
  border-left: 4px solid #1976d2;
}

.syllabus-text pre {
  margin: 0;
  font-family: 'Courier New', monospace;
  white-space: pre-wrap;
  word-wrap: break-word;
  color: #333;
  line-height: 1.6;
}

.learning-objectives {
  margin-bottom: 2rem;
}

.learning-objectives ul {
  list-style-position: inside;
  margin-top: 1rem;
}

.learning-objectives li {
  margin-bottom: 0.5rem;
  line-height: 1.6;
}

.sections-list {
  margin-top: 1rem;
}

.section-item {
  margin-bottom: 1.5rem;
  padding: 1rem;
  background-color: white;
  border-radius: 4px;
  border-left: 4px solid #1976d2;
}

.section-item p {
  margin: 0.5rem 0;
  line-height: 1.6;
  color: #666;
}

.due-date {
  font-weight: 500;
  color: #1976d2;
  margin-top: 0.5rem;
}

.actions {
  display: flex;
  gap: 1rem;
  justify-content: center;
  margin-top: 2rem;
}

.btn {
  padding: 0.75rem 1.5rem;
  font-size: 1rem;
  font-weight: 500;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  transition: background-color 0.2s;
}

.btn-primary {
  background-color: #1976d2;
  color: white;
}

.btn-primary:hover:not(:disabled) {
  background-color: #1565c0;
}

.btn-secondary {
  background-color: #757575;
  color: white;
}

.btn-secondary:hover:not(:disabled) {
  background-color: #616161;
}

.btn:disabled {
  background-color: #ccc;
  cursor: not-allowed;
}

.modal-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background-color: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
}

.modal-content {
  background: white;
  padding: 2rem;
  border-radius: 8px;
  box-shadow: 0 4px 20px rgba(0, 0, 0, 0.2);
  width: 100%;
  max-width: 500px;
  max-height: 80vh;
  overflow-y: auto;
}

.modification-textarea {
  width: 100%;
  padding: 0.75rem;
  margin: 1rem 0;
  border: 1px solid #ddd;
  border-radius: 4px;
  font-family: inherit;
  font-size: 1rem;
  resize: vertical;
  min-height: 150px;
  box-sizing: border-box;
}

.modification-textarea:focus {
  outline: none;
  border-color: #1976d2;
  box-shadow: 0 0 0 3px rgba(25, 118, 210, 0.1);
}

.modification-textarea:disabled {
  background-color: #f5f5f5;
  cursor: not-allowed;
}

.modal-actions {
  display: flex;
  gap: 1rem;
  justify-content: flex-end;
  margin-top: 1.5rem;
}
</style>
