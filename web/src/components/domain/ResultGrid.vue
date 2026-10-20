<script setup lang="ts">
/**
 * 结果卡片：hover 出浮层操作（下载/再生成/送入编辑/作为参考图/删除），
 * 底部给「复制参数」「以此次参数再生成」「近似复现」三个快照操作。
 *
 * 「近似复现」的说明必须显式告知用户：两家都没有 seed，所以同样参数 + 结果图
 * 回灌为参考图只能得到近似结果，不是严格复刻。
 */
import { computed, ref } from 'vue'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import Progress from '@/components/ui/Progress.vue'
import Skeleton from '@/components/ui/Skeleton.vue'
import Modal from '@/components/ui/Modal.vue'
import { assetThumbURL } from '@/api/client'
import type { Job, JobImage } from '@/types'
import {
  copyText,
  downloadAsset,
  errorLabel,
  formatDuration,
  formatTime,
} from './useApiError'
import { useToast } from '@/components/ui/useToast'

const props = defineProps<{
  job: Job
  /** 每个资产的元信息是否需要（宽高/体积），由父组件异步补齐后传入。 */
  meta?: Record<string, { width: number; height: number; bytes: number }>
}>()

const emit = defineEmits<{
  (e: 'regenerate', image: JobImage): void
  (e: 'toEdit', image: JobImage): void
  (e: 'asRef', image: JobImage): void
  (e: 'delete', image: JobImage): void
  (e: 'reuse', job: Job): void
  (e: 'lightbox', assetId: string): void
}>()

const toast = useToast()
const confirmDelete = ref<JobImage | null>(null)

const images = computed<JobImage[]>(() => props.job.images ?? [])
const doneCount = computed(() => images.value.filter((i) => i.status === 'done').length)
const failCount = computed(() => images.value.filter((i) => i.status === 'failed').length)
const total = computed(() => Math.max(props.job.count || 1, images.value.length))

/** 占位：等待中的序位用骨架屏撑住网格，避免完成时布局跳动。 */
const pending = computed<number[]>(() => {
  const n = total.value - images.value.length
  return n > 0 ? Array.from({ length: n }, (_, i) => images.value.length + i) : []
})

const paramText = computed(() => JSON.stringify(props.job.params, null, 2))

