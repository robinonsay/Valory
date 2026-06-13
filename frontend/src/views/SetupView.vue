<script setup lang="ts">
// @{"req": ["REQ-FESETUP-004", "REQ-FESETUP-005", "REQ-FESETUP-006", "REQ-FESETUP-007", "REQ-SYS-071"]}
import { ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useSetupStore } from '@/stores/setup'
import { post, ApiError } from '@/api/client'

interface SetupRequest {
  username: string
  email?: string
  password: string
}

interface SetupResponse {
  username: string
  role: string
}

const router = useRouter()
const setup = useSetupStore()

// @{"req": ["REQ-FESETUP-004"]}
const step = ref<'welcome' | 'form' | 'success'>('welcome')

// @{"req": ["REQ-FESETUP-005"]}
const username = ref('')
const email = ref('')
const password = ref('')
const confirmPassword = ref('')
const validationErrors = ref<Record<string, string>>({})
const serverError = ref<string | null>(null)
const submitting = ref(false)

// @{"req": ["REQ-FESETUP-006"]}
// validate runs entirely client-side before any network call.
function validate(): boolean {
  validationErrors.value = {}
  if (username.value.trim() === '') {
    validationErrors.value.username = 'Username is required.'
  }
  if (password.value.length < 8) {
    validationErrors.value.password = 'Password must be at least 8 characters.'
  }
  if (password.value !== confirmPassword.value) {
    validationErrors.value.confirmPassword = 'Passwords do not match.'
  }
  return Object.keys(validationErrors.value).length === 0
}

// @{"req": ["REQ-FESETUP-005", "REQ-FESETUP-006", "REQ-FESETUP-007"]}
async function submit(): Promise<void> {
  if (!validate()) return
  submitting.value = true
  serverError.value = null
  try {
    const payload: SetupRequest = { username: username.value.trim(), password: password.value }
    if (email.value.trim() !== '') payload.email = email.value.trim()
    await post<SetupResponse>('/api/v1/setup', payload)
    setup.markComplete()
    step.value = 'success'
  } catch (err) {
    if (err instanceof ApiError) {
      if (err.status === 409) {
        // already_configured — mark complete and route to login
        setup.markComplete()
        serverError.value = 'An administrator account already exists. Please log in.'
        await router.push({ name: 'login' })
      } else if (err.status === 400) {
        serverError.value = 'Invalid input. Please check your entries and try again.'
      } else {
        serverError.value = 'An unexpected error occurred. Please try again.'
      }
    } else {
      serverError.value = 'Network error. Please check your connection.'
    }
  } finally {
    submitting.value = false
  }
}

// @{"req": ["REQ-FESETUP-007"]}
// Short delay so the operator sees the confirmation message before redirect.
watch(step, (val) => {
  if (val === 'success') {
    setTimeout(() => router.push({ name: 'login' }), 1500)
  }
})
</script>

