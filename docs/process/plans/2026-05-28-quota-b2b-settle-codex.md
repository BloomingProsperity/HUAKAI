# 2026-05-28 HUAKAI 配额子系统切片 B2b settle/release/cache-hit 实施计划

| 项 | 内容 |
| --- | --- |
| Owner directive | “独立起草 HUAKAI 配额子系统切片 B2b(settle/release/cache-hit)的实施计划；只写计划文档，不写实现代码，不 commit/不 push。” |
| Scope | 只规划 `backend/internal/quota` 新增 service 层文件和 PG 集成测试文件；不写实现代码。 |
| Out of scope | 不碰 `billing_wrapper.go`、`cmd/gateway/wiring.go`、billing 包、冻结包 `gatewayhttp/gateway/proto`、migration、生产部署脚本和真实 secrets。 |
| Success criteria | 计划覆盖 `Service.Settle` / `Service.Release` / `Service.CommitCacheHit` 的事务编排、幂等、overage、补偿、测试、风险和参考对照；执行者可据此在 B2b 只新增 quota 包文件。 |
| Time estimate | 计划已完成；后续实现预计 0.5-1 天工程时间，PG 测试和故障注入另需 0.5 天。 |
| Blast radius | 未来实现若错误，会影响配额窗口、reservation 生命周期、并发槽释放、billing 成功后的 quota 补偿与 cache-hit 请求控制；本计划文档本身无运行时影响。 |
| Decision points | 见“开放决策点”。当前无阻塞 B2b 计划落文档的 Owner 决策。 |

## Clean-Room Metadata

- Lane: SPECIFIER。
- 本稿只输出行为/机制级计划；未复制参考项目源码、函数体、结构体字段、注释或代码块。
- 未读取 `docs/process/plans/2026-05-28-quota-b2b-settle-claude.md`。工作区只看到该文件存在于 `git status --short`，未打开内容。
- Observed regions: 26。
- Inferences: 9。
- Open questions: 3。
- 参考项目 freshness：本地 HEAD 均在 2026-05-28 前 90 天内；New API GitHub API 首查显示 `archived=false` 且 `pushed_at=2026-05-27T05:23:52Z`。其他项目的 GitHub 页面/API 在本会话只作为公开仓库可访问性和近期 release/commit 辅助确认，执行实现前若新增参考主张，应重新查 `archived=false` 与 `pushed_at`。

## 已定信封

- B2a `Reserve` 已完成，使用 Model B：request/cost 都以 `reserved_value + settled_value` 判定，`request_count` 只做观测。当前服务在一次 serializable tx 内锁 claim、解析策略、写 reservation、写窗口和 audit，并对 `40001` / `40P01` 整笔重试，见 `backend/internal/quota/service.go:65`、`backend/internal/quota/service.go:75`、`backend/internal/quota/service.go:95`、`backend/internal/quota/service.go:122`。
- 0070 migration 已锁：`quota_windows` 有 `reserved_value`、`settled_value`、`overage_value`、`request_count`、`version`，`quota_reservations` 有 `reserved/settled/released/expired/reconciliation_needed` 状态，见 `backend/sql/migrations/0070_quota_subsystem.up.sql:74`、`backend/sql/migrations/0070_quota_subsystem.up.sql:110`。
- B2b 必须使用已有 store 原语，不重造：`ApplyWindowSettlement`、`SettleReservation`、`ReleaseReservation`、`ReleaseConcurrencySlots`、`MarkReservationReconciliationNeeded`、`EnqueueReconciliationJob`、`GetReservationByClaimForUpdate`、`ResolvePolicies`、`UpsertWindow`、`GetWindowForUpdate` 已在端口中存在，见 `backend/internal/quota/store.go:14`、`backend/internal/quota/store.go:70`、`backend/internal/quota/store.go:101`、`backend/internal/quota/store.go:118`。
- store 的 PostgreSQL tx 已是 serializable，见 `backend/internal/quota/pg_store.go:50`；窗口 settlement SQL 会释放 reserved、增加 settled 和 overage，见 `backend/sql/queries/quota.sql:142`；reservation settle/release 只接受 `reserved` 或 `reconciliation_needed`，见 `backend/sql/queries/quota.sql:261`、`backend/sql/queries/quota.sql:275`。

## 新增文件计划

