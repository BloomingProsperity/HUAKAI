# 2026-05-21 Phase 1 详细实施设计 - Codex 独立稿

> 【2026-06-02 已更新】本 Phase 1 设计稿中的 L0 router / `AttemptSeq:1`
> 状态是 2026-05-21 历史；当前 `router.DefaultRouter` 已生成多 attempt plan，
> `chatExecution` 已执行 attempt loop、失败账号排除、交付前 retry/failover 与一次最终结算。
> 以下为历史设计稿。

| Owner directive | "HUAKAI 方向 1 已锁定并进入执行,Owner 批准开 Phase 1。现在要 Phase 1 的详细实施设计。" |
|---|---|
| 独立性声明 | 本稿未打开、显示、读取或引用 `docs/process/plans/2026-05-21-phase1-design-claude.md` 的内容；一次存在性检查返回过该路径存在。 |
| 背景输入 | `docs/process/plans/2026-05-21-direction-1.md`；`docs/process/plans/2026-05-21-account-to-api-gap-analysis.md`。 |
| 外部参考使用方式 | 只引用 gap 分析 §4 已有 reference citations；未重读任何外部参考项目源码。 |
| 范围 | Phase 1: 洞 ② 单请求内重试 + 账号 failover；洞 ⑥ 跨池多候选编排。Rust 不接入执行路径，但错误 taxonomy 为 Rust transport class 预留接口。 |
| 不做 | 不改 schema；不新增 runtime dependency；不接 OAuth 热刷新；不改变 LICENSE；不读 Claude 平行稿。 |

## 1. 背景约束

方向 1 权威稿把 Phase 1 定为 Go P0: 单请求内 retry/failover 与跨池编排，当前现状是 `DefaultRouter` 只取第一个 pool candidate、handler 写死 `AttemptSeq: 1`、上游失败直接返回客户端；设计要求预读请求体、router 产多候选、handler 只在交付前 retry，且 ClaimGate 幂等键跨 attempt 稳定、一个 client request 只一次最终结算。见 `docs/process/plans/2026-05-21-direction-1.md:47-53`。

同一权威稿要求错误 taxonomy 未来能消费 Rust transport sidecar 的错误分类，例如 `connect_timeout`、`tls_handshake_failed`、`upstream_429`、`upstream_401_403` 等。Phase 1 暂不接 Rust，但 Go taxonomy 必须能承接这些类别。见 `docs/process/plans/2026-05-21-direction-1.md:174-187`。

风险门槛明确: retry/failover 双计费/错计费是高风险，洞②触及 billing/quota/claim 的具体改动必须执行前单独报 Owner 确认。见 `docs/process/plans/2026-05-21-direction-1.md:221-233`。

gap 分析确认 HUAKAI Go 生产管线目前池内选号强、跨池 Router 是 L0 桩，错误重试与账号 failover 缺失；参考项目证据只作为行为证据使用，单请求内 failover 引证已在 gap §4 列出。见 `docs/process/plans/2026-05-21-account-to-api-gap-analysis.md:24-34` 和 `docs/process/plans/2026-05-21-account-to-api-gap-analysis.md:60-82`。

## 2. 当前 Go 代码勘察小结

### 2.1 `backend/internal/gatewayhttp`

`ChatHandlerDeps` 已把 Phase 1 所需的大部分依赖注入到 handler: `Registry`、`Router`、`ClaimGate`、`Selector`、`CredentialVault`、`Dispatcher`、`Forwarder`、`Settler`、`ReplayStore`、`ChannelHealth`。见 `backend/internal/gatewayhttp/chat_completions_handler.go:32-58`。

主 handler 当前执行顺序是: auth resolve -> validate/request body read -> `newChatExecution` -> `prepareRoute` -> `reserveClaim` -> 非流式 L2 cache -> `prepareClaimAndAccount` -> `resolveCredential` -> non-stream 或 stream 分支。见 `backend/internal/gatewayhttp/chat_completions_handler.go:116-196`。

请求体已经在入口一次性读取并存入 `chatValidatedRequest.Body`；当前硬上限是 `http.MaxBytesReader(..., 1<<20)`，之后 `io.ReadAll`。这已经满足“已接受请求可重放”的基础条件，但还没有“超 retry 阈值但仍接受”的策略。见 `backend/internal/gatewayhttp/chat_completions_validate.go:40-84`。

`prepareRoute` 通过 registry resolve 后调用 router，并把 `router.RoutePlan` 存到 `ex.plan`，但只取 `plan.Attempts[0]` 写入 `ex.attempt`。见 `backend/internal/gatewayhttp/chat_completions_dispatch.go:40-99`。

`reserveClaim` 在上游 attempt 之前执行，`PoolingGroupID` 取当前 `ex.attempt.PoolGroupID`，并把 `Idempotency-Key` 或新 UUID 作为 `LogicalRequestID`。见 `backend/internal/gatewayhttp/chat_completions_dispatch.go:109-157`。

`selectPoolAccount` 调用 `pool.Selector.Select`，当前硬编码 `AttemptSeq: 1`，且没有把跨 attempt 的失败账号集合传入 `ExcludedAccounts`。见 `backend/internal/gatewayhttp/chat_completions_dispatch.go:159-174`。

`resolveCredential` 在账号选定后取凭据，并构造 `gateway.ForwardRequest`，其中 `PoolID`、`ProtocolFamily`、`RoutingReasonPayload`、`SessionHash` 已经能承载最终 attempt 的审计信息。见 `backend/internal/gatewayhttp/chat_completions_dispatch.go:224-258`。

非流式成功路径由 `handleNonStreamingResponse` 调 `dispatchBufferedEnvelope`，生成 audit ledger、转回 client body、计算 cost、调用 `settleCompletion`，最后才写 `200`。这天然适合“先执行 attempt，成功后一次性写客户端”。见 `backend/internal/gatewayhttp/chat_completions_billing.go:24-83`。

非流式 settle request 也硬编码 `AttemptSeq: 1`。见 `backend/internal/gatewayhttp/chat_completions_billing.go:85-104`。

流式路径先 dispatch，再在 2xx 后调用 `forwardSSEAndSettle`；上游非 2xx 目前会 `Abort` 并直接写 normalized error。见 `backend/internal/gatewayhttp/chat_completions_stream.go:87-145`。

