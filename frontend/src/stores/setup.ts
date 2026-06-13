// @{"req": ["REQ-FESETUP-001", "REQ-FESETUP-002", "REQ-FESETUP-003", "REQ-SYS-071"]}
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { get } from '@/api/client'

interface SetupStatusResponse {
  needs_setup: boolean
}

export const useSetupStore = defineStore('setup', () => {
  // null = not yet checked; true = setup required; false = setup complete
  const needsSetup = ref<boolean | null>(null)

  // setupPromise is set once checkSetupStatus() is first called and resolves
  // when the fetch completes. Subsequent calls return the same promise so the
  // router guard's await never fires a second network request (mirrors the
  // auth store's restorePromise pattern).
  let setupPromise: Promise<void> | null = null

  // @{"req": ["REQ-FESETUP-001"]}
  function checkSetupStatus(): Promise<void> {
    if (setupPromise !== null) return setupPromise
    setupPromise = (async () => {
      try {
        const data = await get<SetupStatusResponse>('/api/v1/setup/status')
        needsSetup.value = data.needs_setup
      } catch {
        // Network or server error: treat as setup not required to avoid
        // blocking the app indefinitely. The POST /api/v1/setup endpoint will
        // return 409 if an admin actually exists.
        needsSetup.value = false
      }
    })()
    return setupPromise
  }

  // @{"req": ["REQ-FESETUP-001", "REQ-FESETUP-002", "REQ-FESETUP-003"]}
  function getSetupPromise(): Promise<void> | null {
    return setupPromise
  }

  // @{"req": ["REQ-FESETUP-003"]}
  // markComplete sets needsSetup to false after a successful POST /api/v1/setup
  // so the guard does not redirect back to /setup before the router navigates
  // to /login.
  function markComplete(): void {
    needsSetup.value = false
  }

  return { needsSetup, checkSetupStatus, getSetupPromise, markComplete }
})
