<script setup lang="ts">
/**
 * 通道与 Key 配置。
 *
 * 每家一张卡：Base URL / API Key（写库加密，只读回显掩码）/ 启用开关 / 连通性测试。
 * Key 留空表示不改；勾「清除已保存的 Key」才清空。
 */
import { computed, onMounted, ref } from 'vue'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import Card from '@/components/ui/Card.vue'
import Input from '@/components/ui/Input.vue'
import Switch from '@/components/ui/Switch.vue'
import { useToast } from '@/components/ui/useToast'
import { useProvidersStore } from '@/stores/providers'
import type { ProviderID, ProviderInfo } from '@/types'
import { describeError, formatTime } from '@/components/domain/useApiError'

const providers = useProvidersStore()
const toast = useToast()

/** 每张卡的编辑态草稿。 */
const draft = ref<Record<string, { baseUrl: string; apiKey: string; clearKey: boolean }>>({})
const expanded = ref<Record<string, boolean>>({})

function draftOf(p: ProviderInfo): { baseUrl: string; apiKey: string; clearKey: boolean } {
  const d = draft.value[p.id]
  if (d) return d
  const init = { baseUrl: p.baseUrl, apiKey: '', clearKey: false }
  draft.value[p.id] = init
  return init
}

const modelsOf = computed(() => (p: ProviderInfo) => p.models)

onMounted(async () => {
  await providers.load(true)
  // 默认展开第一个未配 Key 的通道，配 Key 是这张页的主要任务。
  const need = providers.items.find((p) => !p.hasKey)
  if (need) expanded.value[need.id] = true
})

async function save(p: ProviderInfo): Promise<void> {
  const d = draftOf(p)
  const patch: {
    baseUrl?: string
    apiKey?: string
    clearKey?: boolean
    enabled?: boolean
  } = {}

  if (d.baseUrl && d.baseUrl !== p.baseUrl) patch.baseUrl = d.baseUrl
  if (d.clearKey) patch.clearKey = true
  else if (d.apiKey.trim()) patch.apiKey = d.apiKey.trim()

  if (Object.keys(patch).length === 0) {
    toast.info('没有需要保存的改动')
    return
  }

  const updated = await providers.patch(p.id, patch)
  if (!updated) {
    toast.error(providers.error || '保存失败')
    return
  }
  // 保存成功后清掉草稿里的 Key，避免明文留在内存。
  draft.value[p.id] = { baseUrl: updated.baseUrl, apiKey: '', clearKey: false }
  toast.success('已保存，Key 已加密入库')
}

async function toggleEnabled(p: ProviderInfo, next: boolean): Promise<void> {
  const updated = await providers.patch(p.id, { enabled: next })
  if (!updated) {
    toast.error(providers.error || '更新失败')
    return
  }
  toast.success(next ? `已启用 ${updated.name}` : `已停用 ${updated.name}`)
}

async function test(p: ProviderInfo): Promise<void> {
  const r = await providers.test(p.id)
  if (!r) return
  if (r.ok) toast.success(`连通正常（${r.latencyMs}ms）`)
  else toast.error(r.message || '连通性测试失败')
}

function capsSummary(p: ProviderInfo): string[] {
  const c = p.defaultCaps
  const out: string[] = []
  out.push(c.img2img ? '图生图' : '仅文生图')
  if (c.compose) out.push('多图合成')
  if (c.noWatermark) out.push('去水印')
  if (c.promptExtend) out.push('提示词增强')
  out.push(`格式 ${c.outputFormats.join('/')}`)
  out.push(`参考图 ≤${c.maxRefs}`)
  out.push(`宽高比 ${c.ratios.length} 档`)
  return out
}

function onToggleExpand(id: ProviderID): void {
  expanded.value[id] = !expanded.value[id]
}
</script>

