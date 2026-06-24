# 2026-06-23 backend-quality-renew-round2-codex

| Owner directive | `根据我给你的文档继续刚刚未完成的renew` |
| --- | --- |
| Scope | 继续 HUAKAI 后端 renew 第二轮深审。基于 `goal-objective.md` 的点名范围，补足第一轮没有深挖的 worker 生命周期、money path、gatewayhttp 拆包边界、测试质量与重复债务。 |
| Out of scope | 不做 security 专项；不读取或修改其它目标的计划/产物；不写 findings `.md` 报告；不修改生产代码、schema、LICENSE、secrets、部署脚本。 |
| Success criteria | 产出第二轮中文 findings，必须来自当前工作树真实代码，每条有绝对路径、行号、函数/类型名、问题和修法；明确说明哪些仍未全量覆盖，不能再把 triage 说成全仓完成。 |
| Time estimate | 本轮约 1-2 小时 agent 时间，目标是深挖三到四个高风险切面，而不是声称全仓终审。 |
| Blast radius | 本轮唯一写入是本计划文件；审查阶段只读。误判风险来自代码规模大、路径多、测试覆盖间接，缓解方式是每条 finding 都打开源码核实。 |
| Failure modes | 1. 只重复第一轮结论：改为优先读未覆盖切面。2. 混入其它目标：不读取 `backend-security-scan-codex.md` 等无关计划。3. 泛泛而谈：没有 `file:line` 的发现不输出。4. 把局部审查说成完成：最终明确剩余范围。 |
| Decision points | 若发现需要删除文件、拆核心 money/auth/quota 包、改 schema 或改部署，只报告，不执行。 |
| Pre-execution checklist | 1. 确认分支和工作树。2. 不读取其它目标计划。3. 先建本轮计划。4. 优先核 worker lifecycle。5. 再核 money path 与 gatewayhttp 拆包边界。 |

## 具体执行顺序

1. 列出并量化后台 worker：`credentialworker`、`billing` sweep/reconcile、`audit` refund、`auditledger` DLQ、`settlementrecovery`、`eventbus`、`modelsync`、`alerting`、`payment` expire、`mediatask`。
2. 对每个 worker 核 `Start`/`Stop`/`ticker.Stop`/ctx 传播/运行时接线，找确定性生命周期缺口。
3. 深挖 money path：`billing.DefaultSettler`、`quotaenforce`、`budgetenforce`、`settlementrecovery`、`eventbus` critical handler、cache hit commit。
4. 梳理 `gatewayhttp` 文件职责、路由挂载、依赖类型，给可执行拆包边界。
5. 补测试质量和重复债务中第一轮未核实的条目，最终只输出证据充分的第二轮 findings。
