# 2026-05-20 Case C Billing Setting Plan - Codex

| Field | Value |
| --- | --- |
| Owner directive | "Owner 2026-05-20 决定: 流式计费的一个边缘情况要做成'操作面板里的可配置设置'。" |
| Scope | Only planning. No Go implementation, no frontend implementation, no git command, no reference-project source read. |
| Output | This implementation plan for HUAKAI Go backend and admin API. |
| Recommended storage | New tenant-scoped `billing_settings` KV table, not `email_settings`. |
| Recommended scope | Per-tenant setting with default fallback to `no_bill`. |
| Recommended initial values | Implement `no_bill` and `no_bill_record`; keep `bill_input` as Mandatory Roadmap until Owner approves billing state-machine work. |
| Estimated implementation | 8-12 backend agent hours for `no_bill`/`no_bill_record`; +6-10 hours and Owner high-risk approval for `bill_input`. |
| Clean-room note | No reference-project source is needed or should be read for this work. |

## Local Evidence Read

- Current stream settlement gate lives in `backend/internal/gatewayhttp/chat_completions_stream.go:147`; the gate settles only when the stream attempt is chargeable, delivered token count is positive, or `EndClass == AmbiguousUsage`, otherwise it aborts at `:195-211`.
- Current billing state has only `Acquired`, `InFlight`, `Partial`, `Failed`; only `Partial` is chargeable in `backend/internal/billing/state.go:12-63`.
- `AttemptFromGatewayDraft` classifies streamed zero-delivery, non-graceful attempts as `Failed` in `backend/internal/billing/state.go:110-137`.
- `DefaultSettler.Settle` zeros cost through `CostForAttempt` before writing `usage_records` and `billing_events` in `backend/internal/billing/settler.go:126-190`.
- Abort already writes a zero-cost billing event and, when pool acquisition exists, a usage record, but it does not preserve the upstream token counts from the draft in `backend/internal/billing/settler.go:288-360`.
- Existing email settings are a tenant-scoped KV table, but they are explicitly F-AUTH-007 SMTP settings in `backend/sql/migrations/0025_email_settings.up.sql:1-24` and the store is in the email package in `backend/internal/email/settings_store.go:27-109`.
- Admin settings handlers already resolve `platform_admin` / `tenant_operator` and enforce tenant access in `backend/internal/gatewayhttp/admin_email_settings_handler.go:51-100` and `:146-170`; admin identity scope is defined in `backend/internal/admin/operator_auth.go:28-35`.
- DR-001 requires tenant-aware schema from day 1 and says multi-tenant UI can be deferred, while tenancy remains server-resolved in MVP in `docs/process/decisions/DR-001-multi-tenancy.md:19-28` and `:42-62`.
- The current frontend README says `frontend/` is a vertical-closure wedge, not an Admin UI, in `frontend/README.md:1-4`; another internal plan states frontend is frozen and backend API can proceed first in `docs/process/plans/2026-05-17-f-audit-1-implementation-plan-claude.md:48-68`.

## Case C Definition

For this plan, "case C" means a streaming upstream attempt where all of the following are true:

- upstream-reported input usage exists: `draft.TokensInput > 0`;
- no output usage exists: `draft.TokensOutput == 0`;
- no client-visible output was delivered: `streamAttempt.DeliveredTokenCount == 0`;
- the stream did not reach terminal `[DONE]` / protocol terminal marker, initially scoped to `draft.EndClass == gateway.UpstreamEOFNoTerminal`.

Implementation should make the end-class set explicit in a small helper, for example `isInputOnlyInterruptedStream(draft, attempt)`, and test it directly. I recommend starting with `UpstreamEOFNoTerminal` only. Including `UpstreamError5xx`, `UnknownTermination`, timeout, or client disconnect classes would broaden the policy beyond the Owner's described case and should be a separate Owner decision.

## Storage Recommendation

Use a new `billing_settings` table modeled after the shape of `email_settings`, but owned by billing/admin settings code:

