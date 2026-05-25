# F-MODEL-SUBSTITUTION-001: Model Substitution Engine (A29)

| Field | Value |
| --- | --- |
| Status | Draft |
| Feature ID | F-MODEL-SUBSTITUTION-001 |
| Specifier | Claude (executor-lane), 2026-05-06 |
| Specifier date | 2026-05-06 |
| Reviewer | — |
| Review date | — |
| Released date | — |
| Lane mode | Option C |
| Supersedes | — |
| Superseded by | — |

## Sources

> Reference material consulted by specifier-lane only. Implementer lane MUST NOT open these.

- docs/process/plans/2026-05-02-huakai-algo-upgrade-synthesis.md — §6 Q3 决议 + §6.5 客户响应头 + A29 伪代码（internal plan, no external license)
- docs/process/decisions/DR-009-algorithm-upgrade-policy.md — 决议 #3 Model substitution = C 显式 opt-in + 头部标注；响应头清单

## Capability

Satisfies F-MODEL-SUBSTITUTION-001 (new row to be added to [03_FEATURE_PARITY_MATRIX.md](../03_FEATURE_PARITY_MATRIX.md)):

When a requested model is unavailable (quota exhausted or provider outage) and the operator has **explicitly opted in**, the Gateway transparently substitutes a schema-compatible model from the same family, completes the request, and informs the customer via mandatory response headers. Substitution is **disabled by default**; no substitution occurs without operator configuration. Every substitution is audited with full context.

Related features: [F-POOL-001](pool-routing.md) (account pool selection), [F-PROTO-002](protocol-translation.md) (schema compatibility).

## Actor

- **System** (Gateway request pipeline): evaluates substitution eligibility, executes candidate selection (A29), appends audit row, injects response headers.
- **Operator**: configures `route_policies.allow_substitution` and `route_policies.substitution_audit_required`; reviews audit log.
- **Customer** (API caller): observes `X-Huakai-Model-Substituted` and `X-Huakai-Substitution-Reason` response headers; retains the right to reject substituted responses at the application layer.

## Preconditions

1. An inbound request carries a `model` field specifying the originally requested model.
2. Tenant context and API key resolved; route policy record loaded for the matched route.
3. `route_policies.allow_substitution = true` for the matched route (operator explicit opt-in). If absent or false, the engine is a no-op and the request fails normally.
4. A versioned `model_substitution_table` record exists for the requested model with at least one candidate entry.
5. The Attempt DAG planner (A11) has either reported quota exhaustion on the reserve leg (A09), detected model unavailability after all attempt edges are exhausted, or the operator has pre-configured a direct model route mapping.
6. Customer has not suppressed transparency headers via `X-Huakai-Quiet: true`.

## Normal Path

### Trigger Evaluation

1. After A09 quota reserve fails (reason: `quota_exhausted`) **or** after A11 attempt DAG reports all edges terminal (reason: `model_unavailable`) **or** on initial route resolution where the operator has configured a static model mapping (reason: `operator_route_policy`), the request pipeline invokes the substitution engine.
2. Engine reads `route_policy.allow_substitution`. If `false` or absent — return immediately with no substitution; let the upstream failure propagate normally.

### Candidate Selection (A29)

3. Query `model_substitution_table` for the `from_model` value equal to the originally requested model. The table returns an ordered list of candidate models (`to_model_priority_list`) in descending preference order, together with the current `version` and `active_at` timestamp of the record.
4. Iterate over the candidate list in priority order. For each candidate:
   a. Verify the candidate belongs to the same model family as the original (family identity check prevents cross-vendor silently downgrading to an incompatible provider).
   b. Verify schema compatibility: the candidate must accept the same request schema fields present in the inbound request (e.g., tool definitions, structured output schema, vision payloads). Incompatible candidates are skipped.
   c. Gate on provider account support: check whether the current account pool (F-POOL-001) contains at least one healthy account that supports the candidate model. If none available, skip.
5. The first candidate that passes all three gates (family + schema + account support) is selected as `to_model`.
6. If no candidate passes all gates, substitution is not possible. Return failure; do not silently drop to a random model.

### Substitution Execution

