# HUAKAI Phase 2.2 OpenAPI Final Review

| Field | Value |
| --- | --- |
| Review target | `docs/openapi/openapi.yaml` |
| Target size | 1138 lines, synthesized from 3 independent drafts |
| Reviewer | Codex final reviewer for HUAKAI Phase 2.2 |
| Review date | 2026-04-29 |
| Review mode | THOROUGH; not escalated to ADVERSARIAL |
| Final verdict | APPROVE-WITH-FIXES |

## 0. Review Method

1. Pre-commitment predictions before detailed reading:
2. Prediction A: schema attribution gaps would likely exist because synthesis merged three drafts.
3. Prediction B: enum drift would likely exist where SQL fragments already carry stricter checks.
4. Prediction C: OpenAPI 3.1 nullable syntax might regress to `nullable: true`.
5. Prediction D: admin RBAC metadata might be incomplete on some endpoints.
6. Prediction E: synthesis decisions might be documented but not fully applied.
7. Actual result for A: confirmed; 12 schema components lack `x-huakai-spec-source`.
8. Actual result for B: confirmed for `UsageRecord.end_class`; provider-account enums pass.
9. Actual result for C: not confirmed; no `nullable:` occurrences found.
10. Actual result for D: not confirmed; all admin operations carry `x-huakai-required-role`.
11. Actual result for E: mostly not confirmed; D1-D9 are applied.
12. Verification inputs:
13. `docs/openapi/openapi.yaml`
14. `docs/openapi/SYNTHESIS.md`
15. `docs/specs/_REVIEW_CHECKLIST.md`
16. `docs/specs/pool-routing.md`
17. `docs/specs/observability-billing.md`
18. `docs/specs/streaming-forwarder.md`
19. `docs/specs/rate-limiting.md`
20. `docs/specs/protocol-translation.md`
21. `docs/specs/upstream-credential-management.md`
22. `docs/schema/pool-routing.sql`
23. `docs/schema/observability-billing.sql`
24. `docs/schema/protocol-translation.sql`
25. `docs/schema/rate-limiting.sql`
26. `docs/schema/upstream-credential-management.sql`
27. `docs/03_FEATURE_PARITY_MATRIX.md`
28. Structural checks run:
29. Parsed YAML with PyYAML successfully.
30. Counted 27 schema components.
31. Verified all 102 local `$ref` targets under `#/components/schemas/*` resolve.
32. Verified all schema `required` fields are present in each component's `properties`.
33. Grepped for `nullable:` and found zero occurrences.
34. Grepped admin operations for `x-huakai-required-role`.
35. Compared provider-account enums with `pool-routing.sql` CHECK constraints.
36. Compared usage/billing money fields with `observability-billing.sql`.
37. Compared `UsageRecord.end_class` with streaming taxonomy in released spec and schema.
38. Compared feature IDs referenced by the OpenAPI with `docs/03_FEATURE_PARITY_MATRIX.md`.
39. Checked SYNTHESIS D1-D9 against the actual YAML.
40. Reviewed the result through executor, stakeholder, and skeptic lenses.

## 1. CL-001..011 Verdict Matrix

