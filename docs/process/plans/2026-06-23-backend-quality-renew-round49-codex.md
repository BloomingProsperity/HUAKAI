# 2026-06-23 后端质量 renew round49 后台 worker 生命周期审查

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 审查后台 worker / scheduler 生命周期：`credentialworker/scheduler.go`、`modelsync/scheduler.go`、`billing/lease_sweep.go`、`audit/refund_worker.go`、`auditledger/dlq_worker.go`、`settlementrecovery` worker、`eventbus` runner，以及 `cmd/gateway` 启动/停机接线。 |
| Out of scope | 不改 worker 实现、不改 schema、不改 money 结算逻辑、不执行破坏性命令、不写 findings 报告文件。 |
| Success criteria | 找到真实代码层面的 goroutine/ticker/context/Stop/flush/replay 风险或确认无新增发现；每条发现给出精确文件行号、触发条件和可执行修法。 |
| Time estimate | 35-50 分钟 agent 时间；墙钟取决于搜索与工具链可用性。 |
| Blast radius | 只读源码与新增计划文件；无运行态副作用。 |
| Failure modes | 把“有 ctx 参数”误判成可取消；忽略 Stop 只 cancel 不 wait；忽略 worker 单次处理内部用 background/timeout 导致停机卡住；重复报告已覆盖 round45 的同一问题。缓解：逐个看 Start/Run/Stop/loop/handler 流程与 cmd 接线，只报告新证据或更具体证据。 |
| Decision points | 若后续要改 Stop 语义、加 waitgroup、改 DLQ 重放策略、改 money worker 事务，需要 Owner 另行确认。 |
| Pre-execution checklist | 1. 重新读取目标文件；2. 读取 production-scenario-review 技能；3. 确认 round49 计划文件不存在；4. 搜索 worker 的 Start/Stop/ticker/goroutine；5. 阅读 cmd/gateway 的启动/关闭路径；6. 运行可用检查并记录工具链状态。 |
| Concrete execution order | 先列出目标 worker 文件与入口；再读每个 worker 的 loop/Stop/handler；再读 `cmd/gateway` 如何启动和关闭；最后检查已有测试是否覆盖停机、卡住 handler、DLQ flush。 |
