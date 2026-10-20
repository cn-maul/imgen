# Imgen 交接文档

> 交接日期：2026-10-09
> 当前状态：**全栈跑通。后端 + HTTP 层 + 前端均已落地，`imgen serve` 可直接运行。**
> 唯一阻塞主流程的是 **M0 实测**（需要真实 API Key）—— 见 §6。
> 2026-10-20 已小额度补测：429 的 `Retry-After` 与顶层 `image` 对照实验已出结论，见 §6。

---

## 1. 一句话概况

Imgen 是自托管单二进制的图像生成工作台。Go 1.27 后端 + Vue3 前端（前端产物 `go:embed` 进二进制）。
一期只接 **SenseNova** 与 **Agnes** 两家官方通道，OpenAI 通道推迟到二期。

已完成：Provider 抽象、两家适配器、SQLite 仓储、内容寻址图片存储、密钥加密、任务调度与退避重试、
**HTTP 路由层、依赖装配、前端 embed、Vue3 工作台**。

---

## 2. 当前完成度

| 模块 | 文件 | 状态 |
|---|---|---|
| 配置 | `internal/config` | ✅ 完成 |
| 领域模型 | `internal/domain` | ✅ 完成 |
| HTTP 传输层 | `internal/httpx` | ✅ 完成（含单测） |
| Provider 抽象 | `internal/provider` | ✅ 完成 |
| SenseNova 适配器 | `internal/provider/sensenova` | ✅ 完成（含单测） |
| Agnes 适配器 | `internal/provider/agnes` | ✅ 完成（含单测） |
| 密钥加密 | `internal/secret` | ✅ 完成（含单测） |
| 图片存储 | `internal/blob` | ✅ 完成（含单测） |
| SQLite 仓储 | `internal/store` | ✅ 完成（含单测） |
| 任务调度 | `internal/job` | ✅ 完成（含单测） |
| 历史清理 | `internal/janitor`、`internal/store/prune.go` | ✅ 完成（2026-10-20 新增：按 `keepDays` 清理终态任务与孤儿资产，见 §8） |
| HTTP 路由/Handler | `internal/httpapi` | ✅ 完成（34 测试 / 55 用例） |
| 依赖装配 | `internal/app` | ✅ 完成（含 E2E 测试） |
| 前端嵌入 | `internal/web` | ✅ 完成（3 种 build tag） |
| 入口 | `cmd/imgen/main.go` | ✅ 完成（serve / version） |
| 前端 Vue3 | `web/` | ✅ 完成（样式三层 + 20 基础组件 + 6 视图） |
| 提示词预设 | `internal/store/presets.go`、`internal/httpapi/presets.go`、`web/src/views/PresetsView.vue` | ✅ 完成（2026-10-15 新增：预设 CRUD + 有序参考图 + @N 渲染） |
| 内置海报预设 | `internal/store/migrations/0004_builtin_poster_preset.sql` | ✅ 完成（2026-10-18 新增：迁移播种「城市风景 · 上下分区艺术海报」） |

**`go build ./...`、`go vet ./...`、`go test ./...` 全绿；前端 `vue-tsc` 零错误、`vite build` 通过。**

### 怎么跑

```powershell
# 一键构建（前端 → 带前端产物的二进制）；需要 task，或按下面手动执行
task

# 手动
pnpm --dir web install && pnpm --dir web build
go build -tags imgenweb -o imgen.exe ./cmd/imgen

# 纯后端（不含前端，走 dist_stub 占位页，CI 快编译用）
go build -o imgen.exe ./cmd/imgen

./imgen.exe serve
```

开发模式（前端 HMR）：`go run -tags dev ./cmd/imgen serve` + 另开终端 `pnpm --dir web dev`。

冒烟（改完接口/前端后跑一次，**不碰 `./data`**，自己起停服务）：
```powershell
task smoke                                        # = pwsh -NoProfile -File scripts/smoke.ps1
pwsh -NoProfile -File scripts/smoke.ps1 -SkipBuild # 复用已有 imgen.exe，跳过编译
```

---

## 3. 架构要点（接手必读）

### 3.1 Provider 抽象是核心

