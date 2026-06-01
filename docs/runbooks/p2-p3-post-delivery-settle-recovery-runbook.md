# P2/P3 Post-Delivery Settle Recovery — Runbook

## Lane Header

=== CLEAN-ROOM LANE GUARD ===

LANE: IMPLEMENTER

REFERENCE PROJECTS IN SCOPE: none (本 runbook 仅引用 prestudy b06c0srmp 已 cited 的 sub2api / new-api / litellm 证据)。

HARD PROHIBITIONS: 不复制借鉴项目源码;不暴露 settle replay 给非 admin caller;不绕过三证 proof 防重复扣费。

CITATION POLICY: HUAKAI 内部 file:line;参考项目用 prestudy 已 cited 的 `<repo>@<sha>:<file>:<line>`。

=== END CLEAN-ROOM LANE GUARD ===

## §1 目的

把"流式 / eventbus billing handler 失败后,模型内容已发给客户端但 Tx2 settlement 未确认提交 = 钱账丢失"的灰区漏洞堵掉。Settle 失败时把 `eventbus.RequestCompletionEvent` 转 `settlementrecovery.Payload`(保留可 JSON 持久化的 `emit_scheduler_outbox` intent,不再使用不可序列化 callback),enqueue 进 `usage_record_dlq` 新 event_kind `post_delivery_settlement`,worker 后续重调 public `billing.Settler.Settle`。

ErrClaimNotReserving(claim 已 committed)时走三证 proof:claim status='committed' + usage_records 行存在 + billing_events 含 event_type='claim_committed' — 三证齐才视已成功,缺一继续视失败(防止假阳性 idempotent 重复扣费)。

设计依据见 [docs/process/plans/2026-05-24-post-delivery-settle-recovery-synthesis.md](../process/plans/2026-05-24-post-delivery-settle-recovery-synthesis.md)。参考项目对照 (prestudy b06c0srmp):**sub2api / new-api / litellm 三者均无 durable outbox**,HUAKAI 此设计 = 架构升级(durable DLQ)+ 算法升级(三证 proof + max-attempts 闭环)+ 生态升级(Admin DLQ replay UI + worker 链)。

## §2 部署 checklist

- [ ] CI backend-ci 全绿 — go vet + migration round-trip 0-53 + go test -race ./...
- [ ] Migration 0053 `post_delivery_settlement_dlq_kind` 已合到目标分支(0053.up.sql + down.sql 都在 `backend/sql/migrations/`)
- [ ] `dlq.EventKindPostDeliverySettlement` 常量 wire 一致(SQL CHECK + Go const 字符串值都 = `post_delivery_settlement`)
- [ ] `cmd/gateway/wiring.go` `dlqService.Register(EventKindPostDeliverySettlement, settlementHandler.Handle)` 已注册(check `git grep "EventKindPostDeliverySettlement" backend/cmd/gateway/`)
- [ ] `cmd/gateway/routes.go` `ChatHandlerDeps.SettleRecoveryDLQ: d.dlqService` 已 wire
- [ ] BillingPersisterHandler `DLQKind()` 返 `EventKindPostDeliverySettlement` + 实现 `DLQPayload()`
- [ ] Owner 协调 maintenance window(预计停服 < 30s,0053 是纯 ALTER CHECK 不重写数据)

## §3 Migration 步骤

```bash
# 1. 备份(可选,0053 ALTER CHECK 不动数据)
export MIGRATE_DSN="postgres://huakai:<PROD_PW>@<PROD_HOST>:5432/<PROD_DB>?sslmode=require"

# 2. 摘流量
# 3. 跑 0053
migrate -path backend/sql/migrations -database "$MIGRATE_DSN" up 1
# 看到 "53/u post_delivery_settlement_dlq_kind (...ms)" 即成功

# 4. 验:新 CHECK 取值在
psql "$MIGRATE_DSN" -c "
SELECT pg_get_constraintdef(oid)
FROM pg_constraint
WHERE conname='usage_record_dlq_event_kind_check';
"
# 期望:CHECK 取值含 'post_delivery_settlement'

# 5. 恢复流量
```

## §4 Rollback 步骤

```bash
# 仅在 0053 后立即出现严重 incident 时使用
# 注意:down.sql 自带 RAISE EXCEPTION — 如果有 post_delivery_settlement 行,
# down 会失败提示 "cannot rollback 0053: ... drain or quarantine them first"

# 1. 摘流量
# 2. 先查是否有该 kind 行
psql "$MIGRATE_DSN" -c "SELECT count(*) FROM usage_record_dlq WHERE event_kind='post_delivery_settlement';"

# 3a. 如果 count > 0:必须先处理这些行(operator force settle 或 quarantine)
psql "$MIGRATE_DSN" -c "
SELECT id, tenant_id, claim_id, status, replay_attempts, failure_reason
FROM usage_record_dlq
WHERE event_kind='post_delivery_settlement'
ORDER BY failure_at DESC;
"
# 按 §6 incident response 处理后再 rollback

# 3b. 如果 count == 0:正常 rollback
migrate -path backend/sql/migrations -database "$MIGRATE_DSN" down 1

# 4. 部署回退到 commit f91c8ea 之前
# 5. 恢复流量
```

