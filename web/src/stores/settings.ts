/**
 * 系统设置与用量。
 *
 * Settings 三个字段都不是必需的 JSON 字段，做一次兜底；
 * patch 返回后端归一化后的完整对象，直接替换 state。
 */
import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { api } from '@/api/client'
import type { Settings, SettingsPatch, UsageResult } from '@/types'

const DEFAULTS: Settings = { concurrency: 2, maxUploadMB: 20, keepDays: 30 }

export const useSettingsStore = defineStore('settings', () => {
  const settings = ref<Settings>({ ...DEFAULTS })
  const usage = ref<UsageResult | null>(null)
  const loading = ref(false)
  const saving = ref(false)
  const error = ref('')
  const saved = ref(false)

  const loaded = computed(() => loading.value === false && saved.value === false)

  async function load(): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      const s = await api.getSettings()
      settings.value = {
        concurrency: s.concurrency ?? DEFAULTS.concurrency,
        maxUploadMB: s.maxUploadMB ?? DEFAULTS.maxUploadMB,
        keepDays: s.keepDays ?? DEFAULTS.keepDays,
      }
    } catch (e) {
      error.value = (e as Error).message || '设置加载失败'
    } finally {
      loading.value = false
    }
  }

  async function save(): Promise<boolean> {
    saving.value = true
    error.value = ''
    try {
      const patch: SettingsPatch = {
        concurrency: Math.max(1, Math.min(8, settings.value.concurrency)),
        maxUploadMB: Math.max(1, Math.min(200, settings.value.maxUploadMB)),
        keepDays: Math.max(0, Math.min(3650, settings.value.keepDays)),
      }
      settings.value = await api.putSettings(patch)
      saved.value = true
      return true
    } catch (e) {
      error.value = (e as Error).message || '保存失败'
      return false
    } finally {
      saving.value = false
    }
  }

  async function loadUsage(days = 14): Promise<void> {
    try {
      usage.value = await api.usage(days)
    } catch (e) {
      error.value = (e as Error).message || '用量加载失败'
    }
  }

  const usageTotal = computed(() => {
    const items = usage.value?.items ?? []
    return items.reduce(
      (acc, it) => ({
        calls: acc.calls + it.calls,
        ok: acc.ok + it.ok,
        failed: acc.failed + it.failed,
        images: acc.images + it.images,
      }),
      { calls: 0, ok: 0, failed: 0, images: 0 },
    )
  })

  return { settings, usage, usageTotal, loading, saving, error, saved, loaded, load, save, loadUsage }
})