`forwardSSEAndSettle` 设置 SSE headers 后把 `ResponseWriter` 交给 `StreamForwarder.Forward`；是否真的向客户端交付，要看底层 `Write` 是否发生。当前函数内部完成 settle/abort，不把“已交付/未交付、可重试错误、usage draft”返回给外层 loop。见 `backend/internal/gatewayhttp/chat_completions_stream.go:147-215`。

流式 settle request 同样硬编码 `AttemptSeq: 1`。见 `backend/internal/gatewayhttp/chat_completions_stream.go:417-455`。

idempotency replay 已存在: 成功响应会 best-effort 记录响应体，重放时通过 `ClaimGate` 返回的 `ClaimID` 查 `ReplayStore`，与路由无关。见 `backend/internal/gatewayhttp/chat_completions_idempotency_replay.go:21-50` 和 `backend/internal/gatewayhttp/chat_completions_idempotency_replay.go:65-105`。

### 2.2 `backend/internal/router`

router 包边界很清楚: Router 只产计划，不读凭据、不写 DB；调用流写明是 Auth -> Registry -> Router.Plan -> Executor loop -> Pool.Claim。见 `backend/internal/router/router.go:1-15`。

`RoutePlan` 已经有多 attempt 形态: `Attempts []AttemptPlan`、`AttemptBudget`、`RetryableEndClasses`、`SnapshotVersion`。见 `backend/internal/router/route_plan.go:49-70`。

`AttemptPlan` 已有 `Index`、`PoolGroupID`、`RequiredCapabilities`、`MaxConcurrencyHint`、`Reason`。这足够表达 Phase 1 的跨池计划；若要精确支持 binding weight / fallback class，需要补充轻量 metadata 字段。见 `backend/internal/router/route_plan.go:73-102`。

`DefaultRouter` 当前是 L0: 校验 request/model 后只读取 `PoolCandidates[0]`，输出一个 attempt，`AttemptBudget: 1`。见 `backend/internal/router/default_router.go:7-24` 和 `backend/internal/router/default_router.go:63-76`。

现有 router 单测也把“只使用 primary candidate、AttemptBudget=1”固定为当前行为。见 `backend/internal/router/router_test.go:71-104`。

### 2.3 `backend/internal/pool`

`pool.SelectionRequest` 已经包含 Phase 1 所需的 `ExcludedAccounts map[int64]struct{}`、`AttemptSeq int`、`ClaimID int64`、`Vendor string`。见 `backend/internal/pool/router/types.go:24-42`。

`DefaultSelector.Select` 先取账号、取 policy、过 gates，再按 sticky、route affinity、fresh ranking、fallback wait plan 分层选择账号。见 `backend/internal/pool/router/default_selector.go:60-115`。

`ExclusionGate` 已经会拒绝 `SelectionRequest.ExcludedAccounts` 里的账号，Phase 1 handler 只需要维护失败账号集合并传入。见 `backend/internal/pool/router/gates.go:142-150`。

selector 在成功拿到 slot 后，如果 `ClaimID != 0`，会通过 `ClaimGate.WriteAcquisition` 把 `provider_account_id` 与 `acquisition_token` 写回 claim；失败会 release slot。见 `backend/internal/pool/router/default_selector.go:167-192`。

DB slot manager 的 `Acquire` 会在同一 Serializable tx 中增加 `provider_accounts.in_flight_count` 并插入 `pool_slot_acquisitions`，其中 `AttemptSeq` 来自 `SelectionRequest.AttemptSeq`。见 `backend/internal/pool/dispatcher/slot_manager.go:50-120`。

启动 wiring 中 default selector 已注入 health gate、DB slot manager、DB claim gate、sticky store；PASR 模式也用相同 slot/claim 组件。见 `backend/cmd/gateway/selector_wiring.go:61-72` 和 `backend/cmd/gateway/selector_wiring.go:102-116`。

### 2.4 `backend/internal/billing`

`ClaimGate.Reserve` 是 Tx1，`Settler.Settle` / `Abort` / `CommitCacheHit` 是 Tx2。接口注释明确 Settle 会提交 Usage Record + billing event + claim status flip + in-flight decrement。见 `backend/internal/billing/billing.go:19-50`。

`ReserveRequest` 已包含 `PoolingGroupID`，但 idempotency fingerprint 故意不包含 `PoolingGroupID`；这是跨 attempt / 跨路由重试保持幂等键稳定的基础。见 `backend/internal/billing/billing.go:53-67` 和 `backend/internal/billing/claim_gate.go:175-202`。

`DefaultClaimGate.Reserve` 遇到已有 committed claim 返回 `IdempotencyHit`，已有 reserving claim 返回 `ErrClaimRace`，已有 aborted claim 会 `ReReserveAbortedClaim`，把同一 claim 行重新置为 reserving 并 `attempt_seq + 1`。见 `backend/internal/billing/claim_gate.go:85-120`。

生成的 SQL `ReReserveAbortedClaim` 当前只更新 status、aborted_reason、settled_at、attempt_seq、lease、predicted_cost、reserved_at，不更新 `pooling_group_id`。跨池 retry 若复用这条路径，claim 行会保留第一次 pool id，必须修。见 `backend/internal/db/billing/billing_claims.sql.go:190-230`。

`WriteAcquisitionToken` 当前会把 `provider_account_id` 和 `acquisition_token` 写到 reserving claim 上，但也不更新 `pooling_group_id` 或 attempt-level 路由字段。见 `backend/internal/db/billing/billing_claims.sql.go:232-259`。

`Settler.Settle` 用 `(claim_id, tenant_id, acquisition_token, status='reserving')` 锁 claim；写 usage record 时使用 claim 行上的 `AttemptSeq`，不是 caller 的 `req.AttemptSeq`。见 `backend/internal/billing/settler.go:77-149`。

`Settler.Abort` 会把 reserving claim 置 aborted、插入 `claim_aborted` billing event；若 claim 已有 `provider_account_id` 和 `acquisition_token`，还会写零成本 usage record 并释放 slot/in-flight。见 `backend/internal/billing/settler.go:254-388`。

这说明 Phase 1 可以复用“Abort failed attempt -> Reserve reopens same claim”的现有语义，避免 schema 迁移；但必须 Owner 单独确认，因为一个最终成功请求会留下若干 zero-cost aborted attempt evidence，再以同一 claim commit。

### 2.5 `backend/internal/gateway`

`UpstreamDispatcher.Dispatch` 是一次 HTTP 出站: adapter build request -> transport factory -> optional proxy -> `client.Do` -> 返回 `DispatchResult`。失败时 error 是 string wrap，没有类型化 transport class。见 `backend/internal/gateway/upstream_dispatcher.go:92-153`。