## §5 部署后验证

```bash
# A. 流式请求 + settle 路径正常
curl -X POST https://<gateway>/v1/messages -H "Authorization: Bearer <api-key>" -H "Accept: text/event-stream" -d @stream-sample.json
# 等结算完成,正常时 usage_record_dlq 不应新增 post_delivery_settlement 行

psql "$MIGRATE_DSN" -c "
SELECT count(*) FROM usage_record_dlq
WHERE event_kind='post_delivery_settlement' AND failure_at > NOW() - INTERVAL '10 minutes';
"
# 期望 0(正常请求 settle 成功)

# B. 故意触发 settle 失败(只在 staging 做)
# 把 settler.go 的 DB 配置临时改成不可达, 跑流式请求, 验:
#   - 客户端收到完整流响应(已交付)
#   - 日志含 "settle_failed" log_internal_error
#   - DLQ 表新增 post_delivery_settlement 行
#   - worker 重试后(默认 30s 内)行变 status=delivered 或继续 retry

psql "$MIGRATE_DSN" -c "
SELECT id, status, replay_attempts, replay_failure_reason
FROM usage_record_dlq
WHERE event_kind='post_delivery_settlement'
ORDER BY id DESC LIMIT 5;
"

# C. 三证 proof 验:
# 制造 "claim 已 committed 但 usage_records 缺" 异常状态(staging only),
# 跑 worker 重 settle,验:proof 返 false,DLQ 行继续重试不假阳性成功
```

## §6 Incident 响应

| 信号 | 可能原因 | 应对 |
|---|---|---|
| post_delivery_settlement DLQ count 突增 | 上游 settle 路径 DB 异常 / Tx2 SQL 冲突 / billing_persister handler 故障 | 查 `failure_reason` 列定位 root cause;DLQ worker 会自动重试,不必手动 |
| DLQ 行 replay_attempts 达 max → status='quarantined' | 三证 proof 持续返 false(claim corrupt) 或 DB 持续不可达 | (1) 查 quarantined 行的 `replay_failure_reason`;(2) 用 admin DLQ replay endpoint 手动重放(operator 决定);(3) 实在不行 — 手动 SQL `INSERT INTO usage_records / billing_events` 走 settler.go 同事务模式补账(money path 必须 ops 审计后做) |
| `settle_recovery_dlq_enqueue_failed` slog alert 触发 | DB 完全不可写(双环灰区,Owner D-4 接受) | grafana 抓主 DB 健康 → ops 介入恢复 DB;期间客户端流式响应已发出,settle 数据丢失。后续 audit:从 `billing_ledger_claims` 查 reserve 状态 + 客户端 request_id 反查,人工补账 |
| 三证 proof 返 (false, err) | DB 查询失败(网络抖动) | 自动包到 `replay_failure_reason` 触发重试,无需手动 |
| settlementrecovery.Handler.Settler / Proof nil 启动报错 | wiring 缺(测试 / 异常部署) | `cmd/gateway/wiring.go` 检查 settlementrecovery wire 是否被注释或漏 |
| event_kind=post_delivery_settlement 但 worker 不处理 | dlqService.Register 漏 / dlqWorker 未启动 | 查 `lifecycle.go` startup log 是否含 "settlement recovery handler registered" |

## §7 后续 RR(进 risk register)

| RR | 范围 | 触发条件 |
|---|---|---|
| [RR-W5-008](../10_RISK_REGISTER.md) | DLQ persist 自身失败的 disk spool 兜底(Owner D-4 当前 reject) | 若 grafana `settle_recovery_dlq_enqueue_failed` alert 频繁触发,Owner 重新 review 是否启动 |
| [RR-W5-002](../10_RISK_REGISTER.md) | antigravity refresh fail-closed 接 credentialworker.Scheduler.recordAudit | 独立切片,不与本 P2/P3 重叠 |

## §8 验证记录(本次发布)

| 项 | 状态 | 证据 |
|---|---|---|
| Migration 0053 round-trip | ✅ | 本地 PG15 跑通:up 0-53 → 插 post_delivery_settlement row(bypass FK)→ down RAISE EXCEPTION 'cannot rollback 0053' ✓ → cleanup → down → up;schema_migrations dirty=f |
| dlq EventKind 单测 | ✅ | TestLaneForKind_PostDeliverySettlementIsHigh / TestEventKindPostDeliverySettlement_StringValueMatchesSQLCheck / TestReplicaStatusForKind_PostDeliverySettlementIsNone |
| settlementrecovery 包单测 (13) | ✅ | payload_test (6) + enqueue_test (4) + handler_test (8 — wait 实际 8 减重叠) |
| eventbus + observability 单测 (6) | ✅ | dlq_custom_payload_test (3) + billing_persister_dlq_test (3) |
| gatewayhttp 单测 (5) | ✅ | post_delivery_recovery_test (5) |
| 全量 backend test sweep | (见 commit msg) | `go test -race -count=1 -timeout 5m ./...` |
| 6 commit 切片 | ✅ | fc64a1f(C1) / 4a2c6e7(C2) / f1b7dbd(C3) / 84cbaaf(C4) / f91c8ea(C5) / 本 commit(C6) |
