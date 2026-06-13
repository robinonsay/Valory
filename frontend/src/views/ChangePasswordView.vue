// @{"req": ["REQ-FEUSER-001", "REQ-FEUSER-002", "REQ-FEUSER-003", "REQ-FEUSER-004"]}

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { post, ApiError } from '@/api/client'
import { validatePasswordStrength } from '@/utils/passwordValidation'

const router = useRouter()
const auth = useAuthStore()

const currentPassword = ref('')
const newPassword = ref('')
const confirmPassword = ref('')
const error = ref<string | null>(null)
const isLoading = ref(false)
const isForced = ref(auth.mustChangePassword)

const passwordStrengthError = computed(() => {
  if (!newPassword.value) return null
  const validation = validatePasswordStrength(newPassword.value)
  return validation.isValid ? null : validation.error
})

const passwordsMatch = computed(() => {
  if (!newPassword.value || !confirmPassword.value) return true
  return newPassword.value === confirmPassword.value
})

const canSubmit = computed(() => {
  return (
    currentPassword.value.trim() !== '' &&
    newPassword.value.trim() !== '' &&
    confirmPassword.value.trim() !== '' &&
    !passwordStrengthError.value &&
    passwordsMatch &&
    !isLoading.value
  )
})

async function handleSubmit() {
  if (!canSubmit.value) return

  error.value = null
  isLoading.value = true

  try {
    await post<void>('/api/v1/users/me/password', {
      current_password: currentPassword.value,
      new_password: newPassword.value
    })

    await auth.refreshSession()

    if (isForced.value) {
      router.push('/courses')
    } else {
      router.back()
    }
  } catch (err) {
    if (err instanceof ApiError) {
      if (err.status === 401 || err.status === 403) {
        error.value = 'Current password is incorrect'
      } else if (err.status === 400) {
        error.value = 'Password does not meet security requirements'
      } else {
        error.value = 'Failed to change password. Please try again.'
      }
    } else {
      error.value = 'Failed to change password. Please try again.'
    }
  } finally {
    isLoading.value = false
  }
}
</script>

<template>
  <div class="change-password-container">
    <div class="form-wrapper">
      <h1>Change Password</h1>
      <p v-if="isForced" class="forced-notice">
        You must change your password before continuing.
      </p>

      <form @submit.prevent="handleSubmit">
        <div v-if="error" class="error-message">
          {{ error }}
        </div>

        <div class="form-group">
          <label for="current-password">Current Password</label>
          <input
            id="current-password"
            v-model="currentPassword"
            type="password"
            autocomplete="current-password"
            placeholder="Enter your current password"
            required
          />
        </div>

        <div class="form-group">
          <label for="new-password">New Password</label>
          <input
            id="new-password"
            v-model="newPassword"
            type="password"
            autocomplete="new-password"
            placeholder="Enter your new password"
            required
          />
          <div v-if="passwordStrengthError" class="validation-error">
            {{ passwordStrengthError }}
          </div>
        </div>

        <div class="form-group">
          <label for="confirm-password">Confirm New Password</label>
          <input
            id="confirm-password"
            v-model="confirmPassword"
            type="password"
            autocomplete="new-password"
            placeholder="Confirm your new password"
            required
          />
          <div v-if="confirmPassword && !passwordsMatch" class="validation-error">
            Passwords do not match
          </div>
        </div>

        <button type="submit" :disabled="!canSubmit">
          {{ isLoading ? 'Changing password...' : 'Change Password' }}
        </button>
      </form>
    </div>
  </div>
</template>

<style scoped>
.change-password-container {
  display: flex;
  justify-content: center;
  align-items: center;
  min-height: 100vh;
  background-color: #f5f5f5;
}

.form-wrapper {
  background: white;
  padding: 2rem;
  border-radius: 8px;
  box-shadow: 0 2px 10px rgba(0, 0, 0, 0.1);
  width: 100%;
  max-width: 400px;
}

h1 {
  font-size: 1.5rem;
  font-weight: 700;
  color: #2c3e50;
  margin-bottom: 0.5rem;
  text-align: center;
}

.forced-notice {
  color: #d32f2f;
  font-size: 0.95rem;
  text-align: center;
  margin-bottom: 1.5rem;
  padding: 0.75rem;
  background-color: #ffebee;
  border-radius: 4px;
  border-left: 4px solid #d32f2f;
}

form {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.error-message {
  color: #d32f2f;
  padding: 0.75rem;
  background-color: #ffebee;
  border-radius: 4px;
  border-left: 4px solid #d32f2f;
  font-size: 0.9rem;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

label {
  display: block;
  font-weight: 500;
  color: #333;
  font-size: 0.95rem;
}

input[type='password'] {
  width: 100%;
  padding: 0.75rem;
  border: 1px solid #ddd;
  border-radius: 4px;
  font-size: 1rem;
  box-sizing: border-box;
}

input[type='password']:focus {
  outline: none;
  border-color: #1976d2;
  box-shadow: 0 0 0 3px rgba(25, 118, 210, 0.1);
}

.validation-error {
  color: #d32f2f;
  font-size: 0.85rem;
  padding: 0.25rem 0;
}

button {
  width: 100%;
  padding: 0.75rem;
  background-color: #1976d2;
  color: white;
  border: none;
  border-radius: 4px;
  font-size: 1rem;
  font-weight: 500;
  cursor: pointer;
  transition: background-color 0.2s;
}

button:hover:not(:disabled) {
  background-color: #1565c0;
}

button:disabled {
  background-color: #ccc;
  cursor: not-allowed;
}
</style>
