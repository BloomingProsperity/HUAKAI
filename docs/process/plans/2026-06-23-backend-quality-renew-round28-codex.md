# 2026-06-23 backend-quality-renew-round28-codex

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 本轮只审查 `backend/internal/settlementrecovery`、其直接生产接线、DLQ replay handler 协议和相关测试证据。重点是 post-delivery settlement 失败兜底、payload 校验、重放幂等、审计证明、worker/replay 可观测性。不审查前端，不触碰另一个目标文件。 |
| Success criteria | 输出中文 findings，按 S0/S1/S2/S3 分区；每条发现包含具体文件行号、函数/类型、问题和可执行修法；不写 findings `.md` 报告。 |
| Time estimate | 约 45-70 分钟人工等价审查；以只读核证为主。 |
| Blast radius | 只新增本计划 artifact；不改业务代码、不改测试、不改 schema。 |
| Failure modes | 把纯安全问题展开过深：只标注转 security；误判 DLQ 框架行为：必须读取 `settlementrecovery` 与直接 replay 接线；重复既有 eventbus 结论：只记录本子系统新增证据。 |
| Decision points | 若发现需要改 billing ledger、quota enforcement、DLQ schema、auth/RBAC 或生产部署配置，停止在 findings 中标为需 Owner 确认，不直接修改。 |
| Pre-execution checklist | 1. 重读目标文件；2. 读取适用 review skill；3. 确认 worktree 中另一个目标文件只忽略不触碰；4. 量化 `settlementrecovery` 体量；5. 读取 `cmd/gateway/wiring.go` 生产接线；6. 读取 payload/proof/handler/enqueue；7. 读取对应测试。 |
| Concrete execution order | 1. `rg` 定位生产 enqueue/replay 接线；2. 读取 `payload.go` 的输入契约与校验；3. 读取 `handler.go` 的 committed proof、settler replay 与错误分类；4. 读取 `enqueue.go` 的 DLQ envelope；5. 读取 `postgres_proof.go` 的账本证明；6. 读取测试；7. 汇总 findings。 |
