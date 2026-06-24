# 2026-06-23 backend quality renew round33

| Owner directive | "根据我给你的文档继续刚刚未完成的renew"；并遵守"不要触碰到另一个目标" |
| Scope | 本轮只审查 `backend/internal/mediatask` 及其直接生产接线，重点是第二条计费链的 claim/hold/settle/abort/idempotency、worker 租约与生命周期、任务状态机、测试判别力和注释纪律。不进入 security scan 目标，不读取或修改 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`。 |
| Success criteria | 输出中文 findings，按 S0/S1/S2/S3 分区；每条 finding 有真实 `file:line`、函数或类型、触发条件和可执行修法；若未发现 S1/S2，明确说明已读范围和残余风险。 |
| Time estimate | 本轮约 45-75 分钟代理时间，按源码阅读、生产接线核对和测试覆盖核对推进。 |
| Blast radius | 只新增本计划 artifact；业务代码、测试、schema 均不改。 |
| Failure modes | 1. 把安全问题展开成 security 审查：遇到跨租户/密钥泄露只标"转 security 专项"。2. 只看 happy-path handler 而漏 worker 租约与失败路径：必须读 worker/store_money/store_integration_test。3. 被 integration 测试误导：必须核 build tags 与 env。4. 触碰另一个目标：保持只读且不打开 `backend-security-scan` 计划。 |
| Decision points | 若发现需要改 billing ledger、mediatask schema、worker lease 语义或生产部署参数，只作为 finding 交 Owner 确认，本轮不直接改。 |
| Pre-execution checklist | 1. 已重新读取目标文件。2. 已读取 `api-gateway-risk-review` skill。3. 已确认当前 worktree 存在另一个 security scan 计划文件并避开。4. 先用 `rg` 定位 `Claim`、`Settle`、`Abort`、`mediaClaimKey`、`Worker`、`integration_pg`。5. 核对本机 `go` 是否可用后再决定能否运行测试。 |

## Concrete Execution Order

1. 量化 `backend/internal/mediatask` 体量，读取 handler/store_money/worker/store/types/test 文件。
2. 从 `cmd/gateway` 核对 mediatask worker 和 HTTP/API 生产接线。
3. 检查 money claim/hold/settle/abort/idempotency 与主链 billing 状态机是否一致。
4. 检查 worker lease、并发抢占、失败重试、Stop 语义和 integration 测试是否真实可跑。
5. 输出中文 findings，并如实说明本机测试不可运行原因。