<template>
  <div class="pv stack">
    <header class="stack stack--1">
      <h2 class="t-xl">通道与 Key 配置</h2>
      <p class="hint">
        Key 在服务端用 AES-GCM 加密落库，前端只回显掩码，不会返回明文。
        留空表示不修改。
      </p>
    </header>

    <div v-if="providers.loading && !providers.items.length" class="loading-block">
      <p>正在加载通道…</p>
    </div>

    <p v-else-if="providers.error" class="error-text">{{ providers.error }}</p>

    <div v-else class="stack stack--4">
      <Card v-for="p in providers.items" :key="p.id" class="pv__card">
        <template #head>
          <div class="card__title">
            <span>{{ p.name }}</span>
            <span class="card__subtitle mono">{{ p.id }}</span>
          </div>
          <div class="row row--tight flex-none">
            <Badge :tone="p.hasKey ? 'ok' : 'warn'" size="sm" dot>
              {{ p.hasKey ? '已配 Key' : '未配 Key' }}
            </Badge>
            <Badge :tone="p.enabled ? 'accent' : 'neutral'" size="sm">
              {{ p.enabled ? '已启用' : '已停用' }}
            </Badge>
            <Button variant="ghost" size="sm" @click="onToggleExpand(p.id)">
              {{ expanded[p.id] ? '收起' : '展开' }}
            </Button>
          </div>
        </template>

        <div class="stack stack--3">
          <div class="row row--tight row--wrap">
            <Badge v-for="c in capsSummary(p)" :key="c" size="sm" outline>{{ c }}</Badge>
          </div>

          <p class="hint">
            模型：{{ modelsOf(p).map((m) => m.name || m.id).join('、') || '（未上报）' }}
            <span v-if="p.createdAt"> · 创建于 {{ formatTime(p.createdAt) }}</span>
          </p>

          <div v-if="expanded[p.id]" class="stack stack--3">
            <label class="field">
              <span class="field__label">Base URL</span>
              <Input
                v-model="draftOf(p).baseUrl"
                type="url"
                mono
                placeholder="https://…"
              />
            </label>

            <label class="field">
              <span class="field__label">API Key</span>
              <Input
                v-model="draftOf(p).apiKey"
                type="password"
                mono
                autocomplete="off"
                :placeholder="p.maskedKey || (p.hasKey ? '已保存，留空则不修改' : '粘贴 API Key')"
              />
              <span class="field__hint">
                {{ p.hasKey ? `当前：${p.maskedKey || '（已保存）'}` : '尚未配置' }}
              </span>
            </label>

            <Switch
              v-model="draftOf(p).clearKey"
              label="保存时清除已保存的 Key"
              desc="勾选后本次保存会清空该通道的 Key"
            />

            <div class="row row--tight row--wrap">
              <Button
                variant="primary"
                size="md"
                :loading="providers.saving[p.id]"
                @click="save(p)"
              >
                保存
              </Button>
              <Button
                variant="secondary"
                size="md"
                :loading="providers.testing[p.id]"
                :disabled="!p.hasKey"
                :title="p.hasKey ? '打一次真实请求验证 Key' : '请先保存 Key'"
                @click="test(p)"
              >
                测试连通性
              </Button>
              <Switch
                :model-value="p.enabled"
                class="ml-auto"
                :label="p.enabled ? '已启用' : '已停用'"
                @update:model-value="toggleEnabled(p, $event)"
              />
            </div>

            <p
              v-if="providers.testResult[p.id]"
              class="hint"
              :class="providers.testResult[p.id]?.ok ? 'text-ok' : 'text-danger'"
            >
              {{ providers.testResult[p.id]?.message }}
              <span v-if="providers.testResult[p.id]?.latencyMs">
                （{{ providers.testResult[p.id]?.latencyMs }}ms）
              </span>
            </p>
          </div>
        </div>
      </Card>
    </div>

    <p v-if="providers.error" class="error-text">{{ describeError(providers.error) }}</p>
  </div>
</template>

<style scoped>
.pv {
  height: 100%;
  min-height: 0;
  max-width: 880px;
}

.pv__card {
  overflow: visible;
}
</style>
