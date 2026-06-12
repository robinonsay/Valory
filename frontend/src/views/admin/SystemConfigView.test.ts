// @{"req": ["REQ-FEADMIN-040", "REQ-FEADMIN-041", "REQ-FEADMIN-042", "REQ-FEADMIN-043", "REQ-FEADMIN-044", "REQ-FEADMIN-045", "REQ-FEADMIN-300", "REQ-FEADMIN-301", "REQ-FEADMIN-302", "REQ-FEADMIN-303", "REQ-FEADMIN-304", "REQ-FEADMIN-305", "REQ-FEADMIN-306", "REQ-FEADMIN-307", "REQ-FEADMIN-308", "REQ-FEADMIN-309", "REQ-FEADMIN-310", "REQ-FEADMIN-311", "REQ-FEADMIN-312", "REQ-FEADMIN-320", "REQ-FEADMIN-321", "REQ-FEADMIN-322", "REQ-FEADMIN-323", "REQ-FEADMIN-324", "REQ-FEADMIN-325", "REQ-FEADMIN-330", "REQ-FEADMIN-331", "REQ-FEADMIN-332", "REQ-FEADMIN-333", "REQ-FEADMIN-334", "REQ-FEADMIN-335", "REQ-FEADMIN-336", "REQ-FEADMIN-337", "REQ-FEADMIN-338", "REQ-FEADMIN-339", "REQ-FEADMIN-340", "REQ-FEADMIN-341", "REQ-FEADMIN-342", "REQ-FEADMIN-343"]}

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import SystemConfigView from './SystemConfigView.vue'
import { validateWeights } from './systemConfig'
import * as clientModule from '@/api/client'
import { useAuthStore } from '@/stores/auth'

vi.mock('@/api/client', () => ({
  get: vi.fn(),
  patch: vi.fn(),
  ApiError: class extends Error {
    status: number
    body: unknown
    constructor(status: number, body: unknown) {
      super(`HTTP ${status}`)
      this.name = 'ApiError'
      this.status = status
      this.body = body
    }
  }
}))

const { get, patch } = await import('@/api/client')

const FULL_CONFIG = {
  agent_retry_limit: '3',
  correction_loop_max_iterations: '5',
  per_student_token_limit: '500000',
  late_penalty_rate: '0.05',
  homework_weight: '0.7',
  project_weight: '0.3',
  session_inactivity_seconds: '1800',
  account_lockout_seconds: '900',
  max_upload_bytes: '10485760',
  content_generation_timeout_seconds: '300',
  audit_retention_days: '365',
  notification_retention_days: '90',
  consent_version: '1.0'
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
})

function mountWithAuth() {
  const auth = useAuthStore()
  auth.login('test-token', 'admin', Math.floor(Date.now() / 1000) + 3600)
  return mount(SystemConfigView)
}

async function flushAll() {
  await new Promise(resolve => setTimeout(resolve, 50))
}

