---
name: acceptance-test-writer
description: 把行为合同、真实事故、bug pattern、parity 义务和运维恢复转换为会抓真缺陷的正常/失败/并发/恢复验收测试。
---

# 验收测试设计

## 何时使用

- 行为合同和 HUAKAI 适配方案完成后；
- 修复跨模块、money/auth/schema 或恢复缺陷；
- 完整 slice 收口前。

## 前置输入

- `docs/02_CAPABILITY_CONTRACT.md`；
- `docs/08_REAL_WORLD_SCENARIOS.md`；
- `docs/09_BUG_PATTERN_LIBRARY.md`；
- `docs/11_ACCEPTANCE_TEST_MATRIX.md`；
- 真实入口、存储和 worker 接线。

## 执行步骤

1. 选定 capability、actor 和要防的具体缺陷。
2. 写正常路径的前置、动作、最终状态和可观察证据。
3. 写错误、超时、崩溃、部分成功和 operator recovery。
4. 涉及共享状态时加入并发、跨节点重放和幂等冲突。
5. 为每个测试给 mutation：删哪个守卫/接线后必须变红。
6. 使用判别 fixture，断言最终 balance/hold/quota/state/audit/DLQ，而不只断言 HTTP 状态。
7. 指定 unit、PostgreSQL integration、race、fault injection、container/real upstream 层级。
8. 更新现有 test matrix，不另建重复列表。

## 输出

- `AT-*` 行；
- Preconditions / Steps / Expected result / Evidence / Recovery；
- mutation 与测试层级；
- 未能实测的环境盲区。

## 阻断项

测试在破坏目标守卫后仍通过、fixture 不判别、用 `t.Skip`/nil stub 掩盖风险，均视为缺测试而不是通过。
