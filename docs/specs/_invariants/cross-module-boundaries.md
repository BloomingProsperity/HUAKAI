# Cross-Module Boundaries — HUAKAI Architecture Invariants

| Field | Value |
| --- | --- |
| Status | v0.1 |
| Date | 2026-04-30 |
| Trigger | Owner directive 2026-04-30: Router Engine ≠ Resource Pool ≠ Gateway Executor; each layer holds clear responsibility and forbidden boundaries. Sister doc to `docs/02_HUAKAI_FUSION_ARCHITECTURE.md`. |
| Enforcement | Reviewer-lane gate: every PR touching `internal/{router,pool,billing,obs,proto,gatewayhttp,auth,registry}` MUST be checked against this doc by a different agent session than the one that wrote the PR. |

---

## The 3-tier responsibility split (binding)

Owner-stated quote 2026-04-30:

> Router Engine：决定"应该尝试哪些路线"
> Resource Pool：决定"这条路线下哪个资源现在能被 claim"
> Gateway Executor：负责按 RoutePlan 执行 attempt、claim、forward、settle、fallback
>
> Pool 里仍然可以保留"池内选择逻辑"，比如同一个 pool 内的资源权重、冷却、并发槽位、健康度过滤。但跨 provider、跨模型、跨成本、跨策略的排序，应该属于 Router。

So:

| Layer | Decides | Does NOT decide |
|---|---|---|
| **Router Engine** | which `(pool_group_id, model_id, attempt_idx)` triples to try in what order; cross-provider / cross-model / cross-cost / cross-policy ordering; attempt budget; retryable end_class set | which specific account inside one pool; cooling; slot availability; credential validity at runtime |
| **Resource Pool** | within ONE pool_group, which provider account is healthiest / least loaded; intra-pool LoadRate; cap_concurrency slot; sticky binding within group | cross-pool ordering; whether to retry; what model alias maps to what provider |
| **Gateway Executor** | iterate Router's attempts; per-attempt: call Pool.Claim → Adapter.Forward → Settler.Settle/Refund; on failure decide whether to advance to next attempt per Router's policy | what those attempts are; what resources exist; what tokens cost |

**The three layers are CALL-ORDERED, not co-equal.**

```
Auth → Registry → Router → (Executor loop: per attempt → Pool → Adapter → Ledger → next?)
```

---

## Public contracts (stable across HUAKAI versions)

```go
// auth
ResolveInboundAuth(ctx, *http.Request) (RequestContext, error)

// registry
ResolveModel(ctx, publicModel string, tenant TenantID) (ResolvedModel, error)

// router
Plan(ctx, RequestContext, ResolvedModel, RequestFeatures) (RoutePlan, error)

// pool
Claim(ctx, AttemptPlan) (Lease, error)
ReleaseLease(ctx, LeaseID, ReleaseReason) error

// adapter
Forward(ctx, Lease, NormalizedRequest, http.ResponseWriter) (UpstreamResult, error)

// ledger
Reserve(ctx, ReserveRequest) (ClaimID, error)            // Tx1 outer
Settle(ctx, SettleRequest) (SettleResult, error)         // Tx2 commit
Refund(ctx, ClaimID, Amount, Reason) error               // future
RecordAttempt(ctx, AttemptID, AttemptResult) error       // future, attempt audit
RecordUsage(ctx, UsageRecord) error                      // already in Settler
Abort(ctx, TenantID, ClaimID, Reason) error              // already in Settler
```

These signatures are reviewer-gated. Adding/removing a parameter requires a DR-NNN.

---

## Three-ID system

Owner-stated 2026-04-30:

> request_id：一次用户请求，全链路唯一
> attempt_id：一次上游尝试。一次 request 可能有多个 attempt
> lease_id：一次资源占用。它绑定 resource/account/credential/slot 的占用周期

Plus the existing IDs:

| ID | Semantic | Lifetime | Owner |
|---|---|---|---|
| `request_id` | Single end-user HTTP request | Inbound → outbound response sent | chi middleware (set first) |
| `attempt_id` | Single upstream call attempt | Pool.Claim → Forward returns/fails | Executor (per loop iteration) |
| `lease_id` | Single resource occupation (= `pool_slot_acquisitions.id`) | Pool.Claim returns → Pool.ReleaseLease | Pool |
| `claim_id` (existing) | Single Tx1 reserve operation | Tx1 commit → Tx2 commit/abort | Ledger |
| `acquisition_token` (existing) | Internal anti-duplicate-release / anti-tamper token | == lease lifetime | Pool (NOT business audit) |
| `logical_request_id` (existing) | Idempotency hash input | Same as request_id when client provides Idempotency-Key | Auth + Ledger |

**Important reclassification per Owner**:

- `acquisition_token` is INTERNAL plumbing (prevents double-release). It is NOT a business audit identifier. Operator dashboards and audit logs SHOULD use `lease_id` (the `pool_slot_acquisitions.id`) instead, exposing `acquisition_token` only in low-level debugging views.
- `claim_id` keeps its semantics: Tx1 operation ID. It is NOT the same as `request_id` (one request can have multiple claims if Tx1 is replayed in a future fallback model).
- The chain in audit / observability queries should be: `request_id → claim_id(s) → attempt_id(s) → lease_id(s) → usage_record(s)`.

---

## Forbidden cross-module behaviors (reviewer must reject)

