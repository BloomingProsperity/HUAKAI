# Phase 1 详细实施设计 — 洞 ②⑥ 单请求内重试 / failover / 跨池(Claude 独立稿)

- 日期: 2026-05-21
- 范围: 方向 1 Phase 1 —— 洞 ② 单请求内重试 + 账号 failover、洞 ⑥ 跨池多候选编排
- 平行纪律: 按 CLAUDE.md #10 独立起草, 未读 codex 平行稿(`2026-05-21-phase1-design-codex.md`)
- 输入: `2026-05-21-direction-1.md` §2.2/§4.5/§7、`2026-05-21-account-to-api-gap-analysis.md` §4

## 0. 核心判断 —— 比 gap 分析乐观

读源码后发现 Phase 1 **不是从零设计, 而是完成代码早已预留的 L0→real 过渡**:

- 三层架构(Router / Pool / **Executor**)是既定设计 —— `router/router.go:5-13` 注释明写「Gateway Executor — runs the per-attempt loop (claim, forward, settle)」。Executor 这一层只是**还没建**。
- 多 attempt 数据模型**已就位** —— `router/route_plan.go:52-71`: `RoutePlan{Attempts []AttemptPlan, AttemptBudget, RetryableEndClasses}` 全有; `default_router.go:7-10` 注释直说「Slice 5 replaces it with a real planner that enumerates fallback candidates」。
- 账务层**已是 attempt-aware** —— `billing/claim_gate.go:101-119` 的 `ReReserveAbortedClaim` 会重置 claim 状态并 **bump attempt_seq**; `billing/state.go:24-29` 有 `Attempt`/`StreamState` 账务状态机; `attempt_seq` 列遍布 `db/billing/*`。
- 请求体**已全缓冲** —— `chat_completions_handler.go:85` 的 `chatExecution.body []byte` 在路由前已读满, 重放 = 直接复用, 不需要额外缓冲逻辑。

**含义**: Phase 1 三件事 —— ① 让 Router 枚举多候选(数据模型零改动)② 建 Executor 重试循环 ③ attempt_seq 去硬编码。**大概率无 DB schema 迁移**(attempt_seq 列已存在)。

## 1. 现状勘察(file:line)

| 模块 | 现状 |
|---|---|
| `router/router.go:25-27` | `Router.Plan(ctx, PlanInput) (RoutePlan, error)` 接口; 三层边界清晰 |
| `router/default_router.go:35-77` | `DefaultRouter.Plan` 只取 `PoolCandidates[0]` → 1 个 `AttemptPlan`, `AttemptBudget:1`, `RetryableEndClasses:nil` |
| `gatewayhttp/chat_completions_handler.go:116-197` | handler **线性单 attempt**: `prepareRoute → reserveClaim → prepareClaimAndAccount → resolveCredential → dispatch`; `chatExecution.attempt` 是单数 |
| `chat_completions_handler.go:230-298` | `dispatchRawBuffered` 任何错误 → `Settler.Abort` + `writeNormalizedUpstreamError` + return; 无重试、无 failover |
| `AttemptSeq:1` 硬编码 4 处 | `chat_completions_handler_headers.go:217`、`chat_completions_dispatch.go:170`、`chat_completions_stream.go:444`、`chat_completions_billing.go:94` |
| `billing/claim_gate.go:71-173` | `Reserve` 跑 Tx1(Serializable, idempotency fingerprint 8 字段); 命中 `aborted` claim → `ReReserveAbortedClaim` bump attempt_seq |
| `billing/state.go:61-63` | `Attempt.Chargeable()` 仅 `Partial` 可计费; `AmbiguousUsage` → `Failed` 不正向收费 |

## 2. 洞 ⑥ — 跨池多候选编排

