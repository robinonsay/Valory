// @{"req": ["REQ-FEADMIN-010", "REQ-FEADMIN-011", "REQ-FEADMIN-012", "REQ-FEADMIN-013", "REQ-FEADMIN-100", "REQ-FEADMIN-101", "REQ-FEADMIN-102", "REQ-FEADMIN-110", "REQ-FEAUTH-019", "REQ-FEAUTH-020", "REQ-FEONBOARD-002"]}

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import { setActivePinia, createPinia } from 'pinia'
import AdminLayout from './AdminLayout.vue'
import { useAuthStore } from '@/stores/auth'

describe('AdminLayout', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  const createTestRouter = () => {
    return createRouter({
      history: createMemoryHistory(),
      routes: [
        {
          path: '/admin',
          component: AdminLayout,
          children: [
            { path: 'users', name: 'admin-users', component: { template: '<div>Users View</div>' } },
            { path: 'audit', name: 'admin-audit', component: { template: '<div>Audit View</div>' } },
            { path: 'config', name: 'admin-config', component: { template: '<div>Config View</div>' } },
            { path: 'courses', name: 'admin-courses', component: { template: '<div>Courses View</div>' } },
            { path: 'getting-started', name: 'getting-started-admin', component: { template: '<div>Getting Started</div>' } }
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

  it('renders navigation links for Users, Audit Log, Config, Course Oversight, Getting Started', () => {
    const router = createTestRouter()
    const auth = useAuthStore()
    auth.login('test-token', 'admin', Math.floor(Date.now() / 1000) + 3600)

    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ username: 'testadmin' })
    }))

    const wrapper = mount(AdminLayout, {
      global: {
        plugins: [router]
      }
    })

    expect(wrapper.text()).toContain('Users')
    expect(wrapper.text()).toContain('Audit Log')
    expect(wrapper.text()).toContain('Config')
    expect(wrapper.text()).toContain('Course Oversight')
    // @{"req": ["REQ-FEONBOARD-002"]}
    expect(wrapper.text()).toContain('Getting Started')

    const links = wrapper.findAll('a')
    expect(links.length).toBeGreaterThanOrEqual(5)
  })

  // @{"req": ["REQ-FEAUTH-019", "REQ-FEAUTH-020", "REQ-FEAUTH-118", "REQ-FEAUTH-119"]}
  it('logout button calls auth.logoutServer() and navigates to /login', async () => {
    const router = createTestRouter()
    const auth = useAuthStore()
    auth.login('test-token', 'admin', Math.floor(Date.now() / 1000) + 3600)

    // Mock logoutServer so the test does not make real fetch calls; we verify
    // that logoutServer is invoked (it clears state internally) and navigation occurs.
    const logoutServerSpy = vi.spyOn(auth, 'logoutServer').mockResolvedValue(undefined)

    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ username: 'testadmin' })
    }))

    const wrapper = mount(AdminLayout, {
      global: {
        plugins: [router]
      }
    })

    expect(auth.token).not.toBeNull()

    const logoutButton = wrapper.find('.logout-button')
    expect(logoutButton.exists()).toBe(true)

    await logoutButton.trigger('click')
    await router.isReady()

    expect(logoutServerSpy).toHaveBeenCalledOnce()
    expect(router.currentRoute.value.path).toBe('/login')

    logoutServerSpy.mockRestore()
  })

  it('RouterView is rendered', () => {
    const router = createTestRouter()
    const auth = useAuthStore()
    auth.login('test-token', 'admin', Math.floor(Date.now() / 1000) + 3600)

    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ username: 'testadmin' })
    }))

    const wrapper = mount(AdminLayout, {
      global: {
        plugins: [router]
      }
    })

    expect(wrapper.find('.page-content').exists()).toBe(true)
    const routerView = wrapper.findComponent({ name: 'RouterView' })
    expect(routerView.exists()).toBe(true)
  })

  it('displays admin username from API response', async () => {
    const router = createTestRouter()
    const auth = useAuthStore()
    auth.login('test-token', 'admin', Math.floor(Date.now() / 1000) + 3600)

    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ username: 'john_doe' })
    })
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mount(AdminLayout, {
      global: {
        plugins: [router]
      }
    })

    await new Promise(resolve => setTimeout(resolve, 50))
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('john_doe')
  })

  it('displays default Admin username when API call fails', () => {
    const router = createTestRouter()
    const auth = useAuthStore()
    auth.login('test-token', 'admin', Math.floor(Date.now() / 1000) + 3600)

    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('Network error')))

    const wrapper = mount(AdminLayout, {
      global: {
        plugins: [router]
      }
    })

    expect(wrapper.text()).toContain('Admin')
  })

  it('displays default Admin username when API response is not ok', async () => {
    const router = createTestRouter()
    const auth = useAuthStore()
    auth.login('test-token', 'admin', Math.floor(Date.now() / 1000) + 3600)

    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: false,
      status: 500
    }))

    const wrapper = mount(AdminLayout, {
      global: {
        plugins: [router]
      }
    })

    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Admin')
  })
})
