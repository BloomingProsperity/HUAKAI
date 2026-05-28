# Plan: S1-013 Durable Held Balance + Atomic Pre-deduction

## Scope
- 只做缺陷 S1-013 的实现方案，不改代码，只产出计划文件。
- 覆盖以下最小目标集合：
  - 在 Tx1 中加入“同一事务内、行锁顺序固定”的额度预留与扣减前置逻辑。
  - 引入持久化 `held` + 按 `claim_id` 的持有明细。
  - 在 Tx2 中按实际花费捕获额度并释放剩余保留。
  - 新增过期索赔的后台回收入口（orphan sweep）。
  - 评估并给出对 one-api/new-api 的参考对比与差异。
  - 明确测试与迁移方案。

## Success criteria
- 当 1000 个并发同用户请求、余额只够 K 次预测消费时，最终仅 K 次 `Reserve` 成功并返回可执行 claim，剩余为 402。
- Tx1/Tx2 两阶段在无并发冲突时通过；并发冲突下依旧满足幂等与无重复放款。
- 新增 6-9 条表级不变量（持有不为负、金额不超限、claim-idempotency 不变）可在回归中验证。
- 执行孤儿回收后，`claim_id` 持有行与 `held` 必须只减一次，且可多次调度幂等。
- `balanceheld` 系列表（balance、claim 持有、quota 桶）在 0060 迁移中建立索引、约束、版本号，且 down migration 可回滚。

## Blast radius
- 影响 `backend/internal/billing` 的 Tx1/Tx2（claim_gate.go、settler.go）与 SQL 查询层 `backend/sql/queries/billing_claims.sql` / `backend/sql/queries/billing_settle.sql`。
- 增加新包 `backend/internal/balancehold`（非冻结）。
- 新增迁移 `backend/sql/migrations/0060_durable_balance_holds.{up,down}.sql`。
- 需要在网关启动链路新增一个轻量 sweep worker（`backend/cmd/gateway` runtime/wiring）。
- 风险集中在计费一致性与并发；不会触及认证、支付接入方、数据库连接框架。

## Failure modes + mitigations
- 失败场景：`UPDATE ... SET held = held + :cost WHERE balance - held >= :cost` 返回 0 行（余额不足）。
  - 缓解：Tx1 返回专用资金不足错误；上层映射到 HTTP 402。
- 失败场景：Tx1 中断时部分更新未提交。
  - 缓解：全部在 Tx1 内完成并在错误时回滚。
- 失败场景：`Capture` 重复执行导致重复扣款。
  - 缓解：`balance_holds` 按 `claim_id` 记录 state，`Capture` 仅在 `reserved` 执行一次；后续调用直接返回已捕获状态。
- 失败场景：`Release` 被重复触发。
  - 缓解：`Release` 仅对 `reserved` 状态变更；非 reserved 状态 no-op。
- 失败场景：claim 成功后未 settle 的 orphan。
  - 缓解：后台按 `status='reserving' AND lease_expires_at < now()` 与未清理 hold 的 claim 进行回收；回收过程要求“每个 claim 单次释放”。
- 失败场景：跨租户误写。
  - 缓解：所有查询和索引都带 `tenant_id` 约束，避免横向泄漏。

