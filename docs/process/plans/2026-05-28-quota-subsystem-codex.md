# 2026-05-28 配额子系统实施计划（Codex independent draft）

| Owner directive | 独立起草 HUAKAI「配额子系统(quota subsystem)」实施计划；只写计划文档，不 commit / push |
| --- | --- |
| Lane | SPECIFIER：已读参考项目源码，只输出行为/机制级计划，不输出实现代码 |
| Agent | Codex GPT-5 |
| HUAKAI commit | BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4 |
| Observed regions | 32 |
| Inferences | 11 |
| Open questions | 6 |

## 1. 证据基线

维护状态（2026-05-28 UTC 检查）：

- Sub2API：GitHub 页面为 Public，最新 release 为 2026-05-27；本地 `main` HEAD 为 2026-05-20，90 天内活跃。使用提交 `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850`。
- New API：GitHub API 显示 `archived=false` 且 `pushed_at=2026-05-27T05:23:52Z`；本地 HEAD 为 2026-05-20。使用提交 `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca`。
- LiteLLM：release 页面显示 2026-05-27 发布候选版本；本地 HEAD 为 2026-05-19，90 天内活跃。使用提交 `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2`。
- Portkey gateway：GitHub 页面为 Public；本地 HEAD 为 2026-03-25，距 2026-05-28 小于 90 天。使用提交 `Portkey-AI/gateway@351692fd9236af222168134b416924fae0bdba23`。

关键观察：

- HUAKAI 已有 `billing.ClaimGate` / `billing.Settler` 边界，Tx1/Tx2 接口已显式承载预测成本、实际成本、tenant/API key/user 维度和 cache-hit 终结路径 `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/internal/billing/billing.go:19` `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/internal/billing/billing.go:27`。
- 现有 `ClaimGate` 只创建/复用 billing claim，并未真正扣占多维 quota；其事务是 serializable，先做幂等查找，再插入 reserving claim `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/internal/billing/claim_gate.go:77` `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/internal/billing/claim_gate.go:85` `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/internal/billing/claim_gate.go:143`。
- 现有 `Settler` 在 serializable Tx2 内锁定 claim、验证 tenant/account/API key/user 归属，并写 usage / billing event / claim 状态 `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/internal/billing/settler.go:82` `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/internal/billing/settler.go:89` `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/internal/billing/settler.go:140`。
- Chat completions 热路径已经在 pool 选择前计算预测成本并调用 `ClaimGate.Reserve`，这是 quota reserve 最小侵入接入点 `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/internal/gatewayhttp/chat_completions_dispatch.go:151` `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/internal/gatewayhttp/chat_completions_dispatch.go:159`。
- provider account 现有表已包含 account 级 concurrency、quota cap/used、quota status，但 selector eligibility 目前只过滤 enabled/health/model/capability/credential，未把 quota status 纳入 SQL eligibility `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/sql/migrations/0001_pool_routing.up.sql:127` `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/sql/migrations/0001_pool_routing.up.sql:139` `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/sql/queries/pool_accounts.sql:143`。
- pool slot 已用 DB 原子自增保护 provider-account 并发，quota 子系统不应在 S0/S1 切片重复维护同一 account in-flight 计数 `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/sql/queries/pool_accounts.sql:230` `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/internal/pool/dispatcher/slot_manager.go:73`。
- 已有 F-RATE-001 schema 为上游冷却留了 provider-account 状态和审计表；quota 子系统应复用冷却事实，不重建第二套冷却状态 `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/sql/migrations/0004_rate_limiting.up.sql:24` `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/sql/migrations/0004_rate_limiting.up.sql:92`。
- 当前 migration 最高为 `0059`，quota schema 若落地应从 `0060` 起步。
- 包结构规则要求新功能落新包；冻结包 `backend/internal/gatewayhttp`、`backend/internal/gateway`、`backend/internal/proto` 禁止新增文件 `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:AGENTS.md:583` `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:AGENTS.md:596` `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:AGENTS.md:605`。

参考项目对照（行为级，不复用实现）：

