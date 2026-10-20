/**
 * 提示词预设库。
 *
 * 与工作台的 studio store 刻意分开：studio 是「当前正在编辑的草稿」，
 * presets 是「可复用的模板集合」。预设页把选中的模板灌进 studio 提交，
 * 不在这里直接发起生成，避免两处各有一份提交逻辑。
 */
import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { api } from '@/api/client'
import type { PresetBody, PresetInput, PromptPreset } from '@/types'

export const usePresetsStore = defineStore('presets', () => {
  const items = ref<PromptPreset[]>([])
  const loading = ref(false)
  const error = ref('')
  /** 正在保存的预设 id，为空表示新建。 */
  const savingId = ref('')

  const categories = computed(() => {
    const set = new Set<string>()
    for (const p of items.value) {
      if (p.category) set.add(p.category)
    }
    return [...set].sort((a, b) => a.localeCompare(b, 'zh-Hans-CN'))
  })

  async function load(force = false): Promise<void> {
    if (loading.value) return
    if (items.value.length && !force) return
    loading.value = true
    error.value = ''
    try {
      items.value = await api.listPresets()
    } catch (e) {
      error.value = (e as Error).message
    } finally {
      loading.value = false
    }
  }

  /** 新建；成功后插入列表头部（后端按 updated_at 倒序返回）。 */
  async function create(input: PresetInput): Promise<PromptPreset | null> {
    savingId.value = ''
    error.value = ''
    try {
      const created = await api.createPreset(input)
      items.value = [created, ...items.value]
      return created
    } catch (e) {
      error.value = (e as Error).message
      return null
    }
  }

  /** 覆盖更新；成功后按 updated_at 重新排序。 */
  async function update(id: string, input: PresetInput): Promise<PromptPreset | null> {
    savingId.value = id
    error.value = ''
    try {
      const updated = await api.updatePreset(id, input)
      items.value = items.value
        .map((p) => (p.id === id ? updated : p))
        .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))
      return updated
    } catch (e) {
      error.value = (e as Error).message
      return null
    } finally {
      savingId.value = ''
    }
  }

  async function remove(id: string): Promise<boolean> {
    error.value = ''
    try {
      await api.deletePreset(id)
      items.value = items.value.filter((p) => p.id !== id)
      return true
    } catch (e) {
      error.value = (e as Error).message
      return false
    }
  }

  function find(id: string): PromptPreset | null {
    return items.value.find((p) => p.id === id) ?? null
  }

  /** 草稿 → 保存载荷；空 name / 空 prompt 在这里拦住。 */
  function toInput(name: string, body: PresetBody, category = '', description = ''): PresetInput {
    return {
      name: name.trim(),
      category: category.trim(),
      description: description.trim(),
      body: {
        prompt: body.prompt,
        refs: [...body.refs],
        aspect: body.aspect,
        tier: body.tier,
        includeRefNotes: body.includeRefNotes !== false,
      },
    }
  }

  return {
    items,
    loading,
    error,
    savingId,
    categories,
    load,
    create,
    update,
    remove,
    find,
    toInput,
  }
})
