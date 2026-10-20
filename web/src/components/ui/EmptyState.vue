<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    title: string
    desc?: string
    /** 错误态：图标与标题转 danger。 */
    error?: boolean
    /** 紧凑内边距。 */
    compact?: boolean
  }>(),
  { desc: undefined, error: false, compact: false },
)

const classes = computed(() => [
  'empty',
  { 'empty--compact': props.compact, 'is-error': props.error },
])
</script>

<template>
  <div :class="classes" role="status">
    <div v-if="$slots.icon || error" class="empty__icon" aria-hidden="true">
      <slot name="icon">
        <svg v-if="error" viewBox="0 0 24 24">
          <path d="M12 8v5" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" fill="none" />
          <circle cx="12" cy="16.5" r="1" fill="currentColor" />
          <path
            d="M10.3 3.9L2.5 17.4A2 2 0 004.2 20.4h15.6a2 2 0 001.7-3L13.7 3.9a2 2 0 00-3.4 0z"
            stroke="currentColor" stroke-width="1.6" fill="none" stroke-linejoin="round"
          />
        </svg>
        <svg v-else viewBox="0 0 24 24">
          <rect
            x="3" y="4" width="18" height="16" rx="2"
            stroke="currentColor" stroke-width="1.6" fill="none"
          />
          <path
            d="M4 16l4.5-4.5 3 3L15 11l5 5"
            stroke="currentColor" stroke-width="1.6" fill="none"
            stroke-linecap="round" stroke-linejoin="round"
          />
          <circle cx="15" cy="8.5" r="1.2" fill="currentColor" />
        </svg>
      </slot>
    </div>

    <p class="empty__title">{{ title }}</p>
    <p v-if="desc || $slots.default" class="empty__desc">
      <slot>{{ desc }}</slot>
    </p>

    <div v-if="$slots.actions" class="empty__actions">
      <slot name="actions" />
    </div>
  </div>
</template>
