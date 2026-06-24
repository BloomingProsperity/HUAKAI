# 2026-06-23 backend quality renew round45 codex

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 本轮只审查 money-coupled 周边: `backend/internal/settlementrecovery`、`backend/internal/eventbus`、`backend/internal/cache*`、`backend/internal/mediatask` 及其与 gateway/billing 的接线。输出 findings 直接回给 Owner, 不写 findings 报告文件。 |
| Out of scope | 不修改生产代码; 不触碰 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`; 不展开纯 security 专项; 不审前端。 |
| Success criteria | 每条发现都有真码证据、`file:line`、严重度、问题说明与可执行修法; 覆盖异步 money 投递、DLQ/补偿、cache hit 计费链、media task 第二计费链、worker 生命周期、测试假绿/弱测。 |
| Time estimate | 约 45-75 分钟墙钟; 1 个 Codex 审查分片。 |
| Blast radius | 只读审查与新增计划文件, 不影响运行时代码。 |
| Failure modes | 误把文档状态当实现状态: 以 `.go` 真码和测试为准; 混入 security 专项: 只保留转 security 指针; 与别的目标冲突: 不读取/修改 backend security scan 计划。 |
| Decision points | 若发现 S0/S1 资金/补偿风险, 本轮只报告并给修法, 不直接改 money path; 需要 Owner 后续确认是否开修复分片。 |
| Pre-execution checklist | 1. 已重读 objective 文件; 2. 已读取 `api-gateway-risk-review` 技能; 3. 确认 round45 计划不存在; 4. 先量化包体量与入口函数; 5. 再逐链路核对失败/重试/幂等/观测。 |
| Concrete execution order | 1. `rg`/`wc` 量化目标包; 2. 读 settlementrecovery payload/worker/proof/replay; 3. 读 eventbus runner/audit_ref 与 gateway fallback 接线; 4. 读 cache hit CommitCacheHit 路径与 L2 不变量; 5. 读 mediatask money store/worker; 6. 搜 integration_pg/测试覆盖断层; 7. 尝试运行相关 Go 测试并记录环境限制。 |
