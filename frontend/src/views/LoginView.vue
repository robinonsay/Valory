// @{"req": ["REQ-FEAUTH-010", "REQ-FEAUTH-011", "REQ-FEAUTH-012", "REQ-FEAUTH-100", "REQ-FEAUTH-101", "REQ-FEAUTH-102", "REQ-FEAUTH-110", "REQ-FEAUTH-115", "REQ-FEAUTH-120", "REQ-FEAUTH-171", "REQ-FEAUTH-200"]}

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { post, ApiError } from '@/api/client'

const router = useRouter()
const auth = useAuthStore()

const username = ref('')
const password = ref('')
const error = ref('')
const isLoading = ref(false)

async function handleSubmit() {
  error.value = ''
  isLoading.value = true

  try {
    // The API authenticates by username (POST /api/v1/auth/login expects
    // {username, password}). The server sets the __Host-session cookie on
    // success; we do not read the token from the body (REQ-FEAUTH-171).
    // When 2FA is enabled, the response is 202 with a pending_token.
    const response = await post<{
      two_factor_required?: boolean
      pending_token?: string
      expires_at?: string
    }>('/api/v1/auth/login', {
      username: username.value,
      password: password.value
    })

    // Handle 2FA flow: 202 response with pending token
    if (response.two_factor_required && response.pending_token && response.expires_at) {
      auth.setPendingTwoFactor(response.pending_token, response.expires_at)
      router.push('/login/verify')
      return
    }

    // Normal flow: 200 with session cookie set
    await auth.login()

    if (auth.mustChangePassword) {
      router.push('/change-password')
    } else if (auth.isAdmin) {
      router.push('/admin/users')
    } else {
      router.push('/courses')
    }
  } catch (err) {
    if (err instanceof ApiError) {
      if (err.status === 401) {
        error.value = 'Invalid credentials'
      } else if (err.status === 429) {
        error.value = 'Too many attempts. Please try again later.'
      } else {
        error.value = 'An error occurred. Please try again.'
      }
    } else {
      error.value = 'An error occurred. Please try again.'
    }
  } finally {
    isLoading.value = false
  }
}

function clearError() {
  error.value = ''
}
</script>

<template>
  <div class="login-container">
    <form @submit.prevent="handleSubmit">
      <div class="logo-section">
        <img src="/valory.svg" alt="Valory" class="login-logo" />
        <h1 class="logo-title">Valory</h1>
      </div>

      <div v-if="error" class="error-message">
        {{ error }}
      </div>

      <div class="form-group">
        <label for="username">Username</label>
        <input
          id="username"
          v-model="username"
          type="text"
          autocomplete="username"
          required
          @input="clearError"
        />
      </div>

      <div class="form-group">
        <label for="password">Password</label>
        <input
          id="password"
          v-model="password"
          type="password"
          autocomplete="current-password"
          required
          @input="clearError"
        />
      </div>

      <button type="submit" :disabled="isLoading">
        {{ isLoading ? 'Signing in...' : 'Sign in' }}
      </button>
    </form>
  </div>
</template>

<style scoped>
.login-container {
  display: flex;
  justify-content: center;
  align-items: center;
  min-height: 100vh;
  background-color: #f5f5f5;
}

form {
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

.login-logo {
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

input[type='text'],
input[type='password'] {
  width: 100%;
  padding: 0.75rem;
  border: 1px solid #ddd;
  border-radius: 4px;
  font-size: 1rem;
  box-sizing: border-box;
}

input[type='text']:focus,
input[type='password']:focus {
  outline: none;
  border-color: #1976d2;
  box-shadow: 0 0 0 3px rgba(25, 118, 210, 0.1);
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
