# Observability + Atomic Billing — Synthesis (Source-Verified)

| Field | Value |
| --- | --- |
| Status | Action Plan (synthesized from source-verified inputs) |
| Feature ID | F-OBS-001 (with corrections to F-BILL-001 framing) |
| Lane mode | Option C (Usage Record + billing settlement is on the Option C carve-out per [DR-000](../../decisions/DR-000-clean-room-methodology.md)) |
| Author | Claude (PM-Orchestrator) |
| Date | 2026-04-28 |
| Sources | Sub2API ([E-LIC-001](../../07_REFERENCE_EVIDENCE_LEDGER.md), LGPL-3.0, commit `b0a2252...`); Helicone ([E-LIC-009](../../07_REFERENCE_EVIDENCE_LEDGER.md), GPL-3.0 — behavior-only by clean-room policy) |
| Inputs | [observability-source-verified.md](../sub2api/observability-source-verified.md) (Claude Sub2API atomic-billing finding), [helicone/observability-source-verified.md](../helicone/observability-source-verified.md) (Codex Helicone cross-verify) |
| Becomes | After CL-001..011 review APPROVE, file moves (cleaned of source identifiers) to `docs/specs/observability-billing.md` Status=Released. |
| Critical correction | This synthesis carries the corrected framing from F-OBS-001: **Sub2API HAS atomic billing**. Earlier prose (F-BILL-001 cycle 1 synthesis) implied non-atomic billing — that was wrong. HUAKAI's improvement is **promoting Usage Record into the same atomic transaction as billing**, NOT "adding atomic billing where there is none." |

## 1. The Real Sub2API Picture (Source-Verified)

Per [F-OBS-001 Sub2API pass](../sub2api/observability-source-verified.md):

**What Sub2API has (atomic, money-grade)**:
- Single-transaction `Apply` runs:
  - Idempotent claim gate (INSERT...ON CONFLICT(request_id, api_key_id) DO NOTHING) with archive table check + fingerprint conflict detection.
  - 5 atomic effects: subscription rolling-window usage, user balance deduct (RETURNING new_balance), API key quota + status flip, API key rate-limit windows (5h/1d/7d auto-rollover), Account quota multi-window (total/daily/weekly).
  - Cross-threshold detection: when increment crosses any limit, enqueue scheduler outbox row in same transaction → **transactional outbox pattern** for cache invalidation.
- Detached billing context: refresh-token + cancel propagation NOT inherited from request context.
- API Key auth-cache invalidation on quota exhaustion.
- `deferredService.ScheduleLastUsedUpdate` on Apply=false (already settled, just bump last-used).

**What Sub2API does NOT have (the gap)**:
- Pre-call reservation (claim is post-call only).
- Atomic Usage Record write (`writeUsageLogBestEffort` is queued/batched/detached from the Apply transaction).
- Tenant scoping (single-tenant in this code path).
- Numeric(20,8) end-to-end (passes through Go float64 in cmd.BalanceCost).
- Outbox consumer lag observability.
- Per-tenant retention policy.

## 2. The Helicone Picture (Codex Cross-Verify, Behavior-Only per GPL-3.0)

Per [Helicone Codex cross-verify](../helicone/observability-source-verified.md):

