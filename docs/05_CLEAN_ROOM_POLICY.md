本文件面向执行 agent，是 HUAKAI clean-room 的现行专项合同；如与 `AGENTS.md` 冲突，以后者和 Owner 最新指令为准。

# Clean-room 专项合同

## 1. 目标

HUAKAI 保持 MIT 独立实现，同时达到成熟项目同等或更好的有效能力。许可证风险只改变证据隔离、实现方式、发布门和归属要求，不得成为删减功能的理由。

外部项目是行为证据，不是源码提供者。README、宣传页、issue、测试和前端入口只能提供调查线索；能力、机制、差异、算法和 parity 结论必须由当前生产源码证明。

## 2. 允许进入行为合同的内容

- 用户、管理员和系统 actor 的可观察结果；
- path、mode、state、前置条件和状态转换；
- 输入约束、失败分类、幂等结果和并发不变量；
- retry、fallback、补偿、DLQ、对账和人工恢复结果；
- 成本、权限、审计、可观测和 Day-2 运维要求；
- 由真实 issue 提炼、且能回溯到源码行为的验收场景。

## 3. 禁止流入 HUAKAI 的内容

- 复制或近似改写的函数、类型、字段、常量和内部标识符；
- 复制的源码块、注释、schema、SQL、测试、fixture、文件结构和模块边界；
- 外部 UI 源码、独特布局、样式或组件树；
- 按外部代码执行顺序逐行翻译的算法；
- 把上游私有对象模型直接改名后塞进 HUAKAI；
- 无 `repo@sha:file:line` 证据的外部行为断言。

## 4. 强制 lane

### 4.1 `specifier`

`specifier` 可以读取外部项目当前生产源码、官方文档和公开 issue，但不得读取 HUAKAI 当前实现、working-tree diff、内部 schema、内部标识符或实现型文档。它只产出中性的行为合同，不写 HUAKAI 代码、迁移、UI、测试或补丁建议。

行为合同必须包含：

1. `Observed / Inferred / Open Questions` 分类；
2. actor、path、mode、state 清单；
3. 正常、失败、并发、幂等、部分成功和恢复行为；
4. 每项非平凡断言的 `repo@sha:file:line`；
5. `Source files read / Lane / Agent / UTC timestamp` 尾部来源记录。

### 4.2 `reviewer`

同一合同的 clean-room `reviewer` 必须使用不同 session。它只检查证据覆盖、行为完整性和污染风险，不重新读取同一外部源码，也不把上游独特标识符补进合同。

### 4.3 实现 lane

实现 lane 在行为合同完成后才读取 HUAKAI 真码，并从本地合同、不变量、数据模型和运维方式独立设计。读过受限外部源码的同一上下文不得承担贴近该源码的实现工作。

高风险账本、账号池选号、provider failover 和健康启发式执行最严格隔离：实现者只消费已通过 reviewer 的行为合同和 HUAKAI 真码，不再读取同一外部实现。

## 5. 源码读取前置门

涉及外部源码的任务必须先填写 `AGENTS.md` §5.3 的 lane guard。项目范围按领域选择：中转站核心必须覆盖 sub2api、CLIProxyAPI、new-api；支付、退款、账本、身份等专业领域还必须覆盖真正匹配的领域项目。

引用超过 30 天、项目归档、默认分支变化或许可证变化时，旧证据不得直接用于新结论，必须先更新镜像并重新核实。

## 6. 许可证处置

| 许可证/来源 | 默认处置 |
| --- | --- |
| AGPL/GPL/LGPL 外部项目 | 只作行为证据，严格 lane 分离，独立实现 |
| MIT/Apache/BSD 外部项目 | 仍默认独立实现；不得因为宽松许可证而复制不必要结构 |
| 官方 SDK | 先做依赖、许可证、维护和供应链审计，再决定是否直接依赖 |
| vendored 或本地 fork | 必须由 Owner 批准，固定来源 SHA，保留 LICENSE/NOTICE 和修改记录 |

任何依赖或 vendoring 决策都不得修改项目 `LICENSE`，除非 Owner 明确授权。

## 7. 交付门

以下任一项不满足，行为合同或实现不得进入发布状态：

- 外部断言有当前源码证据，且没有把猜测写成事实；
- `specifier`、`reviewer`、实现 lane 的输入边界可追溯；
- 本地命名、结构、schema、算法顺序和测试均为独立设计；
- 有效能力没有因许可证风险缩水；
- 依赖许可证、NOTICE、来源 SHA 和发布清单一致；
- 独立只读 review 未发现 S0/S1 污染风险。

历史方法选型见 [DR-000](process/decisions/DR-000-clean-room-methodology.md)；当前执行只以本文件、`AGENTS.md` 和 `docs/RULES.md` 为准。
