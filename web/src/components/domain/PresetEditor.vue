<script setup lang="ts">
/**
 * 预设编辑器：编辑一个提示词预设的模板内容。
 *
 * 这一块是整个「图生图提示词库」的核心交互。关键设计：
 *   - 参考图以**有序槽位**呈现，序号就是模型看到的顺序；
 *   - 每张图有角色（场景/人物/风格…），渲染成图注交给模型；
 *   - 提示词里用 `@1` 引用，编辑器实时显示渲染后真正发送的文本。
 *
 * 顺序即语义，所以拖拽/上下移动必须与序号严格一致 ——
 * 提交时 `refs` 的数组顺序会原样变成上游的 image 数组顺序。
 */
import { computed } from 'vue'
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import Switch from '@/components/ui/Switch.vue'
import Textarea from '@/components/ui/Textarea.vue'
import { assetThumbURL } from '@/api/client'
import { outOfRangeRefs, renderPrompt, type RenderRef } from './promptRender'
import {
  ALL_ASPECTS,
  ALL_TIERS,
  REF_ROLE_LABELS,
  type Aspect,
  type PresetBody,
  type PresetRef,
  type RefRole,
  type Tier,
} from '@/types'

const props = defineProps<{
  name: string
  category: string
  description: string
  body: PresetBody
  saving?: boolean
  /** 有图被引用但资产已删除时为 true。 */
  hasMissingAssets?: boolean
}>()

const emit = defineEmits<{
  (e: 'update:name', v: string): void
  (e: 'update:category', v: string): void
  (e: 'update:description', v: string): void
  (e: 'update:body', v: PresetBody): void
  (e: 'pick-assets'): void
  (e: 'remove-ref', index: number): void
  (e: 'move-ref', from: number, to: number): void
  (e: 'set-role', index: number, role: RefRole): void
  (e: 'set-label', index: number, label: string): void
  (e: 'save'): void
}>()

const ROLE_OPTIONS = (Object.keys(REF_ROLE_LABELS) as RefRole[]).map((r) => ({
  value: r,
  label: REF_ROLE_LABELS[r],
}))

const ASPECT_OPTIONS = ALL_ASPECTS.map((a) => ({ value: a, label: a }))
const TIER_OPTIONS = ALL_TIERS.map((t) => ({ value: t, label: t }))

const refs = computed<PresetRef[]>(() => props.body.refs ?? [])

const renderRefs = computed<RenderRef[]>(() =>
  refs.value.map((r, index) => ({ ...r, index })),
)

/** 渲染后真正发给上游的提示词 —— 始终展示，避免「以为改了其实没改」。 */
const rendered = computed(() =>
  renderPrompt(props.body.prompt ?? '', renderRefs.value, {
    includeRefNotes: props.body.includeRefNotes !== false,
  }),
)

/** 越界引用：提示词写了 @5 但只有 2 张图。 */
const badRefs = computed(() => outOfRangeRefs(props.body.prompt ?? '', refs.value.length))

function patch(changes: Partial<PresetBody>): void {
  emit('update:body', { ...props.body, ...changes })
}

// 模板里直接调 emit 会让 TS 只能匹配到最后一个重载，
// 故为每个对外事件包一层具名函数。
// Input / Select 的事件签名是 string | number，这里统一收敛为 string。
function onName(v: string | number): void {
  emit('update:name', String(v))
}

function onCategory(v: string | number): void {
  emit('update:category', String(v))
}

function onDescription(v: string | number): void {
  emit('update:description', String(v))
}

function onPrompt(v: string | number): void {
  patch({ prompt: String(v) })
}

function onNotes(v: boolean): void {
  patch({ includeRefNotes: v })
}

function onRole(index: number, v: string | number | null): void {
  if (typeof v === 'string') emit('set-role', index, v as RefRole)
}

function onLabel(index: number, v: string | number): void {
  emit('set-label', index, String(v))
}

function onAspect(v: string | number | null): void {
  if (typeof v === 'string') patch({ aspect: v as Aspect })
}

function onTier(v: string | number | null): void {
  if (typeof v === 'string') patch({ tier: v as Tier })
}

const canSave = computed(() => {
  if (!props.name.trim()) return false
  if (!(props.body.prompt ?? '').trim()) return false
  if (badRefs.value.length) return false
  return true
})

function move(from: number, delta: number): void {
  const to = from + delta
  if (to < 0 || to >= refs.value.length) return
  emit('move-ref', from, to)
}
</script>

