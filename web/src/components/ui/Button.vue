<script setup lang="ts">
import { computed } from 'vue'
import Spinner from './Spinner.vue'
import type { ButtonSize, ButtonVariant } from './types'

const props = withDefaults(
  defineProps<{
    /** 语义变体，决定配色。 */
    variant?: ButtonVariant
    size?: ButtonSize
    /** 原生类型；表单内提交用 'submit'。 */
    type?: 'button' | 'submit' | 'reset'
    /** 加载态：显示 spinner 并阻止点击，宽度不变。 */
    loading?: boolean
    disabled?: boolean
    /** 错误态：边框/文字转 danger。 */
    error?: boolean
    /** 只放图标时保持正方形。 */
    icon?: boolean
    /** 块级（占满父容器宽度）。 */
    block?: boolean
    /** 加载时的无障碍文案。 */
    loadingLabel?: string
    /** 原生 title，同时作为无文本时的无障碍名称。 */
    title?: string
    ariaLabel?: string
  }>(),
  {
    variant: 'secondary',
    size: 'md',
    type: 'button',
    loading: false,
    disabled: false,
    error: false,
    icon: false,
    block: false,
    loadingLabel: '处理中',
    title: undefined,
    ariaLabel: undefined,
  },
)

const emit = defineEmits<{
  (e: 'click', ev: MouseEvent): void
}>()

/** disabled 或 loading 时都不响应点击。 */
const isDisabled = computed(() => props.disabled || props.loading)

const classes = computed(() => [
  'btn',
  `btn--${props.variant}`,
  `btn--${props.size}`,
  {
    'btn--icon': props.icon,
    'btn--block': props.block,
    'is-loading': props.loading,
    'is-disabled': props.disabled,
    'is-error': props.error,
  },
])

function onClick(ev: MouseEvent): void {
  if (isDisabled.value) {
    ev.preventDefault()
    ev.stopPropagation()
    return
  }
  emit('click', ev)
}
</script>

<template>
  <button
    :class="classes"
    :type="type"
    :disabled="isDisabled"
    :title="title"
    :aria-label="ariaLabel ?? (icon ? title : undefined)"
    :aria-busy="loading || undefined"
    :aria-disabled="isDisabled || undefined"
    @click="onClick"
  >
    <span v-if="loading" class="btn__spinner" aria-hidden="true">
      <Spinner size="sm" />
    </span>
    <span v-if="$slots.icon && !loading" class="btn__icon">
      <slot name="icon" />
    </span>
    <span v-if="$slots.default || loading" class="btn__label">
      <slot>{{ loading ? loadingLabel : '' }}</slot>
    </span>
  </button>
</template>
