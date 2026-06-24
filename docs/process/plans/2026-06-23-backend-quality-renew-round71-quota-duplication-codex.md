# 2026-06-23 backend quality renew round71 quota duplication

| Owner directive | "根据我给你的文档继续刚刚未完成的renew" |
| Scope | 审查 `backend/internal/quota` 与 `backend/internal/quotaenforce` 的代码质量、重复分支、quota reservation 事务边界、post-commit fail-open 观测与测试运行性；不展开纯 security 专项。 |
| Success criteria | 以源码核实 `evaluatePolicies` / reservation 应用路径是否仍存在 MetricRequests / CostUSD / TokensEstimated 分支重复；核对 `quotaenforce` 与 reconciler 测试是否覆盖失败恢复；输出中文 findings，含 `file:line`、问题、修法。 |
| Time estimate | 约 35-50 分钟墙钟；1 个 Codex 审查轮次。 |
| Blast radius | 本轮预期只新增计划文件和输出审查结论；不改 quota enforcement、billing ledger、DB schema 或迁移。 |
| Failure modes | 把安全/租户问题展开为本专项；把 integration_pg 存在误判为 CI 已覆盖；只看 service.go 不看 service_assess/service_settle/quotaenforce。缓解：逐文件读实现与测试，并如实记录工具链缺失。 |
| Decision points | 若后续要改 quota 事务、billing ledger、DB schema、迁移或强一致 fail-open 策略，必须先请 Owner 确认。 |
| Pre-execution checklist | 1. 已重读 goal objective；2. 已读取 api-gateway-risk-review 技能；3. 已量化 quota/quotaenforce 体量；4. 读取 Reserve/assessment/settle/reconciler/test；5. 运行可用检查与 clean-room 扫描。 |