| Check | Verdict | Evidence | Notes |
| --- | --- | --- | --- |
| CL-001 | PASS | `rg` found no Sub2API/non-MIT function, handler, config constant, or source-specific identifier names in `openapi.yaml`. | Generic public protocol terms such as OpenAI, Anthropic, relay, quota, channel, and provider are acceptable project-domain terms here. |
| CL-002 | PASS | Schema names and fields are HUAKAI domain names or local SQL fragment names. `ProviderAccount.account_type` aligns with `docs/schema/pool-routing.sql:115`. | No copied upstream table or migration names detected. |
| CL-003 | PASS | OpenAPI contract has no UI component names, class names, or dashboard layout source terms. | Not a UI artifact. |
| CL-004 | PASS | No non-MIT reference prose or source URLs are embedded in implementer-facing OpenAPI sections. | Public protocol names are descriptive, not upstream prose. |
| CL-005 | PASS | The contract defines HTTP shapes, schemas, and metadata; it does not contain algorithmic pseudocode that maps to upstream code. | Behavioral details are delegated to released HUAKAI specs. |
| CL-006 | PASS | `openapi.yaml` has no `Sources` field and cites only local released specs through `x-huakai-spec-source`. | Source-license verification belongs to the released spec files; this contract does not embed upstream sources. |
| CL-007 | PASS | Referenced released specs carry lane modes in their headers; the OpenAPI itself does not introduce lane-mode claims. | `pool-routing`, `observability-billing`, `streaming-forwarder`, and `rate-limiting` are Option C; `protocol-translation` and `upstream-credential-management` are Option B. |
| CL-008 | PASS | Referenced IDs in `openapi.yaml`: F-GW-002, F-PROTO-002, F-AUTH-005, F-POOL-001, F-OBS-001, F-RATE-001. All exist in `docs/03_FEATURE_PARITY_MATRIX.md`. | F-BILL-001 is referenced through F-OBS-001 framing in the matrix, not as a direct OpenAPI feature tag. |
| CL-009 | PASS | `openapi.yaml` does not declare unresolved behavior; `SYNTHESIS.md` says D1-D9 and Q-C/Q-G open questions are resolved. | No Open Questions hold signal remains in the contract. |
| CL-010 | PASS | No upstream source URL appears in implementer-relevant OpenAPI sections. | The only external URL is the MIT license URL under `info.license`, not a reference source. |
| CL-011 | FAIL | Missing `x-huakai-spec-source` on schema components at `openapi.yaml:634`, `644`, `773`, `824`, `840`, `859`, `931`, `954`, `979`, `1036`, `1084`, `1119`. | This violates the Phase 2.2 explicit requirement that every schema component cite a Released spec. |

CL-011 failure is bounded and mechanical, but it blocks direct released status.

## 2. Spot-Check Log

### Spot Check 1: YAML parses and internal schema refs resolve

1. Result: PASS.
2. Evidence: PyYAML parsed `docs/openapi/openapi.yaml` without error.
3. Evidence: 27 schema components detected.
4. Evidence: 102 local schema `$ref` entries checked.
5. Evidence: zero missing schema refs.
6. Evidence: zero `required` properties missing from local schema definitions.
7. Impact: codegen will not fail on basic unresolved schema references.
8. Residual risk: this is not a full OpenAPI validator pass; it checks structure, not every OpenAPI semantic rule.

### Spot Check 2: CL-008 feature IDs exist in parity matrix

1. Result: PASS.
2. `openapi.yaml:49` references F-GW-002, F-PROTO-002, F-AUTH-005.
3. `openapi.yaml:51` references F-POOL-001.
4. `openapi.yaml:53` references F-POOL-001, F-AUTH-005, F-RATE-001.
5. `openapi.yaml:55` references F-OBS-001.
6. `openapi.yaml:57` references F-OBS-001.
7. `openapi.yaml:59` references F-RATE-001, F-POOL-001, F-AUTH-005.
8. `docs/03_FEATURE_PARITY_MATRIX.md:38` contains F-GW-002.
9. `docs/03_FEATURE_PARITY_MATRIX.md:48` contains F-OBS-001.
10. `docs/03_FEATURE_PARITY_MATRIX.md:64` contains F-PROTO-002.
11. `docs/03_FEATURE_PARITY_MATRIX.md:68` contains F-RATE-001.
12. `docs/03_FEATURE_PARITY_MATRIX.md:71` contains F-AUTH-005.
13. `docs/03_FEATURE_PARITY_MATRIX.md:73` contains F-POOL-001.
14. No unknown feature ID was found.

### Spot Check 3: CL-011 schema source attribution

