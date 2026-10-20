# Imgen · 图像生成/编辑平台 · 计划书

> v2.0 ｜ 2026-10-09
> **一期只做 SenseNova + Agnes 两家，跑通再迭代；OpenAI 通道二期接入。**

---

## 1. 目标与范围

### 1.1 定义
自托管的单二进制 Web 应用，通过统一抽象层对接图像生成/编辑通道，提供文生图、图生图、多图合成、批量生成、历史管理的��体化工作台。前端 Vue3 产物 `go:embed` 进 Go 二进制，CSS 完全自实现。

### 1.2 分期

| | 一期（本次） | 二期 |
|---|---|---|
| 通道 | SenseNova（商汤）+ Agnes | 加 OpenAI 兼容通道（含中转站） |
| 能力 | 文生图 / 图生图 / 多图合成（≤5 参考图） | + 局部重绘 mask、渐进预览、透明背景、质量档 |
| 目标 | 跑通主流程，边用边改 | — |

### 1.3 一期范围
- 文生图、图生图、多图合成（≤5 张参考图，首张为主图）
- 尺寸档位（1K–4K）+ 宽高比（8 选 1）
- 批量出图（`Count` 次串行调用，两家上游均只支持单张）
- 参数面板按 provider 能力自适应显隐
- 提示词历史、参数快照、结果下载
- Provider / Key 在 UI 配置（DB 加密存储）
- 生成历史 + 本地资产库

### 1.4 一期不做
- 局部重绘 mask、渐进预览、透明背景、质量档 —— 只有 OpenAI 有，等二期
- 熔断器、令牌桶限流、多实例注册表、能力自测探针、fallback 链 —— 过度设计，见 §9 说明
- 视频生成、多人协作、模型微调

---

## 2. 技术栈

### 2.1 后端
| 项 | 选型 |
|---|---|
| 语言 | Go 1.27（单二进制、CGO-free） |
| 路由 | `chi/v5` |
| 数据库 | SQLite（`modernc.org/sqlite` 纯 Go），仓储走原生 SQL |
| 迁移 | `goose`（embed SQL） |
| 图片 | `golang.org/x/image` + 标准库 `image/*`（解码探测、缩略图） |
| 日志 | `log/slog` |
| 加密 | `crypto/aes` + GCM（API Key） |
| 前端嵌入 | `embed.FS` + 模板注入 |
| 测试 | `go test` + `httptest` 打桩 |

### 2.2 前端
| 项 | 选型 |
|---|---|
| 框架 | Vue 3.5 `<script setup>` + TS 5.x |
| 构建 | Vite 7 → `internal/web/dist` |
| 状态 / 路由 | Pinia / vue-router（hash 模式） |
| CSS | **自实现**，不用任何框架 → §7.3 |
| 请求 | 原生 `fetch` + 自实现轻量 client |
| 图标 / 图片编辑 | 自绘 SVG；Canvas 自实现裁剪与对比滑块 |
| 测试 | Vitest + Playwright 冒烟 |

### 2.3 目录结构
```
Imgen/
├─ cmd/imgen/main.go
├─ internal/
│  ├─ app/            # 依赖装配、生命周期
│  ├─ config/         # 配置加载
│  ├─ httpapi/        # 路由、handler、DTO、错误映射、middleware/、sse/
│  ├─ domain/         # 实体与领域错误
│  ├─ store/          # SQLite 仓储
│  ├─ blob/           # 内容寻址图片存储 + 缩略图
│  ├─ provider/       # ★ 抽象层
│  │  ├─ provider.go  # 接口 + Caps 能力模型
│  │  ├─ sensenova/   # 商汤适配器
│  │  ├─ agnes/       # Agnes 适配器
│  │  └─ openai/      # 二期占位
│  ├─ job/            # 任务调度（简单 worker pool）
│  ├─ secret/         # Key 加密
│  └─ web/            # embed.FS、SPA fallback
├─ web/               # Vue3 源码
├─ migrations/
├─ docs/providers/    # 各通道实测记录
└─ Taskfile.yml
```

---

## 3. Provider 抽象层

### 3.1 两家 API 差异（已核对官方文档）

