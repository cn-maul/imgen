<script setup lang="ts">
/**
 * 系统设置：并发上限 / 上传体积上限 / 保留天数 + 用量统计 + 健康检查。
 *
 * 并发上限是后端 worker pool 的大小（默认 2），调大前先看 Agnes 的 RPM 档位表
 * （1K=10、2K=5、3K=1、4K=1）—— 4K 档调高并发必然 429。
 */
import { computed, onMounted, ref } from 'vue'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import Card from '@/components/ui/Card.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import { useToast } from '@/components/ui/useToast'
import { useSettingsStore } from '@/stores/settings'
import { useJobsStore } from '@/stores/jobs'

const settings = useSettingsStore()
const jobs = useJobsStore()
const toast = useToast()

const form = ref({ concurrency: 2, maxUploadMB: 20, keepDays: 30 })
const days = ref(14)

const daysOptions = [
  { value: 7, label: '近 7 天' },
  { value: 14, label: '近 14 天' },
  { value: 30, label: '近 30 天' },
]

onMounted(async () => {
  await settings.load()
  form.value = {
    concurrency: settings.settings.concurrency,
    maxUploadMB: settings.settings.maxUploadMB,
    keepDays: settings.settings.keepDays,
  }
  await settings.loadUsage(days.value)
  jobs.startHealthProbe()
})

const dirty = computed(() => {
  const s = settings.settings
  return (
    form.value.concurrency !== s.concurrency ||
    form.value.maxUploadMB !== s.maxUploadMB ||
    form.value.keepDays !== s.keepDays
  )
})

async function save(): Promise<void> {
  // 直接改 store 再 save —— store.save() 会做钳制并回写归一化结果。
  settings.settings.concurrency = form.value.concurrency
  settings.settings.maxUploadMB = form.value.maxUploadMB
  settings.settings.keepDays = form.value.keepDays
  const ok = await settings.save()
  if (!ok) {
    toast.error(settings.error || '保存失败')
    return
  }
  form.value = {
    concurrency: settings.settings.concurrency,
    maxUploadMB: settings.settings.maxUploadMB,
    keepDays: settings.settings.keepDays,
  }
  toast.success('设置已保存')
}

function reset(): void {
  form.value = {
    concurrency: settings.settings.concurrency,
    maxUploadMB: settings.settings.maxUploadMB,
    keepDays: settings.settings.keepDays,
  }
}

async function reloadUsage(): Promise<void> {
  await settings.loadUsage(days.value)
}

const successRate = computed(() => {
  const t = settings.usageTotal
  if (t.calls === 0) return '—'
  return `${Math.round((t.ok / t.calls) * 100)}%`
})

const health = computed(() => jobs.health)

/** 按 provider+model 聚合，用表格展示。 */
const usageRows = computed(() => settings.usage?.items ?? [])
</script>

