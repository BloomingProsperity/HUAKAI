---
name: release-readiness-gate
description: 基于真实接线、parity、clean-room、许可证、测试、安全、钱路、运营恢复和部署证据判定是否可发布。
---

# 发布就绪门

## 何时使用

- 完整 slice 声明完成；
- 打开最终 PR 或请求 Owner 合并前；
- 生产部署前。

## 前置输入

- 当前唯一计划和 PR diff；
- parity matrix、risk register、acceptance matrix、release gates；
- build/test/review/container/real-upstream 证据；
- 未验证项清单。

## 执行步骤

1. 核实每项有效能力有合法处置且真实入口/DI/worker/状态回流接通。
2. 核实 clean-room、依赖许可证和 secret handling 无未解决阻断。
3. 核实 normal/failure/concurrency/idempotency/crash/recovery 测试通过。
4. 核实 money/quota/billing 可对账，auth/tenant fail-closed，运营有查询和人工恢复。
5. 核实构建、lint、全量测试、数据库、容器/readiness 和必要真实 smoke 状态。
6. 核实 Mandatory Roadmap 是否仍是上线阻断，不能用例外伪装 full parity。
7. 核实旧链、死代码、过期规则和误导注释已清理。
8. 输出 `Pass / Pass With Documented Exceptions / Block`；最终 merge 仍由 Owner 批准。

## 输出

- 发布结论与阻断项；
- 已验证/未验证清单；
- residual risk 与 Owner 待批准事项。

## 阻断项

S0/S1、未跑 required tests、钱/鉴权/租户恢复缺口、clean-room 污染、必需路线未完成或未获 Owner merge 批准时必须 `Block`。
