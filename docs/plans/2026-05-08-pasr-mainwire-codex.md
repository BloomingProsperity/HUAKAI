# 2026-05-08 PASR-lite main.go 集成计划（Codex lane）

## 1. 元信息

| 字段 | 内容 |
| --- | --- |
| lane | codex |
| 角色 | Codex lane planner，独立 fresh context |
| Owner directive | “把 PASR-lite 接入 cmd/gateway/main.go, feature flag 控制, 与 DefaultSelector 共存, 支持 shadow 5%/25%/100% 渐进切换。” |
| 独立性约束 | 未读取 `docs/plans/2026-05-08-pasr-mainwire-claude.md`；本计划只基于 Owner brief、AGENTS.md、内部规则和本次读取的 HUAKAI 代码。 |
| 真实路径修正 | Owner brief 写 `cmd/gateway/main.go`，当前仓库实际入口是 `backend/cmd/gateway/main.go`。 |
| 相关内部代码观察 | `backend/cmd/gateway/main.go:66-123` 当前 `deps.selector` 是 `*pool.DefaultSelector`；`backend/internal/gatewayhttp/chat_completions_handler.go:31-37` handler 依赖已经是 `pool.Selector`；`backend/internal/gatewayhttp/chat_completions_handler.go:222-233` 是唯一请求路径 selector 调用点；`backend/internal/pool/pasr_selector.go:47-77` 当前 PASR 构造依赖 `RingProvider func() *AccountRing`；`backend/internal/pool/pasr_selector.go:244-258` 当前 PASR 写 claim 但未走 `SlotManager`；`backend/internal/pool/db_slot_manager.go:59-105` DefaultSelector 真实生产路径会递增 in-flight 并写 slot acquisition。 |
| 风险级别 | shadow-only wire 是中风险；actual canary / pasr-primary 因触碰 claim、slot、请求分流，按高风险上线动作处理，执行生产切流前需要 Owner 拍板。 |
| 计划产物 | 本文件是计划，不执行代码改动。 |

## 2. 目标 / 非目标

### 目标

1. 把 PASR-lite 作为 `pool.Selector` 的候选实现接入 gateway 启动流程，但保持 `DefaultSelector` 为默认实际处理路径。
2. 在 `backend/internal/config` 增加 typed feature flag，避免 `main.go` 直接散落 `os.Getenv`。
3. 引入一个 `SelectorDispatcher`，在同一个 handler 入口下支持 5 种模式：`default`、`shadow`、`canary`、`pasr-primary`、`pasr-strict`。
4. 支持 shadow sampling 5% / 25% / 100%，让 PASR 在无副作用路径上和 DefaultSelector 做真实流量比对。
5. 支持 canary sampling 5% 起步；但 actual PASR 进入请求处理前必须先补齐 slot acquisition 语义，否则只能 shadow，不允许生产承载。
6. 把 `SegmentTable`、`PASRCacheFeedback`、`PASRAgingWorker` 和 request-scoped ring source 在 `backend/cmd/gateway/main.go` 统一 wire，和 main shutdown ctx 协调。
7. 增加最小可观测面：shadow match/diff/drop/panic、canary pasr/default used、fallback、PASR latency/error class。
8. 给 Owner 一个清晰 rollback 路径：环境变量切回 `default` + 重启即可恢复 DefaultSelector-only。

### 非目标

1. 不做 A6 PG warm-start，不新增表、不改 migration、不接 DB 持久化段表。
2. 不做 A7 admin rebalance handler，不新增 admin endpoint 热迁移能力。
3. 不修改 `LICENSE`，不引入外部参考项目源码或非 MIT 源码。
4. 不重写 chat handler 主流程，不改变 Tx1 reserve、forward、Tx2 settle 的顺序。
5. 不在第一版加入 SIGHUP / admin API 热切；可以把 dispatcher 内部 mode 放进 `atomic.Value` 方便测试和未来扩展，但生产操作先走 restart-only。

## 3. 主架构方案

推荐方案：**`deps.selector` 升级为接口 + `SelectorDispatcher` 统一分发**。

```
config.Load()
   |
   v
PoolSelectorConfig
   |
   v
backend/cmd/gateway/main.go
   |-- accountSource := pool.NewDBAccountSource(q)
   |-- slotManager   := pool.NewDBSlotManager(pgPool)
   |-- claimGate     := pool.NewDBClaimGate(q)
   |
   |-- defaultSelector := pool.NewDefaultSelector(accountSource, slotManager, claimGate, sticky)
   |
   |-- pasrSegments := pool.NewSegmentTable(...)
   |-- pasrActual   := pool.NewPASRSelector(..., SlotManager=slotManager, Claims=claimGate)
   |-- pasrShadow   := pool.NewPASRSelector(..., SlotManager=nil, Claims=nil)
   |-- pool.RegisterPASRCacheFeedback(pasrSegments)
   |-- agingWorker.Start(ctx)
   |
   v
pool.NewSelectorDispatcher(defaultSelector, pasrActual, pasrShadow, flags, metrics, logger)
   |
   v
gatewayhttp.ChatHandlerDeps{Selector: dispatcher}
   |
   v
chat_completions_handler.go -> d.Selector.Select(ctx, req)
```

