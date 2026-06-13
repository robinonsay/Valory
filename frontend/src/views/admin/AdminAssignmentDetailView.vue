// @{"req": ["REQ-FEADMIN-704", "REQ-FEADMIN-705", "REQ-FEADMIN-706"]}

<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { get, post, del, ApiError } from '@/api/client'

interface StudentAssignment {
  student_id: string
  username: string
  course_id: string
  course_status: string
  assigned_at: string
}

interface AssignmentDetail {
  id: string
  title: string
  topic: string
  level: string
  parameters: Record<string, any>
  created_at: string
  students: StudentAssignment[]
}

interface ActiveStudent {
  id: string
  username: string
  is_active: boolean
}

interface AssignStudentsResponse {
  created: Array<{
    student_id: string
    course_id: string
    course_status: string
  }>
  errors: Array<{
    student_id: string
    error: string
  }>
}

const route = useRoute()
const router = useRouter()

const assignmentId = route.params.id as string

const assignment = ref<AssignmentDetail | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)

// Assign students modal state
const showAssignModal = ref(false)
const availableStudents = ref<ActiveStudent[]>([])
const selectedStudentIds = ref<Set<string>>(new Set())
const searchQuery = ref('')
const assignSubmitting = ref(false)
const assignError = ref<string | null>(null)
const assignSuccess = ref<AssignStudentsResponse | null>(null)

// Unassign state
const unassignSubmitting = ref<string | null>(null)
const unassignError = ref<Record<string, string>>({})
const unassignSuccess = ref<Record<string, boolean>>({})

const MAX_STUDENTS_PER_ASSIGN = 20

// @{"req": ["REQ-FEADMIN-704"]}
async function fetchAssignment(): Promise<void> {
  try {
    loading.value = true
    error.value = null
    const response = await get<AssignmentDetail>(
      `/api/v1/admin/assignments/${assignmentId}`
    )
    assignment.value = response
  } catch (err) {
    if (err instanceof ApiError) {
      error.value = `Failed to load assignment: ${err.message}`
    } else {
      error.value = 'Failed to load assignment'
    }
  } finally {
    loading.value = false
  }
}

// @{"req": ["REQ-FEADMIN-705"]}
async function openAssignModal(): Promise<void> {
  showAssignModal.value = true
  assignError.value = null
  assignSuccess.value = null
  selectedStudentIds.value.clear()
  searchQuery.value = ''

  try {
    const response = await get<{ users: ActiveStudent[] }>(
      '/api/v1/users?role=student'
    )
    availableStudents.value = response.users ?? []
  } catch (err) {
    assignError.value = 'Failed to load students'
  }
}

function closeAssignModal(): void {
  showAssignModal.value = false
  assignError.value = null
  assignSuccess.value = null
  selectedStudentIds.value.clear()
  searchQuery.value = ''
}

const filteredStudents = computed(() => {
  if (!searchQuery.value.trim()) {
    return availableStudents.value
  }
  const query = searchQuery.value.toLowerCase()
  return availableStudents.value.filter(s =>
    s.username.toLowerCase().includes(query)
  )
})

function toggleStudent(studentId: string): void {
  if (selectedStudentIds.value.has(studentId)) {
    selectedStudentIds.value.delete(studentId)
  } else {
    if (selectedStudentIds.value.size < MAX_STUDENTS_PER_ASSIGN) {
      selectedStudentIds.value.add(studentId)
    }
  }
}

function isStudentSelected(studentId: string): boolean {
  return selectedStudentIds.value.has(studentId)
}

function isStudentAlreadyAssigned(studentId: string): boolean {
  return assignment.value?.students.some(s => s.student_id === studentId) ?? false
}

// @{"req": ["REQ-FEADMIN-705"]}
async function assignStudents(): Promise<void> {
  if (selectedStudentIds.value.size === 0) {
    assignError.value = 'Select at least one student'
    return
  }

  assignError.value = null
  assignSubmitting.value = true

  try {
    const response = await post<AssignStudentsResponse>(
      `/api/v1/admin/assignments/${assignmentId}/students`,
      {
        student_ids: Array.from(selectedStudentIds.value)
      }
    )
    assignSuccess.value = response
  } catch (err) {
    if (err instanceof ApiError) {
      assignError.value = `Failed to assign students: ${err.message}`
    } else {
      assignError.value = 'Failed to assign students'
    }
  } finally {
    assignSubmitting.value = false
  }
}

