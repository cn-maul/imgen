<script setup lang="ts">
/**
 * 应用外壳：顶栏（Logo / Provider / Model / 队列水位 / 主题）+ 侧边导航 + 内容槽。
 *
 * PLAN §7.1：顶栏要带 Provider▾ Model▾ 与队列水位；这两个选择器写状态进
 * providers store，工作台的参数面板从同一个 store 读 Caps，避免两处真相。
 */
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import Select from '@/components/ui/Select.vue'
import { useProvidersStore } from '@/stores/providers'
import { useJobsStore } from '@/stores/jobs'

interface NavItem {
  to: string
  label: string
  icon: string
}

const NAV: NavItem[] = [
  { to: '/studio', label: '工作台', icon: 'sparkles' },
  { to: '/presets', label: '提示词库', icon: 'book' },
  { to: '/assets', label: '资产库', icon: 'images' },
  { to: '/history', label: '生成历史', icon: 'clock' },
  { to: '/settings/providers', label: '通道配置', icon: 'key' },
  { to: '/settings/system', label: '系统设置', icon: 'sliders' },
]

const providers = useProvidersStore()
const jobs = useJobsStore()

// ------------------------------------------------------------------ 主题
const THEME_KEY = 'imgen:theme'
const theme = ref<'dark' | 'light'>('dark')

function applyTheme(t: 'dark' | 'light'): void {
  document.documentElement.setAttribute('data-theme', t)
}

onMounted(() => {
  let saved: string | null = null
  try {
    saved = localStorage.getItem(THEME_KEY)
  } catch {
    saved = null
  }
  const initial: 'dark' | 'light' =
    saved === 'light' || saved === 'dark'
      ? saved
      : window.matchMedia?.('(prefers-color-scheme: light)').matches
        ? 'light'
        : 'dark'
  theme.value = initial
  applyTheme(initial)

  void providers.load()
  jobs.startHealthProbe()
})

function toggleTheme(): void {
  theme.value = theme.value === 'dark' ? 'light' : 'dark'
  applyTheme(theme.value)
  try {
    localStorage.setItem(THEME_KEY, theme.value)
  } catch {
    // 忽略：主题不落盘也能用。
  }
}

// ------------------------------------------------------------------ 选择器
const providerOptions = computed(() =>
  providers.items.map((p) => ({
    value: p.id,
    label: p.name + (p.hasKey ? '' : '（未配 Key）'),
    disabled: !p.enabled,
  })),
)

const modelOptions = computed(() =>
  providers.models.map((m) => ({ value: m.id, label: m.name || m.id })),
)

const providerValue = computed(() => providers.current?.id ?? '')
const modelValue = computed(() => providers.modelId)

function onProvider(v: string | number | null): void {
  if (typeof v === 'string' && v) providers.selectProvider(v as never)
}

function onModel(v: string | number | null): void {
  if (typeof v === 'string' && v) providers.selectModel(v)
}

// ------------------------------------------------------------------ 队列水位
const queueTone = computed<'ok' | 'warn' | 'danger'>(() => {
  if (jobs.queueFull) return 'danger'
  if (jobs.queued > 0) return 'warn'
  return 'ok'
})

const queueText = computed(() => {
  if (jobs.workers <= 0) return '队列 —'
  return `队列 ${jobs.queued}/${jobs.workers}`
})

// ------------------------------------------------------------------ 图标
const ICONS: Record<string, string> = {
  sparkles:
    'M12 3l1.6 4.2L18 9l-4.4 1.8L12 15l-1.6-4.2L6 9l4.4-1.8L12 3zm6 9l.9 2.3L21 15l-2.1.7L18 18l-.9-2.3L15 15l2.1-.7L18 12z',
  book:
    'M4 4h6a3 3 0 013 3v13a2.5 2.5 0 00-2.5-2.5H4V4zm16 0h-6a3 3 0 00-3 3v13a2.5 2.5 0 012.5-2.5H20V4z',
  images:
    'M4 5h10v2H4V5zm0 4h10v6H4V9zm12-4h4v10h-4V5zm0 12h4v2h-4v-2zM4 17h10v2H4v-2z',
  clock:
    'M12 2a10 10 0 100 20 10 10 0 000-20zm1 5v5.6l4 2.3-1 1.7-5-2.9V7h2z',
  key: 'M14 2a6 6 0 00-5.7 7.9L2 16v4h4v-2h2v-2h2l1.6-1.6A6 6 0 1014 2zm2.5 4a1.5 1.5 0 110-3 1.5 1.5 0 010 3z',
  sliders: 'M3 7h10v2H3V7zm14 0h4v2h-4V7zM3 15h4v2H3v-2zm8 0h10v2H11v-2z',
  sun: 'M12 7a5 5 0 100 10 5 5 0 000-10zm0-6l1.5 3h-3L12 1zm0 20l1.5-3h-3L12 21zM1 12l3-1.5v3L1 12zm20 0l-3 1.5v-3L23 12z',
  moon: 'M12 3a9 9 0 109 9 7 7 0 11-9-9z',
}
</script>