- Sub2API 在本地 precheck 中按日/分钟窗口跳过预计已耗尽的上游账号，但明确不把本地 precheck 写成真实上游冷却；并发槽用缓存，等待队列缓存错误时偏 fail-open `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/ratelimit_service.go:365` `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/ratelimit_service.go:410` `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/concurrency_service.go:129` `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/concurrency_service.go:208`。
- Sub2API 的 account 调度会把过期、过载、冷却、临时不可调度、quota exhausted 统一作为不可调度条件；用量落在 post-usage 路径中增量写回 `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/account.go:107` `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/account.go:1926` `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/gateway_service.go:7986`。
- New API 在请求前检查 user/token 余额，必要时预扣；失败路径返还；订阅额度有 request 级幂等记录、DB 事务锁定、refund 幂等路径 `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:service/pre_consume_quota.go:34` `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:service/pre_consume_quota.go:66` `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/subscription.go:984` `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/subscription.go:1078`。
- New API 同时有 user/IP/group/request-success 维度的限速入口，并会对 token 余额做 cache/DB 双层增减；HUAKAI 不应照搬缓存异步扣账，而应使用 PG 作为钱路径来源 `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:middleware/rate-limit.go:21` `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:middleware/model-rate-limit.go:84` `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/token.go:405`。
- LiteLLM 的限速描述符覆盖 key/user/team/org/model/agent 等多层作用域；RPM/并发先走预调用检查，TPM 先预留估算 token，成功时按 actual-minus-reserved 对预留作用域回补，失败时只返还实际预留过的作用域 `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/hooks/parallel_request_limiter_v3.py:1499` `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/hooks/parallel_request_limiter_v3.py:1988` `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/hooks/parallel_request_limiter_v3.py:2033` `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/hooks/parallel_request_limiter_v3.py:2589` `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/hooks/parallel_request_limiter_v3.py:2741`。
- LiteLLM 的部署预算过滤和上游冷却是路由前过滤；冷却会把 429/401/408/404 和错误率作为不同触发源 `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/router_strategy/budget_limiter.py:119` `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/router_strategy/budget_limiter.py:242` `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/router_utils/cooldown_handlers.py:70` `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/router_utils/cooldown_handlers.py:223`。
- Portkey gateway 暴露 pre-request validator 作为虚拟 key/budget 闸，配置里可给 integration 配 requests/tokens 限速；Redis 令牌桶脚本支持 check-only 与 consume 两种模式 `Portkey-AI/gateway@351692fd9236af222168134b416924fae0bdba23:src/handlers/handlerUtils.ts:406` `Portkey-AI/gateway@351692fd9236af222168134b416924fae0bdba23:conf.example.json:27` `Portkey-AI/gateway@351692fd9236af222168134b416924fae0bdba23:src/shared/services/cache/utils/rateLimiter.ts:141` `Portkey-AI/gateway@351692fd9236af222168134b416924fae0bdba23:src/shared/services/cache/utils/rateLimiter.ts:153`。

## 2. 范围与切片

两天临退役旧机窗口内只做可闭合小切片，按 S0/S1 必修、S2/S3 记票。

In scope：

- S0/S1：新建 `backend/internal/quota` 自洽包，提供 PG-backed quota reserve/settle/release、rate/cost/concurrency 统一 decision model、audit/reconciliation 表设计、真实 PG 并发测试计划。
- S0/S1：用 wrapper 方式接入现有 `billing.ClaimGate` / `billing.Settler`，不在冻结包新增文件，不重写 gateway handler。
- S0/S1：覆盖 per-API-key / per-user / per-channel / global 的请求数、成本、并发；provider-account 并发仍以现有 pool slot 为权威，避免双计数。
- S0/S1：上游冷却只复用 F-RATE-001 状态作为 selector/policy 输入，不新建第二套 provider cooldown 状态。
- S0/S1：migration 0060 只在 Owner 明确确认后实施；本计划先给 schema 形状、风险和回滚点。
- S2：admin UI、运营手动覆盖、quota policy CRUD 的完整后台页面。
- S2：Redis hot cache 加速。钱/租户隔离路径仍以 PG 为准。
- S2：provider-account quota cap 与新 quota ledger 的完全合流迁移。
- S3：更细粒度的优先级/团队/agent/session 维度；先保留 policy 维度扩展点，不在两天窗口实现全部。