### deps.selector 类型升级方案

候选方案对比：

| 方案 | 说明 | 优点 | 缺点 | 结论 |
| --- | --- | --- | --- | --- |
| 接口字段 | `deps.selector pool.Selector`，由 main 传入 default 或 dispatcher | 最小改动，handler 已接受 `pool.Selector`；不会把 PASR 分支泄漏到 HTTP 层 | 生命周期对象需要在 main 局部变量持有，比如 aging worker | 推荐 |
| 双字段 + handler 分支 | `deps.defaultSelector`、`deps.pasrSelector`，handler 内决定 | 显式 | `/v1/chat/completions` 和 `/v1/messages` 会重复分流逻辑；HTTP 层知道 selector 细节；测试面变大 | 不推荐 |
| 装饰器包 DefaultSelector | 让 PASR 包在 DefaultSelector 周围 | shadow 实现容易 | canary actual、fallback、panic recovery、metrics 都会被装饰器语义挤在一起；命名上会误导“Default 是内核” | 不推荐 |
| Dispatcher concrete | `SelectorDispatcher` 实现 `pool.Selector`，内部持有 default/pasrShadow/pasrActual | 分流、采样、fallback、metrics 集中；handler 无感；可单测 | 需要新增一个小模块 | 推荐采用 |

主决策：`deps.selector` 改为 `pool.Selector`，不把 PASR 模式写进 handler。`SelectorDispatcher` 是唯一模式状态机，DefaultSelector 和 PASRSelector 都只是被调度对象。

## 4. 子原子 M1...Mn

### M1：Typed feature flag config

| 项 | 内容 |
| --- | --- |
| 范围 | `backend/internal/config/config.go`，新增 `PoolSelectorConfig`、enum parser、env var 读取；新增 config 单测。 |
| LoC | 80-140 |
| 风险 | 低-中。启动配置错误会影响 boot，但不触碰请求数据。 |
| 测试 | table tests：默认值、合法模式、非法模式、非法百分比、shadow/canary 百分比缺失、seed 解析。 |
| 验收 | `config.Load()` 默认 `PoolSelector.Mode=default`；非法 env 返回 error；`main.go` 不直接读 PASR env。 |

建议 env：

| env | 默认 | 取值 / 语义 |
| --- | --- | --- |
| `HUAKAI_POOL_SELECTOR_MODE` | `default` | `default` / `shadow` / `canary` / `pasr-primary` / `pasr-strict` |
| `HUAKAI_PASR_SHADOW_PERCENT` | `0` | 0-100；shadow 模式推荐 5、25、100 |
| `HUAKAI_PASR_CANARY_PERCENT` | `0` | 0-100；canary 模式第一档 5 |
| `HUAKAI_PASR_HASH_SALT` | `pasr-lite-v2` | selector sampling salt；非安全密钥，不记录 prompt 内容 |
| `HUAKAI_PASR_HRW_SEED` | `20260508` 或显式 uint64 | HRW ring seed；生产建议显式配置，避免多副本 seed 漂移 |
| `HUAKAI_PASR_LOAD_CAP` | `0.95` | PASR 过滤高负载账号阈值 |
| `HUAKAI_PASR_SEGMENT_CAP` | `100000` | SegmentTable 最大段数 |
| `HUAKAI_PASR_SEGMENT_MAX_AGE` | `30m` | 段无 cache_read 老化时间 |
| `HUAKAI_PASR_AGING_INTERVAL` | `5m` | aging worker ticker |

启动校验策略：**fail-fast return error，不 panic**。`main()` 仍通过现有 `logger.Fatal` 退出。原因是 selector 模式拼错通常是 operator 意图错误，fail-soft 到 default 会制造“以为开了 PASR 其实没开”的假信号。例外：`mode=default` 时不构造 PASR runtime，PASR-specific 空配置不影响启动；但只要 PASR env 被设置且格式非法，仍返回配置错误。

### M2：SelectorDispatcher + metrics

| 项 | 内容 |
| --- | --- |
| 范围 | 新增 `backend/internal/pool/selector_dispatcher.go` 和 `selector_dispatcher_metrics.go`，只依赖 `pool.Selector` 接口和 `SelectionRequest`。 |
| LoC | 180-260 |
| 风险 | 中。请求路径新增分支，但 default mode 应完全等价。 |
| 测试 | fake selector 覆盖 5 模式、panic recovery、sample bucket、fallback、no double claim。 |
| 验收 | default 模式只调用 default；shadow 模式实际结果等于 default；canary 按 bucket 调用 PASR 或 default；pasr-primary/strict 语义明确。 |