```sql
CREATE TABLE billing_settings (
    id            bigserial PRIMARY KEY,
    tenant_id     bigint      NOT NULL REFERENCES tenants(id),
    setting_key   text        NOT NULL,
    setting_value text        NOT NULL,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    updated_by    text        NOT NULL,
    UNIQUE (tenant_id, setting_key)
);
```

Recommended first setting key:

- `stream_input_only_interrupted_policy`

Recommended first accepted values:

- `no_bill`
- `no_bill_record`

Do not allow `bill_input` as a persisted value in the first migration unless Owner explicitly chooses to implement the billing state-machine change in the same slice. A setting that can be saved but is not honored would be worse than a missing feature.

Why not reuse `email_settings`:

- It is documented and named as SMTP/email configuration, including secret-envelope semantics. Reusing it for billing policy would create domain confusion and pull billing code toward `internal/email`.
- Billing policy is money-path behavior. A dedicated table lets future code apply stricter validation, audit, and rollout checks without changing email settings semantics.
- A generic `tenant_settings` table would be more reusable, but it is broader than this slice and would require cross-domain naming, schema, and governance decisions. `billing_settings` is smaller blast radius.

Schema details for implementation:

- Add `CHECK (setting_key <> '')`.
- Add a value check for `stream_input_only_interrupted_policy` limited to the currently supported enum.
- Add `idx_billing_settings_tenant_updated ON billing_settings (tenant_id, updated_at DESC)`.
- Missing row resolves to effective default `no_bill`, preserving current behavior.
- Store raw enum text only; no secret material; no encryption needed.

Risk classification: database schema is high risk under AGENTS.md, so implementation requires Owner approval before creating migration files.

## Tenant Scope Recommendation

Make the setting per-tenant.

Reasons:

- DR-001 says tenant isolation is a Day 1 invariant and explicitly calls out per-tenant feature flags/rate limits as a reason for tenant-aware schema.
- Case C affects billing and retry economics. In SaaS Edition, different tenant operators may choose different commercial policy.
- In Personal Edition, the single default tenant makes a per-tenant setting operationally equivalent to a global setting without blocking future SaaS.
- A global setting could silently change billing behavior for all tenants and would be harder to audit as an operator action.

Do not add a global fallback row in v1. Use code-level default `no_bill` when the tenant row is absent. If a future platform-wide default is needed, add it as a separate explicit feature with audit and bulk-apply semantics.

## Hot Path Read Plan

Do not read PostgreSQL on every streamed request.

Recommended design:

- Add a small `billingsettings` package or `internal/billing/settings.go` with:
  - `PolicyStore` reading/writing `billing_settings`;
  - `PolicyResolver` returning effective tenant policy;
  - process-local cache keyed by `tenant_id`;
  - TTL default 30-60 seconds;
  - write-through invalidation for the current process after admin `PUT`.
- Wire the resolver into `ChatHandlerDeps`.
- Resolve the policy once per request after tenant identity is known and before dispatching upstream. Store the resolved value in `chatExecution`, so a policy edit during a stream does not change that stream's settlement result.
- On cache hit: no DB round trip.
- On cache miss or expired entry: read one tenant row. Use a mutex-protected map; avoid adding `singleflight` as a dependency unless already present.
- Multi-pod propagation can rely on TTL in v1. A later eventbus invalidation can improve freshness.

Failure behavior:

- If cache has a stale value and refresh fails, keep stale value for a bounded stale window and emit a metric/log.
- If there is no cached value and DB read fails, fall back to `no_bill` and mark an internal warning/metric. Unknown config must not cause surprise charging.
- Admin GET should read from store, not only cache, so operators see persisted truth.

## Settlement Behavior By Value

### `no_bill`

Keep current behavior:

- case C goes to `Settler.Abort`;
- actual cost is zero;
- no idempotency replay record is written by the streaming replay path;
- client may retry with the same idempotency key and get a new claim, matching today's behavior.

This remains the default for backward compatibility.

### `no_bill_record`

Recommended behavior:

- force the case C path through `settleCompletion`, not `Abort`;
- pass the original draft and a `StreamAttempt` whose state remains `Failed`;
- `DefaultSettler.Settle` will preserve `tokens_input` but `CostForAttempt` will return zero because the attempt is non-chargeable;
- write `usage_records.tokens_input = upstream-reported input`, `tokens_output = 0`, `actual_cost = 0`, `stream_state = Failed`, `pending_reconciliation = true`, and a reason like `output_token_zero`;
- write a billing event with zero signed cost;
- if an `Idempotency-Key` was present and the replay capture is within limit, record the exact observed response body, even if empty.

The idempotency behavior needs Owner confirmation. My recommendation is: `no_bill_record` means "do not charge, but finalize and audit the attempt"; therefore the same idempotency key should not re-dispatch and potentially spend upstream input again. If Owner wants "audit record plus free retry", that should be a fourth explicit mode because it combines `Abort` replay semantics with committed audit semantics.

### `bill_input`

Do not implement in the first slice.

Reason:

- Today `Chargeable()` means `StreamStatePartial` only, and `CostForAttempt` returns zero for `Failed` attempts.
- Marking a zero-output case C attempt as `Partial` would corrupt the meaning of `Partial/delivered`.
- Charging a `Failed` attempt requires changing the billing state machine, cost derivation, stream-state comments, tests, receipts, refund/reconciliation assumptions, and possibly quota behavior.

Recommended roadmap path:

- Decide whether to add a new explicit state such as "input-only billable" or to make chargeability policy separate from stream state.
- Update `CostForAttempt` and related tests only after Owner approves the billing-core risk.
- Add receipt copy that clearly says "input tokens billed; zero output delivered; upstream interrupted before terminal marker".
- Only then expose `bill_input` as an accepted admin setting.

Risk classification: high. This touches billing core/state machine, and should not be bundled with the low-risk operator setting/API slice unless Owner explicitly prioritizes it.

## Admin API Design

Recommended route family:

- `GET /admin/v1/billing/settings?tenant_id=1`
- `PUT /admin/v1/billing/settings`

Reason: existing admin usage and billing claims endpoints use `/admin/v1/usage` and `/admin/v1/billing/claims`; this keeps billing-ops surfaces together. If the project later normalizes all admin APIs under `/v1/admin/*`, add an alias deliberately, not silently.

GET response:

```json
{
  "tenant_id": 1,
  "settings": {
    "stream_input_only_interrupted_policy": {
      "value": "no_bill",
      "source": "default",
      "allowed_values": ["no_bill", "no_bill_record"],
      "roadmap_values": ["bill_input"],
      "updated_at": null,
      "updated_by": null
    }
  }
}
```

PUT request:

```json
{
  "tenant_id": 1,
  "stream_input_only_interrupted_policy": "no_bill_record",
  "reason": "Operator wants upstream input burn audited without user charge."
}
```

PUT response:

```json
{
  "tenant_id": 1,
  "updated": ["stream_input_only_interrupted_policy"]
}
```

Validation and auth:

- `platform_admin` may read/write any positive `tenant_id`.
- `tenant_operator` may read/write only its `ScopeTenantID`.
- Customer API keys must never authenticate to this endpoint.
- `tenant_id` is required for `platform_admin`; `tenant_operator` may omit it only if the handler deliberately mirrors `parseTenantScope` behavior. To keep the contract simple, I recommend requiring it in the request body for writes.
- Reject unsupported values with 400. Reject `bill_input` specifically with 409 or 422 and an error code like `billing_policy_value_roadmap` until implemented.
- Require a non-empty `reason` for writes because this setting changes money/retry behavior.
- Write an `admin_audit_events` row with actor, tenant, action `update_billing_settings`, target type `billing_setting`, before/after value, and reason.

## Frontend / Ops Panel Plan

Backend first is feasible and recommended while frontend is frozen:

- The actual runtime behavior depends on backend storage, resolver, and `forwardSSEAndSettle`, not on React UI.
- Operators can use the admin API or a temporary runbook while frontend is frozen.
- The OpenAPI contract can be added now so the eventual UI has a stable shape.
- Existing frontend is explicitly a vertical-closure wedge, not a full Admin UI, so forcing UI work into this slice would add churn without improving billing correctness.

When frontend is unfrozen:

- Add this under Settings -> Billing Policy or Billing -> Settings.
- Use a radio/segmented control for `no_bill` and `no_bill_record`.
- Show `bill_input` disabled with "requires billing-core upgrade" text until implemented.
- Show tenant scope, effective source (`default`/`tenant`), last updated time, actor, and audit link.
- Require confirmation and reason before saving because the setting changes retry/charge behavior.

## Implementation Sub-Phases

### Phase 0 - Owner Decisions

Estimate: 15-30 minutes Owner review.

Decisions needed:

- Approve new `billing_settings` table.
- Approve per-tenant setting.
- Approve initial support for `no_bill` and `no_bill_record` only.
- Decide `no_bill_record` idempotency behavior: committed replay vs audit-plus-free-retry.
- Decide whether the case C predicate is only `UpstreamEOFNoTerminal` or a wider class set.
- Approve `/admin/v1/billing/settings` route.

### Phase 1 - Schema, Store, Resolver

Estimate: 2-3 hours.

Work:

- Add migration for `billing_settings`.
- Add store/resolver/cache.
- Add unit tests for default resolution, enum validation, tenant isolation, cache hit, cache expiry, write invalidation, and cold-read failure fallback.

Blast radius: database schema, sqlc/generator outputs if queries are generated.

### Phase 2 - Admin API

Estimate: 2-3 hours.

Work:

- Add admin handler and route wiring.
- Add OpenAPI schema/path.
- Add admin audit write.
- Add tests for platform admin, tenant operator, cross-tenant deny, invalid enum, `bill_input` roadmap rejection, and missing reason.

Blast radius: admin routing/auth and OpenAPI consistency tests.

### Phase 3 - Streaming Settlement Integration

Estimate: 3-4 hours.

Work:

- Resolve/freeze policy in `chatExecution`.
- Add case C predicate helper.
- Keep `no_bill` behavior unchanged.
- Implement `no_bill_record` by forcing settle with failed/non-chargeable attempt and original upstream input tokens.
- Add tests covering current default, explicit `no_bill`, explicit `no_bill_record`, idempotency behavior, and pending reconciliation.

Blast radius: `forwardSSEAndSettle`, idempotency replay, usage/billing event facts.

### Phase 4 - Verification And Review

Estimate: 1-2 hours.

Checks:

- Targeted Go tests for `internal/billing`, `internal/gatewayhttp`, admin handler tests.
- OpenAPI consistency test if changed.
- Migration up/down smoke if the repo has an existing migration harness.
- Per-commit Codex review before commit, per project rule, when implementation work happens.

### Phase 5 - `bill_input` Roadmap Slice

Estimate: 6-10 additional hours plus Owner review.

Only start after explicit Owner approval. This is a separate high-risk money-path slice:

- Decide new stream chargeability model.
- Update `billing/state.go`, settler tests, receipts, refund/reconciliation expectations, and admin copy.
- Add integration tests proving input-only billing charges exactly input tokens, not output, cache, or inferred values.
- Add migration/comment updates if stream-state semantics change.
- Then allow `bill_input` in API and settings table.

## Success Criteria

For the first implementation slice:

- Missing setting preserves today's default `no_bill` behavior.
- `no_bill` explicitly configured behaves the same as current abort path.
- `no_bill_record` records upstream input usage and zero user cost without marking delivered output.
- The same tenant's admin can read/write its setting; another tenant operator cannot.
- The hot path does not execute a DB query on every request after cache warmup.
- Config lookup failure cannot cause surprise charging.
- Every setting write is auditable with actor, tenant, reason, before/after value.
- `bill_input` is not silently accepted or silently downgraded.

For the later `bill_input` slice:

- Input-only billing produces nonzero cost only from input tokens.
- Stream state/chargeability semantics are explicit and not mislabeled as delivered partial output.
- Receipts and audit views explain why zero-output interrupted streams were charged.

## Blast Radius

- Database schema: new settings table and indexes.
- Billing hot path: stream settlement gate and idempotency replay semantics.
- Billing records: `usage_records`, `billing_events`, and claim status outcomes for case C.
- Admin API: new handler, auth, audit write, OpenAPI.
- Runtime performance: policy cache and refresh behavior.
- UI later: settings panel and confirmation workflow.

