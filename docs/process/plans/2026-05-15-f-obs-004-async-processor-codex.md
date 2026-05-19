# 2026-05-15 F-OBS-004 async processor chain Codex plan

| Field | Value |
| --- | --- |
| Owner directive | "Write CODEX plan for HUAKAI F-OBS-004 (async processor chain) — Go backend feature." |
| Authorization | Owner 2026-05-15 全 session 授权；Go backend 临时解冻 |
| Lane | SPECIFIER |
| Independence | Claude is writing a separate plan; this file was drafted without reading any Claude version. |
| Clean-room state | HUAKAI-internal source/docs only. No sub2api/new-api/portkey/helicone/litellm/all-api-hub/envoy-ai-gateway source read. |
| Timestamp | 2026-05-15T13:29:38Z |

## scope

F-OBS-004 要把 request-completion event 变成一条可观测、可重放、可限流的 async processor chain，但不能破坏 F-OBS-001 的 money-grade 结算 invariant。

本计划范围：

- Go backend 设计计划，不实施、不提交。
- 事件入口：reverse-proxy request completion 后生成 `RequestCompletionEvent`。
- 处理链：按 HUAKAI spec role 命名 14 个 handler 槽位，至少覆盖 billing -> audit -> usage/log/body/alert/export side effects。
- 运行时：支持 priority lanes、batch drain window、handler-level idempotency、failure to DLQ。
- F-OBS-005 集成：priority queue、15 minute timeout demotion、DLQ replay、主/备队列非对称双写的 Go 接口与测试计划。
- 必须保留 F-OBS-001 约束：成功上游响应不能因为 bounded worker queue overflow 而绕过 durable settlement/audit。`observability-billing.md` 明确禁止在成功上游响应和 Tx2 commit 之间放置会丢消息的 bounded worker pool（docs/specs/observability-billing.md:45, docs/specs/observability-billing.md:144-147）。

不在本计划直接实施：

- 不改 `LICENSE`。
- 不读取任何参考项目源码。
- 不写业务实现代码。
- 不直接新增 DB migration；但会列出 F-OBS-004/005 需要的 schema decision points。数据库结构属于高风险范围，执行前需要 Owner 对 synthesized plan 明确确认。
- 不声称完全实现 14-handler parity；只定义 HUAKAI 自研处理链形态和验收路径。

成功标准：

- hot path 从直接调用分散副作用，改成统一 `RequestCompletionEvent` 入口。
- money-grade prefix handler 不被普通 async queue 弱化：billing settlement + audit durability 要么 inline 完成，要么完成 durable handoff 且 fail closed。
- 每个 handler 有稳定 idempotency key，重复事件不会重复扣费、重复写审计或重复 cold archive。
- F-OBS-005 的 priority/DLQ/replay 不是后补旁路，而是 processor runtime 的一等控制面。
- 测试覆盖 handler order、failure isolation、priority starvation protection、DLQ replay idempotency、hot path no-drop invariant。

## file-by-file impact (Go files in backend/; grep first)

grep/read 结论：

- `rg -n "F-OBS-004|F-OBS-005|dead-letter|DLQ|priority queue|usage-record-dlq|scheduler_outbox|outbox|async" docs/03_FEATURE_PARITY_MATRIX.md docs/specs docs/schema backend --glob '!docs/decompositions/**' --glob '!backend/internal/proto/fixtures/**'` 显示 F-OBS-004/005 均为 Phase 4.5 Mandatory Roadmap，并要求与 F-OBS-001 outbox/DLQ 共用 consumer runtime（docs/03_FEATURE_PARITY_MATRIX.md:115-116）。
- `rg --files -g '*.go' backend` 显示当前 backend 主要触点在 `internal/billing`、`internal/gatewayhttp`、`internal/obs`、`internal/db`、`cmd/gateway`。
- 当前 `DefaultSettler` 明确注释：Usage Record write failure 的 DLQ + async retry path deferred to Phase 4.5（backend/internal/billing/settler.go:78-83）。
- 当前 HTTP handler 在 non-streaming 和 streaming 两条路径都直接调用 `d.Settler.Settle`（backend/internal/gatewayhttp/chat_completions_handler.go:449-466, backend/internal/gatewayhttp/chat_completions_handler.go:533-553）。
- 当前 DLQ replay admin route 仍是 `notImplemented`（backend/cmd/gateway/main.go:475-479）。

