# Portkey — Feature Inventory (Codex parallel take)

| Field | Value |
| --- | --- |
| Reference | Portkey AI Gateway (MIT, [github.com/Portkey-AI/gateway](https://github.com/Portkey-AI/gateway), [E-LIC-006](../../07_REFERENCE_EVIDENCE_LEDGER.md)) |
| Inventory owner | Codex (specifier-lane, gpt-5.5 + xhigh) |
| Inventory created | 2026-04-28 |
| Companion file | [_INVENTORY.md](_INVENTORY.md) (Claude's parallel take) |

## Inventory

### Gateway / Routing

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Universal Provider API | F-GW-001 | shallow-evidence | README/docs evidence |
| Provider catalog | F-GW-001 | shallow-evidence | Needs capability matrix |
| Automatic retry | F-GW-004 | shallow-evidence | E-PK-001 |
| Error-condition fallback | F-GW-004 | shallow-evidence | E-PK-002 |
| Weighted load balancing | F-GW-001 | shallow-evidence | README evidence |
| Conditional routing | (propose F-ROUTE-003) | unmined | Policy language needed |
| Canary testing | (propose F-ROUTE-004) | unmined | Release safety candidate |
| Circuit breaker | F-CB-001 | unmined | Docs claim; no row |
| Request timeout | F-TIMEOUT-001 | shallow-evidence | E-PK-007 |
| Realtime WebSocket surface | F-RT-001 | shallow-evidence | E-PK-006 |

### Cache / Cost / Limits

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Simple response cache | F-CACHE-001 | shallow-evidence | E-PK-005 |
| Semantic response cache | F-CACHE-001 | shallow-evidence | E-PK-005 |
| Budget limits | F-SEC-006 | shallow-evidence | Docs evidence |
| Rate limits | F-SEC-004 | shallow-evidence | Docs evidence |
| Usage analytics | F-OBS-001 | unmined | Needs observability inventory |
| Provider cost optimization | F-ROUTE-001 | unmined | Compare with Helicone |

### Guardrails / Security

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Output guardrails | F-GUARD-001 | shallow-evidence | E-PK-003 |
| Input guardrails | F-GUARD-001 | unmined | Need behavior split |
| Deterministic guardrails | F-GUARD-001 | unmined | Rule-pack semantics TBD |
| LLM-based guardrails | F-GUARD-001 | unmined | Cost / latency risk |
| Partner / BYO guardrails | F-GUARD-001 | unmined | Plugin boundary |
| Secure key management | F-KEY-001 | shallow-evidence | README evidence |
| Virtual API Keys | F-KEY-001 / F-TENANT-001 | shallow-evidence | E-PK-008 overlap |
| RBAC | F-RBAC-001 | shallow-evidence | E-PK-008 |
| Compliance / data privacy posture | (propose F-SEC-008) | unmined | Certification, not core behavior |

### Protocols / Ops / Collaboration

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Multimodal request normalization | F-MM-001 | shallow-evidence | E-PK-004 |
| Realtime cost tracking | F-RT-001 / F-BILL-001 | unmined | Needs stream settlement |
| Model reference API | (propose F-MODEL-003) | unmined | Capability discovery |
| MCP gateway | F-PROTO-001 | unmined | Separate from LiteLLM evidence |
| Agent framework integrations | (propose F-AGENT-001) | unmined | Phase 9+ |
| OpenTelemetry observability | F-OBS-002 | unmined | Docs claim |
| Prompt templates | (propose F-PROMPT-001) | unmined | Product-layer feature |
| Prompt version / release flow | (propose F-PROMPT-001) | unmined | Not gateway MVP |
| Self-hosted OSS gateway | F-DEPLOY-001 | shallow-evidence | README evidence |

## Coverage Summary

- shallow-evidence: 17
- unmined: 17
- deep-decomposed: 0

L1/L2-relevant unmined rows: Conditional routing; circuit breaker; usage analytics; provider cost optimization; input guardrails; model reference API; MCP gateway; OpenTelemetry observability.

## Mandated Next Dives (Priority Order)

1. Retry / fallback / timeout interaction.
2. Circuit breaker semantics.
3. Guardrail execution boundary.
4. Simple vs semantic cache behavior.
5. Virtual API Key and RBAC scoping.
6. Realtime stream accounting.
7. Conditional routing and canary policy.

## Convergence with Claude's parallel take

Both inventories agree on the major Portkey surfaces (handlers / middlewares / providers / services / errors / APM).

Codex's take adds rows Claude's missed: conditional routing as F-ROUTE-003 candidate; canary testing as F-ROUTE-004 candidate; input vs output guardrail split; LLM-based guardrails as a distinct row; partner / BYO guardrails as a Plugin boundary row; prompt templates / prompt version flow as F-PROMPT-001 candidate; compliance posture as F-SEC-008 candidate.

Claude's take adds: per-package directory mapping (apm/data/errors/handlers/etc); guardrail engine + 40+ rule-pack catalog as a single deep-dive priority; provider-adapter shape as a coordinated dive across OpenAI/Anthropic/Gemini/Azure exemplars.

Both takes agree the L1/L2 critical unmined surfaces are: provider adapters, guardrail engine internals, semantic cache, RBAC revocation, multi-scope limits, APM/OTel.
