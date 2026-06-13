// @{"req": ["REQ-FEADMIN-040", "REQ-FEADMIN-041", "REQ-FEADMIN-042", "REQ-FEADMIN-043", "REQ-FEADMIN-044", "REQ-FEADMIN-045", "REQ-FEADMIN-300", "REQ-FEADMIN-301", "REQ-FEADMIN-302", "REQ-FEADMIN-303", "REQ-FEADMIN-304", "REQ-FEADMIN-305", "REQ-FEADMIN-306", "REQ-FEADMIN-307", "REQ-FEADMIN-308", "REQ-FEADMIN-309", "REQ-FEADMIN-310", "REQ-FEADMIN-311", "REQ-FEADMIN-312", "REQ-FEADMIN-320", "REQ-FEADMIN-321", "REQ-FEADMIN-322", "REQ-FEADMIN-323", "REQ-FEADMIN-324", "REQ-FEADMIN-325", "REQ-FEADMIN-500", "REQ-FEADMIN-501", "REQ-FEADMIN-502", "REQ-FEADMIN-503", "REQ-FEADMIN-504", "REQ-FEADMIN-510", "REQ-FEADMIN-511", "REQ-FEADMIN-512", "REQ-FEADMIN-513", "REQ-FEADMIN-514", "REQ-FEADMIN-515", "REQ-FEADMIN-516", "REQ-FEADMIN-600", "REQ-FEADMIN-601", "REQ-FEADMIN-602", "REQ-FEADMIN-603", "REQ-FEADMIN-604"]}

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { get, patch, post, ApiError } from '@/api/client'
import { CONFIG_KEYS, CONFIG_LABELS, CONFIG_HINTS, EMAIL_CONFIG_KEYS, validateWeights, EXPLANATIONS } from './systemConfig'
import { useSystemConfigStore } from '@/stores/admin/systemConfig'

interface TwoFAPrerequisites {
  email_configured: boolean
  test_send_verified: boolean
  admins_without_email: number
  students_without_email: number
}

interface ConfigResponse {
  config: Record<string, string>
  two_fa_prerequisites?: TwoFAPrerequisites
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
const twoFAPrerequisites = ref<TwoFAPrerequisites | null>(null)

// Secrets UI state
const secretValues = ref<Record<string, string>>({})
const savingSecret = ref<Record<string, boolean>>({})
const secretErrors = ref<Record<string, string>>({})
const expandedExplanations = ref<Record<string, boolean>>({})

// @{"req": ["REQ-FEADMIN-603"]}
// Test-send state
const testEmailTo = ref('')
const testEmailSending = ref(false)
const testEmailResult = ref<{ ok: boolean; message: string; smtp_error?: string } | null>(null)

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

// @{"req": ["REQ-FEADMIN-600"]}
// nonEmailConfigKeys filters CONFIG_KEYS to exclude EMAIL_CONFIG_KEYS and two_factor_enabled
const nonEmailConfigKeys = computed(() => {
  return CONFIG_KEYS.filter(k => !EMAIL_CONFIG_KEYS.includes(k) && k !== 'two_factor_enabled')
})

// @{"req": ["REQ-FEADMIN-700"]}
// twoFAEnabled computes the current 2FA enabled state
const twoFAEnabled = computed(() => {
  return formValues.value['two_factor_enabled'] === 'true'
})

// @{"req": ["REQ-FEADMIN-700"]}
// isTwoFAToggleDisabled checks if prerequisites are unmet
const isTwoFAToggleDisabled = computed(() => {
  if (!twoFAPrerequisites.value) return true
  return (
    !twoFAPrerequisites.value.email_configured ||
    !twoFAPrerequisites.value.test_send_verified ||
    twoFAPrerequisites.value.admins_without_email > 0
  )
})

// @{"req": ["REQ-FEADMIN-700"]}
// getTwoFADisabledReasons builds the reason text for the disabled state
const getTwoFADisabledReasons = computed(() => {
  if (!twoFAPrerequisites.value) return 'Loading prerequisites...'
  const reasons: string[] = []
  if (!twoFAPrerequisites.value.email_configured) {
    reasons.push('Email is not configured')
  }
  if (!twoFAPrerequisites.value.test_send_verified) {
    reasons.push('Test-send has not been verified')
  }
  if (twoFAPrerequisites.value.admins_without_email > 0) {
    reasons.push(`${twoFAPrerequisites.value.admins_without_email} admin(s) lack email address(es)`)
  }
  return reasons.length > 0 ? reasons.join('; ') : ''
})

// @{"req": ["REQ-FEADMIN-042", "REQ-FEADMIN-323"]}
const hasUnsavedChanges = computed(() => Object.keys(changedFields.value).length > 0)

// @{"req": ["REQ-FEADMIN-040", "REQ-FEADMIN-700"]}
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
    twoFAPrerequisites.value = response.two_fa_prerequisites ?? null
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

// @{"req": ["REQ-FEADMIN-043", "REQ-FEADMIN-041", "REQ-FEADMIN-043", "REQ-FEADMIN-320", "REQ-FEADMIN-321", "REQ-FEADMIN-322", "REQ-FEADMIN-323", "REQ-FEADMIN-324", "REQ-FEADMIN-325", "REQ-FEADMIN-700"]}
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

