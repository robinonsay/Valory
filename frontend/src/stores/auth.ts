// @{"req": ["REQ-FEAUTH-001", "REQ-FEAUTH-010", "REQ-FEAUTH-011", "REQ-FEAUTH-019", "REQ-FEAUTH-020", "REQ-FEAUTH-040", "REQ-FEAUTH-041", "REQ-FEAUTH-042", "REQ-FEAUTH-045", "REQ-FEAUTH-118", "REQ-FEAUTH-119", "REQ-FEAUTH-155", "REQ-FEAUTH-156"]}

import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { post, setUnauthorizedHandler } from '@/api/client'

// @{"req": ["REQ-FEAUTH-001", "REQ-FEAUTH-010", "REQ-FEAUTH-011", "REQ-FEAUTH-019", "REQ-FEAUTH-020", "REQ-FEAUTH-040", "REQ-FEAUTH-041", "REQ-FEAUTH-042", "REQ-FEAUTH-045", "REQ-FEAUTH-118", "REQ-FEAUTH-119", "REQ-FEAUTH-155", "REQ-FEAUTH-156"]}
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

  // @{"req": ["REQ-FEAUTH-019", "REQ-FEAUTH-020", "REQ-FEAUTH-118", "REQ-FEAUTH-119"]}
  // logoutServer invalidates the server-side session token before clearing local
  // state. Errors from the server call are deliberately swallowed: the client
  // must clear auth state even when the network is unavailable so the user is
  // never left stranded in a logged-in state after clicking logout.
  // logoutServer lives in the store (not a composable) because it is a state
  // mutation — it reads token to send the server request and delegates to
  // logout() to clear that same state. Separating it into a composable would
  // require injecting the store, adding indirection for no practical benefit.
  async function logoutServer(): Promise<void> {
    const currentToken = token.value
    try {
      if (currentToken) {
        await post<void>('/api/v1/auth/logout', {}, currentToken)
      }
    } catch {
      // Intentionally swallow — the server session may already be expired or
      // the network may be offline. Client state must always be cleared.
    }
    logout()
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
    logoutServer,
    setConsented,
    registerUnauthorizedHandler
  }
})