Out of scope：

- 不修改 `LICENSE`、不读写 secrets、不做生产部署。
- 不引入新 runtime dependency。
- 不在 `backend/internal/gatewayhttp` / `backend/internal/gateway` / `backend/internal/proto` 新增文件。
- 不把参考项目 schema、函数名、缓存 key、脚本结构搬进 HUAKAI。

## 3. 包与文件布局

全部新增 Go 文件落在非冻结包 `backend/internal/quota`。该目录当前不存在，新增后职责为“配额决策、额度预留、结算回补、并发槽、策略读取、审计事件”，不混入 HTTP handler 或 billing ledger 实现。

计划新增：

- `backend/internal/quota/doc.go`：package `quota`，非冻结。包边界、能力 disposition、clean-room 说明。
- `backend/internal/quota/types.go`：package `quota`，非冻结。Scope、Metric、Decision、Reservation、Settlement 的本地中性类型。
- `backend/internal/quota/errors.go`：package `quota`，非冻结。deny/retryable/reconciliation-needed 错误分类。
- `backend/internal/quota/policy.go`：package `quota`，非冻结。policy resolver、scope expansion、mode（enforce/observe/manual-first）。
- `backend/internal/quota/store.go`：package `quota`，非冻结。PG store interface，仅 HUAKAI 自有抽象。
- `backend/internal/quota/pg_store.go`：package `quota`，非冻结。sqlc-backed store，所有查询 tenant-scoped。
- `backend/internal/quota/service.go`：package `quota`，非冻结。reserve / settle / release orchestration。
- `backend/internal/quota/reservation.go`：package `quota`，非冻结。reservation 状态机、lease、idempotency。
- `backend/internal/quota/settlement.go`：package `quota`，非冻结。actual-cost delta、cache-hit、abort、DLQ/reconcile。
- `backend/internal/quota/rate_window.go`：package `quota`，非冻结。fixed/sliding/calendar window 计算，不依赖 Redis。
- `backend/internal/quota/concurrency.go`：package `quota`，非冻结。global/user/API-key/channel 并发槽，不维护 provider account in-flight。
- `backend/internal/quota/cooldown_view.go`：package `quota`，非冻结。只读 F-RATE provider cooldown/policy input。
- `backend/internal/quota/billing_wrapper.go`：package `quota`，非冻结。实现 `billing.ClaimGate` / `billing.Settler` wrapper，控制接入点。
- `backend/internal/quota/reconciler.go`：package `quota`，非冻结。settle/release 失败后的补偿扫描。
- `backend/internal/quota/*_test.go`：package `quota` 或 `quota_test`，非冻结。单元与 PG integration tests。
- `backend/sql/queries/quota.sql`：非 Go 包。sqlc queries，全部带 tenant predicate。
- `backend/sql/migrations/0060_quota_subsystem.up.sql` / `.down.sql`：schema 高风险文件，必须 Owner 确认后执行。

计划修改既有文件：

- `backend/cmd/gateway/wiring.go`：非冻结。构造 quota store/service，把现有 claim gate / settler 包成 quota-aware wrapper。
- `backend/cmd/gateway/routes.go`：非冻结。理想情况下不改；若 deps 字段类型保持 `billing.ClaimGate` / `billing.Settler`，不需要变更。
- `backend/internal/billing/billing.go`：非冻结。只有当 Owner 选择“单事务强一致 Hook”方案时才改接口；默认两天窗口不改。
- `backend/internal/gatewayhttp/chat_completions_dispatch.go`：冻结包既有文件。默认不改；若 Owner 要暴露 quota-specific client error headers，可做小范围既有文件编辑，但不得新增文件。

## 4. 数据模型

推荐 migration `0060_quota_subsystem`，使用 PostgreSQL，money 用 `numeric(20,8)`，所有主表 Day 1 带 `tenant_id`，符合 HUAKAI TS-006/TS-007 `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:docs/RULES.md:106` `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:docs/RULES.md:109`。

表形状（本地原创命名，最终以实施前 schema review 为准）：

