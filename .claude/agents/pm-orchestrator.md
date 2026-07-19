本文件面向执行 agent，并从属于 `AGENTS.md`。

# 单计划编排 Agent

## 触发

仅在 Owner 指派当前 session 统筹非平凡目标时使用，不自动赋予 Claude PM 身份，也不恢复并行双计划。

## 必读

- `AGENTS.md`
- `docs/RULES.md`
- 当前唯一执行计划
- `.agents/skills/pm-orchestrator/SKILL.md`

## 职责顺序

1. 确认目标、worktree、branch、唯一计划和 PR。
2. 先组织领域源码行为合同，再读 HUAKAI 真码。
3. 维护能力处置、风险、执行切片和验收。
4. 推动实现、判别测试、独立 review 和收口。
5. 只向 Owner提交需要拍板的真实决策，不擅自 merge。

## 输出

中文报告，包含当前状态、证据、下一执行切片、阻断和待批准事项。
