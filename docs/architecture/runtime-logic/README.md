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
| relay 转发链(auth→配额/计费→选号→转发→结算) | [relay-forwarding.md](relay-forwarding.md) | ✅ 完成:协作图 + 12 配合点对照三镜 + 20 配合测试用例(T1-T20)+ 实测发现(§7) |
| billing 预扣↔结算↔abort | relay-forwarding.md §2 C1/C2/C3 + §3 T1-T5 | ✅ 含于 relay 文档 |
| quota↔选号↔并发槽释放 | relay-forwarding.md §2 C11/C12 + §7.2 | ⚠️ 已发现配合缺口(reconciler 未结算,待定性) |
| pool 选号↔渠道健康回流↔failover | relay-forwarding.md §2 C4/C5/C6/C7 + §3 T7-T10 | ✅ 含于 relay 文档 |
| credential 采集流状态机(start→callback→finalize) | [credential-acquisition.md](credential-acquisition.md) | ✅ 完成:三入口(PKCE/设备码/CLI导入)汇合 Finalize,6 配合点 + 失败协作(state CSRF/非原子补偿/无邮箱),三镜逐点对照标注待专项调研 |
| media 任务生命周期(submit→poll→settle) | [media-task.md](media-task.md) | ✅ 完成:submit→lease→poll→settle 链 6 配合点 + 失败协作(租约丢失孤儿/worker崩溃LeaseSweeper兜底/actual=0锚定),超时不取消上游列已知缺口 |
| settlement_intents 持久结算意图(意图↔claim↔交付↔sweeper 追平) | [settlement-intent-reconciliation.md](settlement-intent-reconciliation.md) | ✅ 完成:阶段 1 fail-open 旁路 + 阶段 2 sweeper 对账,5 配合点 + 6 配合测试对照三镜,真 PG+race 实测 |

## 已知配合缺口(全局登记,详见各子系统文档)

- **✅[已闭合 2026-07-11]~~[quota reconciliation 未结算,2026-07-02 实测]~~** 原象:`quota_reconciliation_jobs` 卡 `queued`、`quota_reservations` 停 `reserved` 不结算,并发槽只靠 90s lease 过期释放。**定性=当时 dev 部署未起 reconciler worker,非代码 bug**:生产 `quotaReconcilerEnabledFromEnv()` 默认 true(未设/空/解析失败都返回 true,`cmd/gateway/wiring.go:271-281`),wiring.go:1248 无条件启动 `quota.NewReconciliationWorker`;billing 侧 `PendingReconciliationWorker` 亦无条件启动(wiring.go:1235)。二者补上「reservation 卡 reserved 冻结 headroom」的配合断裂。go-live-readiness §3 已列为默认开执行器。
- **[usage 成本分项列未持久化,2026-07-02 实测]** `usage_records.input_cost` / `output_cost` 系统性为 0(聚合 `actual_cost` 正确)。对账分项报表会空。observability 缺口,非 money bug。
