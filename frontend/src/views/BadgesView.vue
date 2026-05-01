<script setup lang="ts">
// @{"req": ["REQ-FECONTENT-050", "REQ-FECONTENT-051", "REQ-FECONTENT-052", "REQ-FECONTENT-053", "REQ-FECONTENT-165", "REQ-FECONTENT-170", "REQ-FECONTENT-175", "REQ-FECONTENT-180"]}
import { ref, onMounted } from 'vue'
import { get, ApiError } from '@/api/client'
import { useAuthStore } from '@/stores/auth'

interface StudentBadge {
  id: string
  badge_id: string
  awarded_at: string
  redeemed_for_submission_id: string | null
  badge_name: string
  badge_reward: string
  badge_reward_value: number
}

const authStore = useAuthStore()

const badges = ref<StudentBadge[]>([])
const loading = ref(true)
const error = ref<string | null>(null)

onMounted(async () => {
  try {
    loading.value = true
    error.value = null
    badges.value = await get<StudentBadge[]>('/api/v1/users/me/badges', authStore.token)
  } catch (err) {
    if (err instanceof ApiError) {
      error.value = 'Failed to load badges. Please try again.'
    } else {
      error.value = 'An unexpected error occurred.'
    }
  } finally {
    loading.value = false
  }
})

const formatDate = (dateString: string): string => {
  return new Date(dateString).toLocaleDateString()
}
</script>

<template>
  <div class="badges-container">
    <h1>Your Badges</h1>

    <div v-if="loading" class="loading-state">
      Loading badges...
    </div>

    <div v-else-if="error" class="error-state">
      {{ error }}
    </div>

    <div v-else-if="badges.length === 0" class="empty-state">
      No badges earned yet.
    </div>

    <div v-else class="badges-grid">
      <div v-for="badge in badges" :key="badge.id" class="badge-card">
        <h2 class="badge-name">{{ badge.badge_name }}</h2>
        <p class="badge-reward">
          {{ badge.badge_reward }}: {{ badge.badge_reward_value }}
        </p>
        <p class="badge-awarded">
          Awarded: {{ formatDate(badge.awarded_at) }}
        </p>
        <p class="badge-redemption-status">
          <span v-if="badge.redeemed_for_submission_id">
            Applied to submission: {{ badge.redeemed_for_submission_id }}
          </span>
          <span v-else>
            Not yet redeemed
          </span>
        </p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.badges-container {
  padding: 2rem;
  max-width: 1200px;
  margin: 0 auto;
}

h1 {
  font-size: 2rem;
  margin-bottom: 2rem;
  color: #333;
}

.loading-state,
.error-state,
.empty-state {
  padding: 1rem;
  text-align: center;
  font-size: 1rem;
  color: #666;
}

.error-state {
  background-color: #ffe0e0;
  color: #d32f2f;
  border: 1px solid #ffcdd2;
  border-radius: 4px;
}

.badges-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 2rem;
}

.badge-card {
  border: 1px solid #ddd;
  border-radius: 8px;
  padding: 1.5rem;
  background-color: #f9f9f9;
  box-shadow: 0 2px 4px rgba(0, 0, 0, 0.1);
  transition: box-shadow 0.3s ease;
}

.badge-card:hover {
  box-shadow: 0 4px 8px rgba(0, 0, 0, 0.15);
}

.badge-name {
  font-size: 1.25rem;
  font-weight: bold;
  margin: 0 0 0.5rem 0;
  color: #333;
}

.badge-reward,
.badge-awarded,
.badge-redemption-status {
  margin: 0.5rem 0;
  color: #555;
  font-size: 0.95rem;
}
</style>
