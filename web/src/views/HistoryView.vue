<script setup lang="ts">
/**
 * 生成历史：任务列表 + 筛选 + 参数快照复用。
 *
 * 每个 job 都存了完整 params（PLAN §7.4），所以这里能直接：
 *   - 「以此次参数再生成」→ 灌回草稿并跳工作台
 *   - 「复制参数」→ JSON 到剪贴板
 *   - 「近似复现」→ 参数 + 结果图回灌为参考图（两家都无 seed，UI 明确标注）
 */
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import Card from '@/components/ui/Card.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import Skeleton from '@/components/ui/Skeleton.vue'
import { useToast } from '@/components/ui/useToast'
import { assetThumbURL } from '@/api/client'
import { useJobsStore, isTerminal } from '@/stores/jobs'
import { useStudioStore } from '@/stores/studio'
import { useProvidersStore } from '@/stores/providers'
import type { Job, JobStatus, ProviderID } from '@/types'
import {
  copyText,
  errorLabel,
  formatDuration,
  formatTime,
  fromNow,
} from '@/components/domain/useApiError'

const router = useRouter()
const jobs = useJobsStore()
const studio = useStudioStore()
const providers = useProvidersStore()
const toast = useToast()

const loading = ref(false)
const providerFilter = ref('')
const statusFilter = ref('')
const q = ref('')

const statusOptions: { value: string; label: string }[] = [
  { value: '', label: '全部状态' },
  { value: 'queued', label: '排队中' },
  { value: 'running', label: '生成中' },
  { value: 'partial', label: '部分完成' },
  { value: 'done', label: '已完成' },
  { value: 'failed', label: '失败' },
  { value: 'canceled', label: '已取消' },
]

const providerOptions = computed(() => [
  { value: '', label: '全部通道' },
  ...providers.items.map((p) => ({ value: p.id, label: p.name })),
])

async function reload(): Promise<void> {
  loading.value = true
  try {
    await jobs.list({
      provider: (providerFilter.value || undefined) as ProviderID | undefined,
      status: (statusFilter.value || undefined) as JobStatus | undefined,
      q: q.value || undefined,
    })
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  if (!providers.loaded) await providers.load()
  await reload()
})

watch([providerFilter, statusFilter], reload)

const items = computed<Job[]>(() => jobs.recent)

function statusTone(s: JobStatus): 'accent' | 'ok' | 'warn' | 'danger' | 'neutral' {
  switch (s) {
    case 'done':
      return 'ok'
    case 'partial':
      return 'warn'
    case 'failed':
      return 'danger'
    case 'canceled':
      return 'neutral'
    default:
      return 'accent'
  }
}

function statusLabel(s: JobStatus): string {
  switch (s) {
    case 'queued':
      return '排队中'
    case 'running':
      return '生成中'
    case 'partial':
      return '部分完成'
    case 'done':
      return '已完成'
    case 'failed':
      return '失败'
    case 'canceled':
      return '已取消'
    default:
      return s
  }
}

function doneImages(job: Job): number {
  return (job.images ?? []).filter((i) => i.status === 'done').length
}

/** 复用参数：灌回草稿 + 选中对应通道/模型 + 跳工作台。 */
function reuse(job: Job): void {
  studio.applyJob(job)
  if (job.provider !== providers.current?.id) providers.selectProvider(job.provider)
  if (job.model !== providers.modelId) providers.selectModel(job.model)
  studio.syncCaps(providers.caps)
  void router.push('/studio')
  toast.info('参数已回填，确认后点击生成')
}

/** 近似复现：参数 + 首张结果图回灌为主参考图。 */
function reproduce(job: Job): void {
  const first = (job.images ?? []).find((i) => i.status === 'done' && i.assetId)
  if (!first?.assetId) {
    toast.warn('这次任务没有可用的结果图，无法复现')
    return
  }
  studio.applyJob(job)
  // 结果图回灌为「主图」：先清空再放，保证它排在第 1 位。
  studio.clearRefs()
  studio.addAssetRef(first.assetId, `复现基准 #${first.seq}`)
  if (job.provider !== providers.current?.id) providers.selectProvider(job.provider)
  if (job.model !== providers.modelId) providers.selectModel(job.model)
  studio.syncCaps(providers.caps)
  void router.push('/studio')
  toast.warn('已回灌为参考图 —— 两家都无 seed，这是近似复现而非逐像素复刻')
}

