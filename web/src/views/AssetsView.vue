<script setup lang="ts">
/**
 * 资产库：本地落盘的图片（上传 / 生成 / 远程链接）。
 *
 * 支持按来源筛选 + 关键词搜索，卡片操作直接服务于工作台：
 * 「作为参考图」「送入编辑」「下载」「删除」。
 */
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import Card from '@/components/ui/Card.vue'
import Dropzone from '@/components/ui/Dropzone.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Input from '@/components/ui/Input.vue'
import Lightbox from '@/components/ui/Lightbox.vue'
import Modal from '@/components/ui/Modal.vue'
import Select from '@/components/ui/Select.vue'
import Skeleton from '@/components/ui/Skeleton.vue'
import { useToast } from '@/components/ui/useToast'
import { assetThumbURL } from '@/api/client'
import { useAssetsStore, isImageFile } from '@/stores/assets'
import { useStudioStore } from '@/stores/studio'
import { useProvidersStore } from '@/stores/providers'
import type { Asset, AssetSource } from '@/types'
import { copyText, downloadAsset, formatBytes, formatTime } from '@/components/domain/useApiError'

const router = useRouter()
const assets = useAssetsStore()
const studio = useStudioStore()
const providers = useProvidersStore()
const toast = useToast()

const items = ref<Asset[]>([])
const source = ref<AssetSource | ''>('')
const q = ref('')
const loading = ref(false)
const total = ref(0)
const lightboxOpen = ref(false)
const lightboxId = ref('')
const confirmDelete = ref<Asset | null>(null)
const uploadOpen = ref(false)

const sourceOptions = [
  { value: '', label: '全部来源' },
  { value: 'upload', label: '上传' },
  { value: 'generated', label: '生成' },
  { value: 'remote_url', label: '远程链接' },
]

async function reload(): Promise<void> {
  loading.value = true
  try {
    const res = await assets.list({
      source: source.value || undefined,
      q: q.value || undefined,
    })
    items.value = res.items
    total.value = res.total
  } finally {
    loading.value = false
  }
}

onMounted(reload)
watch([source], reload)

function onSearch(): void {
  void reload()
}

const filtered = computed(() => {
  const kw = q.value.trim().toLowerCase()
  if (!kw) return items.value
  // 后端 q 只搜 sha/来源，这里在前端再补一层 id 前缀匹配。
  return items.value.filter((a) => a.id.toLowerCase().includes(kw))
})

const grouped = computed(() => ({
  upload: items.value.filter((a) => a.source === 'upload').length,
  generated: items.value.filter((a) => a.source === 'generated').length,
  remote: items.value.filter((a) => a.source === 'remote_url').length,
}))

// ------------------------------------------------------------------ 上传
async function onFiles(files: File[]): Promise<void> {
  const images = files.filter(isImageFile)
  const rejected = files.length - images.length
  if (rejected > 0) toast.warn(`已忽略 ${rejected} 个非图片文件`)
  let ok = 0
  for (const f of images) {
    const a = await assets.upload(f)
    if (a) ok += 1
    else toast.error(assets.error || `${f.name} 上传失败`)
  }
  if (ok > 0) {
    toast.success(`已上传 ${ok} 张`)
    uploadOpen.value = false
    await reload()
  }
}

// ------------------------------------------------------------------ 操作
function openLightbox(id: string): void {
  lightboxId.value = id
  lightboxOpen.value = true
}

const lightboxSrc = computed(() =>
  lightboxId.value ? `/api/v1/assets/${encodeURIComponent(lightboxId.value)}/raw` : '',
)

function asRef(a: Asset): void {
  if (studio.refs.length >= providers.maxRefs) {
    toast.warn(`参考图最多 ${providers.maxRefs} 张`)
    return
  }
  const added = studio.addAssetRef(a.id, `资产 ${a.id.slice(0, 6)}`)
  toast[added ? 'success' : 'info'](added ? '已加入参考图，回到工作台即可使用' : '已在参考图列表中')
}

function toEdit(a: Asset): void {
  if (!providers.canImg2Img) {
    toast.warn('当前模型不支持图生图，请先在顶栏切换到 SenseNova')
    return
  }
  studio.clearRefs()
  studio.addAssetRef(a.id, `资产 ${a.id.slice(0, 6)}`)
  studio.mode = 'img2img'
  void router.push('/studio')
  toast.info('已送入编辑')
}

async function onCopyId(a: Asset): Promise<void> {
  const ok = await copyText(a.id)
  toast[ok ? 'success' : 'error'](ok ? '资产 ID 已复制' : '复制失败')
}

async function onDelete(a: Asset): Promise<void> {
  const ok = await assets.remove(a.id)
  if (!ok) {
    toast.error(assets.error || '删除失败')
    return
  }
  items.value = items.value.filter((x) => x.id !== a.id)
  total.value = Math.max(0, total.value - 1)
  confirmDelete.value = null
  toast.success('已删除')
}

function sourceTone(s: AssetSource): 'accent' | 'ok' | 'warn' {
  if (s === 'generated') return 'ok'
  if (s === 'upload') return 'accent'
  return 'warn'
}

