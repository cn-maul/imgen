/**
 * 提示词渲染：把「原始提示词 + 有序参考图」渲染成真正发给上游的文本。
 *
 * 为什么需要这一步
 * ----------------
 * SenseNova 与 Agnes 的 API 都**没有 per-image 标签参数**。参考图只能作为一个
 * 有序数组下发，模型区分「你指的是哪一张图」的唯一可靠依据就是**数组顺序**。
 * 裸塞 `<img1>` / `@1` 这类标记两家都不会报错，但语义是否被正确理解无法保证
 * （实测两家对 `<img1>` 均正常返回 200，说明它们只是当作普通文本）。
 *
 * 所以这里给模型两个信号：
 *   1. 位置信号 —— 「第1张参考图」这类行内措辞，对应数组第 1 项；
 *   2. 语义信号 —— 前置的图注说明每张图扮演什么角色（场景/人物/风格…）。
 * 两者结合比只塞标记稳得多，且渲染结果对用户完全可见、可调、可复现。
 *
 * 本模块刻意零依赖：角色定义也放在这里而不是 types.ts，
 * 一是因为 RefRole 是纯前端概念（后端只把预设 body 当不透明 JSON 存），
 * 二是这样可以用 `node --experimental-strip-types` 直接跑校验脚本，
 * 不必为了测一个纯函数引入测试框架。
 */

/**
 * 参考图在提示词里的角色。
 *
 * 两家上游 API 都没有 per-image 标签参数，参考图只能作为有序数组下发，
 * 模型区分「哪张图」的唯一可靠依据是**数组顺序**。
 * 角色的作用是在顺序之上补一层语义（这张是背景还是人物），
 * 渲染时会被写进图注，比塞 <img1> 这类裸标记更可靠。
 */
export type RefRole = 'scene' | 'subject' | 'style' | 'pose' | 'outfit' | 'template'

/** 角色的中文展示名，同时用于 UI 选择器与图注。 */
export const REF_ROLE_LABELS: Record<RefRole, string> = {
  scene: '场景/背景',
  subject: '人物/主体',
  style: '风格参考',
  pose: '姿态/构图',
  outfit: '服装/外观',
  template: '模板/版式',
}

/** 预设里的一张参考图。 */
export interface PresetRef {
  assetId: string
  role: RefRole
  /** 给这张图起的名字（可选），用于让图注更具体。 */
  label?: string
}

/**
 * 占位符匹配：`@1` ~ `@9`。
 *
 * 前后都要求不是 ASCII 字母数字，否则会误伤正文里的邮箱与版本号：
 * `a@1x.com` 的 `@1`、`12@3` 的 `@3` 都不是占位符。
 * 中文（如「把@1的人物」）不受该限制 —— 汉字不属于 [A-Za-z0-9]，
 * 所以紧贴中文写的占位符照常生效。
 */
const PLACEHOLDER = /(?<![A-Za-z0-9])@(\d)(?![A-Za-z0-9])/g

/** 图注里每个角色的描述短语，比直接用 UI 文案更像自然语言指令。 */
const ROLE_PHRASE: Record<PresetRef['role'], string> = {
  scene: '提供场景与背景',
  subject: '提供人物或主体',
  style: '提供画风与质感',
  pose: '提供姿态与构图',
  outfit: '提供服装与外观',
  template: '提供版式与布局',
}

/** 单张参考图在渲染时用到的信息。 */
export interface RenderRef extends PresetRef {
  /** 该图在数组中的位置（0 起），由调用方按提交顺序给出。 */
  index: number
}

export interface RenderOptions {
  /** 是否前置「参考图说明」段落，默认 true。 */
  includeRefNotes?: boolean
}

/**
 * 提取提示词里用到的所有 `@N` 序号（去重、升序）。
 *
 * 供 UI 提示「本预设引用了第 1、2 张图」以及校验越界引用。
 */
export function usedIndexes(prompt: string): number[] {
  const found = new Set<number>()
  for (const m of prompt.matchAll(PLACEHOLDER)) {
    found.add(Number(m[1]))
  }
  return [...found].sort((a, b) => a - b)
}

/**
 * 找出提示词里越界的 `@N`（N 超出参考图数量）。
 *
 * 上游不会因为这个报错，但模型会拿到一个指不到任何图的指令，
 * 表现为「莫名少改了一处」，很难排查，因此在 UI 侧提前拦下。
 */
export function outOfRangeRefs(prompt: string, refCount: number): number[] {
  return usedIndexes(prompt).filter((n) => n < 1 || n > refCount)
}

/** 把单张图的图注条目拼成一行，如「第2张参考图（人物/主体：阿澈）：提供人物或主体」。 */
function refLine(ref: RenderRef): string {
  const role = REF_ROLE_LABELS[ref.role] ?? ref.role
  const name = ref.label?.trim()
  const who = name ? `（${role}：${name}）` : `（${role}）`
  return `第${ref.index + 1}张参考图${who}：${ROLE_PHRASE[ref.role]}`
}

/**
 * 把 `@N` 替换为「第N张参考图」。
 *
 * 只替换 1 位数的占位符：参考图上限两家分别是 5 和 6，
 * 单位数已覆盖，超出部分原样保留以免误伤正文里的邮箱、时间等文本。
 */
function expandPlaceholders(prompt: string): string {
  return prompt.replace(PLACEHOLDER, (_whole, digit: string) => {
    return `第${digit}张参考图`
  })
}

/**
 * 渲染最终提示词。
 *
 * params:
 *   prompt     原始提示词，可含 `@1` / `@2`
 *   refs       有序参考图，顺序必须与提交给上游的数组一致
 *   options    是否前置图注（默认前置）
 */
export function renderPrompt(
  prompt: string,
  refs: RenderRef[],
  options: RenderOptions = {},
): string {
  const text = prompt.trim()
  if (!refs.length) return text

  const includeNotes = options.includeRefNotes !== false
  if (!includeNotes) return expandPlaceholders(text)

  const notes = refs.map(refLine).join('；')
  return `参考图说明：${notes}。\n${expandPlaceholders(text)}`
}
