# 2026-06-23 backend quality renew round11
| Owner directive | "根据我给你的文档继续刚刚未完成的renew" |
| Scope | In: `backend/cmd/gateway/` 预算盲区、启动装配大函数、路由注册体量、是否把应落 `internal/` 的逻辑塞进 `cmd/`；Out: security 专项、另一个 security 目标计划、生产代码修改、前端。 |
| Success criteria | 读取真实源码并给出中文增量 findings；每条 finding 有 `file:line`、具体问题、可执行修法；不声称整个 renew 完成。 |
| Time estimate | 30-45 分钟墙钟；1 个 Codex 审查轮。 |
| Blast radius | 本轮只读源码并新增计划文档；不会改生产代码。 |
| Failure modes | 只按文档猜测导致误报；缓解: 以 `cmd/gateway/*.go` 真码、`codebudget` 真码、`wc -l` 和行号为准。误碰另一个目标；缓解: 不读取、不修改 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`。 |
| Decision points | 若发现需要拆 `cmd/gateway` 到新包，作为 finding 提交 Owner 决策；不在本轮直接重构。 |
| Pre-execution checklist | 1. 量化 `cmd/gateway/*.go` 行数；2. 读取 `codebudget` 扫描范围；3. 定位 `buildGatewayRuntime`、路由注册与启动门逻辑；4. 检查是否存在新增逻辑规避预算门；5. 汇总中文 findings。 |