| 维度 | SenseNova | Agnes Image 2.5 Flash |
|---|---|---|
| Base URL | `https://token.sensenova.cn/v1` | `https://api.agnes-ai.cn/v1` |
| 模型 | `sensenova-u1.5-lite` / `sensenova-u1.5-fast` | `agnes-image-2.5-flash` |
| 文生图 | `POST /images/generations` | `POST /images/generations` |
| 编辑 | `POST /images/edits`（**独立端点**） | **同端点**，靠 `image` 参数区分 |
| 参考图 | `images:[{image_url}]`，≤5 张，首张为主图 | `image:[...]`（⚠️ 顶层还是 `extra_body.image` **待实测**） |
| 尺寸 | `size:"WxH"`，W/H 为 **32 倍数**，512~4096，宽高比 ≤3:1 | `size:"1K\|2K\|3K\|4K"` + `ratio` |
| 推荐尺寸 | `2048x2048`(1:1 2K)、`2720x1536`(16:9 2K)、`4096x4096`(1:1 4K) | 档位×ratio 交叉表（2K/16:9 → `2624x1472`） |
| 单请求张数 | `n` 仅 1 | 实测按 1 张 |
| 输出格式 | `output_format: png\|jpeg\|webp` | 无此参数，固定 png |
| 水印 | `watermark: bool`（默认 true，公测去水印免费） | 无 |
| 提示词增强 | `prompt_extend: bool`（默认 true） | 无 |
| 返回 | `response_format: b64_json\|url`（url **24h 过期**） | `extra_body.response_format`，或 `return_base64:true` |
| 额度 | 每模型 1500 次/5h（公测） | 图片当前全免（刊例 1K ¥0.07、2K ¥0.12、3K ¥0.14、4K ¥0.16/张） |
| 限流 | 1500 次/5h | RPM 按档位：1K=10、2K=5、3K=1、4K=1 |
| 错误 | `{"error":{type,code,message}}`，429=`quota_exceeded_error` | 纯 HTTP 状态码（400/401/402/403/422/429/5xx） |
| 共同点 | 都**不支持 seed**、都需 60s~360s 超时 | |

### 3.2 统一领域模型

```go
type Request struct {
    Provider string      // sensenova | agnes
    Model    string
    Prompt   string
    Refs     []Ref       // 参考图（本地 blob id / 公网 URL）
    Mode     Mode        // Txt2Img | Img2Img | Compose
    Aspect   Aspect      // 1:1 3:4 4:3 16:9 9:16 2:3 3:2 21:9
    Tier     Tier        // Tier1K Tier2K Tier3K Tier4K
    Count    int         // 上游只支持 1，由 job 层拆成 Count 次调用

    NoWatermark  bool     // SenseNova
    PromptExtend *bool    // SenseNova
    OutputFormat Format   // Agnes 忽略（固定 png）
}

type Result struct {
    Images        []Image // 本地 blob id + 宽高 + 格式
    Usage         Usage
    RevisedPrompt string
    Raw           json.RawMessage
    Latency       time.Duration
}
```

**尺寸映射**：Agnes 直接透传 `size:"2K" + ratio:"16:9"`；SenseNova 走 `(Tier, Aspect) → WxH` 查表并强制 32 对齐。两家各一张映射表，用表驱动单测锁定。

### 3.3 接口

```go
type Provider interface {
    ID()   string
    Name() string
    Models(ctx) ([]Model, error)
    Capabilities(model string) Caps
    Translate(r Request) (Payload, error)       // 纯函数：领域请求 → 上游 body
    Parse(resp *http.Response) (Result, error)  // 上游响应 → 领域结果
}

type Caps struct {
    Img2Img, Compose   bool
    NoWatermark, PromptExtend bool
    OutputFormats      []Format
    MaxRefs            int
    Ratios             []Aspect
    Timeout            time.Duration
}
```

`Caps` 只驱动前端参数显隐（`GET /api/providers` 一次返回两张表）。

### 3.4 适配器要点

**SenseNova**
- 编辑走 `/images/edits`；`size` 由 `(Tier, Aspect)` 查表映射为官方推荐像素，强制 32 对齐、≤4096
- **始终显式传 `watermark`**（默认值公测后可能变）
- **默认 `response_format=b64_json`** —— url 24h 会过期，落盘成本远低于过期风险
- `n` 恒为 1

