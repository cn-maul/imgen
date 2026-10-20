<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import type { ControlSize, ErrorLike, SelectOption } from './types'

const props = withDefaults(
  defineProps<{
    modelValue: string
    options: SelectOption[]
    placeholder?: string
    size?: ControlSize
    disabled?: boolean
    loading?: boolean
    error?: ErrorLike
    /** 允许清空（选中 placeholder 项）。 */
    clearable?: boolean
    /** 列表向上展开（空间不足时）。 */
    placement?: 'bottom' | 'top'
    id?: string
    ariaLabel?: string
  }>(),
  {
    placeholder: '请选择',
    size: 'md',
    disabled: false,
    loading: false,
    error: false,
    clearable: false,
    placement: 'bottom',
    id: undefined,
    ariaLabel: undefined,
  },
)

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
  (e: 'change', value: string): void
}>()

const open = ref(false)
const activeIndex = ref(-1)
const triggerEl = ref<HTMLElement | null>(null)
const listEl = ref<HTMLElement | null>(null)

const isDisabled = computed(() => props.disabled || props.loading)
const hasError = computed(() => props.error !== false && props.error !== undefined)
const errorMessage = computed(() => (typeof props.error === 'string' ? props.error : ''))

const selected = computed(() => props.options.find((o) => o.value === props.modelValue))
const isPlaceholder = computed(() => !selected.value)
const displayLabel = computed(() => selected.value?.label ?? props.placeholder)

const rootClass = computed(() => [
  'select__wrap',
  {
    'is-open': open.value,
    'is-error': hasError.value,
    'is-disabled': props.disabled,
    'is-loading': props.loading,
  },
])

function firstEnabledIndex(): number {
  const i = props.options.findIndex((o) => !o.disabled)
  return i
}

function toggle(): void {
  if (isDisabled.value) return
  open.value ? close() : show()
}

function show(): void {
  if (isDisabled.value) return
  open.value = true
  activeIndex.value = selected.value
    ? props.options.indexOf(selected.value)
    : firstEnabledIndex()
  nextTick(() => {
    listEl.value?.focus({ preventScroll: true })
  })
}

function close(): void {
  open.value = false
  activeIndex.value = -1
}

function pick(option: SelectOption): void {
  if (option.disabled) return
  emit('update:modelValue', option.value)
  emit('change', option.value)
  close()
  triggerEl.value?.focus({ preventScroll: true })
}

function clear(): void {
  emit('update:modelValue', '')
  emit('change', '')
  close()
}

function move(delta: number): void {
  if (props.options.length === 0) return
  let i = activeIndex.value
  for (let step = 0; step < props.options.length; step++) {
    i = (i + delta + props.options.length) % props.options.length
    if (!props.options[i]?.disabled) break
  }
  activeIndex.value = i
  nextTick(() => {
    const node = listEl.value?.children[i] as HTMLElement | undefined
    node?.scrollIntoView({ block: 'nearest' })
  })
}

function onKeydown(ev: KeyboardEvent): void {
  switch (ev.key) {
    case 'ArrowDown':
      ev.preventDefault()
      open.value ? move(1) : show()
      break
    case 'ArrowUp':
      ev.preventDefault()
      open.value ? move(-1) : show()
      break
    case 'Home':
      if (open.value) {
        ev.preventDefault()
        activeIndex.value = firstEnabledIndex()
      }
      break
    case 'End':
      if (open.value) {
        ev.preventDefault()
        for (let i = props.options.length - 1; i >= 0; i--) {
          if (!props.options[i]?.disabled) {
            activeIndex.value = i
            break
          }
        }
      }
      break
    case 'Enter':
    case ' ':
      ev.preventDefault()
      if (!open.value) {
        show()
      } else if (activeIndex.value >= 0) {
        const option = props.options[activeIndex.value]
        if (option) pick(option)
      }
      break
    case 'Tab':
    case 'Escape':
      close()
      break
  }
}

