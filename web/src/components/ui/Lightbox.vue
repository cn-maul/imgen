<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Spinner from './Spinner.vue'
import { useFocusTrap } from './useFocusTrap'

const props = withDefaults(
  defineProps<{
    open: boolean
    /** 当前图片地址（可为 blob: / /api/v1/assets/:id/raw）。 */
    src: string
    alt?: string
    caption?: string
    /** 多图浏览时的序号与总数；都传才显示计数与左右切换。 */
    index?: number
    total?: number
    /** 图片解码中是否显示 spinner，由父层传入。 */
    loading?: boolean
    ariaLabel?: string
  }>(),
  {
    alt: '',
    caption: undefined,
    index: undefined,
    total: undefined,
    loading: false,
    ariaLabel: undefined,
  },
)

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'update:open', value: boolean): void
  (e: 'prev'): void
  (e: 'next'): void
}>()

const root = ref<HTMLElement | null>(null)
const openRef = computed(() => props.open)
const errored = ref(false)

const hasNav = computed(
  () => props.index !== undefined && props.total !== undefined && props.total > 1,
)

function requestClose(): void {
  emit('update:open', false)
  emit('close')
}

useFocusTrap(root, openRef, {
  autoFocus: true,
  restoreFocus: true,
  onEscape: requestClose,
})

watch(
  () => props.src,
  () => {
    errored.value = false
  },
)

watch(
  () => props.open,
  (isOpen) => {
    if (isOpen) errored.value = false
  },
)

function onKeydown(ev: KeyboardEvent): void {
  if (!hasNav.value) return
  if (ev.key === 'ArrowLeft') {
    ev.preventDefault()
    emit('prev')
  } else if (ev.key === 'ArrowRight') {
    ev.preventDefault()
    emit('next')
  }
}
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open"
      class="overlay overlay--lightbox"
      @click="$event.target === $event.currentTarget && requestClose()"
    >
      <div
        ref="root"
        class="lightbox"
        role="dialog"
        aria-modal="true"
        :aria-label="ariaLabel ?? caption ?? alt ?? '图片预览'"
        tabindex="-1"
        @keydown="onKeydown"
      >
        <div class="lightbox__stage">
          <img
            v-if="!errored"
            class="lightbox__img"
            :class="{ 'is-loading': loading }"
            :src="src"
            :alt="alt"
            @error="errored = true"
          />
          <div v-else class="empty empty--compact">
            <div class="empty__icon" aria-hidden="true">
              <svg viewBox="0 0 24 24" width="24" height="24">
                <path
                  d="M3 5h18v14H3z"
                  stroke="currentColor" stroke-width="1.6" fill="none"
                />
                <path d="M4 16l5-5 4 4 3-3 4 4" stroke="currentColor" stroke-width="1.6" fill="none" />
              </svg>
            </div>
            <p class="empty__title">图片加载失败</p>
            <p class="empty__desc">链接可能已过期，请重新生成或从资产库选择。</p>
          </div>

          <div v-if="loading" class="lightbox__spinner">
            <Spinner size="xl" tone="muted" />
          </div>

          <button
            v-if="hasNav"
            type="button"
            class="lightbox__nav lightbox__nav--prev"
            aria-label="上一张"
            :disabled="index === 0"
            @click="emit('prev')"
          >
            <svg viewBox="0 0 16 16" aria-hidden="true">
              <path
                d="M10 3L5 8l5 5"
                stroke="currentColor" stroke-width="1.8"
                fill="none" stroke-linecap="round" stroke-linejoin="round"
              />
            </svg>
          </button>
          <button
            v-if="hasNav"
            type="button"
            class="lightbox__nav lightbox__nav--next"
            aria-label="下一张"
            :disabled="total !== undefined && index === total - 1"
            @click="emit('next')"
          >
            <svg viewBox="0 0 16 16" aria-hidden="true">
              <path
                d="M6 3l5 5-5 5"
                stroke="currentColor" stroke-width="1.8"
                fill="none" stroke-linecap="round" stroke-linejoin="round"
              />
            </svg>
          </button>
        </div>

        <div class="lightbox__bar">
          <span class="lightbox__caption">{{ caption ?? alt }}</span>
          <div class="row row--tight">
            <span v-if="hasNav" class="lightbox__counter">
              {{ (index ?? 0) + 1 }} / {{ total }}
            </span>
            <button
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
        </div>
      </div>
    </div>
  </Teleport>
</template>
