# 三方向差异化评估 — 综合（含源码证据）

**日期**: 2026-05-09
**作者**: Claude（综合 Lane A/B/C 的 specifier 报告 + 自身评估 + Codex 评估）
**前置文件**:
- `docs/process/plans/2026-05-09-three-directions-claude.md`（Claude 草案，无源码）
- `docs/process/plans/2026-05-09-three-directions-codex.md`（Codex 草案，docs 证据）
- `docs/research/2026-05-09-source-read-sub2api-newapi.md`（Lane A）
- `docs/research/2026-05-09-source-read-oneapi-portkey-litellm.md`（Lane B）
- `docs/research/2026-05-09-source-read-helicone-envoy-allapihub.md`（Lane C）

**触发**: Owner 2026-05-09 quote "去读源码！讲规则里面改下必须读源码"

## TL;DR — 重大修正

之前 Claude/Codex 评估的核心断言"PASR 是首创的 cache-aware routing"**被源码证伪**：
- **LiteLLM 已实现 prompt-cache locality routing**（`BerriAI/litellm@b5d3a5fc:litellm/router_utils/prompt_caching_cache.py`）
- new-api 已实现 **passive cache affinity layer**（per-rule fingerprint + Redis 粘绑）

PASR 的真实差异化不是"做了 cache-aware"，而是**比已有实现更精细**：
1. **跨账号 cache 复制意图**（LiteLLM 单 deployment 硬 pin，无复制语义）
2. **score-based locality + headroom blending**（LiteLLM 是 hard pin）
3. **cache-miss demotion**（8 个参考项目无一实现）

方向 2 / 3 在 8 个参考项目源码中**确为空白**（gateway 层），novelty 站得住——但层级与经济模型问题仍在。

## 1. 源码读取记录（per CLAUDE.md #12）

| 项目 | Repo@SHA | License | Lane | pushed_at | archived | HEAD commit msg | UTC fetched |
|------|----------|---------|------|-----------|----------|-----------------|-------------|
| sub2api | Wei-Shaw/sub2api@dbc8ae65 | LGPL-3.0 | A | 2026-05-09 | false | "chore: update sponsors" (2026-05-08) | 2026-05-09T07:50Z |
| new-api | Calcium-Ion/new-api@d146e45e | AGPL-3.0 | A | 2026-05-09 | false | "chore(web/default): copyright header tooling" (2026-05-09) | 2026-05-09T07:50Z |
| one-api | songquanpeng/one-api@8df4a267 | MIT | B | **2026-01-09 ⚠️ STALE (>90d)** | false | "docs: ByteDance Doubao link" (2025-02-21) | 2026-05-09T07:50Z |
| portkey-gateway | Portkey-AI/gateway@351692fd | MIT | B | 2026-03-25 (45d) | false | "Merge: fix/forward-headers" (2026-03-25) | 2026-05-09T07:50Z |
| litellm | BerriAI/litellm@b5d3a5fc | mixed (skip enterprise/) | B | 2026-05-09 | false | "feat: read-replica routing" (2026-05-08) | 2026-05-09T07:50Z |
| helicone | Helicone/helicone@3f4bd44b | Apache-2.0 | C | 2026-05-05 (4d) | false | "fix: remove RAW debug logs" (2026-05-01) | 2026-05-09T07:50Z |
| envoy-ai-gateway | envoyproxy/ai-gateway@4d3eae8b | Apache-2.0 | C | 2026-05-08 | false | "fix: gcp finish reason mapping" (2026-05-08) | 2026-05-09T07:50Z |
| all-api-hub | qixing-jk/all-api-hub@893e832d | AGPL-3.0 (combined) | C | 2026-05-09 | false | "docs: E2E guidelines" (2026-05-09) | 2026-05-09T07:50Z |

**STALE 注释（per CLAUDE.md #12 first-cite recency rule）**: one-api 已超 90 天窗口（4 个月未推送）。仍 cite 8df4a267 因为：(a) 这是当前 HEAD，没有更新版本可比对，(b) 引用语义是"one-api 当前实现是 priority-bucketed random"——项目状态本身是稳定，不是被遗弃；本评估对此结论的依赖很弱（只用于 cache locality 矩阵的对照行）。如要移除 STALE 警示需 Owner 授权 fetch 较新 fork 或重新评估。

每条断言下方引用具体 file:line。所有 lane 全程 specifier，无 verbatim copy；详细 paraphrased summary 在 `docs/research/`。

## 2. 三方向 verdict 重核（基于源码）

### 方向 1: Account Cache Fabric

**之前 verdict**: Block on cache scope 物理验证

