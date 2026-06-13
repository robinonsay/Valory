// @{"req": ["REQ-FEADMIN-500", "REQ-FEADMIN-501", "REQ-FEADMIN-502", "REQ-FEADMIN-503", "REQ-FEADMIN-504", "REQ-FEADMIN-510", "REQ-FEADMIN-511", "REQ-FEADMIN-512", "REQ-FEADMIN-513", "REQ-FEADMIN-514", "REQ-FEADMIN-515", "REQ-FEADMIN-516", "REQ-FEADMIN-600", "REQ-FEADMIN-601", "REQ-FEADMIN-602", "REQ-FEADMIN-603", "REQ-FEADMIN-604"]}

import { defineStore } from 'pinia'
import { ref } from 'vue'
import { get, put, del, ApiError } from '@/api/client'

export interface SecretStatus {
  name: string
  configured: boolean
  last4: string | null
  updated_at: string | null
  source: 'managed' | 'env' | 'none'
}

export interface SecretsResponse {
  secrets: SecretStatus[]
}

// @{"req": ["REQ-FEADMIN-510", "REQ-FEADMIN-511", "REQ-FEADMIN-512", "REQ-FEADMIN-513", "REQ-FEADMIN-514", "REQ-FEADMIN-515", "REQ-FEADMIN-516"]}
// EXPLANATIONS maps config keys and managed secrets to their plain-language explanations.
// These are displayed to admins on the config page with honesty about reserved/inert keys.
export const EXPLANATIONS: Record<string, string> = {
  'agent_retry_limit':
    'Controls how many times the AI agent retries a failed Anthropic API call before giving up. Each retry waits longer than the last (exponential backoff with jitter). Default: 3. Minimum: 1. Higher values reduce outright failures during API congestion but increase the time a student waits for a response.',

  'correction_loop_max_iterations':
    'Limits how many times the Reviewer agent sends a generated section back to the Professor for revision before escalating to an admin notification. Default: 5. Minimum: 1. Raising this value may improve content quality at the cost of longer generation time.',

  'per_student_token_limit':
    'Maximum cumulative AI tokens (input + output) allowed per student per course. Default: 500000. Set to 0 to disable the limit entirely. Enforced before every AI call; once reached, all AI features are halted for that student\'s course.',

  'late_penalty_rate':
    'Fraction of a submission\'s raw score deducted for each calendar day it is late. Default: 0.05 (5% per day). Valid range: 0.0 to 1.0 inclusive. A value of 0.0 disables late penalties.',

  'homework_weight':
    'Share of the final course grade contributed by homework scores. Must be positive and sum to exactly 1.0 with `project_weight`. Default: 0.7.',

  'project_weight':
    'Share of the final course grade contributed by the final project score. Must be positive and sum to exactly 1.0 with `homework_weight`. Default: 0.3.',

  'session_inactivity_seconds':
    'Changing this value has no effect on the running server. The inactivity timeout is read from the `AUTH_INACTIVITY_PERIOD` environment variable at startup. This key is stored and validated so future wiring can activate it without a migration. Default: 1800 (30 minutes). Minimum: 1.',

  'account_lockout_seconds':
    'Changing this value has no effect on the running server. The lockout duration is read from the `AUTH_LOCKOUT_DURATION` environment variable at startup. This key is stored and validated so future wiring can activate it without a migration. Default: 900 (15 minutes). Minimum: 1.',

  'max_upload_bytes':
    'Maximum size in bytes of a single homework submission file. Default: 10485760 (10 MiB). Minimum: 1024. Files larger than this are rejected before disk write. Changes take effect immediately for new uploads.',

  'content_generation_timeout_seconds':
    'Maximum time for the full section-generation pipeline per course. Default: 300 (5 minutes). Minimum: 1. If generation does not complete within this window, the run is cancelled and the student is notified.',

  'professor_max_tokens':
    'Maximum output tokens the Professor agent may generate per lesson-section API call. Default: 16384. Valid range: 1024 to 16384. Values too low for the prompted section length (200-500 lines of AsciiDoc) cause the model to stop mid-word; the run now fails fast with a clear error instead of looping through doomed reviews. The ceiling exists because generation calls are non-streaming and larger outputs risk HTTP timeouts. Changes take effect on the next generation run.',

  'audit_retention_days':
    'No automated purge is currently implemented. This key is stored and validated but no background worker reads it to delete aged audit entries. The audit log is append-only by design (the `valory_app` DB role holds no DELETE privilege on `audit_log`). This key is a placeholder for a future retention worker. Default: 365. Minimum: 1.',

  'notification_retention_days':
    'Notifications older than this many days are deleted by a background worker that runs every 24 hours. Default: 90. Minimum: 1. Changes take effect at the next worker cycle.',

  'consent_version':
    'The current AI data-sharing consent version string (e.g. "1.0", "2.0"). Any student whose stored consent version is lower than this value must re-accept consent before accessing any protected endpoint. Bumping this value is a gate action — plan it carefully, as it immediately blocks all students who have not yet accepted the new version. Admins are exempt.',

  'anthropic_base_url':
    'The Anthropic API endpoint URL, used for all AI features — content generation, grading, and chat. Leave empty to use Anthropic\'s hosted endpoint (recommended). Only set this when self-hosting a compatible gateway or proxy (e.g., for on-premises deployments or custom routing). An incorrect value will stop ALL AI features (course generation, chat, grading) until corrected. Takes effect immediately after saving — no container restart needed.',

  'anthropic_api_key':
    'The Anthropic API key used to call Claude for all AI features — content generation, grading, and chat. If set here, this value takes precedence over the `ANTHROPIC_API_KEY` environment variable. If neither is set, all AI features will fail at runtime. Changes take effect within 30 seconds without a container restart.',

  'brave_api_key':
    'The Brave Search API key used to ground lesson content in current internet search results. If absent (neither managed nor env var), web search grounding is silently skipped and the Professor generates content without internet context. Changes take effect within 30 seconds without a restart.',

  'smtp_password':
    'The SMTP password or app-specific password for authentication. If set here, this value takes precedence over the SMTP_PASSWORD environment variable. Leave the username empty if your relay does not require authentication. For Gmail: generate an App Password in your Google Account security settings — do NOT use your Gmail account password here. Changes take effect immediately on the next email send.',
}

