# 2026-05-15 F-OBS-005 DLQ + priority + dual-write - Codex independent plan

| Field | Value |
| --- | --- |
| Owner directive | "Write CODEX plan for HUAKAI F-OBS-005 (DLQ + priority + dual-write) - Go backend feature." |
| Authorization | Owner 2026-05-15 全 session 授权; Go backend 临时解冻 |
| Lane | SPECIFIER, HUAKAI-internal only |
| Independence | 未读取 Claude 版本; 只检查目标 Codex plan 是否已存在 |
| Execution boundary | 本文件只写计划; 不实现, 不迁移, 不 commit |

## scope

In:

- F-OBS-005 Go backend plan: failed observability event DLQ, priority lanes, primary plus replica dual-write delivery.
- Billing/audit observability event durability around current `billing.Settler` Tx2 path.
- Durable worker/runtime plan for replay, lane selection, backoff, idempotency, operator replay.
- Admin API impact for DLQ visibility and replay.
- Schema impact assessment, but implementation必须在 Owner 批准 synthesized plan 后另起。

Out:

- 不改 `LICENSE`, 不接触 secrets, 不做 production deployment。
- 不改变 quota enforcement、auth core、payment logic。
- 不在本计划中读取任何非 MIT reference source。
- 不把 upstream 项目的 queue/schema/handler 结构搬进 HUAKAI。
- 不把 replica 读路径切成默认用户读路径; 默认 read-after-write 仍从 primary PostgreSQL 读。

Success criteria:

- 每个 successful Tx2 primary billing/audit row 都有同一事务内持久化的 delivery/replica intent。
- 任何 async observability delivery 失败都进入可查询、可重放、幂等保护的 DLQ 状态。
- hot path request events 不被 background reconciliation 长时间阻塞。
- replica lag 可观测, 有 alert 阈值和 operator recovery path。
- 所有 replay 不会重复提交账务、不跨 tenant、不破坏 immutable ledger/usage invariant。

## file-by-file impact (Go backend/)

- `backend/sql/migrations/0015_observability_delivery_control.up.sql` / `.down.sql`
  - 需要新增迁移, 高风险, 需 Owner sign-off。
  - 建议新增通用 `observability_delivery_events` 表, 不重命名现有 `usage_record_dlq`。
  - 字段方向: `tenant_id`, `event_kind`, `lane`, `status`, `idempotency_key`, `source_table`, `source_id`, `payload`, `attempts`, `max_attempts`, `next_attempt_at`, `locked_by`, `locked_until`, `last_error`, `dlq_at`, `replayed_at`, `primary_committed_at`, `replica_committed_at`, `replica_target`。
  - 索引方向: pending claim (`status`, `lane`, `next_attempt_at`, `id`), tenant DLQ list, unique idempotency (`tenant_id`, `event_kind`, `idempotency_key`, `replica_target`)。

- `backend/sql/queries/observability_delivery.sql`
  - 新增 sqlc queries: enqueue, claim with `FOR UPDATE SKIP LOCKED`, ack, nack/backoff, mark DLQ, manual replay claim, list DLQ, count lag/depth。
  - 所有 query 必须 tenant-scoped, manual replay 还要带 actor audit metadata。

- `backend/internal/db/*.go`
  - `sqlc generate` 后更新 generated models/querier。
  - 只接受由 SQL 生成的机械变更; 不手写 generated code。

- `backend/internal/obsqueue/types.go`
  - 新包, 避免破坏 `internal/obs` 当前 SELECT-only 语义。
  - 定义 `EventKind`, `Lane`, `Status`, `DeliveryEvent`, `ReplayCommand`, `ReplicaSink`。

- `backend/internal/obsqueue/store.go`
  - SQL-backed store: enqueue, claim, lease renewal, ack, fail, DLQ transition。
  - Store 接受 `pgx.Tx` 版本, 让 `billing.Settler` 能在 Tx2 同事务写 delivery intent。

