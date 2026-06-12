// @{"req": ["REQ-FECONTENT-020", "REQ-FECONTENT-021", "REQ-FECONTENT-022", "REQ-FECONTENT-111", "REQ-FECONTENT-112", "REQ-FECONTENT-113", "REQ-FECONTENT-114", "REQ-FECONTENT-120", "REQ-FECONTENT-125", "REQ-FECONTENT-130"]}

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { get, ApiError } from '@/api/client'
import {
  ACCEPTED_EXTENSIONS,
  validateFile
} from './homeworkSubmission'

interface HomeworkDetail {
  id: string
  title: string
  description: string
  due_date: string
}

interface Submission {
  id: string
  format: string
  file_size_bytes: number
  submitted_at: string
  grading_status: string
}

const route = useRoute()

const courseId = route.params.id as string
const hwId = route.params.hwId as string

const homework = ref<HomeworkDetail | null>(null)
const isLoadingHomework = ref(false)
const homeworkError = ref<string | null>(null)

const latestSubmission = ref<Submission | null>(null)
const isLoadingSubmission = ref(false)
const noSubmission = ref(false)

const selectedFile = ref<File | null>(null)
const fileError = ref<string | null>(null)
const isUploading = ref(false)
const uploadProgress = ref(0)
const uploadError = ref<string | null>(null)
const successMessage = ref<string | null>(null)

// @{"req": ["REQ-FECONTENT-020"]}
async function fetchHomework(): Promise<void> {
  isLoadingHomework.value = true
  homeworkError.value = null
  try {
    const data = await get<HomeworkDetail>(
      `/api/v1/courses/${courseId}/homework/${hwId}`
    )
    homework.value = data
  } catch (err) {
    if (err instanceof ApiError) {
      homeworkError.value = 'Failed to load homework details. Please try again.'
    } else {
      homeworkError.value = 'Failed to load homework details. Please try again.'
    }
  } finally {
    isLoadingHomework.value = false
  }
}

// @{"req": ["REQ-FECONTENT-020"]}
async function fetchLatestSubmission(): Promise<void> {
  isLoadingSubmission.value = true
  noSubmission.value = false
  try {
    const data = await get<Submission>(
      `/api/v1/courses/${courseId}/homework/${hwId}/submissions/latest`
    )
    latestSubmission.value = data
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      noSubmission.value = true
    }
  } finally {
    isLoadingSubmission.value = false
  }
}

onMounted(() => {
  fetchHomework()
  fetchLatestSubmission()
})

// @{"req": ["REQ-FECONTENT-021", "REQ-FECONTENT-022", "REQ-FECONTENT-112", "REQ-FECONTENT-113", "REQ-FECONTENT-114"]}
function onFileChange(event: Event): void {
  const input = event.target as HTMLInputElement
  fileError.value = null
  uploadError.value = null
  successMessage.value = null
  if (!input.files || input.files.length === 0) {
    selectedFile.value = null
    return
  }
  const file = input.files[0]
  const err = validateFile(file)
  if (err) {
    fileError.value = err
    selectedFile.value = null
    return
  }
  selectedFile.value = file
}

// @{"req": ["REQ-FECONTENT-020", "REQ-FECONTENT-120", "REQ-FECONTENT-125", "REQ-FECONTENT-130"]}
function uploadFile(file: File): void {
  const formData = new FormData()
  formData.append('file', file)

  const xhr = new XMLHttpRequest()

  xhr.upload.addEventListener('progress', (e) => {
    if (e.lengthComputable) {
      uploadProgress.value = Math.round((e.loaded / e.total) * 100)
    }
  })

  xhr.addEventListener('load', () => {
    if (xhr.status === 201) {
      uploadProgress.value = 100
      successMessage.value = 'Submission uploaded successfully.'
      try {
        const parsed: Submission = JSON.parse(xhr.responseText)
        latestSubmission.value = parsed
        noSubmission.value = false
      } catch {
        // Response body not parseable; success message is still shown
      }
    } else if (xhr.status === 409) {
      uploadError.value = 'You have already submitted for this homework.'
    } else if (xhr.status === 413) {
      uploadError.value = 'File too large. Maximum size is 10 MB.'
    } else if (xhr.status === 422) {
      try {
        const body = JSON.parse(xhr.responseText)
        if (body?.code === 'UNSUPPORTED_FORMAT') {
          uploadError.value = `Accepted formats: ${ACCEPTED_EXTENSIONS.join(', ')}`
        } else if (body?.code === 'INVALID_CONTENT') {
          uploadError.value = 'File content does not match the detected format.'
        } else {
          uploadError.value = 'Submission failed. Please try again.'
        }
      } catch {
        uploadError.value = 'Submission failed. Please try again.'
      }
    } else {
      uploadError.value = 'Upload failed. Please try again.'
    }
    isUploading.value = false
  })

  xhr.addEventListener('error', () => {
    uploadError.value = 'Upload failed. Please try again.'
    isUploading.value = false
  })

  xhr.open('POST', `/api/v1/courses/${courseId}/homework/${hwId}/submissions`)
  // withCredentials ensures the __Host-session cookie is sent cross-origin and
  // that the response Set-Cookie is accepted (same-origin safe by SameSite=Strict).
  xhr.withCredentials = true
  // Read the CSRF cookie fresh on every mutating request so a rotated value is always used
  const csrfToken = document.cookie
    .split(';')
    .map(c => c.trim())
    .find(c => c.startsWith('__Host-csrf='))
    ?.split('=')[1]
  if (csrfToken) {
    xhr.setRequestHeader('X-CSRF-Token', csrfToken)
  }

  isUploading.value = true
  uploadProgress.value = 0
  xhr.send(formData)
}