Dispatcher 结构建议：

```go
type SelectorDispatcher struct {
    defaultSel pool.Selector
    pasrActual pool.Selector
    pasrShadow pool.Selector
    cfg SelectorDispatchConfig
    logger *zap.Logger
    metrics *SelectorDispatchMetrics
}
```

5 模式语义：

| mode | 实际处理 | PASR 是否运行 | 失败处理 |
| --- | --- | --- | --- |
| `default` | DefaultSelector | 不运行 | 维持现状 |
| `shadow` | DefaultSelector | 按 `shadow_percent` 跑 shadow PASR | shadow 失败只记指标，不影响请求 |
| `canary` | bucket 命中时 PASR actual，否则 DefaultSelector | 命中 canary 才实际 PASR；未命中不额外 shadow，除非 Owner 另开 shadow_percent | PASR 非变更前错误可 fallback default；已写 claim / 已 acquire slot 后不 fallback |
| `pasr-primary` | PASR actual 100% | 是 | PASR `ErrNoEligibleAccount` / `ErrNoSlotAvailable` 可 fallback default；其他错误 fail closed |
| `pasr-strict` | PASR actual 100% | 是 | 不 fallback，用于最终验收或压测，不建议直接生产 |

### M3：PASR actual 补齐 slot acquisition，shadow 强制无副作用

| 项 | 内容 |
| --- | --- |
| 范围 | `backend/internal/pool/pasr_selector.go` config 增加 `Slots SlotManager` 或等效字段；actual path 和 DefaultSelector 一样先 acquire slot，再 write claim；shadow path `Slots=nil`、`Claims=nil` 且 dispatcher 把 `ClaimID=0`。 |
| LoC | 120-180 |
| 风险 | 高。触碰实际请求 admission、claim、slot，一旦实现错误会影响并发限制、结算和回滚。 |
| 测试 | PASR actual 成功时 slot acquisition exactly once + claim write exactly once；claim write 失败释放 slot；slot unavailable 返回 `ErrNoSlotAvailable`；shadow 使用 panic claim gate 也不触发。 |
| 验收 | canary/pasr-primary 前必须通过；否则生产只能启用 `default` 或 `shadow`。 |

这是本计划最重要的安全门槛。当前 `PASRSelector.acquireAndReturn` 只生成 token 并写 claim，没有像 `DBSlotManager.Acquire` 那样递增 `provider_accounts.in_flight_count` 和插入 `pool_slot_acquisitions`。因此 **不能直接把当前 PASRSelector 用作 actual production selector**。shadow 可以先运行，因为 shadow PASR 必须无 claim、无 slot、无状态写入，除 `SegmentTable.LookupOrCreate` 的内存段表和 metrics 外不改生产数据库。

推荐 actual acquire 顺序：

1. 选出 candidate account。
2. `Slots.Acquire(ctx, snapshot, req)`，拿到 acquisition token 和 release func。
3. `Claims.WriteAcquisition(ctx, tenantID, claimID, accountID, token)`。
4. claim write 成功：返回 `SelectionResult`。
5. claim write 失败：调用 release，`ErrClaimRace` 映射为可重试/可 fallback 前错误；DB fatal error fail closed。

shadow 强制无副作用：

1. 构造独立 `pasrShadow` 实例：`Claims=nil`、`Slots=nil`。
2. dispatcher 调用前复制 req：`shadowReq := req; shadowReq.ClaimID = 0`。
3. 单测用 `panicClaimGate` 注入 shadow 实例，证明 shadow 不会写 claim。
4. shadow 仍允许更新 `SegmentTable`，这是 PASR 学习路径的一部分；如果 Owner 要“纯观察无学习”，需新增决策点。

### M4：request-scoped AccountRing，避免全局跨租户 ring

| 项 | 内容 |
| --- | --- |
| 范围 | 调整 PASRSelector ring 来源：从 `AccountSource.ListAccounts(ctx, req)` 得到的 eligible snapshots 生成 request-scoped ring；保留 `RingProvider` 作为未来 A7 hot-swap 扩展但 main wire 不依赖它。 |
| LoC | 80-140 |
| 风险 | 中。改变 PASR selector 构造和测试预期，但不改 DB schema。 |
| 测试 | tenant/pool A 与 B 账号集合不同，PASR 不选出对方账号；account 删除后旧 segment 成员被过滤并 full-ring fallback；empty pool 返回 `ErrNoEligibleAccount`。 |
| 验收 | ring 成员只来自当前 `(tenant_id, pool_group_id)` 的 `ListEligibleAccountsByPoolGroup` 结果。 |

