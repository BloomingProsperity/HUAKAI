# 三个既有重并发降级缺陷修复 · Claude 独立计划(双计划我方稿)

日期:2026-07-15。Owner 已拍板「修」。三缺陷均为绑定并发 E2E 验尸时挖出的**既有**缺陷(非三字段切片引入),触配额/计费核心,已获点头。

## 缺陷与修法

### 缺陷1(S1)quota Reserve 把瞬时序列化冲突 fail-closed 硬拒
- **根因**(internal/quota/service.go:107-120):事务闭包内 `GetReservationByClaimForUpdate` 非 ErrNoRows 错误、`ResolvePolicies` 错误,一律走 `failClosedDecision`→deny,**且闭包返回 nil**——外层重试环(:178 `isPgRetryableTxConflict(err)`)看到的 err 是 nil,永远不重试。40001/40P01 瞬时冲突被当成永久拒绝,用户收假 429 quota_denied。
- **修法**:这两个分支先判 `isPgRetryableTxConflict(err)` → 直接 `return err` 把错误抛给外层重试环(与 :184 注释的既有语义一致);非瞬时错误维持 fail-closed 不变(纵深不删)。重试耗尽维持现有 `RetryableError` 契约。
- **判别测试**:fake store 让 GetReservationByClaimForUpdate 先回 40001 再成功 → Reserve 必须成功不 deny;变异(把重试判定删掉恢复 failClosedDecision)→ 红。

### 缺陷2(S2)abort 后 quota.Release 失败悬挂 —— 【2026-07-15 亲核真码后改判,原诊断失真】
- **真码现状**(比记忆里的诊断健康得多):Release 走 `runQuotaFinalizationWithRetry`(service_settle.go:174/:331,3 次序列化重试+退避,与 Reserve 同款);重试耗尽或其他可入队失败会 `enqueueFinalizationReconciliation` 入补偿 job(:233-239);ReconciliationWorker 默认开启(wiring.go:1285 env 显式 false 才跳过)、默认 1 分钟一轮(reconciliation_worker.go:13),每轮①重放补偿 job ②清扫「lease 过期+claim 终态+无 job 史」的孤儿预留(reconciler.go:129-139)。原「Release 单次无重试、悬挂 30 分钟」不成立:入队成功的失败 ≤1 分钟自愈;30 分钟窗口只发生在**job 从未入队**的路径。
- **残余真缺口**(修这个):`enqueueFinalizationReconciliation` 本身与失败事务同场竞争,入队再失败(queueErr)则啥都没排上,只能等 30 分钟 lease 过期走孤儿清扫。修法=给入队动作加同款小重试;顺带核 settler.go Abort 把 Release 错误回传后调用方(gatewayhttp abort 路径)是否吞错不留痕。
- **判别测试**:fake store 让入队前两次 40001 第三次成功 → job 必须落队;变异删入队重试 → 红。E2E quotaStuckReservations 计数维持 ≤2 容忍(其成因是测试不等 worker tick,非生产缺陷)。

### 缺陷3 billing Tx2 abort 序列化重试打穿冻结
- **现状契约**:abort 6 次序列化重试耗尽 → X-Huakai-Abort-Failed 响应头,claim 停 reserving、hold 冻结待 lease sweeper 追平(成文降级,不是静默)。
- **修法方向**(以现场调查为准):①核对 abort 重试是否带指数退避+抖动,没有则补上(降低同刻竞争互踩打穿率);②打穿后不再干等 lease 到期,立即把该 claim 入队/标记给现有 sweeper 快速追平(缩窗口)。**响应头契约与 sweeper 兜底语义不变**,只降频缩窗。
- **判别测试**:注入持续 40001 的 fake 打穿 → 头仍在、快速追平路径被触发;变异(删退避/删快速入队)→ 红。E2E frozen 计数应回落(现容忍 ≤2)。

## 统一门
- 真 PG 集成+E2E:binding_concurrency_e2e_test 四桶收敛跑 3 遍稳定;悬挂/冻结计数常态归零(测试容忍上限暂不收紧,先观察)。
- 亲手变异证红(每缺陷至少一刀)+对抗审查零 S0/S1+CI 四 job 绿。
- 不改 schema、不改对外错误码语义、默认行为零翻转(修的是「本不该拒/悬挂」的错误路径)。

## 风险
- Release 加重试改变 Abort 时延上界(毫秒级退避×3,可忽略);Reserve 分支改动须确保非瞬时错误仍 fail-closed(变异咬住)。
- 缺陷3 若快速追平需新 goroutine/队列,优先复用现有 sweeper 入口,不造第二套。
