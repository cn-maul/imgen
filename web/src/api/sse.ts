/**
 * 任务进度订阅。SSE 断线时按指数退避重连；
 * 浏览器不支持或反复失败时，调用方应回落到轮询。
 */
import type { JobEvent } from '@/types'

export interface SSEOptions {
  /** 终态事件回调之后自动关闭连接。 */
  onEvent: (ev: JobEvent) => void
  onError?: (e: Error) => void
  /** 连接彻底放弃（超过最大重试次数）时回调，调用方据此转轮询。 */
  onGiveUp?: () => void
  maxRetries?: number
}

const TERMINAL: ReadonlySet<string> = new Set(['done', 'failed'])

export class JobEventStream {
  private es: EventSource | null = null
  private retries = 0
  private closed = false
  private readonly maxRetries: number

  constructor(
    private readonly jobId: string,
    private readonly opts: SSEOptions,
  ) {
    this.maxRetries = opts.maxRetries ?? 4
  }

  start(): void {
    if (this.closed) return
    this.open()
  }

  private open(): void {
    if (this.closed) return
    const url = `/api/v1/jobs/${encodeURIComponent(this.jobId)}/events`
    const es = new EventSource(url)
    this.es = es

    es.onmessage = (e: MessageEvent<string>) => {
      // 服务端终态会关闭频道；浏览器表现为反复触发 onerror，
      // 这里靠 TERMINAL 判定主动收尾，避免重连已结束的任务。
      let ev: JobEvent
      try {
        ev = JSON.parse(e.data) as JobEvent
      } catch {
        return
      }
      this.retries = 0
      this.opts.onEvent(ev)
      if (TERMINAL.has(ev.kind)) {
        this.close()
      }
    }

    es.onerror = () => {
      es.close()
      this.es = null
      if (this.closed) return
      if (this.retries >= this.maxRetries) {
        this.opts.onError?.(new Error('SSE 连接失败，转轮询'))
        this.opts.onGiveUp?.()
        this.closed = true
        return
      }
      // 指数退避：1s / 2s / 4s / 8s
      const delay = 1000 * 2 ** this.retries
      this.retries += 1
      setTimeout(() => this.open(), delay)
    }
  }

  close(): void {
    this.closed = true
    this.es?.close()
    this.es = null
  }
}
