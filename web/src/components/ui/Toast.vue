<script setup lang="ts">
import { computed } from 'vue'
import Spinner from './Spinner.vue'
import type { ToastItem } from './useToast'

const props = withDefaults(
  defineProps<{
    item: ToastItem
  }>(),
  {},
)

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'action', index: number): void
}>()

const classes = computed(() => ['toast', `toast--${props.item.kind}`])

function onAction(index: number): void {
  emit('action', index)
}
</script>

<template>
  <div :class="classes" :data-toast-id="item.id">
    <span class="toast__icon" aria-hidden="true">
      <Spinner v-if="item.kind === 'loading'" size="sm" />
      <svg v-else-if="item.kind === 'success'" viewBox="0 0 16 16">
        <circle cx="8" cy="8" r="7" stroke="currentColor" stroke-width="1.6" fill="none" />
        <path
          d="M4.5 8.5l2.5 2.5 4.5-5"
          stroke="currentColor" stroke-width="1.8"
          fill="none" stroke-linecap="round" stroke-linejoin="round"
        />
      </svg>
      <svg v-else-if="item.kind === 'error'" viewBox="0 0 16 16">
        <circle cx="8" cy="8" r="7" stroke="currentColor" stroke-width="1.6" fill="none" />
        <path d="M8 4.5v5" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" fill="none" />
        <circle cx="8" cy="11.4" r="0.9" fill="currentColor" />
      </svg>
      <svg v-else-if="item.kind === 'warn'" viewBox="0 0 16 16">
        <path
          d="M8 1.8l6.2 11.4H1.8z"
          stroke="currentColor" stroke-width="1.5" fill="none" stroke-linejoin="round"
        />
        <path d="M8 6v3.4" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" fill="none" />
        <circle cx="8" cy="11.4" r="0.8" fill="currentColor" />
      </svg>
      <svg v-else viewBox="0 0 16 16">
        <circle cx="8" cy="8" r="7" stroke="currentColor" stroke-width="1.6" fill="none" />
        <path d="M8 7.2v4" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" fill="none" />
        <circle cx="8" cy="4.6" r="0.9" fill="currentColor" />
      </svg>
    </span>

    <div class="toast__body">
      <span v-if="item.title" class="toast__title">{{ item.title }}</span>
      <span class="toast__message">{{ item.message }}</span>
      <div v-if="item.actions.length" class="toast__actions">
        <button
          v-for="(action, i) in item.actions"
          :key="action.label"
          type="button"
          class="btn btn--sm btn--ghost"
          @click="onAction(i)"
        >
          {{ action.label }}
        </button>
      </div>
    </div>

    <button
      v-if="item.dismissible"
      type="button"
      class="toast__close"
      aria-label="关闭提示"
      @click="emit('close')"
    >
      <svg viewBox="0 0 16 16" width="10" height="10" aria-hidden="true">
        <path
          d="M4 4l8 8M12 4l-8 8"
          stroke="currentColor" stroke-width="2"
          stroke-linecap="round" fill="none"
        />
      </svg>
    </button>

    <span
      v-if="item.timeout > 0"
      class="toast__progress"
      :style="{ animationDuration: `${item.timeout}ms` }"
      aria-hidden="true"
    />
  </div>
</template>
