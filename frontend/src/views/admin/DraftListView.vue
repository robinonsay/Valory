// @{"req": ["REQ-FEADMIN-710", "REQ-SYS-077"]}

<script setup lang="ts">
// DraftListView — admin draft list + create + delete.
//
// Lists all course drafts owned by the admin (listDrafts), supports status
// filter and cursor pagination. A create-draft modal collects title, topic,
// and level then calls createDraft (which seeds the draft + root node).
// Delete calls deleteDraft; surfaces 409 DRAFT_ALREADY_PUBLISHED gracefully.

import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { ApiError } from '@/api/client'
import { listDrafts, createDraft, deleteDraft } from '@/api/tree'
import type { CourseDraft } from '@/types/tree'

const router = useRouter()
const auth = useAuthStore()

// Draft list state — summary items from ListDraftsResponse
type DraftSummary = Pick<CourseDraft, 'id' | 'title' | 'topic' | 'level' | 'status' | 'created_at'>

const drafts = ref<DraftSummary[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const nextCursor = ref<string | null>(null)
const statusFilter = ref<string>('')

// Create-draft modal state
const showCreateModal = ref(false)
const createForm = ref({
  title: '',
  topic: '',
  level: 'beginner',
})
const createError = ref<string | null>(null)
const createSubmitting = ref(false)

// Delete confirmation state
const deletingId = ref<string | null>(null)
const deleteError = ref<string | null>(null)

// @{"req": ["REQ-FEADMIN-710"]}
async function fetchDrafts(cursor = ''): Promise<void> {
  loading.value = true
  error.value = null
  try {
    const opts: Parameters<typeof listDrafts>[0] = { limit: 20 }
    if (statusFilter.value) opts.status = statusFilter.value
    if (cursor) opts.cursor = cursor
    const resp = await listDrafts(opts, auth.token ?? null)
    if (cursor) {
      drafts.value.push(...(resp.drafts ?? []))
    } else {
      drafts.value = resp.drafts ?? []
    }
    nextCursor.value = resp.next_cursor ?? null
  } catch (err) {
    if (err instanceof ApiError) {
      error.value = `Failed to load drafts: ${err.message}`
    } else {
      error.value = 'Failed to load drafts. Please try again.'
    }
  } finally {
    loading.value = false
  }
}

// @{"req": ["REQ-FEADMIN-710"]}
function applyFilter(): void {
  nextCursor.value = null
  fetchDrafts()
}

function loadMore(): void {
  if (nextCursor.value) {
    fetchDrafts(nextCursor.value)
  }
}

// @{"req": ["REQ-FEADMIN-710"]}
function openCreateModal(): void {
  createForm.value = { title: '', topic: '', level: 'beginner' }
  createError.value = null
  showCreateModal.value = true
}

function closeCreateModal(): void {
  showCreateModal.value = false
  createError.value = null
}

// @{"req": ["REQ-FEADMIN-710"]}
async function submitCreateDraft(): Promise<void> {
  createError.value = null
  const { title, topic, level } = createForm.value
  if (!topic.trim()) {
    createError.value = 'Topic is required.'
    return
  }
  createSubmitting.value = true
  try {
    const resp = await createDraft(
      { title: title.trim(), topic: topic.trim(), level },
      auth.token ?? null,
    )
    closeCreateModal()
    // Navigate directly to the editor for the new draft.
    await router.push(`/admin/drafts/${resp.draft.id}`)
  } catch (err) {
    if (err instanceof ApiError) {
      createError.value = `Failed to create draft: ${err.message}`
    } else {
      createError.value = 'Failed to create draft. Please try again.'
    }
  } finally {
    createSubmitting.value = false
  }
}

// @{"req": ["REQ-FEADMIN-710"]}
async function handleDelete(draftId: string): Promise<void> {
  if (!window.confirm('Delete this draft? This action cannot be undone.')) return
  deletingId.value = draftId
  deleteError.value = null
  try {
    await deleteDraft(draftId, auth.token ?? null)
    drafts.value = drafts.value.filter(d => d.id !== draftId)
  } catch (err) {
    if (err instanceof ApiError && err.status === 409) {
      const body = err.body as { error?: string }
      if (body?.error === 'DRAFT_ALREADY_PUBLISHED') {
        deleteError.value = 'This draft has already been published and cannot be deleted.'
      } else {
        deleteError.value = `Cannot delete: ${body?.error ?? err.message}`
      }
    } else if (err instanceof ApiError) {
      deleteError.value = `Failed to delete draft: ${err.message}`
    } else {
      deleteError.value = 'Failed to delete draft. Please try again.'
    }
  } finally {
    deletingId.value = null
  }
}

function openEditor(draftId: string): void {
  router.push(`/admin/drafts/${draftId}`)
}

function formatDate(dateString: string): string {
  return new Date(dateString).toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  })
}

