# `envoy-ai-gateway` — Outer/Inner Gateway Topology + AI Route CRD (Claude draft)

| Field | Value |
| --- | --- |
| Status | Draft (Claude lane parallel viewpoint to Codex T7 specifier) |
| Reference | Envoy AI Gateway (Apache-2.0, [E-LIC-008]) |
| Feature in HUAKAI matrix | F-ARCH-001 + F-DEPLOY-002 + F-CONFIG-001 |
| Evidence anchor | E-EAG-001 |
| Specifier session | Claude PM-Orchestrator, parallel viewpoint, 2026-04-29 |
| Companion artifact | docs/decompositions/envoy-ai-gateway/topology-crd-source-verified.md (Codex T7) |
| Source files read | NONE directly. This draft reasons from envoy-ai-gateway/_INVENTORY.md + general Envoy/Kubernetes operator knowledge + HUAKAI's existing single-tier architecture in cmd/gateway/main.go + DR-002 dual-edition decision. Codex T7 fills algorithm + CRD shape. |

> **Lane discipline**: Codex T7 reads source; this draft frames structural divergence between K8s split-tier topology and HUAKAI's monolithic Personal Edition vs nested SaaS Edition. Synthesis combines.

> **License**: Apache-2.0 — most permissive of the references. Behavioral patterns freely citable; line-by-line code translation still avoided per CL-005.

## 1. WHY (motivation)

Every other reference project in HUAKAI's evidence ledger is a **monolithic gateway**: a single binary or process that handles auth + routing + dispatch + observability in one address space. Envoy AI Gateway is the architectural outlier — it splits gateway concerns across **two cooperating tiers**, with declarative Kubernetes resources expressing the routing surface.

Two pressures drive this design:

1. **Operator-vs-tenant separation at infrastructure layer**. In a multi-tenant cloud product, the operator who runs the platform (auth, billing, abuse defense) is structurally different from the tenant who consumes provider models. Envoy's outer/inner split makes this separation a deployment fact: the outer tier is operator-controlled (immutable from tenant view); the inner tier is tenant-mutable (each tenant can declare AI Routes within their namespace). HUAKAI's SaaS Edition has the same nesting per DR-002, so the topology is structurally relevant.

2. **Declarative routing as configuration**. Sub2API + one-api express routes as runtime configuration loaded into an in-process map. Envoy expresses routes as Kubernetes resources with reconciliation loops, so routing changes are auditable, version-controlled, and rollback-able the same way any K8s resource is. Operators in the SaaS Edition who run HUAKAI as a managed product would benefit from this declarativeness; Personal Edition operators who run a single binary do not.

## 2. WHAT (architecture in HUAKAI vocabulary)

### 2.1 Two-tier topology (split semantics)

The **outer tier** owns concerns that are uniformly operator-controlled and tenant-invariant:
- API key authentication (which tenant is this?)
- Global rate limits (across all tenants, by IP / region / abuse signal)
- TLS termination
- Initial logging + correlation id minting
- Routing only at the granularity of "which inner cluster"

The **inner tier** owns concerns that are tenant-mutable and per-Model:
- Per-tenant pool selection (the F-POOL-001 layered selection)
- Protocol translation (F-PROTO-002)
- Streaming forwarder (F-GW-002)
- Per-Account credential refresh + storm budgets (F-AUTH-005)
- Settling Tx2 against per-tenant billing (F-OBS-001)

A request arriving at the outer tier carries no per-Account knowledge; the outer tier authenticates, attaches tenant context, and forwards to the appropriate inner cluster based on the resolved tenant's pool group. The inner cluster runs N replicas of a smaller relay process focused on hot-path execution.

### 2.2 Declarative AI Route resource

Operators (and SaaS tenants in the inner tier) author **declarative resources** to express:

- **AI Route**: which client request shape (model, endpoint family) maps to which Backend with which translation policy and which capability constraints. Status conditions surface health (Ready, Degraded, ConfigError).
- **Backend**: a logical reference to a Provider Account or pool of Provider Accounts, including credential pointer (TLS / OAuth / static) and per-Backend rate limits.
- **Backend Security Policy**: how the Backend's credential is sourced (Kubernetes Secret / Vault / KMS) and constrained (allow-list of egress endpoints).
- **Quota Policy**: per-tenant or per-route quota, attached to AI Route.

Resources are reconciled by a controller; an applied resource may transition through `Pending → Ready → Failed` with operator-readable reasons. CRUD on these resources is the primary configuration surface; runtime API exists only for observability and emergency override.

## 3. INPUTS (HUAKAI signals)

- **Outer tier inputs**: API key (resolves tenant), source IP, request size, requested endpoint family, requested model, optional client-supplied `X-HUAKAI-Trace-Id`.
- **Inner tier inputs**: tenant context (from outer), pool group, the F-POOL-001 selection signals already documented.
- **Resource inputs**: AI Route resources for the tenant's namespace, Backend resources referenced by those routes, Backend Security Policy resources for credential sourcing, Quota Policy attachments.
- **Reconciler inputs**: resource generation counter (so reconciler skips unchanged resources), status condition history, last-reconciled-at.

## 4. FAILURE MODES HANDLED

