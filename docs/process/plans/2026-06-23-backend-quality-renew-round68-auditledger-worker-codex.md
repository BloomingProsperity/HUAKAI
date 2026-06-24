# 2026-06-23 backend-quality-renew round68 auditledger-worker

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 审查 `backend/internal/auditledger`、相关 audit worker / DLQ / 读写审计路径的代码质量、生命周期、错误处理一致性与测试覆盖。 |
| Out of scope | 不修改数据库 schema、不改鉴权核心、不改 billing/quota 主账本、不展开跨租户或密钥泄露 security 专项；本轮默认只读源码并输出 findings。 |
| Success criteria | 输出中文 findings，逐条带绝对路径行号、具体函数/类型、触发条件和可执行修法。 |
| Time estimate | 约 25-35 分钟；一次 Codex renew 审查切面。 |
| Blast radius | 默认只新增计划文件并读取代码；若误改审计写入或 worker 语义，会影响取证可靠性和恢复路径，因此本轮不做生产逻辑改动。 |
| Failure modes | 只看单个 worker 而忽略读写路径纪律差异；把 security 风险展开过深；只看接口不看测试。缓解：同时读取 auditledger 实现、调用点、worker 生命周期和测试。 |
| Decision points | 若确认读路径 best-effort 与写路径 strict 语义冲突，后续由 Owner 决定是否拆分 audit write policy 并补恢复队列。 |
| Pre-execution checklist | 1. 读取 goal objective；2. 写本计划；3. 列出 auditledger/audit 相关文件；4. 读取实现与调用方；5. 读取测试；6. 尝试运行目标包测试并记录环境限制。 |
| Concrete execution order | 先定位文件与调用点，再核 worker/ticker/ctx 生命周期，再核错误处理与测试，最后输出 findings，不写额外报告。 |
