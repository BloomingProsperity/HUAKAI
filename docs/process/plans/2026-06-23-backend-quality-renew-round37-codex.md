# 2026-06-23 backend quality renew round37 credential review

| Owner directive | "做完了？ 这么快？ 这么大的项目你这么快？"；继续按目标文件进行后端代码质量/架构刷新审查。 |
| Scope | 本轮只读审查 `backend/internal/credentialstore`、`backend/internal/credentialworker` 及直接相关测试/接线。重点看 god 文件、重复扫描、刷新 worker 生命周期、错误分类、测试是否判别式。 |
| Out of scope | 不改真实凭据、不读生产 secret、不改 auth/billing/quota/database schema、不读或修改另一个 `backend-security-scan` 目标文件。 |
| Success criteria | 输出中文 S0-S3 findings，每条有 `file:line`、触发条件和可执行修法；明确未能运行的测试限制。 |
| Time estimate | 约 20-35 分钟人工审查等价时间；当前代理按文件读取和证据收集推进。 |
| Blast radius | 只读审查加计划 artifact，理论风险限于新增 docs/process/plans 文件；不改变运行时行为。 |
| Failure modes | 误把安全专项问题展开：只标转 security；误碰另一个目标：通过显式排除 `backend-security-scan` 文件避免；被大文件噪声淹没：按 worker 生命周期/重复扫描/测试质量聚焦。 |
| Decision points | 若发现需要改凭据加密、真实 secret、auth core 或 schema，停止并请求 Owner 确认；本轮默认只报告不修代码。 |
| Pre-execution checklist | 1. 确认目标文件仍为 backend quality 专项；2. 列出 credential 相关文件；3. 读取存储与 worker 主路径；4. 对照测试；5. 仅输出可证据化 findings。 |
| Concrete execution order | 先量化文件体量，再读 `postgres_store.go`、`mode_refresh.go`、scheduler/adapter 接口和测试；最后按 S0-S3 汇总。 |
