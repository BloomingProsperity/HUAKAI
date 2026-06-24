# 2026-06-23 backend quality renew round42

| Owner directive | "根据我给你的文档继续刚刚未完成的renew" |
| Scope | 只读审查 `backend/internal/credentialstore/postgres_store.go`、`backend/internal/credentialworker/mode_refresh.go` 及直接相关测试；不写审查报告文件，findings 直接回 Owner。 |
| Success criteria | 产出带 `file:line`、S0-S3、触发条件、可执行修法的中文 findings；覆盖代码质量、重复、错误分类脆弱性、测试假绿/弱覆盖。 |
| Time estimate | 约 20-35 分钟。 |
| Blast radius | 除本计划文件外不修改代码；不触碰高风险 auth/billing/quota/schema 逻辑。 |
| Failure modes | 把纯安全密钥泄露专项展开过深；只按目标文件行号复述而不核真码；忽略测试是否真的判别。缓解：逐段打开当前源码与测试，以当前工作树为准。 |
| Decision points | 如发现必须修改真实凭据加密、auth core、DB schema 或 refresh 状态机，先作为 finding 交 Owner，不在本轮直接改。 |
| Pre-execution checklist | 1. 不读取/修改 `backend-security-scan-codex.md`；2. 量化两个文件及包预算；3. 定位重复 scan/helper；4. 读取 refresh 错误分类和 adapter 分支；5. 尝试可用检查，若 `go` 不可用则如实记录。 |
| Concrete execution order | 先 `wc/rg/nl` 定位函数和测试，再审 `postgres_store.go` 的扫描、审计、tenant 绑定路径，最后审 `mode_refresh.go` 的错误分类和 vendor 分支。 |
