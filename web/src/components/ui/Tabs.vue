<script setup lang="ts">
import { computed, ref } from 'vue'
import type { TabItem, TabsVariant } from './types'

const props = withDefaults(
  defineProps<{
    modelValue: string
    tabs: TabItem[]
    variant?: TabsVariant
    /** 禁用整个 tab 条。 */
    disabled?: boolean
    ariaLabel?: string
  }>(),
  { variant: 'line', disabled: false, ariaLabel: undefined },
)

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
  (e: 'change', value: string): void
}>()

const listEl = ref<HTMLElement | null>(null)
const currentIndex = computed(() =>
  Math.max(
    0,
    props.tabs.findIndex((t) => t.value === props.modelValue),
  ),
)

const listClass = computed(() => [
  'tabs__list',
  {
    'tabs__list--pills': props.variant === 'pills',
    'tabs__list--segmented': props.variant === 'segmented',
  },
])

function select(tab: TabItem): void {
  if (tab.disabled || props.disabled) return
  emit('update:modelValue', tab.value)
  emit('change', tab.value)
}

function move(delta: number): void {
  if (props.tabs.length === 0 || props.disabled) return
  let i = currentIndex.value
  for (let step = 0; step < props.tabs.length; step++) {
    i = (i + delta + props.tabs.length) % props.tabs.length
    if (!props.tabs[i]?.disabled) {
      const tab = props.tabs[i]!
      select(tab)
      const node = listEl.value?.children[i] as HTMLElement | undefined
      node?.focus({ preventScroll: true })
      return
    }
  }
}

function onKeydown(ev: KeyboardEvent): void {
  switch (ev.key) {
    case 'ArrowRight':
      ev.preventDefault()
      move(1)
      break
    case 'ArrowLeft':
      ev.preventDefault()
      move(-1)
      break
    case 'Home':
      ev.preventDefault()
      select(props.tabs[0] ?? { value: props.modelValue, label: '' })
      break
    case 'End': {
      ev.preventDefault()
      const last = props.tabs[props.tabs.length - 1]
      if (last) select(last)
      break
    }
  }
}
</script>

<template>
  <div class="tabs">
    <div
      ref="listEl"
      :class="listClass"
      role="tablist"
      :aria-label="ariaLabel"
      @keydown="onKeydown"
    >
      <button
        v-for="tab in tabs"
        :key="tab.value"
        type="button"
        class="tabs__tab"
        :class="{
          'is-active': tab.value === modelValue,
          'is-disabled': tab.disabled || disabled,
        }"
        role="tab"
        :aria-selected="tab.value === modelValue"
        :tabindex="tab.value === modelValue ? 0 : -1"
        :disabled="tab.disabled || disabled"
        @click="select(tab)"
      >
        <slot name="tab" :tab="tab">
          <span>{{ tab.label }}</span>
        </slot>
        <span v-if="tab.badge !== undefined" class="tabs__tab-badge">{{ tab.badge }}</span>
      </button>
    </div>

    <div class="tabs__panel" role="tabpanel" :aria-label="ariaLabel">
      <slot :active="modelValue" />
    </div>
  </div>
</template>
