# 2026-05-24 post-delivery-settle-recovery-codex (落档 Claude 代写)

> Codex sandbox read-only 不能 Write 到 docs/。本文件由 Claude 把 codex bazn7km4v 的 stdout 输出原样落档到 plans/,保留 codex 独立 lane 证据。

| 字段 | 值 |
|---|---|
| Owner directive | "P2/P3: HUAKAI 流式响应 settle 失败后兜底不足;Owner 决策 = 现在就修" |
| Scope | In: delivered-after-settle failure durable recovery, DLQ event kind, worker replay, schema gate, tests. Out: payment ledger redesign, auth/quota core schema changes, local disk spool. |
| Success criteria | 已交付流式响应的 settle 失败必须持久化 `post_delivery_settlement` DLQ;worker 可重放 `Settler.Settle`;重复/已提交场景不重复扣费;DB CHECK 接受新 event_kind;至少 6 个判别性测试覆盖 mutation 红体。 |
| Time estimate | 0.5d plan + schema gate;1-1.5d implementation/tests;PG integration 0.5d。 |
| Blast radius | Gateway streaming hot path、eventbus billing handler DLQ、DLQ worker HIGH lane、billing settlement replay。 |
| Clean-room | 不读 `~/refs`;仅用 HUAKAI 内部代码和既有 source-verified docs。 |
| Planner | lane=planner-codex, agent=codex GPT-5, UTC=2026-05-24 |

## 1. 架构决策

**推荐 EventKind:** `post_delivery_settlement`

理由:这不是普通 `usage_record` 失败。它代表"模型内容已交付,但 Tx2 settlement 未确认提交"的完整恢复意图,应 HIGH lane,source_table=`billing_ledger_claims`,source_id=`claim_id`,idempotency_key=`post_delivery_settlement:<tenant_id>:<claim_id>:<request_id>`。

**谁 enqueue:**
- 主入口放在 `backend/internal/gatewayhttp/chat_completions_billing.go:159-179` 的 `settleCompletion` 周边,但必须显式传入 `postDelivery=true`,避免非流式 pre-delivery 500 后仍异步扣费。
- `backend/internal/gatewayhttp/chat_completions_stream.go:247-251` 调用 post-delivery recovery 模式。
- `backend/internal/observability/billing_persister_handler.go:56-83` 的 eventbus handler failure DLQ 改为同一 event kind + 可重放 payload。
- 不放 `settler.go`:`backend/internal/billing/settler.go:77-251` 无法知道客户端是否已收到内容,且会污染 pre-delivery 失败语义。

**Worker handler:**
- 新包:`backend/internal/settlementrecovery/`
- handler 逻辑:decode sanitized payload -> 重调 public `billing.Settler.Settle(ctx, req)`,不要重写底层 insert/update SQL。
- 加 idempotent proof:若 `Settler.Settle` 返回 `billing.ErrClaimNotReserving`,只在 DB 证明 claim 已 committed 且同 tenant/claim 有 `usage_records` + `billing_events(claim_committed)` 时返回 nil;否则继续失败进入 retry/operator_review。
- payload 不直接 JSON marshal `billing.SettleRequest`,因为 `SettleRequest.OutboxEmitter func() bool` 不可 JSON 化。定义 DTO,只保存 ClaimID、TenantID、AccountID、AcquisitionToken、ActualCost string、Draft、StreamAttempt、AuditRequestID、SnapshotVersion 等可重建字段。

## 2. DB Schema Gate

Owner 需批准 0053 migration,仅更新 CHECK,不新建表:

```sql
-- backend/sql/migrations/0053_post_delivery_settlement_dlq_kind.up.sql
BEGIN;
ALTER TABLE usage_record_dlq
    DROP CONSTRAINT IF EXISTS usage_record_dlq_event_kind_check,
    ADD CONSTRAINT usage_record_dlq_event_kind_check
        CHECK (event_kind IN
            ('usage_record', 'billing_event_replica', 'audit_event_replica',
             'audit_mismatch_refund', 'account_health', 'metrics',
             'audit_ledger_entry', 'post_delivery_settlement'));
COMMIT;
```

```sql
-- backend/sql/migrations/0053_post_delivery_settlement_dlq_kind.down.sql
BEGIN;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM usage_record_dlq WHERE event_kind = 'post_delivery_settlement') THEN
        RAISE EXCEPTION 'cannot rollback 0053: post_delivery_settlement DLQ rows exist';
    END IF;
END $$;
ALTER TABLE usage_record_dlq
    DROP CONSTRAINT IF EXISTS usage_record_dlq_event_kind_check,
    ADD CONSTRAINT usage_record_dlq_event_kind_check
        CHECK (event_kind IN
            ('usage_record', 'billing_event_replica', 'audit_event_replica',
             'audit_mismatch_refund', 'account_health', 'metrics',
             'audit_ledger_entry'));
COMMIT;
```

