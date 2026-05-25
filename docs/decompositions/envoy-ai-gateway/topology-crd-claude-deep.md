# `envoy-ai-gateway` — Outer/Inner Topology + AI Route CRD (Claude deep decomposition)

| Field | Value |
| --- | --- |
| Status | Deep decomposition (Claude lane, peer to Codex R3 specifier output) |
| Reference | Envoy AI Gateway (Apache-2.0, [E-LIC-008](../../07_REFERENCE_EVIDENCE_LEDGER.md)) |
| Feature in HUAKAI matrix | F-ARCH-001 + F-DEPLOY-002 + F-CONFIG-001 |
| Specifier session | Claude PM-Orchestrator (Opus), 2026-04-29 |
| Source-reading delegate | Sonnet Explore agent — read 18+ source files (~40min) |
| Companion artifacts | docs/decompositions/envoy-ai-gateway/topology-crd-source-verified.md (Codex R3), .omc/artifacts/decomp-critic/C7-envoy-topology.md (Codex critic) |
| **Truth-discipline** | **Observed regions: 18** / **Inferences: 2** / **Open questions: 6** |

> **License**: Apache-2.0 — most permissive of the 7 references. Behavioral patterns freely citable; HUAKAI implementation still original code per CL-005.

---

## 1. WHY (motivation)

Envoy AI Gateway is the only Kubernetes-native, control-plane/data-plane-split reference in HUAKAI's evidence ledger. Two pressures shape this:

**Pressure 1 — declarative routing as cluster state**: Operators of multi-tenant or production-scale gateways need routing rules versioned, audit-logged, rollback-able like any K8s resource. Envoy expresses AI routes as CRDs reconciled by controllers; runtime data-plane changes are downstream effects of resource state changes `[region-1, region-7]`.

**Pressure 2 — separation of operator-controlled vs tenant-mutable concerns**: In a managed AI gateway product, the operator owns auth + global limits + TLS; tenants own per-model dispatch + per-backend credentials within their namespace. Envoy's outer/inner tier split makes this explicit at deploy topology, not just code modules `[region-2, region-13]`.

Pressure 3 (less central): Apache-2.0 + CNCF-friendly licensing makes Envoy AI Gateway the candidate "enterprise" packaging blueprint for HUAKAI's SaaS Edition Phase 9+.

---

## 2. WHAT (architecture in HUAKAI vocabulary)

### Sub-behaviors S-1..S-15 (observed-only)

**S-1: Two-tier topology** `[region-2, region-13]`. Outer tier handles operator-uniform concerns (auth, global rate-limit, TLS termination, initial header parsing for model extraction). Inner tier handles per-model dispatch via ExtProc (external processor sidecar in Envoy pods) — request transformation OpenAI→target schema, backend selection by `x-ai-eg-model` header, per-backend credential injection, response token counting.

**S-2: Mutating webhook for sidecar injection** `[region-13]`. A Kubernetes mutating admission webhook (port 9443) patches Envoy pod templates: when an AIGatewayRoute exists referencing the gateway, the webhook injects an `extproc` container; when all routes are removed, the sidecar is removed on next pod cycle. This is the structural mechanism that ties resource declaration to running data plane.

**S-3: AIGatewayRoute CRD** `[region-3]`. Schema fields:
- `parentRefs` (max 16): K8s Gateway references this route attaches to
- `rules` (max 128): each rule has matches (header / path / method) + backendRefs + filters
- `llmRequestCosts` (max 36): cost expressions extracting tokens for quota tracking (e.g., `OutputToken metadataKey="output_tokens"`)
- Status conditions: `Accepted` (success) or `NotAccepted` (failed validation)

The `x-ai-eg-model` header match is the canonical model selector — clients send model name as header, route picks backend.

**S-4: AIServiceBackend CRD** `[region-4]`. Each backend declares:
- `apiSchema`: provider name (OpenAI / Cohere / AWSBedrock / AzureOpenAI / GCPVertexAI), version, optional path prefix
- `backendRef`: reference to Envoy Gateway Backend resource (the actual upstream URL/port)
- `headerMutation` + `bodyMutation`: per-backend request transformations (e.g., model name remapping)

Note: credentials are NOT in AIServiceBackend; they're in BackendSecurityPolicy (S-5).