```go
// internal/provider/provider.go
type Provider interface {
    ID() domain.ProviderID
    Name() string
    Models() []domain.Model
    Capabilities(model string) Caps     // 驱动前端参数面板显隐
    Translate(r domain.Request, refs []string) (Payload, error)  // 纯函数
    Extract(body []byte) (Parsed, error)                          // 纯函数
    Classify(status int, body []byte) *domain.Error               // 纯函数
}
```

关键约定：`Translate` / `Extract` / `Classify` **全是纯函数，不碰网络、不碰文件系统**，
所以适配器可以完全脱离网络单测。发请求的事交给 `job.Executor`。

**`Parsed` 只提取待落盘的图片载荷**（`B64 []string` / `URLs []string`），落盘由 Executor 完成。
这样适配器永远不需要知道磁盘的事。`Classify` 返回的错误中 `Retryable` 字段决定 Job 层是否重试。

### 3.2 两家的关键差异（已核实官方文档，勿凭印象改）

| | SenseNova | Agnes |
|---|---|---|
| Base URL | `https://token.sensenova.cn/v1` | `https://apihub.agnes-ai.cn/v1`（实测：旧域名 401） |
| 编辑端点 | `/images/edits`（**独立端点**） | **与文生图同端点** `/images/generations`，靠 image 字段区分 |
| 参考图 | `images:[{image_url}]`，≤5 张 | `extra_body.image[]`（⚠️ 见下） |
| 尺寸 | `size:"WxH"`，**32 倍数**，≤4096 | `size:"2K" + ratio:"16:9"` |
| 输出格式 | `output_format` 可选 png/jpeg/webp | 无此参数，固定 png |
| 水印 | `watermark`（必须显式传） | 无 |
| 提示词增强 | `prompt_extend` | 无 |
| n | 仅支持 1 | 仅支持 1 |

### 3.3 三个已知的坑（改代码前先看）

**① Agnes 参考图字段位置：官方文档自相矛盾**
- 参数表写顶层 `image`
- 「重要说明」与全部示例写 `extra_body.image`

当前实现**默认走 `extra_body.image`**，并留了开关 `Provider.TopLevelImage = true` 切到顶层。
**M0 实测后把结论写回 `docs/providers/agnes.md` 并删除开关的默认值歧义。**
2026-10-20 对照实验：顶层与 `extra_body.image` 两分支**同样**全部 503
（上游排队拥堵），故该 503 与字段位置无关，**顶层支持与否仍未定论**。
单测 `TestTopLevelImageToggle` 已覆盖两个分支。

**② SenseNova 尺寸必须钳制到 4096**
官方推荐尺寸表在「高档位 + 超宽/超高比」时会超单边 4096 上限
（如 4K/16:9 是 5248×2944，直接用会 400）。
`deriveSize()` 按比例整体缩放并重新按 32 对齐 —— **优先保住用户请求的宽高比，而非死守档位标称分辨率**。
单测 `TestSizeTable` 会遍历全表校验约束，改动立刻会红。

**③ 两家 url 都会过期，所以一律要 b64_json**
- SenseNova `url` 明确 24h 过期
- Agnes url 托管 GCS，有效期未知

两个适配器都固定请求 `b64_json`。若上游仍返回 url，`persist()` 会立即下载落盘，失败则记 warn 继续。

### 3.4 httpx：传输层的行为约定

`internal/httpx` 改编自你自己的 `github.com/cn-maul/rosetta`（MIT，已在文件头署名）。
保留了几个关键行为，**不要退化成朴素的 `http.Client`**：

1. **尊重 `Retry-After`** 且夹到 60s 上限，避免上游说等 60s 我却只等 8s
2. **总等待预算 3 分钟**（`maxTotalWait`），防止 MaxRetries × 长 Retry-After 把请求挂着
3. **预算耗尽时返回 body 仍未关闭的 response** —— 让调用方能解析真实上游错误，而不是拿到掩盖真相的通用超时
4. **`MaxIdleConnsPerHost=64`** —— 默认 client 是 2，本项目有 worker pool，否则会反复 TLS 握手
5. **`CrossHostSafeRedirect`** —— 跨主机重定向会带原始 Authorization 给第三方
6. **全抖动退避**（`rand.Float64()+0.5`）—— 避免并发任务同时重试形成尖峰
7. **`MaskSecrets`** 用于日志脱敏