## 3. 改动点

| File | Line | Change |
|---|---:|---|
| `backend/internal/dlq/types.go` | 14-20, 98-105 | Add `EventKindPostDeliverySettlement`; map to `LaneHigh`; replica status remains `none`. |
| `backend/sql/migrations/0053_post_delivery_settlement_dlq_kind.*.sql` | new | Add/drop CHECK value with rollback guard. |
| `backend/internal/settlementrecovery/payload.go` | new | DTO, `FromEvent`, `ToSettleRequest`, JSON validation; no funcs/secrets. |
| `backend/internal/settlementrecovery/enqueue.go` | new | Build `dlq.Event` envelope with stable idempotency key and source metadata. |
| `backend/internal/settlementrecovery/handler.go` | new | DLQ worker handler calls `Settler.Settle`; idempotent proof on already-committed. |
| `backend/internal/eventbus/types.go` | 242-260 | Allow handler-supplied DLQ payload via optional interface; fallback keeps existing payload. |
| `backend/internal/observability/billing_persister_handler.go` | 56-83 | `DLQKind()` returns new kind; implement custom payload for billing persister failures. |
| `backend/internal/gatewayhttp/chat_completions_handler.go` | 45-51 | Add generic `SettleRecoveryDLQ` sink; do not reuse audit-only name. |
| `backend/internal/gatewayhttp/chat_completions_billing.go` | 159-179 | Add post-delivery recovery option around direct settle / bus emit errors. |
| `backend/internal/gatewayhttp/chat_completions_stream.go` | 247-251 | Call settle with post-delivery recovery enabled; keep response path non-blocking. |
| `backend/cmd/gateway/routes.go` | 92-110 | Wire `SettleRecoveryDLQ: d.dlqService`. |
| `backend/cmd/gateway/lifecycle.go` | 258-260 | Register `EventKindPostDeliverySettlement` handler. |

## 4. D 决策点

| ID | Decision | Options | Recommend | 参考项目对照 |
|---|---|---|---|---|
| D1 | EventKind 粒度 | A `post_delivery_settlement`; B reuse `usage_record`; C `billing_event_replica` | A | Sub2API usage record 是 detached/best-effort,失败只 log/fallback,不是完整 settle recovery(`docs/decompositions/sub2api/observability-source-verified.md:162`, `:167-183`, `:295`)。New-api missing/zero usage 可 log 后零结算,HUAKAI 要避免(`docs/decompositions/new-api/cache-billing-reasoning-source-verified.md:25-29`, `:112-129`)。 |
| D2 | Enqueue site | A `settleCompletion` postDelivery option + eventbus custom payload;B stream.go only;C settler.go catch-all | A | B 漏 bus/direct fallback 统一性;C 不知道是否已交付。Sub2API 的 production tradeoff 是钱正确优先、usage 可丢(`sub2api/observability-source-verified.md:196-204`),HUAKAI 这里要把"已交付但未 settle"升级为 durable intent。 |
| D3 | Worker replay | A re-call public `Settler.Settle` + committed proof;B 直接重写底层 SQL;C operator-only manual | A | New-api 的 partial settlement/log 风险显示不能靠人工日志兜底(`new-api/cache-billing-reasoning-source-verified.md:113-116`, `:142-152`)。A 保持 HUAKAI Tx2 单入口。 |
| D4 | DLQ persist 也失败 | A metric+alert+operator_review residual;B local disk spool;C panic/fail process | A for this slice; B separate Owner gate | `eventbus/bus.go:287-295` 当前只 log。若主 DB 不可写,任何 DB-backed DLQ 都不能保证 durable;local disk spool 是新可靠性子系统,需单独 Owner 决策。 |
| D5 | Already committed 判定 | A claim committed + usage + billing_event 三证;B claim committed only;C any `ErrClaimNotReserving` success | A | LiteLLM recovery 常见 TTL/状态恢复不等于健康证明(`docs/decompositions/litellm/cooldown-retry-hierarchy-source-verified.md:65-67`)。HUAKAI money path 必须证据闭环。 |

## 5. 测试矩阵

