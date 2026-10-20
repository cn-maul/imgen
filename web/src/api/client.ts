/**
 * 轻量 fetch 封装：统一 /api/v1 前缀、归一化错误体、超时与 JSON 解析。
 * 不引入 axios —— 原生 fetch 足够。
 */
import {
  ApiError,
  type ApiErrorPayload,
  type Asset,
  type AssetFilter,
  type AssetListResult,
  type Caps,
  type GenerateRequest,
  type Health,
  type Job,
  type JobFilter,
  type JobListResult,
  type PatchProviderInput,
  type PresetInput,
  type PromptPreset,
  type ProviderInfo,
  type Settings,
  type SettingsPatch,
  type TestProviderResult,
  type UsageResult,
} from '@/types'

const BASE = '/api/v1'

/**
 * 后端未就绪 / 某模型缺 caps 时的兜底矩阵。
 * 放在 client 层，保证 Store 与 Panel 回退逻辑一致。
 */
const FALLBACK_CAPS: Caps = {
  img2img: true,
  compose: true,
  noWatermark: true,
  promptExtend: true,
  outputFormats: ['png', 'jpeg', 'webp'],
  maxRefs: 5,
  ratios: ['1:1', '3:4', '4:3', '16:9', '9:16', '2:3', '3:2', '21:9'],
  exactSize: false,
  nativeMultiImage: false,
  timeoutSec: 180,
}

export interface RequestOptions {
  signal?: AbortSignal
  timeoutMs?: number
}

/** 后端错误体可能是 {error:{...}} 或裸 ApiErrorPayload，两种都容错。 */
function extractPayload(raw: unknown, status: number): ApiErrorPayload {
  const asObj = raw as Record<string, unknown> | null
  const inner = (asObj?.error ?? asObj) as Record<string, unknown> | null
  if (inner && typeof inner === 'object' && typeof inner.code === 'string') {
    return {
      code: inner.code as ApiErrorPayload['code'],
      message: String(inner.message ?? inner.code),
      provider: inner.provider as string | undefined,
      upstreamStatus: inner.upstreamStatus as number | undefined,
      upstreamType: inner.upstreamType as string | undefined,
      retryable: Boolean(inner.retryable),
    }
  }
  return {
    code: 'INTERNAL',
    message: `请求失败 (HTTP ${status})`,
    retryable: status >= 500,
  }
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  opts: RequestOptions = {},
): Promise<T> {
  const url = BASE + path
  const controller = new AbortController()
  const timeoutMs = opts.timeoutMs ?? 30_000
  const timer = setTimeout(() => controller.abort(), timeoutMs)

  // 外部取消也要能中断内部请求。
  const onAbort = () => controller.abort()
  opts.signal?.addEventListener('abort', onAbort)

  const init: RequestInit = {
    method,
    signal: controller.signal,
    headers: {},
  }
  if (body !== undefined) {
    init.headers = { 'Content-Type': 'application/json' }
    init.body = JSON.stringify(body)
  }

  let res: Response
  try {
    res = await fetch(url, init)
  } catch (e) {
    if (controller.signal.aborted) {
      throw new ApiError(0, { code: 'INTERNAL', message: '请求已取消', retryable: true })
    }
    throw new ApiError(0, {
      code: 'UPSTREAM_UNAVAILABLE',
      message: `网络错误: ${(e as Error).message}`,
      retryable: true,
    })
  } finally {
    clearTimeout(timer)
    opts.signal?.removeEventListener('abort', onAbort)
  }

  const text = await res.text()
  let data: unknown = null
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = null
    }
  }

  if (!res.ok) {
    throw new ApiError(res.status, extractPayload(data, res.status))
  }
  return data as T
}

function query(params: Record<string, unknown> = {}): string {
  const usp = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === '') continue
    usp.set(k, String(v))
  }
  const s = usp.toString()
  return s ? `?${s}` : ''
}

