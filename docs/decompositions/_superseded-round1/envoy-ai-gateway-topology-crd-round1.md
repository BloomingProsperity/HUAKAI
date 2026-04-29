# Envoy AI Gateway - Outer/Inner Gateway Topology + AI Route CRD

| Field | Value |
| --- | --- |
| Status | Draft |
| Reference | Envoy AI Gateway (Apache-2.0, E-LIC-008) |
| Feature in HUAKAI matrix | F-ARCH-001 + F-DEPLOY-002 + F-CONFIG-001 |
| Evidence ledger row | E-EAG-001 |
| Specifier session | Codex specifier-lane, 2026-04-29 |
| Specifier date | 2026-04-29 |
| Reviewer session | Pending |
| Reviewer date | Pending |
| Source files read | https://github.com/envoyproxy/ai-gateway<br>https://pkg.go.dev/github.com/envoyproxy/ai-gateway<br>https://aigateway.envoyproxy.io/docs/api/<br>https://aigateway.envoyproxy.io/docs/concepts/resources/<br>https://aigateway.envoyproxy.io/docs/capabilities/inference/aigatewayroute-inferencepool/<br>https://github.com/envoyproxy/ai-gateway/releases |

## 1. WHY

Envoy AI Gateway solves a pressure that the other HUAKAI reference projects mostly avoid: large operators want the public gateway boundary and the self-hosted Model cluster boundary to be separable. The public side needs API Key auth, User and User Group resolution, top-level Route selection, global Quota policy, and platform-wide abuse controls. The model-cluster side needs local access to Provider endpoints, self-hosted inference pools, Model-specific dispatch, and endpoint selection. E-EAG-001 records this two-tier split.

The motivation is operational separation, not simply more hops. A SaaS operator can centralize tenant-facing policy while different clusters or regions own model-serving details. That matters when a Provider endpoint is a private Model cluster with its own scheduling, health, and capacity signals. The upstream also uses Kubernetes-native resources so operators can declare routing and backend intent through the same control-plane pattern they use for gateways, policies, and cluster services. Inference: this design is less relevant to single-node users, because the benefits arrive only when the gateway, identity boundary, and model-serving boundary are operated separately.

## 2. WHAT

The upstream behavior decomposes into three layers in HUAKAI vocabulary.

First, an outer gateway acts as the client-facing Route boundary. A client request enters through the public gateway endpoint. The outer tier is responsible for platform auth, global Quota, and coarse Route selection. It should know the User, User Group, API Key, requested Model, and allowed Channel set before any request reaches inner model-serving infrastructure.

Second, an inner gateway acts as the Provider-access boundary for self-hosted or cluster-local Model capacity. The inner tier receives already-governed traffic and applies model-cluster dispatch. Its main job is to turn the selected logical Model and Channel intent into a concrete Provider endpoint or inference pool member. In HUAKAI terms, the inner tier is closest to Channel and Provider Account execution, while the outer tier is closest to User and API Key policy.

Third, Kubernetes resources declare this surface. A route resource describes a unified AI API attached to gateway listeners, then lists matching rules and backend choices. A backend resource describes a single AI-serving target with the API shape it expects and the underlying service or gateway backend it reaches. Security policy resources attach upstream credential behavior to backend access. Status conditions publish whether reconciliation accepted the declared object. The route declaration can also point at an inference pool for model-cluster endpoint selection. HUAKAI should preserve the semantic split: Route selects among Channel-like targets; backend describes one reachable AI-serving target; endpoint picker chooses a concrete serving endpoint behind that target.

## 3. INPUTS

Inputs include the incoming request path, headers, body, requested Model, API Key identity after auth, User Group policy, global Quota state, and any tenant-scoped routing tags HUAKAI adds.

Configuration inputs include gateway attachments, route match conditions, ordered or weighted backend choices, optional model-name rewrite, backend API schema, Provider endpoint reference, upstream credential policy, request mutation policy, and quota/cost metadata. For inner-tier deployments, additional inputs include inference pool membership, endpoint health, model capacity signals, and controller-generated status.

State mutated by the control plane is declarative rather than request-local: generated gateway routing resources, gateway-scoped external processor configuration, and status conditions on route, backend, security, quota, and gateway configuration resources. Runtime state includes selected Channel, selected Provider Account or Provider endpoint, chosen inner endpoint, Usage Record attribution, and Quota reservation/reconciliation.

## 4. FAILURE MODES HANDLED

- Invalid or unsupported route/backend declaration: detected during reconciliation; response is a not-accepted status condition visible to operators.
- Backend reference cannot be resolved or is not permitted across namespaces: detected by the Kubernetes/Gateway policy model; response is no accepted attachment until the reference relationship is allowed.
- Backend expects a different API shape than the public route exposes: handled by declaring input and output API schemas so the gateway can transform between them when supported.
- Multiple model-serving endpoints exist for the same logical Model: handled by routing to an inference pool or backend set where endpoint selection is delegated to model-aware infrastructure.
- Gateway-level processor settings differ across deployments: handled by a gateway-scoped configuration resource that can be shared by multiple gateways and referenced declaratively.
- Operator needs to know whether a resource took effect: handled through status conditions on the custom resources, currently expressed as accepted vs not accepted outcomes.