| Test | File | Discriminating assertion / mutation 红体 |
|---|---|---|
| Stream delivered + `Settler.Settle` fails enqueues recovery | `chat_completions_stream_test.go` | Assert response body/trailer delivered and exactly one `post_delivery_settlement`; mutation: remove enqueue -> 0 rows/events. |
| Stream settle success does not enqueue | `chat_completions_stream_test.go` | Assert DLQ empty; mutation: unconditional enqueue -> test fails. |
| Non-streaming pre-delivery settle failure does not enqueue | `chat_completions_billing_test.go` | Assert 500 and DLQ empty; mutation: enqueue on all settle errors -> test fails. |
| Nil bus/direct settle path is covered | `chat_completions_stream_test.go` or billing helper unit | `CompletionBus=nil`, failing settler, delivered stream -> recovery DLQ exists; mutation: only eventbus DLQ path -> fails. |
| Eventbus billing handler failure produces replayable payload | `eventbus`/`observability` test | Assert new kind and payload decodes to SettleRequest DTO; mutation: leave `usage_record` kind or generic payload -> decode/assert fails. |
| DLQ handler calls `Settler.Settle` | `settlementrecovery/handler_test.go` | Spy settler receives reconstructed TenantID/ClaimID/AuditRequestID/StreamAttempt; mutation: mark delivered without settle -> call count 0. |
| Already committed proof is strict | `settlementrecovery/handler_test.go` | `ErrClaimNotReserving` + proof true => nil; proof false => error; mutation: treat all not-reserving as success -> false proof fails. |
| Migration CHECK accepts new kind and down blocks existing rows | migration integration | Insert `post_delivery_settlement` succeeds after up; down raises if row exists; mutation: omit CHECK value -> insert fails. |

## 6. Commit Slices

1. `schema/dlq-kind`: migration 0053 + `dlq.EventKindPostDeliverySettlement` + retry lane tests.
2. `settlementrecovery-package`: DTO, enqueue builder, handler, proof interface, unit tests.
3. `eventbus-billing-dlq`: custom eventbus DLQ payload + BillingPersister DLQKind tests.
4. `gateway-post-delivery-enqueue`: ChatHandlerDeps wiring, `settleCompletion` postDelivery option, stream/direct tests.
5. `runtime-registration`: lifecycle/register handler, routes wiring, admin DLQ list/replay smoke.
6. `integration-docs`: PG integration test, acceptance matrix/risk register update, codex review.

## 7. Risks

| Severity | Risk | Mitigation |
|---|---|---|
| HIGH | Duplicate charge on replay | Public `Settler.Settle` + strict committed proof; no lower-level SQL replay. |
| HIGH | DB unavailable means settle and DLQ both fail | Record residual risk; metric/alert now; local disk spool only with Owner approval. |
| HIGH | Schema CHECK blocks production inserts if migration missing | 0053 schema gate before app deploy. |
| MED | JSON payload silently loses fields | DTO round-trip tests; never marshal `SettleRequest` directly. |
| MED | Eventbus generic DLQ creates unreplayable rows | BillingPersister custom payload + new kind test. |
| MED | Receipt hook missing when proof says already committed | Record follow-up: proof path may need receipt repair check; not duplicate settle. |
| LOW | Admin UI label unknown | Existing DLQ filter is event_kind based; add display label later if UI complains. |

## 8. 与参考项目差异

参考项目侧按既有 prestudy/docs:Sub2API 把 Usage Record 放 best-effort detached path,失败主要是 log/fallback;New-api 对 missing/zero usage 和 partial settlement 有 log/fail-soft 风险;LiteLLM 的 recovery 更偏 routing/cooldown operational state,不是 money-path durable settle proof。

HUAKAI 这次是三维升级,但主维度是**架构升级**:把"已交付但未 settle"变成 durable DLQ intent + HIGH lane worker。其次是**算法升级**:worker 以 Tx2 public Settler 重放,并用三证 proof 区分已提交和未提交。也有**生态升级**:复用 Admin DLQ replay / operator review / observability worker 链,避免 log+人工成为唯一兜底。

Source files read:
- `backend/internal/gatewayhttp/chat_completions_stream.go`
- `backend/internal/gatewayhttp/chat_completions_billing.go`
- `backend/internal/gatewayhttp/chat_completions_handler.go`
- `backend/internal/billing/settler.go`
- `backend/internal/billing/billing.go`
- `backend/internal/dlq/types.go`
- `backend/internal/dlq/service.go`
- `backend/internal/dlq/store.go`
- `backend/internal/eventbus/bus.go`
- `backend/internal/eventbus/types.go`
- `backend/internal/observability/billing_persister_handler.go`
- `backend/cmd/gateway/lifecycle.go`
- `backend/cmd/gateway/routes.go`
- `backend/sql/migrations/0015_obs_dlq_extend.up.sql`
- `backend/sql/migrations/0050_dlq_audit_ledger_entry_kind.up.sql`
- `docs/decompositions/sub2api/observability-source-verified.md`
- `docs/decompositions/new-api/cache-billing-reasoning-source-verified.md`
- `docs/decompositions/litellm/cooldown-retry-hierarchy-source-verified.md`

Lane: planner-codex
Agent: codex GPT-5
UTC timestamp: 2026-05-24T04:08:00Z