不推荐启动期全局 ring。理由：

1. `DBAccountSource.ListAccounts` 已按 `TenantID` + `PoolGroupID` 查 eligible accounts；全局 ring 会把其他租户 / pool 的账号混进 HRW Top3，再依赖 snapshots 过滤，cache locality 会失真。
2. A7 rebalance handler 暂停，启动期无法维护 per-tenant cached rings。为了真实，第一版应从请求的 eligible account snapshot 构造 ring。
3. ring 重建不是 5min ticker；每次请求已经要查 account snapshot，直接用同一批账号 ID 构造 ring，避免额外 DB 读。后续 A7 可以把 request-scoped ring 升级为 atomic cached ring。

### M5：main.go wire + lifecycle

| 项 | 内容 |
| --- | --- |
| 范围 | `backend/cmd/gateway/main.go`：`deps.selector` 改为 `pool.Selector`；创建 shared `accountSource`、`slotManager`、`claimGate`；按 config 构造 dispatcher；启动 feedback 和 aging worker。 |
| LoC | 100-180 |
| 风险 | 中。boot wiring 影响两个真实 endpoint。 |
| 测试 | main package compile；smoke build；shadow mode boot smoke；default mode 等价。 |
| 验收 | `default` 模式不构造 PASR worker；`shadow/canary/pasr-*` 模式构造 PASR runtime；shutdown 时 aging worker 随 signal ctx 退出。 |

wire 放置建议：

1. `config.Load()` 之后创建 `ctx`。
2. `db.Open`、`q := db.New(pgPool)` 后创建 shared adapters：
   - `accountSource := pool.NewDBAccountSource(q)`
   - `slotManager := pool.NewDBSlotManager(pgPool)`
   - `poolClaimGate := pool.NewDBClaimGate(q)`
3. `defaultSelector := pool.NewDefaultSelector(accountSource, pool.WithSlotManager(slotManager), pool.WithClaimGate(poolClaimGate), pool.WithStickyStore(...))`。
4. 如果 `cfg.PoolSelector.Mode != default`：
   - `segments := pool.NewSegmentTable(...)`
   - `pool.RegisterPASRCacheFeedback(segments)`
   - `agingWorker := pool.NewPASRAgingWorker(...)`
   - `agingWorker.Start(ctx)`
   - `defer agingWorker.Stop()`
   - 构造 `pasrActual` 和 `pasrShadow`
   - `selector := pool.NewSelectorDispatcher(...)`
5. `d.selector = selector`，路由挂载保持不变。

shutdown 协调：`PASRAgingWorker.Start(ctx)` 已监听 ctx cancel；`run` 收到 SIGINT/SIGTERM 后先进入 `<-ctx.Done()`，随后 `srv.Shutdown`。`defer agingWorker.Stop()` 兜底，确保非 signal error path 也不会泄漏 goroutine。

### M6：observability + logs

| 项 | 内容 |
| --- | --- |
| 范围 | expvar metrics + zap logs；不引入新 metrics 依赖。 |
| LoC | 100-160 |
| 风险 | 低-中。日志字段必须避免泄漏 prompt 或 credential。 |
| 测试 | metrics snapshot 单测；panic recovery 计数；log 字段可通过 observer logger 或 fake logger 验证核心字段。 |
| 验收 | `/debug/vars` 能看到 shadow/canary counter；生产 diff 可按 tenant/model/pool_group 聚合。 |

新增 expvar 建议放在 `"pasr_dispatch"` map：

| metric | 语义 |
| --- | --- |
| `shadow_sampled_total` | shadow 被抽样并执行 |
| `shadow_drop_total` | shadow 模式下未抽样 |
| `shadow_match_total` | default 与 PASR 结果同类且账号一致，或同为 no-capacity |
| `shadow_diff_total` | 结果账号、错误类别、是否 no-capacity 不一致 |
| `shadow_pasr_error_total` | shadow PASR 返回 error |
| `shadow_panic_total` | shadow PASR panic 被 recover |
| `canary_pasr_used_total` | canary bucket 命中且 PASR actual 被使用 |
| `canary_default_used_total` | canary 未命中，DefaultSelector 被使用 |
| `canary_fallback_default_total` | PASR 未变更前失败，回退 default |
| `canary_pasr_error_total` | PASR actual 返回错误 |
| `mode_default_total` / `mode_shadow_total` / `mode_canary_total` | dispatcher 运行模式请求计数 |

log schema：

