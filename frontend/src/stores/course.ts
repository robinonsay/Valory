// @{"req": ["REQ-FECOURSE-001", "REQ-FECOURSE-002", "REQ-FECOURSE-003", "REQ-FECOURSE-004", "REQ-FECOURSE-005"]}

import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { get, ApiError } from '@/api/client'
import type { Course, CourseListResponse, CourseResponse } from '@/types/course'

// @{"req": ["REQ-FECOURSE-001", "REQ-FECOURSE-002", "REQ-FECOURSE-003", "REQ-FECOURSE-004", "REQ-FECOURSE-005"]}
export const useCourseStore = defineStore('course', () => {
  const courses = ref<Course[]>([])
  const currentCourse = ref<Course | null>(null)
  const loading = ref(false)
  const error = ref<string | null>(null)

  const sortedCourses = computed(() => {
    return [...courses.value].sort((a, b) => {
      const dateA = new Date(a.updated_at).getTime()
      const dateB = new Date(b.updated_at).getTime()
      return dateB - dateA
    })
  })

  const activeCourse = computed(() => {
    if (currentCourse.value && currentCourse.value.status === 'active') {
      return currentCourse.value
    }
    return null
  })

  // @{"req": ["REQ-FECOURSE-001", "REQ-FECOURSE-002", "REQ-FECOURSE-003", "REQ-FECOURSE-004", "REQ-FECOURSE-005"]}
  async function fetchCourses(token: string): Promise<void> {
    loading.value = true
    error.value = null
    try {
      const response = await get<CourseListResponse>('/api/v1/courses', token)
      courses.value = response.courses
    } catch (err) {
      if (err instanceof ApiError) {
        error.value = err.message
      } else {
        error.value = 'An unexpected error occurred'
      }
    } finally {
      loading.value = false
    }
  }

  // @{"req": ["REQ-FECOURSE-001", "REQ-FECOURSE-002", "REQ-FECOURSE-003", "REQ-FECOURSE-004", "REQ-FECOURSE-005"]}
  async function fetchCourse(id: string, token: string): Promise<void> {
    loading.value = true
    error.value = null
    try {
      const response = await get<CourseResponse>(`/api/v1/courses/${id}`, token)
      currentCourse.value = response.course
    } catch (err) {
      if (err instanceof ApiError) {
        error.value = err.message
      } else {
        error.value = 'An unexpected error occurred'
      }
    } finally {
      loading.value = false
    }
  }

  // @{"req": ["REQ-FECOURSE-001", "REQ-FECOURSE-002", "REQ-FECOURSE-003", "REQ-FECOURSE-004", "REQ-FECOURSE-005"]}
  function setCurrent(course: Course): void {
    currentCourse.value = course
  }

  // @{"req": ["REQ-FECOURSE-001", "REQ-FECOURSE-002", "REQ-FECOURSE-003", "REQ-FECOURSE-004", "REQ-FECOURSE-005"]}
  function reset(): void {
    courses.value = []
    currentCourse.value = null
    loading.value = false
    error.value = null
  }

  return {
    courses,
    currentCourse,
    loading,
    error,
    sortedCourses,
    activeCourse,
    fetchCourses,
    fetchCourse,
    setCurrent,
    reset
  }
})
