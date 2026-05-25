# LiteLLM — Feature Inventory

| Field | Value |
| --- | --- |
| Reference | LiteLLM ([github.com/BerriAI/litellm](https://github.com/BerriAI/litellm), MIT, [E-LIC-005](../../07_REFERENCE_EVIDENCE_LEDGER.md); `enterprise/` subtree carved out under separate license) |
| Inventory owner | Claude (PM-Orchestrator) |
| Inventory created | 2026-04-28 |
| Last refreshed | 2026-04-28 |
| Top-level dirs of `litellm/` package (verified via api.github.com) | 37 directories — see Inventory below |

## Why This File Exists

Owner directive 2026-04-28: "整体代码和逻辑都读完". LiteLLM is HUAKAI's MIT safe-anchor for retry/router/concurrency patterns. The package is much larger than one-api (37 subpackages vs 10), most provider-specific. This inventory groups features so the dive priority is clear.

## Status Legend

`unmined` / `shallow-evidence` / `deep-decomposed`.

## Inventory

### Router Core (`router_strategy/`, `router_utils/`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Cooldown handler (failure-rate threshold + traffic-volume floor + single-account exemption + connection-error class) | F-CH-002 (L2) | shallow-evidence (E-LM-DEEP-001..006, E-LM-DEEP-014) | best-in-class behavior; HUAKAI adopts |
| Retry policy hierarchy (global / per-request / per-deployment / exception-type) | F-GW-004 (L1) | shallow-evidence (E-LM-DEEP-007) | many overrides — HUAKAI must enforce deterministic precedence (DR-003 Constraint 8) |
| Retry sleep zero when same-group healthy deployment available | F-GW-004 (L1) | shallow-evidence (E-LM-DEEP-008) | provider Retry-After header respected |
| Typed fallback chain (deployment tier / model-specific / provider-stripped / generic / context-window-failure / content-policy-failure) | F-GW-004 (L1+L2) | shallow-evidence (E-LM-DEEP-009) | typed branches HUAKAI adopts |
| Bounded fallback recursion (max depth + same-group skip + attempt-count metadata) | F-GW-004 (L1) | shallow-evidence (E-LM-DEEP-010) | depth alone insufficient; add cost+latency budget |
| Streaming fallback usage reconciliation (combine partial usage from failed + fallback streams) | F-GW-002 (L1) | shallow-evidence (E-LM-DEEP-011) | duplicate-output policy needed |
| Per-deployment async semaphore (max-parallel → RPM → TPM-derived → default) | F-CONC-001 (L2) | shallow-evidence (E-LM-DEEP-012) | in-process only — HUAKAI must distribute |
| Pre-call RPM atomic increment + post-call TPM update | F-SEC-006 (L2) | shallow-evidence (E-LM-DEEP-013) | TPM lag during long streams = HUAKAI improvement |
| Routing strategy implementations (round-robin / lowest-latency / lowest-cost / etc.) | F-ROUTE-001 (L2) | unmined (router_strategy/ source) | enumerate strategies; map to HUAKAI Pool selector |

### Caching (`caching/`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Simple key-based response cache | F-CACHE-001 (L2) | shallow-evidence (E-PK-005 cross-ref) | unmined LiteLLM source |
| Semantic / embedding-based cache | F-CACHE-001 (L3) | shallow-evidence (E-PK-005) | unmined LiteLLM source |
| Cache backends (in-memory / Redis / etc.) | F-CACHE-002 (L2) | unmined (source) | how backend abstraction is shaped |

### Proxy Server (`proxy/`, `proxy_auth/`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Proxy server entry / FastAPI router | F-GW-001 (L1) | unmined (source) | LiteLLM is Python — HUAKAI uses Go (DR-003); behavioral patterns still useful |
| Virtual API Key with per-key config (logging / guardrail / cache policy) | F-TENANT-001 (L2) | shallow-evidence (E-LM-003 README) | unmined source — most directly relevant to HUAKAI tenant config |
| Per-tenant cost tracking | F-BILL-001 (L2) | unmined (source) | how costs roll up to tenant |
| Per-tenant feature flag / guardrail attachment | F-GUARD-001 (L2 Plugin) | unmined (source) | guardrail attachment to virtual key |
| Spend tracking dashboard | F-OBS-001 (L2) | unmined (source) | what metrics get exposed |

### LLM Providers (`llms/`, `anthropic_interface/`, `google_genai/`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| 100+ provider integrations (OpenAI / Anthropic / Google / etc.) | F-PROTO-002 (L2) + F-GW-001 (L1) | unmined | massive surface; HUAKAI builds its own provider adapters as needed; LiteLLM serves as catalog reference |
| Anthropic-specific interface (Claude Messages format) | F-PROTO-002 (L2) | unmined (source) | needed for HUAKAI's protocol translation |
| Google GenAI interface (Gemini) | F-PROTO-002 (L2) | unmined (source) | needed |
| Image / Vision handling (`images/`) | F-MM-001 (L3) | unmined | multi-modal request handling |
| Audio handling | F-MM-001 (L3) | unmined | multi-modal |
| Video handling (`videos/`) | F-MM-001 (L3) | unmined | multi-modal — Phase 9+ |
| OCR (`ocr/`) | (out-of-scope) | unmined | not core to relay-station |
| Pass-through requests (`passthrough/`) | F-PROTO-002 (L2) | unmined | direct provider passthrough; HUAKAI Edition mode parallel |

### Realtime / Streaming (`realtime_api/`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| WebSocket realtime API integration | F-RT-001 (L3 Phase 9+) | unmined (source) | how WebSocket lifecycle is managed |

### Rerank (`rerank_api/`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Rerank-specific API surface | F-MODEL-002 (L3 Phase 9+) | unmined (source) | distinct from chat/embedding |

### Vector / RAG (`rag/`, `vector_stores/`, `vector_store_files/`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Vector store integration | (out-of-scope L1-L4) | unmined | RAG is downstream-application concern, not gateway concern |

### Batch / Fine-tuning (`batch_completion/`, `batches/`, `fine_tuning/`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Batch completion API | (deferred L4+ if any) | unmined | OpenAI batch jobs API; HUAKAI optional |
| Fine-tuning lifecycle | (deferred L4+) | unmined | requires upstream fine-tuning support |

### Files / Assistants / Skills (`files/`, `assistants/`, `skills/`, `interactions/`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| File upload / management API | F-PROTO-002 (L2) | unmined | OpenAI files API; some providers need it |
| OpenAI Assistants API | (out-of-scope L1-L4) | unmined | stateful AI agent surface; HUAKAI defers |
| Skills framework | (out-of-scope) | unmined | LiteLLM-specific |

### Protocols (`a2a_protocol/`, `experimental_mcp_client/`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| A2A (Agent-to-Agent) protocol bridge | F-PROTO-001 (L3 Plugin Phase 9+) | shallow-evidence (E-LM-006 README) | unmined source |
| MCP (Model Context Protocol) client | F-PROTO-001 (L3 Plugin Phase 9+) | shallow-evidence (E-LM-006 README) | unmined source |

### Operations (`evals/`, `compression/`, `secret_managers/`, `integrations/`, `endpoints/`, `responses/`, `containers/`, `search/`, `litellm_core_utils/`, `types/`, `completion_extras/`)

| Feature | HUAKAI matrix row | Status | Notes |
| --- | --- | --- | --- |
| Eval framework | (out-of-scope) | unmined | LiteLLM-specific test harness |
| Request/response compression | F-GW-002 (L1) | unmined | hot-path bytes — relevant if HUAKAI adopts |
| Secret manager integrations (Vault / AWS Secrets Manager / etc.) | F-AUTH-002 (L1) | unmined | for SaaS Edition Phase 10+ secret rotation |
| Third-party integrations (Datadog / Langfuse / etc.) | F-OBS-002 (L2) | unmined | observability sinks |
| Endpoint definitions | F-GW-001 (L1) | unmined | route table |
| Response object normalization | F-GW-002 (L1) | unmined | how response shapes are unified |
| Container/Docker glue (`containers/`) | F-DEPLOY-001 (L3 Phase 8) | unmined | deployment patterns |
| Search functionality | (out-of-scope) | unmined | likely RAG-related |
| Core utilities | (cross-cutting) | unmined | likely no direct mapping |
| Type definitions | (cross-cutting) | unmined | shared schema concerns |
| Completion extras | (deferred) | unmined |  |

## Coverage Summary (2026-04-28)

- **Deep-decomposed**: 0 features (no prose file yet for litellm).
- **Shallow-evidence**: 12 features (E-LM-DEEP rows present in router_utils area).
- **Unmined**: ~50 features across 30+ subpackages.

**Phase 1 exit blockers from this inventory**: the 12 shallow-evidence rows are concentrated in `router_utils/` (which Codex deep-read). 4 L1/L2 areas remain critical and unmined:
- Virtual API Key with per-tenant config (`proxy/`)
- Routing strategy implementations (`router_strategy/`)
- Cache backend abstraction (`caching/`)
- Anthropic + Google GenAI interface (`anthropic_interface/`, `google_genai/`)

## Mandated Next Dives (Priority Order)

1. **Routing strategy implementations** (F-ROUTE-001 L2) — beyond cooldown; the actual selection algorithms (round-robin / lowest-latency / cost-aware). → [`litellm/router-strategies.md`](.)
2. **Virtual API Key with per-tenant config** (F-TENANT-001 L2) — most directly relevant to HUAKAI's tenant model. → [`litellm/virtual-key-tenant-config.md`](.)
3. **Anthropic + Google GenAI provider adapters** (F-PROTO-002 L2) — the protocol translation reference. → [`litellm/anthropic-adapter.md`](.) and [`litellm/google-genai-adapter.md`](.)
4. **Cache backend abstraction** (F-CACHE-002 L2) — Redis / in-memory / S3 plug points. → [`litellm/cache-backends.md`](.)
5. **Spend tracking dashboard backend** (F-OBS-001 L2) — what metrics get exposed and how. → [`litellm/spend-tracking.md`](.)
6. **Endpoint table + response normalization** (F-GW-001 / F-GW-002 L1) — confirm route surface and response-shape unification. → [`litellm/endpoint-table.md`](.)

## Specifier-Lane Contamination Note

LiteLLM is MIT — both lanes may read freely. The `enterprise/` subtree has separate licensing (verified per [E-LIC-005](../../07_REFERENCE_EVIDENCE_LEDGER.md)) and must NOT be read; treat it as a quarantine.
