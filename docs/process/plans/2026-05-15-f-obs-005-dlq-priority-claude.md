# 2026-05-15 F-OBS-005 DLQ + priority + dual-write (Claude 独立 plan)

| Lane | SPECIFIER (Claude); 平行 codex |
| Source | F-OBS-005 row + F-OBS-004 dependency |
| Agent | Claude Opus 4.7 (1M context) |
| UTC | 2026-05-15T13:51:00Z |

## scope

F-OBS-004 handler chain 的失败安全网 + 优先级 + replica:
1. **DLQ**: 失败 event 落持久队列, 操作员可见 + 手动重放
2. **Priority lanes**: 不同 handler 不同 priority (BillingPersister > AuditLogger > MetricsAggregator)
3. **Dual-write**: 关键 handler (Billing/Audit) 写主库 + replica 一份,保证不丢

## file-by-file impact

- `backend/internal/dlq/` (新建): DLQ table + retry policy + admin API
- `backend/internal/db/migrations/`: dlq_entries table + audit_events_replica table
- `backend/internal/eventbus/priority.go`: per-handler priority field + worker pool quota
- `backend/internal/admin/dlq_handler.go`: 操作员 API GET /admin/dlq + POST /admin/dlq/{id}/replay

## DLQ retry strategy

- exponential backoff (1s / 5s / 30s / 5min / 1h, max 5 attempts)
- 5 attempts 后转 "operator-review" 状态,等待人工
- 操作员 dashboard 显示 failed_count by handler / error_class

## priority lanes

3 个 tier:
- **HIGH**: BillingPersister, AuditLogger — 单独 dedicated worker pool (2x worker)
- **MED**: AccountHealthProbe — 共享 default pool
- **LOW**: MetricsAggregator — drain-on-shutdown allowed

满 channel 时低优先级先 drop。

## dual-write 一致性

- Primary 写成功 → 异步推 replica (best-effort, 失败入 DLQ)
- Read-after-write: 读主库;replica 仅 read-only 备份
- 最大允许 lag: 10s (告警门槛)

## test plan

- unit: DLQ retry policy unit
- integration: 模拟 handler 错误,验证 DLQ entry 5 次重试后转 operator-review
- chaos: 杀掉 replica DB,验证主库继续 ok 且 DLQ 接住 replica failures
- migration: dry-run dlq_entries + audit_events_replica 创建

## time estimate

5-7 天 codex + 2 天 review + 1 天 migration dry-run

## blast radius

数据层 - **HIGH** (新表 + dual-write 改写路径)。dual-write 一致性 bug 会导致 silent data loss。

## decision points

(D1) Retry policy 参数 (backoff 间隔 + max attempts)  
(D2) Dual-write 是 sync 还是 async  
(D3) replica 是同库 schema 还是不同 (跨机房?)  
(D4) DLQ 操作员手动 replay 是否要审批  
(D5) audit_events_replica 是否给读副本对外服务

## clean-room

声誉级: new-api 据闻有 retry 但无 DLQ; sub2api 据闻有 audit 但没 dual-write replica。HUAKAI 升级点 (架构升级 + 生态升级): DLQ + priority + dual-write 三件套显式; operator-replay UI 在 admin dashboard surface (F-OPS family)。

## sources read

- F-OBS-005 row
- F-OBS-004 plan (Claude 平行版)
- (未读) 上游 reference 源码