non-streaming HCSF dispatcher 对非 2xx 已返回类型化 `UpstreamHTTPError{StatusCode, Body, Header}`，handler 可据此分类。见 `backend/internal/gateway/upstream_dispatcher_hcsf.go:102-120` 和 `backend/internal/gateway/upstream_http_error.go:9-42`。

现有 error normalizer 已有 `ErrorClass`、`RetryAction`、`FsmTransition`、`RetryAfterMs`；规则覆盖 401、403、429、5xx、network timeout、504、413 等。见 `backend/internal/gateway/error_normalize.go:27-49`、`backend/internal/gateway/error_normalize.go:162-276`、`backend/internal/gateway/error_normalize.go:282-307`。

`ApplyClassificationToDraft` 已能把 normalized error class 映射到 stream end class，供 usage draft/audit 使用。见 `backend/internal/gateway/error_apply.go:29-108`。

`StreamForwarder.Forward` 内部已经跟踪 `firstEmitted`，但没有向 HTTP handler 返回；真正写给客户端发生在 `writeAndFlush`，它调用 `ResponseWriter.Write` 后 flush。见 `backend/internal/gateway/forwarder.go:143-209` 和 `backend/internal/gateway/forwarder.go:567-575`。

### 2.6 `backend/internal/channelhealth`

channel health 状态机有 active/degraded/cooling/ramping/disabled/manual_paused 等状态，默认 policy 覆盖错误率、rate limit、5xx cooldown、ban signal cooldown、ramp。见 `backend/internal/channelhealth/types.go:16-46` 和 `backend/internal/channelhealth/types.go:112-165`。

handler 目前通过 `recordChannelHealthSignal` 将 upstream classification 映射成 rate limit、5xx、timeout、token revoked、account suspended 等 signal。见 `backend/internal/gatewayhttp/chat_completions_error.go:67-126`。

pool health gate 会按 provider account 最新健康状态决定账号是否 eligible；cooling/down/disabled/manual paused 会阻止选择，ramping 按 admission key 采样。见 `backend/internal/channelhealth/failover.go:42-80` 和 `backend/internal/channelhealth/failover.go:92-130`。

### 2.7 `backend/cmd/gateway`

生产 wiring 当前把 `routePlanner` 定义为 `*router.DefaultRouter`，构造时 `router.NewDefaultRouter()`，HTTP route 通过 `chatHandlerDeps` 注入。见 `backend/cmd/gateway/wiring.go:44-85`、`backend/cmd/gateway/wiring.go:194-238`、`backend/cmd/gateway/routes.go:92-114`。

stream forwarder 启动配置已有默认 scanner/protocol adapter 和超时；Phase 1 不需要新增运行时依赖。见 `backend/cmd/gateway/middleware.go:93-107`。

## 3. 目标行为与不变量

1. 一个 inbound client request 解析、鉴权、registry resolve、router plan 只做一次；attempt loop 只在上游出站、pool selection、credential resolve、dispatch/forward/settle 边界内循环。
2. 只要客户端交付未开始，retryable failure 可以进入下一 attempt；一旦 `ResponseWriter.WriteHeader` 或 `Write` 已发生，禁止 failover。
3. `AttemptSeq` 从 1 开始，按实际出站 attempt 递增；pool slot acquisition、claim re-reserve attempt_seq、usage record attempt_seq 必须一致。
4. `Idempotency-Key` / `LogicalRequestID` / payload hash 对整个 client request 稳定；跨 pool retry 不触发 fingerprint conflict。
5. 成功请求只调用一次正向 `settleCompletion`；失败 attempt 可以 zero-cost abort/re-reserve 释放 slot，但不得产生多次实际收费。
6. failed account id 要进入 per-request `ExcludedAccounts`，同一请求后续 pool selection 不再选它。
7. 401/403 在 Phase 1 不盲目扩散到多账号；只记录 refresh intent / account health signal，为 Phase 2 OAuth hot refresh 接口留位。
8. Rust transport class 未来输入 taxonomy 后，应能复用同一个 retry decision，不要求 Phase 1 接 gRPC 或 sidecar。

## 4. 跨池候选生成设计（洞⑥）

### 4.1 Router 类型调整

保留 `router.Router` 接口不变，替换实现为 `router.MultiCandidateRouter`，并让 `NewDefaultRouter()` 在 Phase 1 直接返回兼容配置的 multi-candidate router，减少 wiring 改动面。

建议新增/扩展:

```go
type PoolCandidateMeta struct {
    PoolGroupID   int64
    Priority      int32
    Weight        int32
    SelectionMode string
    FallbackClass string
}

type ResolvedModel struct {
    ...
    PoolCandidates []int64
    PoolMetadata   []PoolCandidateMeta
}
```

`gatewayhttp.prepareRoute` 从 `registry.Resolved.BindingMetadata` 映射到 `router.ResolvedModel.PoolMetadata`。registry 已经暴露 binding metadata，包括 priority、weight、selection_mode、fallback_class。见 `backend/internal/registry/registry.go:68-82` 和 `backend/internal/registry/postgres_registry.go:159-179`。

如果 `PoolMetadata` 为空，router 使用现有 `PoolCandidates` 顺序；这样测试 stub 和旧调用方不需要一次性全改。

### 4.2 Ranking 维度

Phase 1 ranking 保守实现，不引入 Router 读 DB 或健康状态的跨边界查询:

1. `Priority` 升序，或缺 metadata 时沿用 `PoolCandidates` 原始顺序。registry 当前已经按 binding priority then id 产出 pool candidates。见 `backend/internal/registry/registry.go:56-60`。
2. 同 priority 内按 `SelectionMode`:
   - `strict_priority`: 继续使用稳定顺序。
   - `priority_weighted`: 用 `(tenant_id, request_id, session_hash/prompt_hash)` 做确定性 weighted shuffle。不得使用全局 `math/rand`，避免并发不确定与 race。
3. `Weight <= 0` 视为 1，防止配置空值把 pool 静默删掉。
4. `FallbackClass` Phase 1 只进入 `AttemptPlan.Reason` / audit，不做复杂策略。未来可让 context_window/safety/quota fallback 影响排序。
5. capability 仍通过 `AttemptPlan.RequiredCapabilities` 交给 pool gate 过滤；Router 不越界读取账号能力。
6. health / capacity 仍由 pool selector + channelhealth gate 负责；跨 attempt 的健康反馈通过失败后 signal 写入，下一次 `Selector.Select` 自然避开 cooling/disabled account。

