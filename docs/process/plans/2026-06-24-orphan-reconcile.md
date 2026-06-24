# 孤儿对账闭环(media_task_orphans)实施计划 — 2026-06-24

## 0. 一句话

`media_task_orphans` 表(迁移 0151)已在持续生产入库孤儿线索,但**零查询、零处置出口**——运维看不到、无法对账,漏掉的上游成本没人管。本切片建闭环:**admin 只读可视化** + **admin 显式手动对账**(推进状态),并在 admin 二次显式选择"追扣"时**复用既有 billing settle 路径**(`billing.Capture`)补记费用。**Manual-First(绝无自动扣费)、幂等防双扣、复用既有 settle 不新写扣费逻辑。**

## 1. 范围

- **建**:`backend/internal/orphanreconcilehttp`(新 admin http 子包,不堆 gatewayhttp 大包):
  - `GET  /admin/v1/media-task-orphans` — 分页列出 pending 孤儿(走 `idx_media_task_orphans_pending`)。
  - `POST /admin/v1/media-task-orphans/{id}/reconcile` — admin 显式对账一个孤儿:推进 `reconcile_status`(`reconciled` / `cancelled` / `ignored`),在 body 显式 `back_charge=true` 时才走 settle 补扣。
- **建**:`mediatask` 包内新增 store 方法 `BackChargeOrphan`(复用 `billing.Capture`,事务内状态门 + 幂等,**不新写 ledger 逻辑**)。
- **接线**:`deps` 加 `mediaTaskStore` 字段 → `mountAdminRoutes` 挂载,复用 `d.adminAuth`(既有 admin RBAC 中间件)+ 既有 `admin_audit_events` 审计写入。
- **不建**:任何后台/定时/cron 自动追扣 worker;任何新的扣费/ledger SQL;任何 schema 迁移。

## 2. orphan 状态机(迁移 0151 真码,file:line)

`backend/sql/migrations/0151_media_task_orphans.up.sql:19-20` 的 CHECK 约束已钉死合法状态:
```
reconcile_status TEXT NOT NULL DEFAULT 'pending'
    CHECK (reconcile_status IN ('pending', 'reconciled', 'cancelled', 'ignored'))
```
- `pending` → 初始态,producer 入库即此态(`store_orphan.go:28-32` 的 `insertOrphanSQL` 不写 status,取默认)。
- `pending → reconciled` — 已对上账(默认动作,可不扣钱;或追扣后)。
- `pending → cancelled` — 经核实上游未真正计费(误报),无需追扣。
- `pending → ignored` — 运维主动忽略(已知不追)。
- 终态(`reconciled/cancelled/ignored`)**不可再变更**:`store_orphan.go:96-99` 的 `markOrphanReconciledSQL` 带 `WHERE id=$1 AND reconcile_status='pending'` 终态守卫,`RowsAffected()>0` 才算真改(`store_orphan.go:104-118`)。这是状态推进幂等的命门,已存在,本切片复用。

## 3. 复用的 settle 路径(关键:绝不自己新写扣费/ledger)

**孤儿的钱在哪**:孤儿事件发生在 `worker.go:180-188`——`MarkProviderSubmitted` 返回 `ErrLeaseLost`(租约被另一 worker 抢走)。原始 `media_tasks` 行在 `insertReservedTask`(`store_money.go:82-125`)创建时已做 `billing.Reserve`(`store_money.go:107`),持有一笔 `hold_ref="claim:<claimID>"`(`store_money.go:247-249`)的余额预扣(held)。抢到租约的 worker 会用**同一幂等键**继续处理该任务并正常 settle(capture/release),孤儿记录是"上游幂等若失效"的留痕兜底。

**追扣该走哪个既有 settle**:把那笔已 Reserve 的预扣**capture 成真实扣费**,走既有 `billing.Capture(ctx, tx, claimID, actualCost)`(`backend/internal/billing/balancehold.go:95-137`)。

**为什么 `Capture` 天然幂等(防双扣命门)**:`balancehold.go:106` —— `if hold.State != "held"` 时 `Capture` **不再扣款**,只读回当前余额快照返回(`balancehold.go:107-117`)。即:
- 若抢到租约的 worker 已 `CompleteSuccess`(capture,hold 变 captured)或已 abort(release,hold 变 released)——再追扣是 **no-op**,余额一分不动。
- 若该 claim 仍 `held`(罕见:winner 也卡住了)——追扣按预扣额 capture 一次,把漏掉的费用补记。

**双层幂等门**(防双扣):
1. **应用层状态门**:只有 `reconcile_status='pending'` 的孤儿才允许追扣;追扣成功后原子推进到 `reconciled`。已 `reconciled` 的孤儿再次追扣 → 状态门 `RowsAffected()==0` → 直接拒绝/no-op,**根本不进 Capture**。
2. **billing 层 hold.State 门**:即使状态门被绕过,`Capture` 的 `hold.State != "held"` 也兜底不双扣。

追扣额 = `media_tasks.estimated_cents`(预扣额,`Task.EstimatedCents`,`types.go:93`),与正常 media task 成功路径 `store_money.go:42-50` 的 clamp 口径一致(按预扣结算,不超收客户)。

## 4. Manual-First 安全设计(money,严格)

- **绝无自动扣费**:本切片**不**加任何 worker / cron / 定时器 / 后台 goroutine。追扣只在 `POST /admin/v1/media-task-orphans/{id}/reconcile` 且 body `back_charge=true` 时由 **admin handler 同步调用**。
- **默认动作不扣钱**:对账默认只标记 `reconciled` / `cancelled` / `ignored`;追扣是 admin 的**显式二次确认**(必须显式 `back_charge=true` + `status=reconciled`)。
- **架构守卫测试**:断言 `BackChargeOrphan`(走 Capture 的入口)在整个 mediatask + cmd/gateway 代码树中**只被 orphan reconcile handler 引用**,无任何 worker/loop/cron 引用。变异:加一个自动追扣调用 → 守卫红。

