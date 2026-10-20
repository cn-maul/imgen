<script setup lang="ts">
/**
 * 参考图槽位区：拖拽 / 粘贴 / 点选上传 + 缩略图排序，首图为主图。
 *
 * 三条上传路径都收敛到这里：
 *   - drop 事件（文件 & 公网 URL）
 *   - paste 事件（window 级监听，画布聚焦时粘贴剪贴板图片）
 *   - 点选（隐藏 input[type=file]）
 * 上传成功后落到 assets 表，只把 assetId 存进草稿 —— 请求体不发二进制。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import Progress from '@/components/ui/Progress.vue'
import { assetThumbURL } from '@/api/client'
import { useAssetsStore } from '@/stores/assets'
import { useStudioStore, type StudioRef } from '@/stores/studio'
import { useToast } from '@/components/ui/useToast'
import { maxUploadMB } from '@/stores/assets'

const props = defineProps<{
  max: number
  disabled?: boolean
  /** 生成中：禁止增删参考图，避免改到正在跑的任务。 */
  busy?: boolean
}>()

const emit = defineEmits<{
  (e: 'urls', urls: string[]): void
}>()

const studio = useStudioStore()
const assets = useAssetsStore()
const toast = useToast()

const draggingIndex = ref<number>(-1)
const overIndex = ref<number>(-1)
const fileInput = ref<HTMLInputElement | null>(null)
const zoneOver = ref(false)
const uploading = ref(false)
const uploadCount = ref(0)
const uploadDone = ref(0)

const refs = computed<StudioRef[]>(() => studio.refs)
const full = computed(() => refs.value.length >= props.max)
const acceptCount = computed(() =>
  props.max > 0 ? Math.max(0, props.max - refs.value.length) : 0,
)

function thumbOf(r: StudioRef): string {
  return r.assetId ? assetThumbURL(r.assetId) : (r.url ?? '')
}

// ------------------------------------------------------------------ 上传
async function ingestFiles(files: File[]): Promise<void> {
  if (!files.length || acceptCount.value <= 0) return
  const candidates = files.slice(0, acceptCount.value)
  const overflow = files.length - candidates.length
  if (overflow > 0) {
    toast.warn(`最多 ${props.max} 张参考图，忽略多余的 ${overflow} 张`)
  }

  uploading.value = true
  uploadCount.value = candidates.length
  uploadDone.value = 0
  try {
    for (const f of candidates) {
      const asset = await assets.upload(f)
      if (asset) {
        studio.addAssetRef(asset.id, f.name || `参考图 ${asset.id.slice(0, 6)}`)
        uploadDone.value += 1
      } else {
        toast.error(assets.error || `${f.name} 上传失败`)
      }
    }
  } finally {
    uploading.value = false
  }
}

function onPick(): void {
  fileInput.value?.click()
}

function onFileChange(ev: Event): void {
  const el = ev.target as HTMLInputElement
  const files = Array.from(el.files ?? [])
  el.value = ''
  void ingestFiles(files)
}

function onDrop(ev: DragEvent): void {
  ev.preventDefault()
  if (draggingIndex.value >= 0) {
    commitReorder()
    return
  }
  zoneOver.value = false
  if (props.disabled || props.busy) return
  const urls = assets.pickUrls(ev.dataTransfer)
  if (urls.length) {
    for (const u of urls.slice(0, acceptCount.value)) studio.addUrlRef(u)
    emit('urls', urls)
    return
  }
  void ingestFiles(assets.pickFiles(ev.dataTransfer))
}

function onDragOver(ev: DragEvent): void {
  ev.preventDefault()
  zoneOver.value = true
}

function onDragLeave(): void {
  zoneOver.value = false
}

// ------------------------------------------------------------------ 粘贴
function onPaste(ev: ClipboardEvent): void {
  if (props.disabled || props.busy || acceptCount.value <= 0) return
  const target = ev.target as HTMLElement | null
  // 输入框里粘贴应该是粘文本，不该抢成上传。
  if (target && /^(INPUT|TEXTAREA)$/.test(target.tagName)) return
  const dt = ev.clipboardData
  const files = assets.pickFiles(dt)
  if (files.length) {
    ev.preventDefault()
    void ingestFiles(files)
    return
  }
  const urls = assets.pickUrls(dt)
  if (urls.length) {
    ev.preventDefault()
    for (const u of urls.slice(0, acceptCount.value)) studio.addUrlRef(u)
  }
}

onMounted(() => {
  window.addEventListener('paste', onPaste)
})
onBeforeUnmount(() => {
  window.removeEventListener('paste', onPaste)
})

// ------------------------------------------------------------------ 排序
function onItemDragStart(i: number, ev: DragEvent): void {
  draggingIndex.value = i
  ev.dataTransfer?.setData('text/plain', String(i))
  if (ev.dataTransfer) ev.dataTransfer.effectAllowed = 'move'
}

function onItemDragOver(i: number, ev: DragEvent): void {
  ev.preventDefault()
  if (draggingIndex.value < 0) return
  overIndex.value = i
}

function commitReorder(): void {
  const from = draggingIndex.value
  const to = overIndex.value
  draggingIndex.value = -1
  overIndex.value = -1
  zoneOver.value = false
  if (from >= 0 && to >= 0) studio.moveRef(from, to)
}

function move(i: number, delta: number): void {
  studio.moveRef(i, i + delta)
}

function remove(i: number): void {
  studio.removeRef(i)
}
</script>