### 4.3 AttemptBudget 默认值

建议默认:

| 条件 | AttemptBudget |
|---|---:|
| request body 不可重放 | 1 |
| stream request 但尚未交付 | 3 |
| non-stream request | 3 |
| pool candidates 少于预算 | 允许按 ranked pool 循环补足 attempt，最多 3；重复 pool 的 reason 标 `same_pool_account_failover` |
| operator 后续显式配置 | 上限 5，Phase 1 不做 UI/config，只在代码常量保留 |

默认值 3 的原因: 能覆盖 primary、一次跨池、一次同池换号/第二跨池，避免 transient 失败直接暴露给 client；同时不会在 429/5xx 时打爆所有账号。

### 4.4 AttemptPlan 生成

输入 `PoolCandidates=[A,B,C]`，默认输出:

```text
Attempt 0: pool A, reason primary
Attempt 1: pool B, reason cross_pool_fallback
Attempt 2: pool C, reason cross_pool_fallback
AttemptBudget=3
```

输入 `PoolCandidates=[A]`，默认输出:

```text
Attempt 0: pool A, reason primary
Attempt 1: pool A, reason same_pool_account_failover
AttemptBudget=2
```

输入 `PoolCandidates=[A,B]`，默认输出:

```text
Attempt 0: pool A, reason primary
Attempt 1: pool B, reason cross_pool_fallback
Attempt 2: pool A, reason same_pool_account_failover
AttemptBudget=3
```

同一 pool 的重复 attempt 依赖 handler 传入 `ExcludedAccounts`，所以不会重复选中已失败账号；若 pool 内无其他 eligible account，selector 返回 no capacity/no eligible，handler 可继续下一个 plan。

## 5. Attempt 执行函数抽取

### 5.1 核心签名

建议在 `backend/internal/gatewayhttp/chat_completions_attempt.go` 新增:

```go
type attemptInput struct {
    Plan             router.AttemptPlan
    AttemptSeq       int
    ExcludedAccounts map[int64]struct{}
    ReplayableBody   bool
    FinalAttempt     bool
}

type attemptOutcome struct {
    AttemptSeq       int
    Attempt          router.AttemptPlan
    AccountID        int64
    AcquisitionToken uuid.UUID
    Selection        *pool.SelectionResult

    Success          *attemptSuccess
    Failure          *classifiedAttemptFailure
    DeliveryStarted  bool
    UsageDraft       gateway.UsageRecordDraft
    StreamAttempt    *billing.Attempt
}

type attemptSuccess struct {
    StatusCode int
    Header     http.Header
    Body       []byte
    Streamed   bool
}

type classifiedAttemptFailure struct {
    ClientStatus      int
    ClientCode        string
    ClientMessage     string
    Classification    gateway.Classification
    TransportClass    gateway.TransportErrorClass
    Retryable         bool
    SwitchAccount     bool
    SwitchPool        bool
    RefreshIntent     gateway.CredentialRefreshIntent
    DeliveredToClient bool
    AbortReason       string
    Cause             error
}

func (ex *chatExecution) runAttempt(w http.ResponseWriter, in attemptInput) attemptOutcome
```

`runAttempt` 可以内部复用现有 `selectPoolAccount`、`resolveCredential`、`dispatchBufferedEnvelope`、stream forwarder 逻辑，但必须停止在“写客户端响应”的边界前，除非是 streaming 2xx body 已经开始 forward。

### 5.2 函数职责拆分

建议拆成以下小函数，降低 handler 单文件膨胀:

1. `prepareAttemptClaimAndAccount(in attemptInput) (*attemptPrepared, *classifiedAttemptFailure)`
   - 必要时调用 `reserveClaimForAttempt`。
   - 调 `Selector.Select`，传 `AttemptSeq` 和 `ExcludedAccounts`。
   - 调 `CredentialVault.Resolve`。
   - 构造 `ForwardRequest` 和 health key。
2. `executeBufferedAttempt(prepared attemptPrepared) attemptOutcome`
   - non-stream dispatch、canonical translation、ledger、client body、cost 计算。
   - 成功时 settle 一次并返回 body；不在内部写 `ResponseWriter`。
   - retryable pre-delivery failure 只返回 failure。
3. `executeStreamingAttempt(w http.ResponseWriter, prepared attemptPrepared) attemptOutcome`
   - 上游 dispatch 非 2xx 时先分类，不立即写错误；若 retryable 且未 final attempt，返回 failure。
   - 上游 2xx 时用 delivery tracker 包住 writer，调用 forwarder。
   - 如果 tracker 表明已交付，则无论 fwdErr 是否 retryable，都返回 `DeliveryStarted=true` 并由当前 attempt settle/abort 结束。
   - 如果未交付且 fwdErr retryable，则 abort/release 后返回可 retry failure。
4. `writeAttemptSuccess(w, outcome)`
   - non-stream 成功统一写 headers/body。
   - stream 成功已经写过，不重复写。
5. `writeAttemptFailure(w, failure)`
   - loop 结束或 nonretryable failure 时写最后一次错误。

## 6. Handler retry loop

### 6.1 结构

现有 handler 从 `prepareRoute` 后直接 `reserveClaim`、选账号、dispatch。Phase 1 改为:

```text
validate/auth/registry/router
reserve once for idempotency + L2 cache check
if L2 cache hit: commit cache hit and return
for i, plan := range plannedAttempts[:budget]:
    outcome := runAttempt(w, attemptInput{Plan: plan, AttemptSeq: i+1, ExcludedAccounts: failedAccounts})
    if outcome.Success != nil:
        write success if non-stream
        return
    if outcome.DeliveryStarted:
        return
    if outcome.AccountID != 0 && outcome.Failure.SwitchAccount:
        failedAccounts[outcome.AccountID] = struct{}{}
    if !outcome.Failure.Retryable || !replayableBody || i+1 >= budget:
        write final failure
        return
    continue
```

### 6.2 AttemptSeq

`AttemptSeq` 统一用 `attemptIndex + 1`:

1. `pool.SelectionRequest.AttemptSeq` 替换当前 hardcoded 1。
2. non-stream `billing.SettleRequest.AttemptSeq` 替换当前 hardcoded 1。
3. stream `billing.SettleRequest.AttemptSeq` 替换当前 hardcoded 1。
4. 若走 `Abort -> Reserve` re-reserve 路径，DB claim `attempt_seq` 也会递增；必须测试它与 pool slot acquisition 的 attempt_seq 对齐。