  // Show confirmation dialog when enabling 2FA
  if (delta['two_factor_enabled'] === 'true' && !twoFAEnabled.value) {
    const confirmed = window.confirm(
      'Two-factor authentication will be required for all users on their next login. Make sure all admin accounts have email addresses. Continue?'
    )
    if (!confirmed) {
      return
    }
  }

  try {
    saving.value = true
    const response = await patch<ConfigResponse>('/api/v1/admin/config', { config: delta })
    originalValues.value = { ...formValues.value }
    twoFAPrerequisites.value = response.two_fa_prerequisites ?? null
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
  if (name === 'smtp_password') return 'SMTP Password'
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

// @{"req": ["REQ-FEADMIN-603"]}
// sendTestEmail sends a test email to the provided address and displays the result.
async function sendTestEmail() {
  testEmailResult.value = null
  if (!testEmailTo.value.trim()) {
    testEmailResult.value = {
      ok: false,
      message: 'Please enter a recipient email address.'
    }
    return
  }

  testEmailSending.value = true
  try {
    const response = await post<{ ok: boolean; message?: string; smtp_error?: string }>(
      '/api/v1/admin/config/email/test',
      { to: testEmailTo.value }
    )
    testEmailResult.value = {
      ok: response.ok,
      message: response.ok ? (response.message || 'Test email sent successfully.') : `Test failed: ${response.smtp_error || 'unknown error'}.`,
      smtp_error: response.smtp_error
    }
  } catch (err) {
    if (err instanceof ApiError) {
      if (err.status === 429) {
        testEmailResult.value = {
          ok: false,
          message: 'Rate limited — try again in 60 seconds.'
        }
      } else {
        const body = err.body as { error?: string; smtp_error?: string } | null
        const errorDetail = body?.smtp_error || body?.error || err.message
        testEmailResult.value = {
          ok: false,
          message: `Test failed: ${errorDetail}.`,
          smtp_error: body?.smtp_error
        }
      }
    } else {
      testEmailResult.value = {
        ok: false,
        message: 'Test failed: network or server error.'
      }
    }
  } finally {
    testEmailSending.value = false
  }
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

      <!-- Two-Factor Authentication Section -->
      <div class="security-card">
        <h2>Two-Factor Authentication</h2>

        <div class="twofa-explanation">
          <p>2FA requires all users to enter a one-time code sent to their email address when signing in.</p>
          <p><strong>Before enabling:</strong></p>
          <ul>
            <li>Email must be configured and a test message must have been sent successfully.</li>
            <li>All admin accounts must have an email address.</li>
          </ul>
        </div>

        <div class="twofa-status">
          <div class="status-item">
            <span class="status-label">Email:</span>
            <span :class="['status-value', twoFAPrerequisites?.email_configured ? 'ok' : 'missing']">
              {{ twoFAPrerequisites?.email_configured ? 'Configured' : 'Not configured' }}
            </span>
          </div>
          <div class="status-item">
            <span class="status-label">Test send:</span>
            <span :class="['status-value', twoFAPrerequisites?.test_send_verified ? 'ok' : 'missing']">
              {{ twoFAPrerequisites?.test_send_verified ? 'Verified' : 'Not yet verified' }}
            </span>
          </div>
          <div class="status-item">
            <span class="status-label">Admin accounts without email:</span>
            <span :class="['status-value', (twoFAPrerequisites?.admins_without_email ?? 0) === 0 ? 'ok' : 'missing']">
              {{ twoFAPrerequisites?.admins_without_email ?? 0 }}
              <span v-if="(twoFAPrerequisites?.admins_without_email ?? 0) > 0">— please add emails before enabling</span>
            </span>
          </div>
          <div v-if="(twoFAPrerequisites?.students_without_email ?? 0) > 0" class="status-item advisory">
            <span class="status-label">Student accounts without email (advisory):</span>
            <span class="status-value">{{ twoFAPrerequisites?.students_without_email }}</span>
          </div>
        </div>

        <div class="twofa-toggle-section">
          <label :for="`config-two_factor_enabled`" class="toggle-label">
            <input
              :id="`config-two_factor_enabled`"
              v-model="formValues['two_factor_enabled']"
              type="checkbox"
              :true-value="'true'"
              :false-value="'false'"
              :disabled="isTwoFAToggleDisabled"
              class="toggle-checkbox"
            />
            <span class="toggle-text">Enable two-factor authentication</span>
          </label>
          <div v-if="isTwoFAToggleDisabled" class="toggle-disabled-reason">
            {{ getTwoFADisabledReasons }}
          </div>
          <div v-if="fieldErrors['two_factor_enabled']" class="field-error">
            {{ fieldErrors['two_factor_enabled'] }}
          </div>
        </div>
      </div>

      <!-- Email Configuration Section -->
      <div class="email-card">
        <h2>Email</h2>

        <!-- Email Config Fields -->
        <div v-for="key in EMAIL_CONFIG_KEYS" :key="key" class="form-field-with-explanation">
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
            <!-- Encryption dropdown for smtp_encryption, text input for others -->
            <select
              v-if="key === 'smtp_encryption'"
              :id="`config-${key}`"
              v-model="formValues[key]"
              class="config-input"
            >
              <option value="">Select encryption mode</option>
              <option value="none">None — plaintext SMTP</option>
              <option value="starttls">STARTTLS — upgrade after connect (recommended)</option>
              <option value="tls">TLS — implicit TLS (port 465)</option>
            </select>
            <input
              v-else
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

        <!-- SMTP Password (managed secret) -->
        <div v-for="secret in secretsStore.secrets.filter(s => s.name === 'smtp_password')" :key="secret.name" class="secret-field email-password-field">
          <div class="secret-header">
            <label>SMTP Password</label>
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

        <!-- Test-Send UI -->
        <div class="test-email-section">
          <div class="test-email-controls">
            <input
              v-model="testEmailTo"
              type="email"
              placeholder="Test email to:"
              class="test-email-input"
            />
            <button
              @click="sendTestEmail"
              :disabled="testEmailSending"
              class="test-email-button"
            >
              {{ testEmailSending ? 'Sending...' : 'Send test email' }}
            </button>
          </div>

          <div
            v-if="testEmailResult"
            :class="['test-email-result', testEmailResult.ok ? 'success' : 'error']"
          >
            {{ testEmailResult.message }}
          </div>
        </div>
      </div>

      <!-- Config Fields with Explanations -->
      <div v-for="key in nonEmailConfigKeys" :key="key" class="form-field-with-explanation">
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

.security-card {
  margin-bottom: 2rem;
  padding: 1.5rem;
  background-color: #f9f9f9;
  border: 1px solid #ddd;
  border-radius: 4px;
}

.security-card h2 {
  margin: 0 0 1rem 0;
  font-size: 1.3rem;
  color: #333;
}

.twofa-explanation {
  margin-bottom: 1.5rem;
  padding: 1rem;
  background-color: #f5f5f5;
  border-left: 3px solid #1976d2;
  border-radius: 2px;
  font-size: 0.9rem;
  color: #555;
  line-height: 1.5;
}

.twofa-explanation p {
  margin: 0 0 0.5rem 0;
}

.twofa-explanation p:last-child {
  margin-bottom: 0;
}

.twofa-explanation ul {
  margin: 0.5rem 0 0 1.5rem;
  padding: 0;
}

.twofa-explanation li {
  margin-bottom: 0.25rem;
}

.twofa-status {
  margin-bottom: 1.5rem;
  padding: 1rem;
  background-color: #fafafa;
  border: 1px solid #eee;
  border-radius: 4px;
}

.status-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 0.75rem;
  font-size: 0.9rem;
}

.status-item:last-child {
  margin-bottom: 0;
}

.status-item.advisory {
  background-color: #fffbf0;
  padding: 0.5rem;
  border-radius: 2px;
  margin: 0.75rem -0.5rem -0.75rem -0.5rem;
  padding: 0.75rem;
}

.status-label {
  font-weight: 500;
  color: #333;
}

.status-value {
  font-weight: 600;
}

.status-value.ok {
  color: #2e7d32;
}

.status-value.missing {
  color: #d32f2f;
}

.twofa-toggle-section {
  margin-top: 1.5rem;
  padding-top: 1.5rem;
  border-top: 1px solid #ddd;
}

.toggle-label {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  cursor: pointer;
  font-weight: 500;
  color: #333;
}

.toggle-checkbox {
  width: 18px;
  height: 18px;
  cursor: pointer;
}

.toggle-checkbox:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.toggle-text {
  user-select: none;
}

.toggle-disabled-reason {
  margin-top: 0.5rem;
  padding: 0.75rem;
  font-size: 0.85rem;
  color: #d32f2f;
  background-color: #ffebee;
  border-radius: 2px;
  border-left: 3px solid #d32f2f;
}

.email-card {
  margin-bottom: 2rem;
  padding: 1.5rem;
  background-color: #f9f9f9;
  border: 1px solid #ddd;
  border-radius: 4px;
}

.email-card h2 {
  margin: 0 0 1rem 0;
  font-size: 1.3rem;
  color: #333;
}

.email-password-field {
  margin-top: 1.5rem;
  margin-bottom: 1.5rem;
  padding-top: 1.5rem;
  padding-bottom: 0;
  border-top: 1px solid #ddd;
}

.test-email-section {
  margin-top: 1.5rem;
  padding-top: 1.5rem;
  border-top: 1px solid #ddd;
}

.test-email-controls {
  display: flex;
  gap: 0.5rem;
  margin-bottom: 0.5rem;
}

.test-email-input {
  flex: 1;
  padding: 0.5rem 0.75rem;
  border: 1px solid #ddd;
  border-radius: 4px;
  font-size: 1rem;
  box-sizing: border-box;
}

.test-email-input:focus {
  outline: none;
  border-color: #1976d2;
  box-shadow: 0 0 0 2px rgba(25, 118, 210, 0.2);
}

.test-email-button {
  padding: 0.5rem 1rem;
  background-color: #1976d2;
  color: white;
  border: none;
  border-radius: 4px;
  font-size: 0.9rem;
  font-weight: 500;
  cursor: pointer;
  transition: background-color 0.2s;
}

.test-email-button:hover:not(:disabled) {
  background-color: #1565c0;
}

.test-email-button:disabled {
  background-color: #bbb;
  cursor: not-allowed;
}

.test-email-result {
  padding: 0.75rem;
  border-radius: 4px;
  border-left: 4px solid;
  font-size: 0.9rem;
  line-height: 1.5;
}

.test-email-result.success {
  background-color: #e8f5e9;
  color: #2e7d32;
  border-left-color: #2e7d32;
}

.test-email-result.error {
  background-color: #ffebee;
  color: #d32f2f;
  border-left-color: #d32f2f;
}
</style>
