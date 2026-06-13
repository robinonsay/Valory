// @{"req": ["REQ-FEPROFILE-002"]}

<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { get, put, post, ApiError } from '@/api/client'

interface ProfileResponse {
  student_id: string
  summary: string
  source: string
  created_at: string
  updated_at: string
}

const router = useRouter()
const auth = useAuthStore()

const profile = ref<ProfileResponse | null>(null)
const isLoading = ref(false)
const isSaving = ref(false)
const isRerunning = ref(false)
const editedSummary = ref('')
const error = ref<string | null>(null)
const successMessage = ref<string | null>(null)
const hasChanges = computed(() => editedSummary.value !== (profile.value?.summary ?? ''))
const showConfirmDialog = ref(false)

// @{"req": ["REQ-FEPROFILE-002"]}
async function loadProfile(): Promise<void> {
  isLoading.value = true
  error.value = null
  try {
    const response = await get<ProfileResponse>('/api/v1/profile')
    profile.value = response
    editedSummary.value = response.summary
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      // No profile yet - that's OK
      profile.value = null
      editedSummary.value = ''
    } else {
      error.value = 'Failed to load profile. Please try again.'
    }
  } finally {
    isLoading.value = false
  }
}

// @{"req": ["REQ-FEPROFILE-002"]}
async function saveProfile(): Promise<void> {
  if (!editedSummary.value.trim()) {
    error.value = 'Profile summary cannot be empty'
    return
  }

  isSaving.value = true
  error.value = null
  successMessage.value = null

  try {
    const response = await put<ProfileResponse>('/api/v1/profile', {
      summary: editedSummary.value.trim()
    })
    profile.value = response
    successMessage.value = 'Profile updated successfully!'
    setTimeout(() => {
      successMessage.value = null
    }, 3000)
  } catch (err) {
    if (err instanceof ApiError) {
      if (err.status === 400) {
        error.value = 'Profile summary must be 1-2000 characters'
      } else {
        error.value = 'Failed to save profile. Please try again.'
      }
    } else {
      error.value = 'An error occurred. Please try again.'
    }
  } finally {
    isSaving.value = false
  }
}

// @{"req": ["REQ-FEPROFILE-002"]}
async function rerunOnboarding(): Promise<void> {
  showConfirmDialog.value = false
  isRerunning.value = true
  error.value = null

  try {
    await post('/api/v1/profile/onboarding/start', {})
    router.push('/onboarding')
  } catch (err) {
    error.value = 'Failed to restart onboarding. Please try again.'
    isRerunning.value = false
  }
}

function cancelEdit(): void {
  editedSummary.value = profile.value?.summary ?? ''
  error.value = null
}

onMounted(() => {
  loadProfile()
})
</script>

<template>
  <div class="profile-container">
    <div class="profile-header">
      <h1>Learning Profile</h1>
      <p>
        Your learning profile helps us personalize your courses. You can edit it here
        or re-run the onboarding to update your preferences.
      </p>
    </div>

    <div v-if="error" class="error-message">
      {{ error }}
    </div>

    <div v-if="successMessage" class="success-message">
      {{ successMessage }}
    </div>

    <div v-if="isLoading" class="loading-state">
      <p>Loading your profile...</p>
    </div>

    <div v-else class="profile-content">
      <div v-if="profile === null" class="no-profile-state">
        <p>You haven't completed onboarding yet.</p>
        <button
          class="primary-button"
          @click="showConfirmDialog = true"
          :disabled="isRerunning"
        >
          {{ isRerunning ? 'Starting...' : 'Start Onboarding' }}
        </button>
      </div>

      <div v-else class="profile-form">
        <div class="form-group">
          <label for="summary">Profile Summary</label>
          <p class="help-text">
            <strong>Source:</strong> {{ profile.source }}
          </p>
          <textarea
            id="summary"
            v-model="editedSummary"
            :disabled="isSaving"
            class="profile-textarea"
            placeholder="Describe your learning style, preferences, and goals..."
            maxlength="2000"
          />
          <p class="char-count">
            {{ editedSummary.length }} / 2000 characters
          </p>
        </div>

        <div class="button-group">
          <button
            class="primary-button"
            @click="saveProfile"
            :disabled="!hasChanges || isSaving || editedSummary.trim() === ''"
          >
            {{ isSaving ? 'Saving...' : 'Save Changes' }}
          </button>
          <button
            type="button"
            class="secondary-button"
            @click="cancelEdit"
            :disabled="isSaving"
          >
            Cancel
          </button>
          <button
            type="button"
            class="secondary-button"
            @click="showConfirmDialog = true"
            :disabled="isSaving || isRerunning"
          >
            {{ isRerunning ? 'Starting...' : 'Re-run Onboarding' }}
          </button>
        </div>
      </div>
    </div>

    <!-- Confirm Dialog for Re-run Onboarding -->
    <div v-if="showConfirmDialog" class="modal-overlay" @click.self="showConfirmDialog = false">
      <div class="modal-content">
        <h2>Re-run Onboarding</h2>
        <p>
          Re-running onboarding will start a new learning profile questionnaire.
          Your current profile will be replaced with the new answers.
        </p>
        <div class="modal-buttons">
          <button class="primary-button" @click="rerunOnboarding">
            Continue
          </button>
          <button class="secondary-button" @click="showConfirmDialog = false">
            Cancel
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.profile-container {
  display: flex;
  flex-direction: column;
  max-width: 800px;
  margin: 0 auto;
  padding: 2rem;
  background-color: white;
  border-radius: 8px;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
}

