# 2026-06-23 backend quality renew round15

| Owner directive | "根据我给你的文档继续刚刚未完成的renew" |
| Scope | 本轮只审查 relay handler 请求体读取、缓冲上限、privacy middleware 捕获与 multipart 二次读取: `gatewayhttp`、`completionshttp`、`embeddingshttp`、`imageshttp`、`rerankhttp`、`geminihttp`、`audiohttp` 等入口。排除纯安全专项、生产代码修改、另一个 security 目标文件。 |
| Success criteria | 输出中文增量 findings, 每条都有当前源码 `file:line`、具体函数/常量、触发条件与可执行修法; 已由源码证明一致或可接受的路径不报缺陷。 |
| Time estimate | 约 35-50 分钟墙钟; 1 个 Codex 回合内完成证据读取与汇报。 |
| Blast radius | 只读审查与新增计划文档; 不改 handler、不改 middleware、不运行破坏性命令。 |
| Failure modes | 把已有统一 helper 误判为重复: 用 `rg` 和逐文件读取确认。把安全问题展开过深: 只按代码质量/内存/可维护性定级。误触另一个目标: 不读取/修改 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`。 |
| Decision points | 若发现需要改公共请求体读取 helper 或 privacy middleware 行为, 本轮只报修法; 是否进入实现 PR 由 Owner 确认。 |
| Pre-execution checklist | 1. 已重读目标文件。2. 已重读 `api-gateway-risk-review` skill。3. 确认不触碰 security 目标文件。4. 读取请求体大小常量、`MaxBytesReader`/`io.ReadAll` 调用、privacy middleware 和 multipart 解析代码。 |
