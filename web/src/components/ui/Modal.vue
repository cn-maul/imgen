<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useFocusTrap } from './useFocusTrap'
import type { ModalSize } from './types'

const props = withDefaults(
  defineProps<{
    open: boolean
    title?: string
    subtitle?: string
    size?: ModalSize
    /** 加载态：body 变灰且不可交互。 */
    loading?: boolean
    /** 是否显示右上角关闭按钮。 */
    closable?: boolean
    /** 点击遮罩是否关闭。 */
    closeOnOverlay?: boolean
    /** Esc 是否关闭（焦点陷阱已处理 Esc，这里只做开关）。 */
    closeOnEsc?: boolean
    /** 隐藏底部栏（自定义 footer 用 #foot slot 即可）。 */
    hideFoot?: boolean
    ariaLabel?: string
  }>(),
  {
    title: undefined,
    subtitle: undefined,
    size: 'md',
    loading: false,
    closable: true,
    closeOnOverlay: true,
    closeOnEsc: true,
    hideFoot: false,
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

function onOverlayClick(ev: MouseEvent): void {
  if (!props.closeOnOverlay) return
  if (ev.target === ev.currentTarget) requestClose()
}

const panelClass = computed(() => [
  'modal',
  `modal--${props.size}`,
  { 'is-loading': props.loading },
])
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open"
      class="overlay overlay--modal"
      @click="onOverlayClick"
    >
      <div
        ref="panel"
        :class="panelClass"
        role="dialog"
        aria-modal="true"
        :aria-label="ariaLabel ?? title"
        :aria-busy="loading || undefined"
      >
        <div v-if="title || subtitle || closable" class="modal__head">
          <div class="modal__title">
            <slot name="title">
              <span>{{ title }}</span>
              <span v-if="subtitle" class="modal__subtitle">{{ subtitle }}</span>
            </slot>
          </div>
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

        <div class="modal__body" :class="{ 'modal__body--flush': $slots.flush }">
          <slot />
        </div>

        <div v-if="!hideFoot && $slots.foot" class="modal__foot">
          <slot name="foot" :close="requestClose" />
        </div>
      </div>
    </div>
  </Teleport>
</template>