- **做法**: 替换 `DefaultRouter` 为真 planner(`router/default_router.go` 内, 接口不变), 把 `ResolvedModel.PoolCandidates` **全部**枚举成 `RoutePlan.Attempts`(不再只取 `[0]`)。
- 每个候选 → 一个 `AttemptPlan{Index, PoolGroupID, RequiredCapabilities, Reason}`; `Reason` 填 `"primary"` / `"fallback_pool_2"` 等。
- `AttemptBudget = min(len(Attempts), 配置上限)`, 默认上限 **2**(可配 env, streaming 默认更保守见 §7)。
- `RetryableEndClasses` 填入可重试错误类(见 §5)。
- **数据模型零改动** —— `RoutePlan`/`AttemptPlan` 结构现成。改动收敛在 `internal/router/` + `cmd/gateway/wiring.go`(换 `NewDefaultRouter()`)。
- ranking 初版保守: 就按 Registry 给的 `PoolCandidates` 顺序(它已按 binding priority 排好), 不引复杂成本模型。健康/容量过滤可后续迭代。
- **此洞不碰 billing, 低风险** —— 适合作 Phase 1 的第一个 PR 单独落。

## 3. 洞 ② — Executor 重试循环

### 3.1 attempt 执行函数抽取

把 handler 里「一次出站尝试」的步骤(pool slot acquire + 凭据解析 + dispatch)抽成一个可重复调用的函数:

```
func (ex *chatExecution) runAttempt(ctx, ap router.AttemptPlan, seq int) attemptResult

type attemptResult struct {
    ok           bool                 // 成功
    delivered    bool                 // 是否已向客户端写出首字节(流已开始)
    err          classifiedError      // 失败时的分类错误
    usage        ...                  // 成功时的 usage(交给结算)
    accountID    int64                // 本次用的账号(健康信号 / 失败排除)
}
```

`reserveClaim`(ClaimGate.Reserve)**留在循环外**, 一个 client request 只 Reserve 一次。`runAttempt` 内做: `Selector` 选号 + slot acquire → `resolveCredential` → dispatch。

### 3.2 循环结构

```
reserveClaim()                      // 一次
// ex.body 已是 []byte, 天然可重放, 无需额外缓冲
failed := map[int64]struct{}{}      // 已失败账号
for i, ap := range plan.Attempts {
    if i >= plan.AttemptBudget { break }
    res := ex.runAttempt(ctx, ap, seq=i+1)   // seq 替换 4 处硬编码 :1
    if res.ok { settleOnce(res); return }
    if res.delivered { finalizeAsPartialOrFailed(res); return }  // 流已开始 → 禁 failover
    if !retryable(res.err) { break }
    releaseAttemptSlot(); recordHealthSignal(res.accountID, res.err)
    failed[res.accountID] = struct{}{}
}
abortOnce(); writeNormalizedUpstreamError()   // 全部失败 → 一次 abort
```

`attemptSeq = i+1` —— 这是去掉 4 处 `AttemptSeq:1` 硬编码的唯一来源。

## 4. 请求体缓冲

**基本免费**: `chatExecution.body` 已是 `[]byte`(`chat_completions_handler.go:85`), 路由前已读满。重放 = 复用 `ex.body`。大小上限已由 `validateChatCompletionsRequest` + RequestBodyLimit 中间件保证。无需新增缓冲代码、无「超阈值不可重试」分支(根本没有未读流)。

## 5. 错误 taxonomy

复用现有 `gateway.Classify(status, headers, body, platform) → classification.Class`。新增 `retryDecision(class) → {Retry, Terminal}` 映射:

| 错误类 | Phase 1 决策 |
|---|---|
| 连接错误 / `upstream_dispatch_error` | 可重试, failover 换号 |
| 上游 5xx | 可重试, failover |
| 上游 429 | 可重试, failover + 记 cooldown(channelhealth 已有) |
| 上游 401 / 403 | Phase 1: failover 换号(别的账号有自己的 token)。**Phase 2 洞④** 再加「同号刷新 token 后重试」的优化 |
| 客户端 4xx(400/422 等请求本身错) | **不重试**, 直接返回客户端 |
| `canonical_response_error` / adapter 错误 | **不重试**(协议/我方问题, 重试无用), 返回 |

可重试类写入 `RoutePlan.RetryableEndClasses`, 为未来 Rust 错误类(方向 1 §4.5)留同一接口。

## 6. billing / claim 原子性 —— ★ 触及 billing 的改动点清单(要 Owner 确认)

Phase 1 触及计费的改动**逐条列出**, 这些是高风险、要 Owner 单独点头:

