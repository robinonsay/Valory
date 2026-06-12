// @{"req": ["REQ-FEADMIN-050", "REQ-FEADMIN-051", "REQ-FEADMIN-052", "REQ-FEADMIN-053", "REQ-FEADMIN-400", "REQ-FEADMIN-401", "REQ-FEADMIN-402", "REQ-FEADMIN-403", "REQ-FEADMIN-404", "REQ-FEADMIN-405", "REQ-FEADMIN-406"]}

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { get, ApiError } from '@/api/client'

interface OversightCourse {
  id: string
  title: string
  status: string
  student_id: string
  created_at: string
  updated_at: string
}

interface CoursesResponse {
  courses: OversightCourse[]
  next_cursor: string
}

const auth = useAuthStore()

const courses = ref<OversightCourse[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const selectedStatus = ref('all')
const nextCursor = ref<string>('')

const statusOptions = [
  { value: 'all', label: 'All' },
  { value: 'intake', label: 'Intake' },
  { value: 'syllabus_draft', label: 'Syllabus Draft' },
  { value: 'syllabus_approved', label: 'Syllabus Approved' },
  { value: 'generating', label: 'Generating' },
  { value: 'active', label: 'Active' },
  { value: 'archived', label: 'Archived' },
  { value: 'completed', label: 'Completed' }
]

async function fetchCourses(status: string = 'all', cursor: string = '') {
  try {
    loading.value = true
    error.value = null

    let url = '/api/v1/courses?limit=20'
    if (status !== 'all') {
      url += `&status=${status}`
    }
    if (cursor) {
      url += `&cursor=${cursor}`
    }

    const response = await get<CoursesResponse>(url, auth.token)
    courses.value.push(...(response.courses ?? []))
    nextCursor.value = response.next_cursor
  } catch (err) {
    if (err instanceof ApiError) {
      error.value = `Failed to load courses: ${err.message}`
    } else {
      error.value = 'Failed to load courses'
    }
  } finally {
    loading.value = false
  }
}

async function handleStatusChange(newStatus: string) {
  selectedStatus.value = newStatus
  courses.value = []
  nextCursor.value = ''
  await fetchCourses(newStatus)
}

async function loadMore() {
  await fetchCourses(selectedStatus.value, nextCursor.value)
}

function formatDate(dateString: string): string {
  const date = new Date(dateString)
  return date.toLocaleDateString('en-US', { year: 'numeric', month: 'short', day: 'numeric' })
}

onMounted(() => {
  fetchCourses()
})
</script>

<template>
  <div class="course-oversight">
    <div class="header">
      <h1>Course Oversight</h1>
      <div class="filter-section">
        <label for="status-filter">Status:</label>
        <select
          id="status-filter"
          :value="selectedStatus"
          @change="(e) => handleStatusChange((e.target as HTMLSelectElement).value)"
          class="status-filter"
        >
          <option v-for="option in statusOptions" :key="option.value" :value="option.value">
            {{ option.label }}
          </option>
        </select>
      </div>
    </div>

    <div v-if="loading && courses.length === 0" class="loading">
      Loading courses...
    </div>

    <div v-else-if="error" class="error">
      {{ error }}
    </div>

    <div v-else class="content">
      <table v-if="courses.length > 0" class="courses-table">
        <thead>
          <tr>
            <th>Title</th>
            <th>Status</th>
            <th>Student ID</th>
            <th>Created</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="course in courses" :key="course.id" class="course-row">
            <td class="title">{{ course.title }}</td>
            <td class="status">{{ course.status }}</td>
            <td class="student-id">{{ course.student_id }}</td>
            <td class="created">{{ formatDate(course.created_at) }}</td>
          </tr>
        </tbody>
      </table>

      <div v-else class="no-courses">
        No courses found.
      </div>

      <div v-if="nextCursor && !loading" class="load-more-section">
        <button @click="loadMore" class="load-more-button">
          Load More
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.course-oversight {
  padding: 2rem;
  max-width: 1400px;
  margin: 0 auto;
}

.header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 2rem;
  border-bottom: 1px solid #ddd;
  padding-bottom: 1rem;
}

.header h1 {
  margin: 0;
  font-size: 2rem;
  color: #333;
}

.filter-section {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}

.filter-section label {
  font-weight: 600;
  color: #333;
}

.status-filter {
  padding: 0.5rem 0.75rem;
  border: 1px solid #ddd;
  border-radius: 4px;
  font-size: 1rem;
  cursor: pointer;
}

.status-filter:hover {
  border-color: #999;
}

.loading {
  text-align: center;
  padding: 2rem;
  font-size: 1.1rem;
  color: #666;
}

.error {
  padding: 1rem;
  background-color: #ffebee;
  color: #d32f2f;
  border-radius: 4px;
  border-left: 4px solid #d32f2f;
  margin-bottom: 1rem;
}

.content {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.courses-table {
  width: 100%;
  border-collapse: collapse;
  background-color: white;
  border-radius: 4px;
  overflow: hidden;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
}

.courses-table thead {
  background-color: #f5f5f5;
  font-weight: 600;
}

.courses-table th {
  padding: 1rem;
  text-align: left;
  border-bottom: 2px solid #ddd;
  color: #333;
}

.courses-table td {
  padding: 1rem;
  border-bottom: 1px solid #eee;
}

.course-row:hover {
  background-color: #fafafa;
}

.course-row:last-child td {
  border-bottom: none;
}

.title {
  font-weight: 500;
  color: #1976d2;
}

.status {
  text-transform: capitalize;
  font-size: 0.9rem;
}

.student-id {
  font-size: 0.9rem;
  color: #666;
}

.created {
  font-size: 0.9rem;
  color: #666;
}

.no-courses {
  text-align: center;
  padding: 2rem;
  color: #999;
  font-size: 1.1rem;
}

.load-more-section {
  display: flex;
  justify-content: center;
  padding: 1rem;
}

.load-more-button {
  padding: 0.75rem 1.5rem;
  background-color: #1976d2;
  color: white;
  border: none;
  border-radius: 4px;
  font-weight: 500;
  cursor: pointer;
  font-size: 1rem;
  transition: background-color 0.2s;
}

.load-more-button:hover {
  background-color: #1565c0;
}

.load-more-button:disabled {
  background-color: #bbb;
  cursor: not-allowed;
}
</style>