<template>
  <section class="reftray">
    <div class="row row--tight row--between">
      <div class="row row--tight">
        <span class="label">参考图</span>
        <Badge size="sm" :tone="full ? 'warn' : 'neutral'">{{ refs.length }} / {{ max }}</Badge>
        <span v-if="refs.length > 0" class="hint">第一张为主图</span>
      </div>

      <div class="row row--tight">
        <Button
          v-if="refs.length > 1"
          variant="ghost"
          size="sm"
          :disabled="busy"
          title="清空参考图"
          @click="studio.clearRefs()"
        >
          清空
        </Button>
        <Button
          variant="secondary"
          size="sm"
          :disabled="disabled || busy || full || max === 0"
          :title="full ? `已达到 ${max} 张上限` : '拖拽、粘贴或点选上传'"
          @click="onPick"
        >
          添加参考图
        </Button>
      </div>
    </div>

    <input
      ref="fileInput"
      type="file"
      accept="image/*"
      multiple
      class="dropzone__native"
      @change="onFileChange"
    />

    <div
      class="reft__zone"
      :class="{
        'is-over': zoneOver,
        'is-disabled': disabled || busy || max === 0,
      }"
      @drop="onDrop"
      @dragover="onDragOver"
      @dragleave="onDragLeave"
    >
      <ul v-if="refs.length" class="reft__grid" role="list">
        <li
          v-for="(r, i) in refs"
          :key="r.key"
          class="reft__item"
          :class="{
            'is-dragging': draggingIndex === i,
            'is-target': overIndex === i && draggingIndex !== i,
          }"
          draggable="true"
          :aria-label="`参考图 ${i + 1}${i === 0 ? '（主图）' : ''}`"
          @dragstart="onItemDragStart(i, $event)"
          @dragover="onItemDragOver(i, $event)"
          @dragend="commitReorder"
          @drop.stop="onDrop"
        >
          <div class="thumb reft__thumb">
            <img :src="thumbOf(r)" :alt="r.name" loading="lazy" />
            <span class="reft__seq">{{ i + 1 }}</span>
            <Badge v-if="i === 0" tone="solid-accent" size="sm" class="reft__badge">
              第 1 张（主图）
            </Badge>
          </div>

          <div class="reft__meta">
            <span class="t-xs truncate text-2" :title="r.name">{{ r.name }}</span>
            <div class="row row--tight reft__ops">
              <button
                type="button"
                class="btn btn--ghost btn--sm btn--icon"
                :disabled="i === 0"
                title="前移"
                @click="move(i, -1)"
              >
                ‹
              </button>
              <button
                type="button"
                class="btn btn--ghost btn--sm btn--icon"
                :disabled="i === refs.length - 1"
                title="后移"
                @click="move(i, 1)"
              >
                ›
              </button>
              <button
                type="button"
                class="btn btn--danger-ghost btn--sm btn--icon"
                title="移除"
                @click="remove(i)"
              >
                ✕
              </button>
            </div>
          </div>
        </li>
      </ul>

      <div v-else class="empty empty--compact">
        <p class="empty__desc">
          拖拽图片到这里、<span class="kbd">Ctrl</span>+<span class="kbd">V</span> 粘贴，或点右上角按钮选择文件。
          <br />
          单张上限 {{ maxUploadMB() }}MB。
        </p>
      </div>

      <div v-if="uploading" class="reft__upload">
        <Progress :value="uploadCount ? (uploadDone / uploadCount) * 100 : 0" size="sm" />
        <span class="hint">上传中 {{ uploadDone }}/{{ uploadCount }}</span>
      </div>
    </div>

    <p v-if="max === 0" class="hint">当前模型不支持参考图，仅可用于文生图。</p>
    <p v-else class="hint">
      超出 {{ max }} 张的部分会被忽略。
      <slot name="hint" />
    </p>
  </section>
</template>

<style scoped>
.reftray {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}

.reft__zone {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-height: 96px;
  padding: var(--space-3);
  border: 1px dashed var(--border);
  border-radius: var(--radius-lg);
  background: var(--surface-1);
  transition:
    border-color var(--dur-fast) var(--ease-out),
    background-color var(--dur-fast) var(--ease-out);
}

.reft__zone.is-over {
  border-color: var(--accent);
  border-style: solid;
  background: var(--accent-bg);
}

.reft__zone.is-disabled {
  opacity: 0.6;
  pointer-events: none;
}

.reft__grid {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.reft__item {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  width: 104px;
  padding: var(--space-1);
  border: var(--border-w) solid var(--border);
  border-radius: var(--radius-md);
  background: var(--surface-2);
  cursor: grab;
  transition:
    border-color var(--dur-fast) var(--ease-out),
    transform var(--dur-fast) var(--ease-out),
    opacity var(--dur-fast) var(--ease-out);
}

.reft__item.is-dragging {
  opacity: 0.4;
  cursor: grabbing;
}

.reft__item.is-target {
  border-color: var(--accent);
}

.reft__thumb {
  position: relative;
  aspect-ratio: 1 / 1;
}

.reft__seq {
  position: absolute;
  top: var(--space-1);
  left: var(--space-1);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border-radius: var(--radius-full);
  background: var(--surface-0);
  color: var(--text-1);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
}

.reft__badge {
  position: absolute;
  bottom: var(--space-1);
  left: 50%;
  transform: translateX(-50%);
  white-space: nowrap;
}

.reft__meta {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.reft__ops {
  gap: 2px;
}

.reft__upload {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}
</style>