图像生成无副作用，所以 `Executor` 用 `RetryPolicy: RetryAlways`。

### 3.5 刻意不做的东西

按之前讨论，**不要加回来**，除非明确变更：

- ❌ 熔断器 —— 挂了重试一次就暴露，熔断只是让失败变慢
- ❌ 令牌桶限流 —— 全局并发上限 2 + httpx 退避已够
- ❌ 多实例注册表 / fallback 链 —— 一期每家只有一个地址
- ❌ 能力自测探针 —— 二期接中转站时再加

保留的最小集：`Queue` 的 worker pool（默认并发 2）+ httpx 的 429/5xx 退避。
**Agnes 4K 档只有 1 RPM**，没有退避必然失败，这条不能删。

---

## 4. A–E 已全部完成；接下来只做实测与打磨

### A–E 交付摘要

| 项 | 落点 | 备注 |
|---|---|---|
| 入口与装配 | `cmd/imgen/main.go`、`internal/app` | 装配顺序见 `app.New`；`KeyResolver` 由 `app.keyResolver` 实现（store 读密文 + cipher 解密） |
| HTTP 层 | `internal/httpapi`（7 文件） | 20 个路由全实现；`New(Deps) *API` + `Handler()` |
| 前端嵌入 | `internal/web`（3 个 build tag） | `imgenweb`=真实 dist，默认=dist_stub，`dev`=反代 Vite |
| 单测 | `blob` / `store` / `job` / `httpapi` / `app` / `janitor` | 含 `app` 的 E2E（假上游跑通提交→出图→落盘→终态）与 `janitor_wiring_test.go`（设置改动能被 janitor 读到） |
| 前端 | `web/` | 样式三层 + 20 基础组件 + 5 视图，CSS 零框架 |
| 冒烟脚本 | `scripts/smoke.ps1`（`task smoke`） | 独立端口 + 临时数据目录起服务，校验各接口信封形状 + 前端契约（2026-10-20 新增） |

**装配顺序**（`internal/app/app.go`）：
```
config.Load → EnsureDirs → secret.NewCipherFromEnv
→ store.Open(dbPath)（自动跑 migrations）
→ blob.NewStore → provider map → job.Executor
→ job.NewQueue(cfg.Concurrency) → job.Manager
→ httpapi.New(Deps) → janitor.New（KeepDays: api.KeepDays）
→ mux: /api/* → httpapi，其余 → web.Handler（SPA fallback）
→ Serve(): janitor.Start() 先扫一轮，退出时先 stopJanitor() 再 Q.Close()
```

### 关键契约（改动前必看）

- **`GET /providers` 返回 `{providers:[...]}` 信封**，每条含 `caps: map[modelID]Caps`。
  `defaultModel` / `defaultCaps` 是**前端派生**的（`api/client.ts`），后端不给。
- **`GET /settings` 是裸对象，但 `PUT /settings` 回 `{settings:{...}}` 信封** —— 两种形状都兼容。
- **`POST /jobs` 回 202 `{jobId,status}`**（只有 id）。前端 `createJob` 会回读一次拿完整 Job。
- 上传 form 字段名 `file` 或 `files`；响应是 `{items:[...]}`。
- 错误体恒为 `{"error":{...}}`，`retryable` 原样透出。

> `pnpm --dir web run check:contract` 会用真实 HTTP 响应校验前端类型与后端形状一致。
> **改任一端之后请跑一次** —— TS 的 `as T` 转换在编译期发现不了信封差异。

可以用 `testing.TempDir()` 建临时 SQLite，`:memory:` 不行（driver 与连接数限制冲突）。

### E. 前端 Vue3（M4–M7，约 2 周）
CSS 必须自实现，PLAN §7.3 的三层结构（tokens / 原子类 / 组件类）已定好，照做即可。
参数面板必须按 `GET /api/v1/providers` 返回的 `Caps` 动态显隐。

---

## 5. 环境注意事项

