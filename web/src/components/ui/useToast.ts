/**
 * 全局 Toast 队列。
 *
 * 设计：模块级单例响应式队列 + ToastHost.vue 渲染。
 * 不依赖 Pinia，任何地方（组件内、store 内、api 拦截器内）都能直接调用。
 *
 * 用法：
 *   import { useToast } from '@/components/ui/useToast'
 *   const toast = useToast()
 *   const id = toast.push({ kind: 'error', message: 'Key 无效' })
 *   toast.success('已保存')
 *   const id = toast.loading('生成中…')       // 不会自动消失
 *   toast.update(id, { kind: 'success', message: '完成', timeout: 3000 })
 *   toast.dismiss(id)
 *   toast.clear()
 */
import { reactive } from 'vue'

export type ToastKind = 'info' | 'success' | 'warn' | 'error' | 'loading'

export interface ToastAction {
  label: string
  /** 点击后执行；返回 true 或 undefined 时顺带关闭该 toast。 */
  onClick: () => void | boolean
}

export interface ToastOptions {
  kind?: ToastKind
  /** 主标题，可选；无标题时只显示 message。 */
  title?: string
  /** 正文，必填（loading 态给过程文案）。 */
  message: string
  /**
   * 自动消失毫秒数。
   * - 默认按 kind：info/success 3000，warn 4500，error 6000，loading 0（不消失）
   * - 传 0 表示常驻，必须手动 dismiss 或 update
   */
  timeout?: number
  /** 最多 2 个动作按钮（如「重试」）。 */
  actions?: ToastAction[]
  /** 是否显示关闭按钮，默认 true。 */
  dismissible?: boolean
  /** 同 dedupeKey 的 toast 只保留最新一条，用于防抖动刷屏。 */
  dedupeKey?: string
}

export interface ToastItem extends Required<Pick<ToastOptions, 'message'>> {
  id: number
  kind: ToastKind
  title?: string
  timeout: number
  actions: ToastAction[]
  dismissible: boolean
  dedupeKey?: string
  createdAt: number
}

const DEFAULT_TIMEOUT: Record<ToastKind, number> = {
  info: 3000,
  success: 3000,
  warn: 4500,
  error: 6000,
  loading: 0,
}

const MAX_VISIBLE = 6

const state = reactive<{ items: ToastItem[] }>({ items: [] })

let seq = 0
const timers = new Map<number, ReturnType<typeof setTimeout>>()

function clearTimer(id: number): void {
  const t = timers.get(id)
  if (t !== undefined) {
    clearTimeout(t)
    timers.delete(id)
  }
}

function armTimer(item: ToastItem): void {
  clearTimer(item.id)
  if (item.timeout > 0) {
    timers.set(
      item.id,
      setTimeout(() => dismiss(item.id), item.timeout),
    )
  }
}

function dismiss(id: number): void {
  clearTimer(id)
  const i = state.items.findIndex((t) => t.id === id)
  if (i >= 0) state.items.splice(i, 1)
}

function push(options: ToastOptions): number {
  const kind = options.kind ?? 'info'
  const id = ++seq

  if (options.dedupeKey) {
    for (const t of state.items) {
      if (t.dedupeKey === options.dedupeKey) dismiss(t.id)
    }
  }

  const item: ToastItem = {
    id,
    kind,
    title: options.title,
    message: options.message,
    timeout: options.timeout ?? DEFAULT_TIMEOUT[kind],
    actions: options.actions ?? [],
    dismissible: options.dismissible ?? true,
    dedupeKey: options.dedupeKey,
    createdAt: Date.now(),
  }

  state.items.push(item)
  armTimer(item)

  // 超出上限时挤掉最旧的非常驻 toast
  while (state.items.length > MAX_VISIBLE) {
    const victim = state.items.find((t) => t.timeout > 0)
    if (!victim) break
    dismiss(victim.id)
  }

  return id
}

function update(id: number, patch: Partial<ToastOptions>): void {
  const item = state.items.find((t) => t.id === id)
  if (!item) return
  if (patch.kind !== undefined) item.kind = patch.kind
  if (patch.title !== undefined) item.title = patch.title
  if (patch.message !== undefined) item.message = patch.message
  if (patch.actions !== undefined) item.actions = patch.actions
  if (patch.dismissible !== undefined) item.dismissible = patch.dismissible
  if (patch.timeout !== undefined) {
    item.timeout = patch.timeout
  } else if (patch.kind !== undefined) {
    item.timeout = DEFAULT_TIMEOUT[patch.kind]
  }
  armTimer(item)
}

function clear(): void {
  for (const t of [...state.items]) dismiss(t.id)
}

export interface ToastApi {
  /** 当前队列（只读，供 ToastHost 渲染）。 */
  items: readonly ToastItem[]
  push: (options: ToastOptions) => number
  info: (message: string, options?: Omit<ToastOptions, 'message' | 'kind'>) => number
  success: (message: string, options?: Omit<ToastOptions, 'message' | 'kind'>) => number
  warn: (message: string, options?: Omit<ToastOptions, 'message' | 'kind'>) => number
  error: (message: string, options?: Omit<ToastOptions, 'message' | 'kind'>) => number
  /** 常驻过程提示，返回 id，用 update 收尾。 */
  loading: (message: string, options?: Omit<ToastOptions, 'message' | 'kind'>) => number
  update: (id: number, patch: Partial<ToastOptions>) => void
  dismiss: (id: number) => void
  clear: () => void
}

const api: ToastApi = {
  items: state.items,
  push,
  info: (message, o) => push({ ...o, kind: 'info', message }),
  success: (message, o) => push({ ...o, kind: 'success', message }),
  warn: (message, o) => push({ ...o, kind: 'warn', message }),
  error: (message, o) => push({ ...o, kind: 'error', message }),
  loading: (message, o) => push({ ...o, kind: 'loading', message, timeout: 0 }),
  update,
  dismiss,
  clear,
}

/** 获取全局 toast 控制器（单例，可重复调用）。 */
export function useToast(): ToastApi {
  return api
}

/** 供 ToastHost 内部或测试直接读取可变队列。 */
export function toastState() {
  return state
}
