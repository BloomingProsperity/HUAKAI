# 2026-06-23 backend quality renew round16

| Owner directive | "根据我给你的文档继续刚刚未完成的renew" |
| Scope | 本轮审查重复 helper 与错误分类漂移: signed envelope/HMAC 编解码、租户解析 helper、凭据刷新错误分类、privacy 错误脱敏分类。排除纯安全专项、生产代码修改、另一个 security 目标文件。 |
| Success criteria | 输出中文增量 findings, 每条都有当前源码 `file:line`、具体函数/类型、触发条件与可执行修法; 已由源码证明一致或可接受的路径不报缺陷。 |
| Time estimate | 约 35-55 分钟墙钟; 1 个 Codex 回合内完成证据读取与汇报。 |
| Blast radius | 只读审查与新增计划文档; 不改生产代码、不改测试、不运行破坏性命令。 |
| Failure modes | 把刻意差异误判为重复: 逐函数比较输入、payload 结构、过期语义和调用边界。把安全漏洞展开过深: 只按代码质量/可维护性/错误分类漂移定级。误触另一个目标: 不读取/修改 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`。 |
| Decision points | 若发现需要抽公共签名包、公共租户解析包或类型化错误, 本轮只报告修法; 是否进入实现 PR 由 Owner 确认。 |
| Pre-execution checklist | 1. 已重读目标文件。2. 已重读 `api-gateway-risk-review` skill。3. 确认不触碰 security 目标文件。4. 读取本轮目标函数与测试覆盖后再定级。 |
