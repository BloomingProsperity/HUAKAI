# 2026-05-08 纵向闭环计划 — Claude 草案

| 字段 | 值 |
| ---- | ---- |
| Owner directive | "横向扩展完成后立即进行纵向闭环" |
| 横向 已完成 | Track A audit / Track B sticky routing / Track C auto-inject / Track D global metrics / Track P per-account metrics（Bedrock A1-A8 + Anthropic translator + 闭环 wire 全 in main） |
| Lane | planner（独立草案，未参考 codex 同名 plan） |
| 输出形态 | 计划草案（不写代码），待 Owner + codex 双 lane 同步后才执行 |

## 1. 纵向闭环候选

### 候选 A: Bedrock-on-Anthropic 全 E2E
- **状态**：全栈代码 in main（A1 endpoint URL、A2 binary EventStream、A3 SSE scanner、A4 proto adapter、A5+A6 forwarder 注册、A7 chat-completions handler、A8 Anthropic→Bedrock translator + Track C cache_control 注入）
- **覆盖**：Anthropic CLI / Claude Code 客户端 → HUAKAI 入口 → routing → bedrock_invoke adapter → SigV4 sign → AWS Bedrock invoke-with-response-stream → binary EventStream → HUAKAI canonical events → SSE 输出 → 客户端
- **缺口**：单 atom 单测 OK，但 **整链 E2E** 是否 actually work 没人验过
- **凭据**：sigv4，无 OAuth 复杂度
- **风控**：低（AWS 是公开 endpoint，不踩 vendor 风控）

### 候选 B: OpenAI Chat Completions 全 E2E
- **状态**：adapter + scanner + forwarder + handler 都早已 in main
- **覆盖**：OpenAI client → HUAKAI → openai_chat → ChatCompletions endpoint → SSE → 客户端
- **缺口**：cache 路径（Track B/C/D/P）目前 wire 在 OpenAI adapter 上是否真起作用？尚未 E2E 验
- **凭据**：apikey
- **风控**：低（标准 OpenAI API）

### 候选 C: 同时双闭环
- **不推荐**：scope 太大，先单一 vertical 拿到证据再扩

## 2. 推荐: 候选 A (Bedrock-on-Anthropic)

理由:
1. 横向工作刚落在这一栈（A1-A8 + Track C wire），E2E 验证最有信息密度
2. Owner 最近 push "继续 bedrock"（plan 引用）— 与 Owner 前进方向一致
3. SigV4 = 自实现签名（不依赖 aws-sdk-go），E2E 能查出"我们签的对不对"，单测覆盖不到
4. Bedrock 是所有 cache fields（cache_creation_input_tokens / cache_read_input_tokens）都透传走的最长路径——闭环验证后 Track D + P metrics 能看到真数据

## 3. 闭环要素（Bedrock-on-Anthropic 全栈验证）

| 项 | 验证方式 | 通过标准 |
|----|---------|---------|
| ① 入站协议 | curl POST `/v1/messages` (Anthropic Messages API form) → HUAKAI | 200 + SSE stream |
| ② 路由 | Bedrock account routing via sticky_bindings | 同 prompt hash 复用 account |
| ③ Body 翻译 | TranslateAnthropicAPIToBedrock 剥 model + 注 anthropic_version | 出站 body 含 `anthropic_version: bedrock-2023-05-31`，无 `model` 字段 |
| ④ Cache 注入 | AutoInjectSystemCacheControl 长 system prompt | 出站 body 末块含 `cache_control:{type:"ephemeral"}` |
| ⑤ SigV4 签名 | 自实现 sigv4Signer 对 post-translation body | AWS 接受请求，不 401 |
| ⑥ EventStream 解码 | binary :event-type / :content-type 解析 + base64 payload | 客户收 Anthropic 形 SSE 事件 |
| ⑦ Canonical 事件流 | message_start / content_block_delta / message_delta / message_stop | 透传无丢失 |
| ⑧ Cache metrics | /debug/vars cachemetrics.cache_token_count.{creation_total,read_total} | 第二次相同请求 read_total > 0（缓存命中） |
| ⑨ Per-account metrics | cache_token_count_by_account.<account_id>.* | 命中流量分账号统计 |
| ⑩ Sticky binding 写入 | sticky_bindings 表 row | upsert 成功 (tenant_id, session_hash, model, account_id) |