**Agnes**
- `size:"2K" + ratio:"16:9"` 直接透传
- 参考图走 `extra_body.image`（**M0 首要实测项**）
- 图生图要 Base64 输出必须用 `extra_body.response_format:"b64_json"`；顶层放 `response_format` 是官方文档明确列出的错误写法
- 固定 png；遇 429 直接指数退避重试

### 3.5 图片落盘
1. `b64_json` → 直接解码（最快路径，无网络依赖）
2. `url` → 立即 `GET` 下载（超时 + 大小上限）
3. 校验 Content-Type + 解码头，非图片或超限则丢弃并记错
4. `sha256` 内容寻址存 `blobs/ab/cd/<sha>.<ext>`
5. 生成 `thumb/<sha>.webp` 供列表用
6. 下载失败 → DB 存原 URL 并标 `expires_at`，UI 提示「链接已过期，需重新生成」

---

## 4. 后端 API

```
GET    /api/v1/health
GET    /api/v1/providers                 # 两家的模型 + 能力矩阵 + 是否已配 Key
POST   /api/v1/providers/:id/test        # 连通性探测
PATCH  /api/v1/providers/:id             # 配置 baseUrl / apiKey / enabled

POST   /api/v1/jobs                     # 建任务，立即返回 job_id
GET    /api/v1/jobs/:id                 # 轮询
GET    /api/v1/jobs/:id/events          # SSE（queued→running→partial→done/failed）
POST   /api/v1/jobs/:id/cancel
GET    /api/v1/jobs?limit&offset&provider&model&q

POST   /api/v1/assets                   # 上传（multipart，限 20MB，嗅探真实类型）
GET    /api/v1/assets                   # 资产库列表
GET    /api/v1/assets/:id/raw
GET    /api/v1/assets/:id/thumb
DELETE /api/v1/assets/:id

GET    /api/v1/usage                     # 用量统计
GET    /api/v1/settings
PUT    /api/v1/settings
```

**统一错误体**
```json
{ "error": { "code": "UPSTREAM_REJECTED", "message": "...", "provider": "agnes", "upstream_status": 422, "retryable": false } }
```
错误码：`UPSTREAM_AUTH`(401/403)、`UPSTREAM_QUOTA`(402/429)、`UPSTREAM_REJECTED`(400/422)、`UPSTREAM_TIMEOUT`(408/504)、`UPSTREAM_UNAVAILABLE`(5xx)、`BAD_REQUEST`、`NOT_FOUND`、`INTERNAL`。

**任务状态机**：`queued → running → partial → done | failed | canceled`

---

## 5. 数据模型

```sql
providers(id TEXT PK, name, base_url, api_key_enc BLOB, enabled INT,
          timeout_ms INT, created_at, updated_at)

models(provider_id, model_id, display_name, PRIMARY KEY(provider_id, model_id))

jobs(id TEXT PK, provider, model, mode, prompt, params_json, status,
     progress INT, error_json, count, latency_ms,
     created_at, started_at, finished_at)
job_images(job_id, seq, asset_id, status, error_json)

assets(id TEXT PK, sha256 UNIQUE, mime, width, height, bytes,
       path, thumb_path, source TEXT,   -- upload | generated | remote_url
       source_url TEXT, expires_at, created_at)

usage_daily(day, provider, model, calls, images, est_cost)

kv(key PK, value_json)
```

索引：`(status, created_at)`、`(provider, model, created_at)`、`assets(sha256)`、`assets(created_at)`。

---

## 6. 任务调度（保持简单）

- 全局 worker pool，**并发上限默认 2**（可在设置里调），一条通道一条队列
- 队列满时前端按钮显示「排队中」
- 429 → 指数退避重试（1s/2s/4s/8s，最多 4 次）；4xx 不重试，直接失败
- 批量任务 `Count>1` 拆成 N 个子任务串行执行，逐张落库，前端逐张点亮
- 不做熔断、不做令牌桶、不做多实例 fallback —— 遇到 429 退避重试就够了

---

## 7. 前端设计

