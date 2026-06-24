# 2026-06-23 backend quality renew round18

| Owner directive | "根据我给你的文档继续刚刚未完成的renew" |
| Scope | 本轮审查 relay 流式转发复杂度: `internal/gateway/forwarder.go` 的 timer/EOF/drain/补帧主循环、`newUpstreamState` 与 `protosse` 重构状态注册的一致性、相关测试判别力。排除纯安全专项、生产代码修改、另一个 security 目标文件。 |
| Success criteria | 输出中文增量 findings, 每条都有当前源码 `file:line`、具体函数/类型、触发条件与可执行修法; 若 timer 或状态机已有充分守护, 明确不报该点。 |
| Time estimate | 约 45-65 分钟墙钟; 1 个 Codex 回合内完成证据读取与汇报。 |
| Blast radius | 只读审查与新增计划文档; 不改生产代码、不改测试、不运行破坏性命令。 |
| Failure modes | 把复杂但已被测试守住的逻辑误报为缺陷: 同读实现与测试。把协议差异误判为重复: 对比 OpenAI/Anthropic/Gemini adapter 分支语义。误触另一个目标: 不读取/修改 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`。 |
| Decision points | 若发现需要重构 stream state registry、timer helper 或 forwarder 子包, 本轮只报告修法; 是否进入实现 PR 由 Owner 确认。 |
| Pre-execution checklist | 1. 已重读目标文件。2. 已重读 `api-gateway-risk-review` skill。3. 确认不触碰 security 目标文件。4. 读取 forwarder/protosse 代码与测试后再定级。 |
