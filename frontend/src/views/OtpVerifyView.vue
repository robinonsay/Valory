// @{"req": ["REQ-FEAUTH-200"]}

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { ApiError } from '@/api/client'

const router = useRouter()
const auth = useAuthStore()

const otp = ref('')
const error = ref<string | null>(null)
const isLoading = ref(false)
const attemptsRemaining = ref<number | null>(null)
const isLocked = ref(false)
const resendCountdownSeconds = ref(0)
const resendMessage = ref<string | null>(null)
const countdownInterval = ref<ReturnType<typeof setInterval> | null>(null)

const canSubmit = computed(() => {
  return otp.value.length === 6 && !isLoading.value && !isLocked.value
})

const canResend = computed(() => {
  return resendCountdownSeconds.value === 0
})

onMounted(() => {
  if (!auth.pendingTwoFactor) {
    router.push('/login')
  }
})

onUnmounted(() => {
  if (countdownInterval.value !== null) {
    clearInterval(countdownInterval.value)
  }
})

function handleOtpInput() {
  otp.value = otp.value.replace(/[^0-9]/g, '').slice(0, 6)
  error.value = null
}

async function handleSubmit() {
  if (!canSubmit.value) return

  error.value = null
  isLoading.value = true
  attemptsRemaining.value = null

  try {
    await auth.verifyOtp(otp.value)

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
        const body = err.body as { error?: string; attempts_remaining?: number } | null
        if (body?.error === 'too_many_attempts') {
          isLocked.value = true
          error.value = 'Too many incorrect attempts. Please sign in again.'
        } else {
          error.value = 'Incorrect code.'
          if (body?.attempts_remaining !== undefined) {
            attemptsRemaining.value = body.attempts_remaining
            error.value += ` ${body.attempts_remaining} attempt(s) remaining.`
          }
        }
      } else if (err.status === 429) {
        const body = err.body as { error?: string; resend_available_at?: string } | null
        error.value = body?.error === 'resend_throttled'
          ? 'Too many resend requests. Please wait.'
          : 'Rate limited. Please try again later.'
      } else {
        error.value = 'Verification failed. Please try again.'
      }
    } else {
      error.value = 'Verification failed. Please try again.'
    }
  } finally {
    isLoading.value = false
  }
}

async function handleResend() {
  if (!canResend.value) return

  error.value = null
  resendMessage.value = null

  try {
    const response = await auth.resendOtp()

    if (response.resendAvailableAt) {
      const availableTime = new Date(response.resendAvailableAt).getTime()
      const now = Date.now()
      const secondsUntilAvailable = Math.max(0, Math.ceil((availableTime - now) / 1000))

      if (secondsUntilAvailable > 0) {
        resendCountdownSeconds.value = secondsUntilAvailable
        startCountdown()
      }
    }

    resendMessage.value = 'Code resent. Check your email.'
    setTimeout(() => {
      resendMessage.value = null
    }, 3000)
  } catch (err) {
    if (err instanceof ApiError) {
      const body = err.body as { error?: string; resend_available_at?: string } | null
      if (err.status === 429) {
        if (body?.error === 'resend_daily_cap_exceeded') {
          error.value = 'You have requested too many codes today. Contact your administrator.'
        } else if (body?.resend_available_at) {
          const availableTime = new Date(body.resend_available_at).getTime()
          const now = Date.now()
          const secondsUntilAvailable = Math.max(0, Math.ceil((availableTime - now) / 1000))
          if (secondsUntilAvailable > 0) {
            resendCountdownSeconds.value = secondsUntilAvailable
            startCountdown()
          }
          error.value = 'Resend is throttled. Please wait.'
        } else {
          error.value = 'Failed to resend code. Please try again.'
        }
      } else {
        error.value = 'Failed to resend code. Please try again.'
      }
    } else {
      error.value = 'Failed to resend code. Please try again.'
    }
  }
}

function startCountdown() {
  if (countdownInterval.value !== null) {
    clearInterval(countdownInterval.value)
  }

  countdownInterval.value = setInterval(() => {
    resendCountdownSeconds.value--
    if (resendCountdownSeconds.value <= 0) {
      clearInterval(countdownInterval.value as ReturnType<typeof setInterval>)
      countdownInterval.value = null
    }
  }, 1000)
}

function handleCancel() {
  auth.clearPendingTwoFactor()
  router.push('/login')
}

function handleRestart() {
  auth.clearPendingTwoFactor()
  router.push('/login')
}
</script>