计划中的 Go 文件影响：

| File | Impact | Risk |
| --- | --- | --- |
| `backend/internal/asyncprocessor/types.go` | 新增 `RequestCompletionEvent`、`HandlerResult`、`Priority`、`HandlerID`、`EventStatus`、`DLQReason`。事件字段只引用现有 claim/account/acquisition/draft/snapshot 数据，不复制 upstream identifier。 | Medium: new package, no hot-path mutation until wired. |
| `backend/internal/asyncprocessor/chain.go` | 新增 chain executor：ordered handlers、per-handler timeout、batch drain window、idempotency key contract、failure classification。 | Medium: core control flow; must be heavily unit-tested. |
| `backend/internal/asyncprocessor/runtime.go` | 新增 worker runtime：priority dequeue、worker lifecycle、graceful shutdown、metrics hooks。第一版建议 single-process runtime，避免分布式 claim 复杂度；Postgres queue 用 `SKIP LOCKED` 时再多 worker。 | Medium/High if DB-backed. |
| `backend/internal/asyncprocessor/queue.go` | 抽象 queue interface：`EnqueuePrimary`、`MirrorBackup`、`DequeueBatch`、`AckHandler`、`Requeue`。先写 interface + memory fake；DB implementation 等 schema 确认。 | Medium. |
| `backend/internal/asyncprocessor/dlq.go` | 抽象 DLQ writer/replayer；F-OBS-005 需要 generic async DLQ，不应继续把所有 handler failure 塞进 `usage_record_dlq`。 | Medium; DB schema decision required. |
| `backend/internal/asyncprocessor/handlers_billing.go` | `BillingSettlementHandler` 调现有 `billing.Settler`，保持 Tx2 semantic；必须 idempotent by `(tenant_id, claim_id, acquisition_token, handler_id)`. | High: touches billing semantics if wired incorrectly. |
| `backend/internal/asyncprocessor/handlers_audit.go` | `AuditDurabilityHandler` 只处理 processor-level audit/chain evidence；不要绕开 `Settler` 已写的 money-grade `billing_events`。 | Medium. |
| `backend/internal/asyncprocessor/handlers_sideeffects.go` | usage projection/cold archive/alert/OTel/reconciliation marker 等非 money-grade handler 槽位。raw body storage 默认只持有 redacted reference，真实 cold store 需 Owner 另定。 | Medium; privacy risk around body capture. |
| `backend/internal/asyncprocessor/runtime_test.go` | chain order、priority order、drain boundary、timeout demotion、failure isolation 单测。 | Low. |
| `backend/internal/asyncprocessor/dlq_test.go` | DLQ classification、replay idempotency、handler retry budget 单测。 | Low. |
| `backend/internal/billing/billing.go` | 不改 `Settler` 基本 contract；可新增 `CompletionSettlementRequest` adapter type 或 helper，但不应把 existing Tx2 invariant 隐藏在 generic async abstraction 后面。 | High if interface churn affects current tests. |
| `backend/internal/billing/settler.go` | 第一阶段尽量少改；只允许注入 fault/test hook 或复用 existing `Settle` from billing handler。任何改变 insert order 都要 cross-review。 | High: billing ledger core. |
| `backend/internal/gatewayhttp/chat_completions_handler.go` | 增加 `CompletionProcessor` dependency；non-streaming/streaming completion 统一构造 event。推荐 first slice 保留 `critical-inline prefix`: billing settlement/audit durable prefix 完成后，剩余 handler async。 | High: reverse-proxy hot path. |
| `backend/cmd/gateway/main.go` | wire processor runtime、start/stop lifecycle、admin DLQ replay handler dependency。 | Medium. |
| `backend/internal/config/config.go` | 新增 env config：enabled flag、worker count、batch size、drain window、priority ratios、DLQ max attempts、timeout demotion duration。YAML 仍只是 example。 | Medium. |
| `backend/internal/obs/obs.go` | 当前 interface 已有 `ReplayDLQ`，但 implementation 尚未完成（backend/internal/obs/obs.go:12-19, backend/internal/obs/obs.go:73-75）。可拆成 `asyncprocessor.Replayer`，admin 层调用统一 replay service。 | Medium. |
| `backend/internal/gatewayhttp/admin_observability_handler.go` | 若本 slice 暴露 DLQ list/replay，需要新增 store methods 和 route handler；否则只保留 plan/test stub。 | Medium. |
| `backend/internal/db/*.sql.go`, `backend/internal/db/models.go` | sqlc generated only；不要手写。若 schema/queries 获批，运行 sqlc 后更新。 | High because schema-dependent. |
| `backend/internal/billing/settler_integration_test.go` | 扩展 AT-OBS-017/018/020 coverage：usage write fail -> billing audit preserved / DLQ row / no dropped settlement。现有 AT-OBS-004 atomic test 已覆盖 current Tx2 happy path。 | Medium. |
| `backend/cmd/gateway/smoke_test.go` | 增加 end-to-end assertion：successful request produces claim/usage/billing event plus async event state consumed or acknowledged。 | Medium. |