- 新增 `backend/internal/quota/service_settle.go`：只放 B2b service 方法、请求/结果类型、B2a retry 镜像、settlement/release/cache-hit 私有编排 helper、补偿 enqueue helper。该包不是冻结包。
- 新增 `backend/internal/quota/service_settle_integration_test.go`：只放 `integration_pg` 下的真实 PostgreSQL 判别测试，不 mock 钱、窗口或槽。
- 不新增 `backend/internal/gatewayhttp`、`backend/internal/gateway`、`backend/internal/proto` 文件；不修改 SQL/migration/wiring/billing wrapper。

## Public Service API

### `Service.Settle`

用途：billing 已成功提交后，quota 用独立 serializable tx 把 claim 从 reserved 迁到 settled。它是 B1 wrapper+补偿模型里的 quota 后半段；切片 C 再接 wiring。

输入类型建议命名为 `SettleRequest`，字段：
- `TenantID`、`ClaimID`、`ReservationID`：三者一起校验，claim 是幂等键，reservation ID 是调用方一致性保护。
- `ActualCost`：`decimal.Decimal`，禁止 float；持久化语义对齐 `numeric(20,8)`。
- `SettledAt`：默认可归一化为当前时间，但测试固定传入。
- `Actor`：可选 audit actor。

输出类型建议命名为 `SettleResult`，字段：
- `Reservation`：锁定并最终确认的 reservation 视图。
- `IdempotencyHit`：同 claim 二次 settle 时为 true。
- `OverageValue`：本次 cost overage。
- `ReconciliationQueued`：tx 失败后是否成功排入补偿队列。

### `Service.Release`

用途：上游未交付或调用方 abort，释放 reserved hold，不扣成本，释放本地并发槽。

输入类型建议命名为 `ReleaseRequest`，字段：
- `TenantID`、`ClaimID`、`ReservationID`。
- `Reason`：调用方传入受控 reason，计划使用 `abort`、`upstream_error`、`caller_cancelled`、`pre_billing_failure` 这类 service 自有值。
- `ReleasedAt`、`Actor`。

输出类型建议命名为 `ReleaseResult`，字段：
- `Reservation`、`IdempotencyHit`、`ReconciliationQueued`。

### `Service.CommitCacheHit`

用途：缓存命中已向用户交付响应，成本为零，但不是 abort。它应把 reservation 结算成成功状态并写 cache-hit audit，供未来 wrapper 在不打上游时调用。

输入类型建议命名为 `CacheHitRequest`，字段：
- `TenantID`、`ClaimID`、`ReservationID`。
- `CommittedAt`、`Actor`。
- 可选 `CacheKey` / `CacheSource` 仅进入 audit payload，不影响配额算法。

输出类型建议命名为 `CacheHitResult`，字段同 `SettleResult`，但 `OverageValue=0`。

## 事务编排

三条入口都镜像 B2a：

- 外层做最多 N 次 bounded retry，只对 PostgreSQL serialization/deadlock 类错误重试；重试单位必须是整笔 tx，而不是单个 SQL step。
- 每次尝试都通过 `s.withStore` 进入 store tx，继承 `backend/internal/quota/pg_store.go:56` 的 serializable。
- tx 第一件事用 `GetReservationByClaimForUpdate` 锁住 claim，claim 行锁是 settle/release/cache-hit 的幂等和并发边界，见 `backend/sql/queries/quota.sql:164`。
- 如果 `(tenant, claim)` 不存在，返回 fail-closed error；如果 `ReservationID` 不匹配，返回一致性错误并 audit，不尝试猜测。
- 如果状态已经是目标终态，返回幂等结果，不再写窗口、不再释放槽、不再写重复 money audit。
- 如果状态是 `reserved` 或 `reconciliation_needed`，执行窗口 settlement、reservation 状态迁移、并发槽释放和 audit；任一步失败则整笔 tx rollback。
- 如果状态是 `expired`，在 greenfield B2b 不扩大 SQL 状态机：返回 reconciliation-needed 结果并在 tx 外排补偿 job。理由是现有 `SettleReservation` / `ReleaseReservation` SQL 不接受 expired，擅自改 SQL 超出本切片边界。

## Settle 算法