- **⚠️ 监听地址与鉴权（部署前必读，2026-10-20 修正）**：
  `internal/config/config.go` 的默认值是 `IMGEN_ADDR=":8080"`，Go 的 `http.Server`
  对 `":8080"` 的解释是**监听本机所有网卡**（`0.0.0.0:8080`），**不是**只监听 `127.0.0.1`。
  而**本应用没有任何鉴权**：没有登录、没有 token、没有中间件拦 `PATCH /api/v1/providers/:id`。
  也就是说，**任何能访问该端口的人都能读写 Provider API Key**
  （Key 在 `GET /api/v1/providers` 里只以 `apiKeyMask` 掩码出现、明文永不外泄，
  但 **`PATCH` 可以覆盖它** —— 攻击者能把 Key 换成自己的，或把 `baseUrl` 改到自己的服务器
  从而截获后续请求），还能提交任务、删除资产、改设置。
  - **仅本机**：显式设 `IMGEN_ADDR=127.0.0.1:8080`（只绑回环）。
  - **局域网 / 公网**：必须自行加**反向代理 + 认证**（Caddy `basicauth` / Nginx `auth_basic`
    / 已有 SSO），**不要**直接把 `:8080` 暴露出去。
  - Docker 的 `-p 8080:8080` 同样会把无鉴权服务发到宿主所有网卡；个人用 `-p 127.0.0.1:8080:8080`。
  - 二期若要真正对外开放，鉴权应该做在后端（中间件），而不是只靠用户自觉配反代。
- **Go 模块代理**：`goproxy.cn` 会 EOF 失败。已切到 `https://proxy.golang.org,direct`，
  下载依赖时需要走 HTTP 代理：
  ```powershell
  $env:HTTP_PROXY="http://127.0.0.1:7897"; $env:HTTPS_PROXY="http://127.0.0.1:7897"
  go get ...
  ```
- **依赖列表**（`go.mod`，部分标 indirect 是因为还没被实际 import，加 httpapi 后 `go mod tidy` 会修正）：
  `modernc.org/sqlite`（纯 Go，无 CGO）、`chi/v5`、`golang.org/x/image`
- **SQLite 连接数被设为 1**（`db.SetMaxOpenConns(1)`）：SQLite 单写者模型。
  这意味着**不要在事务内发 HTTP 请求**，否则会串行化整个 worker pool。Executor 目前没在事务里，保持这样。
