// @{"req": ["REQ-FECONTENT-001", "REQ-FECONTENT-002", "REQ-FECONTENT-010", "REQ-FECONTENT-011", "REQ-FECONTENT-015", "REQ-FECONTENT-016", "REQ-FECONTENT-017", "REQ-FECONTENT-100", "REQ-FECONTENT-110", "REQ-FECONTENT-115", "REQ-FECONTENT-130", "REQ-FECONTENT-140"]}

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { get, post, ApiError } from '@/api/client'
import DOMPurify from 'dompurify'

interface SectionContent {
  id: string
  title: string
  body: string
  order: number
  course_id: string
  total_sections: number
}

interface SectionListItem {
  id: string
  order: number
  title: string
}

interface SectionsListResponse {
  sections: SectionListItem[]
}

const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()

const courseId = route.params.id as string
const sectionId = route.params.sectionId as string

const section = ref<SectionContent | null>(null)
const sectionList = ref<SectionListItem[]>([])
const loading = ref(true)
const error = ref<string | null>(null)

const showFeedback = ref(false)
const feedbackText = ref('')
const feedbackError = ref<string | null>(null)
const feedbackSuccess = ref(false)
const isSubmittingFeedback = ref(false)

const showExportError = ref(false)

// @{"req": ["REQ-FECONTENT-010", "REQ-FECONTENT-100", "REQ-FECONTENT-001"]}
async function fetchSection(): Promise<void> {
  loading.value = true
  error.value = null
  try {
    const data = await get<SectionContent>(
      `/api/v1/courses/${courseId}/sections/${sectionId}`,
      authStore.token
    )
    section.value = data
  } catch (err) {
    if (err instanceof ApiError) {
      if (err.status === 404) {
        error.value = 'Section not found'
      } else {
        error.value = 'Failed to load section. Please try again.'
      }
    } else {
      error.value = 'Failed to load section. Please try again.'
    }
  } finally {
    loading.value = false
  }
}

// @{"req": ["REQ-FECONTENT-010", "REQ-FECONTENT-100", "REQ-FECONTENT-130"]}
async function fetchSectionList(): Promise<void> {
  try {
    const data = await get<SectionsListResponse>(
      `/api/v1/courses/${courseId}/sections`,
      authStore.token
    )
    sectionList.value = data.sections
  } catch {
    // Navigation list is best-effort; primary content error is handled by fetchSection
  }
}

onMounted(async () => {
  await Promise.all([fetchSection(), fetchSectionList()])
})

// @{"req": ["REQ-FECONTENT-130", "REQ-FECONTENT-009"]}
function prevSectionId(): string | null {
  if (!section.value) return null
  const sorted = [...sectionList.value].sort((a, b) => a.order - b.order)
  const idx = sorted.findIndex(s => s.id === sectionId)
  if (idx <= 0) return null
  return sorted[idx - 1].id
}

// @{"req": ["REQ-FECONTENT-130", "REQ-FECONTENT-009"]}
function nextSectionId(): string | null {
  if (!section.value) return null
  const sorted = [...sectionList.value].sort((a, b) => a.order - b.order)
  const idx = sorted.findIndex(s => s.id === sectionId)
  if (idx < 0 || idx >= sorted.length - 1) return null
  return sorted[idx + 1].id
}

// @{"req": ["REQ-FECONTENT-009", "REQ-FECONTENT-090"]}
function goToPrev(): void {
  const id = prevSectionId()
  if (id) {
    router.push(`/courses/${courseId}/sections/${id}`)
  }
}

// @{"req": ["REQ-FECONTENT-009", "REQ-FECONTENT-091"]}
function goToNext(): void {
  const id = nextSectionId()
  if (id) {
    router.push(`/courses/${courseId}/sections/${id}`)
  }
}

// @{"req": ["REQ-FECONTENT-070", "REQ-FECONTENT-007"]}
function openFeedback(): void {
  feedbackText.value = ''
  feedbackError.value = null
  feedbackSuccess.value = false
  showFeedback.value = true
}

