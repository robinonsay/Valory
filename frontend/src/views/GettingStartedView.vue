// @{"req": ["REQ-FEONBOARD-001", "REQ-FEONBOARD-003", "REQ-FEONBOARD-004", "REQ-FEONBOARD-010", "REQ-FEONBOARD-011", "REQ-FEONBOARD-012", "REQ-FEONBOARD-013", "REQ-FEONBOARD-014", "REQ-FEONBOARD-015", "REQ-FEONBOARD-016", "REQ-FEONBOARD-020", "REQ-FEONBOARD-021", "REQ-FEONBOARD-022", "REQ-FEONBOARD-023"]}

<script setup lang="ts">
import { computed } from 'vue'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()

// @{"req": ["REQ-FEONBOARD-003", "REQ-FEONBOARD-004"]}
// Content is driven by the user's role. isAdmin is checked first because
// both roles can reach this route — the view itself selects the correct
// step sequence rather than relying on the router to pick a different component.
const isAdmin = computed(() => auth.isAdmin)
</script>

<template>
  <div class="getting-started-container">
    <!-- @{"req": ["REQ-FEONBOARD-003", "REQ-FEONBOARD-010", "REQ-FEONBOARD-011", "REQ-FEONBOARD-012", "REQ-FEONBOARD-013", "REQ-FEONBOARD-014", "REQ-FEONBOARD-015", "REQ-FEONBOARD-016"]} -->
    <template v-if="!isAdmin">
      <h1>Getting Started — Student Guide</h1>
      <p class="intro">
        Follow these seven steps to complete your first course on Valory.
      </p>

      <ol class="steps-list">
        <li class="step">
          <div class="step-header">
            <span class="step-number">1</span>
            <h2>Accept AI Consent</h2>
          </div>
          <p>
            On your first login you will be directed to a consent screen. Read the
            AI processing consent statement and click <strong>Accept</strong> to
            allow Valory to process your course data. This is a one-time step
            required before you can access any course content.
          </p>
        </li>

        <li class="step">
          <div class="step-header">
            <span class="step-number">2</span>
            <h2>Create a Course</h2>
          </div>
          <p>
            Go to <RouterLink to="/courses">Courses</RouterLink> and click
            <strong>New course</strong>. Enter a topic (e.g. "Linear Algebra") and
            submit. A non-empty topic is required — the AI professor uses it to
            design the entire course.
          </p>
        </li>

        <li class="step">
          <div class="step-header">
            <span class="step-number">3</span>
            <h2>Intake Chat</h2>
          </div>
          <p>
            After creating the course you enter the intake chat. Converse with the
            AI professor to describe your learning goals, prior experience, and any
            preferences. The professor uses these answers to tailor the syllabus
            specifically for you.
          </p>
        </li>

        <li class="step">
          <div class="step-header">
            <span class="step-number">4</span>
            <h2>Review and Approve the Syllabus</h2>
          </div>
          <p>
            The AI professor will draft a syllabus based on the intake chat. Read
            through the proposed units and schedule, then click <strong>Approve</strong>
            to confirm. The professor will not generate course sections until you
            approve the syllabus.
          </p>
        </li>

        <li class="step">
          <div class="step-header">
            <span class="step-number">5</span>
            <h2>Read the Generated Sections</h2>
          </div>
          <p>
            Once you approve the syllabus, Valory generates your course sections
            automatically. Navigate to the course hub to find all sections listed.
            Click any section title to open the reader and work through the content
            at your own pace.
          </p>
        </li>

        <li class="step">
          <div class="step-header">
            <span class="step-number">6</span>
            <h2>Submit Homework</h2>
          </div>
          <p>
            Each section may have an associated homework assignment. Upload your
            solution as a Markdown, LaTeX, or AsciiDoc file (maximum 10 MB).
            Navigate to the homework page for the relevant section and use the
            upload form to submit.
          </p>
        </li>

        <li class="step">
          <div class="step-header">
            <span class="step-number">7</span>
            <h2>Track Grades and Badges</h2>
          </div>
          <p>
            After your homework is graded, visit the <strong>Grades</strong> page
            for the course to see your scores and feedback. Badges are awarded for
            milestones — check the <strong>Badges</strong> page to see what you
            have earned.
          </p>
        </li>
      </ol>
    </template>

    <!-- @{"req": ["REQ-FEONBOARD-004", "REQ-FEONBOARD-020", "REQ-FEONBOARD-021", "REQ-FEONBOARD-022", "REQ-FEONBOARD-023"]} -->
    <template v-else>
      <h1>Getting Started — Admin Guide</h1>
      <p class="intro">
        Complete these four setup steps to configure and monitor Valory.
      </p>

      <ol class="steps-list">
        <li class="step">
          <div class="step-header">
            <span class="step-number">1</span>
            <h2>User Management</h2>
          </div>
          <p>
            Go to <RouterLink to="/admin/users">Users</RouterLink> to create
            student and admin accounts. Passwords are set at account creation —
            share them securely with the account holder (e.g. via an encrypted
            channel). You can deactivate, reactivate, or delete accounts at any
            time from the same screen.
          </p>
        </li>

        <li class="step">
          <div class="step-header">
            <span class="step-number">2</span>
            <h2>System Configuration</h2>
          </div>
          <p>
            Go to <RouterLink to="/admin/config">Config</RouterLink> to adjust
            system-wide settings. Each setting is documented in the
            <strong>admin configuration guide</strong> at
            <code>docs/guides/admin-configuration.md</code> in the repository.
            Read the guide before making changes — each key has validation rules
            and audit implications described there.
          </p>
        </li>

        <li class="step">
          <div class="step-header">
            <span class="step-number">3</span>
            <h2>Audit Log</h2>
          </div>
          <p>
            Go to <RouterLink to="/admin/audit">Audit Log</RouterLink> to review
            a chronological record of every admin action. The log is stored as a
            tamper-evident hash chain — each entry includes a hash of the previous
            entry so any modification to historical records is detectable. Use the
            verify action on the audit page to confirm chain integrity.
          </p>
        </li>

        <li class="step">
          <div class="step-header">
            <span class="step-number">4</span>
            <h2>Course Oversight</h2>
          </div>
          <p>
            Go to <RouterLink to="/admin/courses">Course Oversight</RouterLink>
            to view all students' courses across every stage of the pipeline —
            intake, syllabus draft, generating, and active. Use this view to
            monitor progress and diagnose issues for any student.
          </p>
        </li>
      </ol>
    </template>
  </div>
