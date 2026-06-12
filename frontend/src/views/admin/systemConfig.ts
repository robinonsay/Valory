// @{"req": ["REQ-FEADMIN-040", "REQ-FEADMIN-041", "REQ-FEADMIN-042", "REQ-FEADMIN-043", "REQ-FEADMIN-044", "REQ-FEADMIN-045", "REQ-FEADMIN-300", "REQ-FEADMIN-301", "REQ-FEADMIN-302", "REQ-FEADMIN-303", "REQ-FEADMIN-304", "REQ-FEADMIN-305", "REQ-FEADMIN-306", "REQ-FEADMIN-307", "REQ-FEADMIN-308", "REQ-FEADMIN-309", "REQ-FEADMIN-310", "REQ-FEADMIN-311", "REQ-FEADMIN-312", "REQ-FEADMIN-330", "REQ-FEADMIN-510", "REQ-FEADMIN-511", "REQ-FEADMIN-512", "REQ-FEADMIN-513", "REQ-FEADMIN-514", "REQ-FEADMIN-515", "REQ-FEADMIN-516"]}

export const CONFIG_KEYS = [
  'agent_retry_limit',
  'correction_loop_max_iterations',
  'per_student_token_limit',
  'late_penalty_rate',
  'homework_weight',
  'project_weight',
  'session_inactivity_seconds',
  'account_lockout_seconds',
  'max_upload_bytes',
  'content_generation_timeout_seconds',
  'audit_retention_days',
  'notification_retention_days',
  'consent_version',
  'anthropic_base_url'
] as const

export const WEIGHT_KEYS = [
  'homework_weight',
  'project_weight'
] as const

export const WEIGHT_TOLERANCE = 0.001

// @{"req": ["REQ-FEADMIN-043", "REQ-FEADMIN-330", "REQ-FEADMIN-331", "REQ-FEADMIN-332", "REQ-FEADMIN-333", "REQ-FEADMIN-334", "REQ-FEADMIN-335", "REQ-FEADMIN-336", "REQ-FEADMIN-337", "REQ-FEADMIN-338", "REQ-FEADMIN-339", "REQ-FEADMIN-340", "REQ-FEADMIN-341", "REQ-FEADMIN-342", "REQ-FEADMIN-343"]}
export const CONFIG_LABELS: Record<string, string> = {
  'agent_retry_limit': 'Agent Retry Limit',
  'correction_loop_max_iterations': 'Correction Loop Max Iterations',
  'per_student_token_limit': 'Per Student Token Limit',
  'late_penalty_rate': 'Late Penalty Rate',
  'homework_weight': 'Homework Weight',
  'project_weight': 'Project Weight',
  'session_inactivity_seconds': 'Session Inactivity (seconds)',
  'account_lockout_seconds': 'Account Lockout (seconds)',
  'max_upload_bytes': 'Max Upload Size (bytes)',
  'content_generation_timeout_seconds': 'Content Generation Timeout (seconds)',
  'audit_retention_days': 'Audit Retention (days)',
  'notification_retention_days': 'Notification Retention (days)',
  'consent_version': 'Consent Version',
  'anthropic_base_url': 'Anthropic API Endpoint URL'
}

// @{"req": ["REQ-FEADMIN-043", "REQ-FEADMIN-330", "REQ-FEADMIN-331", "REQ-FEADMIN-332", "REQ-FEADMIN-333", "REQ-FEADMIN-334", "REQ-FEADMIN-335", "REQ-FEADMIN-336", "REQ-FEADMIN-337", "REQ-FEADMIN-338", "REQ-FEADMIN-339", "REQ-FEADMIN-340", "REQ-FEADMIN-341", "REQ-FEADMIN-342", "REQ-FEADMIN-343"]}
export const CONFIG_HINTS: Record<string, string> = {
  'agent_retry_limit': 'integer >= 1',
  'correction_loop_max_iterations': 'integer >= 1',
  'per_student_token_limit': 'integer >= 0 (0 disables limit)',
  'late_penalty_rate': 'float between 0.0 and 1.0 inclusive',
  'homework_weight': 'float > 0.0 and <= 1.0',
  'project_weight': 'float > 0.0 and <= 1.0',
  'session_inactivity_seconds': 'integer >= 1',
  'account_lockout_seconds': 'integer >= 1',
  'max_upload_bytes': 'integer >= 1024',
  'content_generation_timeout_seconds': 'integer >= 1',
  'audit_retention_days': 'integer >= 1',
  'notification_retention_days': 'integer >= 1',
  'consent_version': 'non-empty string',
  'anthropic_base_url': 'optional; empty or absolute http(s) URL'
}

// @{"req": ["REQ-FEADMIN-043", "REQ-FEADMIN-330"]}
// validateWeights checks that homework_weight and project_weight sum to 1.0 within a
// tolerance of 0.001 so that a minor floating-point representation error does not
// block a valid save.
export function validateWeights(config: Record<string, string>): string | null {
  const sum = WEIGHT_KEYS.reduce((acc, key) => acc + parseFloat(config[key] || '0'), 0)
  // >= matches the server exactly (config_handler.go fails when >= 0.001);
  // a strict > here would let a boundary value through that the server 422s.
  if (Math.abs(sum - 1.0) >= WEIGHT_TOLERANCE) {
    return `homework_weight + project_weight must equal 1.0`
  }
  return null
}

// @{"req": ["REQ-FEADMIN-510", "REQ-FEADMIN-511", "REQ-FEADMIN-512", "REQ-FEADMIN-513", "REQ-FEADMIN-514", "REQ-FEADMIN-515", "REQ-FEADMIN-516"]}
// EXPLANATIONS maps config keys to plain-language explanations displayed to admins.
// These explain what each key does, its impact, and note any reserved/inert keys honestly.
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
}
