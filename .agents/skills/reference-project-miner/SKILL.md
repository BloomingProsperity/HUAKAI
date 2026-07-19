---
name: reference-project-miner
description: 在任何对标、能力补齐或机制判断前，按领域选择成熟项目并读取生产源码，形成与 HUAKAI 实现隔离的 clean-room 行为合同。
---

# 参考项目源码调研

## 何时使用

- 开始新功能、修复成熟能力缺口或比较机制之前；
- Owner 问“某项目怎么做”；
- 需要判断外部项目是否有某能力、状态、算法或恢复入口；
- 为计划、决策、parity 或 acceptance tests 提供证据。

## 前置输入

- 用户/运营结果和任务领域；
- `AGENTS.md` §4-6；
- `dependency-license-auditor` 的许可证/活跃度结论；
- 独立 clean-room `specifier` session。

## 执行步骤

1. **先分领域。** 中转站核心使用 sub2api、CLIProxyAPI、new-api 三镜；专业模块补该领域头部项目。钱路按问题覆盖发卡/数字商品、电商退款、支付编排、订阅计费和账本中的相关类别。
2. **验证候选。** 默认镜像放在 `~/refs/<project>/`；只做当前行为核实时可 shallow clone，研究历史时才拉完整 history。记录未归档状态、HEAD、最近提交、许可证和为什么匹配当前问题；项目名或 star 数不构成证据。
3. **隔离输入。** `specifier` 不得读取 HUAKAI 当前源码、diff、schema、内部标识符或实现型文档；不得提出本地补丁。
4. **读生产源码。** 从入口、权限、状态、持久化、外部副作用、幂等、异步、重试、恢复、审计和运营面追真实调用链；README、前端、测试和 issue 只作线索。README 中若包含实现代码块，也按外部源码执行 clean-room guard。
5. **列完整 shape。** 记录 path/mode/state/actor、正常/失败/部分成功、累计边界、并发、幂等、人工恢复和未处理问题。
6. **逐条引用。** 每项行为使用 `repo@sha:file:line`；标记 `Observed / Inferred / Open Question`，禁止猜测。
7. **形成行为合同。** 使用中性 HUAKAI 词汇重述保证形式，不复制名称、schema、结构、代码顺序或测试。
8. **独立交接。** 只把行为合同交给后续实现 lane；后续 lane 再读 HUAKAI 真码判断直接适配、融合改造、Safe Equivalent 或不适用。

## 输出

- 候选与许可证/新鲜度；
- Source Coverage Proof；
- shape inventory 与行为合同；
- 参考项目不足和 Open Questions；
- Source files read、Lane、Agent、UTC tail。

## 阻断项

- 未读生产源码却下能力/机制结论；
- specifier 接触 HUAKAI 当前实现细节；
- 只看三镜处理非中转站专业问题；
- 复制或近似翻译外部实现；
- 无 file:line 证据。
