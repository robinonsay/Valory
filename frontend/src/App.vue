<script setup lang="ts">
// @{"req": ["REQ-FEAUTH-169", "REQ-FEAUTH-170"]}
import { onMounted } from 'vue'
import { RouterView } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()

// REQ-FEAUTH-169: call restoreSession once at SPA boot to kick off the
// session-restore fetch. The router guard awaits auth.getRestorePromise() so
// navigation blocks until the fetch completes — no router.replace() correction
// hack is needed; the guard itself decides the correct destination the first
// time (REQ-FEAUTH-170).
onMounted(() => {
  auth.restoreSession()
})
</script>

<template>
  <RouterView />
</template>
