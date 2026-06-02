# 支付子系统 Slice P1 — 平行计划交叉综合 (2026-05-29)

> CLAUDE.md #10 交叉综合。Claude 稿 (`2026-05-29-payment-p1-claude.md`) + Codex 稿 (`2026-05-29-payment-p1-codex.md`) 独立成文后交叉。实现由 Claude 直接写代码 (Owner 2026-05-29)。分支 work/quota-subsystem。

## 交叉结果：~90% 一致，直接采纳

两稿独立成文，核心设计高度一致，以下全部直接采纳：

- **入账 seam 复刻 voucher**：SERIALIZABLE 事务内 INSERT 一条 `billing_events(event_type='payment_credited')`，零 import `internal/billing`，余额=派生 SUM（无独立 user_balance 表）。
- **状态机**：`pending → paid → recharging → completed`，旁路 `expired/cancelled/failed`。
- **幂等三重**：`(tenant_id, out_trade_no)` 唯一 + 状态 CAS rows-affected + `payment_credits` `UNIQUE(tenant_id, order_id)` + 同事务入账。
- **表**：`payment_orders` + `payment_credits`（一单最多一条 credit）。
- **billing_events 扩展**：加 `payment_credit_id` 列 + event_type CHECK 加 `payment_credited` + 互斥约束扩成三分支 + FK。
- **handler 放新包 `internal/paymenthttp`**（冻结 gatewayhttp 禁新文件）；`routes.go`/`wiring.go` 串行编辑。
- **migration 0071**，Owner-gated + 与新机串行协调 billing_events 约束。
- **provider seam**：P1 仅 manual + test provider；真钱 SDK Owner-gated 后续切片。
- **真 PG 判别测试**：重复 out_trade_no 不双账、并发履约只一次 CAS、入账事件+派生余额精确、跨租户隔离。

## 取证修正既有事实

- **migration 号**：我的 worktree 最高 0070（quota），新机 hermes 到 0059，0060-0069 空缺 → **0071 无碰撞**。
- **billing_events 约束当前状态**：经 grep 全 migration 确认，event_type CHECK + claim_or_voucher_check **只在 0023 定义过，至今无任何后续 migration 扩展**（0051 改的是 `credential_audit_events`，非 billing_events，早前误判已纠正）。当前 event_type 白名单 = `claim_committed / claim_aborted / reconciliation_appended / voucher_redeemed`；互斥约束两分支。0071 在此基础上加 payment_credited 第三分支。
- **`fix/waveb-billing` 远端已不存在**（合并/删除），新机当前只剩 `fix/hermes-phase-1-e33d940`，其 0050-0059 均未动 billing_events 约束 → 当前已知状态无冲突；唯一风险是新机未来 0060+ 若也扩展同约束 → 留 §协调点。

## 两个真分歧 + 裁决

### 分歧①（技术，Claude 自决）：Fulfill 单事务 vs 两段式 → **采 codex 两段式**

- Claude 稿：CAS paid→recharging + 入账 + recharging→completed 同一 SERIALIZABLE 事务。
- Codex 稿：phase1 短事务 CAS `paid→recharging` 先**持久提交**；phase2 SERIALIZABLE 事务 re-lock + INSERT credit + INSERT billing_event + 回填 + `recharging→completed`。
- **裁决采 codex 两段式**：单事务设计下 `recharging` 永不被持久观察到（要么回滚到 paid 要么提交为 completed），中间态形同虚设；两段式让 `recharging` 成为**真正可崩溃恢复的持久断点**——崩在两段之间订单停在 recharging，重试从 phase2 续跑；phase2 自身原子（credit+event+completed 全成或全不成），`payment_credits UNIQUE(order)` 兜底并发 phase2。这是审批计划内的实现选择，不需 Owner gate（CLAUDE.md #10：审批计划内的编码执行选择不触发）。
- **CAS 入口放宽**：phase2 接受 `{paid, recharging}` → recharging，使崩溃后 paid/recharging 两态都能续跑。

### 分歧②（涉 schema 范围，Owner 已拍）：支付审计 → **Owner 选 A：单独建 `payment_audit_events` 表**

- Claude 稿：voucher 式 AuditSink 接口（无新表）。
- Codex 稿：独立 `payment_audit_events` 领域审计表。
- **Owner 决策（2026-05-29 AskUserQuestion）= A 独立表**。理由（#15 横向对照已 surface）：
  - sub2api 同款——有专用的支付领域审计日志表（`Wei-Shaw/sub2api@91da8159:backend/internal/service/payment_fulfillment.go:15` 引入该审计表），且用审计记录的存在性查询来做幂等（`:356/:368` 查某动作是否已记录）。
  - new-api 反向——复用一张带类型标签的通用操作日志表（`QuantumNous/new-api@20d3e737:model/topup.go:155,:387` 写充值类操作日志），无支付专表。
  - HUAKAI 取 A：钱路径操作可追责=信任链核心差异化；独立表隔离 admin 共享审计约束，零新机协调冲突。
  - **HUAKAI 升级 delta**：`payment_audit_events` 与 billing_events 分工——billing_events 记**成功入账的钱事实**（不可变台账），payment_audit_events 记**操作轨迹**（who/why/failed/replay）；两者互补，区别于 sub2api 把幂等也压在审计表（HUAKAI 幂等靠 out_trade_no unique + CAS + credit unique，审计表纯观测不承载正确性，职责更清）。