async function refreshAfterAssign(): Promise<void> {
  closeAssignModal()
  await fetchAssignment()
}

// @{"req": ["REQ-FEADMIN-706"]}
async function unassignStudent(studentId: string): Promise<void> {
  if (!confirm(`Unassign this student?`)) {
    return
  }

  unassignSubmitting.value = studentId
  unassignError.value = { ...unassignError.value }
  unassignSuccess.value = { ...unassignSuccess.value }

  try {
    await del<void>(
      `/api/v1/admin/assignments/${assignmentId}/students/${studentId}`
    )
    unassignSuccess.value[studentId] = true
    if (assignment.value) {
      assignment.value.students = assignment.value.students.filter(
        s => s.student_id !== studentId
      )
    }
  } catch (err) {
    if (err instanceof ApiError && err.status === 409) {
      const body = err.body as { error?: string } | null
      if (body?.error === 'GENERATION_IN_PROGRESS') {
        unassignError.value[studentId] =
          'Cannot unassign: course generation has started'
      } else if (body?.error === 'COURSE_ALREADY_ACTIVE') {
        unassignError.value[studentId] =
          'Cannot unassign: course is active or beyond'
      } else {
        unassignError.value[studentId] = 'Cannot unassign this student'
      }
    } else if (err instanceof ApiError) {
      unassignError.value[studentId] = `Failed to unassign: ${err.message}`
    } else {
      unassignError.value[studentId] = 'Failed to unassign student'
    }
  } finally {
    unassignSubmitting.value = null
  }
}

function formatDate(dateString: string): string {
  const date = new Date(dateString)
  return date.toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric'
  })
}

onMounted(() => {
  fetchAssignment()
})
</script>

