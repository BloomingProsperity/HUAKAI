# 2026-07-05 billing Settle/Abort Serializable 重试

| Owner directive | “给 billing Settle/Abort 加 Serializable 40001/40P01 重试(对齐 Reserve,修并发下 abort_failed/settle_failed)” |
| --- | --- |
| Scope | 仅修改 `backend/internal/billing/settler.go` 及必要测试；为 `DefaultSettler.Settle` / `Abort` 的 Tx2 主事务增加 40001/40P01 有限重试。 |
| Out of scope | 不改计费金额、扣款/退款策略、数据库 schema、调用方 DLQ/lease sweep 兜底、网关重试策略、`LICENSE`、真实密钥。 |
| Success criteria | Settle/Abort 遇模拟 40001/40P01 时会重跑完整 Tx2 并最终成功；非序列化错误与业务哨兵原样返回；幂等结果不重复写 usage/billing event/hold release；并发压测不再出现本任务目标中的 `abort_failed`/`settle_failed` 噪音。 |
| Time estimate | 代码阅读 15 分钟；补丁 20-30 分钟；单元/集成/并发门禁 30-90 分钟，取决于 PostgreSQL 测试库状态。 |
| Blast radius | Money path Tx2 结算与中止；错误会影响 usage_records、billing_events、balance_holds、pool slot 释放和网关审计日志。 |
| Failure modes | 重试包裹范围过小导致部分事务外副作用重复；错误分类过宽导致业务错误被重试；耗尽后错误被错误映射；测试只验证成功不验证幂等计数；并发压测仍暴露其他路径 40001。 |
| Mitigation | 重试函数只接受“完整 BeginTx→Commit/Rollback”的 once 函数；复用 `isReserveSerializationConflict` 与 `reserveBackoff`；耗尽返回最后一个原始错误；测试断言调用次数、业务哨兵不重试、Settle/Abort 集成幂等计数。 |
| Decision points | 若需要改 schema、计费策略、调用方 attempt 策略、DLQ/lease 行为或新增运行时依赖，停止并请求 Owner 确认。本任务当前不需要这些动作。 |
| Cross-discussion note | 本文件为 Codex 独立计划；未读取同主题 Claude 计划内容。当前 Owner 已明确授权该 money path 健壮性修复，执行不做 git commit/push。 |

## Scope Adjustment During Verification

`e2e_concurrency` 在 Settle/Abort 40001 重试修复后仍红，但失败点从 40001 泄漏变成 rejected claim 有 2 条 `claim_aborted`。代码阅读确认 `queue_wait` 分支被构造成 `retryableLocalAttemptFailure`，即 Abort 成功后同一 HTTP 请求仍会立即 re-reserve/re-abort；这不是计费金额策略变更，而是容量排队分支的内部重试噪音。为满足本任务“attempt_seq=1 / 1 条 claim_aborted”的验收，允许追加一个最小网关修正：`queue_wait` 返回 429 + Retry-After 后终止当前请求，不在同一请求内重试。仍不修改测试断言、计费金额、billing ledger schema 或 quota enforcement。

## Pre-execution checklist

1. 读取 `AGENTS.md` 指令、`docs/RULES.md` 规则清单、`internal/billing/retry.go`、`internal/billing/claim_gate.go`、`internal/billing/settler.go`。
2. 确认 `retry.go` 中 `isReserveSerializationConflict`、`reserveBackoff`、`sleepWithContext`、`defaultReserveRand` 可复用。
3. 确认 `Settle` 与 `Abort` 的业务哨兵仍由 status='reserving' 守卫产生，不能被包装成其它错误。
4. 检查现有 `internal/billing` 测试夹具，优先复用已有 PostgreSQL 集成测试种子。
5. 改动后运行 gofmt、单元测试、集成测试、smoke、e2e_concurrency 与 build/vet 门禁。

## Concrete execution order

1. 在 `settler.go` 增加 Tx2 重试 helper，使用项目既有退避参数，耗尽后返回最后一次错误。
2. 将 `Settle` 外层改为参数校验 + 重试包装，原事务主体抽为 `settleOnce`。
3. 将 `Abort` 外层改为参数校验 + 重试包装，原事务主体抽为 `abortOnce`。
4. 在注释中写明幂等依据：Serializable 冲突整事务回滚、status='reserving' 守卫、写入/释放全部在 Tx2 内。
5. 补测试：helper 级变异证红覆盖 40001/40P01 成功重试、非重试错误/业务哨兵不重试、耗尽返回最后错误；必要时补 Settle/Abort 集成幂等计数。
6. 按 Owner 给定命令跑门禁；若 PostgreSQL 环境问题阻断，原样记录失败命令和错误。
