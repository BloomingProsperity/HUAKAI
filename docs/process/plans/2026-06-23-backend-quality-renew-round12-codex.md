# 2026-06-23 backend quality renew round12
| Owner directive | "根据我给你的文档继续刚刚未完成的renew" |
| Scope | In: `internal/eventbus` 异步 money 投递、`gatewayhttp` completion bus fallback、`settlementrecovery` DLQ 幂等、budget/quota/subscription fail-open 策略；Out: security 专项、另一个 security 目标计划、生产代码修改、前端。 |
| Success criteria | 基于真实源码输出中文增量 findings；每条 finding 有 `file:line`、具体风险、可执行修法；不声称整个 renew 完成。 |
| Time estimate | 30-45 分钟墙钟；1 个 Codex 审查轮。 |
| Blast radius | 本轮只读源码并新增计划文档；不会改生产代码。 |
| Failure modes | 把正常幂等 fallback 误判成双结算；缓解: 同时读 Emit 调用方、eventbus runner、audit_ref 去重与 tests。把纯安全问题扩展出界；缓解: 只从代码质量/竞态/静默放行角度记录。误碰另一个目标；缓解: 不读取、不修改 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`。 |
| Decision points | 若发现强一致 money gate fail-open 或重复结算路径，作为 finding 提交 Owner 决策；不在本轮直接修复。 |
| Pre-execution checklist | 1. 定位 completion bus Emit 调用和 direct settle fallback；2. 读取 eventbus runner/Bus/audit_ref 去重；3. 读取 settlementrecovery payload/handler/worker 幂等；4. 对照 fail-open 策略实现；5. 汇总中文 findings。 |
