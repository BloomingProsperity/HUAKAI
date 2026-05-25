# Phase 2.2: API Contract — Released

| Field | Value |
| --- | --- |
| Status | Released |
| Spec ID | Phase 2.2 contract lock (cross-feature; not a single F-* row) |
| Specifier | Claude (PM-Orchestrator) + Codex (gateway draft) + Codex (admin draft), 2026-04-29 |
| Specifier date | 2026-04-29 |
| Reviewer | Codex final reviewer-lane, 2026-04-29 (APPROVE-WITH-FIXES; 3 fixes applied this revision; 14 schema-component edits) |
| Review date | 2026-04-29 |
| Released date | 2026-04-29 |
| Lane mode | Option B (multi-source synthesis; OpenAPI is HUAKAI-public surface) |
| Supersedes | — |
| Superseded by | — |

## Sources

- 6 Released specs that the contract derives from:
  - [docs/specs/pool-routing.md](pool-routing.md) — F-POOL-001
  - [docs/specs/observability-billing.md](observability-billing.md) — F-OBS-001 + F-BILL-001 framing
  - [docs/specs/streaming-forwarder.md](streaming-forwarder.md) — F-GW-002
  - [docs/specs/rate-limiting.md](rate-limiting.md) — F-RATE-001
  - [docs/specs/protocol-translation.md](protocol-translation.md) — F-PROTO-002
  - [docs/specs/upstream-credential-management.md](upstream-credential-management.md) — F-AUTH-005
- 6 schema fragments at [docs/schema/](../schema/)
- Specifier backing artifacts:
  - [docs/openapi/openapi.yaml](../openapi/openapi.yaml) — the unified contract YAML (1184 lines after fixes)
  - [docs/openapi/SYNTHESIS.md](../openapi/SYNTHESIS.md) — synthesis decisions (D1..D9)
  - [docs/openapi/claude-draft.yaml](../openapi/claude-draft.yaml) — Claude independent pass
  - [docs/openapi/codex-gateway-draft.yaml](../openapi/codex-gateway-draft.yaml) — Codex gateway pass
  - [docs/openapi/codex-admin-draft.yaml](../openapi/codex-admin-draft.yaml) — Codex admin pass

## Capability

This spec locks the **external HTTP surface** for HUAKAI Phase 2.2 contract closure. After this Released, Phase 3 implementation skeleton (Go module, sqlc, chi router) is allowed per DR-008 §1.

The contract covers:
- 3 client-facing relay endpoints (OpenAI Chat / OpenAI Responses / Anthropic Messages)
- 14 admin endpoints (Pool Groups / Provider Accounts / Usage / Billing / Audit / DLQ)
- Shared error envelope with HUAKAI typed `code` enum
- Money fields as numeric(20,8)-precise strings
- OpenAPI 3.1 nullable union syntax (`type: [..., 'null']`)
- snake_case JSON convention end-to-end

## Actor

Three actor classes interact with the contract:
- **Client developer** (gateway endpoints): authenticates with HUAKAI API Key.
- **Tenant operator** (admin endpoints, default role): manages own tenant's Pool Groups / Provider Accounts / Usage queries.
- **Platform admin** (admin endpoints, elevated role): cross-tenant operations, DLQ replay, forced-route override.

## Preconditions

1. Tenant context resolved from API Key on every request (server-side; never trusted from client header).
2. Released specs unchanged since 2026-04-28 (this contract MUST be regenerated if any underlying spec is materially revised).
3. Schema fragments at `docs/schema/*.sql` are field-level locked.

## Normal Path

The contract YAML at [docs/openapi/openapi.yaml](../openapi/openapi.yaml) is the authoritative artifact. Implementer-lane consumes it via codegen (Phase 3); generated server stubs match the contract.

For each path:
- `x-huakai-spec-source` extension lists the Released spec(s) that govern semantics.
- `x-huakai-required-role` extension on admin endpoints (`platform_admin` | `tenant_operator`).
- All schemas have `x-huakai-spec-source` (Codex final-review fix #1: 12 missing components added).

## Failure Path

The contract documents 7 standard error responses (400 / 401 / 402 / 403 / 404 / 409 / 429 / 503) all using shared `ErrorResponse` envelope:

```yaml
ErrorResponse:
  required: [error]
  properties:
    error:
      required: [code, message]
      properties:
        code: <HUAKAI typed enum>
        message: <human-readable, sanitized>
        request_id: <correlation>
        retry_after_seconds: <only on 429/503>
        protocol_loss: <array; per F-PROTO-002>
        details: <free-form>
```

`code` is a HUAKAI domain enum (FINGERPRINT_CONFLICT, QUOTA_EXHAUSTED, NO_ELIGIBLE_ACCOUNT, RATE_LIMIT_5H_EXCEEDED, OVERLOADED, TOKEN_PERMANENTLY_REVOKED, etc.) drawn from per-feature failure taxonomies in the 6 Released specs.

## Operator Recovery

Implementer-side recovery for contract-validation failure:
- Codegen errors: re-pull the YAML from this Released spec; do not edit generated stubs to "fix" mismatches.
- Runtime contract drift: if an implementer needs a field not in the contract, open a new DR rather than silently extending.

Operator-side recovery for runtime errors uses standard HTTP semantics + Retry-After header on 429/503.

## Audit / Usage / Log Evidence

Every gateway request produces a Usage Record per F-OBS-001. Every admin mutation produces an audit event per the relevant F-spec.

## Acceptance Test Direction

Phase 3 will codegen client + server stubs from this YAML. Acceptance tests for the contract itself:
- AT-API-001 / OpenAPI parser: file parses cleanly under spec-compliant OpenAPI 3.1 validator.
- AT-API-002 / All `$ref` references resolve.
- AT-API-003 / Every schema has `x-huakai-spec-source`.
- AT-API-004 / Every admin endpoint has `x-huakai-required-role`.
- AT-API-005 / End-class enum matches `docs/specs/streaming-forwarder.md` Phase C taxonomy (15 values).
- AT-API-006 / drain_outcome enum matches spec.
- AT-API-007 / Provider Account enums align with `docs/schema/pool-routing.sql` CHECK constraints.
- AT-API-008 / Money fields use string + numeric(20,8) regex.

## Open Questions

Codex final review identified 5 minor suggestions for follow-on work (NOT blocking release):
1. Should `rate_limit_reason`, `last_refresh_outcome`, `oauth_endpoint_health` be enum-constrained in OpenAPI to match SQL fragments? (currently typed as strings; can tighten in Phase 3 if codegen requires).
2. Should `routing_reason` jsonb get a typed schema for the structured payload (per F-POOL-001 §Audit)? (currently free-form `additionalProperties: true`).
3. Should Phase 2.3 add typed SSE event-type catalogs?
4. Add OpenAPI `examples` to gateway endpoints for client-developer onboarding?
5. Add a `/admin/v1/health` endpoint for operator probing?

These are tracked as Phase 2.3 / Phase 3 follow-ups, not release blockers.

## Implementer Notes (added by implementer lane)

> Filled by implementer after consuming the spec.

(empty until Phase 3 begins)
