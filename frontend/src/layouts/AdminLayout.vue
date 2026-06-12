// @{"req": ["REQ-FEADMIN-010", "REQ-FEADMIN-011", "REQ-FEADMIN-012", "REQ-FEADMIN-013", "REQ-FEADMIN-100", "REQ-FEADMIN-101", "REQ-FEADMIN-102", "REQ-FEADMIN-110"]}

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const auth = useAuthStore()

const username = ref<string>('Admin')

onMounted(async () => {
  try {
    const response = await fetch('/api/v1/users/me', {
      headers: {
        'Authorization': `Bearer ${auth.token}`
      }
    })
    if (response.ok) {
      const data = await response.json() as { username: string }
      username.value = data.username
    }
  } catch {
    username.value = 'Admin'
  }
})

// @{"req": ["REQ-FEAUTH-019", "REQ-FEAUTH-020", "REQ-FEAUTH-118", "REQ-FEAUTH-119"]}
const handleLogout = async (): Promise<void> => {
  await auth.logoutServer()
  await router.push('/login')
}
</script>

<template>
  <div class="admin-layout">
    <aside class="sidebar">
      <nav class="sidebar-nav">
        <div class="nav-links">
          <RouterLink
            to="/admin/users"
            active-class="active"
            exact-active-class="active"
            class="nav-link"
          >
            Users
          </RouterLink>
          <RouterLink
            to="/admin/audit"
            active-class="active"
            exact-active-class="active"
            class="nav-link"
          >
            Audit Log
          </RouterLink>
          <RouterLink
            to="/admin/config"
            active-class="active"
            exact-active-class="active"
            class="nav-link"
          >
            Config
          </RouterLink>
          <RouterLink
            to="/admin/courses"
            active-class="active"
            exact-active-class="active"
            class="nav-link"
          >
            Course Oversight
          </RouterLink>
          <!-- @{"req": ["REQ-FEONBOARD-002"]} -->
          <RouterLink
            to="/admin/getting-started"
            active-class="active"
            exact-active-class="active"
            class="nav-link"
          >
            Getting Started
          </RouterLink>
        </div>
      </nav>
    </aside>

    <main class="main-content">
      <header class="top-bar">
        <div class="top-bar-content">
          <div class="admin-info">
            Admin: {{ username }}
          </div>
          <button class="logout-button" @click="handleLogout">
            Logout
          </button>
        </div>
      </header>

      <div class="page-content">
        <RouterView />
      </div>
    </main>
  </div>
</template>

<style scoped>
.admin-layout {
  display: flex;
  min-height: 100vh;
  background-color: #f5f5f5;
}

.sidebar {
  width: 250px;
  background-color: #2c3e50;
  color: #ecf0f1;
  position: fixed;
  left: 0;
  top: 0;
  bottom: 0;
  overflow-y: auto;
}

.sidebar-nav {
  padding: 2rem 0;
}

.nav-links {
  display: flex;
  flex-direction: column;
}

.nav-link {
  display: block;
  padding: 1rem 1.5rem;
  color: #ecf0f1;
  text-decoration: none;
  transition: all 0.2s ease;
  border-left: 4px solid transparent;
}

.nav-link:hover {
  background-color: rgba(255, 255, 255, 0.1);
  border-left-color: #3498db;
}

.nav-link.active {
  background-color: rgba(52, 152, 219, 0.2);
  border-left-color: #3498db;
  color: #3498db;
  font-weight: 500;
}

.main-content {
  margin-left: 250px;
  flex: 1;
  display: flex;
  flex-direction: column;
}

.top-bar {
  background-color: white;
  border-bottom: 1px solid #ddd;
  padding: 1rem 2rem;
  box-shadow: 0 2px 4px rgba(0, 0, 0, 0.05);
}

.top-bar-content {
  display: flex;
  justify-content: space-between;
  align-items: center;
  max-width: 100%;
}

.admin-info {
  font-size: 1rem;
  color: #333;
  font-weight: 500;
}

.logout-button {
  padding: 0.5rem 1rem;
  background-color: #e74c3c;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 0.95rem;
  transition: background-color 0.2s;
}

.logout-button:hover {
  background-color: #c0392b;
}

.page-content {
  flex: 1;
  padding: 2rem;
  overflow-y: auto;
}
</style>