<template>
  <div class="setup-container">
    <!-- Step 1: Welcome (REQ-FESETUP-004) -->
    <div v-if="step === 'welcome'" class="setup-card">
      <div class="logo-section">
        <img src="/valory.svg" alt="Valory" class="setup-logo" />
        <h1 class="logo-title">Valory</h1>
      </div>
      <h2 class="step-heading">Welcome to Valory</h2>
      <p class="step-description">
        No administrator account exists yet. This wizard creates the first administrator account.
        You will use these credentials to log in and manage the platform.
      </p>
      <button type="button" @click="step = 'form'">Get Started</button>
    </div>

    <!-- Step 2: Form (REQ-FESETUP-005, REQ-FESETUP-006) -->
    <form v-else-if="step === 'form'" class="setup-card" @submit.prevent="submit">
      <div class="logo-section">
        <img src="/valory.svg" alt="Valory" class="setup-logo" />
        <h1 class="logo-title">Valory</h1>
      </div>
      <h2 class="step-heading">Create Administrator Account</h2>

      <div v-if="serverError" class="error-message">
        {{ serverError }}
      </div>

      <div class="form-group">
        <label for="setup-username">Username</label>
        <input
          id="setup-username"
          v-model="username"
          type="text"
          autocomplete="username"
          required
        />
        <span v-if="validationErrors.username" class="field-error">
          {{ validationErrors.username }}
        </span>
      </div>

      <div class="form-group">
        <label for="setup-email">Email <span class="optional">(optional)</span></label>
        <input
          id="setup-email"
          v-model="email"
          type="email"
          autocomplete="email"
        />
      </div>

      <div class="form-group">
        <label for="setup-password">Password</label>
        <input
          id="setup-password"
          v-model="password"
          type="password"
          autocomplete="new-password"
          required
        />
        <span v-if="validationErrors.password" class="field-error">
          {{ validationErrors.password }}
        </span>
      </div>

      <div class="form-group">
        <label for="setup-confirm-password">Confirm Password</label>
        <input
          id="setup-confirm-password"
          v-model="confirmPassword"
          type="password"
          autocomplete="new-password"
          required
        />
        <span v-if="validationErrors.confirmPassword" class="field-error">
          {{ validationErrors.confirmPassword }}
        </span>
      </div>

      <button type="submit" :disabled="submitting">
        {{ submitting ? 'Creating account...' : 'Create Account' }}
      </button>
    </form>

    <!-- Step 3: Success (REQ-FESETUP-007) -->
    <div v-else-if="step === 'success'" class="setup-card">
      <div class="logo-section">
        <img src="/valory.svg" alt="Valory" class="setup-logo" />
        <h1 class="logo-title">Valory</h1>
      </div>
      <h2 class="step-heading">Setup Complete</h2>
      <p class="step-description">
        Your administrator account has been created. You can now log in.
      </p>
      <p class="redirect-notice">Redirecting to login...</p>
    </div>
  </div>
</template>

<style scoped>
.setup-container {
  display: flex;
  justify-content: center;
  align-items: center;
  min-height: 100vh;
  background-color: #f5f5f5;
}

.setup-card {
  background: white;
  padding: 2rem;
  border-radius: 8px;
  box-shadow: 0 2px 10px rgba(0, 0, 0, 0.1);
  width: 100%;
  max-width: 400px;
}

.logo-section {
  display: flex;
  flex-direction: column;
  align-items: center;
  margin-bottom: 2rem;
}

.setup-logo {
  height: 64px;
  width: 64px;
  margin-bottom: 1rem;
}

.logo-title {
  font-size: 1.75rem;
  font-weight: 700;
  color: #2c3e50;
  margin: 0;
  letter-spacing: 0.02em;
}

.step-heading {
  font-size: 1.25rem;
  font-weight: 600;
  color: #2c3e50;
  margin: 0 0 1rem 0;
  text-align: center;
}

.step-description {
  color: #555;
  line-height: 1.6;
  margin-bottom: 1.5rem;
  text-align: center;
}

.redirect-notice {
  color: #777;
  font-size: 0.875rem;
  text-align: center;
  margin-top: 1rem;
}

.error-message {
  color: #d32f2f;
  padding: 1rem;
  margin-bottom: 1rem;
  background-color: #ffebee;
  border-radius: 4px;
  border-left: 4px solid #d32f2f;
}

.form-group {
  margin-bottom: 1.5rem;
}

label {
  display: block;
  margin-bottom: 0.5rem;
  font-weight: 500;
  color: #333;
}

.optional {
  font-weight: 400;
  color: #777;
  font-size: 0.875em;
}

input[type='text'],
input[type='email'],
input[type='password'] {
  width: 100%;
  padding: 0.75rem;
  border: 1px solid #ddd;
  border-radius: 4px;
  font-size: 1rem;
  box-sizing: border-box;
}

input[type='text']:focus,
input[type='email']:focus,
input[type='password']:focus {
  outline: none;
  border-color: #1976d2;
  box-shadow: 0 0 0 3px rgba(25, 118, 210, 0.1);
}

.field-error {
  display: block;
  color: #d32f2f;
  font-size: 0.875rem;
  margin-top: 0.25rem;
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
