# 2026-06-23 backend quality renew round44

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 本轮审查额度/预算/订阅 enforcement 质量热点：`backend/internal/quota/`、`backend/internal/budget/`、`backend/internal/budgetenforce/`、`backend/internal/quotaenforce/`、`backend/internal/subscription/`、`backend/internal/subscriptionenforce/`。不触碰另一个目标的 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`。 |
| Success criteria | 输出中文 findings，逐条包含 S0-S3、`file:line`、触发条件和可执行修法；至少覆盖四套账本编排重复、fail-open/fail-closed 语义差异、quota 分支重复、错误标签/观测风险。 |
| Time estimate | 约 35-50 分钟墙钟时间；1 个 Codex 审查批次。 |
| Blast radius | 只读源码与测试，新增本计划文件；不改生产代码、不改测试、不改 schema、不改 LICENSE。 |
| Failure modes | 把策略差异误判成安全漏洞：本轮只从代码质量和一致性角度定级；误信旧文档：以 `.go` 真码和测试为准；行号漂移：实际打开文件核对。 |
| Decision points | 如果发现强一致余额/配额主闸实际 fail-open 或账本丢失，只输出 findings，是否改实现由 Owner 确认。 |
| Pre-execution checklist | 1. 重读 goal objective；2. 重读 `api-gateway-risk-review` skill；3. 检查 worktree 并避开另一个目标；4. 用 `rg`/`wc` 定位热点；5. 读源码和测试；6. 尝试可用测试，若无 Go 工具链则如实记录。 |

## Concrete Execution Order

1. 量化 quota/budget/subscription 相关包的非测试行数、文件数和 baseline 位置。
2. 读取 `quota/service.go`、`quota/service_settle.go` 的 reservation/evaluate/rollback 分支。
3. 读取 `budget/service.go`、`budgetenforce/enforce.go`、`quotaenforce/settler.go`、`subscriptionenforce/gate.go` 的失败策略。
4. 对照测试是否覆盖 fail-open/fail-closed 差异、rollback、错误标签。
5. 汇总中文 findings；不写 `.md` findings 报告。