// @{"req": ["REQ-FECONTENT-022", "REQ-FECONTENT-112", "REQ-FECONTENT-113", "REQ-FECONTENT-114", "REQ-FECONTENT-120"]}
function onSubmit(): void {
  fileError.value = null
  uploadError.value = null
  successMessage.value = null

  if (!selectedFile.value) {
    fileError.value = 'Please select a file to upload.'
    return
  }

  // Client-side validation runs before any network call
  const err = validateFile(selectedFile.value)
  if (err) {
    fileError.value = err
    return
  }

  uploadFile(selectedFile.value)
}

function formatDate(dateStr: string): string {
  return new Date(dateStr).toLocaleString()
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(2)} MB`
}

</script>

<template>
  <div class="submission-container">
    <div v-if="isLoadingHomework" class="loading">
      <p>Loading homework details...</p>
    </div>

    <div v-else-if="homeworkError" class="error-message">
      {{ homeworkError }}
    </div>

    <div v-else-if="homework">
      <div class="homework-header">
        <h1 class="homework-title">{{ homework.title }}</h1>
        <p class="homework-description">{{ homework.description }}</p>
        <p class="homework-due-date">Due: {{ formatDate(homework.due_date) }}</p>
      </div>

      <div class="latest-submission-section">
        <h2>Latest Submission</h2>
        <div v-if="isLoadingSubmission" class="loading">
          <p>Loading submission...</p>
        </div>
        <div v-else-if="noSubmission" class="no-submission">
          <p>No prior submission.</p>
        </div>
        <div v-else-if="latestSubmission" class="submission-details">
          <p><strong>Format:</strong> {{ latestSubmission.format }}</p>
          <p><strong>File size:</strong> {{ formatBytes(latestSubmission.file_size_bytes) }}</p>
          <p><strong>Submitted at:</strong> {{ formatDate(latestSubmission.submitted_at) }}</p>
          <p><strong>Grading status:</strong> {{ latestSubmission.grading_status }}</p>
        </div>
      </div>

      <div class="upload-section">
        <h2>Upload Submission</h2>

        <div class="file-input-wrapper">
          <input
            type="file"
            @change="onFileChange"
            :disabled="isUploading"
          />
        </div>

        <p v-if="fileError" class="error-message inline-error">{{ fileError }}</p>

        <div v-if="isUploading" class="progress-wrapper">
          <div class="progress-bar">
            <div class="progress-fill" :style="{ width: uploadProgress + '%' }"></div>
          </div>
          <span class="progress-label">{{ uploadProgress }}%</span>
        </div>

        <p v-if="uploadError" class="error-message inline-error">{{ uploadError }}</p>
        <p v-if="successMessage" class="success-message">{{ successMessage }}</p>

        <button
          class="submit-button"
          :disabled="isUploading || !selectedFile"
          @click="onSubmit"
        >
          {{ isUploading ? 'Uploading...' : 'Upload Submission' }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.submission-container {
  padding: 2rem;
  max-width: 800px;
  margin: 0 auto;
}

.homework-header {
  margin-bottom: 2rem;
}

.homework-title {
  font-size: 1.8rem;
  margin: 0 0 0.5rem 0;
  color: #333;
}

.homework-description {
  color: #555;
  margin: 0.5rem 0;
}

.homework-due-date {
  color: #e53935;
  font-weight: 600;
  margin: 0.5rem 0;
}

.latest-submission-section,
.upload-section {
  background: #fafafa;
  border: 1px solid #ddd;
  border-radius: 4px;
  padding: 1.5rem;
  margin-bottom: 1.5rem;
}

.latest-submission-section h2,
.upload-section h2 {
  margin: 0 0 1rem 0;
  font-size: 1.2rem;
  color: #333;
}

.submission-details p {
  margin: 0.4rem 0;
  color: #555;
}

.no-submission {
  color: #888;
  font-style: italic;
}

.file-input-wrapper {
  margin-bottom: 1rem;
}

.loading {
  text-align: center;
  padding: 2rem;
  color: #666;
}

.error-message {
  color: #d32f2f;
  background-color: #ffebee;
  border-radius: 4px;
  border-left: 4px solid #d32f2f;
  padding: 1rem;
  margin-bottom: 1rem;
}

.inline-error {
  padding: 0.5rem 0.75rem;
  margin-bottom: 0.75rem;
}

.success-message {
  color: #2e7d32;
  background-color: #e8f5e9;
  border-radius: 4px;
  border-left: 4px solid #2e7d32;
  padding: 0.5rem 0.75rem;
  margin-bottom: 0.75rem;
}

.progress-wrapper {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  margin-bottom: 0.75rem;
}

.progress-bar {
  flex: 1;
  height: 8px;
  background: #e0e0e0;
  border-radius: 4px;
  overflow: hidden;
}

.progress-fill {
  height: 100%;
  background: #1976d2;
  border-radius: 4px;
  transition: width 0.1s ease;
}

.progress-label {
  font-size: 0.85rem;
  color: #555;
  min-width: 3rem;
  text-align: right;
}

.submit-button {
  padding: 0.75rem 1.5rem;
  background-color: #1976d2;
  color: white;
  border: none;
  border-radius: 4px;
  font-size: 1rem;
  cursor: pointer;
  transition: background-color 0.2s;
}

.submit-button:disabled {
  background-color: #bdbdbd;
  cursor: not-allowed;
}

.submit-button:not(:disabled):hover {
  background-color: #1565c0;
}
</style>
