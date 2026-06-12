// @{"req": ["REQ-FEADMIN-040", "REQ-FEADMIN-041", "REQ-FEADMIN-042", "REQ-FEADMIN-043", "REQ-FEADMIN-044", "REQ-FEADMIN-045", "REQ-FEADMIN-300", "REQ-FEADMIN-301", "REQ-FEADMIN-302", "REQ-FEADMIN-303", "REQ-FEADMIN-304", "REQ-FEADMIN-305", "REQ-FEADMIN-306", "REQ-FEADMIN-307", "REQ-FEADMIN-308", "REQ-FEADMIN-309", "REQ-FEADMIN-310", "REQ-FEADMIN-311", "REQ-FEADMIN-312", "REQ-FEADMIN-320", "REQ-FEADMIN-321", "REQ-FEADMIN-322", "REQ-FEADMIN-323", "REQ-FEADMIN-324", "REQ-FEADMIN-325", "REQ-FEADMIN-500", "REQ-FEADMIN-501", "REQ-FEADMIN-502", "REQ-FEADMIN-503", "REQ-FEADMIN-504", "REQ-FEADMIN-510", "REQ-FEADMIN-511", "REQ-FEADMIN-512", "REQ-FEADMIN-513", "REQ-FEADMIN-514", "REQ-FEADMIN-515", "REQ-FEADMIN-516"]}

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { get, patch, ApiError } from '@/api/client'
import { CONFIG_KEYS, CONFIG_LABELS, CONFIG_HINTS, validateWeights, EXPLANATIONS } from './systemConfig'
import { useSystemConfigStore } from '@/stores/admin/systemConfig'

interface ConfigResponse {
  config: Record<string, string>
}

const auth = useAuthStore()
const secretsStore = useSystemConfigStore()

const formValues = ref<Record<string, string>>({})
const originalValues = ref<Record<string, string>>({})
const loading = ref(false)
const saving = ref(false)
const fetchError = ref<string | null>(null)
const saveError = ref<string | null>(null)
const saveSuccess = ref(false)
const weightError = ref<string | null>(null)
const fieldErrors = ref<Record<string, string>>({})

// Secrets UI state
const secretValues = ref<Record<string, string>>({})
const savingSecret = ref<Record<string, boolean>>({})
const secretErrors = ref<Record<string, string>>({})
const expandedExplanations = ref<Record<string, boolean>>({})

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
    const response = await get<ConfigResponse>('/api/v1/admin/config')
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
    await patch<ConfigResponse>('/api/v1/admin/config', { config: delta })
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

// @{"req": ["REQ-FEADMIN-500", "REQ-FEADMIN-501", "REQ-FEADMIN-502"]}
// getSecretLabel returns the display label for a managed secret.
function getSecretLabel(name: string): string {
  if (name === 'anthropic_api_key') return 'Anthropic API Key'
  if (name === 'brave_api_key') return 'Brave Search API Key'
  return name
}

// @{"req": ["REQ-FEADMIN-500", "REQ-FEADMIN-503"]}
// getSecretStatusBadge returns the status text and class for a secret based on its source.
function getSecretStatusBadge(secret: SecretStatus): { text: string; class: string } {
  if (secret.source === 'managed' && secret.last4) {
    return {
      text: `Configured (••••${secret.last4})`,
      class: 'badge-configured'
    }
  } else if (secret.source === 'env') {
    return {
      text: 'Set via environment',
      class: 'badge-env'
    }
  } else {
    return {
      text: 'Not configured',
      class: 'badge-none'
    }
  }
}

// @{"req": ["REQ-FEADMIN-501", "REQ-FEADMIN-502"]}
// saveSecret persists a new secret value via PUT and refreshes the status.
async function saveSecret(name: string) {
  const value = secretValues.value[name] || ''
  if (!value) {
    secretErrors.value[name] = 'Value cannot be empty'
    return
  }

  savingSecret.value[name] = true
  secretErrors.value[name] = ''

  const err = await secretsStore.saveSecret(name, value)
  if (err) {
    secretErrors.value[name] = err
  } else {
    secretValues.value[name] = ''
  }

  savingSecret.value[name] = false
}

// @{"req": ["REQ-FEADMIN-504"]}
// deleteSecret removes a managed secret after confirmation.
async function deleteSecret(name: string) {
  const label = getSecretLabel(name)
  const confirmMsg = `Remove managed ${label}? The environment variable fallback will be used if set.`
  if (!window.confirm(confirmMsg)) {
    return
  }

  secretErrors.value[name] = ''
  const err = await secretsStore.deleteSecret(name)
  if (err) {
    secretErrors.value[name] = err
  }
}

// @{"req": ["REQ-FEADMIN-510"]}
// toggleExplanation toggles the visibility of an explanation block.
function toggleExplanation(key: string) {
  expandedExplanations.value[key] = !expandedExplanations.value[key]
}

