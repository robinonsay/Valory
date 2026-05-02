// @{"req": ["REQ-FECOURSE-075", "REQ-FECOURSE-076", "REQ-FECOURSE-077", "REQ-FECOURSE-078", "REQ-FECOURSE-570", "REQ-FECOURSE-580", "REQ-FECOURSE-590", "REQ-FECOURSE-600"]}

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { get, ApiError } from '@/api/client'

interface Section {
  section_index: number
  title: string
}

interface HomeworkItem {
  id: string
  section_index: number
  title: string
  grade_weight: number
  due_date: string | null
}

interface CourseDetail {
  id: string
  topic: string
  status: string
  created_at: string
  updated_at: string
}

const route = useRoute()
const auth = useAuthStore()

const courseId = route.params.id as string
const course = ref<CourseDetail | null>(null)
const sections = ref<Section[]>([])
const homework = ref<HomeworkItem[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
const hasHomework = ref(false)

onMounted(async () => {
  try {
    loading.value = true
    error.value = null

    const courseResponse = await get<CourseDetail>(`/api/v1/courses/${courseId}`, auth.token)
    course.value = courseResponse

    const sectionsResponse = await get<Section[]>(`/api/v1/courses/${courseId}/sections`, auth.token)
    sections.value = sectionsResponse

    try {
      const homeworkResponse = await get<HomeworkItem[]>(`/api/v1/courses/${courseId}/homework`, auth.token)
      homework.value = homeworkResponse
      hasHomework.value = homework.value.length > 0
    } catch {
      hasHomework.value = false
    }
  } catch (err) {
    if (err instanceof ApiError) {
      error.value = `Failed to load course: ${err.message}`
    } else {
      error.value = 'Failed to load course'
    }
  } finally {
    loading.value = false
  }
})

function formatDate(dateString: string): string {
  const date = new Date(dateString)
  return date.toLocaleDateString('en-US', { year: 'numeric', month: 'long', day: 'numeric' })
}
</script>

<template>
  <div class="course-hub">
    <div v-if="loading" class="loading">
      Loading course...
    </div>

    <div v-else-if="error" class="error">
      {{ error }}
    </div>

    <div v-else-if="course" class="content">
      <div class="header">
        <h1>{{ course.topic }}</h1>
        <span class="status-badge" :class="course.status">
          {{ course.status }}
        </span>
      </div>

      <section class="sections-section">
        <h2>Sections</h2>
        <ul class="sections-list">
          <li v-for="section in sections" :key="section.section_index" class="section-item">
            <router-link :to="`/courses/${courseId}/sections/${section.section_index}`">
              {{ section.title }}
            </router-link>
          </li>
        </ul>
      </section>

      <section v-if="hasHomework" class="homework-section">
        <h2>Homework</h2>
        <ul class="homework-list">
          <li v-for="hw in homework" :key="hw.id" class="homework-item">
            <router-link :to="`/courses/${courseId}/homework/${hw.id}`">
              <div class="homework-title">{{ hw.title }}</div>
              <div class="homework-due">Due: {{ hw.due_date ? formatDate(hw.due_date) : 'Not scheduled' }}</div>
            </router-link>
          </li>
        </ul>
      </section>

      <section class="links-section">
        <router-link :to="`/courses/${courseId}/grades`" class="link-button">
          Grades
        </router-link>
        <router-link :to="`/courses/${courseId}/badges`" class="link-button">
          Badges
        </router-link>
      </section>
    </div>
  </div>
</template>

<style scoped>
.course-hub {
  padding: 2rem;
  max-width: 1200px;
  margin: 0 auto;
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
}

.content {
  display: flex;
  flex-direction: column;
  gap: 2rem;
}

.header {
  display: flex;
  align-items: center;
  gap: 1rem;
  border-bottom: 1px solid #ddd;
  padding-bottom: 1rem;
}

.header h1 {
  margin: 0;
  font-size: 2rem;
  color: #333;
}

.status-badge {
  padding: 0.5rem 1rem;
  border-radius: 4px;
  font-size: 0.85rem;
  font-weight: 600;
  text-transform: capitalize;
  color: white;
}

.status-badge.active {
  background-color: #4caf50;
}

.status-badge.archived {
  background-color: #ff9800;
}

.status-badge.completed {
  background-color: #2196f3;
}

.status-badge.intake {
  background-color: #9c27b0;
}

.status-badge.syllabus_draft {
  background-color: #2196f3;
}

.status-badge.generating {
  background-color: #ff9800;
}

.sections-section,
.homework-section,
.links-section {
  padding: 1.5rem;
  background-color: #f9f9f9;
  border-radius: 4px;
}

.sections-section h2,
.homework-section h2 {
  margin-top: 0;
  margin-bottom: 1rem;
  font-size: 1.3rem;
  color: #333;
}

.sections-list,
.homework-list {
  list-style: none;
  padding: 0;
  margin: 0;
}

.section-item {
  margin-bottom: 0.75rem;
}

.section-item a {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.75rem;
  color: #1976d2;
  text-decoration: none;
  border-radius: 4px;
  transition: background-color 0.2s;
}

.section-item a:hover {
  background-color: #e3f2fd;
}

.checkmark {
  color: #4caf50;
  font-weight: bold;
  margin-left: 0.5rem;
}

.homework-item {
  margin-bottom: 1rem;
}

.homework-item a {
  display: block;
  padding: 1rem;
  color: #1976d2;
  text-decoration: none;
  background-color: white;
  border-radius: 4px;
  border: 1px solid #ddd;
  transition: border-color 0.2s;
}

.homework-item a:hover {
  border-color: #1976d2;
}

.homework-title {
  font-weight: 600;
  margin-bottom: 0.5rem;
}

.homework-due {
  font-size: 0.9rem;
  color: #666;
}

.links-section {
  display: flex;
  gap: 1rem;
  justify-content: center;
}

.link-button {
  padding: 0.75rem 1.5rem;
  background-color: #1976d2;
  color: white;
  text-decoration: none;
  border-radius: 4px;
  font-weight: 500;
  transition: background-color 0.2s;
}

.link-button:hover {
  background-color: #1565c0;
}
</style>