function onBlur(ev: FocusEvent): void {
  const related = ev.relatedTarget as Node | null
  if (related && (triggerEl.value?.contains(related) || listEl.value?.contains(related))) return
  close()
}

// 选项变化后若当前值已不在列表中，置空，避免 UI 与实际值不符
watch(
  () => props.options,
  (opts) => {
    if (props.modelValue && !opts.some((o) => o.value === props.modelValue)) {
      emit('update:modelValue', '')
    }
  },
)
</script>

<template>
  <div class="select">
    <div :class="rootClass" @keydown="onKeydown" @focusout="onBlur">
      <button
        :id="id"
        ref="triggerEl"
        type="button"
        class="select__trigger"
        :class="[
          `select__trigger--${size}`,
          { 'is-disabled': isDisabled, 'is-placeholder': isPlaceholder },
        ]"
        role="combobox"
        :aria-expanded="open"
        :aria-controls="`${id ?? 'select'}-listbox`"
        :disabled="isDisabled"
        :aria-label="ariaLabel"
        :aria-invalid="hasError || undefined"
        @click="toggle"
      >
        <span class="select__value" :class="{ 'is-placeholder': isPlaceholder }">
          <slot name="value" :option="selected">{{ displayLabel }}</slot>
        </span>
        <span v-if="loading" class="select__arrow" aria-hidden="true">
          <svg viewBox="0 0 16 16" width="12" height="12">
            <circle
              cx="8" cy="8" r="6"
              stroke="currentColor" stroke-width="2" fill="none"
              stroke-dasharray="28" stroke-dashoffset="8"
            />
          </svg>
        </span>
        <span v-else class="select__arrow" aria-hidden="true">
          <svg viewBox="0 0 16 16" width="12" height="12">
            <path
              d="M4 6l4 4 4-4"
              stroke="currentColor" stroke-width="1.6"
              fill="none" stroke-linecap="round" stroke-linejoin="round"
            />
          </svg>
        </span>
      </button>

      <ul
        v-if="open"
        ref="listEl"
        :id="`${id ?? 'select'}-listbox`"
        class="select__listbox"
        :class="{ 'select__listbox--up': placement === 'top' }"
        role="listbox"
        tabindex="-1"
        :aria-label="ariaLabel ?? '选项'"
        :aria-activedescendant="
          activeIndex >= 0 ? `${id ?? 'select'}-opt-${activeIndex}` : undefined
        "
      >
        <li v-if="clearable" role="none">
          <button type="button" class="select__option" @click="clear">
            <span class="select__option-label">{{ placeholder }}</span>
          </button>
        </li>
        <li v-if="options.length === 0" role="none">
          <div class="select__empty">暂无选项</div>
        </li>
        <li
          v-for="(option, i) in options"
          :id="`${id ?? 'select'}-opt-${i}`"
          :key="option.value"
          role="none"
        >
          <button
            type="button"
            class="select__option"
            :class="{
              'is-selected': option.value === modelValue,
              'is-active': i === activeIndex,
              'is-disabled': option.disabled,
            }"
            :disabled="option.disabled"
            role="option"
            :aria-selected="option.value === modelValue"
            @click="pick(option)"
            @mouseenter="activeIndex = i"
          >
            <span class="select__option-label">{{ option.label }}</span>
            <span v-if="option.hint" class="hint">{{ option.hint }}</span>
            <span v-if="option.value === modelValue" class="select__check" aria-hidden="true">
              <svg viewBox="0 0 16 16" width="12" height="12">
                <path
                  d="M3 8.5l3.5 3.5L13 5"
                  stroke="currentColor" stroke-width="2"
                  fill="none" stroke-linecap="round" stroke-linejoin="round"
                />
              </svg>
            </span>
          </button>
        </li>
      </ul>
    </div>
    <p v-if="errorMessage" class="error-text" role="alert">{{ errorMessage }}</p>
  </div>
</template>
