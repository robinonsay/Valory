// @{"req": ["REQ-FEADMIN-045", "REQ-FEADMIN-046", "REQ-FEADMIN-047", "REQ-FEADMIN-050", "REQ-FEADMIN-055", "REQ-FEADMIN-210", "REQ-FEADMIN-215", "REQ-FEADMIN-220", "REQ-FEADMIN-230", "REQ-FEADMIN-240", "REQ-FEADMIN-250", "REQ-FEADMIN-260"]}

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
  llm_model: 'claude-3-5-sonnet-20241022',
  llm_temperature: '0.7',
  llm_max_tokens: '4096',
  grading_weight_homework: '0.334',
  grading_weight_participation: '0.333',
  grading_weight_final: '0.333',
  course_max_sections: '10',
  homework_max_submissions: '3',
  late_penalty_percent: '10',
  max_course_duration_days: '90',
  notification_poll_interval_seconds: '30',
  export_formats: 'pdf,html',
  max_upload_size_bytes: '10485760'
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
  // @{"req": ["REQ-FEADMIN-045", "REQ-FEADMIN-046", "REQ-FEADMIN-210"]}
  it('fetches config on mount and renders all 13 fields', async () => {
    vi.mocked(get).mockResolvedValue({ config: FULL_CONFIG })

    const wrapper = mountWithAuth()
    await flushAll()
    await wrapper.vm.$nextTick()

    expect(vi.mocked(get)).toHaveBeenCalledWith('/api/v1/admin/config', 'test-token')

    const inputs = wrapper.findAll('input.config-input')
    expect(inputs).toHaveLength(13)

    const expectedKeys = [
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
    ]

    for (const key of expectedKeys) {
      const input = wrapper.find(`#config-${key}`)
      expect(input.exists(), `Input for ${key} should exist`).toBe(true)
    }
  })

  // @{"req": ["REQ-FEADMIN-220"]}
  it('validateWeights returns null when weights sum to exactly 1.0', () => {
    const config: Record<string, string> = {
      ...FULL_CONFIG,
      grading_weight_homework: '0.5',
      grading_weight_participation: '0.3',
      grading_weight_final: '0.2'
    }
    expect(validateWeights(config)).toBeNull()
  })

  // @{"req": ["REQ-FEADMIN-220", "REQ-FEADMIN-230"]}
  it('validateWeights returns null when weights sum within tolerance (0.999)', () => {
    const config: Record<string, string> = {
      ...FULL_CONFIG,
      grading_weight_homework: '0.333',
      grading_weight_participation: '0.333',
      grading_weight_final: '0.333'
    }
    // 0.333 * 3 = 0.999, which is within 0.001 of 1.0
    expect(validateWeights(config)).toBeNull()
  })

  // @{"req": ["REQ-FEADMIN-220", "REQ-FEADMIN-230"]}
  it('validateWeights returns null when weights sum within tolerance (1.001)', () => {
    const config: Record<string, string> = {
      ...FULL_CONFIG,
      grading_weight_homework: '0.4',
      grading_weight_participation: '0.3',
      grading_weight_final: '0.301'
    }
    // 0.4 + 0.3 + 0.301 = 1.001 in floating-point; Math.abs(1.001 - 1.0) is
    // 0.0009999... which is strictly less than the 0.001 tolerance, so it passes.
    expect(validateWeights(config)).toBeNull()
  })

  // @{"req": ["REQ-FEADMIN-220", "REQ-FEADMIN-230"]}
  it('validateWeights returns error when sum is 0.998 (outside tolerance)', () => {
    const config: Record<string, string> = {
      ...FULL_CONFIG,
      grading_weight_homework: '0.332',
      grading_weight_participation: '0.333',
      grading_weight_final: '0.333'
    }
    // 0.332 + 0.333 + 0.333 = 0.998, distance from 1.0 is 0.002 > 0.001
    const result = validateWeights(config)
    expect(result).not.toBeNull()
    expect(result).toContain('Grading weights must sum to 1.0')
  })

  // @{"req": ["REQ-FEADMIN-220", "REQ-FEADMIN-230"]}
  it('validateWeights returns error when sum is 1.002 (outside tolerance)', () => {
    const config: Record<string, string> = {
      ...FULL_CONFIG,
      grading_weight_homework: '0.335',
      grading_weight_participation: '0.334',
      grading_weight_final: '0.333'
    }
    // 0.335 + 0.334 + 0.333 = 1.002, distance from 1.0 is 0.002 > 0.001
    const result = validateWeights(config)
    expect(result).not.toBeNull()
    expect(result).toContain('Grading weights must sum to 1.0')
  })

  // @{"req": ["REQ-FEADMIN-240", "REQ-FEADMIN-250"]}
  it('PATCH body contains only changed fields (delta-only, not full config)', async () => {
    vi.mocked(get).mockResolvedValue({ config: FULL_CONFIG })
    vi.mocked(patch).mockResolvedValue({ config: FULL_CONFIG })

    const wrapper = mountWithAuth()
    await flushAll()
    await wrapper.vm.$nextTick()

    const llmModelInput = wrapper.find('#config-llm_model')
    await llmModelInput.setValue('claude-3-opus-20240229')

    const saveButton = wrapper.find('.save-button')
    await saveButton.trigger('click')
    await flushAll()
    await wrapper.vm.$nextTick()

    expect(vi.mocked(patch)).toHaveBeenCalledTimes(1)
    const callArgs = vi.mocked(patch).mock.calls[0]
    const body = callArgs[1] as { config: Record<string, string> }

    expect(Object.keys(body.config)).toHaveLength(1)
    expect(body.config['llm_model']).toBe('claude-3-opus-20240229')
    expect(body.config['llm_temperature']).toBeUndefined()
  })

  // @{"req": ["REQ-FEADMIN-240", "REQ-FEADMIN-250", "REQ-FEADMIN-260"]}
  it('PATCH body shape is {"config": {"key": "value"}} with all string values', async () => {
    vi.mocked(get).mockResolvedValue({ config: FULL_CONFIG })
    vi.mocked(patch).mockResolvedValue({ config: FULL_CONFIG })

    const wrapper = mountWithAuth()
    await flushAll()
    await wrapper.vm.$nextTick()

    const llmMaxTokensInput = wrapper.find('#config-llm_max_tokens')
    await llmMaxTokensInput.setValue('8192')

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

  // @{"req": ["REQ-FEADMIN-220", "REQ-FEADMIN-230"]}
  it('validation error shown inline and PATCH not called', async () => {
    vi.mocked(get).mockResolvedValue({ config: FULL_CONFIG })

    const wrapper = mountWithAuth()
    await flushAll()
    await wrapper.vm.$nextTick()

    // Set weights that do not sum to 1.0 (sum = 0.5)
    await wrapper.find('#config-grading_weight_homework').setValue('0.2')
    await wrapper.find('#config-grading_weight_participation').setValue('0.1')
    await wrapper.find('#config-grading_weight_final').setValue('0.2')

    const saveButton = wrapper.find('.save-button')
    await saveButton.trigger('click')
    await wrapper.vm.$nextTick()

    expect(vi.mocked(patch)).not.toHaveBeenCalled()
    const errorBanners = wrapper.findAll('.weight-error')
    expect(errorBanners.length).toBeGreaterThan(0)
    expect(errorBanners[0].text()).toContain('Grading weights must sum to 1.0')
  })

  // @{"req": ["REQ-FEADMIN-250"]}
  it('success message shown after successful save', async () => {
    vi.mocked(get).mockResolvedValue({ config: FULL_CONFIG })
    vi.mocked(patch).mockResolvedValue({ config: FULL_CONFIG })

    const wrapper = mountWithAuth()
    await flushAll()
    await wrapper.vm.$nextTick()

    const llmModelInput = wrapper.find('#config-llm_model')
    await llmModelInput.setValue('claude-3-haiku-20240307')

    const saveButton = wrapper.find('.save-button')
    await saveButton.trigger('click')
    await flushAll()
    await wrapper.vm.$nextTick()

    const successBanner = wrapper.find('.success-banner')
    expect(successBanner.exists()).toBe(true)
    expect(successBanner.text()).toContain('Configuration saved.')
  })
})