## Exact migration 0060 DDL (up)
```sql
-- 0060 Durable balance holds and quota bucket model for atomic pre-deduction.
BEGIN;

CREATE TABLE IF NOT EXISTS user_balances (
    tenant_id    bigint      NOT NULL REFERENCES tenants(id),
    user_id      bigint      NOT NULL,
    balance      numeric(20,8) NOT NULL DEFAULT 0,
    held         numeric(20,8) NOT NULL DEFAULT 0,
    version      bigint      NOT NULL DEFAULT 0,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT user_balances_pk PRIMARY KEY (tenant_id, user_id),
    CONSTRAINT user_balances_non_negative_balance CHECK (balance >= 0),
    CONSTRAINT user_balances_non_negative_held CHECK (held >= 0),
    CONSTRAINT user_balances_held_lte_balance CHECK (held <= balance)
);
CREATE INDEX IF NOT EXISTS idx_user_balances_tenant_user_updated ON user_balances (tenant_id, updated_at);

CREATE TABLE IF NOT EXISTS quota_buckets (
    tenant_id      bigint      NOT NULL REFERENCES tenants(id),
    owner_type     text        NOT NULL,
    owner_id       bigint      NOT NULL,
    bucket_key     text        NOT NULL,
    bucket_start   timestamptz NOT NULL,
    limit_amount   numeric(20,8),
    used_amount    numeric(20,8) NOT NULL DEFAULT 0,
    reserved_amount numeric(20,8) NOT NULL DEFAULT 0,
    updated_at     timestamptz NOT NULL DEFAULT now(),
    version        bigint      NOT NULL DEFAULT 0,
    CONSTRAINT quota_buckets_pk PRIMARY KEY (tenant_id, owner_type, owner_id, bucket_key),
    CONSTRAINT quota_buckets_non_negative_used CHECK (used_amount >= 0),
    CONSTRAINT quota_buckets_non_negative_reserved CHECK (reserved_amount >= 0)
);
CREATE INDEX IF NOT EXISTS idx_quota_buckets_tenant_owner ON quota_buckets (tenant_id, owner_type, owner_id);
CREATE INDEX IF NOT EXISTS idx_quota_buckets_reservation_cleanup ON quota_buckets (tenant_id, bucket_key, updated_at);

CREATE TABLE IF NOT EXISTS balance_holds (
    tenant_id      bigint      NOT NULL REFERENCES tenants(id),
    claim_id       bigint      NOT NULL REFERENCES billing_ledger_claims(id) ON DELETE CASCADE,
    user_id        bigint      NOT NULL,
    state          text        NOT NULL DEFAULT 'reserving',
    predicted_cost numeric(20,8) NOT NULL CHECK (predicted_cost >= 0),
    actual_cost    numeric(20,8),
    reason         text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT balance_holds_pk PRIMARY KEY (tenant_id, claim_id),
    CONSTRAINT balance_holds_state CHECK (state IN ('reserving','captured','released'))
);
CREATE INDEX IF NOT EXISTS idx_balance_holds_tenant_state ON balance_holds (tenant_id, state, updated_at);
CREATE INDEX IF NOT EXISTS idx_balance_holds_user_state ON balance_holds (tenant_id, user_id, state);

COMMIT;
```

## Exact migration 0060 DDL (down)
```sql
BEGIN;
DROP TABLE IF EXISTS balance_holds;
DROP TABLE IF EXISTS quota_buckets;
DROP TABLE IF EXISTS user_balances;
COMMIT;
```

## balancehold package API + key SQL query shapes
**Package:** `backend/internal/balancehold`（新建，不属于冻结包）

### Public API
- `Reserve(ctx context.Context, tx pgx.Tx, tenantID, userID int64, claimID int64, predictedCost decimal.Decimal) error`
- `Capture(ctx context.Context, tx pgx.Tx, tenantID int64, claimID int64, actualCost decimal.Decimal) error`
- `Release(ctx context.Context, tx pgx.Tx, tenantID int64, claimID int64) error`
- 建议新增 helper：`GetHoldState(ctx, tx, tenantID, claimID) (state string, predicted, actual decimal.Decimal, ok bool, err error)`（仅内部使用）。

### 关键 SQL shape（与 sqlc 适配）
- `INSERT`/`UPDATE` 方式（伪 SQL）：
```sql
INSERT INTO balance_holds (tenant_id, claim_id, user_id, state, predicted_cost)
VALUES ($1,$2,$3,'reserving',$4)
ON CONFLICT (tenant_id, claim_id)
DO UPDATE SET updated_at = now(), predicted_cost = EXCLUDED.predicted_cost
RETURNING state, predicted_cost;
```
- 预留原子更新（核心）:
```sql
WITH locked AS (
  SELECT tenant_id, user_id, balance, held
  FROM user_balances
  WHERE tenant_id=$1 AND user_id=$2
  FOR UPDATE
)
UPDATE user_balances
SET held = held + $3,
    version = version + 1,
    updated_at = now()
WHERE tenant_id=$1 AND user_id=$2
  AND (balance - held) >= $3;
```
- 捕获时：
  1) 将 hold 状态转 `captured`（仅 `reserving`）并写 `actual_cost`；
  2) `user_balances` 做
`balance = balance - :actual, held = held - :predicted`，且需要持久性等于：仅当该 claim 当前 hold state 为 `reserving` 时生效。 
- 释放时：
  1) 将 hold 状态转 `released`（仅 `reserving`）；
  2) `user_balances.held -= predicted`（仅在 hold 为 reserving）。
- 可选：`quota_buckets` 的同模式更新作为 Tx1 的第二阶段（owner/resource/bucket）预留，当前切片优先实现 user + claim hold，后续补齐其它 bucket。

### SQL 文件约束
- 在 `backend/sql/balancehold/*.sql` 增加持久查询（`reserve.sql`, `capture.sql`, `release.sql`, `find_hold.sql`）并通过 `sqlc generate` 产出 `backend/internal/db/balancehold/*.sql.go`。

## Tx1 integration (file:line + changes)
- **Edit file:** `backend/internal/billing/claim_gate.go:71-174`
- 保持现有 `Serializable` 事务与幂等流程（`GetClaimByIdempotency` → `ReReserveAbortedClaim` → `InsertClaim`）。
- 在 `InsertClaim` 成功后、提交前插入 `balancehold.Reserve(...)`。
- 约束顺序：
  1. claim row 行锁（已经由 `GetClaimByIdempotency` / `GetClaimByIdempotency` + 插入路径维持）
  2. 调用 balancehold reserve 时对 `user_balances` 进行行锁更新
  3. 预留成功后再尝试 `quota_buckets`（若本切片范围内完成）