</template>

<style scoped>
.getting-started-container {
  padding: 2rem;
  max-width: 860px;
  margin: 0 auto;
}

.getting-started-container h1 {
  font-size: 2rem;
  color: #2c3e50;
  margin: 0 0 0.5rem 0;
}

.intro {
  font-size: 1.05rem;
  color: #555;
  margin-bottom: 2rem;
}

.steps-list {
  list-style: none;
  padding: 0;
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.step {
  background: white;
  border: 1px solid #ddd;
  border-radius: 8px;
  padding: 1.5rem;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.05);
}

.step-header {
  display: flex;
  align-items: center;
  gap: 1rem;
  margin-bottom: 0.75rem;
}

.step-number {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 2rem;
  height: 2rem;
  background-color: #1976d2;
  color: white;
  border-radius: 50%;
  font-weight: 700;
  font-size: 0.95rem;
  flex-shrink: 0;
}

.step-header h2 {
  font-size: 1.2rem;
  color: #333;
  margin: 0;
}

.step p {
  margin: 0;
  color: #555;
  line-height: 1.6;
  font-size: 0.95rem;
}

.step a {
  color: #1976d2;
  text-decoration: none;
}

.step a:hover {
  text-decoration: underline;
}

code {
  background-color: #f5f5f5;
  padding: 0.15rem 0.4rem;
  border-radius: 3px;
  font-size: 0.875rem;
  color: #c0392b;
}
</style>
