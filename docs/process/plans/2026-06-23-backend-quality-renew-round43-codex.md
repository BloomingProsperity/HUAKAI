# 2026-06-23 backend quality renew round43

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 本轮只审查 relay 数据面质量热点：`backend/internal/gateway/forwarder.go`、`backend/internal/gateway/upstream_dispatcher*.go`、`backend/internal/proto/*` 中与流式恢复、非流缓冲上限、HCSF 拷贝、包/文件预算相关的代码。明确不触碰另一个目标的 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`。 |
| Success criteria | 输出中文 findings，包含具体 `file:line`、严重度 S0-S3、问题触发条件和可执行修法；至少覆盖目标文件要求的 timer/drain/stream 恢复分派、缓冲上限重复、HCSF 拷贝性能债、包体量预算。 |
| Time estimate | 约 30-45 分钟墙钟时间；1 个 Codex 审查批次。 |
| Blast radius | 只读源码与测试，新增本计划文件；不改生产代码、不改测试、不改 schema、不改 LICENSE。 |
| Failure modes | 误把安全专项问题展开为本轮结论：只作为转 security 指针；误信陈旧文档：以 `.go` 真码和测试为准；行号漂移：实际打开文件核对。 |
| Decision points | 若发现需要改 schema、auth/billing/quota 强一致逻辑或删除文件，只记录为需要 Owner 确认，不执行。 |
| Pre-execution checklist | 1. 重读 goal objective；2. 重读 `api-gateway-risk-review` skill；3. 检查 worktree，避免触碰另一个目标；4. 用 `rg`/`wc` 定位热点；5. 读源码和测试；6. 尝试可用测试，若无 Go 工具链则如实记录。 |

## Concrete Execution Order

1. 量化 `internal/gateway` 与 `internal/proto` 非测试行数、文件数、baseline 位置。
2. 读取 `forwarder.go` 的主循环、timer 创建/停止、EOF/error/drain/补帧逻辑。
3. 对比 `forwarder.go` 与 `protosse/reconstruct.go` 的 stream state 分派，确认新增 proto 族是否需要双处同步。
4. 对比 gatewayhttp 与 gateway dispatcher 的非流响应缓冲上限，确认硬编码重复与测试覆盖。
5. 读取 HCSF clone 路径，确认是否每请求 JSON 深拷贝。
6. 汇总中文 findings；不写 `.md` findings 报告。
