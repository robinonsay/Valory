// @{"req": ["REQ-FECONTENT-050", "REQ-FECONTENT-051", "REQ-FECONTENT-052", "REQ-FECONTENT-053", "REQ-FECONTENT-165", "REQ-FECONTENT-170", "REQ-FECONTENT-175", "REQ-FECONTENT-180"]}
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import BadgesView from './BadgesView.vue'
import { setActivePinia, createPinia } from 'pinia'
import { useAuthStore } from '@/stores/auth'
import * as clientModule from '@/api/client'

beforeEach(() => {
  setActivePinia(createPinia())
  vi.restoreAllMocks()
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('BadgesView', () => {
  // @{"req": ["REQ-FECONTENT-050"]}
  it('fetches badges from GET /api/v1/users/me/badges on mount', async () => {
    const mockBadges = [
      {
        id: 'badge-1',
        badge_id: 'def-1',
        awarded_at: '2024-01-15T10:00:00Z',
        redeemed_for_submission_id: null,
        badge_name: 'First Submission',
        badge_reward: 'Late Days',
        badge_reward_value: 1
      }
    ]

    const mockGet = vi.spyOn(clientModule, 'get').mockResolvedValue(mockBadges)

    const authStore = useAuthStore()
    authStore.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    const wrapper = mount(BadgesView, {
      global: {
        stubs: {
          RouterView: true
        }
      }
    })

    await flushPromises()

    expect(mockGet).toHaveBeenCalledWith('/api/v1/users/me/badges', 'test-token')
  })

  // @{"req": ["REQ-FECONTENT-051", "REQ-FECONTENT-170"]}
  it('renders badge cards with name, reward, and date', async () => {
    const badges = [
      {
        id: 'badge-1',
        badge_id: 'def-1',
        awarded_at: '2024-01-15T10:00:00Z',
        redeemed_for_submission_id: null,
        badge_name: 'First Submission',
        badge_reward: 'Late Days',
        badge_reward_value: 2
      },
      {
        id: 'badge-2',
        badge_id: 'def-2',
        awarded_at: '2024-02-20T14:30:00Z',
        redeemed_for_submission_id: 'sub-456',
        badge_name: 'Perfect Score',
        badge_reward: 'Grade Improvement',
        badge_reward_value: 5
      }
    ]

    vi.spyOn(clientModule, 'get').mockResolvedValue(badges)

    const authStore = useAuthStore()
    authStore.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    const wrapper = mount(BadgesView, {
      global: {
        stubs: {
          RouterView: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain('First Submission')
    expect(wrapper.text()).toContain('Late Days: 2')
    expect(wrapper.text()).toContain('Perfect Score')
    expect(wrapper.text()).toContain('Grade Improvement: 5')
  })

  // @{"req": ["REQ-FECONTENT-052", "REQ-FECONTENT-175"]}
  it('shows redeemed submission ID when present', async () => {
    const badges = [
      {
        id: 'badge-1',
        badge_id: 'def-1',
        awarded_at: '2024-01-15T10:00:00Z',
        redeemed_for_submission_id: 'sub-123',
        badge_name: 'Early Bird',
        badge_reward: 'Extra Credit',
        badge_reward_value: 3
      }
    ]

    vi.spyOn(clientModule, 'get').mockResolvedValue(badges)

    const authStore = useAuthStore()
    authStore.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    const wrapper = mount(BadgesView, {
      global: {
        stubs: {
          RouterView: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain('Applied to submission: sub-123')
  })

  // @{"req": ["REQ-FECONTENT-053"]}
  it('shows "Not yet redeemed" when redeemed_for_submission_id is null', async () => {
    const badges = [
      {
        id: 'badge-1',
        badge_id: 'def-1',
        awarded_at: '2024-01-15T10:00:00Z',
        redeemed_for_submission_id: null,
        badge_name: 'Achiever',
        badge_reward: 'Grade Boost',
        badge_reward_value: 2
      }
    ]

    vi.spyOn(clientModule, 'get').mockResolvedValue(badges)

    const authStore = useAuthStore()
    authStore.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    const wrapper = mount(BadgesView, {
      global: {
        stubs: {
          RouterView: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain('Not yet redeemed')
  })

  // @{"req": ["REQ-FECONTENT-165"]}
  it('shows "No badges earned yet." for empty array', async () => {
    vi.spyOn(clientModule, 'get').mockResolvedValue([])

    const authStore = useAuthStore()
    authStore.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    const wrapper = mount(BadgesView, {
      global: {
        stubs: {
          RouterView: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain('No badges earned yet.')
  })

  // @{"req": ["REQ-FECONTENT-180"]}
  it('displays error message on fetch failure', async () => {
    const apiError = new clientModule.ApiError(500, { error: 'server error' })
    vi.spyOn(clientModule, 'get').mockRejectedValue(apiError)

    const authStore = useAuthStore()
    authStore.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    const wrapper = mount(BadgesView, {
      global: {
        stubs: {
          RouterView: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain('Failed to load badges. Please try again.')
  })
})
