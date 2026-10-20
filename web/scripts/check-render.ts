/**
 * 提示词渲染校验。
 *
 * 渲染结果直接决定模型「知不知道你说的是哪张图」，是这块功能里唯一
 * 有真实语义风险的逻辑，必须有回归保护。项目没有引入测试框架，
 * 这里沿用 check-contract 的做法：一个零依赖脚本 + 断言计数。
 *
 * 运行：`node --experimental-strip-types scripts/check-render.ts`
 */
import {
  outOfRangeRefs,
  renderPrompt,
  usedIndexes,
  type RenderRef,
} from '../src/components/domain/promptRender.ts'

let failures = 0

function check(name: string, cond: boolean, detail?: unknown): void {
  if (cond) {
    console.log(`  ok   ${name}`)
  } else {
    failures++
    console.log(`  FAIL ${name}`, detail ?? '')
  }
}

function refs(n: number, roles: RenderRef['role'][] = []): RenderRef[] {
  return Array.from({ length: n }, (_, i) => ({
    index: i,
    assetId: `a_${i}`,
    role: roles[i] ?? 'subject',
  }))
}

console.log('[usedIndexes]')
check('无占位符返回空', usedIndexes('just text').length === 0)
check('单个占位符', JSON.stringify(usedIndexes('用 @1 的背景')) === '[1]')
check('多个占位符升序去重', JSON.stringify(usedIndexes('@2 和 @1 还有 @2')) === '[1,2]')
check('邮箱里的 @N 不算占位符', usedIndexes('mail@2x.com').length === 0)
check('正文里的 @ 不误伤', usedIndexes('@ 符号').length === 0)
check('紧贴中文的占位符仍生效', JSON.stringify(usedIndexes('把@1的人物换掉')) === '[1]')
check('紧贴标点的占位符仍生效', JSON.stringify(usedIndexes('用 @1、@2 合成')) === '[1,2]')

console.log('\n[outOfRangeRefs]')
check('都在范围内为空', outOfRangeRefs('@1 @2', 2).length === 0)
check('越界被抓出', JSON.stringify(outOfRangeRefs('@3', 2)) === '[3]')
check('@0 也算越界', JSON.stringify(outOfRangeRefs('@0', 2)) === '[0]')
check('无图时任何引用都越界', outOfRangeRefs('@1', 0).length === 1)

console.log('\n[renderPrompt]')

// 无参考图：原样返回，不做任何加工。
check(
  '无参考图时原样返回',
  renderPrompt('  一只猫  ', []) === '一只猫',
  renderPrompt('  一只猫  ', []),
)

// 有参考图：@N 展开为「第N张参考图」。
const two = renderPrompt('把 @2 的人物放进 @1 的场景', refs(2, ['scene', 'subject']))
check('@N 展开为「第N张参考图」', two.includes('把 第2张参考图 的人物放进 第1张参考图 的场景'), two)
check('展开后不再残留 @数字', !/@\d/.test(two), two)

// 图注前置，且描述每张图的角色 —— 这是让模型分清哪张是哪张的关键。
check('含参考图说明段落', two.startsWith('参考图说明：'), two)
check('图注标出第1张的角色', two.includes('第1张参考图（场景/背景）'), two)
check('图注标出第2张的角色', two.includes('第2张参考图（人物/主体）'), two)
check('图注与正文之间换行', two.includes('。\n'), two)

// label 参与图注，让「阿澈」这类具体指代能被模型对上。
const labelled = renderPrompt('让 @1 转身', [
  { index: 0, assetId: 'a_0', role: 'subject', label: '阿澈' },
])
check('label 进入图注', labelled.includes('（人物/主体：阿澈）'), labelled)

// 关闭图注时只做占位符展开，方便用户自己完全掌控提示词。
const bare = renderPrompt('把 @2 放进 @1', refs(2), { includeRefNotes: false })
check('关闭图注时不加说明段落', !bare.startsWith('参考图说明'), bare)
check('关闭图注时仍展开占位符', bare.includes('第2张参考图'), bare)

// 顺序即语义：图注里的序号必须与传入顺序严格一致，不能按 role 重排。
const ordered = renderPrompt('@1 @2 @3', refs(3, ['style', 'scene', 'subject']))
const i1 = ordered.indexOf('第1张参考图（风格参考）')
const i2 = ordered.indexOf('第2张参考图（场景/背景）')
const i3 = ordered.indexOf('第3张参考图（人物/主体）')
check('图注按传入顺序排列', i1 >= 0 && i1 < i2 && i2 < i3, ordered)

// 邮箱、时间这类含 @ 的正文不该被改写。
const safe = renderPrompt('联系 a@1x.com 或 12@3', refs(2))
check('邮箱里的 @1 不被展开', safe.includes('a@1x.com'), safe)
check('@3 越界但渲染不崩（由 UI 另行拦截）', typeof safe === 'string')

console.log('')
if (failures) {
  console.log(`${failures} 项失败`)
  process.exit(1)
}
console.log('渲染校验全部通过')
