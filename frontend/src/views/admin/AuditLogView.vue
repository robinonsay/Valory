// @{"req": ["REQ-FEADMIN-035", "REQ-FEADMIN-036", "REQ-FEADMIN-037", "REQ-FEADMIN-038", "REQ-FEADMIN-170", "REQ-FEADMIN-175", "REQ-FEADMIN-180", "REQ-FEADMIN-185", "REQ-FEADMIN-190", "REQ-FEADMIN-195", "REQ-FEADMIN-200"]}

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { get, ApiError } from '@/api/client'

interface AuditEntry {
  id: string
  actor_id: string
  actor_username: string
  action: string
  target_id: string | null
  target_type: string | null
  created_at: string
  sequence: number
}

interface AuditResponse {
  entries: AuditEntry[]
  // next_before is an int64 Unix nanoseconds cursor; 0 means no more pages
  next_before: number
}

const auth = useAuthStore()

const entries = ref<AuditEntry[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
// nextBefore holds the cursor from the most recent response; 0 means exhausted
const nextBefore = ref<number>(0)

// @{"req": ["REQ-FEADMIN-035", "REQ-FEADMIN-170", "REQ-FEADMIN-175", "REQ-FEADMIN-180", "REQ-FEADMIN-185", "REQ-FEADMIN-190", "REQ-FEADMIN-195", "REQ-FEADMIN-200"]}
async function fetchAuditLog(before?: number): Promise<void> {
  try {
    loading.value = true
    error.value = null

    let url = '/api/v1/admin/audit?limit=50'
    // Append the cursor only when paginating; initial load uses no before param
    if (before !== undefined && before !== 0) {
      url += `&before=${before}`
    }

    const response = await get<AuditResponse>(url, auth.token)
    entries.value.push(...response.entries)
    nextBefore.value = response.next_before
  } catch (err) {
    if (err instanceof ApiError) {
      error.value = `Failed to load audit log: ${err.message}`
    } else {
      error.value = 'Failed to load audit log'
    }
  } finally {
    loading.value = false
  }
}

// @{"req": ["REQ-FEADMIN-185", "REQ-FEADMIN-190"]}
async function loadMore(): Promise<void> {
  await fetchAuditLog(nextBefore.value)
}

// @{"req": ["REQ-FEADMIN-036"]}
function formatTimestamp(dateString: string): string {
  const date = new Date(dateString)
  return date.toLocaleString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit'
  })
}

onMounted(() => {
  fetchAuditLog()
})
</script>

<template>
  <div class="audit-log">
    <div class="header">
      <h1>Audit Log</h1>
    </div>

    <div v-if="loading && entries.length === 0" class="loading">
      Loading audit log...
    </div>

    <div v-else-if="error" class="error">
      {{ error }}
    </div>

    <div v-else class="content">
      <table v-if="entries.length > 0" class="audit-table">
        <thead>
          <tr>
            <th>Timestamp</th>
            <th>Actor</th>
            <th>Action</th>
            <th>Target</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="entry in entries" :key="entry.id" class="audit-row">
            <td class="timestamp">{{ formatTimestamp(entry.created_at) }}</td>
            <td class="actor">{{ entry.actor_username }}</td>
            <td class="action">{{ entry.action }}</td>
            <td class="target">
              <span v-if="entry.target_type">{{ entry.target_type }}</span>
              <span v-if="entry.target_type && entry.target_id"> / </span>
              <span v-if="entry.target_id">{{ entry.target_id }}</span>
              <span v-if="!entry.target_type && !entry.target_id" class="no-target">—</span>
            </td>
          </tr>
        </tbody>
      </table>

      <div v-else class="no-entries">
        No audit log entries found.
      </div>

      <!-- Load More is shown only when nextBefore !== 0, meaning more pages exist -->
      <div v-if="nextBefore !== 0" class="load-more-section">
        <button
          @click="loadMore"
          :disabled="loading"
          class="load-more-button"
        >
          Load More
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.audit-log {
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

.audit-table {
  width: 100%;
  border-collapse: collapse;
  background-color: white;
  border-radius: 4px;
  overflow: hidden;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
}

.audit-table thead {
  background-color: #f5f5f5;
  font-weight: 600;
}

.audit-table th {
  padding: 1rem;
  text-align: left;
  border-bottom: 2px solid #ddd;
  color: #333;
}

.audit-table td {
  padding: 1rem;
  border-bottom: 1px solid #eee;
}

.audit-row:hover {
  background-color: #fafafa;
}

.audit-row:last-child td {
  border-bottom: none;
}

.timestamp {
  font-size: 0.9rem;
  color: #666;
  white-space: nowrap;
}

.actor {
  font-weight: 500;
}

.action {
  font-family: monospace;
  font-size: 0.9rem;
}

.target {
  font-size: 0.9rem;
  color: #555;
}

.no-target {
  color: #bbb;
}

.no-entries {
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

.load-more-button:hover:not(:disabled) {
  background-color: #1565c0;
}

.load-more-button:disabled {
  background-color: #bbb;
  cursor: not-allowed;
}
</style>
