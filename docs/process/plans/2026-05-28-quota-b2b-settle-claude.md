# 配额 B2b settle/release/cache-hit 实施计划 — Claude 独立稿 (2026-05-28)

> CLAUDE.md #10 平行计划法 Claude 一侧,独立成文未参考 codex 稿。分支 `work/quota-subsystem`。
> 上游已批准:子系统范围=codex 全稿一次到位;原子性=B1 wrapper+补偿;overage=C1 commit+audit+影响后续。
> B2b 只实现 **service 层 settle/release/cache-hit 编排**,在已批准设计的信封内执行,不引入新 Owner 决策。

## 0. 边界(greenfield + 已批准信封)

- **In**:`backend/internal/quota` 新文件 `service_settle.go` —— `Service.Settle / Service.Release / Service.CommitCacheHit` 三个方法,复用切片 B 已实现的 store 方法(`SettleReservation` / `ReleaseReservation` / `ApplyWindowSettlement` / `ReleaseConcurrencySlots` / `MarkReservationReconciliationNeeded` / `EnqueueReconciliationJob`)。
- **Out(推迟,动现有文件)**:`billing_wrapper.go`(实现 `billing.Settler`)+ `cmd/gateway/wiring.go` 接线 = 切片 C,触碰冻结/现有文件,等新机落定。B2b 方法是纯 quota-service,供未来 wrapper 调用。
- **不动**:migration 0070(已锁,`quota_windows` 有 reserved/settled/overage/request_count 列)、冻结包、billing。

## 1. 算法(镜像 B2a reserve 写入,反向)

B2a reserve 写入回顾:requests `IncrementWindowReserved(ReserveDelta=1, RequestCountDelta=1)`;cost `IncrementWindowReserved(ReserveDelta=predicted, RequestCountDelta=0)`;concurrency `AcquireConcurrencySlot`。

### Settle(billing 已提交后的独立结算)

单 serializable tx(走 `withStore` + 复用 B2a 的 40001/40P01 有界重试):

1. `GetReservationByClaimForUpdate(tenant, claim)`。
   - `ErrNoRows` → 返回 typed `ErrReservationNotFound`(quota 无预留,caller/wrapper 决定;不 panic、不放行错账)。
2. **幂等**:`status==settled` → 直接返回 already-settled 结果,不二次施加。
3. `status==released/expired`(lease 已被 sweeper 释放,settle 迟到)→ **divergence**:`MarkReservationReconciliationNeeded` + audit + 返回 `DecisionRequiresReconciliation`;不重复施加 settled(预留 hold 已被释放,再加会双算)。
4. `status==reserved`(正常)→ 按 `reservation.Scopes` 在 `req.At` **重解析** policies(`ResolvePolicies`,与 reserve 同路径,settle 紧随 reserve 故窗口同期)。对每条 enforce 窗口:
   - **requests**:`ApplyWindowSettlement(ReservedReleaseValue=1, SettledAddValue=1, OverageAddValue=0)` —— 请求已发生,从 reserved 移到 settled,窗口总量不变(仍计 1 次)。
   - **cost**:`ReservedReleaseValue=reservation.PredictedCost`,`SettledAddValue=actualCost`,`OverageAddValue=overage`。
     - overage 定义:`overage = max(0, (window.SettledValue_after) - policy.LimitValue)`,其中 `SettledValue_after = window.SettledValue + actualCost`。即实际结算把 settled 推过 limit 的部分。C1:**commit 不拒**,记 overage + audit,后续 reserve 看到 settled≥limit 自然 deny。
5. `ReleaseConcurrencySlots(tenant, reservationID, reason="settled")`。
6. `SettleReservation(Settlement{status→settled, ActualCost, SettledUnits, OverageUnits, SettledAt=req.At})`。
7. `InsertAuditEvent(EventType="reserve_settled", AmountReserved=predicted, AmountSettled=actual, payload 含 overage)`。
8. commit。
   - tx 内 PG 错(非 40001):rollback;耗尽重试后在 **失败 tx 之外** best-effort `EnqueueReconciliationJob(kind="settle")` + `MarkReservationReconciliationNeeded`,返回 `RequiresReconciliation` error。绝不吞错。

### Release(abort:上游失败/客户端取消,无成本)

单 tx:

1. lock reservation。`status==released` → 幂等 no-op 返回。
2. `status==reserved` → 重解析窗口,对每条反向释放 reserved hold:`ApplyWindowSettlement(ReservedReleaseValue=<该 metric reserved 量: requests=1 / cost=predicted>, SettledAddValue=0, OverageAddValue=0)`。**不加 settled、不计 overage、不扣成本**。
3. `ReleaseConcurrencySlots(reason="aborted")`。
4. `ReleaseReservation(status→released, reason)`。
5. audit `reserve_released`。
6. 失败 → reconciliation job(kind="release")。

### CommitCacheHit(L2 缓存命中:成功路径,零成本)

等价 settle 但 `actualCost=0` 且 audit 区分:

1. 同 settle 步 1-3 的 reserved→settled 迁移,但 cost 窗口 `SettledAddValue=0`(零成本),requests 窗口 `ReservedReleaseValue=1, SettledAddValue=1`(**缓存命中仍是一次已服务请求,计入速率窗口** —— 见 §4 决策默认)。
2. `ReleaseConcurrencySlots(reason="cache_hit")`。
3. `SettleReservation(ActualCost=0, status→settled)`。
4. audit `EventType="cache_hit"`(成功,**非 aborted**)。
5. 失败 → reconciliation job(kind="cache_hit_settle")。

