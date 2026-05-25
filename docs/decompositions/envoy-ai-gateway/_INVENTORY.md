# envoy-ai-gateway — Feature Inventory

| Field | Value |
| --- | --- |
| Reference | Envoy AI Gateway (Apache-2.0, [github.com/envoyproxy/ai-gateway](https://github.com/envoyproxy/ai-gateway), [E-LIC-008](../../07_REFERENCE_EVIDENCE_LEDGER.md)) |
| Inventory owner | Codex (specifier-lane, gpt-5.5 + xhigh) |
| Inventory created | 2026-04-28 |

## Inventory

### Topology / Architecture

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Outer gateway for auth / global limits | F-ARCH-001 | shallow-evidence | E-EAG-001 |
| Inner gateway for Model cluster access | F-ARCH-001 | shallow-evidence | E-EAG-001 |
| Single-tier Personal Edition equivalent | F-ARCH-001 | unmined | HUAKAI must preserve simple deploy |
| Envoy data-plane integration | F-DEPLOY-002 | shallow-evidence | Apache ecosystem pattern |

### Kubernetes Resource Shapes

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Kubernetes-native AI Route resource | F-DEPLOY-002 / F-CONFIG-001 | shallow-evidence | CRD shape evidence |
| Backend resource for Provider / Model service | F-ROUTE-002 | shallow-evidence | Maps to Channel backend |
| Backend security policy resource | F-SEC-005 / F-AUTH-002 | shallow-evidence | Credential egress policy |
| Gateway config resource | F-DEPLOY-002 | shallow-evidence | Per-gateway processor config |
| Quota policy resource | F-SEC-006 | shallow-evidence | Token quota surface |
| MCP route resource | F-PROTO-001 | shallow-evidence | Phase 9+ |
| Resource status conditions | F-OBS-002 | unmined | Operator UX needed |

### Routing / Endpoint Picker

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Weighted backend routing | F-GW-001 | shallow-evidence | Gateway API pattern |
| Priority backend routing | F-GW-001 | shallow-evidence | SaaS Phase 10+ |
| Model name virtualization | (propose F-MODEL-003) | shallow-evidence | Capability discovery |
| Header mutation per Route / backend | F-SEC-005 | shallow-evidence | Header firewall relevance |
| Body mutation per Route / backend | F-PROTO-002 | shallow-evidence | Protocol compatibility risk |
| Endpoint picker integration | F-ROUTE-002 | shallow-evidence | E-EAG-002 |
| Endpoint metrics-aware selection | F-ROUTE-002 | shallow-evidence | Queue / cache / adapter signals |
| Endpoint picker fallback | F-GW-004 | shallow-evidence | Failure containment |

### Security / Quota / Protocols

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Provider credential policies | F-AUTH-002 | shallow-evidence | Egress credential scope |
| Token quota per backend | F-SEC-006 | shallow-evidence | E-EAG-003 adjunct |
| OpenAI Responses support | F-PROTO-002 | shallow-evidence | Release-note evidence |
| Prompt caching for selected Providers | F-CACHE-001 | shallow-evidence | Release-note evidence |
| CEL-based MCP authorization | F-PROTO-001 / F-SEC-004 | shallow-evidence | Phase 9+ |
| Search grounding bridge | (out-of-scope) | unmined | Provider-specific feature |

### Deployment / Operations

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Kubernetes operator deployment | F-DEPLOY-002 | shallow-evidence | E-EAG-003 |
| Gateway API conformance posture | F-DEPLOY-002 | shallow-evidence | Apache ecosystem |
| CLI surface | (propose F-OPS-006) | unmined | Ops convenience |
| OpenTelemetry tracing | F-OBS-002 | shallow-evidence | Public blog index |
| Upgrade / migration guidance | F-OPS-002 | shallow-evidence | Release-note evidence |

## Coverage Summary

- shallow-evidence: 26
- unmined: 4
- deep-decomposed: 0

L1/L2-relevant unmined rows: Single-tier Personal Edition equivalent; resource status conditions. Most other rows are out-of-scope for Personal Edition and SaaS Phase 10+ candidates.

## Mandated Next Dives (Priority Order)

1. Endpoint picker policy and failure behavior.
2. Kubernetes Route / Backend / Security / Quota CRD shapes.
3. Two-tier topology tradeoffs.
4. Header and body mutation safety.
5. Token quota policy semantics.
6. MCP route authorization.
7. Personal Edition single-tier safe equivalent.

## Specifier-Lane Contamination Note

Envoy AI Gateway is **Apache-2.0** — fourth confirmed safe anchor. Both lanes may read freely. Most CRD-shape evidence applies only to SaaS Edition Phase 10+ when Kubernetes deployment becomes a supported track.
