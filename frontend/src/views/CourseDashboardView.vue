// @{"req": ["REQ-FECOURSE-001", "REQ-FECOURSE-002", "REQ-FECOURSE-003", "REQ-FECOURSE-010", "REQ-FECOURSE-011", "REQ-FECOURSE-012", "REQ-FECOURSE-300", "REQ-FECOURSE-301"]}

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useCourseStore } from '@/stores/course'
import { post, ApiError } from '@/api/client'
import type { CourseResponse } from '@/types/course'

const router = useRouter()
const auth = useAuthStore()
const courseStore = useCourseStore()

const showCreateModal = ref(false)
const newCourseTopic = ref('')
const createError = ref<string | null>(null)
const isCreating = ref(false)

onMounted(async () => {
  if (auth.token) {
    await courseStore.fetchCourses(auth.token)
  }
})

const navigateToCourse = (courseId: string, status: string): void => {
  const routeMap: Record<string, string> = {
    'intake': `/courses/${courseId}/intake`,
    'syllabus_draft': `/courses/${courseId}/syllabus`,
    'generating': `/courses/${courseId}/generating`,
    'active': `/courses/${courseId}/hub`,
    'archived': `/courses/${courseId}/hub`,
    'completed': `/courses/${courseId}/hub`
  }
  const route = routeMap[status]
  if (route) {
    router.push(route)
  }
}

const openCreateModal = (): void => {
  showCreateModal.value = true
  createError.value = null
  newCourseTopic.value = ''
}

const closeCreateModal = (): void => {
  showCreateModal.value = false
  createError.value = null
  newCourseTopic.value = ''
}

const createNewCourse = async (): Promise<void> => {
  if (!auth.token) {
    return
  }

  isCreating.value = true
  createError.value = null

  try {
    const response = await post<CourseResponse>(
      '/api/v1/courses',
      { topic: newCourseTopic.value.trim() },
      auth.token
    )

    // The backend returns the created course FLAT (no {course} wrapper).
    courseStore.setCurrent(response)
    courseStore.courses.unshift(response)

    closeCreateModal()
    await router.push(`/courses/${response.id}/intake`)
    // Reset after navigation: normally the component unmounts, but if the
    // navigation is cancelled by a guard the re-opened modal must not be
    // stuck on the disabled "Creating..." state.
    isCreating.value = false
  } catch (err) {
    if (err instanceof ApiError) {
      if (err.status === 409) {
        createError.value = 'You already have an active course. Complete or archive it first.'
      } else {
        createError.value = 'Failed to create course. Please try again.'
      }
    } else {
      createError.value = 'Failed to create course. Please try again.'
    }
    isCreating.value = false
  }
}
</script>

<template>
  <div class="dashboard-container">
    <div class="dashboard-header">
      <h1>Courses</h1>
      <button class="create-button" @click="openCreateModal">New course</button>
    </div>

    <div v-if="courseStore.loading" class="loading">
      <p>Loading courses...</p>
    </div>

    <div v-else-if="courseStore.error" class="error-message">
      {{ courseStore.error }}
    </div>

    <div v-else-if="courseStore.sortedCourses.length === 0" class="empty-state">
      <p>You have no courses yet.</p>
      <button class="create-button" @click="openCreateModal">New course</button>
    </div>

    <div v-else class="courses-grid">
      <div
        v-for="course in courseStore.sortedCourses"
        :key="course.id"
        class="course-card"
        @click="navigateToCourse(course.id, course.status)"
      >
        <h3>{{ course.title || course.topic }}</h3>
        <div class="course-details">
          <span class="status-badge">{{ course.status }}</span>
          <span class="date">{{ new Date(course.created_at).toLocaleDateString() }}</span>
        </div>
        <button
          class="continue-button"
          @click.stop="navigateToCourse(course.id, course.status)"
        >
          Continue
        </button>
      </div>
    </div>

    <div v-if="showCreateModal" class="modal-overlay" @click.self="closeCreateModal">
      <div class="modal">
        <div class="modal-header">
          <h2>Create New Course</h2>
          <button class="close-button" @click="closeCreateModal">×</button>
        </div>

        <div v-if="createError" class="error-message">
          {{ createError }}
        </div>

        <form @submit.prevent="createNewCourse">
          <div class="form-group">
            <label for="course-topic">Topic</label>
            <input
              id="course-topic"
              v-model="newCourseTopic"
              type="text"
              placeholder="e.g. Linear Algebra"
              required
            />
          </div>

          <div class="modal-actions">
            <button type="button" class="cancel-button" @click="closeCreateModal">
              Cancel
            </button>
            <button
              type="submit"
              class="submit-button"
              :disabled="isCreating || newCourseTopic.trim() === ''"
            >
              {{ isCreating ? 'Creating...' : 'Create' }}
            </button>
          </div>
        </form>
      </div>
    </div>
  </div>
