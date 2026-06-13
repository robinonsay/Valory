// @{"req": ["REQ-FEPROFILE-001", "REQ-FEPROFILE-003"]}

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import OnboardingChatView from './OnboardingChatView.vue'
import * as clientModule from '@/api/client'
import { useAuthStore } from '@/stores/auth'

describe('OnboardingChatView', () => {
  let router: any

  beforeEach(() => {
    setActivePinia(createPinia())
    router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/onboarding', component: OnboardingChatView },
        { path: '/courses', component: { template: '<div>Courses</div>' } }
      ]
    })
    vi.restoreAllMocks()
    vi.clearAllMocks()
  })

  // @{"verifies": ["REQ-FEPROFILE-001"]}
  it('renders the onboarding header with explanation text', async () => {
    const mockPost = vi.spyOn(clientModule, 'post').mockResolvedValueOnce({
      session_active: true,
      first_message: 'Hello! Let\'s talk about your learning style.'
    })

    const wrapper = mount(OnboardingChatView, {
      global: {
        plugins: [router, createPinia()]
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain("Let's get to know your learning style")
    expect(wrapper.text()).toContain('personalize your courses')
    expect(mockPost).toHaveBeenCalledWith('/api/v1/profile/onboarding/start', {})
  })

  // @{"verifies": ["REQ-FEPROFILE-001"]}
  it('displays the first message from the server on mount', async () => {
    const firstMsg = 'What is your experience level with programming?'
    vi.spyOn(clientModule, 'post').mockResolvedValueOnce({
      session_active: true,
      first_message: firstMsg
    })

    const wrapper = mount(OnboardingChatView, {
      global: {
        plugins: [router]
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain(firstMsg)
  })

  // @{"verifies": ["REQ-FEPROFILE-001"]}
  it('allows sending a message and displays it in the chat', async () => {
    const firstMsg = 'First question?'
    const assistantReply = 'Great! Next question?'

    vi.spyOn(clientModule, 'post')
      .mockResolvedValueOnce({
        session_active: true,
        first_message: firstMsg
      })
      .mockResolvedValueOnce({
        reply: assistantReply,
        done: false
      })

    const wrapper = mount(OnboardingChatView, {
      global: {
        plugins: [router]
      }
    })

    await flushPromises()

    const textarea = wrapper.find('.chat-textarea') as any
    textarea.element.value = 'I am a beginner'
    await textarea.trigger('input')
    await wrapper.find('form').trigger('submit')

    await flushPromises()

    expect(wrapper.text()).toContain('I am a beginner')
    expect(wrapper.text()).toContain(assistantReply)
  })

  // @{"verifies": ["REQ-FEPROFILE-001"]}
  it('shows complete button when conversation is done', async () => {
    const firstMsg = 'Question 1?'
    const lastMsg = 'Great! That completes our conversation. ONBOARDING_COMPLETE'

    vi.spyOn(clientModule, 'post')
      .mockResolvedValueOnce({
        session_active: true,
        first_message: firstMsg
      })
      .mockResolvedValueOnce({
        reply: lastMsg,
        done: true
      })

    const wrapper = mount(OnboardingChatView, {
      global: {
        plugins: [router]
      }
    })

    await flushPromises()

    const textarea = wrapper.find('.chat-textarea') as any
    textarea.element.value = 'My answer'
    await textarea.trigger('input')
    await wrapper.find('form').trigger('submit')

    await flushPromises()

    // After done=true, complete button should be visible
    const completeButton = wrapper.find('.complete-button')
    expect(completeButton.exists()).toBe(true)
    expect(completeButton.text()).toContain('Complete')
  })

  // @{"verifies": ["REQ-FEPROFILE-001"]}
  it('calls complete endpoint and routes to /courses on completion', async () => {
    const auth = useAuthStore()
    vi.spyOn(clientModule, 'post')
      .mockResolvedValueOnce({
        session_active: true,
        first_message: 'Q1?'
      })
      .mockResolvedValueOnce({
        reply: 'Done ONBOARDING_COMPLETE',
        done: true
      })
      .mockResolvedValueOnce({
        summary: 'Profile summary'
      })

    vi.spyOn(clientModule, 'get').mockResolvedValueOnce({
      user_id: 'user-1',
      username: 'test',
      role: 'student',
      consented: true,
      expires_at: new Date(Date.now() + 3600000).toISOString(),
      onboarding_prompted: true
    })

    const wrapper = mount(OnboardingChatView, {
      global: {
        plugins: [router]
      }
    })

    await flushPromises()

    const textarea = wrapper.find('.chat-textarea') as any
    textarea.element.value = 'Answer'
    await textarea.trigger('input')
    await wrapper.find('form').trigger('submit')

    await flushPromises()

    const completeBtn = wrapper.find('.complete-button')
    await completeBtn.trigger('click')

    await flushPromises()

    expect(clientModule.post).toHaveBeenCalledWith('/api/v1/profile/onboarding/complete', {})
  })

  // @{"verifies": ["REQ-FEPROFILE-003"]}
  it('displays skip button throughout the chat', async () => {
    vi.spyOn(clientModule, 'post').mockResolvedValueOnce({
      session_active: true,
      first_message: 'Question?'
    })

    const wrapper = mount(OnboardingChatView, {
      global: {
        plugins: [router]
      }
    })

    await flushPromises()

    const skipButton = wrapper.find('.skip-button')
    expect(skipButton.exists()).toBe(true)
    expect(skipButton.text()).toContain('Skip for now')
  })

  // @{"verifies": ["REQ-FEPROFILE-003"]}
  it('calls skip endpoint and routes to /courses on skip', async () => {
    const auth = useAuthStore()

    vi.spyOn(clientModule, 'post')
      .mockResolvedValueOnce({
        session_active: true,
        first_message: 'Question?'
      })
      .mockResolvedValueOnce({
        skipped: true
      })

    vi.spyOn(clientModule, 'get').mockResolvedValueOnce({
      user_id: 'user-1',
      username: 'test',
      role: 'student',
      consented: true,
      expires_at: new Date(Date.now() + 3600000).toISOString(),
      onboarding_prompted: true
    })

    const wrapper = mount(OnboardingChatView, {
      global: {
        plugins: [router]
      }
    })

    await flushPromises()

    // Mock window.confirm to return true
    vi.spyOn(window, 'confirm').mockReturnValueOnce(true)

    const skipButton = wrapper.find('.skip-button')
    await skipButton.trigger('click')

    await flushPromises()

    expect(clientModule.post).toHaveBeenCalledWith('/api/v1/profile/onboarding/skip', {})
  })

  it('shows error message when starting fails', async () => {
    vi.spyOn(clientModule, 'post').mockRejectedValueOnce(
      new clientModule.ApiError(500, { error: 'Server error' })
    )

    const wrapper = mount(OnboardingChatView, {
      global: {
        plugins: [router]
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain('Failed to start onboarding')
  })

  it('shows error message when sending message fails', async () => {
    vi.spyOn(clientModule, 'post')
      .mockResolvedValueOnce({
        session_active: true,
        first_message: 'Question?'
      })
      .mockRejectedValueOnce(
        new clientModule.ApiError(500, { error: 'Failed' })
      )

    const wrapper = mount(OnboardingChatView, {
      global: {
        plugins: [router]
      }
    })

    await flushPromises()

    const textarea = wrapper.find('.chat-textarea') as any
    textarea.element.value = 'Answer'
    await textarea.trigger('input')
    await wrapper.find('form').trigger('submit')

    await flushPromises()

    expect(wrapper.text()).toContain('Failed to send message')
  })
})
