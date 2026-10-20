<script setup lang="ts">
import { computed, ref } from 'vue'
import Spinner from './Spinner.vue'
import type { ErrorLike } from './types'

const props = withDefaults(
  defineProps<{
    disabled?: boolean
    loading?: boolean
    error?: ErrorLike
    hint?: string
    /** input 的 accept，默认只收图片。 */
    accept?: string
    /** 是否允许多选。 */
    multiple?: boolean
    /** 是否响应窗口级粘贴（同时只允许一个 Dropzone 开启）。 */
    paste?: boolean
    ariaLabel?: string
  }>(),
  {
    disabled: false,
    loading: false,
    error: false,
    hint: undefined,
    accept: 'image/*',
    multiple: true,
    paste: true,
    ariaLabel: undefined,
  },
)

const emit = defineEmits<{
  /** 选中/拖入/粘贴的文件。 */
  (e: 'files', files: File[]): void
  /** 拖拽或粘贴里的纯文本 URL（粘贴图片链接时用）。 */
  (e: 'urls', urls: string[]): void
  /** 被拒绝的文件（类型不符等），交给父层提示。 */
  (e: 'reject', reason: string): void
}>()

const inputEl = ref<HTMLInputElement | null>(null)
const over = ref(false)
const dragDepth = ref(0)

const isDisabled = computed(() => props.disabled || props.loading)
const hasError = computed(() => props.error !== false && props.error !== undefined)
const errorMessage = computed(() => (typeof props.error === 'string' ? props.error : ''))

const classes = computed(() => [
  'dropzone',
  {
    'is-over': over.value,
    'is-disabled': props.disabled,
    'is-loading': props.loading,
    'is-error': hasError.value,
  },
])

function emitFiles(list: FileList | File[]): void {
  const files = Array.from(list)
  if (files.length === 0) return
  emit('files', files)
}

function onPick(): void {
  if (isDisabled.value) return
  inputEl.value?.click()
}

function onChange(ev: Event): void {
  const el = ev.target as HTMLInputElement
  if (el.files) emitFiles(el.files)
  // 允许重复选同一文件
  el.value = ''
}

function onDragEnter(ev: DragEvent): void {
  if (isDisabled.value) return
  dragDepth.value++
  over.value = true
  ev.preventDefault()
}

function onDragOver(ev: DragEvent): void {
  if (isDisabled.value) return
  ev.preventDefault()
  if (ev.dataTransfer) ev.dataTransfer.dropEffect = 'copy'
}

function onDragLeave(ev: DragEvent): void {
  if (isDisabled.value) return
  dragDepth.value = Math.max(0, dragDepth.value - 1)
  if (dragDepth.value === 0) over.value = false
  ev.preventDefault()
}

function onDrop(ev: DragEvent): void {
  if (isDisabled.value) return
  dragDepth.value = 0
  over.value = false
  ev.preventDefault()

  const dt = ev.dataTransfer
  if (!dt) return

  if (dt.files && dt.files.length > 0) {
    emitFiles(dt.files)
    return
  }

  const text = dt.getData('text/plain')
  const urls = extractUrls(text)
  if (urls.length > 0) emit('urls', urls)
  else if (text) emit('reject', '拖入的内容不是图片或图片链接')
}

function extractUrls(text: string): string[] {
  if (!text) return []
  const found = text.match(/https?:\/\/\S+/g) ?? []
  return found.map((u) => u.replace(/[),;]+$/, ''))
}

function onPaste(ev: ClipboardEvent): void {
  if (!props.paste || isDisabled.value) return
  const items = ev.clipboardData?.items
  if (!items) return

  const files: File[] = []
  for (const item of Array.from(items)) {
    if (item.kind !== 'file') continue
    const file = item.getAsFile()
    if (file) files.push(file)
  }
  if (files.length > 0) {
    emit('files', files)
    return
  }

  const text = ev.clipboardData?.getData('text/plain') ?? ''
  const urls = extractUrls(text)
  if (urls.length > 0) emit('urls', urls)
}

function onKeydown(ev: KeyboardEvent): void {
  if (isDisabled.value) return
  if (ev.key === 'Enter' || ev.key === ' ') {
    ev.preventDefault()
    onPick()
  }
}
</script>

<template>
  <div @paste="onPaste">
    <div
      :class="classes"
      role="button"
      tabindex="0"
      :aria-disabled="isDisabled || undefined"
      :aria-label="ariaLabel ?? '上传图片'"
      :aria-busy="loading || undefined"
      @click="onPick"
      @keydown="onKeydown"
      @dragenter="onDragEnter"
      @dragover="onDragOver"
      @dragleave="onDragLeave"
      @drop="onDrop"
    >
      <span v-if="loading" class="dropzone__icon" aria-hidden="true">
        <Spinner size="lg" tone="muted" />
      </span>
      <span v-else class="dropzone__icon" aria-hidden="true">
        <svg viewBox="0 0 24 24" width="28" height="28">
          <path
            d="M12 16V6m0 0L8 10m4-4l4 4"
            stroke="currentColor" stroke-width="1.7"
            fill="none" stroke-linecap="round" stroke-linejoin="round"
          />
          <path
            d="M4 16v2a2 2 0 002 2h12a2 2 0 002-2v-2"
            stroke="currentColor" stroke-width="1.7"
            fill="none" stroke-linecap="round"
          />
        </svg>
      </span>

      <slot>
        <span class="dropzone__title">拖拽图片到此处，或点击选择</span>
        <span v-if="hint" class="dropzone__hint">{{ hint }}</span>
      </slot>

      <input
        ref="inputEl"
        class="dropzone__native"
        type="file"
        :accept="accept"
        :multiple="multiple"
        :disabled="isDisabled"
        tabindex="-1"
        @change="onChange"
      />
    </div>
    <p v-if="errorMessage" class="error-text" role="alert">{{ errorMessage }}</p>
  </div>
</template>