<template>
  <div class="otp-verify-container">
    <div class="otp-form">
      <div class="otp-header">
        <h1>Check your email</h1>
        <p class="otp-subtitle">Enter the 6-digit code sent to your email address.</p>
      </div>

      <form @submit.prevent="handleSubmit">
        <div v-if="error" class="error-message" :class="{ 'locked-message': isLocked }">
          {{ error }}
        </div>

        <div v-if="resendMessage" class="success-message">
          {{ resendMessage }}
        </div>

        <div class="form-group">
          <input
            v-model="otp"
            type="text"
            inputmode="numeric"
            pattern="[0-9]{6}"
            maxlength="6"
            placeholder="000000"
            autocomplete="one-time-code"
            class="otp-input"
            :disabled="isLoading || isLocked"
            @input="handleOtpInput"
          />
        </div>

        <button
          v-if="!isLocked"
          type="submit"
          :disabled="!canSubmit"
          class="verify-button"
        >
          {{ isLoading ? 'Verifying...' : 'Verify' }}
        </button>

        <button
          v-else
          type="button"
          @click="handleRestart"
          class="restart-button"
        >
          Return to sign in
        </button>
      </form>

      <div v-if="!isLocked" class="resend-section">
        <div v-if="resendCountdownSeconds > 0" class="countdown-message">
          Resend available in {{ resendCountdownSeconds }}s
        </div>

        <button
          v-else
          @click="handleResend"
          type="button"
          class="resend-button"
        >
          Resend code
        </button>
      </div>

      <div class="cancel-link">
        <a href="#" @click.prevent="handleCancel">Cancel and return to sign in</a>
      </div>
    </div>
  </div>
</template>

<style scoped>
.otp-verify-container {
  display: flex;
  justify-content: center;
  align-items: center;
  min-height: 100vh;
  background-color: #f5f5f5;
}

.otp-form {
  background: white;
  padding: 2rem;
  border-radius: 8px;
  box-shadow: 0 2px 10px rgba(0, 0, 0, 0.1);
  width: 100%;
  max-width: 400px;
}

.otp-header {
  text-align: center;
  margin-bottom: 2rem;
}

.otp-header h1 {
  font-size: 1.75rem;
  font-weight: 700;
  color: #2c3e50;
  margin: 0 0 0.5rem 0;
  letter-spacing: 0.02em;
}

.otp-subtitle {
  font-size: 0.95rem;
  color: #666;
  margin: 0;
  line-height: 1.5;
}

form {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.error-message {
  color: #d32f2f;
  padding: 1rem;
  background-color: #ffebee;
  border-radius: 4px;
  border-left: 4px solid #d32f2f;
  font-size: 0.95rem;
}

.error-message.locked-message {
  margin-bottom: 0.5rem;
}

.success-message {
  color: #2e7d32;
  padding: 1rem;
  background-color: #e8f5e9;
  border-radius: 4px;
  border-left: 4px solid #2e7d32;
  font-size: 0.95rem;
  text-align: center;
}

.form-group {
  display: flex;
  justify-content: center;
  margin: 1.5rem 0;
}

.otp-input {
  width: 100%;
  padding: 1rem;
  font-size: 2rem;
  text-align: center;
  letter-spacing: 0.5em;
  border: 2px solid #ddd;
  border-radius: 4px;
  box-sizing: border-box;
  font-family: monospace;
  font-weight: 600;
}

.otp-input:focus {
  outline: none;
  border-color: #1976d2;
  box-shadow: 0 0 0 3px rgba(25, 118, 210, 0.1);
}

.otp-input:disabled {
  background-color: #f5f5f5;
  color: #999;
  cursor: not-allowed;
}

.verify-button,
.restart-button {
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

.verify-button:hover:not(:disabled) {
  background-color: #1565c0;
}

.verify-button:disabled {
  background-color: #ccc;
  cursor: not-allowed;
}

.restart-button {
  background-color: #d32f2f;
}

.restart-button:hover {
  background-color: #c62828;
}

.resend-section {
  text-align: center;
  margin-top: 1.5rem;
  padding-top: 1.5rem;
  border-top: 1px solid #ddd;
}

.countdown-message {
  color: #666;
  font-size: 0.9rem;
  margin-bottom: 0.5rem;
}

.resend-button {
  background: none;
  border: none;
  color: #1976d2;
  cursor: pointer;
  font-size: 0.95rem;
  font-weight: 500;
  text-decoration: none;
  transition: color 0.2s;
}

.resend-button:hover {
  color: #1565c0;
  text-decoration: underline;
}

.cancel-link {
  text-align: center;
  margin-top: 1.5rem;
  padding-top: 1.5rem;
  border-top: 1px solid #ddd;
}

.cancel-link a {
  color: #666;
  font-size: 0.9rem;
  text-decoration: none;
  transition: color 0.2s;
}

.cancel-link a:hover {
  color: #1976d2;
  text-decoration: underline;
}
</style>
