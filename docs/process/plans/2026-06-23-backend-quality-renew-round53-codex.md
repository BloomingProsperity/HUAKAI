# 2026-06-23 backend-quality-renew-round53-codex

| Owner directive | "根据我给你的文档继续刚刚未完成的renew" |
| Scope | 聚焦 `backend/internal/userkey` 及直接相关的 API key 生命周期测试、预算门和 CI 运行证据；不触碰另一个 security-scan 目标，不修改生产代码。 |
| Success criteria | 形成带 `file:line` 证据的中文 findings，覆盖 userkey 近顶文件预算、职责边界、重复逻辑、错误/审计/测试质量；如无法运行测试，记录真实原因。 |
| Time estimate | 约 30-45 分钟人工审查等价时间；本轮 agent 时间按一个切片执行。 |
| Blast radius | 只新增本计划文件；后续动作以只读审查为主。若误改生产代码，会污染 renew 审查边界，需立即停止并报告。 |
| Failure modes | 误信状态文档而不读真码；把纯安全问题展开到本专项；重复报告已有 round 结论而缺少新证据；漏掉 CI/test 假绿。缓解：只引用当前源码、测试和 workflow 证据。 |
| Decision points | 若发现需要修改 auth core、quota enforcement、数据库 schema 或真实密钥，停止并请求 Owner；本轮仅输出审查结论。 |
| Pre-execution checklist | 1. 已重读 goal objective；2. 已读取 `production-scenario-review` skill；3. 确认不触碰 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`；4. 使用 `rg`/`nl`/`wc` 读取当前源码；5. 最后运行可用测试命令并记录环境限制。 |

## Concrete Execution Order

1. 统计 `backend/internal/userkey` 非测试/测试体量、`codebudget` baseline 与 near-ceiling 余量。
2. 阅读 `userkey.go` 中创建、校验、撤销、轮换、列表、审计/存储接口的职责边界。
3. 搜索调用方，确认 userkey 是否混入 auth/quota/gateway 入口职责或出现重复解析逻辑。
4. 阅读 userkey 测试，判断是否有判别式断言、假绿 skip、仅非空/非错误弱断言。
5. 运行当前环境可用检查；若 `go` 不存在，如实记录。
6. 直接在 chat 输出 `## S0/S1/S2/S3` findings 和重构优先级表，不写 findings `.md`。