## 5. INTERFACES TO HUAKAI

For Personal Edition, HUAKAI should keep a single-tier gateway: API Key auth, User Group resolution, Quota reservation, Route match, Channel selection, Provider Account selection, Provider call, Usage Record, and Quota reconciliation remain in one deployable service. Personal Edition may expose the same conceptual Route and Channel model, but it must not require Kubernetes or an inner gateway.

For SaaS Edition, HUAKAI should model the outer tier as the operator-control entry point. It owns API Key validation, User and User Group policy, tenant-level Quota, audit, global abuse controls, and top-level Route selection. The optional inner tier is reserved for enterprise or self-hosted Model clusters. It owns Channel execution, Provider Account or Provider endpoint access, model-cluster endpoint picking, and local observability.

The upstream Backend resource is semantically narrower than HUAKAI pool_groups. In HUAKAI, a Channel can pool several Provider Accounts, expose model aliases, apply per-channel limits, and carry status. The upstream backend concept is closer to one reachable AI-serving target plus API compatibility metadata. HUAKAI should map it to a Channel backend adapter, not replace Channel or pool_groups outright.

## 6. RISKS

- Two tiers can hide policy gaps if the inner tier trusts traffic without verifying that the outer tier already enforced User Group and Quota decisions.
- A declarative route surface can drift from Admin UI state unless HUAKAI has one source of truth or a clear import/sync rule.
- Status conditions that only say accepted or not accepted are too coarse for SaaS operators who need a reason, last transition time, affected Route, affected Channel, and remediation hint.
- Cross-namespace or cross-cluster routing can become a tenant isolation risk if backend attachment is not owner-approved.
- Backend-level request mutation can create protocol or audit ambiguity unless Usage Records capture the pre-route Model, final upstream Model, Channel, and Provider Account.
- Kubernetes-first deployment is unsuitable for Personal Edition and small self-hosters.

## 7. SAFE ADAPTATION FOR HUAKAI

- Personal Edition stays single-tier and non-Kubernetes-runnable, with optional export of declarative Route/Channel config for future migration.
- SaaS Edition adds an optional split-tier deployment flag: outer gateway required, inner gateway only when a tenant or operator enables cluster-local Model serving.
- HUAKAI Route remains the tenant-facing selection rule; Channel remains the abstraction over Provider Account selection; inner endpoint picking is a sub-policy of Channel, not a replacement for Route.
- HUAKAI should expose richer operator status than the upstream minimum: accepted state, reason, validation error, blocked dependency, last successful generation, and impacted tenant scope.
- HUAKAI should support Kubernetes CRDs as one control-plane input in SaaS Edition Phase 10+, but Admin UI and API contracts remain canonical or explicitly synchronized.
- Backend attachment across namespaces or clusters must require an owner-approved grant equivalent, audited as an Audit Event.
- Request mutation must be feature-flagged, logged, and reflected in Usage Records so billing and debugging remain explainable.

## 8. EVIDENCE LEDGER ROWS

- E-LIC-008: Envoy AI Gateway is Apache-2.0 and is safe as a behavioral reference.
- E-EAG-001: Two-tier topology: outer gateway for authentication, identity, top-level routing, and global rate limiting; inner gateway for self-hosted model-cluster ingress and inference endpoint selection.
- E-EAG-002: Endpoint-picker behavior selects among model-cluster endpoints and maps to HUAKAI endpoint selection under Channel/Pool policy.
- E-EAG-003: Kubernetes-native CRD/operator deployment is a primary surface and maps to HUAKAI SaaS deployment roadmap, not Personal Edition requirements.

## 9. OPEN QUESTIONS

- Should HUAKAI SaaS Edition allow the Admin UI to generate Kubernetes resources, or should CRDs import into the Admin API as external desired state?
- What is the minimum status condition vocabulary HUAKAI needs before an operator can safely run split-tier routing in production?
- Should inner-tier Provider Account access be tenant-isolated by namespace, by cluster, or by explicit Channel ownership?
- How should Usage Records represent a request that enters through an outer Route but is finally served by an inner endpoint picker?
- Should split-tier deployment require mutual service identity between tiers before any traffic is accepted?

Owner 总结：本文件拆解了 Envoy AI Gateway 的外层/内层网关拓扑和 AI Route/Backend 类 CRD 的行为语义；与已有 sub2api 拆解的关键差异是，sub2api 更偏单体协议适配和账号转发，而 Envoy AI Gateway 提供的是 Kubernetes/CNCF 原生的分层部署与声明式控制面；HUAKAI 应吸收“外层管 API Key、User/User Group、全局 Quota、顶层 Route，内层管 Channel 执行、Provider endpoint 和模型集群 endpoint picker”的架构边界，同时明确 Personal Edition 保持单层，SaaS Edition 才引入可选双层与 CRD 同步能力。
