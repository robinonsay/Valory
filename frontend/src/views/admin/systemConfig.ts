// @{"req": ["REQ-FEADMIN-045", "REQ-FEADMIN-046", "REQ-FEADMIN-047", "REQ-FEADMIN-050", "REQ-FEADMIN-055", "REQ-FEADMIN-210", "REQ-FEADMIN-215", "REQ-FEADMIN-220", "REQ-FEADMIN-230", "REQ-FEADMIN-240", "REQ-FEADMIN-250", "REQ-FEADMIN-260"]}

export const CONFIG_KEYS = [
  'llm_model',
  'llm_temperature',
  'llm_max_tokens',
  'grading_weight_homework',
  'grading_weight_participation',
  'grading_weight_final',
  'course_max_sections',
  'homework_max_submissions',
  'late_penalty_percent',
  'max_course_duration_days',
  'notification_poll_interval_seconds',
  'export_formats',
  'max_upload_size_bytes'
] as const

export const WEIGHT_KEYS = [
  'grading_weight_homework',
  'grading_weight_participation',
  'grading_weight_final'
] as const

export const WEIGHT_TOLERANCE = 0.001

// @{"req": ["REQ-FEADMIN-220", "REQ-FEADMIN-230"]}
// validateWeights checks that the three grading weight fields sum to 1.0 within a
// tolerance of 0.001 so that a minor floating-point representation error does not
// block a valid save.
export function validateWeights(config: Record<string, string>): string | null {
  const sum = WEIGHT_KEYS.reduce((acc, key) => acc + parseFloat(config[key] || '0'), 0)
  if (Math.abs(sum - 1.0) > WEIGHT_TOLERANCE) {
    return `Grading weights must sum to 1.0 (currently ${sum.toFixed(4)})`
  }
  return null
}
