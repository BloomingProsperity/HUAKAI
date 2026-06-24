# 计划:account health probe 死开关接线(写 last_probe_at)

- 日期:2026-06-24
- 分支:`feat/account-health-probe-wiring`(base = `feat/frontend-portal` @e3f12655)
- 切片类型:纯可观测接线(点亮"建了没用"的死开关)
- 风险等级:低(不沾钱 / auth,无默认行为翻转,无 schema 迁移)

## 1. 范围(scope)

`provider_accounts` 的 `last_probe_latency_ms` / `last_probe_at` 两列(迁移 0110 早已加)
读取侧齐全——三处 SELECT 回显给运维健康面板:

- `internal/gatewayhttp/admin_pool_accounts_handler.go`
- `internal/adminhttp/provider_account_health_handler.go`
- `internal/hermesops/tools_health.go`

但**全仓零写入** → 两列恒 NULL → 面板恒空。

命门:`cmd/gateway/middleware.go` 给 `observability.NewAccountHealthProbeHandler(...)`
传的 `probe` 参数是 `nil`。该 handler 挂在 completion eventbus 上(异步、Tier=MED、
`Critical()==false`、有超时、失败走 DLQ),每次请求完成都被触发,但
`account_health_probe_handler.go` 里 `if h.probe == nil return nil` 直接空转。

本切片注入一个真实 `probe`,把 `signal.At` 写进对应池账号的 `last_probe_at` 列,
点亮健康面板。

## 2. scope_decision:为什么只写 last_probe_at、不写 last_probe_latency_ms

任务要求自核 4 个 `RequestCompletionEvent{}` 发射点定夺 latency 是否一并写。核完结论:

发射点(grep `RequestCompletionEvent{`,均在 `internal/gatewayhttp/`):

1. `chat_completions_stream.go:768` — 流式定稿;上游延迟 = `time.Since(upstreamAttemptStartedAt)`,
   局部变量,未挂到 event 上。
2. `chat_completions_billing.go:129` — 非流式定稿;延迟在 `dispatchRawBuffered` /
   `finalizeBufferedEnvelope` 局部 `time.Since(startedAt)` 算,未上浮。
3. `chat_completions_handler_headers.go:228` — **L2 缓存命中**路径;**根本没有上游往返**,
   "上游探测延迟"语义未定义(命中不打上游)。
4. `chat_completions_handler_headers.go:299` — 缓存命中定稿的另一分支,同上无上游延迟。

`RequestCompletionEvent` 结构体没有显式延迟字段;`chatExecution.startedAt` 是**请求开始**
时间(含排队 / 路由 / 缓存查找),不是运维期望的"上游探测往返延迟"。要写一个**有意义**的
latency,需要在 4 个发射点(含语义未定义的缓存命中路径)各自把局部延迟上浮成 event 新字段
+ `AccountHealthSignal` 新字段,且其中两处穿过 money / billing 定稿热路径。

**判定:发射点分散、跨缓存命中语义空洞、且触及 billing 热路径,侵入大、收益小。
按任务给定的"别为半列硬塞进热路径"原则,本切片只写 `last_probe_at`,
`last_probe_latency_ms` 留作 follow-up。** follow-up 的干净做法是:在 `chatExecution`
上加一个"上游往返延迟"专用字段(只在真正打上游的 dispatch / stream 路径赋值,缓存命中
留零 / 不写),在 emission 处一处读出塞进 event,而不是在 4 个发射点各塞一遍。

## 3. blast radius(影响面)

新增 / 改动:

- `backend/sql/queries/admin_provider_account_health.sql` — 新增 `TouchProviderAccountProbe :exec`
  (单行 `UPDATE provider_accounts SET last_probe_at WHERE id AND tenant_id AND deleted_at IS NULL`)。
- `backend/internal/db/admin/admin_provider_account_health.sql.go` — 手写对应 sqlc 生成代码
  (环境无 sqlc 二进制,按既有风格补,不改其它生成块)。
- `backend/internal/db/admin/querier.go` — 接口加 `TouchProviderAccountProbe`。
- `backend/internal/observability/accounthealthprobe/postgres_probe.go` — **新子包**,
  `NewPostgresProbe(store) func(ctx, AccountHealthSignal) error`,把 signal 写成 UPDATE。
  放新子包而非堆 gatewayhttp,守包预算纪律。
- `backend/cmd/gateway/middleware.go` — 把 `buildCompletionEventBus` 透传 `pgPool`,
  用 `accounthealthprobe.NewPostgresProbe(admindb.New(pgPool))` 替换 `nil`。
- 测试:probe 子包单测(fake store,3+ 用例)+ admin 包 `integration_pg` 真 PG 测。
- `cmd/gateway/wiring_test.go` — 跟随 `buildCompletionEventBus` 签名新增 nil 形参。

**不动**:read 侧三处 handler、计费 / settler、health_state 状态机、Sidebar / 前端、
proxies-collision 包。

## 4. 性质守住

- **不沾钱 / auth**:probe 只 UPDATE 一个时间戳列,不碰余额 / ledger / token / session / RBAC。
- **无默认行为翻转**:此前 probe 恒空转(死),现在恒写(活);不存在"翻一个默认开关"
  改变既有可观察行为——是把一个一直无效的回调变有效。eventbus 本身 Enabled 与否的默认不变。
- **无 schema 迁移**:`last_probe_at` 列在迁移 0110 已存在,只是没人写。
- **不拖慢请求热路径**:写发生在异步 eventbus handler 内(`Critical()==false`、有
  `Timeout()`、失败走 DLQ),DB 写是单行 PK 定位的 `UPDATE`。同步请求转发路径不加任何阻塞;
  probe 报错只透传给 eventbus 走 DLQ,不反压请求。

## 5. 测试与 mutation 证伪

- A 死路点亮:probe 非 nil 时处理一个 `RequestCompletionEvent` → store 落 1 次写,
  参数 = 正确 (AccountID, TenantID) + `last_probe_at` 非 NULL。变异:probe 改回 nil → red。
- C 不阻断:probe 内 DB 出错 → error 透传给 eventbus(走 DLQ);断言 `Critical()==false`。
- 真 PG(integration_pg):`last_probe_at` 从 NULL → 预期时间戳;跨租户调用不篡改。
- latency(B)本切片不实现(见 §2),故无 B 测试,follow-up 补。

## 6. follow-up(留给后续切片)

- `last_probe_latency_ms`:在 `chatExecution` 加专用上游往返延迟字段,只在真正打上游的
  dispatch / stream 路径赋值,emission 一处读出塞进 event + signal,再在 probe UPDATE
  里补 `last_probe_latency_ms = $2`。缓存命中路径延迟列留 NULL(语义正确)。