onMounted(() => {
  fetchDrafts()
})
</script>

<template>
  <div class="drafts-view">
    <div class="header">
      <h1>Course Drafts</h1>
      <button class="create-button" @click="openCreateModal">New Draft</button>
    </div>

    <!-- Filter bar -->
    <div class="filter-bar">
      <label for="status-filter" class="filter-label">Status:</label>
      <select id="status-filter" v-model="statusFilter" class="filter-select" @change="applyFilter">
        <option value="">All</option>
        <option value="draft">Draft</option>
        <option value="published">Published</option>
      </select>
    </div>

    <!-- Error banners -->
    <div v-if="error" class="error-banner" data-testid="error-banner">
      {{ error }}
      <button class="dismiss-btn" @click="error = null">Dismiss</button>
    </div>
    <div v-if="deleteError" class="error-banner" data-testid="delete-error-banner">
      {{ deleteError }}
      <button class="dismiss-btn" @click="deleteError = null">Dismiss</button>
    </div>

    <!-- Loading / empty states -->
    <div v-if="loading && drafts.length === 0" class="loading">
      Loading drafts...
    </div>

    <div v-else-if="!loading && drafts.length === 0" class="empty-state">
      No drafts found. Create one to start building a course tree.
    </div>

    <!-- Draft table -->
    <div v-else class="content">
      <table class="drafts-table">
        <thead>
          <tr>
            <th>Title</th>
            <th>Topic</th>
            <th>Level</th>
            <th>Status</th>
            <th>Created</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="draft in drafts"
            :key="draft.id"
            class="draft-row"
          >
            <td class="col-title">
              <button class="link-button" @click="openEditor(draft.id)">
                {{ draft.title || '(Untitled)' }}
              </button>
            </td>
            <td class="col-topic">{{ draft.topic }}</td>
            <td class="col-level">
              <span class="level-badge" :class="draft.level">{{ draft.level }}</span>
            </td>
            <td class="col-status">
              <span class="status-badge" :class="draft.status">{{ draft.status }}</span>
            </td>
            <td class="col-created">{{ formatDate(draft.created_at) }}</td>
            <td class="col-actions">
              <button class="edit-button" @click="openEditor(draft.id)">
                Edit
              </button>
              <button
                class="delete-button"
                :disabled="deletingId === draft.id"
                @click="handleDelete(draft.id)"
              >
                {{ deletingId === draft.id ? 'Deleting...' : 'Delete' }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>

      <div v-if="nextCursor && !loading" class="load-more-section">
        <button class="load-more-button" @click="loadMore">Load More</button>
      </div>
      <div v-if="loading && drafts.length > 0" class="loading-more">
        Loading more...
      </div>
    </div>

    <!-- Create Draft Modal -->
    <div v-if="showCreateModal" class="modal-overlay" @click.self="closeCreateModal">
      <div class="modal">
        <div class="modal-header">
          <h2>New Course Draft</h2>
          <button class="close-button" @click="closeCreateModal">&times;</button>
        </div>

        <div v-if="createError" class="modal-error" data-testid="create-error">
          {{ createError }}
        </div>

        <form class="modal-form" @submit.prevent="submitCreateDraft">
          <div class="form-group">
            <label for="draft-title">Title <span class="required">*</span></label>
            <input
              id="draft-title"
              v-model="createForm.title"
              type="text"
              placeholder="e.g. Introduction to Linear Algebra"
              required
            />
          </div>

          <div class="form-group">
            <label for="draft-topic">Topic <span class="required">*</span></label>
            <input
              id="draft-topic"
              v-model="createForm.topic"
              type="text"
              placeholder="e.g. Linear Algebra"
              required
            />
          </div>

          <div class="form-group">
            <label for="draft-level">Level</label>
            <select id="draft-level" v-model="createForm.level">
              <option value="beginner">Beginner</option>
              <option value="intermediate">Intermediate</option>
              <option value="advanced">Advanced</option>
            </select>
          </div>

          <div class="modal-actions">
            <button type="button" class="cancel-button" @click="closeCreateModal">
              Cancel
            </button>
            <button
              type="submit"
              class="submit-button"
              :disabled="!createForm.title.trim() || !createForm.topic.trim() || createSubmitting"
            >
              {{ createSubmitting ? 'Creating...' : 'Create Draft' }}
            </button>
          </div>
        </form>
      </div>
    </div>
  </div>
</template>

<style scoped>
.drafts-view {
  padding: 2rem;
  max-width: 1400px;
  margin: 0 auto;
}

.header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 1.5rem;
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

.filter-bar {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  margin-bottom: 1.25rem;
}

.filter-label {
  font-size: 0.9rem;
  font-weight: 600;
  color: #555;
}

.filter-select {
  padding: 0.4rem 0.75rem;
  border: 1px solid #ccc;
  border-radius: 4px;
  font-size: 0.9rem;
  font-family: inherit;
}

.error-banner {
  padding: 0.75rem 1rem;
  background-color: #ffebee;
  color: #d32f2f;
  border-left: 4px solid #d32f2f;
  border-radius: 4px;
  margin-bottom: 1.25rem;
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
  white-space: nowrap;
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

.drafts-table {
  width: 100%;
  border-collapse: collapse;
  background-color: white;
  border-radius: 4px;
  overflow: hidden;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
}

.drafts-table thead {
  background-color: #f5f5f5;
}

.drafts-table th {
  padding: 1rem;
  text-align: left;
  border-bottom: 2px solid #ddd;
  color: #333;
  font-size: 0.95rem;
  font-weight: 600;
}

.drafts-table td {
  padding: 0.9rem 1rem;
  border-bottom: 1px solid #eee;
  vertical-align: middle;
}

.draft-row:last-child td {
  border-bottom: none;
}

.col-title {
  font-weight: 500;
}

.link-button {
  background: none;
  border: none;
  padding: 0;
  color: #1976d2;
  cursor: pointer;
  font-size: inherit;
  font-weight: 500;
  text-decoration: underline;
  text-underline-offset: 2px;
}

.link-button:hover {
  color: #1565c0;
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

.col-status {
  text-align: center;
}

.status-badge {
  display: inline-block;
  padding: 0.25rem 0.75rem;
  border-radius: 12px;
  font-size: 0.8rem;
  font-weight: 600;
  text-transform: capitalize;
}

.status-badge.draft {
  background-color: #e3f2fd;
  color: #1565c0;
}

.status-badge.published {
  background-color: #e8f5e9;
  color: #2e7d32;
}

.col-created {
  font-size: 0.9rem;
  color: #666;
}

.col-actions {
  display: flex;
  gap: 0.5rem;
  align-items: center;
}

.edit-button {
  padding: 0.4rem 0.9rem;
  background-color: #1976d2;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 0.85rem;
  font-weight: 500;
  transition: background-color 0.2s;
}

.edit-button:hover {
  background-color: #1565c0;
}

.delete-button {
  padding: 0.4rem 0.9rem;
  background-color: white;
  color: #d32f2f;
  border: 1px solid #d32f2f;
  border-radius: 4px;
  cursor: pointer;
  font-size: 0.85rem;
  font-weight: 500;
  transition: background-color 0.2s, color 0.2s;
}

.delete-button:hover:not(:disabled) {
  background-color: #d32f2f;
  color: white;
}

.delete-button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
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
  cursor: pointer;
  font-size: 1rem;
  font-weight: 500;
  transition: background-color 0.2s;
}

.load-more-button:hover {
  background-color: #1565c0;
}

.loading-more {
  text-align: center;
  color: #666;
  font-size: 0.9rem;
  padding: 0.5rem;
}

/* Modal */
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
  max-width: 480px;
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
  font-size: 1.4rem;
  color: #333;
}

.close-button {
  background: none;
  border: none;
  font-size: 1.8rem;
  cursor: pointer;
  color: #999;
  padding: 0;
  width: 36px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  line-height: 1;
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
  gap: 0.4rem;
}

.form-group label {
  font-size: 0.9rem;
  font-weight: 600;
  color: #333;
}

.required {
  color: #d32f2f;
}

.form-group input,
.form-group select {
  padding: 0.6rem 0.8rem;
  border: 1px solid #ccc;
  border-radius: 4px;
  font-size: 0.95rem;
  font-family: inherit;
}

.form-group input:focus,
.form-group select:focus {
  outline: none;
  border-color: #1976d2;
  box-shadow: 0 0 0 2px rgba(25, 118, 210, 0.1);
}

.modal-actions {
  display: flex;
  gap: 1rem;
  justify-content: flex-end;
  margin-top: 0.5rem;
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
