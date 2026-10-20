<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useFocusTrap } from './useFocusTrap'
import type { DrawerSize } from './types'

const props = withDefaults(
  defineProps<{
    open: boolean
    title?: string
    /** 从左侧还是右侧滑出。 */
    side?: 'left' | 'right'
    size?: DrawerSize
    loading?: boolean
    closable?: boolean
    closeOnOverlay?: boolean
    closeOnEsc?: boolean
    ariaLabel?: string
  }>(),
  {
    title: undefined,
    side: 'right',
    size: 'md',
    loading: false,
    closable: true,
    closeOnOverlay: true,
    closeOnEsc: true,
    ariaLabel: undefined,
  },
)

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'update:open', value: boolean): void
  (e: 'opened'): void
  (e: 'closed'): void
}>()

const panel = ref<HTMLElement | null>(null)
const openRef = computed(() => props.open)

function requestClose(): void {
  if (props.loading) return
  emit('update:open', false)
  emit('close')
}

useFocusTrap(panel, openRef, {
  autoFocus: true,
  restoreFocus: true,
  onEscape: () => {
    if (props.closeOnEsc) requestClose()
  },
})

watch(
  () => props.open,
  (isOpen) => {
    if (isOpen) emit('opened')
    else emit('closed')
  },
)

const panelClass = computed(() => [
  'drawer',
  `drawer--${props.side}`,
  `drawer--${props.size}`,
  { 'is-loading': props.loading },
])
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open"
      class="overlay overlay--drawer"
      @click="closeOnOverlay && $event.target === $event.currentTarget && requestClose()"
    >
      <aside
        ref="panel"
        :class="panelClass"
        role="dialog"
        aria-modal="true"
        :aria-label="ariaLabel ?? title"
        :aria-busy="loading || undefined"
      >
        <div v-if="title || closable || $slots.head" class="drawer__head">
          <slot name="head">
            <h2 class="drawer__title">{{ title }}</h2>
          </slot>
          <button
            v-if="closable"
            type="button"
            class="modal__close"
            aria-label="关闭"
            @click="requestClose"
          >
            <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
              <path
                d="M4 4l8 8M12 4l-8 8"
                stroke="currentColor" stroke-width="1.8"
                stroke-linecap="round" fill="none"
              />
            </svg>
          </button>
        </div>

        <div class="drawer__body">
          <slot />
        </div>

        <div v-if="$slots.foot" class="drawer__foot">
          <slot name="foot" :close="requestClose" />
        </div>
      </aside>
    </div>
  </Teleport>
</template>
