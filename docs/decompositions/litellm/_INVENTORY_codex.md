# LiteLLM — Feature Inventory (Codex parallel take)

| Field | Value |
| --- | --- |
| Reference | LiteLLM (MIT, [github.com/BerriAI/litellm](https://github.com/BerriAI/litellm), [E-LIC-005](../../07_REFERENCE_EVIDENCE_LEDGER.md); `enterprise/` carved out) |
| Inventory owner | Codex (specifier-lane, gpt-5.5 + xhigh) |
| Inventory created | 2026-04-28 |
| Companion file | [_INVENTORY.md](_INVENTORY.md) (Claude's parallel take — focused on the 37 subpackages) |

## Inventory

### Gateway / Protocol Surface

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Unified Provider API | F-GW-001 | shallow-evidence | E-LM-001 |
| OpenAI-compatible client surface | F-GW-001 | unmined | Needs HUAKAI row or merge |
| Native Provider format bridge | F-PROTO-002 | unmined | Provider adapters not decomposed |
| Provider capability matrix | (propose F-MODEL-003) | unmined | Needed before parity claims |
| Chat / Messages / Responses surfaces | F-PROTO-002 | unmined | Surface inventory only |
| Embedding / image / audio / rerank surfaces | F-MM-001 / F-MODEL-002 | unmined | Split by active phase |

### Routing / Reliability

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Cross-deployment fallback | F-GW-004 | shallow-evidence | E-LM-002 |
| Retry policy hierarchy | F-GW-004 | shallow-evidence | E-LM-DEEP-007 |
| Deployment-aware backoff | F-GW-004 | shallow-evidence | E-LM-DEEP-008 |
| Typed fallback branches | F-GW-004 | shallow-evidence | E-LM-DEEP-009 |
| Bounded fallback ledger | F-GW-004 | shallow-evidence | E-LM-DEEP-010 |
| Streaming fallback usage reconciliation | F-GW-002 / F-GW-004 | shallow-evidence | E-LM-DEEP-011 |
| Deployment load balancing | F-GW-001 | shallow-evidence | README evidence |
| Auto routing | (propose F-ROUTE-003) | unmined | Needs behavior proof |
| Failure-rate cooldown | F-CH-002 | shallow-evidence | E-LM-DEEP-001 |
| Last-resort Account protection | F-POOL-001 / F-CH-002 | shallow-evidence | E-LM-DEEP-005 |
| Time-windowed re-enable | F-CH-002 | shallow-evidence | E-LM-DEEP-003 |
| Per-deployment concurrency guard | F-CONC-001 | shallow-evidence | E-LM-DEEP-012 |
| RPM precheck / TPM post-call update | F-SEC-004 | shallow-evidence | E-LM-DEEP-013 |

### Keys / Billing / Tenancy

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Virtual API Keys | F-KEY-001 | shallow-evidence | README evidence |
| Multi-tenant spend tracking | F-BILL-001 | shallow-evidence | README evidence |
| Project / User budget controls | F-SEC-006 | shallow-evidence | README evidence |
| Per-tenant config on credential | F-TENANT-001 | shallow-evidence | E-LM-003 |
| Admin dashboard | F-OPS-003 | shallow-evidence | README evidence |
| Cost tracking SDK / gateway | F-BILL-001 | unmined | Needs accounting dive |

### Security / Ops / Extensions

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Gateway authN / authZ | F-SEC-002 | unmined | Gateway claim only |
| Enterprise SSO | F-AUTH-003 | shallow-evidence | E-LM-005 |
| Signed container images | F-SEC-003 | shallow-evidence | E-LM-004 |
| OpenAI-compatible errors | F-GW-004 | unmined | Error taxonomy needed |
| Observability callbacks | F-OBS-002 | unmined | Needs connector inventory |
| Guardrail policy attachment | F-GUARD-001 | shallow-evidence | E-LM-003 |
| Cache policy attachment | F-CACHE-001 | shallow-evidence | E-LM-003 |
| Logging policy per key | F-TENANT-001 / F-OBS-002 | shallow-evidence | E-LM-003 |
| MCP gateway | F-PROTO-001 | shallow-evidence | E-LM-006 |
| A2A bridge | F-PROTO-001 | shallow-evidence | E-LM-006 |
| IDE / agent framework adapters | (propose F-AGENT-001) | unmined | Phase 9+ candidate |

## Coverage Summary

- shallow-evidence: 25
- unmined: 11
- deep-decomposed: 0

L1/L2-relevant unmined rows: OpenAI-compatible client surface; native Provider format bridge; Provider capability matrix; auto routing; cost tracking; gateway authN / authZ; OpenAI-compatible errors; observability callbacks.

## Mandated Next Dives (Priority Order)

1. Provider adapter transformation behavior.
2. Retry / fallback / cooldown complete prose decomposition.
3. Streaming fallback duplicate-output policy.
4. Cost tracking and Usage Record attribution.
5. Virtual API Key authorization path.
6. Gateway authN / authZ.
7. Observability callback boundaries.

## Convergence with Claude's parallel take

Both inventories agree on:
- Same router_strategy / router_utils evidence base (E-LM-DEEP-007..014).
- Same set of mandated next dives (provider adapters / virtual key tenant config / cost tracking / cache backends).

Claude's take broke down LiteLLM's 37 subpackages explicitly (RAG / vector_stores / batch / fine_tuning / etc). Codex's take groups by behavior category (routing / billing / extensions). Both views are useful: Claude's gives the subpackage map; Codex's gives the feature-area map.

Both takes flag the same enterprise/ subtree quarantine.
