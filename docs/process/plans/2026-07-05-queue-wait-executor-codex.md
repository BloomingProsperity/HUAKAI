# 2026-07-05 queue_wait 排队执行层实现计划（Codex 独立轨）

| Owner directive | 「独立起草『queue_wait 排队执行层』实现计划(只写计划,不写代码)」 |
| Scope | 只写计划文档；复核 HUAKAI 当前源码；设计 `/v1/chat/completions` 现有 dispatch 路径上的 WaitPlan 执行层；不改生产代码、不改 SQL、不提交。 |
| Out of scope | 本计划不读取 Claude 轨计划；不实现代码；不改变 billing/schema/quota/auth 核心；不重新挖外部参考项目源码。题干给定的产品语义视为 Owner 已拍板输入，实现方案按 HUAKAI 当前接口独立设计。 |
| Success criteria | 计划能指导后续小切片实现：满池时请求按 WaitPlan 有界等待槽位；超过 MaxWaiting 立即 429；等待超时 429；成功等待后真实拿到 pool slot 并继续现有 dispatch；失败仍保持现行 retryable queue_wait 分类、Retry-After、claim abort 与 hold/quota 释放语义。 |
| Time estimate | 计划：本次完成。后续实现预估 0.5-1 天；§17 e2e 与变异验证另需 0.5-1 天，取决于本地 DB/e2e 环境。 |
| Blast radius | 热路径 `/v1/chat/completions` 的选号、claim/hold/quota 生命周期、slot acquire/release、retry/model-fallback 延迟；错误会表现为超并发、hold 泄漏、429 语义漂移或请求长尾增加。 |
| Failure modes | 计数器泄漏、等待者跨 attempt 重复排队、等待期间客户端断连未释放 hold、等待成功绕过 claim-slot writeback、等待超时改变 retryable end class、退避过密打爆 DB、横向多实例下 MaxWaiting 只做到单进程上限。 |
| Decision points | 见「需要 Owner 确认」。当前源码中 slot lease 为 90s、claim lease 为 30min；题干提到 120s，后续若要改常量或 schema，需单独确认。 |
| Pre-execution checklist | 后续实现前先跑现有相关单测；确认 `codex` review 流程；实现只加小文件和小 hook；不在 `chat_completions_dispatch.go` 继续堆大段逻辑；先写单测红点，再改实现，再跑 e2e。 |

## 独立性声明

- 我只看到了 `docs/process/plans/2026-07-05-queue-wait-executor-claude.md` 的文件名，未打开、未读取其内容。
- 本计划只基于题干和我亲读的 HUAKAI 源码/测试。没有读取外部参考项目源码，因此不在本文复述外部实现细节。

## 源码复核

