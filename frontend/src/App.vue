<script setup lang="ts">
// @{"req": ["REQ-FEAUTH-169", "REQ-FEAUTH-170", "REQ-FESETUP-001", "REQ-SYS-071"]}
import { onMounted } from 'vue'
import { RouterView } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useSetupStore } from '@/stores/setup'

const auth = useAuthStore()
const setup = useSetupStore()

// REQ-FEAUTH-169: call restoreSession once at SPA boot to kick off the
// session-restore fetch. The router guard awaits auth.getRestorePromise() so
// navigation blocks until the fetch completes — no router.replace() correction
// hack is needed; the guard itself decides the correct destination the first
// time (REQ-FEAUTH-170).
// REQ-FESETUP-001: call checkSetupStatus once at SPA boot alongside
// restoreSession. The router guard awaits setup.getSetupPromise() so navigation
// blocks until setup status is known before any routing decision.
// Both calls are fire-and-forget: on the very first navigation the guard may
// observe a null setupPromise/needsSetup (before this onMounted runs) and falls
// through its setup rule; it self-corrects on the next navigation once status is
// known, so there is no freeze and no way to slip past the setup gate.
onMounted(() => {
  auth.restoreSession()
  setup.checkSetupStatus()
})
</script>

<template>
  <RouterView />
</template>