- 余额不足时：
  - 删除插入的 claim（回滚）
  - 将 `Err...` 映射为网关层 402（而非 200/500）。
- 参考现有 Tx1 约束顺序注释与失效路径：`backend/internal/billing/claim_gate.go:63-74,85-90,143-174` 和 `docs/specs/observability-billing.md:47-57`。

## Tx2 integration (file:line + changes)
- **Edit file:** `backend/internal/billing/settler.go:77-252`
- 在 Tx2 里，在 `billing_event` / `usage_record` / `UpdateClaimCommitted` 前后插入余额收敛调用：
  - 成功结算：`balancehold.Capture(ctx, tx, claim.TenantID, claim.ID, actualCost)`。
  - 失败/中断路径（`Abort`）先后再加入 `balancehold.Release`，保持 `usage_record` 与 `billing_event` 写入的业务语义。
- 使 `Settle` 的返回 `NewUserBalance` 使用 `capture` 后实际余额（从更新结果返回）。
- 幂等保障：若 `claim` 已被重复 settle/replay，`Capture` 应无副作用（仅第一次变更）。
- 在 `Abort` 路径（`backend/internal/billing/settler.go:254-337`）中增加同样的幂等释放：释放一次即可，重复调用不再变更。 

## Lock ordering
- 严格固定顺序：
  1. `billing_ledger_claims`（claim row, existing lock in Tx1/Tx2）
  2. `user_balances`（`SELECT ... FOR UPDATE`）
  3. `quota_buckets`（按 owner/bucket 锁定行；若实现）
  4. 既有 pool/provider/user usage 更新按现有顺序执行。
- 在文档里保持该顺序与 `docs/specs/observability-billing.md:47-57` 一致，避免死锁。

## Lease sweep design
- **Where it lives:** 新增 `internal/balancehold/sweeper.go`（worker 程序）与 `internal/balancehold/sweeper_test.go`。
- **Query:**
```sql
SELECT blc.tenant_id, blc.id AS claim_id, blc.user_id, bh.predicted_cost
FROM billing_ledger_claims blc
JOIN balance_holds bh ON bh.tenant_id = blc.tenant_id AND bh.claim_id = blc.id
WHERE blc.status = 'reserving'
  AND blc.lease_expires_at < now()
  AND bh.state = 'reserving';
```
- 对每个命中 claim 调用 `balancehold.Release`。
- **Schedule:**
  - 在 `backend/cmd/gateway/wiring.go` 创建并启动 sweeper（`replayJanitor` 同时段）
    - 推荐启动点：`buildDeps` 内 `replayJanitor.Start(ctx)` 旁边。
  - 关闭时在 `backend/cmd/gateway/lifecycle.go` 释放；新增 `gatewayRuntime.balanceHoldSweepStop` 并接入 `close()` / `shutdownGateway` 顺序。
  - 调度周期建议 `1m`（与 claim lease 默认 `90s` 对齐，可配置）。
- **Sweep idempotency checks:** `Release` 本身幂等 + `WHERE state='reserving'` 防多次执行。

## Discriminating test list
1. **TestConcurrentOverspendReservation**（`backend/internal/billing/claim_gate_integration_test.go`，新增）
   - `N=100` 并发，单用户余额只支持 `K` 次，`predictedCost=1`。
   - 断言：`success == K`，`claim` 唯一性成立，`insufficient` 次数为 `N-K`。
   - 断言 final `user_balances.held == K`。
   - Mutation check（必须红）：把预留条件从 `(balance - held) >= :cost` 改成恒真后，`success > K`。

2. **TestCaptureIdempotentOnce**（`backend/internal/billing/settler_integration_test.go`）
   - 同一 `claim_id` 调用 `Capture` 两次。
   - 断言：`user_balances.balance` 仅减少一次、`user_balances.held` 与 `billing_ledger_claims.actual_cost` 恢复/一致，不新增二次 `claim_committed`。
   - Mutation check：去掉 hold-state guard 后第二次会重复扣款。

3. **TestReleaseIdempotentAndOrphanSweepOnce**（`backend/internal/billing/settler_integration_test.go` + 新 `balancehold/sweeper_integration_test.go`）
   - 构造 `status='reserving'` 的 claim 与 hold（`lease_expires_at` 已过期），重复触发 sweep。
   - 断言：`claim` 仍 `reserving`/未重复 settle；`balance_holds.state='released'`，`user_balances.held` 恢复为 0；第二次 sweep 不改变行数。
   - Mutation check：若 sweep 不带幂等条件，则第二次 sweep 会让 held 变成负值或报约束失败。

