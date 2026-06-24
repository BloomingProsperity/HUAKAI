# 2026-06-23 backend-quality-renew round65 tenant-resolver

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 审查 `backend/internal/adminuserhttp/tenant_scope.go`、`backend/internal/alertinghttp/helpers.go`、`backend/internal/moderationhttp/helpers.go`、`backend/internal/usernoticehttp/handlers.go` 中租户解析逻辑的重复、fallback 顺序、错误语义与测试覆盖。 |
| Out of scope | 不修改鉴权核心、不改数据库 schema、不改生产 handler 行为、不展开跨租户 security 专项；只做代码质量与漂移风险审查。 |
| Success criteria | 输出中文 findings，逐条带绝对路径行号、具体函数/类型、触发条件和可执行修法。 |
| Time estimate | 约 25-35 分钟；一次 Codex renew 审查切面。 |
| Blast radius | 默认只读源码并写计划文件；若误改 tenant 解析可能影响后台接口可用性和错误码一致性，因此本轮不做生产代码改动。 |
| Failure modes | 只比较函数名而不读调用上下文；把不同产品域的有意差异误判为漂移；把安全问题展开成 security 专项。缓解：逐文件读实现、调用方和测试，只报代码质量与可维护性风险。 |
| Decision points | 若确认重复成立，后续由 Owner 决定是否抽 `internal/tenantresolver` 或复用现有 claims helper，并安排兼容性测试。 |
| Pre-execution checklist | 1. 读取 goal objective；2. 读取四处 helper；3. 读取调用 handler；4. 检索相关测试；5. 尝试运行目标包测试并记录环境限制。 |
| Concrete execution order | 先读源码，再读测试和错误映射，最后输出 findings，不写额外报告。 |