1. Result: FAIL.
2. Evidence: the OpenAPI header says every component cites released specs through `x-huakai-spec-source`.
3. Evidence: `openapi.yaml:8` states `x-huakai-spec-source extension`.
4. Evidence: `openapi.yaml:634` `ChatMessage:` has no `x-huakai-spec-source`.
5. Evidence: `openapi.yaml:644` `ToolSpec:` has no `x-huakai-spec-source`.
6. Evidence: `openapi.yaml:773` `PageMeta:` has no `x-huakai-spec-source`.
7. Evidence: `openapi.yaml:824` `PoolGroupCreate:` has no `x-huakai-spec-source`.
8. Evidence: `openapi.yaml:840` `PoolGroupUpdate:` has no `x-huakai-spec-source`.
9. Evidence: `openapi.yaml:859` `PoolGroupList:` has no `x-huakai-spec-source`.
10. Evidence: `openapi.yaml:931` `ProviderAccountCreate:` has no `x-huakai-spec-source`.
11. Evidence: `openapi.yaml:954` `ProviderAccountUpdate:` has no `x-huakai-spec-source`.
12. Evidence: `openapi.yaml:979` `ProviderAccountList:` has no `x-huakai-spec-source`.
13. Evidence: `openapi.yaml:1036` `UsageRecordList:` has no `x-huakai-spec-source`.
14. Evidence: `openapi.yaml:1084` `BillingLedgerClaimList:` has no `x-huakai-spec-source`.
15. Evidence: `openapi.yaml:1119` `AuditEventList:` has no `x-huakai-spec-source`.
16. This is not cosmetic because Phase 3 implementers will use components as the codegen boundary.

### Spot Check 4: Provider Account enum alignment with pool-routing SQL

1. Result: PASS.
2. `docs/schema/pool-routing.sql:115` has `account_type IN ('oauth', 'api_key', 'service_account', 'upstream_static')`.
3. `openapi.yaml:896-899` has the same `ProviderAccount.account_type` enum.
4. `openapi.yaml:939-942` has the same `ProviderAccountCreate.account_type` enum.
5. `docs/schema/pool-routing.sql:120-122` has `health_state IN ('operational', 'degraded', 'failed', 'cooling_down', 'error')`.
6. `openapi.yaml:901-903` has the same `ProviderAccount.health_state` enum.
7. `docs/schema/pool-routing.sql:124-125` has `credential_state IN ('valid', 'refreshing', 'refreshing_with_grace', 'refresh_failed', 'revoked')`.
8. `openapi.yaml:904-906` has the same `ProviderAccount.credential_state` enum.
9. This satisfies the requested enum alignment check.

### Spot Check 5: Money field typing and precision

1. Result: PASS.
2. `docs/schema/observability-billing.sql:42` defines `predicted_cost numeric(20,8)`.
3. `docs/schema/observability-billing.sql:43` defines nullable `actual_cost numeric(20,8)` on claims.
4. `docs/schema/observability-billing.sql:139` defines `usage_records.actual_cost numeric(20,8)`.
5. `openapi.yaml:1012-1015` defines `UsageRecord.actual_cost` as string with pattern `^-?[0-9]+(\.[0-9]{1,8})?$`.
6. `openapi.yaml:1072-1074` defines `BillingLedgerClaim.predicted_cost` as string with the same 8-decimal pattern.
7. `openapi.yaml:1075-1077` defines `BillingLedgerClaim.actual_cost` as `[string, 'null']` with the same 8-decimal pattern.
8. This matches the Owner requirement that money fields be string-typed with regex pattern.
9. Note: the pattern allows negative values, which is correct for signed adjustments but broad for predicted claims.
10. I am not scoring that as a finding because SQL and specs permit signed adjustment rows and the contract is not modeling separate adjustment entries yet.

### Spot Check 6: OpenAPI 3.1 nullable syntax

1. Result: PASS.
2. `rg "nullable:" docs/openapi/openapi.yaml` returned zero matches.
3. Nullable shapes use JSON Schema 3.1 unions such as `type: [string, 'null']`.
4. Examples include `openapi.yaml:778`, `779`, `899`, `911`, `912`, `913`, `914`, `915`, `916`, and `1021`.
5. This applies SYNTHESIS D2.

### Spot Check 7: Admin endpoint role metadata

