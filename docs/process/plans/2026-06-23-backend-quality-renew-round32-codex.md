# 2026-06-23 backend quality renew round32

| Owner directive | "根据我给你的文档继续刚刚未完成的renew"；并遵守"不要触碰到另一个目标" |
| Scope | 本轮只审查 `backend/internal/observability` 及其直接生产接线，重点是 eventbus 实际 handler、billing persister、audit logger、dual-run reconciler、metrics/account-health handler 的资源上限、状态一致性、错误分类、测试判别力和注释纪律。不进入 security scan 目标，不读取或修改 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`。 |
| Success criteria | 输出中文 findings，按 S0/S1/S2/S3 分区；每条 finding 有真实 `file:line`、函数或类型、触发条件和可执行修法；若未发现 S1/S2，明确说明已读范围和残余风险。 |
| Time estimate | 本轮约 35-60 分钟代理时间，按源码阅读、生产接线核对和测试覆盖核对推进。 |
| Blast radius | 只新增本计划 artifact；业务代码、测试、schema 均不改。 |
| Failure modes | 1. 把 eventbus runner 已报告的问题重复计入本轮：只看 observability handler 自身。2. 把纯安全问题展开：遇到跨租户/密钥泄露只标"转 security 专项"。3. 被测试名误导：必须核断言是否覆盖生产 handler。4. 触碰另一个目标：保持只读且不打开 `backend-security-scan` 计划。 |
| Decision points | 若发现需要改变 billing settler、审计账本 schema、production handler 顺序或 reconciliation 运行策略，只作为 finding 交 Owner 确认，本轮不直接改。 |
| Pre-execution checklist | 1. 已重新读取目标文件。2. 已读取 `api-gateway-risk-review` skill。3. 已确认当前 worktree 存在另一个 security scan 计划文件并避开。4. 先用 `rg` 定位 `NewBillingPersisterHandler`、`DualRunReconciler`、`AuditLoggerHandler`、`MetricsAggregatorHandler`、测试与生产接线。5. 核对本机 `go` 是否可用后再决定能否运行测试。 |

## Concrete Execution Order

1. 量化 `backend/internal/observability` 体量，读取 handler/reconciler/test 文件。
2. 从 `cmd/gateway/middleware.go` 核对生产 handler 的顺序、tier、timeout 和依赖注入。
3. 检查 dual-run reconciler 内存上限、过期清理、实际调用路径和测试是否覆盖。
4. 检查 billing/audit/metrics/account-health handler 的错误分类、DLQ payload、ctx 使用和注释纪律。
5. 输出中文 findings，并如实说明本机测试不可运行原因。
