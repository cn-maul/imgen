/**
 * 通道（Provider）状态：列表、当前选中 provider/model、以及驱动参数面板显隐的 Caps。
 *
 * Caps 的取法：caps[modelId] → defaultCaps → FALLBACK（后端未就绪时不至于白屏）。
 * 面板只读 store 派生的布尔量，不自己判字段，保证「能力自适应」只有一处逻辑。
 */
import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { api } from '@/api/client'
import {
  ALL_ASPECTS,
  type Aspect,
  type Caps,
  type Format,
  type Model,
  type PatchProviderInput,
  type ProviderID,
  type ProviderInfo,
  type TestProviderResult,
} from '@/types'

/** 后端 /providers 尚未返回时的兜底能力矩阵：全放开，避免面板一片空白。 */
const FALLBACK_CAPS: Caps = {
  img2img: true,
  compose: true,
  noWatermark: true,
  promptExtend: true,
  outputFormats: ['png', 'jpeg', 'webp'],
  maxRefs: 5,
  ratios: ALL_ASPECTS,
  exactSize: false,
  nativeMultiImage: true,
  timeoutSec: 180,
}

export const useProvidersStore = defineStore('providers', () => {
  // ---------------------------------------------------------------- state
  const items = ref<ProviderInfo[]>([])
  const loading = ref(false)
  const loaded = ref(false)
  const error = ref('')
  const selectedProvider = ref<ProviderID | ''>('')
  const selectedModel = ref('')
  const saving = ref<Record<string, boolean>>({})
  const testing = ref<Record<string, boolean>>({})
  const testResult = ref<Record<string, TestProviderResult | undefined>>({})

  // ---------------------------------------------------------------- 派生
  const enabled = computed<ProviderInfo[]>(() => items.value.filter((p) => p.enabled))

  /** 当前通道：优先用户选择，其次「已启用且已配 Key」，再次「已启用」，最后第一项。 */
  const current = computed<ProviderInfo | undefined>(() => {
    if (selectedProvider.value) {
      const hit = items.value.find((p) => p.id === selectedProvider.value)
      if (hit) return hit
    }
    return (
      items.value.find((p) => p.enabled && p.hasKey) ??
      items.value.find((p) => p.enabled) ??
      items.value[0]
    )
  })

  const models = computed<Model[]>(() => current.value?.models ?? [])

  /** 当前模型：选择值若不在该通道模型表里则回落到 defaultModel。 */
  const modelId = computed<string>(() => {
    const p = current.value
    if (!p) return ''
    if (selectedModel.value && p.models.some((m) => m.id === selectedModel.value)) {
      return selectedModel.value
    }
    return p.defaultModel || p.models[0]?.id || ''
  })

  const caps = computed<Caps>(() => {
    const p = current.value
    if (!p) return FALLBACK_CAPS
    return p.caps?.[modelId.value] ?? p.defaultCaps ?? FALLBACK_CAPS
  })

  /** 面板显隐开关 —— 全部由 Caps 派生，视图不得自行判字段。 */
  const hasKey = computed(() => Boolean(current.value?.hasKey))
  const enabledNow = computed(() => Boolean(current.value?.enabled))
  const ready = computed(() => hasKey.value && enabledNow.value)
  const ratios = computed<Aspect[]>(() =>
    caps.value.ratios?.length ? caps.value.ratios : ALL_ASPECTS,
  )
  const formats = computed<Format[]>(() => caps.value.outputFormats ?? [])
  const showFormat = computed(() => formats.value.length > 1)
  const maxRefs = computed(() => Math.max(0, caps.value.maxRefs ?? 0))
  const canImg2Img = computed(() => caps.value.img2img)
  const canCompose = computed(() => caps.value.compose)
  const showNoWatermark = computed(() => caps.value.noWatermark)
  const showPromptExtend = computed(() => caps.value.promptExtend)
  const anyConfigured = computed(() => items.value.some((p) => p.hasKey))

  // ---------------------------------------------------------------- actions
  async function load(force = false): Promise<void> {
    if (loading.value) return
    if (loaded.value && !force) return
    loading.value = true
    error.value = ''
    try {
      items.value = await api.listProviders()
      loaded.value = true
      if (!selectedProvider.value) selectedProvider.value = current.value?.id ?? ''
      if (!selectedModel.value) selectedModel.value = modelId.value
    } catch (e) {
      error.value = (e as Error).message || '通道列表加载失败'
    } finally {
      loading.value = false
    }
  }

  function selectProvider(id: ProviderID): void {
    selectedProvider.value = id
    // 模型表随通道变化，先清空让 modelId 回落到该通道默认模型。
    selectedModel.value = ''
  }

  function selectModel(id: string): void {
    selectedModel.value = id
  }

  async function patch(id: ProviderID, input: PatchProviderInput): Promise<ProviderInfo | null> {
    saving.value[id] = true
    try {
      const updated = await api.patchProvider(id, input)
      const i = items.value.findIndex((p) => p.id === id)
      if (i >= 0) items.value[i] = updated
      else items.value.push(updated)
      selectedProvider.value = id
      return updated
    } catch (e) {
      error.value = (e as Error).message || '保存失败'
      return null
    } finally {
      saving.value[id] = false
    }
  }

  async function test(id: ProviderID): Promise<TestProviderResult | null> {
    testing.value[id] = true
    try {
      const r = await api.testProvider(id)
      testResult.value[id] = r
      return r
    } catch (e) {
      const r: TestProviderResult = {
        ok: false,
        latencyMs: 0,
        message: (e as Error).message || '连通性测试失败',
      }
      testResult.value[id] = r
      return r
    } finally {
      testing.value[id] = false
    }
  }

  function clearTest(id: ProviderID): void {
    delete testResult.value[id]
  }

  return {
    // state
    items,
    loading,
    loaded,
    error,
    selectedProvider,
    selectedModel,
    saving,
    testing,
    testResult,
    // 派生
    enabled,
    current,
    models,
    modelId,
    caps,
    hasKey,
    enabledNow,
    ready,
    ratios,
    formats,
    showFormat,
    maxRefs,
    canImg2Img,
    canCompose,
    showNoWatermark,
    showPromptExtend,
    anyConfigured,
    // actions
    load,
    selectProvider,
    selectModel,
    patch,
    test,
    clearTest,
  }
})