当前硬编码位置: pool selection `backend/internal/gatewayhttp/chat_completions_dispatch.go:169-171`，non-stream settle `backend/internal/gatewayhttp/chat_completions_billing.go:85-104`，stream settle `backend/internal/gatewayhttp/chat_completions_stream.go:436-453`。

### 6.3 交付前失败才 retry

实现一个 `deliveryTracker`:

```go
type deliveryTracker struct {
    http.ResponseWriter
    started bool
    status  int
}
```

`WriteHeader` 和成功写入 `Write` 都把 `started=true`。`Flush` 不单独代表业务交付；但若底层先 `WriteHeader(200)` 再 flush，也已 started。

non-streaming 在完成 settle 前不写客户端，所以 pre-delivery 判断简单。streaming 必须包住传给 `forwardSSEAndSettle`/forwarder 的 writer；`gateway.StreamForwarder.writeAndFlush` 是真实首字节边界。见 `backend/internal/gateway/forwarder.go:567-575`。

如果 `DeliveryStarted=true`:

1. 禁止 failover。
2. 不再写 normalized JSON error 覆盖已开始的 SSE。
3. 按当前 streaming billing 规则 settle partial / ambiguous / abort no-delivery。
4. 记录 channelhealth signal。

## 7. 请求体缓冲与重放策略

当前代码已把 request body 读入 `[]byte`，所有 dispatcher 调用都使用 `ex.body` 或由它转换出的 body，因此被接受的请求天然可重放。见 `backend/internal/gatewayhttp/chat_completions_validate.go:75-84`、`backend/internal/gateway/upstream_dispatcher.go:37-52`。

Phase 1 建议保守策略:

1. 保留现有 hard limit `1 MiB`，避免 Phase 1 同时扩大 API 请求体上限。
2. 新增显式 replay descriptor:

```go
type requestReplayBuffer struct {
    Body      []byte
    Replayable bool
    Reason    string
}
```

3. 现阶段所有通过 `readChatRequestBody` 的请求 `Replayable=true`。
4. 如果后续 Owner 要把 hard max 提到例如 8 MiB，则引入 `maxRetryableBodyBytes=1 MiB`: `1 MiB < body <= 8 MiB` 允许单 attempt，但 `Replayable=false`，错误 taxonomy 即使 retryable 也不 retry。
5. body 读失败或超过 hard max 仍是 client 4xx，不进入 ClaimGate、不进入 pool、不 retry。

这满足 Phase 1 的安全目标，同时把“超阈值大体不可重试”接口先放好，不在本 PR 扩大内存风险。

## 8. 错误 taxonomy

### 8.1 新增统一 retry decision 层

现有 `gateway.Classify` 是 provider error normalization，不直接表达“当前 attempt 是否可 retry、是否换账号、是否需要 OAuth refresh”。建议在 `backend/internal/gateway/attempt_error.go` 加一层:

```go
type TransportErrorClass string
type CredentialRefreshIntent string

const (
    RefreshNone CredentialRefreshIntent = ""
    RefreshOAuthHotPath CredentialRefreshIntent = "oauth_hot_path"
)

type AttemptRetryDecision struct {
    RetryableBeforeDelivery bool
    SwitchAccount           bool
    SwitchPool              bool
    RefreshIntent           CredentialRefreshIntent
    ClientStatus            int
    AbortReason             string
}
```

输入可以是:

1. HTTP status + header + body + provider -> 复用 `gateway.Classify`。
2. local dispatch error -> inspect `context.DeadlineExceeded`、`os.IsTimeout`、`net.Error`、TLS/x509/url error，并映射到 `TransportErrorClass`。
3. future Rust class -> 直接转换为 `TransportErrorClass`，再走同一 decision 表。

### 8.2 Phase 1 taxonomy 表

| 错误 | Phase 1 可重试? | 换账号? | 换池? | 触发刷新? | health signal | 说明 |
|---|---:|---:|---:|---|---|---|
| upstream 5xx | 是，交付前 | 是 | 是 | 否 | `SignalUpstream5xx` | 使用现有 `ErrorClassServerError` / `ErrorClassOverloaded`。 |
| connect timeout / network timeout | 是，交付前 | 是 | 是 | 否 | `SignalTimeout` | local error 或 future Rust `connect_timeout`。 |
| TLS handshake failed | 是，交付前 | 是 | 是 | 否 | `SignalChannelError` | future Rust `tls_handshake_failed`；Go native 由 tls/x509/url error 映射。 |
| upstream header timeout | 是，交付前 | 是 | 是 | 否 | `SignalTimeout` | future Rust `upstream_header_timeout`。 |
| upstream body idle timeout | 未交付可重试；已交付不可重试 | 是 | 是 | 否 | `SignalTimeout` | 与方向 1 Rust 表一致。 |
| 429 | 是，交付前 | 是 | 是 | 否 | `SignalRateLimit` | 带 `Retry-After` 时传给 channelhealth cooldown。 |
| 401 | Phase 1 否 | 否 | 否 | `RefreshOAuthHotPath` | token revoked / auth failure | Phase 2 先同账号热刷新；Phase 1 禁止盲目打满多账号。 |
| 403 | Phase 1 否 | 否 | 否 | 视 classification | forbidden / account suspended | platform policy / workspace disabled 不 retry。 |
| 400/404/client 4xx | 否 | 否 | 否 | 否 | client malformed 或 channel error | 客户端请求问题不应换号。 |
| 413 request too large | 否 | 否 | 否 | 否 | client malformed | 现有 classifier 已有 `ErrorClassRequestTooLarge`。 |
| proxy resolver / adapter missing / local config | 否 | 否 | 否 | 否 | local gateway 5xx | DI/config 错不应 failover 掩盖。 |
| unknown upstream | 否，默认保守 | 否 | 否 | 否 | channel error | 未分类错误不扩散。 |

### 8.3 401/403 与 Phase 2 接口

Phase 1 不实现 OAuth hot refresh，但 failure result 要带:

```go
RefreshIntent: gateway.RefreshOAuthHotPath
```

Phase 2 可以在 attempt loop 中插入:

```text
if failure.RefreshIntent == RefreshOAuthHotPath:
    refreshed := credentialRefresher.Refresh(...)
    if refreshed && !deliveryStarted:
        retry same account once
    else:
        apply disable/failover policy
```

这避免 Phase 1 先把过期 token 造成的 401 扩散到全池。

