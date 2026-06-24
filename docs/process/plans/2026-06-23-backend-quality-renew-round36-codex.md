# 2026-06-23 backend quality renew round36

| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；“不要触碰到另一个目标，你做你的，他做他的”；“做完了？这么快？这么大的项目你这么快？” |
| Scope | 只审查 money-path 相关的后端代码质量、测试判别力与一致性风险，重点看 `backend/internal/billing/settler.go`、`backend/internal/quotaenforce/`、`backend/internal/budget/`、`backend/internal/subscription/` 与相关测试。明确不读取、不修改 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`，不改业务代码、不改 schema、不碰真实凭据。 |
| Success criteria | 输出中文 S0-S3 findings，必须带具体 `file:line` 证据；区分真实 bug、测试弱覆盖、结构债与已被现有代码防住的路径；不把未读代码当事实。 |
| Time estimate | 约 45-75 分钟墙钟；本轮只做一个可闭合 review slice。 |
| Blast radius | 审查本身只读，新增本计划 artifact；若后续建议修复，可能影响计费定稿、quota 预留/释放、订阅权益、余额冻结/扣减等钱路径，属于高风险实现区，本轮不直接改。 |
| Failure modes | 误把测试 stub 当生产行为；漏读跨包调用导致误判；只看 happy path 漏掉重复 settle、并发重试、DLQ/recovery、quota fail-open/fail-closed 分支；把历史文档当当前事实。缓解：优先读当前 `.go` 和测试，必要时用 `rg` 回溯调用链，结论只写已核实路径。 |
| Decision points | 任何需要改 schema、账本、quota enforcement、billing ledger、支付逻辑或生产 migration 的建议，只作为 Owner 待确认项，不在本轮执行。 |
| Pre-execution checklist | 1. 重新确认目标文件要求与不触碰另一个目标；2. 统计 money-path 文件体量；3. 读 settler/quota/budget/subscription 入口与关键测试；4. 核对 retry/idempotency/重复 settle/异常恢复；5. 核对测试是否判别真实 money outcome；6. 输出中文 findings。 |
| Concrete execution order | 先用 `rg` 找 settle/quota/subscription 主入口和调用点；再读取核心实现与测试；随后核对幂等、并发、失败恢复、审计字段和测试判别性；最后只输出 findings，不做实现修改。 |