1. **一个 client request 只一次 Reserve、只一次终态**(settle 或 abort)。现状每个 `Settler.Abort` 散落在 `dispatchRawBuffered` 各错误分支; Phase 1 把终态收敛到循环出口 —— 成功 attempt `settleOnce`, 全失败 `abortOnce`。**绝不能两次 settle。**
2. **每个失败 attempt 释放自己的 pool slot**。`pool_slot_acquisitions` 有 `attempt_seq` 列。失败 attempt 释放 slot, 但 claim 保持 `reserving` 给下个 attempt; 不是 abort 整个 claim。
3. **`attempt_seq` 去硬编码** —— 4 处 `:1` → 循环 index(1..N)。流向 `usage_records` / `billing_events` / `pool_slot_acquisitions` 的 `attempt_seq`。
4. **claim 跨 attempt 保持 `reserving`** —— 不在 attempt 间 abort+ReReserve(那是跨请求重试的原语, 单请求内不用)。claim 是幂等锚点, attempt 是其下子单元。
5. **无 DB schema 迁移**(待 codex 稿 + 实现核实)—— `attempt_seq` 列已存在; 失败账号集 `failed` 仅 in-request 内存。**若核实发现要持久化失败账号 → 那才是 schema 变更, 单独再报 Owner。**

→ 结论: Phase 1 的 billing 改动是「**收敛终态 + 线程化 attempt_seq + 逐 attempt 释放 slot**」, 不引新表、不改幂等语义。这是相对可控的 billing 改动, 但仍属高风险区, 实现 PR 前把上述 1-5 的具体函数 diff 给 Owner 过目。

## 7. 流式 —— 流开始后禁 failover(硬规则)

- `attemptResult.delivered`: 一旦 `handleStreamingResponse` 向 `w` 写出首字节(或 buffered 路径写出 200 body), `delivered=true`。
- 循环规则: `res.delivered == true` → **绝不进下一 attempt**, 直接 finalize(成功 / partial / failed)并 return。
- buffered 与 streaming 的重试窗口不同: buffered 在 `finalizeBufferedEnvelope` 写 body **之前**失败 → 仍可换 attempt(窗口较宽); streaming 一旦发首 token 即锁定。
- 代码强制: `delivered` 标志由写 `w` 的那一处置位, 循环只读不猜。

## 8. 改哪些文件 / 测试 / 风险 / 估时 / 分 PR

**改哪些**:
- `internal/router/default_router.go`(或新 planner 文件)、`cmd/gateway/wiring.go` —— 洞⑥。
- `internal/gatewayhttp/`: 新 `attempt_executor.go`(循环)、改 `chat_completions_handler.go` / `chat_completions_dispatch.go` / `chat_completions_stream.go` / `chat_completions_billing.go` / `chat_completions_handler_headers.go` —— 洞②。

**测试**:
- router planner 单测: 多 `PoolCandidates` → 多 `AttemptPlan`; budget 截断。
- handler 集成: ❶ attempt 1 连接失败 → attempt 2 成功 → 只结算一次 ❷ 全失败 → 只一次 abort ❸ 流已发首字节后上游断 → 不 failover、记 partial ❹ 客户端 4xx → 不重试 ❺ 失败 attempt 的 pool slot 被释放(无泄漏)❻ 幂等: 同 request 不写多份终态。

**风险**:
- billing 双结算 —— 缓解: 终态只在循环出口一次。
- pool slot 泄漏 —— 缓解: 失败 attempt 显式释放 + 测试 ❺。
- handler 重构 blast radius —— 缓解: 洞⑥ 先单独 PR(不碰 billing), 洞② 的 executor 抽取配齐回归测试。

**估时**: 洞⑥ ~3-4 天; 洞② ~5-7 天。

**分 PR**:
- PR1: 洞⑥ router 多候选 planner(零 billing, 低风险, 先落)。
- PR2: 洞② executor 循环 + attempt 函数抽取 + attempt_seq 线程化(**触 billing — Owner 确认门**)。
- PR3(可并入 PR2): 逐 attempt 健康信号 + 失败账号排除。

---
本稿 lane: planner/architect —— Claude 读 HUAKAI 自有 Go 源码起草, 未读外部参考项目源码、未读 codex 平行稿。读过: `router/{router,default_router,route_plan}.go`、`gatewayhttp/chat_completions_handler.go`、`billing/{claim_gate,state}.go`、`router`/`billing`/`pool`/`gateway` 目录清单。agent: Claude (claude-opus-4-7)。UTC 2026-05-21。