- **Outer tier authenticates an unknown API key**: 401 with stable error envelope; outer tier never invokes inner tier (saves inner-tier cost on every probe).
- **Inner tier targeted by tenant lookup but not Ready**: outer tier returns 503 with `Retry-After`; routing reason records `inner_cluster_not_ready` (different from "no eligible account" — distinguishes infra failure from tenant config failure).
- **AI Route resource references a Backend that doesn't exist**: reconciler sets `Ready=False` with reason `BackendNotFound`; route is unschedulable; tenant sees the error in their dashboard before traffic flows.
- **Backend credential expired** (the credential Secret was rotated but the Backend Security Policy wasn't updated): F-AUTH-005 OAuth refresh path catches; Backend marked temp_unsched; reconciler does NOT modify Backend resource (the resource is operator-authored), only the runtime status.
- **Tenant declares conflicting AI Routes** (two routes claim the same model + endpoint family): reconciler picks deterministic winner by resource creation timestamp + sets `Conflicted=True` on the loser.

## 5. INTERFACES TO HUAKAI

For HUAKAI **Personal Edition** (single-binary deploy):
- Outer/inner split is NOT used. The single binary is the conceptual outer-tier; routing surface is internal data structures, not K8s resources.
- AI Route concept maps to existing `routes` table (already in `docs/schema/pool-routing.sql`).
- Backend concept maps to existing `provider_accounts`.
- Resources are CRUD'd via admin API (per `docs/openapi/openapi.yaml`) instead of K8s.

For HUAKAI **SaaS Edition** (multi-tenant managed deploy):
- Outer tier is the operator-run process accepting all tenants' traffic.
- Inner tier is per-tenant or per-pool-group (operator chooses bin-packing strategy) replica set.
- AI Route + Backend resources live in PostgreSQL (DR-006) and are exposed as a YAML-equivalent admin API (`/admin/v1/ai-routes` etc.) for declarative authoring; the reconciliation loop runs in the orchestrator process.
- A future K8s-native packaging is possible without redesigning data models — the YAML resources are the same shape regardless of authoring surface.

## 6. RISKS HUAKAI MUST GUARD AGAINST

- **Adopting the outer/inner split prematurely for Personal Edition**: doubles the deploy surface for an operator who serves themselves. Personal Edition stays single-tier (DR-002 explicit constraint). Adopting Envoy's split for Personal would punish the simplicity goal.
- **Reconciliation loop ↔ runtime divergence**: the resource is the declared truth; the runtime is the cached materialization. If the runtime diverges (a manual SQL UPDATE on a routes row), the next reconciliation overwrites silently. HUAKAI MUST have a sentinel (`runtime_drifted_at` column) so a manual override surfaces as a divergence event before being clobbered.
- **Tenant-authored Backend Security Policy referencing a Secret outside their namespace**: clean SaaS isolation must enforce that a tenant's policy can only reference credentials in that tenant's namespace. RBAC at the resource layer.
- **Status condition staleness**: a `Ready=True` condition that's 24h old should be treated as suspect. Reconciler must heartbeat conditions on a TTL.
- **CRD versioning**: when the resource shape evolves, in-flight resources must continue to validate. Schema migrations on resource definitions need a conversion strategy (similar to Kubernetes API conversion webhooks).

## 7. SAFE ADAPTATION FOR HUAKAI (clean-room divergences from Envoy AI Gateway)

- **Apache-2.0 license**: behaviorally inspired patterns can be cited; no need to fork code. Implementer-lane works from this draft + Codex T7 + spec, not from envoy source.
- **Kubernetes is optional, not foundational**: HUAKAI's data model is in PostgreSQL; K8s CRD shape is a *projection* HUAKAI may publish in Phase 9+ if SaaS Edition takes off. The data model is K8s-shaped, not K8s-bound.
- **Envoy's data plane is C++ + Lua**: HUAKAI's data plane is Go (DR-003). The runtime characteristics differ; ranking algorithms designed assuming Envoy's per-request filter chain may need re-thinking for HUAKAI's request-per-goroutine model.
- **Backend concept overlap with HUAKAI's existing `provider_accounts`**: do NOT introduce a separate "Backend" entity in PostgreSQL; reuse `provider_accounts` and add fields if needed.
- **AI Route concept overlap with HUAKAI's existing `routes`**: same — extend, do not duplicate.

## 8. EVIDENCE LEDGER ROWS NEEDED

- E-EAG-001 (existing): outer/inner gateway topology — promote to deep when Codex T7 lands.
- E-EAG-NEW: AI Route CRD reconciliation status semantics (likely a separate row; condition lifecycle is its own behavior).

## 9. OPEN QUESTIONS (for Codex T7 to resolve from source)

1. Where exactly does authentication terminate in the outer tier — at the proxy data plane or in a sidecar?
2. How does the reconciler handle "AI Route references a Quota Policy whose generation is older than the Route's"? First-write-wins, last-write-wins, or both-must-be-current?
3. Is Backend resource state derived from runtime probes (the reconciler probes upstream health) or from operator declaration only?
4. Are there resource-level webhooks for validation (admission control) or only post-apply reconciliation status?
5. Does the project ship a Personal Edition equivalent (single-binary mode) or is K8s the only deploy target?

## Owner Chinese summary

本 draft 是 Claude lane 对 envoy-ai-gateway **outer/inner 双层 + AI Route CRD 声明式拓扑**的独立结构化拆解，未读源码。这是 8 个借鉴项目里**唯一的非 monolithic 架构**——HUAKAI Personal Edition 不会用，但 SaaS Edition 的 operator-vs-tenant 嵌套（DR-002）和这个 split 概念结构同源；synthesis 后框出 SaaS Edition 在 Phase 9+ 把现有 PostgreSQL routes/provider_accounts 投影成 CRD 的可选包装方案。最大风险：过早把双层引进 Personal Edition 会双倍部署面（违 DR-002 简洁约束）；recon ↔ runtime drift 必须有 sentinel；CRD 版本演化需要 conversion 策略。Apache-2.0 license 最宽松，clean-room 风险最低。