<template>
  <div class="assignment-detail">
    <div v-if="loading" class="loading">
      Loading assignment...
    </div>

    <div v-else-if="error" class="error-banner" data-testid="error-banner">
      {{ error }}
      <button class="dismiss-btn" @click="error = null">Dismiss</button>
    </div>

    <div v-else-if="assignment" class="content">
      <div class="header">
        <div class="title-section">
          <h1>{{ assignment.title || assignment.topic }}</h1>
          <p class="assignment-topic" v-if="assignment.title">
            {{ assignment.topic }}
          </p>
        </div>
        <button class="assign-button" @click="openAssignModal">
          Assign Students
        </button>
      </div>

      <div class="metadata">
        <div class="metadata-item">
          <span class="label">Level:</span>
          <span class="level-badge" :class="assignment.level">
            {{ assignment.level }}
          </span>
        </div>
        <div class="metadata-item">
          <span class="label">Created:</span>
          <span class="value">{{ formatDate(assignment.created_at) }}</span>
        </div>
      </div>

      <div v-if="Object.keys(assignment.parameters).length > 0" class="parameters-section">
        <h2>Parameters</h2>
        <pre class="parameters-json">{{ JSON.stringify(assignment.parameters, null, 2) }}</pre>
      </div>

      <div class="students-section">
        <h2>Assigned Students ({{ assignment.students.length }})</h2>

        <div v-if="assignment.students.length === 0" class="no-students">
          No students assigned yet.
        </div>

        <table v-else class="students-table">
          <thead>
            <tr>
              <th>Username</th>
              <th>Course Status</th>
              <th>Assigned Date</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="student in assignment.students"
              :key="student.student_id"
              class="student-row"
            >
              <td class="col-username">{{ student.username }}</td>
              <td class="col-status">
                <span class="status-badge" :class="student.course_status">
                  {{ student.course_status }}
                </span>
              </td>
              <td class="col-assigned-date">{{ formatDate(student.assigned_at) }}</td>
              <td class="col-actions">
                <div class="action-cell">
                  <button
                    class="unassign-btn"
                    :disabled="unassignSubmitting === student.student_id"
                    @click="unassignStudent(student.student_id)"
                  >
                    {{ unassignSubmitting === student.student_id ? 'Removing...' : 'Unassign' }}
                  </button>
                  <div
                    v-if="unassignError[student.student_id]"
                    class="unassign-error"
                  >
                    {{ unassignError[student.student_id] }}
                  </div>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- Assign Students Modal -->
    <div v-if="showAssignModal" class="modal-overlay" @click.self="closeAssignModal">
      <div class="modal">
        <div class="modal-header">
          <h2>Assign Students</h2>
          <button class="close-button" @click="closeAssignModal">×</button>
        </div>

        <div v-if="assignError" class="modal-error" data-testid="assign-error">
          {{ assignError }}
        </div>

        <div v-if="assignSuccess" class="assign-success">
          <div v-if="assignSuccess.created.length > 0" class="success-section">
            <h3>Successfully assigned ({{ assignSuccess.created.length }})</h3>
            <ul>
              <li v-for="item in assignSuccess.created" :key="item.student_id">
                Student assigned
              </li>
            </ul>
          </div>
          <div v-if="assignSuccess.errors.length > 0" class="error-section">
            <h3>Errors ({{ assignSuccess.errors.length }})</h3>
            <ul>
              <li v-for="item in assignSuccess.errors" :key="item.student_id">
                {{ item.error }}
              </li>
            </ul>
          </div>
          <button class="refresh-button" @click="refreshAfterAssign">
            Done
          </button>
        </div>

        <div v-else class="modal-content">
          <div class="search-box">
            <input
              v-model="searchQuery"
              type="text"
              placeholder="Search students..."
              class="search-input"
            />
          </div>

          <div class="student-count">
            Selected: {{ selectedStudentIds.size }}/{{ MAX_STUDENTS_PER_ASSIGN }}
          </div>

          <div class="students-list">
            <label
              v-for="student in filteredStudents"
              :key="student.id"
              class="student-checkbox"
              :class="{ disabled: isStudentAlreadyAssigned(student.id) }"
            >
              <input
                type="checkbox"
                :checked="isStudentSelected(student.id)"
                :disabled="
                  isStudentAlreadyAssigned(student.id) ||
                  (selectedStudentIds.size >= MAX_STUDENTS_PER_ASSIGN &&
                    !isStudentSelected(student.id))
                "
                @change="toggleStudent(student.id)"
              />
              <span class="username">{{ student.username }}</span>
              <span v-if="isStudentAlreadyAssigned(student.id)" class="already-assigned">
                (already assigned)
              </span>
            </label>
          </div>

          <div class="modal-actions">
            <button class="cancel-button" @click="closeAssignModal">
              Cancel
            </button>
            <button
              class="submit-button"
              :disabled="selectedStudentIds.size === 0 || assignSubmitting"
              @click="assignStudents"
            >
              {{ assignSubmitting ? 'Assigning...' : 'Assign' }}
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.assignment-detail {
  padding: 2rem;
  max-width: 1400px;
  margin: 0 auto;
}

.loading {
  text-align: center;
  padding: 2rem;
  color: #666;
  font-size: 1.1rem;
}

.error-banner {
  padding: 0.75rem 1rem;
  background-color: #ffebee;
  color: #d32f2f;
  border-left: 4px solid #d32f2f;
  border-radius: 4px;
  margin-bottom: 1.5rem;
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.dismiss-btn {
  background: none;
  border: none;
  color: #d32f2f;
  cursor: pointer;
  font-size: 0.85rem;
  text-decoration: underline;
}

.content {
  display: flex;
  flex-direction: column;
  gap: 2rem;
}

.header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  border-bottom: 1px solid #ddd;
  padding-bottom: 1rem;
  gap: 1rem;
}

.title-section {
  flex: 1;
}

.header h1 {
  margin: 0 0 0.5rem 0;
  font-size: 2rem;
  color: #333;
}

.assignment-topic {
  margin: 0;
  font-size: 0.95rem;
  color: #666;
}

.assign-button {
  padding: 0.75rem 1.5rem;
  background-color: #1976d2;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 0.95rem;
  font-weight: 500;
  white-space: nowrap;
  transition: background-color 0.2s;
}

