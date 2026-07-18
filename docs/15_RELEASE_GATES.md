# HUAKAI 发布门

发布门用于阻止功能缩水、错误接线、资金/权限漂移、不可恢复故障和许可证污染。它不把职责永久绑定给某个模型，也不授权自动合并或跳过 Owner 的生产批准。

## 必过门

| Gate | 可验证要求 | 责任 |
| --- | --- | --- |
| Truth Gate | 完成声明与当前生产源码、迁移、配置、入口/DI/worker 接线和测试一致；无文档冒充实现。 | 当前执行者 + 独立 reviewer |
| Source Gate | 外部能力、机制、差异和 parity 结论来自当前生产源码，带 `repo@sha:file:line`；无过期引用冒充现状。 | specifier + clean-room reviewer |
| Clean-Room Gate | 行为合同与实现 lane 分离；无复制或近似翻译外部代码、标识符、结构、schema、UI 或测试。 | 独立 reviewer |
| Parity Gate | 每项有效能力都有合法 disposition 和独立 status；无静默删除，Merged/Safe Equivalent 保留用户与运营结果。 | 当前执行者 + parity reviewer |
| Whole-Chain Gate | 入口、身份、规范化、决策、持久化、副作用、异步、重试、健康、账务/配额、审计、DLQ/恢复和运营状态真实闭环。 | 当前执行者 |
| Scenario Gate | 正常、失败、部分成功、超时、崩溃、重放、多副本竞争、滥用和人工恢复场景已覆盖。 | 当前执行者 + risk reviewer |
| Acceptance Gate | 测试能在目标缺陷重新出现时变红；fixture、断言、gate 和 SQL 条件具有判别性。 | 独立 reviewer |
| PostgreSQL Gate | money/auth/schema/并发链在迁移后的 PostgreSQL 上验证事务、锁、唯一约束、幂等、失败与恢复。 | 当前执行者 + 独立 reviewer |
| Billing Gate | usage、hold、claim、settlement、refund、quota 和 ledger 金额守恒、可重放、可对账、可人工恢复。 | money-path reviewer |
| Security Gate | 凭据、权限、租户隔离、审计、SSRF、输入上限和滥用控制经过专项检查。 | security reviewer |
| OpenAPI Gate | 公开路由、请求/响应、鉴权和错误合同与 OpenAPI 一致，现有一致性测试通过。 | 当前执行者 |
| Ops Gate | 管理员能查询、筛选、诊断、重试、对账、隔离和人工裁决；敏感操作有权限与审计。 | 当前执行者 |
| Code Budget Gate | `backend/internal/codebudget` 通过；不抬 baseline 掩盖继续膨胀。 | 当前执行者 |
| Reference Tracking Gate | [持续追踪台账](24_REFERENCE_TRACKING_POLICY.md) 在当前发布周期内有效；已核实与本次变更相关的上游修复。 | 当前执行者 |
| Review Gate | 每个提交无未结 S0/S1；完整 slice 与 money/auth/schema/跨功能改动完成 full reviewer gate。 | 独立 reviewer |
| Release Decision Gate | Mandatory Roadmap、未验证项、运行开关、迁移、恢复手册和生产风险已明确，生产部署由 Owner 批准。 | Owner |

## 发布判定

- 任一资金、鉴权、租户隔离、数据损失、许可证污染或 required test 的 S0/S1 未关闭：禁止发布。
- 任一宣称完成的能力只有代码片段、没有真实接线或恢复面：按未完成处理。
- 任一参考能力没有 disposition/status，或被页面/API 合并后丢了权限、状态、审计或恢复：不得宣称 full parity。
- S2/S3 必须进入当前唯一计划或 commit body，不新建零散 review 文档；重复出现时修复或升级严重度。
- Owner 启动门、决策停门和 merge/部署边界统一见 [`docs/RULES.md`](RULES.md)，本文件不另造风险模型。