- **临时目录要放在工作区内**（沙箱环境下 `%TEMP%` 可能对子进程不可写）：
  `scripts/smoke.ps1` 的默认临时目录是仓库内的 `.smoke-tmp\`，而不是系统 `%TEMP%`。
  原因：某些受限环境（IDE / 沙箱代理）只允许子进程写工作区，服务进程写 `%TEMP%` 会
  `mkdir ...: Access is denied` 直接退出。手动跑其他探针时也建议遵循这条。

---

## 6. 实测待办（M0）

**已于 2026-10-15 完成主要项，结论见 `docs/providers/README.md`：**

- [x] Agnes 正确入口是 `apihub.agnes-ai.cn/v1`（旧域名 401），已改默认值与迁移 SQL
- [x] Agnes 参考图走 `extra_body.image`（成功样本），张数上限 **6**（9 张被拒）
- [x] Agnes 图像模型只有 `agnes-image-2.5/2.1/2.0-flash`；
      `agnes-2.5-flash`/`agnes-3.0-flash` 是聊天模型，不能生图
- [x] Agnes 未充值也能出图（1K 约 10s）
- [x] SenseNova `size:auto` 可用，返回 2048x2048；参考图 256–4096px、≤10MB、比例 ≤2:1
- [x] SenseNova url 签名有效期 86400s（24h）
- [x] 耗时：SenseNova 1K≈4s / 2K≈8s / 4K≈28s；Agnes 1K≈10s / 4K≈20s

**2026-10-20 补测（小额度，已回写 `docs/providers/`）：**

- [x] **两家 429 是否带 `Retry-After`** —— 已确认 **Agnes 429 不带**该头
      （4K 连发第 2 次即 `429 rate_limit_exceeded`，响应头无 `Retry-After`、
      无 `X-RateLimit-*`）；**SenseNova 连发 5 次均 200，未触发 429**，
      如实记为「未触发」而非「不存在」。→ `httpx` 自实现抖动退避是必需的。
- [x] **Agnes 顶层 `image` vs `extra_body.image` 对照实验** —— 同一张 1x1 参考图，
      顶层跑 4 次、对照组跑 3 次，**两分支全部 503 `image queue is full`**。
      → 该 503 是**上游排队拥堵，与字段位置无关**。这排除了「顶层字段不被支持」的
      猜测，但**仍没有顶层成功样本**。

**仍待补**（两项；每项可执行验证步骤见 `docs/providers/README.md` 的「M0 剩余项」）：

- [ ] Agnes 顶层 `image` 的成功样本 —— 需在上游队列空闲时重跑，
      **且必须先跑 `extra_body.image` 对照组确认队列通了**，否则 503 会被误读为字段错误
- [ ] Agnes url 的实际过期时间 —— 需跨 +1h/+6h/+24h 探测，单次会话内无法完成
      （用户已确认不为它长时间挂起会话）；适配器已固定请求 `b64_json`，不影响当前代码路径

---

## 7. 我踩过并已修的坑

留个记录，避免重复：

1. **尺寸表越界** —— 直接照抄 Agnes 推荐的交叉表，但 SenseNova 单边上限 4096，
   高档位宽幅组合全超限。改为 `deriveSize()` 缩放钳制。
2. **`Payload.Body` 值拷贝** —— `pl := Payload{Body: b}` 之后再改 `b.Images`，
   请求体里会丢参考图。改成构造完再赋值。
3. **自己造轮子** —— 曾用手写 `indexOf`/`containsAny` 替代 `strings.Contains`，
   以及包级变量绕过实例配置。都清理了。
4. **x/image 没有 webp 编码器** —— 缩略图改回 stdlib JPEG。
5. **`joinComma` 被 WHERE 子句误用**（`store`）—— 它拼的是逗号，而
   `ListJobs`/`ListAssets` 需要 `AND`，结果任何带过滤的查询都
   `SQL logic error: near ","`。已拆成 `joinWhere`（AND）与 `joinSet`（逗号）两个函数。
6. **`ListJobs` 把 `created_at` 直接 Scan 进 `time.Time`** —— 列里是 RFC3339 文本，
   driver 转不了，**任何** ListJobs 调用都失败。改成先取字符串再 `parseTS`。
7. **`partial` 不在终态集合里**（`store.UpdateJobStatus`）—— 导致 partial 任务
   `finished_at` 永远是 NULL（前端历史拿不到完成时间）。已补进终态 case。
8. **`kvInt` 把「键不存在」误判成 0** —— `GetKV` 在键缺失时原样返回默认指针且不报错，
   于是新库首启动把 concurrency 钳到 1、keepDays 归零。改用 `-1` 哨兵。
9. **取消任务不通知 SSE 订阅者** —— 只调 `context.CancelFunc` 不够：任务若还在排队
   （没有协程在跑 `run`），没人发终态事件，订阅者会挂到超时。
   `Manager.Cancel` 现在在确认状态变为 canceled 后主动广播一次终态。
10. **SPA fallback 把带点的路径一律 404** —— 于是真实前端的 js/css 全部取不到。
    改成先 `fs.Stat` 确认文件存在，存在就交给 FileServer，不存在且带扩展名才 404。
11. **前端类型与后端信封不匹配** —— 后端 `{providers:[...]}` / `{settings:{...}}`，
    前端却声明成裸数组/裸对象；TS 的 `as T` 让编译期完全看不出来。
    已修 client 的解包，并加 `pnpm run check:contract` 用真实响应守住。
12. **`defer cancel()` 提前取消，导致大响应体读失败**（2026-10-15，用户实测发现）——
    `Executor.RunOnce` 的 `call` 闭包里写了 `cctx, cancel := context.WithTimeout(...)`
    加 `defer cancel()`，但 `httpx.Do` 返回后 `resp.Body` 仍绑定在 `cctx` 上，
    闭包一返回 `cancel()` 就执行，后续 `ReadBody` 立刻报 `context canceled`，
    任务全部以 `UPSTREAM_UNAVAILABLE / 读取上游响应失败` 收场。
    **小响应测不出来**：几百字节的响应已整体进内核缓冲区，取消也能读完，
    所以原有单测全绿；真实的 base64 图像响应 1MB+ 分块传输才会触发。
    修法：`call` 改为返回 `context.CancelFunc`，由调用方在读完 body 后 `defer cancel()`。
    回归用例 `TestRunOnceLargeChunkedBody` 用 256KB 分块响应锁住（已验证能抓到该 bug）。
13. **提示词占位符误伤邮箱**（2026-10-15，新增提示词库时发现）——
    最初用 `/@(\d)/g` 匹配 `@N`，会把 `a@1x.com` 里的 `@1` 当成参考图引用。
    改为前后都要求非 `[A-Za-z0-9]` 的 lookbehind/lookahead：
    `(?<![A-Za-z0-9])@(\d)(?![A-Za-z0-9])`。
    中文紧贴（`把@1的人物`）与标点紧贴（`用 @1、@2`）仍生效，
    校验脚本 `web/scripts/check-render.ts` 覆盖了这两类边界。
14. **内置预设让「空列表」假设失效**（2026-10-18，新增海报预设时发现）——
    迁移 `0004` 往 `prompt_presets` 播种了一条内置预设，于是
    `TestPresetsCRUDOverHTTP` 的「初始列表为空」与 `TestPresetCRUD` 的
    「列表长度 = 1」全部变红。这两处不是逻辑错，而是**前提变了**：
    以后再往 `prompt_presets` 播种内置数据，必须同步改这两条断言，
    或者干脆让断言按「内置条数 + 自建条数」计算。
    另外内置预设的 `refs` **必须是空数组**：资产 id 与具体库绑定，
    写死的话换台机器就会看到「有参考图已被删除」。
    用户在中栏点「从资产库添加」补图后，提示词里的 `@1` 随即生效。

---

## 8. 历史清理（janitor，2026-10-20 新增）

**背景**：历史保留天数此前**只有配置项，没有实现** —— `internal/config` 读它、
`/api/v1/settings` 能改它并落 kv，但**没有任何代码真的删过东西**。
等于 UI 上有个「保留 N 天」的承诺，实际数据只增不减。本轮补上了真正的清理任务。

### 实现

- 新增包 **`internal/janitor`**（`janitor.go` + `janitor_test.go`），
  清理逻辑落在 `internal/store/prune.go`（`PruneTerminalJobs` / `PruneUnreferencedAssets`）。
- **装配位置**：`janitor` 在 `app.Handler()` 里惰性构造（因为要拿 `httpapi.API` 实例读运行期
  `keepDays`），在 `app.Serve()` 里 `Start()`，关闭时**先 `stopJanitor()` 再 `a.Q.Close()`**。
- **周期扫描**：默认间隔 **1 小时**（`janitor.DefaultInterval`）；**启动时先同步跑一轮**，
  随后才进 `time.Ticker`，所以重启后过期数据会被立即回收。
- **保留期判定用 `COALESCE(finished_at, created_at)`，不是只看 `created_at`**：
  用户的心智模型是「跑完的任务保留 N 天」，一个 30 天前创建、昨天才跑完的长任务
  不应该一重启就被清掉；没有 `finished_at` 的历史/异常数据才退回 `created_at`。
- 时间比较刻意放在 Go 侧而非 SQL：`created_at` / `finished_at` 存 RFC3339Nano 文本，
  RFC3339Nano 会裁掉小数秒尾随零（`.4Z` > `.45Z`），按字符串比较方向是反的。

### 清理对象（两类）

1. **过期任务**：`status` 是**终态**（`done` / `partial` / `failed` / `canceled`）
   且保留期判定早于 cutoff 的 jobs。其 `job_images` 由外键 `ON DELETE CASCADE` 一并删除。
   ⚠️ **只删终态**：`queued` / `running` 的任务即便很老也不动，否则会抹掉正在跑的历史。
2. **孤儿资产**：`created_at` 早于 cutoff，且**无人引用**的 assets。
   「有人引用」覆盖**三处**（少查一处就会误删仍在用的图）：
   - `job_images.asset_id` —— 某个任务的产出；
   - `jobs.params_json` 的 `refs[].assetId` —— 某个任务拿它当过参考图；
   - `prompt_presets.body_json` 的 `refs[].assetId` —— 某个预设绑定了它。

   删除 DB 行的同时**也删磁盘上的原图与缩略图**（`blob.Store.Remove` 两个都删），
   否则 blob 目录只增不减。单文件删不掉只记 warn，不中断整轮扫描。

**顺序要紧**：先 `PruneTerminalJobs` 再 `PruneUnreferencedAssets` ——
旧任务删掉后它们对资产的引用才消失，那些资产才应该在**下一轮**被判为孤儿。

### 语义与配置

- **`keepDays = 0` 表示完全禁用清理**（不是「立刻删光历史」）。负值在 config 层收敛到 0。
- 配置来源两处，**运行期改从下一轮扫描起生效**：
  - `GET/PUT /api/v1/settings` 的 `keepDays`（合法区间 **0..365**，落 kv `settings.keep_days`，
    重启后 `loadPersistedSettings` 读回并覆盖 config 注入值）；
  - 环境变量 `IMGEN_KEEP_DAYS`（默认 30，作为 kv 为空时的初值）。
- ⚠️ **两处的钳制范围不一致**：`internal/config` 只把负数收敛到 0、**不设上限**；
  而 `httpapi.New` 会把注入值 `clamp(0..365)`。所以 `IMGEN_KEEP_DAYS=1000`
  实际生效的是 **365**。想让历史超过一年不被删，得改 `maxKeepDays`。
- config 字段已从 `KeepQueueDays` **改名为 `KeepDays`**（`internal/config/config.go`），
  与 `settings.keepDays` 对齐；`httpapi.Deps` 也新增了 `KeepDays` 注入字段。
- 因为「下一轮起生效」，改完别期待立刻看到数据消失；想手工验证可用
  `app.Janitor.Sweep(ctx)`（`janitor_wiring_test.go` 就是这么测的）。

**这一项已从「只有配置没有实现」变成已落地，不用再当 TODO 看。**

---

## 9. 待确认事项（PLAN §11，仍未拍板）

1. 存储：SQLite 可以吗？还是要 Postgres？
2. 部署：纯本地还是局域网访问？要不要密码？
   （注意：默认 `IMGEN_ADDR=":8080"` 是**监听所有网卡**且**无任何鉴权**，
   详见 §5 第一条 —— 在这个问题拍板之前，对外暴露务必自己加反向代理 + 认证。）
3. Key：UI 配置落库加密，还是只读环境变量？
4. 默认并发 2 可以吗？
5. 默认通道给 SenseNova 还是 Agnes？
6. 界面只中文，还是要中英双语？

---

## 10. Docker 部署（2026-10-15 新增）

纯 Go 静态二进制 + Alpine 运行层，无 CGO、无外部运行时依赖。

- `Dockerfile`：多阶段构建（node:22-alpine 构建前端 → golang:1.27-alpine 编译内嵌 dist 的静态二进制 → alpine:3 运行层，非 root，`/api/v1/health` 健康检查）。
- `.dockerignore`：排除本机二进制、`node_modules`、数据目录与密钥，绝不入镜像。
- 数据目录为可写卷 `/home/imgen/data`（SQLite、图片 blob、主密钥落盘处）。

```bash
# 构建（VERSION / BUILD_TIME 会注入二进制与前端 boot 信息）
docker build -t imgen:latest .

# 运行（宿主 8080，数据卷持久化）
docker run -d --name imgen -p 8080:8080 -v imgen-data:/home/imgen/data imgen:latest

# 常用环境变量（同原生部署，见 start.ps1 / main.go 用法）
#   -e IMGEN_ADDR / IMGEN_CONCURRENCY / IMGEN_MAX_UPLOAD_MB / IMGEN_MASTER_KEY ...
```

也可用 Task：`task docker:build` / `task docker:run`。
前端产物在镜像内已 embed（`-tags imgenweb`），容器内无需 pnpm。