核心规则：以 reservation 的 `Scopes` 在 settle 时重新解析窗口。这不是偏好，而是 0070 schema 约束：没有 per-window reservation 子表，reservation 只保存 scope/policy snapshot，窗口行本身按 policy+window 聚合。`Reservation` 当前持有 scopes/predicted/reserved/status，见 `backend/internal/quota/types.go:99`；窗口 counter 保存 reserved/settled/overage/request_count，见 `backend/internal/quota/types.go:140`。

执行顺序：

1. 锁 reservation 后，从 reservation 的 scopes 重新 `ResolvePolicies`，指标只取 requests 和 cost；concurrency 不参与窗口 settlement，只在最终释放槽。
2. 对每个会产生 reserve hold 的 request/cost 窗口，按 B2a 的 window 方式 `UpsertWindow` 后 `GetWindowForUpdate`，再调用 `ApplyWindowSettlement`。
3. request 窗口：`ReservedReleaseValue = reservation.ReservedUnits`，`SettledAddValue = 1`，`OverageAddValue = 0`。B2a 已在 reserve 阶段把 `request_count` 作为观测值加过一次，B2b 不再二次增加 `request_count`。
4. cost 窗口：`ReservedReleaseValue = reservation.PredictedCost`，`SettledAddValue = ActualCost`，`OverageAddValue = max(ActualCost - PredictedCost, 0)`。
5. 调 `SettleReservation` 写 `ActualCost`、`SettledUnits=1`、`OverageUnits=cost overage`；现有 store 会把 numeric 参数写到 reservation，见 `backend/internal/quota/pg_store.go:300`。
6. 调 `ReleaseConcurrencySlots(tenant, reservationID, "settled")`，避免成功响应后槽泄漏。store 端口已存在，见 `backend/internal/quota/store.go:29`。
7. 写 audit：至少包括 `settle_committed`；若 overage > 0，再写 `settle_overage` 或在 payload 中明确 overage。audit 视图支持 reserved/settled amount 和 payload，见 `backend/internal/quota/types.go:165`。

## Overage 定义

- C1 overage 是“响应已交付后 actual 超出 predicted 的差额”，不得事后拒绝本次响应。
- 数值定义：`cost_overage = max(actual_cost - predicted_cost, 0)`，精度为 `decimal.Decimal` 并映射到 PostgreSQL `numeric(20,8)`。
- 如果 actual 小于 predicted，overage 为 0；窗口释放 predicted hold，只把 actual 加到 settled。释放出的差额立即让后续请求看到更多余量。
- 如果 actual 大于 predicted，窗口 settled 增加 actual，overage 额外累计差额；后续 B2a reserve 因为读取 `reserved_value + settled_value`，会自然受到 actual settled 的影响，overage 字段用于 audit/UI/补偿分析。
- request metric 不产生 overage：一次成功响应只从 reserved 迁到 settled 1 个 request 单位。

## 幂等规则

- 同一 `(TenantID, ClaimID)` 是主幂等键，DB 也以 claim 锁行，见 `backend/sql/migrations/0070_quota_subsystem.up.sql:133`。
- `Settle` 二次调用：
  - 已是 `settled` 且 reservation ID 一致：返回 `IdempotencyHit=true`，不重复写窗口、不重复 overage、不重复释放槽。
  - 已是 `released`：如果调用语义是 billing 已成功后的 settle，则这是真实状态冲突，返回 reconciliation-needed；不得把 released 当成功 settle。
- `Release` 二次调用：
  - 已是 `released`：返回幂等命中，不重复释放窗口或槽。
  - 已是 `settled`：返回不可 release 的状态错误；响应已经交付并结算，不能再“退回” quota。
- `CommitCacheHit` 二次调用：
  - 已是 `settled` 且 audit/payload 表明 cache-hit 或 zero-cost success：返回幂等命中。
  - 已是 `released`：返回状态冲突并排补偿；缓存命中不是 abort。

## Release / Abort

`Service.Release` 是反向释放 reserved hold：

- request 窗口：release reserved 1，settled 0，overage 0。
- cost 窗口：release predicted cost，settled 0，overage 0。
- reservation 迁到 `released`，写 reason。
- 并发槽必须释放，reason 透传为受控 release reason。
- audit event 使用 `release_aborted` 或更具体的 abort reason；不得写成功 settle audit。
- 不扣成本、不写 overage、不把 release 伪装成 cache-hit。

