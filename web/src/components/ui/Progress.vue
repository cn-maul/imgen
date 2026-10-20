<script setup lang="ts">
import { computed, watch } from 'vue'
import type { ProgressTone } from './types'

const props = withDefaults(
  defineProps<{
    /** 当前进度 0..max。 */
    value: number
    max?: number
    size?: 'sm' | 'md' | 'lg'
    tone?: ProgressTone
    /** 未知进度：滑块来回扫动。 */
    indeterminate?: boolean
    /** 显示百分比文本（在进度条右侧）。 */
    showValue?: boolean
    ariaLabel?: string
  }>(),
  {
    max: 100,
    size: 'md',
    tone: 'accent',
    indeterminate: false,
    showValue: false,
    ariaLabel: undefined,
  },
)

const emit = defineEmits<{
  (e: 'done'): void
}>()

const percent = computed(() => {
  if (props.indeterminate) return 0
  if (props.max <= 0) return 0
  return Math.min(100, Math.max(0, (props.value / props.max) * 100))
})

const classes = computed(() => [
  'progress',
  {
    'progress--sm': props.size === 'sm',
    'progress--lg': props.size === 'lg',
    'is-indeterminate': props.indeterminate,
    'is-error': props.tone === 'error',
    'is-ok': props.tone === 'ok',
  },
])

const barStyle = computed(() =>
  props.indeterminate ? undefined : { width: `${percent.value}%` },
)

// 进度满格时通知一次（父层可据此切换按钮文案）
watch(percent, (p) => {
  if (p >= 100) emit('done')
})
</script>

<template>
  <div class="row row--tight" style="width: 100%">
    <div
      :class="classes"
      role="progressbar"
      :aria-label="ariaLabel"
      :aria-valuemin="0"
      :aria-valuemax="max"
      :aria-valuenow="indeterminate ? undefined : Math.round(value)"
      :aria-valuetext="indeterminate ? '进行中' : `${Math.round(percent)}%`"
    >
      <div class="progress__bar" :style="barStyle" />
    </div>
    <span v-if="showValue && !indeterminate" class="hint mono">{{ Math.round(percent) }}%</span>
  </div>
</template>
