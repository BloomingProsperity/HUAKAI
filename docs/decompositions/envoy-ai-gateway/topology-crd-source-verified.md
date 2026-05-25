# envoy-ai-gateway topology and CRD reconciliation source-verified decomposition

Metadata:
- Project: envoy-ai-gateway
- Feature: Outer/inner gateway topology + AI Route reconciliation + Backend resource lifecycle + Quota Policy attachment + status condition lifecycle
- HUAKAI matrix rows: F-ARCH-001 + F-DEPLOY-002 + F-CONFIG-001
- Lane: Codex specifier-lane ROUND 3
- Truth-discipline: Observed regions: 16 / Inferences: 12 / Open questions: 9
- Source stance: public upstream docs, release notes, and Go package documentation were read. Direct raw GitHub source fetch was unavailable in this environment, so controller-internal race handling and private implementation details are not asserted.
- Clean-room stance: this document records behavior-level evidence only. It intentionally avoids upstream function names, package layouts, distinctive implementation structure, and code-shaped descriptions.

## §1 WHY

The upstream design pressure is to expose one AI-facing entry point while delegating much of the HTTP routing and data-plane management to the surrounding gateway ecosystem. The README describes a two-tier pattern where an outer tier handles authentication, top-level routing, and global rate limiting, while an inner tier handles access to self-hosted model serving with endpoint-picker support [region-1]. The architecture page separates control plane and data plane: the control plane manages data-plane configuration, and the data plane contains proxy plus an AI-specific processor in the request path [region-2].

For HUAKAI, this matters because the feature is not only "route a model to a backend." It is a topology contract spanning Gateway, Account, Channel, Pool, Quota, Health, Admin, and Plugin vocabulary. The upstream CRD layer creates or references lower-level gateway resources, allows generated resources to be patched by gateway policies, and exposes only coarse Accepted/NotAccepted style status for several objects [region-3] [region-9]. That is useful evidence, but HUAKAI must adapt it into tenant-aware, edition-aware, PostgreSQL-backed operations instead of copying the namespace-centered control model.

## §2 WHAT

S-1. Two-tier deployment topology: upstream describes a first gateway tier for centralized entry, authentication, top-level routing, and global rate limiting, plus a second gateway tier for self-hosted model access and endpoint-picker optimization [region-1].

S-2. Control/data-plane split: upstream describes the control plane as the component that manages data-plane configuration, while the request path contains proxy plus an AI-specific external processor [region-2].

S-3. AI Route attachment: an AI route attaches to Gateway resources through parent references whose current kind is Gateway, and its rules are translated into lower-level HTTP routing behavior [region-8].

S-4. Generated route artifacts: an AI route produces a top-level HTTP routing resource and a host-rewrite filter in the same namespace as the AI route; upstream calls these generated resources an implementation detail subject to change [region-3].

S-5. Generated-resource extension point: upstream says operators can use generated resources as references or patch them with gateway policy APIs, including traffic policy for retry fallback [region-3].

S-6. Backend resource lifecycle: an AI service backend represents one AI backend, attaches to a Gateway backend object, and causes backend-specific logic to be inserted into the final generated routing configuration when targeted by an AI route [region-4].

S-7. Backend reference namespace behavior: a route rule's backend references default to the AI route namespace, may specify another namespace, and require referent-side ReferenceGrant consent for cross-namespace reference [region-6].

S-8. Backend type split: a route rule can reference either normal AI service backends or InferencePool resources, but an individual rule allows only one InferencePool backend and cannot mix it with normal AI service backend references [region-6].

S-9. Fallback split: upstream says InferencePool fallback is handled by the endpoint picker, while normal AI service backend fallback is achieved by combining multiple backends with gateway traffic policies [region-6].

S-10. InferencePool narrower semantics: model-name override, header mutation, body mutation, and priority are ignored for InferencePool references, while normal backend references support those knobs [region-7].

S-11. Weighted backend routing: normal backend references carry a weight with default 1, and priority defaults to 0 for normal backend references [region-7].

S-12. Model-based routing: AI route matching can use an internal model header because the model name is extracted from request content before route selection [region-8].

S-13. Timeout default: if a rule does not set request timeout, upstream defaults AI requests to 60 seconds rather than the surrounding gateway's 15 second default, and upstream warns streaming responses may need longer timeouts [region-6].