参考对照：LiteLLM 在请求失败时会释放或失效已做的预算 hold，避免失败请求继续占预算；其失败路径从 failure callback 进入 release/失效逻辑，证据见 `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/hooks/proxy_track_cost_callback.py:50`、`BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/hooks/proxy_track_cost_callback.py:58`。New API 的预扣失败返回路径以负向 post-usage delta 归还预扣量，证据见 `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:service/pre_consume_quota.go:17`、`QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:service/pre_consume_quota.go:23`。

## Cache-Hit 语义

判断：缓存命中必须计入 requests 速率窗口，但 cost 为 0。

理由：

- 用户已获得一次响应，若不计 request 窗口，会允许缓存热键绕过 RPM/请求数限制。
- B2a 已在 reserve 时占用 request hold 并增加观测 `request_count`；cache-hit commit 只需把 request reserved 迁到 settled，不再增加第二个 `request_count`。
- cost 窗口释放 predicted hold 并 settled 0，避免 cache-hit 被扣钱。
- reservation 状态应是 `settled`，不是 `released`；audit event 必须区分 `cache_hit` 与 abort。

参考对照：LiteLLM 对缓存命中把响应成本视为零但仍走后续 spend/log 更新路径，证据见 `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/hooks/proxy_track_cost_callback.py:226`、`BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/hooks/proxy_track_cost_callback.py:241`；其持久更新也维护请求计数类字段，见 `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/db/db_spend_update_writer.py:1727`。New API 的统计把消费日志同时用于 spend sum 与近 60 秒请求/令牌计数，见 `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/log.go:451`、`QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/log.go:478`。Portkey Gateway 在缓存命中时直接返回缓存响应并记录缓存状态，说明 cache-hit 是一次完成的 gateway 请求而不是失败释放，见 `Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/middlewares/cache/index.ts:44`、`Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/handlers/services/logsService.ts:196`。

## 失败与补偿

失败分两类：

- `Release` 失败：主要风险是 reserved hold 或 concurrency slot 泄漏，不是钱已扣。
- `Settle` / `CommitCacheHit` 失败：响应已经交付，billing 可能已经成功，quota 失败会造成真实钱路径风险；必须进入 reconciliation。

tx 失败后的外部补偿策略：

1. 保留原始错误，不吞错。
2. 在 failed tx 之外，先 best-effort `MarkReservationReconciliationNeeded`。如果 status 已不允许标记，记录这个失败但继续 enqueue。
3. 再 best-effort `EnqueueReconciliationJob`，job kind 使用 service 自有值：`quota_settle_failed`、`quota_release_failed`、`quota_cache_hit_failed`。SQL 对 queued/running 同 claim/kind 做幂等合并，见 `backend/sql/queries/quota.sql:389`。
4. enqueue 成功时 result 标记 `ReconciliationQueued=true`；enqueue 失败时返回原错误并附带 enqueue 失败，调用方不能误以为已补偿。
5. 若失败发生在 serialization/deadlock 类错误内，先按 B2a 有界重试；只有重试耗尽后进入补偿。

参考对照：LiteLLM 的数据库更新失败后会释放已做预算 hold，counter 更新失败则使 counter 失效并终止本次预算对象，证据见 `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/hooks/proxy_track_cost_callback.py:482`、`BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/hooks/proxy_track_cost_callback.py:496`、`BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/hooks/proxy_track_cost_callback.py:524`。Sub2API 的 post-usage 侧把去重 claim、扣费/配额效果和 commit 放在一个事务中，并在 gateway 层使用 detached context 执行后置计费，见 `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/repository/usage_billing_repo.go:35`、`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/repository/usage_billing_repo.go:45`、`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/gateway_service.go:8109`。HUAKAI 不把 billing 与 quota 放同一 tx，是已批准 B1 wrapper+补偿模型，因此补偿队列是不可省略的安全阀。

## 测试计划：mutation-discriminating

所有钱、窗口、slot 风险测试用真实 PostgreSQL integration，不用 mock store。现有 reserve 测试已经写明“守住的缺陷 + mutation”，B2b 沿用同一纪律，见 `backend/internal/quota/service_integration_test.go:18`、`backend/internal/quota/service_integration_test.go:320`、`backend/internal/quota/service_integration_test.go:363`、`backend/internal/quota/service_integration_test.go:401`。

计划测试：

