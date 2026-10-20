<script setup lang="ts">
/**
 * 图生图提示词库。
 *
 * 三栏：预设列表 / 模板编辑器 / 生成结果。
 * 中栏的核心是「参考图有序槽位 + @N 引用 + 渲染预览」——
 * 让用户在提交前就确切知道模型会收到什么、哪张图是第几张。
 */
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import Card from '@/components/ui/Card.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Modal from '@/components/ui/Modal.vue'
import { useToast } from '@/components/ui/useToast'
import PresetEditor from '@/components/domain/PresetEditor.vue'
import { renderPrompt, type RenderRef } from '@/components/domain/promptRender'
import { api, assetThumbURL } from '@/api/client'
import { useJobsStore } from '@/stores/jobs'
import { usePresetsStore } from '@/stores/presets'
import { useProvidersStore } from '@/stores/providers'
import type { Asset, Aspect, PresetBody, PresetRef, PromptPreset, RefRole, Tier } from '@/types'

const presets = usePresetsStore()
const jobs = useJobsStore()
const providers = useProvidersStore()
const toast = useToast()
const router = useRouter()

// ---------------------------------------------------------------- 编辑态
const selectedId = ref('')
const name = ref('')
const category = ref('')
const description = ref('')
const body = ref<PresetBody>({ prompt: '', refs: [] })

const emptyBody = (): PresetBody => ({
  prompt: '',
  refs: [],
  aspect: '1:1',
  tier: '1K',
  includeRefNotes: true,
})

const selected = computed(() => (selectedId.value ? presets.find(selectedId.value) : null))
const isNew = computed(() => !selectedId.value)
const saving = computed(() => presets.savingId === selectedId.value)

function loadInto(p: PromptPreset | null): void {
  if (!p) {
    selectedId.value = ''
    name.value = ''
    category.value = ''
    description.value = ''
    body.value = emptyBody()
    return
  }
  selectedId.value = p.id
  name.value = p.name
  category.value = p.category
  description.value = p.description
  // 合并默认值：旧预设可能缺 includeRefNotes / aspect。
  body.value = { ...emptyBody(), ...p.body, refs: [...(p.body.refs ?? [])] }
}

function select(p: PromptPreset): void {
  loadInto(p)
}

function startNew(): void {
  loadInto(null)
}

onMounted(async () => {
  await presets.load()
  await loadAssets()
  if (presets.items.length) loadInto(presets.items[0])
  else loadInto(null)
})

// ---------------------------------------------------------------- 资产库
const allAssets = ref<Asset[]>([])

async function loadAssets(): Promise<void> {
  const res = await api.listAssets({ limit: 100 })
  allAssets.value = res.items
}

// 资产被删除后，预设里的引用会失效。
const knownAssetIds = computed(() => new Set(allAssets.value.map((a) => a.id)))
const hasMissingAssets = computed(() =>
  body.value.refs.some((r) => knownAssetIds.value.size > 0 && !knownAssetIds.value.has(r.assetId)),
)

// ---------------------------------------------------------------- 参考图编辑
function onRemoveRef(i: number): void {
  const next = [...body.value.refs]
  next.splice(i, 1)
  body.value = { ...body.value, refs: next }
}

function onMoveRef(from: number, to: number): void {
  if (from === to) return
  const next = [...body.value.refs]
  const [item] = next.splice(from, 1)
  next.splice(to, 0, item)
  body.value = { ...body.value, refs: next }
}

function onSetRole(i: number, role: RefRole): void {
  const next = [...body.value.refs]
  next[i] = { ...next[i], role }
  body.value = { ...body.value, refs: next }
}

function onSetLabel(i: number, label: string): void {
  const next = [...body.value.refs]
  next[i] = { ...next[i], label }
  body.value = { ...body.value, refs: next }
}

// ---------------------------------------------------------------- 资产选择
const pickerOpen = ref(false)

async function openPicker(): Promise<void> {
  pickerOpen.value = true
  await loadAssets()
}

function addRefFromAsset(assetId: string): void {
  if (body.value.refs.some((r) => r.assetId === assetId)) {
    toast.info('这张图已在参考图里')
    return
  }
  const next: PresetRef[] = [...body.value.refs, { assetId, role: 'subject' }]
  body.value = { ...body.value, refs: next }
  toast.success(`已添加为第 ${next.length} 张参考图`)
}

