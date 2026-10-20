# Imgen

自托管的单二进制图像生成/编辑工作台。Go 后端 + Vue3 前端（前端产物 `go:embed` 进二进制，纯 Go、无 CGO）。
一期对接 **SenseNova** 与 **Agnes** 两家官方通道，统一抽象层屏蔽端点差异，提供文生图、图生图、
多图合成、批量生成与本地资产库。

## 特性

- 单文件二进制部署，前端已内嵌，运行时无需 Node
- 两家通道（商汤日日新 SenseNova / Agnes AI）统一 `provider` 抽象，能力自适应参数面板
- 参考图按通道能力自适应（**SenseNova 上限 5 张，Agnes 上限 6 张**，面板按 `caps.maxRefs` 显隐），
  本地内容寻址存储 + 缩略图，生成结果下载 / 送编辑 / 作参考
- 任务队列（默认并发 2，可在线调整），429 / 5xx 指数退避重试，SSE 进度推送
- API Key 在 UI 配置、AES-GCM 加密落库，主密钥走环境变量
- 提示词预设库（可复用模板 + `@N` 参考图渲染）

## 目录结构

```
cmd/imgen/            # 入口（serve / version）
internal/
  app/               # 依赖装配与生命周期
  config/            # 环境变量配置
  domain/            # 实体与领域错误
  httpapi/           # chi 路由 / handler / DTO / 错误映射
  httpx/             # 上游 HTTP 客户端（退避重试）
  job/               # worker pool 调度
  janitor/           # 按 keepDays 周期清理历史任务与孤儿资产
  provider/          # 抽象 + sensenova/ agnes/ 适配器
  secret/            # Key 加密
  blob/              # 内容寻址图片 + 缩略图
  store/             # SQLite 仓储（modernc 纯 Go）+ 内嵌迁移
  web/               # 前端嵌入 / SPA fallback（3 种 build tag）
web/                 # Vue3 源码
docs/providers/      # 各通道实测记录（改适配器前先看）
scripts/             # 开发脚本（smoke.ps1 冒烟）
```

## 本地运行

前置：Go 1.27、Node 20+、pnpm（仅构建前端时需要）。

```powershell
# 一键：构建前端 + 编译内嵌前端产物的二进制
task
# 或手动
pnpm --dir web install --frozen-lockfile
pnpm --dir web run build
go build -tags imgenweb -o imgen.exe ./cmd/imgen

./imgen.exe serve            # 默认 :8080（所有网卡），数据目录 ./data
```

### ⚠️ 监听地址与鉴权（部署前必读）

`IMGEN_ADDR` 默认是 `:8080`，即**监听所有网卡**（不是只监听 `127.0.0.1`）。
而**本应用没有任何鉴权**——没有登录、没有 token、没有访问控制，任何能访问该端口的人
都可以读写 Provider API Key（UI 里 Key 只以掩码显示，但可以被覆盖）、提交任务、删除资产。

因此：

- **仅本机使用**：显式改成 `IMGEN_ADDR=127.0.0.1:8080`（只监听回环，外部不可达）。
- **要局域网 / 公网访问**：必须自己在前面加**反向代理 + 认证**（如 Caddy `basicauth`、
  Nginx `auth_basic` 或放在已有 SSO 之后），不要直接把 `:8080` 暴露出去。

```powershell
# 只监听本机（推荐的个人使用姿势）
$env:IMGEN_ADDR = "127.0.0.1:8080"
./imgen.exe serve
```

纯后端编译（不含前端，走占位页，CI 快编译）：`go build ./cmd/imgen`。
开发模式（前端 HMR）：`go run -tags dev ./cmd/imgen serve` + 另开终端 `pnpm --dir web dev`。

常用环境变量：`IMGEN_ADDR`（默认 `:8080`，**所有网卡**）、`IMGEN_DATA`（默认 `./data`）、
`IMGEN_CONCURRENCY`（默认 2）、`IMGEN_MASTER_KEY`（AES-GCM 主密钥，留空自动生成）、
`IMGEN_MAX_UPLOAD_MB`、`IMGEN_MAX_REMOTE_MB`、
`IMGEN_KEEP_DAYS`（历史保留天数，默认 30，**0 = 完全禁用自动清理**）。
详见 `cmd/imgen/main.go` 的 `help` 输出。

## Docker 部署

纯 Go 静态二进制 + Alpine 运行层，无 CGO、无外部运行时依赖（见 `Dockerfile`）。

```bash
docker build -t imgen:latest .
docker run -d --name imgen -p 8080:8080 -v imgen-data:/home/imgen/data imgen:latest
```

数据目录为可写卷 `/home/imgen/data`（SQLite、图片、主密钥落盘处）。
前端产物在镜像内已内嵌（`-tags imgenweb`），容器内无需 pnpm。也可用 `task docker:build` / `task docker:run`。

> `-p 8080:8080` 会把无鉴权的服务发到宿主所有网卡上。个人使用请绑回环
> （`-p 127.0.0.1:8080:8080`），对外提供服务请前置反向代理 + 认证（见上一节）。

## 测试

```
go build ./... && go vet ./... && go test ./...   # 全绿
task check                                        # 全量校验（含前端类型与契约）
task smoke                                        # 冒烟：起服务 → 校验各接口 → 跑前端契约校验
```

`Taskfile.yml` 里的 `smoke` 对应 `pwsh -NoProfile -File scripts/smoke.ps1`：用独立端口与
**临时数据目录**拉起一份 `imgen serve`，逐项校验 `health` / `providers` / `settings` /
`jobs` / `assets` / `usage` / `presets` / 静态首页的形状，再跑 `pnpm run check:contract`，
结束后自动停进程并清理。它**永远不会读写 `./data`**（真实数据）。

## 文档

- `HANDOVER.md` —— 当前状态、架构、已知坑位与修复记录（排障先看这里）
- `PLAN.md` —— 一期范围、技术选型、里程碑与风险
- `docs/providers/` —— 各通道实测记录，`M0` 实测清单
