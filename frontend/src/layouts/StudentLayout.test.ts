// @{"req": ["REQ-FEAUTH-019", "REQ-FEAUTH-118", "REQ-FEONBOARD-002"]}

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import { setActivePinia, createPinia } from 'pinia'
import StudentLayout from './StudentLayout.vue'
import { useAuthStore } from '@/stores/auth'

// @{"req": ["REQ-FEAUTH-019", "REQ-FEAUTH-118", "REQ-FEONBOARD-002"]}
describe('StudentLayout', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  const createTestRouter = () => {
    return createRouter({
      history: createMemoryHistory(),
      routes: [
        {
          path: '/',
          component: StudentLayout,
          children: [
            { path: 'courses', name: 'courses', component: { template: '<div>Courses</div>' } },
            { path: 'getting-started', name: 'getting-started', component: { template: '<div>Getting Started</div>' } }
          ]
        },
        {
          path: '/login',
          name: 'login',
          component: { template: '<div>Login</div>' }
        }
      ]
    })
  }

  // @{"req": ["REQ-FEAUTH-118", "REQ-FEONBOARD-002"]}
  it('renders brand, Courses link, Getting Started link, and Logout button', () => {
    const router = createTestRouter()
    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const wrapper = mount(StudentLayout, {
      global: { plugins: [router] }
    })

    expect(wrapper.text()).toContain('Valory')
    expect(wrapper.text()).toContain('Courses')
    expect(wrapper.text()).toContain('Getting Started')
    expect(wrapper.find('.logout-button').exists()).toBe(true)

    const logo = wrapper.find('.logo')
    expect(logo.exists()).toBe(true)
    expect(logo.attributes('src')).toContain('valory.svg')
    expect(logo.attributes('alt')).toBe('Valory')
  })

  // @{"req": ["REQ-FEAUTH-118", "REQ-FEONBOARD-002"]}
  it('Courses link points to /courses and Getting Started link points to /getting-started', () => {
    const router = createTestRouter()
    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const wrapper = mount(StudentLayout, {
      global: { plugins: [router] }
    })

    const links = wrapper.findAll('a')
    const hrefs = links.map(l => l.attributes('href'))
    expect(hrefs).toContain('/courses')
    expect(hrefs).toContain('/getting-started')
  })

  // @{"req": ["REQ-FEAUTH-019", "REQ-FEAUTH-020", "REQ-FEAUTH-119"]}
  it('logout button calls logoutServer and navigates to /login', async () => {
    const router = createTestRouter()
    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    // Mock logoutServer so we control its resolution without real fetch
    const logoutServerSpy = vi.spyOn(auth, 'logoutServer').mockResolvedValue(undefined)

    const wrapper = mount(StudentLayout, {
      global: { plugins: [router] }
    })

    const logoutButton = wrapper.find('.logout-button')
    expect(logoutButton.exists()).toBe(true)

    await logoutButton.trigger('click')
    await router.isReady()

    expect(logoutServerSpy).toHaveBeenCalledOnce()
    expect(router.currentRoute.value.path).toBe('/login')

    logoutServerSpy.mockRestore()
  })

  // @{"req": ["REQ-FEAUTH-118"]}
  it('RouterView is rendered', () => {
    const router = createTestRouter()
    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const wrapper = mount(StudentLayout, {
      global: { plugins: [router] }
    })

    const routerView = wrapper.findComponent({ name: 'RouterView' })
    expect(routerView.exists()).toBe(true)
  })
})
