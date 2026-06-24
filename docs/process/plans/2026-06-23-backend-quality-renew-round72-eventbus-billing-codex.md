# 2026-06-23 backend quality renew round72 eventbus billing

| Owner directive | "根据我给你的文档继续刚刚未完成的renew" |
| Scope | 审查 `backend/internal/eventbus` 与 `backend/internal/gatewayhttp/chat_completions_billing.go` 的异步 completion bus、direct settle fallback、handler DLQ、post-delivery recovery 与测试覆盖；不展开纯 security 专项。 |
| Success criteria | 用源码核实 `CompletionBus.Emit` 失败到 direct settle 的边界；判断是否存在同一 claim 通过 bus 与 fallback 双重 settle 的竞态；核对 DLQ payload/replay 与 money-path audit ref 测试覆盖；输出中文 findings，含 `file:line`、问题、修法。 |
| Time estimate | 约 35-55 分钟墙钟；1 个 Codex 审查轮次。 |
| Blast radius | 本轮预期只新增计划文件和输出审查结论；不改 billing ledger、quota、eventbus 调度行为、DB schema 或迁移。 |
| Failure modes | 把幂等保护存在误判为无风险；只看 gateway fallback 不看 eventbus handler/DLQ；把 post-delivery DLQ 与 bus handler DLQ 混淆。缓解：读 runner、types、handler、gateway 调用点和相关 tests。 |
| Decision points | 若后续需要修改 billing settlement 幂等、eventbus 交付语义、DLQ schema/replay 或 quota/billing ledger，必须先请 Owner 确认。 |
| Pre-execution checklist | 1. 已重读 goal objective；2. 已读取 api-gateway-risk-review 技能；3. 已初扫 eventbus/gateway billing 调用点；4. 读取实现与测试；5. 运行可用检查与 clean-room 扫描。 |

