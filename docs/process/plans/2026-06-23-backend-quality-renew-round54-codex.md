# 2026-06-23 backend-quality-renew-round54-codex

| Owner directive | "根据我给你的文档继续刚刚未完成的renew" |
| Scope | 聚焦 `backend/internal/userkeycontrols`、`backend/internal/userkeycontrolshttp`、`backend/sql/queries/userkey_controls.sql` 及 API key 控制在 auth/quota 读路径中的接线；不触碰另一个 security-scan 目标，不修改生产代码。 |
| Success criteria | 输出带 `file:line` 证据的中文 findings，覆盖配额/IP/model/group 控制的生产语义、事务一致性、测试运行真实性、包/文件体量债务。 |
| Time estimate | 约 35-50 分钟人工审查等价时间；本轮 agent 时间按一个切片执行。 |
| Blast radius | 只新增本计划文件；后续为只读审查和可用检查。若发现需要改 quota enforcement、auth core、schema 或真实 secrets，停止并作为 Owner 确认项记录。 |
| Failure modes | 只看 HTTP 单元测试而漏 SQL 读路径；把控制面设置存在误判为 relay 已 enforce；重复报告泛泛 CI 假绿而不落到 userkeycontrols 证据；误碰 security-scan 计划文件。 |
| Decision points | 若发现需要修改数据库 schema、quota enforcement、auth resolver 或生产权限逻辑，本轮不直接改，先输出审查结论。 |
| Pre-execution checklist | 1. 已重读 goal objective；2. 已读取 `production-scenario-review` skill；3. 不读取/修改 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`；4. 使用 `rg`/`nl`/`wc` 读取当前源码；5. 最后运行可用测试命令并记录环境限制。 |

## Concrete Execution Order

1. 统计 `userkeycontrols` / `userkeycontrolshttp` / SQL 生成文件体量和测试分布。
2. 阅读 service/store/SQL，确认 quota/group/IP/model 控制的写入和 owner/tenant scope。
3. 阅读 auth resolver、quota resolver、relay handlers，确认控制面字段是否被运行时真正消费。
4. 阅读测试，特别是 integration_pg 是否带 tag、是否读正确 env、是否有判别式断言。
5. 运行可用检查；如果 Go 工具链不存在，如实记录。
6. 直接在 chat 输出 `## S0/S1/S2/S3` findings 和重构优先级表，不写 findings `.md`。