- `backend/internal/obsqueue/retry.go`
  - backoff policy, jitter, max-attempt cutoff, 15-minute timeout/demotion rule。
  - 使用 fake clock 便于 unit tests。

- `backend/internal/obsqueue/worker.go`
  - lane-aware worker loop。
  - 支持 graceful shutdown, lease expiry recovery, bounded concurrency, no in-memory-only queue。

- `backend/internal/obsqueue/replica.go`
  - `ReplicaSink` abstraction。
  - v1 可先实现 PostgreSQL replica DSN sink; 如 Owner 不批准第二 DSN, 先落 local durable replica-intent table and status, 不宣称跨库 DR。

- `backend/internal/billing/billing.go`
  - 给 `Settler` 增加 narrow interface, 例如 `ObservabilityPublisher` / `DeliveryPublisher`。
  - 不把 obsqueue implementation 泄入业务接口; billing 只知道 "enqueue durable delivery intent"。

- `backend/internal/billing/settler.go`
  - Tx2 内写 primary `billing_events` 后, 同事务 enqueue replica/delivery intent。
  - `usage_records` 写失败路径需要重排: primary billing event 不应因 rich analytics write failure 全部丢失。建议先把 usage payload 封装为 delivery event, 或在 Tx2 内写 `usage_record_dlq`/`observability_delivery_events` fallback row。
  - Primary billing/audit write failure 仍 fail closed; 不允许成功响应绕过 ledger/audit。

- `backend/internal/billing/settler_integration_test.go`
  - 加 Tx2 atomicity + DLQ fallback + replica intent integration cases。
  - 保留现有 AT-OBS-004 等断言, 避免回归。

- `backend/internal/obs/obs.go` / `backend/internal/obs/repository.go`
  - 不建议加入 replay writer。
  - 可只扩 read-side `ListDLQ`/`CountDLQ` 视图; replay writer 放在 `obsqueue` 或 `gatewayhttp` deps。

- `backend/internal/gatewayhttp/admin_dlq_handler.go`
  - 替换当前 501 replay route。
  - 如果 Owner 批准 API 扩展, 增加 `GET /admin/v1/usage-record-dlq` 或 generic `GET /admin/v1/observability-dlq`。
  - `POST /admin/v1/usage-record-dlq/{id}/replay` 必须 platform_admin 起步, tenant_operator 是否允许另作 Owner 决策。

- `backend/internal/gatewayhttp/admin_observability_handler.go`
  - 如只加 replay, 影响较小。
  - 如加 list endpoint, 扩展 `AdminObservabilityStore` 或拆出 `AdminDLQStore` 以避免接口过胖。

- `backend/cmd/gateway/main.go`
  - Wire `obsqueue.Store`, optional replica sink, worker lifecycle。
  - 用真实 handler 替换 `notImplemented("F-OBS-001 DLQ replay")`。
  - worker start/stop 要纳入 gateway shutdown; 不允许 goroutine 泄漏。

- `backend/internal/config/config.go`
  - 新增 env config: worker enable, per-lane concurrency, max attempts, base/max backoff, lag alert seconds, optional replica DSN。
  - 默认值要保守: primary path enabled, worker can be disabled for tests, replica strict mode disabled unless Owner 明确。

- `backend/config.example.yaml`
  - 更新示例 config, 但标明 YAML 仍是 illustrative; runtime 当前来自 env。

- `backend/sqlc.yaml`
  - 大概率不需要改; 只有新增 enum/name override 时才动。

## DLQ retry strategy (exponential backoff? max attempts? operator dashboard?)

建议策略:

- Retry 使用 exponential backoff + jitter:
  - base: 1s。
  - cap: 5m。
  - jitter: +/-20%, tests 中由 deterministic random/fake clock 固定。
  - `next_attempt_at = now + min(base * 2^attempts, cap) + jitter`。