// ---------------------------------------------------------------- 保存
async function onSave(): Promise<void> {
  const input = presets.toInput(name.value, body.value, category.value, description.value)
  if (!input.name) {
    toast.error('请填写预设名称')
    return
  }
  const saved = selectedId.value
    ? await presets.update(selectedId.value, input)
    : await presets.create(input)
  if (!saved) {
    toast.error(presets.error || '保存失败')
    return
  }
  loadInto(saved)
  toast.success(selectedId.value ? '已更新预设' : '已创建预设')
}

async function onDelete(): Promise<void> {
  const p = selected.value
  if (!p) return
  if (!window.confirm(`删除预设「${p.name}」？`)) return
  const ok = await presets.remove(p.id)
  if (!ok) {
    toast.error(presets.error || '删除失败')
    return
  }
  const next = presets.items[0] ?? null
  loadInto(next)
  toast.success('已删除')
}

// ---------------------------------------------------------------- 生成
const renderRefs = computed<RenderRef[]>(() =>
  body.value.refs.map((r, index) => ({ ...r, index })),
)

const finalPrompt = computed(() =>
  renderPrompt(body.value.prompt, renderRefs.value, {
    includeRefNotes: body.value.includeRefNotes !== false,
  }),
)

/** 提交前的就绪检查，把「哪里不对」直接说清楚。 */
const blockReason = computed(() => {
  if (!body.value.refs.length) return '请先添加至少一张参考图'
  if (!body.value.prompt.trim()) return '请填写提示词'
  if (hasMissingAssets.value) return '有参考图已被删除，请先移除'
  const p = providers.current
  if (!p) return '请先在顶栏选择通道'
  if (!p.hasKey) return `通道「${p.name}」尚未配置 API Key`
  const caps = p.caps[providers.modelId] ?? p.defaultCaps
  if (body.value.refs.length > caps.maxRefs) {
    return `该模型最多 ${caps.maxRefs} 张参考图，当前 ${body.value.refs.length} 张`
  }
  return ''
})

async function onGenerate(): Promise<void> {
  const p = providers.current
  if (!p || blockReason.value) return
  const caps = p.caps[providers.modelId] ?? p.defaultCaps
  const refs = body.value.refs.map((r) => ({ assetId: r.assetId }))
  const job = await jobs.submit({
    provider: p.id,
    model: providers.modelId,
    prompt: finalPrompt.value,
    refs,
    mode: refs.length > 1 ? 'compose' : 'img2img',
    aspect: (body.value.aspect ?? '1:1') as Aspect,
    tier: (body.value.tier ?? '1K') as Tier,
    count: 1,
    ...(caps.noWatermark ? { noWatermark: true } : {}),
    ...(caps.promptExtend ? { promptExtend: false } : {}),
  })
  if (!job) {
    toast.error(jobs.error || '提交失败')
    return
  }
  toast.success('已提交，可在右侧看结果')
}

/** 批量跑同一个预设 N 次，用来横向比较不同参数的出图倾向。 */
const batchCount = ref(3)
const batchRunning = ref(false)

async function onRunBatch(): Promise<void> {
  const p = providers.current
  if (!p || blockReason.value || batchRunning.value) return
  const caps = p.caps[providers.modelId] ?? p.defaultCaps
  const n = Math.min(5, Math.max(1, batchCount.value))
  batchRunning.value = true
  const results: string[] = []
  try {
    for (let i = 0; i < n; i++) {
      const job = await jobs.submit({
        provider: p.id,
        model: providers.modelId,
        prompt: finalPrompt.value,
        refs: body.value.refs.map((r) => ({ assetId: r.assetId })),
        mode: body.value.refs.length > 1 ? 'compose' : 'img2img',
        aspect: (body.value.aspect ?? '1:1') as Aspect,
        tier: (body.value.tier ?? '1K') as Tier,
        count: 1,
        ...(caps.noWatermark ? { noWatermark: true } : {}),
        ...(caps.promptExtend ? { promptExtend: false } : {}),
      })
      if (!job) {
        toast.error(jobs.error || `第 ${i + 1} 次提交失败`)
        break
      }
      results.push(job.id)
    }
    if (results.length) {
      toast.success(`已提交 ${results.length} 个任务，串行执行中`)
    }
  } finally {
    batchRunning.value = false
  }
}