1. `pool_groups` 已有 sticky/fallback 等待预算列：`sticky_wait_max_waiting`、`fallback_wait_max_waiting`、`sticky_wait_timeout_ms`、`fallback_wait_timeout_ms`，默认分别是 2/8 和 5000/30000ms（`sql/migrations/0001_pool_routing.up.sql:67`）。
2. 生产 `DBAccountSource` 把 `provider_accounts.cap_queue_fallback` 映射为 `AccountSnapshot.MaxWaiting`，但当前把 `WaitTimeoutMS` 留为 0，依赖 routing policy 覆盖 timeout（`internal/pool/dispatcher/account_source.go:62`、`internal/pool/dispatcher/account_source.go:70`）。
3. `DefaultSelector.Select` 先走 sticky/routed/fresh 三层；fresh 也拿不到 slot 时，才调用 `fallbackPlan` 返回 `SelectionResult{WaitPlan: plan}`（`internal/pool/router/default_selector.go:144`、`internal/pool/router/default_selector.go:163`）。
4. `fallbackPlan` 取 fresh 排序后的第一个候选账号，把 policy 的 `FallbackTimeoutMS/FallbackMaxWaiting` 覆盖到账号级预算，最后产出 `WaitPlan{AccountID, MaxConcurrency, TimeoutMS, MaxWaiting}`（`internal/pool/router/default_selector.go:344`）。
5. `WaitPlan` 类型只有四个字段，说明它现在只是 admission 计划，没有执行状态（`internal/pool/router/types.go:133`）。
6. `selectPoolAccount` 当前拿到 `selRes.WaitPlan` 后立即 `abortReservation(..., "queue_wait", ...)`，构造 retryable 429，并只用 `TimeoutMS` 计算 `Retry-After`（`internal/gatewayhttp/chat_completions_dispatch.go:497`、`internal/gatewayhttp/chat_completions_dispatch.go:532`）。
7. claim/hold/quota reservation 发生在 pool selection 之前：`prepareClaimAndAccount` 先 `reserveClaim`，再 `selectPoolAccount`（`internal/gatewayhttp/chat_completions_dispatch.go:313`）。因此等待期间如果保持现结构，就会保持 claim 处于 reserving、hold/quota 处于预留状态。
8. abort 已经脱离请求 ctx，用 `context.WithoutCancel` + 30s 超时释放 hold 与并发槽，适合等待超时和客户端断连路径复用（`internal/gatewayhttp/chat_completions_attempt.go:186`、`internal/gatewayhttp/chat_completions_attempt.go:197`）。
9. retry gate 只看失败是否已交付、body 是否可重放、`RetryableEndClasses` 是否允许该 end class；`queue_wait` 被归类为 `UpstreamRateLimit`（`internal/gatewayhttp/chat_completions_attempt.go:273`、`internal/gatewayhttp/chat_completions_attempt.go:220`）。后续实现不应改这里。
10. `SlotManager.Acquire` 成功时会递增 `provider_accounts.in_flight_count`、插入 `pool_slot_acquisitions`，并通过 `ClaimGate.WriteAcquisition` 写回 claim；这条路径不能绕过（`internal/pool/router/default_selector.go:217`、`internal/pool/dispatcher/slot_manager.go:76`、`internal/pool/router/default_selector.go:229`）。
11. 当前 slot lease 是 90s（`internal/pool/dispatcher/slot_manager.go:29`），claim lease 当前是 30min，且注释明确要求覆盖最大 600s 流式请求生命周期（`internal/billing/claim_gate.go:42`、`internal/billing/claim_gate.go:52`）。题干中的 120s 约束与当前代码不一致；实现时至少不得让等待预算逼近任何活跃 lease 窗口。
12. 现有 e2e `TestAccountSlotConcurrencyE2E_NoCapacityAndRelease` 当前断言满槽 overflow 都返回 429/queue_wait（`cmd/gateway/account_slot_concurrency_e2e_test.go:89`、`cmd/gateway/account_slot_concurrency_e2e_test.go:102`），并已有成功/拒绝 money-path 与无泄漏断言（`cmd/gateway/account_slot_concurrency_e2e_test.go:383`、`cmd/gateway/account_slot_concurrency_e2e_test.go:402`、`cmd/gateway/account_slot_concurrency_e2e_test.go:501`）。

## 设计主张

排队循环放在 dispatch 层调用的新执行组件中，不放进 selector 本体。selector 继续保持「一次 Select = 一次即时 admission 尝试」；等待执行层负责 HTTP request ctx、deadline、MaxWaiting admission、Retry-After、abort 与测试可控 sleep。这样不会把 router 包变成阻塞式 HTTP 生命周期管理器，也能保留当前 selector 的原子 slot acquire + claim writeback。

建议新增小包 `internal/pool/queuewait`，由 `gatewayhttp` 调用：

- `queuewait.Executor`：实现等待循环，输入 `pool.Selector`、原始 `pool.SelectionRequest`、`pool.WaitPlan`。
- `queuewait.Tracker`：进程内等待计数器，按 `{tenant_id, pool_group_id, account_id}` 计数。
- `queuewait.Result`：区分 `acquired`、`overflow`、`timeout`、`cancelled`、`selector_error`。

生产装配在 `cmd/gateway` 创建一个共享 `queuewait.Executor` 并注入 `gatewayhttp.ChatHandlerDeps`。`gatewayhttp` 只做结果映射：成功则填 `ex.selRes/ex.acquiredAccountID/ex.acquisitionToken`；失败则沿用现有 `queue_wait` retryable 429 构造和 `abortReservation`。

## 钉住账号还是重跑完整选号

等待期间应钉住 `WaitPlan.AccountID` 抢槽，而不是每轮重跑完整选号。

理由：

- `WaitPlan.AccountID` 已经是 fresh 排序后的首选等待目标（`internal/pool/router/default_selector.go:348`），`MaxWaiting` 也是围绕这个账号容量表达的队列上限。
- 重跑完整选号会让等待者在多个账号之间漂移，导致每账号 MaxWaiting 难以解释，也可能让等待者绕过前一次排队顺序。
- `SelectionRequest.PinnedAccountID` 已有 gate，能在 selector 内排除非目标账号（`internal/pool/router/types.go:66`、`internal/pool/router/gates.go:351`）。等待执行层每轮用同一请求加 `PinnedAccountID=WaitPlan.AccountID` 调 `Selector.Select`，仍走完整 gate、slot acquire、claim writeback。
- 如果目标账号在等待期间被健康/策略 gate 排除，selector 会返回非 WaitPlan 的 no-capacity/error，执行层停止等待并交回现有错误映射，而不是盲等坏账号。

