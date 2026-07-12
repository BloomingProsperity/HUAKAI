# Envoy AI Gateway - Outer/inner gateway topology + AI Route CRD reconciliation + Backend lifecycle + Quota Policy attachment
| Field | Value |
| --- | --- |
| Status | Draft |
| Reference | Envoy AI Gateway, Apache-2.0, E-LIC-008 |
| Feature in HUAKAI matrix | F-ARCH-001 + F-DEPLOY-002 + F-CONFIG-001 |
| Evidence ledger row | E-EAG-001, E-EAG-002, E-EAG-003; Round-2 source-deep evidence to be added as E-EAG-DEEP-001..006 |
| Specifier session | Codex specifier-lane Round 2 |
| Specifier date | 2026-04-29 |
| Reviewer session | Pending reviewer-lane |
| Reviewer date | Pending |
| Source files read | https://github.com/envoyproxy/ai-gateway, https://pkg.go.dev/github.com/envoyproxy/ai-gateway, https://pkg.go.dev/github.com/envoyproxy/ai-gateway/internal/translator, https://aigateway.envoyproxy.io/docs/api/, https://aigateway.envoyproxy.io/docs/concepts/architecture/, https://aigateway.envoyproxy.io/docs/capabilities/inference/aigatewayroute-inferencepool/, https://github.com/envoyproxy/ai-gateway/releases |
## 1. WHY (motivation / context)
Envoy AI Gateway solves a Kubernetes-native operations problem: operators want AI request routing to be declared as infrastructure, reconciled into data-plane resources, and separated from application teams that only know Model and Provider intent.
The reference is not only a simple reverse proxy. Its public README and architecture documentation describe a two-tier topology: an outer tier handles platform-facing concerns such as authentication, identity, top-level routing, and global limits; an inner tier reaches model-serving clusters and can use endpoint selection for self-hosted inference. This corroborates E-EAG-001 and E-EAG-002, but the critic is correct that the README topology is aspirational as a product contract. The API and package material distribute behavior across Route resources, Provider Account-like backend resources, credential policies, gateway-scoped configuration, generated data-plane objects, cost metadata, and external Gateway policy. HUAKAI must therefore absorb the separation-of-concerns idea, not copy the upstream resource split as the product boundary.
The real pressure behind the design is operator delegation. Platform operators own a shared gateway, application teams own Route intent, model-platform teams may own model pools, and security teams own credential and quota policy. Kubernetes users expect that a desired state object can generate lower-level runtime objects, can be patched through standard policy APIs, and can report readiness through status. That pressure explains why the source docs say an AI Route generates a gateway HTTP route and a same-namespace filter resource, and why the docs also call that generated shape an implementation detail. The critic's C-001 and D-002 findings are corroborated: generated resources are visible enough to become an operations surface, but unstable enough that relying on them as a stable product API is risky.
HUAKAI's context is different. HUAKAI is an AI Gateway + Account Hub + Admin Ops Platform, not just a Kubernetes controller. DR-001 requires tenant-aware state from day one. DR-002 requires Personal Edition first and SaaS Edition later, with one codebase and edition-gated surfaces. DR-006 requires PostgreSQL as the production state engine, with explicit transaction boundaries for quota, billing, usage, and audit. Therefore the feature must be decomposed as a HUAKAI control-plane capability:
- Personal Edition keeps a single-tier gateway as the default.
- SaaS Edition can add an outer/inner topology when scale or enterprise isolation justifies it.
- Kubernetes CRDs are one deployment surface, not the only product contract.
- Route intent, Channel selection, Provider Account lifecycle, Quota, Usage Record, Billing Ledger, and Audit Event remain HUAKAI first-class concepts.
- Generated data-plane resources are replaceable output, not the authority of record.
Round 1 was shallow because it stopped at "there is a CRD and a topology." Round 2 must be deeper: it must specify reconciliation ownership, tenant authorization, InferencePool limitations, model extraction failure handling, timeout defaults, quota trust boundaries, GatewayConfig blast radius, status shape, edition portability, and version drift.
## 2. WHAT (algorithm in HUAKAI vocabulary)
### S-1 Route intent acceptance
Trigger condition: An operator creates or updates a Route-like declaration through HUAKAI Admin Ops, config-as-code, or a Kubernetes adapter.
State transitions: HUAKAI writes a tenant-scoped Route draft, validates referenced Channels and Provider Accounts, records an Audit Event, and moves the Route from draft to pending-reconcile. In Kubernetes mode, the adapter may observe an AI Route CRD and convert it into the same internal Route intent.
Concurrency interaction: Two updates to the same Route must be serialized by tenant_id plus Route id. Later writes must not erase prior generated-resource conflict state until a new reconcile attempt records a replacement outcome. Concurrent updates to different Routes can proceed if they do not mutate the same generated data-plane identity.
### S-2 Parent gateway attachment
Trigger condition: A Route intent names one or more gateway parents or deployment targets.
State transitions: HUAKAI records an attachment edge from Route to gateway target, validates edition constraints, and marks each parent attachment as accepted, denied, or pending. This addresses critic C-008 and N-003: a single Accepted/NotAccepted flag is insufficient. HUAKAI must track per-parent status.
Concurrency interaction: If a gateway target is deleted while a Route is reconciling, the Route attachment becomes stale and a reconcile retry must avoid creating orphaned runtime resources. If two Routes attach to the same gateway at once, their generated matches must be conflict-checked under one parent-scoped lock.
### S-3 Tenant boundary check before backend resolution
Trigger condition: A Route references a Channel or Provider Account outside its local administrative namespace or adapter context.
State transitions: HUAKAI resolves tenant_id from the authenticated operator or API Key context, not from a namespace or request header. Cross-namespace consent in Kubernetes mode is treated as necessary but not sufficient. HUAKAI requires a tenant authorization edge: owning tenant, consuming tenant, target kind, allowed Route ids or patterns, expiration, approval actor, and Audit Event.
Concurrency interaction: If a grant is revoked while reconciliation is running, the Route must fail closed before new runtime output becomes active. Already-active traffic must enter a draining or denied state according to rollout policy, and the revocation Audit Event must sort before any subsequent successful attachment event.
### S-4 Channel and Provider Account lifecycle binding
Trigger condition: A Route references one or more Channels that expose a logical Model set and map to Provider Accounts.
State transitions: HUAKAI reads Channel status, allowed Model list, Provider Account lifecycle state, credential policy status, and health state. Eligible Channel references become routable; disabled, expired, quota-exhausted, under-investigation, or degraded Provider Accounts produce structured denial reasons. This maps the upstream backend resource lifecycle into HUAKAI vocabulary.
Concurrency interaction: Channel health, Provider Account disablement, and Route reconciliation are multiple writers to routability. HUAKAI must define precedence: hard operator disable beats health recovery; credential expiry beats route weight; tenant suspension beats all routing. Each writer records a reason and does not silently overwrite another writer's reason.
### S-5 Generated runtime resource ownership
Trigger condition: A Route accepted by at least one gateway target needs data-plane output.
State transitions: HUAKAI generates runtime route output and any required request-processing filter output. The source docs say the reference generates a gateway HTTP route and a host-rewrite filter in the Route namespace, and operators may patch generated resources with Gateway policy APIs. The critic's C-001 claim is corroborated by that source pattern: generated resources are live operational objects, not static compilation artifacts.
Concurrency interaction: HUAKAI must handle three-way races: operator patch to generated output, Route edit, and controller resync. The owner of truth is the HUAKAI Route intent plus approved override policy. A manual patch outside approved override policy is detected as drift, surfaced to Admin Ops, and either reverted or quarantined depending on safety. A validated override is preserved across resync and has its own Audit Event.
### S-6 Model extraction before Route matching
Trigger condition: A client request reaches the gateway and the Route match depends on requested Model.
State transitions: HUAKAI parses the request envelope before Route match, derives an internal model_route_key, blocks tenant-supplied spoofing of any internal routing header, and stores the derived value in request-local state. The source docs expose an internal synthetic model header as available for matching after model extraction. The critic's C-004 and F-001 claims are confirmed: model routing depends on request content, not a normal client-supplied header.
Concurrency interaction: Model extraction is per-request and should not mutate shared state. However, parser configuration and body-size limits are shared gateway policy. If policy is updated while requests are active, each request must use the policy version captured at ingress so identical request bodies are not routed differently mid-flight.
### S-7 Fail-closed parser outcomes
Trigger condition: Model extraction sees malformed JSON, unsupported endpoint shape, streaming body ambiguity, oversized body, missing model, or a client-supplied value that collides with an internal routing attribute.
State transitions: HUAKAI rejects or routes to a dedicated safe error path before Provider Account use. It writes a Usage Record only if the request passed API Key authentication and reached billable admission; it does not decrement Provider Account quota for parser failures. It emits an operator-visible event with redacted parser reason and request trace id.
Concurrency interaction: Parser failure handling must be stateless except for Usage Record and Audit/Event append. Under concurrent malformed bursts, rate limiting and abuse detection should aggregate by authenticated User, API Key, tenant, and source IP, not by untrusted model header.
### S-8 Normal AI backend rule semantics
Trigger condition: A Route rule selects one or more ordinary Channels backed by Provider Accounts.
State transitions: HUAKAI applies match criteria, then ordered Channel preference, weights, priority, mutation policy, timeout policy, and fallback policy. Provider Account selection occurs within the selected Channel. Quota reservation happens before upstream spend, and final Usage Record settlement occurs after completion or terminal failure.
Concurrency interaction: Multiple requests against the same User quota and Provider Account quota must use PostgreSQL row locks or advisory locks per DR-006. In-memory counters may accelerate but cannot be the billing or quota authority.
### S-9 InferencePool-like backend rule semantics
Trigger condition: A Route rule selects a self-hosted model pool through an endpoint picker integration.
State transitions: HUAKAI treats this as a Plugin or Feature Flag module, not a plain Channel variant. The source docs say a single rule may use only one model-pool backend, must not mix it with ordinary backend references in that rule, and ignores model override, header/body mutation, weight, and priority for that pool path. The critic's C-003 and D-005 claims are confirmed. HUAKAI preserves this by allowing multiple rules under one Route for model-based split, but forbidding mixed backend types inside one rule.
Concurrency interaction: Endpoint picker health is a shared dependency. If endpoint picker readiness changes while ordinary Channel routing remains healthy, only pool-backed rules degrade. Concurrent picker choices must be bounded by pool-level concurrency and per-tenant budgets, and a single picker instance must not become a silent single point of failure.
### S-10 Quota Policy attachment
Trigger condition: A Route, gateway target, Channel, User Group, or tenant has a Quota Policy attached.
State transitions: HUAKAI derives tenant/account identity from authenticated Account Hub context. It may use Envoy-style dynamic metadata as a data-plane hint, but quota and Billing Ledger keys are PostgreSQL-backed and signed or server-derived. The critic's C-006 and N-001 claims are confirmed: source examples show bucket selection keyed by a tenant-like header through external Gateway policy, but HUAKAI must not trust a client-provided tenant header for quota or billing.
Concurrency interaction: Quota reservation and final reconciliation are multi-writer hot paths. Concurrent requests for the same Quota must lock or atomically update one tenant-scoped row/window. Data-plane metadata arriving late or duplicated cannot create duplicate Billing Ledger entries because the request trace id and idempotency key gate settlement.
### S-11 Timeout policy selection
Trigger condition: A Route has no explicit request timeout, a streaming request begins, or an operator overrides defaults.
State transitions: Source docs say the AI-specific default request timeout is 60 seconds rather than the lower default from the generic gateway layer. The critic's C-005 and S-003 claims are confirmed. HUAKAI must define edition defaults separately: request timeout, stream idle timeout, total stream lifetime, upstream response-header timeout, quota reservation expiration, and accounting finalization timeout.
Concurrency interaction: Timeout policy is captured per request at ingress. If an operator changes timeout while long streams are active, existing requests keep the captured policy unless the change is an emergency kill policy with explicit Audit Event and tenant-scope reason.
### S-12 Gateway-scoped configuration resolution
Trigger condition: A gateway target references gateway-scoped configuration or a shared config object.
State transitions: Source docs indicate multiple gateway targets can reference one config object and that per-gateway config environment values can override controller-global values. The critic's C-007, F-004, and S-002 claims are confirmed. HUAKAI must resolve effective config as platform defaults -> edition policy -> tenant policy -> gateway target policy -> approved emergency override, with forbidden keys blocked from tenant control.
Concurrency interaction: A shared config update can affect many gateway targets. HUAKAI must stage the change, compute blast radius, dry-run generated output, require approval for high-impact keys, and roll out by scope. Concurrent Route reconciles must use a config version pin so partial rollout does not produce non-reproducible behavior.
### S-13 Status normalization
Trigger condition: Any reconcile or runtime health check completes, fails, or becomes ambiguous.
State transitions: Source API status semantics are documented as a limited accepted/not accepted condition for several resource types. The critic's C-008, N-003, and S-006 claims are confirmed: that status shape is too weak for Admin Ops recovery. HUAKAI writes structured conditions per parent gateway, Route rule, Channel, Provider Account, credential policy, Quota Policy, tenant authorization edge, generated runtime output, and plugin readiness.
Concurrency interaction: Status writers append observations with generation and source. A late health check must not overwrite a newer operator denial. Admin Ops reads the latest effective condition per dimension plus a timeline for audit.
### S-14 Version compatibility and adapter migration
Trigger condition: Kubernetes CRD versions, adapter versions, or upstream-style fields change.
State transitions: Release notes and docs show version churn: route attachment patterns changed, schema fields moved or disappeared, security policy attachment changed, endpoint prefixes shifted, and processor resource configuration moved. The critic's F-005, D-003, D-004, and N-007 claims are confirmed. HUAKAI must keep a stable internal Route contract and treat Kubernetes CRD compatibility as adapter input/output, not as the public Admin Ops contract.
Concurrency interaction: During migration, old and new adapters may observe the same external resources. HUAKAI must use generation and adapter ownership markers to prevent double generation, and migration must record an Audit Event and rollback target.
### S-15 Edition portability
Trigger condition: A feature is invoked in Personal Edition or SaaS Edition.
State transitions: Personal Edition supports single-tier Route -> Channel -> Provider Account routing, config-as-code, Admin Ops UI, PostgreSQL-backed Usage Records, and Quota. It does not require Kubernetes. SaaS Edition may enable outer/inner gateway topology, tenant onboarding, cross-tenant Admin Ops, and Kubernetes operator integration. The critic's C-010 claim is confirmed: standalone/local and Kubernetes modes should not leak into one mental model.
Concurrency interaction: Edition switches are high-risk operational changes. In-flight requests keep the edition mode captured at ingress. New requests use the new mode only after config commit, Audit Event append, and health gates pass.
## 2-bis. Request lifecycles
### Happy-path lifecycle
1. Client presents API Key and request body.
2. Gateway authenticates API Key and resolves tenant_id, User, User Group, and API Key state from Account Hub.
3. Gateway captures request policy version: Route generation, timeout policy, quota policy, edition mode, and parser limits.
4. Gateway parses the request envelope and derives internal model_route_key.
5. Route matching selects a Route rule and eligible Channel or pool plugin.
6. Quota reservation happens in PostgreSQL before Provider Account or pool spend.
7. Channel selects a Provider Account, or the pool plugin selects an endpoint with a recorded reason.
8. Upstream call completes successfully.
9. Usage Record is appended with tenant_id, User, API Key, Route, Channel, Provider Account or pool, Model, timing, usage source, final status, and trace id.
10. Quota and optional Billing Ledger reconcile in an explicit transaction.
11. Response returns to client with internal routing attributes stripped.
### Partial-failure lifecycle
1. Request authenticates, parses, matches a Route, and reserves Quota.
2. Primary Channel or pool endpoint fails before any client-visible output.
3. Gateway classifies failure: provider unavailable, credential denial, rate limited, timeout, picker unready, or policy conflict.
4. If fallback is allowed for the failure class, HUAKAI releases or adjusts the attempt-specific reservation and tries the next Channel under the same request id.
5. The Usage Record preserves each attempt summary or references attempt records; the final record names the successful Channel and the fallback reason.
6. Operator-visible signal shows the degraded Channel or pool reason.
7. If fallback succeeds, Billing Ledger settlement happens once using the request idempotency key.
### Full-failure lifecycle
1. Request authenticates or fails before authentication.
2. If authentication fails, no tenant quota is touched; security event may be aggregated.
3. If authentication succeeds but model parsing fails, HUAKAI fails closed before Provider Account use.
4. If parsing and matching succeed but every eligible Channel is denied or all pool endpoints are unavailable, no fallback remains.
5. Quota reservation is released or settled as failed according to whether upstream spend occurred.
6. Usage Record is appended if the request reached authenticated admission; otherwise only an abuse/security event is written.
7. Generated runtime output is not changed by request-level failure.
8. Admin Ops shows repair target: Route, Channel, Provider Account, credential policy, Quota Policy, tenant grant, gateway config, or pool plugin.
## 3. INPUTS (signals consumed, state mutated)
### Per-request data
Fields read:
- API Key credential presented by client.
- Request path and method.
- Request body envelope.
- Requested Model inside body when present.
- Streaming preference.
- Request headers after header firewall.
- Client IP and connection metadata.
- Request id or generated trace id.
- Idempotency key if supplied, otherwise server-generated.
- Tenant context resolved from API Key.
- Policy version captured at ingress.
Fields written:
- Internal model_route_key.
- Chosen Route id and Route generation.
- Chosen Route rule id.
- Chosen Channel id or pool plugin id.
- Chosen Provider Account id or endpoint reason.
- Quota reservation id.
- Attempt records.
- Usage Record terminal status.
- Billing Ledger reference when chargeable.
- Sanitized operator error reason.
- Response header allowlist outcome.
### Per-Account / per-Channel state
State read:
- Provider Account lifecycle: active, disabled, expired, quota-exhausted, under-investigation.
- Provider Account credential policy health.
- Provider Account region, tier, capability tags, and health snapshot.
- Channel enabled/paused/degraded state.
- Channel allowed Model list and Model mapping.
- Channel per-model timeout and fallback policy.
- Channel per-tenant eligibility and User Group eligibility.
- Channel concurrency and rate limits.
State mutated:
- Health state observations.
- Cooldown or quarantine state.
- Operator disable/enable transitions.
- Quota windows tied to Provider Account or Channel.
- Audit Events for every operator mutation.
- Usage-linked success/failure counters.
Lifetime:
- Provider Accounts and Channels are durable PostgreSQL rows in HUAKAI.
- Health snapshots are durable enough for Admin Ops and may be cached in process for routing.
- In-memory routing cache entries are derived and invalidated by generation, not source of truth.
### Per-tenant state
Isolation boundaries:
- tenant_id on every primary table per DR-001 and DR-006.
- Route, Channel, Provider Account, Quota, Usage Record, Billing Ledger, Audit Event, config version, and generated-output ownership are tenant-scoped unless explicitly platform-owned.
- Cross-tenant backend access requires a tenant authorization edge and Audit Event.
- Kubernetes namespace or ReferenceGrant-like consent is not tenant authority.
- Request headers are never tenant authority.
### Per-process state
In-memory caches and queues:
- Route generation cache.
- Effective config cache.
- Gateway target cache.
- Channel and Provider Account health cache.
- Parser policy cache.
- Generated-output reconcile queue.
- Status update queue.
- Plugin readiness cache.
- Goroutine-local request context with trace id, policy version, and attempt history.
Concurrency obligations:
- Caches must carry tenant_id and generation.
- Queues must de-duplicate by tenant_id plus target resource id plus generation.
- Process-local state cannot enforce cluster-wide quota, billing, or tenant isolation.
### Persistent state
Tables and indexes touched in HUAKAI design:
- tenants: tenant lifecycle and edition mode.
- users: User identity and tenant membership.
- api_keys: API Key state and owner.
- user_groups: quota and Route eligibility grouping.
- routes: desired Route intent, generation, and status summary.
- route_rules: match criteria and Channel preference.
- route_attachments: per-parent gateway attachment state.
- channels: Channel configuration and lifecycle.
- provider_accounts: Provider Account lifecycle and credential reference.
- model_registry: logical Model mapping.
- quota_policies: policy attachment, scope, and version.
- quota_reservations: in-flight request reservation.
- quota_windows: durable counters.
- usage_records: append-only request outcome.
- billing_ledger: append-only charge or adjustment.
- audit_events: append-only operator and system action log.
- generated_outputs: runtime output identity, owner, drift state, and override policy.
- gateway_configs: effective gateway config versions and blast radius.
- tenant_authorizations: cross-tenant or cross-namespace access grant.
- plugin_health: pool picker and external module readiness.
Index requirements:
- tenant_id plus id on every primary entity.
- tenant_id plus Route generation for reconciliation.
- tenant_id plus API Key hash for authentication.
- tenant_id plus quota scope and window for reservation.
- tenant_id plus request id/idempotency key for settlement.
- tenant_id plus generated output identity for drift detection.
- tenant_id plus Audit Event timestamp for Admin Ops trace.
Transaction boundaries:
- Route update plus Audit Event in one transaction.
- Quota reservation before upstream spend in one transaction.
- Final Usage Record plus quota reconciliation plus Billing Ledger append in one transaction when chargeable.
- Generated-output ownership update plus status condition in one transaction.
- Cross-tenant authorization grant/revoke plus Audit Event in one transaction.
## 4. FAILURE MODES HANDLED
### FM-1 Malformed or unsupported request body
Trigger: Body parser cannot derive a valid Model or endpoint shape.
Observable outcome: Request fails before Channel or Provider Account use.
Operator-visible signal: Redacted parser_failure condition with trace id and endpoint category.
Recovery action: Fix client request, add supported endpoint adapter, or adjust parser limit policy.
Blast radius: Single request; burst can become single-tenant abuse signal.
### FM-2 Tenant spoofing through header
Trigger: Client sends a tenant or model-routing header that conflicts with Account Hub context or internal route key.
Observable outcome: Header is stripped or request is denied fail-closed.
Operator-visible signal: security_event with API Key, User, tenant_id, and redacted header name class.
Recovery action: Client stops sending reserved headers; operator may block compromised API Key.
Blast radius: Single tenant if API Key scoped correctly.
### FM-3 Cross-namespace backend denied
Trigger: Kubernetes adapter sees namespace consent but HUAKAI tenant authorization is missing, expired, or revoked.
Observable outcome: Route attachment denied for that Channel or Provider Account.
Operator-visible signal: per-backend condition names missing tenant authorization and referent owner.
Recovery action: Owning tenant grants access through Admin Ops or operator rewrites Route to local Channel.
Blast radius: Single Route or single tenant authorization edge.
### FM-4 Generated output drift
Trigger: Operator patches generated data-plane output outside approved override policy, or controller resync races with a Route edit.
Observable outcome: Drift condition appears; HUAKAI reverts, quarantines, or preserves only validated overrides.
Operator-visible signal: generated_output_drift status plus Audit Event for accepted override or denied patch.
Recovery action: Move patch into first-class policy or approve an override with rollback target.
Blast radius: Single gateway target or Route; shared generated filters can affect multiple Routes if not isolated.
### FM-5 Inference pool plugin unready
Trigger: Endpoint picker service, pool controller, required permissions, or health probes are unavailable.
Observable outcome: Pool-backed Route rules deny or degrade; ordinary Channel rules remain eligible.
Operator-visible signal: plugin_health condition with readiness gate and missing dependency.
Recovery action: Repair picker deployment, permissions, or health gate; scale to HA.
Blast radius: Single pool or tenant unless shared picker is global.
### FM-6 Shared gateway config bad rollout
Trigger: A shared gateway config changes resource settings, processor mode, telemetry, or environment overrides.
Observable outcome: Dry-run fails, rollout pauses, or affected gateway targets enter degraded state.
Operator-visible signal: config_blast_radius report and per-gateway condition.
Recovery action: Roll back config version or narrow scope; high-impact keys require approval.
Blast radius: Potentially single process, multiple tenants, or cluster-wide depending on sharing.
### FM-7 Timeout too short or too long
Trigger: Default timeout kills valid stream, or long stream exceeds accounting budget without final usage.
Observable outcome: Request ends with timeout status, quota reservation released or settled as partial, and no indefinite quota hold.
Operator-visible signal: timeout condition separating request timeout, stream idle timeout, and accounting timeout.
Recovery action: Tune Route/Channel timeout, move workload to streaming-friendly Channel, or set tenant-specific limit.
Blast radius: Single Route or Channel; bad shared default can affect many tenants.
### FM-8 Quota metadata mismatch
Trigger: Data-plane cost metadata is missing, duplicated, late, or keyed by untrusted client header.
Observable outcome: Billing settlement uses PostgreSQL request idempotency and server-derived tenant context; suspicious metadata is ignored or marked partial.
Operator-visible signal: quota_metadata_mismatch event and Usage Record usage_source flag.
Recovery action: Repair data-plane metadata path; reconcile from Provider Account usage if available.
Blast radius: Single request; systematic metadata bug can be tenant-wide or process-wide.
### FM-9 Version adapter mismatch
Trigger: Kubernetes CRD version changes field shape or attachment semantics.
Observable outcome: Adapter marks external resource unsupported or migrates to stable HUAKAI Route contract.
Operator-visible signal: adapter_version_mismatch condition and migration Audit Event.
Recovery action: Upgrade adapter, run migration, or pin supported CRD version.
Blast radius: Adapter scope; potentially cluster-wide in Kubernetes mode.
### FM-10 Provider Account lifecycle race
Trigger: Provider Account is disabled, expired, or quota-exhausted while Route reconciliation or request routing is selecting it.
Observable outcome: Selection rechecks lifecycle before upstream call and fails over or denies.
Operator-visible signal: provider_account_unroutable reason on attempt record.
Recovery action: Re-enable, rotate credential, top up quota, or remove from Channel.
Blast radius: Single Provider Account, possibly many Routes using that Channel.
## 5. FAILURE MODES NOT HANDLED (gaps)
- The source documents generated resources but do not define a full ownership contract for patch-vs-reconcile races. HUAKAI must define one.
- The source allows cross-namespace references through Kubernetes consent, but does not provide HUAKAI's tenant authorization model. HUAKAI must add tenant grants and audit.
- The source documents a limited status shape. HUAKAI Admin Ops needs structured, queryable status per parent, rule, Channel, Provider Account, policy, tenant, and plugin.
- The source's model extraction path creates a security boundary around an internal routing attribute. HUAKAI must explicitly strip, deny, and test spoofing.
- The source demonstrates quota/rate policy with header-derived bucket examples. HUAKAI must derive tenant/account keys from Account Hub and PostgreSQL.
- The source's pool integration requires extra controllers, permissions, and services. HUAKAI must gate it as an optional module with HA and readiness requirements.
- The source mixes Kubernetes and standalone concepts in documentation and releases. HUAKAI must publish a portability contract per edition.
- The source's shared gateway config precedence can change many gateway targets at once. HUAKAI needs dry-run, staged rollout, approval, audit, and rollback.
- The source release history shows version churn. HUAKAI must keep stable internal contracts and adapter migrations.
- The source does not define money-grade Usage Record and Billing Ledger settlement. HUAKAI must use DR-006 transaction discipline.
## 6. KEEP / IMPROVE / AVOID for HUAKAI
- KEEP: Keep the two-tier architecture option from E-EAG-001 for SaaS Edition, because outer auth/global limit and inner model-pool routing are legitimate scale boundaries.
- KEEP: Keep Kubernetes-native declarative deployment as F-DEPLOY-002, but make it an additional deployment surface, not a Personal Edition requirement.
- KEEP: Keep generated runtime resources as reconcile output, because they fit operator expectations for gateway data planes.
- KEEP: Keep model-based routing, because it aligns client intent with Route selection.
- KEEP: Keep model-pool endpoint selection as a Channel/Pool policy concept, because it improves self-hosted inference efficiency.
- IMPROVE: Replace namespace-only authorization with tenant-aware grants per DR-001; this directly addresses C-002 and N-008.
- IMPROVE: Replace single accepted status with structured per-parent, per-rule, per-backend, per-policy, per-tenant, and per-plugin reasons; this addresses C-008, N-003, and S-006.
- IMPROVE: Convert generated-resource patching into first-class override policy with drift detection, Audit Events, and rollback; this addresses C-001 and N-002.
- IMPROVE: Derive quota and billing identity from Account Hub and PostgreSQL, not headers; this addresses C-006 and N-001.
- IMPROVE: Treat GatewayConfig changes as versioned rollout objects with blast-radius analysis and approval; this addresses C-007, F-004, N-004, and S-002.
- IMPROVE: Add fail-closed model extraction with body limits, parser taxonomy, streaming limits, and anti-spoofing tests; this addresses C-004, F-001, and N-006.
- IMPROVE: Split ordinary Channel fallback semantics from pool-plugin fallback semantics; this addresses C-003, F-003, and D-005.
- IMPROVE: Define edition-specific timeout defaults: request, stream idle, total stream, upstream response-header, reservation expiry, and accounting finalization; this addresses C-005 and S-003.
- IMPROVE: Make pool integration HA by default and least-privilege by design; this addresses F-002, N-005, S-001, and S-004.
- IMPROVE: Keep Kubernetes CRD churn behind adapters so HUAKAI's public Admin Ops and OpenAPI contracts remain stable; this addresses F-005, D-003, D-004, and N-007.
- AVOID: Do not copy client-visible tenant headers as quota or billing keys.
- AVOID: Do not copy generated data-plane resources as a support contract without an ownership model.
- AVOID: Do not copy a single-condition status model.
- AVOID: Do not copy shared config precedence without tenant and edition scoping.
- AVOID: Do not copy pool integration permissions without minimization and readiness gates.
- AVOID: Do not copy an internal model-header mechanism as public API.
- AVOID: Do not copy Kubernetes namespace as the tenant primitive.
HUAKAI-specific risks if copying blindly:
1. DR-001 risk: Namespace consent can cross tenant boundaries. HUAKAI must enforce tenant_id and tenant grants.
2. DR-001 risk: Client headers can forge tenant quota keys. HUAKAI must derive identity from Account Hub.
3. DR-002 risk: Kubernetes-only mental model can break Personal Edition. HUAKAI must support non-Kubernetes single-tier routing.
4. DR-002 risk: Shared config can expose SaaS-only knobs in Personal Edition or tenant-admin surfaces. HUAKAI must gate by edition.
5. DR-006 risk: Data-plane metadata alone is not durable enough for quota and billing. HUAKAI must settle in PostgreSQL.
6. DR-006 risk: In-memory or process-local rate buckets cannot enforce cluster-wide Quota. HUAKAI must use explicit database transactions for authoritative state.
7. DR-006 risk: Adapter migration without durable generation tracking can duplicate generated outputs. HUAKAI must persist generation and idempotency.
## 7. ATTRIBUTION
- Source files read: public README region, public API reference region, public architecture concept region, public InferencePool guide region, public release notes region, public module/package metadata region, public translator package documentation region.
- Specifier-lane session: Codex specifier-lane Round 2, 2026-04-29.
- Reviewer-lane session: Pending.
- Verified clean-room compliance: CL-001 through CL-010 reviewed. This file uses behavior-level prose, HUAKAI vocabulary, public source URLs, and avoids upstream function names, upstream file paths, distinctive implementation layout, copied schema, and line-by-line translation.
## Review Sign-Off
| Field | Value |
| --- | --- |
| Reviewer | Pending |
| Review date | Pending |
| Checks passed | Pending CL-001 through CL-010 |
| Notes | Round-2 draft intentionally includes critic-addressed table for reviewer verification. |
## 8. Open questions for implementer-lane
- Should the Kubernetes adapter be read-only import, write-back controller, or both in Phase 8?
- Which generated-output overrides are safe enough for tenant admins, and which require platform operator approval?
- What is the first supported pool plugin shape: Kubernetes inference extension, custom endpoint picker, or HUAKAI-native Channel pool?
- What exact timeout defaults should Personal Edition ship with for streaming and non-streaming?
- Which quota scopes are L1: User, API Key, User Group, Model, Channel, Provider Account, tenant, or all?
- How should pool endpoint decisions be reflected in Usage Record without leaking internal pod or infrastructure names to clients?
- What migration policy applies when an external CRD version is older than the adapter supports?
## 9. Acceptance test directions
- AT-ARCH-001: Personal Edition boots in single-tier mode, while SaaS Edition can stage a two-tier Route with separate outer and inner gateway targets.
- AT-DEPLOY-002: Kubernetes adapter imports a Route declaration, creates HUAKAI Route intent, and records generated-output ownership without making Kubernetes namespace the tenant authority.
- AT-CONFIG-001: Config-as-code and UI wizard produce the same Route artifact, and reload records Audit Events.
- AT-EAG-ROUTE-001: Model extraction fails closed on malformed body, oversized body, missing Model, unsupported endpoint, and spoofed routing header.
- AT-EAG-GEN-001: Manual generated-output patch, Route edit, and controller resync race resolves according to override policy and emits drift status.
- AT-EAG-TENANT-001: Cross-namespace backend consent without HUAKAI tenant authorization is denied and audited.
- AT-EAG-POOL-001: Pool-backed rule rejects weight, priority, mutation, and mixed ordinary backend references inside one rule; multiple rules under one Route remain valid.
- AT-EAG-QUOTA-001: Quota Policy uses Account Hub tenant context and PostgreSQL settlement even when data-plane metadata carries a forged tenant header.
- AT-EAG-CONFIG-001: Shared gateway config update computes blast radius, dry-runs, and rolls back for high-impact key failure.
- AT-EAG-STATUS-001: Admin Ops can filter unhealthy state by Route parent, rule, Channel, Provider Account, Quota Policy, tenant authorization, generated output, and plugin.
- AT-EAG-TIMEOUT-001: Unset AI request timeout resolves to HUAKAI edition default; streaming idle timeout and accounting timeout are separately enforced.
- AT-EAG-MIGRATE-001: Adapter migration from older CRD semantics to stable HUAKAI Route contract does not duplicate generated outputs or drop Route behavior.
## 10. Source Coverage Proof
| Source region read | What it contributed |
| --- | --- |
| https://github.com/envoyproxy/ai-gateway, `<public README topology region>` | Confirmed E-EAG-001 and E-EAG-002: two-tier outer/inner topology, outer auth/top routing/global limit intent, inner model-cluster endpoint selection intent. |
| https://pkg.go.dev/github.com/envoyproxy/ai-gateway, `<module package and license region>` | Confirmed Apache-2.0 safe anchor E-LIC-008 and the public module areas for controller, translator, filter, cost-expression, and provider-facing packages. |
| https://aigateway.envoyproxy.io/docs/api/, `<AI Route generated resource and status region>` | Confirmed generated gateway HTTP route/filter behavior, generated resource namespace, patchability with Gateway policy, and limited accepted/not-accepted status semantics. |
| https://aigateway.envoyproxy.io/docs/api/, `<backend reference and model matching region>` | Confirmed cross-namespace backend reference pattern, model-based matching through internal synthetic model routing attribute, timeout default, token-cost metadata, and QuotaPolicy/API maturity mix. |
| https://aigateway.envoyproxy.io/docs/concepts/architecture/, `<controller and gateway configuration region>` | Confirmed distributed responsibility across route resources, credential policy, gateway-scoped config, and controller/global configuration, supporting critic D-001 and C-007. |
| https://aigateway.envoyproxy.io/docs/capabilities/inference/aigatewayroute-inferencepool/, `<InferencePool limitation and installation region>` | Confirmed pool rule limitations: one pool backend per rule, no mixing with ordinary backend refs, ignored model override/mutation/weight/priority, additional controller/RBAC/picker dependencies. |
| https://github.com/envoyproxy/ai-gateway/releases, `<version migration and breaking-change region>` | Confirmed API/version churn around route attachment, removed/deprecated schema surfaces, security-policy attachment, endpoint prefix changes, and external processor resource configuration movement. |
| https://pkg.go.dev/github.com/envoyproxy/ai-gateway/internal/translator, `<translator package public documentation region>` | Confirmed a translation layer exists between desired Route-like resources and gateway runtime output; this supports the generated-output ownership and adapter-migration decomposition without copying implementation names. |
## 11. Round-2 critic-finding addressed table
| Critic finding ID | This round's status | Where addressed in this file |
|---|---|---|
| C-001 | CONFIRMED | 搂2 S-5, 搂4 FM-4, 搂6 IMPROVE generated-resource override |
| C-002 | CONFIRMED | 搂2 S-3, 搂3 Per-tenant state, 搂4 FM-3, 搂6 HUAKAI risks |
| C-003 | CONFIRMED | 搂2 S-9, 搂9 AT-EAG-POOL-001 |
| C-004 | CONFIRMED | 搂2 S-6 and S-7, 搂4 FM-1/FM-2 |
| C-005 | CONFIRMED | 搂2 S-11, 搂4 FM-7, 搂9 AT-EAG-TIMEOUT-001 |
| C-006 | CONFIRMED | 搂2 S-10, 搂4 FM-8, 搂6 AVOID tenant headers |
| C-007 | CONFIRMED | 搂2 S-12, 搂4 FM-6, 搂6 IMPROVE config rollout |
| C-008 | CONFIRMED | 搂2 S-2 and S-13, 搂9 AT-EAG-STATUS-001 |
| C-009 | CONFIRMED | 搂1, 搂2 S-15, 搂6 KEEP/IMPROVE |
| C-010 | CONFIRMED | 搂2 S-15, 搂6 DR-002 risks |
| F-001 | CONFIRMED | 搂2 S-6 and S-7 |
| F-002 | CONFIRMED | 搂2 S-9, 搂4 FM-5, 搂6 IMPROVE pool HA |
| F-003 | CONFIRMED | 搂2 S-8 and S-9, 搂2-bis partial-failure lifecycle |
| F-004 | CONFIRMED | 搂2 S-12, 搂6 IMPROVE shared config |
| F-005 | CONFIRMED | 搂2 S-14, 搂5 version gap, 搂9 AT-EAG-MIGRATE-001 |
| D-001 | CONFIRMED | 搂1, 搂10 architecture and API regions |
| D-002 | CONFIRMED | 搂1, 搂2 S-5, 搂5 generated-resource gap |
| D-003 | CONFIRMED | 搂2 S-14, 搂10 release/API regions |
| D-004 | CONFIRMED | 搂2 S-14, 搂10 release notes region |
| D-005 | CONFIRMED | 搂2 S-9, 搂9 AT-EAG-POOL-001 |
| N-001 | CONFIRMED | 搂2 S-10, 搂6 AVOID tenant headers |
| N-002 | CONFIRMED | 搂2 S-5, 搂6 IMPROVE generated-resource override |
| N-003 | CONFIRMED | 搂2 S-13, 搂6 AVOID single-condition status |
| N-004 | CONFIRMED | 搂2 S-12, 搂6 AVOID shared config precedence |
| N-005 | CONFIRMED | 搂2 S-9, 搂4 FM-5, 搂6 IMPROVE pool privileges |
| N-006 | CONFIRMED | 搂2 S-6, 搂6 AVOID internal model-header public API |
| N-007 | CONFIRMED | 搂2 S-14, 搂6 IMPROVE adapter stability |
| N-008 | CONFIRMED | 搂2 S-3, 搂3 Per-tenant state, 搂6 AVOID namespace tenancy |
| S-001 | CONFIRMED | 搂2 S-9, 搂4 FM-5, 搂6 IMPROVE pool HA |
| S-002 | CONFIRMED | 搂2 S-12, 搂4 FM-6 |
| S-003 | CONFIRMED | 搂2 S-11, 搂6 IMPROVE timeout defaults |
| S-004 | CONFIRMED | 搂2 S-7 and S-9, 搂6 IMPROVE pool readiness/fail-closed |
| S-005 | CONFIRMED | 搂2 S-3 and S-6, 搂3 Per-tenant state |
| S-006 | CONFIRMED | 搂2 S-13, 搂4 failure taxonomy |
| S-007 | CONFIRMED | 搂4 FM-4/FM-5/FM-6/FM-9, 搂9 acceptance tests |
（编码修复注 2026-07-05：本行原为 GBK 误解码乱码，按字节尽力恢复，� 处为不可恢复的损字。）Owner 中文摘要：本轮按 Round 2 要求�?Envoy AI Gateway 的外/内网关拓扑��AI Route 调和、Backend 生命周期、Quota Policy 附着拆到行为级和故障级：列出 15 个子行为�? 条请求生命周期��完整输�?状��?持久化结构��?0 类失败模式��HUAKAI �?DR-001/DR-002/DR-006 下不能盲抄的 7 个风险，并新�?Source Coverage Proof。critic �?35 �?finding 全部已��项处置，均�?CONFIRMED，没�?REFUTED �?OPEN；关键差异是本版不再停留在��有 CRD/有两层网关��，而是明确生成资源 ownership、跨租户授权、模型解�?fail-closed、InferencePool 限制、Header 不可信配额��共享配置爆炸半径��结构化状����版本��配�?Personal/SaaS 便携边界。HUAKAI 应吸收的是可选两层拓扑��声明式路由、池�?endpoint 选择�?Kubernetes adapter；必须改造的是租户身份��PostgreSQL 结算、Admin Ops 状����审计��回滚��插件化 InferencePool 和稳定内部契约��?
