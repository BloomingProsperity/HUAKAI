# media 任务生命周期 运行逻辑

> 异步媒体任务(图像生成等)的 submit→lease→poll→settle 链,横跨 billing 预扣、worker 租约、
> provider 派发、孤儿对账多模块。本文记它们**怎么配合 + 失败怎么补偿**(尤其"上游已产生费用"的
> 各种半途失败)。相关 [relay-forwarding.md](relay-forwarding.md)(同步 relay 结算)。

## 1. 请求/操作生命周期(数据流)

1. **Submit**(`service.go:33`):建 task(pending)+ billing claim reserve 预扣(hold);预估费用
   落 EstimatedCents。
2. **worker AcquireLease**(`worker.go:126`,LeaseTTL 默认 30s):后台 worker 抢租约领 task,
   `processLeased` 驱动。租约覆盖任务处理期,防被 billing LeaseSweeper 当"久挂预扣"提前 abort。
3. **provider submit → poll**:worker 向 provider 提交拿 `ProviderTaskID`,轮询直到终态。
4. **结算**:
   - 成功 `CompleteSuccess`(`store_money.go:21`):`billing.Capture` 真扣,`billedCents<=0` 时
     锚定 EstimatedCents(不 $0 白吃)、上限钳制不超扣;
   - 失败 `CompleteFailure`:退款;
   - 超时 `ExpireTask`(`store_money.go:97`):全额退款(现状**不取消上游**,见 §5)。

## 2. 关键配合点表

| from→to | 传什么 | 配合关系 | 配合错的后果 | file:line |
|---|---|---|---|---|
| Submit→billing | claim reserve | 预扣 hold,EstimatedCents 落库 | 不预扣→成功时无 hold 可 Capture | service.go:33 |
| Submit/task→worker | task pending | worker AcquireLease 领,租约覆盖生命周期 | 租约太短→任务未完被 LeaseSweeper abort | worker.go:126 / :160 |
| worker→provider | submit 请求 | 拿 ProviderTaskID(**租约在 Submit 期间被抢→上游已创建却无法落库=孤儿**) | 孤儿丢失→上游计费但本地无记录 | worker.go:15-23 |
| worker→billing settle | PollResult | CompleteSuccess 锚定 EstimatedCents 真扣 | actual=0 归零→白吃上游成本(#7 已修 #236) | store_money.go:21,48 |
| worker 崩溃→LeaseSweeper | (租约过期) | 预扣久挂由 billing LeaseSweeper 兜底释放 | 无兜底→hold 永久冻结额度 | worker.go:160 |
| 掉租→orphan store | ProviderTaskID | PersistOrphan 幂等 `(task_id,provider_task_id)` 唯一入账 | 重复上报双入账 / 空 ProviderTaskID 无对账价值跳过 | store_orphan.go:27-34 |

## 3. 失败协作

| 场景 | 涉及模块 | 怎么协作补偿 | file:line |
|---|---|---|---|
| Submit 期间租约被抢,上游已创建 | worker↔orphan store | worker 已在上游建 ProviderTaskID 却无法落库→PersistOrphan 落孤儿线索,运维对账处置 | worker.go:23 / store_orphan.go |
| 已释放 hold 的孤儿 | orphan↔billing | 标注 `hold_released_needs_manual_charge`(真实新扣款属人工决策,不自动扣) | 见 go-live-readiness §5 |
| worker 崩溃致 task 卡"运行"僵态 | worker↔billing LeaseSweeper | 已 Reserve 预扣久挂由 billing LeaseSweeper 兜底(与 settlement sweeper 同域兜底范式) | worker.go:160 |
| 上游未回用量 actual=0 | settle | 锚定 EstimatedCents 不白吃 + 上限钳制不超扣(#236) | store_money.go:48-53 |
| 孤儿重复上报(多 worker 撞) | orphan store | `(task_id,provider_task_id)` 唯一索引幂等,不重复入账 | store_orphan.go:27 |

## 4. 三镜对照

| 镜 | 同款做法 | HUAKAI delta |
|---|---|---|
| new-api | 异步任务(Suno/视频/MJ)预扣→FetchTask 拉状态差额结算 + 超时清扫 sweeper 全额退 + DB 租约 fencing | 等价预扣+租约;**delta:HUAKAI settle 只锚定预扣估算不推断上游金额差额补扣**(new-api 差额补扣/退) |
| sub2api / CLIProxyAPI | (媒体异步任务对照需专项 specifier-lane 调研) | — |

> **诚实标注**:new-api 异步任务对照来自 settlement sweeper 切片的三镜调研(task_polling/task_billing);
> sub2api 媒体任务链与 CLIProxyAPI(纯 relay 无异步任务账本)逐点对照按 §12 待专项调研,不臆造。

## 5. 已知配合缺口(Owner-gated 后续)

- **超时退款不取消上游(#5,S2 money,已 surface Owner)**:`ExpireTask` 全额退款但不向 provider
  发取消 → 上游任务可能仍跑完并计费 = 白吃成本。修法触 money 语义(超时不立即退款/喂对账/真发
  取消),Owner-gated。当前现状即已知期望(退款优先保护用户,成本暴露登记待决)。
- **孤儿对账人工裁决**:掉租孤儿落线索 + admin reconcile 面处置;自动追扣/裁决 UI 留后续。

## 6. 配合点测试清单

| 测哪个配合 | 构造条件 | 判别断言 |
|---|---|---|
| actual=0 不白吃 | 上游回 ActualCents<=0 | billedCents 锚定 EstimatedCents,非 $0 |
| 超扣钳制 | 上游 actual > estimated | billedCents 钳到 EstimatedCents |
| 孤儿幂等入账 | 同 (task_id,provider_task_id) 多次上报 | 只入一条 |
| 空 ProviderTaskID 跳过 | 孤儿无 ProviderTaskID | 不入账(无对账价值) |
| 租约兜底 | worker 崩溃后租约过期 | billing LeaseSweeper 释放久挂预扣 |
