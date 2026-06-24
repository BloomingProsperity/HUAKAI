# 2026-06-23 backend-quality-renew round67 privacy-error-classifier

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 审查 `backend/internal/privacy/redactor.go` 的 `SanitizeError` / 错误分类逻辑、调用方观测字段、误判风险与测试覆盖。 |
| Out of scope | 不修改鉴权核心、不改变生产错误码策略、不展开密钥泄露 security 专项；只做代码质量、分类稳定性和测试质量审查。 |
| Success criteria | 输出中文 findings，逐条带绝对路径行号、具体函数/类型、触发条件和可执行修法。 |
| Time estimate | 约 20-30 分钟；一次 Codex renew 审查切面。 |
| Blast radius | 默认只读源码并写计划文件；若误改错误分类会影响日志/审计/告警归因，因此本轮不做生产逻辑改动。 |
| Failure modes | 只看 `SanitizeError` 不看调用方；把刻意脱敏误判为信息丢失；把 security 泄露问题展开成专项。缓解：读取实现、测试和调用点，只报代码质量与可维护性风险。 |
| Decision points | 若确认分类脆弱，后续由 Owner 决定是否引入 typed error classifier 或统一 privacy error taxonomy。 |
| Pre-execution checklist | 1. 读取 goal objective；2. 读取 redactor 实现；3. 检索调用方；4. 读取相关测试；5. 尝试运行目标包测试并记录环境限制。 |
| Concrete execution order | 先读实现，再读调用方与测试，最后输出 findings，不写额外报告。 |