### 7.1 信息架构
```
/studio            工作台（左参数面板 / 右画布 + 结果流）
/assets            资产库
/settings/providers 通道与 Key 配置
/settings/system   系统设置
/history           生成历史
```

### 7.2 工作台布局
```
┌──────────────────────────────────────────────────────────────┐
│ 顶栏: Logo | Provider▾ Model▾ | 队列水位 | 主题 | 设置       │
├───────────────┬──────────────────────────────────────────────┤
│ 参数面板       │  画布区                                     │
│ ├ 模式        │   · 参考图槽位（拖拽上传，排序，首图=主图）    │
│ │  (文生/图生  │   · 生成中占位（骨架 + 阶段文案 + 耗时）      │
│ │   /多图合成) │   · 结果网格（hover：下载/再生成/            │
│ ├ 提示词       │     送入编辑/作为参考图/删除）               │
│ │  +模板/历史  │   · 双图对比滑块（Canvas 自实现）            │
│ ├ 档位 1K-4K   │                                              │
│ ├ 宽高比 8 选 1│  右下：批次进度条                             │
│ ├ 张数 1-8     │                                              │
│ ├ 去水印※      │                                              │
│ ├ 提示词增强※  │                                              │
│ └ [生成]       │                                              │
└───────────────┴──────────────────────────────────────────────┘
                     ※ 仅 SenseNova 显示；Agnes 下自动隐藏
```

### 7.3 自实现 CSS 方案（对应「自实现 css」要求）

不引入任何 CSS 框架，三层落地：

**1. 设计令牌层** `styles/tokens.css`
- 色彩：语义化 `--surface-1..3`、`--text-1..3`、`--accent`、`--danger/warn/ok`
- 排版：`--fs-xs..3xl`、`--lh-*`、字族栈
- 间距/圆角/阴影/动效：`--space-*`、`--radius-*`、`--shadow-*`、`--dur-*`
- 深浅主题：`:root` / `[data-theme="dark"]`，附 `prefers-color-scheme` 自动跟随
- 单一来源，编译期生成 `theme.ts` 供 Canvas 取色

**2. 基础与原子层** `styles/base.css` + `styles/atomic.css`
- reset、排版基线、滚动条、可访问性（focus-visible、reduced-motion）
- 原子类 `p-2 / gap-3 / flex-col / items-center / t-sm / rounded-md / bg-surface-2 / text-accent`
- 由 token 表脚本生成，杜绝魔法数字

**3. 组件层** `styles/components/*.css`
- 基础：`Button / Input / Select / Slider / Checkbox / Tabs / Modal / Drawer / Toast / Card / Badge / Tooltip / Skeleton / Dropzone / Lightbox`
- 图像域：`SplitCompare`（对比滑块）、`ImageGrid`、`Dropzone`（多图排序）
- 每个组件一个 css + 一个 `.vue`，BEM 命名，用类名而非 scoped 哈希做作用域

规范：z-index 层级表、间距只用 token、颜色只允许语义名、组件必须覆盖 loading/disabled/error 三态、键盘可达（Tab 序、Esc 关闭、焦点陷阱）。

### 7.4 关键交互
- **参考图**：拖拽 / 粘贴 / 点选上传；缩略图排序，badge 标「第 1 张（主图）」；上限 5 张跟随 `Caps.MaxRefs`
- **能力自适应**：切 provider 时按 `Caps` 显隐——Agnes 下隐藏「去水印/提示词增强」，SenseNova 下隐藏格式选择
- **参数快照**：每个 job 存完整参数，结果卡片可「复制参数」或「以此次参数再生成」
- **无 seed 的复现**：两家都无 seed，提供「复现」= 当前参数 + 结果图回灌为参考图，UI 说明这是**近似复现**
- **队列水位**：显示排队数与并发占用，满时按钮变「排队中」
- **离线恢复**：SSE 断线降级轮询（指数退避），刷新后恢复未完成 job

### 7.5 构建与嵌入
- `vite.config.ts` → `build.outDir = '../internal/web/dist'`，`base: './'`
- `//go:embed all:dist` 编译期打包
- 启动时注入 `window.__IMGEN__ = {version, buildTime, maxUploadMB}`
- SPA fallback：非 `/api/*` 未命中静态文件 → 返回 `index.html`
- `./imgen serve --data ./data --addr :8080`；`-tags dev` 走 Vite dev server
- Taskfile：`build:web → build:go`；embed 缺失时用 `dist_stub/` 占位保证纯后端可编译