S-14. Token cost capture: route-level request cost definitions capture token-related numbers into per-request dynamic metadata and override gateway-level defaults for the same metadata key [region-8].

S-15. Quota policy attachment: upstream's quota policy targets AI service backends, defines service-level and per-model quotas, uses sliding windows, can select clients by traffic attributes, returns HTTP 429 when enforced quota is exceeded, and supports shadow mode where checks run but the outcome is not enforced [region-10].

S-16. Status condition shape: AI route, AI service backend, backend security policy, GatewayConfig, and quota policy statuses expose conditions from reconciliation, and the API reference repeatedly states that at most one Accepted/NotAccepted condition is set for the observed resources [region-9] [region-10] [region-13].

S-17. Gateway-scoped external processor config: GatewayConfig configures the AI processor container for a Gateway, is referenced by annotation from a Gateway in the same namespace, can be shared by multiple Gateways, and its environment values override global controller values when names collide [region-5].

S-18. GatewayConfig migration direction: v0.5 guidance moves external processor resource settings from route-level configuration into GatewayConfig and shows the Gateway annotation as the binding mechanism [region-13] [region-14].

S-19. InferencePool operational setup: the InferencePool guide installs the inference extension, deploys inference backends, creates InferencePool resources, and then binds Gateway plus AI route rules that match model names to InferencePool backends [region-11].

S-20. InferencePool processor controls: InferencePool annotations choose streamed versus buffered body processing and decide whether the external processor can override processing mode [region-12].

## §2-bis Lifecycle traces

Trace A - normal AI route to normal backend:
1. Operator defines Gateway and AI route with parent Gateway reference [region-8].
2. Operator defines AI service backend attached to a Gateway backend object [region-4].
3. AI route rules target backend references; local namespace is default unless an explicit namespace is supplied [region-6].
4. Controller-generated route artifacts bind all backends and insert AI processing behavior [region-3] [region-8].
5. Reconciliation reports an Accepted or NotAccepted style condition on the AI route and backend resources [region-9].

Trace B - cross-namespace backend reference:
1. Route rule specifies a backend namespace different from the route namespace [region-6].
2. Referent namespace must contain a ReferenceGrant accepting the reference [region-6].
3. If HUAKAI adapts this, namespace consent is not enough by itself; tenant_id authorization and audit must be added (inference, not observed).

Trace C - route to InferencePool:
1. Operator installs the inference extension and supporting pool resources [region-11].
2. AI route rule matches a model and references an InferencePool backend [region-11].
3. The rule cannot mix InferencePool and normal AI service backend references [region-6].
4. Unsupported knobs for InferencePool, including model override and mutation controls, are ignored [region-7].
5. Endpoint picker handles fallback for the pool [region-6].

Trace D - quota and cost:
1. AI route declares token cost metadata keys [region-8].
2. The request path captures token numbers into dynamic metadata [region-8].
3. Quota policy attaches to targeted AI service backends and computes service or model quota burndown [region-10].
4. Enforced over-quota requests receive 429; shadow-mode requests still run checks but are allowed [region-10].

Trace E - GatewayConfig rollout:
1. Operator creates GatewayConfig with external processor container settings [region-5] [region-13].
2. Gateway references it by annotation in the same namespace [region-5] [region-13].
3. Multiple Gateways may share it, and GatewayConfig environment values override global controller values on conflict [region-5].
4. GatewayConfig status reports Accepted or NotAccepted [region-13].

## §3 INPUTS

Observed input inventory:

| Input | Observed fields or concepts | Source |
| --- | --- | --- |
| Gateway | Parent target for AI route; same-namespace annotation binds GatewayConfig | [region-5] [region-8] |
| AI route | Parent Gateway references, rules, matches, backend references, request timeout, model-driven matching, request cost metadata | [region-6] [region-8] |
| AI service backend | AI backend identity, Gateway backend attachment, schema/translation requirement, request header/body mutation | [region-4] [region-9] |
| Backend security policy | Backend egress authentication or authorization; status condition | [region-9] |
| GatewayConfig | External processor container settings, environment variables, resource requirements, shared use, status | [region-5] [region-13] |
| Quota policy | Target backend references, service quota, per-model quota, cost expression, client selectors, shadow mode, sliding window | [region-10] |
| InferencePool | Optional backend target through inference extension; annotations for body processing and override permission | [region-11] [region-12] |
| Dynamic metadata | Token cost values captured per request under AI gateway namespace | [region-8] |

