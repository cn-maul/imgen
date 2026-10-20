/**
 * 本地上传接口 + 资产列表 + 元信息缓存。
 *
 * 依赖 `window.__IMGEN__.maxUploadMB` 做前端预检，超限直接拦下，
 * 免得大图白跑一趟网络。
 */
import { ref } from 'vue'
import { defineStore } from 'pinia'
import { ApiError, api } from '@/api/client'
import type { Asset, AssetFilter, AssetListResult, AssetSource } from '@/types'

export interface UploadTask {
  key: string
  file: File
  progress: number
  status: 'pending' | 'uploading' | 'done' | 'error'
  error?: string
  asset?: Asset
}

// window.__IMGEN__ 的声明在 web/env.d.ts（ImgenBoot），这里直接用。

/** 上传体积上限兜底：后端注入值缺失时用 PLAN 里定的 20MB。 */
const DEFAULT_MAX_MB = 20

export function maxUploadMB(): number {
  return window.__IMGEN__?.maxUploadMB ?? DEFAULT_MAX_MB
}

export function isImageFile(f: File): boolean {
  return f.type.startsWith('image/') || /\.(png|jpe?g|webp|gif|bmp|avif)$/i.test(f.name)
}

export const useAssetsStore = defineStore('assets', () => {
  const busy = ref(false)
  const loading = ref(false)
  const error = ref('')
  const lastUploaded = ref<Asset | null>(null)
  const removing = ref<Record<string, boolean>>({})
  /** assetId → 宽高体积，按需查询后缓存，供结果卡片显示尺寸。 */
  const meta = ref<Record<string, { width: number; height: number; bytes: number }>>({})

  async function upload(file: File, task?: UploadTask): Promise<Asset | null> {
    busy.value = true
    error.value = ''
    if (task) {
      task.status = 'uploading'
      task.progress = 10
    }
    try {
      const max = maxUploadMB()
      if (file.size > max * 1024 * 1024) {
        throw new Error(`文件超过 ${max}MB 上限`)
      }
      if (!isImageFile(file)) throw new Error('仅支持图片文件')
      const asset = await api.uploadAsset(file)
      lastUploaded.value = asset
      if (task) {
        task.status = 'done'
        task.progress = 100
        task.asset = asset
      }
      return asset
    } catch (e) {
      const msg =
        e instanceof ApiError && e.code === 'UPSTREAM_QUOTA'
          ? '存储空间或配额不足'
          : (e as Error).message || '上传失败'
      error.value = msg
      if (task) {
        task.status = 'error'
        task.error = msg
      }
      return null
    } finally {
      busy.value = false
    }
  }

  /** 从 dataTransfer / paste 事件里抽出图片文件（含 URL 条目降级为远程引用）。 */
  function pickFiles(dt: DataTransfer | null): File[] {
    if (!dt) return []
    const files: File[] = []
    if (dt.files?.length) {
      for (const f of Array.from(dt.files)) files.push(f)
    } else if (dt.items?.length) {
      for (const it of Array.from(dt.items)) {
        if (it.kind === 'file') {
          const f = it.getAsFile()
          if (f) files.push(f)
        }
      }
    }
    return files.filter(isImageFile)
  }

  /** 从拖拽/粘贴里抽公网 URL（不上传，直接作为 Ref.url 下发）。 */
  function pickUrls(dt: DataTransfer | null): string[] {
    if (!dt) return []
    const raw = dt.getData?.('text/uri-list') || dt.getData?.('text/plain') || ''
    return raw
      .split(/\s+/)
      .map((s) => s.trim())
      .filter((s) => /^https?:\/\//i.test(s))
  }

  /**
   * 列表查询。视图层传 filter，结果留在 store（资产库页与「作为参考图」共用）。
   * 不分页无限滚动：一期资产量级是几百张，一次拉 200 足够。
   */
  async function list(f: AssetFilter = {}): Promise<AssetListResult> {
    loading.value = true
    try {
      const res = await api.listAssets({ limit: 200, ...f })
      for (const a of res.items) {
        meta.value[a.id] = { width: a.width, height: a.height, bytes: a.bytes }
      }
      return res
    } catch (e) {
      error.value = (e as Error).message || '资产加载失败'
      return { items: [], total: 0, limit: 0, offset: 0 }
    } finally {
      loading.value = false
    }
  }

  /**
   * 按需补齐宽高/体积，结果进 meta 缓存。
   *
   * /assets 没有按 id 批量查询的能力，但结果图都是刚生成的，必然落在按时间倒序
   * 的前几页里 —— 拉一页（默认 100）建立 id → Asset 索引即可命中绝大多数情况。
   * 查不到就算了：尺寸只是装饰信息，失败不许影响主流程。
   */
  async function loadMeta(ids: string[]): Promise<void> {
    const missing = ids.filter((id) => !(id in meta.value))
    if (!missing.length) return
    const want = new Set(missing)
    try {
      const res = await api.listAssets({ limit: 100 })
      for (const a of res.items) {
        if (want.has(a.id)) {
          meta.value[a.id] = { width: a.width, height: a.height, bytes: a.bytes }
        }
      }
    } catch {
      // 忽略：尺寸是装饰信息。
    }
  }

  async function remove(id: string): Promise<boolean> {
    removing.value[id] = true
    try {
      await api.deleteAsset(id)
      return true
    } catch (e) {
      error.value = (e as Error).message || '删除失败'
      return false
    } finally {
      removing.value[id] = false
    }
  }

  function label(source: AssetSource): string {
    switch (source) {
      case 'upload':
        return '上传'
      case 'generated':
        return '生成'
      case 'remote_url':
        return '远程链接'
      default:
        return source
    }
  }

  return {
    busy,
    loading,
    error,
    lastUploaded,
    removing,
    meta,
    upload,
    pickFiles,
    pickUrls,
    list,
    loadMeta,
    remove,
    label,
  }
})
