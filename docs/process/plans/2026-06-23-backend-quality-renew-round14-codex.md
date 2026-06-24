# 2026-06-23 backend quality renew round14

| Owner directive | "根据我给你的文档继续刚刚未完成的renew" |
| Scope | 本轮只审查后台 worker / ticker 生命周期与停机语义: `credentialworker/scheduler.go`、`billing/lease_sweep.go`、`audit/refund_worker.go`、`auditledger/dlq_worker.go`、`settlementrecovery` worker、`modelsync/scheduler.go` 及 gateway runtime 接线。排除纯安全专项、生产代码修改、另一个 security 目标文件。 |
| Success criteria | 输出中文增量 findings, 每条都有当前源码 `file:line`、具体函数/类型、触发条件与可执行修法; 已由源码证明安全的路径不报缺陷。 |
| Time estimate | 约 35-50 分钟墙钟; 1 个 Codex 回合内完成证据读取与汇报。 |
| Blast radius | 只读审查与新增计划文档; 不改 worker 实现、不改 lifecycle、不运行破坏性命令。 |
| Failure modes | 把已被 `Stop`/ctx 收敛的 worker 误报为泄漏: 逐个核 `Start`/`Stop`/`Run`、ticker `Stop`、gateway runtime close 接线和测试覆盖。重复既有 round11 eventbus/cmd finding: 本轮聚焦未覆盖的 worker。 |
| Decision points | 若发现需要改 shutdown contract 或 money worker 事务语义, 本轮只报修法; 是否进入实现 PR 由 Owner 确认。 |
| Pre-execution checklist | 1. 已重读目标文件。2. 已重读 `api-gateway-risk-review` skill。3. 确认不触碰 security 目标文件。4. 读取 worker 源码、gateway runtime lifecycle、相关测试。 |