**源码证据修正**:
- LiteLLM 的 prompt-cache locality routing：SHA-256 hash 可缓存前缀 → model_id 映射，dual cache (memory+Redis) 300s TTL，pre-call filter 限定到 pinned deployment（`BerriAI/litellm@b5d3a5fc:litellm/router_utils/prompt_caching_cache.py`、`prompt_caching_deployment_check.py`）
- new-api 的 cache affinity：基于 body 字段（`prompt_cache_key` / Anthropic `metadata.user_id`）的规则化 sticky，hybrid memory+Redis cache（Lane A 报告，Calcium-Ion/new-api@d146e45e）
- 两者都是**单账号上的 cache locality**，**不做跨账号复制**
- sub2api 有 `intercept_warmup_requests` flag 但语义相反——拦截客户端 warmup probe 返回 mock，**不是 gateway 主动预热账号**

**最终 verdict**: **暂停验证**，但框架修正
- ✅ 跨账号 cache 复制（active replication）确实是 8 项目空白
- ❌ "首创 cache-aware routing"叙事错误，LiteLLM 已做 passive 版本
- 实施前仍需 Owner 授权 smoke test 验证 cache scope（per-workspace / per-org 边界），数据见 Codex 草案的官方 doc 引文
- 经济模型仍是 Codex 草案中的"花钱买 latency / capacity / reliability"，不是省钱

### 方向 2: Multi-Account Request Decomposition

**之前 verdict**: 移出 gateway scope（层级错位）

**源码证据修正**:
- 8 项目无一在 gateway 层做请求分解（Lane A/B/C 一致）
- LiteLLM 的 batch mode 是用户驱动（1 batch in / 1 batch out），不是 gateway 决策的 fan-out（Lane B）
- Portkey 的 conditional routing 仅做选择，不做拆分（Lane B）
- 没有任何 speculative / hedged / parallel-race upstream attempts

**最终 verdict**: **gateway scope 内否决，作为独立产品候选**
- novelty 真实存在
- 但 Claude / Codex 共识的"层级错位"理由仍成立：idempotency 模型崩塌、SSE 合并语义、错误半失败、billing 1:N
- 应单立 `HUAKAI Orchestration Runtime` 项目，gateway 不动

### 方向 3: Predictive Session Migration

**之前 verdict**: 经济模型不成立，降级到 reactive cache bridge

**源码证据修正**:
- 8 项目全部反应式 failover，无预测式（Lane A: sub2api 删除 sticky 启动冷；new-api 通道监控只填 dashboard / Lane B: LiteLLM 有 MidStreamFallbackError 但触发是异常 / Lane C: envoy 委托给 Envoy Gateway BackendTrafficPolicy 也是反应式）
- predictive migration 真为空白

**最终 verdict**: **暂缓预测层，并入 F-SESSION-001 的 reactive bridge**
- novelty 真实
- Codex 草案的经济模型分析仍成立：5min TTL + 嘈杂信号 = 假阳性烧钱
- **新发现**：LiteLLM 的 `MidStreamFallbackError`（`BerriAI/litellm@b5d3a5fc:litellm/router.py:2052-2194`）实现了 mid-stream 切换 + continuation prompt + usage merge——这是 HUAKAI R5/R7/R8 流式稳定层的强参照，远比"预测式迁移"更紧迫且可落地

## 3. PASR 真实差异化矩阵（fusion-upgrade 三维格式 per CLAUDE.md #12）