## handler chain architecture (ASCII)

核心原则：F-OBS-004 是 chain/runtime abstraction；F-OBS-001 的 Tx2 durable settlement 不降级。第一实现建议把 chain 分成 `critical prefix` 和 `async suffix`。

```text
HTTP reverse-proxy hot path
  |
  | upstream completed / stream finalized
  v
RequestCompletionEventBuilder
  |
  | builds event from:
  | tenant_id, request_id, claim_id, account_id,
  | acquisition_token, attempt_seq, UsageRecordDraft,
  | routing_reason, snapshot_version, redacted body refs
  v
CompletionSubmitter
  |
  +-- critical-inline prefix (must finish or fail closed)
  |     01 EventValidationHandler
  |     02 IdempotencyGateHandler
  |     03 BillingSettlementHandler        -> existing billing.Settler.Settle / Abort
  |     04 BillingAuditDurabilityHandler   -> verifies money-grade audit evidence exists
  |
  +-- durable async suffix enqueue (priority-aware)
        |
        v
  Primary Queue (Postgres, authoritative)
        |
        +--> Backup Queue Mirror (best-effort/non-authoritative, F-OBS-005)
        |
        v
  ProcessorRuntime
        |
        | dequeue by priority: critical > normal > low
        | batch drain window: per batch, not global
        v
  Chain suffix handlers
        05 UsageProjectionHandler
        06 RequestBodyRedactionHandler
        07 RequestBodyColdArchiveHandler
        08 ResponseBodyRedactionHandler
        09 ResponseBodyColdArchiveHandler
        10 SchedulerOutboxDispatchHandler
        11 ReconciliationMarkerHandler
        12 ProviderHealthSignalHandler
        13 OperatorAlertHandler
        14 OTelMetricsExportHandler
        |
        +-- handler failure
              |
              v
            F-OBS-005 DLQ
              |
              +-- replay same handler idempotently
              +-- timeout > 15m demotes to low-priority lane unless marked critical
```

Why this shape:

- `BillingSettlementHandler` keeps using the existing `billing.Settler` contract instead of rebuilding ledger logic in a worker. Current `Settler` contract says it owns Tx2 Usage Record + billing event + claim status + in-flight decrement (backend/internal/billing/billing.go:27-38).
- Current implementation writes Usage Record before Billing Event and rolls back everything if Usage Record insert fails (backend/internal/billing/settler.go:78-119). F-OBS-004/005 must fix this deferred gap, but the fix should be explicit and tested, not hidden behind "async".
- The async suffix handles heavy/non-money side effects after the critical prefix, so reverse-proxy work is still reduced without violating `BILLING_PIPELINE_DROP` prevention (docs/specs/observability-billing.md:144-147).

Handler contract:

- `Handle(ctx, event, state) (HandlerResult, error)`.
- Every handler receives `event_id`, `handler_id`, `tenant_id`, `claim_id`, `acquisition_token`, and `idempotency_key`.
- A handler may return `Done`, `RetryAfter`, `SkipAlreadyDone`, `SendToDLQ`, or `FatalStopChain`.
- Chain state records last completed handler; replay resumes from failed handler unless the previous handler is explicitly non-resumable.
- Handler names are HUAKAI spec role names, not upstream identifiers.

