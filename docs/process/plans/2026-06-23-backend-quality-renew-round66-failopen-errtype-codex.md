# 2026-06-23 backend-quality-renew round66 failopen-errtype

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 审查 `backend/internal/budget`、`backend/internal/budgetenforce`、`backend/internal/quotaenforce`、`backend/internal/subscriptionenforce` 中 fail-open/fail-closed 语义、错误分类与 metric label 稳定性。 |
| Out of scope | 不修改余额、配额、订阅、计费 ledger 生产逻辑；不改数据库 schema；不展开跨租户或密钥安全专项。 |
| Success criteria | 输出中文 findings，逐条带绝对路径行号、具体函数/类型、触发条件和可执行修法。 |
| Time estimate | 约 30-45 分钟；一次 Codex renew 审查切面。 |
| Blast radius | 默认只读源码并写计划文件；若误改 fail policy 会影响 money-adjacent 放行/拒绝路径，因此本轮不做生产代码改动。 |
| Failure modes | 把有意的软限流 fail-open 误判成主闸漏洞；只读实现不读测试；把 metric label 问题夸大成安全专项。缓解：按 api-gateway-risk-review 技能核对 workflow、失败模式、账本/配额一致性、观测维度和测试方向。 |
| Decision points | 若确认策略分散成立，后续由 Owner 决定是否抽统一 `failpolicy`/`enforcementpolicy` 包，并安排兼容性测试。 |
| Pre-execution checklist | 1. 读取 goal objective；2. 读取 api-gateway-risk-review 技能；3. 读取四处 enforcement 实现；4. 检索错误分类和 fail-mode 测试；5. 尝试运行目标包测试并记录环境限制。 |
| Concrete execution order | 先读实现，再读测试和调用方，最后输出 findings，不写额外报告。 |