</template>

<style scoped>
.dashboard-container {
  padding: 2rem;
  max-width: 1200px;
  margin: 0 auto;
}

.dashboard-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 2rem;
}

.dashboard-header h1 {
  font-size: 2rem;
  margin: 0;
  color: #333;
}

.create-button {
  padding: 0.75rem 1.5rem;
  background-color: #1976d2;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 1rem;
  transition: background-color 0.2s;
}

.create-button:hover {
  background-color: #1565c0;
}

.create-button:disabled {
  background-color: #ccc;
  cursor: not-allowed;
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
}

.empty-state {
  text-align: center;
  padding: 3rem 2rem;
  color: #666;
}

.empty-state p {
  font-size: 1.1rem;
  margin-bottom: 1.5rem;
}

.courses-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 1.5rem;
}

.course-card {
  background: white;
  border: 1px solid #ddd;
  border-radius: 8px;
  padding: 1.5rem;
  cursor: pointer;
  transition: all 0.3s ease;
}

.course-card:hover {
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.1);
  transform: translateY(-2px);
}

.course-card h3 {
  margin: 0 0 1rem 0;
  font-size: 1.25rem;
  color: #333;
}

.course-details {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 1rem;
  font-size: 0.9rem;
  color: #666;
}

.status-badge {
  background-color: #e3f2fd;
  color: #1976d2;
  padding: 0.25rem 0.75rem;
  border-radius: 4px;
  font-weight: 500;
  text-transform: capitalize;
}

.date {
  color: #999;
}

.continue-button {
  width: 100%;
  padding: 0.75rem;
  background-color: #1976d2;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 1rem;
  transition: background-color 0.2s;
}

.continue-button:hover {
  background-color: #1565c0;
}

.modal-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background-color: rgba(0, 0, 0, 0.5);
  display: flex;
  justify-content: center;
  align-items: center;
  z-index: 1000;
}

.modal {
  background: white;
  border-radius: 8px;
  padding: 2rem;
  max-width: 400px;
  width: 90%;
  box-shadow: 0 4px 20px rgba(0, 0, 0, 0.2);
}

.modal-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 1.5rem;
}

.modal-header h2 {
  margin: 0;
  font-size: 1.5rem;
  color: #333;
}

.close-button {
  background: none;
  border: none;
  font-size: 1.5rem;
  cursor: pointer;
  color: #999;
  padding: 0;
  width: 2rem;
  height: 2rem;
  display: flex;
  align-items: center;
  justify-content: center;
}

.close-button:hover {
  color: #333;
}

.form-group {
  margin-bottom: 1.5rem;
}

.form-group label {
  display: block;
  margin-bottom: 0.5rem;
  font-weight: 500;
  color: #333;
}

.form-group input {
  width: 100%;
  padding: 0.75rem;
  border: 1px solid #ddd;
  border-radius: 4px;
  font-size: 1rem;
  box-sizing: border-box;
}

.form-group input:disabled {
  background-color: #f5f5f5;
  cursor: not-allowed;
  color: #999;
}

.modal-actions {
  display: flex;
  gap: 1rem;
  justify-content: flex-end;
}

.cancel-button {
  padding: 0.75rem 1.5rem;
  background-color: #f5f5f5;
  color: #333;
  border: 1px solid #ddd;
  border-radius: 4px;
  cursor: pointer;
  font-size: 1rem;
  transition: background-color 0.2s;
}

.cancel-button:hover {
  background-color: #eeeeee;
}

.submit-button {
  padding: 0.75rem 1.5rem;
  background-color: #1976d2;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 1rem;
  transition: background-color 0.2s;
}

.submit-button:hover:not(:disabled) {
  background-color: #1565c0;
}

.submit-button:disabled {
  background-color: #ccc;
  cursor: not-allowed;
}
</style>
