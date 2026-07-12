# Plan — 系统健康端点补 runtime 资源快照 (F-GW-003 Phase 1: 测量半)

- 日期: 2026-06-18
- 作者: Claude PM (autonomous; Owner「你定但不能偏移」, 拐点后默认安全 big-build Phase 1)
- 基线: origin/feat/frontend-portal @ bf76c873
- 分支: feat/system-health-runtime-snapshot

## 背景 (禁止凭记忆)

F-GW-003(parity L2, 审计 PARTIAL): 运维可断言+验证网关的「运行时资源预算(内存/二进制大小/冷启动)」。审计称 latency 半已建
(已核: latency→alert 链全建 alerting/service.go+wiring.go:1232), **resource 半零基建**(已核: grep ReadMemStats/
os.Executable/NumGoroutine 在非测试码 0 命中)。

**Phase 1 = 测量半**: 给既有 admin `GET /v1/admin/system/health`(systemhealthhttp, ADMIN-042, 已 adminGate)补一个
**live runtime 快照**(heap/goroutine/GC/uptime/go-version/binary-size)。这不是从零建子系统——是扩既有聚合端点(同
/quota、priority 的 surfacing 模式)。Phase 2(budget threshold + violation → 接已建 alert 引擎)、Phase 3(forecast)后续 PR。

非 money/auth/schema(纯 runtime 读, 无 DB/迁移/外部依赖)/avoidance; systemhealthhttp 与 proxies 分支 0 碰撞(已核);
只读、无副作用。secret 安全: 只暴露二进制**大小**不暴露**路径**;heap/goroutine/version/uptime 皆诊断非密。

## #16 三镜像 (clean-room specifier lane, 已完成)
「网关暴露自身进程 runtime 资源自指标(heap/goroutine/GC/uptime/version)于 health/status/admin 端点」:
- **new-api@1ac0f58**: root-gated `GET /api/performance/stats`(controller/performance.go:83)**live** 读 Go runtime 内存
  (heap alloc/total/sys)+GC 计数+goroutine 数(performance.go:124-130)+gopsutil host CPU/内存/磁盘%; 纯展示无 budget;
  无 go-version/uptime 字段。
- **sub2api@e34ad2b (默认 tiebreaker)**: admin ops 仪表盘暴露 goroutine 数+OS/cgroup 内存/CPU%+uptime+app 版本, 但经
  **后台采集器→DB→读最新行**(ops_metrics_collector.go:109/301, dashboard_handler.go:77 uptime live), **windowed 持久化快照**
  非 live runtime.ReadMemStats; 配 health-score 阈值(ops_health_score.go:71)。无 Go heap/GC/go-version。
- **CLIProxyAPI@2a050dc**: **no-equivalent** —— /healthz 仅 liveness {status:ok}(server.go:409), pprof opt-in 独立端口默认关。

### 取舍 + HUAKAI delta (生态/observability) — 偏离 tiebreaker 的有据 carve-out
| 维度 | new-api | sub2api(tiebreaker) | CLIProxy | HUAKAI delta | dimension |
|---|---|---|---|---|---|
| runtime 读法 | **live** runtime.ReadMemStats(root-gated 专用端点) | **采集器→DB→windowed**(admin 仪表盘) | 无 | **live** 读(贴 HUAKAI system/health 既有 live 聚合本质) | 架构(读法贴端点性质) |
| 放置 | 独立 perf 端点 | 独立仪表盘 | n/a | **inline 于既有聚合 system/health**(一读含 components+runtime) | 生态 |
| 维度 | heap/GC/goroutine + host% | goroutine/OS-mem/uptime/ver + health-score | 无 | heap/GC/goroutine **+ uptime + go-version + binary-size**(三家无一齐全) | 生态 |
- **carve-out 理由**: tiebreaker(sub2api)用采集器/windowed, 但 HUAKAI system/health **本就是 live per-request 聚合**(DBPing/
  ChannelHealthSummary 皆 live), 故 live runtime 读(new-api 式)才一致; 采集器/windowed 需新 DB 表(schema-gated, 更大)=
  Phase-2+ 选项。非 money/security/schema 高风险叉(纯架构契合), 故自决取 live。

## 实现范围 (success criteria)
- systemhealthhttp/systemhealth.go: RuntimeInfo 结构 + Runtime 字段挂 HealthResponse + collectRuntimeInfo()(runtime.ReadMemStats
  + NumGoroutine + Version + uptime[包级 processStart var] + os.Executable→Stat 取 binary size, 失败省略)+ handler 200 路径置。
- openapi.yaml: SystemHealthResponse +runtime(required) + SystemHealthRuntime schema。
- 测试(变异验证): TestSystemHealthRuntimeSnapshot 断 go_version 前缀"go"/goroutine>=1/heap>0/uptime>=0(live 不变量);
  删 Runtime 置→零值→3 断言红(已证)。

## blast radius
- 仅 systemhealthhttp/systemhealth.go(+测试)+ openapi.yaml。**不动 cmd/gateway 接线**(runtime 读无需 source/deps)、不动
  alert 引擎/channelhealth。无迁移、无外部依赖、无 money/auth。codebudget: 文件 ~230 行 < 600。

## 门禁
ultracode 对抗审查零 S0/S1 → 干净基线 fail 0(含 cmd/gateway OpenAPI 一致性)→ squash → ff。Phase 2/3 后续 PR。

## Clean-room 出处 (#11(d))
- Source files read: new-api@1ac0f58 {controller/performance.go, controller/misc.go, common/system_monitor.go, router/api-router.go};
  sub2api@e34ad2b {internal/service/ops_metrics_collector.go, ops_dashboard.go, ops_port.go, ops_health_score.go, handler/admin/dashboard_handler.go, server/routes/admin.go, server/routes/common.go};
  CLIProxyAPI@2a050dc {internal/api/server.go, sdk/cliproxy/pprof_server.go, internal/config/config.go}
- 首引 recency#12: 三 SHA archived/disabled=false, pushed_at 2026-06-18(同 [[parity-audit-2026-06-18]])。
- Lane: specifier(三独立 agent). Agent: Claude PM. UTC: 2026-06-18