| Feature | Upstream A cite | Upstream B cite | HUAKAI delta | Dimension(s) |
|---------|-----------------|-----------------|--------------|--------------|
| Cache 感知路由 | LiteLLM prefix-hash → model_id pin: `BerriAI/litellm@b5d3a5fc:litellm/router_utils/prompt_caching_cache.py` | new-api rule-based channel affinity: `Calcium-Ion/new-api@d146e45e:service/channel_affinity.go` | vendor-neutral observer + tenant-scoped segment table，多账号 bitmap 而非单 model_id pin | 架构 + 算法 |
| Score blending (locality+headroom) | LiteLLM hard pin (无 blend): `BerriAI/litellm@b5d3a5fc:litellm/router_utils/pre_call_checks/prompt_caching_deployment_check.py` | (8 项目均无 score blending) | `score = locality_signal × cache_score + headroom × headroom_weight`，热门账号自动让路: `backend/internal/pool/pasr_selector.go:209-240` | 算法 |
| Cache-miss demotion | (8 项目均无 — per Lane A:209-211, Lane B:22, Lane C:64) | — | MissCount + 阈值 demote 自动遗忘失效绑定: `backend/internal/pool/prefix_segment.go:89-105` + `pasr_feedback.go:97-105` (今天 A3 落) | 算法 + 生态 (metrics) |
| 跨账号 cache 复制 | (8 项目均无 — 同上) | — | 段表 bitmap 模型为多账号共享设计；待 vendor cache scope smoke test 验证物理可行 | 架构 |
| Sticky session | sub2api session-hash + `previous_response_id` 链 (10min TTL): per Lane A:266 | new-api rule-based fingerprint: per Lane A | session_hash + tenant_id 键 + prefix segment 集成: `backend/internal/pool/prefix_segment.go:48-99` | 架构 |
| 反应式 failover | LiteLLM `MidStreamFallbackError` mid-stream switch: `BerriAI/litellm@b5d3a5fc:litellm/router.py:2063+2209` + `litellm_core_utils/streaming_handler.py:2268-2328` | Helicone ordered sequential attempts: per Lane C:64 | Tx2 R5/R6/R7 stream stability 立项以 LiteLLM 为基线（见 §5 P0 行动） | 算法 + 架构 |
| 预测式 failover | (8 项目均无 — 同上) | — | 暂缓（5min TTL + EWMA 噪声 → 经济模型不成立，per Codex 草案） | 算法（暂不实施） |
| 请求拆分 | (8 项目均无 — 同上) | — | 不进 gateway scope，单立 `HUAKAI Orchestration Runtime` | 架构（独立产品） |
| 异步任务 axis (DLQ/outbox) | Helicone hot-path single-queue + 14 wired handler chain + DLQ + 15min timeout + priority lanes: `Helicone/helicone@3f4bd44b:worker/src/lib/dbLogger/DBLoggable.ts:1032` + `valhalla/jawn/src/managers/LogManager.ts:71-230, 174-205` | — | 修 settler.go:78-83 全 rollback 缺口；HUAKAI 现状 0%（生态层最大空白） | 生态 + 架构 |

**核心修正**：HUAKAI 在 cache-aware routing 上**不是首创**——LiteLLM + new-api 都已 passive 版本（per Lane B + Lane A 源码核）。差异化是**精细度**（score blend + miss demote）和**架构意图**（vendor-neutral observer + 跨账号复制规划）。Owner 对外叙事必须按"在 LiteLLM cache locality + new-api channel_affinity 基础上叠加 [架构/算法/生态] delta"格式。

## 4. 真正的差异化空间（基于源码独立列举）

源码读完后，新发现的可借鉴/可对标模式（Lane B/C 整理）：

### 已有项目实现，HUAKAI 应借鉴的（paraphrased）

1. **LiteLLM MidStreamFallbackError 模式** → HUAKAI R5/R7/R8 流式稳定层
   - `BerriAI/litellm@b5d3a5fc:litellm/router.py:2052-2194`
   - mid-stream 切上游 + continuation prompt + usage object merge
   - 比"预测迁移"更紧迫，可立即立项

2. **Helicone 的 escrow reserve→cancel-on-fail→settle 钱包流** → HUAKAI billing
   - 与现有 Tx1/Tx2 协议同构但更轻量
   - Lane C 报告引用 `Helicone/helicone@3f4bd44b`

3. **Envoy AI Gateway 的 OIDC→cloud-STS 凭据交换** → HUAKAI enterprise tier
   - 客户集群部署不存长期 secret（AssumeRoleWithWebIdentity for AWS 等）
   - `envoyproxy/ai-gateway@4d3eae8b:internal/...`（具体路径见 Lane C 报告）
   - 标 Mandatory Roadmap

4. **all-api-hub 的 vendor-pluggable auto-checkin scheduler** → HUAKAI account hub
   - 2633 LoC scheduler.ts 实现 deterministic-or-random within window + 双闹钟（daily/retry）
   - AGPL-3.0，paraphrased 重写
   - `qixing-jk/all-api-hub@893e832d`

5. **all-api-hub 的 verification probe registry**（model+token+CLI-compat 三轴）→ HUAKAI 健康仪表盘

6. **all-api-hub 的 credential profile + 一键 CLI-tool 导出** → HUAKAI 用户体验

7. **Helicone 的 async log consumer chain + DLQ + priority lanes**（重核后修正） → HUAKAI settler 的 DLQ 替代方案
   - 解决 settler.go:78-83 已知的"usage record 写败全 rollback"问题
   - 实际架构（per `Helicone/helicone@3f4bd44b` re-verify lane）：
     - hot path: 单 producer.sendMessage → Kafka 或 SQS（`worker/src/lib/dbLogger/DBLoggable.ts:1032`）
     - cold path: **14 个** wired handler（不是 Lane C 报告的 15）via `AbstractLogHandler.setNext()` chain（`valhalla/jawn/src/managers/AbstractLogHandler.ts:5-26` + `LogManager.ts:104-118`）
     - 真正 durability: DLQ queue + 15min per-message timeout + `setLowerPriority()` 两 tier 优先级（`LogManager.ts:174-205, 309-333, 125-128`）
     - DualWriteProducer 是 **asymmetric** —— Kafka 是 shadow/migration，SQS 才是 awaited authoritative
     - handler 共享 mutable `HandlerContext`，结构是"per-message chain + per-batch flush"
   - 详细 paraphrased summary: `docs/research/2026-05-09-helicone-chain-reverify.md`

