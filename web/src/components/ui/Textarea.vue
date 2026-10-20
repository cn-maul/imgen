<script setup lang="ts">
import { computed, ref } from 'vue'
import type { ErrorLike } from './types'

const props = withDefaults(
  defineProps<{
    modelValue: string
    placeholder?: string
    rows?: number
    maxlength?: number
    disabled?: boolean
    readonly?: boolean
    loading?: boolean
    error?: ErrorLike
    /** 等宽字体（提示词、JSON 用）。 */
    mono?: boolean
    /** 右下角显示字数。 */
    count?: boolean
    /** 高度随内容自动增长（忽略 rows）。 */
    autosize?: boolean
    minRows?: number
    maxRows?: number
    id?: string
    ariaLabel?: string
  }>(),
  {
    placeholder: undefined,
    rows: 4,
    maxlength: undefined,
    disabled: false,
    readonly: false,
    loading: false,
    error: false,
    mono: false,
    count: false,
    autosize: false,
    minRows: 2,
    maxRows: 16,
    id: undefined,
    ariaLabel: undefined,
  },
)

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
  (e: 'change', value: string): void
  (e: 'blur', ev: FocusEvent): void
  (e: 'focus', ev: FocusEvent): void
  /** Ctrl/Cmd + Enter 提交。 */
  (e: 'submit', value: string): void
}>()

const el = ref<HTMLTextAreaElement | null>(null)

const hasError = computed(() => props.error !== false && props.error !== undefined)
const errorMessage = computed(() => (typeof props.error === 'string' ? props.error : ''))
const isDisabled = computed(() => props.disabled || props.loading)
const length = computed(() => props.modelValue?.length ?? 0)
const over = computed(() => props.maxlength !== undefined && length.value > props.maxlength)

const wrapClass = computed(() => [
  'textarea__wrap',
  {
    'is-error': hasError.value,
    'is-disabled': props.disabled,
    'is-readonly': props.readonly,
    'is-loading': props.loading,
  },
])

const style = computed(() => {
  if (props.autosize) return undefined
  return { minHeight: `${props.rows * 1.55}em` }
})

function resize(): void {
  const node = el.value
  if (!node || !props.autosize) return
  node.style.height = 'auto'
  const lineHeight = parseFloat(getComputedStyle(node).lineHeight) || 20
  const pad = parseFloat(getComputedStyle(node).paddingTop) * 2 || 0
  const min = props.minRows * lineHeight
  const max = props.maxRows * lineHeight
  const next = Math.min(Math.max(node.scrollHeight - pad, min), max)
  node.style.height = `${next}px`
}

function onInput(ev: Event): void {
  const node = ev.target as HTMLTextAreaElement
  emit('update:modelValue', node.value)
  emit('change', node.value)
  resize()
}

function onKeydown(ev: KeyboardEvent): void {
  if (ev.key === 'Enter' && (ev.ctrlKey || ev.metaKey)) {
    ev.preventDefault()
    emit('submit', props.modelValue)
  }
}
</script>

<template>
  <div class="textarea-root">
    <div :class="wrapClass">
      <textarea
        :id="id"
        ref="el"
        class="textarea"
        :style="style"
        :value="modelValue"
        :placeholder="placeholder"
        :rows="autosize ? minRows : rows"
        :maxlength="maxlength"
        :disabled="isDisabled"
        :readonly="readonly"
        :aria-label="ariaLabel"
        :aria-invalid="hasError || undefined"
        @input="onInput"
        @keydown="onKeydown"
        @focus="emit('focus', $event)"
        @blur="emit('blur', $event)"
      />
      <div v-if="count || $slots.foot" class="textarea__foot">
        <slot name="foot" />
        <span
          v-if="count"
          class="textarea__count"
          :class="{ 'is-over': over }"
        >{{ length }}<template v-if="maxlength"> / {{ maxlength }}</template></span>
      </div>
    </div>
    <p v-if="errorMessage" class="error-text" role="alert">{{ errorMessage }}</p>
  </div>
</template>
