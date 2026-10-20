/**
 * 契约校验：确认 client.ts 的解包逻辑与真实后端响应形状一致。
 * TypeScript 的 `as T` 转换无法在编译期发现信封/裸对象的差异，
 * 这里用真实 HTTP 响应兜住。
 *
 * 运行：起服务后 `node --experimental-strip-types scripts/check-contract.mts`
 * 或直接 `npx tsx scripts/check-contract.ts`
 */
const BASE = process.env.IMGEN_BASE ?? 'http://127.0.0.1:18086'
const API = BASE + '/api/v1'

let failures = 0
function check(name: string, cond: boolean, detail?: unknown) {
  if (cond) {
    console.log(`  ok   ${name}`)
  } else {
    failures++
    console.log(`  FAIL ${name}`, detail ?? '')
  }
}

async function get(path: string) {
  const r = await fetch(API + path)
  if (!r.ok) throw new Error(`${path} → HTTP ${r.status}`)
  return r.json()
}

async function main() {
  console.log(`校验后端契约 @ ${API}\n`)

  console.log('[providers]')
  const provRaw = await get('/providers')
  check('响应是 {providers:[...]} 信封', Array.isArray(provRaw.providers))
  const p = provRaw.providers?.[0]
  check('ProviderInfo.id 存在', typeof p?.id === 'string', p?.id)
  check('models 是非空数组', Array.isArray(p?.models) && p.models.length > 0)
  check('caps 是 map[modelID]Caps', p?.caps && typeof p.caps === 'object')
  const firstModel = p?.models?.[0]?.id
  check('caps 覆盖每个模型', firstModel && p.caps[firstModel] !== undefined, {
    firstModel, keys: Object.keys(p?.caps ?? {}),
  })
  const caps = p?.caps?.[firstModel]
  check('caps.maxRefs 是数字', typeof caps?.maxRefs === 'number', caps?.maxRefs)
  check('caps.ratios 非空', Array.isArray(caps?.ratios) && caps.ratios.length > 0)
  check('caps.outputFormats 非空', Array.isArray(caps?.outputFormats))
  check('未配 Key 时 hasKey=false 且无明文', p?.hasKey === false && !('apiKey' in p))
  const noPlain = JSON.stringify(provRaw)
  check('响应体不含 apiKey 明文', !noPlain.includes('"apiKey"'))

  console.log('\n[settings]')
  const s = await get('/settings')
  check('GET /settings 是裸对象（无 settings 信封）',
    s.settings === undefined && typeof s.concurrency === 'number', s)
  check('queueDepth 存在（顶栏水位）', typeof s.queueDepth === 'number', s.queueDepth)
  check('concurrency 在 1..16', s.concurrency >= 1 && s.concurrency <= 16, s.concurrency)
  check('keepDays 有默认值（非 0）', typeof s.keepDays === 'number' && s.keepDays > 0, s.keepDays)

  const putRes = await fetch(API + '/settings', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ concurrency: s.concurrency }),
  })
  const put = await putRes.json()
  check('PUT /settings 回 {settings:{...}} 信封', put.settings !== undefined, put)
  check('PUT 回显 concurrency 正确', put.settings?.concurrency === s.concurrency)

  console.log('\n[jobs]')
  const jobs = await get('/jobs')
  check('GET /jobs 是 {items,total,limit,offset}',
    Array.isArray(jobs.items) && typeof jobs.total === 'number', jobs)
  check('limit 与 store 翻页步长一致（<=100）', jobs.limit > 0 && jobs.limit <= 100, jobs.limit)

  console.log('\n[assets]')
  const assets = await get('/assets')
  check('GET /assets 是 {items,...}', Array.isArray(assets.items), assets)

  console.log('\n[usage]')
  const usage = await get('/usage')
  check('GET /usage 是 {days,items}', typeof usage.days === 'number' && Array.isArray(usage.items), usage)

  console.log('\n[health]')
  const h = await get('/health')
  check('health.ok 为 true', h.ok === true, h)

  console.log(failures === 0 ? '\n全部通过 ✓' : `\n${failures} 项失败 ✗`)
  process.exit(failures === 0 ? 0 : 1)
}

main().catch((e) => {
  console.error('契约校验无法运行:', e.message)
  process.exit(1)
})
