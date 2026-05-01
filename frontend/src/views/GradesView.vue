// @{"req": ["REQ-FECONTENT-040", "REQ-FECONTENT-041", "REQ-FECONTENT-042", "REQ-FECONTENT-043", "REQ-FECONTENT-145", "REQ-FECONTENT-150", "REQ-FECONTENT-155", "REQ-FECONTENT-160"]}

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { get, ApiError } from '@/api/client'

interface GradeItem {
  homework_id: string
  homework_title: string
  score: number | null
  max_score: number
  late_penalty: number
  final_score: number | null
  submitted_at: string | null
  graded_at: string | null
}

interface GradesResponse {
  grades: GradeItem[]
  overall_average: number | null
}

const route = useRoute()
const auth = useAuthStore()

const courseId = computed(() => route.params.id as string)
const grades = ref<GradeItem[]>([])
const overallAverage = ref<number | null>(null)
const isLoading = ref(false)
const error = ref<string | null>(null)

const fetchGrades = async (): Promise<void> => {
  if (!auth.token) {
    error.value = 'Not authenticated'
    return
  }

  isLoading.value = true
  error.value = null

  try {
    const response = await get<GradesResponse>(
      `/api/v1/courses/${courseId.value}/grades`,
      auth.token
    )
    grades.value = response.grades
    overallAverage.value = response.overall_average
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

const formatDate = (dateStr: string | null): string => {
  if (!dateStr) return ''
  return new Date(dateStr).toLocaleDateString()
}

const formatOverallAverage = (avg: number | null): string => {
  if (avg === null) return 'N/A'
  return avg.toFixed(2)
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

    <div v-else>
      <div class="grades-content">
        <div v-if="grades.length === 0" class="empty-state">
          <p>No grades yet.</p>
        </div>

        <div v-else class="grades-table-wrapper">
          <table class="grades-table">
            <thead>
              <tr>
                <th>Assignment</th>
                <th>Score</th>
                <th>Late Penalty</th>
                <th>Final Score</th>
                <th>Graded</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="item in grades" :key="item.homework_id">
                <td class="title">{{ item.homework_title }}</td>
                <td class="score">
                  <span v-if="item.score !== null">
                    {{ formatScore(item.score) }} / {{ formatScore(item.max_score) }}
                  </span>
                  <span v-else class="pending">Pending</span>
                </td>
                <td class="penalty">
                  <span v-if="item.late_penalty > 0">
                    Late penalty: -{{ item.late_penalty }}%
                  </span>
                  <span v-else>-</span>
                </td>
                <td class="final-score">
                  <span v-if="item.final_score !== null">
                    {{ formatScore(item.final_score) }}
                  </span>
                  <span v-else class="pending">Pending</span>
                </td>
                <td class="graded-at">
                  {{ item.graded_at ? formatDate(item.graded_at) : 'Pending' }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <div class="overall-summary">
          <h2>Overall Average</h2>
          <div class="average-value">
            {{ formatOverallAverage(overallAverage) }}
          </div>
        </div>
      </div>
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

.grades-content {
  display: flex;
  flex-direction: column;
  gap: 2rem;
}

.grades-table-wrapper {
  overflow-x: auto;
}

.grades-table {
  width: 100%;
  border-collapse: collapse;
  background-color: white;
  border: 1px solid #ddd;
  border-radius: 4px;
}

.grades-table thead {
  background-color: #f5f5f5;
  border-bottom: 2px solid #ddd;
}

.grades-table th {
  padding: 1rem;
  text-align: left;
  font-weight: 600;
  color: #333;
}

.grades-table td {
  padding: 1rem;
  border-bottom: 1px solid #eee;
  color: #666;
}

.grades-table tbody tr:hover {
  background-color: #fafafa;
}

.title {
  font-weight: 500;
  color: #333;
}

.score,
.penalty,
.final-score,
.graded-at {
  text-align: center;
}

.pending {
  color: #999;
  font-style: italic;
}

.overall-summary {
  background-color: #f9f9f9;
  border: 1px solid #ddd;
  border-radius: 4px;
  padding: 1.5rem;
  text-align: center;
}

.overall-summary h2 {
  margin: 0 0 1rem 0;
  font-size: 1.1rem;
  color: #333;
}

.average-value {
  font-size: 2rem;
  font-weight: 600;
  color: #1976d2;
}
</style>
