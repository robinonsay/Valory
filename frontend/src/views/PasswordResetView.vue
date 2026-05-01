// @{"req": ["REQ-FEAUTH-020", "REQ-FEAUTH-021", "REQ-FEAUTH-022", "REQ-FEAUTH-023", "REQ-FEAUTH-130", "REQ-FEAUTH-131", "REQ-FEAUTH-132", "REQ-FEAUTH-133", "REQ-FEAUTH-134"]}

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { post, ApiError } from '@/api/client'

const route = useRoute()
const router = useRouter()

const isLoading = ref(false)
const errorMessage = ref<string | null>(null)
const successMessage = ref<string | null>(null)

const email = ref('')
const password = ref('')

const hasToken = computed(() => route.query.token !== undefined && route.query.token !== '')
const token = computed(() => route.query.token as string)

const isRequestMode = computed(() => !hasToken.value)
const isConfirmMode = computed(() => hasToken.value)

const canSubmitRequest = computed(() => email.value.trim() !== '' && !isLoading.value)
const canSubmitConfirm = computed(() => token.value && password.value.trim() !== '' && !isLoading.value)

async function submitRequest(): Promise<void> {
  if (!canSubmitRequest.value) return

  isLoading.value = true
  errorMessage.value = null
  successMessage.value = null

  try {
    await post<void>('/api/v1/auth/reset-password', { email: email.value })
    successMessage.value = 'If an account with that email exists, you will receive a reset link shortly.'
  } catch (err) {
    if (err instanceof ApiError) {
      if (err.status === 429) {
        errorMessage.value = 'Too many requests. Please wait before trying again.'
      } else {
        successMessage.value = 'If an account with that email exists, you will receive a reset link shortly.'
      }
    } else {
      errorMessage.value = 'Failed to reset password. Please try again.'
    }
  } finally {
    isLoading.value = false
  }
}

async function submitConfirm(): Promise<void> {
  if (!canSubmitConfirm.value) return

  isLoading.value = true
  errorMessage.value = null
  successMessage.value = null

  try {
    await post<void>('/api/v1/auth/reset-password/confirm', {
      token: token.value,
      password: password.value
    })
    await router.push('/login?reset=success')
  } catch (err) {
    if (err instanceof ApiError) {
      if (err.status === 400) {
        errorMessage.value = 'Invalid or expired reset link.'
      } else {
        errorMessage.value = 'Failed to reset password. Please try again.'
      }
    } else {
      errorMessage.value = 'Failed to reset password. Please try again.'
    }
  } finally {
    isLoading.value = false
  }
}
</script>

<template>
  <div class="password-reset-view">
    <h1>Reset Password</h1>

    <!-- Request Mode: Email field -->
    <div v-if="isRequestMode" class="form-group">
      <label for="email">Email Address</label>
      <input
        id="email"
        v-model="email"
        type="email"
        placeholder="Enter your email"
        @keyup.enter="canSubmitRequest ? submitRequest() : null"
      />
      <button
        :disabled="!canSubmitRequest"
        @click="submitRequest"
      >
        {{ isLoading ? 'Sending...' : 'Send reset email' }}
      </button>
    </div>

    <!-- Confirm Mode: Token and Password fields -->
    <div v-if="isConfirmMode" class="form-group">
      <label for="token">Reset Token</label>
      <input
        id="token"
        :value="token"
        type="text"
        disabled
      />

      <label for="password">New Password</label>
      <input
        id="password"
        v-model="password"
        type="password"
        placeholder="Enter new password"
        @keyup.enter="canSubmitConfirm ? submitConfirm() : null"
      />
      <button
        :disabled="!canSubmitConfirm"
        @click="submitConfirm"
      >
        {{ isLoading ? 'Setting password...' : 'Set new password' }}
      </button>
    </div>

    <!-- Error message -->
    <div v-if="errorMessage" class="error-message">
      {{ errorMessage }}
    </div>

    <!-- Success message -->
    <div v-if="successMessage" class="success-message">
      {{ successMessage }}
    </div>
  </div>
</template>

<style scoped>
.password-reset-view {
  max-width: 400px;
  margin: 0 auto;
  padding: 2rem;
}

h1 {
  text-align: center;
  margin-bottom: 2rem;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

label {
  font-weight: bold;
  margin-bottom: 0.5rem;
}

input {
  padding: 0.5rem;
  border: 1px solid #ccc;
  border-radius: 4px;
  font-size: 1rem;
}

input:disabled {
  background-color: #f5f5f5;
  cursor: not-allowed;
}

button {
  padding: 0.75rem;
  background-color: #007bff;
  color: white;
  border: none;
  border-radius: 4px;
  font-size: 1rem;
  cursor: pointer;
  transition: background-color 0.3s;
}

button:hover:not(:disabled) {
  background-color: #0056b3;
}

button:disabled {
  background-color: #ccc;
  cursor: not-allowed;
}

.error-message {
  margin-top: 1rem;
  padding: 0.75rem;
  background-color: #f8d7da;
  color: #721c24;
  border: 1px solid #f5c6cb;
  border-radius: 4px;
}

.success-message {
  margin-top: 1rem;
  padding: 0.75rem;
  background-color: #d4edda;
  color: #155724;
  border: 1px solid #c3e6cb;
  border-radius: 4px;
}
</style>