Helicone's contributions to HUAKAI thinking:
- **H-K1**: Decouple operator analytics ingestion from the caller-visible response path (Sub2API does this too, both KEEP).
- **H-K2**: Durable queue + dead-letter path between gateway and analytic ingestion (Sub2API has queue but DLQ unclear).
- **H-K3**: Split analytic metadata from large body retention — keep request/response bodies in cold storage (e.g. S3), keep metadata + cost in hot store (e.g. ClickHouse).
- **H-K4**: Capture stream terminal reason explicitly (HUAKAI taxonomy already has this).
- **H-K5**: Polling for first-version live dashboards is fine (don't over-engineer real-time push).
- **H-K6**: Apply tenant context at multiple layers (gateway, queue partition, hot-store tenant_id column).

These do NOT contradict Sub2API; they extend it.

## 3. Convergence (Both References Agree)

1. **Decouple analytics from caller path** — neither lets analytics write block client response.
2. **Best-effort with retry** — both have queue + retry semantics.
3. **Hot-vs-cold retention split** — Helicone explicit; Sub2API implicit (usage_logs hot, raw bodies not retained by default).
4. **Tenant context at multiple layers** — Helicone explicit; Sub2API has tenant scaffolding but doesn't fully exercise.

## 4. Where Sub2API Sharpens Helicone

- **S1 — Atomic 5-effect billing transaction with idempotent claim**. Helicone is observability-focused, not money-grade billing-focused. Sub2API's transactional integrity is stronger.
- **S2 — Transactional outbox pattern** for cross-threshold scheduler invalidation. Helicone has retry semantics but not transactional outbox.
- **S3 — Status flip atomic with quota increment** (single SQL row update). Defensive primitive against status-vs-counter race.

## 5. Where Helicone Sharpens Sub2API

- **P1 — Explicit DLQ** for analytics queue overflow. Sub2API drops on queue full with sync fallback; doesn't preserve dropped events for later retry.
- **P2 — Hot-vs-cold storage tier split** with explicit retention policy. Sub2API's `usage_cleanup_repo.go` is global, not tier-aware.
- **P3 — Polling-first dashboards** philosophy. Sub2API doesn't have a dashboard story explicitly; HUAKAI takes the discipline.

## 6. HUAKAI Design Improvements (Neither Reference Has)

These are HUAKAI-DESIGN, NOT inherited:

- **H1 — Pre-call reservation Tx1**: claim row written BEFORE upstream call with `status=reserving`. Orphan sweep recovers crashed reservations. Sub2API has post-call only.
- **H2 — Tx2 Usage Record atomic with billing**: Usage Record write promoted into the Apply transaction (Sub2API has the interface for this — `tx in context` path — but doesn't use it in production). HUAKAI uses it.
- **H3 — Tenant_id in every claim, every effect, every audit row, every cache key**.
- **H4 — Numeric(20,8) money type end-to-end**: Go `decimal.Decimal` (or shopspring/decimal), no float64 in cost paths.
- **H5 — Outbox consumer lag metric** with operator alert when row-age exceeds threshold (default 60s).
- **H6 — Per-tenant retention policy**: `usage_logs` partitioned by tenant_id; per-tenant retention class.
- **H7 — Pending reconciliation flag** for `inferred` / `partial` Usage Records: marks for later reconciliation against upstream out-of-band usage report.
- **H8 — Audit-grade billing event row INSIDE Apply tx** (separate from full Usage Record), so audit trail survives Usage Record async failure.
- **H9 — Token-leakage-safe logging**: no credential bytes in error messages, even fragments.
- **H10 — Helicone-style explicit DLQ** for Usage Record write retries.
- **H11 — Helicone-style hot-vs-cold tier split** for body retention.

## 7. The Synthesized HUAKAI Architecture — Final

### 7.1 Two-transaction model (Quota+Billing)

**Tx1 — Reserve** (per request, before upstream call):
- Acquire row locks in fixed order: Billing Ledger claim → User → API Key → Subscription → Provider Account → rate-window rows.
- Look up or insert claim row by `idempotency_key` (HUAKAI fields: tenant_id, api_key_id, logical_request_id, endpoint_family, payload_hash, requested_model, pooling_group_id, billing_policy_version, request_class).
- If existing claim with same fingerprint AND status=committed: return cached prior response.
- Else: write claim row with status=reserving, predicted_cost.
- Reserve quota across User / API Key / Subscription / Account (5 effects).
- Commit. **Pool slot acquisition follows in F-POOL-001 Pattern B.**

**Tx2 — Reconcile** (per request, after upstream resolution):
- Acquire same row locks in same order.
- Update claim row: status → committed (or aborted on terminal failure).
- Apply final cost: subscription / balance / API key quota / API key rate-limit windows / Account quota (5 atomic effects, mirroring Sub2API's `applyUsageBillingEffects`).
- Cross-threshold detection → enqueue scheduler outbox row in same tx (Sub2API S2 pattern).
- **Write Usage Record into THIS transaction** (HUAKAI H2 — Sub2API does NOT do this in production).
- Write audit-grade billing event row (HUAKAI H8 — survives Usage Record async failure).
- Decrement Provider Account in_flight_count atomically (HUAKAI ties Pool slot release into Tx2).
- Commit.

If Tx2 commit fails: orphan sweep + claim row status=reserving + age threshold → roll back reservation.

### 7.2 Idempotency key composition (HUAKAI)

```
idempotency_key = HASH(
    tenant_id,
    api_key_id,
    logical_request_id,
    endpoint_family,
    normalized_request_payload_hash,
    requested_model,
    pooling_group_id,
    billing_policy_version,
    request_class
)
```

`logical_request_id`:
1. Client-supplied stable (HTTP `Idempotency-Key` header).
2. Else: deterministic derivation from `(api_key_id, normalized_payload_hash, tenant_id, time-window-bucket-15s)`.
3. Never: random fallback (would defeat replay-safety).

Provider Account id is **NOT** in the key (per F-POOL-001 §6 / Pattern B). Provider Account lives in per-attempt records.

### 7.3 Usage source taxonomy (carries through to Tx2)

Closed enum: `reported` / `normalized` / `inferred` / `partial` / `ambiguous`. Sub2API has none; HUAKAI design improvement (H7).

When `inferred` or `partial`: `pending_reconciliation = true`. Background reconciliation worker scans for pending rows; if upstream out-of-band usage report arrives, update Usage Record + write delta to billing event log (NOT modifying claim — claims are immutable post-commit).

### 7.4 Hot-vs-cold storage (Helicone-inspired)

```
HOT STORE (PostgreSQL, low-latency):
- billing_ledger_claim     (idempotency, money-grade, tx-anchored)
- billing_event             (audit-grade, in Tx2)
- usage_record_metadata     (cost, tokens, routing_reason — partitioned by tenant_id)
- scheduler_outbox          (cross-threshold invalidation messages)

COLD STORE (S3 / equivalent, high-volume):
- usage_record_full         (raw request/response bodies, retained per-tenant policy)
```

Hot store retention: per-tenant policy, default 90 days. Cold store retention: per-tenant policy, default 7 days. Both operator-tunable per Pool / Tenant.

### 7.5 Outbox + Consumer pattern

- Outbox rows enqueued INSIDE Tx2 commits (transactional outbox).
- Consumer pulls rows in order, fires invalidation events to scheduler / cache layer.
- Idempotent invalidation: scheduler can re-process same row (at-least-once delivery).
- **Consumer lag metric** (HUAKAI H5): if any row age > 60s, alert.

### 7.6 DLQ pattern (Helicone-inspired)

- Usage Record async write queue: bounded, with DLQ tail for sync-fallback failures.
- DLQ entries persisted to PostgreSQL `usage_record_dlq` table (NOT in-memory only).
- Operator dashboard surfaces DLQ depth and retry success rate.
- Auto-replay on operator-configured cadence (e.g. every 5min); manual replay via admin API.

## 8. Concurrency / Correctness Invariants

| # | Invariant | Source |
|---|-----------|--------|
| O1 | Tx1 commits before any upstream call. | HUAKAI-DESIGN (Sub2API has post-call only). |
| O2 | Tx2 atomically commits: claim status flip + 5 effects + cross-threshold outbox + Usage Record + audit event. | HUAKAI-DESIGN (Sub2API has 5 effects + outbox; HUAKAI adds Usage Record + audit event in same tx). |
| O3 | Idempotency key excludes Provider Account id. | HUAKAI-DESIGN (Sub2API includes account in key in some paths). |
| O4 | All claim and effect rows carry tenant_id. | HUAKAI-DESIGN. |
| O5 | Money fields use numeric(20,8) end-to-end (Go decimal type). | HUAKAI-DESIGN (Sub2API uses float64). |
| O6 | Outbox consumer lag is monitored; alert fires on row > 60s old. | HUAKAI-DESIGN. |
| O7 | Usage Records with inferred/partial source carry pending_reconciliation flag. | HUAKAI-DESIGN. |
| O8 | Audit-grade billing event row in Tx2 survives Usage Record async failure. | HUAKAI-DESIGN. |
| O9 | Cross-threshold outbox covers all dimensions (total / daily / weekly + new HUAKAI: tenant-monthly, billing-cycle). | HUAKAI extends Sub2API. |
| O10 | DLQ for Usage Record write failures persisted to PostgreSQL. | HELICONE-INSPIRED, HUAKAI-DESIGN. |
| O11 | Hot-vs-cold storage tier with per-tenant retention policy. | HELICONE-INSPIRED, HUAKAI-DESIGN. |
| O12 | Tx1/Tx2 lock order is fixed (alphabetical by entity-id pair) and documented. | KEEP from prior cycle-1 synthesis discipline. |
| O13 | Cache is read-through hint, NEVER source of truth for spend admission. | KEEP from Sub2API + cycle-1. |
| O14 | All retries inside one logical request share the same Billing Ledger claim. | KEEP from Sub2API + cycle-1. |

## 9. Failure Taxonomy (Tx1 + Tx2 boundary)

| Reason | Trigger | Recovery |
|--------|---------|----------|
| `TX1_QUOTA_EXHAUSTED` | User / API Key / Subscription / Account quota predicts insufficient | Client 402 Payment Required + cached response if same fingerprint already settled |
| `TX1_CLAIM_RACE` | Concurrent attempt won the idempotency claim | Retry with same key; re-read settled response |
| `TX1_FINGERPRINT_CONFLICT` | Same request_id, different fingerprint | Reject with 409 Conflict; do not bill |
| `TX1_LOCK_TIMEOUT` | Could not acquire row lock within wait budget | 503 Service Busy + Retry-After |
| `UPSTREAM_FAIL_AFTER_TX1` | Upstream call fails after Tx1 commit | Tx2 aborts claim; reservation rolled back |
| `TX2_RACE_LOST` | Pattern B writeback race lost | Restart from F-POOL-001 Phase B |
| `TX2_LOCK_TIMEOUT` | Could not acquire row lock for Tx2 | Orphan sweep recovery (status=reserving + age) |
| `USAGE_RECORD_WRITE_FAIL` | DLQ insertion needed | Audit event row in Tx2 survives; DLQ holds retry payload |
| `INFERRED_RECONCILIATION_PENDING` | Stream ended without explicit usage frame | Usage Record committed with pending_reconciliation=true |
| `OUTBOX_CONSUMER_LAG` | Consumer dead | Operator alert; manual restart |

## 10. Test Scenarios (AT-OBS-001..017)

### Sub2API-inheritable (verifiable against source)

- AT-OBS-001 / Idempotent replay (same fingerprint): Apply=false, no double charge.
- AT-OBS-002 / Replay attack (different fingerprint): ErrUsageBillingRequestConflict.
- AT-OBS-003 / Archive replay (claim in archive table): Apply=false.
- AT-OBS-004 / Atomic 5-effect: all-or-nothing.
- AT-OBS-005 / Quota status flip: API key crosses quota → status=quota_exhausted in same row update.
- AT-OBS-006 / Multi-window auto-rollover: 5h window expired → usage_5h resets, not increments stale.
- AT-OBS-007 / Cross-threshold outbox: account daily limit crossed → scheduler_outbox row in same tx.
- AT-OBS-008 / Detached context survival: gateway crash mid-Tx2 → Apply commit visible if Tx2 reached commit; rolled back otherwise.
- AT-OBS-009 / Best-effort drop with sync fallback: Usage Record queue overflow → sync Create.
- AT-OBS-010 / LRU dedup: same Usage Record submitted twice in 5s → second deduplicated.

### HUAKAI-design (no Sub2API equivalent)

- AT-OBS-011 / Pre-call Tx1 rollback: gateway crash before upstream → no claim row, no charge.
- AT-OBS-012 / Tx1 commit + Tx2 missing: orphan sweep finds status=reserving + age → reservation rolled back.
- AT-OBS-013 / Tenant isolation: T1 request_id A and T2 request_id A do NOT collide.
- AT-OBS-014 / Money precision: 1M × $0.0000001 → exactly $0.10 (decimal end-to-end), not $0.099999... (float drift).
- AT-OBS-015 / Outbox lag alert: consumer dead → row > 60s → alert.
- AT-OBS-016 / Pending reconciliation: stream ends with `usage_source=inferred` → Usage Record with pending_reconciliation=true; later upstream report → updated.
- AT-OBS-017 / Audit-grade billing event: Apply tx writes both billing_event AND queues Usage Record. Usage Record async fails → billing_event still serves audit query.
- AT-OBS-018 / DLQ persistence: Usage Record write fails AND sync fallback fails → entry in usage_record_dlq table; replay succeeds.
- AT-OBS-019 / Hot-vs-cold split: hot store has metadata, cold store has body; per-tenant retention different.

## 11. Open TODOs

- **TODO-1**: Read remaining lines of `usage_log_repo.go` (lines 1-258 + 308-end) for full prepared-insert + dedup-LRU mechanics.
- **TODO-2**: Read `billing_cache_service.go` (965 lines) for cache update queue semantics + cache→DB sync.
- **TODO-3**: Find and read scheduler-outbox consumer for at-least-once delivery + idempotent invalidation.
- **TODO-4**: Verify `r.db.BeginTx(ctx, nil)` isolation level — PostgreSQL default is `READ COMMITTED`, but Sub2API may rely on `SERIALIZABLE` for some invariants.

These do NOT block synthesis sign-off; they DO block Released spec (per CL-009 — must close or convert before Released).

## 12. Provenance

- Sub2API: commit `b0a2252...`, files `repository/usage_billing_repo.go` (full 337 lines), `repository/usage_log_repo.go` lines 258-340, `service/gateway_service.go:7476-7835` (multiple sections), source-verified by Claude PM 2026-04-28.
- Helicone: behavioral cross-verify by Codex 2026-04-28, GPL-3.0 license bound — behavior-only references in this synthesis, no Helicone source code reproduced.
- This synthesis: Claude PM, after both inputs read.
- Reviewer-lane sign-off: pending Codex final review CL-001..011.

## 13. Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | (pending — fresh agent session must run CL-001..011) |
| Review date | (pending) |
| Owner answers received | N/A (no Owner-decision questions in this feature; Q1..Q4 from F-POOL-001 are upstream of HUAKAI's lock-order documentation here) |
| Checks passed | (pending) |
| Notes | F-OBS-001 + F-BILL-001 framing correction synthesis. Critical correction: Sub2API HAS atomic billing; HUAKAI's improvement is promoting Usage Record into Tx2 + adding pre-call Tx1 reservation. 11 HUAKAI-design improvements clearly labeled. 4 open TODOs, none blocking synthesis. |
