# 2026-06-23 backend-quality-renew round69 credentialworker-refresh

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 审查 `backend/internal/credentialworker` 刷新 worker、`mode_refresh.go` vendor adapter 编排、错误分类、audit 写入与测试覆盖。 |
| Out of scope | 不读取或改动真实凭据、不修改 auth 核心、不改 billing/quota/database schema、不展开密钥泄露 security 专项；本轮默认只读源码并输出 findings。 |
| Success criteria | 输出中文 findings，逐条带绝对路径行号、具体函数/类型、触发条件和可执行修法。 |
| Time estimate | 约 25-35 分钟；一次 Codex renew 审查切面。 |
| Blast radius | 默认只新增计划文件并读取代码；若误改刷新 worker 会影响 provider account 健康和 OAuth credential lifecycle，因此本轮不做生产逻辑改动。 |
| Failure modes | 只看 adapter registry 不看调度生命周期；把 provider 错误误判为 security；只看测试名不看断言。缓解：读取实现、scheduler、audit 调用点和测试。 |
| Decision points | 若确认 string-based 错误分类或 audit best-effort 影响生产恢复，后续由 Owner 决定是否引入 typed refresh error / audit fail policy。 |
| Pre-execution checklist | 1. 读取 goal objective；2. 写本计划；3. 读取 credentialworker 文件与行数；4. 读取 mode_refresh/scheduler/audit；5. 读取相关测试；6. 尝试运行目标包测试并记录环境限制。 |
| Concrete execution order | 先量化包与文件，再读刷新实现、调度生命周期和 audit 路径，最后核测试与输出 findings，不写额外报告。 |
