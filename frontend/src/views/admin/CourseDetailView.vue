// @{"req": ["REQ-FEADMIN-707"]}

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { get, ApiError } from '@/api/client'
import { renderAsciidoc } from '@/utils/renderAsciidoc'
import type {
  CourseOverviewResponse,
  OverviewSection,
  OverviewHomework,
  OverviewSubmission,
  OverviewGrade,
} from '@/types/admin'

const route = useRoute()
const router = useRouter()

const courseId = route.params.id as string

const overview = ref<CourseOverviewResponse | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)

// Per-section rendered HTML cache: map from section id to sanitized HTML string.
// Populated lazily when a section panel is first expanded.
const sectionHtmlCache = ref<Record<string, string>>({})
const expandedSection = ref<string | null>(null)

// @{"req": ["REQ-FEADMIN-707"]}
async function fetchOverview(): Promise<void> {
  loading.value = true
  error.value = null
  try {
    const data = await get<CourseOverviewResponse>(`/api/v1/admin/courses/${courseId}/overview`)
    overview.value = data
  } catch (err) {
    if (err instanceof ApiError) {
      if (err.status === 404) {
        error.value = 'Course not found.'
      } else {
        error.value = `Failed to load course (HTTP ${err.status}).`
      }
    } else {
      error.value = 'Failed to load course. Please try again.'
    }
  } finally {
    loading.value = false
  }
}

// @{"req": ["REQ-FEADMIN-707"]}
async function toggleSection(section: OverviewSection): Promise<void> {
  if (expandedSection.value === section.id) {
    expandedSection.value = null
    return
  }
  expandedSection.value = section.id
  if (!(section.id in sectionHtmlCache.value)) {
    // Render once and cache so repeated opens are instant.
    sectionHtmlCache.value[section.id] = await renderAsciidoc(section.content_adoc)
  }
}

// @{"req": ["REQ-FEADMIN-707"]}
function gradeForSubmission(submissionId: string): OverviewGrade | undefined {
  return overview.value?.grades.find(g => g.submission_id === submissionId)
}

// @{"req": ["REQ-FEADMIN-707"]}
function submissionsForHomework(homeworkId: string): OverviewSubmission[] {
  return overview.value?.submissions.filter(s => s.homework_id === homeworkId) ?? []
}

// @{"req": ["REQ-FEADMIN-707"]}
function homeworkForSection(sectionIndex: number): OverviewHomework | undefined {
  return overview.value?.homework.find(h => h.section_index === sectionIndex)
}