- `quota_policies`：tenant、scope kind/id、metric、window kind/seconds、limit value、burst、mode、priority、enabled、valid_from/valid_to、created/updated actor。
- `quota_windows`：tenant、policy、window_start/window_end、reserved_value、settled_value、request_count、version。唯一键为 policy + window_start。
- `quota_reservations`：tenant、claim、request fingerprint、scope snapshot、predicted cost、reserved units、status、lease_expires_at、settled cost、release reason、created/updated。
- `quota_concurrency_slots`：tenant、reservation、scope kind/id、lease_expires_at、released_at、status。用于 global/user/API-key/channel 并发，不用于 provider-account in-flight。
- `quota_audit_events`：tenant、reservation/claim、event type、decision code、scope/metric、amounts、retry_after、payload、actor、occurred_at。
- `quota_reconciliation_jobs`：tenant、claim/reservation、kind、attempt_count、last_error、next_run_at、status。用于 billing 成功但 quota settle/release 失败后的补偿。

不直接扩展 `billing_ledger_claims` 的理由：当前 billing claim 是钱路径和 idempotency 的权威，已经在 0002 migration 中承担 Tx1/Tx2 claim/usage/audit `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/sql/migrations/0002_observability_billing.up.sql:19` `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/sql/migrations/0002_observability_billing.up.sql:93` `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/sql/migrations/0002_observability_billing.up.sql:121`。quota ledger 独立后，Owner 可回滚 quota 功能而不破坏现有 usage/billing immutable rows。

## 5. 2-phase reserve + settle 算法

推荐两天窗口方案：quota 作为 billing wrapper，而不是重写 billing core。

Reserve 阶段：

1. Gateway 仍按现有路径计算 predicted cost，并调用 `ClaimGate.Reserve` 接口；实际对象换成 `quota.ReservingClaimGate` wrapper。
2. Wrapper 先调用 base billing reserve，得到 claim 或幂等 replay/fingerprint conflict。
3. 如果 billing 返回幂等 hit 或 fingerprint conflict，不创建新 quota reservation，保持 replay/409 语义不变。
4. 对新 claim，quota service 开 serializable Tx，按固定 scope 顺序锁 policy/window：global -> user -> API key -> channel -> pool group/provider view。统一计算 requests/cost/concurrency 是否可预留。
5. 若允许：写 reservation、window reserved counters、concurrency slots、audit event，然后返回原 billing reserve result。
6. 若拒绝：立即调用 existing settler abort，把 claim 终结为 zero-cost aborted，并返回 quota deny。若 abort 失败，写 reconciliation job，claim lease sweeper 必须能后续收敛。

Settle 阶段：

1. `quota.ReservingSettler.Settle` 先调用 base billing settler。原因：客户端响应已依赖 upstream 结果，billing/usage 是钱路径主记录，不能因为 quota audit 暂时失败导致 delivered response 无法落账。
2. base settle 成功后，quota settle 在独立 Tx 中把 reservation 从 reserved 转为 settled，用 actual cost 替换 predicted cost 差额：reserved scope 应用 `actual - reserved`；未在 reserve 阶段占用的 scope 只记录 actual audit，不倒扣。
3. 如果 actual > predicted 且超过剩余额度，默认策略为“commit overage + audit + optional future block”，因为响应已经交付，不能事后制造用户可见失败。这个行为需要 Owner 决策。
4. base settle 成功但 quota settle 失败时，写 reconciliation job 并保留 reservation；后台按 claim/reservation 幂等重试。这个风险必须测试覆盖。

Abort / cache hit：

- Abort：base abort 成功后释放 quota reservation 和并发槽；释放失败进入 reconciliation job。失败路径不得扣成本。
- L2 cache hit：base `CommitCacheHit` 成功后，quota 把并发槽释放，cost settled 为 0，但 audit 记录成功 cache 命中，不能把成功请求记成 aborted。现有 billing 已有 provider-less cache hit 终结接口 `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/internal/billing/billing.go:40`。

和现有 billing 的冲突规避：

