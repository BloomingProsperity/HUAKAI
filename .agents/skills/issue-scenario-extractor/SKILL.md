---
name: issue-scenario-extractor
description: 将公开 issue、事故、讨论和运营投诉转换为 clean-room 的真实失败场景、恢复要求和判别测试输入。
---

# Issue 场景提取

## 何时使用

- 外部项目或 HUAKAI 有公开 bug/事故/运营投诉；
- happy path 已有，但失败和人工恢复场景不足；
- 需要从真实问题补强 acceptance tests。

## 前置输入

- issue/事故原文、版本、环境和时间；
- 相关行为合同；
- `docs/08_REAL_WORLD_SCENARIOS.md`、`docs/09_BUG_PATTERN_LIBRARY.md`、`docs/11_ACCEPTANCE_TEST_MATRIX.md`。

## 执行步骤

1. 提取 actor、前置条件、触发、可观察失败、影响和期望恢复。
2. 区分事实、报告者推断和维护者尚未核实的猜测。
3. 不复制非许可 patch；若机制结论依赖源码，转交 `reference-project-miner` 核实。
4. 把一次问题抽象为可复发 bug pattern，并指出可能辐射的兄弟模块。
5. 设计正常、失败、并发/重放和 operator recovery 场景。
6. 更新现有场景、bug pattern 和 acceptance matrix，不另建重复报告。

## 输出

- Actor / Preconditions / Trigger / Failure / Impact / Expected recovery；
- 影响能力和全链位置；
- 可判别测试方向与证据链接。

## 阻断项

issue 只能证明有人报告过现象，不能单独证明当前生产机制；未经源码或复现核实，不把 issue 推断写成事实。
