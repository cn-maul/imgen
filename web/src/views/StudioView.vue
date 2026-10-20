<script setup lang="ts">
/**
 * 工作台（核心页）：左参数面板 / 右画布 + 结果流。
 *
 * 关键行为：
 *  - 提交后立刻拿到 job，靠 jobs store 的 SSE 订阅推进度（降级轮询已在 store 内）
 *  - 生成中：骨架屏 + 阶段文案 + 耗时
 *  - 队列满：按钮文案变「排队中」
 *  - 刷新后恢复未完成任务（store.resume）
 *  - 结果操作：下载 / 再生成 / 送入编辑 / 作为参考图 / 删除
 */
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import Button from '@/components/ui/Button.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Lightbox from '@/components/ui/Lightbox.vue'
import Progress from '@/components/ui/Progress.vue'
import Skeleton from '@/components/ui/Skeleton.vue'
import ParamPanel from '@/components/domain/ParamPanel.vue'
import ResultGrid from '@/components/domain/ResultGrid.vue'
import { useToast } from '@/components/ui/useToast'
import { useProvidersStore } from '@/stores/providers'
import { useStudioStore } from '@/stores/studio'
import { useJobsStore } from '@/stores/jobs'
import { useAssetsStore } from '@/stores/assets'
import type { Job, JobImage } from '@/types'
import { describeError, formatDuration, isRetryable } from '@/components/domain/useApiError'

const router = useRouter()
const providers = useProvidersStore()
const studio = useStudioStore()
const jobs = useJobsStore()
const assets = useAssetsStore()
const toast = useToast()

const lightboxSrc = ref('')
const lightboxOpen = ref(false)
const deleting = ref<Set<string>>(new Set())

// ------------------------------------------------------------------ 初始化
onMounted(async () => {
  if (!providers.loaded) await providers.load()
  studio.syncCaps(providers.caps)
  // 刷新后恢复未完成任务（store 里带 localStorage 记录）。
  await jobs.resume()
})

/** 切 provider/model → 把草稿钳到新能力区间内。 */
watch(
  () => [providers.current?.id, providers.modelId] as const,
  () => studio.syncCaps(providers.caps),
)

// ------------------------------------------------------------------ 提交
const busy = computed(() => jobs.busy)

const submitLabel = computed(() => {
  if (jobs.submitting) return '提交中'
  if (jobs.queueFull) return '排队中'
  if (busy.value) return '生成中'
  return '生成'
})

async function onSubmit(): Promise<void> {
  const p = providers.current
  if (!p) {
    toast.warn('还没有可用的通道')
    return
  }
  if (!providers.hasKey) {
    toast.error('该通道未配置 API Key', {
      actions: [{ label: '去配置', onClick: () => void router.push('/settings/providers') }],
    })
    return
  }

  const reason = studio.blockedReason(providers.caps, providers.hasKey)
  if (reason) {
    toast.warn(reason)
    return
  }

  const req = studio.buildRequest(p.id, providers.modelId, providers.caps)
  try {
    const job = await jobs.submit(req)
    if (!job) return
    if (jobs.queueFull) toast.info('队列已满，任务将排队执行')
  } catch (e) {
    // 建任务就失败：retryable 决定给「重试」还是引导改提示词。
    const msg = describeError(e)
    if (isRetryable(e)) {
      toast.error(msg, {
        actions: [{ label: '重试', onClick: () => void onSubmit() }],
      })
    } else {
      toast.error(msg, { title: '请修改提示词或参数后重试' })
    }
  }
}

// ------------------------------------------------------------------ 结果操作
/** 结果图的元信息（宽高/体积）由列表接口按需补齐，这里只缓存已见过的。 */
const meta = ref<Record<string, { width: number; height: number; bytes: number }>>({})

async function ensureMeta(): Promise<void> {
  const ids = (jobs.current?.images ?? [])
    .map((i) => i.assetId)
    .filter((id): id is string => typeof id === 'string' && !(id in meta.value))
  if (!ids.length) return
  await assets.loadMeta(ids)
  for (const [id, m] of Object.entries(assets.meta)) meta.value[id] = m
}

watch(() => jobs.current?.images?.length, ensureMeta, { immediate: true })

function openLightbox(assetId: string): void {
  lightboxSrc.value = `/api/v1/assets/${encodeURIComponent(assetId)}/raw`
  lightboxOpen.value = true
}