- 不修改 claim idempotency hash，不把 quota policy version 放入 billing idempotency，避免 admin 改 policy 后合法 retry 变成 idempotency conflict。
- quota reservation 用 `claim_id + tenant_id` 幂等，拒绝同 claim 二次预留。
- provider-account 并发仍由 pool slot 管，不在 quota concurrency slots 中重复扣占。
- 所有 quota rows 都带 tenant_id，settle/release 查询必须同时带 tenant_id + claim_id/reservation_id。

强一致升级选项：若 Owner 要求“billing claim + quota reserve 单 DB Tx”，需要把 billing core 提供 transaction hook 或抽出 shared Tx runner。这会触碰 billing/quota enforcement 高风险文件，不适合两天窗口默认执行。

## 6. 多维统一抽象

统一抽象：

- Scope：global、user、api_key、channel、pool_group、provider_account(read-only in S0/S1)、future team/org/agent/session。
- Metric：requests、tokens_estimated、cost_usd、concurrency、cooldown_state。
- Window：none、fixed duration、calendar day/week、manual reset。
- Mode：enforce、observe、manual-first、disabled。
- Decision：allow/reserve、deny/retry_after、observe-only warning、requires_reconciliation。

S0/S1 enforcement：

- Request rate：global/user/API-key/channel。
- Cost cap：global/user/API-key/channel，cost source 为 existing rate table + predicted/actual billing cost。
- Concurrency：global/user/API-key/channel。
- Cooldown：provider-account/channel 只读输入，复用 F-RATE/provider_accounts 状态；local quota precheck 不写 provider cooldown。

S2/S3 延后：

- token TPM 精确估算与 post-call token delta；S0/S1 以 cost + request + concurrency 为主，tokens_estimated 可以 observe。
- priority reservation / tenant edition-specific fair-share。
- Redis hot cache。PG 仍是 source of truth。

## 7. Go 热路径集成

默认集成路径：

1. `backend/cmd/gateway/wiring.go` 初始化 `quota.Store`、`quota.Service`。
2. 用 `quota.WrapClaimGate(base billing.ClaimGate, service)` 替代 `billing.NewClaimGate(pgPool)` 的直接赋值。
3. 用 `quota.WrapSettler(base billing.Settler, service)` 替代直接 settler 赋值。
4. `gatewayhttp` 继续只看 `billing.ClaimGate` / `billing.Settler` 接口；不新增冻结包文件。
5. selector 已有 provider-account slot acquire 和 credential SQL/gate；quota S0/S1 不侵入 `pool` 选择器，只在 policy 中读取 channel/provider status 作为 deny/observe 输入。若要 channel hard limit 在 selector 前生效，route plan 必须能提供 channel；若只能在 account 选择后得知 channel，S0/S1 先做 channel observe 或 pool_group hard-limit。

错误输出：

- S0/S1 先复用现有 reserve error path，quota deny 映射到 `429` / quota-specific client code。
- 若要新增 `x-ratelimit-*` headers 或 `Retry-After`，需要编辑冻结包既有 handler/response helper；可做，但必须单独列为 Owner 确认点，不新增文件。

## 8. 测试计划（mutation-discriminating）

测试必须依赖真实 PostgreSQL 集成环境，禁止 nil-stub 假通过。每个测试声明它要抓的具体缺陷：