// @{"req": ["REQ-FECONTENT-070", "REQ-FECONTENT-179"]}
function closeFeedback(): void {
  showFeedback.value = false
  feedbackError.value = null
}

// @{"req": ["REQ-FECONTENT-071", "REQ-FECONTENT-072", "REQ-FECONTENT-074", "REQ-FECONTENT-075", "REQ-FECONTENT-160", "REQ-FECONTENT-161", "REQ-FECONTENT-162", "REQ-FECONTENT-181"]}
async function submitFeedback(): Promise<void> {
  feedbackError.value = null

  if (!feedbackText.value.trim()) {
    feedbackError.value = 'Feedback text is required.'
    return
  }
  if (feedbackText.value.length > 2000) {
    feedbackError.value = 'Feedback must not exceed 2000 characters.'
    return
  }

  isSubmittingFeedback.value = true
  try {
    await post<unknown>(
      `/api/v1/courses/${courseId}/sections/${sectionId}/feedback`,
      { feedback_text: feedbackText.value },
      authStore.token
    )
    showFeedback.value = false
    feedbackSuccess.value = true
  } catch (err) {
    if (err instanceof ApiError) {
      feedbackError.value = `Failed to submit feedback (${err.status}). Please try again.`
    } else {
      feedbackError.value = 'Failed to submit feedback. Please try again.'
    }
  } finally {
    isSubmittingFeedback.value = false
  }
}

// @{"req": ["REQ-FECONTENT-060", "REQ-FECONTENT-061", "REQ-FECONTENT-157", "REQ-FECONTENT-158", "REQ-FECONTENT-159"]}
async function downloadExport(format: 'pdf' | 'html'): Promise<void> {
  showExportError.value = false
  const url = `/api/v1/courses/${courseId}/sections/${sectionId}/export?format=${format}`
  const response = await fetch(url, {
    headers: { Authorization: `Bearer ${authStore.token}` }
  })
  if (!response.ok) {
    showExportError.value = true
    return
  }
  const blob = await response.blob()
  const objectUrl = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = objectUrl
  anchor.download = `section-${sectionId}.${format}`
  document.body.appendChild(anchor)
  anchor.click()
  document.body.removeChild(anchor)
  URL.revokeObjectURL(objectUrl)
}
</script>

