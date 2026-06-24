# 2026-06-23 backend quality renew round13

| Owner directive | "根据我给你的文档继续刚刚未完成的renew" |
| Scope | 本轮只审查后端测试态势断层: CI 是否真实运行 `integration_pg`、测试环境变量是否与测试代码一致、纯 `t.Skip` 占位测试是否虚增覆盖。排除纯安全专项、生产代码修改、另一个 security 目标文件。 |
| Success criteria | 输出中文增量 findings, 每条都有当前源码 `file:line`、具体函数/测试/工作流、风险说明与可执行修法; 若某线索已被代码兜住, 明确不报为缺陷。 |
| Time estimate | 约 30-45 分钟墙钟; 1 个 Codex 回合内完成读取与汇报。 |
| Blast radius | 只读审查与新增计划文档; 不改 CI、不改 Go 代码、不运行破坏性命令。 |
| Failure modes | 误把未运行测试当已覆盖: 通过读取 workflow、build tags、env 名和测试 `Skip` 条件交叉验证。误触另一个目标: 不读取/修改 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`。 |
| Decision points | 若发现需要实际改 CI 或测试, 本轮只报修法; 是否开实现 PR 由 Owner 另行确认。 |
| Pre-execution checklist | 1. 已重读目标文件。2. 已重读 `api-gateway-risk-review` skill。3. 确认本轮不触碰 security 目标文件。4. 读取 `.github/workflows`、`integration_pg` 测试、测试 env 读取点、纯 skip 占位测试。 |
