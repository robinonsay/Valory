// @{"req": ["REQ-FECONTENT-060", "REQ-FECONTENT-061", "REQ-FECONTENT-062", "REQ-FECONTENT-063", "REQ-FECONTENT-190", "REQ-FECONTENT-200", "REQ-FECONTENT-205", "REQ-FECONTENT-210", "REQ-FECONTENT-215"]}

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { get, post, ApiError } from '@/api/client'

// @{"req": ["REQ-FECONTENT-060", "REQ-FECONTENT-061", "REQ-FECONTENT-062", "REQ-FECONTENT-063", "REQ-FECONTENT-190", "REQ-FECONTENT-200", "REQ-FECONTENT-205", "REQ-FECONTENT-210", "REQ-FECONTENT-215"]}
interface Notification {
  id: string
  title: string
  body: string
  read_at?: string | null
  created_at: string
}

interface NotificationsResponse {
  notifications: Notification[]
}

const auth = useAuthStore()

const notifications = ref<Notification[]>([])
const isDropdownOpen = ref(false)
const error = ref('')
let pollInterval: number | null = null

const unreadCount = computed(() => {
  return notifications.value.filter(n => !n.read_at).length
})

async function fetchNotifications() {
  try {
    const response = await get<NotificationsResponse>('/api/v1/notifications', auth.token || undefined)
    notifications.value = response.notifications ?? []
    error.value = ''
  } catch (err) {
    if (err instanceof ApiError && err.status !== 401) {
      error.value = 'Failed to fetch notifications'
    }
  }
}

async function markNotificationRead(notificationId: string) {
  const notification = notifications.value.find(n => n.id === notificationId)
  if (!notification) return

  try {
    await post(`/api/v1/notifications/${notificationId}/read`, {}, auth.token || undefined)
    notification.read_at = new Date().toISOString()
    error.value = ''
  } catch (err) {
    if (err instanceof ApiError) {
      if (err.status === 404) {
        notifications.value = notifications.value.filter(n => n.id !== notificationId)
      } else if (err.status === 403) {
        error.value = 'Cannot mark this notification as read'
      }
    }
  }
}

async function markAllRead() {
  const unreadNotifications = notifications.value.filter(n => !n.read_at)
  await Promise.all(unreadNotifications.map(n => markNotificationRead(n.id)))
}

function toggleDropdown() {
  isDropdownOpen.value = !isDropdownOpen.value
}

function closeDropdown() {
  isDropdownOpen.value = false
}

onMounted(() => {
  fetchNotifications()
  pollInterval = window.setInterval(() => {
    fetchNotifications()
  }, 30000)
})

onUnmounted(() => {
  if (pollInterval !== null) {
    clearInterval(pollInterval)
  }
})
</script>

<template>
  <div class="notifications-panel">
    <button class="bell-button" @click="toggleDropdown" title="Notifications">
      <span class="bell-icon">🔔</span>
      <span v-if="unreadCount > 0" class="badge">{{ unreadCount }}</span>
    </button>

    <div v-if="isDropdownOpen" class="dropdown-panel">
      <div class="dropdown-header">
        <h3>Notifications</h3>
        <button v-if="unreadCount > 0" class="mark-all-btn" @click="markAllRead">
          Mark all as read
        </button>
      </div>

      <div v-if="error" class="error-message">
        {{ error }}
      </div>

      <div v-if="notifications.length === 0" class="empty-state">
        No new notifications.
      </div>

      <ul v-else class="notifications-list">
        <li v-for="notification in notifications" :key="notification.id" class="notification-item">
          <button
            class="notification-content"
            :class="{ 'is-unread': !notification.read_at }"
            @click="markNotificationRead(notification.id)"
          >
            <div v-if="!notification.read_at" class="unread-indicator"></div>
            <div class="notification-text">
              <div class="notification-title" :class="{ 'bold': !notification.read_at }">
                {{ notification.title }}
              </div>
              <div class="notification-body">
                {{ notification.body }}
              </div>
              <div class="notification-time">
                {{ new Date(notification.created_at).toLocaleString() }}
              </div>
            </div>
          </button>
        </li>
      </ul>
    </div>

    <div v-if="isDropdownOpen" class="dropdown-overlay" @click="closeDropdown"></div>
  </div>
</template>

<style scoped>
.notifications-panel {
  position: relative;
  display: inline-block;
}

.bell-button {
  position: relative;
  background: none;
  border: none;
  cursor: pointer;
  padding: 0.5rem;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 1.5rem;
  transition: transform 0.2s;
}

.bell-button:hover {
  transform: scale(1.1);
}

.badge {
  position: absolute;
  top: -4px;
  right: -4px;
  background-color: #d32f2f;
  color: white;
  border-radius: 50%;
  width: 20px;
  height: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 0.75rem;
  font-weight: bold;
}

.dropdown-panel {
  position: absolute;
  top: 100%;
  right: 0;
  margin-top: 0.5rem;
  background: white;
  border: 1px solid #ddd;
  border-radius: 8px;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
  width: 350px;
  max-height: 500px;
  overflow-y: auto;
  z-index: 1000;
}

.dropdown-header {
  padding: 1rem;
  border-bottom: 1px solid #eee;
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.dropdown-header h3 {
  margin: 0;
  font-size: 1rem;
  font-weight: 600;
}

.mark-all-btn {
  background: none;
  border: none;
  color: #1976d2;
  cursor: pointer;
  font-size: 0.875rem;
  text-decoration: underline;
  padding: 0;
  transition: color 0.2s;
}

.mark-all-btn:hover {
  color: #1565c0;
}

.empty-state {
  padding: 2rem 1rem;
  text-align: center;
  color: #999;
}

.error-message {
  color: #d32f2f;
  padding: 0.75rem 1rem;
  background-color: #ffebee;
  border-bottom: 1px solid #eee;
}

.notifications-list {
  list-style: none;
  margin: 0;
  padding: 0;
}

.notification-item {
  border-bottom: 1px solid #eee;
}

.notification-item:last-child {
  border-bottom: none;
}

.notification-content {
  width: 100%;
  padding: 1rem;
  text-align: left;
  background: none;
  border: none;
  cursor: pointer;
  display: flex;
  gap: 0.75rem;
  transition: background-color 0.2s;
}

.notification-content:hover {
  background-color: #f9f9f9;
}

.notification-content.is-unread {
  background-color: #f0f7ff;
}

.unread-indicator {
  width: 8px;
  height: 8px;
  background-color: #1976d2;
  border-radius: 50%;
  margin-top: 0.5rem;
  flex-shrink: 0;
}

.notification-text {
  flex: 1;
  min-width: 0;
}

.notification-title {
  font-size: 0.95rem;
  color: #333;
  margin-bottom: 0.25rem;
}

.notification-title.bold {
  font-weight: 600;
}

.notification-body {
  font-size: 0.875rem;
  color: #666;
  margin-bottom: 0.5rem;
  word-wrap: break-word;
  white-space: pre-wrap;
}

.notification-time {
  font-size: 0.75rem;
  color: #999;
}

.dropdown-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  z-index: 999;
}
</style>
