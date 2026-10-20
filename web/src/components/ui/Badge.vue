<script setup lang="ts">
import { computed } from 'vue'
import type { BadgeTone } from './types'

const props = withDefaults(
  defineProps<{
    tone?: BadgeTone
    size?: 'sm' | 'md' | 'lg'
    /** 前置圆点（状态徽标常用）。 */
    dot?: boolean
    /** 胶囊形圆角。 */
    pill?: boolean
    /** 显示关闭按钮。 */
    closable?: boolean
    /** 无障碍标签，用于图标-only 徽标。 */
    ariaLabel?: string
  }>(),
  { tone: 'neutral', size: 'md', dot: false, pill: false, closable: false, ariaLabel: undefined },
)

const emit = defineEmits<{
  (e: 'close'): void
}>()

const classes = computed(() => [
  'badge',
  `badge--${props.tone}`,
  {
    'badge--sm': props.size === 'sm',
    'badge--lg': props.size === 'lg',
    'badge--dot': props.dot,
    'badge--pill': props.pill,
  },
])
</script>

<template>
  <span :class="classes" :aria-label="ariaLabel">
    <slot />
    <button
      v-if="closable"
      type="button"
      class="badge__close"
      aria-label="移除"
      @click.stop="emit('close')"
    >
      <svg viewBox="0 0 16 16" width="10" height="10" aria-hidden="true">
        <path
          d="M4 4l8 8M12 4l-8 8"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          fill="none"
        />
      </svg>
    </button>
  </span>
</template>