1. Result: PASS.
2. All admin operations carry `x-huakai-required-role`.
3. `openapi.yaml:207-208` list pools: `tenant_operator`.
4. `openapi.yaml:224-225` create pool: `tenant_operator`.
5. `openapi.yaml:248-249` get pool: `tenant_operator`.
6. `openapi.yaml:265-266` update pool: `tenant_operator`.
7. `openapi.yaml:290-294` list provider accounts: `tenant_operator`.
8. `openapi.yaml:315-317` create provider account: `tenant_operator`.
9. `openapi.yaml:338-342` get provider account: `tenant_operator`.
10. `openapi.yaml:354-357` update provider account: `tenant_operator`.
11. `openapi.yaml:384-385` clear rate limit: `tenant_operator`.
12. `openapi.yaml:400-401` list usage records: `tenant_operator`.
13. `openapi.yaml:435-436` list billing claims: `tenant_operator`.
14. `openapi.yaml:461-465` list audit events: `tenant_operator`.
15. `openapi.yaml:494-495` DLQ replay: `platform_admin`.
16. This applies SYNTHESIS D9.

### Spot Check 8: Usage Record end_class taxonomy

1. Result: FAIL.
2. `docs/specs/streaming-forwarder.md:70-85` defines a typed stream-end taxonomy.
3. `docs/schema/observability-billing.sql:146-154` constrains persisted `usage_records.end_class`.
4. SQL values are `stream_end_graceful`, `stream_end_no_terminal_marker`, `upstream_error_4xx`, `upstream_error_5xx`, `upstream_rate_limit`, `upstream_auth_failure`, `first_token_timeout`, `inter_event_timeout`, `total_stream_timeout`, `client_disconnect`, `event_size_exceeded`, `orchestrator_cancelled`, `usage_ambiguous`, `unknown_termination`, and `non_streaming`.
5. `openapi.yaml:1016` currently says only `end_class: { type: string }`.
6. This fails the specific Owner request to verify `Usage Record end_class enum matches streaming-forwarder.md §Phase C taxonomy`.
7. It also fails SQL alignment because the API can emit values the database will reject or analytics cannot group.

### Spot Check 9: Usage source taxonomy

1. Result: PASS.
2. `docs/specs/streaming-forwarder.md:59` says usage source is a closed enum: `reported`, `normalized`, `inferred`, `partial`, `ambiguous`.
3. `docs/schema/observability-billing.sql:156-158` repeats that CHECK constraint.
4. `openapi.yaml:1017-1019` has the same `UsageRecord.usage_source` enum.
5. This is correctly aligned.

### Spot Check 10: Protocol loss schema and header

1. Result: PASS.
2. `SYNTHESIS.md:64-72` resolves protocol loss as both response header and Usage Record body.
3. `docs/specs/protocol-translation.md:83-85` requires `protocol_loss` array plus `X-HUAKAI-Protocol-Loss` header.
4. `openapi.yaml:101-106`, `146-147`, and `184-185` define the response header on all gateway endpoints.
5. `openapi.yaml:1027-1029` exposes `UsageRecord.protocol_loss` as an array of `ProtocolLossEntry`.
6. `openapi.yaml:1044-1055` defines `ProtocolLossEntry`.
7. `openapi.yaml:1051-1054` constrains direction and verdict.
8. This applies SYNTHESIS D5.

### Spot Check 11: Retry-After coverage

1. Result: PASS.
2. `SYNTHESIS.md:74-82` resolves Retry-After only on 429 and 503, not 402.
3. `openapi.yaml:562-568` `PaymentRequired` explicitly says no Retry-After.
4. `openapi.yaml:588-596` `RateLimited` defines a `Retry-After` header.
5. `openapi.yaml:597-607` `ServiceBusy` defines a `Retry-After` header.
6. This applies SYNTHESIS D6.

### Spot Check 12: Gateway request permissiveness and admin strictness

1. Result: PASS.
2. `SYNTHESIS.md:45-52` resolves permissive gateway request bodies and strict admin request bodies.
3. `openapi.yaml:612-615` `ChatCompletionsRequest` has `additionalProperties: true`.
4. `openapi.yaml:663-665` `ResponsesRequest` has `additionalProperties: true`.
5. `openapi.yaml:694-696` `AnthropicMessagesRequest` has `additionalProperties: true`.
6. `openapi.yaml:824-826` `PoolGroupCreate` has `additionalProperties: false`.
7. `openapi.yaml:840-842` `PoolGroupUpdate` has `additionalProperties: false`.
8. `openapi.yaml:931-933` `ProviderAccountCreate` has `additionalProperties: false`.
9. `openapi.yaml:954-956` `ProviderAccountUpdate` has `additionalProperties: false`.
10. This applies SYNTHESIS D3.