伪流程：

```text
selectPoolAccount()
  req := buildSelectionRequest(...)
  selRes := Selector.Select(ctx, req)
  if selRes.WaitPlan == nil:
      维持现有成功/无账号/错误路径

  result := QueueWaiter.Wait(ctx, Selector, req, selRes.WaitPlan)
  switch result:
    acquired:
      使用 result.SelectionResult 继续 resolveCredential/dispatch
    overflow:
      abort claim, retryable 429 queue_wait, Retry-After=ceil(plan.TimeoutMS/1000)
    timeout:
      abort claim, retryable 429 queue_wait, Retry-After=ceil(plan.TimeoutMS/1000)
    cancelled:
      defer 归还 waiting gauge；abort claim 释放 hold/quota；响应是否写出取决于连接状态
    selector_error:
      走现有 classifyPoolSelectFailure 或 no-account fallback，不吞掉真实配置/DB/健康错误
```

## MaxWaiting 计数器

v1 用进程内计数器，不落 DB、不加 schema。

- key：`tenant_id + pool_group_id + account_id`，避免不同租户/池/账号互相挤占。
- admission：进入等待前在 mutex 下读取当前值 `n`；如果 `n+1 > plan.MaxWaiting`，不入队，立即返回 overflow；否则自增并返回 `release()`。
- 释放：等待函数成功、超时、selector error、ctx cancel 任一路径都 `defer release()`；计数降到 0 时删除 map key，避免长期 churn。
- `MaxWaiting <= 0`：按 0 个等待位处理，即第一名等待者也 overflow。理由是字段语义是最大等待数，不能把 0 解释成无限等待。
- 可观测性：实现时给 `Tracker.Depth(key)` 测试钩子；是否再接 expvar/Prometheus 可作为低风险增强，但不能替代单测断言。

风险说明：多进程/多副本部署下，进程内计数只能保证单进程不超过 MaxWaiting，集群总等待数可能是 `MaxWaiting * replicas`。本计划认为 v1 可接受，因为加 DB 队列表/ advisory lock 属于高爆炸半径 schema/一致性变更；若 SaaS 多副本要求全局严格上限，应另开计划。

## Billing Claim/Reservation 生命周期

保持当前顺序：先 reserve claim/hold/quota，再等待 pool slot。等待期间不占 pool slot，只持有 claim/hold/quota 预留。

- 等待成功：通过 selector 的真实 `Acquire` 获得 slot，claim 写回 acquisition token，后续 settlement/release 继续走现有路径。
- overflow/timeout：调用 `abortReservation(claimID, "queue_wait", 0, protocolLoss)`，释放 hold/quota；响应为 retryable 429 queue_wait。
- 客户端断连：等待 ctx 退出，`Tracker` 必须 defer 归还；仍调用 detached abort，不能因请求 ctx canceled 导致 hold/quota 泄漏。
- lease 约束：等待 deadline 必须来自 `WaitPlan.TimeoutMS`，并加实现侧上限保护。当前默认 fallback 30s，小于 slot 90s 与 claim 30min；如果后续 Owner 确认存在 120s lease 目标，也仍有足够余量。
- 不在等待期发 SSE ping：等待发生在上游 dispatch 前，stream 请求若最终超时仍是预交付 JSON 错误；发 SSE ping 会破坏现有「未交付前可 retry/JSON 错误」语义。

## Attempt/Model-Fallback 交互

等待失败继续生成现有 `retryableLocalAttemptFailure(429, queue_wait, ..., UpstreamRateLimit)`，不改 `shouldRetryAttemptFailure`，也不改 `RetryableEndClasses` 和 model-fallback 分类。

这意味着一个 HTTP 请求在现有 attempt budget 内，等待超时后仍可能进入下一 attempt。这是题干要求保留的语义。实现只保证每个失败 attempt 都正确 abort，同一幂等 claim/hold 不重复退款；不在 queue_wait 执行层私自抑制 retry。

## 退避参数

建议：

- 第 0 轮先立即尝试一次 pinned selector，不睡眠，避免槽刚释放时多等一个 tick。
- 初始 sleep：100ms。
- 指数：每次乘 2。
- 上限：2s。
- jitter：±20%，避免同一进程内等待者整齐打 DB。
- 最后一轮 sleep 取 `min(backoff, deadline-now)`，deadline 到即 timeout。

