// @{"req": ["REQ-FEADMIN-702", "REQ-FEADMIN-703"]}

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { get, post, ApiError } from '@/api/client'

interface Assignment {
  id: string
  title: string
  topic: string
  level: string
  student_count: number
  created_at: string
}

interface AssignmentsResponse {
  assignments: Assignment[]
  next_cursor: string | null
}

interface CreateAssignmentRequest {
  title?: string
  topic: string
  level?: string
  parameters?: Record<string, any>
}

interface CreateAssignmentResponse {
  id: string
  admin_id: string
  title: string
  topic: string
  level: string
  parameters: Record<string, any>
  created_at: string
  updated_at: string
}

const router = useRouter()

const assignments = ref<Assignment[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const nextCursor = ref<string | null>(null)

// Create assignment modal state
const showCreateModal = ref(false)
const createForm = ref({
  title: '',
  topic: '',
  level: 'beginner',
  parameters: ''
})
const createError = ref<string | null>(null)
const createSubmitting = ref(false)

// @{"req": ["REQ-FEADMIN-702"]}
async function fetchAssignments(cursor: string = ''): Promise<void> {
  try {
    loading.value = true
    error.value = null

    let url = '/api/v1/admin/assignments?limit=20'
    if (cursor) {
      url += `&cursor=${cursor}`
    }

    const response = await get<AssignmentsResponse>(url)
    assignments.value.push(...(response.assignments ?? []))
    nextCursor.value = response.next_cursor ?? null
  } catch (err) {
    if (err instanceof ApiError) {
      error.value = `Failed to load assignments: ${err.message}`
    } else {
      error.value = 'Failed to load assignments'
    }
  } finally {
    loading.value = false
  }
}

function openCreateModal(): void {
  showCreateModal.value = true
  createError.value = null
  createForm.value = {
    title: '',
    topic: '',
    level: 'beginner',
    parameters: ''
  }
}

function closeCreateModal(): void {
  showCreateModal.value = false
  createError.value = null
  createForm.value = {
    title: '',
    topic: '',
    level: 'beginner',
    parameters: ''
  }
}

// @{"req": ["REQ-FEADMIN-703"]}
async function createAssignment(): Promise<void> {
  createError.value = null

  if (!createForm.value.topic.trim()) {
    createError.value = 'Topic is required'
    return
  }

  const body: CreateAssignmentRequest = {
    topic: createForm.value.topic.trim(),
    level: createForm.value.level,
    title: createForm.value.title.trim() || undefined
  }

  if (createForm.value.parameters.trim()) {
    try {
      body.parameters = JSON.parse(createForm.value.parameters)
    } catch {
      createError.value = 'Invalid JSON in parameters'
      return
    }
  }

  try {
    createSubmitting.value = true
    const newAssignment = await post<CreateAssignmentResponse>(
      '/api/v1/admin/assignments',
      body
    )
    assignments.value.unshift({
      id: newAssignment.id,
      title: newAssignment.title,
      topic: newAssignment.topic,
      level: newAssignment.level,
      student_count: 0,
      created_at: newAssignment.created_at
    })
    closeCreateModal()
  } catch (err) {
    if (err instanceof ApiError) {
      createError.value = `Failed to create assignment: ${err.message}`
    } else {
      createError.value = 'Failed to create assignment'
    }
  } finally {
    createSubmitting.value = false
  }
}

function navigateToDetail(assignmentId: string): void {
  router.push(`/admin/assignments/${assignmentId}`)
}

function loadMore(): void {
  if (nextCursor.value) {
    fetchAssignments(nextCursor.value)
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
  fetchAssignments()
})
</script>

<template>
  <div class="assignments-view">
    <div class="header">
      <h1>Assignments</h1>
      <button class="create-button" @click="openCreateModal">New Assignment</button>
    </div>

    <div v-if="error" class="error-banner" data-testid="error-banner">
      {{ error }}
      <button class="dismiss-btn" @click="error = null">Dismiss</button>
    </div>

    <div v-if="loading && assignments.length === 0" class="loading">
      Loading assignments...
    </div>

    <div v-else-if="assignments.length === 0" class="empty-state">
      No assignments found. Create one to get started.
    </div>

    <div v-else class="content">
      <table class="assignments-table">
        <thead>
          <tr>
            <th>Title</th>
            <th>Topic</th>
            <th>Level</th>
            <th>Student Count</th>
            <th>Created</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="assignment in assignments"
            :key="assignment.id"
            class="assignment-row"
            @click="navigateToDetail(assignment.id)"
          >
            <td class="col-title">{{ assignment.title || '(Untitled)' }}</td>
            <td class="col-topic">{{ assignment.topic }}</td>
            <td class="col-level">
              <span class="level-badge" :class="assignment.level">
                {{ assignment.level }}
              </span>
            </td>
            <td class="col-student-count">{{ assignment.student_count }}</td>
            <td class="col-created">{{ formatDate(assignment.created_at) }}</td>
          </tr>
        </tbody>
      </table>

      <div v-if="nextCursor && !loading" class="load-more-section">
        <button @click="loadMore" class="load-more-button">
          Load More
        </button>
      </div>
    </div>

    <!-- Create Assignment Modal -->
    <div v-if="showCreateModal" class="modal-overlay" @click.self="closeCreateModal">
      <div class="modal">
        <div class="modal-header">
          <h2>Create Assignment</h2>
          <button class="close-button" @click="closeCreateModal">×</button>
        </div>

        <div v-if="createError" class="modal-error" data-testid="create-error">
          {{ createError }}
        </div>

        <form class="modal-form" @submit.prevent="createAssignment">
          <div class="form-group">
            <label for="create-title">Title (optional)</label>
            <input
              id="create-title"
              v-model="createForm.title"
              type="text"
              placeholder="e.g. Week 3 — Linear Algebra"
            />
          </div>

          <div class="form-group">
            <label for="create-topic">Topic</label>
            <input
              id="create-topic"
              v-model="createForm.topic"
              type="text"
              placeholder="e.g. Linear Algebra"
              required
            />
          </div>

          <div class="form-group">
            <label for="create-level">Level</label>
            <select id="create-level" v-model="createForm.level">
              <option value="beginner">Beginner</option>
              <option value="intermediate">Intermediate</option>
              <option value="advanced">Advanced</option>
            </select>
          </div>

          <div class="form-group">
            <label for="create-parameters">Parameters (JSON, optional)</label>
            <textarea
              id="create-parameters"
              v-model="createForm.parameters"
              placeholder='e.g. {"required_topics": ["eigenvalues"]}'
              rows="3"
            ></textarea>
          </div>

          <div class="modal-actions">
            <button type="button" class="cancel-button" @click="closeCreateModal">
              Cancel
            </button>
            <button
              type="submit"
              class="submit-button"
              :disabled="!createForm.topic.trim() || createSubmitting"
            >
              {{ createSubmitting ? 'Creating...' : 'Create' }}
            </button>
          </div>
        </form>
      </div>
    </div>
  </div>
</template>

<style scoped>
.assignments-view {
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

.create-button {
  padding: 0.75rem 1.5rem;
  background-color: #1976d2;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 1rem;
  font-weight: 500;
  transition: background-color 0.2s;
}

.create-button:hover {
  background-color: #1565c0;
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

.loading {
  text-align: center;
  padding: 2rem;
  color: #666;
  font-size: 1.1rem;
}

.empty-state {
  text-align: center;
  padding: 2rem;
  color: #999;
  font-size: 1rem;
}

.content {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.assignments-table {
  width: 100%;
  border-collapse: collapse;
  background-color: white;
  border-radius: 4px;
  overflow: hidden;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
}

.assignments-table thead {
  background-color: #f5f5f5;
  font-weight: 600;
}

.assignments-table th {
  padding: 1rem;
  text-align: left;
  border-bottom: 2px solid #ddd;
  color: #333;
  font-size: 0.95rem;
}

.assignments-table td {
  padding: 1rem;
  border-bottom: 1px solid #eee;
}

.assignment-row {
  cursor: pointer;
  transition: background-color 0.2s;
}

.assignment-row:hover {
  background-color: #fafafa;
}

.assignment-row:last-child td {
  border-bottom: none;
}

.col-title {
  font-weight: 500;
  color: #1976d2;
}

.col-topic {
  color: #333;
}

.col-level {
  text-align: center;
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

.col-student-count {
  text-align: center;
  color: #666;
}

.col-created {
  font-size: 0.9rem;
  color: #666;
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
  max-width: 500px;
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

.modal-form {
  padding: 1.5rem;
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.form-group label {
  font-size: 0.9rem;
  font-weight: 600;
  color: #333;
}

.form-group input,
.form-group select,
.form-group textarea {
  padding: 0.6rem 0.8rem;
  border: 1px solid #ccc;
  border-radius: 4px;
  font-size: 0.95rem;
  font-family: inherit;
}

.form-group textarea {
  resize: vertical;
  font-family: monospace;
}

.form-group input:focus,
.form-group select:focus,
.form-group textarea:focus {
  outline: none;
  border-color: #1976d2;
  box-shadow: 0 0 0 2px rgba(25, 118, 210, 0.1);
}

.modal-actions {
  display: flex;
  gap: 1rem;
  justify-content: flex-end;
  margin-top: 1rem;
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
