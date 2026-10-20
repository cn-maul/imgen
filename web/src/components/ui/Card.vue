<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    /** 非交互卡片保持 div；clickable 时渲染成 button 以获得键盘可达性。 */
    clickable?: boolean
    /** hover 抬升阴影。 */
    hover?: boolean
    flat?: boolean
    selected?: boolean
    disabled?: boolean
    loading?: boolean
    error?: boolean
    title?: string
    subtitle?: string
    ariaLabel?: string
  }>(),
  {
    clickable: false,
    hover: false,
    flat: false,
    selected: false,
    disabled: false,
    loading: false,
    error: false,
    title: undefined,
    subtitle: undefined,
    ariaLabel: undefined,
  },
)

const emit = defineEmits<{
  (e: 'click', ev: MouseEvent): void
}>()

const classes = computed(() => [
  'card',
  {
    'card--flat': props.flat,
    'card--hover': props.hover,
    'card--clickable': props.clickable,
    'is-selected': props.selected,
    'is-disabled': props.disabled,
    'is-loading': props.loading,
    'is-error': props.error,
  },
])
</script>

<template>
  <component
    :is="clickable ? 'button' : 'div'"
    :class="classes"
    :type="clickable ? 'button' : undefined"
    :disabled="clickable ? disabled || loading : undefined"
    :aria-label="ariaLabel"
    :aria-busy="loading || undefined"
    :aria-pressed="clickable && selected ? selected : undefined"
    @click="emit('click', $event)"
  >
    <div v-if="title || subtitle || $slots.head" class="card__head">
      <slot name="head">
        <div class="card__title">
          <span>{{ title }}</span>
          <span v-if="subtitle" class="card__subtitle">{{ subtitle }}</span>
        </div>
      </slot>
      <slot name="head-extra" />
    </div>

    <div v-if="$slots.media" class="card__media">
      <slot name="media" />
    </div>

    <div class="card__body" :class="{ 'card__body--flush': $slots.flush }">
      <slot />
    </div>

    <div v-if="$slots.foot" class="card__foot">
      <slot name="foot" />
    </div>
  </component>
</template>
