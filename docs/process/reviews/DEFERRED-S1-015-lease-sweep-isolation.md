# DEFERRED — S1-015-fu piece B：lease-sweep 测试隔离

**状态**: 延后（test-hygiene 债，非 review finding）  **日期**: 2026-05-29
**来源**: docs/process/reviews/DEFERRED-S1-015-cache-tier-pricing.md:16
**相关**: piece A（纯缓存流结算闸门）已落 fix/hermes 4221ad2；本项与 piece A 无耦合。

## 问题

`backend/internal/billing/balancehold_settle_integration_test.go` 的
`TestSettler_LeaseSweepAbortsExpiredClaims`（`//go:build integration_pg`）在**共享 huakai_dev 库**上
靠全局 `SweepOnce`（`NewLeaseSweeper(pool, set, 10)`，batch=10）回收 seeded claim。其他无 `t.Cleanup`
的测试遗留的过期 `reserving` 孤儿会挤占 batch、或卡住 sweep，使 seeded claim 落在批次窗口外 → flaky red。
现状 mitigation：每次 integration_pg 跑前手动预清理。

## 为什么不能在本切片轻易修

`SelectExpiredReservingClaims`（backend/internal/db/billing/balance_holds.sql.go:201）按
`ORDER BY lease_expires_at LIMIT $1 FOR UPDATE SKIP LOCKED` 选取，**无租户维度** —— lease sweeper
本质是全局的。在共享库上驱动真实 sweeper 的任何测试方案都撞同一堵墙（2026-05-29 试了 3 个，均被 #8 否决）：

1. **循环 SweepOnce 逐批排空直到 target 回收**：被 poison 孤儿击穿——hold 已失的过期 reserving claim，
   `Abort` 永远失败、claim 永留 reserving、按 lease 排首位，batch=1 时每轮都选它、永久占位，loop 到不了 target。
2. **setup 全局 `UPDATE billing_ledger_claims SET status='aborted' WHERE status='reserving' AND lease_expires_at<NOW()`**：
   绕过 `DefaultSettler.Abort`，把**其他租户**有完整 hold 的过期 claim 标 aborted 却不释放 hold →
   `user_balances.held` 残留、无 abort event、sweeper 再不修。污染共享库（#8 R1 P1）。
3. **batch = 当前全部过期 reserving 数 + 余量，单次 sweep 覆盖 target**：batch 跨所有租户 → SweepOnce 会
   abort 他租户 claim、释放 hold/slot、写 billing event 到 fixture 之外（#8 R2 P1）。且 poison fixture 不忠实：
   `balancehold.Release` 把「balance_holds 行缺失」当**成功**，删 hold 并不会造出 stuck 路径（#8 R2 P1）。

## 正解（需 test-infra，独立 follow-up）

二选一，均超出 test-hygiene 微调范围：

- **A. 隔离/一次性测试 DB**（首选）：每个 integration_pg 测试（或至少 sweep 测试）跑在专属 schema/临时库上，
  使全局 sweep 只见本测试数据。需扩 `openPool`/测试 harness 支持 per-test schema 或 template DB clone。
  这是最干净的根治，且惠及所有 integration_pg 测试的隔离性。
- **B. 让 sweep 可按租户 scope**：给 `SelectExpiredReservingClaims` + `LeaseSweeper.SweepOnce` 加可选
  tenant 过滤（生产默认全局、测试传 tenant）。改动触 db/billing 生成代码 + lease_sweep.go，需评估对生产
  sweep 行为零影响。

## 忠实 poison fixture 备注（给未来实现者）

要造出真正的 stuck/abort-error 孤儿：**保留 balance_holds 行**，改为让 `user_balances` 行缺失或金额不匹配
——`DefaultSettler.Abort` 的 release→apply 阶段对 user_balances 做 `UPDATE ... RETURNING`，无行即报
`apply released hold: no rows in result set`（即本机观察到的 claim 50/51 真实错因）。删 balance_holds 行
不行（Release 视其为幂等成功）。

## 不影响

piece A（cache token 计费信号 + usage_record）已独立落地验证，与本项零耦合。本项纯属测试可靠性，
不涉生产行为、不涉金额、不涉迁移（prod）。
