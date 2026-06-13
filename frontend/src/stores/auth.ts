// @{"req": ["REQ-FEAUTH-001", "REQ-FEAUTH-010", "REQ-FEAUTH-011", "REQ-FEAUTH-019", "REQ-FEAUTH-020", "REQ-FEAUTH-040", "REQ-FEAUTH-041", "REQ-FEAUTH-042", "REQ-FEAUTH-045", "REQ-FEAUTH-118", "REQ-FEAUTH-119", "REQ-FEAUTH-155", "REQ-FEAUTH-156", "REQ-FEAUTH-169", "REQ-FEAUTH-170", "REQ-FEAUTH-171", "REQ-FEAUTH-200", "REQ-FEAUTH-201", "REQ-FEPROFILE-004"]}

import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { get, post, setUnauthorizedHandler, setMustChangePasswordHandler, ApiError } from '@/api/client'
import { useCourseStore } from '@/stores/course'
import { useRouter } from 'vue-router'

interface SessionResponse {
  user_id: string
  username: string
  role: 'student' | 'admin'
  consented: boolean
  expires_at: string
  must_change_password?: boolean
  onboarding_prompted?: boolean
}

interface LoginResponse {
  two_factor_required?: boolean
  pending_token?: string
  expires_at?: string
  token?: string
  role?: 'student' | 'admin'
  must_change_password?: boolean
}

interface VerifyOtpResponse {
  token: string
  role: 'student' | 'admin'
  expires_at: string
  must_change_password?: boolean
}

interface ResendOtpResponse {
  expires_at: string
  resend_available_at: string | null
}

