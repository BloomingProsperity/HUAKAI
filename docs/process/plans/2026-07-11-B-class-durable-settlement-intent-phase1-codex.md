# 2026-07-11 B 类持久结算意图阶段 1 Codex 执行计划

> 状态：已被 `2026-07-11-B-class-durable-settlement-intent-phase1-remediation-codex.md` 取代。本文中的 `(request_id, attempt_seq)` 设计前提已被 claim 复活证据否决，不得作为当前实现依据。

| 项目 | 内容 |
| --- | --- |
| Owner directive | “本轮只做阶段 1：迁移建表 + sqlc + 意图行生命周期接线 + 判别测试。” |
| Scope | 新增 `0175` 迁移、`settlement_intents` sqlc 查询与生成代码、灰度配置、relay 主链路 pending/delivering/settled/aborted/settling 旁路接线、四类判别测试；不改 Reserve/hold/扣款/结算金额口径。 |
| Out of scope | 不实现 sweeper，不把 recovery proof 绑定 `attempt_seq`，不做 `superseded` 状态迁移逻辑，不做运维人工裁决出口，不改现有表。 |
| Success criteria | 迁移可正反执行；`UNIQUE(request_id, attempt_seq)` 在真 PostgreSQL 中拒绝重复；开关关闭时零行为变化；开关打开时成功请求完整经历 pending→delivering→settled；意图存储失败不阻断交付和原结算；删除 routes 注入会使 wiring 测试失败；全部指定门禁通过。 |
| Time estimate | 约 3—5 小时墙钟时间；单 agent 约 5—8 小时工程时间，主要取决于现有 relay/结算抽象和 integration-pg 时长。 |
| Blast radius | 每个启用意图功能的请求新增一次 INSERT 和至多两次 UPDATE；错误接线可能阻断请求、遗漏终态、使用错误 attempt 或改变钱路，因此所有意图操作必须显式 fail-open 且与原结算返回值解耦。 |
| Failure modes | sqlc 全量 churn：改用与生成风格一致的最小生成文件；版本乐观锁丢更新：本请求保存并递增本地版本；首帧路径不统一：覆盖流式首个 business frame 与非流式写响应；依赖漏注入：静态 wiring 判别测试；真 PG 污染：只用官方纯净库脚本；门禁重复失败：每门最多三轮后停止并如实报告。 |
| Decision points | Owner 已批准纯新增表。阶段 1 按独立短事务写意图，避免改 Reserve 的 Tx1；该边界不保证 Reserve 与 intent 原子性，是明确剩余风险。灰度开关默认关闭以保持发布前行为不变。若现有依赖结构无法在不触碰认证、额度或账本核心的情况下接线，则停止并请求确认。 |
| Pre-execution checklist | 见下列清单。 |

## 与总体设计的对齐结论

- 一致：持久意图必须在 Reserve 成功后、首字节前创建，并绑定 `(request_id, attempt_seq)`。
- 一致：交付点写 `first_byte_at`，成功结算写实际金额，失败进入 `settling`，Abort 写 `aborted`。
- 本轮收窄：只建立阶段 2 所需的未解决计数查询，不消费它；`superseded` 仅作为合法状态保留。
- 安全选择：默认关闭灰度；启用后所有意图写错误只记 warning，不改变原请求或原钱路的控制流。
- 事务选择：本轮使用独立短事务，不把新增表写入 Reserve Tx1，以遵守“不动 Reserve 准入语义”；代价是 Reserve 成功与 intent 插入之间存在可观测缺口。

## 执行前检查清单

1. 确认工作树，仅保留并避开 Owner/其他 agent 的既有改动。
2. 读取迁移、sqlc、金额类型、配置开关、ChatHandlerDeps、routes 和 gateway wiring 的现有模式。
3. 定位 Reserve 成功点、流式首个 business frame、非流式响应、settle/abort/恢复入队分支。
4. 先落迁移和查询，再做最小 sqlc 生成；若产生无关 churn，恢复本次生成 churn 并按现有生成风格补最小文件。
5. 设计独立 intent 运行时接口，使测试可注入失败，生产实现只调用 sqlc 存储。
6. 接线开关和依赖，确保 disabled/nil/DB error 均为 fail-open。
7. 添加生命周期、唯一约束、fail-open、routes/cmd wiring 判别测试。
8. 依次运行 gofmt、定向测试、build、vet、全量测试、integration-pg、codebudget 与 OpenAPI 一致性门禁；每门最多三轮。
9. 检查最终 diff、未跟踪文件和敏感路径；不执行 `git add`、`git commit`、`git push`。

## 具体执行顺序

1. 数据层：`0175` up/down、查询定义、最小 sqlc 产物、真 PG 唯一约束测试。
2. 运行时抽象：定义意图创建/状态迁移所需最小接口和结构，金额保持 `numeric(20,8)` 对应类型。
3. 配置与装配：解析 `HUAKAI_SETTLEMENT_INTENT_ENABLED`，接入 `ChatHandlerDeps`、routes 和 `cmd/gateway`。
4. relay 生命周期：Reserve 后创建 pending；首个业务交付写 delivering；成功/Abort/恢复分支写终态或 settling。
5. 判别测试：逐个验证删除关键调用、唯一约束、把 fail-open 改成 return、删除 routes 注入时均会红。
6. 门禁与报告：保存真实命令输出摘要，记录 Tx1 非原子边界与任何未覆盖路径。

## 约束与风险记录

- 不修改 `LICENSE`、现有迁移、现有表、认证核心、额度准入、账本与金额计算。
- 所有新增 Go 注释和测试说明使用中文，不写参考项目名或借鉴措辞。
- `settlement_intents` 是旁路证据，不成为本阶段结算成功与否的判定来源。
- 独立事务意味着进程可能在 Reserve 成功后、intent INSERT 前崩溃；阶段 1 无法消除此窗口，只有未来与 Tx1 原子合并才能闭合。