- Auto retry cutoff:
  - 默认 `max_attempts = 10`。
  - 任一 event 从 `created_at` 起超过 15m 仍未 delivered, status 转 `dlq` 或 lane 降到 `background` 并触发 alert; billing/audit critical replica intent 不静默降级, 必须 operator-visible。
  - manual replay 不计入 auto retry 上限, 但每次写 audit event and attempt row。
- Error class:
  - transient: connection timeout, serialization failure, replica unavailable -> retry。
  - permanent: payload validation, tenant mismatch, idempotency conflict -> DLQ immediately, no hot loop。
  - conflict: same idempotency delivered -> ack idempotently, not DLQ。
- Operator dashboard/API:
  - Minimum: DLQ depth by `tenant_id`, `event_kind`, `lane`, oldest age, `attempts`, `last_error`, `replica_lag_seconds`。
  - Actions: replay, quarantine/hold, mark resolved only if event already delivered elsewhere。
  - Current OpenAPI only has replay route; list endpoint is a decision point below。

## priority lanes (how many tiers? what triggers tier assignment?)

建议三层 lane, 加一个 manual override flag:

1. `critical`
   - Trigger: request hot path Tx2 emits billing/audit delivery or replica intent; committed claim audit; usage record recovery needed to make a just-finished request visible。
   - Rule: highest worker share, short lease, alert on lag > 60s。

2. `standard`
   - Trigger: normal near-real-time observability fanout, admin usage visibility, scheduler invalidation side effects that are important but not money-source-of-truth。
   - Rule: normal worker share, lag alert same threshold unless operator tunes。

3. `background`
   - Trigger: reconciliation, old DLQ retries after 15m, cold analytics/export style events, bulk replay。
   - Rule: bounded concurrency, cannot starve critical/standard。

Manual override:

- `promote_once=true` lets platform admin replay one DLQ event in `critical` lane for incident recovery。
- Override must be audited and rate-limited to prevent a bulk replay from starving live traffic。

Scheduling:

- Use weighted fair dequeue, not pure strict priority forever。
- Initial weights: `critical:standard:background = 8:3:1`。
- Each worker claim uses `FOR UPDATE SKIP LOCKED` and a lease; crashed worker recovery happens through `locked_until < now()`。
- Aging rule: a `standard` event older than alert threshold can temporarily receive higher claim priority, but never ahead of currently due `critical` events。

## dual-write coherence (read-after-write guarantee? acceptable lag?)

Primary coherence:

- Primary source of truth remains PostgreSQL primary tables: `billing_ledger_claims`, `billing_events`, `usage_records` when present。
- Read-after-write guarantee is primary-only: after Tx2 commit, admin/billing reads from primary must see the committed row immediately。
- Replica is not used for serving read-after-write unless the row has `replica_committed_at` and the caller explicitly asks for replica diagnostics。

Dual-write model:

- v1 recommended default is asymmetric durable intent:
  - Tx2 commits primary billing/audit rows and `observability_delivery_events` replica intent in the same transaction。
  - Background worker writes to replica sink idempotently。
  - If replica write fails, primary row stays authoritative and replica intent retries/DLQs with visible lag。
- This avoids remote replica outage taking down the hot path by default。
- It does not provide zero-RPO cross-database disaster recovery if the primary database is destroyed before the replica intent drains. If Owner requires that, choose strict synchronous replica mode below。

Optional strict mode:

- `HUAKAI_OBS_REPLICA_STRICT=true` means Tx2 waits for primary + replica write before commit/response, with a short timeout。
- On replica failure, Settle fails closed and the request cannot be treated as fully settled。
- This gives stronger durability but increases latency and availability blast radius; it needs explicit Owner sign-off。

Acceptable lag:

- Default target: P50 < 2s, P95 < 10s under normal load。
- Alert: oldest undelivered `critical` event > 60s。
- DLQ/operator action: undelivered event > 15m or attempts >= 10。

Coherence invariants:

- No committed primary billing/audit row without durable replica intent。
- No replica event without stable idempotency key。
- Replay is idempotent: delivered replica write + duplicate replay returns ack, not duplicate ledger mutation。
- Replica divergence is surfaced as state, never hidden behind "success" UI。

