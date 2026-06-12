// @{"req": ["REQ-FECONTENT-060", "REQ-FECONTENT-061", "REQ-FECONTENT-062", "REQ-FECONTENT-063", "REQ-FECONTENT-190", "REQ-FECONTENT-200", "REQ-FECONTENT-205", "REQ-FECONTENT-210", "REQ-FECONTENT-215"]}

import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { useAuthStore } from '@/stores/auth'
import NotificationsPanel from './NotificationsPanel.vue'
import * as clientModule from '@/api/client'

describe('NotificationsPanel', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    vi.useFakeTimers({
      toFake: ['setTimeout', 'setInterval', 'clearInterval']
    })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('should fetch notifications on mount', async () => {
    const mockGet = vi.spyOn(clientModule, 'get').mockResolvedValue({
      notifications: [
        {
          id: '1',
          title: 'Test Notification',
          body: 'Test body',
          read_at: null,
          created_at: '2024-01-01T12:00:00Z'
        }
      ]
    })

    const auth = useAuthStore()
    auth.$patch({ role: 'student', restoreDone: true })

    const wrapper = mount(NotificationsPanel)

    await vi.advanceTimersByTimeAsync(0)

    expect(mockGet).toHaveBeenCalledWith('/api/v1/notifications')
    expect(wrapper.vm.notifications).toHaveLength(1)
    expect(wrapper.vm.notifications[0].title).toBe('Test Notification')

    wrapper.unmount()
    mockGet.mockRestore()
  })

  it('should poll every 30 seconds', async () => {
    const mockGet = vi.spyOn(clientModule, 'get').mockResolvedValue({
      notifications: []
    })

    const auth = useAuthStore()
    auth.$patch({ role: 'student', restoreDone: true })

    const wrapper = mount(NotificationsPanel)

    await vi.advanceTimersByTimeAsync(0)

    const callCountAfterMount = mockGet.mock.calls.length
    expect(callCountAfterMount).toBeGreaterThan(0)

    await vi.advanceTimersByTimeAsync(30000)

    expect(mockGet.mock.calls.length).toBeGreaterThan(callCountAfterMount)

    wrapper.unmount()
    mockGet.mockRestore()
  })

  it('should compute unread count correctly', async () => {
    const mockGet = vi.spyOn(clientModule, 'get').mockResolvedValue({
      notifications: [
        {
          id: '1',
          title: 'Unread 1',
          body: 'Body 1',
          read_at: null,
          created_at: '2024-01-01T12:00:00Z'
        },
        {
          id: '2',
          title: 'Read 1',
          body: 'Body 2',
          read_at: '2024-01-01T13:00:00Z',
          created_at: '2024-01-01T12:00:00Z'
        },
        {
          id: '3',
          title: 'Unread 2',
          body: 'Body 3',
          read_at: null,
          created_at: '2024-01-01T12:00:00Z'
        }
      ]
    })

    const auth = useAuthStore()
    auth.$patch({ role: 'student', restoreDone: true })

    const wrapper = mount(NotificationsPanel)

    await vi.advanceTimersByTimeAsync(0)

    expect(wrapper.vm.unreadCount).toBe(2)

    wrapper.unmount()
    mockGet.mockRestore()
  })

  it('should show badge only when unread count > 0', async () => {
    const mockGet = vi.spyOn(clientModule, 'get').mockResolvedValue({
      notifications: [
        {
          id: '1',
          title: 'Test',
          body: 'Body',
          read_at: '2024-01-01T13:00:00Z',
          created_at: '2024-01-01T12:00:00Z'
        }
      ]
    })

    const auth = useAuthStore()
    auth.$patch({ role: 'student', restoreDone: true })

    const wrapper = mount(NotificationsPanel)

    await vi.advanceTimersByTimeAsync(0)

    let badge = wrapper.find('.badge')
    expect(badge.exists()).toBe(false)

    wrapper.vm.notifications = [
      {
        id: '1',
        title: 'Test',
        body: 'Body',
        read_at: null,
        created_at: '2024-01-01T12:00:00Z'
      }
    ]

    await wrapper.vm.$nextTick()

    badge = wrapper.find('.badge')
    expect(badge.exists()).toBe(true)
    expect(badge.text()).toBe('1')

    wrapper.unmount()
    mockGet.mockRestore()
  })

  it('should toggle dropdown when bell is clicked', async () => {
    const mockGet = vi.spyOn(clientModule, 'get').mockResolvedValue({
      notifications: []
    })

    const auth = useAuthStore()
    auth.$patch({ role: 'student', restoreDone: true })

    const wrapper = mount(NotificationsPanel)

    await vi.advanceTimersByTimeAsync(0)

    const bellButton = wrapper.find('.bell-button')
    expect(wrapper.vm.isDropdownOpen).toBe(false)

    await bellButton.trigger('click')
    expect(wrapper.vm.isDropdownOpen).toBe(true)

    await bellButton.trigger('click')
    expect(wrapper.vm.isDropdownOpen).toBe(false)

    wrapper.unmount()
    mockGet.mockRestore()
  })

  it('should POST to mark-read endpoint when notification is clicked', async () => {
    const mockGet = vi.spyOn(clientModule, 'get').mockResolvedValue({
      notifications: [
        {
          id: 'notif-1',
          title: 'Test Notification',
          body: 'Test body',
          read_at: null,
          created_at: '2024-01-01T12:00:00Z'
        }
      ]
    })

    const mockPost = vi.spyOn(clientModule, 'post').mockResolvedValue({})

    const auth = useAuthStore()
    auth.$patch({ role: 'student', restoreDone: true })

    const wrapper = mount(NotificationsPanel)

    await vi.advanceTimersByTimeAsync(0)

    await wrapper.vm.markNotificationRead('notif-1')

    expect(mockPost).toHaveBeenCalledWith(
      '/api/v1/notifications/notif-1/read',
      {},
    )

    expect(wrapper.vm.notifications[0].read_at).toBeTruthy()

    wrapper.unmount()
    mockGet.mockRestore()
    mockPost.mockRestore()
  })

  it('should treat absent read_at field as unread', async () => {
    const mockGet = vi.spyOn(clientModule, 'get').mockResolvedValue({
      notifications: [
        {
          id: '1',
          title: 'T',
          body: 'B',
          created_at: '2024-01-01T12:00:00Z'
        }
      ]
    })

    const auth = useAuthStore()
    auth.$patch({ role: 'student', restoreDone: true })

    const wrapper = mount(NotificationsPanel)

    await vi.advanceTimersByTimeAsync(0)

    const notification = wrapper.vm.notifications[0] as any
    expect(!notification.read_at).toBe(true)
    expect(wrapper.vm.unreadCount).toBe(1)

    wrapper.unmount()
    mockGet.mockRestore()
  })

  it('should treat explicit null read_at as unread', async () => {
    const mockGet = vi.spyOn(clientModule, 'get').mockResolvedValue({
      notifications: [
        {
          id: '1',
          title: 'T',
          body: 'B',
          read_at: null,
          created_at: '2024-01-01T12:00:00Z'
        }
      ]
    })

    const auth = useAuthStore()
    auth.$patch({ role: 'student', restoreDone: true })

    const wrapper = mount(NotificationsPanel)

    await vi.advanceTimersByTimeAsync(0)

    const notification = wrapper.vm.notifications[0]
    expect(!notification.read_at).toBe(true)
    expect(wrapper.vm.unreadCount).toBe(1)

    wrapper.unmount()
    mockGet.mockRestore()
  })

  it('should clear interval on unmount', async () => {
    const mockGet = vi.spyOn(clientModule, 'get').mockResolvedValue({
      notifications: []
    })

    const auth = useAuthStore()
    auth.$patch({ role: 'student', restoreDone: true })

    const wrapper = mount(NotificationsPanel)

    await vi.advanceTimersByTimeAsync(0)

    const callCountBeforeUnmount = mockGet.mock.calls.length

    wrapper.unmount()

    await vi.advanceTimersByTimeAsync(30000)

    expect(mockGet.mock.calls.length).toBe(callCountBeforeUnmount)

    mockGet.mockRestore()
  })
})
