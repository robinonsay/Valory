// @{"req": ["REQ-FEAUTH-001", "REQ-FEAUTH-010", "REQ-FEAUTH-011", "REQ-FEAUTH-040", "REQ-FEAUTH-041", "REQ-FEAUTH-042", "REQ-FEAUTH-045", "REQ-FEAUTH-155", "REQ-FEAUTH-156"]}

import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { setUnauthorizedHandler } from '@/api/client'

// @{"req": ["REQ-FEAUTH-001", "REQ-FEAUTH-010", "REQ-FEAUTH-011", "REQ-FEAUTH-040", "REQ-FEAUTH-041", "REQ-FEAUTH-042", "REQ-FEAUTH-045", "REQ-FEAUTH-155", "REQ-FEAUTH-156"]}
export const useAuthStore = defineStore('auth', () => {
  const token = ref<string | null>(null)
  const role = ref<'student' | 'admin' | null>(null)
  const expiresAt = ref<number | null>(null)
  const isConsented = ref(false)

  // @{"req": ["REQ-FEAUTH-040", "REQ-FEAUTH-041", "REQ-FEAUTH-042"]}
  const isAuthenticated = computed(() => token.value !== null)

  // @{"req": ["REQ-FEAUTH-041"]}
  const isStudent = computed(() => role.value === 'student')

  // @{"req": ["REQ-FEAUTH-042"]}
  const isAdmin = computed(() => role.value === 'admin')

  // @{"req": ["REQ-FEAUTH-155"]}
  const isExpired = computed(() => expiresAt.value !== null && Date.now() / 1000 > expiresAt.value)

  // @{"req": ["REQ-FEAUTH-010", "REQ-FEAUTH-011", "REQ-FEAUTH-045"]}
  function login(newToken: string, newRole: 'student' | 'admin', newExpiresAt: number): void {
    token.value = newToken
    role.value = newRole
    expiresAt.value = newExpiresAt
  }

  // @{"req": ["REQ-FEAUTH-021", "REQ-FEAUTH-155", "REQ-FEAUTH-156"]}
  function logout(): void {
    token.value = null
    role.value = null
    expiresAt.value = null
    isConsented.value = false
    setUnauthorizedHandler(null)
  }

  // @{"req": ["REQ-FEAUTH-036"]}
  function setConsented(): void {
    isConsented.value = true
  }

  // @{"req": ["REQ-FEAUTH-155", "REQ-FEAUTH-156"]}
  function registerUnauthorizedHandler(): void {
    setUnauthorizedHandler(() => logout())
  }

  return {
    token,
    role,
    expiresAt,
    isConsented,
    isAuthenticated,
    isStudent,
    isAdmin,
    isExpired,
    login,
    logout,
    setConsented,
    registerUnauthorizedHandler
  }
})
