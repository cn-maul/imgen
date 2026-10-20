<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'

const props = withDefaults(
  defineProps<{
    /** 气泡内容；与 #content slot 二选一，slot 优先。 */
    text?: string
    placement?: 'top' | 'bottom' | 'left' | 'right'
    /** 悬停到显示之间的延迟毫秒。 */
    delay?: number
    disabled?: boolean
    /** 气泡最大宽度，覆盖默认 260px。 */
    maxWidth?: string
  }>(),
  { text: undefined, placement: 'top', delay: 120, disabled: false, maxWidth: undefined },
)

const triggerEl = ref<HTMLElement | null>(null)
const visible = ref(false)
const coords = ref({ x: 0, y: 0 })
let timer: ReturnType<typeof setTimeout> | undefined

const GAP = 8

function measure(): void {
  const el = triggerEl.value
  if (!el) return
  const r = el.getBoundingClientRect()
  switch (props.placement) {
    case 'top':
      coords.value = { x: r.left + r.width / 2, y: r.top - GAP }
      break
    case 'bottom':
      coords.value = { x: r.left + r.width / 2, y: r.bottom + GAP }
      break
    case 'left':
      coords.value = { x: r.left - GAP, y: r.top + r.height / 2 }
      break
    case 'right':
      coords.value = { x: r.right + GAP, y: r.top + r.height / 2 }
      break
  }
}

function show(): void {
  if (props.disabled) return
  clearTimeout(timer)
  timer = setTimeout(() => {
    measure()
    visible.value = true
  }, props.delay)
}

function hide(): void {
  clearTimeout(timer)
  visible.value = false
}

function onFocus(): void {
  measure()
  visible.value = true
}

function onKeydown(ev: KeyboardEvent): void {
  if (ev.key === 'Escape') hide()
}

function reposition(): void {
  if (visible.value) measure()
}

watch(() => props.placement, reposition)

window.addEventListener('scroll', reposition, true)
window.addEventListener('resize', reposition)

onBeforeUnmount(() => {
  clearTimeout(timer)
  window.removeEventListener('scroll', reposition, true)
  window.removeEventListener('resize', reposition)
})

const bubbleStyle = computed(() => ({
  left: `${coords.value.x}px`,
  top: `${coords.value.y}px`,
  maxWidth: props.maxWidth,
}))
</script>

<template>
  <span
    ref="triggerEl"
    class="tooltip"
    @mouseenter="show"
    @mouseleave="hide"
    @focusin="onFocus"
    @focusout="hide"
    @keydown="onKeydown"
  >
    <slot />
    <Teleport to="body">
      <div
        v-if="visible && (text || $slots.content)"
        class="tooltip__bubble"
        :class="`tooltip__bubble--${placement}`"
        :style="bubbleStyle"
        role="tooltip"
      >
        <slot name="content">{{ text }}</slot>
        <span class="tooltip__arrow" aria-hidden="true" />
      </div>
    </Teleport>
  </span>
</template>
