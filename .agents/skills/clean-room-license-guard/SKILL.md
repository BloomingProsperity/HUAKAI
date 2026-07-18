---
name: clean-room-license-guard
description: 审查行为合同、计划、补丁、schema、UI 和测试是否违反 lane 隔离或引入外部实现污染，同时保证功能不缩水。
---

# Clean-room 许可证守卫

## 何时使用

- 外部源码调研产物交给实现前；
- 受外部项目启发的计划、代码、schema、UI 或测试提交前；
- 完整 slice 和发布门。

## 前置输入

- `AGENTS.md` clean-room guard；
- 行为合同及其 Source Coverage Proof；
- lane/agent/UTC provenance；
- 当前 diff 与许可证审计结论。

## 执行步骤

1. 核实 `specifier` 未读取 HUAKAI 实现，implementer 未获得外部源码细节。
2. 查函数/字段/配置名、注释、schema、目录布局、UI、测试和算法顺序是否过度相似。
3. 核实外部行为都被改写为中性保证形式，而不是源码摘要。
4. 核实引用只用于证据，正文未复述独特标识符。
5. 发现污染时删除/重做污染实现，但保留业务结果并选择独立实现、Safe Equivalent、Plugin、Feature Flag 或 Mandatory Roadmap。
6. 将风险更新到现有 risk register/唯一计划。

## 输出

- 污染风险、证据位置、严重度；
- 必须重做的范围；
- 保留功能结果的独立实现路径；
- `Pass / Block` 结论。

## 阻断项

未经 lane 隔离、存在近似翻译或来源许可证不明时阻止落地；clean-room 不得用于删功能。