## 5. 幂等键 / 防双扣

- **幂等键**=孤儿行的 `reconcile_status` 状态 + 行 id(唯一)。同一孤儿(同 id)重复 POST reconcile:首次 `pending→reconciled`(若 back_charge 则 capture 一次),二次 `RowsAffected==0` → 返回 `already_reconciled`,**不进 Capture,余额不动**。
- 叠加 billing 层 `hold.State` 兜底门(§3)。
- **判别测试**(命门):对已 reconciled 孤儿再次追扣 → 断言 Capture 调用次数 / 余额变化只发生一次。**变异**:去掉状态门(允许重复进 Capture)→ 断言"扣了两次" → 红。

## 6. 审计

复用既有 `admin_audit_events`(`backend/internal/db/admin` 的 `InsertAdminAuditEvent`,模式见 `adminquotahttp/store.go:66-80,115-132`)。对账/追扣动作在**同一事务**内写一行审计:`action=orphan_reconciled|orphan_cancelled|orphan_ignored`、`target_type=media_task_orphan`、`target_id=<orphan id>`、`actor_id/actor_role` 取自 `admin.AdminIdentity`、`payload` 记 `back_charge` / `captured_cents` / `task_id` / `provider_task_id`。审计与状态推进 + capture 原子(BeginFunc 单 tx),失败整体回滚。

## 7. RBAC

复用 `d.adminAuth`(`*admin.AdminResolver`)+ `adminquotahttp` 同款 `resolveTenantIdentity` 语义:`platform_admin` 可跨租户(列表默认全局扫 tenantID<=0);`tenant_operator` 仅限自己 `ScopeTenantID`,追扣前用 `CanIssueForTenant(orphan.TenantID)` 校验该孤儿属其租户(防越权对账他租户孤儿)。非 admin → 401/403。
- **变异**:让非 admin 调对账 → 鉴权测试红(确认 `Auth.Resolve` 挡住)。

## 8. blast radius(影响面)

- 纯新增:1 个 http 子包 + mediatask 1 个 store 方法 + deps 1 字段 + routes 挂载几行。
- **不动** schema、**不动** billing 不变量(只调用既有 `Capture`)、**不动** 既有 producer/worker/settle 路径。
- 最坏情况:追扣 bug → 至多对一个 `held` claim capture 一次预扣额(有界,等同正常成功结算);幂等双门保证不双扣。读列表纯只读。

## 9. Owner-gated 评估

- **追扣(动钱)是否需 Owner 签字?** 本切片**不新写扣费逻辑、不改 schema、不改 billing 不变量**——只复用既有 `billing.Capture`(把已 Reserve 的预扣 capture,这是 media task 成功路径每天在做的同一动作),且 100% Manual-First(只有 admin 显式 `back_charge=true` 才触发)。属"复用既有 settle 的安全接线",不是新建动钱核心。
- **保守降级开关**:若 review 认为"admin 手动 capture 预扣"仍触及 billing 红线,则本切片可只交付**可视化 + 状态推进(reconciled/cancelled/ignored,不扣钱)**的安全子闭环,把"追扣钱"作为带本 plan 的 Owner-gated 后续。代码上 `back_charge` 默认 false,不传即纯标记,天然支持降级。
- 无 schema 迁移、无新运行时依赖、无 admin RBAC 新建(复用既有)。

## 10. 三镜对照(§16,clean-room,只读,严禁拷标识符)

- **sub2api(default tiebreaker)**:`backend/internal/service/payment_order_lifecycle.go:269-298`(`VerifyOrderByOutTradeNo`)+ `payment_fulfillment.go:142-208`(`toPaid`/`alreadyProcessed`)——对账=**状态门 + 既有 fulfillment 路径**:只对 `Pending/Expired` 状态的订单对账(lifecycle.go:287),crediting 走既有 `HandlePaymentNotification` 而非 bespoke credit;幂等靠**条件状态 UPDATE 的 RowsAffected==0 短路**(fulfillment.go:146-162,`c==0 → alreadyProcessed` 不二次入账)。**这正是 HUAKAI 已有的 `markOrphanReconciledSQL` WHERE status='pending' + RowsAffected 模式**,本切片直接对齐。
- **new-api**:`service/task_billing.go:184-240`(`RecalculateTaskQuota`)——异步媒体任务的**差额结算**:`actualQuota - preConsumedQuota` 调资金来源 + 写 `RecordTaskBillingLog`;`quotaDelta==0` 时短路不动钱(task_billing.go:194-198)。证"复用既有 quota 调整 + 日志、不新写一套"是成熟做法。
- **CLIProxyAPI**:**no-equivalent**——纯 relay account→API 代理,无 payment/order/billing/对账 模块(`~/refs/CLIProxyAPI/internal/access/reconcile.go` 的 `ReconcileProviders` 是 provider 配置 reconcile / 访问控制,与计费对账无关;全仓无 billing 包)。已显式 source-cited no-equivalent。

**融合-升级 delta**:HUAKAI = sub2api 的状态门+既有 settle 复用 + new-api 的预扣→实际差额口径 + delta(**双层幂等门**:应用层 reconcile_status 状态门 **叠加** billing 层 hold.State 门;**Manual-First 无自动追扣 worker**——sub2api 有 `ReconcilePendingWxpayOrders` 定时 reconcile,HUAKAI 孤儿追扣刻意只走 admin 显式动作,money 安全优先)。维度:架构(双门 + admin-only 入口)+ 生态(admin 可视化对账台 + 审计)。
