# docs/providers

各通道的实测记录。**改任何适配器之前先看这里，改完把结论写回对应文件。**

| 文件 | 状态 |
|---|---|
| [sensenova.md](sensenova.md) | ✅ 已实测（2026-10-15） |
| [agnes.md](agnes.md) | ✅ 已实测（2026-10-15，apihub 入口） |

## M0 实测清单（拿到真实 Key 后按顺序跑，结论写回对应文件）

- [x] **Agnes**：参考图到底走顶层 `image` 还是 `extra_body.image`
      （官方文档自相矛盾：参数表写顶层，「重要说明」与全部示例写 `extra_body.image`）。
      ✅ 2026-10-15 实测：`extra_body.image` 成功；顶层 `image` 因上游持续
      `503 image queue is full` 未拿到成功样本，但 9 张参考图错误信息确认
      走的是 `extra_body.image` 路径。详见 [agnes.md](agnes.md)。
      ⏳ **顶层 `image` 的成功样本仍未验证**（2026-10-20 收尾轮未执行），见下方待办。
- [x] **Agnes**：参考图张数上限（原 `maxRefsGuess = 8` 是猜的）
      ✅ 2026-10-15 实测：9 张返回 400 `too many input images: 9 provided, at most 6 allowed`，
      上限为 **6**。已把 `maxRefsGuess` 改为 6。
- [ ] **Agnes**：返回 url 的实际有效期（url 托管 GCS）
      ⏳ **待真实 Key 实测（2026-10-20 收尾轮未执行）** —— 2026-10-15 已拿到 url
      样本并立即下载成功，过期时间未验证。验证步骤见下方「M0 剩余三项」②。
- [x] **SenseNova**：`size:auto` 在 `/images/edits` 下的实际行为
      ✅ 2026-10-15 实测：`size:auto` 可用，返回 `2048x2048`。
- [ ] **两家**：429 时是否返回 `Retry-After`
      ✅ **2026-10-20 实测结论：两家 429 都【不返回】`Retry-After`**。
      - Agnes：4K 档连发第 2 次即 429（`rate_limit_exceeded`，
        `You've reached the API rate limit for free users`），
        响应头**无** `Retry-After`、也无 `X-RateLimit-*`。
      - SenseNova：串行连发 5 次 1K 请求**均返回 200**，未触发 429
        （限流阈值高于 5 次/分钟，未触达）；因此「SenseNova 429 是否带头」
        仍**无法确认**，但因两家共用 `httpx` 退避路径，实现上无需区分。
      → 结论：`httpx` 的自实现抖动退避是**必需**的，不能依赖上游 `Retry-After`。
- [x] **两家**：4K 档实际耗时（决定默认并发是否要下调）
      SenseNova 4K 实测约 28s（1K 约 4s、2K 约 8s）；Agnes 4K 实测约 20s。
- [ ] **Agnes**：顶层 `image` 的**对照实验**（2026-10-20 新增，比只看 503 更有价值）
      ✅ 2026-10-20 实测：用同一张 1x1 参考图分别打顶层 `image` 与 `extra_body.image`，
      **两者都返回 503 `image queue is full`**（顶层 4 次、对照组 3 次，无一成功）。
      → 该 503 是**上游排队拥堵**，与参考图字段位置**无关**。
      这排除了「顶层 `image` 因字段错误而被拒」的猜测，但也仍**没有**顶层成功样本。
      要拿到成功样本需在队列空闲时重试，见下方 ①。

## M0 剩余项（2026-10-20 收尾轮部分完成）

2026-10-20 用本机 `data/imgen.db` 里已配置的 Key 做了**小额度**实测，
完成项已回写上方清单。以下为**仍未完成**的两项及原因。

### ⏳ ① Agnes 顶层 `image` 的成功样本（仍缺）

已完成对照实验（见上方清单），确认 503 与字段位置无关。
**仍未拿到顶层 `image` 的成功响应** —— 不是字段不被支持，而是
**上游图像队列持续拥堵**（该时段 4 次顶层 + 3 次对照组全部 503）。

**验证步骤**（约 1 张图额度，建议在低峰时段做）：

1. 起一份**独立数据目录**的服务（别用 `./data`）：
   `IMGEN_DATA=./.probe-data IMGEN_ADDR=127.0.0.1:18090 ./imgen.exe serve`
2. 把 `internal/provider/agnes` 里 `TopLevelImage` 置 `true`（临时改，测完还原），
   重新 `go build -tags imgenweb -o imgen.exe ./cmd/imgen`。
3. 传 1 张参考图 → 提交一次**编辑**任务（1K 档），记录 HTTP 状态码与完整错误体。
4. 若 200：把 `TopLevelImage` 默认值与注释定稿，删除「文档自相矛盾」的歧义说明；
   若 4xx：在 [agnes.md](agnes.md) 写明「顶层 `image` 实测不被接受」。
5. **注意**：若又拿到 503，说明仍是排队问题，**不能**据此判定字段不支持 ——
   必须先跑一次 `extra_body.image` 对照组确认队列是通的。

### ⏳ ② Agnes 返回 url 的实际过期时间（本轮刻意未做）

`response_format:url` 的链接托管在 `https://platform-outputs.agnes-ai.space/...`，
已确认「拿到后立即下载成功」，但**多久失效未知**（SenseNova 已明确 24h）。

**本轮未做的原因**：验证需跨 **+1h / +6h / +24h** 三次探测，
单次会话内无法完成（用户已确认不为此长时间挂起会话）。

**验证步骤**（1 次生成 + 若干次 HEAD，几乎不额外消耗额度）：

1. 打一次 `/v1/images/generations` 带 `"response_format":"url"`，保存返回的 URL。
2. 立刻 `curl.exe -I <url>` 记状态码（预期 200）。
3. 在 **+1h / +6h / +24h** 各探一次：`curl.exe -I "<url>"`，看 `HTTP/` 状态行。
4. 首次出现 `403` / `404` 的时刻即为实际 TTL 下界。
5. 结论写回 [agnes.md](agnes.md)。

> 适配器已固定请求 `b64_json`，所以此结果**不影响当前代码路径**，
> 只影响文档与未来的降级策略。

## 已核实的共性事实

- 两家 `n` 均只支持 1 → 批量出图由 job 层拆成 N 次串行调用
- 两家都**不支持 seed** → 「复现」只能是近似（当前参数 + 结果图回灌为参考图）
- 两家返回的 url 都会过期 → 适配器固定请求 `b64_json`，
  若上游仍返回 url 则 `Executor.persist` 立即下载落盘
