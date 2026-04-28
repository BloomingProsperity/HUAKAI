# helicone — Feature Inventory

| Field | Value |
| --- | --- |
| Reference | Helicone AI Gateway (GPL-3.0, [github.com/Helicone/ai-gateway](https://github.com/Helicone/ai-gateway), [E-LIC-007](../../07_REFERENCE_EVIDENCE_LEDGER.md)) |
| Inventory owner | Codex (specifier-lane, gpt-5.5 + xhigh) |
| Inventory created | 2026-04-28 |

## Inventory

### Gateway / Routing

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Unified Provider gateway | F-GW-001 | shallow-evidence | Public docs evidence |
| Provider catalog | F-GW-001 | shallow-evidence | Needs capability split |
| Client-selected Provider destination | (propose F-ROUTE-003) | unmined | Header-driven behavior, no local row |
| OpenAI SDK compatibility | F-GW-001 | unmined | Integration surface |
| Streaming support | F-GW-002 | unmined | Needs stream settlement dive |
| Automatic failover | F-GW-004 | shallow-evidence | E-HLC-001 overlap |
| Smart load balancing | F-GW-001 | shallow-evidence | E-HLC-001 |
| Cheapest-provider routing | F-ROUTE-001 | shallow-evidence | E-HLC-001 |
| Custom-rule routing | F-CONFIG-001 | shallow-evidence | E-HLC-006 |
| Prompt management | (propose F-PROMPT-001) | unmined | Product-layer feature |

### Cache / Limits / Billing

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Semantic cache | F-CACHE-001 | shallow-evidence | E-HLC-002 |
| Pluggable cache backend | F-CACHE-002 | shallow-evidence | E-HLC-002 |
| Request-count rate limits | F-SEC-004 | shallow-evidence | E-HLC-004 |
| Cost-amount limits | F-SEC-006 | shallow-evidence | E-HLC-004 |
| Provider-price passthrough display | (propose F-BILL-004) | unmined | SaaS billing display |

### Observability / Ops

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Per-request logs | F-OBS-001 | shallow-evidence | Public docs evidence |
| OpenTelemetry export | F-OBS-002 | shallow-evidence | E-HLC-003 |
| Analytics for cost / latency / errors | F-OBS-001 | shallow-evidence | Public docs evidence |
| Feedback capture | (propose F-OBS-003) | unmined | Product-layer |
| Provider credential vault | F-AUTH-002 | unmined | Needs trust-boundary review |
| Domain / Provider approval workflow | (propose F-SEC-007) | unmined | Safety boundary candidate |

### Performance / Deployment / Config

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Published latency and resource budget | F-GW-003 | shallow-evidence | E-HLC-005 |
| Lightweight gateway artifact | F-DEPLOY-001 | shallow-evidence | Public README evidence |
| Cloud-hosted gateway option | F-DEPLOY-001 | shallow-evidence | Deployment surface |
| Docker / local package deployment | F-DEPLOY-001 | shallow-evidence | E-HLC-007 |
| Declarative routing config | F-CONFIG-001 | shallow-evidence | E-HLC-006 |
| UI routing wizard | F-CONFIG-001 | shallow-evidence | E-HLC-006 |
| API Key management | F-SEC-002 | shallow-evidence | Public changelog evidence |
| Request validation | F-SEC-005 | shallow-evidence | Public changelog evidence |
| Enterprise support | (out-of-scope) | unmined | Commercial support, not parity feature |

## Coverage Summary

- shallow-evidence: 21
- unmined: 9
- deep-decomposed: 0

L1/L2-relevant unmined rows: Client-selected Provider destination; OpenAI SDK compatibility; streaming support; Provider credential vault; domain / Provider approval workflow.

## Mandated Next Dives (Priority Order)

1. Performance-aware routing signal aggregation.
2. Streaming support and Usage Record settlement.
3. OpenTelemetry export surface.
4. Cache backend isolation model.
5. Cost-limit enforcement.
6. Declarative routing config reload.
7. Provider credential trust boundary.

## Specifier-Lane Contamination Note

Helicone is **GPL-3.0** (specifier read source per Owner clarification 2026-04-28). Network use does not trigger GPL distribution requirements but binary distribution does; HUAKAI Phase 8 deploy artifacts must not vendor or link Helicone code.
