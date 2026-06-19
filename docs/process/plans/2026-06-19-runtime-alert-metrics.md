# Plan — runtime 资源指标接既有告警引擎 (F-GW-003 Phase 2: 阈值/告警半)

- 日期: 2026-06-19
- 作者: Claude PM (autonomous; Owner「你定但不能偏移」+ 本轮回「a」= 批准默认推进 Phase 2)
- 基线: origin/feat/frontend-portal @ 9e72e32e
- 分支: feat/runtime-alert-metrics

## 背景 (禁止凭记忆 — 真码已核)

F-GW-003 resource 半: Phase 1(PR#47)给 admin /system/health 补了 live runtime **快照**(测量)。Phase 2 = 让运维能对这些
runtime 资源**设阈值告警**(预算执行半)。

**真码 grounding(本轮 2-3 探针)确认 tight, 无 schema**:
- 告警引擎已全建: alerting.Service(规则 CRUD + 调度评估 + sustained-breach + cooldown + silence + 投递)+ alertinghttp CRUD。
- 指标源已全建: alertmetrics.CompositeMetricSource(composite.go:77)聚合 GlobalSource + UsageRolluper(latency)+ AccountHealth;
  GlobalSource = **otelbridge.NewExpvarMetricSource()**(wiring.go:1236), 其 Snapshot 遍历 **bridgeCounters() 白名单**(expvarbridge.go:94)
  返回 {metric:value} 喂进 composite → 调度器评估规则。
- **关键: 规则 metric 名无 allow-list**。alerting.validateRule(service.go:392)只校验 metric 非空(metricKeyForRule==""), comparator/
  threshold/severity/window 有校验, 但 **metric 字符串自由**。→ 运维现在就能建 metric="huakai_runtime_heap_alloc_bytes" 的规则;
  规则在评估时按 metric 名查 snapshot, 名字在 snapshot 里就触发, 不在就静默不触发。
- **结论: 唯一改动 = 把 runtime gauge 加进 bridgeCounters() 白名单** → 出现在 snapshot → 运维用既有 CRUD 设规则。
  无新 schema、无 CRUD 改、无 wiring 改、无 openapi 改(无端点/响应变更)。STW: 仅 heap 读 MemStats, composite snapshot 有缓存
  (composite.go:181)→ 每 TTL 至多一次, 成本可忽略。

## #16 三镜像 (specifier lane, 本轮新探针 #16-rt-threshold 完成)
「网关对自身 runtime 资源(heap/goroutine/uptime)经一等告警规则引擎设运维阈值」:
- **sub2api@e34ad2b (默认 tiebreaker)**: **有**真·运维可配告警规则引擎(value-vs-threshold + sustained + cooldown + silence + event/email,
  ops_alert_evaluator_service.go:435-638, ops_alerts_handler.go:19-36 有 metric-type **allow-list**); **也**采集进程 goroutine 数
  (ops_metrics_collector.go:301-361)—— **但二者从不相遇**: allow-list/评估 switch 里无 Go-runtime gauge,"memory_usage_percent" 解析
  成 host/cgroup % 非 Go heap; goroutine 数仅 dashboard 展示, 非规则 subject; 无 heap-bytes/uptime metric 类型。health-score 是另一条
  hardcoded 路径(ops_health_score.go:71-133)非运维阈值。
- **new-api@1ac0f58**: Go memstats/goroutine **仅展示**(controller/performance.go:88-140);**无**通用告警引擎;另有 host cpu/mem/disk-% 阈值→
  **503 拒绝**的准入守卫(middleware/performance.go:41-71)非告警、非 runtime gauge;通知仅 quota/channel subject。
- **CLIProxyAPI@2a050dc**: **no-equivalent**(/healthz liveness + 可选 pprof, server.go:409 / pprof_server.go:44)。

### HUAKAI delta — novel-at-this-precision (三镜像无一做到)
| 维度 | sub2api | new-api | CLIProxy | HUAKAI delta | dimension |
|---|---|---|---|---|---|
| runtime gauge 可设告警阈值 | ✗(引擎在但 allow-list 不含 runtime gauge) | ✗(仅展示+host% 503守卫) | ✗ | **✓ heap/goroutine/uptime 升为一等规则 subject** | 架构 |
| 复用既有业务指标告警引擎 | n/a(runtime 不进引擎) | 无引擎 | 无 | ✓ 同 error-rate/latency 同一引擎同一路径 | 架构 |
| uptime<N crash-loop 谓词 | 仅 health-score 内 job-heartbeat 近似(不可配) | 无 | 无 | ✓ 运维可配 uptime 阈值 | 算法 |
- **delta(架构)**: 把进程 runtime 自指标提升为既有运维告警规则引擎的**一等 subject**(三镜像都没把 runtime gauge 和告警引擎打通);
  HUAKAI 因 metric 名自由(无 allow-list)使这步只需在指标源白名单加 3 条。**算法**次 delta: uptime<N 的 crash-loop 告警谓词无镜像对应。

## 实现范围 (success criteria)
- otelbridge/expvarbridge.go: bridgeCounters() 加 3 条 runtime gauge(read func 直接读 runtime/time, 非 expvar-backed,
  同既有 dlq_pending_depth gauge 经此 bridge 之先例): huakai_runtime_heap_alloc_bytes(ReadMemStats.HeapAlloc)、
  huakai_runtime_goroutines(NumGoroutine)、huakai_runtime_uptime_seconds(time.Since(processStart))。+ processStart 包级 var + runtime/time import。
- 测试(变异验证): runtime_gauges_test.go 经 ExpvarMetricSource.Snapshot()(真·喂告警引擎路径)断三 gauge present + heap>0/goroutine>=1
  live 不变量 + uptime 显式 present 检查;改 heap key 名→present=false→红(已证)。
- 决策(自决, 非 money/security/schema 高风险叉故不 gate): 暴露 3 个(heap_alloc/goroutine/uptime)非全部 RuntimeInfo 字段——
  heap_sys/num_gc 略去以免第二次 ReadMemStats STW(后续要可加共享读 helper);这 3 个正是 resource-budget 告警三维(内存泄漏/goroutine 泄漏/重启)。

## blast radius
- 仅 otelbridge/expvarbridge.go(+新测试文件)。bridgeCounters() 同时供 OTel 导出(RegisterBridge 作 ObservableCounter)+ 告警 Snapshot;
  runtime gauge 经 counter instrument 导出语义略不精(gauge vs counter)——但**既有 dlq_pending_depth 已是此先例**, 非新增债;
  告警 Snapshot(主消费)读原值正确。ops002_bridge_test 按**具体名**匹配非总数, +3 不破。无迁移/依赖/money/auth/schema。
  codebudget: expvarbridge.go +~30 行远 < 600。otelbridge 与 proxies 分支 0 碰撞(已核)。

## 门禁
ultracode 对抗审查零 S0/S1 → 干净基线 fail 0(含 alerting 包 + cmd/gateway OpenAPI) → squash → ff。
Phase 3(runtime forecast / 趋势)更大, 后续。运维用法文档(metric 名词汇表)目前无 catalog 文档(dlq 等也没记)→ 不在本切片造, 避免 scope-creep。

## Clean-room 出处 (#11(d))
- Source files read: sub2api@e34ad2b {internal/service/ops_alert_evaluator_service.go, ops_alerts.go, ops_metrics_collector.go, ops_health_score.go, ops_port.go, handler/admin/ops_alerts_handler.go};
  new-api@1ac0f58 {controller/performance.go, middleware/performance.go, common/system_monitor.go, setting/operation_setting/monitor_setting.go, pkg/perf_metrics/types.go, dto/notify.go, service/user_notify.go};
  CLIProxyAPI@2a050dc {internal/api/server.go, sdk/cliproxy/pprof_server.go}
- 首引 recency#12: 三 SHA 同 [[parity-audit-2026-06-18]] Phase-1 已核 active@2026-06-18(本轮 GitHub API 沙箱不可达, 复用并记 SHA)。
- Lane: specifier(独立 agent #16-rt-threshold). Agent: Claude PM. UTC: 2026-06-19