1. `TestServiceSettle_MovesPredictedToActualAndRecordsOverage`
   - 守住：actual cost 必须进入 settled，predicted hold 必须释放，overage 必须是 actual-predicted 的正差。
   - 判别 fixture：predicted=4.00000000，actual=6.50000000，cost limit 10；断言 cost reserved=0、settled=6.5、overage=2.5，request settled=1，reservation settled。
   - Mutation：用 predicted 写 settled、overage 固定 0、或 release 后不 settle，测试变红。

2. `TestServiceSettle_IdempotentSameClaimDoesNotDoubleCount`
   - 守住：同 claim 二次 settle 不双算钱、不双算 request、不重复 overage。
   - 判别 fixture：同一 claim 连续调用 settle；断言窗口值、reservation settle 时间、audit 数量不变，第二次 `IdempotencyHit=true`。
   - Mutation：已 settled 分支重跑 window settlement，测试变红。

3. `TestServiceRelease_AbortReleasesWindowsAndSlotsWithoutCost`
   - 守住：abort 只能释放 hold 和槽，不能产生 cost settled 或 overage。
   - 判别 fixture：同时 seed request/cost/concurrency policy；reserve 后 release；断言 active slot=0、窗口 reserved=0、settled=0、overage=0、reservation released。
   - Mutation：漏掉 `ReleaseConcurrencySlots`、把 release 当 settle 0 成本、或只释放 cost 不释放 request，测试变红。

4. `TestServiceRelease_IdempotentAfterReleasedDoesNotDoubleRelease`
   - 守住：release 重放不能把 reserved 扣成负数，也不能写第二条关键 audit。
   - 判别 fixture：第二次 release 后窗口值和 audit 数保持第一次结果。
   - Mutation：不检查状态直接重复 `ApplyWindowSettlement`，测试变红。

5. `TestServiceCommitCacheHit_CountsRequestButZeroCost`
   - 守住：cache-hit 是成功响应，request 迁 settled=1；cost settled=0；reservation settled；audit 是 cache-hit 不是 abort。
   - 判别 fixture：predicted cost 非零，cache hit 后 cost reserved=0、settled=0、request settled=1、status settled。
   - Mutation：把 cache-hit 走 `Release` 分支，request settled 会是 0 且 status released，测试变红。

6. `TestServiceCommitCacheHit_IdempotentDoesNotDoubleCount`
   - 守住：同 claim cache-hit 重放不重复 request settled。
   - 判别 fixture：重复调用 cache-hit；断言 request settled 仍为 1，cost 仍为 0。
   - Mutation：已 settled 分支重跑 settlement，测试变红。

7. `TestServiceSettle_UsesReservationScopesAtSettlement`
   - 守住：settle 必须用 reservation 保存的 scopes 重解析窗口，而不是使用空输入或 caller 临时 scopes。
   - 判别 fixture：reserve 时带 user/API-key scopes；settle request 不携带 scopes；断言对应窗口被释放和 settled。
   - Mutation：settle 从 request scope 解析或默认 tenant-only，测试变红。

8. `TestServiceSettle_RetryableTxConflictRetriesWholeTransaction`
   - 守住：`40001` / `40P01` 类冲突必须重试整笔 tx，且不能留下半次 window mutation。
   - 判别 fixture：用真实 PG 并发持锁/序列化冲突触发一次失败，再让第二次成功；断言最终只有一次 settled。
   - Mutation：无 retry 或只重试单步，测试变红或双算。

9. `TestServiceSettle_FailureQueuesReconciliationOutsideFailedTx`
   - 守住：billing 成功后 quota tx 失败必须排 reconciliation job。
   - 判别 fixture：在测试 DB 为特定 audit event 安装临时 trigger 抛错，强制 quota tx rollback；然后断言 reservation 仍未 settled，但 `quota_reconciliation_jobs` 有 queued job。cleanup trigger/function。
   - Mutation：失败后直接返回错误不 enqueue，测试变红。

10. `TestServiceSettle_ReconciliationEnqueueFailureIsNotSwallowed`
    - 守住：补偿队列也失败时，不能让调用方误以为补偿已安排。
    - 判别 fixture：测试 DB 同时让主 tx 失败，并让 reconciliation insert 对该 tenant/job kind 抛错；断言返回错误包含主失败和 enqueue 失败，`ReconciliationQueued=false`。
    - Mutation：吞掉 enqueue 失败，测试变红。

