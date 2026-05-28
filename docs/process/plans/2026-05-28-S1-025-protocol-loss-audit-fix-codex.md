# 2026-05-28 S1-025 Protocol-loss evidence持久化修复（Codex 计划）
| Owner directive | Implement audit fix S1-025: persist protocol-loss evidence through settlement/abort/cache-hit billing path |
| Scope | 修改 backend/internal/gateway/forwarder_types.go、backend/internal/gatewayhttp/chat_completions_billing.go、backend/internal/billing/billing.go、backend/internal/billing/settler.go，以及必要的 billing 测试 |
| Success criteria | Settle/Abort/CommitCacheHit 在账单入库时写入 usage_records.protocol_loss（空则为 []，非空保留真实 JSON）；回归测试能验证输入的 protocol_loss 与数据库持久化一致（mutation 至 hardcoded [] 会失败） |
| Time estimate | 45 分钟（代码改造 20m + 测试 15m + 验证 10m） |
| Blast radius | 账单落库字段（usage_records.protocol_loss）；错误将影响审计可追溯性和问题归因 |
| Failure modes | 遗漏 Abort 入参导致 cache/abort 分支仍丢失 evidence；误用 json.RawMessage 导致空 nil 与空数组语义错位；测试 fixture 与 SQL 读取列不一致导致回归不判别 |
| Decision points | 1) Abort 是否通过 SettleRequest 新增字段承载 Loss（推荐） 2) 仅在 gateway 入口序列化后透传，还是 settle 层兜底重序列化（按最小侵入选前者） |
| Pre-execution checklist | 1. 确认 protocol_loss 列存在且参数已存在（已确认） 2. 遵守冻结包仅改既有文件 3. 只做结构最小变更，不改 sqlc/scheme |
| Concrete execution order | 1) 增加 UsageRecordDraft.ProtocolLoss carrier；2) 扩展 billing.SettleRequest（或保留仅通过 Draft + 新增 Abort 专用参数）
2) 在 gateway settle/abort 入口把 env.CapabilityGraph.ProtocolLoss 编码为 JSON；3) 在 settler 使用 jsonOrEmptyArray 引入 carried 值；4) 增加判别式 integration/单测并验证；5) 运行要求命令并汇总输出 |