/** 「再生成」：同一张结果图作为输入，参数不变。 */
function onRegenerate(image: JobImage): void {
  if (!image.assetId) return
  studio.clearRefs()
  studio.addAssetRef(image.assetId, `结果 #${image.seq}`)
  studio.mode = providers.canImg2Img ? 'img2img' : 'txt2img'
  toast.info('已把该图设为参考图，可直接生成（近似复现）')
}

/** 「送入编辑」：切到图生图并跳到工作台顶部聚焦提示词。 */
function onToEdit(image: JobImage): void {
  if (!image.assetId) return
  if (!providers.canImg2Img) {
    toast.warn('当前模型不支持图生图，请切换到 SenseNova')
    return
  }
  studio.clearRefs()
  studio.addAssetRef(image.assetId, `结果 #${image.seq}`)
  studio.mode = 'img2img'
  toast.info('已送入编辑，请补充编辑指令后生成')
}

function onAsRef(image: JobImage): void {
  if (!image.assetId) return
  if (studio.refs.length >= providers.maxRefs) {
    toast.warn(`参考图最多 ${providers.maxRefs} 张`)
    return
  }
  const ok = studio.addAssetRef(image.assetId, `结果 #${image.seq}`)
  toast[ok ? 'success' : 'info'](ok ? '已加入参考图' : '该图已在参考图列表中')
}

async function onDelete(image: JobImage): Promise<void> {
  if (!image.assetId) return
  const id = image.assetId
  deleting.value.add(id)
  try {
    const ok = await assets.remove(id)
    if (!ok) {
      toast.error(assets.error || '删除失败')
      return
    }
    // 从当前任务的 images 里摘掉，UI 立刻反映。
    const job = jobs.current
    if (job) {
      jobs.upsert({
        ...job,
        images: job.images.filter((i) => i.assetId !== id),
      })
    }
    toast.success('已删除')
  } catch (e) {
    toast.error(describeError(e))
  } finally {
    deleting.value.delete(id)
  }
}

/** 「以此次参数再生成」：把历史任务参数灌回草稿。 */
function onReuse(job: Job): void {
  studio.applyJob(job)
  if (job.provider !== providers.current?.id) providers.selectProvider(job.provider)
  if (job.model !== providers.modelId) providers.selectModel(job.model)
  studio.syncCaps(providers.caps)
  toast.info('参数已回填，确认后点击生成')
}

// ------------------------------------------------------------------ 展示
const current = computed(() => jobs.current)
const pendingCount = computed(() => {
  const j = current.value
  if (!j) return 0
  return Math.max(0, (j.count || 1) - (j.images?.length ?? 0))
})

const stageText = computed(() => {
  if (!busy.value) return ''
  return jobs.stage || '处理中'
})

const errorOf = computed(() => current.value?.error)

const errorRetryable = computed(() => {
  const err = errorOf.value
  if (!err) return false
  return err.retryable
})

function onRetry(): void {
  void onSubmit()
}

function onCancel(): void {
  const id = current.value?.id
  if (id) void jobs.cancel(id)
}

function onNew(): void {
  jobs.clearCurrent()
}
</script>

