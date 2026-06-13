// @{"req": ["REQ-FEADMIN-040", "REQ-FEADMIN-041", "REQ-FEADMIN-042", "REQ-FEADMIN-043", "REQ-FEADMIN-044", "REQ-FEADMIN-045", "REQ-FEADMIN-300", "REQ-FEADMIN-301", "REQ-FEADMIN-302", "REQ-FEADMIN-303", "REQ-FEADMIN-304", "REQ-FEADMIN-305", "REQ-FEADMIN-306", "REQ-FEADMIN-307", "REQ-FEADMIN-308", "REQ-FEADMIN-309", "REQ-FEADMIN-310", "REQ-FEADMIN-311", "REQ-FEADMIN-312", "REQ-FEADMIN-330", "REQ-FEADMIN-510", "REQ-FEADMIN-511", "REQ-FEADMIN-512", "REQ-FEADMIN-513", "REQ-FEADMIN-514", "REQ-FEADMIN-515", "REQ-FEADMIN-516", "REQ-FEADMIN-600", "REQ-FEADMIN-601", "REQ-FEADMIN-602", "REQ-FEADMIN-603", "REQ-FEADMIN-604"]}

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
  'professor_max_tokens',
  'audit_retention_days',
  'notification_retention_days',
  'consent_version',
  'anthropic_base_url',
  'two_factor_enabled',
  'smtp_host',
  'smtp_port',
  'smtp_from',
  'smtp_username',
  'smtp_encryption'
] as const

// @{"req": ["REQ-FEADMIN-600", "REQ-FEADMIN-601", "REQ-FEADMIN-602", "REQ-FEADMIN-603", "REQ-FEADMIN-604"]}
export const EMAIL_CONFIG_KEYS = [
  'smtp_host',
  'smtp_port',
  'smtp_from',
  'smtp_username',
  'smtp_encryption'
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
  'professor_max_tokens': 'Section Generation Max Output Tokens',
  'audit_retention_days': 'Audit Retention (days)',
  'notification_retention_days': 'Notification Retention (days)',
  'consent_version': 'Consent Version',
  'anthropic_base_url': 'Anthropic API Endpoint URL',
  'two_factor_enabled': 'Enable two-factor authentication',
  'smtp_host': 'SMTP Server Host',
  'smtp_port': 'SMTP Server Port',
  'smtp_from': 'SMTP From Address',
  'smtp_username': 'SMTP Username',
  'smtp_encryption': 'Encryption Mode'
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
  'professor_max_tokens': 'integer between 1024 and 16384',
  'audit_retention_days': 'integer >= 1',
  'notification_retention_days': 'integer >= 1',
  'consent_version': 'non-empty string',
  'anthropic_base_url': 'optional; empty or absolute http(s) URL',
  'two_factor_enabled': 'true or false',
  'smtp_host': 'hostname or IP; leave empty to disable',
  'smtp_port': 'integer 1–65535; default 587',
  'smtp_from': 'email address',
  'smtp_username': 'leave empty for no AUTH',
  'smtp_encryption': 'none, starttls, or tls'
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

  'smtp_host':
    'The hostname or IP address of your SMTP server. Leave empty to disable email. Examples: smtp.gmail.com (Gmail with an App Password), email-smtp.us-east-1.amazonaws.com (Amazon SES), localhost (local postfix/exim relay — no auth needed, use encryption: none).',

  'smtp_port':
    'The SMTP server port. Common values: 587 (STARTTLS — recommended for external providers), 465 (implicit TLS / SMTPS), 25 (unauthenticated local relay). Default: 587.',

  'smtp_from':
    'The envelope sender address shown in the From: header of all outbound emails (e.g. noreply@yourschool.edu). Must be an address your SMTP server is authorized to send from. Leave empty if SMTP host is not configured.',

  'smtp_username':
    'The username for SMTP authentication. Leave empty to skip authentication entirely — required for localhost relays (postfix/exim) that do not need credentials. For Gmail, enter your full Gmail address and use an App Password (not your account password).',

  'smtp_encryption':
    'How the connection to the SMTP server is secured. STARTTLS (recommended): starts a plain connection then upgrades it to TLS — use this for most external providers on port 587. TLS: wraps the entire connection in TLS from the start (implicit TLS) — use this for providers that require port 465. None: no encryption — only safe for a loopback relay on localhost. Using None over a routable network sends credentials and message content in plaintext.',

  'smtp_password':
    'The SMTP password or app-specific password for authentication. If set here, this value takes precedence over the SMTP_PASSWORD environment variable. Leave the username empty if your relay does not require authentication. For Gmail: generate an App Password in your Google Account security settings — do NOT use your Gmail account password here. Changes take effect immediately on the next email send.',

  'two_factor_enabled':
    '2FA requires all users to enter a one-time code sent to their email address when signing in. Before enabling: email must be configured and a test message must have been sent successfully, and all admin accounts must have an email address on file.',
}