### Spot Check 13: Pagination decision

1. Result: PASS.
2. `SYNTHESIS.md:54-62` resolves structured `page` object.
3. `openapi.yaml:773-780` defines `PageMeta`.
4. List schemas consistently use `items` plus `page`.
5. Examples: `openapi.yaml:859-865`, `979-985`, `1036-1042`, `1084-1090`, `1119-1125`.
6. This applies SYNTHESIS D4.
7. The only flaw is attribution on `PageMeta` and wrapper list schemas, already covered under CL-011.

### Spot Check 14: SSE event typing deferral

1. Result: PASS.
2. `SYNTHESIS.md:84-92` defers typed SSE event schemas to Phase 2.3.
3. `openapi.yaml:737-739` defines `SSEFrame` as a string frame citing streaming-forwarder and protocol-translation.
4. Gateway `text/event-stream` responses reference `SSEFrame`.
5. This applies SYNTHESIS D7.

### Spot Check 15: Idempotency cache hit header

1. Result: PASS.
2. `SYNTHESIS.md:94-99` resolves echo through `X-HUAKAI-Idempotency-Hit: true`.
3. `openapi.yaml:104-106` defines this header on Chat Completions.
4. `openapi.yaml:147` defines it on Responses.
5. `openapi.yaml:185` defines it on Anthropic Messages.
6. This applies SYNTHESIS D8.

### Spot Check 16: Pool routing fields

1. Result: PASS.
2. `docs/schema/pool-routing.sql:59` constrains `top_k_default BETWEEN 1 AND 10`.
3. `openapi.yaml:831` has `top_k_default` minimum 1 and maximum 10.
4. `docs/schema/pool-routing.sql:62` constrains `capability_default` to `exact_capability_only` or `safe_equivalent_allowed`.
5. `openapi.yaml:832-834` has the same enum.
6. `openapi.yaml:835-838` exposes sticky session and mid-stream failover settings.
7. This is aligned with F-POOL-001 and SYNTHESIS Q-C1.

### Spot Check 17: Upstream credential account types

1. Result: PASS.
2. `docs/specs/upstream-credential-management.md:36` requires account types `oauth`, `api_key`, `service_account`, `upstream_static`.
3. `openapi.yaml:896-899` and `939-942` match exactly.
4. This aligns F-AUTH-005 with F-POOL-001 schema.

### Spot Check 18: Audit event shape

1. Result: PASS with attribution fix needed on wrapper only.
2. `openapi.yaml:1092-1117` defines `AuditEvent` with `event_type`, `actor_type`, `actor_id`, `target_type`, `target_id`, `payload`, and `occurred_at`.
3. `openapi.yaml:1095-1098` cites `observability-billing`, `pool-routing`, `rate-limiting`, and `upstream-credential-management`.
4. `openapi.yaml:1119-1125` `AuditEventList` lacks its own `x-huakai-spec-source`.
5. The base entity is attributed; the list wrapper is not.

## 3. Findings

### Critical Findings

None.

No finding rose to CRITICAL after realist check. The current contract should not be locked as Released, but the defects are bounded schema-contract fixes rather than a structural collapse of the external HTTP surface.

### Major Findings

1. CL-011 is violated by 12 schema components missing `x-huakai-spec-source`.
   - Confidence: HIGH.
   - Evidence: `openapi.yaml:634` `ChatMessage:`, `644` `ToolSpec:`, `773` `PageMeta:`, `824` `PoolGroupCreate:`, `840` `PoolGroupUpdate:`, `859` `PoolGroupList:`, `931` `ProviderAccountCreate:`, `954` `ProviderAccountUpdate:`, `979` `ProviderAccountList:`, `1036` `UsageRecordList:`, `1084` `BillingLedgerClaimList:`, `1119` `AuditEventList:`.
   - Why this matters: Phase 3 codegen will treat these components as first-class contract surfaces. Missing provenance weakens clean-room traceability exactly at the boundary implementers consume.
   - Fix: add `x-huakai-spec-source` arrays to every missing component, using the replacement list in §4.
   - Realist check: not CRITICAL because it is metadata-only and does not change runtime behavior, but it blocks release because CL-011 is explicit and mechanical.

