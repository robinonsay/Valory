// @{"req": ["REQ-FEADMIN-040", "REQ-FEADMIN-041", "REQ-FEADMIN-042", "REQ-FEADMIN-043", "REQ-FEADMIN-044", "REQ-FEADMIN-045", "REQ-FEADMIN-300", "REQ-FEADMIN-301", "REQ-FEADMIN-302", "REQ-FEADMIN-303", "REQ-FEADMIN-304", "REQ-FEADMIN-305", "REQ-FEADMIN-306", "REQ-FEADMIN-307", "REQ-FEADMIN-308", "REQ-FEADMIN-309", "REQ-FEADMIN-310", "REQ-FEADMIN-311", "REQ-FEADMIN-312", "REQ-FEADMIN-320", "REQ-FEADMIN-321", "REQ-FEADMIN-322", "REQ-FEADMIN-323", "REQ-FEADMIN-324", "REQ-FEADMIN-325"]}

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { get, patch, ApiError } from '@/api/client'
import { CONFIG_KEYS, CONFIG_LABELS, CONFIG_HINTS, validateWeights } from './systemConfig'

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
const fieldErrors = ref<Record<string, string>>({})

// @{"req": ["REQ-FEADMIN-325"]}
// parseFieldError extracts the key prefix from backend validation error strings
// e.g. "homework_weight: must equal 1.0" -> key="homework_weight", message="must equal 1.0"
function parseFieldError(errorString: string): { key: string; message: string } | null {
  const colonIdx = errorString.indexOf(':')
  if (colonIdx === -1) return null
  const key = errorString.substring(0, colonIdx).trim()
  const message = errorString.substring(colonIdx + 1).trim()
  return { key, message }
}

// @{"req": ["REQ-FEADMIN-041", "REQ-FEADMIN-320"]}
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

// @{"req": ["REQ-FEADMIN-042", "REQ-FEADMIN-323"]}
const hasUnsavedChanges = computed(() => Object.keys(changedFields.value).length > 0)

// @{"req": ["REQ-FEADMIN-040"]}
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

// @{"req": ["REQ-FEADMIN-043", "REQ-FEADMIN-041", "REQ-FEADMIN-043", "REQ-FEADMIN-320", "REQ-FEADMIN-321", "REQ-FEADMIN-322", "REQ-FEADMIN-323", "REQ-FEADMIN-324", "REQ-FEADMIN-325"]}
async function saveConfig() {
  saveError.value = null
  saveSuccess.value = false
  weightError.value = null
  fieldErrors.value = {}

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
      if (err.status === 422) {
        const body = err.body as { validation_errors?: string[] } | null
        if (body?.validation_errors && Array.isArray(body.validation_errors)) {
          const errors: Record<string, string> = {}
          for (const errorStr of body.validation_errors) {
            const parsed = parseFieldError(errorStr)
            if (parsed) {
              errors[parsed.key] = parsed.message
            }
          }
          fieldErrors.value = errors
        } else {
          saveError.value = 'Validation failed'
        }
      } else {
        const body = err.body as { error?: string } | null
        saveError.value = body?.error ?? err.message
      }
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
        <label :for="`config-${key}`">
          {{ CONFIG_LABELS[key] }}
          <span v-if="CONFIG_HINTS[key]" class="hint">({{ CONFIG_HINTS[key] }})</span>
        </label>
        <input
          :id="`config-${key}`"
          v-model="formValues[key]"
          type="text"
          class="config-input"
        />
        <div v-if="fieldErrors[key]" class="field-error">{{ fieldErrors[key] }}</div>
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
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}

.hint {
  font-weight: 400;
  color: #666;
  font-size: 0.85rem;
}

.field-error {
  font-size: 0.85rem;
  color: #d32f2f;
  margin-top: 0.25rem;
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
