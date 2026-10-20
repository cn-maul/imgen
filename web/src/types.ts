/**
 * 与后端 internal/httpapi DTO 一一对应的类型契约。
 * 字段名与 Go 侧 JSON tag 严格对齐，改这里必须同步改 Go。
 */
import type { PresetRef } from '@/components/domain/promptRender'

// ---------------------------------------------------------------- 领域枚举

export type ProviderID = 'sensenova' | 'agnes' | 'openai'

export type Mode = 'txt2img' | 'img2img' | 'compose'

export type Tier = '1K' | '2K' | '3K' | '4K'

export type Aspect =
  | '1:1' | '3:4' | '4:3' | '16:9'
  | '9:16' | '2:3' | '3:2' | '21:9'

export type Format = 'png' | 'jpeg' | 'webp'

export type JobStatus =
  | 'queued' | 'running' | 'partial'
  | 'done' | 'failed' | 'canceled'

export const ALL_ASPECTS: Aspect[] = [
  '1:1', '3:4', '4:3', '16:9', '9:16', '2:3', '3:2', '21:9',
]

export const ALL_TIERS: Tier[] = ['1K', '2K', '3K', '4K']

// ---------------------------------------------------------------- 错误

/** 归一化错误码，与 domain.Error.Code 一致。 */
export type ErrorCode =
  | 'BAD_REQUEST'
  | 'NOT_FOUND'
  | 'INTERNAL'
  | 'UPSTREAM_AUTH'
  | 'UPSTREAM_QUOTA'
  | 'UPSTREAM_REJECTED'
  | 'UPSTREAM_MODERATION'
  | 'UPSTREAM_TIMEOUT'
  | 'UPSTREAM_UNAVAILABLE'
  | 'UPSTREAM_UNPARSEABLE'

export interface ApiErrorPayload {
  code: ErrorCode
  message: string
  provider?: string
  upstreamStatus?: number
  upstreamType?: string
  /** 决定弹「重试」还是「改提示词」。 */
  retryable: boolean
}

/** fetch client 抛出的错误，携带后端归一化错误体。 */
export class ApiError extends Error {
  readonly status: number
  readonly payload: ApiErrorPayload

  constructor(status: number, payload: ApiErrorPayload) {
    super(payload.message || payload.code)
    this.name = 'ApiError'
    this.status = status
    this.payload = payload
  }

  get code(): ErrorCode {
    return this.payload.code
  }

  /** 重试是否有意义 —— 驱动 UI 按钮文案。 */
  get retryable(): boolean {
    return this.payload.retryable
  }
}

// ---------------------------------------------------------------- 能力矩阵

/** Caps 驱动参数面板显隐，与 provider.Caps 一致。 */
export interface Caps {
  img2img: boolean
  compose: boolean
  noWatermark: boolean
  promptExtend: boolean
  outputFormats: Format[]
  maxRefs: number
  ratios: Aspect[]
  /** true 表示上游用像素尺寸而非档位（SenseNova）。 */
  exactSize: boolean
  nativeMultiImage: boolean
  timeoutSec: number
}

export interface Model {
  id: string
  name: string
}

export interface ProviderInfo {
  id: ProviderID
  name: string
  baseUrl: string
  enabled: boolean
  hasKey: boolean
  /** 已配置时回显掩码，未配置为 ''。 */
  maskedKey?: string
  models: Model[]
  /** 每模型一份能力矩阵；未知模型取通道级默认。 */
  caps: Record<string, Caps>
  /** 便捷入口：默认模型的能力，等于 caps[defaultModel]。 */
  defaultCaps: Caps
  defaultModel: string
  createdAt?: string
}

export interface TestProviderResult {
  ok: boolean
  latencyMs: number
  message: string
  error?: ApiErrorPayload
}

export interface PatchProviderInput {
  baseUrl?: string
  apiKey?: string
  clearKey?: boolean
  enabled?: boolean
  timeoutMs?: number
}

// ---------------------------------------------------------------- 生成请求

export interface Ref {
  assetId?: string
  url?: string
}