## 9. Billing / Claim 原子性设计

### 9.1 推荐路径: 复用 Abort -> ReReserve

当前 billing 已经有 re-attempt 语义: aborted claim 可被 `Reserve` 重开并 `attempt_seq + 1`。见 `backend/internal/billing/claim_gate.go:101-120` 和 `backend/internal/db/billing/billing_claims.sql.go:215-219`。

Phase 1 推荐复用这条路:

1. 第一次 attempt 前正常 `Reserve`，得到 claim id。
2. retryable pre-delivery failure:
   - 如果已经 acquire slot，调用 `Settler.Abort(..., reason=attempt_failure_*)`，释放 slot/in-flight，写 zero-cost attempt evidence。
   - 如果尚未 acquire slot，也调用 `Abort` 终结本次 reserving claim，便于下一次 `Reserve` 走 re-reserve。
3. 下一 attempt 调用同一 `reserveClaimForAttempt`，相同 idempotency fingerprint 命中 aborted claim，`ReReserveAbortedClaim` 把同一 claim 重开，并递增 DB `attempt_seq`。
4. 最终成功只调用一次正向 `settleCompletion`，写 committed usage/cost。
5. 所有 attempt 共用同一 `LogicalRequestID`、payload hash、claim id；`PoolingGroupID` 作为可变 route 归属更新，不进入 idempotency hash。

优点:

- 不需要 schema migration。
- 已有 `Abort` 原子释放 slot/in-flight。
- aborted attempt 有 audit/usage evidence。

风险:

- 同一 claim 会出现 `claim_aborted` event 后又 `claim_committed` event；虽然现有 `ReReserveAbortedClaim` 注释已承认 re-attempt path，但这属于 billing 语义变更，必须 Owner 单独确认。
- 当前 `ReReserveAbortedClaim` 不更新 `pooling_group_id`，跨池 retry 会让 claim 行 pool id 停在第一次 attempt，必须改 query。

### 9.2 幂等键稳定

`ComputeIdempotencyFingerprint` 当前刻意排除了 `PoolingGroupID`，理由是 pool group 来自可变 admin state，不应让 legitimate retry 变成 fingerprint conflict。见 `backend/internal/billing/claim_gate.go:175-185`。Phase 1 必须保持这一点不变。

### 9.3 失败 attempt 的释放

失败 attempt 释放必须走 `Settler.Abort`，而不是 handler 直接调 DB release query。理由:

1. `Abort` 已 tenant-scoped lock claim，防 cross-tenant stale claim id。见 `backend/internal/billing/settler.go:272-288`。
2. `Abort` 已在同一个 Tx2 中写 billing event、usage evidence、release slot。见 `backend/internal/billing/settler.go:289-388`。
3. 直接 release slot 会绕过 billing audit，导致 in-flight 与 claim 状态难追。

如果 Owner 不接受 aborted->re-reserve 语义，则替代方案是新增 `Settler.ReleaseAttemptForRetry`，只 release slot + clear claim acquisition，不写 aborted status；但这比推荐方案更接近新 billing 协议，风险更高。

### 9.4 触及 billing/claim 的所有改动点清单（高风险，执行前 Owner 单独确认）

以下清单是 Phase 1 真正触及 money path 的改动点，不能在 Owner 确认前实施:

1. `backend/internal/billing/billing.go`
   - 可能扩展 `ReserveResult` 返回 DB `AttemptSeq`，让 handler 验证 re-reserve 后 seq 与 loop seq 一致。
   - 或新增 retry attempt release 接口；推荐方案暂不新增。
2. `backend/internal/billing/claim_gate.go`
   - `DefaultClaimGate.Reserve`: re-reserve aborted claim 时把当前 attempt 的 `PoolingGroupID` 传给 sqlc query。
   - 保持 `ComputeIdempotencyFingerprint` 不加入 `PoolingGroupID`。
3. `backend/internal/db/billing/billing_claims.sql.go` 对应的源 SQL
   - `ReReserveAbortedClaim`: 增加 `pooling_group_id = $5`，确保跨池 retry 后 claim route 归属是当前 attempt。
   - 若 Owner 决定不走 abort/re-reserve，则另需新增 clear acquisition / release-for-retry query。
4. `backend/internal/pool/binding/claim_gate.go`
   - 评估 `WriteAcquisitionToken` 是否需要同时写 `pooling_group_id` 或防止 stale acquisition overwrite；推荐先由 `ReReserveAbortedClaim` 更新 pool id，`WriteAcquisitionToken` 保持职责不变。
5. `backend/internal/billing/settler.go`
   - `Abort`: 确认 retryable attempt abort reason 进入允许列表/长度限制；不改成本语义。
   - `Settle`: 不改用 claim.AttemptSeq 的权威逻辑；测试覆盖它与 handler seq 对齐。
6. `backend/internal/gatewayhttp/chat_completions_dispatch.go`
   - `reserveClaim` 改为 `reserveClaimForAttempt`，允许 failed attempt abort 后同请求 re-reserve。
   - `selectPoolAccount` 传入 `AttemptSeq` 和 `ExcludedAccounts`。
7. `backend/internal/gatewayhttp/chat_completions_billing.go`
   - `nonStreamingSettleRequest` 使用当前 attempt seq/account/token；保持正向 settle 只发生一次。
8. `backend/internal/gatewayhttp/chat_completions_stream.go`
   - `streamingCompletionEvent` 使用当前 attempt seq/account/token；交付后失败不得 re-reserve。
9. tests
   - billing integration 必须证明: attempt 1 abort releases slot；attempt 2 re-reserve 同 claim id 并更新 pooling_group_id；final settle only one positive-cost committed record；idempotency replay 仍按 same claim id 命中。

## 10. 流式硬规则

硬规则: 流已开始，禁止 failover。

代码强制方式:

1. 在 `executeStreamingAttempt` 中用 `deliveryTracker` 包住真正传给 forwarder 的 writer。
2. 对上游 dispatch error、nil response、非 2xx response，先分类并返回 outcome，不直接写 `ResponseWriter`；只有 loop 决定不 retry 时才写 JSON error。
3. 对 2xx streaming response，调用 forwarder 后检查 tracker:
   - `started=false` 且 end class 是 first token timeout / network timeout / upstream EOF no terminal / upstream 5xx: 可按 taxonomy retry。
   - `started=true`: 立即终止 loop，按当前 streaming billing 规则 settle partial / ambiguous；不再尝试任何 pool/account。