These are WRITTEN AS INVARIANTS to be cited in PR reviews:

### CMB-1: Router does not read credentials

The Router Engine MUST NOT call `auth.GetAccessToken`, MUST NOT read the `credentials` field of any `provider_accounts` row, and MUST NOT make outbound network calls to any provider. It receives only metadata: provider id, capability matrix, pricing class, last-known health.

**Why**: keeps the routing-decision tier pure (planning, no side effects); allows in-memory cache + parallel planning without lock/timeout concerns; legal: routing decisions can be derived from non-secret data.

**Enforced via**: lint + reviewer gate. Any import of `internal/auth` from `internal/router` blocks merge unless explicitly justified in DR-NNN.

### CMB-2: Resource Pool does not compute cost

`pool.Claim(...)` returns a Lease with `account_id, lease_id, acquisition_token, [provider_id, channel_id]`. It MUST NOT include cost numbers, predicted-charge values, or any decimal field. Cost lives in the Ledger.

**Why**: cost-aware routing is Router's job (B11 per-tenant retry budget); pool-internal selection is health/concurrency-driven, not cost-driven, so blending the two creates dual-source-of-truth bugs.

**Enforced via**: type signature; Lease struct has no decimal fields.

### CMB-3: Adapter does not bypass Ledger

`Forward(ctx, Lease, NormalizedRequest, w)` MUST NOT call `Settler.Settle/Abort/Reserve` directly. It returns a `UsageRecordDraft` (or error); the executor handles ledger interactions.

**Why**: an adapter that calls Settle directly creates two settle paths (success vs error), making the F-OBS-001 §Tx2 5-effect atomicity untestable.

**Enforced via**: adapter package does NOT import `internal/billing`.

### CMB-4: Ledger settles ONLY via events

Ledger Tx2 commits MUST be triggered by an explicit `SettleRequest` from the Executor with the full result tuple (claim_id, lease_id, attempt_id, usage_draft). Settlements MUST NOT be inferred from "did Forward return without error" or from a stream-end signal interpreted by the Adapter alone.

**Why**: F-OBS-001 H8 — usage record async failure must not lose the audit billing event. Coupling settle to a per-stream signal makes this untestable.

**Enforced via**: Executor as the sole place that touches `Settler.Settle/Abort`.

### CMB-5: Credentials never enter logs

Logs / spans / traces / structured fields MUST NOT include the `credentials` JSON, OAuth access_token, refresh_token, API key, or any plaintext secret. Even in error paths. Even in reviewer-only debug levels.

**Why**: per F-AUTH-005 + R-LIC-001 + R-AUTH-005 + post-mortem of all-api-hub plaintext-leak antipattern.

**Enforced via**: structured-log field allowlist + grep CI gate (Phase F.) Currently a reviewer manual check — gates added in Phase E.

### CMB-6: Every request has a request_id; every attempt has an attempt_id

The chi middleware sets `request_id` BEFORE auth. The Executor sets `attempt_id` BEFORE calling `Pool.Claim`. The Pool emits `lease_id` upon successful Claim. All three IDs propagate into every Ledger row written for that request.

**Why**: operator debugging requires the chain `request_id → claim_id → attempt_id → lease_id → usage_record`. Without it, correlating a customer-facing 502 with a slot leak is impossible.

**Enforced via**: schema constraint — `usage_records.request_id NOT NULL` + `usage_records.attempt_id NOT NULL` + `usage_records.lease_id NOT NULL` (planned 0007 migration; current schema lacks these columns and they will be added additively).

### CMB-7: Router writes nothing; Pool writes only its slot row; Ledger writes everything else

| Layer | Writes to DB? |
|---|---|
| Auth | No (read-only on api_keys/users in Phase E) |
| Registry | No (read-only on model/capability tables) |
| Router | No |
| Pool | YES — but only `pool_slot_acquisitions` row + atomic `provider_accounts.in_flight_count` increment; nothing else |
| Adapter | No |
| Ledger | YES — `billing_ledger_claims`, `usage_records`, `billing_events`, `scheduler_outbox`, `billing_ledger_adjustments` |

**Why**: localizes the failure surface. If a Tx2 invariant is violated, the bug is in Ledger code, not in Router.

---

## Migration discipline for adding/changing invariants

To change anything in this doc:

1. Open a DR-NNN explaining the proposed change.
2. PR must demonstrate that the change does not break the F-OBS-001 §Tx2 50 invariants.
3. Reviewer-lane (different agent session) signs off.
4. This doc updates with `Date superseded:` next to the old invariant + new invariant added.

---

## Reviewer checklist for any PR touching these layers

- [ ] Does the PR cross any forbidden boundary listed above?
- [ ] If new fields are added to a public contract (auth/registry/router/pool/adapter/ledger), is a DR-NNN attached?
- [ ] Does every new Ledger write include `request_id` + `claim_id` (or `attempt_id` if attempt-scoped)?
- [ ] Does the PR add any decimal field to a Pool struct (CMB-2 violation)?
- [ ] Does the PR add any auth/credential import inside `internal/router` (CMB-1 violation)?
- [ ] Does the PR add a `Settler.X` call from `internal/proto` or `pkg/adapter` (CMB-3 violation)?
- [ ] Does the PR log/span any field whose name suggests credential content (token, secret, key, password) (CMB-5 violation)?

If any answer is yes without a documented mitigation, request changes.