## Slice P1 最终范围（小切片闭合）

1. migration 0071：`payment_orders` + `payment_credits` + `payment_audit_events` + billing_events 三分支扩展（payment_credit_id 列 + event_type CHECK + claim_or_voucher_or_payment 互斥 + FK）。up/down 对称；down 对非空 payment 数据 fail-fast（不静默删钱数据，采 codex §3.4）。
2. `internal/payment`：types/store/store_postgres/store_memory/service/provider/idempotency/audit/privacy + 测试。
3. `internal/paymenthttp/handler.go`：admin 建单/确认+履约/查单 + user 查单+余额。
4. wiring：`routes.go` 挂载 + `wiring.go` 构造（串行协调点）。

## 判别测试（真 PG，mutation-discriminating #14，融合两稿）

| 测试 | 守的缺陷 | 判别 fixture | mutation 变红 |
|---|---|---|---|
| T1 DuplicateOutTradeNoNoDoubleCredit | 重复建单/履约双账 | 同 (tenant,out_trade_no) 建单两次→确认两次→断言 1 单/1 credit/1 payment_credited event/余额只增一次 | 去 unique/CAS/credit-unique → 双账 → 红 |
| T2 ConcurrentFulfillOnlyOneCASWins | 并发履约超发 | 同一 paid 订单 32 goroutine barrier 并发 Fulfill → 只 1 次入账，余额增一次 | 非 CAS（先查后写）/去 credit-unique → 多入账 → 红 |
| T3 CreditBillingEventAndDerivedBalanceMatch | 漏写事件/金额方向/互斥错/余额漏算 payment | 履约后断言恰一条 payment_credited(actual_cost=actual_cost_signed=金额, claim_id NULL, voucher_redemption_id NULL, payment_credit_id 匹配) + 余额 SUM 精确 | 漏写 event/写负 signed/余额只算 voucher → 红 |
| T4 TenantIsolationSameOutTradeNo | 串租户 | tenant A、B 同 out_trade_no 各建单，只履约 A → A 余额增、B=0、B 查 A 单 404/403 | 漏 tenant 谓词 → 串租户/错账 → 红 |
| T5 RechargingRetryCompletesOnce | 崩溃卡死/重试双账（采 codex 8.5） | 推进到 recharging 但不写 credit（store helper 只跑 phase1）→ 再调 Fulfill → completed, credit/event 各 1, 余额一次 | Fulfill 不接受 recharging 续跑→卡失败；续跑重复入账→count/balance 红 |
| T6 FulfillRequiresPaidOrRecharging | 状态机越权 | 对 pending（未确认）订单直接 Fulfill → 拒（CAS rows=0），无入账 | CAS 入口放宽到含 pending → 误入账 → 红 |
| T7 PaymentAuditTrailRecorded | 操作审计缺口 | 建单→确认→履约后断言 payment_audit_events 含 order_created/paid_confirmed/credited 且带 actor admin id + request id | 漏写 audit → 行缺失 → 红 |

- 自证：T1 同测内对比"一次" vs "重复"的余额必须相同；T3 同测对比 payment-credited 与 baseline（无 payment）的余额必须差精确金额。

## 协调点（硬约束）

- **billing_events 约束扩展 = 与新机串行协调点**：0071 DROP+ADD 两约束。当前新机 0050-0059 未动它 → 无即时冲突。**主线合并前**须与新机确认其未来 0060+ 是否也扩展同约束；若有，union 两边新增值后由 Owner 指定单机落最终 master 约束。本分支 0071 自包含可独立 local 测，push 到 work/quota-subsystem 不等于生产 landing。
- `routes.go`/`wiring.go` 串行编辑：落前确认新机未同时改。
- migration 落地（主线 merge / 生产）= Owner-gated 高风险（CLAUDE.md database schema）。

## fusion-upgrade delta（三维，#12 已 source-read）

- **架构**：入账走 append-only `billing_events`（可审计事件流）+ 派生余额，而非参考项目独立可变余额表（new-api `model/topup.go` 直接加用户额度字段）；payment_credits 与 voucher_redemption 并列为 credit 来源统一进信任链；payment_audit_events 与 billing_events 职责分离（观测 vs 钱事实）。
- **算法**：sub2api RECHARGING-CAS 幂等（无应用层锁，`payment_fulfillment.go:142,156`）+ 两段式断点续跑；HUAKAI 升级=入账与终态同 SERIALIZABLE + credit unique(order) 兜底 + 幂等不依赖审计表（区别于上游用审计记录存在性查询做幂等）。
- **生态**：admin/user 端点 + 领域审计表，为 P2 provider/webhook、P3 订阅（授予 internal/quota 策略）、P4 签到/返佣、P5 退款打底；真钱 Owner-gated 隔离。

## Source files read

- HUAKAI（SPECIFIER lane，自有）：`internal/voucher/{store_postgres,store,store_memory,service,types,audit}.go`、`sql/migrations/0023_voucher_system.up.sql:118-167`、`0051_credential_state_event_types.up.sql`、`internal/quota/*` 测试基建、`cmd/gateway/{routes,wiring}.go`。
- 参考（行为级，#12/#15）：`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/{payment_order,payment_fulfillment}.go`、`QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:{model/topup.go,controller/topup.go,model/subscription.go}`。

Lane: synthesis (Claude reviewer of both plans + 亲自取证)｜Agent: Claude Opus 4.8｜UTC: 2026-05-29