onMounted(async () => {
  await fetchConfig()
  await secretsStore.fetchSecrets()
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

      <!-- Secrets Section -->
      <div v-if="!secretsStore.loading" class="secrets-card">
        <h2>API Keys</h2>
        <div v-if="secretsStore.error" class="error-banner">{{ secretsStore.error }}</div>

        <div v-for="secret in secretsStore.secrets" :key="secret.name" class="secret-field">
          <div class="secret-header">
            <label>{{ getSecretLabel(secret.name) }}</label>
            <span :class="['secret-badge', getSecretStatusBadge(secret).class]">
              {{ getSecretStatusBadge(secret).text }}
            </span>
          </div>

          <div class="secret-input-group">
            <input
              :id="`secret-${secret.name}`"
              v-model="secretValues[secret.name]"
              type="password"
              placeholder="Enter new value to update"
              class="secret-input"
              autocomplete="new-password"
            />
            <button
              @click="saveSecret(secret.name)"
              :disabled="savingSecret[secret.name]"
              class="secret-button save-secret-button"
            >
              {{ savingSecret[secret.name] ? 'Saving...' : 'Save' }}
            </button>
            <button
              v-if="secret.source === 'managed'"
              @click="deleteSecret(secret.name)"
              class="secret-button delete-secret-button"
            >
              Clear
            </button>
          </div>

          <div v-if="secretErrors[secret.name]" class="field-error">
            {{ secretErrors[secret.name] }}
          </div>

          <div class="secret-explanation">
            {{ EXPLANATIONS[secret.name] }}
          </div>
        </div>
      </div>

      <!-- Config Fields with Explanations -->
      <div v-for="key in CONFIG_KEYS" :key="key" class="form-field-with-explanation">
        <div class="form-field">
          <div class="field-header">
            <label :for="`config-${key}`">
              {{ CONFIG_LABELS[key] }}
              <span v-if="CONFIG_HINTS[key]" class="hint">({{ CONFIG_HINTS[key] }})</span>
            </label>
            <button
              @click="toggleExplanation(key)"
              class="explanation-toggle"
              :title="expandedExplanations[key] ? 'Hide explanation' : 'Show explanation'"
            >
              ?
            </button>
          </div>
          <input
            :id="`config-${key}`"
            v-model="formValues[key]"
            type="text"
            class="config-input"
          />
          <div v-if="fieldErrors[key]" class="field-error">{{ fieldErrors[key] }}</div>
        </div>

        <div v-if="expandedExplanations[key]" class="explanation-block">
          {{ EXPLANATIONS[key] }}
        </div>
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

.secrets-card {
  margin-bottom: 2rem;
  padding: 1.5rem;
  background-color: #f9f9f9;
  border: 1px solid #ddd;
  border-radius: 4px;
}

.secrets-card h2 {
  margin: 0 0 1rem 0;
  font-size: 1.3rem;
  color: #333;
}

.secret-field {
  margin-bottom: 1.5rem;
  padding-bottom: 1.5rem;
  border-bottom: 1px solid #ddd;
}

.secret-field:last-child {
  margin-bottom: 0;
  padding-bottom: 0;
  border-bottom: none;
}

.secret-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 0.5rem;
}

.secret-header label {
  font-weight: 600;
  color: #333;
  font-size: 0.95rem;
}

.secret-badge {
  padding: 0.25rem 0.75rem;
  border-radius: 4px;
  font-size: 0.85rem;
  font-weight: 500;
}

.badge-configured {
  background-color: #c8e6c9;
  color: #2e7d32;
}

.badge-env {
  background-color: #ffe0b2;
  color: #e65100;
}

.badge-none {
  background-color: #ffcdd2;
  color: #d32f2f;
}

.secret-input-group {
  display: flex;
  gap: 0.5rem;
  margin: 0.5rem 0;
}

.secret-input {
  flex: 1;
  padding: 0.5rem 0.75rem;
  border: 1px solid #ddd;
  border-radius: 4px;
  font-size: 1rem;
  box-sizing: border-box;
}

.secret-input:focus {
  outline: none;
  border-color: #1976d2;
  box-shadow: 0 0 0 2px rgba(25, 118, 210, 0.2);
}

.secret-button {
  padding: 0.5rem 1rem;
  border: none;
  border-radius: 4px;
  font-size: 0.9rem;
  font-weight: 500;
  cursor: pointer;
  transition: background-color 0.2s;
}

.save-secret-button {
  background-color: #1976d2;
  color: white;
}

.save-secret-button:hover:not(:disabled) {
  background-color: #1565c0;
}

.save-secret-button:disabled {
  background-color: #bbb;
  cursor: not-allowed;
}

.delete-secret-button {
  background-color: #d32f2f;
  color: white;
}

.delete-secret-button:hover {
  background-color: #c62828;
}

.secret-explanation {
  margin-top: 0.5rem;
  padding: 0.75rem;
  background-color: #f5f5f5;
  border-left: 3px solid #1976d2;
  font-size: 0.9rem;
  color: #555;
  line-height: 1.5;
}

.form-field-with-explanation {
  margin-bottom: 1rem;
}

.field-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 0.5rem;
}

.field-header label {
  flex: 1;
}

.explanation-toggle {
  padding: 0.25rem 0.5rem;
  background-color: #f5f5f5;
  border: 1px solid #ddd;
  border-radius: 4px;
  font-size: 0.9rem;
  font-weight: 600;
  cursor: pointer;
  color: #666;
  transition: background-color 0.2s;
  flex-shrink: 0;
}

.explanation-toggle:hover {
  background-color: #e0e0e0;
}

.explanation-block {
  margin-top: 0.5rem;
  padding: 0.75rem;
  background-color: #f5f5f5;
  border-left: 3px solid #1976d2;
  font-size: 0.9rem;
  color: #555;
  line-height: 1.5;
}
</style>