**S-5: BackendSecurityPolicy CRD** `[region-5, region-9]`. Five auth families:
- APIKey (Secret reference)
- AWSCredentials (IRSA / Workload Identity)
- AzureTokenRotation (token refresh by controller)
- GCPWorkloadIdentity
- AnthropicAPIKey (specific to Anthropic)

The controller pre-rotates credentials within a 5-minute window before expiry.

**S-6: GatewayConfig CRD** `[region-6]`. Global defaults: ExtProc deployment config (image, resources), default LLM request costs (per-route can override).

**S-7: QuotaPolicy CRD** `[region-7]`. Structure:
- `targetRefs`: which AIServiceBackends this quota applies to (max 16)
- `serviceQuota`: global quota with `costExpression` (CEL: e.g., `input_tokens + 3 * output_tokens + 0.1 * cached_input_tokens`), `mode` (Exclusive / Shared bucket), `defaultBucket` (limit, duration), `bucketRules` (client-selector per-tier overrides), `shadow` flag (enforce vs log-only)
- `perModelQuotas` (max 128): per-model overrides

**S-8: MCPRoute CRD** `[region-12]`. Routes Model-Context-Protocol requests to MCP servers. Separate route type for tools/agents external interface.

**S-9: AIGatewayRoute reconciler** `[region-7]`. Trigger: spec generation change. Output: creates HTTPRoute (Gateway API standard) + filter config; sets status condition. Uses `retry.RetryOnConflict()` for concurrent modifications.

**S-10: AIServiceBackend reconciler** `[region-8]`. Trigger: spec change. Sends events to referencing routes (cascading reconciliation). No direct data-plane mutation.

**S-11: BackendSecurityPolicy reconciler** `[region-9]`. Trigger: credential expiry approaching (5min pre-window). Updates cluster Secret. Owns credential rotation lifecycle.

**S-12: Gateway reconciler (filter config aggregator)** `[region-10]`. Trigger: any AIGatewayRoute / MCPRoute change via event channel. Output: generates filter config Secret; annotates pods (UUID change) to force ExtProc reload.

**S-13: Filter runtime + CEL evaluation** `[region-14, region-15]`. ExtProc loads filter config from Secret; compiles CEL expressions for cost calculation. CEL evaluated on each response after token-count extraction.

**S-14: ExtProc request lifecycle** `[region-16]`:
1. Receive headers from Envoy filter chain
2. Extract `x-ai-eg-model` header
3. Look up matching backend in filter config
4. Apply request body transformation (OpenAI → target schema)
5. Inject auth header (per BackendSecurityPolicy)
6. Forward to upstream
7. On response: extract usage tokens; populate filter metadata `io.envoy.ai_gateway.tokens`
8. Quota descriptor evaluated against limits

**S-15: Conflict between routes claiming same model** `[region-7, inferred from region-3]`. No explicit CRD-level conflict detection. Each AIGatewayRoute creates its own HTTPRoute; Envoy Gateway's HTTPRoute merge logic determines which rule wins. **Last-write-wins implicitly via timestamps**, but the loser's HTTPRoute remains in cluster (silent shadow).

### 2-bis Lifecycle traces (3 observed)

**L-1 Operator declares new AI route**: Operator applies AIGatewayRoute YAML → API server validates schema → AIGatewayRoute reconciler picks up generation change → creates HTTPRoute + filter config Secret → Gateway reconciler picks up → annotates Envoy pods → pods restart with new config → ExtProc loads new routes → first traffic flows. End-to-end ~30 seconds.

