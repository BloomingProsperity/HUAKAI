# 2026-06-23 backend quality renew round7

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 仅做 Codex 后端质量与架构 renew 增量审查。本轮聚焦 `backend/internal/pool`、`backend/internal/gateway` 流式复杂度、`backend/internal/settlementrecovery` 与 `backend/internal/mediatask` money 周边。明确不读取、不修改另一个 security-scan 目标文件。 |
| Success criteria | 输出中文增量 findings；每条发现有真实源码或测试 `file:line` 证据、问题说明、可执行修法；不把本轮结果冒充整个大目标完成。 |
| Time estimate | 45-75 分钟墙钟；1 个 Codex 增量审查轮次。 |
| Blast radius | 只读源码与测试，新增本计划文件；不改生产代码、不改测试、不改 schema、不改 `LICENSE`。 |
| Failure modes | 误把文档旧状态当事实：只以 `.go`/`.rs` 和测试为证据。误碰另一个目标：不打开 `2026-06-23-backend-security-scan-codex.md`。把纯安全问题展开：只标注转 security 专项，不扩写。 |
| Decision points | 若发现需要修改 auth、billing ledger、quota enforcement、数据库 schema 或删除文件，仅记录发现并请求 Owner 后续确认，不在本轮直接改。 |
| Pre-execution checklist | 1. 读取 active objective。2. 读取 `api-gateway-risk-review` skill。3. 记录 worktree 状态。4. 写本计划。5. 读取目标源码与测试。6. 输出 findings，不写 `.md` findings 报告。 |

## Concrete Execution Order

1. 量化并读取 `internal/pool` 的 router、selector、gates、测试覆盖，找复杂度与判别式测试缺口。
2. 读取 `internal/gateway/forwarder.go` 与相关 stream reconstruct 分派，核 timer 生命周期、状态机双分派漂移。
3. 读取 `settlementrecovery` 与 `mediatask/store_money.go`，核 DLQ/claim/hold/abort/idempotency 与主链一致性。
4. 汇总为 round7 增量 findings，按 S0/S1/S2/S3 输出中文审查正文。
