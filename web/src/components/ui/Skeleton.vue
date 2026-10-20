<script setup lang="ts">
import { computed } from 'vue'
import type { SkeletonVariant } from './types'

const props = withDefaults(
  defineProps<{
    variant?: SkeletonVariant
    /** 任意 CSS 长度；不传时按 variant 取默认值。 */
    width?: string
    height?: string
    /** 圆角覆盖（circle 变体自动 --radius-full）。 */
    radius?: string
    /** text 变体生成几行占位文本（末行自动缩短）。 */
    lines?: number
    /** 是否播放扫光动画。 */
    active?: boolean
  }>(),
  { variant: 'rect', width: undefined, height: undefined, radius: undefined, lines: 1, active: true },
)

const defaultSize: Record<SkeletonVariant, { w: string; h: string }> = {
  text: { w: '100%', h: '1em' },
  rect: { w: '100%', h: '100%' },
  circle: { w: '2.5em', h: '2.5em' },
}

const style = computed(() => ({
  width: props.width ?? defaultSize[props.variant].w,
  height: props.height ?? defaultSize[props.variant].h,
  borderRadius: props.radius ?? (props.variant === 'circle' ? 'var(--radius-full)' : undefined),
}))

const classes = computed(() => [
  'skeleton',
  `skeleton--${props.variant}`,
  { 'is-inactive': !props.active },
])

const count = computed(() => (props.variant === 'text' ? Math.max(1, props.lines) : 1))
</script>

<template>
  <div v-if="count > 1" class="stack stack--2" :style="{ width: style.width }">
    <span
      v-for="i in count"
      :key="i"
      :class="classes"
      :style="{ ...style, width: i === count ? '60%' : '100%' }"
      aria-hidden="true"
    />
  </div>
  <span v-else :class="classes" :style="style" aria-hidden="true" />
</template>
