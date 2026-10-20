# SenseNova（商汤日日新）实测记录

入口与模型以 2026-10-15 实测为准。改 `internal/provider/sensenova` 前先看这里，改完把结论写回。

## 入口

- Base URL：`https://token.sensenova.cn/v1`
- 文生图：`POST /images/generations`
- 图生图 / 编辑：`POST /images/edits`（独立端点）
- 模型：`sensenova-u1.5-lite`（质量优先）/ `sensenova-u1.5-fast`（速度优先）

## 2026-10-15 实测（Key 有效，全部通过）

| 项 | 结果 |
|---|---|
| 文生图 1K 1024x1024 | ✅ 200，约 4.2s，返回 b64_json |
| 文生图 2K 2720x1536 | ✅ 200，约 8.6s |
| 文生图 4K 4096x4096 | ✅ 200，约 27.9s |
| 文生图 21:9 3136x1344 | ✅ 200，约 8.1s |
| 编辑 `size:auto` | ✅ 200，约 8.9s，返回 2048x2048 |
| 编辑参考图约束 | 256–4096px、≤10MB、宽高比 ≤2:1（400 错误原文） |
| `response_format:url` | ✅ 返回签名 URL，`X-Amz-Expires=86400`（24h），立即下载成功 |
| 重复调用 | ✅ 连续多次 200，未见 429 |

## 2026-10-20 补测

串行连发 **5 次** 1K 文生图请求（间隔约 1–2s，总计约 55s）：
**5 次全部 200，未触发 429**，每次耗时 10–12s。

结论：
- SenseNova 的限流阈值**高于 5 次/分钟**，用这个速率打不上去。
- 因此「SenseNova 429 是否带 `Retry-After`」**仍未确认** —— 不是没查，是没打出来。
- 实现上无需为此额外工作：两家共用 `internal/httpx` 的退避路径，
  而 Agnes 侧已确认 **429 不带 `Retry-After`**（见 [agnes.md](agnes.md)），
  所以「不能依赖上游指示等待时长」这个结论对两家都成立。

## 已核实结论

- 4K 档实测约 28s（1K 约 4s、2K 约 8s）→ 影响默认并发取值（见 [README](README.md) 的 M0 清单）。
- 编辑 `size:auto` 可用，返回 `2048x2048`。
- url 24h 过期（`X-Amz-Expires=86400`）→ 适配器固定请求 `b64_json`，规避过期。
- 去水印 `watermark`、提示词增强 `prompt_extend` 是 SenseNova 独有能力，前端据此显隐。
- 429 未复现：2026-10-15 与 2026-10-20 两轮共 5+ 次连发**均未触发 429**，
  无 `Retry-After` 样本；如实记录为「未触发」而非「不存在」。
