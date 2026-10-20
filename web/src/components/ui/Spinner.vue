<script setup lang="ts">
import { computed } from 'vue'
import type { SpinnerSize, SpinnerTone } from './types'

const props = withDefaults(
  defineProps<{
    size?: SpinnerSize
    /** 不传则继承父级 currentColor。 */
    tone?: SpinnerTone
    /** 无障碍名称；纯装饰传 null 之外的 label 即可。 */
    label?: string
  }>(),
  { size: 'md', tone: 'inherit', label: '加载中' },
)

const classes = computed(() => [
  'spinner',
  `spinner--${props.size}`,
  props.tone === 'inherit' ? '' : `spinner--${props.tone}`,
])
</script>

<template>
  <span :class="classes" role="status" :aria-label="label" />
</template>
