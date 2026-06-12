// @{"req": ["REQ-FEAUTH-034", "REQ-FEAUTH-035", "REQ-FEAUTH-036", "REQ-FEAUTH-037", "REQ-FEAUTH-038", "REQ-FEAUTH-039", "REQ-FEAUTH-056", "REQ-FEAUTH-057", "REQ-FEAUTH-149", "REQ-FEAUTH-167", "REQ-FEAUTH-168"]}
<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { post, ApiError } from '@/api/client'

// Single source of truth for consent version string
// Used both in the displayed label and in the POST body
const CONSENT_VERSION = '1.0'

const router = useRouter()
const auth = useAuthStore()

const isLoading = ref(false)
const errorMessage = ref<string | null>(null)

const handleAgree = async (): Promise<void> => {
  isLoading.value = true
  errorMessage.value = null

  try {
    await post('/api/v1/consent', { version: CONSENT_VERSION })
    auth.setConsented()
    // Admins are exempt from the consent gate but can still land here;
    // send each role to its own home.
    await router.push(auth.isAdmin ? '/admin/users' : '/courses')
  } catch (err) {
    if (err instanceof ApiError) {
      errorMessage.value = 'Failed to record consent. Please try again.'
    } else {
      errorMessage.value = 'Failed to record consent. Please try again.'
    }
  } finally {
    isLoading.value = false
  }
}
</script>

<template>
  <div class="consent-container">
    <div class="consent-content">
      <div class="consent-logo-section">
        <img src="/valory.svg" alt="Valory" class="consent-logo" />
      </div>
      <h1 class="consent-title">AI Processing Consent Statement — Version {{ CONSENT_VERSION }}</h1>

      <div class="consent-statement-panel">
        <section class="consent-section">
          <h2>About This Document</h2>
          <p>
            This statement explains what personal data the Valory AI professor system
            collects, how that data is processed by an external AI service, how activity
            is recorded, and what rights you have over your data. By clicking "I agree"
            in the Valory application, you record your consent to the practices described
            here. Your consent is stored with this document version ({{ CONSENT_VERSION }}) and a UTC
            timestamp.
          </p>
        </section>

        <section class="consent-section">
          <h2>What Data Valory Holds About You</h2>
          <p>Valory stores the following categories of data linked to your account:</p>
          <dl class="data-list">
            <dt>Account data</dt>
            <dd>Your username, hashed password, and role (student or admin).</dd>

            <dt>Course information</dt>
            <dd>
              The topic you chose to study, the current status of your course, and
              timestamps showing when the course was created and last updated.
            </dd>

            <dt>Intake chat messages</dt>
            <dd>
              Every message you send during the intake questionnaire, and every reply
              the assistant sends, stored in the chat_messages table with your course
              identifier and a creation timestamp.
            </dd>

            <dt>Active-course chat messages</dt>
            <dd>
              Messages you send and assistant replies received while your course is in
              the active phase, stored in the same chat_messages table.
            </dd>

            <dt>Homework submissions</dt>
            <dd>
              Files you upload as homework answers, stored on the server filesystem
              under a path recorded in the submissions table, together with the
              submission timestamp and grading status.
            </dd>

            <dt>Grades and feedback</dt>
            <dd>
              Numeric scores and computed grades derived from your submissions, stored
              in the grades table. Section-level feedback you provide on generated
              content is stored in section_feedback.
            </dd>

            <dt>Syllabus drafts</dt>
            <dd>
              Each version of your course syllabus, including any that were modified at
              your request, stored in the syllabi table.
            </dd>

            <dt>Notifications</dt>
            <dd>
              System notifications sent to you (for example, timeout or grading alerts),
              stored in the notifications table.
            </dd>

            <dt>Consent record</dt>
            <dd>
              The fact that you agreed to this statement, the version number ({{ CONSENT_VERSION }}), and
              the UTC timestamp of agreement, stored in the consents table.
            </dd>
          </dl>
        </section>

        <section class="consent-section">
          <h2>How Your Data Is Sent to Anthropic's Claude API</h2>
          <p>
            Valory generates course content, conducts the intake questionnaire, and grades
            homework by sending data to the Claude API, which is operated by Anthropic PBC.
            The following data categories are included in those API calls:
          </p>
          <ul class="data-list-items">
            <li>Your course topic.</li>
            <li>Your intake chat messages and the assistant's replies, used to build a personalised course syllabus.</li>
            <li>Your active-course chat messages and the assistant's replies, used to answer questions during the course.</li>
            <li>The text of your homework submissions, together with the homework rubric, used to produce a numeric score.</li>
            <li>AsciiDoc syllabus content, used when regenerating individual course sections at your request.</li>
          </ul>
          <p>
            Anthropic processes these inputs under its own terms of service and privacy
            policy. Valory does not control Anthropic's data handling practices beyond
            what is specified in the API agreement in force at the time of processing.
            You should consult Anthropic's current documentation if you need information
            about how Anthropic handles API request data.
          </p>
        </section>

        <section class="consent-section">
          <h2>Audit Log</h2>
          <p>
            All pipeline activity — course creation, intake completion, syllabus
            generation steps, content generation events, grading events — is recorded in
            the pipeline_events table. These records are associated with your course
            identifier and are not deleted when you delete your account (see the section
            below on data retention). The audit log exists to support operational
            monitoring and is not used for purposes other than system administration.
          </p>
        </section>

        <section class="consent-section">
          <h2>Data Retention and Your Right to Deletion</h2>
          <p>
            You may request permanent deletion of your personal data at any time through
            the account deletion function available to administrators. Upon an authorized
            deletion request, Valory will permanently delete:
          </p>
          <ul class="data-list-items">
            <li>Your user account record.</li>
            <li>All courses, syllabi, and homework records linked to your account.</li>
            <li>All chat messages linked to your courses.</li>
            <li>All homework submissions and associated files on disk.</li>
            <li>All grades and feedback records linked to your account.</li>
            <li>All notifications addressed to you.</li>
            <li>Your consent record.</li>
          </ul>
          <p>
            The audit log rows in pipeline_events and agent_runs are associated with
            course identifiers, not directly with your user UUID. When your courses are
            deleted, those rows are cascade-deleted because agent_runs references
            courses(id) ON DELETE RESTRICT and pipeline_events references
            agent_runs(id) ON DELETE CASCADE. In practice this means pipeline event
            rows are removed as part of course deletion. If the delete operation encounters
            a restrict violation — for example because an agent run is still in the
            running state — the deletion will be blocked until in-flight operations are
            terminated.
          </p>
          <p>
            In-flight agent operations are terminated before deletion proceeds.
          </p>
        </section>

        <section class="consent-section">
          <h2>What This Statement Does Not Cover</h2>
          <p>
            This statement describes the practices that can be verified in the Valory
            source code as of the version in which this document was introduced. It does
            not:
          </p>
          <ul class="data-list-items">
            <li>
              Claim compliance with any specific legal framework (for example GDPR or
              CCPA), because legal compliance determinations require qualified legal
              review that has not been performed.
            </li>
            <li>
              Claim that Anthropic holds any certification (for example SOC 2 or ISO
              27001), because Valory cannot represent the certification status of a
              third-party service.
            </li>
            <li>
              Describe data flows introduced after this document version was written.
              If the system is materially changed, a new consent version will be issued
              and a fresh consent prompt will be displayed.
            </li>
          </ul>
        </section>

        <section class="consent-section">
          <h2>Consent Version and Recording</h2>
          <p>
            When you click "I agree," the Valory application sends a POST request to
            /api/v1/consent with the payload { "version": "{{ CONSENT_VERSION }}" }. The server records
            your user identifier, the version string {{ CONSENT_VERSION }}, and the UTC timestamp of
            agreement. You will not be prompted again for the same version unless your
            consent record is deleted.
          </p>
        </section>
      </div>

      <div v-if="errorMessage" class="error-message">
        {{ errorMessage }}
      </div>

      <button
        :disabled="isLoading"
        @click="handleAgree"
        class="agree-button"
      >
        I agree
      </button>
    </div>
  </div>