.assign-button:hover {
  background-color: #1565c0;
}

.metadata {
  display: flex;
  gap: 2rem;
  flex-wrap: wrap;
}

.metadata-item {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.label {
  font-weight: 600;
  color: #666;
  font-size: 0.9rem;
}

.value {
  color: #333;
}

.level-badge {
  display: inline-block;
  padding: 0.25rem 0.75rem;
  border-radius: 12px;
  font-size: 0.8rem;
  font-weight: 600;
  text-transform: capitalize;
}

.level-badge.beginner {
  background-color: #e8f5e9;
  color: #2e7d32;
}

.level-badge.intermediate {
  background-color: #fff3e0;
  color: #e65100;
}

.level-badge.advanced {
  background-color: #f3e5f5;
  color: #6a1b9a;
}

.parameters-section {
  background-color: #f5f5f5;
  border: 1px solid #ddd;
  border-radius: 4px;
  padding: 1rem;
}

.parameters-section h2 {
  margin-top: 0;
  margin-bottom: 1rem;
  font-size: 1.1rem;
  color: #333;
}

.parameters-json {
  margin: 0;
  padding: 0.75rem;
  background-color: white;
  border: 1px solid #ddd;
  border-radius: 4px;
  overflow-x: auto;
  font-size: 0.85rem;
  color: #333;
}

.students-section {
  background-color: white;
  border: 1px solid #ddd;
  border-radius: 4px;
  padding: 1.5rem;
}

.students-section h2 {
  margin-top: 0;
  margin-bottom: 1rem;
  font-size: 1.25rem;
  color: #333;
}

.no-students {
  text-align: center;
  padding: 2rem;
  color: #999;
}

.students-table {
  width: 100%;
  border-collapse: collapse;
}

.students-table thead {
  background-color: #f5f5f5;
}

.students-table th {
  padding: 0.75rem 1rem;
  text-align: left;
  border-bottom: 2px solid #ddd;
  font-weight: 600;
  color: #333;
  font-size: 0.9rem;
}

.students-table td {
  padding: 0.75rem 1rem;
  border-bottom: 1px solid #eee;
}

.student-row:hover {
  background-color: #fafafa;
}

.student-row:last-child td {
  border-bottom: none;
}

.col-username {
  font-weight: 500;
  color: #333;
}

.col-status {
  text-align: center;
}

.col-assigned-date {
  font-size: 0.9rem;
  color: #666;
}

.col-actions {
  text-align: right;
}

.action-cell {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  align-items: flex-end;
}

.status-badge {
  display: inline-block;
  padding: 0.25rem 0.6rem;
  border-radius: 4px;
  font-size: 0.8rem;
  font-weight: 600;
  text-transform: capitalize;
}

.status-badge.syllabus_approved {
  background-color: #e3f2fd;
  color: #1565c0;
}

.status-badge.generating {
  background-color: #fff3e0;
  color: #e65100;
}

.status-badge.active {
  background-color: #e8f5e9;
  color: #2e7d32;
}

.status-badge.archived {
  background-color: #f3e5f5;
  color: #6a1b9a;
}

.status-badge.completed {
  background-color: #e8f5e9;
  color: #2e7d32;
}

.status-badge.failed {
  background-color: #ffebee;
  color: #c62828;
}

.unassign-btn {
  padding: 0.35rem 0.75rem;
  background-color: #ffebee;
  color: #c62828;
  border: none;
  border-radius: 4px;
  font-size: 0.85rem;
  cursor: pointer;
  font-weight: 500;
  transition: background-color 0.2s;
}

.unassign-btn:hover:not(:disabled) {
  background-color: #ffcdd2;
}

.unassign-btn:disabled {
  background-color: #ccc;
  color: #666;
  cursor: not-allowed;
}

.unassign-error {
  font-size: 0.75rem;
  color: #c62828;
  margin-top: 0.25rem;
  max-width: 200px;
  text-align: right;
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
  background-color: white;
  border-radius: 6px;
  max-width: 600px;
  width: 90%;
  max-height: 90vh;
  overflow-y: auto;
  box-shadow: 0 4px 6px rgba(0, 0, 0, 0.1);
}

.modal-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 1.5rem;
  border-bottom: 1px solid #ddd;
}