.profile-header {
  margin-bottom: 2rem;
  border-bottom: 1px solid #eee;
  padding-bottom: 1.5rem;
}

.profile-header h1 {
  margin: 0 0 0.5rem;
  font-size: 1.75rem;
  color: #333;
}

.profile-header p {
  margin: 0;
  color: #666;
  line-height: 1.5;
}

.error-message {
  padding: 1rem;
  background-color: #fee;
  color: #c33;
  border-left: 4px solid #c33;
  margin-bottom: 1.5rem;
  border-radius: 4px;
}

.success-message {
  padding: 1rem;
  background-color: #efe;
  color: #3c3;
  border-left: 4px solid #3c3;
  margin-bottom: 1.5rem;
  border-radius: 4px;
}

.loading-state {
  text-align: center;
  padding: 2rem;
  color: #666;
}

.profile-content {
  flex: 1;
}

.no-profile-state {
  text-align: center;
  padding: 2rem;
  background-color: #f9f9f9;
  border-radius: 4px;
}

.no-profile-state p {
  margin: 0 0 1.5rem;
  color: #666;
  font-size: 1rem;
}

.profile-form {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.form-group {
  display: flex;
  flex-direction: column;
}

.form-group label {
  font-weight: 600;
  margin-bottom: 0.5rem;
  color: #333;
}

.help-text {
  margin: 0.25rem 0 0.75rem;
  font-size: 0.875rem;
  color: #999;
}

.profile-textarea {
  padding: 0.75rem;
  border: 1px solid #ddd;
  border-radius: 4px;
  font-size: 1rem;
  font-family: inherit;
  resize: vertical;
  min-height: 150px;
  line-height: 1.5;
}

.profile-textarea:disabled {
  background-color: #f9f9f9;
  color: #999;
}

.char-count {
  margin: 0.5rem 0 0;
  font-size: 0.875rem;
  color: #999;
}

.button-group {
  display: flex;
  gap: 0.75rem;
  flex-wrap: wrap;
  align-items: center;
}

.primary-button,
.secondary-button {
  padding: 0.75rem 1.5rem;
  border: none;
  border-radius: 4px;
  font-size: 1rem;
  cursor: pointer;
  transition: background-color 0.2s ease;
  font-weight: 500;
}

.primary-button {
  background-color: #007bff;
  color: white;
}

.primary-button:hover:not(:disabled) {
  background-color: #0056b3;
}

.primary-button:disabled {
  background-color: #ccc;
  cursor: not-allowed;
}

.secondary-button {
  background-color: transparent;
  color: #666;
  border: 1px solid #ddd;
}

.secondary-button:hover:not(:disabled) {
  background-color: #f9f9f9;
}

.secondary-button:disabled {
  color: #ccc;
  border-color: #ddd;
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
  background-color: white;
  padding: 2rem;
  border-radius: 8px;
  box-shadow: 0 4px 20px rgba(0, 0, 0, 0.15);
  max-width: 400px;
  margin: 0 1rem;
}

.modal-content h2 {
  margin: 0 0 1rem;
  font-size: 1.5rem;
  color: #333;
}

.modal-content p {
  margin: 0 0 1.5rem;
  color: #666;
  line-height: 1.5;
}

.modal-buttons {
  display: flex;
  gap: 0.75rem;
  justify-content: flex-end;
}
</style>