// 预设的产出进「历史」更合适长期翻看，这里给一个跳转。
function goHistory(): void {
  void router.push('/history')
}

// 切换选中项时清掉上一个任务的展示，避免串台。
watch(selectedId, () => {
  jobs.clearCurrent()
})
</script>

<template>
  <div class="pv">
    <!-- 左：预设列表 -->
    <aside class="pv__list">
      <header class="pv__listhead">
        <h2 class="t-md fw-medium">提示词预设</h2>
        <Button size="sm" variant="primary" @click="startNew">新建</Button>
      </header>

      <div v-if="presets.loading" class="pv__loading hint">加载中…</div>

      <EmptyState
        v-else-if="!presets.items.length"
        title="还没有预设"
        desc="把常用的图生图提示词存成预设，配合有序参考图复用。"
        compact
      >
        <template #actions>
          <Button size="sm" variant="primary" @click="startNew">创建第一个</Button>
        </template>
      </EmptyState>

      <ul v-else class="pv__items">
        <li v-for="p in presets.items" :key="p.id">
          <button
            type="button"
            class="pv__item"
            :class="{ 'is-active': p.id === selectedId }"
            @click="select(p)"
          >
            <span class="pv__itemname truncate">{{ p.name }}</span>
            <span class="pv__itemmeta">
              <Badge v-if="p.category" tone="neutral">{{ p.category }}</Badge>
              <span class="hint">{{ p.body.refs?.length ?? 0 }} 图</span>
            </span>
          </button>
        </li>
      </ul>
    </aside>

    <!-- 中：编辑器 -->
    <section class="pv__editor">
      <Card>
        <template #header>
          <div class="row row--tight flex-1 min-w-0">
            <h2 class="t-md fw-medium truncate">
              {{ isNew ? '新建预设' : selected?.name || '预设' }}
            </h2>
            <Badge v-if="selected" tone="accent">已保存</Badge>
            <Badge v-else tone="neutral">草稿</Badge>
          </div>
          <Button
            v-if="selected"
            size="sm"
            variant="ghost"
            @click="onDelete"
          >
            删除
          </Button>
        </template>

        <PresetEditor
          v-model:name="name"
          v-model:category="category"
          v-model:description="description"
          :body="body"
          :saving="saving"
          :has-missing-assets="hasMissingAssets"
          @update:body="(v) => (body = v)"
          @pick-assets="openPicker"
          @remove-ref="onRemoveRef"
          @move-ref="onMoveRef"
          @set-role="onSetRole"
          @set-label="onSetLabel"
          @save="onSave"
        />
      </Card>
    </section>

    <!-- 右：生成与结果 -->
    <section class="pv__result">
      <Card>
        <template #header>
          <h2 class="t-md fw-medium">生成</h2>
        </template>

        <div class="pv__runbar">
          <Button
            variant="primary"
            block
            :disabled="Boolean(blockReason) || jobs.busy"
            :loading="jobs.submitting"
            @click="onGenerate"
          >
            生成 1 张
          </Button>
        </div>

        <p v-if="blockReason" class="hint">{{ blockReason }}</p>

        <div class="pv__batch">
          <label class="hint" for="batch-n">批量对比</label>
          <input
            id="batch-n"
            v-model.number="batchCount"
            class="input input--sm"
            type="number"
            min="1"
            max="5"
          />
          <Button
            size="sm"
            :disabled="Boolean(blockReason) || batchRunning || jobs.busy"
            @click="onRunBatch"
          >
            跑 {{ Math.min(5, Math.max(1, batchCount || 1)) }} 次
          </Button>
        </div>

        <div class="divider" />

        <!-- 当前任务 -->
        <div v-if="!jobs.current" class="hint">
          还没有生成记录。选好参考图和提示词后点上面的按钮。
        </div>
        <div v-else class="pv__job">
          <div class="row row--tight">
            <Badge :tone="jobs.current.status === 'done' ? 'ok' : jobs.current.status === 'failed' ? 'danger' : 'warn'">
              {{ jobs.current.status }}
            </Badge>
            <span class="hint">{{ Math.round(jobs.elapsedMs / 1000) }}s</span>
            <div class="flex-1" />
            <Button size="sm" variant="ghost" @click="goHistory">全部历史</Button>
          </div>

          <p v-if="jobs.current.error" class="error-text">
            {{ jobs.current.error.message }}
          </p>

          <div v-if="jobs.current.images?.length" class="pv__shots">
            <figure v-for="img in jobs.current.images" :key="img.seq" class="pv__shot">
              <img
                v-if="img.assetId"
                :src="`/api/v1/assets/${img.assetId}/raw`"
                :alt="`结果 ${img.seq + 1}`"
                loading="lazy"
              />
              <div v-else class="pv__shotfail hint">
                {{ img.error?.message || '未产出' }}
              </div>
            </figure>
          </div>
          <div v-else-if="jobs.busy" class="hint">{{ jobs.stage || '生成中…' }}</div>
        </div>
      </Card>
    </section>

    <!-- 资产选择 -->
    <Modal
      :open="pickerOpen"
      title="从资产库添加参考图"
      size="lg"
      @close="pickerOpen = false"
    >
      <div v-if="!allAssets.length" class="hint">
        资产库还是空的。先去「资产库」上传一些图。
      </div>
      <div v-else class="pv__picker">
        <button
          v-for="a in allAssets"
          :key="a.id"
          type="button"
          class="pv__pick"
          :disabled="body.refs.some((r) => r.assetId === a.id)"
          @click="addRefFromAsset(a.id)"
        >
          <img :src="assetThumbURL(a.id)" :alt="a.id" loading="lazy" />
          <span class="hint">{{ a.width }}×{{ a.height }}</span>
        </button>
      </div>
      <template #footer>
        <Button variant="secondary" @click="pickerOpen = false">完成</Button>
      </template>
    </Modal>
  </div>
