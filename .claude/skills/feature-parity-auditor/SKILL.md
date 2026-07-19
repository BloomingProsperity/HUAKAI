---
name: feature-parity-auditor
description: 检查行为证据中的每项有效能力是否在 HUAKAI 有真实处置、实现接线、运维入口和验收方向，防止静默丢失。
---

# 功能完整性审计

## 何时使用

- 行为合同完成后、设计前；
- 实现 slice 收口或外部项目更新后；
- Owner 怀疑“模块建了但没接线”。

## 前置输入

- `docs/02_CAPABILITY_CONTRACT.md`；
- `docs/03_FEATURE_PARITY_MATRIX.md`；
- `docs/04_FEATURE_LOCK.md`；
- 行为合同与 `docs/11_ACCEPTANCE_TEST_MATRIX.md`；
- HUAKAI 当前源码接线证据。

## 执行步骤

1. 列出行为合同中的每个 path/mode/state/actor 和用户/运营结果。
2. 验证每项都有 parity row 和合法 disposition。
3. 分开核实“代码存在”“真实接线”“当前状态”“目标处置”。
4. 拒绝 `Dropped / Ignored / Not needed / Too risky` 等无效处置。
5. 检查 Merged/Safe Equivalent 是否保留权限、状态、失败、审计和恢复结果。
6. 检查 Mandatory Roadmap 是否有触发条件、验收标准和发布阻断语义。
7. 检查实现项是否有正常、失败、并发/幂等和 operator recovery 测试方向。

## 输出

- 缺失/伪实现/未接线能力；
- 无效 disposition 与弱等价声明；
- 缺失测试和 release blockers；
- `Implemented / Better / Merged / Safe / Plugin / Flag / Mandatory Roadmap` 建议。

## 阻断项

代码文件或前端入口不能代替真实运行链证据；任何有效能力无处置或无接线时阻止 parity closure。