## §4 FAILURE MODES

Only source-observed failure modes or source-observed operational hazards are listed.

| Failure mode | Observed upstream behavior | HUAKAI implication |
| --- | --- | --- |
| Cross-namespace backend without accepted grant | Cross-namespace backend references require referent-side ReferenceGrant | HUAKAI must add tenant authorization, not only namespace consent |
| Quota exceeded | Enforced quota returns HTTP 429 | HUAKAI must map 429 to account/channel quota state and billing continuity |
| Shadow quota overrun | Shadow mode performs checks but does not enforce | HUAKAI Admin must label shadow quota as non-blocking |
| GatewayConfig invalid | GatewayConfig status can be NotAccepted for validation errors | Admin Ops needs structured remediation reasons |
| Generated resource drift | Generated route resources are implementation detail, but upstream allows patching generated resources | HUAKAI should avoid unmanaged patch drift |
| InferencePool unsupported knobs | Several per-backend knobs are ignored for InferencePool | UI/API must not promise mutation, override, or priority for that plugin path |
| Default timeout too short for streaming | Upstream notes 60s default and says streaming may require longer timeouts | HUAKAI must separate request timeout, stream idle timeout, and quota accounting timeout |
| Shared GatewayConfig blast radius | Multiple Gateways may share one GatewayConfig and resource values override global values | HUAKAI needs audit, dry-run, and rollback for shared config changes |

## §5 INTERFACES TO HUAKAI

Personal Edition:
- Provide a local Gateway adapter that can model AI route, backend, quota, and GatewayConfig concepts without requiring Kubernetes as the user-facing configuration surface (inference, not observed).
- Support normal backend routing, model extraction, timeout defaults, and token cost capture as local configuration concepts (inference, not observed).
- Treat InferencePool as Plugin or Mandatory Roadmap unless a local endpoint-picker equivalent exists (inference, not observed).
- Status should be surfaced as local Health records with route, backend, quota, and processor dimensions, rather than only a single Accepted flag (inference, not observed).

SaaS Edition:
- Represent Gateway, Account, Channel, Pool, Quota, Billing, Health, Logs, Admin, and Plugin as first-class tenant-scoped resources (inference, not observed).
- Derive tenant_id and account identity from HUAKAI Auth/Account Hub context, never from a client-controlled header (inference, not observed).
- Persist quota and billing reconciliation in PostgreSQL per DR-006, using token metadata only as request evidence (inference, not observed).
- Gate GatewayConfig-like changes with approval, audit, dry-run, rollback, and edition scoping because one shared config may affect multiple Gateways (inference, not observed).

## §6 RISKS

| Risk | Basis | HUAKAI-fit decision |
| --- | --- | --- |
| Namespace is mistaken for tenant boundary | Cross-namespace backend references are accepted with ReferenceGrant [region-6] | Inference, not observed: SaaS tenant boundary must be tenant_id + Account Hub authorization, with namespace only deployment isolation |
| Generated-resource patching creates ownership ambiguity | Upstream allows patching generated route resources [region-3] | Inference, not observed: expose validated HUAKAI policy APIs instead of asking operators to patch generated internals |
| Single-condition status is too weak for operations | Status uses at most one Accepted/NotAccepted condition [region-9] [region-13] | Inference, not observed: Admin Ops needs per-parent, per-backend, per-policy, per-tenant reasons |
| InferencePool looks equivalent but is narrower | InferencePool ignores several routing knobs and cannot be mixed in one rule [region-6] [region-7] | Inference, not observed: model it as an optional Plugin with capability flags |
| Model extraction creates spoofing and parser-failure concerns | Matching uses an internal header derived from request body [region-8] | Inference, not observed: strip or overwrite client-supplied internal routing attributes and fail closed on parse ambiguity |
| Shared GatewayConfig can alter unrelated Gateways | Multiple Gateways can share one config and its env values override globals [region-5] | Inference, not observed: require blast-radius preview and rollback |
| Quota examples rely on traffic attributes | Quota client selectors can use traffic attributes [region-10] | Inference, not observed: billing and quota keys must come from authenticated Account Hub context, not raw request headers |
| Version churn can leak into HUAKAI public contract | Release notes document GatewayConfig migration and API updates across v0.5 [region-14] | Inference, not observed: keep Kubernetes-style compatibility behind adapters and publish stable HUAKAI OpenAPI/admin contracts |

