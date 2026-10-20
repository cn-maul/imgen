/**
 * ui 层共享类型。
 * 放在这里而不是各 SFC 内 export —— Vue 的 <script setup> 不允许 ES 值导出，
 * 类型导出也不稳妥，统一抽到 .ts 供组件与视图层 import type。
 */

// ------------------------------------------------------------------ Button

export type ButtonVariant =
  | 'primary'
  | 'secondary'
  | 'ghost'
  | 'outline'
  | 'danger'
  | 'danger-ghost'
  | 'link'

export type ButtonSize = 'sm' | 'md' | 'lg'

// ---------------------------------------------------------------- 通用尺寸

export type ControlSize = 'sm' | 'md' | 'lg'

/** error 既可以是布尔标记（外部自己渲染 error-text），也可以是直接显示的消息。 */
export type ErrorLike = boolean | string

// ------------------------------------------------------------------- Input

export type InputType = 'text' | 'password' | 'number' | 'url' | 'search' | 'email'

// ------------------------------------------------------------------ Select

export interface SelectOption {
  value: string
  label: string
  disabled?: boolean
  /** 右侧辅助说明（如尺寸档位的像素值）。 */
  hint?: string
}

// -------------------------------------------------------------------- Tabs

export interface TabItem {
  value: string
  label: string
  badge?: string | number
  disabled?: boolean
  /** 传了则渲染右侧小徽标，badge 优先。 */
  icon?: string
}

export type TabsVariant = 'line' | 'pills' | 'segmented'

// ------------------------------------------------------------------- Badge

export type BadgeTone =
  | 'neutral'
  | 'accent'
  | 'ok'
  | 'warn'
  | 'danger'
  | 'solid-accent'
  | 'solid-ok'
  | 'solid-warn'
  | 'solid-danger'
  | 'outline'

// ----------------------------------------------------------------- Spinner

export type SpinnerSize = 'sm' | 'md' | 'lg' | 'xl'
export type SpinnerTone = 'accent' | 'muted' | 'inherit'

// ---------------------------------------------------------------- Skeleton

export type SkeletonVariant = 'text' | 'rect' | 'circle'

// ---------------------------------------------------------------- 浮层尺寸

export type ModalSize = 'sm' | 'md' | 'lg' | 'xl' | 'full'
export type DrawerSize = 'sm' | 'md' | 'lg' | 'xl'

// --------------------------------------------------------------- Progress

export type ProgressTone = 'accent' | 'ok' | 'error'

// ------------------------------------------------------------------- Toast

export type ToastKind = 'info' | 'success' | 'warn' | 'error' | 'loading'
