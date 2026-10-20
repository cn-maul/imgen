/**
 * 工作台草稿态：参数面板的表单值 + 参考图槽位。
 *
 * 放在 store 而不是组件内的原因：资产库 / 历史页都要能往工作台「灌参数」和
 * 「灌参考图」，跨路由共享一个草稿。
 */
import { ref } from 'vue'
import { defineStore } from 'pinia'
import {
  ALL_ASPECTS,
  type Aspect,
  type Caps,
  type Format,
  type GenerateRequest,
  type Job,
  type Mode,
  type ProviderID,
  type Tier,
} from '@/types'

export interface StudioRef {
  key: string
  assetId?: string
  url?: string
  /** 展示名（文件名或资产 id 前缀）。 */
  name: string
}

/** 批量张数上限：两家上游单请求只出 1 张，由后端拆成 N 次串行调用。 */
export const MAX_COUNT = 8
export const MIN_COUNT = 1

export const useStudioStore = defineStore('studio', () => {
  const mode = ref<Mode>('txt2img')
  const prompt = ref('')
  const aspect = ref<Aspect>('1:1')
  const tier = ref<Tier>('2K')
  const count = ref(1)
  const noWatermark = ref(true)
  const promptExtend = ref(true)
  const outputFormat = ref<Format>('png')
  const refs = ref<StudioRef[]>([])
  /** 由历史页「以此次参数再生成」置位，工作台挂载后消费。 */
  const autoSubmit = ref(false)

  let seq = 0
  function nextKey(): string {
    seq += 1
    return `ref-${seq}-${Date.now()}`
  }

  // ---------------------------------------------------------------- 参考图
  function addAssetRef(assetId: string, name: string): boolean {
    if (refs.value.some((r) => r.assetId === assetId)) return false
    refs.value.push({ key: nextKey(), assetId, name })
    return true
  }

  function addUrlRef(url: string, name?: string): void {
    refs.value.push({ key: nextKey(), url, name: name || url })
  }

  function removeRef(index: number): void {
    if (index < 0 || index >= refs.value.length) return
    refs.value.splice(index, 1)
  }

  function moveRef(from: number, to: number): void {
    if (from === to) return
    if (from < 0 || from >= refs.value.length) return
    if (to < 0 || to >= refs.value.length) return
    const [item] = refs.value.splice(from, 1)
    refs.value.splice(to, 0, item)
  }

  function clearRefs(): void {
    refs.value = []
  }

  /** 按 Caps.MaxRefs 截断（切通道时调用）。 */
  function clampRefs(max: number): void {
    if (refs.value.length > max) refs.value = refs.value.slice(0, max)
  }

  // ---------------------------------------------------------------- 能力自适应
  /**
   * 切 provider / model 后把草稿钳制到合法区间：
   * 模式不支持 → 回落文生图；宽高比不在 Caps.Ratios → 取第一个；
   * 格式不支持 → 取第一个；参考图超上限 → 截断。
   */
  function syncCaps(caps: Caps): void {
    if (!caps.img2img && mode.value === 'img2img') mode.value = 'txt2img'
    if (!caps.compose && mode.value === 'compose') mode.value = 'txt2img'

    const ratios = caps.ratios?.length ? caps.ratios : ALL_ASPECTS
    if (!ratios.includes(aspect.value)) aspect.value = ratios[0] ?? '1:1'

    const formats = caps.outputFormats ?? []
    if (formats.length && !formats.includes(outputFormat.value)) {
      outputFormat.value = formats[0] ?? 'png'
    }

    clampRefs(Math.max(0, caps.maxRefs ?? 0))
  }

  // ---------------------------------------------------------------- 快照
  /** 从历史任务回填草稿（参数快照 + 参考图回灌）。 */
  function applyJob(job: Job): void {
    const p = job.params
    if (!p) return
    mode.value = p.mode
    prompt.value = p.prompt
    aspect.value = p.aspect
    tier.value = p.tier
    count.value = Math.min(MAX_COUNT, Math.max(MIN_COUNT, p.count || 1))
    if (p.noWatermark !== undefined) noWatermark.value = p.noWatermark
    if (p.promptExtend !== undefined) promptExtend.value = p.promptExtend
    if (p.outputFormat) outputFormat.value = p.outputFormat
    refs.value = (p.refs ?? []).map((r) => ({
      key: nextKey(),
      assetId: r.assetId,
      url: r.url,
      name: r.assetId ? `参考图 ${r.assetId.slice(0, 6)}` : r.url || '远程图',
    }))
  }

  /** 构造请求体：不支持的开关一律不下发，避免上游 400。 */
  function buildRequest(provider: ProviderID, model: string, caps: Caps): GenerateRequest {
    const req: GenerateRequest = {
      provider,
      model,
      prompt: prompt.value.trim(),
      mode: mode.value,
      aspect: aspect.value,
      tier: tier.value,
      count: Math.min(MAX_COUNT, Math.max(MIN_COUNT, count.value)),
    }

    if (mode.value !== 'txt2img') {
      const list = refs.value
        .filter((r) => Boolean(r.assetId || r.url))
        .map((r) => (r.assetId ? { assetId: r.assetId } : { url: r.url as string }))
      if (list.length) req.refs = list
    }

    if (caps.noWatermark) req.noWatermark = noWatermark.value
    if (caps.promptExtend) req.promptExtend = promptExtend.value
    if ((caps.outputFormats?.length ?? 0) > 1) req.outputFormat = outputFormat.value

    return req
  }

  /** 判定当前草稿能否提交；返回空串表示可提交。 */
  function blockedReason(caps: Caps, hasKey: boolean): string {
    if (!prompt.value.trim()) return '请先填写提示词'
    if (!hasKey) return '该通道未配置 API Key'
    if (mode.value === 'img2img' && !caps.img2img) return '该模型不支持图生图'
    if (mode.value === 'compose' && !caps.compose) return '该模型不支持多图合成'
    if (mode.value !== 'txt2img' && refs.value.length === 0) return '请先上传参考图'
    if (mode.value === 'compose' && refs.value.length < 2) return '多图合成至少需要 2 张参考图'
    return ''
  }

  return {
    mode,
    prompt,
    aspect,
    tier,
    count,
    noWatermark,
    promptExtend,
    outputFormat,
    refs,
    autoSubmit,
    addAssetRef,
    addUrlRef,
    removeRef,
    moveRef,
    clearRefs,
    clampRefs,
    syncCaps,
    applyJob,
    buildRequest,
    blockedReason,
  }
})