## §7 SAFE ADAPTATION

1. Implement a HUAKAI RouteSpec that maps to one or more runtime gateway adapters, but do not expose generated gateway artifacts as the primary customization interface.
2. Split backend capability flags: normal Provider/Pool backends may support weight, priority, mutation, and model override; InferencePool-like plugin backends must declare unsupported features explicitly.
3. Model routing keys as internal request attributes. A client-visible header may be accepted only as input evidence after authentication and parser validation.
4. Treat quota as an Account Hub concern backed by PostgreSQL. Dynamic token metadata can feed quota computation, but it is not the ledger.
5. Replace single Accepted/NotAccepted status with structured Health states: RouteAccepted, ParentBound, BackendResolved, CredentialReady, QuotaReady, ProcessorReady, PolicyApplied, DataPlaneProgrammed.
6. Make GatewayConfig-like processor settings edition-scoped: Personal local config, SaaS platform config, and tenant-approved overrides.
7. Put InferencePool support behind Plugin with health gates, RBAC review, HA expectations, and explicit unavailable-feature messages.
8. Preserve feature parity by disposition, not by copying: generated route behavior becomes Safe Equivalent; GatewayConfig becomes Implemented Better; InferencePool becomes Plugin unless native endpoint picker exists.

## §8 EVIDENCE LEDGER ROWS

| Evidence ID | Matrix row | Source type | Observation | KEEP / IMPROVE / AVOID |
| --- | --- | --- | --- | --- |
| E-EAG-DEEP-R3-001 | F-ARCH-001 | Public docs deep read | Upstream documents two-tier gateway topology plus control/data-plane split | KEEP concept, IMPROVE tenant/account ownership |
| E-EAG-DEEP-R3-002 | F-CONFIG-001 | Public API docs deep read | AI route attaches to Gateway and generates lower-level routing resources | KEEP behavior, AVOID generated-resource patching as primary UX |
| E-EAG-DEEP-R3-003 | F-CONFIG-001 | Public API docs deep read | Backend references include namespace rules, ReferenceGrant requirement, backend type split, and InferencePool limits | KEEP capability, IMPROVE authorization and capability flags |
| E-EAG-DEEP-R3-004 | F-CONFIG-001 | Public API docs deep read | Model matching depends on request-content extraction before routing | KEEP model routing, IMPROVE fail-closed parsing and anti-spoofing |
| E-EAG-DEEP-R3-005 | F-CONFIG-001 | Public API docs deep read | QuotaPolicy attaches to AI service backends and supports service/model quota, selectors, shadow mode, 429 enforcement | KEEP quota semantics, IMPROVE Account Hub/PostgreSQL reconciliation |
| E-EAG-DEEP-R3-006 | F-DEPLOY-002 | Public docs/release deep read | GatewayConfig centralizes external processor container config and may be shared by multiple Gateways | KEEP config layer, IMPROVE audit/dry-run/rollback |
| E-EAG-DEEP-R3-007 | F-DEPLOY-002 | Public docs deep read | InferencePool support requires optional inference extension resources and endpoint-picker path | KEEP as Plugin, AVOID treating as normal backend parity |
| E-EAG-DEEP-R3-008 | F-CONFIG-001 | Public API docs deep read | Status model is coarse Accepted/NotAccepted across key CRDs | AVOID single-condition Admin Ops model |

## §9 OPEN QUESTIONS

1. Controller-internal reconciliation conflict behavior when generated route artifacts are patched while the AI route changes was not directly observed.
2. Exact condition reason taxonomy for backend-not-found, grant-denied, processor-not-ready, quota-invalid, and generated-resource conflict was not observed.
3. Exact malformed JSON behavior during model extraction was not observed.
4. Exact behavior for client-supplied internal model header spoofing was not observed.
5. Exact body size limit and streaming-body behavior before route selection was not observed.
6. Exact quota storage backend and reconciliation persistence model was not observed.
7. Exact standalone/local parity for AI route and quota policy without Kubernetes was not observed.
8. Exact HA guidance for endpoint picker deployments was not observed in the read regions.
9. Exact rollback behavior for shared GatewayConfig changes was not observed.

## §10 SOURCE COVERAGE PROOF