async function copyParams(job: Job): Promise<void> {
  const ok = await copyText(JSON.stringify(job.params, null, 2))
  toast[ok ? 'success' : 'error'](ok ? '参数已复制' : '复制失败')
}

function open(job: Job): void {
  jobs.open(job)
  void router.push('/studio')
}

async function cancel(job: Job): Promise<void> {
  await jobs.cancel(job.id)
  await reload()
}
</script>

<template>
  <div class="history stack">
    <header class="row row--between row--wrap">
      <div class="stack stack--1">
        <h2 class="t-xl">生成历史</h2>
        <p class="hint">共 {{ jobs.total }} 条 · 每个任务都保留了完整参数快照</p>
      </div>

      <div class="row row--tight">
        <Input v-model="q" placeholder="搜索提示词" size="sm" clearable @enter="reload" />
        <Select v-model="providerFilter" :options="providerOptions" size="sm" />
        <Select v-model="statusFilter" :options="statusOptions" size="sm" />
        <Button variant="secondary" size="sm" @click="reload">刷新</Button>
      </div>
    </header>

    <div v-if="loading && !items.length" class="stack stack--3">
      <div v-for="i in 4" :key="`sk-${i}`" class="history__sk">
        <Skeleton variant="text" width="40%" />
        <Skeleton variant="rect" height="72px" />
      </div>
    </div>

    <EmptyState
      v-else-if="!items.length"
      title="还没有生成记录"
      desc="回到工作台跑一次生成，任务会带着完整参数出现在这里，随时可以复用或复现。"
    >
      <template #actions>
        <Button variant="primary" size="md" @click="router.push('/studio')">
          去工作台
        </Button>
      </template>
    </EmptyState>

    <div v-else class="stack stack--3">
      <Card v-for="job in items" :key="job.id" hover class="history__item">
        <template #head>
          <div class="card__title min-w-0">
            <span class="truncate">{{ job.prompt || '（无提示词）' }}</span>
            <span class="card__subtitle">
              {{ job.provider }} · {{ job.model }} · {{ fromNow(job.createdAt) }}
            </span>
          </div>
          <div class="row row--tight flex-none">
            <Badge :tone="statusTone(job.status)" size="sm" dot>
              {{ statusLabel(job.status) }}
            </Badge>
            <Badge size="sm" outline>{{ doneImages(job) }}/{{ job.count }} 张</Badge>
            <Badge v-if="job.latencyMs" size="sm" outline>
              {{ formatDuration(job.latencyMs) }}
            </Badge>
          </div>
        </template>

        <div class="history__body">
          <!-- 结果缩略图条 -->
          <div v-if="job.images?.length" class="history__thumbs">
            <div
              v-for="img in job.images.filter((i) => i.assetId)"
              :key="img.seq"
              class="thumb history__thumb"
            >
              <img :src="assetThumbURL(img.assetId!)" :alt="`结果 #${img.seq}`" loading="lazy" />
            </div>
          </div>
          <p v-else class="hint">本次任务没有产出图片。</p>

          <p v-if="job.error" class="error-text">
            {{ errorLabel(job.error) }}：{{ job.error.message }}
          </p>

          <p class="hint mono">{{ formatTime(job.createdAt) }} · {{ job.id }}</p>
        </div>

        <template #foot>
          <Button variant="secondary" size="sm" @click="reuse(job)">
            以此次参数再生成
          </Button>
          <Button
            variant="secondary"
            size="sm"
            :disabled="doneImages(job) === 0"
            @click="reproduce(job)"
          >
            近似复现
          </Button>
          <Button variant="ghost" size="sm" @click="copyParams(job)">复制参数</Button>

          <div class="row row--tight ml-auto">
            <Button
              v-if="!isTerminal(job.status)"
              variant="ghost"
              size="sm"
              @click="open(job)"
            >
              查看进度
            </Button>
            <Button
              v-if="!isTerminal(job.status)"
              variant="danger-ghost"
              size="sm"
              @click="cancel(job)"
            >
              取消
            </Button>
            <Button v-else variant="ghost" size="sm" @click="open(job)">查看结果</Button>
          </div>
        </template>
      </Card>
    </div>
  </div>
</template>

<style scoped>
.history {
  height: 100%;
  min-height: 0;
}

.history__item {
  overflow: hidden;
}

.history__body {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.history__thumbs {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.history__thumb {
  width: 72px;
  aspect-ratio: 1 / 1;
}

.history__sk {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}
</style>
