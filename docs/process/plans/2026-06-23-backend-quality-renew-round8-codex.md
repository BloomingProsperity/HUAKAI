# 2026-06-23 backend quality renew round8

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 仅做 Codex 后端质量与架构 renew 增量审查。本轮聚焦 `backend/internal/quota`、`backend/internal/budget`、`backend/internal/budgetenforce`、`backend/internal/quotaenforce`、`backend/internal/subscriptionenforce` 的额度/预算/订阅 enforcement 编排、fail-open 语义、重复逻辑与测试判别力。明确不读取、不修改另一个 security-scan 目标文件。 |
| Success criteria | 输出中文增量 findings；每条发现有真实源码或测试 `file:line` 证据、问题说明、可执行修法；不把本轮结果冒充整个大目标完成。 |
| Time estimate | 45-75 分钟墙钟；1 个 Codex 增量审查轮次。 |
| Blast radius | 只读源码与测试，新增本计划文件；不改生产代码、不改测试、不改 schema、不改 `LICENSE`。 |
| Failure modes | 误把安全专项展开：本轮只从代码质量、可维护性、fail-open 漂移与测试假绿角度记录。误把配置意图当事实：只以源码和测试为证据。误碰另一个目标：不打开 `2026-06-23-backend-security-scan-codex.md`。 |
| Decision points | 若发现需要修改 quota enforcement、billing ledger、数据库 schema 或强一致 money gate，仅记录发现并请求 Owner 后续确认，不在本轮直接改。 |
| Pre-execution checklist | 1. 读取 active objective。2. 读取 `api-gateway-risk-review` skill。3. 记录 worktree 状态。4. 写本计划。5. 读取目标源码与测试。6. 输出 findings，不写 `.md` findings 报告。 |

## Concrete Execution Order

1. 读取 `quota/service.go`、`quota/service_settle.go` 与测试，核三类指标预留/rollback/settle 是否重复且易漂移。
2. 读取 `budget/service.go`、`budgetenforce/enforce.go`、`quotaenforce/settler.go` 与测试，核 fail-open 策略、metric label、内存 reservation 与 ledger 双轨。
3. 读取 `subscriptionenforce/gate.go` 与测试，核 repo error 放行、fail-open observer、fail-closed observer 是否统一。
4. 汇总为 round8 增量 findings，按 S0/S1/S2/S3 输出中文审查正文。
