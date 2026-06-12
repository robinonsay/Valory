// @{"req": ["REQ-FEAUTH-019", "REQ-FEAUTH-118", "REQ-FEONBOARD-002"]}

<script setup lang="ts">
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const auth = useAuthStore()

// @{"req": ["REQ-FEAUTH-019", "REQ-FEAUTH-020", "REQ-FEAUTH-118", "REQ-FEAUTH-119"]}
const handleLogout = async (): Promise<void> => {
  await auth.logoutServer()
  await router.push('/login')
}
</script>

<template>
  <!-- @{"req": ["REQ-FEAUTH-118", "REQ-FEONBOARD-002"]} -->
  <div class="student-layout">
    <header class="top-bar">
      <div class="top-bar-content">
        <RouterLink to="/courses" class="brand-link">
          <img src="/valory.svg" alt="Valory" class="logo" />
          <span class="brand-text">Valory</span>
        </RouterLink>
        <nav class="nav-links">
          <RouterLink
            to="/courses"
            active-class="active"
            class="nav-link"
          >
            Courses
          </RouterLink>
          <RouterLink
            to="/getting-started"
            active-class="active"
            class="nav-link"
          >
            Getting Started
          </RouterLink>
        </nav>
        <button class="logout-button" @click="handleLogout">
          Logout
        </button>
      </div>
    </header>

    <main class="page-content">
      <RouterView />
    </main>
  </div>
</template>

<style scoped>
.student-layout {
  display: flex;
  flex-direction: column;
  min-height: 100vh;
  background-color: #f5f5f5;
}

.top-bar {
  background-color: white;
  border-bottom: 1px solid #ddd;
  padding: 0.75rem 2rem;
  box-shadow: 0 2px 4px rgba(0, 0, 0, 0.05);
  position: sticky;
  top: 0;
  z-index: 100;
}

.top-bar-content {
  display: flex;
  align-items: center;
  gap: 2rem;
  max-width: 1200px;
  margin: 0 auto;
  width: 100%;
}

.brand-link {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  text-decoration: none;
  flex-shrink: 0;
  transition: opacity 0.2s ease;
}

.brand-link:hover {
  opacity: 0.8;
}

.logo {
  height: 32px;
  width: 32px;
}

.brand-text {
  font-size: 1.25rem;
  font-weight: 700;
  color: #2c3e50;
  letter-spacing: 0.02em;
}

.nav-links {
  display: flex;
  gap: 0.25rem;
  flex: 1;
}

.nav-link {
  display: inline-block;
  padding: 0.5rem 1rem;
  color: #555;
  text-decoration: none;
  border-radius: 4px;
  font-size: 0.95rem;
  transition: all 0.2s ease;
}

.nav-link:hover {
  background-color: #f0f0f0;
  color: #333;
}

.nav-link.active {
  background-color: #e3f2fd;
  color: #1976d2;
  font-weight: 500;
}

.logout-button {
  margin-left: auto;
  padding: 0.5rem 1rem;
  background-color: #e74c3c;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 0.95rem;
  transition: background-color 0.2s;
  flex-shrink: 0;
}

.logout-button:hover {
  background-color: #c0392b;
}

.page-content {
  flex: 1;
  overflow-y: auto;
}
</style>