## test plan

Unit tests:

- `obsqueue/retry`: backoff schedule, cap, jitter bounds, max-attempt cutoff, 15m demotion。
- `obsqueue/lane`: lane assignment for hot path, reconciliation, manual replay; weighted fair behavior; no starvation。
- `obsqueue/types`: idempotency key construction rejects missing tenant/event kind/source id。
- `gatewayhttp`: DLQ replay auth, bad id, forbidden tenant/operator, malformed cursor if list endpoint is approved。

SQL/integration tests (`integration_pg`):

- Enqueue and claim uses tenant scope and `FOR UPDATE SKIP LOCKED`; concurrent workers do not double-claim。
- Worker crash lease expiry makes the event claimable again。
- `ack` marks delivered once; duplicate ack/replay is idempotent。
- transient failure increments attempts and schedules `next_attempt_at`。
- permanent failure goes directly to DLQ。
- manual replay of a DLQ row writes attempt/audit metadata and does not bypass idempotency。
- Tx2 commit writes primary billing_event + replica intent atomically。
- Tx2 rollback leaves neither primary event nor replica intent。
- Usage record write failure path preserves primary billing_event and durable DLQ/delivery row, instead of losing both。
- Replica sink unavailable: primary read-after-write passes, replica state pending/failed is visible, lag metric increments。
- Strict replica mode: replica failure causes fail-closed behavior。
- Cross-tenant replay/list attempts return not found/forbidden without leaking row existence。

Regression/smoke:

- Existing `TestAT_OBS_004_AtomicFiveEffect`, abort path, token mismatch, snapshot_version tests must still pass。
- Gateway route smoke verifies `/admin/v1/usage-record-dlq/{id}/replay` is no longer 501 after implementation。

Expected commands after implementation:

- `cd backend && sqlc generate`
- `cd backend && go test ./internal/obsqueue ./internal/gatewayhttp ./internal/config`
- `cd backend && ./scripts/run-go-test.sh ./internal/billing/...`
- `cd backend && go test -tags integration_pg ./internal/billing/... ./internal/obsqueue/...`
- `cd backend && go test ./cmd/gateway/...`

## time estimate

- Planning/reconciliation after Claude-Codex diff: 1-2h。
- Migration + sqlc + store: 4-6h。
- Worker + retry/lane logic: 4-6h。
- Billing Tx2 integration: 4-8h, because this touches money-grade settlement ordering。
- Admin replay/list API: 3-5h。
- Tests + fix cycle: 6-10h。
- Total implementation estimate: 2-3 focused engineering days, assuming PostgreSQL integration test environment is available。

## blast radius (data layer - careful)

Severity: HIGH because this touches schema, Tx2 settlement, billing/audit durability, and replay.

Main risks:

- Double billing or duplicate audit rows if replay idempotency is wrong。
- Silent billing/audit loss if delivery intent is not written in the same transaction as primary event。
- Hot path latency regression if strict replica mode is enabled without budget。
- Replica divergence hidden from operators。
- Worker lock contention on `observability_delivery_events` under high QPS。
- Cross-tenant DLQ visibility leak in admin APIs。
- Manual replay causing ordering surprises or replaying obsolete payloads。
- Migration mistake on existing `usage_record_dlq` or FK constraints。

Mitigations:

- Keep primary ledger tables authoritative。
- Add new table instead of renaming existing `usage_record_dlq`。
- Use additive migration only; no destructive backfill in first patch。
- Require idempotency unique index and delivery attempt audit trail。
- Default to async durable intent, not synchronous remote write。
- Gate strict replica mode behind explicit config and Owner approval。
- Integration tests must cover rollback, duplicate replay, tenant isolation, and worker crash recovery。

## decision points (Owner sign-off; migration needed?)

1. Migration needed: yes.
   - Current `usage_record_dlq` lacks generic event kinds, lane, next retry, lease, replica status, and idempotency fields for F-OBS-005。
   - Because database schema is high-risk, implementation must wait for Owner approval of the synthesized plan。