## priority queue + dead-letter integration with F-OBS-005

F-OBS-005 requires DLQ + priority + asymmetric dual write. Existing schema only has narrow `usage_record_dlq` for Usage Record write failure (docs/schema/observability-billing.sql:193-217) and `scheduler_outbox` for cache invalidation lag/consumer state (docs/schema/pool-routing.sql:313-336). Those tables are insufficient for a 14-handler processor chain because they do not store handler_id, event priority, next-visible time, backup mirror state, or per-handler idempotency status.

Recommended integration:

1. Add a generic queue domain, subject to Owner schema approval:
   - `async_processor_events`: authoritative primary queue, event payload, priority, status, visible_at, attempt counters, payload_hash, created_at.
   - `async_processor_handler_state`: `(event_id, handler_id)` state, idempotency_key, completed_at, failure count, last_error_class.
   - `async_processor_dlq`: event_id, handler_id, failure_reason, payload snapshot/hash, replay_attempts, last_replay_at, replayed_at.
   - `async_processor_backup_events`: backup mirror with event_id/payload_hash only; non-authoritative disaster recovery pointer.
2. Keep `usage_record_dlq` as compatibility table for existing F-OBS-001 admin semantics until migration. Do not overload it for generic handler failures.
3. Priority lanes:
   - `critical`: settlement/audit-related handoff, hot-path suffix work that must drain before operator-visible consistency.
   - `normal`: usage projection, scheduler outbox dispatch, provider health feedback.
   - `low`: cold body archive, OTel export, bulk replays, messages demoted after timeout.
4. Fairness:
   - Always serve `critical` first, but reserve a small ratio for `normal/low` when critical is continuously non-empty to prevent permanent starvation of replay/export.
   - Critical replay of billing handler is allowed only when idempotency gate proves no prior commit.
5. Timeout demotion:
   - Event visible for more than 15 minutes without claim/handler progress becomes `timeout_demoted`.
   - Critical settlement events must not silently demote; they page/alert and stay critical until manually marked safe, because demoting billing can hide revenue loss.
6. DLQ replay:
   - Replay must re-run the failed handler with the same `event_id`, `handler_id`, and idempotency_key.
   - Replay of `BillingSettlementHandler` must first read claim status and acquisition token; committed/aborted claims are `SkipAlreadyDone`, not a second write.
   - Replay writes an audit trail with actor_id for admin replay; current admin route is not implemented (backend/cmd/gateway/main.go:475-479).
7. Primary/backup non-symmetric dual write:
   - Primary queue write is authoritative and required before async suffix acknowledgement.
   - Backup mirror write is non-authoritative; if it fails, emit `backup_mirror_lag` alert and retry from primary. Backup cannot create ledger mutations by itself.
   - Reconciliation compares primary event count/hash vs backup mirror; divergence is operational signal, not a second source of truth.

Open schema decision:

- If Owner does not approve new queue tables in this slice, implement only in-memory/runtime interfaces plus tests and leave DB-backed F-OBS-005 as Mandatory Roadmap. An in-memory queue cannot satisfy F-OBS-005 durability.

## test plan (unit + integration)

Unit tests:

- `asyncprocessor`: chain executes handlers 01..14 in order and stops on first non-retryable failure.
- handler idempotency: duplicate event with same `(tenant_id, claim_id, acquisition_token, handler_id)` returns `SkipAlreadyDone`.
- priority ordering: critical events dequeue before normal/low; fairness still services low under sustained critical load.
- batch drain: one batch timeout does not cancel unrelated batches or poison next handler.
- DLQ classification: retryable error requeues with backoff; exhausted retry or validation fault writes DLQ.
- timeout demotion: event older than 15 minutes moves to low priority only when handler class allows demotion.
- critical no-drop invariant: when queue is full/unavailable, submitter either runs critical prefix inline or returns fail-closed; it never reports success with unpersisted settlement.
- backup mirror failure: primary ack remains authoritative; mirror failure emits alert metric and retry request.
- billing handler fake: `BillingSettlementHandler` calls `Settler.Settle` once; duplicate committed claim does not double-settle.
- audit handler fake: verifies billing evidence is present before suffix chain continues.

Integration tests with `integration_pg`:

- Queue dequeue uses tenant-scoped row locking and does not double-claim the same event under two workers.
- Critical/normal/low priority order persists across process restart.
- DLQ row contains event_id, handler_id, tenant_id, claim_id, payload_hash, failure_reason, replay state.
- DLQ replay succeeds exactly once and records actor_id/replay timestamp.
- Billing settlement path still produces claim, usage_record, billing_event, slot release, and claim status atomically. Existing `TestAT_OBS_004_AtomicFiveEffect` covers current happy path and should be retained/expanded.
- Simulated Usage Record insert failure path verifies the target invariant from F-OBS-001: billing audit survives and usage entry is recoverable from DLQ (docs/specs/observability-billing.md:129-132; docs/specs/_invariants/F-OBS-001-tx2-invariants-checklist.md:60-64).
- Outbox lag alert: rows older than configured threshold produce observable lag signal; current `scheduler_outbox` has lag threshold fields and indexes (docs/schema/pool-routing.sql:327-336).
- Admin route `/admin/v1/usage-record-dlq/{id}/replay` changes from `501` stub to authenticated replay path only after replay service exists.

HTTP/smoke tests:

- Streaming `/v1/chat/completions` still returns SSE and eventually settles; current hot path calls forwarder then `Settler.Settle` (backend/internal/gatewayhttp/chat_completions_handler.go:516-553).
- Non-streaming path still writes response only after critical settlement prefix succeeds; current path settles before writing response (backend/internal/gatewayhttp/chat_completions_handler.go:449-474).
- Queue unavailable test: successful upstream body must not be returned as normal success if settlement/audit critical prefix failed.

Commands expected after implementation:

- `cd backend && go test ./internal/asyncprocessor ./internal/billing ./internal/gatewayhttp`
- `cd backend && go test -tags integration_pg ./internal/billing ./internal/obs ./internal/asyncprocessor` with `HUAKAI_DATABASE_URL`.
- `cd backend && go test -tags smoke ./cmd/gateway` with `HUAKAI_DATABASE_URL`.
- Before any commit: `codex exec review --uncommitted --full-auto` per project discipline.

## time estimate

Plan-only work: completed in this file.

Implementation estimate after synthesized plan approval:

- Minimal Go runtime + memory fakes + unit tests: 0.75-1.5 engineer days.
- DB-backed queue/DLQ schema + sqlc + integration tests: 2-3 engineer days after Owner approves schema.
- HTTP/admin wiring + smoke coverage: 1-1.5 engineer days.
- Hardening/failure injection/review loop: 1-2 engineer days.

Total realistic slice: 5-8 engineer days, assuming schema approval and available PostgreSQL integration test environment. Without schema approval, only an interface/runtime scaffold is feasible and does not close F-OBS-004/005.

## blast radius

High-risk areas:

- Billing ledger and Tx2 settlement. Existing invariant says every Tx2 commit produces billing ledger entry, billing event, and usage record together (docs/specs/observability-billing.md:162-168). Moving this behind a weak queue can create revenue loss.
- Reverse-proxy hot path. `chat_completions_handler.go` currently controls exactly when response is written relative to settlement; changing this can produce 200 OK without durable charge.
- Database schema and generated sqlc code. Generic async queue/DLQ needs new tables or significant extension; this touches high-risk schema.
- Admin DLQ replay. Replay can double-settle if idempotency guard is wrong.
- Request/response body capture. Cold archive handlers may store sensitive payloads; default must be redacted references until retention/privacy policy is explicit.
- Worker lifecycle. A runtime started/stopped from `cmd/gateway` can block shutdown or lose in-flight handler progress if ack semantics are wrong.

Mitigations:

- Keep first implementation's billing/audit critical prefix inline or wait-for-durable before response success.
- Treat generic queue schema as Owner decision point, not silent implementation detail.
- Make every handler idempotency-keyed and replay-safe before admin replay is enabled.
- Build failure injection tests before wiring live HTTP path.
- Keep existing direct `Settler` tests passing; add processor tests around them rather than replacing them wholesale.

## decision points