<template>
  <div class="shell">
    <header class="shell__header">
      <div class="row row--tight flex-none">
        <span class="shell__logo" aria-hidden="true">
          <svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor">
            <path :d="ICONS.sparkles" />
          </svg>
        </span>
        <strong class="t-lg lh-tight">Imgen</strong>
      </div>

      <div class="divider divider--vertical" />

      <div class="row row--tight flex-1 min-w-0">
        <Select
          class="shell__pick"
          :model-value="providerValue"
          :options="providerOptions"
          placeholder="选择通道"
          size="sm"
          @update:model-value="onProvider"
        />
        <Select
          class="shell__pick"
          :model-value="modelValue"
          :options="modelOptions"
          placeholder="选择模型"
          size="sm"
          @update:model-value="onModel"
        />
      </div>

      <div class="row row--tight flex-none">
        <span class="hint whitespace-nowrap">{{ queueText }}</span>
        <Badge :tone="queueTone" dot>{{ jobs.queued }}</Badge>

        <Button
          variant="ghost"
          size="sm"
          icon
          :title="theme === 'dark' ? '切换到浅色' : '切换到深色'"
          @click="toggleTheme"
        >
          <svg viewBox="0 0 24 24" width="16" height="16" fill="currentColor">
            <path :d="theme === 'dark' ? ICONS.sun : ICONS.moon" />
          </svg>
        </Button>
      </div>
    </header>

    <div class="shell__main">
      <nav class="shell__nav" aria-label="主导航">
        <RouterLink
          v-for="item in NAV"
          :key="item.to"
          :to="item.to"
          class="shell__navlink"
          active-class="is-active"
        >
          <svg viewBox="0 0 24 24" width="16" height="16" fill="currentColor" aria-hidden="true">
            <path :d="ICONS[item.icon]" />
          </svg>
          <span class="truncate">{{ item.label }}</span>
        </RouterLink>

        <div class="flex-1" />

        <p v-if="!providers.anyConfigured" class="hint shell__navhint">
          还没有通道配 Key，<RouterLink to="/settings/providers">去配置</RouterLink>
        </p>
      </nav>

      <main class="shell__content">
        <slot />
      </main>
    </div>
  </div>
</template>

<style scoped>
/* 外壳布局：只在这里定义骨架尺寸，其余一律交给原子类与 token。 */
.shell {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  background: var(--surface-0);
}

.shell__header {
  flex: none;
  display: flex;
  align-items: center;
  gap: var(--space-3);
  height: var(--header-h);
  padding-inline: var(--space-4);
  border-bottom: var(--border-w) solid var(--border);
  background: var(--surface-1);
}

.shell__logo {
  display: inline-flex;
  color: var(--accent);
}

.shell__pick {
  width: 168px;
  flex: none;
}

.shell__main {
  flex: 1 1 auto;
  display: flex;
  min-height: 0;
  min-width: 0;
}

.shell__nav {
  flex: none;
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  width: 168px;
  padding: var(--space-3);
  border-right: var(--border-w) solid var(--border);
  background: var(--surface-1);
  overflow-y: auto;
}

.shell__navlink {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  color: var(--text-2);
  font-size: var(--fs-sm);
  transition:
    background-color var(--dur-fast) var(--ease-out),
    color var(--dur-fast) var(--ease-out);
}

.shell__navlink:hover {
  background: var(--surface-2);
  color: var(--text-1);
}

.shell__navlink.is-active {
  background: var(--accent-bg);
  color: var(--accent);
  font-weight: var(--fw-medium);
}

.shell__navhint {
  padding: var(--space-2);
  border-radius: var(--radius-md);
  background: var(--warn-bg);
  color: var(--text-2);
}

.shell__content {
  flex: 1 1 auto;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  padding: var(--space-4);
}

@media (max-width: 860px) {
  .shell__nav {
    width: 56px;
    padding: var(--space-2);
  }

  .shell__navlink span {
    display: none;
  }
}
</style>
