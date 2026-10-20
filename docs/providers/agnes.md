# Agnes AI 实测记录

入口与模型以 2026-10-15 实测为准。改 `internal/provider/agnes` 前先看这里，改完把结论写回。

## 入口

实测入口为 `https://apihub.agnes-ai.cn/v1`。
`https://api.agnes-ai.cn/v1` 会返回 `401 无效的令牌`，但 apihub 入口正常。

**未充值也能用**：`agnes-image-2.5-flash` 在无充值状态下实测出图成功（1K 约 10s）。

模型列表（实测）：`/v1/models` 里图像模型只有三个：
`agnes-image-2.5-flash` / `agnes-image-2.1-flash` / `agnes-image-2.0-flash`。
注意 `agnes-2.5-flash`、`agnes-3.0-flash` 是**聊天模型**，走 `/v1/chat/completions`，
用于生图会返回 `Model ... is a chat model`；`agnes-image-3.0-flash` 根本不存在
（返回 `model_not_found`）。迁移 `0002_fix_agnes_models.sql` 已清理错误记录。

## 2026-10-15 实测（apihub 入口，Key 有效）

| 项 | 结果 |
|---|---|
| 文生图 1K | ✅ 200，约 10.4s，返回 b64_json |
| 文生图 4K | ✅ 200，约 20.3s |
| 编辑 `extra_body.image` | ✅ 200，约 19s |
| 编辑顶层 `image` | ⏳ 多次 `503 image queue is full`，未拿到成功样本 |
| 参考图 6 张 | ✅ 200，约 120s（排队很久） |
| 参考图 9 张 | ❌ 400 `too many input images: 9 provided, at most 6 allowed` |
| 参考图上限 | ✅ 6 张 |
| `response_format:url` | ✅ 返回 `https://platform-outputs.agnes-ai.space/...`，立即下载成功，有效期待验证 |
| 排队 | ⚠️ 频繁 `503 image queue is full`，**不带 `Retry-After` 头**；
  httpx 退避重试 3 次（约 0.5s/1s/2s）常常不够，队列持续满时会最终失败 |
| 429 限流 | ✅ 2026-10-20 实测：4K 连发第 2 次即 `429 rate_limit_exceeded`，
  响应头**无** `Retry-After`、无 `X-RateLimit-*` |

## 2026-10-20 补测（小额度）

### 429 确认不带 `Retry-After`

连发两次 4K 请求：第 1 次 200（约 20.3s），第 2 次立即 **429**（518ms）：

```json
{"error":{"message":"You've reached the API rate limit for free users. Upgrade to a Token Plan ...","type":"AgnesAI_error","param":"","code":"rate_limit_exceeded"}}
```

**响应头里没有 `Retry-After`，也没有任何 `X-RateLimit-*`**。
→ `httpx` 必须靠自己的抖动退避，不能指望上游指示等待时长。这条自 2026-10-15 起
就写在 `httpx` 的设计里，本次得到直接验证。

### 顶层 `image` vs `extra_body.image` 对照实验

用同一张 1x1 参考图，分别打两个分支（各请求 3–4 次）：

| 分支 | 结果 |
|---|---|
| 顶层 `image`（`TopLevelImage=true` 对应分支） | 4 次全部 `503 image queue is full` |
| `extra_body.image`（对照组，已验证可用） | 3 次全部 `503 image queue is full`（另 1 次客户端超时） |

**关键结论：两个分支表现完全一致，503 是上游排队拥堵，与参考图字段位置无关。**
这排除了「顶层 `image` 因字段不被支持而被拒」的猜测 —— 但也**仍没有**顶层的成功样本。
要定稿字段位置，需在上游队列空闲时重跑顶层分支，且**必须先跑一次对照组**
确认队列通了，否则 503 会被误读为字段错误。

## 已核实结论

**默认 BaseURL 使用 apihub 入口**，已同步到适配器与迁移 SQL。
参考图上限为 6，已同步到 `maxRefsGuess`（`internal/provider/agnes` 与前端）。

- 参考图字段位置：文档自相矛盾（参数表写顶层 `image`，「重要说明」与示例写
  `extra_body.image`）。实测 `extra_body.image` 成功；顶层 `image` 因上游持续
  `503 image queue is full` 未拿到成功样本，但 9 张参考图错误信息确认走的也是
  `extra_body.image` 路径。适配器默认走 `extra_body.image`，保留 `TopLevelImage` 回落开关。
  → 2026-10-20 对照实验证明该 503 与字段位置无关（两分支同样 503），
  **顶层支持与否仍待队列空闲时验证**，见 [README.md](README.md) ①。
- 4K 档实测约 20s。
- **429 不带 `Retry-After`**（2026-10-20 确认），`httpx` 自实现抖动退避是必需的。
- 返回 url 托管在 GCS（`platform-outputs.agnes-ai.space`），已拿到样本并立即下载成功，
  过期时间仍待后续验证（需跨 +1h/+6h/+24h 探测）。
