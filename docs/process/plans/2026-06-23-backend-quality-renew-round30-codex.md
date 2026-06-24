# 2026-06-23 backend quality renew round30

| Owner directive | "根据我给你的文档继续刚刚未完成的renew"；并遵守"不要触碰到另一个目标" |
| Scope | 本轮只审查 `backend/internal/dlq` 及其直接生产接线、worker 生命周期、重放错误保真、retry/quarantine 决策、测试判别力和注释纪律；结合 `settlementrecovery` 的使用场景验证钱路恢复队列本体是否可靠。不进入另一个 security scan 目标，不修改 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`。 |
| Success criteria | 输出中文 findings，按 S0/S1/S2/S3 分区；每条 finding 有真实 `file:line`、函数或类型、触发条件和可执行修法；若未发现 S1/S2，明确说明已读范围和残余风险。 |
| Time estimate | 本轮约 35-60 分钟代理时间，按源码阅读和测试覆盖核对推进。 |
| Blast radius | 只新增本计划 artifact；业务代码、测试、schema 均不改。 |
| Failure modes | 1. 把 DLQ 框架通用问题夸大为 settlementrecovery 必现：必须核调用链。2. 漏看 worker Stop/RunOnce/ProcessClaim 差异：逐函数读。3. 把纯安全问题展开：遇到跨租户或密钥泄露只标"转 security 专项"。4. 触碰另一个目标：保持只读且不打开 `backend-security-scan` 计划。 |
| Decision points | 若发现需要改 billing ledger、DLQ schema、worker 部署配置或生产重试策略，只作为 finding 交 Owner 确认，本轮不直接改。 |
| Pre-execution checklist | 1. 已重新读取目标文件。2. 已读取 `api-gateway-risk-review` skill。3. 已确认当前 worktree 存在另一个 security scan 计划文件并避开。4. 先用 `rg` 定位 `ProcessClaim`、`Replay`、`Worker.Start/Stop`、`MarkFailed`、`ErrUnretryable`。5. 核对 DLQ 测试是否覆盖 worker 自动路径和 money-path post_delivery_settlement。 |

## Concrete Execution Order

1. 量化 `backend/internal/dlq` 体量，读取 service/store/worker/types/handler。
2. 对比 `Replay` 与 `ProcessClaim` 的错误处理和状态持久化。
3. 核对 worker Start/Stop/RunOnce 生命周期、timer/ticker 释放和 lane drain 策略。
4. 阅读测试，检查是否能让 handler 错误丢失、mark failed 失败、unretryable 分类漂移等 mutation 变红。
5. 输出中文 findings，并如实说明本机测试不可运行原因。
