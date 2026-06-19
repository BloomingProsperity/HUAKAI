# mediatask 孤儿 providerTaskID 持久化(闭合审计 #71 残留,money 安全)

## 背景
审计 wy94u3tn9 的 S1 #8(mediatask 重复提交/孤儿成本)在 PR #71 **仅缓解未闭合**:worker
`processLeased` 在 `provider.Submit` 拿到 providerTaskID 后,若租约在此期间过期被另一 worker 抢走,
`MarkProviderSubmitted` 返回 `ErrLeaseLost`,#71 改成调 `reportOrphan`——但 `reportOrphan` 只**打日志 +
可选 OrphanReporter 接口投递**,而生产 `wiring.go:1082` 用空 `WorkerOptions{}` 构造 worker,OrphanReporter
为 nil → **孤儿只进日志,从不持久化**(日志随轮转丢失,无法对账)。本切片补上耐久持久化。

## #16 三镜像调研结论(specifier lane,已读源码)
- **new-api**:有最成熟的异步任务模块(MJ 专表 + 通用 task 表,公开 ID/上游 ID 双字段),且同为
  **"先上游后本地"**(`controller/relay.go` 提交上游成功后才 Insert)——与 HUAKAI 同构、**同样的孤儿风险
  且同样未填**:Insert 失败仅 `SysError` 记日志不补偿;轮询只能扫"已落库但空上游 ID"的行,**扫不到
  "本地零记录"的真孤儿**。
- **sub2api**:无上游异步任务模块;但有成熟的请求级幂等协调器(`idempotency_record` 唯一键 +
  processing 占位 + reclaim),是"先本地占位"范式的范本。
- **CLIProxyAPI**:纯 relay,视频接口直转上游无本地态,无孤儿概念。
- **裁决**:HUAKAI 的"独立孤儿对账表 + (task_id,provider_task_id) 幂等键 + 四态生命周期"比三家都完备
  (架构升级:对账与任务主表职责分离,优于 new-api 混在主表轮询;算法对齐 sub2api 唯一键+状态机;
  生态领先:三家都没把"乐观租约异步提交孤儿"显式建模)。**先上游后本地的权衡正由本表兜底。**

## 范围(additive,不改既有 money 写路径)
1. 迁移 `0151_media_task_orphans`(纯新增表,可逆 down=DROP):耐久对账台账,(task_id,provider_task_id)
   唯一幂等键,pending/reconciled/cancelled/ignored 四态,pending 部分索引供扫描。**刻意无外键**——
   append-only 台账,worker best-effort 写入绝不能因引用完整性失败丢线索。
2. `store_orphan.go`(raw pgx,无 sqlc):`PersistOrphan`(ON CONFLICT DO NOTHING 幂等;空 ID 跳过)+
   `ListPendingOrphans`(对账消费者/运维查,租户或全局)+ `MarkOrphanReconciled`(pending→终态,守卫幂等)。
3. `orphan_reporter.go`:`PersistingOrphanReporter` 实现 `OrphanReporter`,把孤儿事件落库;fire-and-forget,
   落库失败只记日志、绝不阻塞/panic worker(worker 调用前已有结构化 Warn 兜底)。
4. `wiring.go`:把 `OrphanReporter: NewPersistingOrphanReporter(mediaTaskStore, nil)` 注入生产 worker。

## 成功标准
- 迁移 up/down/re-up 在 dev DB 干净(已验)。
- 单测(普通门):reporter 字段映射正确 + 落库报错不传播/不 panic + nil store no-op;变异(映射写错 /
  错误改 panic)→ RED(已验)。
- 集成测(integration_pg,真 PG):幂等(重复持久化 1 行)+ 空 ID 跳过 + 待对账查询 + 终态推进幂等 +
  非法状态拒绝;变异(删 ON CONFLICT / 删终态守卫)→ RED(已验)。
- mediatask 单测 + 集成测全量 + cmd/gateway 构建 + codebudget 全绿(已验)。

## blast radius / 可能出错
- 纯新增表 + 新文件 + 一处 wiring 注入,**不动既有 media_tasks 表与 money 写路径**;down 直接 DROP,可逆无数据丢失。
- reporter 同步落库在 worker 主循环里:单条 INSERT(带索引),非慢操作;失败只记日志不阻塞。

## 决策点 / 明确的后续(本切片不做,非阻断)
- **对账消费者 + best-effort 取消上游**:provider 接口(`AsyncMediaProvider`)当前**只有 Submit/Poll、无 Cancel**
  → 取消上游需给接口加 Cancel(改所有 provider),是更大的独立切片。本切片只持久化 + 提供查询/标记 surface,
  消费者/admin 视图/取消留作 follow-up。
- **#16 建议的"先本地 pending 占位行再提交上游"**(把孤儿从"本地零记录"降级为"本地 pending 行可被扫描发现):
  是更优但更大的提交流重构(改 worker 提交时序),留作 follow-up;本表是"先上游后本地"权衡下的正确补丁。