export const useSystemConfigStore = defineStore('systemConfig', () => {
  const secrets = ref<SecretStatus[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  // @{"req": ["REQ-FEADMIN-500"]}
  async function fetchSecrets() {
    try {
      loading.value = true
      error.value = null
      const response = await get<SecretsResponse>('/api/v1/admin/secrets')
      secrets.value = response.secrets || []
    } catch (err) {
      if (err instanceof ApiError) {
        error.value = `Failed to load secrets: ${err.message}`
      } else {
        error.value = 'Failed to load secrets'
      }
      secrets.value = []
    } finally {
      loading.value = false
    }
  }

  // @{"req": ["REQ-FEADMIN-501", "REQ-FEADMIN-502"]}
  async function saveSecret(name: string, value: string) {
    try {
      error.value = null
      await put<void>(`/api/v1/admin/secrets/${name}`, { value })
      await fetchSecrets()
      return null
    } catch (err) {
      if (err instanceof ApiError) {
        const message = (err.body as { error?: string })?.error ?? err.message
        error.value = message
        return message
      } else {
        error.value = 'Failed to save secret'
        return 'Failed to save secret'
      }
    }
  }

  // @{"req": ["REQ-FEADMIN-504"]}
  async function deleteSecret(name: string) {
    try {
      error.value = null
      await del<void>(`/api/v1/admin/secrets/${name}`)
      await fetchSecrets()
      return null
    } catch (err) {
      if (err instanceof ApiError) {
        const message = (err.body as { error?: string })?.error ?? err.message
        error.value = message
        return message
      } else {
        error.value = 'Failed to delete secret'
        return 'Failed to delete secret'
      }
    }
  }

  return {
    secrets,
    loading,
    error,
    fetchSecrets,
    saveSecret,
    deleteSecret,
  }
})
