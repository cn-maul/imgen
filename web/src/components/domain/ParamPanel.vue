<script setup lang="ts">
/**
 * 参数面板 —— 显隐完全由 Caps 驱动（PLAN §7.4 最高优先级）。
 *
 * 规则（与 providers store 的派生量一一对应）：
 *   caps.img2img=false       → 隐藏「图生图」
 *   caps.compose=false       → 隐藏「多图合成」
 *   caps.noWatermark=false   → 隐藏「去水印」（Agnes 无此参数）
 *   caps.promptExtend=false  → 隐藏「提示词增强」
 *   caps.outputFormats≤1     → 隐藏格式选择
 *   caps.maxRefs             → 参考图槽位上限
 *   caps.ratios              → 宽高比只给这些选项
 */
import { computed, ref } from 'vue'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import Checkbox from '@/components/ui/Checkbox.vue'
import Select from '@/components/ui/Select.vue'
import Tabs from '@/components/ui/Tabs.vue'
import Textarea from '@/components/ui/Textarea.vue'
import RefTray from './RefTray.vue'
import { MAX_COUNT, MIN_COUNT, useStudioStore } from '@/stores/studio'
import { useProvidersStore } from '@/stores/providers'
import { ALL_TIERS, type Aspect, type Format, type Mode } from '@/types'
import type { SelectOption } from '@/components/ui/types'

const providers = useProvidersStore()
const studio = useStudioStore()

const emit = defineEmits<{
  (e: 'submit'): void
  (e: 'goSettings'): void
}>()

// ------------------------------------------------------------------ 模式
const modeTabs = computed(() => {
  const c = providers.caps
  const list = [{ value: 'txt2img', label: '文生图' }]
  if (c.img2img) list.push({ value: 'img2img', label: '图生图' })
  if (c.compose) list.push({ value: 'compose', label: '多图合成' })
  return list
})

const mode = computed({
  get: () => studio.mode,
  set: (v: string) => {
    studio.mode = v as Mode
  },
})

// ------------------------------------------------------------------ 宽高比
const aspectOptions = computed<SelectOption[]>(() =>
  providers.ratios.map((r) => ({ value: r, label: r })),
)

const aspect = computed({
  get: () => studio.aspect,
  set: (v: string) => {
    studio.aspect = v as Aspect
  },
})

const tierOptions = computed<SelectOption[]>(() =>
  ALL_TIERS.map((t) => ({ value: t, label: t })),
)

const tier = computed({
  get: () => studio.tier,
  set: (v: string) => {
    studio.tier = v as never
  },
})

const formatOptions = computed<SelectOption[]>(() =>
  providers.formats.map((f) => ({ value: f, label: f.toUpperCase() })),
)

const outputFormat = computed({
  get: () => studio.outputFormat,
  set: (v: string) => {
    studio.outputFormat = v as Format
  },
})

// ------------------------------------------------------------------ 张数
const count = computed({
  get: () => studio.count,
  set: (v: number | string) => {
    const n = typeof v === 'string' ? Number(v) : v
    if (Number.isFinite(n)) studio.count = Math.min(MAX_COUNT, Math.max(MIN_COUNT, n))
  },
})

const blocked = computed(() => {
  if (!providers.hasKey) return '该通道未配置 API Key'
  if (!providers.enabledNow) return '该通道已停用'
  return studio.blockedReason(providers.caps, providers.hasKey)
})

const canSubmit = computed(() => !blocked.value)

// ------------------------------------------------------------------ 能力说明
const capsHint = computed(() => {
  const c = providers.caps
  const parts: string[] = []
  parts.push(`参考图上限 ${c.maxRefs}`)
  parts.push(`宽高比 ${providers.ratios.length}/${8}`)
  parts.push(c.exactSize ? '像素尺寸' : '档位尺寸')
  parts.push(`超时 ${c.timeoutSec}s`)
  return parts.join(' · ')
})

const showRefs = computed(() => studio.mode !== 'txt2img' && providers.maxRefs > 0)

// ------------------------------------------------------------------ 提示词模板
const TEMPLATES: { label: string; text: string }[] = [
  { label: '写实人像', text: '一位年轻人的写实肖像，柔和自然光，浅景深，8K 细节' },
  { label: '产品图', text: '白色背景上的产品摄影，柔和阴影，商业级棚拍，高清' },
  { label: '概念场景', text: '漂浮在云雾峡谷之上的发光城市，电影级光影，超广角构图' },
]

const showTemplates = ref(false)

function applyTemplate(text: string): void {
  studio.prompt = text
  showTemplates.value = false
}
</script>

