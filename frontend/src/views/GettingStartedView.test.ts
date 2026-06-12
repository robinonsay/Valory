// @{"req": ["REQ-FEONBOARD-001", "REQ-FEONBOARD-003", "REQ-FEONBOARD-004", "REQ-FEONBOARD-010", "REQ-FEONBOARD-011", "REQ-FEONBOARD-012", "REQ-FEONBOARD-013", "REQ-FEONBOARD-014", "REQ-FEONBOARD-015", "REQ-FEONBOARD-016", "REQ-FEONBOARD-020", "REQ-FEONBOARD-021", "REQ-FEONBOARD-022", "REQ-FEONBOARD-023"]}

import { describe, it, expect, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import { setActivePinia, createPinia } from 'pinia'
import { useAuthStore } from '@/stores/auth'
import GettingStartedView from './GettingStartedView.vue'

// @{"req": ["REQ-FEONBOARD-001", "REQ-FEONBOARD-003", "REQ-FEONBOARD-004"]}
describe('GettingStartedView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  const createTestRouter = () =>
    createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/getting-started', component: GettingStartedView },
        { path: '/courses', component: { template: '<div />' } },
        { path: '/admin/users', component: { template: '<div />' } },
        { path: '/admin/audit', component: { template: '<div />' } },
        { path: '/admin/config', component: { template: '<div />' } },
        { path: '/admin/courses', component: { template: '<div />' } }
      ]
    })

  // @{"req": ["REQ-FEONBOARD-003", "REQ-FEONBOARD-010", "REQ-FEONBOARD-011", "REQ-FEONBOARD-012", "REQ-FEONBOARD-013", "REQ-FEONBOARD-014", "REQ-FEONBOARD-015", "REQ-FEONBOARD-016"]}
  it('renders seven student steps for a student user', () => {
    const router = createTestRouter()
    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const wrapper = mount(GettingStartedView, {
      global: { plugins: [router] }
    })

    expect(wrapper.text()).toContain('Student Guide')

    // Verify all seven step headings are present
    expect(wrapper.text()).toContain('Accept AI Consent')
    expect(wrapper.text()).toContain('Create a Course')
    expect(wrapper.text()).toContain('Intake Chat')
    expect(wrapper.text()).toContain('Review and Approve the Syllabus')
    expect(wrapper.text()).toContain('Read the Generated Sections')
    expect(wrapper.text()).toContain('Submit Homework')
    expect(wrapper.text()).toContain('Track Grades and Badges')

    // Exactly 7 step-number badges
    const stepNumbers = wrapper.findAll('.step-number')
    expect(stepNumbers).toHaveLength(7)
  })

  // @{"req": ["REQ-FEONBOARD-003"]}
  it('does not render admin steps for a student user', () => {
    const router = createTestRouter()
    const auth = useAuthStore()
    auth.$patch({ role: 'student', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const wrapper = mount(GettingStartedView, {
      global: { plugins: [router] }
    })

    expect(wrapper.text()).not.toContain('Admin Guide')
    expect(wrapper.text()).not.toContain('User Management')
  })

  // @{"req": ["REQ-FEONBOARD-004", "REQ-FEONBOARD-020", "REQ-FEONBOARD-021", "REQ-FEONBOARD-022", "REQ-FEONBOARD-023"]}
  it('renders four admin steps for an admin user', () => {
    const router = createTestRouter()
    const auth = useAuthStore()
    auth.$patch({ role: 'admin', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const wrapper = mount(GettingStartedView, {
      global: { plugins: [router] }
    })

    expect(wrapper.text()).toContain('Admin Guide')

    // Verify all four step headings are present
    expect(wrapper.text()).toContain('User Management')
    expect(wrapper.text()).toContain('System Configuration')
    expect(wrapper.text()).toContain('Audit Log')
    expect(wrapper.text()).toContain('Course Oversight')

    // Exactly 4 step-number badges
    const stepNumbers = wrapper.findAll('.step-number')
    expect(stepNumbers).toHaveLength(4)
  })

  // @{"req": ["REQ-FEONBOARD-004"]}
  it('does not render student steps for an admin user', () => {
    const router = createTestRouter()
    const auth = useAuthStore()
    auth.$patch({ role: 'admin', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const wrapper = mount(GettingStartedView, {
      global: { plugins: [router] }
    })

    expect(wrapper.text()).not.toContain('Student Guide')
    expect(wrapper.text()).not.toContain('Accept AI Consent')
  })

  // @{"req": ["REQ-FEONBOARD-021"]}
  it('admin step 2 references docs/guides/admin-configuration.md', () => {
    const router = createTestRouter()
    const auth = useAuthStore()
    auth.$patch({ role: 'admin', expiresAt: Math.floor(Date.now() / 1000) + 3600, restoreDone: true })

    const wrapper = mount(GettingStartedView, {
      global: { plugins: [router] }
    })

    expect(wrapper.text()).toContain('docs/guides/admin-configuration.md')
  })
})