| Region | Source read | Lines | What it contributed |
| --- | --- | --- | --- |
| region-1 | Upstream README rendered on GitHub | 343-351 | Two-tier gateway pattern and tier responsibilities |
| region-2 | Architecture docs | 50-64 | Control/data-plane split and request-path processor |
| region-3 | API reference, AI route kind | 70-78 | Generated routing artifacts, same-namespace creation, patchability, implementation-detail warning |
| region-4 | API reference, AI service backend kind | 143-146 | Backend resource lifecycle and generated backend-specific routing behavior |
| region-5 | API reference, GatewayConfig kind | 274-285 | GatewayConfig binding, same-namespace reference, shared config, env precedence |
| region-6 | API reference, route rule backendRefs | 548-566 plus 575-582 | Backend refs, cross-namespace ReferenceGrant, InferencePool restrictions, fallback split, timeout default |
| region-7 | API reference, backend reference fields | 612-690 | Backend target identity, namespace, InferencePool support, ignored knobs, weight/priority defaults |
| region-8 | API reference, route spec and costs | 715-744 and 830-833 | Gateway parent refs, generated routing, model extraction, per-route cost metadata |
| region-9 | API reference, statuses and backend schema | 834-906 | AI route/backend status shape and backend transformation inputs |
| region-10 | API reference, quota policy and quota types | 411-446 and 2352-2428 | QuotaPolicy purpose, target refs, service/model quota, client selectors, 429, shadow mode, sliding windows |
| region-11 | InferencePool guide | 707-789 and 1029-1043 | Gateway plus AI route configuration for InferencePool and advertised AI-specific benefits |
| region-12 | InferencePool guide annotations | 948-1004 | Processing body mode and mode override controls |
| region-13 | GatewayConfig capability docs | 275-357 | Migration from route-level processor resources, Gateway annotation, GatewayConfig status |
| region-14 | v0.5 release notes | 229-230, 253-278, 293-324 | GatewayConfig introduction, body mutation updates, policy target expansion, deprecations, migration guidance |
| region-15 | v0.5 release notes dependencies | 341-348 | Version dependency snapshot for Envoy Gateway, Gateway API, inference extension |
| region-16 | Architecture docs navigation | 67-80 | Upstream frames controller orchestration and external processor traffic flow as architecture sections |

## §11 ROUND-2 CRITIC FINDINGS

