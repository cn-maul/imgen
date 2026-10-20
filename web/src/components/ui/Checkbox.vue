<script setup lang="ts">
import { computed } from 'vue'
import type { ErrorLike } from './types'

const props = withDefaults(
  defineProps<{
    modelValue: boolean
    label?: string
    desc?: string
    disabled?: boolean
    loading?: boolean
    error?: ErrorLike
    /** 半选态（父级全选场景）。 */
    indeterminate?: boolean
    /** 原生 name，用于 form 提交。 */
    name?: string
    value?: string
    id?: string
  }>(),
  {
    label: undefined,
    desc: undefined,
    disabled: false,
    loading: false,
    error: false,
    indeterminate: false,
    name: undefined,
    value: undefined,
    id: undefined,
  },
)

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
  (e: 'change', value: boolean): void
}>()

const isDisabled = computed(() => props.disabled || props.loading)
const hasError = computed(() => props.error !== false && props.error !== undefined)
const errorMessage = computed(() => (typeof props.error === 'string' ? props.error : ''))
const checked = computed(() => props.modelValue === true)

const rootClass = computed(() => [
  'checkbox',
  {
    'is-checked': checked.value && !props.indeterminate,
    'is-indeterminate': props.indeterminate,
    'is-disabled': props.disabled,
    'is-loading': props.loading,
    'is-error': hasError.value,
  },
])

function toggle(): void {
  if (isDisabled.value) return
  const next = !checked.value
  emit('update:modelValue', next)
  emit('change', next)
}
</script>

<template>
  <div>
    <label :class="rootClass">
      <input
        :id="id"
        class="checkbox__native"
        type="checkbox"
        :checked="checked"
        :indeterminate.prop="indeterminate"
        :disabled="isDisabled"
        :name="name"
        :value="value"
        :aria-checked="indeterminate ? 'mixed' : checked"
        :aria-invalid="hasError || undefined"
        @change="toggle"
      />
      <span class="checkbox__box" aria-hidden="true">
        <svg v-if="indeterminate" viewBox="0 0 16 16">
          <path d="M4 8h8" stroke="currentColor" stroke-width="2" stroke-linecap="round" fill="none" />
        </svg>
        <svg v-else viewBox="0 0 16 16">
          <path
            d="M3.5 8.5l3 3 6-6.5"
            stroke="currentColor" stroke-width="2"
            fill="none" stroke-linecap="round" stroke-linejoin="round"
          />
        </svg>
      </span>
      <span v-if="label || desc || $slots.default" class="checkbox__content">
        <span class="checkbox__label">
          <slot>{{ label }}</slot>
        </span>
        <span v-if="desc" class="checkbox__desc">{{ desc }}</span>
      </span>
    </label>
    <p v-if="errorMessage" class="error-text" role="alert">{{ errorMessage }}</p>
  </div>
</template>
