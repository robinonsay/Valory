// @{"req": ["REQ-FEADMIN-040", "REQ-FEADMIN-041", "REQ-FEADMIN-042", "REQ-FEADMIN-043", "REQ-FEADMIN-044", "REQ-FEADMIN-045", "REQ-FEADMIN-300", "REQ-FEADMIN-301", "REQ-FEADMIN-302", "REQ-FEADMIN-303", "REQ-FEADMIN-304", "REQ-FEADMIN-305", "REQ-FEADMIN-306", "REQ-FEADMIN-307", "REQ-FEADMIN-308", "REQ-FEADMIN-309", "REQ-FEADMIN-310", "REQ-FEADMIN-311", "REQ-FEADMIN-312", "REQ-FEADMIN-330"]}

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
  'consent_version'
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
  'consent_version': 'Consent Version'
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
  'consent_version': 'non-empty string'
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