| 字段 | 说明 |
| --- | --- |
| `event` | `pasr_shadow_compare` / `pasr_canary_select` / `pasr_dispatch_panic` |
| `selector_mode` | 当前 mode |
| `sample_percent` | shadow/canary percent |
| `sample_bucket` | 0-99 |
| `tenant_id` | 整数；允许用于内部 ops 聚合 |
| `pool_group_id` | 整数 |
| `requested_model` | 请求模型别名 |
| `endpoint_family` | chat/messages |
| `prefix_hash8` | `SessionHash` 前 8 字符；不记录原 prompt |
| `default_account_id` | default 结果账号，错误时 0 |
| `pasr_account_id` | PASR 结果账号，错误时 0 |
| `default_error_class` / `pasr_error_class` | `none` / `no_eligible` / `no_slot` / `claim_race` / `other` / `panic` |
| `default_latency_ms` / `pasr_latency_ms` | 选择器耗时 |
| `fallback_used` | canary PASR 失败后是否回退 default |

### M7：tests and smoke

| 项 | 内容 |
| --- | --- |
| 范围 | pool dispatcher unit tests、config unit tests、PASR selector slot/ring tests、gateway main smoke extension。 |
| LoC | 250-450 |
| 风险 | 低。测试为主。 |
| 验收 | `go test ./internal/config ./internal/pool ./internal/gatewayhttp ./cmd/gateway` 通过；有 DB env 时 smoke 覆盖 default/shadow 至少一档。 |

优先测试顺序：

1. 不需要 DB 的 unit tests 先写完，保证分流和无副作用。
2. 有 DB 的 smoke 只在 `HUAKAI_DATABASE_URL` 存在时跑，保持现有跳过模式。
3. actual canary 的 DB 状态断言必须检查 `billing_ledger_claims.provider_account_id`、`acquisition_token`、`pool_slot_acquisitions`、`provider_accounts.in_flight_count`。

## 5. Feature flag 状态机

```
default
  | set mode=shadow, shadow_percent=5/25/100
  v
shadow
  | Owner approves actual canary after metrics + slot tests
  v
canary (5%)
  | no HIGH risk signal for observation window
  v
canary (25%)
  | no HIGH risk signal for observation window
  v
canary (100%) or pasr-primary
  | final confidence / no fallback drill
  v
pasr-strict

Any state -- set HUAKAI_POOL_SELECTOR_MODE=default + restart --> default
```

模式进入条件：

| from | to | 条件 |
| --- | --- | --- |
| default | shadow 5% | config + dispatcher tests green；Owner 同意 shadow |
| shadow 5% | shadow 25% | `shadow_panic_total=0`，PASR error rate 可解释，diff 不暴露安全/租户边界问题 |
| shadow 25% | shadow 100% | diff 已按 tenant/model/pool 聚合分析；没有 claim/slot 写入 |
| shadow 100% | canary 5% | M3 slot/claim parity tests green；Owner 明确批准 actual traffic |
| canary 5% | canary 25% | no capacity error 未显著上升；settle/abort 行为正常；cache hit 指标有改善或不退化 |
| canary 25% | canary 100% / pasr-primary | shadow/canary diff 已可接受；rollback drill 成功 |
| any | default | 任意生产异常、Owner 要求、或 metrics 超阈值 |

状态机实现不放在 handler；只在 dispatcher 内部根据 typed config 判断。

## 6. Shadow mode 算法 + metrics

### 算法

1. dispatcher 收到 `Select(ctx, req)`。
2. 根据 `sampleBucket(req, salt)` 判断是否 shadow sampled。
3. 始终先调用 `defaultSel.Select(ctx, req)`，得到真实结果或真实错误。
4. 如果未 sampled：`shadow_drop_total++`，直接返回 default 结果。
5. 如果 sampled：
   - `shadowReq := req`
   - `shadowReq.ClaimID = 0`
   - 用 `pasrShadow.Select(shadowCtx, shadowReq)` 运行；`pasrShadow` 构造时 `Claims=nil`、`Slots=nil`。
   - `defer recover`，panic 只记 `shadow_panic_total`，不影响 default 结果。
6. 对比 default 与 PASR：
   - 双方成功且 `AccountID` 相同：match。
   - 双方成功但账号不同：diff，reason=`account_mismatch`。
   - default 成功，PASR `ErrNoEligibleAccount`：diff，reason=`pasr_no_eligible`，这是有效 diff，不应忽略。
   - default `ErrNoEligibleAccount`，PASR 成功：diff，reason=`default_no_eligible`。
   - 双方同为 no-capacity：match，reason=`both_no_capacity`。
   - 任一方其他 error：diff + error counter。
7. 返回 default 真实结果。

### 比对粒度

第一版采用 **per-request sampled compare**，不做 batch compare 作为主逻辑。batch 聚合由 expvar counter 和 logs 离线完成。per-request 的优势是能直接定位 `tenant_id/pool_group_id/model/prefix_hash8`，对 5%/25% 渐进足够。batch-only 会隐藏特定 pool 的租户边界错误。

