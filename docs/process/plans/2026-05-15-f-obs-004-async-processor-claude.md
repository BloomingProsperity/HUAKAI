# 2026-05-15 F-OBS-004 async processor chain (Claude 独立 plan)

| Lane | SPECIFIER (Claude); 平行 codex CLAUDE.md #10 |
| Source | F-OBS-004 + F-OBS-005 dependency + memory project_sub2api_scaling_bottleneck |
| Agent | Claude Opus 4.7 (1M context) |
| UTC | 2026-05-15T13:48:00Z |

## scope

请求完成 / 失败事件 → async handler chain,把 billing / audit / metrics / DLQ 推到 reverse proxy 热路径之外。memory `project_sub2api_scaling_bottleneck` 7 known causes 之一是同步持久化堆积,本 feature 解决。

## handler chain architecture

```
[Hot Path: Reverse Proxy]
       │
       ▼
  emit "request_completed" event (non-blocking)
       │
       ▼
  [Async Event Bus (in-process channel + persistent queue)]
       │
       ├── handler[0]: BillingPersister  (写 Tx2 ledger)
       ├── handler[1]: AuditLogger        (写 audit_events; F-TRUST 信任链卖点)
       ├── handler[2]: MetricsAggregator  (Prometheus counter inc)
       ├── handler[3]: AccountHealthProbe (account_health 表更新)
       └── handler[4]: DLQ failure path (if any handler returns error)
```

每 handler 独立 goroutine pool + 有界 channel buffer + 超时 deadline。

## file-by-file impact

- `backend/internal/eventbus/` (新建) — Channel + worker pool + handler registry
- `backend/internal/observability/{billing,audit,metrics,health}_handler.go` (新建 / 重构) — 5 个 handler
- `backend/internal/proxy/proxy.go` hot path — 把同步 billing/audit 调改为 `eventbus.Emit(...)`
- `backend/internal/config/eventbus.go` — buffer size / worker count / timeout 配置

## priority queue + DLQ integration

handler chain 失败时:
- transient (e.g. DB busy) → 重试 handler 自身;DLQ for transient = retry queue (F-OBS-005)
- permanent (e.g. schema mismatch) → 直接 DLQ permanent (F-OBS-005)
- ordered 关系: BillingPersister 必须在 AuditLogger 前 (audit 引用 billing entry)

## test plan

- unit: `eventbus/bus_test.go` (multi-handler dispatch / handler timeout / handler panic recovery)
- integration: 模拟 100 req/s 进 hot path,验证 hot path latency 不被 handler slow path 影响
- chaos: 杀掉某 handler,验证其他 handler 不受影响 + DLQ 接住 failed events

## time estimate

4-6 天 codex 实施 + 1-2 天 Claude review

## blast radius

中. 改 hot path 路由 → 任何 bug 影响所有请求。但 fail-open 设计 (handler fail 不阻塞 hot path) 降低 blast。

## decision points

(D1) in-process channel vs Redis Pub/Sub vs disk-backed queue  
(D2) handler 顺序是 strict-ordered 还是 fan-out 并行  
(D3) Backpressure 策略: 满 channel 是 drop oldest, drop newest, 还是 block hot path  
(D4) 上线前是否双跑 (旧同步 + 新异步) 校对 7-14 天

## clean-room

声誉级灵感: portkey / helicone 据闻都有 async ingestion。LiteLLM 据闻是同步 + Redis。HUAKAI 升级点 (架构升级 + 生态升级): 5 handler 显式 declaration + ordered/fan-out 可配 + DLQ 与 F-OBS-005 共享。

## sources read

- F-OBS-004 row in parity matrix
- memory project_sub2api_scaling_bottleneck
- backend/internal/proxy/proxy.go (热路径 grep)
- (未读) 上游 reference 源码
