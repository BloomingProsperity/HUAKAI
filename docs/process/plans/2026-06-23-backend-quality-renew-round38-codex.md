# 2026-06-23 后端质量 renew round38 payment 审查

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 本轮只读审查 `backend/internal/payment` 及直接相关测试、CI 可见性和调用边界。重点核 god 包/大文件、订单/回调/退款/奖励 money path、幂等、审计、worker 生命周期、测试判别力。 |
| Out of scope | 不改真实支付配置、不触碰真实 secret、不改数据库 schema、不改 billing/quota/auth 核心、不读或修改另一个 `backend-security-scan` 目标文件。 |
| Success criteria | 输出中文 S0-S3 findings，每条有 `file:line`、函数/类型、触发条件、可执行修法；明确本机无法运行 Go 测试的限制。 |
| Time estimate | 约 30-45 分钟人工审查等价时间；当前代理按文件读取和证据收集推进。 |
| Blast radius | 只新增本计划 artifact 并只读审查 payment 代码；不改变运行时行为。 |
| Failure modes | 把纯安全专项展开：仅标“转 security 专项”；误碰高风险文件：遇到 schema/真实 secret/支付账本变更只报告不修改；被大文件淹没：按真实 money path、幂等和测试质量聚焦。 |
| Decision points | 若发现必须修改支付账本、真实支付配置、schema、退款资金路径或生产 secret 才能继续，停止并请求 Owner 确认。 |
| Pre-execution checklist | 1. 量化 payment 文件体量；2. 列出 order/webhook/callback/refund/reward/store 文件；3. 读取主 money path 和测试；4. 对照目标文件的 codebudget、幂等、审计完整性规则；5. 输出证据化 findings。 |
| Concrete execution order | 先读文件地图和体量，再读 `store_postgres.go`、`order.go`、`webhook.go`、`callback.go`、退款/奖励文件和测试，最后按 S0-S3 汇总。 |