/** 远程链接类资产可能过期（PLAN §3.5 第 6 步）。 */
function expired(a: Asset): boolean {
  if (!a.expiresAt) return false
  const t = Date.parse(a.expiresAt)
  return !Number.isNaN(t) && t < Date.now()
}
</script>

<template>
  <div class="assets stack">
    <header class="row row--between row--wrap">
      <div class="stack stack--1">
        <h2 class="t-xl">资产库</h2>
        <p class="hint">
          共 {{ total }} 张 · 上传 {{ grouped.upload }} · 生成 {{ grouped.generated }} ·
          远程 {{ grouped.remote }}
        </p>
      </div>

      <div class="row row--tight">
        <Input v-model="q" placeholder="搜索资产 ID" size="sm" clearable @enter="onSearch" />
        <Select v-model="source" :options="sourceOptions" size="sm" />
        <Button variant="secondary" size="sm" @click="onSearch">搜索</Button>
        <Button variant="primary" size="sm" @click="uploadOpen = !uploadOpen">
          {{ uploadOpen ? '收起上传' : '上传图片' }}
        </Button>
      </div>
    </header>

    <Dropzone
      v-if="uploadOpen"
      :disabled="assets.busy"
      :loading="assets.busy"
      hint="支持拖拽 / 粘贴 / 点选，单张上限 20MB"
      @files="onFiles"
      @reject="toast.warn($event)"
    />

    <div v-if="loading && !items.length" class="assets__grid">
      <div v-for="i in 8" :key="`sk-${i}`" class="assets__skeleton">
        <Skeleton variant="rect" height="100%" />
      </div>
    </div>

    <EmptyState
      v-else-if="!filtered.length"
      title="还没有资产"
      desc="上传一张图片，或回到工作台生成一张 —— 落盘后的图片都会出现在这里，可随时作为参考图复用。"
    >
      <template #actions>
        <Button variant="primary" size="md" @click="uploadOpen = true">上传图片</Button>
        <Button variant="secondary" size="md" @click="router.push('/studio')">
          去工作台生成
        </Button>
      </template>
    </EmptyState>

    <div v-else class="assets__grid">
      <Card v-for="a in filtered" :key="a.id" hover class="assets__card">
        <template #media>
          <div class="assets__thumb" @click="openLightbox(a.id)">
            <img :src="assetThumbURL(a.id)" :alt="`资产 ${a.id}`" loading="lazy" />
            <Badge :tone="sourceTone(a.source)" size="sm" class="assets__badge">
              {{ assets.label(a.source) }}
            </Badge>
          </div>
        </template>

        <div class="stack stack--1">
          <div class="row row--tight">
            <span class="mono t-xs truncate">{{ a.id }}</span>
            <Button variant="ghost" size="sm" class="ml-auto" @click="onCopyId(a)">
              复制 ID
            </Button>
          </div>
          <p class="hint">
            {{ a.width }}×{{ a.height }} · {{ formatBytes(a.bytes) }} ·
            {{ formatTime(a.createdAt) }}
          </p>
          <p v-if="expired(a)" class="error-text">
            远程链接已过期，需重新生成
          </p>
        </div>

        <template #foot>
          <Button variant="secondary" size="sm" @click="asRef(a)">作为参考图</Button>
          <Button variant="secondary" size="sm" :disabled="!providers.canImg2Img" @click="toEdit(a)">
            送入编辑
          </Button>
          <Button variant="ghost" size="sm" @click="downloadAsset(a.id, `imgen-${a.id}.png`)">
            下载
          </Button>
          <Button
            variant="danger-ghost"
            size="sm"
            class="ml-auto"
            :loading="assets.removing[a.id]"
            @click="confirmDelete = a"
          >
            删除
          </Button>
        </template>
      </Card>
    </div>

    <Lightbox
      :open="lightboxOpen"
      :src="lightboxSrc"
      :caption="`资产 ${lightboxId}`"
      @close="lightboxOpen = false"
    />

    <Modal
      :open="confirmDelete !== null"
      title="删除这张资产？"
      size="sm"
      @close="confirmDelete = null"
    >
      <p class="t-md text-2">
        该图片会从本地资产库移除，无法恢复。引用它的历史任务将显示为空图位。
      </p>
      <template #foot>
        <Button variant="ghost" size="md" @click="confirmDelete = null">取消</Button>
        <Button
          variant="danger"
          size="md"
          @click="confirmDelete && onDelete(confirmDelete)"
        >
          确认删除
        </Button>
      </template>
    </Modal>
  </div>
</template>

<style scoped>
.assets {
  height: 100%;
  min-height: 0;
}

.assets__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: var(--space-4);
}

.assets__card {
  overflow: hidden;
}

.assets__thumb {
  position: relative;
  aspect-ratio: 1 / 1;
  background: var(--surface-2);
  cursor: zoom-in;
}

.assets__thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.assets__badge {
  position: absolute;
  top: var(--space-2);
  left: var(--space-2);
}

.assets__skeleton {
  aspect-ratio: 1 / 1;
  border-radius: var(--radius-lg);
  overflow: hidden;
}
</style>