<template>
  <form class="pparams stack" @submit.prevent="emit('submit')">
    <!-- 未配 Key：最显眼的引导 -->
    <div v-if="!providers.hasKey" class="pparams__alert">
      <div class="stack stack--2">
        <strong class="t-sm">该通道还没配置 API Key</strong>
        <p class="hint">生成前需要先到通道配置页填写 Key，否则上游会直接拒绝。</p>
      </div>
      <Button variant="outline" size="sm" @click="emit('goSettings')">去配置</Button>
    </div>

    <section class="section">
      <h4 class="section__title">模式</h4>
      <Tabs v-model="mode" :tabs="modeTabs" variant="segmented" />
      <p v-if="!providers.canImg2Img && !providers.canCompose" class="hint">
        当前模型只支持文生图。
      </p>
    </section>

    <section class="section">
      <h4 class="section__title">提示词</h4>
      <Textarea
        v-model="studio.prompt"
        placeholder="描述你想生成的画面…"
        :rows="5"
        :maxlength="2000"
        count
        @submit="emit('submit')"
      />
      <div class="row row--tight row--wrap">
        <Button variant="ghost" size="sm" @click="showTemplates = !showTemplates">
          {{ showTemplates ? '收起模板' : '提示词模板' }}
        </Button>
        <Button
          v-if="studio.prompt"
          variant="ghost"
          size="sm"
          @click="studio.prompt = ''"
        >
          清空
        </Button>
      </div>
      <div v-if="showTemplates" class="stack stack--1">
        <button
          v-for="t in TEMPLATES"
          :key="t.label"
          type="button"
          class="pparams__tpl"
          @click="applyTemplate(t.text)"
        >
          <span class="t-sm font-medium">{{ t.label }}</span>
          <span class="t-xs text-3 line-clamp-2">{{ t.text }}</span>
        </button>
      </div>
    </section>

    <section class="section" :class="{ 'is-dim': !showRefs }">
      <h4 class="section__title">参考图</h4>
      <RefTray :max="providers.maxRefs" />
    </section>

    <section class="section">
      <h4 class="section__title">尺寸与张数</h4>

      <label class="field">
        <span class="field__label">尺寸档位</span>
        <Select v-model="tier" :options="tierOptions" />
      </label>

      <label class="field">
        <span class="field__label">
          宽高比
          <span v-if="providers.ratios.length < 8" class="hint">
            该模型仅支持 {{ providers.ratios.length }} 档
          </span>
        </span>
        <Select v-model="aspect" :options="aspectOptions" />
      </label>

      <label class="field">
        <span class="field__label">张数（1–{{ MAX_COUNT }}，上游逐张串行）</span>
        <div class="row row--tight">
          <input
            v-model.number="count"
            class="pparams__count"
            type="range"
            :min="MIN_COUNT"
            :max="MAX_COUNT"
            step="1"
          />
          <span class="mono pparams__countval">{{ studio.count }}</span>
        </div>
      </label>

      <label v-if="providers.showFormat" class="field">
        <span class="field__label">输出格式</span>
        <Select v-model="outputFormat" :options="formatOptions" />
      </label>
    </section>

    <!-- 能力相关开关：Caps 不支持时整段不渲染 -->
    <section v-if="providers.showNoWatermark || providers.showPromptExtend" class="section">
      <h4 class="section__title">通道选项</h4>
      <Checkbox
        v-if="providers.showNoWatermark"
        v-model="studio.noWatermark"
        label="去水印"
        desc="关闭上游默认水印（该通道支持）"
      />
      <Checkbox
        v-if="providers.showPromptExtend"
        v-model="studio.promptExtend"
        label="提示词增强"
        desc="让上游自动扩写提示词，可能改变画面意图"
      />
    </section>

    <p class="hint">{{ capsHint }}</p>

    <div class="pparams__foot">
      <Button
        type="submit"
        variant="primary"
        size="lg"
        block
        :disabled="!canSubmit"
        :title="blocked || '开始生成'"
      >
        {{ blocked ? '无法生成' : '生成' }}
      </Button>
      <p v-if="blocked" class="error-text">{{ blocked }}</p>
      <div class="row row--tight row--wrap">
        <Badge size="sm" :tone="providers.ready ? 'ok' : 'warn'" dot>
          {{ providers.ready ? '通道就绪' : '通道未就绪' }}
        </Badge>
        <Badge size="sm" outline>{{ providers.modelId || '未选模型' }}</Badge>
      </div>
    </div>
  </form>
</template>

<style scoped>
.pparams__alert {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
  padding: var(--space-3);
  border: var(--border-w) solid var(--warn);
  border-radius: var(--radius-md);
  background: var(--warn-bg);
}

.pparams__tpl {
  display: flex;
  flex-direction: column;
  gap: 2px;
  width: 100%;
  padding: var(--space-2) var(--space-3);
  border: var(--border-w) solid var(--border);
  border-radius: var(--radius-md);
  background: var(--surface-2);
  text-align: left;
  transition: border-color var(--dur-fast) var(--ease-out);
}

.pparams__tpl:hover {
  border-color: var(--accent);
}

.pparams__count {
  flex: 1 1 auto;
  accent-color: var(--accent);
}

.pparams__countval {
  flex: none;
  width: 2ch;
  text-align: right;
  color: var(--text-1);
}

.pparams__foot {
  position: sticky;
  bottom: calc(-1 * var(--space-4));
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding-top: var(--space-3);
  background: var(--surface-1);
}

.is-dim {
  opacity: 0.6;
}
</style>
