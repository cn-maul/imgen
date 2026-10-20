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
    name?: string
    id?: string
  }>(),
  {
    label: undefined,
    desc: undefined,
    disabled: false,
    loading: false,
    error: false,
    name: undefined,
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
  'switch',
  {
    'is-checked': checked.value,
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
        class="switch__native"
        type="checkbox"
        role="switch"
        :checked="checked"
        :disabled="isDisabled"
        :name="name"
        :aria-checked="checked"
        :aria-invalid="hasError || undefined"
        @change="toggle"
      />
      <span class="switch__track" aria-hidden="true">
        <span class="switch__thumb" />
      </span>
      <span v-if="label || desc || $slots.default" class="checkbox__content">
        <span class="checkbox__label"><slot>{{ label }}</slot></span>
        <span v-if="desc" class="checkbox__desc">{{ desc }}</span>
      </span>
    </label>
    <p v-if="errorMessage" class="error-text" role="alert">{{ errorMessage }}</p>
  </div>
</template>