<template>
  <div class="section-reader">
    <div v-if="loading" class="loading">
      <p>Loading section...</p>
    </div>

    <div v-else-if="error" class="error-message">
      {{ error }}
    </div>

    <div v-else-if="section">
      <div class="section-header">
        <h1>{{ section.title }}</h1>
      </div>

      <div class="section-body" v-html="DOMPurify.sanitize(section.body)"></div>

      <div v-if="feedbackSuccess" class="success-message">
        Feedback submitted.
      </div>

      <div v-if="showExportError" class="error-message">
        Export failed. Please try again.
      </div>

      <div class="section-actions">
        <button class="feedback-button" @click="openFeedback">Give Feedback</button>
        <button class="export-button" @click="downloadExport('pdf')">Export PDF</button>
        <button class="export-button" @click="downloadExport('html')">Export HTML</button>
      </div>

      <div class="section-navigation">
        <button
          class="nav-button prev-button"
          :disabled="section.order === 1"
          @click="goToPrev"
        >
          Previous
        </button>
        <button
          class="nav-button next-button"
          :disabled="section.order === section.total_sections"
          @click="goToNext"
        >
          Next
        </button>
      </div>
    </div>

    <div v-if="showFeedback" class="modal-overlay" @click.self="closeFeedback">
      <div class="modal">
        <div class="modal-header">
          <h2>Give Feedback</h2>
          <button class="close-button" @click="closeFeedback">x</button>
        </div>

        <div v-if="feedbackError" class="error-message">
          {{ feedbackError }}
        </div>

        <div class="form-group">
          <textarea
            v-model="feedbackText"
            class="feedback-textarea"
            placeholder="Enter your feedback..."
            rows="6"
            maxlength="2000"
          ></textarea>
          <div class="char-counter">{{ feedbackText.length }} / 2000</div>
        </div>

        <div class="modal-actions">
          <button class="cancel-button" @click="closeFeedback">Cancel</button>
          <button
            class="submit-button"
            :disabled="isSubmittingFeedback"
            @click="submitFeedback"
          >
            {{ isSubmittingFeedback ? 'Submitting...' : 'Submit' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.section-reader {
  padding: 2rem;
  max-width: 900px;
  margin: 0 auto;
}

.loading,
.error-message {
  text-align: center;
  padding: 2rem;
}

.error-message {
  color: #d32f2f;
  background-color: #ffebee;
  border-radius: 4px;
  border-left: 4px solid #d32f2f;
  padding: 1rem;
  margin-bottom: 1rem;
}

.success-message {
  color: #2e7d32;
  background-color: #e8f5e9;
  border-radius: 4px;
  border-left: 4px solid #2e7d32;
  padding: 1rem;
  margin-bottom: 1rem;
}

.section-header {
  margin-bottom: 1.5rem;
}

.section-header h1 {
  font-size: 2rem;
  color: #333;
  margin: 0;
}

.section-body {
  line-height: 1.7;
  color: #444;
  margin-bottom: 2rem;
}

.section-actions {
  display: flex;
  gap: 1rem;
  margin-bottom: 2rem;
}

.feedback-button,
.export-button {
  padding: 0.5rem 1rem;
  background-color: #1976d2;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 0.9rem;
}

.feedback-button:hover,
.export-button:hover {
  background-color: #1565c0;
}

.section-navigation {
  display: flex;
  gap: 1rem;
  justify-content: space-between;
  margin-top: 2rem;
}

.nav-button {
  padding: 0.75rem 1.5rem;
  background-color: #1976d2;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 1rem;
}

.nav-button:disabled {
  background-color: #ccc;
  cursor: not-allowed;
}

.nav-button:not(:disabled):hover {
  background-color: #1565c0;
}

.modal-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background-color: rgba(0, 0, 0, 0.5);
  display: flex;
  justify-content: center;
  align-items: center;
  z-index: 1000;
}

.modal {
  background: white;
  border-radius: 8px;
  padding: 2rem;
  max-width: 500px;
  width: 90%;
  box-shadow: 0 4px 20px rgba(0, 0, 0, 0.2);
}

.modal-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 1.5rem;
}

.modal-header h2 {
  margin: 0;
  font-size: 1.5rem;
  color: #333;
}

.close-button {
  background: none;
  border: none;
  font-size: 1.5rem;
  cursor: pointer;
  color: #999;
  padding: 0;
}

.close-button:hover {
  color: #333;
}

.form-group {
  margin-bottom: 1rem;
}

.feedback-textarea {
  width: 100%;
  padding: 0.75rem;
  border: 1px solid #ddd;
  border-radius: 4px;
  font-size: 1rem;
  font-family: inherit;
  resize: vertical;
  box-sizing: border-box;
}

.char-counter {
  text-align: right;
  font-size: 0.8rem;
  color: #999;
  margin-top: 0.25rem;
}

.modal-actions {
  display: flex;
  gap: 1rem;
  justify-content: flex-end;
}

.cancel-button {
  padding: 0.75rem 1.5rem;
  background-color: #f5f5f5;
  color: #333;
  border: 1px solid #ddd;
  border-radius: 4px;
  cursor: pointer;
  font-size: 1rem;
}

.cancel-button:hover {
  background-color: #eeeeee;
}

.submit-button {
  padding: 0.75rem 1.5rem;
  background-color: #1976d2;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 1rem;
}

.submit-button:disabled {
  background-color: #ccc;
  cursor: not-allowed;
}

.submit-button:not(:disabled):hover {
  background-color: #1565c0;
}
</style>
