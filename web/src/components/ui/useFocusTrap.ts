/**
 * 焦点陷阱 + Esc 关闭 + 打开/关闭时焦点转移。
 * 供 Modal / Drawer / Lightbox 共用，不依赖任何库。
 */
import { onBeforeUnmount, watch, type Ref } from 'vue'

const FOCUSABLE_SELECTOR = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled]):not([type="hidden"])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
  '[contenteditable="true"]',
].join(',')

function focusable(root: HTMLElement): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR)).filter(
    (el) => {
      if (el.hasAttribute('disabled')) return false
      if (el.getAttribute('aria-hidden') === 'true') return false
      const style = el.ownerDocument.defaultView?.getComputedStyle(el)
      if (style && (style.display === 'none' || style.visibility === 'hidden')) {
        return false
      }
      return el.tabIndex !== -1
    },
  )
}

export interface TrapOptions {
  /** 打开时是否把焦点移入容器。默认 true。 */
  autoFocus?: boolean
  /** 关闭后是否把焦点还给打开前的元素。默认 true。 */
  restoreFocus?: boolean
  /** Esc 回调；不传则 Esc 什么都不做。 */
  onEscape?: () => void
}

export interface TrapHandle {
  /** 手动把焦点移入容器（第一个可聚焦元素，或容器本身）。 */
  focusFirst: () => void
  release: () => void
}

/**
 * 在 container 上建立焦点陷阱。open 由 false → true 时激活。
 * 组件卸载会自动清理。
 */
export function useFocusTrap(
  container: Ref<HTMLElement | null | undefined>,
  open: Ref<boolean>,
  options: TrapOptions = {},
): TrapHandle {
  const { autoFocus = true, restoreFocus = true, onEscape } = options

  let previous: HTMLElement | null = null
  let active = false

  function focusFirst(): void {
    const el = container.value
    if (!el) return
    const items = focusable(el)
    const target = items[0] ?? (el.tabIndex >= 0 ? el : null)
    if (target) {
      target.focus({ preventScroll: true })
    } else {
      // 容器自身不可聚焦时临时降级，保证后续 Tab 仍被捕获
      el.setAttribute('tabindex', '-1')
      el.focus({ preventScroll: true })
    }
  }

  function onKeydown(e: KeyboardEvent): void {
    if (!active) return
    if (e.key === 'Escape') {
      e.stopPropagation()
      onEscape?.()
      return
    }
    if (e.key !== 'Tab') return

    const el = container.value
    if (!el) return
    const items = focusable(el)
    if (items.length === 0) {
      e.preventDefault()
      el.focus({ preventScroll: true })
      return
    }

    const first = items[0]!
    const last = items[items.length - 1]!
    const current = document.activeElement as HTMLElement | null

    if (!e.shiftKey && (current === last || !el.contains(current))) {
      e.preventDefault()
      first.focus({ preventScroll: true })
    } else if (e.shiftKey && (current === first || !el.contains(current))) {
      e.preventDefault()
      last.focus({ preventScroll: true })
    }
  }

  function release(): void {
    if (!active) return
    active = false
    document.removeEventListener('keydown', onKeydown, true)
    if (restoreFocus && previous && previous.isConnected) {
      previous.focus({ preventScroll: true })
    }
    previous = null
  }

  watch(
    open,
    (isOpen) => {
      if (isOpen) {
        if (active) return
        previous = document.activeElement as HTMLElement | null
        active = true
        document.addEventListener('keydown', onKeydown, true)
        if (autoFocus) {
          // 等 DOM 渲染完成
          requestAnimationFrame(() => focusFirst())
        }
      } else {
        release()
      }
    },
    { immediate: true },
  )

  onBeforeUnmount(release)

  return { focusFirst, release }
}