const statusTone = computed<'ok' | 'danger' | 'warn' | 'accent' | 'neutral'>(() => {
  switch (props.job.status) {
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
})

const statusLabel = computed(() => {
  switch (props.job.status) {
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
      return props.job.status
  }
})

function thumb(image: JobImage): string {
  return image.assetId ? assetThumbURL(image.assetId) : ''
}

function sizeOf(image: JobImage): string {
  const m = image.assetId ? props.meta?.[image.assetId] : undefined
  return m && m.width ? `${m.width}×${m.height}` : ''
}

async function onDownload(image: JobImage): Promise<void> {
  if (!image.assetId) return
  await downloadAsset(image.assetId, `imgen-${props.job.id.slice(0, 8)}-${image.seq}.png`)
}

async function onCopyParams(): Promise<void> {
  const ok = await copyText(paramText.value)
  toast[ok ? 'success' : 'error'](ok ? '参数已复制到剪贴板' : '复制失败，请手动选择')
}
</script>

<template>
  <article class="rgrid card">
    <header class="card__head">
      <div class="min-w-0">
        <div class="row row--tight">
          <h3 class="card__title truncate">{{ job.prompt || '（无提示词）' }}</h3>
          <Badge :tone="statusTone" size="sm">{{ statusLabel }}</Badge>
          <Badge size="sm" outline>{{ job.provider }}</Badge>
        </div>
        <p class="card__subtitle truncate">
          {{ job.model }} · {{ images.length }}/{{ total }} 张 ·
          {{ job.latencyMs ? formatDuration(job.latencyMs) : formatTime(job.createdAt) }}
        </p>
      </div>

      <div class="row row--tight flex-none">
        <Button variant="ghost" size="sm" @click="onCopyParams">复制参数</Button>
        <Button variant="secondary" size="sm" @click="emit('reuse', job)">
          以此次参数再生成
        </Button>
      </div>
    </header>

    <!-- 批次进度：部分成功/运行中才显示 -->
    <div v-if="job.status !== 'done' && job.status !== 'failed'" class="rgrid__progress">
      <Progress
        :value="(doneCount / total) * 100"
        size="sm"
        :tone="failCount > 0 ? 'error' : 'accent'"
      />
      <span class="hint">已完成 {{ doneCount }}/{{ total }}</span>
    </div>

    <div v-if="job.error" class="rgrid__err">
      <Badge tone="danger" size="sm">{{ errorLabel(job.error) }}</Badge>
      <span class="t-xs text-2 break-words">{{ job.error.message }}</span>
    </div>

    <div class="card__body">
      <div class="rgrid__grid">
        <figure v-for="image in images" :key="image.seq" class="rgrid__cell">
          <template v-if="image.assetId">
            <img
              class="rgrid__img"
              :src="thumb(image)"
              :alt="`结果 #${image.seq}`"
              loading="lazy"
              @click="emit('lightbox', image.assetId!)"
            />
            <figcaption class="rgrid__cap">
              <span class="t-xs mono">#{{ image.seq }}</span>
              <span v-if="sizeOf(image)" class="t-xs text-3">{{ sizeOf(image) }}</span>
            </figcaption>
            <div class="rgrid__ops">
              <Button
                variant="secondary"
                size="sm"
                title="下载原图"
                @click="onDownload(image)"
              >
                下载
              </Button>
              <Button
                variant="secondary"
                size="sm"
                title="用同一张图再生成一次"
                @click="emit('regenerate', image)"
              >
                再生成
              </Button>
              <Button
                variant="secondary"
                size="sm"
                title="把这张图作为图生图输入"
                @click="emit('toEdit', image)"
              >
                送入编辑
              </Button>
              <Button
                variant="secondary"
                size="sm"
                title="加入参考图队列"
                @click="emit('asRef', image)"
              >
                作为参考图
              </Button>
              <Button
                variant="danger-ghost"
                size="sm"
                title="从资产库删除"
                @click="confirmDelete = image"
              >
                删除
              </Button>
            </div>
          </template>

          <!-- 失败席位：显示上游原因而不是黑框图 -->
          <div v-else class="rgrid__failed">
            <Badge tone="danger" size="sm">{{ errorLabel(image.error) }}</Badge>
            <p class="t-xs text-2 line-clamp-3 break-words">
              {{ image.error?.message || '该席位生成失败' }}
            </p>
          </div>
        </figure>

        <!-- 等待中的席位用骨架屏占位 -->
        <div v-for="i in pending" :key="`pending-${i}`" class="rgrid__cell">
          <Skeleton variant="rect" height="100%" />
          <span class="rgrid__pending t-xs text-3">等待中 #{{ i + 1 }}</span>
        </div>
      </div>

      <p class="hint rgrid__notice">
        <strong>近似复现</strong>：两家通道都不支持 seed，用「当前参数 + 结果图回灌为参考图」
        只能得到<em>近似</em>结果，并非逐像素复刻。
      </p>
    </div>

    <Modal
      :open="confirmDelete !== null"
      title="删除这张图片？"
      size="sm"
      @close="confirmDelete = null"
    >
      <p class="t-md text-2">
        该图片会从本地资产库移除，无法恢复。已复制或已导出的副本不受影响。
      </p>
      <template #foot>
        <Button variant="ghost" size="md" @click="confirmDelete = null">取消</Button>
        <Button
          variant="danger"
          size="md"
          @click="
            confirmDelete && emit('delete', confirmDelete);
            confirmDelete = null
          "
        >
          确认删除
        </Button>
      </template>
    </Modal>
  </article>
</template>

<style scoped>
.rgrid__progress {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-2) var(--space-4);
  border-bottom: var(--border-w) solid var(--border);
  background: var(--surface-2);
}

.rgrid__err {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-4);
  border-bottom: var(--border-w) solid var(--border);
  background: var(--danger-bg);
}

.rgrid__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
  gap: var(--space-3);
}

.rgrid__cell {
  position: relative;
  margin: 0;
  aspect-ratio: 1 / 1;
  border-radius: var(--radius-md);
  overflow: hidden;
  background: var(--surface-2);
}

.rgrid__img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  cursor: zoom-in;
}

.rgrid__cap {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  padding: var(--space-1) var(--space-2);
  background: linear-gradient(to top, rgba(6, 8, 12, 0.78), transparent);
  color: var(--text-1);
}

/* hover 才浮出操作条；键盘 Tab 进来时同样显示 */
.rgrid__ops {
  position: absolute;
  inset: 0;
  display: flex;
  flex-wrap: wrap;
  align-content: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-2);
  background: rgba(6, 8, 12, 0.72);
  opacity: 0;
  transition: opacity var(--dur-fast) var(--ease-out);
}

.rgrid__cell:hover .rgrid__ops,
.rgrid__cell:focus-within .rgrid__ops {
  opacity: 1;
}

.rgrid__failed {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  justify-content: center;
  gap: var(--space-2);
  height: 100%;
  padding: var(--space-3);
  background: var(--danger-bg);
}

.rgrid__pending {
  position: absolute;
  inset: auto 0 var(--space-2) 0;
  text-align: center;
}

.rgrid__notice {
  margin-top: var(--space-3);
}
</style>