// @{"req": ["REQ-FEAUTH-001", "REQ-FEAUTH-010", "REQ-FEAUTH-011", "REQ-FEAUTH-019", "REQ-FEAUTH-020", "REQ-FEAUTH-040", "REQ-FEAUTH-041", "REQ-FEAUTH-042", "REQ-FEAUTH-045", "REQ-FEAUTH-118", "REQ-FEAUTH-119", "REQ-FEAUTH-155", "REQ-FEAUTH-156", "REQ-FEAUTH-169", "REQ-FEAUTH-170", "REQ-FEAUTH-171", "REQ-FEUSER-002", "REQ-FEPROFILE-004"]}
export const useAuthStore = defineStore('auth', () => {
  const router = useRouter()
  // REQ-FEAUTH-171: the raw session token is NOT stored in reactive state.
  // After login the store is populated via GET /api/v1/auth/session so the
  // token never lives in JS-readable memory beyond the login response lifetime.
  const userId = ref<string | null>(null)
  const username = ref<string | null>(null)
  const role = ref<'student' | 'admin' | null>(null)
  const expiresAt = ref<number | null>(null)
  const isConsented = ref(false)
  const mustChangePassword = ref(false)
  // @{"req": ["REQ-FEPROFILE-004"]}
  const onboardingPrompted = ref(false)

  // @{"req": ["REQ-FEAUTH-201"]}
  // pendingTwoFactor holds the short-lived token between login phase 1 and phase 2.
  // It is in-memory only: a page reload loses it and the user must restart login.
  // pendingTwoFactor being non-null does NOT mean the user is authenticated.
  const pendingTwoFactor = ref<{
    token: string
    expiresAt: number
  } | null>(null)

  // restoreDone is false until the first restoreSession() call completes (success
  // or failure). Retained for backwards compatibility with any code that reads it
  // directly; the router guard now awaits restorePromise instead (REQ-FEAUTH-170).
  const restoreDone = ref(false)

  // restorePromise is set the moment restoreSession() starts and resolves when
  // the session-restore fetch completes (success or failure). The router guard
  // awaits this promise so navigation blocks until auth state is known, which
  // eliminates the race where protected components mount before restore completes
  // and fire spurious 401 API calls (REQ-FEAUTH-170).
  let restorePromise: Promise<void> | null = null

  // @{"req": ["REQ-FEAUTH-040", "REQ-FEAUTH-041", "REQ-FEAUTH-042"]}
  // isAuthenticated is based on role rather than a token ref so it works
  // correctly in the cookie-only path after a page refresh.
  const isAuthenticated = computed(() => role.value !== null)

  // @{"req": ["REQ-FEAUTH-041"]}
  const isStudent = computed(() => role.value === 'student')

  // @{"req": ["REQ-FEAUTH-042"]}
  const isAdmin = computed(() => role.value === 'admin')

  // @{"req": ["REQ-FEAUTH-155"]}
  const isExpired = computed(() => expiresAt.value !== null && Date.now() / 1000 > expiresAt.value)

  // @{"req": ["REQ-FEAUTH-169", "REQ-FEAUTH-170", "REQ-FEAUTH-171"]}
  // restoreSession is called once at SPA boot (App.vue onMounted) to re-hydrate
  // auth state from the server-side session via the httpOnly cookie. On 200 the
  // store is fully populated. On 401 or network error all state is cleared,
  // which the router interprets as "not logged in".
  //
  // Idempotent after the first call: subsequent calls return the same promise so
  // the router guard's await never triggers a second network request.
  function restoreSession(): Promise<void> {
    if (restorePromise !== null) {
      return restorePromise
    }
    restorePromise = (async () => {
      try {
        const data = await get<SessionResponse>('/api/v1/auth/session')
        userId.value = data.user_id
        username.value = data.username
        role.value = data.role
        expiresAt.value = new Date(data.expires_at).getTime() / 1000
        isConsented.value = data.consented
        mustChangePassword.value = data.must_change_password ?? false
        onboardingPrompted.value = data.onboarding_prompted ?? false
        registerUnauthorizedHandler()
        registerMustChangePasswordHandler()
      } catch {
        // 401 or network error — treat as not logged in.
        userId.value = null
        username.value = null
        role.value = null
        expiresAt.value = null
        isConsented.value = false
        mustChangePassword.value = false
        onboardingPrompted.value = false
      } finally {
        restoreDone.value = true
      }
    })()
    return restorePromise
  }

  // @{"req": ["REQ-FEAUTH-010", "REQ-FEAUTH-011", "REQ-FEAUTH-171"]}
  // login is called after a successful POST /api/v1/auth/login. Rather than
  // writing the raw token into reactive state (REQ-FEAUTH-171), it delegates
  // to restoreSession() which populates the store from GET /api/v1/auth/session.
  // The server has already set the __Host-session cookie, so the GET request
  // will authenticate via that cookie.
  async function login(): Promise<void> {
    // Force a FRESH restore: the boot-time restoreSession() (which 401'd for a
    // logged-out visitor) is memoized in restorePromise; without clearing it,
    // this call would return that stale resolved promise and the store would
    // never pick up the new session — leaving the user stuck on /login.
    restorePromise = null
    await restoreSession()
  }

  // @{"req": ["REQ-FEAUTH-021", "REQ-FEAUTH-155", "REQ-FEAUTH-156"]}
  function logout(): void {
    userId.value = null
    username.value = null
    role.value = null
    expiresAt.value = null
    isConsented.value = false
    mustChangePassword.value = false
    onboardingPrompted.value = false
    setUnauthorizedHandler(null)
    // Reset restorePromise so that a subsequent login (same page session without
    // a reload) triggers a fresh GET /api/v1/auth/session call rather than
    // returning the stale resolved promise from the previous session.
    restorePromise = null
    // Clear user-scoped stores so stale data from the previous session cannot
    // leak into the next session (e.g. courses list belonging to another user).
    useCourseStore().reset()
  }

  // @{"req": ["REQ-FEAUTH-019", "REQ-FEAUTH-020", "REQ-FEAUTH-118", "REQ-FEAUTH-119"]}
  // logoutServer invalidates the server-side session before clearing local state.
  // After a page refresh, token is null and the session cookie is the credential;
  // the POST carries the cookie automatically so no explicit token is needed.
  // Errors are swallowed so the user is never stranded in a logged-in state.
  async function logoutServer(): Promise<void> {
    try {
      await post<void>('/api/v1/auth/logout', {})
    } catch {
      // Intentionally swallow — server session may already be expired or
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

  // @{"req": ["REQ-FEUSER-002"]}
  function registerMustChangePasswordHandler(): void {
    setMustChangePasswordHandler(() => {
      mustChangePassword.value = true
      router.push('/change-password')
    })
  }

  // @{"req": ["REQ-FEAUTH-170"]}
  // getRestorePromise returns the in-flight (or completed) restoreSession promise
  // so the router guard can await it. Returns null before restoreSession() is called.
  function getRestorePromise(): Promise<void> | null {
    return restorePromise
  }

  // @{"req": ["REQ-FEUSER-001", "REQ-FEPROFILE-004"]}
  // refreshSession refreshes the auth state from the server without clearing the
  // memoized restorePromise, allowing the guard to work correctly after password change.
  async function refreshSession(): Promise<void> {
    try {
      const data = await get<SessionResponse>('/api/v1/auth/session')
      userId.value = data.user_id
      username.value = data.username
      role.value = data.role
      expiresAt.value = new Date(data.expires_at).getTime() / 1000
      isConsented.value = data.consented
      mustChangePassword.value = data.must_change_password ?? false
      onboardingPrompted.value = data.onboarding_prompted ?? false
      registerUnauthorizedHandler()
      registerMustChangePasswordHandler()
    } catch {
      userId.value = null
      username.value = null
      role.value = null
      expiresAt.value = null
      isConsented.value = false
      mustChangePassword.value = false
      onboardingPrompted.value = false
    }
  }

  // @{"req": ["REQ-FEAUTH-201"]}
  // setPendingTwoFactor is called by LoginView after a 202 response with two_factor_required.
  function setPendingTwoFactor(token: string, expiresAt: string): void {
    pendingTwoFactor.value = {
      token,
      expiresAt: new Date(expiresAt).getTime() / 1000
    }
  }

  // @{"req": ["REQ-FEAUTH-201"]}
  // clearPendingTwoFactor clears the pending state.
  // Called on success, lockout, or user cancellation.
  function clearPendingTwoFactor(): void {
    pendingTwoFactor.value = null
  }

  // @{"req": ["REQ-FEAUTH-200"]}
  // verifyOtp calls POST /api/v1/auth/2fa/verify with the pending token and OTP.
  // On success, calls login() to populate session state.
  async function verifyOtp(otp: string): Promise<void> {
    if (!pendingTwoFactor.value) {
      throw new Error('No pending 2FA token')
    }

    try {
      await post<VerifyOtpResponse>('/api/v1/auth/2fa/verify', {
        pending_token: pendingTwoFactor.value.token,
        otp
      })

      clearPendingTwoFactor()
      await login()
    } catch (err) {
      throw err
    }
  }

  // @{"req": ["REQ-FEAUTH-200"]}
  // resendOtp calls POST /api/v1/auth/2fa/resend with the pending token.
  // Returns { resendAvailableAt: string | null } from the server response.
  async function resendOtp(): Promise<{ resendAvailableAt: string | null }> {
    if (!pendingTwoFactor.value) {
      throw new Error('No pending 2FA token')
    }

    const response = await post<ResendOtpResponse>('/api/v1/auth/2fa/resend', {
      pending_token: pendingTwoFactor.value.token
    })

    return {
      resendAvailableAt: response.resend_available_at
    }
  }

  return {
    userId,
    username,
    role,
    expiresAt,
    isConsented,
    mustChangePassword,
    onboardingPrompted,
    pendingTwoFactor,
    restoreDone,
    isAuthenticated,
    isStudent,
    isAdmin,
    isExpired,
    login,
    logout,
    logoutServer,
    restoreSession,
    refreshSession,
    getRestorePromise,
    setConsented,
    registerUnauthorizedHandler,
    registerMustChangePasswordHandler,
    setPendingTwoFactor,
    clearPendingTwoFactor,
    verifyOtp,
    resendOtp
  }
})