7. Rewrite the request's `model` field to `to_model`. All other request fields are preserved verbatim.
8. Re-enter the normal upstream dispatch pipeline (A11 attempt DAG) with the substituted model. The substitution flag is propagated so A11 does not loop back into substitution on the same request.
9. On successful upstream response, prepend to the outbound response:
   - `X-Huakai-Model-Substituted: <from_model>→<to_model>` (omitted only if customer sent `X-Huakai-Quiet: true`)
   - `X-Huakai-Substitution-Reason: <quota_exhausted | model_unavailable | operator_route_policy>` (same quiet suppression applies)
10. Write one audit row (see Audit section).

## Failure Path

### Failure: substitution_disabled

- **Trigger**: `route_policies.allow_substitution` is `false` or the route policy record has no substitution configuration.
- **Observable outcome**: request fails with the same error that triggered substitution evaluation (quota error or provider error). No substitution headers emitted.
- **Operator-visible signal**: standard upstream failure log entry; no substitution audit row written. Operator can enable substitution by setting `allow_substitution = true` on the route policy.

### Failure: no_candidates_in_table

- **Trigger**: `model_substitution_table` has no row for `from_model`, or the `to_model_priority_list` is empty.
- **Observable outcome**: substitution engine returns immediately with no candidate; upstream failure propagates to customer.
- **Operator-visible signal**: warning log entry `SUBST_NO_CANDIDATES` with `{from_model, route_policy_id}`; no audit row written.

### Failure: all_candidates_gated_out

- **Trigger**: Every candidate in `to_model_priority_list` fails at least one gate (family mismatch, schema incompatibility, or no supporting account in pool).
- **Observable outcome**: substitution engine returns no result; upstream failure propagates.
- **Operator-visible signal**: log entry `SUBST_ALL_GATED {from_model, candidates_evaluated, gate_failures[]}` at WARN level.

### Failure: substituted_model_also_fails

- **Trigger**: The selected `to_model` is dispatched but the upstream call also fails (provider error, quota on substituted model, etc.).
- **Observable outcome**: error returned to customer. The substitution headers are **not** emitted (substitution did not complete successfully).
- **Operator-visible signal**: audit row written with `outcome = failed`; standard attempt audit from A11 covers the inner failure.

## Operator Recovery

- **Enable substitution**: set `route_policies.allow_substitution = true` on the target route via admin API; no restart required; takes effect on next request.
- **Add or reorder candidates**: update the `model_substitution_table` row for the relevant `from_model`, incrementing `version`; the new version is picked up immediately (no cache beyond request lifetime).
- **Disable after unexpected substitution**: set `allow_substitution = false`; existing in-flight requests complete normally, future requests stop substituting.
- **Audit review**: query substitution audit rows by `route_policy_version` to correlate a table version with substitution volume and outcomes.

## Data Structures

### model_substitution_table

| Column | Type | Notes |
|---|---|---|
| from_model | text PK | Canonical model identifier (e.g. `gpt-4o`) |
| to_model_priority_list | jsonb | Ordered array of candidate model identifiers, highest preference first |
| version | integer | Monotonically increasing; used in audit rows and Merkle snapshot |
| active_at | timestamptz | When this version became active |
| merkle_hash | text | SHA-256 hash of `(from_model, to_model_priority_list, version)` — same Merkle-chain pattern as A15 pricing snapshots; prevents silent table tampering |
| superseded_at | timestamptz | Nullable; set when a newer version activates |

The Merkle hash chain is maintained identically to A15 pricing snapshots: each new version's `merkle_hash` covers its own content plus the previous version's `merkle_hash`, forming a tamper-evident log. Audit rows reference `snapshot_id` = `(from_model, version)` to pin the exact table state at time of substitution.

### route_policies extensions

Two boolean columns added to the existing `route_policies` table:

| Column | Type | Default | Notes |
|---|---|---|---|
| allow_substitution | boolean | false | Operator explicit opt-in (Q3 decision C). Must be `true` for any substitution to occur. |
| substitution_audit_required | boolean | true | When `true`, a missing audit row for a substituted request is treated as a pipeline fault. Operators may set to `false` only for low-criticality routes. |

## Audit / Usage / Log Evidence

Every completed substitution (outcome `succeeded` or `failed`) writes one audit row to the substitution audit table:

| Field | Type | Notes |
|---|---|---|
| id | uuid | Primary key |
| request_id | text | Inbound logical request identifier |
| from_model | text | Originally requested model |
| to_model | text | Selected substitute model (null if substitution was not possible) |
| reason | enum | `quota_exhausted`, `model_unavailable`, `operator_route_policy` |
| outcome | enum | `succeeded`, `failed`, `skipped` (skipped = engine ran but no candidate passed gates) |
| route_policy_id | uuid | Route policy that authorized substitution |
| route_policy_version | integer | Version of route policy at time of substitution |
| snapshot_id | text | `<from_model>/<version>` — pins the exact model_substitution_table state used |
| gates_evaluated | jsonb | Array of `{candidate, gate, result}` records for observability |
| created_at | timestamptz | Wall time of substitution decision |

When `route_policies.substitution_audit_required = true`, the pipeline asserts that an audit row was committed before returning the response to the customer. Failure to write the audit row is treated as an internal error (500) rather than silently serving the response without a record.

## Acceptance Test Direction

### AT-MODELSUBST-001 — Substitution disabled by default

Setup: route policy with no `allow_substitution` field (or `false`). Trigger A09 quota reserve failure.
Expected: request returns upstream quota error; no `X-Huakai-Model-Substituted` header; no audit row written.

### AT-MODELSUBST-002 — Normal path quota_exhausted substitution

Setup: `allow_substitution = true`; `model_substitution_table` row for `gpt-4o` → `[gpt-4o-mini]`; pool has healthy account supporting `gpt-4o-mini`. Trigger A09 quota reserve failure for `gpt-4o`.
Expected: request completes via `gpt-4o-mini`; response carries `X-Huakai-Model-Substituted: gpt-4o→gpt-4o-mini` and `X-Huakai-Substitution-Reason: quota_exhausted`; one audit row with `outcome = succeeded`.

### AT-MODELSUBST-003 — Normal path model_unavailable substitution

Setup: same as AT-MODELSUBST-002 but trigger via A11 all-edges-terminal (provider 503 on all attempts for `gpt-4o`).
Expected: identical headers with `X-Huakai-Substitution-Reason: model_unavailable`; audit row `reason = model_unavailable`.

### AT-MODELSUBST-004 — Quiet suppression

Setup: AT-MODELSUBST-002 conditions plus customer sends `X-Huakai-Quiet: true`.
Expected: substitution still occurs and succeeds; response does NOT carry `X-Huakai-Model-Substituted` or `X-Huakai-Substitution-Reason`; audit row still written (quiet only suppresses headers, not audit).

### AT-MODELSUBST-005 — All candidates gated out

Setup: `allow_substitution = true`; candidate list contains one entry but pool has no account supporting it (account.supports returns false).
Expected: substitution engine returns no candidate; upstream failure propagates; log contains `SUBST_ALL_GATED`; no `X-Huakai-Model-Substituted` header; audit row with `outcome = skipped`.

### AT-MODELSUBST-006 — Merkle hash tamper detection

Setup: manually modify a `to_model_priority_list` value in `model_substitution_table` without updating `merkle_hash`.
Expected: pipeline detects hash mismatch on load; logs `SUBST_TABLE_INTEGRITY_FAIL`; treats the corrupted row as absent (substitution disabled for that `from_model`); alert emitted to operator.

## Open Questions

1. **Family definition boundary**: which models constitute the same "family" for gate (a)? Suggested: family determined by a `model_family` column on the candidate list entries in `to_model_priority_list` jsonb rather than inferred from model name strings. Implementer lane should confirm with operator team before coding the gate.
2. **Schema compatibility check granularity**: full field-level diff vs. capability flags (`supports_tools`, `supports_vision`, `supports_structured_output`)? Capability flags are cheaper but may miss edge cases. Recommend capability flags at MVP with a fallback reject on unrecognised request fields.
3. **operator_route_policy trigger timing**: should static model mapping (reason: `operator_route_policy`) be evaluated before A09 reserve (pre-call rewrite) or only as a fallback after failure? Pre-call rewrite avoids wasting quota reserve on a model the operator has already decided to redirect; post-failure is simpler. Recommend pre-call rewrite for explicit static mappings.
4. **A29 interaction with A11 attempt DAG edge typing**: synthesis §1 lists `model_subst` as an edge type in the attempt DAG. Does the substitution engine insert a `model_subst` edge into A11's DAG, or does it run outside the DAG and restart it? Recommend: insert edge so attempt accounting covers the substituted attempt correctly.

## Implementer Notes (added by implementer lane)

> This section is filled by the implementer after consuming the spec, NOT by the specifier.

- (vacant)
