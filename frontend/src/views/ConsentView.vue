// @{"req": ["REQ-FEAUTH-030", "REQ-FEAUTH-031", "REQ-FEAUTH-032", "REQ-FEAUTH-140", "REQ-FEAUTH-141", "REQ-FEAUTH-142"]}
<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { post, ApiError } from '@/api/client'

const router = useRouter()
const auth = useAuthStore()

const isLoading = ref(false)
const errorMessage = ref<string | null>(null)

const handleAgree = async (): Promise<void> => {
  isLoading.value = true
  errorMessage.value = null

  try {
    await post('/api/v1/consent', { version: '1.0' }, auth.token)
    auth.setConsented()
    // Admins are exempt from the consent gate but can still land here;
    // send each role to its own home.
    await router.push(auth.isAdmin ? '/admin/users' : '/courses')
  } catch (err) {
    if (err instanceof ApiError) {
      errorMessage.value = 'Failed to record consent. Please try again.'
    } else {
      errorMessage.value = 'Failed to record consent. Please try again.'
    }
    isLoading.value = false
  }
}
</script>

<template>
  <div class="consent-container">
    <div class="consent-content">
      <p class="consent-text">
        By using Valory, you agree to our terms of service and privacy policy.
      </p>

      <div v-if="errorMessage" class="error-message">
        {{ errorMessage }}
      </div>

      <button
        :disabled="isLoading"
        @click="handleAgree"
        class="agree-button"
      >
        I agree
      </button>
    </div>
  </div>
</template>

<style scoped>
.consent-container {
  display: flex;
  justify-content: center;
  align-items: center;
  min-height: 100vh;
  padding: 20px;
}

.consent-content {
  max-width: 600px;
  text-align: center;
}

.consent-text {
  font-size: 16px;
  line-height: 1.6;
  margin-bottom: 20px;
  color: #333;
}

.error-message {
  color: #d32f2f;
  margin-bottom: 20px;
  padding: 10px;
  background-color: #ffebee;
  border-radius: 4px;
}

.agree-button {
  padding: 10px 24px;
  font-size: 16px;
  background-color: #1976d2;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  transition: background-color 0.3s ease;
}

.agree-button:hover:not(:disabled) {
  background-color: #1565c0;
}

.agree-button:disabled {
  background-color: #bdbdbd;
  cursor: not-allowed;
}
</style>