4. `forwardSSEAndSettle` 要拆成 `forwardSSEAttempt`，返回 `gateway.UsageRecordDraft`、`billing.Attempt`、`fwdErr`、`deliveryStarted`，由外层根据 retry decision 调 `Abort` 或 `Settle`。
5. `writeStreamingUpstreamError` 只允许在 final failure 时调用；retryable pre-delivery upstream error 不写。

## 11. 文件 / 包改动设计

### PR 1: Router 多候选 planner（低-中风险）

文件:

- `backend/internal/router/route_plan.go`
- `backend/internal/router/default_router.go`
- `backend/internal/router/router_test.go`
- `backend/internal/gatewayhttp/chat_completions_dispatch.go`（只映射 registry binding metadata）

内容:

- 引入 `PoolCandidateMeta`。
- `DefaultRouter.Plan` 从全部 candidates 生成 attempts。
- 默认 budget 3，单 pool 时 budget 2。
- 保持 `Router` 接口不变。
- 单测覆盖 ordered candidates、metadata absent fallback、weighted deterministic order、single-pool same-account-failover duplicate、snapshot stamp 不变。

### PR 2: Error taxonomy + attempt outcome 类型（中风险）

文件:

- `backend/internal/gateway/attempt_error.go`（新）
- `backend/internal/gateway/attempt_error_test.go`（新）
- `backend/internal/gatewayhttp/chat_completions_attempt.go`（新，类型与 helper）
- `backend/internal/gatewayhttp/chat_completions_error.go`

内容:

- 增加 `TransportErrorClass` / `CredentialRefreshIntent` / `AttemptRetryDecision`。
- 把 HTTP classification、local dispatch error、future Rust class 收敛到同一 decision。
- 单测覆盖 taxonomy 表。

### PR 3: Handler attempt loop skeleton（中风险）

文件:

- `backend/internal/gatewayhttp/chat_completions_handler.go`
- `backend/internal/gatewayhttp/chat_completions_dispatch.go`
- `backend/internal/gatewayhttp/chat_completions_billing.go`
- `backend/internal/gatewayhttp/chat_completions_stream.go`
- `backend/internal/gatewayhttp/chat_completions_attempt.go`
- 相关 handler tests

内容:

- 抽 `runAttempt`。
- `AttemptSeq` 去硬编码。
- `ExcludedAccounts` 贯穿 pool selection。
- non-stream 成功延迟写 response。
- stream 增加 delivery tracker。

注意: PR 3 可先用 `AttemptBudget=1` feature flag/常量关闭实际 retry，确保 extraction 不改行为；PR 4 再打开 retry。这能降低大重构风险。

### PR 4: Billing/claim retry 原子性（高风险，Owner 确认后）

文件:

- `backend/internal/billing/billing.go`
- `backend/internal/billing/claim_gate.go`
- billing sqlc source + generated files
- `backend/internal/billing/claim_gate_integration_test.go`
- `backend/internal/billing/settler_integration_test.go`
- handler retry integration tests

内容:

- `ReReserveAbortedClaim` 更新 `pooling_group_id`。
- handler 在 retryable pre-delivery failure 后 `Abort` 并 re-reserve。
- 确认 claim id 稳定、attempt_seq 递增、slot release 正确、最终正向 settle 一次。

### PR 5: Enable retry/failover end-to-end（中-高风险）

文件:

- `backend/internal/gatewayhttp/*retry/failover tests`
- `backend/internal/channelhealth/*` 只补测试或轻量 helper（如需要）
- `backend/cmd/gateway/wiring.go` 若 `routePlanner` 类型从 `*router.DefaultRouter` 改成接口或新 struct

内容:

- 默认启用 AttemptBudget。
- 集成 coverage: 500 -> second account success；429 -> cooldown + next account；stream pre-first-byte timeout -> retry；stream post-first-byte error -> no retry。

## 12. 测试策略

### 12.1 单测

Router:

- `PoolCandidates=[99,100,101]` -> 3 attempts, budget 3, reasons primary/cross_pool.
- `PoolCandidates=[99]` -> 2 attempts, both pool 99, second reason same_pool_account_failover.
- `PoolMetadata` priority/weight deterministic ranking。
- missing request id / missing tenant / missing protocol 继续 fail closed。

Gateway taxonomy:

- HTTP 500/529/504 -> retryable before delivery。
- local timeout / TLS error -> retryable before delivery。
- 429 with `Retry-After` -> retryable + switch account + retry after ms。
- 401/403 -> nonretryable in Phase 1 + refresh intent。
- 413/client 4xx -> nonretryable。
- unknown -> nonretryable。

Handler:

- non-stream attempt 1 upstream 500 before write，attempt 2 success -> client 200；`Selector.Select` 收到 seq 1/2；failed account 被排除；`Settle` 一次。
- non-stream 401 -> no second attempt；refresh intent observable in error/log/test hook。
- pool no capacity on attempt 1 with second pool available -> retry second pool。
- all attempts fail -> client receives last classified error, not first generic error。
- idempotency replay: successful retried request stores one final response; same key replay returns stored response。

Streaming:

- dispatch error before upstream response -> retry。
- upstream non-2xx before SSE body -> retry if taxonomy allows。
- forwarder first-token timeout before `Write` -> retry。
- after first `Write`, upstream EOF/timeout -> no retry；settle partial/ambiguous according to existing billing rules。
- streaming idempotency capture still records only final delivered stream。

### 12.2 Integration tests

Billing/claim:

- Attempt 1 acquire slot -> retryable upstream 500 -> `Abort` releases slot and writes zero-cost attempt evidence。
- Attempt 2 re-reserve same claim id -> claim `attempt_seq=2` -> final settle positive cost once。
- Cross-pool retry updates claim `pooling_group_id` to final attempt pool。
- `ComputeIdempotencyFingerprint` unchanged across pool changes。
- concurrent same idempotency key while first request reserving returns claim race / replay behavior unchanged。

Pool:

- `ExcludedAccounts` prevents reselecting failed account in same request。
- single pool with two accounts can failover account when first account is excluded。
- pool health cooling account is filtered by channelhealth gate。

Wiring:

- `chatHandlerDeps` still provides router, selector, claim gate, settler, health。
- new router constructor does not require new runtime dependency。

### 12.3 Regression cases from Owner background

- 多账号 failover 只正向结算一次。
- 流式中途失败不伪装成新 200，也不重试。
- selector in-flight count 在 failed attempt 后释放。
- 幂等键不写多份、不因 pool 变化 fingerprint conflict。
- 429 按响应头 cooldown。

