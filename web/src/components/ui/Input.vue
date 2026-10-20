<script setup lang="ts">
import { computed, ref } from 'vue'
import Spinner from './Spinner.vue'
import type { ControlSize, ErrorLike, InputType } from './types'

const props = withDefaults(
  defineProps<{
    modelValue: string | number
    type?: InputType
    placeholder?: string
    size?: ControlSize
    disabled?: boolean
    readonly?: boolean
    loading?: boolean
    /** true 或字符串；字符串会渲染成 .error-text 消息。 */
    error?: ErrorLike
    /** 等宽字体（Key、URL 用）。 */
    mono?: boolean
    /** 显示清除按钮。 */
    clearable?: boolean
    /** 表单自动补全提示。 */
    autocomplete?: string
    name?: string
    id?: string
    maxlength?: number
    min?: number | string
    max?: number | string
    step?: number | string
    ariaLabel?: string
  }>(),
  {
    type: 'text',
    placeholder: undefined,
    size: 'md',
    disabled: false,
    readonly: false,
    loading: false,
    error: false,
    mono: false,
    clearable: false,
    autocomplete: undefined,
    name: undefined,
    id: undefined,
    maxlength: undefined,
    min: undefined,
    max: undefined,
    step: undefined,
    ariaLabel: undefined,
  },
)

const emit = defineEmits<{
  (e: 'update:modelValue', value: string | number): void
  (e: 'change', value: string | number): void
  (e: 'blur', ev: FocusEvent): void
  (e: 'focus', ev: FocusEvent): void
  (e: 'enter', value: string | number): void
  (e: 'clear'): void
}>()

const focused = ref(false)

const hasError = computed(() => props.error !== false && props.error !== undefined)
const errorMessage = computed(() => (typeof props.error === 'string' ? props.error : ''))
const isDisabled = computed(() => props.disabled || props.loading)
const showClear = computed(
  () => props.clearable && !isDisabled.value && !props.readonly && String(props.modelValue ?? '') !== '',
)

const wrapClass = computed(() => [
  'input__wrap',
  {
    'is-error': hasError.value,
    'is-disabled': props.disabled,
    'is-readonly': props.readonly,
    'is-loading': props.loading,
    'is-focused': focused.value,
  },
])

const rootClass = computed(() => [
  'input',
  `input--${props.size}`,
  { 'input--mono': props.mono },
])

function onInput(ev: Event): void {
  const el = ev.target as HTMLInputElement
  if (props.type === 'number') {
    const n = el.value === '' ? '' : Number(el.value)
    emit('update:modelValue', n)
    emit('change', n)
    return
  }
  emit('update:modelValue', el.value)
  emit('change', el.value)
}

function clear(): void {
  emit('update:modelValue', '')
  emit('change', '')
  emit('clear')
}

function onKeydown(ev: KeyboardEvent): void {
  if (ev.key === 'Enter') {
    emit('enter', props.modelValue)
  }
}
</script>

<template>
  <div :class="rootClass">
    <div :class="wrapClass">
      <span v-if="$slots.prefix" class="input__prefix">
        <slot name="prefix" />
      </span>

      <input
        :id="id"
        class="input"
        :type="type"
        :value="modelValue"
        :placeholder="placeholder"
        :disabled="isDisabled"
        :readonly="readonly"
        :autocomplete="autocomplete"
        :name="name"
        :maxlength="maxlength"
        :min="min"
        :max="max"
        :step="step"
        :aria-label="ariaLabel"
        :aria-invalid="hasError || undefined"
        @input="onInput"
        @keydown="onKeydown"
        @focus="focused = true; emit('focus', $event)"
        @blur="focused = false; emit('blur', $event)"
      />

      <span v-if="loading" class="input__suffix" aria-hidden="true">
        <Spinner size="sm" tone="muted" />
      </span>

      <button
        v-if="showClear"
        type="button"
        class="input__clear"
        aria-label="清空"
        @click="clear"
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

      <span v-if="$slots.suffix" class="input__suffix">
        <slot name="suffix" />
      </span>
    </div>

    <p v-if="errorMessage" class="error-text" role="alert">{{ errorMessage }}</p>
  </div>
</template>