4. **TestClaimGateToSettlerFlowWithHoldCaptured**（`backend/internal/billing/settler_integration_test.go`）
   - 先 Tx1 保留，再 Tx2 实际成本小于预测，断言：`balance=原始 - actual`，`held=原始-actual`（剩余 hold 捕获到实际）。
   - Mutation check：若 capture 直接用 predicted 代替 actual，断言期望失败。

5. **TestAbortReleasesHold**（`backend/internal/billing/settler_integration_test.go`）
   - Tx1 后立即 `Abort`。
   - 断言 `billing_ledger_claims.status='aborted'` 且 `balance_holds.state='released'`、`user_balances.held` 减零。
   - Mutation check：缺失 Abort release 会导致 held 残留。

## Reference comparison table
| 项目 | one-api（`songquanpeng/one-api@8df4a26:relay/controller/helper.go:68-95, text.go:46-87`） | new-api（`QuantumNous/new-api@20d3e73:service/pre_consume_quota.go:31-79`） | HUAKAI delta | Dimension |
|---|---|---|---|---|
| 预扣与回退模型 | 上游控制流在请求前先做预测额度估算并尝试立即扣减，失败即拒绝；失败分支再返还预扣额度 | 在请求前先判断 `user quota - preConsumed >= 0`，通过后进行预扣并同步减少余额；响应失败分支做按量回退（异步） | HUAKAI 不再做内存级瞬态 quota，而是改为 DB 持久化 `user_balances.held` + `balance_holds`，通过 Tx1/Tx2 保证并发扣款不会超卖 | 架构 / 算法 |
| 幂等/异常收敛 | one-api 通过 `preConsumed` 与 `returnPreConsumedQuota` 做失败补偿，未见“按 claim 生命周期”持久化回收痕迹；new-api 同理是请求 ID 维度的预扣与 `returnPreConsumed`，并结合失败异步调整 | 两者都存在“先预扣后回收”，但与 HUAKAI 的 claim_id 持久 hold 解耦：本项目以 claim 为核心单元，Capture/Release 全依赖 per-claim 状态与 Tx2 幂等性 | 架构 |
| 资金超发防护 | 均基于请求内余额判断和扣减，缺少可持久 hold 的 claim 约束路径 | 无 claim 生命周期绑定的持有行；失败回滚依赖逻辑补偿 | HUAKAI 明确要求 claim 行和 hold 行都可重放幂等且可回收（并发场景下仍不超预留） | 架构 / 算法 |

## Explicit decision points for Owner
- 决策点 1：本切片是否在 0060 里把 `quota_buckets` 做为完整 5 维预留（用户/APIKey/订阅/Provider/rate-window）启动，还是先实现 `user_balances + balance_holds` 后再逐步补齐？
- 决策点 2：Sweeper 是否与 gateway runtime 内联常驻 worker（同进程）还是拆分为独立管理进程；当前提案优先同进程以最小变更接近现有 `ReplayJanitor`。
- 决策点 3：`NewUserBalance` 返回值是否作为运行时观测字段继续透传（保留接口兼容）或改为占位字段并在后续切片补充。

## Reference check points (quick)
- claim path evidence（当前缺陷）: `backend/internal/billing/claim_gate.go:71-174`
- settle path evidence（当前缺失）：`backend/internal/billing/settler.go:77-252`、`backend/sql/queries/billing_settle.sql:6-33`
- migration evidence（缺少 held）：`backend/sql/migrations/0002_observability_billing.up.sql:19-60`
- startup/workers evidence（可插入 sweep）：`backend/cmd/gateway/wiring.go:345-490`、`backend/cmd/gateway/lifecycle.go:26-55`
- spec target: `docs/specs/observability-billing.md:47-57,63-74`

Source files read: AGENTS.md, CLAUDE.md, docs/RULES.md, backend/internal/billing/claim_gate.go, backend/internal/billing/settler.go, backend/sql/migrations/0002_observability_billing.up.sql, docs/specs/observability-billing.md, backend/sql/queries/billing_claims.sql, backend/sql/queries/billing_settle.sql, backend/internal/billing/billing.go, backend/internal/billing/replay_store.go, backend/internal/billing/replay_store.go, backend/internal/billing/replay_store.go, backend/cmd/gateway/lifecycle.go, backend/cmd/gateway/wiring.go, /home/ubuntu/refs/one-api/relay/controller/helper.go, /home/ubuntu/refs/one-api/relay/controller/text.go, /home/ubuntu/refs/new-api/service/pre_consume_quota.go

Lane: specifier
Agent: Codex
UTC timestamp: 2026-05-28T09:57:37Z
Observed regions: 22
Inferences: 8
Open questions: 2