### Shadow timeout

shadow 不应无限拖慢请求。建议 shadow selector 使用 `context.WithTimeout(ctx, 50ms~100ms)`；如果 Owner 更重视观察完整性，可以用原 ctx 但只在 5% 开。初始建议：5% shadow 用 100ms timeout，25%/100% 前根据 p95 selector latency 调整。

## 7. Canary 切流细节

### 抽样方式

推荐 **deterministic hash bucket**，不是 random，也不是人工 tenant 白名单作为唯一机制。

bucket key：

1. 优先 `SessionHash`，保证同一 prompt prefix 在 canary 中稳定走同一 selector，便于 cache locality 验证。
2. `SessionHash` 为空时用 `tenantID|apiKeyID|poolGroupID|requestedModel|claimID`，这是 per-request 稳定 fallback；claimID 会让短 prompt 分布更均匀。
3. `sampleBucket = fnv64a(salt || key) % 100`。

方式对比：

| 方式 | 优点 | 缺点 | 结论 |
| --- | --- | --- | --- |
| hash mod | 可复现、分布稳定、单测简单 | session hash 空时需 fallback key | 推荐 |
| tenant 白名单 | 运维可控 | 容易样本偏、万人级租户大小差异大 | 可作为未来附加过滤，不作为首版主机制 |
| random | 实现简单 | 难复现，单个 session 会抖动，cache 评估失真 | 不推荐 |
| per-request 纯 claimID | 分布均匀 | 同 prefix 不稳定，不利于 cache locality | 只作为无 SessionHash fallback 的一部分 |

### canary actual 失败回退

canary 期 PASR 命中 bucket 后：

1. 如果 PASR 在 **未 acquire slot、未 write claim** 前返回 `ErrNoEligibleAccount` 或 `ErrNoSlotAvailable`，可以回退 DefaultSelector，记 `canary_fallback_default_total`。
2. 如果 PASR 已经 acquire slot 或 write claim，则不允许再调用 DefaultSelector；否则可能出现双 slot、双 claim 或 claim race。此类错误必须 release slot 后 fail closed，并由 handler 走 abort。
3. `ErrClaimRace` 不应该被静默 fallback；它代表 claim 状态已经不符合预期，需要按错误分类记录。
4. `pasr-primary` 可以沿用第 1 条 fallback；`pasr-strict` 禁止 fallback。

## 8. AccountRing / PASR 启动期 wire

### ring 来源

第一版采用 **request-scoped per-tenant/pool ring**：

1. PASRSelector 调 `AccountSource.ListAccounts(ctx, req)`。
2. 从返回 snapshots 提取 eligible account IDs。
3. 用 `NewAccountRing(ids, cfg.HRWSeed)` 构造 ring。
4. `SegmentTable.LookupOrCreate(prefix, ring)` 获取段。
5. 旧 segment 中不在当前 snapshots 的成员按现有逻辑过滤；必要时走 full-ring fallback。

不做全局 ring，不做 5min DB ticker 重建 ring。原因是 A7 暂停时无法安全维护多租户多 pool ring cache；每请求从当前 eligible accounts 构造 ring 最真实，不引入跨租户候选污染。A7 恢复后可把 request-scoped ring 替换为 `RingProviderFor(req)` + atomic cached per-pool ring。

### 4 个组件放置

| 组件 | 放置位置 | 生命周期 |
| --- | --- | --- |
| `SegmentTable` | `run()` 中 PASR runtime 局部变量；被 actual/shadow selector 和 feedback 共享 | 进程级内存，重启清空 |
| `RingProvider` / ring builder | 初版在 PASRSelector 内从 request snapshots 构造；不在 main 做全局 provider | 每请求构造 |
| `PASRAgingWorker` | `run()` 构造，`Start(ctx)`，`defer Stop()` | 跟 signal ctx 退出 |
| `CacheFeedback` | `run()` 调 `pool.RegisterPASRCacheFeedback(segments)` | 进程级 observer，一次注册 |

## 9. 风险登记

