# 运行逻辑 / 模块间配合文档(runtime-logic)

> Owner 2026-07-02 指令:「看模块之间的作用与配合……整个项目的运行逻辑都要经得起推敲。一定要先看三家是怎么做的！再看看我们是怎么做的！」
> 规则见 `CLAUDE.md` #17 + `AGENTS.md` §"Module Interplay & Runtime Logic Review"。

## 这个目录记什么

`docs/process/plans/` 记「要做什么」;本目录记 **HUAKAI 的模块之间怎么协作**——不是「有哪些模块」(那是功能清单),而是一个请求/操作流过系统时,各颗粒度模块**串联协作的运行逻辑**:

- **数据/状态传递链**:上一模块的输出如何成为下一模块的输入,identity / hold_id / account_id / attempt 上下文 / reservation 等状态在链上怎么传。
- **关键配合点(模块交界)**:两个模块的接口处传什么、配合错了会怎样。这是单模块测试测不到、缺陷高发的地方。
- **失败协作**:上游 4xx/5xx、流式中途断、余额不足、结算 DB 故障、换号等失败时,各模块怎么协作回滚补偿(hold 释放 / claim abort / 健康信号 / DLQ),保证不漏钱/冻钱/重复扣/状态不一致。
- **三镜对照**:同款子系统 sub2api / new-api / CLIProxyAPI 怎么串联模块、失败怎么协作(先看三家再看自己)。
- **已知配合缺口**:实测发现的模块协作断裂(带证据),排进修复。

## 每份子系统文档的结构(模板)

```
# <子系统> 运行逻辑
## 1. 请求/操作生命周期(数据流)
## 2. 关键配合点表(from→to | 传什么 | 配合关系 | 配合错的后果 | file:line)
## 3. 失败协作(场景 | 涉及模块 | 怎么协作补偿 | file:line)
## 4. 三镜对照(sub2api / new-api / CLIProxyAPI 同款怎么做 | HUAKAI delta:等价/更强/有隐患)
## 5. 已知配合缺口(证据 + 严重度 + 修复排期)
## 6. 配合点测试清单(测哪些模块的配合 | 构造条件 | 判别断言)
```

## 子系统清单(按运行逻辑复杂度)

| 子系统 | 文档 | 状态 |
|---|---|---|
| relay 转发链(auth→配额/计费→选号→转发→结算) | [relay-forwarding.md](relay-forwarding.md) | 🚧 调研中(module-interplay workflow) |
| billing 预扣↔结算↔abort | (relay 文档内含,后续可拆) | 待补 |
| quota↔选号↔并发槽释放 | (relay 文档内含) | ⚠️ 已发现配合缺口(reconciler 未结算) |
| pool 选号↔渠道健康回流↔failover | (relay 文档内含) | 待补 |
| credential 采集流状态机(start→callback→finalize) | credential-acquisition.md | 待补 |
| media 任务生命周期(submit→poll→settle) | media-task.md | 待补 |

## 已知配合缺口(全局登记,详见各子系统文档)

- **[quota reconciliation 未结算,2026-07-02 实测]** `quota_reconciliation_jobs` 卡 `queued`(0 成功),`quota_reservations` 停在 `reserved` 不结算,并发槽只靠 90s lease 过期释放、从不即时释放 → concurrency 退化为「90s 窗口内请求启动数上限」而非「真在途并发数」。属 billing 结算 ↔ quota reconciler 配合断裂(可用性,非亏钱——RPM 与计费金额均正确)。待多方位证实(是此 dev 部署未起 reconciler worker,还是真 bug)后定级。
- **[usage 成本分项列未持久化,2026-07-02 实测]** `usage_records.input_cost` / `output_cost` 系统性为 0(聚合 `actual_cost` 正确)。对账分项报表会空。observability 缺口,非 money bug。