`bill_input` expands blast radius to billing state machine, cost derivation, quota/refund assumptions, and receipt semantics; keep it separate unless Owner explicitly accepts the risk.

## Failure Modes And Mitigations

| Failure mode | Risk | Mitigation |
| --- | --- | --- |
| Cross-tenant setting read/write | Tenant leakage and wrong billing behavior | Tenant-scoped table, `adminCanAccessTenant`/`parseTenantScope` style checks, negative tests. |
| DB read per request | Latency and DB load on streaming hot path | Process-local TTL cache, resolve once per request, write invalidation. |
| Config DB outage | Surprise charge or inconsistent behavior | Cold miss falls back to `no_bill`; stale cache used only within bounded window; emit metric/log. |
| Setting changes mid-stream | Same request could settle under a different policy than it started with | Freeze resolved policy in `chatExecution` before upstream dispatch. |
| `no_bill_record` replay surprises clients | Same idempotency key may replay an empty/zero-output stream | Owner decision required; document contract in API/runbook. |
| `bill_input` accepted too early | Operator believes charging occurs but cost remains zero | Reject as roadmap until billing core implements it; tests assert rejection. |
| Mislabel failed input-only stream as `Partial` | Audit/receipt lies about delivered output | Keep failed state for `no_bill_record`; design a separate chargeability model for `bill_input`. |
| Missing admin audit | Operators cannot explain who changed money policy | Require reason and write `admin_audit_events` on every PUT. |
| Multi-pod stale cache | One pod applies old setting briefly | TTL-limited stale window in v1; optional eventbus invalidation later. |

## Decision Points For Owner

1. Approve `billing_settings` as a new tenant-scoped table.
2. Confirm per-tenant setting as the product rule.
3. Confirm initial implementation excludes `bill_input`.
4. Confirm `no_bill_record` should commit a zero-cost usage record and record idempotency replay for the same key.
5. Confirm case C predicate end classes: narrow `UpstreamEOFNoTerminal` only, or broader no-terminal upstream failures.
6. Confirm `/admin/v1/billing/settings` as the admin route.
7. Confirm frontend remains deferred until freeze is lifted.

## Pre-Execution Checklist

- [ ] Owner approves high-risk database migration.
- [ ] Owner decides `no_bill_record` idempotency semantics.
- [ ] Owner confirms `bill_input` deferral or explicitly approves the high-risk billing-core slice.
- [ ] Confirm no reference-project source is needed.
- [ ] Identify current migration number after `0045`.
- [ ] Add tests before or alongside hot-path changes.
- [ ] Run targeted Go tests and OpenAPI consistency checks.
- [ ] Stage implementation and run `codex exec review --uncommitted --full-auto` before any commit, per project rule.

## Final Recommendation

Implement the setting as a per-tenant billing policy now, with `no_bill` default and `no_bill_record` as the first operator-controlled alternative. Use a new `billing_settings` table and a cached backend resolver so the stream hot path does not hit PostgreSQL per request. Defer `bill_input` into a separate Owner-approved billing-core slice because current `CostForAttempt` intentionally returns zero for non-`Partial` states and changing that is a money-path state-machine change, not just an admin setting.

中文总结：建议新建租户级 `billing_settings` 表，不复用 `email_settings`；设置按 tenant 生效，默认 `no_bill` 保持当前行为；第一期只做 `no_bill` 和 `no_bill_record`，其中 `no_bill_record` 走零成本 settle 以记录上游 input token，`bill_input` 因为会改 `billing/state.go` 和计费状态机，建议列入 Mandatory Roadmap 等 Owner 单独批准。前端冻结期内后端 API + OpenAPI + 测试先行可行，UI 解冻后再接操作面板。功能不缩水：三个候选值都被保留为实现/路线图处置；clean-room 风险无，因为不读参考源码；安全风险主要是跨租户设置、热路径缓存陈旧、误收费，计划里用租户校验、默认不收费和审计缓解；需要 Owner 拍板的是新表、per-tenant、`bill_input` 是否延期、`no_bill_record` 幂等语义和 case C 精确定义。