2. `UsageRecord.end_class` is unconstrained despite released spec and SQL defining a closed taxonomy.
   - Confidence: HIGH.
   - Evidence: `openapi.yaml:1016` has `end_class: { type: string }`.
   - Evidence: `docs/specs/streaming-forwarder.md:70-85` defines typed stream terminal classes.
   - Evidence: `docs/schema/observability-billing.sql:146-154` constrains `usage_records.end_class`.
   - Why this matters: codegen from the current YAML will produce a loose string field, letting clients and admin UI accept values the database rejects and metrics cannot aggregate consistently.
   - Fix: replace `end_class: { type: string }` with the closed enum in §4.
   - Realist check: not CRITICAL because implementation could still validate server-side, but Phase 3 explicitly plans to codegen from the YAML, so leaving the contract loose causes real downstream rework.

### Minor Findings

1. `UsageRecord.drain_outcome` is loose.
   - Evidence: `openapi.yaml:1025` has `drain_outcome: { type: [string, 'null'] }`.
   - Evidence: `docs/specs/streaming-forwarder.md:91-94` defines three drain budgets and records which budget triggers exit.
   - Evidence: `docs/schema/observability-billing.sql:161` documents `max_seconds`, `max_bytes`, `max_estimated_cost`, or null.
   - Why this matters: this is less central than `end_class`, but tightening it now improves generated admin types and dashboard filters.
   - Fix: change it to an enum union as listed in §4.

2. The review should not be upgraded to direct release until a real OpenAPI semantic validator is run after fixes.
   - Evidence: I performed YAML parse and internal `$ref` checks, not a full `redocly`/`spectral` validation.
   - Why this matters: OpenAPI 3.1 has edge cases beyond YAML and `$ref` validity.
   - Fix: run the repo's chosen validator in Phase 2.2 closeout or Phase 3 skeleton bootstrap.

## 4. Final Verdict

FINAL VERDICT: APPROVE-WITH-FIXES.

Meaning: do not flip `docs/specs/api-contract.md` to `Status=Released` and do not start Phase 3 codegen from the current file until the bounded fixes below are applied.

The contract is close enough that it does not require replanning or re-synthesis.

The fix list is bounded to three items.

### Bounded Fix 1: Add missing `x-huakai-spec-source` to 12 schema components

Apply these exact source mappings:

1. Under `ChatMessage:`, add:

```yaml
      x-huakai-spec-source: [docs/specs/protocol-translation.md]
```

2. Under `ToolSpec:`, add:

```yaml
      x-huakai-spec-source: [docs/specs/protocol-translation.md]
```

3. Under `PageMeta:`, add:

```yaml
      x-huakai-spec-source:
        - docs/specs/pool-routing.md
        - docs/specs/observability-billing.md
```

4. Under `PoolGroupCreate:`, add:

```yaml
      x-huakai-spec-source: [docs/specs/pool-routing.md]
```

5. Under `PoolGroupUpdate:`, add:

```yaml
      x-huakai-spec-source: [docs/specs/pool-routing.md]
```

6. Under `PoolGroupList:`, add:

```yaml
      x-huakai-spec-source: [docs/specs/pool-routing.md]
```

7. Under `ProviderAccountCreate:`, add:

```yaml
      x-huakai-spec-source:
        - docs/specs/pool-routing.md
        - docs/specs/upstream-credential-management.md
```

8. Under `ProviderAccountUpdate:`, add:

```yaml
      x-huakai-spec-source:
        - docs/specs/pool-routing.md
        - docs/specs/upstream-credential-management.md
        - docs/specs/rate-limiting.md
```

9. Under `ProviderAccountList:`, add:

```yaml
      x-huakai-spec-source:
        - docs/specs/pool-routing.md
        - docs/specs/upstream-credential-management.md
        - docs/specs/rate-limiting.md
```

10. Under `UsageRecordList:`, add:

```yaml
      x-huakai-spec-source: [docs/specs/observability-billing.md]
```

11. Under `BillingLedgerClaimList:`, add:

```yaml
      x-huakai-spec-source: [docs/specs/observability-billing.md]
```

12. Under `AuditEventList:`, add:

```yaml
      x-huakai-spec-source:
        - docs/specs/observability-billing.md
        - docs/specs/pool-routing.md
        - docs/specs/rate-limiting.md
        - docs/specs/upstream-credential-management.md
```

Acceptance check for Fix 1:

```powershell
@'
import yaml, pathlib
data = yaml.safe_load(pathlib.Path("docs/openapi/openapi.yaml").read_text(encoding="utf-8"))
missing = [
    name for name, schema in data["components"]["schemas"].items()
    if isinstance(schema, dict) and "x-huakai-spec-source" not in schema
]
print(missing)
'@ | python -
```

Expected output:

```text
[]
```

### Bounded Fix 2: Replace loose `UsageRecord.end_class` with the released taxonomy enum

Replace:

```yaml
        end_class: { type: string }
```

With:

```yaml
        end_class:
          type: string
          enum:
            - stream_end_graceful
            - stream_end_no_terminal_marker
            - upstream_error_4xx
            - upstream_error_5xx
            - upstream_rate_limit
            - upstream_auth_failure
            - first_token_timeout
            - inter_event_timeout
            - total_stream_timeout
            - client_disconnect
            - event_size_exceeded
            - orchestrator_cancelled
            - usage_ambiguous
            - unknown_termination
            - non_streaming
```

Acceptance check for Fix 2:

```powershell
@'
import yaml, pathlib
data = yaml.safe_load(pathlib.Path("docs/openapi/openapi.yaml").read_text(encoding="utf-8"))
print(data["components"]["schemas"]["UsageRecord"]["properties"]["end_class"])
'@ | python -
```

Expected output includes all 15 values from `docs/schema/observability-billing.sql:146-154`.

### Bounded Fix 3: Tighten `UsageRecord.drain_outcome`

Replace:

```yaml
        drain_outcome: { type: [string, 'null'] }
```

With:

```yaml
        drain_outcome:
          type: [string, 'null']
          enum: [max_seconds, max_bytes, max_estimated_cost, null]
```

Acceptance check for Fix 3:

```powershell
@'
import yaml, pathlib
data = yaml.safe_load(pathlib.Path("docs/openapi/openapi.yaml").read_text(encoding="utf-8"))
print(data["components"]["schemas"]["UsageRecord"]["properties"]["drain_outcome"])
'@ | python -
```

Expected output includes `max_seconds`, `max_bytes`, `max_estimated_cost`, and `None`.

## 5. Additional Review Notes

1. D1 snake_case convention is applied.
2. I found no camelCase request or response property drift beyond expected OpenAPI component names and operation IDs.
3. D2 nullable syntax is applied.
4. D3 gateway permissive / admin strict split is applied.
5. D4 structured `page` pagination is applied.
6. D5 protocol loss appears both as response header and Usage Record field.
7. D6 Retry-After appears on 429 and 503, not 402.
8. D7 typed SSE schema deferral is applied through `SSEFrame`.
9. D8 idempotency cache-hit header is applied on all gateway endpoints.
10. D9 admin role extension is applied on all admin endpoints.
11. Provider Account account type, health state, and credential state align with SQL.
12. Usage source aligns with streaming-forwarder and SQL.
13. Money fields are string-typed with 8-decimal regex.
14. No `nullable: true` syntax exists.
15. No non-MIT upstream source URL or source path is embedded in `openapi.yaml`.
16. No direct upstream implementation identifier was detected.
17. No unresolved schema `$ref` was detected.
18. No `required` field points to a missing property.
19. The two release-blocking issues are bounded enough for fix-and-release, not reject-and-redesign.
20. After fixes, rerun YAML parse, internal `$ref` check, CL-011 missing-source script, and a full OpenAPI semantic validator if available.

