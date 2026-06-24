# 2026-06-23 backend quality renew round47 codex

| Owner directive | "根据我给你的文档继续刚刚未完成的renew" |
| Scope | 审查 `backend/internal/auth`、`usersession`、`twofa`、`privacy`、`auditledger` 以及目标文件点名的租户解析 helper 重复。只做源码审查、测试尝试与中文 findings 输出；不写 findings 报告文件。 |
| Out of scope | 不展开纯安全专项结论；不修改 `LICENSE`、数据库 schema、认证核心行为、账本逻辑或另一个目标的计划文件。 |
| Success criteria | 每条发现均有当前源码 `file:line` 证据、触发条件与可执行修法；能区分 S1/S2/S3；运行可用检查并记录无法运行的原因。 |
| Time estimate | 约 45-75 分钟墙钟；1 个 Codex 审查轮。 |
| Blast radius | 读源码和新增计划文件风险低；若后续修复涉及认证/会话核心，需要单独 Owner 确认，本轮不直接改高风险实现。 |
| Failure modes | 误把纯安全问题展开为本轮结论；误信陈旧文档；把重复实现当成已抽象；遗漏测试假绿。缓解：以 `.go` 真码与测试为准，逐条引用行号。 |
| Decision points | 若发现需要改变认证核心、会话签名格式或审计账本写入语义，本轮只标发现与修法，不直接改。 |
| Pre-execution checklist | 1. 重读目标文件。2. 读取适用 skill。3. 确认计划文件不存在。4. 用 `rg`/`nl` 读取目标源码。5. 尝试运行目标包测试。 |
| Concrete execution order | 1. 量化包/文件体量。2. 对比 signed-envelope 实现。3. 对比租户解析 helper。4. 核对 `privacy` 错误分类。5. 核对 `auditledger` 读写审计纪律与 worker 生命周期线索。6. 输出中文 findings。 |
