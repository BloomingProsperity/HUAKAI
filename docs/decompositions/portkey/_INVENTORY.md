# Portkey — Feature Inventory

| Field | Value |
| --- | --- |
| Reference | Portkey AI Gateway ([github.com/Portkey-AI/gateway](https://github.com/Portkey-AI/gateway), MIT, [E-LIC-006](../../07_REFERENCE_EVIDENCE_LEDGER.md)) |
| Inventory owner | Claude (PM-Orchestrator) |
| Inventory created | 2026-04-28 |
| Last refreshed | 2026-04-28 |
| Top-level dirs of `src/` (verified via api.github.com) | `apm` `data` `errors` `handlers` `middlewares` `providers` `public` `services` `shared` `tests` `types` `utils` |

## Why This File Exists

Owner directive 2026-04-28: "整体代码和逻辑都读完". Portkey is HUAKAI's MIT safe-anchor for guardrails, semantic cache, and TypeScript gateway patterns. Stack: TypeScript on Hono / Cloudflare Workers / Node — different stack from HUAKAI (Go), so the value here is **behavioral patterns**, not code.

## Status Legend

`unmined` / `shallow-evidence` / `deep-decomposed`.

## Inventory

### Request Handlers (`handlers/`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Chat completions handler | F-GW-001 (L1) | unmined (source) | entry point for the hot path |
| Streaming response handler | F-GW-002 (L1) | unmined (source) | how Portkey forwards SSE |
| Multi-modal handlers (vision / audio / image generation) | F-MM-001 (L3) | shallow-evidence (E-PK-004 README) | unmined source |
| Realtime / WebSocket handler | F-RT-001 (L3) | shallow-evidence (E-PK-006 README) | unmined source — WebSocket lifecycle |
| Embeddings / rerank / batch handlers | F-MODEL-002 (L3) | unmined (source) | enumerate the surface |

### Middlewares (`middlewares/`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Auth / virtual-key resolution | F-AUTH-001 (L1) | unmined (source) | how Portkey resolves a virtual key |
| Rate limit (global / team / per-user, count + dollar) | F-SEC-006 (L2) | shallow-evidence (E-HLC cross-ref + E-PK README) | unmined source — multi-scope limits |
| Retry middleware (exponential backoff up to N attempts) | F-GW-004 (L1) | shallow-evidence (E-PK-001 README) | unmined source |
| Fallback middleware (error-condition-driven) | F-GW-004 (L1) | shallow-evidence (E-PK-002 README) | unmined source |
| Cache middleware (simple + semantic) | F-CACHE-001 (L2) | shallow-evidence (E-PK-005 README) | **unmined source** — semantic cache is HUAKAI's L3 |
| Guardrail middleware (output validation against rule packs) | F-GUARD-001 (L2 Plugin) | shallow-evidence (E-PK-003 README) | **unmined source** — 40+ rule packs catalog |
| Per-request timeout enforcement | F-TIMEOUT-001 (L1) | shallow-evidence (E-PK-007 README) | unmined source |

### Providers (`providers/`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| 250+ provider integrations across 45+ vendors | F-GW-001 (L1) + F-PROTO-002 (L2) | unmined (source) | catalog reference |
| OpenAI provider adapter | F-PROTO-002 (L2) | unmined (source) | base case |
| Anthropic provider adapter | F-PROTO-002 (L2) | unmined (source) | high priority for HUAKAI |
| Google Vertex / Gemini adapter | F-PROTO-002 (L2) | unmined (source) | high priority |
| Azure OpenAI adapter | F-PROTO-002 (L2) | unmined (source) | high priority |
| Provider config schema (per-provider credential + endpoint shape) | F-AUTH-002 + F-CONFIG-001 (L1/L2) | unmined (source) | how provider configs are structured |

### Services (`services/`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Cost tracking service | F-BILL-001 (L2) | unmined (source) | how Portkey aggregates spend |
| Logging service | F-OBS-001 (L2) | unmined (source) | log envelope shape |
| Cache service | F-CACHE-001 (L2) | unmined (source) | the cache implementation specifics |
| Guardrail service | F-GUARD-001 (L2 Plugin) | unmined (source) | how rule packs are evaluated |

### Errors (`errors/`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Typed error class hierarchy | F-GW-004 (L1) | unmined (source) | maps to HUAKAI typed-failure taxonomy |
| Provider-error normalization | F-GW-004 (L1) | unmined (source) | how upstream 4xx/5xx become client-safe errors |

### Observability (`apm/`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| APM hooks / tracing | F-OBS-002 (L2) | unmined (source) | OpenTelemetry-compatible? |
| Latency / cost / error metrics emission | F-OBS-001 (L2) | unmined (source) | what metrics, what aggregation |

### Configuration / Data (`data/`, `shared/`, `types/`, `utils/`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Routing config schema (YAML / UI-generated) | F-CONFIG-001 (L2) | shallow-evidence (E-HLC-006 cross-ref + E-PK README) | unmined source |
| Type definitions for cross-provider request shape | F-PROTO-002 (L2) | unmined (source) | the canonical envelope |
| Shared utilities (token counting / etc) | (cross-cutting) | unmined | reference for HUAKAI's tokenizer |

### Operational Surfaces (`public/`, `tests/`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Public assets (admin UI?) | (out-of-scope here) | unmined | Portkey may have a separate admin UI repo |
| Test suite | (cross-cutting) | unmined | reference for HUAKAI's test patterns |

### Compliance Claims (README)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| SOC2 / HIPAA / GDPR / CCPA claims | F-SEC-003 (L3 Phase 8) + Phase 10+ | shallow-evidence (E-PK-011 README) | claims require independent attestation; HUAKAI design only |
| RBAC with instant revocation diff | F-RBAC-001 (L2) | shallow-evidence (E-PK-008 README) | unmined source — revocation propagation |

## Coverage Summary (2026-04-28)

- **Deep-decomposed**: 0 features.
- **Shallow-evidence**: 8 features (E-PK-* README rows).
- **Unmined**: ~30+ features across 12 subpackages.

**Phase 1 exit blockers**: 7 L1/L2 features are critical and `unmined`:
- F-PROTO-002 Anthropic / Google / Azure adapters
- F-GUARD-001 guardrail engine + rule pack catalog
- F-CACHE-001 semantic cache implementation
- F-RBAC-001 RBAC with revocation diff
- F-OBS-002 APM tracing hooks
- F-CONFIG-001 routing config schema

## Mandated Next Dives (Priority Order)

1. **Guardrail middleware + rule pack catalog** (F-GUARD-001 L2) — Portkey's flagship; HUAKAI Plugin design depends on this. → [`portkey/guardrail-engine.md`](.)
2. **Semantic cache implementation** (F-CACHE-001 L3) — embedding-based key derivation, similarity threshold, TTL. → [`portkey/semantic-cache.md`](.)
3. **Provider adapter shape** (F-PROTO-002 L2) — pick OpenAI + Anthropic + Gemini + Azure as canonical exemplars. → [`portkey/provider-adapters.md`](.)
4. **Multi-scope rate + cost limits** (F-SEC-006 L2) — global / team / per-user, count + dollar. → [`portkey/multi-scope-limits.md`](.)
5. **RBAC with instant revocation diff** (F-RBAC-001 L2) — propagation guarantee + diff display. → [`portkey/rbac-revocation.md`](.)
6. **APM / OpenTelemetry surface** (F-OBS-002 L2) — exporter selection + metric set. → [`portkey/apm-otel.md`](.)
7. **Realtime WebSocket handler** (F-RT-001 L3) — connection lifecycle + partial-stream usage. → [`portkey/realtime-websocket.md`](.)
8. **Routing config schema** (F-CONFIG-001 L2) — YAML shape + UI-wizard generation parity. → [`portkey/routing-config.md`](.)

## Specifier-Lane Contamination Note

Portkey is MIT — both lanes may read freely. Stack difference (TS vs HUAKAI's Go) means CL-005 (no algorithmic line-by-line translation) is naturally easier here; the language barrier discourages copy-paste, but vocabulary discipline (CL-001..004) still applies.
