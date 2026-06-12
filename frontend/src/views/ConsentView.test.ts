// @{"req": ["REQ-FEAUTH-034", "REQ-FEAUTH-035", "REQ-FEAUTH-036", "REQ-FEAUTH-037", "REQ-FEAUTH-038", "REQ-FEAUTH-039", "REQ-FEAUTH-056", "REQ-FEAUTH-057", "REQ-FEAUTH-149", "REQ-FEAUTH-167", "REQ-FEAUTH-168"]}
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import ConsentView from './ConsentView.vue'
import { useAuthStore } from '@/stores/auth'
import * as clientModule from '@/api/client'

const mockPush = vi.fn()

const router = createRouter({
  history: createMemoryHistory(),
  routes: [
    {
      path: '/consent',
      component: ConsentView
    },
    {
      path: '/courses',
      component: { template: '<div>Courses</div>' }
    }
  ]
})

describe('ConsentView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    mockPush.mockClear()
  })

  // @{"verifies": ["REQ-FEAUTH-034", "REQ-FEAUTH-143"]}
  it('renders consent text and "I agree" button', () => {
    const wrapper = mount(ConsentView, {
      global: {
        plugins: [router]
      }
    })

    expect(wrapper.text()).toContain('AI Processing Consent Statement')
    expect(wrapper.find('button').text()).toBe('I agree')
  })

  // @{"verifies": ["REQ-FEAUTH-165"]}
  it('disables the button while request is in flight', async () => {
    vi.spyOn(clientModule, 'post').mockImplementation(() => new Promise(() => {}))

    const wrapper = mount(ConsentView, {
      global: {
        plugins: [router]
      }
    })

    const button = wrapper.find('button')
    expect(button.attributes('disabled')).toBeUndefined()

    await button.trigger('click')
    await wrapper.vm.$nextTick()

    expect(button.attributes('disabled')).toBeDefined()
  })

  // @{"verifies": ["REQ-FEAUTH-035", "REQ-FEAUTH-149"]}
  it('sends POST to /api/v1/consent with { version: "1.0" } body', async () => {
    const mockPost = vi.spyOn(clientModule, 'post').mockResolvedValue({})

    const wrapper = mount(ConsentView, {
      global: {
        plugins: [router]
      }
    })

    const auth = useAuthStore()
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    await wrapper.find('button').trigger('click')
    await wrapper.vm.$nextTick()

    expect(mockPost).toHaveBeenCalledWith('/api/v1/consent', { version: '1.0' }, 'test-token')
  })

  // @{"verifies": ["REQ-FEAUTH-035"]}
  it('calls auth.setConsented() on successful 200 response', async () => {
    vi.spyOn(clientModule, 'post').mockResolvedValue({})

    const wrapper = mount(ConsentView, {
      global: {
        plugins: [router]
      }
    })

    const auth = useAuthStore()
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    expect(auth.isConsented).toBe(false)

    await wrapper.find('button').trigger('click')
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(auth.isConsented).toBe(true)
  })

  // @{"verifies": ["REQ-FEAUTH-036"]}
  it('navigates to /courses on successful response', async () => {
    vi.spyOn(clientModule, 'post').mockResolvedValue({})
    const routerPushSpy = vi.spyOn(router, 'push')

    const wrapper = mount(ConsentView, {
      global: {
        plugins: [router]
      }
    })

    const auth = useAuthStore()
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    await wrapper.find('button').trigger('click')
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(routerPushSpy).toHaveBeenCalledWith('/courses')
  })

  // @{"verifies": ["REQ-FEAUTH-039"]}
  it('displays error message on API error', async () => {
    const errorToThrow = new clientModule.ApiError(500, {})
    vi.spyOn(clientModule, 'post').mockRejectedValue(errorToThrow)

    const wrapper = mount(ConsentView, {
      global: {
        plugins: [router]
      }
    })

    const auth = useAuthStore()
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    await wrapper.find('button').trigger('click')
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(wrapper.text()).toContain('Failed to record consent. Please try again.')
  })

  // @{"verifies": ["REQ-FEAUTH-166"]}
  it('re-enables button after error', async () => {
    const errorToThrow = new clientModule.ApiError(500, {})
    vi.spyOn(clientModule, 'post').mockRejectedValue(errorToThrow)

    const wrapper = mount(ConsentView, {
      global: {
        plugins: [router]
      }
    })

    const auth = useAuthStore()
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    const button = wrapper.find('button')

    await button.trigger('click')
    await wrapper.vm.$nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(button.attributes('disabled')).toBeUndefined()
  })

  // @{"verifies": ["REQ-FEAUTH-056"]}
  it('renders full consent statement with distinctive phrases from the document', () => {
    const wrapper = mount(ConsentView, {
      global: {
        plugins: [router]
      }
    })

    const text = wrapper.text()
    expect(text).toContain('Anthropic')
    expect(text).toContain('Claude API')
    expect(text).toContain('permanent deletion of your personal data')
    expect(text).toContain('Data Retention and Your Right to Deletion')
  })

  // @{"verifies": ["REQ-FEAUTH-057"]}
  it('displays version label with CONSENT_VERSION', () => {
    const wrapper = mount(ConsentView, {
      global: {
        plugins: [router]
      }
    })

    expect(wrapper.text()).toContain('AI Processing Consent Statement — Version 1.0')
  })

  // @{"verifies": ["REQ-FEAUTH-057"]}
  it('includes version in consent statement content', () => {
    const wrapper = mount(ConsentView, {
      global: {
        plugins: [router]
      }
    })

    const text = wrapper.text()
    expect(text).toContain('Your consent is stored with this document version (1.0) and a UTC')
  })

  // @{"verifies": ["REQ-FEAUTH-168"]}
  it('sends POST body version matching displayed version constant', async () => {
    const mockPost = vi.spyOn(clientModule, 'post').mockResolvedValue({})

    const wrapper = mount(ConsentView, {
      global: {
        plugins: [router]
      }
    })

    const auth = useAuthStore()
    auth.login('test-token', 'student', Math.floor(Date.now() / 1000) + 3600)

    await wrapper.find('button').trigger('click')
    await wrapper.vm.$nextTick()

    expect(mockPost).toHaveBeenCalledWith('/api/v1/consent', { version: '1.0' }, 'test-token')
  })

  // @{"verifies": ["REQ-FEAUTH-056"]}
  it('renders consent statement in scrollable panel', () => {
    const wrapper = mount(ConsentView, {
      global: {
        plugins: [router]
      }
    })

    const panel = wrapper.find('.consent-statement-panel')
    expect(panel.exists()).toBe(true)
    expect(panel.text()).toContain('About This Document')
    expect(panel.text()).toContain('What Data Valory Holds About You')
  })

  // @{"verifies": ["REQ-FEAUTH-167"]}
  it('displays accept button after scrollable panel', () => {
    const wrapper = mount(ConsentView, {
      global: {
        plugins: [router]
      }
    })

    const content = wrapper.find('.consent-content')
    const children = Array.from(content.element.children)

    const panelIndex = children.findIndex(el => el.classList.contains('consent-statement-panel'))
    const buttonIndex = children.findIndex(el => el.classList.contains('agree-button'))

    expect(panelIndex).toBeGreaterThan(-1)
    expect(buttonIndex).toBeGreaterThan(panelIndex)
  })
})
