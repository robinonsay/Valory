// @{"req": ["REQ-FEADMIN-020", "REQ-FEADMIN-021", "REQ-FEADMIN-022", "REQ-FEADMIN-025", "REQ-FEADMIN-026", "REQ-FEADMIN-027", "REQ-FEADMIN-113", "REQ-FEADMIN-120", "REQ-FEADMIN-130", "REQ-FEADMIN-140", "REQ-FEADMIN-150", "REQ-FEADMIN-160"]}

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { get, post, del, ApiError } from '@/api/client'

interface UserItem {
  id: string
  username: string
  role: string
  is_active: boolean
  email?: string
  created_at: string
}

interface UsersResponse {
  users: UserItem[]
}

const auth = useAuthStore()

const users = ref<UserItem[]>([])
const loading = ref(false)
const error = ref<string | null>(null)

// Create user form state
const createForm = ref({
  username: '',
  email: '',
  password: '',
  role: ''
})
const createError = ref<string | null>(null)
const createSubmitting = ref(false)

// @{"req": ["REQ-FEADMIN-020", "REQ-FEADMIN-100"]}
async function fetchUsers(): Promise<void> {
  try {
    loading.value = true
    error.value = null
    const response = await get<UsersResponse>('/api/v1/users', auth.token)
    users.value = response.users
  } catch (err) {
    if (err instanceof ApiError) {
      error.value = `Failed to load users: ${err.message}`
    } else {
      error.value = 'Failed to load users'
    }
  } finally {
    loading.value = false
  }
}

// @{"req": ["REQ-FEADMIN-023", "REQ-FEADMIN-130", "REQ-FEADMIN-131", "REQ-FEADMIN-132", "REQ-FEADMIN-133"]}
async function deactivateUser(user: UserItem): Promise<void> {
  if (!confirm(`Deactivate ${user.username}? They will not be able to log in.`)) {
    return
  }
  try {
    error.value = null
    await post<void>(`/api/v1/users/${user.id}/deactivate`, {}, auth.token)
    const target = users.value.find(u => u.id === user.id)
    if (target) {
      target.is_active = false
    }
  } catch (err) {
    if (err instanceof ApiError) {
      error.value = `Failed to deactivate user: ${err.message}`
    } else {
      error.value = 'Failed to deactivate user'
    }
  }
}

// @{"req": ["REQ-FEADMIN-024", "REQ-FEADMIN-134", "REQ-FEADMIN-135"]}
async function activateUser(user: UserItem): Promise<void> {
  try {
    error.value = null
    await post<void>(`/api/v1/users/${user.id}/activate`, {}, auth.token)
    const target = users.value.find(u => u.id === user.id)
    if (target) {
      target.is_active = true
    }
  } catch (err) {
    if (err instanceof ApiError) {
      error.value = `Failed to activate user: ${err.message}`
    } else {
      error.value = 'Failed to activate user'
    }
  }
}

// @{"req": ["REQ-FEADMIN-025", "REQ-FEADMIN-140", "REQ-FEADMIN-141", "REQ-FEADMIN-142", "REQ-FEADMIN-143", "REQ-FEADMIN-144"]}
async function deleteUser(user: UserItem): Promise<void> {
  if (!confirm(`Permanently delete ${user.username} and all their data? This cannot be undone.`)) {
    return
  }
  try {
    error.value = null
    await del<void>(`/api/v1/users/${user.id}`, auth.token)
    users.value = users.value.filter(u => u.id !== user.id)
  } catch (err) {
    if (err instanceof ApiError && err.status === 409) {
      error.value = 'Cannot delete: agent operations could not be terminated'
    } else if (err instanceof ApiError) {
      error.value = `Failed to delete user: ${err.message}`
    } else {
      error.value = 'Failed to delete user'
    }
  }
}

// @{"req": ["REQ-FEADMIN-021", "REQ-FEADMIN-113", "REQ-FEADMIN-114", "REQ-FEADMIN-115", "REQ-FEADMIN-116"]}
async function createUser(): Promise<void> {
  createError.value = null

  if (!createForm.value.username) {
    createError.value = 'Username is required.'
    return
  }
  if (!createForm.value.role) {
    createError.value = 'Role is required.'
    return
  }
  if (!createForm.value.password) {
    createError.value = 'Password is required.'
    return
  }

  const body: Record<string, string> = {
    username: createForm.value.username,
    password: createForm.value.password,
    role: createForm.value.role
  }
  if (createForm.value.email) {
    body.email = createForm.value.email
  }

  try {
    createSubmitting.value = true
    const newUser = await post<UserItem>('/api/v1/users', body, auth.token)
    users.value.unshift(newUser)
    createForm.value = { username: '', email: '', password: '', role: '' }
  } catch (err) {
    if (err instanceof ApiError && err.status === 409) {
      createError.value = 'Username already exists.'
    } else if (err instanceof ApiError) {
      const body = err.body as { error?: string } | null
      createError.value = body?.error ?? err.message
    } else {
      createError.value = 'Failed to create user'
    }
  } finally {
    createSubmitting.value = false
  }
}

function formatDate(dateString: string): string {
  const date = new Date(dateString)
  return date.toLocaleString()
}

onMounted(() => {
  fetchUsers()
})
</script>