</template>

<style scoped>
.consent-container {
  display: flex;
  justify-content: center;
  align-items: flex-start;
  min-height: 100vh;
  padding: 20px;
  background-color: #f5f5f5;
}

.consent-content {
  max-width: 800px;
  width: 100%;
  display: flex;
  flex-direction: column;
  max-height: 100vh;
  padding: 20px;
  background-color: white;
  border-radius: 8px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
}

.consent-logo-section {
  display: flex;
  justify-content: center;
  margin-bottom: 20px;
}

.consent-logo {
  height: 40px;
  width: 40px;
}

.consent-title {
  font-size: 24px;
  font-weight: 600;
  margin-bottom: 20px;
  color: #1976d2;
}

.consent-statement-panel {
  flex: 1;
  overflow-y: auto;
  padding: 20px;
  background-color: #fafafa;
  border: 1px solid #e0e0e0;
  border-radius: 4px;
  margin-bottom: 20px;
}

.consent-section {
  margin-bottom: 24px;
}

.consent-section h2 {
  font-size: 18px;
  font-weight: 600;
  margin-bottom: 12px;
  color: #333;
}

.consent-section p {
  font-size: 14px;
  line-height: 1.6;
  color: #555;
  margin-bottom: 12px;
}

.data-list {
  font-size: 14px;
  line-height: 1.8;
  color: #555;
  margin-left: 0;
}

.data-list dt {
  font-weight: 600;
  margin-top: 8px;
}

.data-list dd {
  margin-left: 20px;
  margin-bottom: 12px;
}

.data-list-items {
  font-size: 14px;
  line-height: 1.8;
  color: #555;
  margin-left: 20px;
}

.data-list-items li {
  margin-bottom: 8px;
}

.error-message {
  color: #d32f2f;
  margin-bottom: 20px;
  padding: 12px;
  background-color: #ffebee;
  border-radius: 4px;
  font-size: 14px;
}

.agree-button {
  align-self: center;
  padding: 12px 32px;
  font-size: 16px;
  font-weight: 600;
  background-color: #1976d2;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  transition: background-color 0.3s ease;
}

.agree-button:hover:not(:disabled) {
  background-color: #1565c0;
}

.agree-button:disabled {
  background-color: #bdbdbd;
  cursor: not-allowed;
}
</style>