| Finding | Disposition | R3 handling |
| --- | --- | --- |
| C-001 generated-resource ownership hazard | CONFIRM-from-source | §2 S-4/S-5 and §9 OQ-1 capture generated resources, patchability, and unknown conflict behavior |
| C-002 cross-namespace tenant authorization | CONFIRM-from-source + HUAKAI inference | §2 S-7 confirms ReferenceGrant; §6 requires tenant_id and audit as HUAKAI adaptation |
| C-003 InferencePool narrower semantics | CONFIRM-from-source | §2 S-8/S-10 and §7 split backend capability flags |
| C-004 model routing body parsing and synthetic header | CONFIRM-from-source + OPEN | §2 S-12 confirms extraction; §9 OQ-3/OQ-4/OQ-5 keep parser/spoofing details open |
| C-005 default timeout behavior | CONFIRM-from-source | §2 S-13 and §4 timeout row |
| C-006 quota metadata and trust boundary | CONFIRM-from-source + HUAKAI inference | §2 S-14/S-15 confirm metadata/quota; §6 rejects header-derived billing keys |
| C-007 GatewayConfig sharing blast radius | CONFIRM-from-source | §2 S-17 and §6 shared config risk |
| C-008 status too weak | CONFIRM-from-source | §2 S-16 and §7 structured Health states |
| C-009 two-tier topology aspirational | CONFIRM-from-source + OPEN | §1 and §2 S-1 cite README; product tenant onboarding remains HUAKAI inference |
| C-010 standalone/local vs Kubernetes modes | OPEN-question-because-source-ambiguous | §9 OQ-7 |
| F-001 model route hides body dependency | CONFIRM-from-source | §2 S-12 and §6 anti-spoofing risk |
| F-002 inference optimization hides extra control plane | CONFIRM-from-source | §2 S-19 and §7 Plugin adaptation |
| F-003 fallback not field-level guarantee | CONFIRM-from-source | §2 S-9 splits fallback behavior |
| F-004 gateway-scoped config precedence | CONFIRM-from-source | §2 S-17 |
| F-005 version churn | CONFIRM-from-source | §6 version churn risk and §10 region-14 |
| D-001 responsibility distributed across resources | CONFIRM-from-source | §1 and §2 span route, backend, quota, GatewayConfig |
| D-002 generated resource stability drift | CONFIRM-from-source | §2 S-4 states implementation-detail warning |
| D-003 alpha/beta maturity mixing | CONFIRM-from-source | §10 records latest API contains v1alpha1 and v1beta1; HUAKAI avoids exposing churn |
| D-004 older version migration drift | CONFIRM-from-source | §10 region-14 and §6 version risk |
| D-005 InferencePool mixed route vs mixed rule | CONFIRM-from-source | §2 S-8 says individual rule cannot mix backend types |
| N-001 do not copy tenant header quota key | CONFIRM-as-HUAKAI-risk | §6 quota trust boundary |
| N-002 do not copy generated patch model | CONFIRM-as-HUAKAI-risk | §7 validated policy API |
| N-003 do not copy single-condition status | CONFIRM-as-HUAKAI-risk | §7 structured Health |
| N-004 do not copy shared config precedence | CONFIRM-as-HUAKAI-risk | §6 shared config risk |
| N-005 do not copy InferencePool RBAC shape blindly | OPEN-question-because-source-ambiguous | RBAC shape was not verified in read regions; §7 still requires Plugin health/RBAC review as HUAKAI inference |
| N-006 do not copy implicit model header | CONFIRM-as-HUAKAI-risk | §6 anti-spoofing and §7 internal request attribute |
| N-007 do not copy version churn | CONFIRM-as-HUAKAI-risk | §6 version churn |
| N-008 do not copy namespace as tenancy primitive | CONFIRM-as-HUAKAI-risk | §6 namespace tenant risk |
| S-001 endpoint picker one replica smell | OPEN-question-because-source-ambiguous | §9 OQ-8 |
| S-002 hidden global/shared state | CONFIRM-from-source | §2 S-17 |
| S-003 magic constants | CONFIRM-from-source | §2 S-13 timeout; §2 S-20 / region-12 annotations; other ports not included because not product behavior |
| S-004 fail-open override risk | CONFIRM-from-source + HUAKAI inference | §2 S-20 confirms override option; §7 recommends gated Plugin behavior |
| S-005 tenant leakage potential | CONFIRM-as-HUAKAI-risk | §6 namespace tenant risk |
| S-006 inconsistent error taxonomy | CONFIRM-from-source + OPEN | §2 S-16 confirms weak status; §9 OQ-2 asks for exact reasons |
| S-007 recovery gap | OPEN-question-because-source-ambiguous | §9 OQ-1/OQ-9 |
| Recommendation: fail-closed model extraction | CONFIRM-as-HUAKAI-required | §6 and §7 include fail-closed adaptation |
| Recommendation: split backend semantics | CONFIRM-from-source | §2 S-8/S-10 and §7 capability flags |
| Recommendation: generated ownership/status/operator overrides | CONFIRM-from-source + OPEN | §2 S-4/S-5/S-16 and §9 OQ-1/OQ-2 |
| Recommendation: tenant/account identity from HUAKAI | CONFIRM-as-HUAKAI-required | §5 SaaS and §6 |
| Recommendation: GatewayConfig audit/rollback | CONFIRM-as-HUAKAI-required | §5 and §6 |
| Recommendation: InferencePool plugin | CONFIRM-as-HUAKAI-required | §5 and §7 |

Owner 中文总结：本轮拆解的是 envoy-ai-gateway 的双层拓扑、AI route 到 Gateway/Backend/Quota/GatewayConfig 的行为边界，以及 InferencePool 和状态条件的生产化风险；真观察来自 16 个公开 source/docs/release 区域，主要覆盖生成路由资源、backendRef、ReferenceGrant、model 提取、token cost、QuotaPolicy、GatewayConfig、InferencePool 限制和 Accepted/NotAccepted 状态，合理推断集中在 HUAKAI tenant_id、Account Hub、PostgreSQL quota/billing、Admin Ops 健康状态和插件化适配；critic 的 C/F/D/N/S 和 synthesis 建议都已逐项处置，能从 source 确认的标为 CONFIRM，源码未能直接观察的竞态、parser 失败、spoofing、HA、rollback 等标为 OPEN；当前 open question 数量为 9，本文件没有为了字数补造 controller 内部行为。