// @{"req": ["REQ-FEADMIN-707"]}
function formatDate(dateString: string): string {
  const date = new Date(dateString)
  return date.toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

// @{"req": ["REQ-FEADMIN-707"]}
function goBack(): void {
  router.push('/admin/courses')
}

onMounted(() => {
  fetchOverview()
})
</script>

<template>
  <div class="course-detail">
    <div class="breadcrumb">
      <button class="back-link" @click="goBack">Course Oversight</button>
      <span class="breadcrumb-sep">/</span>
      <span class="breadcrumb-current">Course Detail</span>
    </div>

    <div v-if="loading" class="loading" data-testid="loading">
      Loading course...
    </div>

    <div v-else-if="error" class="error" data-testid="error">
      {{ error }}
    </div>

    <template v-else-if="overview">
      <!-- Course summary -->
      <section class="course-summary" data-testid="course-summary">
        <h1 class="course-topic">{{ overview.course.topic }}</h1>
        <div class="summary-meta">
          <span class="meta-item"><strong>Student:</strong> {{ overview.course.username }}</span>
          <span class="meta-item"><strong>Status:</strong> <span class="status-badge">{{ overview.course.status }}</span></span>
          <span class="meta-item"><strong>Created:</strong> {{ formatDate(overview.course.created_at) }}</span>
          <span class="meta-item"><strong>Updated:</strong> {{ formatDate(overview.course.updated_at) }}</span>
        </div>
      </section>

      <!-- Syllabus -->
      <section class="panel" data-testid="syllabus-panel">
        <h2 class="panel-title">Syllabus</h2>
        <div v-if="overview.syllabus" class="syllabus-body">
          <div class="syllabus-meta">
            <span class="meta-item"><strong>Version:</strong> {{ overview.syllabus.version }}</span>
            <span v-if="overview.syllabus.approved_at" class="meta-item">
              <strong>Approved:</strong> {{ formatDate(overview.syllabus.approved_at) }}
            </span>
            <span v-else class="meta-item pending-badge">Pending approval</span>
          </div>
          <pre class="adoc-source">{{ overview.syllabus.content_adoc }}</pre>
        </div>
        <p v-else class="empty-state">No syllabus yet.</p>
      </section>

      <!-- Sections -->
      <section class="panel" data-testid="sections-panel">
        <h2 class="panel-title">Sections ({{ overview.sections.length }})</h2>
        <div v-if="overview.sections.length > 0" class="section-list">
          <div
            v-for="section in overview.sections"
            :key="section.id"
            class="section-item"
          >
            <button
              class="section-toggle"
              :aria-expanded="expandedSection === section.id"
              @click="toggleSection(section)"
            >
              <span class="section-index">{{ section.section_index }}.</span>
              <span class="section-title">{{ section.title }}</span>
              <span class="citation-badge" v-if="section.citation_verified">Citations verified</span>
              <span class="toggle-arrow">{{ expandedSection === section.id ? '▲' : '▼' }}</span>
            </button>

            <div v-if="expandedSection === section.id" class="section-content-wrapper">
              <div
                class="section-body adoc-content"
                v-html="sectionHtmlCache[section.id] ?? ''"
              ></div>

              <!-- Homework for this section, if any -->
              <template v-if="homeworkForSection(section.section_index)">
                <div class="homework-block" :data-testid="`hw-${section.section_index}`">
                  <h3 class="homework-title">
                    Homework: {{ homeworkForSection(section.section_index)!.title }}
                  </h3>
                  <div class="homework-meta">
                    <span class="meta-item">
                      <strong>Weight:</strong> {{ homeworkForSection(section.section_index)!.grade_weight }}%
                    </span>
                    <span v-if="homeworkForSection(section.section_index)!.due_date" class="meta-item">
                      <strong>Due:</strong> {{ formatDate(homeworkForSection(section.section_index)!.due_date!) }}
                    </span>
                  </div>
                  <div class="rubric-block">
                    <h4>Rubric</h4>
                    <pre class="rubric-text">{{ homeworkForSection(section.section_index)!.rubric }}</pre>
                  </div>

                  <!-- Submissions for this homework -->
                  <div
                    v-if="submissionsForHomework(homeworkForSection(section.section_index)!.id).length > 0"
                    class="submissions-block"
                  >
                    <h4>Submissions</h4>
                    <table class="submissions-table">
                      <thead>
                        <tr>
                          <th>Submitted</th>
                          <th>Format</th>
                          <th>Size</th>
                          <th>Grading Status</th>
                          <th>Raw Score</th>
                          <th>Final Score</th>
                        </tr>
                      </thead>
                      <tbody>
                        <tr
                          v-for="sub in submissionsForHomework(homeworkForSection(section.section_index)!.id)"
                          :key="sub.id"
                        >
                          <td>{{ formatDate(sub.submitted_at) }}</td>
                          <td>{{ sub.format }}</td>
                          <td>{{ (sub.file_size_bytes / 1024).toFixed(1) }} KB</td>
                          <td>
                            <span :class="['grading-badge', `grading-${sub.grading_status}`]">
                              {{ sub.grading_status }}
                            </span>
                          </td>
                          <td>{{ sub.raw_score !== null ? sub.raw_score : '—' }}</td>
                          <td>
                            <template v-if="gradeForSubmission(sub.id)">
                              {{ gradeForSubmission(sub.id)!.final_score }}
                            </template>
                            <template v-else>—</template>
                          </td>
                        </tr>
                      </tbody>
                    </table>
                  </div>
                  <p v-else class="empty-state">No submissions yet.</p>
                </div>
              </template>
            </div>
          </div>
        </div>
        <p v-else class="empty-state">No sections yet.</p>
      </section>

      <!-- Grades summary -->
      <section class="panel" data-testid="grades-panel">
        <h2 class="panel-title">Grades Summary</h2>
        <div v-if="overview.grades.length > 0">
          <table class="grades-table">
            <thead>
              <tr>
                <th>Graded At</th>
                <th>Raw Score</th>
                <th>Late Days</th>
                <th>Late Penalty</th>
                <th>Badge Waiver</th>
                <th>Badge Improvement</th>
                <th>Final Score</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="grade in overview.grades" :key="grade.id">
                <td>{{ formatDate(grade.graded_at) }}</td>
                <td>{{ grade.raw_score }}</td>
                <td>{{ grade.late_days }}</td>
                <td>{{ grade.late_penalty_amount }}</td>
                <td>{{ grade.badge_waiver_applied ? 'Yes' : 'No' }}</td>
                <td>{{ grade.badge_improvement ? 'Yes' : 'No' }}</td>
                <td class="final-score">{{ grade.final_score }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-else class="empty-state">No grades yet.</p>
      </section>
    </template>
  </div>
</template>

<style scoped>
.course-detail {
  padding: 2rem;
  max-width: 1200px;
  margin: 0 auto;
}

.breadcrumb {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  margin-bottom: 1.5rem;
  font-size: 0.9rem;
  color: #666;
}

.back-link {
  background: none;
  border: none;
  color: #1976d2;
  cursor: pointer;
  padding: 0;
  font-size: 0.9rem;
  text-decoration: underline;
}

.back-link:hover {
  color: #1565c0;
}

.breadcrumb-sep {
  color: #999;
}

.breadcrumb-current {
  color: #333;
}

/* Course summary */
.course-summary {
  margin-bottom: 2rem;
}

.course-topic {
  font-size: 1.75rem;
  color: #333;
  margin: 0 0 0.75rem;
}

.summary-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 1rem;
  font-size: 0.95rem;
  color: #555;
}

.meta-item {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
}

.status-badge {
  background: #e3f2fd;
  color: #1565c0;
  padding: 0.1rem 0.4rem;
  border-radius: 3px;
  font-size: 0.85rem;
  text-transform: capitalize;
}

.pending-badge {
  background: #fff8e1;
  color: #f57c00;
  padding: 0.1rem 0.4rem;
  border-radius: 3px;
  font-size: 0.85rem;
}

/* Panels */
.panel {
  background: white;
  border: 1px solid #e0e0e0;
  border-radius: 6px;
  margin-bottom: 1.5rem;
  overflow: hidden;
}

.panel-title {
  font-size: 1.15rem;
  font-weight: 600;
  color: #333;
  margin: 0;
  padding: 1rem 1.25rem;
  border-bottom: 1px solid #e0e0e0;
  background: #fafafa;
}

.empty-state {
  padding: 1.25rem;
  color: #999;
  font-style: italic;
  margin: 0;
}

/* Syllabus */
.syllabus-body {
  padding: 1.25rem;
}

.syllabus-meta {
  display: flex;
  gap: 1.5rem;
  margin-bottom: 1rem;
  font-size: 0.9rem;
  color: #555;
}

.adoc-source {
  background: #f6f8fa;
  border: 1px solid #e0e0e0;
  border-radius: 4px;
  padding: 1rem;
  font-size: 0.85rem;
  overflow-x: auto;
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
}

/* Sections */
.section-list {
  padding: 0.5rem;
}

.section-item {
  border: 1px solid #e8e8e8;
  border-radius: 4px;
  margin-bottom: 0.5rem;
  overflow: hidden;
}

.section-toggle {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.75rem 1rem;
  background: #fafafa;
  border: none;
  cursor: pointer;
  text-align: left;
  font-size: 0.95rem;
  color: #333;
}

.section-toggle:hover {
  background: #f0f4f8;
}

.section-index {
  font-weight: 600;
  color: #666;
  min-width: 1.5rem;
}

.section-title {
  flex: 1;
  font-weight: 500;
}

.citation-badge {
  background: #e8f5e9;
  color: #2e7d32;
  padding: 0.1rem 0.4rem;
  border-radius: 3px;
  font-size: 0.75rem;
}

.toggle-arrow {
  color: #999;
  font-size: 0.75rem;
  margin-left: auto;
}

.section-content-wrapper {
  padding: 1rem 1.25rem;
  border-top: 1px solid #e8e8e8;
}

/* Rendered AsciiDoc content (mirrors SectionReaderView styles) */
.section-body {
  line-height: 1.7;
  color: #444;
  margin-bottom: 1.5rem;
}

.section-body :deep(h1),
.section-body :deep(h2) {
  margin: 1.25rem 0 0.75rem;
  color: #333;
}

.section-body :deep(h1) { font-size: 1.4rem; }
.section-body :deep(h2) { font-size: 1.2rem; border-bottom: 1px solid #e0e0e0; padding-bottom: 0.25rem; }
.section-body :deep(h3),
.section-body :deep(h4),
.section-body :deep(h5),
.section-body :deep(h6) {
  font-size: 1rem;
  margin: 0.75rem 0 0.4rem;
  color: #444;
}

.section-body :deep(ul),
.section-body :deep(ol) {
  margin: 0.5rem 0 0.5rem 1.5rem;
}

.section-body :deep(li) {
  margin-bottom: 0.25rem;
}

.section-body :deep(pre) {
  background-color: #f6f8fa;
  border: 1px solid #e0e0e0;
  border-radius: 4px;
  padding: 1rem;
  overflow-x: auto;
  font-size: 0.9rem;
}

.section-body :deep(code) {
  font-family: 'Courier New', 'Consolas', monospace;
  background-color: #f0f0f0;
  padding: 0.1em 0.3em;
  border-radius: 3px;
  font-size: 0.9em;
}

.section-body :deep(pre code) {
  background-color: transparent;
  padding: 0;
}

.section-body :deep(table) {
  border-collapse: collapse;
  width: 100%;
  margin: 1rem 0;
}

.section-body :deep(th),
.section-body :deep(td) {
  border: 1px solid #ddd;
  padding: 0.5rem 0.75rem;
  text-align: left;
}

.section-body :deep(th) {
  background-color: #f5f5f5;
  font-weight: 600;
}

/* Homework */
.homework-block {
  border: 1px solid #e0e0e0;
  border-radius: 4px;
  padding: 1rem;
  background: #fafafa;
  margin-top: 0.5rem;
}

.homework-title {
  font-size: 1rem;
  font-weight: 600;
  color: #333;
  margin: 0 0 0.5rem;
}

.homework-meta {
  display: flex;
  gap: 1rem;
  font-size: 0.85rem;
  color: #555;
  margin-bottom: 0.75rem;
}

.rubric-block {
  margin-bottom: 1rem;
}

.rubric-block h4 {
  font-size: 0.9rem;
  color: #555;
  margin: 0.5rem 0 0.25rem;
}

.rubric-text {
  white-space: pre-wrap;
  word-break: break-word;
  background: #f0f0f0;
  border: 1px solid #ddd;
  border-radius: 4px;
  padding: 0.75rem;
  font-size: 0.85rem;
  margin: 0;
}

/* Submissions */
.submissions-block h4 {
  font-size: 0.9rem;
  color: #555;
  margin: 0.5rem 0 0.25rem;
}

.submissions-table,
.grades-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.9rem;
}

.submissions-table th,
.submissions-table td,
.grades-table th,
.grades-table td {
  padding: 0.6rem 0.75rem;
  text-align: left;
  border-bottom: 1px solid #eee;
}

.submissions-table thead tr,
.grades-table thead tr {
  background: #f5f5f5;
  font-weight: 600;
  border-bottom: 2px solid #ddd;
}

.grading-badge {
  padding: 0.1rem 0.4rem;
  border-radius: 3px;
  font-size: 0.8rem;
  text-transform: capitalize;
}

.grading-pending {
  background: #fff8e1;
  color: #f57c00;
}

.grading-graded {
  background: #e8f5e9;
  color: #2e7d32;
}

.grading-failed {
  background: #ffebee;
  color: #c62828;
}

/* Grades panel */
.grades-table {
  margin: 0;
}

.grades-table td,
.grades-table th {
  padding: 0.75rem;
}

.final-score {
  font-weight: 600;
  color: #1565c0;
}

/* Loading / error */
.loading {
  text-align: center;
  padding: 3rem;
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
</style>
