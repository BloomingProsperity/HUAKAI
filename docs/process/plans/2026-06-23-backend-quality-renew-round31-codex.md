# 2026-06-23 backend quality renew round31

| Owner directive | "根据我给你的文档继续刚刚未完成的renew"；并遵守"不要触碰到另一个目标" |
| Scope | 本轮只审查 `backend/internal/eventbus` 及其直接生产接线，重点是异步 money 投递、handler 超时、direct fallback 去重、DLQ 入队、worker 生命周期、测试判别力和注释纪律。不进入 security scan 目标，不读取或修改 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`。 |
| Success criteria | 输出中文 findings，按 S0/S1/S2/S3 分区；每条 finding 有真实 `file:line`、函数或类型、触发条件和可执行修法；若未发现 S1/S2，明确说明已读范围和残余风险。 |
| Time estimate | 本轮约 40-70 分钟代理时间，按源码阅读、生产接线核对和测试覆盖核对推进。 |
| Blast radius | 只新增本计划 artifact；业务代码、测试、schema 均不改。 |
| Failure modes | 1. 把安全问题展开成 security 审查：遇到纯跨租户/密钥问题只标"转 security 专项"。2. 忽略 direct fallback 与 eventbus 去重链路的交互：必须读生产接线。3. 只看 `RunOnce` 风格测试而漏掉真实 goroutine/timeout 行为：逐函数核 `Start/Stop/Emit/Register`。4. 触碰另一个目标：保持只读且不打开 `backend-security-scan` 计划。 |
| Decision points | 若发现需要改 billing ledger、quota enforcement、event schema、worker 部署参数或 production fallback 策略，只作为 finding 交 Owner 确认，本轮不直接改。 |
| Pre-execution checklist | 1. 已重新读取目标文件。2. 已读取 `api-gateway-risk-review` skill。3. 已确认当前 worktree 存在另一个 security scan 计划文件并避开。4. 先用 `rg` 定位 `Emit`、`Register`、runner、audit ref、DLQ 入队与测试。5. 核对 money handler 的幂等键、audit reference 与 timeout 语义是否能防重复 settle。 |

## Concrete Execution Order

1. 量化 `backend/internal/eventbus` 体量，读取 runner/bus/audit_ref/DLQ 相关文件。
2. 从 `cmd/gateway` 与 gatewayhttp/billing 调用点核对生产 money 投递链路。
3. 检查 handler timeout、goroutine 生命周期、Stop 语义、错误落盘和 DLQ 入队。
4. 阅读测试，确认是否覆盖 direct fallback 竞态、handler 超时、DLQ custom payload、audit reference 幂等。
5. 输出中文 findings，并如实说明本机测试不可运行原因。