---

## 8. 里程碑（一期约 4 周，单人全职）

| 阶段 | 内容 | 交付物 | 估算 |
|---|---|---|---|
| **M0 实测**（2 天） | 真实 key 打两家 API，确认 ① Agnes 参考图字段在顶层还是 `extra_body` ② Agnes url 有效期与参考图上限 ③ 两家错误响应样本 ④ 实测耗时分布 | `docs/providers/{sensenova,agnes}.md` | 2d |
| **M1 骨架**（3 天） | 仓库、`app` 装配、config、store+迁移、embed web、健康检查、Taskfile | 可启动空壳 | 3d |
| **M2 Provider**（5 天） | 接口、两个适配器、尺寸映射表、`httptest` 契约测试（正常/429/401/422/超时/脏响应） | 映射逻辑全覆盖单测 | 5d |
| **M3 资产与任务**（4 天） | blob 存储、缩略图、上传、job 调度、worker pool、SSE、错误映射 | 后端 API 全通 | 4d |
| **M4 前端基座**（4 天） | Vite+TS+Pinia、设计令牌与原子类、基础组件集、API client、主题 | 可交互骨架 | 4d |
| **M5 工作台**（6 天） | 参数面板（能力自适应）、画布、结果流、批次、对比、历史复用 | **主流程可用** | 6d |
| **M6 资产库/设置**（2 天） | 资产库、Provider 配置页、系统设置、用量统计 | 功能完整 | 2d |
| **M7 测试与打磨**（2 天） | e2e 冒烟、跨平台打包、错误提示与空态 | 可交付 | 2d |

**关键路径**：M0 → M2 → M3 → M5。M0 不可跳过——Agnes 参考图字段的文档矛盾必须先实测。

**验收标准**：两家各跑通文生图 / 图生图 / 3 图合成 / 8 张批量；断网重连正常；Key 失效有明确提示；429 有退避而非报错；100 张批量不崩。

---

## 9. 关于「不做熔断限流」的说明

同意砍掉。理由与保留的最小集：

| 原设计 | 处置 | 理由 |
|---|---|---|
| 熔断器 | ❌ 砍 | 挂了重试一次就暴露了，熔断只是让失败变慢 |
| 令牌桶限流 | ❌ 砍 | 并发上限 2 + 429 退避重试已够用 |
| 多实例注册表 / fallback 链 | ❌ 砍 | 一期只有一个地址，配多个是假需求 |
| 能力自测探针 | ❌ 砍 | 二期接中转站时再加 |
| **全局并发上限（默认 2）** | ✅ 留 | 约 20 行代码，防止打爆上游与本机内存 |
| **429 指数退避** | ✅ 留 | Agnes 4K 只有 1 RPM，不退避必失败 |

保留 `providers.base_url` 这一列（一行字段），二期接中转站时不用改表结构。

---

## 10. 风险

| 风险 | 影响 | 对策 |
|---|---|---|
| **Agnes 参考图字段文档自相矛盾**（参数表写顶层 `image`，示例与注意事项写 `extra_body.image`） | 图生图直接失败 | M0 实测，结论固化进 `docs/providers/agnes.md` + 单测锁定 |
| Agnes 参考图上限未公开 | 多图合成的张数上限靠猜 | M0 试出边界，写进 `Caps.MaxRefs` |
| SenseNova `url` 24h 过期、Agnes url 有效期未知 | 历史图片变死链 | 默认强制 `b64_json` + 立即落盘 + `expires_at` 标记 |
| 两家 `n` 均只支持 1 | 批量出图需多次调用 | job 层拆分 + 串行执行 + UI 显示批次进度 |
| 上游尺寸约束不对称（32 倍数 vs 档位×ratio） | 参数在某些通道上非法 | 统一模型 + 适配器查表映射 + 前端能力自适应 |
| 4K 档限流极低（Agnes 4K = 1 RPM） | 高并发直接 429 | 并发上限 2 + 退避重试；UI 显示排队 |
| 免费额度政策变化（Agnes 现价 ¥0、SenseNova 公测免费） | 上线后成本失控 | 记录每张产出到 `usage_daily`，留作后续计费依据 |
| Key 泄漏 | 资损 | AES-GCM 加密、主密钥走环境变量、日志脱敏、前端只回掩码 |
| 大图内存占用 | OOM | 下载限长、缩略图异步生成、列表虚拟化 |