- `TestReserveTenantIsolationWithSameAPIKeyID`：tenant A 已耗尽、tenant B 同数值 API key ID 未耗尽时 B 必须 allow。若 SQL 漏 `tenant_id` predicate，会红；抓串租户和钱路径污染。
- `TestReserveCostCapCountsReservedAndSettled`：cap=10，settled=5，两个并发 predicted=4，只允许一个。若只看 settled 不看 reserved，会红；抓并发绕过成本上限。
- `TestReserveSerializableRaceAtBoundary`：N 个 goroutine 同时冲击 cap，允许数必须精确等于剩余额度。若没有 row lock / serializable / retry，允许数会超，测试红。
- `TestAbortReleasesConcurrencySlots`：reserve 后 abort，再发同 scope 请求必须 allow。若 abort 没释放并发槽，第二次会被拒绝，测试红。
- `TestSettleActualGreaterThanPredictedCreatesOverageAudit`：predicted=1、actual=3 时，reservation settled 值必须反映 actual delta，并产生 overage audit。若实现只用 predicted，测试红。
- `TestSettleFailureAfterBillingSuccessCreatesReconciliationJob`：注入 quota settle PG 失败但 base billing settle 成功，必须出现 reconciliation job。若只 log 错误或吞掉失败，测试红。
- `TestCacheHitCommitsZeroCostAndReleasesConcurrency`：cache hit 走 success 路径、cost=0、并发释放、audit 为 cache success。若实现把 cache hit 当 abort 或忘释放，测试红。
- `TestIdempotentReplayDoesNotDoubleReserveQuota`：相同 idempotency retry 命中已提交 claim，不得新增 quota reservation。若按 logical request 新建 reservation，测试红。
- `TestProviderAccountConcurrencyNotDoubleCounted`：pool in-flight cap 为 1，quota 并发也启用 user/global；一次请求 settle 后 provider in-flight 必须回 0。若 quota 也维护 provider account slot 并重复 decrement，测试红。
- `TestLocalQuotaPrecheckDoesNotPersistUpstreamCooldown`：本地 quota deny 不应写 provider cooldown；模拟上游 429 才写 F-RATE 状态。若本地 precheck 污染 cooldown，测试红；对齐 Sub2API 本地 precheck 与上游冷却分离观察。
- `TestChannelPolicyObserveThenHardEnforce`：observe mode 下超限只写 audit 不拒绝，切 enforce 后同请求拒绝。若 mode 被忽略，测试红。
- `TestNoFloatingMoneyMath`：cost 使用 decimal/numeric，构造 0.1+0.2 边界。若实现退回 float 比较，测试红。

附加 review 测试：

- sqlc query golden review：所有 quota query 必须带 tenant predicate。
- migration down/up round-trip：0060 up/down 在空库和含数据库均可运行；这是 high-risk，Owner 确认后才执行。
- package budget check：新增文件不得进入 frozen packages。

## 9. 成功标准

- 计划阶段：本文件完成，包含 source-cited 参考对照、文件布局、schema 决策点、测试判别性说明。
- 实施 S0/S1 完成时：
  - `backend/internal/quota` 编译通过，职责单一。
  - `go test ./internal/quota ./internal/billing ./internal/pool ./cmd/gateway` 在 `backend/` 通过；PG integration tests 在有 PG DSN 的环境通过。
  - 没有新增 frozen package 文件。
  - quota deny 不导致 billing double charge；abort/cache-hit 不泄漏并发槽。
  - 所有 quota query tenant-scoped。
  - migration 0060 经 Owner 确认，up/down 可验证。
  - per-commit Codex review 无 S0/S1 未解决项。

## 10. Blast Radius / What Could Go Wrong

- 钱路径风险：quota settle 失败但 billing settle 已成功，导致短期 quota 视图滞后。缓解：reservation ledger + reconciliation job + audit event + PG test。
- 串租户风险：scope/window query 漏 tenant。缓解：tenant-first schema、query review、mutation test。
- 限额绕过：只统计 settled，不统计 reserved。缓解：reserve 阶段同时检查 settled+reserved。
- 并发泄漏：abort/cache hit/settle failure 未释放 slots。缓解：lease_expires_at + sweeper + release idempotency tests。
- 热路径延迟：serializable Tx 增加 p99。缓解：S0/S1 scope 数固定、锁顺序固定、后续可加 Redis read-through cache，但 PG 为准。
- 误冷却上游：本地配额拒绝被写成 provider cooldown。缓解：cooldown_view 只读，只有 upstream error classifier 写 F-RATE 状态。
- schema 回滚风险：migration 0060 是高风险。缓解：Owner sign-off、down migration、空库/含数据 round-trip。
- 冻结包违规：为了 handler header 方便而新建 gatewayhttp 文件。缓解：wrapper 集成，任何 frozen edit 仅改既有文件并单独确认。

## 11. Owner 决策点

### A. Schema 策略