2. Dual-write mode:
   - Recommended default: primary commit + durable replica intent in Tx2, async replica worker。
   - Stronger option: strict synchronous replica write, fail closed on replica failure。
   - Owner must choose whether v1 optimizes availability or zero-RPO durability。

3. Replica target:
   - Separate PostgreSQL DSN, same-cluster audit replica table, object-store append log, or staged local-only intent。
   - This affects config, tests, and operator runbook。

4. Admin surface:
   - Existing OpenAPI has replay only。
   - Operator dashboard really needs list/depth/lag endpoint. Owner should approve adding `GET /admin/v1/usage-record-dlq` or a generic `GET /admin/v1/observability-dlq`。

5. Replay authorization:
   - Default platform_admin only。
   - SaaS tenant_operator scoped replay can be added later with rate limit and audit, but should not be default for money-path events。

6. Backoff constants:
   - Proposed: base 1s, cap 5m, max attempts 10, 15m DLQ threshold。
   - Owner can tune before implementation; these become operator config defaults。

7. Billing Tx2 ordering:
   - Current code comments admit usage insert failure rolls back Tx2 and loses billing_event。
   - F-OBS-005 should fix this, but that is a money-path behavior change. Owner should approve that this is in-scope for this slice。

## clean-room: references by reputation only

- 本计划只读取 HUAKAI 内部 docs/backend。
- 未读取 `~/refs/` 或任何非 MIT reference project source。
- 未引用 upstream function names, struct fields, schemas, comments, file layout, or algorithms。
- 对 F-OBS-005 的 reference 背景只采用 HUAKAI 内部 `03_FEATURE_PARITY_MATRIX.md` 的 reputation-level roadmap row; 本计划不新增任何 reference-project capability/mechanism claim。
- 因未做 upstream 行为断言, CLAUDE.md #12 的 source-must-read trigger 未被触发; 若后续要写 "Project X implements Y by Z", 必须另起 clean-room guarded source read。

## sources read

- `.agents/skills/pm-orchestrator/SKILL.md`
- `CLAUDE.md`
- `docs/RULES.md`
- `docs/01_PROJECT_BRIEF.md`
- `docs/02_HUAKAI_FUSION_ARCHITECTURE.md`
- `docs/03_FEATURE_PARITY_MATRIX.md`
- `docs/10_RISK_REGISTER.md`
- `docs/12_AGENT_WORKFLOW.md`
- `docs/15_RELEASE_GATES.md`
- `docs/specs/observability-billing.md`
- `docs/specs/api-contract.md`
- `docs/openapi/openapi.yaml`
- `docs/schema/observability-billing.sql`
- `docs/schema/pool-routing.sql`
- `backend/config.example.yaml`
- `backend/internal/config/config.go`
- `backend/cmd/gateway/main.go`
- `backend/sql/migrations/0001_pool_routing.up.sql`
- `backend/sql/migrations/0002_observability_billing.up.sql`
- `backend/sql/queries/billing_settle.sql`
- `backend/sql/queries/observability.sql`
- `backend/sql/queries/obs_queries.sql`
- `backend/internal/billing/billing.go`
- `backend/internal/billing/settler.go`
- `backend/internal/billing/settler_integration_test.go`
- `backend/internal/obs/obs.go`
- `backend/internal/obs/repository.go`
- `backend/internal/gatewayhttp/admin_observability_handler.go`
- `backend/internal/gatewayhttp/admin_observability_helpers.go`
- `backend/internal/gatewayhttp/admin_observability_handler_test.go`
- `backend/internal/db/models.go`
- `backend/internal/db/querier.go` (searched)
- `backend/internal/db/billing_settle.sql.go` (searched)

Source files read: HUAKAI-internal files listed above; no reference-project source.
Lane: specifier
Agent: Codex GPT-5
UTC timestamp: 2026-05-15T13:32:30Z
