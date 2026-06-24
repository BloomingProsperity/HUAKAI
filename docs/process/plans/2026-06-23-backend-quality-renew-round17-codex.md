# 2026-06-23 backend quality renew round17

| Owner directive | "根据我给你的文档继续刚刚未完成的renew" |
| Scope | 本轮审查凭据子系统结构债与状态漂移: `internal/credentialstore/postgres_store.go` 的多套扫描函数、refresh 成功/失败状态写入、审计耦合与测试覆盖。排除纯安全专项、生产代码修改、另一个 security 目标文件。 |
| Success criteria | 输出中文增量 findings, 每条都有当前源码 `file:line`、具体函数/类型、触发条件与可执行修法; 若源码显示已有判别测试或统一实现, 明确不报该点。 |
| Time estimate | 约 40-60 分钟墙钟; 1 个 Codex 回合内完成证据读取与汇报。 |
| Blast radius | 只读审查与新增计划文档; 不改生产代码、不改测试、不运行破坏性命令。 |
| Failure modes | 把刻意不同的 scan 投影误判为复制粘贴: 对比 SELECT 列、Scan 目标和调用语义。只看静态行数不看行为: 必须读取 refresh 成功/失败与 audit 路径。误触另一个目标: 不读取/修改 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`。 |
| Decision points | 若发现需要拆 `credentialstore` 子包、合并扫描投影或调整 refresh state 机, 本轮只报告修法; 是否进入实现 PR 由 Owner 确认。 |
| Pre-execution checklist | 1. 已重读目标文件。2. 已重读 `api-gateway-risk-review` skill。3. 确认不触碰 security 目标文件。4. 读取 `postgres_store.go` 的 scan/refresh/audit 代码与相关测试后再定级。 |