- A1（推荐）：新增 `quota_*` ledger 表。参考对照：New API 用 request 级 DB 记录保证预扣幂等与返还 `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/subscription.go:984`；LiteLLM 在预调用存储 reservation metadata 并成功/失败回补 `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/hooks/parallel_request_limiter_v3.py:2033`。HUAKAI 升级点是 PG tenant-scoped ledger，而不是 request metadata/cache。
- A2：扩展 `billing_ledger_claims` / `provider_accounts`。参考对照：Sub2API 将 account quota exhausted 纳入 account schedulability `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/account.go:124`；HUAKAI provider_accounts 已有 account quota columns `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/sql/migrations/0001_pool_routing.up.sql:139`。风险是把通用 quota 和 pool account 状态耦死。
- A3：先不加表，只 observe。参考对照：Portkey 可做 pre-request validator/cache-style limit `Portkey-AI/gateway@351692fd9236af222168134b416924fae0bdba23:src/handlers/handlerUtils.ts:406`；New API 钱路径会真实扣减 user/token 额度 `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:service/pre_consume_quota.go:71`。风险是 parity 的 F-OBS-001 2-phase reserve+settle 仍未达标。

### B. Reserve/settle 原子性

- B1（推荐两天窗口）：billing wrapper + compensation。参考对照：New API 有预扣/返还补偿路径 `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:service/pre_consume_quota.go:17`；LiteLLM 成功/失败 callback 都处理预留回补 `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/hooks/parallel_request_limiter_v3.py:2589` `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/hooks/parallel_request_limiter_v3.py:2741`。风险是短暂非单 Tx，需要 reconciliation。
- B2：重构 billing core 让 quota reserve 与 claim reserve 同 Tx。参考对照：New API 订阅预扣在 DB transaction 内锁定候选额度 `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/subscription.go:1006`；HUAKAI billing claim gate 现已自管 Tx `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/internal/billing/claim_gate.go:77`。这是正确性更强但风险更高。
- B3：observe-only reserve。参考对照：Sub2API 本地 precheck 可只跳过而不写冷却 `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/ratelimit_service.go:365`；Portkey 支持 check-only 令牌桶 `Portkey-AI/gateway@351692fd9236af222168134b416924fae0bdba23:src/shared/services/cache/utils/rateLimiter.ts:141`。风险是成本限额仍可绕过。

### C. Post-response actual > predicted

- C1（推荐）：已交付响应必须 billing commit；quota 记录 overage audit，并影响后续请求。参考对照：LiteLLM 成功时用 actual 对 reserved 做 delta 回补 `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/hooks/parallel_request_limiter_v3.py:2523`；New API post-usage 会更新 user/channel consumed quota `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:service/quota.go:225`。
- C2：严格 cap，actual 超 predicted 后标记 pending reconciliation 并人工处理。参考对照：HUAKAI usage_records 已有 pending reconciliation 字段 `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/sql/migrations/0002_observability_billing.up.sql:160`；LiteLLM 在 fallback 时也接受窗口内短期 counter divergence 有 TTL 边界 `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/hooks/parallel_request_limiter_v3.py:1088`。
- C3：拒绝响应/回滚 billing。不可推荐，因为 response 已交付后回滚会破坏 receipt/usage truth。

### D. 冷却来源

- D1（推荐）：只有上游错误 classifier 写 provider cooldown；本地 quota deny 只写 quota audit。参考对照：Sub2API 本地 precheck 明确不持久化为上游冷却 `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/ratelimit_service.go:367`；LiteLLM 冷却由 upstream status/error-rate 触发 `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/router_utils/cooldown_handlers.py:70`。
- D2：本地 quota exhausted 也写 provider cooldown。参考对照：Sub2API account schedulability 会把 account quota exhausted 作为不可调度 `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/account.go:124`；HUAKAI provider_accounts 有 quota_status `BloomingProsperity/HUAKAI@7d9dd8218283016b19f10947739f7a7a18490ab4:backend/sql/migrations/0001_pool_routing.up.sql:148`。风险是把“本地额度”误报成“上游冷却”。
- D3：冷却全 admin manual-first。风险是 F-RATE-001 parity 不足。

## 12. Fusion-upgrade delta