| ID | 风险 | 触发条件 | 影响 | 缓解 |
| --- | --- | --- | --- | --- |
| R-PASR-MW-001 | PASR actual 绕过 slot acquisition | 直接把当前 PASRSelector 作为 canary actual | 超过账号并发上限，settle/release 状态不一致 | M3 必做；actual canary 前必须有 slot/claim parity tests；Owner 审批 |
| R-PASR-MW-002 | Shadow 误写 claim | shadow 实例复用 actual PASR，或 req.ClaimID 未清零 | billing claim 被非实际 selector 抢写，真实请求失败 | 独立 pasrShadow：`Claims=nil`、`Slots=nil`；dispatcher 强制 `ClaimID=0`；panic claim gate 单测 |
| R-PASR-MW-003 | 全局 ring 混入其他租户账号 | main 启动期用所有账号构建 AccountRing | cache locality 失真，极端情况下候选过滤后 no-capacity 上升 | request-scoped ring，只从 `ListAccounts(ctx, req)` eligible snapshots 构造 |
| R-PASR-MW-004 | canary fallback 双写 | PASR 写 claim 或 acquire slot 后再 fallback DefaultSelector | 双 slot、claim race、settle 对不上 | fallback 只允许发生在无 mutation 前；mutation 后 fail closed + release |
| R-PASR-MW-005 | shadow 增加请求延迟 | 25%/100% shadow 同步跑 DB account query | p95/p99 selector latency 上升 | sampled rollout；shadow timeout；记录 `pasr_latency_ms`；必要时先 5% 长窗口 |
| R-PASR-MW-006 | 配置拼错导致实际未启用或错误启用 | env value typo、percent 越界 | 观察数据不可信或流量误切 | typed config fail-fast；启动日志输出 mode/percent；非法值不 fail-soft |
| R-PASR-MW-007 | SegmentTable 内存增长 | shadow 100% + 高 prefix cardinality | 内存压力，GC 抖动 | `HUAKAI_PASR_SEGMENT_CAP` 默认 100k；aging 30min；监控 `pasr_segment_count` 和 evictions |
| R-PASR-MW-008 | diff 指标误读 | Default 和 PASR 设计目标不同，账号不同不一定坏 | Owner 误判 PASR 劣化或过早切流 | diff log 带 reason、model、pool；按 cache hit / latency / no-capacity 联合评估 |
| R-PASR-MW-009 | panic 影响请求 | PASR shadow/canary bug panic | 请求 500 或进程崩溃 | dispatcher recover；shadow panic 不影响 default；actual panic fail closed 并计数 |
| R-PASR-MW-010 | 回滚速度不足 | production no-capacity 激增，需要马上切回 | 继续影响 live traffic | 首版 restart-only 回滚；预写 runbook；保留 default selector 一直在进程中；是否做 SIGHUP 由 Owner 决策 |

## 10. 决策点

| ID | 需要 Owner 拍板的问题 | 推荐 |
| --- | --- | --- |
| D-PASR-MW-001 | actual canary 前是否允许补 M3（PASR SlotManager parity）作为 mainwire 范围的一部分？ | 必须允许；否则只能 shadow，不能真实承载生产流量。 |
| D-PASR-MW-002 | 第一版 rollback 是否接受 env 改 + restart，而不是 SIGHUP 热切？ | 接受 restart-only；万人级生产等稳定后再做热切，避免本轮多一个控制面风险。 |
| D-PASR-MW-003 | shadow 是否允许更新 in-memory SegmentTable 学习状态？ | 推荐允许；这是验证 cache locality 的必要学习路径，且不写 DB。若 Owner 要纯观察，需要另设 `HUAKAI_PASR_SHADOW_LEARN=false`。 |
| D-PASR-MW-004 | canary 5% 是否按 hash bucket 全租户抽样，还是先 tenant 白名单？ | 推荐 hash bucket 全租户 5%；如业务上有重点租户，可加白名单附加过滤但不替代 hash。 |
| D-PASR-MW-005 | `pasr-primary` 是否允许 no-capacity fallback default？ | 推荐允许；最终再用 `pasr-strict` 验证无 fallback。 |
| D-PASR-MW-006 | HRW seed 是否必须生产显式设置？ | 推荐生产必须显式设置，dev/test 可默认。 |

## 11. 测试矩阵