<template>
  <div class="pe">
    <!-- 元信息 -->
    <div class="pe__meta">
      <Input
        :model-value="name"
        label="名称"
        placeholder="如：人像换背景"
        @update:model-value="onName"
      />
      <Input
        :model-value="category"
        label="分类"
        placeholder="如：换背景"
        @update:model-value="onCategory"
      />
    </div>
    <Input
      :model-value="description"
      label="说明"
      placeholder="这个模板做什么用（可选）"
      @update:model-value="onDescription"
    />

    <!-- 尺寸偏好 -->
    <div class="pe__sizes">
      <Select
        :model-value="body.aspect ?? '1:1'"
        :options="ASPECT_OPTIONS"
        label="宽高比"
        @update:model-value="onAspect"
      />
      <Select
        :model-value="body.tier ?? '1K'"
        :options="TIER_OPTIONS"
        label="档位"
        @update:model-value="onTier"
      />
    </div>

    <!-- 参考图槽位 -->
    <section class="pe__refs">
      <header class="pe__refshead">
        <h3 class="t-sm fw-medium">参考图（按顺序）</h3>
        <Button size="sm" variant="ghost" @click="emit('pick-assets')">从资产库添加</Button>
      </header>

      <p v-if="!refs.length" class="hint">
        还没有参考图。纯文生图模板不需要参考图；图生图请先添加。
      </p>

      <ul v-else class="pe__reflist">
        <li v-for="(r, i) in refs" :key="r.assetId + i" class="pe__ref">
          <span class="pe__refno">{{ i + 1 }}</span>
          <img
            class="pe__thumb"
            :src="assetThumbURL(r.assetId)"
            :alt="`第 ${i + 1} 张参考图`"
            loading="lazy"
          />
          <div class="pe__reffields">
            <Select
              :model-value="r.role"
              :options="ROLE_OPTIONS"
              size="sm"
              :aria-label="`第 ${i + 1} 张图的角色`"
              @update:model-value="(v) => onRole(i, v)"
            />
            <Input
              :model-value="r.label ?? ''"
              size="sm"
              placeholder="备注（可选，如：阿澈）"
              :aria-label="`第 ${i + 1} 张图的备注`"
              @update:model-value="(v) => onLabel(i, v)"
            />
          </div>
          <div class="pe__refops">
            <Button
              icon
              size="sm"
              variant="ghost"
              title="上移"
              :disabled="i === 0"
              @click="move(i, -1)"
            >
              <svg viewBox="0 0 24 24" width="14" height="14" fill="currentColor">
                <path d="M12 8l6 8H6z" />
              </svg>
            </Button>
            <Button
              icon
              size="sm"
              variant="ghost"
              title="下移"
              :disabled="i === refs.length - 1"
              @click="move(i, 1)"
            >
              <svg viewBox="0 0 24 24" width="14" height="14" fill="currentColor">
                <path d="M12 16l-6-8h12z" />
              </svg>
            </Button>
            <Button
              icon
              size="sm"
              variant="ghost"
              title="移除"
              @click="emit('remove-ref', i)"
            >
              <svg viewBox="0 0 24 24" width="14" height="14" fill="currentColor">
                <path
                  d="M6 7h12l-1 13H7L6 7zm3-3h6l1 2H8l1-2z"
                />
              </svg>
            </Button>
          </div>
        </li>
      </ul>

      <p v-if="hasMissingAssets" class="error-text">
        有参考图已被删除，提交前需要移除或替换。
      </p>
    </section>

    <!-- 提示词 -->
    <Textarea
      :model-value="body.prompt ?? ''"
      label="提示词"
      :autosize="true"
      :min-rows="4"
      :max-rows="16"
      mono
      count
      placeholder="描述想要的效果，用 @1 引用第 1 张参考图、@2 引用第 2 张"
      @update:model-value="onPrompt"
    />

    <p v-if="badRefs.length" class="error-text" role="alert">
      提示词引用了不存在的参考图：{{ badRefs.map((n) => `@${n}`).join('、') }}
      （当前只有 {{ refs.length }} 张）
    </p>

    <label class="pe__notes">
      <Switch
        :model-value="body.includeRefNotes !== false"
        @update:model-value="onNotes"
      />
      <span class="t-sm">自动前置「参考图说明」段落（推荐）</span>
    </label>

    <!-- 渲染预览 -->
    <section v-if="rendered.trim()" class="pe__preview">
      <h3 class="t-sm fw-medium">实际发送给模型的提示词</h3>
      <pre class="pe__rendered">{{ rendered }}</pre>
    </section>

    <div class="pe__actions">
      <Button variant="primary" :disabled="!canSave" :loading="saving" @click="emit('save')">
        保存预设
      </Button>
    </div>
  </div>
</template>

<style scoped>
.pe {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.pe__meta,
.pe__sizes {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--space-3);
}

.pe__refs {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3);
  border: var(--border-w) solid var(--border);
  border-radius: var(--radius-md);
}

.pe__refshead {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.pe__reflist {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.pe__ref {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2);
  border-radius: var(--radius-sm);
  background: var(--surface-2);
}

.pe__refno {
  flex: none;
  display: grid;
  place-items: center;
  width: 22px;
  height: 22px;
  border-radius: var(--radius-full, 999px);
  background: var(--accent-bg);
  color: var(--accent);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.pe__thumb {
  flex: none;
  width: 44px;
  height: 44px;
  object-fit: cover;
  border-radius: var(--radius-sm);
  background: var(--surface-3);
}

.pe__reffields {
  flex: 1 1 auto;
  min-width: 0;
  display: grid;
  grid-template-columns: minmax(96px, 128px) 1fr;
  gap: var(--space-2);
}

.pe__refops {
  flex: none;
  display: flex;
  gap: var(--space-1);
}

.pe__notes {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  cursor: pointer;
}

.pe__preview {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3);
  border: var(--border-w) solid var(--border);
  border-radius: var(--radius-md);
  background: var(--surface-2);
}

.pe__rendered {
  margin: 0;
  padding: var(--space-2);
  max-height: 220px;
  overflow: auto;
  border-radius: var(--radius-sm);
  background: var(--surface-1);
  color: var(--text-2);
  font-size: var(--fs-sm);
  line-height: var(--lh-relaxed, 1.6);
  white-space: pre-wrap;
  word-break: break-word;
  font-family: var(--font-mono, ui-monospace, monospace);
}

.pe__actions {
  display: flex;
  justify-content: flex-end;
}

@media (max-width: 720px) {
  .pe__meta,
  .pe__sizes,
  .pe__reffields {
    grid-template-columns: 1fr;
  }
}
</style>