## 4. 执行路径选项

### 路径 X: Owner 本机真 AWS 凭据 E2E
- 需要 Owner 提供 AWS access key + region + Bedrock model 启用权限
- 跑 2 次相同请求（第二次应命中缓存）
- 优势：真上游，最强证据
- 障碍：依赖 Owner 提供凭据 + 真实 AWS 账单

### 路径 Y: httptest mock-server E2E
- 在 sandbox 跑真 binary：HUAKAI gateway 起来 + httptest.Server 模拟 AWS Bedrock invoke-with-response-stream（返 binary EventStream payload 含真 base64 的 Anthropic 事件 JSON）
- curl 真 POST `/v1/messages` → HUAKAI → mock Bedrock
- 验 ⑥-⑩ 全栈，不验 SigV4 服务端接受（mock 不签）
- 优势：可全自动化，可入 CI
- 缺口：⑤ SigV4 真 AWS 接受性需另跑

### 路径 Z (推荐): 双轨
- 先跑路径 Y（落地一个 E2E 集成测试 file，CI 可重跑）
- 然后 surface 给 Owner 跑路径 X 一次（5-10 min，2 个真请求）
- 这样 ⑤ 也得真证据但不阻塞 CI

## 5. 不在 scope

- 不引入新 vendor (OpenAI / Gemini / 其它已 wire 的等下一轮 vertical)
- 不改 cache 路由算法 (横向工作已落)
- 不引依赖 (httptest 是 stdlib)
- 不动 mimicry / R7 (R7 还在 OCAW 评估，与本 vertical 无关)

## 6. 风险

| 风险 | 概率 | 缓解 |
|------|------|------|
| AWS 真凭据需 Owner 出 → 阻塞 | 中 | 路径 Z 把真凭据从必须降为补充 |
| EventStream 解码 corner case 漏 | 低 | binary fixture file 多 case 覆盖 |
| 缓存命中要等 5 min TTL → CI 时间长 | 中 | Track C 注入 ephemeral 是默认 5min；mock server 直接 echo 命中字段，不真等 vendor |
| sticky_bindings 表 dependency on Postgres | 中 | E2E 测可用 SQLite-compat 或 testcontainers |

## 7. 估时

| 步骤 | 估时 |
|------|------|
| 写 mock Bedrock httptest server (binary EventStream emit) | 60 min |
| 写 E2E test (curl-style, gateway 启动 + mock server) | 45 min |
| 跑通 + 修发现的 wire bug | 60 min（保守） |
| Owner 路径 X 真 AWS 验证 + 文档化 | 15 min Owner-side |
| docs/plans 更新 + commit | 15 min |
| **合计** | **~3 hours**（Claude 一个人推） |

## 8. 决策点（待 Owner）

1. 候选 A (Bedrock) 还是 B (OpenAI) 还是 C (双)？
2. 路径 X / Y / Z？（Z 是推荐）
3. 是否需要在闭环里加 chaos test（断 TCP / 截 EventStream 中段）？
4. cache_creation 第一次 + cache_read 第二次的 1-iteration 验证够不够，还是要加 5-iteration burst？

## 9. clean-room 边界

- 本 vertical 不读 CPA / sub2api 源码（HUAKAI 自己已 in-main 全栈，闭环验证不需要外参考）
- 若发现 EventStream 解码 corner case 需对照外参考，按 2026-05-08 强化规则读源码 + 不抄

---

**草案完成，待 codex 平行 draft + Owner 决策。**