这个区间足够短，能覆盖 800ms 左右 mock holder 释放；又不会在 30s fallback 窗口内形成毫秒级 DB 轮询风暴。

## 文件划分

遵守 codebudget，不往已 789 行的 `internal/gatewayhttp/chat_completions_dispatch.go` 继续塞大段逻辑。

建议后续实现文件：

- `internal/pool/queuewait/executor.go`：等待执行器、退避、结果类型，目标 < 300 行。
- `internal/pool/queuewait/tracker.go`：MaxWaiting 计数器，目标 < 160 行。
- `internal/pool/queuewait/executor_test.go`：纯单元测试，含 fake selector/fake sleeper/fake clock。
- `internal/gatewayhttp/chat_completions_queue_wait.go`：把 queuewait result 映射成 `classifiedAttemptFailure` 或 selection success，目标 < 220 行。
- `internal/gatewayhttp/chat_completions_queue_wait_test.go`：handler/dispatch 层单测，替代或扩展当前 `TestHandler_WaitPlanReturnsQueueWait`。
- `cmd/gateway/account_slot_queue_wait_e2e_test.go` 或扩展既有 `account_slot_concurrency_e2e_test.go`：全链路并发 e2e。若扩展既有文件超过预算，可新建同 build tag 文件。

`chat_completions_dispatch.go` 只做两类小改动：

- 抽一个 `buildPoolSelectionRequest(in)`，避免等待时重复手拼请求。
- 将当前 WaitPlan 立即 abort 分支替换为调用 helper。

## 具体执行顺序

1. 写 `queuewait.Tracker` 单测：允许到 MaxWaiting、超过立即拒绝、release 后可再进、ctx cancel 路径 defer 释放。
2. 写 `queuewait.Executor` 单测：立即成功、先 WaitPlan 后成功、overflow 不调用 selector、timeout 返回 timeout、selector 非 WaitPlan 错误透出、pinned request 必带目标 account。
3. 在 `gatewayhttp` 增加 `QueueWaiter` 依赖和 helper，更新当前 WaitPlan 单测：旧测试从“立刻 429”改为“MaxWaiting=0 立即 429 且 Retry-After 不变”；新增“等待后 selector 成功则不 abort”单测。
4. 接 `cmd/gateway` 生产装配：共享一个 queuewait executor/tracker 注入 `chatHandlerDeps`。
5. 改 e2e：把旧 overflow 全拒绝场景拆成等待成功/溢出/超时/断连/无泄漏场景。
6. 跑单测和 e2e；再按 AGENTS 规则 stage 目标 diff 并跑 `codex exec review --uncommitted --full-auto --sandbox read-only`。

## §17 重测计划

### 单元测试

- `queuewait.Tracker_AllowsUpToMaxWaiting`：MaxWaiting=2 时第 1/2 个进入，第 3 个 overflow；释放一个后第 3 个可进入。
- `queuewait.Tracker_ReleasesOnEveryExit`：success/timeout/error/cancel 四条路径后 depth 回 0。
- `queuewait.Executor_PinsWaitPlanAccount`：fake selector 断言每轮 `PinnedAccountID == WaitPlan.AccountID`。
- `queuewait.Executor_SucceedsAfterSlotRelease`：fake selector 前两次返回同账号 WaitPlan，第三次返回 `AccountID/AcquisitionToken`，结果为 acquired。
- `queuewait.Executor_TimeoutKeepsQueueWaitOutcome`：fake clock 推进到 deadline，返回 timeout，不产出 selection。
- `queuewait.Executor_SelectorErrorDoesNotBecomeQueueWait`：DB/配置错误原样透出，防止隐藏真实故障。
- `gatewayhttp.WaitPlanOverflow_AbortQueueWaitRetryAfter`：MaxWaiting=0 立即 429，`Retry-After` 仍等于 `retryAfterSecondsForWaitPlan`。
- `gatewayhttp.WaitPlanSuccess_DoesNotAbortClaim`：等待后成功拿号时 `Settler.Abort` 0 次，`resolveCredential/dispatch` 后正常 commit。
- `gatewayhttp.WaitPlanCancel_AbortDetachedAndGaugeReturned`：请求 ctx cancel 后 tracker depth 回 0，settler abort 被调用。

### 全链路 e2e

基于 `cmd/gateway/account_slot_concurrency_e2e_test.go` 的现有结构：