1. **Schema approval**: approve new `async_processor_*` tables, or limit this slice to Go interfaces/memory runtime only.
2. **Hot path release rule**: choose between:
   - recommended: critical prefix (`BillingSettlementHandler` + audit durability) must finish before HTTP success; async suffix can lag;
   - riskier: durable primary queue handoff is enough to return success, with fail-closed if handoff fails. This needs explicit Owner acceptance because it weakens current "Tx2 before response" behavior.
3. **Generic DLQ vs usage_record_dlq extension**: recommended generic DLQ plus compatibility bridge; extending `usage_record_dlq` is likely too narrow for 14 handlers.
4. **Raw body storage**: decide whether request/response body archive is in scope now. Recommended default: only redacted refs + payload hash until cold-store retention policy is finalized.
5. **Backup queue semantics**: recommended non-authoritative backup mirror. Owner must confirm whether backup divergence blocks release or is alert-only.
6. **Admin replay exposure**: do not expose replay UI/API until idempotency and audit actor logging are tested.
7. **Worker topology**: single-process worker first vs multi-instance Postgres queue. Recommended: single-process first, DB queue designed for multi-instance but rollout gated.
8. **Per-commit review**: implementation commits must run `codex exec review --uncommitted --full-auto`; slice completion needs cross-review if declared done.

## clean-room: upstream references by reputation only

- F-OBS-004/F-OBS-005 rows name Helicone as the reputation/evidence source in HUAKAI's feature matrix (docs/03_FEATURE_PARITY_MATRIX.md:115-116).
- This plan did not read Helicone, Sub2API, New API, Portkey, LiteLLM, All API Hub, or Envoy AI Gateway source.
- This plan makes no upstream mechanism claim beyond what HUAKAI's internal matrix already records.
- All architecture here is HUAKAI-internal synthesis from current F-OBS-001 invariants and Go code shape.
- Handler names are generic HUAKAI role names. No upstream function names, struct fields, comments, schemas, or implementation order are copied.
- If future implementation wants to claim exact upstream parity behavior, CLAUDE.md #11/#12 require a separate source-read lane with citations and clean-room guard. That was intentionally out of scope here.

## sources read

HUAKAI internal docs:

- `.agents/skills/pm-orchestrator/SKILL.md`
- `CLAUDE.md`
- `docs/01_PROJECT_BRIEF.md`
- `docs/03_FEATURE_PARITY_MATRIX.md`
- `docs/10_RISK_REGISTER.md`
- `docs/12_AGENT_WORKFLOW.md`
- `docs/specs/observability-billing.md`
- `docs/specs/_invariants/F-OBS-001-tx2-invariants-checklist.md`
- `docs/schema/observability-billing.sql`
- `docs/schema/pool-routing.sql`

HUAKAI backend files:

- `backend/go.mod`
- `backend/config.example.yaml`
- `backend/sqlc.yaml`
- `backend/sql/queries/billing_settle.sql`
- `backend/sql/queries/observability.sql`
- `backend/internal/billing/billing.go`
- `backend/internal/billing/settler.go`
- `backend/internal/billing/settler_integration_test.go`
- `backend/internal/gateway/gateway.go`
- `backend/internal/gateway/forwarder.go`
- `backend/internal/gatewayhttp/chat_completions_handler.go`
- `backend/internal/gatewayhttp/admin_observability_handler.go`
- `backend/internal/gatewayhttp/admin_observability_helpers.go`
- `backend/internal/gatewayhttp/admin_observability_handler_test.go`
- `backend/internal/obs/obs.go`
- `backend/internal/obs/repository.go`
- `backend/internal/obs/repository_integration_test.go`
- `backend/internal/config/config.go`
- `backend/cmd/gateway/main.go`
- `backend/cmd/gateway/smoke_test.go`

Commands used:

- `rg --files -g '*.go' backend`
- `rg -n "F-OBS-004|F-OBS-005|dead-letter|DLQ|priority queue|usage-record-dlq|scheduler_outbox|outbox|async" docs/03_FEATURE_PARITY_MATRIX.md docs/specs docs/schema backend --glob '!docs/decompositions/**' --glob '!backend/internal/proto/fixtures/**'`
- targeted `sed`/`awk` reads listed above
- `git status --short`

No reference project source files were read.

Source files read: HUAKAI internal paths listed above only
Lane: SPECIFIER
Agent: GPT-5 Codex
UTC timestamp: 2026-05-15T13:29:38Z