### 8 项目集体空白（lane 交叉验证），HUAKAI 真有 novelty 的

**Lane crossref 证据（per CLAUDE.md #12 anti-pattern flag — 'no project does Y' 必须给 per-lane 引用）**:
- sub2api / new-api: per Lane A `docs/research/2026-05-09-source-read-sub2api-newapi.md` "Direction 2/3" 段（"Evidence not found in either repo"）
- one-api / Portkey / LiteLLM: per Lane B `docs/research/2026-05-09-source-read-oneapi-portkey-litellm.md:22` "TRUE for chat/completion paths"
- helicone / envoy / all-api-hub: per Lane C `docs/research/2026-05-09-source-read-helicone-envoy-allapihub.md` "A. Cache & fan-out — three-direction novelty CONFIRMED"

满足"no project at our precision does Y"短语的精度维度: gateway 层 / 透明转发契约 / OpenAI-compatible chat completion 接口范围内。

A. **跨账号 cache 复制意图**（即方向 1，但需先做 smoke test）
B. **请求语义分解**（方向 2，应作为独立 runtime）
C. **预测式迁移**（方向 3，经济模型不成立，留作未来）
D. **PASR 的 score blending + miss demotion 精细度**（已有，需明确这是 PASR 真差异，不是"cache-aware 首创"）
E. **多账号 quota 聚合 → 用户视角"无限 Pro"**（envoy/helicone 不做，all-api-hub 做管理面但不是 gateway）

## 5. 给 Owner 的合并建议（更新版）

### P0（立即做）
1. **修 PASR OpenAI/Gemini cache 接入缺口**（Codex 草案已识别死路径 + Gemini 未喂入）
2. **借鉴 LiteLLM MidStreamFallbackError 模式实现 R5/R7/R8 流式稳定层**——比方向 3 更紧迫且证据充分
3. **修正 PASR 对外叙事**：从"首创 cache-aware routing"改为"score-based locality + miss demote 的精细化版本"
4. **借鉴 Helicone 异步日志 chain 模式修 settler.go DLQ 缺口**

### Block on Owner 授权
1. **方向 1 smoke test**：跨 workspace / 跨 org 的 cache 复制实测（Anthropic + OpenAI + Gemini 各 4 组合）
2. **方向 2 立项**：是否单立 orchestration runtime
3. **all-api-hub 借鉴清单实施**：auto-checkin scheduler、verification probe registry、credential profile

### 降级
- 方向 3 → F-SESSION-001 manual cache bridge（不做预测层）

### 不做
- "PASR 是首创 cache-aware routing" 叙事

## 6. 新增/更新的 memory（per CLAUDE.md #12）

应新增 memory：
- LiteLLM 已实现 prompt-cache locality routing（@b5d3a5fc:router_utils/prompt_caching_cache.py 等）
- PASR 真差异化矩阵（score blend + miss demote + cross-account replication 意图）
- 8 项目集体空白（gateway 层精度，per Lane A/B/C 交叉验证 — 见 §4 lane crossref）：方向 2、方向 3、跨账号 cache 复制
- LiteLLM MidStreamFallbackError 模式应作为 HUAKAI R5/R7/R8 参照

## 7. 风险与盲点（自评）

- 仅读了 8 个项目；可能有非主流项目做了三方向（如商业闭源 SaaS）。但 paid-SaaS detection-resistance 说法常无源码可证伪
- LiteLLM 主目录 license 复杂，本次跳过 enterprise/ 子目录——若 enterprise 子模块有更激进 routing，本评估未覆盖
- new-api 是 AGPL-3.0，源码读 OK 但 paraphrase 必须更严格——Lane A 报告本身已遵守
- 没有跑实际的 smoke test 验证 cache scope；这仍是方向 1 的硬前置

## 8. 决策点（待 Owner）

- [ ] 同意修正 PASR 对外叙事框架？
- [ ] 授权方向 1 smoke test（含测试账号 + 预算）？
- [ ] 同意 LiteLLM MidStreamFallbackError 模式立项作为 R5/R7/R8 实现路径？
- [ ] 同意 Helicone 异步日志 chain 模式修 settler DLQ 缺口？
- [ ] 同意 all-api-hub auto-checkin / verification probe / credential profile 借鉴清单进 roadmap？
- [ ] 方向 2 单立 orchestration runtime 还是搁置？