## 6. Multi-Perspective Notes

Executor perspective:

1. A Phase 3 executor can codegen most of this contract without confusion.
2. The executor will get weak generated types for `end_class` unless Fix 2 is applied.
3. The executor will lose traceability for wrapper and nested schemas unless Fix 1 is applied.
4. Admin role metadata is clear enough for route middleware scaffolding.
5. The bearer security model is clear enough for initial skeleton.

Stakeholder perspective:

1. The external surface preserves the released Phase 2.2 scope.
2. No feature appears silently dropped.
3. Gateway compatibility choices are pragmatic.
4. Admin surfaces cover pools, accounts, usage, billing, audit events, and DLQ replay.
5. The current release risk is contract precision, not missing product scope.

Skeptic perspective:

1. The strongest argument against release is that CL-011 was explicitly claimed in the file header but not true for every schema.
2. The second strongest argument is that codegen from loose enums creates rework exactly where analytics and billing operations need stable values.
3. Those arguments are valid, but bounded.
4. They do not justify rejecting the entire synthesis.
5. They do justify blocking immediate `Released` status.

## 7. Self-Audit and Realist Check

1. Major Finding 1 confidence: HIGH.
2. Major Finding 1 refutable by author with missing context: NO.
3. Major Finding 1 flaw vs preference: FLAW.
4. Major Finding 1 realistic worst case: implementers consume unattributed components and traceability is broken for codegen boundaries.
5. Major Finding 1 mitigating factors: runtime behavior unaffected; fix is metadata-only.
6. Major Finding 1 severity after realist check: MAJOR, not CRITICAL.
7. Major Finding 2 confidence: HIGH.
8. Major Finding 2 refutable by author with missing context: NO.
9. Major Finding 2 flaw vs preference: FLAW.
10. Major Finding 2 realistic worst case: generated server/client types accept invalid terminal classes and produce downstream validation or analytics drift.
11. Major Finding 2 mitigating factors: server implementation could add validation later, and database CHECK would catch bad persisted values.
12. Major Finding 2 severity after realist check: MAJOR, not CRITICAL.
13. Minor Finding 1 confidence: HIGH.
14. Minor Finding 1 realistic worst case: less precise UI filtering and generated type drift for drain outcomes.
15. Minor Finding 1 severity after realist check: MINOR because the spec text is less central and SQL currently documents rather than CHECK-constrains it.
16. I did not escalate to ADVERSARIAL mode because there are two MAJOR findings, no CRITICAL findings, and no broad pattern of systemic failure after D1-D9 and schema checks.

## 8. Open Questions

1. Should `PageMeta` cite all specs that use pagination, or only `pool-routing` and `observability-billing` as representative released admin-query specs?
2. Should `BillingLedgerClaim.actual_cost` allow negative strings, or should negative values be reserved for a future adjustment-entry schema?
3. Should a Phase 2.3 issue be opened to model typed SSE event catalogs outside OpenAPI codegen?
4. Should `AuditEvent.event_type` be tightened to a union across released audit event taxonomies now, or left as string until the audit event catalog is centralized?
5. Should `rate_limit_reason`, `last_refresh_outcome`, and `oauth_endpoint_health` be enum-constrained in OpenAPI to match later SQL fragments?

These are not release-blocking for this review.

## 9. Owner-Facing Chinese Summary

本次审查结论是 `APPROVE-WITH-FIXES`，不能直接 Released，但不需要推翻合成稿。主要问题有两个：12 个 schema component 缺少 `x-huakai-spec-source`，以及 `UsageRecord.end_class` 没有按 released streaming taxonomy 收紧为 enum；另有一个低风险建议是收紧 `drain_outcome`。其余关键点通过：Feature ID 都存在、money 字段为 string+regex、无 `nullable: true`、admin endpoint 都有角色扩展、Provider Account 枚举与 SQL CHECK 对齐、SYNTHESIS D1-D9 基本落地。修完 §4 的 3 个 bounded fixes 后，可以重新跑结构检查和 OpenAPI validator，再推进 `api-contract.md` Released 与 Phase 3 codegen。