## 2. 类型(types.go 已有,不新增 migration)

- `Settlement{TenantID, ReservationID, ClaimID, ActualCost, SettledUnits, OverageUnits, SettledAt}` ✓ 已存在。
- `ReservationRelease{TenantID, ReservationID, ClaimID, Reason}` ✓。
- 新增请求/结果类型(service_settle.go 内):`SettleRequest{TenantID, ClaimID, ActualCost, At}`、`SettleResult{Settled bool, IdempotencyHit bool, Reservation, Decision}`、`ReleaseRequest`、`CacheHitRequest`。

## 3. 测试计划(mutation-discriminating #14,真 PG)

| 测试 | 守的缺陷 | Mutation(变红) |
|---|---|---|
| T1 SettleUsesActualNotPredicted | settle 用 predicted 不用 actual → 配额账漂移 | 用 predicted → settled≠actual → 红 |
| T2 SettleOverageCommitsAndAudits | actual 推过 limit 时拒/不记 overage(违 C1) | cap/拒 → 红;不写 overage audit → 红 |
| T3 SettleIdempotent | 同 claim settle 两次双算 | 去幂等 → settled 翻倍 → 红 |
| T4 AbortReleasesConcurrencySlots | abort 不释放并发槽 → 槽泄漏 | 删 ReleaseConcurrencySlots → 同 scope 第二请求被拒 → 红 |
| T5 AbortReversesReservedNoCost | abort 扣成本/不回退 reserved | abort 加 settled → window settled>0 → 红 |
| T6 CacheHitZeroCostReleasesConcurrency | 缓存命中记成 aborted 或漏释放/记了成本 | 当 abort → audit=aborted → 红;cost≠0 → 红 |
| T7 SettleFailureEnqueuesReconciliation | quota settle 失败被吞 → 钱账不一致无补偿 | 注入 PG 失败,断言 reconciliation job 出现;吞错 → 无 job → 红 |
| T8 SettleAfterReleaseGoesReconciliation | released 后 settle 迟到却重复施加 | 重复施加 settled → 双算 → 红 |
| T9 NoFloatingMoney | cost 用 float | 0.1+0.2 边界 → 红 |

- 真 PG(scratch DB migrate→0070),不 mock,钱风险活在真依赖(feedback_risk_based_testing)。
- 自证测试:T1 同测内对比 actual-settle 与 predicted-settle 结果不同。

## 4. 被 schema/已批准信封约束的默认(非新 Owner 决策,但记录假设)

- **重解析 vs 记录窗口**:0070 无 reservation-windows 子表 → settle 只能按 `reservation.Scopes` 重解析窗口。被 schema 约束,非开放选择。settle 紧随 reserve 故窗口同期;跨期 divergence 由 reconciliation 兜底。
- **O1 缓存命中是否计入 requests 速率窗口**:默认 **计入**(缓存命中也是一次已服务请求,占 1)。理由:用户视角发了请求;若不计,缓存可绕过速率限额。可被 Owner 翻。
- **O2 settle-after-release**:默认走 reconciliation(不重复施加)。
- **O3 overage 精度**:`max(0, settled_after - limit)`,decimal numeric(20,8)。

## 5. blast radius / what-could-go-wrong

- 钱:settle delta 错 → quota 视图账漂移(billing 是独立钱权威,不会双扣,但配额准入会误放/误拒)。缓解:decimal、reconciliation、真 PG delta 测试。
- 并发泄漏:settle/abort/cachehit 漏释放槽。缓解:T4/T6 + lease sweeper 兜底。
- 幂等:重复 settle 双算。缓解:status 机 + T3。
- 失败吞错:billing 成功 quota settle 失败无补偿。缓解:reconciliation job + T7(对齐 codex 子系统计划 §5.4 + §8)。

## 6. fusion-upgrade delta(三维)

- **架构**:settle/release/cachehit 与 reserve 同包同 store 抽象,reserved/settled/overage 三计数器在 `quota_windows` 单表;比 new-api(cache+DB 双层异步扣账,`service/quota.go`)更易审计、PG 为单一真相。
- **算法**:reserved→settled 迁移 + actual-predicted delta + overage commit(C1),对齐 litellm 成功时 actual-minus-reserved 回补(`parallel_request_limiter_v3.py` 成功回调),升级点=overage 不丢、走 PG ledger + reconciliation 而非内存 counter。
- **生态**:settle/abort/cachehit/reconciliation 全程 audit(`quota_audit_events`),为 admin 可视化 + receipt 打底;reconciliation job 覆盖"billing 成功 quota 失败"真实钱路径风险。

## 7. Source files read

HUAKAI(SPECIFIER lane,本包自有代码非参考项目):`backend/internal/quota/{store,types,service,reservation}.go`、子系统已批准计划 `docs/process/plans/2026-05-28-quota-subsystem-codex.md` §5/§8/§11。
参考项目对照见子系统 codex 稿 §5/§8(litellm 成功回补 / new-api 预扣返还 / sub2api post-usage),本 B2b 在其已 cite 的信封内,不新增参考读取。

Lane: specifier(仅读 HUAKAI 自有代码)
Agent: Claude Opus 4.7
UTC: 2026-05-28