.modal-header h2 {
  margin: 0;
  font-size: 1.5rem;
  color: #333;
}

.close-button {
  background: none;
  border: none;
  font-size: 2rem;
  cursor: pointer;
  color: #999;
  padding: 0;
  width: 40px;
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: center;
}

.close-button:hover {
  color: #333;
}

.modal-error {
  margin: 1rem;
  padding: 0.75rem;
  background-color: #ffebee;
  color: #d32f2f;
  border-radius: 4px;
  font-size: 0.9rem;
}

.assign-success {
  padding: 1.5rem;
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.success-section,
.error-section {
  border-radius: 4px;
  padding: 1rem;
}

.success-section {
  background-color: #e8f5e9;
  color: #2e7d32;
}

.success-section h3 {
  margin-top: 0;
  margin-bottom: 0.5rem;
  font-size: 1rem;
}

.success-section ul {
  margin: 0;
  padding-left: 1.5rem;
}

.success-section li {
  margin: 0.25rem 0;
}

.error-section {
  background-color: #ffebee;
  color: #c62828;
}

.error-section h3 {
  margin-top: 0;
  margin-bottom: 0.5rem;
  font-size: 1rem;
}

.error-section ul {
  margin: 0;
  padding-left: 1.5rem;
}

.error-section li {
  margin: 0.25rem 0;
  font-size: 0.9rem;
}

.refresh-button {
  align-self: flex-end;
  padding: 0.6rem 1.2rem;
  background-color: #1976d2;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-weight: 500;
  transition: background-color 0.2s;
}

.refresh-button:hover {
  background-color: #1565c0;
}

.modal-content {
  padding: 1.5rem;
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.search-box {
  margin-bottom: 0.5rem;
}

.search-input {
  width: 100%;
  padding: 0.6rem 0.8rem;
  border: 1px solid #ccc;
  border-radius: 4px;
  font-size: 0.95rem;
}

.search-input:focus {
  outline: none;
  border-color: #1976d2;
  box-shadow: 0 0 0 2px rgba(25, 118, 210, 0.1);
}

.student-count {
  font-size: 0.9rem;
  color: #666;
  padding: 0.5rem 0;
}

.students-list {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  max-height: 300px;
  overflow-y: auto;
  border: 1px solid #ddd;
  border-radius: 4px;
  padding: 0.5rem;
  background-color: #fafafa;
}

.student-checkbox {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.5rem 0.75rem;
  cursor: pointer;
  border-radius: 4px;
  transition: background-color 0.2s;
}

.student-checkbox:hover:not(.disabled) {
  background-color: #f0f0f0;
}

.student-checkbox.disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.student-checkbox input[type='checkbox'] {
  margin: 0;
  cursor: pointer;
}

.student-checkbox.disabled input[type='checkbox'] {
  cursor: not-allowed;
}

.username {
  flex: 1;
  font-weight: 500;
  color: #333;
}

.already-assigned {
  font-size: 0.8rem;
  color: #999;
  font-style: italic;
}

.modal-actions {
  display: flex;
  gap: 1rem;
  justify-content: flex-end;
  margin-top: 1rem;
  padding-top: 1rem;
  border-top: 1px solid #ddd;
}

.cancel-button {
  padding: 0.6rem 1.2rem;
  background-color: #f5f5f5;
  color: #333;
  border: 1px solid #ddd;
  border-radius: 4px;
  cursor: pointer;
  font-size: 0.95rem;
  font-weight: 500;
  transition: background-color 0.2s;
}

.cancel-button:hover {
  background-color: #eee;
}

.submit-button {
  padding: 0.6rem 1.2rem;
  background-color: #1976d2;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 0.95rem;
  font-weight: 500;
  transition: background-color 0.2s;
}

.submit-button:hover:not(:disabled) {
  background-color: #1565c0;
}

.submit-button:disabled {
  background-color: #bbb;
  cursor: not-allowed;
}
</style>