## What Could Go Wrong

- 幂等缺陷：同 claim 二次 settle/cache-hit 会双算钱或 request。
- 钱路径缺陷：billing 成功后 quota settle 失败但没 enqueue，导致账单和 quota 分叉。
- 槽泄漏：settle/release/cache-hit 后忘记释放 concurrency slots，后续请求被假性阻塞。
- cache-hit 绕过：把 cache-hit 当纯 release，会让热缓存绕过 request limit。
- overage 错误：actual>predicted 时只 settle predicted，后续窗口低估真实用量。
- decimal 错误：用 float 或字符串四舍五入不一致，破坏 `numeric(20,8)` 钱精度。
- policy drift：settle 时重解析窗口是 schema 约束，但若 reserve 后策略被禁用/改窗，旧 hold 可能难以精确回收；B2b 必须 audit 这类异常并排补偿，后续可考虑 per-window settlement map。
- expired 状态：现有 SQL 不允许直接 settle/release expired reservation；B2b 在 greenfield 边界内只能排补偿，不能偷偷改 SQL。

## Fusion-Upgrade Delta

### 架构

- LiteLLM 更偏运行时预算 hold + 数据库/计数器双写后的释放/失效处理；HUAKAI B2b 使用 PostgreSQL reservation ledger、serializable tx 和显式 reconciliation job，把失败收敛成可查询任务，参考 LiteLLM 成本更新失败后的释放/失效路径 `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/hooks/proxy_track_cost_callback.py:496`。
- New API 是预扣后按 actual-pre 执行增减 delta；HUAKAI 保留这个“先 hold 后 settle”的结果，但升级为多 scope、多窗口、多指标 reservation ledger，参考 New API 预扣和结算差额路径 `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:service/pre_consume_quota.go:66`、`QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:service/billing.go:37`。
- Sub2API 的 post-usage billing 有事务去重和 detached context；HUAKAI 采用 claim 幂等与后置执行思想，但保留 billing/quota 分账本和补偿边界，参考 `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/repository/usage_billing_repo.go:49`、`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/gateway_service.go:8229`。

### 算法

- HUAKAI 的升级点是 Model B 统一计数：request/cost 都通过 reserved->settled 两阶段窗口迁移，不把 request 只当 log，也不把 cost release 与 spend counter 分散到多个缓存。当前 B2a 判定已以 `reserved_value + settled_value` 为准，见 `backend/internal/quota/service.go:273`、`backend/internal/quota/service.go:293`。
- overage 不做事后拒绝，actual 全量进入 settled，overage 额外审计。这比只做 actual-pre delta 更适合多窗口 enforcement，因为未来 reserve 直接读取 settled 值。
- cache-hit 算法明确“request 成功、cost 为 0”：对比 Portkey 记录 cache status 与 LiteLLM 零成本但继续更新请求统计，HUAKAI 把这一语义落到 quota ledger，见 `Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/handlers/services/cacheService.ts:93`、`BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/db/db_spend_update_writer.py:1730`。

### 生态

- B2b 暴露纯 quota-service 方法，供切片 C 的 wrapper 接线；当前不改 gateway/billing/wiring，降低跨包风险。
- Reconciliation job 给 Admin Ops UI、审计报表和后台 worker 留出统一入口；SQL 已支持同 claim/kind queued/running 合并，见 `backend/sql/queries/quota.sql:414`。
- PG integration tests 直接覆盖钱、槽、窗口和补偿，避免 “mock store 通过但真实 SQL where/status 不一致” 的假信心。

## Pre-Execution Checklist

后续实现前按顺序确认：

1. 再次确认没有读取 Claude 平行稿。
2. 确认只新建 `backend/internal/quota/service_settle.go` 与 `backend/internal/quota/service_settle_integration_test.go`，若需要 helper，优先放同文件；不得给冻结包新增文件。
3. 复读 `backend/internal/quota/service.go` 的 retry/error helper，复用 B2a 语义。
4. 复读 `backend/sql/queries/quota.sql` 的 status where 条件，避免计划外修改 SQL。
5. 先写 PG integration 测试，确保每个测试有“守住缺陷 + mutation”注释。
6. 实现 service 方法后运行 quota 单元测试和 `integration_pg` 测试；若环境没有 PG，必须如实记录未跑。
7. Stage 后按 Owner 规则跑 per-commit Codex review；本任务当前只写计划，不 commit。