<template>
  <div class="user-management">
    <h1>User Management</h1>

    <div v-if="error" class="error-banner" data-testid="error-banner">
      {{ error }}
      <button class="dismiss-btn" @click="error = null">Dismiss</button>
    </div>

    <section class="create-user-section">
      <h2>Create User</h2>
      <form class="create-form" @submit.prevent="createUser">
        <div class="form-field">
          <label for="create-username">Username</label>
          <input
            id="create-username"
            v-model="createForm.username"
            type="text"
            placeholder="Username"
            autocomplete="off"
          />
        </div>
        <div class="form-field">
          <label for="create-email">Email (optional)</label>
          <input
            id="create-email"
            v-model="createForm.email"
            type="email"
            placeholder="Email"
            autocomplete="off"
          />
        </div>
        <div class="form-field">
          <label for="create-password">Password</label>
          <input
            id="create-password"
            v-model="createForm.password"
            type="password"
            placeholder="Password"
            autocomplete="new-password"
          />
        </div>
        <div class="form-field">
          <label for="create-role">Role</label>
          <select id="create-role" v-model="createForm.role">
            <option value="">Select role</option>
            <option value="student">student</option>
            <option value="admin">admin</option>
          </select>
        </div>

        <div v-if="createError" class="create-error" data-testid="create-error">
          {{ createError }}
        </div>

        <button type="submit" :disabled="createSubmitting" class="submit-btn">
          {{ createSubmitting ? 'Creating...' : 'Create User' }}
        </button>
      </form>
    </section>

    <section class="user-list-section">
      <h2>Users</h2>

      <div v-if="loading" class="loading">Loading users...</div>

      <table v-else class="users-table">
        <thead>
          <tr>
            <th>Username</th>
            <th>Role</th>
            <th>Status</th>
            <th>Email</th>
            <th>Created</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="user in users" :key="user.id" class="user-row">
            <td class="col-username">{{ user.username }}</td>
            <td class="col-role">{{ user.role }}</td>
            <td class="col-status">
              <span :class="['status-badge', user.is_active ? 'active' : 'inactive']">
                {{ user.is_active ? 'Active' : 'Inactive' }}
              </span>
            </td>
            <td class="col-email">{{ user.email ?? '' }}</td>
            <td class="col-created">{{ formatDate(user.created_at) }}</td>
            <td class="col-actions">
              <button
                v-if="user.is_active"
                class="action-btn deactivate-btn"
                @click="deactivateUser(user)"
              >
                Deactivate
              </button>
              <button
                v-else
                class="action-btn activate-btn"
                @click="activateUser(user)"
              >
                Activate
              </button>
              <button
                v-if="user.role === 'student'"
                class="action-btn delete-btn"
                @click="deleteUser(user)"
              >
                Delete
              </button>
            </td>
          </tr>
          <tr v-if="users.length === 0">
            <td colspan="6" class="no-users">No users found.</td>
          </tr>
        </tbody>
      </table>
    </section>
  </div>
</template>

<style scoped>
.user-management {
  padding: 2rem;
  max-width: 1400px;
  margin: 0 auto;
}

h1 {
  font-size: 2rem;
  color: #333;
  margin-bottom: 1.5rem;
}

h2 {
  font-size: 1.25rem;
  color: #555;
  margin-bottom: 1rem;
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

.create-user-section {
  background: white;
  border: 1px solid #ddd;
  border-radius: 6px;
  padding: 1.5rem;
  margin-bottom: 2rem;
}

.create-form {
  display: flex;
  flex-wrap: wrap;
  gap: 1rem;
  align-items: flex-end;
}

.form-field {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  min-width: 180px;
}

.form-field label {
  font-size: 0.85rem;
  font-weight: 600;
  color: #444;
}

.form-field input,
.form-field select {
  padding: 0.5rem 0.75rem;
  border: 1px solid #ccc;
  border-radius: 4px;
  font-size: 0.95rem;
}

.create-error {
  width: 100%;
  color: #d32f2f;
  font-size: 0.9rem;
  padding: 0.4rem 0;
}

.submit-btn {
  padding: 0.5rem 1.25rem;
  background-color: #1976d2;
  color: white;
  border: none;
  border-radius: 4px;
  font-size: 0.95rem;
  cursor: pointer;
  font-weight: 500;
}

.submit-btn:disabled {
  background-color: #bbb;
  cursor: not-allowed;
}

.user-list-section {
  background: white;
  border: 1px solid #ddd;
  border-radius: 6px;
  padding: 1.5rem;
}

.loading {
  text-align: center;
  padding: 2rem;
  color: #666;
}

.users-table {
  width: 100%;
  border-collapse: collapse;
}

.users-table thead {
  background-color: #f5f5f5;
}

.users-table th {
  padding: 0.75rem 1rem;
  text-align: left;
  border-bottom: 2px solid #ddd;
  font-weight: 600;
  color: #333;
}

.users-table td {
  padding: 0.75rem 1rem;
  border-bottom: 1px solid #eee;
}

.user-row:hover {
  background-color: #fafafa;
}

.status-badge {
  display: inline-block;
  padding: 0.2rem 0.6rem;
  border-radius: 12px;
  font-size: 0.8rem;
  font-weight: 600;
}

.status-badge.active {
  background-color: #e8f5e9;
  color: #2e7d32;
}

.status-badge.inactive {
  background-color: #fce4ec;
  color: #c62828;
}

.col-actions {
  display: flex;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.action-btn {
  padding: 0.35rem 0.75rem;
  border: none;
  border-radius: 4px;
  font-size: 0.85rem;
  cursor: pointer;
  font-weight: 500;
}

.deactivate-btn {
  background-color: #fff3e0;
  color: #e65100;
}

.deactivate-btn:hover {
  background-color: #ffe0b2;
}

.activate-btn {
  background-color: #e8f5e9;
  color: #2e7d32;
}

.activate-btn:hover {
  background-color: #c8e6c9;
}

.delete-btn {
  background-color: #ffebee;
  color: #c62828;
}

.delete-btn:hover {
  background-color: #ffcdd2;
}

.no-users {
  text-align: center;
  color: #999;
  padding: 2rem;
}
</style>