<template>
  <div class="studio">
    <!-- ------------------------------------------------------- 左：参数面板 -->
    <aside class="studio__panel panel">
      <div class="panel__head">
        <h2 class="panel__title">参数</h2>
        <span v-if="busy" class="hint">生成中，参数已锁定</span>
      </div>
      <div class="panel__body scroll-y">
        <ParamPanel @submit="onSubmit" @go-settings="router.push('/settings/providers')" />
      </div>
      <div class="panel__foot">
        <Button
          variant="primary"
          size="lg"
          block
          :loading="jobs.submitting"
          :disabled="busy || !providers.ready"
          :title="jobs.queueFull ? '队列已满，将排队执行' : '开始生成'"
          @click="onSubmit"
        >
          {{ submitLabel }}
        </Button>
        <Button
          v-if="busy && current"
          variant="danger-ghost"
          size="sm"
          block
          class="mt-2"
          @click="onCancel"
        >
          取消任务
        </Button>
      </div>
    </aside>

    <!-- ------------------------------------------------------- 右：画布 + 结果 -->
    <section class="studio__canvas panel">
      <div class="panel__head">
        <h2 class="panel__title">画布</h2>
        <div class="row row--tight">
          <span v-if="busy" class="hint">{{ stageText }}</span>
          <span v-if="busy" class="hint mono">{{ formatDuration(jobs.elapsedMs) }}</span>
          <Button v-if="current && !busy" variant="ghost" size="sm" @click="onNew">
            新建
          </Button>
        </div>
      </div>

      <div v-if="busy" class="studio__progress">
        <Progress
          :value="jobs.progress"
          :indeterminate="jobs.progress <= 6"
          size="sm"
        />
        <span class="hint">{{ jobs.finishedImages }} / {{ current?.count ?? 1 }} 张</span>
      </div>

      <div class="panel__body scroll-y">
        <!-- 空态 -->
        <EmptyState
          v-if="!current"
          title="还没有生成结果"
          desc="在左侧填写提示词，点击「生成」开始。支持拖拽或粘贴参考图做图生图与多图合成。"
        />

        <!-- 错误：retryable → 重试；否则引导改提示词 -->
        <div v-else-if="errorOf" class="studio__err">
          <EmptyState
            :title="errorOf.message || '生成失败'"
            :desc="
              errorRetryable
                ? '这是可重试的失败（超时、限流或上游不可用），可以直接再来一次。'
                : '上游拒绝了本次请求或内容未过审 —— 建议修改提示词或参数后重新生成。'
            "
            error
          >
            <template #actions>
              <Button v-if="errorRetryable" variant="primary" size="md" @click="onRetry">
                重试
              </Button>
              <Button variant="secondary" size="md" @click="onNew">修改提示词</Button>
            </template>
          </EmptyState>
        </div>

        <!-- 结果 / 生成中 -->
        <div v-else class="studio__results">
          <ResultGrid
            v-if="current"
            :job="current"
            :meta="meta"
            @regenerate="onRegenerate"
            @to-edit="onToEdit"
            @as-ref="onAsRef"
            @delete="onDelete"
            @reuse="onReuse"
            @lightbox="openLightbox"
          />

          <!-- 等待中的席位：骨架屏 -->
          <div v-if="pendingCount > 0" class="studio__skeletons">
            <div v-for="i in pendingCount" :key="`sk-${i}`" class="studio__skeleton">
              <Skeleton variant="rect" height="100%" />
            </div>
          </div>
        </div>
      </div>

      <!-- 右下角：批次进度条 -->
      <div v-if="busy" class="studio__dock">
        <Progress :value="jobs.progress" size="lg" />
        <div class="row row--tight row--between">
          <span class="hint">{{ stageText }} · {{ formatDuration(jobs.elapsedMs) }}</span>
          <span class="hint mono">
            {{ jobs.finishedImages }}/{{ current?.count ?? 1 }}
          </span>
        </div>
      </div>
    </section>

    <Lightbox
      :open="lightboxOpen"
      :src="lightboxSrc"
      caption="结果原图"
      @close="lightboxOpen = false"
    />
  </div>
</template>

<style scoped>
.studio {
  display: flex;
  gap: var(--space-4);
  height: 100%;
  min-height: 0;
}

.studio__panel {
  flex: none;
  width: var(--panel-w);
  max-width: 42vw;
}

.studio__canvas {
  position: relative;
  flex: 1 1 auto;
  min-width: 0;
}

.studio__progress {
  flex: none;
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-2) var(--space-4);
  border-bottom: var(--border-w) solid var(--border);
  background: var(--surface-2);
}

.studio__err {
  padding: var(--space-4);
}

.studio__results {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.studio__skeletons {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
  gap: var(--space-3);
}

.studio__skeleton {
  aspect-ratio: 1 / 1;
}

/* 右下角批次进度：浮在画布上，不占布局 */
.studio__dock {
  position: absolute;
  right: var(--space-4);
  bottom: var(--space-4);
  z-index: var(--z-sticky);
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  width: 240px;
  padding: var(--space-3);
  border: var(--border-w) solid var(--border);
  border-radius: var(--radius-md);
  background: var(--surface-1);
  box-shadow: var(--shadow-2);
}

@media (max-width: 1080px) {
  .studio {
    flex-direction: column;
    overflow-y: auto;
  }

  .studio__panel {
    width: 100%;
    max-width: none;
  }
}
</style>
