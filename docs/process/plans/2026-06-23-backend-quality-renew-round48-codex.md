# 2026-06-23 后端质量 renew round48 测试质量与死代码审查

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 审查 `backend/.github/workflows`、`backend/internal/codebudget`、deadcode baseline、provider / integration 测试占位与 no-op skip、`cmd/gateway` 退款回执相关死代码线索。只输出 findings，不写 findings 报告文件。 |
| Out of scope | 不修改业务实现，不删除 deadcode，不调整 CI，不触碰另一个目标的计划文件。 |
| Success criteria | 每条发现有真实文件行号、具体触发条件、严重度和可执行修法；区分“测试未运行”“测试占位”“预算门盲区”“死代码豁免”四类风险。 |
| Time estimate | 30-45 分钟 agent 时间；墙钟取决于搜索与工具链可用性。 |
| Blast radius | 只读源码与新增计划文件；无生产行为影响。 |
| Failure modes | 误把 build-tag 测试当作已覆盖；误把普通 skip 当 no-op 占位；把历史 deadcode 豁免误判为当前引用。缓解：用 `rg`、`nl`、`go test` 实际命令和当前文件行号交叉确认。 |
| Decision points | 若后续要改 CI、删 deadcode、改 baseline 或引入新测试依赖，需要 Owner 另行确认。 |
| Pre-execution checklist | 1. 重新读取目标文件；2. 读取 production-scenario-review 技能；3. 确认 round48 计划文件不存在；4. 搜索 CI、build tag、skip、deadcode baseline、codebudget walker；5. 尝试运行可用检查并记录工具链状态。 |
| Concrete execution order | 先核 CI 与 `integration_pg`；再核 no-op skip；再核 deadcode baseline 与 `cmd/gateway` 回执线索；最后核 codebudget walker 是否仍只覆盖 `internal/`，输出中文 findings。 |
