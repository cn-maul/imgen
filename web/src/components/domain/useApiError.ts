/**
 * 领域层共享的小工具：API 错误的中文文案 + 可重试判定 + 时间/体积格式化。
 *
 * 放这里而不是各 view 里复制一份 —— 错误提示策略（retryable → 弹「重试」还是
 * 「改提示词」）必须是全站一致的。
 */
import { ApiError } from '@/api/client'
import type { ApiErrorPayload, ErrorCode } from '@/types'

const MESSAGES: Record<ErrorCode, string> = {
  BAD_REQUEST: '请求参数不合法，请检查输入',
  NOT_FOUND: '资源不存在，可能已被清理',
  INTERNAL: '服务内部错误',
  UPSTREAM_AUTH: 'API Key 无效或无权访问，请到通道配置页检查',
  UPSTREAM_QUOTA: '上游额度已用尽或触发限流',
  UPSTREAM_REJECTED: '上游拒绝了本次请求',
  UPSTREAM_MODERATION: '提示词或图片未通过上游内容审核，请修改后重试',
  UPSTREAM_TIMEOUT: '上游响应超时',
  UPSTREAM_UNAVAILABLE: '上游服务暂时不可用',
  UPSTREAM_UNPARSEABLE: '上游返回了无法解析的内容',
}

/** 兜底：非 ApiError（网络错误 / TypeError）也要有可读文案。 */
export function describeError(e: unknown): string {
  if (e instanceof ApiError) {
    const base = e.payload?.message || MESSAGES[e.payload?.code] || '请求失败'
    return base
  }
  if (e instanceof Error) return e.message || '未知错误'
  return '未知错误'
}

/** ApiError.retryable 决定 UI 给「重试」还是引导改提示词。 */
export function isRetryable(e: unknown): boolean {
  if (e instanceof ApiError) return e.retryable
  return true
}

/** 错误码 → 短标签，用于 badge 展示。 */
export function errorLabel(payload?: ApiErrorPayload): string {
  if (!payload) return '失败'
  switch (payload.code) {
    case 'UPSTREAM_AUTH':
      return 'Key 无效'
    case 'UPSTREAM_QUOTA':
      return '额度/限流'
    case 'UPSTREAM_MODERATION':
      return '内容审核'
    case 'UPSTREAM_TIMEOUT':
      return '上游超时'
    case 'UPSTREAM_REJECTED':
      return '请求被拒'
    case 'UPSTREAM_UNAVAILABLE':
      return '上游不可用'
    case 'UPSTREAM_UNPARSEABLE':
      return '响应异常'
    case 'NOT_FOUND':
      return '不存在'
    case 'BAD_REQUEST':
      return '参数错误'
    default:
      return '失败'
  }
}

export function formatDuration(ms: number): string {
  if (!ms || ms < 0) return '—'
  if (ms < 1000) return `${Math.round(ms)}ms`
  const s = ms / 1000
  if (s < 60) return `${s.toFixed(1)}s`
  const m = Math.floor(s / 60)
  const rest = Math.round(s % 60)
  return `${m}m${String(rest).padStart(2, '0')}s`
}

export function formatBytes(bytes: number): string {
  if (!bytes || bytes <= 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0
  let v = bytes
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i += 1
  }
  return `${v.toFixed(i === 0 ? 0 : 1)}${units[i]}`
}

export function formatTime(iso?: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

export function fromNow(iso?: string): string {
  if (!iso) return '—'
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return '—'
  const diff = Date.now() - t
  if (diff < 0) return '刚刚'
  const min = Math.floor(diff / 60000)
  if (min < 1) return '刚刚'
  if (min < 60) return `${min} 分钟前`
  const h = Math.floor(min / 60)
  if (h < 24) return `${h} 小时前`
  const d = Math.floor(h / 24)
  if (d < 30) return `${d} 天前`
  return formatTime(iso)
}

/** 复制到剪贴板；不支持时回落 execCommand。 */
export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // 继续走回落。
  }
  try {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.setAttribute('readonly', 'true')
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    return ok
  } catch {
    return false
  }
}

/** 触发下载（同源 raw URL 直接 a[download]，跨域则用 fetch + blob）。 */
export async function downloadAsset(id: string, filename: string): Promise<void> {
  const url = `/api/v1/assets/${encodeURIComponent(id)}/raw`
  try {
    const res = await fetch(url)
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const blob = await res.blob()
    const href = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = href
    a.download = filename
    document.body.appendChild(a)
    a.click()
    a.remove()
    setTimeout(() => URL.revokeObjectURL(href), 2000)
  } catch {
    // 兜底：直接跳 raw URL，让浏览器自己处理。
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    a.target = '_blank'
    a.rel = 'noopener'
    document.body.appendChild(a)
    a.click()
    a.remove()
  }
}

export { MESSAGES as ERROR_MESSAGES }