describe('SystemConfigView', () => {
  // @{"req": ["REQ-FEADMIN-040", "REQ-FEADMIN-041", "REQ-FEADMIN-042"]}
  it('fetches config on mount and renders all 13 fields', async () => {
    vi.mocked(get).mockResolvedValue({ config: FULL_CONFIG })

    const wrapper = mountWithAuth()
    await flushAll()
    await wrapper.vm.$nextTick()

    expect(vi.mocked(get)).toHaveBeenCalledWith('/api/v1/admin/config', 'test-token')

    const inputs = wrapper.findAll('input.config-input')
    expect(inputs).toHaveLength(13)

    const expectedKeys = [
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
    ]

    for (const key of expectedKeys) {
      const input = wrapper.find(`#config-${key}`)
      expect(input.exists(), `Input for ${key} should exist`).toBe(true)
    }
  })

  // @{"req": ["REQ-FEADMIN-330"]}
  it('validateWeights returns null when weights sum to exactly 1.0', () => {
    const config: Record<string, string> = {
      ...FULL_CONFIG,
      homework_weight: '0.6',
      project_weight: '0.4'
    }
    expect(validateWeights(config)).toBeNull()
  })

  // @{"req": ["REQ-FEADMIN-330"]}
  it('validateWeights returns null when weights sum within tolerance (0.9999)', () => {
    const config: Record<string, string> = {
      ...FULL_CONFIG,
      homework_weight: '0.5',
      project_weight: '0.4999'
    }
    // 0.5 + 0.4999 = 0.9999, which is within 0.001 of 1.0
    expect(validateWeights(config)).toBeNull()
  })

  // @{"req": ["REQ-FEADMIN-330"]}
  it('validateWeights returns null when weights sum within tolerance (1.001)', () => {
    const config: Record<string, string> = {
      ...FULL_CONFIG,
      homework_weight: '0.501',
      project_weight: '0.5'
    }
    // 0.501 + 0.5 = 1.001 in floating-point; Math.abs(1.001 - 1.0) is
    // 0.0009999... which is strictly less than the 0.001 tolerance, so it passes.
    expect(validateWeights(config)).toBeNull()
  })

  // @{"req": ["REQ-FEADMIN-330"]}
  it('validateWeights returns error when sum is 0.998 (outside tolerance)', () => {
    const config: Record<string, string> = {
      ...FULL_CONFIG,
      homework_weight: '0.499',
      project_weight: '0.499'
    }
    // 0.499 + 0.499 = 0.998, distance from 1.0 is 0.002 > 0.001
    const result = validateWeights(config)
    expect(result).not.toBeNull()
    expect(result).toContain('homework_weight + project_weight must equal 1.0')
  })

  // @{"req": ["REQ-FEADMIN-330"]}
  it('validateWeights returns error when sum is 1.002 (outside tolerance)', () => {
    const config: Record<string, string> = {
      ...FULL_CONFIG,
      homework_weight: '0.501',
      project_weight: '0.501'
    }
    // 0.501 + 0.501 = 1.002, distance from 1.0 is 0.002 > 0.001
    const result = validateWeights(config)
    expect(result).not.toBeNull()
    expect(result).toContain('homework_weight + project_weight must equal 1.0')
  })

  // @{"req": ["REQ-FEADMIN-041", "REQ-FEADMIN-320", "REQ-FEADMIN-321"]}
  it('PATCH body contains only changed fields (delta-only, not full config)', async () => {
    vi.mocked(get).mockResolvedValue({ config: FULL_CONFIG })
    vi.mocked(patch).mockResolvedValue({ config: FULL_CONFIG })

    const wrapper = mountWithAuth()
    await flushAll()
    await wrapper.vm.$nextTick()

    const agentRetryLimitInput = wrapper.find('#config-agent_retry_limit')
    await agentRetryLimitInput.setValue('5')

    const saveButton = wrapper.find('.save-button')
    await saveButton.trigger('click')
    await flushAll()
    await wrapper.vm.$nextTick()

    expect(vi.mocked(patch)).toHaveBeenCalledTimes(1)
    const callArgs = vi.mocked(patch).mock.calls[0]
    const body = callArgs[1] as { config: Record<string, string> }

    expect(Object.keys(body.config)).toHaveLength(1)
    expect(body.config['agent_retry_limit']).toBe('5')
    expect(body.config['correction_loop_max_iterations']).toBeUndefined()
  })

  // @{"req": ["REQ-FEADMIN-041", "REQ-FEADMIN-320", "REQ-FEADMIN-321"]}
  it('PATCH body shape is {"config": {"key": "value"}} with all string values', async () => {
    vi.mocked(get).mockResolvedValue({ config: FULL_CONFIG })
    vi.mocked(patch).mockResolvedValue({ config: FULL_CONFIG })

    const wrapper = mountWithAuth()
    await flushAll()
    await wrapper.vm.$nextTick()

    const maxUploadBytesInput = wrapper.find('#config-max_upload_bytes')
    await maxUploadBytesInput.setValue('20971520')

    const saveButton = wrapper.find('.save-button')
    await saveButton.trigger('click')
    await flushAll()
    await wrapper.vm.$nextTick()

    expect(vi.mocked(patch)).toHaveBeenCalledTimes(1)
    const callArgs = vi.mocked(patch).mock.calls[0]

    expect(callArgs[0]).toBe('/api/v1/admin/config')

    const body = callArgs[1] as { config: Record<string, string> }
    expect(typeof body).toBe('object')
    expect(body).toHaveProperty('config')
    expect(typeof body.config).toBe('object')

    for (const [k, v] of Object.entries(body.config)) {
      expect(typeof k, `key ${k} must be string`).toBe('string')
      expect(typeof v, `value for ${k} must be string`).toBe('string')
    }
  })

  // @{"req": ["REQ-FEADMIN-043", "REQ-FEADMIN-330"]}
  it('validation error shown inline and PATCH not called', async () => {
    vi.mocked(get).mockResolvedValue({ config: FULL_CONFIG })

    const wrapper = mountWithAuth()
    await flushAll()
    await wrapper.vm.$nextTick()

    // Set weights that do not sum to 1.0 (sum = 0.5)
    await wrapper.find('#config-homework_weight').setValue('0.2')
    await wrapper.find('#config-project_weight').setValue('0.3')

    const saveButton = wrapper.find('.save-button')
    await saveButton.trigger('click')
    await wrapper.vm.$nextTick()

    expect(vi.mocked(patch)).not.toHaveBeenCalled()
    const errorBanners = wrapper.findAll('.weight-error')
    expect(errorBanners.length).toBeGreaterThan(0)
    expect(errorBanners[0].text()).toContain('homework_weight + project_weight must equal 1.0')
  })

  // @{"req": ["REQ-FEADMIN-044", "REQ-FEADMIN-324"]}
  it('success message shown after successful save', async () => {
    vi.mocked(get).mockResolvedValue({ config: FULL_CONFIG })
    vi.mocked(patch).mockResolvedValue({ config: FULL_CONFIG })

    const wrapper = mountWithAuth()
    await flushAll()
    await wrapper.vm.$nextTick()

    const agentRetryLimitInput = wrapper.find('#config-agent_retry_limit')
    await agentRetryLimitInput.setValue('7')

    const saveButton = wrapper.find('.save-button')
    await saveButton.trigger('click')
    await flushAll()
    await wrapper.vm.$nextTick()

    const successBanner = wrapper.find('.success-banner')
    expect(successBanner.exists()).toBe(true)
    expect(successBanner.text()).toContain('Configuration saved.')
  })

  // @{"req": ["REQ-FEADMIN-045", "REQ-FEADMIN-325"]}
  it('server 422 validation errors parsed and displayed per field', async () => {
    vi.mocked(get).mockResolvedValue({ config: FULL_CONFIG })

    const validationErrors = [
      'agent_retry_limit: agent_retry_limit must be an integer >= 1'
    ]
    const apiError = new (clientModule.ApiError as any)(422, {
      validation_errors: validationErrors
    })
    vi.mocked(patch).mockRejectedValue(apiError)

    const wrapper = mountWithAuth()
    await flushAll()
    await wrapper.vm.$nextTick()

    // Change a valid field to trigger PATCH
    await wrapper.find('#config-agent_retry_limit').setValue('5')

    const saveButton = wrapper.find('.save-button')
    await saveButton.trigger('click')
    await flushAll()
    await wrapper.vm.$nextTick()

    const fieldErrors = wrapper.findAll('.field-error')
    expect(fieldErrors.length).toBeGreaterThan(0)
    expect(fieldErrors[0].text()).toContain('agent_retry_limit must be an integer >= 1')
  })
})
