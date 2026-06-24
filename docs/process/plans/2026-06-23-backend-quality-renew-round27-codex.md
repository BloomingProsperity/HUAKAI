# 2026-06-23 backend-quality-renew-round27-codex

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 本轮只审查 `backend/internal/credentialworker` 及其直接生产接线、SQL/测试证据。重点是 `mode_refresh.go`、`scheduler.go`、worker 生命周期、错误分类、审计与测试判别力。不审查前端，不触碰另一个目标文件。 |
| Success criteria | 输出中文 findings，按 S0/S1/S2/S3 分区；每条发现包含具体文件行号、函数/类型、问题和可执行修法；不写 findings `.md` 报告。 |
| Time estimate | 约 45-70 分钟人工等价审查；本轮以只读核证为主。 |
| Blast radius | 只新增本计划 artifact；不改业务代码、不改测试、不改 schema。 |
| Failure modes | 误信陈旧文档：以当前 `.go`、`.sql`、测试为准；重复上一轮发现：对既有结论只在 credentialworker 内有新证据时纳入；把安全专项展开过深：纯安全项只标注转 security。 |
| Decision points | 若发现需要改 schema、auth core、billing ledger、quota enforcement 或真实部署配置，停止在 findings 中标为需 Owner 确认，不直接修改。 |
| Pre-execution checklist | 1. 重读目标文件；2. 读取适用 review skill；3. 确认 worktree 中另一个目标文件只忽略不触碰；4. 量化 `credentialworker` 体量；5. 读取生产接线；6. 精读 scheduler/refresher/mode_refresh/audit/health 流程；7. 核对测试和注释规则。 |
| Concrete execution order | 1. `wc -l` 和 `rg` 确认包体量与入口；2. 读取 `cmd/gateway/wiring.go` 中 scheduler 接线；3. 读取 `scheduler.go` 的 ctx/ticker/storm/audit 路径；4. 读取 `mode_refresh.go` 的 provider adapter 与错误分类；5. 读取 `audit.go`、`health_state.go`、`refresher.go` 的状态写入；6. 读取对应测试；7. 汇总 findings。 |