export const api = {
  // ------------------------------------------------------------ 系统
  health: (opts?: RequestOptions) => request<Health>('GET', '/health', undefined, opts),

  getSettings: async (): Promise<Settings> => {
    const raw = await request<Settings & { queueDepth?: number }>('GET', '/settings')
    const s = raw as Settings & { settings?: Settings }
    return s.settings ?? s
  },

  putSettings: async (patch: SettingsPatch): Promise<Settings> => {
    // PUT 回显 {settings:{...}}（含 queueDepth），GET 是裸对象 —— 两种都兼容。
    const res = await request<Settings & { settings?: Settings }>('PUT', '/settings', patch)
    return res.settings ?? res
  },

// ---------------------------------------------------------------- 列表
listProviders: async (): Promise<ProviderInfo[]> => {
  interface RawProvider extends Omit<ProviderInfo, 'caps' | 'defaultCaps' | 'defaultModel'> {
    caps?: Record<string, Caps>
  }
  const res = await request<{ providers: RawProvider[] }>('GET', '/providers')
  // 后端只给「每模型一份 caps」，defaultModel/defaultCaps 由前端派生，
  // 让 Panel 与 Shell 不必各自回退。
  return (res.providers ?? []).map((p) => {
    const caps = p.caps ?? {}
    const defaultModel = p.models?.[0]?.id ?? ''
    return {
      ...p,
      caps,
      defaultModel,
      defaultCaps: caps[defaultModel] ?? FALLBACK_CAPS,
    }
  })
},

patchProvider: async (id: string, patch: PatchProviderInput): Promise<ProviderInfo> => {
  // PATCH 回显 {provider:{...}}，与列表同构，前端可直接合并。
  const res = await request<{ provider: ProviderInfo }>(
    'PATCH', `/providers/${encodeURIComponent(id)}`, patch)
  return res.provider
},

testProvider: (id: string) =>
  request<TestProviderResult>('POST', `/providers/${encodeURIComponent(id)}/test`, {}, { timeoutMs: 120_000 }),

  // ---------------------------------------------------------------- 任务
  /**
   * 建任务：后端回 202 `{jobId,status}`（只有 id），但调用方要一个完整 Job
   * 才能立刻渲染条目并起 SSE。故回读一次 GET /jobs/:id。
   * 回读失败不抛 —— 任务确实已提交，交给 SSE/轮询兜底。
   */
  createJob: async (req: GenerateRequest): Promise<Job> => {
    const created = await request<{ jobId: string; status: string }>('POST', '/jobs', req)
    const stub: Job = {
      id: created.jobId,
      provider: req.provider,
      model: req.model,
      mode: req.mode,
      prompt: req.prompt,
      params: req,
      status: (created.status || 'queued') as Job['status'],
      progress: 0,
      count: req.count || 1,
      latencyMs: 0,
      images: [],
      createdAt: new Date().toISOString(),
    }
    try {
      return await api.getJob(created.jobId)
    } catch {
      return stub
    }
  },

  getJob: (id: string) => request<Job>('GET', `/jobs/${encodeURIComponent(id)}`),

  listJobs: (f: JobFilter = {}) =>
    request<JobListResult>('GET', '/jobs' + query(f as Record<string, unknown>)),

  cancelJob: (id: string) =>
    request<Job>('POST', `/jobs/${encodeURIComponent(id)}/cancel`, {}),

  // ---------------------------------------------------------------- 资产
  listAssets: (f: AssetFilter = {}) =>
    request<AssetListResult>('GET', '/assets' + query(f as Record<string, unknown>)),

  /** form 字段名 'file'，与后端约定一致。后端回 {items:[...]}，取首项。 */
  uploadAsset: async (file: File): Promise<Asset> => {
    const fd = new FormData()
    fd.append('file', file)
    const res = await fetch(BASE + '/assets', { method: 'POST', body: fd })
    const data = await res.json().catch(() => null)
    if (!res.ok) throw new ApiError(res.status, extractPayload(data, res.status))
    const asList = data as { items?: Asset[] } | null
    if (asList?.items?.length) return asList.items[0]
    return data as Asset
  },

  deleteAsset: (id: string) =>
    request<void>('DELETE', `/assets/${encodeURIComponent(id)}`),

  // ------------------------------------------------------------ 用量
  usage: (days = 14) =>
    request<UsageResult>('GET', '/usage' + query({ days })),

  // ------------------------------------------------------------ 提示词预设
  listPresets: async (): Promise<PromptPreset[]> => {
    const res = await request<{ presets: PromptPreset[] }>('GET', '/presets')
    return res.presets ?? []
  },

  createPreset: async (input: PresetInput): Promise<PromptPreset> => {
    const res = await request<{ preset: PromptPreset }>('POST', '/presets', input)
    return res.preset
  },

  updatePreset: async (id: string, input: PresetInput): Promise<PromptPreset> => {
    const res = await request<{ preset: PromptPreset }>(
      'PUT', `/presets/${encodeURIComponent(id)}`, input)
    return res.preset
  },

  deletePreset: (id: string) =>
    request<void>('DELETE', `/presets/${encodeURIComponent(id)}`),
}

/** 图片访问地址（走后端，非 data URI）。 */
export function assetRawURL(id: string): string {
  return `${BASE}/assets/${encodeURIComponent(id)}/raw`
}

export function assetThumbURL(id: string): string {
  return `${BASE}/assets/${encodeURIComponent(id)}/thumb`
}

export { ApiError }
