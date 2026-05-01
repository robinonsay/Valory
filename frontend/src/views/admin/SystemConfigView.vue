// @{"req": ["REQ-FEADMIN-045", "REQ-FEADMIN-046", "REQ-FEADMIN-047", "REQ-FEADMIN-050", "REQ-FEADMIN-055", "REQ-FEADMIN-210", "REQ-FEADMIN-215", "REQ-FEADMIN-220", "REQ-FEADMIN-230", "REQ-FEADMIN-240", "REQ-FEADMIN-250", "REQ-FEADMIN-260"]}

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { get, patch, ApiError } from '@/api/client'
import { CONFIG_KEYS, validateWeights } from './systemConfig'

interface ConfigResponse {
  config: Record<string, string>
}

const auth = useAuthStore()

const formValues = ref<Record<string, string>>({})
const originalValues = ref<Record<string, string>>({})
const loading = ref(false)
const saving = ref(false)
const fetchError = ref<string | null>(null)
const saveError = ref<string | null>(null)
const saveSuccess = ref(false)
const weightError = ref<string | null>(null)

// @{"req": ["REQ-FEADMIN-240", "REQ-FEADMIN-250"]}
// changedFields computes only the keys whose values differ from the fetched
// originals so the PATCH body never contains unchanged entries.
const changedFields = computed<Record<string, string>>(() => {
  const delta: Record<string, string> = {}
  for (const key of CONFIG_KEYS) {
    if (formValues.value[key] !== originalValues.value[key]) {
      delta[key] = formValues.value[key]
    }
  }
  return delta
})

// @{"req": ["REQ-FEADMIN-215"]}
const hasUnsavedChanges = computed(() => Object.keys(changedFields.value).length > 0)

// @{"req": ["REQ-FEADMIN-045", "REQ-FEADMIN-046", "REQ-FEADMIN-210"]}
async function fetchConfig() {
  try {
    loading.value = true
    fetchError.value = null
    const response = await get<ConfigResponse>('/api/v1/admin/config', auth.token)
    const cfg: Record<string, string> = {}
    for (const key of CONFIG_KEYS) {
      cfg[key] = response.config[key] ?? ''
    }
    formValues.value = { ...cfg }
    originalValues.value = { ...cfg }
  } catch (err) {
    if (err instanceof ApiError) {
      fetchError.value = `Failed to load configuration: ${err.message}`
    } else {
      fetchError.value = 'Failed to load configuration'
    }
  } finally {
    loading.value = false
  }
}

// @{"req": ["REQ-FEADMIN-047", "REQ-FEADMIN-220", "REQ-FEADMIN-230", "REQ-FEADMIN-240", "REQ-FEADMIN-250", "REQ-FEADMIN-260"]}
async function saveConfig() {
  saveError.value = null
  saveSuccess.value = false
  weightError.value = null

  const weightValidationError = validateWeights(formValues.value)
  if (weightValidationError !== null) {
    weightError.value = weightValidationError
    return
  }

  const delta = changedFields.value
  if (Object.keys(delta).length === 0) {
    return
  }

  try {
    saving.value = true
    await patch<ConfigResponse>('/api/v1/admin/config', { config: delta }, auth.token)
    originalValues.value = { ...formValues.value }
    saveSuccess.value = true
  } catch (err) {
    if (err instanceof ApiError) {
      const body = err.body as { error?: string } | null
      saveError.value = body?.error ?? err.message
    } else {
      saveError.value = 'Failed to save configuration'
    }
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  fetchConfig()
})
</script>

<template>
  <div class="system-config">
    <div class="header">
      <h1>System Configuration</h1>
    </div>

    <div v-if="loading" class="loading">Loading configuration...</div>

    <div v-else-if="fetchError" class="error-banner">{{ fetchError }}</div>

    <div v-else class="config-form">
      <div v-if="hasUnsavedChanges" class="unsaved-indicator">
        You have unsaved changes.
      </div>

      <div v-if="saveSuccess" class="success-banner">
        Configuration saved.
      </div>

      <div v-if="saveError" class="error-banner">{{ saveError }}</div>

      <div v-if="weightError" class="error-banner weight-error">{{ weightError }}</div>

      <div v-for="key in CONFIG_KEYS" :key="key" class="form-field">
        <label :for="`config-${key}`">{{ key }}</label>
        <input
          :id="`config-${key}`"
          v-model="formValues[key]"
          type="text"
          class="config-input"
        />
      </div>

      <div class="form-actions">
        <button
          @click="saveConfig"
          :disabled="saving"
          class="save-button"
        >
          {{ saving ? 'Saving...' : 'Save' }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.system-config {
  padding: 2rem;
  max-width: 800px;
  margin: 0 auto;
}

.header {
  margin-bottom: 2rem;
  border-bottom: 1px solid #ddd;
  padding-bottom: 1rem;
}

.header h1 {
  margin: 0;
  font-size: 2rem;
  color: #333;
}

.loading {
  text-align: center;
  padding: 2rem;
  font-size: 1.1rem;
  color: #666;
}

.error-banner {
  padding: 1rem;
  background-color: #ffebee;
  color: #d32f2f;
  border-radius: 4px;
  border-left: 4px solid #d32f2f;
  margin-bottom: 1rem;
}

.success-banner {
  padding: 1rem;
  background-color: #e8f5e9;
  color: #2e7d32;
  border-radius: 4px;
  border-left: 4px solid #2e7d32;
  margin-bottom: 1rem;
}

.unsaved-indicator {
  padding: 0.75rem 1rem;
  background-color: #fff8e1;
  color: #f57f17;
  border-radius: 4px;
  border-left: 4px solid #f9a825;
  margin-bottom: 1rem;
  font-weight: 500;
}

.config-form {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.form-field {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}

.form-field label {
  font-weight: 600;
  color: #333;
  font-size: 0.95rem;
}

.config-input {
  padding: 0.5rem 0.75rem;
  border: 1px solid #ddd;
  border-radius: 4px;
  font-size: 1rem;
  width: 100%;
  box-sizing: border-box;
}

.config-input:focus {
  outline: none;
  border-color: #1976d2;
  box-shadow: 0 0 0 2px rgba(25, 118, 210, 0.2);
}

.form-actions {
  margin-top: 1rem;
  display: flex;
  justify-content: flex-end;
}

.save-button {
  padding: 0.75rem 2rem;
  background-color: #1976d2;
  color: white;
  border: none;
  border-radius: 4px;
  font-weight: 500;
  cursor: pointer;
  font-size: 1rem;
  transition: background-color 0.2s;
}

.save-button:hover:not(:disabled) {
  background-color: #1565c0;
}

.save-button:disabled {
  background-color: #bbb;
  cursor: not-allowed;
}
</style>
