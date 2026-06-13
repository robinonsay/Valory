// @{"req": ["REQ-FEPROFILE-002"]}

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import ProfileView from './ProfileView.vue'
import * as clientModule from '@/api/client'

describe('ProfileView', () => {
  let router: any

  beforeEach(() => {
    setActivePinia(createPinia())
    router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/profile', component: ProfileView },
        { path: '/onboarding', component: { template: '<div>Onboarding</div>' } }
      ]
    })
    vi.restoreAllMocks()
  })

  // @{"verifies": ["REQ-FEPROFILE-002"]}
  it('loads and displays existing profile', async () => {
    const profileSummary = 'I prefer example-first explanations and work 10 hours per week.'
    const mockGet = vi.spyOn(clientModule, 'get').mockResolvedValueOnce({
      student_id: 'student-1',
      summary: profileSummary,
      source: 'onboarding',
      created_at: '2026-06-12T10:00:00Z',
      updated_at: '2026-06-12T10:00:00Z'
    })

    const wrapper = mount(ProfileView, {
      global: {
        plugins: [router]
      }
    })

    await flushPromises()

    expect(mockGet).toHaveBeenCalledWith('/api/v1/profile')
    const textarea = wrapper.find('.profile-textarea') as any
    expect(textarea.element.value).toBe(profileSummary)
    expect(wrapper.text()).toContain('Source: onboarding')
  })

  // @{"verifies": ["REQ-FEPROFILE-002"]}
  it('shows "no profile yet" state when 404 is returned', async () => {
    vi.spyOn(clientModule, 'get').mockRejectedValueOnce(
      new clientModule.ApiError(404, { error: 'NOT_FOUND' })
    )

    const wrapper = mount(ProfileView, {
      global: {
        plugins: [router]
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain("You haven't completed onboarding yet")
  })

  // @{"verifies": ["REQ-FEPROFILE-002"]}
  it('allows editing and saving profile summary', async () => {
    const originalSummary = 'Original profile text.'
    const newSummary = 'Updated profile text with new preferences.'

    vi.spyOn(clientModule, 'get').mockResolvedValueOnce({
      student_id: 'student-1',
      summary: originalSummary,
      source: 'onboarding',
      created_at: '2026-06-12T10:00:00Z',
      updated_at: '2026-06-12T10:00:00Z'
    })

    const mockPut = vi.spyOn(clientModule, 'put').mockResolvedValueOnce({
      student_id: 'student-1',
      summary: newSummary,
      source: 'manual_edit',
      created_at: '2026-06-12T10:00:00Z',
      updated_at: '2026-06-13T10:00:00Z'
    })

    const wrapper = mount(ProfileView, {
      global: {
        plugins: [router]
      }
    })

    await flushPromises()

    const textarea = wrapper.find('.profile-textarea') as any
    expect(textarea.element.value).toBe(originalSummary)

    textarea.element.value = newSummary
    await textarea.trigger('input')

    const saveButton = wrapper.find('.primary-button')
    await saveButton.trigger('click')

    await flushPromises()

    expect(mockPut).toHaveBeenCalledWith('/api/v1/profile', {
      summary: newSummary
    })
  })

  // @{"verifies": ["REQ-FEPROFILE-002"]}
  it('shows success message after saving', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValueOnce({
      student_id: 'student-1',
      summary: 'Original text.',
      source: 'onboarding',
      created_at: '2026-06-12T10:00:00Z',
      updated_at: '2026-06-12T10:00:00Z'
    })

    vi.spyOn(clientModule, 'put').mockResolvedValueOnce({
      student_id: 'student-1',
      summary: 'Updated text.',
      source: 'manual_edit',
      created_at: '2026-06-12T10:00:00Z',
      updated_at: '2026-06-13T10:00:00Z'
    })

    const wrapper = mount(ProfileView, {
      global: {
        plugins: [router]
      }
    })

    await flushPromises()

    const textarea = wrapper.find('.profile-textarea') as any
    textarea.element.value = 'Updated text.'
    await textarea.trigger('input')

    await wrapper.find('.primary-button').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('Profile updated successfully!')
  })

  // @{"verifies": ["REQ-FEPROFILE-002"]}
  it('displays re-run onboarding button', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValueOnce({
      student_id: 'student-1',
      summary: 'Existing profile.',
      source: 'onboarding',
      created_at: '2026-06-12T10:00:00Z',
      updated_at: '2026-06-12T10:00:00Z'
    })

    const wrapper = mount(ProfileView, {
      global: {
        plugins: [router]
      }
    })

    await flushPromises()

    const buttons = wrapper.findAll('button')
    const rerunButton = buttons.find(b => b.text().includes('Re-run Onboarding'))
    expect(rerunButton).toBeDefined()
  })

  // @{"verifies": ["REQ-FEPROFILE-002"]}
  it('starts onboarding when re-run is confirmed', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValueOnce({
      student_id: 'student-1',
      summary: 'Existing profile.',
      source: 'onboarding',
      created_at: '2026-06-12T10:00:00Z',
      updated_at: '2026-06-12T10:00:00Z'
    })

    const mockPost = vi.spyOn(clientModule, 'post').mockResolvedValueOnce({
      session_active: true,
      first_message: 'Let\'s start over.'
    })

    const wrapper = mount(ProfileView, {
      global: {
        plugins: [router]
      }
    })

    await flushPromises()

    const buttons = wrapper.findAll('button')
    const rerunButton = buttons.find(b => b.text().includes('Re-run Onboarding'))
    await rerunButton?.trigger('click')

    await flushPromises()

    // Modal should appear
    expect(wrapper.find('.modal-overlay').exists()).toBe(true)

    // Click continue in the modal
    const modalButtons = wrapper.findAll('.modal-buttons button')
    await modalButtons[0].trigger('click')

    await flushPromises()

    expect(mockPost).toHaveBeenCalledWith('/api/v1/profile/onboarding/start', {})
  })

  // @{"verifies": ["REQ-FEPROFILE-002"]}
  it('shows error when save fails', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValueOnce({
      student_id: 'student-1',
      summary: 'Original text.',
      source: 'onboarding',
      created_at: '2026-06-12T10:00:00Z',
      updated_at: '2026-06-12T10:00:00Z'
    })

    vi.spyOn(clientModule, 'put').mockRejectedValueOnce(
      new clientModule.ApiError(500, { error: 'Server error' })
    )

    const wrapper = mount(ProfileView, {
      global: {
        plugins: [router]
      }
    })

    await flushPromises()

    const textarea = wrapper.find('.profile-textarea') as any
    textarea.element.value = 'Updated text.'
    await textarea.trigger('input')

    await wrapper.find('.primary-button').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('Failed to save profile')
  })

  it('disables save button when no changes are made', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValueOnce({
      student_id: 'student-1',
      summary: 'Original text.',
      source: 'onboarding',
      created_at: '2026-06-12T10:00:00Z',
      updated_at: '2026-06-12T10:00:00Z'
    })

    const wrapper = mount(ProfileView, {
      global: {
        plugins: [router]
      }
    })

    await flushPromises()

    const buttons = wrapper.findAll('.primary-button')
    const saveButton = buttons.find(b => b.text().includes('Save Changes'))
    expect(saveButton?.attributes('disabled')).toBeDefined()
  })

  it('shows character count for textarea', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValueOnce({
      student_id: 'student-1',
      summary: 'Test text.',
      source: 'onboarding',
      created_at: '2026-06-12T10:00:00Z',
      updated_at: '2026-06-12T10:00:00Z'
    })

    const wrapper = mount(ProfileView, {
      global: {
        plugins: [router]
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain('/ 2000 characters')
  })

  it('shows start onboarding button when no profile exists', async () => {
    vi.spyOn(clientModule, 'get').mockRejectedValueOnce(
      new clientModule.ApiError(404, { error: 'NOT_FOUND' })
    )

    const wrapper = mount(ProfileView, {
      global: {
        plugins: [router]
      }
    })

    await flushPromises()

    const startButton = wrapper.find('button')
    expect(startButton.text()).toContain('Start Onboarding')
  })
})
