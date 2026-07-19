---
name: pm-orchestrator
description: 在 Owner 指派当前 agent 统筹时，维护唯一计划、能力处置、风险、执行顺序、审查和发布状态，不启动旧的并行双计划。
---

# 单计划编排

## 何时使用

- 非平凡目标需要跨模块调研、设计、实现和发布收口；
- 多个能力、风险和依赖需要统一排序；
- Owner 要求一个 agent 全权负责当前目标。

## 前置输入

- `AGENTS.md`、`docs/RULES.md`；
- Owner 最新指令；
- 当前唯一计划、branch/PR 状态；
- parity、risk、acceptance 和 release gate。

## 执行步骤

1. 确认目标、worktree、分支、唯一计划和唯一 PR，不碰其他目标。
2. 按领域调用许可证审计与源码调研，先形成行为合同。
3. 读取 HUAKAI 真码，建立 shape、全链、依赖、爆炸半径和失败模式。
4. 把能力分为实现、融合、Safe Equivalent、Plugin/Flag 或 Mandatory Roadmap。
5. 更新一个计划，排执行切片、判别测试、恢复和 Owner 决策点。
6. 推动实现、测试、独立 review 和小提交；不为同一目标创建平行计划。
7. 每个 slice 收口真实状态，删除被覆盖的旧规则、计划和错误注释。
8. 发布前调用 `release-readiness-gate`，不擅自 merge。

## 输出

- 当前唯一计划和状态；
- 清晰执行顺序、依赖、验收和风险；
- Owner 决策材料；
- PR/release 状态。

## 阻断项

计划缺前置源码证据、存在两个互相竞争的权威计划、S0/S1 未关或 Owner 尚未批准 merge 时，不得宣称目标完成。
