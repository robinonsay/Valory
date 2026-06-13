// @{"req": ["REQ-SYS-073", "REQ-SYS-077", "REQ-FEADMIN-709"]}

<script setup lang="ts">
// LayerExpandControl renders the "Expand to next layer" prompt and button.
//
// It is enabled when the SSE composable has received a layer_awaiting_expand
// event for the given layer and the expand action has not yet been taken.
//
// 409 conflict codes (LAYER_NOT_FULLY_APPROVED, TREE_ALREADY_COMPLETE) are
// surfaced as user-visible error messages; other errors are shown generically.

import { ref } from 'vue'
import type { ConflictCode } from '@/types/tree'
import { ApiError } from '@/api/client'

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

// @{"req": ["REQ-SYS-073", "REQ-SYS-077"]}
interface Props {
  // layer is the current layer name (e.g. 'section_goal') for which all nodes
  // are approved and the expand call is pending.
  layer: string
  // nextLayer is the name of the layer that will be unlocked, shown in the UI.
  nextLayer?: string
  // onExpand is called when the user clicks "Expand". The parent supplies the
  // correct API method (expandCourseLayer or expandDraftLayer) so this
  // component stays actor-agnostic.
  onExpand: () => Promise<void>
}

const props = defineProps<Props>()

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const pending = ref(false)
const error = ref<string | null>(null)
const done = ref(false)

// ---------------------------------------------------------------------------
// Expand handler
// ---------------------------------------------------------------------------

// @{"req": ["REQ-SYS-073", "REQ-SYS-077"]}
async function handleExpand(): Promise<void> {
  pending.value = true
  error.value = null
  try {
    await props.onExpand()
    done.value = true
  } catch (err: unknown) {
    if (err instanceof ApiError && err.status === 409) {
      const body = err.body as { error?: ConflictCode; message?: string } | null
      const code: ConflictCode | undefined = body?.error as ConflictCode
      if (code === 'TREE_ALREADY_COMPLETE') {
        error.value = 'The tree is already complete. No further layers can be generated.'
      } else if (code === 'LAYER_NOT_FULLY_APPROVED') {
        error.value = 'Not all nodes in this layer are approved yet. Please approve all nodes first.'
      } else {
        error.value = body?.message ?? 'Conflict: cannot expand layer.'
      }
    } else if (err instanceof Error) {
      error.value = err.message
    } else {
      error.value = 'Failed to expand layer. Please try again.'
    }
  } finally {
    pending.value = false
  }
}
</script>

<template>
  <div class="layer-expand-control">
    <div v-if="done" class="expand-success">
      Layer expanded! Generating {{ nextLayer ?? 'next' }} nodes...
    </div>
    <div v-else>
      <div class="expand-prompt">
        <span class="expand-icon">&#10003;</span>
        All <strong>{{ layer.replace(/_/g, ' ') }}</strong> nodes approved.
        <span v-if="nextLayer">
          Ready to generate the <strong>{{ nextLayer.replace(/_/g, ' ') }}</strong> layer.
        </span>
      </div>
      <div v-if="error" class="expand-error">{{ error }}</div>
      <button
        class="btn btn-expand"
        :disabled="pending"
        @click="handleExpand"
      >
        {{ pending ? 'Expanding...' : `Expand to ${nextLayer ?? 'next layer'}` }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.layer-expand-control {
  padding: 0.75rem 1rem;
  background: #e8f5e9;
  border: 1px solid #a5d6a7;
  border-radius: 6px;
  margin-bottom: 1rem;
}

.expand-prompt {
  font-size: 0.9rem;
  color: #2e7d32;
  margin-bottom: 0.5rem;
}

.expand-icon {
  font-weight: bold;
  margin-right: 0.4rem;
}

.expand-success {
  font-size: 0.9rem;
  color: #1b5e20;
  font-style: italic;
}

.expand-error {
  font-size: 0.85rem;
  color: #c62828;
  margin-bottom: 0.5rem;
}

.btn-expand {
  padding: 0.4rem 1.1rem;
  background: #2e7d32;
  color: #fff;
  border: none;
  border-radius: 4px;
  font-size: 0.9rem;
  cursor: pointer;
  transition: background 0.15s;
}

.btn-expand:hover:not(:disabled) {
  background: #1b5e20;
}

.btn-expand:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}
</style>
