<script setup lang="ts">
/**
 * 全局 Toast 渲染器。App.vue 里挂一次即可。
 * 只负责渲染队列与交互；队列本身在 useToast.ts（模块级单例）。
 */
import { computed } from 'vue'
import Toast from './Toast.vue'
import { useToast } from './useToast'

const props = withDefaults(
  defineProps<{
    position?: 'top-right' | 'top-center' | 'bottom-right' | 'bottom-center'
  }>(),
  { position: 'top-right' },
)

const toast = useToast()
const items = computed(() => toast.items)

function onAction(id: number, index: number): void {
  const item = items.value.find((t) => t.id === id)
  const action = item?.actions[index]
  if (!action) return
  const result = action.onClick()
  // 未显式返回 false 时顺带关闭该条
  if (result !== false) toast.dismiss(id)
}
</script>

<template>
  <Teleport to="body">
    <!--
      aria-live 容器常驻（即使队列为空），否则屏幕阅读器不会播报后插入的内容。
      role="status" + aria-live="polite" 用于常规提示；
      error 走 assertive 容器，见下方第二个 region。
    -->
    <div
      class="toast-host"
      :class="`toast-host--${position}`"
      role="status"
      aria-live="polite"
      aria-atomic="false"
    >
      <TransitionGroup name="toast" tag="div" class="stack stack--2" style="width: 100%">
        <Toast
          v-for="item in items.filter((i) => i.kind !== 'error')"
          :key="item.id"
          :item="item"
          @close="toast.dismiss(item.id)"
          @action="(i) => onAction(item.id, i)"
        />
      </TransitionGroup>
    </div>

    <!-- 错误单独走 assertive 容器，确保立即播报。 -->
    <div
      v-if="items.some((i) => i.kind === 'error')"
      class="toast-host"
      :class="`toast-host--${position}`"
      role="alert"
      aria-live="assertive"
      aria-atomic="false"
    >
      <TransitionGroup name="toast" tag="div" class="stack stack--2" style="width: 100%">
        <Toast
          v-for="item in items.filter((i) => i.kind === 'error')"
          :key="item.id"
          :item="item"
          @close="toast.dismiss(item.id)"
          @action="(i) => onAction(item.id, i)"
        />
      </TransitionGroup>
    </div>
  </Teleport>
</template>
