/**
 * 任务状态：当前/近期任务列表、进度订阅、轮询降级、队列水位。
 *
 * 订阅策略（PLAN §7.4）：先 SSE（JobEventStream），SSE 反复失败（onGiveUp）回落到
 * 轮询 api.getJob；刷新后从 localStorage 恢复未完成任务继续跟单。
 */
import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { api } from '@/api/client'
import { JobEventStream, type SSEOptions } from '@/api/sse'
import type { GenerateRequest, Health, Job, JobFilter, JobStatus } from '@/types'

const RESUME_KEY = 'imgen: current-job'
const TERMINAL: JobStatus[] = ['done', 'failed', 'canceled']

export function isTerminal(s: JobStatus | string): boolean {
  return TERMINAL.includes(s as JobStatus)
}

interface Handle {
  stream?: JobEventStream
  poll?: ReturnType<typeof setTimeout>
  /** true 表示已降级为轮询，避免重复启动。 */
  polling: boolean
  retries: number
}

export const useJobsStore = defineStore('jobs', () => {
  // ---------------------------------------------------------------- state
  /** 工作台当前跟踪的任务。 */
  const current = ref<Job | null>(null)
  /** 近期任务（历史页用，倒序）。 */
  const recent = ref<Job[]>([])
  const total = ref(0)
  const submitting = ref(false)
  const error = ref('')
  /** 最近一次失败是否可重试 —— 决定弹「重试」还是「改提示词」。 */
  const lastRetryable = ref(false)
  const health = ref<Health | null>(null)
  /** 阶段文案；running 时的细化描述。 */
  const stage = ref('')
  const startedMs = ref(0)
  /** 用于进度条/耗时显示的 tick，每 500ms 自增。 */
  const tick = ref(0)

  /** jobId → 订阅句柄。 */
  const handles = new Map<string, Handle>()
  let tickTimer: ReturnType<typeof setInterval> | null = null
  let healthTimer: ReturnType<typeof setInterval> | null = null

  // ---------------------------------------------------------------- 派生
  const busy = computed(() => {
    if (!current.value) return false
    return !isTerminal(current.value.status)
  })

  /** 已完成 / 部分成功的图片总数（用于批次进度）。 */
  const finishedImages = computed(() => {
    const j = current.value
    if (!j) return 0
    return j.images.filter((i) => i.status === 'done').length
  })

  const progress = computed(() => {
    const j = current.value
    if (!j) return 0
    if (isTerminal(j.status)) return j.status === 'done' ? 100 : j.progress || 0
    if (j.status === 'running') return Math.max(6, j.progress || 0)
    return Math.max(2, j.progress || 0)
  })

  const elapsedMs = computed(() => {
    void tick.value
    const j = current.value
    if (!j) return 0
    const start = startedMs.value || Date.parse(j.createdAt)
    if (!start) return 0
    const end = j.finishedAt ? Date.parse(j.finishedAt) : Date.now()
    return Math.max(0, end - start)
  })

  /** 队列水位：排队数 / 并发上限。 */
  const queued = computed(() => health.value?.queued ?? 0)
  const workers = computed(() => health.value?.workers ?? 0)
  const queueFull = computed(() => workers.value > 0 && queued.value >= workers.value * 2)

  // ---------------------------------------------------------------- 内部
  function upsert(job: Job): void {
    const i = recent.value.findIndex((j) => j.id === job.id)
    if (i >= 0) recent.value[i] = job
    else recent.value.unshift(job)
    recent.value.sort((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt))
    if (current.value?.id === job.id) {
      if (job.status === 'running' && current.value.status !== 'running') {
        startedMs.value = Date.now()
      }
      current.value = job
    }
  }

  function stopHandle(id: string): void {
    const h = handles.get(id)
    if (!h) return
    h.stream?.close()
    if (h.poll) clearTimeout(h.poll)
    handles.delete(id)
  }

  function stopAll(): void {
    for (const id of [...handles.keys()]) stopHandle(id)
    if (tickTimer) clearInterval(tickTimer)
    tickTimer = null
    if (healthTimer) clearInterval(healthTimer)
    healthTimer = null
  }

  function startTick(): void {
    if (tickTimer) return
    tickTimer = setInterval(() => {
      tick.value += 1
    }, 500)
  }

  function stopTickIfIdle(): void {
    if (busy.value) return
    if (tickTimer) clearInterval(tickTimer)
    tickTimer = null
  }

  /** SSE 不可用时的轮询：1s 起步，指数退避到 8s。 */
  function poll(id: string, handle: Handle): void {
    const delay = Math.min(8000, 1000 * 2 ** Math.min(handle.retries, 3))
    handle.poll = setTimeout(async () => {
      if (!handle.polling) return
      try {
        const job = await api.getJob(id)
        handle.retries = 0
        upsert(job)
        applyStage(job)
        if (isTerminal(job.status)) {
          stopHandle(id)
          stopTickIfIdle()
          persist(id, false)
          return
        }
        poll(id, handle)
      } catch {
        handle.retries += 1
        poll(id, handle)
      }
    }, delay)
  }

  function applyStage(job: Job): void {
    if (job.status === 'queued') stage.value = '排队中，等待空闲 worker'
    else if (job.status === 'running') stage.value = '生成中，上游返回可能需要数十秒'
    else if (job.status === 'partial') stage.value = '部分完成，剩余仍在重试'
    else stage.value = ''
  }

  function persist(id: string, active: boolean): void {
    try {
      if (active) localStorage.setItem(RESUME_KEY, id)
      else localStorage.removeItem(RESUME_KEY)
    } catch {
      // localStorage 不可用时静默降级：刷新后不自动恢复。
    }
  }

  function getPersisted(): string {
    try {
      return localStorage.getItem(RESUME_KEY) ?? ''
    } catch {
      return ''
    }
  }

  /** 订阅任务：SSE 优先，失败后落轮询。 */
  function watch(id: string): void {
    const handle: Handle = { polling: false, retries: 0 }
    handles.set(id, handle)
    persist(id, true)
    startTick()

    let gaveUp = false
    const opts: SSEOptions = {
      onEvent: (ev) => {
        const cur = current.value
        if (cur && cur.id === id) {
          const next: Job = {
            ...cur,
            status: (ev.status as JobStatus) ?? cur.status,
            progress: ev.progress ?? cur.progress,
            latencyMs: ev.latencyMs ?? cur.latencyMs,
            error: ev.error ?? cur.error,
          }
          if (ev.kind === 'partial' || ev.kind === 'done') {
            const list = [...cur.images]
            const seq = ev.seq ?? list.length
            const i = list.findIndex((x) => x.seq === seq)
            if (i >= 0) list[i] = { ...list[i], seq, assetId: ev.assetId, status: 'done' }
            else list.push({ seq, assetId: ev.assetId, status: 'done' })
            next.images = list.sort((a, b) => a.seq - b.seq)
          }
          current.value = next
          upsert(next)
          applyStage(next)
          if (isTerminal(next.status)) {
            stopHandle(id)
            stopTickIfIdle()
            persist(id, false)
          }
        }
      },
      onError: () => {
        stage.value = '进度通道不稳定，切换为轮询'
      },
      onGiveUp: () => {
        if (gaveUp) return
        gaveUp = true
        handle.polling = true
        handle.stream?.close()
        handle.stream = undefined
        stage.value = '已降级为轮询'
        poll(id, handle)
      },
    }

    const es = new JobEventStream(id, opts)
    handle.stream = es
    es.start()
  }

  // ---------------------------------------------------------------- actions
  async function submit(req: GenerateRequest): Promise<Job | null> {
    submitting.value = true
    error.value = ''
    lastRetryable.value = false
    try {
      const job = await api.createJob(req)
      recent.value.unshift(job)
      current.value = job
      startedMs.value = Date.now()
      stage.value = '已提交，排队中'
      applyStage(job)
      watch(job.id)
      startTick()
      return job
    } catch (e) {
      const msg = (e as Error).message || '提交失败'
      error.value = msg
      lastRetryable.value = Boolean((e as { retryable?: boolean }).retryable)
      return null
    } finally {
      submitting.value = false
    }
  }

  async function cancel(id: string): Promise<void> {
    try {
      const job = await api.cancelJob(id)
      upsert(job)
      stopHandle(id)
    } catch (e) {
      error.value = (e as Error).message || '取消失败'
    }
  }

  function open(job: Job): void {
    current.value = job
    startedMs.value = Date.parse(job.createdAt) || Date.now()
    applyStage(job)
    if (!isTerminal(job.status)) {
      startTick()
      watch(job.id)
    }
  }

  function clearCurrent(): void {
    if (current.value) stopHandle(current.value.id)
    current.value = null
    stage.value = ''
    startedMs.value = 0
  }

  async function refresh(): Promise<Job | null> {
    const id = current.value?.id
    if (!id) return null
    try {
      const job = await api.getJob(id)
      upsert(job)
      applyStage(job)
      return job
    } catch {
      return null
    }
  }

  async function list(f: JobFilter = {}): Promise<void> {
    try {
      const r = await api.listJobs({ limit: 60, ...f })
      recent.value = [...r.items].sort((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt))
      total.value = r.total
    } catch (e) {
      error.value = (e as Error).message || '历史加载失败'
    }
  }

  async function remove(): Promise<void> {
    clearCurrent()
  }

  /** 队列水位轮询（10s 一次，够用了）。 */
  function startHealthProbe(): void {
    if (healthTimer) return
    const probe = async () => {
      try {
        health.value = await api.health({ timeoutMs: 5000 })
      } catch {
        // 心跳失败不打扰用户，水位保持上次值。
      }
    }
    void probe()
    healthTimer = setInterval(probe, 10_000)
  }

  /** 刷新后恢复未完成任务。 */
  async function resume(): Promise<Job | null> {
    const id = getPersisted()
    if (!id) return null
    try {
      const job = await api.getJob(id)
      upsert(job)
      if (isTerminal(job.status)) {
        persist(id, false)
        return job
      }
      open(job)
      return job
    } catch {
      return null
    }
  }

  return {
    current,
    recent,
    total,
    submitting,
    error,
    lastRetryable,
    health,
    stage,
    tick,
    busy,
    finishedImages,
    progress,
    elapsedMs,
    queued,
    workers,
    queueFull,
    submit,
    cancel,
    open,
    clearCurrent,
    refresh,
    list,
    remove,
    startHealthProbe,
    resume,
    stopAll,
    upsert,
  }
})