</template>

<style scoped>
.pv {
  display: grid;
  grid-template-columns: 220px minmax(0, 1fr) 320px;
  gap: var(--space-4);
  height: 100%;
  min-height: 0;
}

.pv__list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-height: 0;
  overflow-y: auto;
  padding-right: var(--space-1);
}

.pv__listhead {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.pv__items {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin: 0;
  padding: 0;
  list-style: none;
}

.pv__item {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  width: 100%;
  padding: var(--space-2) var(--space-3);
  border: var(--border-w) solid transparent;
  border-radius: var(--radius-md);
  background: var(--surface-1);
  color: var(--text-1);
  text-align: left;
  cursor: pointer;
  transition: background-color var(--dur-fast) var(--ease-out);
}

.pv__item:hover {
  background: var(--surface-2);
}

.pv__item.is-active {
  border-color: var(--accent);
  background: var(--accent-bg);
}

.pv__itemname {
  font-size: var(--fs-sm);
  font-weight: var(--fw-medium);
}

.pv__itemmeta {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.pv__editor,
.pv__result {
  min-width: 0;
  min-height: 0;
  overflow-y: auto;
}

.pv__runbar {
  margin-bottom: var(--space-2);
}

.pv__batch {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin-top: var(--space-2);
}

.pv__batch .input {
  width: 64px;
}

.pv__job {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.pv__shots {
  display: grid;
  grid-template-columns: 1fr;
  gap: var(--space-2);
}

.pv__shot {
  margin: 0;
  border: var(--border-w) solid var(--border);
  border-radius: var(--radius-md);
  overflow: hidden;
  background: var(--surface-2);
}

.pv__shot img {
  display: block;
  width: 100%;
  height: auto;
}

.pv__shotfail {
  padding: var(--space-3);
}

.pv__picker {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(96px, 1fr));
  gap: var(--space-2);
  max-height: 60vh;
  overflow-y: auto;
}

.pv__pick {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding: var(--space-1);
  border: var(--border-w) solid var(--border);
  border-radius: var(--radius-md);
  background: var(--surface-1);
  cursor: pointer;
}

.pv__pick:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.pv__pick img {
  width: 100%;
  aspect-ratio: 1;
  object-fit: cover;
  border-radius: var(--radius-sm);
  background: var(--surface-3);
}

@media (max-width: 1100px) {
  .pv {
    grid-template-columns: 200px minmax(0, 1fr);
  }
  .pv__result {
    grid-column: 1 / -1;
  }
}

@media (max-width: 720px) {
  .pv {
    grid-template-columns: 1fr;
  }
}
</style>