1. **等待者槽释放后真成功**：cap=3，启动 3 个 holder 打满；再发 1 个 waiter，配置 `fallback_wait_timeout_ms` 足够大、`cap_queue_fallback>=1`；holder 释放后 waiter 应 200，PG 中有 committed claim、usage_records=1、pool_slot_acquisitions released_success=1。
2. **溢出立即拒**：cap 满时并发发 `MaxWaiting+1` 个 waiter；前 MaxWaiting 个进入等待，第 `MaxWaiting+1` 个应快速 429/queue_wait，且该 rejected claim 无 pool slot row。
3. **超时**：holder 延迟大于 `fallback_wait_timeout_ms`，waiter 最终 429/queue_wait，Retry-After 保留，claim aborted_reason=queue_wait，hold/quota released。
4. **断连 gauge 归还**：waiter 用短 client ctx 或主动断开；服务端应 abort claim，tracker depth 回 0，后续新 waiter 可进入等待。
5. **无槽泄漏**：所有请求结束后 `provider_accounts.in_flight_count=0`、`pool_slot_acquisitions status='acquired'` 计数 0、`balance_holds state='held'` 计数 0、`user_balances.held=0`。
6. **hold 只退一次**：queue_wait 仍 retryable，失败 claim 的 `balance_holds` 只有 1 行且最终 released，`claim_aborted` 审计条数等于 attempt_seq，不允许重复退款。
7. **峰值不越 cap**：采样 `provider_accounts.in_flight_count`，峰值必须等于 cap 且不超过 cap。

### 变异证红点

- 删掉 `defer release()`：断连/timeout 后 tracker depth 非 0，断连 gauge 测试红。
- 把 overflow 条件从 `n+1 > MaxWaiting` 改成 `n > MaxWaiting`：MaxWaiting+1 会被放入队列，溢出测试红。
- 等待轮次不设置 `PinnedAccountID`：fake selector pin 断言红；e2e 可能出现跨账号漂移。
- 等待成功时绕过 selector 直接伪造 `SelectionResult`：PG 中缺 `pool_slot_acquisitions` 或 claim acquisition writeback，成功 money-path 测试红。
- timeout 后不 abort claim：`balance_holds held` 或 quota reservation acquired/released 断言红。
- ctx cancel 使用请求 ctx 调 abort，而不是 detached abort：断连场景 hold/quota 泄漏测试红。
- 改 queue_wait end class 或 failure code：retry/model-fallback 相关单测红，`Retry-After`/body 断言红。
- 去掉 jitter/退避上限较难稳定变异；用 fake sleeper 断言 sleep 序列为 100ms、200ms、400ms、800ms、1600ms、2s cap。

## 风险与爆炸半径

- **Money-path 风险高**：等待失败必须释放 claim/hold/quota；等待成功必须通过真实 slot acquire 写回 claim。任何绕路都会引入漏钱或超并发。
- **延迟风险中**：queue_wait 从立即 429 变成最长等待 `TimeoutMS`，再叠加现有 attempt retry，用户可见延迟可能上升。需靠 e2e 和日志确认。
- **DB 压力中**：等待轮询会重复调用 selector，涉及账号列表、gate、slot acquire 事务。退避+jitter 是必须项。
- **多实例一致性中**：进程内 MaxWaiting 不是全局队列。v1 先保守实现，若 SaaS 多副本要求全局严格上限，再设计 DB/advisory lock。
- **代码体量风险中**：`chat_completions_dispatch.go` 已超过单文件预算，后续实现必须新文件承载逻辑。
- **Clean-room 风险低**：实现依据 HUAKAI 当前接口与 Owner 给定产品语义，不复制外部源码结构/标识符/注释。

## 需要 Owner 确认

1. 题干提到「lease TTL 120s」，当前源码读到 slot lease=90s、claim lease=30min。后续实现是否只按现源码保护，还是另开任务调整 lease 常量。
2. v1 MaxWaiting 是否接受进程内上限；如果要求跨副本全局严格上限，需要 DB/schema 设计，属于高风险另开计划。
3. 本切片是否只覆盖 chat/messages 共用的 `gatewayhttp` chat handler；embeddings/completions/image/audio/rerank 也注入 selector，但未在本任务指定源码范围内。建议先把 queuewait executor 做成可复用包，chat 先落地，后续端点按同模式补齐。

## 下一步建议

Owner 批准后，按「先单测红点、后实现、再 e2e」执行。第一批实现只做 queuewait 执行器、chat dispatch hook、cmd/gateway 注入和现有 account slot e2e 改造；不动 SQL、不改 retry gate、不改 billing/settlement 核心。