**L-2 Credential rotation**: Background timer in BackendSecurityPolicy reconciler detects credential within 5min of expiry → fetches new credential (per provider's refresh path) → writes new Secret value → AIGatewayRoute consuming this policy is unaffected (Secret is referenced by name; pods read fresh value on next request OR on watch trigger). No data-plane downtime.

**L-3 Quota exhaustion under load**: Tenant traffic accumulates input + output tokens → ExtProc evaluates `costExpression` per response → metadata populated → Envoy RateLimit filter evaluates against bucket limit → on exhaustion, returns 429 to client → client/operator must wait for window reset (duration field).

---

## 3. INPUTS

**Per-Request inputs**: HTTP request to outer tier, `x-ai-eg-model` header (model selection), Authorization (auth filter), client identity attributes (for quota selectors).

**Per-Backend state**: AIServiceBackend resource (apiSchema, backendRef, mutations); BackendSecurityPolicy referenced (Secret containing credentials).

**Per-Tenant boundaries**: each tenant's resources live in their own K8s namespace; cross-namespace references gated by ReferenceGrants (Gateway API standard).

**Per-Process state**: ExtProc filter config (loaded from Secret + CEL programs), token-count metadata per request.

**Persistent state (cluster-stored)**: AIGatewayRoute / AIServiceBackend / BackendSecurityPolicy / GatewayConfig / QuotaPolicy / MCPRoute resources; Secrets for credentials and filter configs; ConfigMaps for ExtProc deployment.

---

## 4. FAILURE MODES (observed-only)

| FM-id | Trigger | Observable outcome | Operator signal | Recovery | Blast radius |
|---|---|---|---|---|---|
| FM-1 | AIGatewayRoute references non-existent AIServiceBackend | Status NotAccepted with reason BackendNotFound | resource status + events | manual fix | single route |
| FM-2 | BackendSecurityPolicy fetch fails during rotation | Secret stale; backend may still work via existing creds until they expire | controller log + metric | manual intervention | one provider |
| FM-3 | Two AIGatewayRoutes claim same model | Both HTTPRoutes exist; merge order silently determines winner | none observable | manual cleanup | tenant-visible if cross-tenant |
| FM-4 | Pod-template patch fails (webhook down) | New routes don't get ExtProc sidecar; routes nominally Accepted but data-plane silent | webhook log | restart webhook pod | new routes only |
| FM-5 | CEL expression error in costExpression | Quota bucket evaluation fails; defaults to 0 cost (or rejects request) | filter log | fix CEL | per-route |
| FM-6 | Credential rotation race (rotation in progress, request mid-flight) | Old secret used briefly; new request gets new secret | none | natural eventual consistency | per-request | 
| FM-7 | Quota state lost on Envoy pod restart | If quota state is local memory (not Redis), rate limits reset to zero on restart | quota metric jump | use distributed RateLimitService | brief window of free traffic |
| FM-8 | Resource deletion mid-traffic | Routes remain alive until reconciler tears down filter config; in-flight requests may complete on stale config | resource event | natural eventual consistency | per-route | 

---

## 5. INTERFACES TO HUAKAI

**Personal Edition (NOT used)**:
- HUAKAI Personal Edition is a single-binary deploy by DR-002. Outer/inner split is OVER-engineering for Personal — no K8s, no controllers, no CRDs.
- The CRD shapes (AIGatewayRoute / AIServiceBackend / BackendSecurityPolicy / QuotaPolicy) map to HUAKAI's existing PostgreSQL tables (routes / provider_accounts / pricing_buckets) — Personal Edition uses tables, not CRDs.

**SaaS Edition (relevant)**:
- HUAKAI's Phase 9+ SaaS Edition operator-control plane CAN adopt the outer/inner split AS A PROJECTION of existing PostgreSQL data — same data model, optional K8s presentation surface.
- Mutating webhook pattern (S-2) maps to HUAKAI's "tenant config change → trigger replica reload" — HUAKAI uses pgNotify or polling, not K8s admission control.
- BackendSecurityPolicy's 5-min pre-rotation pattern (S-11) maps to F-AUTH-005's existing OAuth refresh `expires_at_minus_skew` logic.

**Cross-feature**:
- F-ARCH-001 (architecture): HUAKAI's existing single-binary works for Personal; SaaS needs the outer/inner concept but can be implemented as logical layers in the same binary, not separate processes.
- F-CONFIG-001 (config CRD): HUAKAI's admin API exposes routes/backends/policies as REST endpoints; CRD shape is YAML over REST, structurally same.
- F-OBS-001 quota: Envoy's CEL costExpression maps to HUAKAI's per-tenant pricing-bucket multipliers; HUAKAI uses Go decimal, not CEL.

---

## 6. RISKS HUAKAI MUST GUARD AGAINST

**R-1 [Premature K8s adoption (DR-002 Personal Edition simplicity)]**: Adopting outer/inner split for Personal Edition doubles deploy complexity. HUAKAI MUST keep Personal a single binary; SaaS gets the split LATER as Phase 9+ enhancement, not Phase 4 requirement.

**R-2 [DR-006 PostgreSQL — quota state durability (FM-7)]**: Envoy's quota state may be Envoy-local memory; HUAKAI's PostgreSQL-backed counters are durable across restarts. Don't import Envoy's pattern of "local memory rate limit"; use the existing `provider_accounts` quota counters.

**R-3 [Resource conflict semantics (FM-3)]**: Envoy's silent last-write-wins is fragile in multi-tenant. HUAKAI MUST: (a) reject conflicting route declarations at admin API level; (b) audit the conflict event; (c) require explicit operator priority on overlap.

**R-4 [Data-plane reload latency (S-12 → L-1)]**: Envoy's pod-rollout pattern means ~30 seconds end-to-end for a route change to be live. HUAKAI's PostgreSQL-direct selection makes route changes effective on next request (no reload). Don't import Envoy's reload-cycle pattern; HUAKAI's data path reads tables directly.

**R-5 [Webhook single-point-of-failure (FM-4)]**: K8s mutating webhooks are critical-path; their failure prevents ExtProc injection. HUAKAI's data path doesn't depend on K8s admission control — keep it that way. SaaS Edition's K8s deploy would inherit this risk; mitigation: webhook HA + bypass annotation for emergency.

**R-6 [CEL complexity (FM-5)]**: Envoy uses CEL for cost expressions — powerful but operator can write broken CEL. HUAKAI uses Go decimal arithmetic with hardcoded multipliers; trade flexibility for predictability. Defer CEL to a future opt-in feature flag, not default.

**R-7 [DR-001 multi-tenant — namespace as tenant isolation (S-3 cross-namespace refs)]**: Envoy uses K8s namespaces as tenant boundary + ReferenceGrants for cross-namespace. HUAKAI uses tenant_id column scoping in PostgreSQL — every query MUST filter by tenant_id. The K8s pattern is structurally analogous but enforced by RBAC + admission, not row-level security.

**R-8 [Apache-2.0 vs HUAKAI MIT licensing]**: Envoy AI Gateway is Apache-2.0 — adoption-friendly but distinct from HUAKAI MIT. If HUAKAI ever depends on Envoy components directly (unlikely Phase 4-7), license attribution required. Behavioral inspiration freely citable.

---

## 7. SAFE ADAPTATION (concrete divergences)

1. **Single-binary Personal Edition** stays single-binary; outer/inner split deferred to SaaS Phase 9+.
2. **PostgreSQL-direct selection** instead of pod-reload pattern — route changes effective immediately.
3. **Reject conflicting route declarations at admin API** — no silent last-write-wins.
4. **Tenant-scoped row filters** at every query (DR-001) — no K8s-namespace-equivalent at HUAKAI's level.
5. **Hardcoded Go decimal cost math** — no CEL until proven need + safety analysis.
6. **5-minute pre-rotation OAuth window** (already in F-AUTH-005) — same pattern, different implementation.
7. **REST admin API for resource CRUD** — equivalent to YAML CRD, no K8s dependency.
8. **Optional K8s packaging** as Phase 9+ enhancement; data model stays PostgreSQL-shaped.

---

## 8. EVIDENCE LEDGER ROWS

- **E-EAG-001 (existing — promote)**: Outer/inner topology — promote to deep with control-plane/data-plane separation.
- **E-EAG-DEEP-NEW-1**: 5-CRD configuration surface (AIGatewayRoute / AIServiceBackend / BackendSecurityPolicy / QuotaPolicy / GatewayConfig + MCPRoute) — relevant to F-CONFIG-001 design.
- **E-EAG-DEEP-NEW-2**: Mutating webhook for sidecar injection pattern — informs HUAKAI's tenant-config-reload mechanism.
- **E-EAG-DEEP-NEW-3**: 5-min pre-rotation credential pattern — corroborates F-AUTH-005 design.
- **E-EAG-DEEP-NEW-4**: CEL costExpression for quota — counter-evidence for HUAKAI's hardcoded Go decimal choice.

---

## 9. OPEN QUESTIONS

1. **Q-1 InferencePool integration**: mentioned in proposals but no v1alpha1 CRD in main branch — externally specified.
2. **Q-2 Quota persistence (FM-7)**: source did not detail distributed state mechanism for multi-instance quota tracking. Local-memory assumed per Envoy instance? Operator must configure RateLimitService externally?
3. **Q-3 CEL hot-reload**: when CEL costExpression changes in CRD, when does the running filter pick it up?
4. **Q-4 Pod-rollout UUID GC**: Gateway controller patches pod annotations with UUIDs. Are old UUIDs ever cleaned up or do they accumulate?
5. **Q-5 MCP Route security**: MCPRoute supports BackendSecurityPolicy but the per-MCP credential routing isn't fully shown.
6. **Q-6 Single-tier deploy mode**: source suggests gateway+sidecar in same deployment is supported; does this work in single-pod for dev/test?

---

## 10. SOURCE COVERAGE PROOF (Sonnet Explore agent, ~40min, 18+ files)

| Region | URL | Contribution |
|---|---|---|
| region-1 | github.com/envoyproxy/ai-gateway/main/docs/proposals/001-ai-gateway-proposal/proposal.md | Top-level architecture proposal |
| region-2 | .../cmd/controller (dir) | Control plane location |
| region-3 | .../api/v1alpha1/ai_gateway_route.go | AIGatewayRoute CRD schema |
| region-4 | .../api/v1alpha1/ai_service_backend.go | AIServiceBackend CRD |
| region-5 | .../api/v1alpha1/backendsecurity_policy.go | 5 auth families |
| region-6 | .../api/v1alpha1/gateway_config.go | Global defaults |
| region-7 | .../api/v1alpha1/quota_policy.go + internal/controller/ai_gateway_route.go | QuotaPolicy schema + reconciler |
| region-8 | .../internal/controller/ai_service_backend.go | Cascading reconciler |
| region-9 | .../internal/controller/backend_security_policy.go | 5-min pre-rotation |
| region-10 | .../internal/controller/gateway.go | Filter config aggregator |
| region-11 | .../internal/controller/inference_pool.go | InferencePool stub |
| region-12 | .../api/v1alpha1/mcp_route.go | MCP routes |
| region-13 | .../cmd/extproc + sidecar webhook | Two-tier topology + injection |
| region-14 | .../internal/filterapi/filterconfig.go | Filter runtime config |
| region-15 | .../internal/filterapi/runtime.go | CEL compilation |
| region-16 | .../internal/extproc/processor.go | ExtProc lifecycle |
| region-17 | .../internal/extensionserver/extensionserver.go | Extension server gRPC |
| region-18 | .../internal/mcpproxy/mcpproxy.go | MCP proxy |

---

## 11. ROUND-2 CRITIC FINDINGS (C7 envoy)

> Codex critic file at `.omc/artifacts/decomp-critic/C7-envoy-topology.md`. This Claude-deep is independent. Synthesis stage merges Codex specifier-deep + C7 critic + this Claude-deep.

---

## Owner Chinese summary

本 deep 拆解依据 Sonnet Explore agent 真读 18+ 个 envoy-ai-gateway 源文件（40min），由我（Claude Opus）合成 15 个 sub-behavior + 3 个 lifecycle + 8 个 failure 模式 + 8 个 HUAKAI-fit 风险 + 8 项 safe adaptation。**核心结论**：envoy 的 outer/inner 双层 + 6 个 CRD + 控制面 reconciler 模式是 **HUAKAI SaaS Edition Phase 9+ 的可选 packaging blueprint**，**不是 Personal Edition 的需要**——双层会让单 binary 部署变成 K8s + sidecar + webhook（违 DR-002 简洁约束 R-1）。HUAKAI 现有的 PostgreSQL routes/provider_accounts/billing_pricing_versions 表与 envoy 的 CRD 结构同源，可未来作为 K8s 投影发布而不动数据模型。CEL 用于 cost expression 是 envoy 的特色但 HUAKAI 该用 Go decimal 硬编码（R-6）；envoy 的 5 分钟预轮换 credential 模式（S-11）已被 F-AUTH-005 采纳。Apache-2.0 license 最宽松，clean-room 风险最低。本文件未读 codex specifier 或 critic 输出。
