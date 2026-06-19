# Plan — 预算/限流 fail-open 计数器接入告警/指标 (observability tight slice)

- 日期: 2026-06-19
- 作者: Claude PM (autonomous; Owner「你定但不能偏移」+「别反复问」; 收尾挖矿 4-agent sweep 选出最干净候选)
- 基线: origin/feat/frontend-portal @ 48f48cf4
- 分支: feat/bridge-budget-failopen

## 背景 (禁止凭记忆 — 真码已核)

收尾挖矿(4 并行 agent 扫 disjoint 包)选出最干净候选: **bridge `budget_fail_open_total`**。

- **producer 真实非死**: budget.types.go:22 `expvar.NewInt("budget_fail_open_total")`, 在 service.go **三处自增**:
  - :152 settle delta 的 store.Adjust 失败 → fail-open(return nil 不传错)
  - :175 release 的 store.Adjust 失败 → fail-open
  - :256 reserve store 失败且无 memory fallback → **return ReserveResult{Allowed:true, FailOpen:true}**(请求被放行、enforcement 被绕过)
  → 即「预算/限流后端出错时放行而非拒绝」的安全旁路计数。持续 fail-open = enforcement 实质失效 = 成本/滥用风险, 运维必须能告警。
- **gap**: 它**不在** otelbridge.bridgeCounters()(expvarbridge.go), 而其同类 peer `group_policy_fail_open_total` **已在**(expvarbridge.go:150)。明显缺失的对称项。
- **路径复用**: bridgeCounters() 同时供 RegisterBridge(Prometheus /metrics)+ ExpvarMetricSource.Snapshot(告警引擎指标快照, wiring.go GlobalSource)。加一条即同时进 Prometheus + 告警规则可设阈值(同 PR#48 机制), **零 cmd/gateway 接线、零 schema、零 openapi**(metric 非 API 响应)。

非 money-logic 改动(只读既有 expvar 计数, **不动 budget 包**, 同 group_policy 计数被读法), 非 schema/auth/avoidance; otelbridge 与 proxies 0 碰撞(已核)。

## #16 三镜像 (specifier lane, 本轮新探针 #16-failopen-obs 完成)
「网关把 enforcement fail-open(限流/预算后端出错放行)暴露成运维可告警指标」:
- **sub2api@e34ad2b**: **有** fail-open(限流中间件默认放行 rate_limiter.go:100-108/18-21; 预算/配额 cache 出错降级单查不拒 billing_cache_service.go:1212-1224)——但旁路**仅日志**(rate_limiter.go:101 log.Printf), **无计数器**, 无 Prometheus/expvar, alert 引擎 metric enum 无 fail-open 键(ops_alert_evaluator_service.go:448-611)。最近似 precedent(幂等 store-unavailable 原子计数 idempotency_observability.go:13-22)是另一子系统且无端点消费。
- **new-api@1ac0f58**: 全 **fail-closed**(限流/配额后端出错→HTTP 500 拒绝: rate-limit.go:25-31, model-rate-limit.go:86-91, pre_consume_quota.go:33-37; 配额 cache 出错降级 DB 非旁路 user.go:784-806)→ 没有这个问题也没计数。
- **CLIProxyAPI@2a050dc**: **no-equivalent**(纯中继, 无本地预算/配额/限流 enforcement; quota.go/antigravity_credits.go 是上游账号配额非本地预算)。

### HUAKAI delta — novel-at-this-precision
| 维度 | sub2api | new-api | CLIProxy | HUAKAI delta | dimension |
|---|---|---|---|---|---|
| enforcement fail-open 存在 | ✓(默认放行) | ✗(fail-closed) | ✗(无 enforcement) | ✓(reserve/settle/release 放行) | — |
| fail-open 成一等可告警指标 | ✗(仅日志, 无计数/无 Prometheus/alert enum 无键) | ✗ | ✗ | **✓ 专用计数器 bridge 进 Prometheus + 告警快照可设阈值** | 架构(旁路信号→可阈值指标)+生态(Prometheus/alert 集成, 两镜像都没有) |
- **delta**: 把「enforcement 被后端故障静默绕过」从日志行提升为专用具名计数器并接进 Prometheus + 告警规则快照(运维可 threshold)——三镜像无一做到的闭环(sub2api 最近但仅日志)。

## 实现范围 (success criteria)
- otelbridge/expvarbridge.go: bridgeCounters() 加 1 条 `huakai_budget_failopen_total` 读 `budget_fail_open_total`(命名同 peer 约定: bridge 名 failopen / expvar 键 fail_open)。
- 测试(变异验证): TestPrometheusExporterEnabledBridgesBudgetFailOpen(scrape /metrics 断 =9)+ 扩 TestExpvarMetricSourceSnapshotsBridgeMetrics(告警快照断 =6); 改 budget key 名→两测试红(已证: Prometheus 0≠9 + snapshot 0≠6)。fixture 值 9/6 与既有 3/4/5/11 各异。

## blast radius
- 仅 otelbridge/expvarbridge.go(+ otelbridge_test.go)。OTel 导出语义: budget fail-open 是单调累计计数(只 .Add(1) 从不减)→ 作 Int64ObservableCounter **语义正确**(不同于 PR#48 的 gauge)。ops002_bridge_test 按具体名匹配非总数, +1 不破。无迁移/依赖/money-logic/auth/schema/openapi。codebudget: +~12 行远 < 600。

## 门禁
ultracode 对抗审查零 S0/S1 → 干净基线 fail 0(含 cmd/gateway OpenAPI) → squash → ff。

## Clean-room 出处 (#11(d))
- Source files read: sub2api@e34ad2b {internal/middleware/rate_limiter.go, internal/service/billing_cache_service.go, ops_alert_evaluator_service.go, ops_metrics_collector.go, idempotency_observability.go, server/routes/auth.go, handler/gateway_handler.go};
  new-api@1ac0f58 {middleware/rate-limit.go, middleware/model-rate-limit.go, service/pre_consume_quota.go, model/user.go, model/user_cache.go, pkg/perf_metrics/types.go};
  CLIProxyAPI@2a050dc {internal/api/handlers/management/quota.go, sdk/cliproxy/auth/antigravity_credits.go, sdk/cliproxy/auth/types.go, sdk/cliproxy/auth/conductor.go, sdk/api/handlers/handlers.go}
- 首引 recency#12: 三 SHA 同 [[parity-audit-2026-06-18]] 已核 active@2026-06-18(GitHub API 沙箱不可达, 复用并记 SHA)。
- Lane: specifier(独立 agent #16-failopen-obs). Agent: Claude PM. UTC: 2026-06-19
