// @{"req": ["REQ-FEAUTH-200", "REQ-FEAUTH-201", "REQ-FEAUTH-202"]}

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import { setActivePinia, createPinia } from 'pinia'
import OtpVerifyView from './OtpVerifyView.vue'
import { useAuthStore } from '@/stores/auth'
import * as clientModule from '@/api/client'

describe('OtpVerifyView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  // @{"req": ["REQ-FEAUTH-202"]}
  it('should redirect to /login when no pending token', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/login/verify', component: { template: '<div></div>' } },
        { path: '/login', component: { template: '<div></div>' } }
      ]
    })

    await router.push('/login/verify')
    await router.isReady()

    const auth = useAuthStore()
    expect(auth.pendingTwoFactor).toBeNull()

    mount(OtpVerifyView, {
      global: {
        plugins: [router]
      }
    })

    await new Promise(resolve => setTimeout(resolve, 50))
    expect(router.currentRoute.value.path).toBe('/login')
  })

  // @{"req": ["REQ-FEAUTH-200"]}
  it('should render OTP input field when pending token exists', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/login/verify', component: { template: '<div></div>' } }]
    })

    const auth = useAuthStore()
    const token = 'test-token'
    const expiresAt = new Date(Date.now() + 600000).toISOString()
    auth.setPendingTwoFactor(token, expiresAt)

    const wrapper = mount(OtpVerifyView, {
      global: {
        plugins: [router]
      }
    })

    const input = wrapper.find('.otp-input')
    expect(input.exists()).toBe(true)

    const title = wrapper.find('h1')
    expect(title.text()).toContain('Check your email')
  })

  // @{"req": ["REQ-FEAUTH-200"]}
  it('submit button should be disabled until OTP is 6 digits', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/login/verify', component: { template: '<div></div>' } }]
    })

    const auth = useAuthStore()
    const token = 'test-token'
    const expiresAt = new Date(Date.now() + 600000).toISOString()
    auth.setPendingTwoFactor(token, expiresAt)

    const wrapper = mount(OtpVerifyView, {
      global: {
        plugins: [router]
      }
    })

    const input = wrapper.find('.otp-input') as any
    const button = wrapper.find('.verify-button')

    expect(button.element.disabled).toBe(true)

    input.element.value = '123456'
    await input.trigger('input')
    await wrapper.vm.$nextTick()

    expect(button.element.disabled).toBe(false)
  })


  // @{"req": ["REQ-FEAUTH-200"]}
  it('cancel link should clear pending state and navigate to login', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/login/verify', component: { template: '<div></div>' } },
        { path: '/login', component: { template: '<div></div>' } }
      ]
    })

    const auth = useAuthStore()
    const token = 'test-token'
    const expiresAt = new Date(Date.now() + 600000).toISOString()
    auth.setPendingTwoFactor(token, expiresAt)

    const wrapper = mount(OtpVerifyView, {
      global: {
        plugins: [router]
      }
    })

    expect(auth.pendingTwoFactor).not.toBeNull()

    const cancelLink = wrapper.find('.cancel-link a')
    await cancelLink.trigger('click')

    expect(auth.pendingTwoFactor).toBeNull()
  })

  // @{"req": ["REQ-FEAUTH-200"]}
  it('resend button should be disabled during countdown', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/login/verify', component: { template: '<div></div>' } }]
    })

    const auth = useAuthStore()
    const token = 'test-token'
    const expiresAt = new Date(Date.now() + 600000).toISOString()
    auth.setPendingTwoFactor(token, expiresAt)

    const wrapper = mount(OtpVerifyView, {
      global: {
        plugins: [router]
      }
    })

    let resendButton = wrapper.find('.resend-button')
    expect(resendButton.exists()).toBe(true)

    const vm = wrapper.vm as any
    vm.resendCountdownSeconds = 30

    await wrapper.vm.$nextTick()

    const countdown = wrapper.find('.countdown-message')
    expect(countdown.exists()).toBe(true)
    expect(countdown.text()).toContain('30s')
  })
})