---

## 11. 待确认

1. **存储**：SQLite（单文件零依赖）可接受吗？还是要外置 Postgres？
2. **部署**：纯本地单机（默认只听 127.0.0.1）还是要局域网访问？要不要密码？
3. **Key 来源**：UI 配置落库加密，还是只读环境变量？
4. **并发默认**：默认 2 个并发可以吗？
5. **默认通道**：建议默认 SenseNova（有去水印开关、返回尺寸更可控），是否认可？
6. **i18n**：界面只中文，还是需要中英双语？
7. **二期何时启动**：一期跑通后就直接接 OpenAI，还是先让你用一阵子再动？

---

## 附录 A：SenseNova 最小可用请求

```bash
# 文生图（b64 输出，规避 24h URL 过期）
curl https://token.sensenova.cn/v1/images/generations \
  -H "Authorization: Bearer $SENSENOVA_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "sensenova-u1.5-fast",
    "prompt": "A luminous floating city above a misty canyon at sunrise, cinematic realism",
    "n": 1,
    "size": "2720x1536",
    "output_format": "png",
    "response_format": "b64_json",
    "watermark": false,
    "prompt_extend": true
  }'

# 图生图（独立端点，参考图 ≤5，首图为主图）
curl https://token.sensenova.cn/v1/images/edits \
  -H "Authorization: Bearer $SENSENOVA_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "sensenova-u1.5-fast",
    "images": [{ "image_url": "https://example.com/in.png" }],
    "prompt": "背景换成在一望无际的冰川上",
    "size": "auto",
    "watermark": false,
    "response_format": "b64_json"
  }'
```

## 附录 B：Agnes 最小可用请求

```bash
# 文生图 · 2K · 16:9 → 2624x1472
curl https://api.agnes-ai.cn/v1/images/generations \
  -H "Authorization: Bearer $AGNES_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "agnes-image-2.5-flash",
    "prompt": "A luminous floating city above a misty canyon at sunrise, cinematic realism",
    "size": "2K",
    "ratio": "16:9",
    "extra_body": { "response_format": "b64_json" }
  }'

# 图生图（文档注意事项要求走 extra_body.image —— M0 首要验证项）
curl https://api.agnes-ai.cn/v1/images/generations \
  -H "Authorization: Bearer $AGNES_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "agnes-image-2.5-flash",
    "prompt": "将白天街道改为赛博朋克夜景，保留原始街道布局与相机角度",
    "size": "2K",
    "ratio": "16:9",
    "extra_body": {
      "image": ["data:image/png;base64,BASE64_HERE"],
      "response_format": "b64_json"
    }
  }'
```

## 附录 C：二期 —— OpenAI 通道备忘

一期不做，这里只留接口设计约束，二期直接落地不再调研：

- Base URL 与模型名**必须从配置读取**，不硬编码（走中转站时模型名大概率不是官方的）
- 官方规格：主力 `gpt-image-2.5-sunburst`（编辑精度）/ `gpt-image-2.5-flare`（快速），端点 `/images/generations` + `/images/edits`，参考图 ≤16 张且支持 `file_id`
- 尺寸：W/H 为 16 倍数、总像素 655,360~8,294,400、边长 ≤3840 → **`4096x4096` 会被拒**，故 Tier4K 对 OpenAI 不可达，需 `Caps.MaxTier`
- 独有能力：`mask` 局部重绘、`stream`+`partial_images 0..3` 渐进预览、`background:transparent`、5 档质量、`n>1` 原生支持
- 注意：`input_fidelity` 只对 `gpt-image-1`/`1.5` 有效，2.0+ 必须省略否则 400
- 像素校验默认**告警而非硬拦**（中转可能放宽官方约束）