## 开放决策点

1. Cache-hit 是否计入 requests 速率窗口：本计划建议“计入”。参考项目对照显示 cache-hit 是成功响应而非失败释放，且有请求统计/缓存状态记录；若 Owner 想让 cache-hit 不计请求数，需要明示为产品策略例外，否则会形成热缓存绕过请求限额的风险。
2. Expired reservation 的 delivered-response settle：当前 greenfield 边界不改 SQL，而现有 SQL 不允许直接 settle/release expired。B2b 只能排 reconciliation；若 Owner 要求本切片直接 settle expired，需要批准改 SQL/store 状态条件，属于本切片边界外。
3. Policy drift 精确回收：Owner 已指定 settle 时按 reservation scopes 重解析窗口，这是 0070 无 per-window 子表下的唯一路径。若未来要完全解决策略变更后的旧 hold 回收，需要新增 per-window settlement map 或 worker 规则，属于后续 schema/worker 决策。

## Source Coverage Proof

- HUAKAI service reserve tx/retry/idempotency：`backend/internal/quota/service.go:65`、`backend/internal/quota/service.go:75`、`backend/internal/quota/service.go:95`、`backend/internal/quota/service.go:122`。
- HUAKAI Model B 判定：`backend/internal/quota/service.go:273`、`backend/internal/quota/service.go:293`。
- HUAKAI store/type/schema/SQL：`backend/internal/quota/store.go:14`、`backend/internal/quota/types.go:99`、`backend/internal/quota/types.go:140`、`backend/internal/quota/pg_store.go:50`、`backend/internal/quota/pg_store.go:300`、`backend/sql/migrations/0070_quota_subsystem.up.sql:74`、`backend/sql/migrations/0070_quota_subsystem.up.sql:110`、`backend/sql/queries/quota.sql:142`、`backend/sql/queries/quota.sql:164`、`backend/sql/queries/quota.sql:261`、`backend/sql/queries/quota.sql:275`、`backend/sql/queries/quota.sql:389`。
- HUAKAI test quality examples：`backend/internal/quota/service_integration_test.go:18`、`backend/internal/quota/service_integration_test.go:320`、`backend/internal/quota/service_integration_test.go:363`、`backend/internal/quota/service_integration_test.go:401`。
- Reference LiteLLM production regions: `litellm/proxy/hooks/proxy_track_cost_callback.py`、`litellm/proxy/spend_tracking/budget_reservation.py`、`litellm/proxy/db/db_spend_update_writer.py`。
- Reference New API production regions: `service/pre_consume_quota.go`、`service/billing.go`、`model/log.go`。
- Reference Sub2API production regions: `backend/internal/repository/usage_billing_repo.go`、`backend/internal/service/gateway_service.go`。
- Reference Portkey Gateway production regions: `src/middlewares/cache/index.ts`、`src/handlers/services/cacheService.ts`、`src/handlers/services/logsService.ts`。

Source files read: backend/internal/quota/service.go; backend/internal/quota/store.go; backend/internal/quota/types.go; backend/internal/quota/pg_store.go; backend/internal/quota/service_integration_test.go; backend/sql/queries/quota.sql; backend/sql/migrations/0070_quota_subsystem.up.sql; /home/codex/refs/litellm/litellm/proxy/hooks/proxy_track_cost_callback.py; /home/codex/refs/litellm/litellm/proxy/spend_tracking/budget_reservation.py; /home/codex/refs/litellm/litellm/proxy/db/db_spend_update_writer.py; /home/codex/refs/new-api/service/pre_consume_quota.go; /home/codex/refs/new-api/service/billing.go; /home/codex/refs/new-api/model/log.go; /home/codex/refs/sub2api/backend/internal/repository/usage_billing_repo.go; /home/codex/refs/sub2api/backend/internal/service/gateway_service.go; /home/codex/refs/portkey-gateway/src/middlewares/cache/index.ts; /home/codex/refs/portkey-gateway/src/handlers/services/cacheService.ts; /home/codex/refs/portkey-gateway/src/handlers/services/logsService.ts

Lane: specifier

Agent: GPT-5 Codex

UTC timestamp: 2026-05-28T13:04:30Z