- 架构升级：HUAKAI 用 tenant-scoped PG quota ledger + billing wrapper 接入，不把 quota 混进 gatewayhttp/gateway/proto 冻结包；比参考项目的 middleware/cache/precheck 分散形态更容易审计、回滚和做 per-tenant receipt。
- 算法升级：统一 reserve/settle/release 处理 requests/cost/concurrency；reserved+settled 同时参与判定；actual-cost delta 和 reconciliation job 明确覆盖“billing 成功、quota settle 失败”这一真实钱路径风险。
- 生态升级：quota audit/reconciliation 与 F-RATE、billing receipts、Admin Ops 未来 policy CRUD 对齐；支持 enforce/observe/manual-first 逐步上线，不牺牲功能 parity。

## 13. Pre-execution checklist

1. Owner 在 A/B/C/D 决策点中确认默认选项，特别是 migration 0060 和 reserve 原子性。
2. 重新跑 `git status`，确认只改计划或已获准的实现文件。
3. 若进入实现，先写 migration/schema tests，再写 `quota` store/service。
4. 先跑 PG integration tests，再接 wiring。
5. Stage intended diff，跑 `codex exec review --uncommitted --full-auto --sandbox read-only`，S0/S1 必修，S2/S3 记录。

## 14. Open questions

- 是否允许两天窗口采用 wrapper + compensation，还是必须先做 billing core 单 Tx hook？
- channel hard limit 是 S0/S1 必须 hard enforce，还是在 route plan 无 channel ID 时先 pool_group hard-limit + channel observe？
- cost cap 的默认 scope 是 tenant/global 还是从 API key/user/channel 明确配置后才启用？
- overage audit 是否立即触发 admin alert / user-visible receipt 标记？
- quota policy CRUD 是否本 slice 只留 DB seed/manual SQL，Admin Ops UI 后置？
- 0060 migration 是否允许新增 down migration 删除 quota_* 表，还是 release 后按 append-only migration 原则只 forward fix？

Source files read:
- HUAKAI: `AGENTS.md`; `docs/RULES.md`; `backend/internal/billing/billing.go`; `backend/internal/billing/claim_gate.go`; `backend/internal/billing/settler.go`; `backend/internal/gatewayhttp/chat_completions_dispatch.go`; `backend/sql/migrations/0001_pool_routing.up.sql`; `backend/sql/migrations/0002_observability_billing.up.sql`; `backend/sql/migrations/0004_rate_limiting.up.sql`; `backend/sql/queries/pool_accounts.sql`; `backend/internal/pool/dispatcher/slot_manager.go`; `backend/internal/pool/binding/auth_credential_gate.go`.
- Sub2API: `backend/internal/service/ratelimit_service.go`; `backend/internal/service/concurrency_service.go`; `backend/internal/service/account.go`; `backend/internal/service/gateway_service.go`; `backend/internal/service/account_usage_service.go`.
- New API: `service/pre_consume_quota.go`; `service/quota.go`; `middleware/rate-limit.go`; `middleware/model-rate-limit.go`; `model/token.go`; `model/token_cache.go`; `model/subscription.go`; `model/user.go`.
- LiteLLM: `litellm/proxy/hooks/parallel_request_limiter_v3.py`; `litellm/proxy/hooks/max_budget_limiter.py`; `litellm/proxy/hooks/dynamic_rate_limiter_v3.py`; `litellm/router_strategy/budget_limiter.py`; `litellm/router_utils/cooldown_handlers.py`; `litellm/router.py`.
- Portkey gateway: `src/shared/services/cache/utils/rateLimiter.ts`; `src/shared/services/cache/index.ts`; `src/handlers/handlerUtils.ts`; `src/handlers/services/preRequestValidatorService.ts`; `src/globals.ts`; `conf.example.json`; `initializeSettings.ts`.

Lane: specifier
Agent: Codex GPT-5
UTC timestamp: 2026-05-28T08:22:29Z

中文总结：本计划的真实观察来自 HUAKAI billing/pool/migration 热路径和四个参考项目的配额/限速/并发/冷却源码区域；合理推断主要集中在 HUAKAI 应以 wrapper + PG ledger 方式把 quota 接到现有 ClaimGate/Settler，而不是把逻辑塞回冻结包或复制参考项目缓存实现；当前 open questions 为 6 个，最高优先级是 Owner 确认 0060 migration 与 reserve 原子性方案。
