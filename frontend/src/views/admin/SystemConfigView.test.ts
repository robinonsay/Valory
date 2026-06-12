// @{"req": ["REQ-FEADMIN-040", "REQ-FEADMIN-041", "REQ-FEADMIN-042", "REQ-FEADMIN-043", "REQ-FEADMIN-044", "REQ-FEADMIN-045", "REQ-FEADMIN-300", "REQ-FEADMIN-301", "REQ-FEADMIN-302", "REQ-FEADMIN-303", "REQ-FEADMIN-304", "REQ-FEADMIN-305", "REQ-FEADMIN-306", "REQ-FEADMIN-307", "REQ-FEADMIN-308", "REQ-FEADMIN-309", "REQ-FEADMIN-310", "REQ-FEADMIN-311", "REQ-FEADMIN-312", "REQ-FEADMIN-320", "REQ-FEADMIN-321", "REQ-FEADMIN-322", "REQ-FEADMIN-323", "REQ-FEADMIN-324", "REQ-FEADMIN-325", "REQ-FEADMIN-330", "REQ-FEADMIN-331", "REQ-FEADMIN-332", "REQ-FEADMIN-333", "REQ-FEADMIN-334", "REQ-FEADMIN-335", "REQ-FEADMIN-336", "REQ-FEADMIN-337", "REQ-FEADMIN-338", "REQ-FEADMIN-339", "REQ-FEADMIN-340", "REQ-FEADMIN-341", "REQ-FEADMIN-342", "REQ-FEADMIN-343", "REQ-FEADMIN-500", "REQ-FEADMIN-501", "REQ-FEADMIN-502", "REQ-FEADMIN-503", "REQ-FEADMIN-504", "REQ-FEADMIN-510", "REQ-FEADMIN-511", "REQ-FEADMIN-512", "REQ-FEADMIN-513", "REQ-FEADMIN-514", "REQ-FEADMIN-515", "REQ-FEADMIN-516"]}

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import SystemConfigView from './SystemConfigView.vue'
import { validateWeights } from './systemConfig'
import * as clientModule from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { useSystemConfigStore, type SecretStatus } from '@/stores/admin/systemConfig'

