# 2026-06-23 backend-quality-renew-round19-codex

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | Codex 独立继续后端代码质量与架构 renew；本轮聚焦 quota / budget / billing 相关编排的重复状态机、测试可运行性证据、codebudget 压力。只读源码与测试，必要时新增本计划文件；不写 findings 报告 md，不触碰另一个 security-scan 目标。 |
| Success criteria | 输出至少一组经过源码行号核实的增量发现；每条含具体 `file:line`、函数/类型、问题、修法；明确哪些点因证据不足不下结论。 |
| Time estimate | 约 30-45 分钟墙钟；一个 Codex 审查轮次。 |
| Blast radius | 计划文件为低风险文档；源码只读无运行态影响。若后续建议涉及 quota / billing / budget 实现修改，需另开小 slice 并按 money-path 规则审查。 |
| Failure modes | 误把纯安全问题混入质量专项；重复既有 findings；只看文档不看代码；把 integration 测试存在误判为 CI 已运行。缓解：只用当前源码、测试、工作树命令作证据；不展开纯安全结论；显式标注验证限制。 |
| Decision points | 是否把 quota / budget / billing 的重复状态机抽公共策略，需要 Owner 后续确认；本轮只给审查结论，不直接改 money-path 代码。 |
| Pre-execution checklist | 1. 重读目标文件与 `api-gateway-risk-review` skill；2. 确认工作树并避开 `backend-security-scan` 计划；3. 读取 quota/budget/billing 真实代码与测试；4. 检查 Go 工具链可用性；5. 输出中文 findings。 |
| Concrete execution order | 1. 用 `rg`/`nl` 定位 quota、budget、billing 关键函数；2. 对照测试是否判别重复状态机与失败分支；3. 量化 codebudget / 文件长度；4. 汇总 S1/S2/S3 增量结论。 |
