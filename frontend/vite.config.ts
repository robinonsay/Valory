import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url))
    }
  },
  server: {
    proxy: {
      '/api': {
        target: 'https://localhost:8443',
        changeOrigin: true,
        secure: false
      }
    }
  },
  test: {
    environment: 'jsdom',
    globals: true,
    // Exclude the Playwright e2e directory so Vitest never picks up e2e specs.
    // e2e tests import @playwright/test which is not available in jsdom and
    // would cause parse errors if Vitest tried to collect them.
    exclude: ['e2e/**', 'node_modules/**']
  }
})
