// @{"req": ["REQ-FECONTENT-040", "REQ-FECONTENT-041", "REQ-FECONTENT-042", "REQ-FECONTENT-043", "REQ-FECONTENT-145", "REQ-FECONTENT-150", "REQ-FECONTENT-155", "REQ-FECONTENT-160"]}

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { get, ApiError } from '@/api/client'

interface GradeSummaryResponse {
  course_id: string
  student_id: string
  weighted_score: number
  total_weight: number
}

const route = useRoute()
const auth = useAuthStore()

const courseId = computed(() => route.params.id as string)
const summary = ref<GradeSummaryResponse | null>(null)
const isLoading = ref(false)
const error = ref<string | null>(null)

const fetchGrades = async (): Promise<void> => {
  if (!auth.isAuthenticated) {
    error.value = 'Not authenticated'
    return
  }

  isLoading.value = true
  error.value = null

  try {
    summary.value = await get<GradeSummaryResponse>(
      `/api/v1/courses/${courseId.value}/grade`
    )
  } catch (err) {
    if (err instanceof ApiError) {
      error.value = 'Failed to load grades. Please try again.'
    } else {
      error.value = 'Failed to load grades. Please try again.'
    }
  } finally {
    isLoading.value = false
  }
}

onMounted(() => {
  fetchGrades()
})

const formatScore = (score: number): string => {
  return score.toFixed(2)
}
</script>

<template>
  <div class="grades-container">
    <div class="grades-header">
      <h1>Grades</h1>
    </div>

    <div v-if="isLoading" class="loading">
      <p>Loading grades...</p>
    </div>

    <div v-else-if="error" class="error-message">
      {{ error }}
    </div>

    <div v-else-if="summary">
      <div class="summary-card">
        <div class="summary-row">
          <span class="label">Weighted Score</span>
          <span class="value">{{ formatScore(summary.weighted_score) }}</span>
        </div>
        <div class="summary-row">
          <span class="label">Total Weight Completed</span>
          <span class="value">{{ formatScore(summary.total_weight) }}</span>
        </div>
      </div>
    </div>

    <div v-else class="empty-state">
      <p>No grades yet.</p>
    </div>
  </div>
</template>

<style scoped>
.grades-container {
  padding: 2rem;
  max-width: 1000px;
  margin: 0 auto;
}

.grades-header {
  margin-bottom: 2rem;
}

.grades-header h1 {
  font-size: 2rem;
  margin: 0;
  color: #333;
}

.loading,
.error-message {
  text-align: center;
  padding: 2rem;
  font-size: 1rem;
}

.error-message {
  color: #d32f2f;
  background-color: #ffebee;
  border-radius: 4px;
  border-left: 4px solid #d32f2f;
  padding: 1rem;
}

.empty-state {
  text-align: center;
  padding: 3rem 2rem;
  color: #666;
  font-size: 1.1rem;
}

.summary-card {
  background-color: white;
  border: 1px solid #ddd;
  border-radius: 4px;
  padding: 1.5rem;
  max-width: 400px;
}

.summary-row {
  display: flex;
  justify-content: space-between;
  padding: 0.75rem 0;
  border-bottom: 1px solid #eee;
}

.summary-row:last-child {
  border-bottom: none;
}

.label {
  color: #555;
  font-weight: 500;
}

.value {
  font-size: 1.2rem;
  font-weight: 600;
  color: #1976d2;
}
</style>