| 测试 ID | 场景 | 类型 | 断言 |
| --- | --- | --- | --- |
| T-PASR-MW-001 | config 默认 | unit | 未设置 PASR env 时 `Mode=default`，现有 gateway 行为不变 |
| T-PASR-MW-002 | config 非法模式 | unit | `HUAKAI_POOL_SELECTOR_MODE=foo` 返回 config error |
| T-PASR-MW-003 | config percent 越界 | unit | `-1`、`101`、非数字均 fail-fast |
| T-PASR-MW-004 | dispatcher default | unit | 只调用 default，不调用 PASR |
| T-PASR-MW-005 | dispatcher shadow sampled | unit | 返回 default 结果；PASR shadow 被调用；shadow `ClaimID=0` |
| T-PASR-MW-006 | dispatcher shadow not sampled | unit | `shadow_drop_total++`；PASR 不调用 |
| T-PASR-MW-007 | shadow match/diff | unit | 同账号记 match；不同账号 / PASR no eligible 记 diff |
| T-PASR-MW-008 | shadow panic | unit | recover；返回 default；`shadow_panic_total++` |
| T-PASR-MW-009 | canary distribution | unit | 固定 SessionHash bucket 可复现；5% 样本在大样本下接近 5% |
| T-PASR-MW-010 | canary PASR used | unit | bucket 命中时调用 PASR actual，未命中调用 default |
| T-PASR-MW-011 | canary fallback | unit | PASR no eligible 且无 mutation 时 fallback default；计数正确 |
| T-PASR-MW-012 | no double claim shadow | unit | shadow 注入 panic claim gate 不触发；actual 一次请求最多写一次 claim |
| T-PASR-MW-013 | PASR slot parity success | unit/integration | actual PASR 成功时写 `pool_slot_acquisitions`，递增 in-flight，claim token 一致 |
| T-PASR-MW-014 | PASR claim failure release | unit/integration | claim write 失败时 release slot，不泄漏 in-flight |
| T-PASR-MW-015 | per-tenant ring | unit | tenant A 请求只从 A 的 eligible accounts 中选 |
| T-PASR-MW-016 | empty ring / empty pool | unit | 返回 `ErrNoEligibleAccount`，canary 可 fallback default |
| T-PASR-MW-017 | main boot default | compile/smoke | `go test ./cmd/gateway` 或 build smoke 默认模式通过 |
| T-PASR-MW-018 | main boot shadow | smoke with DB env | `HUAKAI_POOL_SELECTOR_MODE=shadow` + `HUAKAI_PASR_SHADOW_PERCENT=100` 可启动并处理请求 |
| T-PASR-MW-019 | rollback | unit/smoke | mode 从 canary config 改为 default 后 dispatcher 不再调用 PASR |
| T-PASR-MW-020 | race sanity | optional | `go test -race ./internal/pool`，SegmentTable + dispatcher 无明显 data race |

必须优先通过的 gate：

1. M1/M2 default 等价测试通过。
2. shadow 无副作用测试通过。
3. M3 slot/claim parity 通过之前，不允许 production actual canary。
4. smoke 在 default 和 shadow 至少一档通过。

## 12. Rollback 路径

第一版 rollback 采用 restart-only：

1. 设置 `HUAKAI_POOL_SELECTOR_MODE=default`。
2. 清空或忽略 `HUAKAI_PASR_SHADOW_PERCENT` / `HUAKAI_PASR_CANARY_PERCENT`。
3. 重启 gateway 进程 / 滚动重启 deployment。
4. 验证启动日志 `selector_mode=default`。
5. 查 `/debug/vars`：`pasr_dispatch` 的 canary/pasr counters 不再增长；DefaultSelector 请求和 billing settle 正常。

预期恢复速度取决于部署系统，目标是 1 个滚动窗口内恢复。因为 DefaultSelector 始终保留且 handler 只依赖 `pool.Selector`，rollback 不需要 DB migration，不需要清段表，不需要清 claim 数据。

SIGHUP / in-memory atomic hot flag 本轮不作为默认实施项。可以在 `SelectorDispatcher` 内部用 atomic config 方便测试和未来热切，但没有 operator API 前不要宣传为生产回滚能力。若 Owner 要秒级回滚，需要另开 A7-lite 或 admin control plane 原子，属于中-高风险控制面改动。

## 13. Pre-execution checklist

1. Owner 确认本计划或合并计划，特别是 D-PASR-MW-001、D-PASR-MW-002、D-PASR-MW-003。
2. 确认本轮只读 HUAKAI 内部代码，不涉及非 MIT reference source。
3. 确认不改 DB schema、不做 A6/A7。
4. 先实现 M1/M2，保持 `mode=default` 等价。
5. 再实现 shadow wire，跑无副作用测试。
6. 再补 M3 slot/claim parity；M3 未完成前禁止 actual canary。
7. 最后接 main.go lifecycle、metrics、smoke。
8. 代码完成后 staging，跑 `codex exec review --uncommitted --full-auto`，按 AGENTS.md 每 commit 互审制度处理。

## 14. Success criteria

1. `HUAKAI_POOL_SELECTOR_MODE=default` 下行为与现状等价，已有 tests/smoke 不退化。
2. `shadow` 下用户请求实际仍由 DefaultSelector 处理，PASR 对比指标可见，shadow 不写 claim、不 acquire slot。
3. `canary` 下 5% hash bucket 可复现，PASR actual 与 DefaultSelector fallback 语义清楚，且不会双写 claim / slot。
4. PASR runtime 的 `SegmentTable`、feedback、aging worker 在 main.go 中生命周期完整，无 goroutine 泄漏。
5. rollback 到 default 不需要迁移、不需要删除数据，只需 env + restart。
6. 无功能缩水：DefaultSelector、sticky、claim、settle、cache feedback 都保留；PASR 只是新增 feature-flagged 调度路径。
7. clean-room 风险低：本轮只接入自有 PASR-lite 和 HUAKAI 内部接口，不读取或复制外部参考项目源码。