export interface GenerateRequest {
  provider: ProviderID
  model: string
  prompt: string
  refs?: Ref[]
  mode: Mode
  aspect: Aspect
  tier: Tier
  count: number
  noWatermark?: boolean
  promptExtend?: boolean
  outputFormat?: Format
}

// ---------------------------------------------------------------- 任务

export interface JobImage {
  seq: number
  assetId?: string
  status: string
  error?: ApiErrorPayload
}

export interface Job {
  id: string
  provider: ProviderID
  model: string
  mode: Mode
  prompt: string
  params: GenerateRequest
  status: JobStatus
  progress: number
  count: number
  latencyMs: number
  error?: ApiErrorPayload
  images: JobImage[]
  createdAt: string
  startedAt?: string
  finishedAt?: string
}

export interface JobListResult {
  items: Job[]
  total: number
  limit: number
  offset: number
}

export interface JobFilter {
  provider?: ProviderID
  status?: JobStatus
  q?: string
  limit?: number
  offset?: number
}

// ---------------------------------------------------------------- 资产

export type AssetSource = 'upload' | 'generated' | 'remote_url'

export interface Asset {
  id: string
  sha256: string
  mime: string
  width: number
  height: number
  bytes: number
  source: AssetSource
  sourceUrl?: string
  expiresAt?: string
  createdAt: string
}

export interface AssetListResult {
  items: Asset[]
  total: number
  limit: number
  offset: number
}

export interface AssetFilter {
  source?: AssetSource
  q?: string
  limit?: number
  offset?: number
}

// ---------------------------------------------------------------- 用量与设置

export interface UsageStat {
  day: string
  provider: string
  model: string
  calls: number
  ok: number
  failed: number
  images: number
}

export interface UsageResult {
  items: UsageStat[]
  days: number
}

export interface Settings {
  concurrency: number
  maxUploadMB: number
  keepDays: number
  /** 当前排队数，供顶栏显示队列水位（只读，PUT 时忽略）。 */
  queueDepth?: number
}

export interface SettingsPatch {
  concurrency?: number
  maxUploadMB?: number
  keepDays?: number
}

// ---------------------------------------------------------------- 提示词预设

/**
 * 参考图角色与预设参考图的定义在 promptRender.ts ——
 * 那里是渲染逻辑的归属地，且该模块零依赖，可被校验脚本直接加载。
 * 这里只做重导出，保证「types.ts 是类型总入口」的既有约定不被破坏。
 */
export { REF_ROLE_LABELS } from '@/components/domain/promptRender'
export type { PresetRef, RefRole } from '@/components/domain/promptRender'

/**
 * 预设的 body：模板内容。
 *
 * 字段刻意保持扁平且都是「渲染提示词所需」的最小集 ——
 * 预设只负责复用「用什么提示词 + 哪几张图 + 什么尺寸偏好」，
 * 执行时仍走工作台的提交链路，故刻意不放 provider / model：
 * 那两项由顶栏全局选择决定，存进预设会让它看起来能固定通道，实际不能。
 */
export interface PresetBody {
  /** 原始提示词，用 @1 / @2 引用参考图序号。 */
  prompt: string
  refs: PresetRef[]
  aspect?: Aspect
  tier?: Tier
  /** 是否在渲染时前置「参考图说明」段落，默认 true。 */
  includeRefNotes?: boolean
}

export interface PromptPreset {
  id: string
  name: string
  category: string
  description: string
  body: PresetBody
  createdAt: string
  updatedAt: string
}

export interface PresetInput {
  name: string
  category?: string
  description?: string
  body: PresetBody
}

// ---------------------------------------------------------------- 健康与队列

export interface Health {
  ok: boolean
  version: string
  buildTime: string
  /** 队列水位：排队中 / 并发上限。 */
  queued: number
  workers: number
  providers: number
}

// ---------------------------------------------------------------- SSE 事件

export type EventKind =
  | 'queued' | 'running' | 'partial'
  | 'done' | 'failed'

export interface JobEvent {
  jobId: string
  kind: EventKind
  status: string
  progress: number
  seq?: number
  assetId?: string
  latencyMs?: number
  error?: ApiErrorPayload
  at: string
}