vi.mock('@/api/client', () => ({
  get: vi.fn(),
  patch: vi.fn(),
  put: vi.fn(),
  del: vi.fn(),
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

const { get, patch, put, del } = await import('@/api/client')

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
  auth.$patch({ role: 'admin', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })
  return mount(SystemConfigView)
}

const EMPTY_SECRETS: SecretStatus[] = [
  {
    name: 'anthropic_api_key',
    configured: false,
    last4: null,
    updated_at: null,
    source: 'none'
  },
  {
    name: 'brave_api_key',
    configured: false,
    last4: null,
    updated_at: null,
    source: 'none'
  }
]

const CONFIGURED_SECRETS: SecretStatus[] = [
  {
    name: 'anthropic_api_key',
    configured: true,
    last4: 'sk-5678',
    updated_at: '2024-06-12T10:00:00Z',
    source: 'managed'
  },
  {
    name: 'brave_api_key',
    configured: false,
    last4: null,
    updated_at: null,
    source: 'none'
  }
]

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

    expect(vi.mocked(get)).toHaveBeenCalledWith('/api/v1/admin/config')

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

  // @{"req": ["REQ-FEADMIN-500", "REQ-FEADMIN-503"]}
  it('secrets section renders with status badges from API response', async () => {
    vi.mocked(get).mockResolvedValue({ config: FULL_CONFIG })
    vi.mocked(get).mockImplementation((path) => {
      if (path === '/api/v1/admin/secrets') {
        return Promise.resolve({ secrets: CONFIGURED_SECRETS })
      }
      return Promise.resolve({ config: FULL_CONFIG })
    })

    const wrapper = mountWithAuth()
    await flushAll()
    await wrapper.vm.$nextTick()

    const secretCard = wrapper.find('.secrets-card')
    expect(secretCard.exists()).toBe(true)

    const badges = wrapper.findAll('.secret-badge')
    expect(badges.length).toBe(2)
    expect(badges[0].text()).toContain('Configured (••••sk-5678)')
    expect(badges[1].text()).toContain('Not configured')
  })

  // @{"req": ["REQ-FEADMIN-501", "REQ-FEADMIN-502"]}
  it('secret input is type=password and never prefilled with saved value', async () => {
    vi.mocked(get).mockImplementation((path) => {
      if (path === '/api/v1/admin/secrets') {
        return Promise.resolve({ secrets: CONFIGURED_SECRETS })
      }
      return Promise.resolve({ config: FULL_CONFIG })
    })

    const wrapper = mountWithAuth()
    await flushAll()
    await wrapper.vm.$nextTick()

    const secretInput = wrapper.find('#secret-anthropic_api_key')
    expect(secretInput.exists()).toBe(true)
    expect(secretInput.attributes('type')).toBe('password')
    expect(secretInput.attributes('autocomplete')).toBe('new-password')
    expect(secretInput.element.value).toBe('')
  })

  // @{"req": ["REQ-FEADMIN-501", "REQ-FEADMIN-502"]}
  it('save secret flow: PUT request sent, input cleared, secrets refetched', async () => {
    let secretsFetchCount = 0
    vi.mocked(get).mockImplementation((path) => {
      if (path === '/api/v1/admin/secrets') {
        secretsFetchCount++
        return Promise.resolve({ secrets: CONFIGURED_SECRETS })
      }
      return Promise.resolve({ config: FULL_CONFIG })
    })
    vi.mocked(put).mockResolvedValue(undefined)

    const wrapper = mountWithAuth()
    await flushAll()
    await wrapper.vm.$nextTick()

    const secretInput = wrapper.find('#secret-anthropic_api_key')
    await secretInput.setValue('test-api-key-12345')

    const saveButton = wrapper.find('.save-secret-button')
    await saveButton.trigger('click')
    await flushAll()
    await wrapper.vm.$nextTick()

    expect(vi.mocked(put)).toHaveBeenCalledWith(
      '/api/v1/admin/secrets/anthropic_api_key',
      { value: 'test-api-key-12345' }
    )

    expect(secretsFetchCount).toBe(2)

    const updatedInput = wrapper.find('#secret-anthropic_api_key')
    expect(updatedInput.element.value).toBe('')
  })

  // @{"req": ["REQ-FEADMIN-504"]}
  it('clear button visible only for managed secrets and deletes after confirmation', async () => {
    vi.mocked(get).mockImplementation((path) => {
      if (path === '/api/v1/admin/secrets') {
        return Promise.resolve({ secrets: CONFIGURED_SECRETS })
      }
      return Promise.resolve({ config: FULL_CONFIG })
    })
    vi.mocked(del).mockResolvedValue(undefined)

    const wrapper = mountWithAuth()
    await flushAll()
    await wrapper.vm.$nextTick()

    const clearButtons = wrapper.findAll('.delete-secret-button')
    expect(clearButtons).toHaveLength(1)
    expect(clearButtons[0].text()).toBe('Clear')

    window.confirm = vi.fn().mockReturnValue(true)
    await clearButtons[0].trigger('click')
    await flushAll()

    expect(vi.mocked(del)).toHaveBeenCalledWith('/api/v1/admin/secrets/anthropic_api_key')
  })

  // @{"req": ["REQ-FEADMIN-510"]}
  it('all 13 config fields render with explanation blocks', async () => {
    vi.mocked(get).mockResolvedValue({ config: FULL_CONFIG })
    vi.mocked(get).mockImplementation((path) => {
      if (path === '/api/v1/admin/secrets') {
        return Promise.resolve({ secrets: EMPTY_SECRETS })
      }
      return Promise.resolve({ config: FULL_CONFIG })
    })

    const wrapper = mountWithAuth()
    await flushAll()
    await wrapper.vm.$nextTick()

    const toggleButtons = wrapper.findAll('.explanation-toggle')
    expect(toggleButtons).toHaveLength(13)
  })

  // @{"req": ["REQ-FEADMIN-511"]}
  it('reserved keys show honest disclosure about env-override behavior', async () => {
    vi.mocked(get).mockResolvedValue({ config: FULL_CONFIG })
    vi.mocked(get).mockImplementation((path) => {
      if (path === '/api/v1/admin/secrets') {
        return Promise.resolve({ secrets: EMPTY_SECRETS })
      }
      return Promise.resolve({ config: FULL_CONFIG })
    })

    const wrapper = mountWithAuth()
    await flushAll()
    await wrapper.vm.$nextTick()

    const toggleButtons = wrapper.findAll('.explanation-toggle')
    const sessionInactivityIndex = 6

    await toggleButtons[sessionInactivityIndex].trigger('click')
    await wrapper.vm.$nextTick()

    const allExplanationBlocks = wrapper.findAll('.explanation-block')
    let foundSession = false
    for (const block of allExplanationBlocks) {
      const text = block.text()
      if (text.includes('AUTH_INACTIVITY_PERIOD')) {
        foundSession = true
        expect(text).toContain('Changing this value has no effect on the running server')
        expect(text).toContain('AUTH_INACTIVITY_PERIOD')
        break
      }
    }
    expect(foundSession).toBe(true)
  })

  // @{"req": ["REQ-FEADMIN-512"]}
  it('audit_retention_days explanation discloses missing purge worker', async () => {
    vi.mocked(get).mockResolvedValue({ config: FULL_CONFIG })
    vi.mocked(get).mockImplementation((path) => {
      if (path === '/api/v1/admin/secrets') {
        return Promise.resolve({ secrets: EMPTY_SECRETS })
      }
      return Promise.resolve({ config: FULL_CONFIG })
    })

    const wrapper = mountWithAuth()
    await flushAll()
    await wrapper.vm.$nextTick()

    const toggleButtons = wrapper.findAll('.explanation-toggle')
    const auditRetentionIndex = 10
    await toggleButtons[auditRetentionIndex].trigger('click')
    await wrapper.vm.$nextTick()

    const allExplanationBlocks = wrapper.findAll('.explanation-block')
    let foundAudit = false
    for (const block of allExplanationBlocks) {
      const text = block.text()
      if (text.includes('No automated purge')) {
        foundAudit = true
        expect(text).toContain('No automated purge is currently implemented')
        expect(text).toContain('no background worker')
        break
      }
    }
    expect(foundAudit).toBe(true)
  })
})