## 13. 风险与缓解

| 风险 | 等级 | 缓解 |
|---|---|---|
| billing 双计费 / 错计费 | 高 | PR 4 单独 Owner 确认；integration test 验证 `Settle` 只一次正成本；Abort 均 zero-cost。 |
| claim pooling_group_id stale | 高 | 修改 `ReReserveAbortedClaim` 更新 pooling_group_id；测试跨池 final claim pool。 |
| 流已开始仍 retry | 高 | delivery tracker 硬门；已 Write/WriteHeader 后 taxonomy 结果被忽略。 |
| 401/403 扩散打全池 | 高 | Phase 1 taxonomy 禁止 auth failure failover，只输出 refresh intent。 |
| 请求体内存放大 | 中 | 保留现有 1MiB hard limit；先只新增 replay descriptor。 |
| Router 排序引入不可复现 | 中 | deterministic weighted shuffle；不使用全局 rand。 |
| channelhealth 信号过猛导致误降级 | 中 | taxonomy 区分 client 4xx、本地配置错、upstream 5xx、429；测试 signal mapping。 |
| EventBus async settle 与 retry 交错 | 中 | failed attempt Abort 同步执行；final `settleCompletion` 仍沿用现有 direct fallback。 |
| L2 cache 与 retry 交叉 | 低-中 | cache check 仍在 upstream attempts 前；cache hit 不进入 loop。 |

## 14. 估时

| PR | 估时 |
|---|---:|
| PR 1 Router planner | 1-1.5 天 |
| PR 2 taxonomy/outcome | 1-1.5 天 |
| PR 3 handler extraction with retry disabled | 2-3 天 |
| PR 4 billing/claim retry atomicity | 2-3 天，需 Owner 确认后开始 |
| PR 5 enable + integration regression | 1.5-2 天 |

总估时: 7.5-11 天。若 Owner 要同时扩大 request body hard max 或改 billing 为 `ReleaseAttemptForRetry` 新协议，另加 2-4 天与更高 review 成本。

## 15. Owner 确认点

1. 是否接受推荐 billing 语义: failed pre-delivery attempt 用 `Settler.Abort` 写 zero-cost aborted evidence，然后同一 claim `ReReserveAbortedClaim` 继续，最终成功再 committed。
2. 是否批准修改 `ReReserveAbortedClaim` 让跨池 retry 更新 `pooling_group_id`。
3. Phase 1 是否保持 1MiB request body hard limit；若要放大 hard max，是否接受 `maxRetryableBodyBytes` 与 `Replayable=false` 单 attempt 策略。
4. 401/403 Phase 1 是否按本稿保守处理为不 failover，只留 Phase 2 refresh intent。
5. Default AttemptBudget 是否采用 3，单 pool 是否采用 2。

## 16. 功能 preservation / clean-room / security

没有功能缩水。Phase 1 把当前单 attempt 桩升级为多候选 + retry/failover；401/403 不立即 failover 是安全 rollout，不删除 Phase 2 OAuth hot refresh 能力。

没有新增 clean-room 风险。本稿没有读取外部参考源码，只引用 HUAKAI gap 文档 §4 已存在的 file:line evidence。

安全风险集中在 billing/claim money path、错误分类导致的账号扩散、流式交付边界。设计中均设置了 Owner 确认点、taxonomy 保守默认和 delivery tracker 硬门。

## 17. Source files read

Go files read:

- `backend/internal/gatewayhttp/chat_completions_handler.go`
- `backend/internal/gatewayhttp/chat_completions_dispatch.go`
- `backend/internal/gatewayhttp/chat_completions_stream.go`
- `backend/internal/gatewayhttp/chat_completions_error.go`
- `backend/internal/gatewayhttp/chat_completions_billing.go`
- `backend/internal/gatewayhttp/chat_completions_validate.go`
- `backend/internal/gatewayhttp/chat_completions_idempotency_replay.go`
- `backend/internal/gatewayhttp/chat_completions_stream_replay.go`
- `backend/internal/gatewayhttp/chat_completions_handler_test.go`
- `backend/internal/gatewayhttp/chat_completions_dispatch_test.go`
- `backend/internal/router/router.go`
- `backend/internal/router/route_plan.go`
- `backend/internal/router/default_router.go`
- `backend/internal/router/router_test.go`
- `backend/internal/pool/api.go`
- `backend/internal/pool/types.go`
- `backend/internal/pool/router/types.go`
- `backend/internal/pool/router/default_selector.go`
- `backend/internal/pool/router/gates.go`
- `backend/internal/pool/router/slot.go`
- `backend/internal/pool/dispatcher/dispatcher.go`
- `backend/internal/pool/dispatcher/slot_manager.go`
- `backend/internal/pool/binding/claim_gate.go`
- `backend/internal/billing/billing.go`
- `backend/internal/billing/claim_gate.go`
- `backend/internal/billing/settler.go`
- `backend/internal/billing/state.go`
- `backend/internal/db/billing/billing_claims.sql.go`
- `backend/internal/db/billing/billing_settle.sql.go`
- `backend/internal/gateway/upstream_dispatcher.go`
- `backend/internal/gateway/upstream_dispatcher_hcsf.go`
- `backend/internal/gateway/upstream_http_error.go`
- `backend/internal/gateway/error_normalize.go`
- `backend/internal/gateway/error_apply.go`
- `backend/internal/gateway/forwarder.go`
- `backend/internal/gateway/forwarder_types.go`
- `backend/internal/channelhealth/types.go`
- `backend/internal/channelhealth/service.go`
- `backend/internal/channelhealth/signal_classifier.go`
- `backend/internal/channelhealth/failover.go`
- `backend/cmd/gateway/wiring.go`
- `backend/cmd/gateway/routes.go`
- `backend/cmd/gateway/selector_wiring.go`
- `backend/cmd/gateway/config.go`
- `backend/cmd/gateway/middleware.go`
- `backend/cmd/gateway/lifecycle.go`
- `backend/cmd/gateway/wiring_test.go`
- `backend/internal/registry/registry.go`
- `backend/internal/registry/postgres_registry.go`

Background docs read:

- `docs/process/plans/2026-05-21-direction-1.md`
- `docs/process/plans/2026-05-21-account-to-api-gap-analysis.md`

Agent: Codex GPT-5
UTC timestamp: 2026-05-21T07:42:08Z