<template>
  <div class="sys stack">
    <header class="stack stack--1">
      <h2 class="t-xl">系统设置</h2>
      <p class="hint">并发、上传上限与历史保留策略。</p>
    </header>

    <div class="sys__cols">
      <!-- ------------------------------------------------------ 设置表单 -->
      <Card title="运行参数" class="sys__form">
        <div class="stack stack--4">
          <label class="field">
            <span class="field__label">全局并发上限</span>
            <Input v-model="form.concurrency" type="number" :min="1" :max="8" />
            <span class="field__hint">
              worker pool 大小。Agnes 4K 档只有 1 RPM，调高会直接撞 429 退避。
            </span>
          </label>

          <label class="field">
            <span class="field__label">单张上传上限（MB）</span>
            <Input v-model="form.maxUploadMB" type="number" :min="1" :max="200" />
            <span class="field__hint">超过这个体积的图片在上传前会被前端拦下。</span>
          </label>

          <label class="field">
            <span class="field__label">历史保留天数</span>
            <Input v-model="form.keepDays" type="number" :min="0" :max="365" />
            <span class="field__hint">
              终态任务（完成/失败/取消）按结束时间计算保留期，到期后连同它产出的图片一起删除；
              只被这些任务引用的资产也会被清掉，被预设或未引用任务用到的资产会保留。0 表示不清理。
            </span>
          </label>

          <div class="row row--tight">
            <Button variant="primary" size="md" :loading="settings.saving" :disabled="!dirty" @click="save">
              保存
            </Button>
            <Button variant="ghost" size="md" :disabled="!dirty" @click="reset">撤销</Button>
            <Badge v-if="dirty" tone="warn" size="sm">有未保存改动</Badge>
          </div>

          <p v-if="settings.error" class="error-text">{{ settings.error }}</p>
        </div>
      </Card>

      <!-- ------------------------------------------------------ 右侧信息栏 -->
      <div class="stack stack--4">
        <Card title="服务状态">
          <div class="stack stack--2">
            <div class="row row--between">
              <span class="hint">健康</span>
              <Badge :tone="health?.ok ? 'ok' : 'danger'" size="sm" dot>
                {{ health?.ok ? '正常' : '异常' }}
              </Badge>
            </div>
            <div class="row row--between">
              <span class="hint">版本</span>
              <span class="mono t-sm">{{ health?.version || '—' }}</span>
            </div>
            <div class="row row--between">
              <span class="hint">构建时间</span>
              <span class="mono t-sm">{{ health?.buildTime || '—' }}</span>
            </div>
            <div class="row row--between">
              <span class="hint">队列水位</span>
              <span class="mono t-sm">{{ jobs.queued }} / {{ jobs.workers }}</span>
            </div>
            <div class="row row--between">
              <span class="hint">已注册通道</span>
              <span class="mono t-sm">{{ health?.providers ?? '—' }}</span>
            </div>
          </div>
        </Card>

        <Card title="用量统计">
          <template #head>
            <div class="card__title">
              <span>用量统计</span>
              <span class="card__subtitle">
                成功率 {{ successRate }} · 共 {{ settings.usageTotal.images }} 张
              </span>
            </div>
            <div class="row row--tight flex-none">
              <Select
                :model-value="String(days)"
                :options="daysOptions.map((d) => ({ value: String(d.value), label: d.label }))"
                size="sm"
                @update:model-value="days = Number($event); reloadUsage()"
              />
            </div>
          </template>

          <div v-if="!usageRows.length" class="hint">这段时间还没有调用记录。</div>

          <div v-else class="sys__table-wrap">
            <table class="sys__table">
              <thead>
                <tr>
                  <th>日期</th>
                  <th>通道</th>
                  <th class="text-right">调用</th>
                  <th class="text-right">成功</th>
                  <th class="text-right">失败</th>
                  <th class="text-right">出图</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="row in usageRows" :key="`${row.day}-${row.provider}-${row.model}`">
                  <td class="mono t-xs">{{ row.day }}</td>
                  <td class="t-xs truncate">{{ row.provider }}</td>
                  <td class="text-right mono t-xs">{{ row.calls }}</td>
                  <td class="text-right mono t-xs text-ok">{{ row.ok }}</td>
                  <td class="text-right mono t-xs" :class="{ 'text-danger': row.failed > 0 }">
                    {{ row.failed }}
                  </td>
                  <td class="text-right mono t-xs">{{ row.images }}</td>
                </tr>
              </tbody>
            </table>
          </div>

          <div class="row row--tight row--wrap">
            <Badge size="sm" outline>调用 {{ settings.usageTotal.calls }}</Badge>
            <Badge size="sm" tone="ok">成功 {{ settings.usageTotal.ok }}</Badge>
            <Badge v-if="settings.usageTotal.failed" size="sm" tone="danger">
              失败 {{ settings.usageTotal.failed }}
            </Badge>
            <Badge size="sm" tone="accent">出图 {{ settings.usageTotal.images }}</Badge>
          </div>
        </Card>
      </div>
    </div>

    <p class="hint">
      当前队列：{{ jobs.queued }} 个任务排队，并发上限 {{ jobs.workers }}。
      <span v-if="jobs.queueFull" class="text-warn">队列已满，新任务会显示「排队中」。</span>
    </p>
  </div>
</template>

<style scoped>
.sys {
  height: 100%;
  min-height: 0;
}

.sys__cols {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: var(--space-4);
  align-items: start;
}

.sys__table-wrap {
  max-height: 280px;
  overflow-y: auto;
}

.sys__table {
  width: 100%;
  font-size: var(--fs-sm);
}

.sys__table th {
  position: sticky;
  top: 0;
  padding: var(--space-2);
  background: var(--surface-2);
  color: var(--text-3);
  font-weight: var(--fw-medium);
  text-align: left;
  border-bottom: var(--border-w) solid var(--border);
}

.sys__table td {
  padding: var(--space-2);
  border-bottom: var(--border-w) solid var(--border);
  color: var(--text-2);
}

@media (max-width: 1080px) {
  .sys__cols {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
